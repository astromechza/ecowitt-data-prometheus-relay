// Package main implements the ecowitt-data-prometheus-relay service.
package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync/atomic"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
)

const mainUsage = `ecowitt-data-prometheus-relay accepts a payload from an ecowitt weather station and presents the
data on a /metrics endpoint to present to a prometheus scraper.

Options:
`

// Config holds the application configuration loaded from disk.
type Config struct {
}

var errNoPositionalArgs = errors.New("no positional arguments expected")

type lastReport struct {
	body    []byte
	headers http.Header
}

// relay holds shared state for the HTTP handlers.
type relay struct {
	reg        prometheus.Registerer
	counter    atomic.Int64  // counts successful report POSTs; used by TTL goroutine
	lastReport atomic.Value  // stores *lastReport
}

func (r *relay) handleReport(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodPost {
		zap.S().Infof("received request: %v", req.RequestURI)
		zap.S().Infof("received headers: %v", req.Header.Clone())
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	data, err := io.ReadAll(req.Body)
	if err != nil {
		zap.S().Errorw("failed to read body stream", "err", err)
		w.WriteHeader(http.StatusInternalServerError)
		return
	}
	zap.S().Infof("received request: %v", req.RequestURI)
	zap.S().Infof("received headers: %v", req.Header.Clone())
	zap.S().Infof("received report: '%v'", string(data))

	values, err := url.ParseQuery(string(data))
	if err != nil {
		zap.S().Warnf("failed to parse as url encoded body: %v", err)
		w.WriteHeader(http.StatusBadRequest)
		return
	}

	r.lastReport.Store(&lastReport{body: data, headers: req.Header.Clone()})
	w.WriteHeader(http.StatusOK)

	modelField := values.Get("model")
	if modelField == "" {
		modelField = "unknown"
	}
	stationField := values.Get("stationtype")
	if stationField == "" {
		stationField = "unknown"
	}
	sourceIP := req.Header.Get("X-Real-IP")
	if sourceIP == "" {
		sourceIP = "unknown"
	}

	for _, s := range []string{"dateutc", "PASSKEY", "model", "stationtype", "freq"} {
		values.Del(s)
	}

	r.incrementReportCount(modelField, stationField, sourceIP)

	for left, right := range values {
		rightValue, err := strconv.ParseFloat(right[0], 64)
		if err != nil {
			zap.S().Warnf("failed to parse numeric value for %s: '%s'", left, right[0])
			continue
		}
		r.updateGauge(modelField, stationField, sourceIP, left, rightValue)
	}

	r.counter.Add(1)
}

func (r *relay) handleLast(w http.ResponseWriter, req *http.Request) {
	if req.Method != http.MethodGet && req.Method != http.MethodHead {
		w.WriteHeader(http.StatusMethodNotAllowed)
		return
	}
	lr, ok := r.lastReport.Load().(*lastReport)
	if !ok || lr == nil {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	// Replay original request headers, excluding sensitive ones.
	// Note: /last is intended for trusted-network debugging only.
	for k, vals := range lr.headers {
		if http.CanonicalHeaderKey(k) == "Authorization" {
			continue
		}
		for _, v := range vals {
			w.Header().Add("X-Original-"+k, v)
		}
	}
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(lr.body)
}

func (r *relay) updateGauge(model, station, sourceIP, key string, value float64) {
	gauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name:      key + "_raw",
		Namespace: "ecowitt_relay",
		ConstLabels: map[string]string{
			"source_ip":   sourceIP,
			"model":       model,
			"stationType": station,
		},
	})
	if err := r.reg.Register(gauge); err != nil {
		if conflict := new(prometheus.AlreadyRegisteredError); errors.As(err, conflict) {
			gauge = conflict.ExistingCollector.(prometheus.Gauge)
		} else {
			zap.L().Fatal("failed to register gauge", zap.Error(err))
		}
	}
	gauge.Set(value)
}

func (r *relay) incrementReportCount(model, station, sourceIP string) {
	counter := prometheus.NewCounter(prometheus.CounterOpts{
		Name:      "report_count",
		Namespace: "ecowitt_relay",
		ConstLabels: map[string]string{
			"source_ip":   sourceIP,
			"model":       model,
			"stationType": station,
		},
	})
	if err := r.reg.Register(counter); err != nil {
		if conflict := new(prometheus.AlreadyRegisteredError); errors.As(err, conflict) {
			counter = conflict.ExistingCollector.(prometheus.Counter)
		} else {
			zap.L().Fatal("failed to register counter", zap.Error(err))
		}
	}
	counter.Inc()
}

func mainInner() error {
	fs := flag.NewFlagSet(os.Args[0], flag.ExitOnError)
	debugFlag := fs.Bool("debug", false, "Show debug logs")
	configFlag := fs.String("config", "/config.json", "Json account config file (default: /config.json)")
	ttl := fs.Duration("ttl", -1, "TTL before the app restarts (default no restart)")

	fs.Usage = func() {
		_, _ = fmt.Fprint(os.Stderr, mainUsage)
		fs.PrintDefaults()
	}
	if err := fs.Parse(os.Args[1:]); err != nil {
		return err
	}
	if fs.NArg() != 0 {
		fs.Usage()
		_, _ = fmt.Fprintf(os.Stderr, "\n")
		return errNoPositionalArgs
	}

	logger, _ := zap.NewProduction()
	if *debugFlag {
		logger, _ = zap.NewDevelopment()
	}
	zap.ReplaceGlobals(logger)

	conf := &Config{}
	zap.S().Infow("loading config", "config", *configFlag)
	//nolint:gosec // path is operator-provided via CLI flag
	confFile, err := os.Open(*configFlag)
	if err != nil {
		return err
	}
	defer func() {
		if cerr := confFile.Close(); cerr != nil {
			zap.S().Warnw("failed to close config file", "err", cerr)
		}
	}()

	zap.S().Infow("decoding config", "config", *configFlag)
	if err = json.NewDecoder(confFile).Decode(conf); err != nil {
		return err
	}

	r := &relay{reg: prometheus.DefaultRegisterer}

	mux := http.NewServeMux()
	mux.Handle("/metrics", promhttp.Handler())
	mux.Handle("/last", http.HandlerFunc(r.handleLast))
	mux.Handle("/data/report/", http.HandlerFunc(r.handleReport))
	mux.Handle("/", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		zap.S().Infof("received request: %v", request.RequestURI)
		zap.S().Infof("received headers: %v", request.Header.Clone())
		writer.WriteHeader(http.StatusNotFound)
	}))

	addr := ":8080"

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	if int(*ttl) > 0 {
		go func() {
			lastIncrement := time.Now()
			lastCount := r.counter.Load()
			ticker := time.NewTicker(time.Second)
			defer ticker.Stop()
			for {
				select {
				case <-ticker.C:
					count := r.counter.Load()
					if count > 0 {
						if count == lastCount {
							if time.Since(lastIncrement) > *ttl {
								zap.L().Info("ttl expired with no reports")
								os.Exit(1)
								return
							}
						} else {
							lastIncrement = time.Now()
							lastCount = count
						}
					}
				case <-ctx.Done():
					zap.L().Info("closing background routine")
					return
				}
			}
		}()
	}

	zap.S().Infow("starting server", "address", addr)
	srv := &http.Server{
		Addr:         addr,
		Handler:      mux,
		ReadTimeout:  30 * time.Second,
		WriteTimeout: 30 * time.Second,
		IdleTimeout:  120 * time.Second,
	}
	if err := srv.ListenAndServe(); err != nil {
		return err
	}
	return nil
}

func main() {
	if err := mainInner(); err != nil {
		zap.S().Errorw("failed", "err", err)
		os.Exit(1)
	}
}
