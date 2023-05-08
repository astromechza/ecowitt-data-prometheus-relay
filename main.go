package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"go.uber.org/zap"
	"io"
	"net/http"
	"net/url"
	"os"
	"strconv"
	"time"

	"github.com/prometheus/client_golang/prometheus/promhttp"
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

	gauges := make(map[string]prometheus.Gauge)

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

		// drop some fields we know aren't needed
		for _, s := range []string{"dateutc", "PASSKEY", "model", "stationtype"} {
			values.Del(s)
		}

		// construct gauges and emit values
		for left, right := range values {

			rightValue, err := strconv.ParseFloat(right[0], 64)
			if err != nil {
				zap.S().Warnf("failed to parse numeric value for %s: '%s'", left, right)
				continue
			}

			gauge, ok := gauges[left]
			if !ok {
				gauge = promauto.NewGauge(prometheus.GaugeOpts{
					Name:      left + "_raw",
					Namespace: "ecowitt_relay",
					ConstLabels: map[string]string{
						"model":       modelField,
						"stationType": stationField,
					},
				})
				gauges[left] = gauge
			}

			gauge.Set(rightValue)
		}

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
		zap.S().Infof("server will exit after %v", *ttl)
		go func() {
			timer := time.NewTimer(*ttl)
			select {
			case <-timer.C:
				zap.L().Info("ttl expired")
				os.Exit(1)
				return
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

func main() {
	if err := mainInner(); err != nil {
		zap.S().Errorw("failed", "err", err)
		os.Exit(1)
	}
}
