package main

import (
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/prometheus/client_golang/prometheus"
	dto "github.com/prometheus/client_model/go"
)

// Test_metricsMiddlewareCountsRequests verifies that the middleware records a completed request.
func Test_metricsMiddlewareCountsRequests(t *testing.T) {
	const (
		method = http.MethodPost
		path   = "/metrics-test"
		status = "204"
	)

	before := metricValue(t, method, path, status)
	handler := metricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNoContent)
	}))

	req := httptest.NewRequest(method, "http://lin.ko"+path, nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	after := metricValue(t, method, path, status)
	if after != before+1 {
		t.Fatalf("unexpected counter value: got %v, want %v", after, before+1)
	}
	if rr.Code != http.StatusNoContent {
		t.Fatalf("unexpected response status: got %d, want %d", rr.Code, http.StatusNoContent)
	}
}

// Test_metricsMiddlewareDefaultsToOKStatus verifies that a body write without WriteHeader counts as HTTP 200.
func Test_metricsMiddlewareDefaultsToOKStatus(t *testing.T) {
	const (
		method = http.MethodGet
		path   = "/metrics-default-ok"
		status = "200"
	)

	before := metricValue(t, method, path, status)
	handler := metricsMiddleware(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("ok"))
	}))

	req := httptest.NewRequest(method, "http://lin.ko"+path, nil)
	rr := httptest.NewRecorder()
	handler.ServeHTTP(rr, req)

	after := metricValue(t, method, path, status)
	if after != before+1 {
		t.Fatalf("unexpected counter value: got %v, want %v", after, before+1)
	}
	if rr.Code != http.StatusOK {
		t.Fatalf("unexpected response status: got %d, want %d", rr.Code, http.StatusOK)
	}
}

// metricValue returns the current http_requests_total value for the provided label set.
func metricValue(t *testing.T, method, path, status string) float64 {
	t.Helper()

	mfs, err := prometheus.DefaultGatherer.Gather()
	if err != nil {
		t.Fatalf("failed to gather metrics: %v", err)
	}

	for _, mf := range mfs {
		if mf.GetName() != "http_requests_total" {
			continue
		}
		for _, metric := range mf.GetMetric() {
			if hasMetricLabels(metric, method, path, status) {
				return metric.GetCounter().GetValue()
			}
		}
	}

	return 0
}

// hasMetricLabels reports whether a metric has the requested method, path, and status labels.
func hasMetricLabels(metric *dto.Metric, method, path, status string) bool {
	var gotMethod, gotPath, gotStatus string
	for _, label := range metric.GetLabel() {
		switch label.GetName() {
		case "method":
			gotMethod = label.GetValue()
		case "path":
			gotPath = label.GetValue()
		case "status":
			gotStatus = label.GetValue()
		}
	}

	return gotMethod == method && gotPath == path && gotStatus == status
}
