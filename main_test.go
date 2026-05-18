package main

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

func newTestRelay() (*relay, *prometheus.Registry) {
	reg := prometheus.NewRegistry()
	return &relay{reg: reg}, reg
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
