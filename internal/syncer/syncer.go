// Package syncer keeps a Cloudflare IP List equal to the active CrowdSec bans.
package syncer

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/prometheus/client_golang/prometheus"

	"github.com/ynsta/cf-list-bouncer/internal/blocklist"
	"github.com/ynsta/cf-list-bouncer/internal/lapi"
)

// healthyAfterIntervals is how many sync intervals may pass without a success before the
// bouncer reports itself unhealthy. One failed sync is normal (API blip); three is not.
const healthyAfterIntervals = 3

// DefaultMaxBackoff caps the wait between pushes while the sink keeps rate limiting.
const DefaultMaxBackoff = 30 * time.Minute

// maxRetryAfter caps a Retry-After asked for by the sink, against absurd values.
const maxRetryAfter = 24 * time.Hour

// ErrTooManyFailures is returned by Run when MaxConsecutiveFailures is reached. The
// process then exits and the container restart policy takes over.
var ErrTooManyFailures = errors.New("too many consecutive sync failures")

// Source returns the active decisions.
type Source interface {
	ActiveDecisions(ctx context.Context) ([]lapi.Decision, error)
}

// Sink replaces the remote list.
type Sink interface {
	ReplaceItems(ctx context.Context, items []blocklist.Item) error
}

// RateLimited is implemented by sink errors meaning "slow down". RateLimited returns the
// delay the remote asked for, or zero. Such errors trigger a backoff and never count
// toward MaxConsecutiveFailures: restarting would only push again, sooner.
type RateLimited interface {
	error
	RateLimited() time.Duration
}

// Options tunes a Syncer.
type Options struct {
	Interval time.Duration
	// ResyncInterval forces a push of an unchanged list, to undo drift such as manual
	// edits in the Cloudflare dashboard. Zero disables it.
	ResyncInterval         time.Duration
	MaxConsecutiveFailures int
	// MaxBackoff caps the wait after repeated rate limits. Zero means DefaultMaxBackoff.
	MaxBackoff time.Duration
	Build      blocklist.Options
}

// Metrics are the Prometheus collectors of a Syncer.
type Metrics struct {
	Items               prometheus.Gauge
	Skipped             *prometheus.GaugeVec
	Syncs               *prometheus.CounterVec
	LastSuccess         prometheus.Gauge
	ConsecutiveFailures prometheus.Gauge
}

// Syncer runs the LAPI → Cloudflare reconciliation loop.
type Syncer struct {
	source  Source
	sink    Sink
	opts    Options
	log     *slog.Logger
	metrics Metrics
	now     func() time.Time

	mu          sync.Mutex
	started     time.Time
	lastSuccess time.Time
	lastPush    time.Time
	pushed      []blocklist.Item // nil until the first successful push
	failures    int              // failed syncs since the last success, any cause
	hardFails   int              // same, rate limits excluded: drives MaxConsecutiveFailures
	rateLimits  int              // rate-limited pushes since the last success
}

// New returns a Syncer and registers its metrics on reg.
func New(source Source, sink Sink, opts Options, log *slog.Logger, reg prometheus.Registerer) *Syncer {
	m := Metrics{
		Items: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cf_list_bouncer_list_items",
			Help: "Items in the Cloudflare list after the last successful push.",
		}),
		Skipped: prometheus.NewGaugeVec(prometheus.GaugeOpts{
			Name: "cf_list_bouncer_decisions_skipped",
			Help: "Decisions left out of the last build, by reason.",
		}, []string{"reason"}),
		Syncs: prometheus.NewCounterVec(prometheus.CounterOpts{
			Name: "cf_list_bouncer_syncs_total",
			Help: "Sync attempts by result.",
		}, []string{"result"}),
		LastSuccess: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cf_list_bouncer_last_success_timestamp_seconds",
			Help: "Unix time of the last successful sync.",
		}),
		ConsecutiveFailures: prometheus.NewGauge(prometheus.GaugeOpts{
			Name: "cf_list_bouncer_consecutive_failures",
			Help: "Failed syncs since the last success.",
		}),
	}

	reg.MustRegister(m.Items, m.Skipped, m.Syncs, m.LastSuccess, m.ConsecutiveFailures)

	for _, r := range []string{"pushed", "unchanged", "error", "rate_limited"} {
		m.Syncs.WithLabelValues(r) // export zero values so rate() works from the start
	}

	return &Syncer{
		source: source, sink: sink, opts: opts, log: log, metrics: m,
		now: time.Now, started: time.Now(),
	}
}

// SetClock replaces the clock; tests only.
func (s *Syncer) SetClock(now func() time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.now = now
	s.started = now()
}

// Metrics exposes the collectors, for tests.
func (s *Syncer) Metrics() Metrics { return s.metrics }

// Run syncs immediately, then Interval after each sync, until ctx is cancelled (returns
// nil) or MaxConsecutiveFailures is reached (returns ErrTooManyFailures). A rate-limited
// push waits longer instead: see Backoff.
func (s *Syncer) Run(ctx context.Context) error {
	for {
		err := s.RunOnce(ctx)

		select {
		case <-ctx.Done():
			return nil // shutdown: err, if any, is the cancellation
		default:
		}

		delay := s.opts.Interval
		if err != nil {
			delay, err = s.afterFailure(ctx, err)
			if err != nil {
				return err
			}
		}

		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()

			return nil
		case <-timer.C:
		}
	}
}

// Backoff is the wait after the attempt-th rate limit in a row (attempt >= 1): interval
// doubled per attempt, capped at maxBackoff, but never shorter than retryAfter (itself
// capped at 24 h).
func Backoff(interval, maxBackoff time.Duration, attempt int, retryAfter time.Duration) time.Duration {
	d := interval
	for range attempt {
		if d >= maxBackoff/2 {
			d = maxBackoff

			break
		}

		d *= 2
	}

	d = min(d, maxBackoff)

	return max(d, min(retryAfter, maxRetryAfter))
}

// RunOnce performs one reconciliation. A LAPI error never reaches Cloudflare: pushing a
// list built from a failed read would wipe every ban at the edge.
func (s *Syncer) RunOnce(ctx context.Context) error {
	decisions, err := s.source.ActiveDecisions(ctx)
	if err != nil {
		return s.failed(fmt.Errorf("read decisions: %w", err))
	}

	items, skipped := blocklist.Build(toBlocklist(decisions), s.opts.Build)
	s.recordSkipped(ctx, skipped)

	if !s.needsPush(items) {
		s.succeeded("unchanged", items, false)

		return nil
	}

	err = s.sink.ReplaceItems(ctx, items)
	if err != nil {
		if _, ok := errors.AsType[RateLimited](err); ok {
			return s.rateLimited(fmt.Errorf("push list: %w", err))
		}

		return s.failed(fmt.Errorf("push list: %w", err))
	}

	s.log.InfoContext(ctx, "list pushed", "items", len(items), "decisions", len(decisions))
	s.succeeded("pushed", items, true)

	return nil
}

// Healthy reports whether a sync succeeded recently. Before the first success, the
// process gets the same grace period from its start.
func (s *Syncer) Healthy() bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	ref := s.lastSuccess
	if ref.IsZero() {
		ref = s.started
	}

	return s.now().Sub(ref) <= healthyAfterIntervals*s.opts.Interval
}

// afterFailure logs a failed sync and returns the delay before the next one, or the error
// that ends Run.
func (s *Syncer) afterFailure(ctx context.Context, syncErr error) (time.Duration, error) {
	if rl, ok := errors.AsType[RateLimited](syncErr); ok {
		n := s.rateLimitCount()
		delay := Backoff(s.opts.Interval, s.maxBackoff(), n, rl.RateLimited())
		s.log.WarnContext(ctx, "push rate limited, backing off", "error", syncErr,
			"consecutive_rate_limits", n, "retry_in", delay.String())

		return delay, nil
	}

	n := s.failureCount()
	s.log.WarnContext(ctx, "sync failed", "error", syncErr, "consecutive_failures", n)

	if s.opts.MaxConsecutiveFailures > 0 && n >= s.opts.MaxConsecutiveFailures {
		return 0, fmt.Errorf("%w (%d): last error: %w", ErrTooManyFailures, n, syncErr)
	}

	return s.opts.Interval, nil
}

func (s *Syncer) maxBackoff() time.Duration {
	if s.opts.MaxBackoff > 0 {
		return s.opts.MaxBackoff
	}

	return DefaultMaxBackoff
}

func (s *Syncer) needsPush(items []blocklist.Item) bool {
	s.mu.Lock()
	defer s.mu.Unlock()

	if s.pushed == nil || !blocklist.Equal(items, s.pushed) {
		return true
	}

	return s.opts.ResyncInterval > 0 && s.now().Sub(s.lastPush) >= s.opts.ResyncInterval
}

func (s *Syncer) succeeded(result string, items []blocklist.Item, pushed bool) {
	s.mu.Lock()
	defer s.mu.Unlock()

	now := s.now()
	s.lastSuccess = now
	s.failures = 0
	s.hardFails = 0
	s.rateLimits = 0

	if pushed {
		s.pushed = items
		s.lastPush = now
		s.metrics.Items.Set(float64(len(items)))
	}

	s.metrics.Syncs.WithLabelValues(result).Inc()
	s.metrics.LastSuccess.Set(float64(now.Unix()))
	s.metrics.ConsecutiveFailures.Set(0)
}

func (s *Syncer) failed(err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.failures++
	s.hardFails++
	s.metrics.Syncs.WithLabelValues("error").Inc()
	s.metrics.ConsecutiveFailures.Set(float64(s.failures))

	return err
}

func (s *Syncer) rateLimited(err error) error {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.failures++
	s.rateLimits++
	s.metrics.Syncs.WithLabelValues("rate_limited").Inc()
	s.metrics.ConsecutiveFailures.Set(float64(s.failures))

	return err
}

func (s *Syncer) failureCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.hardFails
}

func (s *Syncer) rateLimitCount() int {
	s.mu.Lock()
	defer s.mu.Unlock()

	return s.rateLimits
}

// recordSkipped exports skip counts and logs the ones that point to a problem. Non-ban
// decisions and unsupported scopes are expected and only logged at debug level.
func (s *Syncer) recordSkipped(ctx context.Context, skipped map[blocklist.SkipReason]int) {
	s.metrics.Skipped.Reset()

	for reason, n := range skipped {
		s.metrics.Skipped.WithLabelValues(string(reason)).Set(float64(n))

		level := slog.LevelWarn
		if reason == blocklist.SkipNotBan || reason == blocklist.SkipScope {
			level = slog.LevelDebug
		}

		s.log.Log(ctx, level, "decisions skipped", "reason", string(reason), "count", n)
	}
}

func toBlocklist(in []lapi.Decision) []blocklist.Decision {
	out := make([]blocklist.Decision, 0, len(in))
	for _, d := range in {
		out = append(out, blocklist.Decision{Type: d.Type, Scope: d.Scope, Value: d.Value, Scenario: d.Scenario})
	}

	return out
}
