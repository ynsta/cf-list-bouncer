// Package blocklist turns CrowdSec decisions into the items of a Cloudflare IP List.
//
// It is pure: no I/O, no clock. The same decisions always give the same items in the same
// order, so the caller can compare two builds to decide whether a sync is needed.
package blocklist

import (
	"net/netip"
	"slices"
	"strings"
)

// MaxCommentLen bounds the comment stored with each list item. Cloudflare limits item
// comments; scenario names are short, so a tight bound loses nothing useful.
const MaxCommentLen = 100

// Cloudflare IP Lists only accept prefixes within these bounds. Anything wider is
// rejected by the API and would fail the whole bulk operation.
const (
	minIPv4Bits = 8
	minIPv6Bits = 4
)

// Decision is the subset of a CrowdSec LAPI decision this package needs.
type Decision struct {
	Type     string
	Scope    string
	Value    string
	Scenario string
}

// Item is one entry of the Cloudflare IP List.
type Item struct {
	// IP is an address ("203.0.113.1") or a CIDR ("2001:db8::/64"), in canonical form.
	IP      string
	Comment string
}

// SkipReason says why a decision did not become a list item.
type SkipReason string

// Reasons a decision is left out of the list.
const (
	SkipNotBan    SkipReason = "not_ban"
	SkipScope     SkipReason = "unsupported_scope"
	SkipInvalid   SkipReason = "invalid_value"
	SkipTooWide   SkipReason = "too_wide"
	SkipNotPublic SkipReason = "not_public"
	SkipOverLimit SkipReason = "over_limit"
)

// Options tunes Build.
type Options struct {
	// IPv6Prefix is the length every IPv6 address or narrower range is widened to.
	// Clients rotate addresses inside their /64, so banning a single /128 is easy to evade.
	IPv6Prefix int
	// MaxItems caps the list size; Cloudflare plans limit the number of list items.
	MaxItems int
}

// Build converts decisions into a sorted, deduplicated list of items, and counts the
// decisions it left out by reason. The result is never nil.
func Build(decisions []Decision, opts Options) ([]Item, map[SkipReason]int) {
	skipped := map[SkipReason]int{}
	byIP := make(map[string]Item, len(decisions))

	for _, d := range decisions {
		prefix, reason := normalize(d, opts.IPv6Prefix)
		if reason != "" {
			skipped[reason]++

			continue
		}

		key := format(prefix)
		if _, seen := byIP[key]; seen {
			continue // first decision wins; later ones only differ by scenario
		}

		byIP[key] = Item{IP: key, Comment: comment(d.Scenario)}
	}

	items := make([]Item, 0, len(byIP))
	for _, it := range byIP {
		items = append(items, it)
	}

	slices.SortFunc(items, func(a, b Item) int { return strings.Compare(a.IP, b.IP) })

	if opts.MaxItems > 0 && len(items) > opts.MaxItems {
		skipped[SkipOverLimit] += len(items) - opts.MaxItems
		items = items[:opts.MaxItems]
	}

	return items, skipped
}

// Equal reports whether two builds hold the same addresses. Comments are ignored: a
// scenario change alone is not worth a Cloudflare bulk operation.
func Equal(a, b []Item) bool {
	return slices.EqualFunc(a, b, func(x, y Item) bool { return x.IP == y.IP })
}

func normalize(d Decision, ipv6Prefix int) (netip.Prefix, SkipReason) {
	if !strings.EqualFold(d.Type, "ban") {
		return netip.Prefix{}, SkipNotBan
	}

	prefix, reason := parse(d)
	if reason != "" {
		return netip.Prefix{}, reason
	}

	if prefix.Addr().Is6() && prefix.Bits() > ipv6Prefix {
		prefix = netip.PrefixFrom(prefix.Addr(), ipv6Prefix)
	}

	prefix = prefix.Masked()

	minBits := minIPv4Bits
	if prefix.Addr().Is6() {
		minBits = minIPv6Bits
	}

	if prefix.Bits() < minBits {
		return netip.Prefix{}, SkipTooWide
	}

	if !isPublic(prefix.Addr()) {
		return netip.Prefix{}, SkipNotPublic
	}

	return prefix, ""
}

// parse reads an Ip or Range decision value as a prefix; a single address becomes a
// full-length prefix.
func parse(d Decision) (netip.Prefix, SkipReason) {
	switch strings.ToLower(d.Scope) {
	case "ip":
		addr, err := netip.ParseAddr(d.Value)
		if err != nil {
			return netip.Prefix{}, SkipInvalid
		}

		addr = addr.Unmap().WithZone("")

		return netip.PrefixFrom(addr, addr.BitLen()), ""
	case "range":
		p, err := netip.ParsePrefix(d.Value)
		if err != nil {
			return netip.Prefix{}, SkipInvalid
		}

		return p, ""
	default:
		return netip.Prefix{}, SkipScope
	}
}

// isPublic rejects addresses that can never be a Cloudflare visitor. Banning them at the
// edge is useless, and a private address in the list usually means a parsing mistake.
func isPublic(a netip.Addr) bool {
	return !a.IsPrivate() && !a.IsLoopback() && !a.IsLinkLocalUnicast() &&
		!a.IsUnspecified() && !a.IsMulticast()
}

// format writes a single address without its prefix length, as Cloudflare stores it.
func format(p netip.Prefix) string {
	if p.IsSingleIP() {
		return p.Addr().String()
	}

	return p.String()
}

func comment(scenario string) string {
	c := "crowdsec: " + scenario
	if r := []rune(c); len(r) > MaxCommentLen {
		c = string(r[:MaxCommentLen])
	}

	return c
}
