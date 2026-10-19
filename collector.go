package main

import (
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"go.uber.org/zap"
)

const metricNamespace = "ecowitt_relay"

// stationLabels identifies one reporting station. Every exported series carries these labels.
type stationLabels struct {
	model    string
	station  string
	sourceIP string
}

type stationState struct {
	lastSeen time.Time
	reports  float64
	gauges   map[string]float64
}

// stationCollector holds the latest values per station label set and exports them on scrape.
// Label sets not seen within ttl are dropped on the next Collect, so a station whose labels change
// (e.g. firmware update bumping stationtype) does not leave a frozen copy of its old series behind.
type stationCollector struct {
	ttl time.Duration // <= 0 disables expiry
	now func() time.Time

	mu       sync.Mutex
	stations map[stationLabels]*stationState
}

func newStationCollector(ttl time.Duration, now func() time.Time) *stationCollector {
	return &stationCollector{ttl: ttl, now: now, stations: make(map[stationLabels]*stationState)}
}

// observe records one report for the label set, merging the given gauge values into its state.
func (c *stationCollector) observe(ls stationLabels, values map[string]float64) {
	c.mu.Lock()
	defer c.mu.Unlock()
	s, ok := c.stations[ls]
	if !ok {
		s = &stationState{gauges: make(map[string]float64, len(values))}
		c.stations[ls] = s
	}
	s.lastSeen = c.now()
	s.reports++
	for k, v := range values {
		s.gauges[k] = v
	}
}

// Describe sends nothing, making this an unchecked collector: metric names depend on the reported fields.
func (c *stationCollector) Describe(chan<- *prometheus.Desc) {}

func (c *stationCollector) Collect(ch chan<- prometheus.Metric) {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.now()
	for ls, s := range c.stations {
		if c.ttl > 0 && now.Sub(s.lastSeen) > c.ttl {
			zap.S().Infow("expiring stale station label set",
				"model", ls.model, "stationType", ls.station, "source_ip", ls.sourceIP, "last_seen", s.lastSeen)
			delete(c.stations, ls)
			continue
		}
		labels := prometheus.Labels{"model": ls.model, "stationType": ls.station, "source_ip": ls.sourceIP}
		emit(ch, "report_count", prometheus.CounterValue, s.reports, labels)
		for key, v := range s.gauges {
			emit(ch, key+"_raw", prometheus.GaugeValue, v, labels)
		}
	}
}

func emit(ch chan<- prometheus.Metric, name string, vt prometheus.ValueType, v float64, labels prometheus.Labels) {
	desc := prometheus.NewDesc(prometheus.BuildFQName(metricNamespace, "", name), "", nil, labels)
	m, err := prometheus.NewConstMetric(desc, vt, v)
	if err != nil {
		zap.S().Warnw("skipping invalid metric", "name", name, "err", err)
		return
	}
	ch <- m
}
