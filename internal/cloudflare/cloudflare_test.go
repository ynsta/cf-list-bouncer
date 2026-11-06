package cloudflare_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/ynsta/cf-list-bouncer/internal/blocklist"
	"github.com/ynsta/cf-list-bouncer/internal/cloudflare"
)

const (
	account = "0123456789abcdef0123456789abcdef"
	list    = "fedcba9876543210fedcba9876543210"
	token   = "cf-token"
)

type fakeCF struct {
	t        *testing.T
	polls    atomic.Int32
	statuses []string // successive bulk-operation statuses
	opError  string
	putBody  []map[string]string
	putFail  bool
	// putLimited answers the PUT with 429 / 10040 and this Retry-After value ("" = header absent).
	putLimited bool
	retryAfter string
}

func (f *fakeCF) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	if r.Header.Get("Authorization") != "Bearer "+token {
		w.WriteHeader(http.StatusForbidden)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":10000,"message":"Authentication error"}],"result":null}`))

		return
	}

	switch {
	case r.Method == http.MethodPut && r.URL.Path == "/accounts/"+account+"/rules/lists/"+list+"/items":
		if err := json.NewDecoder(r.Body).Decode(&f.putBody); err != nil {
			f.t.Errorf("decode PUT body: %v", err)
		}
		if f.putLimited {
			if f.retryAfter != "" {
				w.Header().Set("Retry-After", f.retryAfter)
			}
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":10040,"message":"you have been ratelimited please wait and try again"}],"result":null}`))

			return
		}
		if f.putFail {
			w.WriteHeader(http.StatusBadRequest)
			_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":10021,"message":"invalid list item"}],"result":null}`))

			return
		}
		_, _ = w.Write([]byte(`{"success":true,"errors":[],"result":{"operation_id":"op-1"}}`))
	case r.Method == http.MethodGet && r.URL.Path == "/accounts/"+account+"/rules/lists/bulk_operations/op-1":
		i := int(f.polls.Add(1)) - 1
		status := f.statuses[min(i, len(f.statuses)-1)]
		_ = json.NewEncoder(w).Encode(map[string]any{
			"success": true, "errors": []any{},
			"result": map[string]string{"id": "op-1", "status": status, "error": f.opError},
		})
	default:
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
	}
}

func newClient(t *testing.T, f *fakeCF, tok string) *cloudflare.Client {
	t.Helper()

	f.t = t
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)

	return cloudflare.New(cloudflare.Options{
		BaseURL: srv.URL, Token: tok, AccountID: account, ListID: list,
		UserAgent: "test", HTTP: srv.Client(),
		PollInterval: time.Millisecond, PollTimeout: time.Second,
	})
}

func TestReplaceItems_WaitsForCompletion(t *testing.T) {
	t.Parallel()

	f := &fakeCF{statuses: []string{"pending", "running", "completed"}}
	c := newClient(t, f, token)

	err := c.ReplaceItems(context.Background(), []blocklist.Item{
		{IP: "34.84.140.206", Comment: "crowdsec: x"},
		{IP: "2001:db8::/64", Comment: "crowdsec: y"},
	})
	if err != nil {
		t.Fatal(err)
	}

	if len(f.putBody) != 2 || f.putBody[0]["ip"] != "34.84.140.206" || f.putBody[1]["comment"] != "crowdsec: y" {
		t.Fatalf("PUT body = %v", f.putBody)
	}
	if got := f.polls.Load(); got != 3 {
		t.Fatalf("polls = %d, want 3", got)
	}
}

func TestReplaceItems_EmptyListSendsEmptyArray(t *testing.T) {
	t.Parallel()

	f := &fakeCF{statuses: []string{"completed"}}
	if err := newClient(t, f, token).ReplaceItems(context.Background(), []blocklist.Item{}); err != nil {
		t.Fatal(err)
	}
	if f.putBody == nil || len(f.putBody) != 0 {
		t.Fatalf("PUT body = %#v, want []", f.putBody)
	}
}

func TestReplaceItems_Errors(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name    string
		f       *fakeCF
		tok     string
		timeout bool
		want    string
	}{
		{name: "auth", f: &fakeCF{statuses: []string{"completed"}}, tok: "wrong", want: "Authentication error"},
		{name: "rejected", f: &fakeCF{statuses: []string{"completed"}, putFail: true}, tok: token, want: "invalid list item"},
		{name: "operation failed", f: &fakeCF{statuses: []string{"failed"}, opError: "too many items"}, tok: token, want: "too many items"},
		{name: "operation never ends", f: &fakeCF{statuses: []string{"running"}}, tok: token, want: "timed out"},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			err := newClient(t, tc.f, tc.tok).ReplaceItems(context.Background(), []blocklist.Item{{IP: "203.0.113.1"}})
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to contain %q", err, tc.want)
			}
			if strings.Contains(err.Error(), token) {
				t.Fatalf("error leaks the token: %v", err)
			}
		})
	}
}

func TestReplaceItems_RateLimited(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name       string
		retryAfter string
		want       time.Duration
	}{
		{name: "seconds", retryAfter: "120", want: 2 * time.Minute},
		{name: "absent", retryAfter: "", want: 0},
		{name: "garbage", retryAfter: "soon", want: 0},
		{name: "negative", retryAfter: "-5", want: 0},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			f := &fakeCF{statuses: []string{"completed"}, putLimited: true, retryAfter: tc.retryAfter}
			err := newClient(t, f, token).ReplaceItems(context.Background(), []blocklist.Item{{IP: "203.0.113.1"}})

			if !errors.Is(err, cloudflare.ErrRateLimited) {
				t.Fatalf("err = %v, want ErrRateLimited", err)
			}
			if !errors.Is(err, cloudflare.ErrAPI) {
				t.Fatalf("err = %v, want it to still match ErrAPI", err)
			}

			var rl *cloudflare.RateLimitError
			if !errors.As(err, &rl) {
				t.Fatalf("err = %v, want a *RateLimitError", err)
			}
			if got := rl.RetryAfter(); got != tc.want {
				t.Fatalf("RetryAfter = %v, want %v", got, tc.want)
			}
			if !strings.Contains(err.Error(), "10040") {
				t.Fatalf("err = %v, want the API error code kept", err)
			}
		})
	}
}

func TestReplaceItems_RetryAfterHTTPDate(t *testing.T) {
	t.Parallel()

	at := time.Now().Add(10 * time.Minute).UTC().Format(http.TimeFormat)
	f := &fakeCF{statuses: []string{"completed"}, putLimited: true, retryAfter: at}
	err := newClient(t, f, token).ReplaceItems(context.Background(), nil)

	var rl *cloudflare.RateLimitError
	if !errors.As(err, &rl) {
		t.Fatalf("err = %v, want a *RateLimitError", err)
	}
	if got := rl.RetryAfter(); got < 9*time.Minute || got > 10*time.Minute {
		t.Fatalf("RetryAfter = %v, want about 10m", got)
	}
}
