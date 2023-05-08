package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"net/http"
	"os"
	"time"

	"go.uber.org/zap"

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
	updateInterval := fs.Duration("interval", time.Minute*30, "Interval to scan ovo (default: 30m)")

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
	if *updateInterval < time.Second*10 {
		return fmt.Errorf("update interval must be at least 10 seconds")
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

	http.Handle("/metrics", promhttp.Handler())
	http.Handle("/data/report", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
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
	}))
	http.Handle("/", http.HandlerFunc(func(writer http.ResponseWriter, request *http.Request) {
		zap.S().Infof("received request: %v", request.RequestURI)
		zap.S().Infof("received headers: %v", request.Header.Clone())
		writer.WriteHeader(http.StatusNotFound)
	}))
	addr := ":8080"
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
