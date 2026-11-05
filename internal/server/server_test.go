package server_test

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/ynsta/cf-list-bouncer/internal/server"
)

func get(t *testing.T, h http.Handler, method, path string) *httptest.ResponseRecorder {
	t.Helper()

	rec := httptest.NewRecorder()
	h.ServeHTTP(rec, httptest.NewRequestWithContext(t.Context(), method, path, nil))

	return rec
}

func TestHandler(t *testing.T) {
	t.Parallel()

	reg := prometheus.NewRegistry()
	g := prometheus.NewGauge(prometheus.GaugeOpts{Name: "cf_list_bouncer_test_gauge", Help: "test"})
	reg.MustRegister(g)

	healthy := true
	h := server.Handler(func() bool { return healthy }, reg)

	if rec := get(t, h, http.MethodGet, "/healthz"); rec.Code != http.StatusOK || strings.TrimSpace(rec.Body.String()) != "ok" {
		t.Fatalf("healthy: %d %q", rec.Code, rec.Body.String())
	}

	healthy = false
	if rec := get(t, h, http.MethodGet, "/healthz"); rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("unhealthy: %d", rec.Code)
	}

	if rec := get(t, h, http.MethodGet, "/metrics"); rec.Code != http.StatusOK || !strings.Contains(rec.Body.String(), "cf_list_bouncer_test_gauge") {
		t.Fatalf("metrics: %d", rec.Code)
	}

	if rec := get(t, h, http.MethodGet, "/"); rec.Code != http.StatusNotFound {
		t.Fatalf("unknown path: %d", rec.Code)
	}

	if rec := get(t, h, http.MethodPost, "/healthz"); rec.Code != http.StatusMethodNotAllowed {
		t.Fatalf("POST /healthz: %d", rec.Code)
	}
}
