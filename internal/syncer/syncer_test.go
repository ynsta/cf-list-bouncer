package syncer_test

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/testutil"

	"github.com/ynsta/cf-list-bouncer/internal/blocklist"
	"github.com/ynsta/cf-list-bouncer/internal/lapi"
	"github.com/ynsta/cf-list-bouncer/internal/syncer"
)

var errBoom = errors.New("boom")

type fakeSource struct {
	mu        sync.Mutex
	decisions []lapi.Decision
	err       error
}

func (f *fakeSource) ActiveDecisions(context.Context) ([]lapi.Decision, error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	return f.decisions, f.err
}

func (f *fakeSource) set(d []lapi.Decision, err error) {
	f.mu.Lock()
	defer f.mu.Unlock()

	f.decisions, f.err = d, err
}

type fakeSink struct {
	mu     sync.Mutex
	pushes [][]blocklist.Item
	err    error
}

func (f *fakeSink) ReplaceItems(_ context.Context, items []blocklist.Item) error {
	f.mu.Lock()
	defer f.mu.Unlock()

	if f.err != nil {
		return f.err
	}

	f.pushes = append(f.pushes, items)

	return nil
}

func (f *fakeSink) count() int {
	f.mu.Lock()
	defer f.mu.Unlock()

	return len(f.pushes)
}

type clock struct{ t time.Time }

func (c *clock) now() time.Time { return c.t }

func ban(value string) lapi.Decision {
	return lapi.Decision{Type: "ban", Scope: "Ip", Value: value, Scenario: "s", Origin: "crowdsec"}
}

func newSyncer(t *testing.T, src *fakeSource, sink *fakeSink, clk *clock, opts syncer.Options) (*syncer.Syncer, *prometheus.Registry) {
	t.Helper()

	reg := prometheus.NewRegistry()
	if opts.Interval == 0 {
		opts.Interval = 30 * time.Second
	}
	if opts.Build.MaxItems == 0 {
		opts.Build = blocklist.Options{IPv6Prefix: 64, MaxItems: 100}
	}

	s := syncer.New(src, sink, opts, slog.New(slog.NewTextHandler(io.Discard, nil)), reg)
	s.SetClock(clk.now)

	return s, reg
}

func TestRunOnce_PushesThenSkipsUnchanged(t *testing.T) {
	t.Parallel()

	src := &fakeSource{decisions: []lapi.Decision{ban("34.84.140.206")}}
	sink := &fakeSink{}
	clk := &clock{t: time.Unix(1_800_000_000, 0)}
	s, reg := newSyncer(t, src, sink, clk, syncer.Options{ResyncInterval: time.Hour})

	ctx := context.Background()
	for range 3 {
		if err := s.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
		clk.t = clk.t.Add(time.Minute)
	}

	if sink.count() != 1 {
		t.Fatalf("pushes = %d, want 1 (unchanged list must not be re-pushed)", sink.count())
	}
	if got := testutil.ToFloat64(s.Metrics().Items); got != 1 {
		t.Fatalf("items gauge = %v", got)
	}

	want := `
# HELP cf_list_bouncer_syncs_total Sync attempts by result.
# TYPE cf_list_bouncer_syncs_total counter
cf_list_bouncer_syncs_total{result="error"} 0
cf_list_bouncer_syncs_total{result="pushed"} 1
cf_list_bouncer_syncs_total{result="unchanged"} 2
`
	if err := testutil.GatherAndCompare(reg, strings.NewReader(want), "cf_list_bouncer_syncs_total"); err != nil {
		t.Fatal(err)
	}
}

func TestRunOnce_PushesOnChangeAndOnResync(t *testing.T) {
	t.Parallel()

	src := &fakeSource{decisions: []lapi.Decision{ban("34.84.140.206")}}
	sink := &fakeSink{}
	clk := &clock{t: time.Unix(1_800_000_000, 0)}
	s, _ := newSyncer(t, src, sink, clk, syncer.Options{ResyncInterval: time.Hour})
	ctx := context.Background()

	mustRun := func() {
		t.Helper()
		if err := s.RunOnce(ctx); err != nil {
			t.Fatal(err)
		}
	}

	mustRun() // push 1
	src.set([]lapi.Decision{ban("34.84.140.206"), ban("203.0.113.1")}, nil)
	mustRun() // changed → push 2
	src.set([]lapi.Decision{}, nil)
	mustRun() // ban expired → push 3, an empty list
	clk.t = clk.t.Add(61 * time.Minute)
	mustRun() // unchanged but resync due → push 4

	if sink.count() != 4 {
		t.Fatalf("pushes = %d, want 4", sink.count())
	}
	if last := sink.pushes[2]; len(last) != 0 {
		t.Fatalf("expired bans must empty the list, got %v", last)
	}
}

func TestRunOnce_SourceErrorNeverPushes(t *testing.T) {
	t.Parallel()

	src := &fakeSource{err: errBoom}
	sink := &fakeSink{}
	s, _ := newSyncer(t, src, sink, &clock{t: time.Unix(1_800_000_000, 0)}, syncer.Options{})

	if err := s.RunOnce(context.Background()); !errors.Is(err, errBoom) {
		t.Fatalf("err = %v", err)
	}
	if sink.count() != 0 {
		t.Fatal("a LAPI failure must never push (it would wipe the list)")
	}
}

func TestRunOnce_SinkErrorRetriesNextTime(t *testing.T) {
	t.Parallel()

	src := &fakeSource{decisions: []lapi.Decision{ban("34.84.140.206")}}
	sink := &fakeSink{err: errBoom}
	s, _ := newSyncer(t, src, sink, &clock{t: time.Unix(1_800_000_000, 0)}, syncer.Options{})
	ctx := context.Background()

	if err := s.RunOnce(ctx); !errors.Is(err, errBoom) {
		t.Fatalf("err = %v", err)
	}

	sink.mu.Lock()
	sink.err = nil
	sink.mu.Unlock()

	if err := s.RunOnce(ctx); err != nil {
		t.Fatal(err)
	}
	if sink.count() != 1 {
		t.Fatal("a failed push must be retried even though the list did not change")
	}
}

func TestHealthy(t *testing.T) {
	t.Parallel()

	src := &fakeSource{decisions: []lapi.Decision{}}
	clk := &clock{t: time.Unix(1_800_000_000, 0)}
	s, _ := newSyncer(t, src, &fakeSink{}, clk, syncer.Options{Interval: 30 * time.Second})

	if !s.Healthy() {
		t.Fatal("healthy during the start-up grace period")
	}
	clk.t = clk.t.Add(2 * time.Minute)
	if s.Healthy() {
		t.Fatal("no success after 3 intervals must be unhealthy")
	}
	if err := s.RunOnce(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !s.Healthy() {
		t.Fatal("healthy right after a success")
	}
}

func TestRun_StopsAfterMaxConsecutiveFailures(t *testing.T) {
	t.Parallel()

	src := &fakeSource{err: errBoom}
	s, _ := newSyncer(t, src, &fakeSink{}, &clock{t: time.Unix(1_800_000_000, 0)},
		syncer.Options{Interval: time.Millisecond, MaxConsecutiveFailures: 3})

	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	if err := s.Run(ctx); !errors.Is(err, syncer.ErrTooManyFailures) {
		t.Fatalf("err = %v, want ErrTooManyFailures", err)
	}
}

func TestRun_ReturnsNilOnCancel(t *testing.T) {
	t.Parallel()

	src := &fakeSource{decisions: []lapi.Decision{}}
	s, _ := newSyncer(t, src, &fakeSink{}, &clock{t: time.Unix(1_800_000_000, 0)},
		syncer.Options{Interval: time.Millisecond})

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)

	go func() { done <- s.Run(ctx) }()

	time.Sleep(20 * time.Millisecond)
	cancel()

	if err := <-done; err != nil {
		t.Fatalf("err = %v, want nil on shutdown", err)
	}
}
