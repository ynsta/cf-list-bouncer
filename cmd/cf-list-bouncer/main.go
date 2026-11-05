// Command cf-list-bouncer keeps a Cloudflare IP List equal to the active CrowdSec bans,
// so banned clients are blocked at the Cloudflare edge by a WAF rule on that list.
package main

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/collectors"

	"github.com/ynsta/cf-list-bouncer/internal/blocklist"
	"github.com/ynsta/cf-list-bouncer/internal/cloudflare"
	"github.com/ynsta/cf-list-bouncer/internal/config"
	"github.com/ynsta/cf-list-bouncer/internal/lapi"
	"github.com/ynsta/cf-list-bouncer/internal/server"
	"github.com/ynsta/cf-list-bouncer/internal/syncer"
)

const serviceName = "cf-list-bouncer"

// version is set at build time with -ldflags "-X main.version=...".
var version = "dev"

const (
	httpTimeout     = 30 * time.Second
	pollInterval    = 2 * time.Second
	pollTimeout     = 2 * time.Minute
	shutdownTimeout = 5 * time.Second
)

func main() {
	if len(os.Args) > 1 && os.Args[1] == "-version" {
		_, _ = fmt.Fprintln(os.Stdout, version)

		return
	}

	err := run()
	if err != nil {
		slog.Error("exiting", "error", err)
		os.Exit(1)
	}
}

func run() error {
	cfg, err := config.Load(os.Getenv)
	if err != nil {
		return fmt.Errorf("load config: %w", err)
	}

	var level slog.Level
	err = level.UnmarshalText([]byte(cfg.LogLevel))
	if err != nil {
		return fmt.Errorf("log level: %w", err)
	}

	log := slog.New(slog.NewJSONHandler(os.Stdout, &slog.HandlerOptions{Level: level})).
		With("service", serviceName)
	slog.SetDefault(log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	userAgent := serviceName + "/" + version
	hc := &http.Client{Timeout: httpTimeout}

	reg := prometheus.NewRegistry()
	reg.MustRegister(collectors.NewGoCollector(), collectors.NewProcessCollector(collectors.ProcessCollectorOpts{}))

	s := syncer.New(
		lapi.New(cfg.LAPIURL, cfg.LAPIKey, cfg.Origins, userAgent, hc),
		cloudflare.New(cloudflare.Options{
			BaseURL: cfg.CFAPIURL, Token: cfg.CFToken, AccountID: cfg.CFAccountID, ListID: cfg.CFListID,
			UserAgent: userAgent, HTTP: hc, PollInterval: pollInterval, PollTimeout: pollTimeout,
		}),
		syncer.Options{
			Interval:               cfg.SyncInterval,
			ResyncInterval:         cfg.ResyncInterval,
			MaxConsecutiveFailures: cfg.MaxConsecutiveFailures,
			Build:                  blocklist.Options{IPv6Prefix: cfg.IPv6Prefix, MaxItems: cfg.MaxItems},
		},
		log, reg,
	)

	srv := server.New(cfg.ListenAddr, server.Handler(s.Healthy, reg))
	srvErr := make(chan error, 1)

	go func() {
		listenErr := srv.ListenAndServe()
		if listenErr != nil && !errors.Is(listenErr, http.ErrServerClosed) {
			srvErr <- listenErr
		}

		close(srvErr)
	}()

	// Secrets are deliberately absent from this line.
	log.InfoContext(ctx, "starting", "version", version, "lapi_url", cfg.LAPIURL,
		"cf_account_id", cfg.CFAccountID, "cf_list_id", cfg.CFListID, "origins", cfg.Origins,
		"sync_interval", cfg.SyncInterval.String(), "listen_addr", cfg.ListenAddr)

	runErr := make(chan error, 1)
	go func() { runErr <- s.Run(ctx) }()

	select {
	case err = <-runErr:
	case err = <-srvErr:
		err = fmt.Errorf("ops server: %w", err)

		stop()

		loopErr := <-runErr
		if loopErr != nil {
			log.WarnContext(ctx, "sync loop stopped with an error", "error", loopErr)
		}
	}

	shutdownCtx, cancel := context.WithTimeout(context.WithoutCancel(ctx), shutdownTimeout)
	defer cancel()

	shutdownErr := srv.Shutdown(shutdownCtx)
	if shutdownErr != nil {
		log.WarnContext(ctx, "ops server shutdown", "error", shutdownErr)
	}

	if err != nil {
		return fmt.Errorf("sync loop: %w", err)
	}

	log.InfoContext(ctx, "stopped")

	return nil
}
