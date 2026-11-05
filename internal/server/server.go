// Package server exposes the operational endpoints: /healthz and /metrics.
//
// There is no other API. The bouncer is driven entirely by its configuration.
package server

import (
	"net/http"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promhttp"
)

// Timeouts for the ops server. Requests are tiny; generous values only protect against
// slow clients holding connections open.
const (
	readHeaderTimeout = 5 * time.Second
	writeTimeout      = 30 * time.Second
	idleTimeout       = 60 * time.Second
)

// Handler routes GET /healthz and GET /metrics.
func Handler(healthy func() bool, gatherer prometheus.Gatherer) http.Handler {
	mux := http.NewServeMux()

	mux.HandleFunc("GET /healthz", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/plain; charset=utf-8")

		if !healthy() {
			w.WriteHeader(http.StatusServiceUnavailable)
			_, _ = w.Write([]byte("no successful sync recently\n"))

			return
		}

		_, _ = w.Write([]byte("ok\n"))
	})

	mux.Handle("GET /metrics", promhttp.HandlerFor(gatherer, promhttp.HandlerOpts{}))

	return mux
}

// New returns an http.Server for the ops endpoints.
func New(addr string, h http.Handler) *http.Server {
	return &http.Server{
		Addr:              addr,
		Handler:           h,
		ReadHeaderTimeout: readHeaderTimeout,
		WriteTimeout:      writeTimeout,
		IdleTimeout:       idleTimeout,
	}
}
