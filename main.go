package main

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"go.uber.org/zap"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"sync/atomic"
	"time"
)

const mainUsage = `ecowitt-data-prometheus-relay accepts a payload from an ecowitt weather station and presents the
data on a /metrics endpoint to present to a prometheus scraper.

Options:
`

type Config struct {
}

func mainInner() error {
	// Define and parse the top level cli flags - each subcommand has their own flag set too!
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
		return fmt.Errorf("no positional arguments expected")
	}

	logger, _ := zap.NewProduction()
	if *debugFlag {
		logger, _ = zap.NewDevelopment()
	}
	zap.ReplaceGlobals(logger)

	conf := &Config{}
	zap.S().Infow("loading config", "config", *configFlag)
	confFile, err := os.Open(*configFlag)
	if err != nil {
		return err
	}
	defer confFile.Close()

	zap.S().Infow("decoding config", "config", *configFlag)
	if err = json.NewDecoder(confFile).Decode(conf); err != nil {
		return err
	}

	counter := int64(0)

	http.Handle("/metrics", promhttp.Handler())
	http.Handle("/data/report/", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		if request.Method != http.MethodPost {
			zap.S().Infof("received request: %v", request.RequestURI)
			zap.S().Infof("received headers: %v", request.Header.Clone())
			writer.WriteHeader(http.StatusMethodNotAllowed)
			return
		}
		data, err := io.ReadAll(request.Body)
		if err != nil {
			zap.S().Errorw("failed to read body stream", "err", err)
			writer.WriteHeader(http.StatusInternalServerError)
			return
		}
		zap.S().Infof("received request: %v", request.RequestURI)
		zap.S().Infof("received headers: %v", request.Header.Clone())
		zap.S().Infof("received report: '%v'", string(data))
		writer.WriteHeader(http.StatusOK)

		values, err := url.ParseQuery(string(data))
		if err != nil {
			zap.S().Warnf("failed to parse as url encoded body: %v", err)
			return
		}

		// capture model and station
		modelField := values.Get("model")
		if modelField == "" {
			modelField = "unknown"
		}
		stationField := values.Get("stationtype")
		if stationField == "" {
			stationField = "unknown"
		}
		sourceIp := request.Header.Get("X-Real-IP")
		if sourceIp == "" {
			sourceIp = "unknown"
		}

		// drop some fields we know aren't needed
		for _, s := range []string{"dateutc", "PASSKEY", "model", "stationtype", "freq"} {
			values.Del(s)
		}

		incrementReportCount(modelField, stationField, sourceIp)

		// construct gauges and emit values
		for left, right := range values {
			rightValue, err := strconv.ParseFloat(right[0], 64)
			if err != nil {
				zap.S().Warnf("failed to parse numeric value for %s: '%s'", left, right)
				continue
			}
			updateGauge(modelField, stationField, sourceIp, left, rightValue)
		}

		atomic.AddInt64(&counter, 1)
	}))
	http.Handle("/", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
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
			lastCount := atomic.LoadInt64(&counter)
			timer := time.NewTicker(time.Second)
			select {
			case <-timer.C:
				count := atomic.LoadInt64(&counter)
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
		}()
	}

	zap.S().Infow("starting server", "address", addr)
	if err := http.ListenAndServe(addr, nil); err != nil {
		return err
	}
	return nil
}

func updateGauge(model, station, sourceIp, key string, value float64) {
	gauge := prometheus.NewGauge(prometheus.GaugeOpts{
		Name:      key + "_raw",
		Namespace: "ecowitt_relay",
		ConstLabels: map[string]string{
			"source_ip":   sourceIp,
			"model":       model,
			"stationType": station,
		},
	})
	if err := prometheus.DefaultRegisterer.Register(gauge); err != nil {
		if conflict := new(prometheus.AlreadyRegisteredError); errors.As(err, conflict) {
			gauge = conflict.ExistingCollector.(prometheus.Gauge)
		} else {
			zap.L().Fatal("failed to register counter", zap.Error(err))
		}
	}

	gauge.Set(value)
}

func incrementReportCount(model, station, sourceIp string) {
	counter := prometheus.NewCounter(prometheus.CounterOpts{
		Name:      "report_count",
		Namespace: "ecowitt_relay",
		ConstLabels: map[string]string{
			"source_ip":   sourceIp,
			"model":       model,
			"stationType": station,
		},
	})
	if err := prometheus.DefaultRegisterer.Register(counter); err != nil {
		if conflict := new(prometheus.AlreadyRegisteredError); errors.As(err, conflict) {
			counter = conflict.ExistingCollector.(prometheus.Counter)
		} else {
			zap.L().Fatal("failed to register counter", zap.Error(err))
		}
	}

	counter.Inc()
}

func main() {
	if err := mainInner(); err != nil {
		zap.S().Errorw("failed", "err", err)
		os.Exit(1)
	}
}
