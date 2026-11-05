package blocklist_test

import (
	"reflect"
	"strings"
	"testing"

	"github.com/ynsta/cf-list-bouncer/internal/blocklist"
)

func ban(scope, value, scenario string) blocklist.Decision {
	return blocklist.Decision{Type: "ban", Scope: scope, Value: value, Scenario: scenario}
}

func values(items []blocklist.Item) []string {
	out := make([]string, 0, len(items))
	for _, it := range items {
		out = append(out, it.IP)
	}
	return out
}

func TestBuild_NormalizesAndDeduplicates(t *testing.T) {
	t.Parallel()

	decisions := []blocklist.Decision{
		ban("Ip", "34.84.140.206", "crowdsecurity/http-probing"),
		ban("Ip", "34.84.140.206", "crowdsecurity/http-sensitive-files"), // same IP, second decision
		ban("Ip", "2a0d:e487:131f:94e1::adb:535d", "manual"),             // IPv6 address → /64
		ban("Ip", "2a0d:e487:131f:94e1::1", "manual"),                    // same /64 → deduplicated
		ban("Range", "203.0.113.0/24", "manual"),
		ban("Range", "198.51.100.7/32", "manual"),          // /32 range → plain address
		ban("Range", "2001:db8:1:2:3::/80", "manual"),      // narrower than /64 → widened
		ban("range", "192.0.2.128/25", "case-insensitive"), // scope matched case-insensitively
		ban("Ip", "::ffff:198.51.100.9", "mapped"),         // IPv4-mapped IPv6 → IPv4
	}

	got, skipped := blocklist.Build(decisions, blocklist.Options{IPv6Prefix: 64, MaxItems: 100})

	want := []string{
		"192.0.2.128/25",
		"198.51.100.7",
		"198.51.100.9",
		"2001:db8:1:2::/64",
		"203.0.113.0/24",
		"2a0d:e487:131f:94e1::/64",
		"34.84.140.206",
	}
	if !reflect.DeepEqual(values(got), want) {
		t.Fatalf("items = %v, want %v", values(got), want)
	}
	if len(skipped) != 0 {
		t.Fatalf("skipped = %v, want none", skipped)
	}
}

func TestBuild_KeepsFirstScenarioAsComment(t *testing.T) {
	t.Parallel()

	got, _ := blocklist.Build([]blocklist.Decision{
		ban("Ip", "34.84.140.206", "crowdsecurity/http-probing"),
		ban("Ip", "34.84.140.206", "crowdsecurity/http-sensitive-files"),
	}, blocklist.Options{IPv6Prefix: 64, MaxItems: 10})

	if len(got) != 1 || got[0].Comment != "crowdsec: crowdsecurity/http-probing" {
		t.Fatalf("got %+v", got)
	}
}

func TestBuild_TruncatesLongComments(t *testing.T) {
	t.Parallel()

	got, _ := blocklist.Build([]blocklist.Decision{
		ban("Ip", "34.84.140.206", strings.Repeat("x", 500)),
	}, blocklist.Options{IPv6Prefix: 64, MaxItems: 10})

	if n := len([]rune(got[0].Comment)); n != blocklist.MaxCommentLen {
		t.Fatalf("comment length = %d, want %d", n, blocklist.MaxCommentLen)
	}
}

func TestBuild_Skips(t *testing.T) {
	t.Parallel()

	decisions := []blocklist.Decision{
		{Type: "captcha", Scope: "Ip", Value: "34.84.140.206"}, // not a ban
		ban("Country", "CN", "geo"),                            // unsupported scope
		ban("Ip", "not-an-ip", "bad"),                          // invalid value
		ban("Range", "10.0.0.0/7", "bad"),                      // wider than Cloudflare allows
		ban("Range", "2000::/3", "bad"),                        // wider than Cloudflare allows
		ban("Ip", "10.1.2.3", "private"),                       // not publicly routable
		ban("Ip", "127.0.0.1", "loopback"),
		ban("Ip", "fe80::1", "link-local"),
	}

	got, skipped := blocklist.Build(decisions, blocklist.Options{IPv6Prefix: 64, MaxItems: 10})

	if len(got) != 0 {
		t.Fatalf("items = %v, want none", values(got))
	}
	want := map[blocklist.SkipReason]int{
		blocklist.SkipNotBan:    1,
		blocklist.SkipScope:     1,
		blocklist.SkipInvalid:   1,
		blocklist.SkipTooWide:   2,
		blocklist.SkipNotPublic: 3,
	}
	if !reflect.DeepEqual(skipped, want) {
		t.Fatalf("skipped = %v, want %v", skipped, want)
	}
}

func TestBuild_CapsAtMaxItems(t *testing.T) {
	t.Parallel()

	decisions := []blocklist.Decision{
		ban("Ip", "203.0.113.3", "s"),
		ban("Ip", "203.0.113.1", "s"),
		ban("Ip", "203.0.113.2", "s"),
	}

	got, skipped := blocklist.Build(decisions, blocklist.Options{IPv6Prefix: 64, MaxItems: 2})

	if want := []string{"203.0.113.1", "203.0.113.2"}; !reflect.DeepEqual(values(got), want) {
		t.Fatalf("items = %v, want %v", values(got), want)
	}
	if skipped[blocklist.SkipOverLimit] != 1 {
		t.Fatalf("skipped = %v, want 1 over_limit", skipped)
	}
}

func TestBuild_EmptyInputGivesEmptyList(t *testing.T) {
	t.Parallel()

	got, skipped := blocklist.Build(nil, blocklist.Options{IPv6Prefix: 64, MaxItems: 10})
	if got == nil || len(got) != 0 || len(skipped) != 0 {
		t.Fatalf("got %v / %v, want empty non-nil list and no skips", got, skipped)
	}
}

func TestEqual(t *testing.T) {
	t.Parallel()

	a := []blocklist.Item{{IP: "203.0.113.1", Comment: "x"}}
	b := []blocklist.Item{{IP: "203.0.113.1", Comment: "y"}}
	c := []blocklist.Item{{IP: "203.0.113.2", Comment: "x"}}

	if !blocklist.Equal(a, b) {
		t.Error("comment-only difference must compare equal")
	}
	if blocklist.Equal(a, c) || blocklist.Equal(a, nil) {
		t.Error("different addresses must not compare equal")
	}
}
