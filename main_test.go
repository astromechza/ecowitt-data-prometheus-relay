package main

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"regexp"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/prometheus/common/expfmt"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// fakeClock is a manually advanced clock for exercising expiry.
type fakeClock struct{ t time.Time }

func (c *fakeClock) now() time.Time          { return c.t }
func (c *fakeClock) advance(d time.Duration) { c.t = c.t.Add(d) }

func newTestRelayWithClock(ttl time.Duration, clock *fakeClock) (*relay, *prometheus.Registry) {
	reg := prometheus.NewRegistry()
	r := &relay{stations: newStationCollector(ttl, clock.now)}
	reg.MustRegister(r.stations)
	return r, reg
}

func newTestRelay() (*relay, *prometheus.Registry) {
	return newTestRelayWithClock(0, &fakeClock{t: time.Unix(0, 0)})
}

// gatherMetric finds the first metric matching name from the registry.
func gatherMetric(t *testing.T, g prometheus.Gatherer, name string) *dto.Metric {
	t.Helper()
	mfs, err := g.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		if mf.GetName() == name {
			if m := mf.GetMetric(); len(m) > 0 {
				return m[0]
			}
		}
	}
	return nil
}

func labelMap(m *dto.Metric) map[string]string {
	out := make(map[string]string, len(m.GetLabel()))
	for _, lp := range m.GetLabel() {
		out[lp.GetName()] = lp.GetValue()
	}
	return out
}

func postReport(r *relay, body string, headers map[string]string) *httptest.ResponseRecorder {
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/data/report/", strings.NewReader(body))
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	w := httptest.NewRecorder()
	r.handleReport(w, req)
	return w
}

func TestHandleReport_NonPostReturns405(t *testing.T) {
	r, _ := newTestRelay()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/data/report/", nil)
	w := httptest.NewRecorder()
	r.handleReport(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestHandleReport_ValidPostReturns200AndRegistersGauge(t *testing.T) {
	r, reg := newTestRelay()
	body := url.Values{
		"tempf":       {"72.5"},
		"model":       {"GW1000"},
		"stationtype": {"GW1000A"},
	}.Encode()
	w := postReport(r, body, map[string]string{"X-Real-IP": "10.0.0.1"})

	assert.Equal(t, http.StatusOK, w.Code)

	m := gatherMetric(t, reg, "ecowitt_relay_tempf_raw")
	require.NotNil(t, m, "expected ecowitt_relay_tempf_raw to be registered")
	assert.Equal(t, 72.5, m.GetGauge().GetValue())
	labels := labelMap(m)
	assert.Equal(t, "GW1000", labels["model"])
	assert.Equal(t, "GW1000A", labels["stationType"])
	assert.Equal(t, "10.0.0.1", labels["source_ip"])
}

func TestHandleReport_MalformedBodyReturns400(t *testing.T) {
	r, _ := newTestRelay()
	// invalid percent-encoding causes url.ParseQuery to fail
	w := postReport(r, "%ZZ=bad", nil)
	assert.Equal(t, http.StatusBadRequest, w.Code)
}

func TestHandleReport_MalformedBodyDoesNotIncrementTTLCounter(t *testing.T) {
	r, _ := newTestRelay()
	postReport(r, "%ZZ=bad", nil)
	assert.Equal(t, int64(0), r.counter.Load())
}

func TestHandleReport_SuccessfulPostIncrementsTTLCounter(t *testing.T) {
	r, _ := newTestRelay()
	postReport(r, url.Values{"tempf": {"55.0"}}.Encode(), nil)
	postReport(r, url.Values{"tempf": {"56.0"}}.Encode(), nil)
	assert.Equal(t, int64(2), r.counter.Load())
}

func TestHandleReport_DroppedFieldsProduceNoGauges(t *testing.T) {
	r, reg := newTestRelay()
	body := url.Values{
		"PASSKEY":     {"secret"},
		"freq":        {"915M"},
		"dateutc":     {"2024-01-01"},
		"model":       {"GW1000"},
		"stationtype": {"GW1000A"},
	}.Encode()
	postReport(r, body, nil)

	mfs, err := reg.Gather()
	require.NoError(t, err)
	for _, mf := range mfs {
		n := mf.GetName()
		assert.NotContains(t, n, "PASSKEY", "PASSKEY should not produce a gauge")
		assert.NotContains(t, n, "freq", "freq should not produce a gauge")
		assert.NotContains(t, n, "dateutc", "dateutc should not produce a gauge")
	}
}

func TestHandleReport_NonNumericFieldSkipped(t *testing.T) {
	r, reg := newTestRelay()
	body := url.Values{
		"tempf":    {"not-a-number"},
		"humidity": {"65"},
	}.Encode()
	w := postReport(r, body, nil)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Nil(t, gatherMetric(t, reg, "ecowitt_relay_tempf_raw"), "non-numeric field should be skipped")
	require.NotNil(t, gatherMetric(t, reg, "ecowitt_relay_humidity_raw"), "valid numeric field should be registered")
}

func TestHandleReport_MissingLabelsDefaultToUnknown(t *testing.T) {
	r, reg := newTestRelay()
	body := url.Values{"tempf": {"55.0"}}.Encode()
	postReport(r, body, nil)

	m := gatherMetric(t, reg, "ecowitt_relay_tempf_raw")
	require.NotNil(t, m)
	labels := labelMap(m)
	assert.Equal(t, "unknown", labels["model"])
	assert.Equal(t, "unknown", labels["stationType"])
	assert.Equal(t, "unknown", labels["source_ip"])
}

func TestHandleReport_DuplicateReportUpdatesGauge(t *testing.T) {
	r, reg := newTestRelay()
	post := func(temp string) {
		body := url.Values{"tempf": {temp}}.Encode()
		w := postReport(r, body, nil)
		require.Equal(t, http.StatusOK, w.Code)
	}

	post("60.0")
	post("75.0")

	m := gatherMetric(t, reg, "ecowitt_relay_tempf_raw")
	require.NotNil(t, m)
	assert.Equal(t, 75.0, m.GetGauge().GetValue())
}

func TestHandleLast_NoReportReturns404(t *testing.T) {
	r, _ := newTestRelay()
	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/last", nil)
	w := httptest.NewRecorder()
	r.handleLast(w, req)
	assert.Equal(t, http.StatusNotFound, w.Code)
}

func TestHandleLast_NonGetReturns405(t *testing.T) {
	r, _ := newTestRelay()
	postReport(r, url.Values{"tempf": {"55.0"}}.Encode(), nil)
	req := httptest.NewRequestWithContext(context.Background(), http.MethodPost, "/last", nil)
	w := httptest.NewRecorder()
	r.handleLast(w, req)
	assert.Equal(t, http.StatusMethodNotAllowed, w.Code)
}

func TestHandleLast_ReturnsVerbatimLastBody(t *testing.T) {
	r, _ := newTestRelay()
	body := url.Values{"tempf": {"72.5"}, "model": {"GW1100A"}}.Encode()
	postReport(r, body, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/last", nil)
	w := httptest.NewRecorder()
	r.handleLast(w, req)

	assert.Equal(t, http.StatusOK, w.Code)
	assert.Equal(t, body, w.Body.String())
}

func TestHandleLast_ReturnsOriginalHeaders(t *testing.T) {
	r, _ := newTestRelay()
	body := url.Values{"tempf": {"72.5"}}.Encode()
	postReport(r, body, map[string]string{
		"Content-Type": "application/x-www-form-urlencoded",
		"X-Real-IP":    "10.0.0.1",
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/last", nil)
	w := httptest.NewRecorder()
	r.handleLast(w, req)

	assert.Equal(t, "application/x-www-form-urlencoded", w.Header().Get("X-Original-Content-Type"))
	assert.Equal(t, "10.0.0.1", w.Header().Get("X-Original-X-Real-Ip"))
}

func TestHandleLast_AuthorizationHeaderNotRelayed(t *testing.T) {
	r, _ := newTestRelay()
	postReport(r, url.Values{"tempf": {"72.5"}}.Encode(), map[string]string{
		"Authorization": "Bearer secret-token",
	})

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/last", nil)
	w := httptest.NewRecorder()
	r.handleLast(w, req)

	assert.Empty(t, w.Header().Get("X-Original-Authorization"))
}

func TestHandleLast_ReturnsOnlyMostRecent(t *testing.T) {
	r, _ := newTestRelay()
	postReport(r, url.Values{"tempf": {"60.0"}}.Encode(), nil)
	second := url.Values{"tempf": {"75.0"}}.Encode()
	postReport(r, second, nil)

	req := httptest.NewRequestWithContext(context.Background(), http.MethodGet, "/last", nil)
	w := httptest.NewRecorder()
	r.handleLast(w, req)

	assert.Equal(t, second, w.Body.String())
}

func TestHandleReport_ReportCounterIncrements(t *testing.T) {
	r, reg := newTestRelay()
	body := url.Values{"tempf": {"50.0"}}.Encode()
	postReport(r, body, nil)
	postReport(r, body, nil)

	m := gatherMetric(t, reg, "ecowitt_relay_report_count")
	require.NotNil(t, m, "expected report_count counter")
	assert.Equal(t, 2.0, m.GetCounter().GetValue())
}

// stationTypes returns the stationType label of every series of the named metric.
func stationTypes(t *testing.T, g prometheus.Gatherer, name string) []string {
	t.Helper()
	mfs, err := g.Gather()
	require.NoError(t, err)
	var out []string
	for _, mf := range mfs {
		if mf.GetName() != name {
			continue
		}
		for _, m := range mf.GetMetric() {
			out = append(out, labelMap(m)["stationType"])
		}
	}
	return out
}

func TestStationExpiry_OldLabelSetDroppedAfterTTL(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	r, reg := newTestRelayWithClock(15*time.Minute, clock)

	postReport(r, url.Values{"tempf": {"60.0"}, "stationtype": {"GW1100A_V2.4.5"}}.Encode(), nil)
	clock.advance(time.Minute)
	// firmware update changes stationtype
	postReport(r, url.Values{"tempf": {"61.0"}, "stationtype": {"GW1100A_V2.4.6"}}.Encode(), nil)

	assert.ElementsMatch(t, []string{"GW1100A_V2.4.5", "GW1100A_V2.4.6"}, stationTypes(t, reg, "ecowitt_relay_tempf_raw"))

	// old label set is exactly at the TTL: still exported
	clock.advance(14 * time.Minute)
	assert.Len(t, stationTypes(t, reg, "ecowitt_relay_report_count"), 2)

	// past the TTL: old label set disappears from every metric, the fresh one stays
	clock.advance(time.Second)
	assert.Equal(t, []string{"GW1100A_V2.4.6"}, stationTypes(t, reg, "ecowitt_relay_tempf_raw"))
	assert.Equal(t, []string{"GW1100A_V2.4.6"}, stationTypes(t, reg, "ecowitt_relay_report_count"))
}

func TestStationExpiry_ReportRefreshesLastSeen(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	r, reg := newTestRelayWithClock(15*time.Minute, clock)
	body := url.Values{"tempf": {"60.0"}}.Encode()

	postReport(r, body, nil)
	clock.advance(10 * time.Minute)
	postReport(r, body, nil)
	clock.advance(10 * time.Minute)

	m := gatherMetric(t, reg, "ecowitt_relay_report_count")
	require.NotNil(t, m)
	assert.Equal(t, 2.0, m.GetCounter().GetValue())
}

func TestStationExpiry_ReturningLabelSetStartsFresh(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	r, reg := newTestRelayWithClock(15*time.Minute, clock)

	postReport(r, url.Values{"tempf": {"60.0"}, "humidity": {"40"}}.Encode(), nil)
	clock.advance(time.Hour)
	assert.Nil(t, gatherMetric(t, reg, "ecowitt_relay_report_count"))

	postReport(r, url.Values{"tempf": {"70.0"}}.Encode(), nil)
	assert.Equal(t, 1.0, gatherMetric(t, reg, "ecowitt_relay_report_count").GetCounter().GetValue())
	assert.Equal(t, 70.0, gatherMetric(t, reg, "ecowitt_relay_tempf_raw").GetGauge().GetValue())
	assert.Nil(t, gatherMetric(t, reg, "ecowitt_relay_humidity_raw"), "fields from the expired state should not return")
}

func TestStationExpiry_ZeroTTLNeverExpires(t *testing.T) {
	clock := &fakeClock{t: time.Unix(1_700_000_000, 0)}
	r, reg := newTestRelayWithClock(0, clock)
	postReport(r, url.Values{"tempf": {"60.0"}}.Encode(), nil)
	clock.advance(24 * time.Hour)
	require.NotNil(t, gatherMetric(t, reg, "ecowitt_relay_tempf_raw"))
}

func TestHandleReport_InvalidFieldNameSkipped(t *testing.T) {
	r, reg := newTestRelay()
	postReport(r, url.Values{"bad\xffname": {"1"}, "tempf": {"60.0"}}.Encode(), nil)
	require.NotNil(t, gatherMetric(t, reg, "ecowitt_relay_tempf_raw"))
}

// sampleLine matches series lines in testdata/live-metrics-sample.txt, e.g.
// ecowitt_relay_tempf_raw{model="GW1100A",source_ip="192.168.4.6",stationType="GW1100A_V2.4.4"} 52.52
var sampleLine = regexp.MustCompile(`^ecowitt_relay_(\w+)_raw\{model="([^"]+)",source_ip="([^"]+)",stationType="([^"]+)"\} (\S+)$`)

// TestMetricsOutput_MatchesLiveSample replays the fields from a v0.0.9 /metrics snapshot and checks the
// exposition output is identical, guarding metric names, labels, value formatting and report_count semantics
// that the Grafana dashboard depends on.
func TestMetricsOutput_MatchesLiveSample(t *testing.T) {
	raw, err := os.ReadFile("testdata/live-metrics-sample.txt")
	require.NoError(t, err)

	var expected []string
	body := url.Values{}
	headers := map[string]string{}
	reports := 0
	sc := bufio.NewScanner(bytes.NewReader(raw))
	for sc.Scan() {
		line := sc.Text()
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		expected = append(expected, line)
		if strings.HasPrefix(line, "ecowitt_relay_report_count{") {
			_, err := fmt.Sscanf(line[strings.LastIndex(line, " ")+1:], "%d", &reports)
			require.NoError(t, err)
			continue
		}
		m := sampleLine.FindStringSubmatch(line)
		require.NotNil(t, m, "unexpected sample line: %s", line)
		body.Set(m[1], m[5])
		body.Set("model", m[2])
		body.Set("stationtype", m[4])
		headers["X-Real-IP"] = m[3]
	}
	require.NoError(t, sc.Err())
	require.Positive(t, reports)

	r, reg := newTestRelay()
	for range reports {
		require.Equal(t, http.StatusOK, postReport(r, body.Encode(), headers).Code)
	}

	mfs, err := reg.Gather()
	require.NoError(t, err)
	var buf bytes.Buffer
	for _, mf := range mfs {
		_, err := expfmt.MetricFamilyToText(&buf, mf)
		require.NoError(t, err)
	}
	var actual []string
	for _, line := range strings.Split(strings.TrimSpace(buf.String()), "\n") {
		if !strings.HasPrefix(line, "#") {
			actual = append(actual, line)
		}
	}
	assert.Equal(t, expected, actual)

	for _, mf := range mfs {
		want := dto.MetricType_GAUGE
		if mf.GetName() == "ecowitt_relay_report_count" {
			want = dto.MetricType_COUNTER
		}
		assert.Equal(t, want, mf.GetType(), mf.GetName())
	}
}

// TestStationCollector_ConcurrentObserveAndCollect is meant for -race: reports land while scrapes expire entries.
func TestStationCollector_ConcurrentObserveAndCollect(t *testing.T) {
	c := newStationCollector(time.Nanosecond, time.Now)
	reg := prometheus.NewRegistry()
	reg.MustRegister(c)

	var wg sync.WaitGroup
	for i := range 8 {
		wg.Go(func() {
			ls := stationLabels{model: "GW1100A", station: "v1", sourceIP: fmt.Sprintf("10.0.0.%d", i)}
			for j := range 200 {
				c.observe(ls, map[string]float64{"tempf": float64(j)})
			}
		})
		wg.Go(func() {
			for range 50 {
				_, err := reg.Gather()
				assert.NoError(t, err)
			}
		})
	}
	wg.Wait()
}

func TestHandleReport_OmittedFieldKeepsLastValue(t *testing.T) {
	r, reg := newTestRelay()
	postReport(r, url.Values{"tempf": {"60.0"}, "humidity": {"40"}}.Encode(), nil)
	postReport(r, url.Values{"tempf": {"61.0"}}.Encode(), nil)

	assert.Equal(t, 61.0, gatherMetric(t, reg, "ecowitt_relay_tempf_raw").GetGauge().GetValue())
	// Known limitation (AGENTS.md "Stale value flatline"): only whole label sets expire, not individual fields.
	assert.Equal(t, 40.0, gatherMetric(t, reg, "ecowitt_relay_humidity_raw").GetGauge().GetValue())
}
