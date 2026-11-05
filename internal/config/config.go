// Package config loads and validates the bouncer configuration from the environment.
//
// Every value is validated at startup and an invalid one stops the process: a typo must
// fail at boot, never silently change what gets blocked.
package config

import (
	"errors"
	"fmt"
	"net/url"
	"os"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
)

// minSyncInterval keeps the Cloudflare API usage reasonable: each changed sync is one
// bulk operation, and the API is rate limited per account.
const minSyncInterval = 10 * time.Second

// Cloudflare IP Lists accept IPv6 prefixes from /4 to /64.
const (
	minIPv6Prefix = 4
	maxIPv6Prefix = 64
)

var cfID = regexp.MustCompile(`^[0-9a-f]{32}$`)

// ErrInvalid wraps every validation error.
var ErrInvalid = errors.New("invalid configuration")

// Config is the validated bouncer configuration.
type Config struct {
	LAPIURL     string
	LAPIKey     string
	CFAPIURL    string
	CFToken     string
	CFAccountID string
	CFListID    string
	Origins     []string

	SyncInterval   time.Duration
	ResyncInterval time.Duration
	IPv6Prefix     int
	MaxItems       int

	ListenAddr             string
	MaxConsecutiveFailures int
	LogLevel               string
}

// Load reads the configuration through getenv (os.Getenv in production).
func Load(getenv func(string) string) (Config, error) {
	l := loader{getenv: getenv}

	c := Config{
		LAPIURL:     l.url("CROWDSEC_LAPI_URL", "", false),
		LAPIKey:     l.secret("CROWDSEC_LAPI_KEY"),
		CFAPIURL:    l.url("CF_API_URL", "https://api.cloudflare.com/client/v4", true),
		CFToken:     l.secret("CF_API_TOKEN"),
		CFAccountID: l.id("CF_ACCOUNT_ID"),
		CFListID:    l.id("CF_LIST_ID"),
		Origins:     l.list("ORIGINS", "crowdsec,cscli"),

		SyncInterval:   l.duration("SYNC_INTERVAL", "30s", minSyncInterval),
		ResyncInterval: l.duration("RESYNC_INTERVAL", "1h", 0),
		IPv6Prefix:     l.integer("IPV6_PREFIX", "64", minIPv6Prefix, maxIPv6Prefix),
		MaxItems:       l.integer("MAX_ITEMS", "10000", 1, 1_000_000),

		ListenAddr:             l.str("LISTEN_ADDR", ":9090"),
		MaxConsecutiveFailures: l.integer("MAX_CONSECUTIVE_FAILURES", "20", 0, 1_000_000),
		LogLevel:               l.oneOf("LOG_LEVEL", "info", "debug", "info", "warn", "error"),
	}

	if len(l.errs) > 0 {
		return Config{}, fmt.Errorf("%w: %w", ErrInvalid, errors.Join(l.errs...))
	}

	return c, nil
}

// loader collects every error so the operator sees all of them in one run.
type loader struct {
	getenv func(string) string
	errs   []error
}

func (l *loader) failf(format string, args ...any) {
	l.errs = append(l.errs, fmt.Errorf(format, args...))
}

func (l *loader) str(key, def string) string {
	if v := strings.TrimSpace(l.getenv(key)); v != "" {
		return v
	}

	return def
}

func (l *loader) url(key, def string, requireHTTPS bool) string {
	v := l.str(key, def)
	if v == "" {
		l.failf("%s is required", key)

		return ""
	}

	err := checkURL(v, requireHTTPS)
	if err != nil {
		l.failf("%s: %w", key, err)
	}

	return strings.TrimRight(v, "/")
}

func checkURL(v string, requireHTTPS bool) error {
	u, err := url.Parse(v)

	switch {
	case err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https"):
		return fmt.Errorf("must be an absolute http(s) URL, got %q", v)
	case u.RawQuery != "" || u.Fragment != "" || u.User != nil:
		return errors.New("must not contain credentials, a query or a fragment")
	case requireHTTPS && u.Scheme != "https":
		return errors.New("must use https")
	}

	return nil
}

// secret reads KEY, or the file named by KEY_FILE (Docker secrets). Exactly one must be
// set. Error messages never include the value.
func (l *loader) secret(key string) string {
	direct := l.getenv(key)
	file := strings.TrimSpace(l.getenv(key + "_FILE"))

	switch {
	case direct != "" && file != "":
		l.failf("set either %s or %s_FILE, not both", key, key)

		return ""
	case file != "":
		b, err := os.ReadFile(file) //nolint:gosec // the path is operator configuration, reading it is the point
		if err != nil {
			l.failf("%s_FILE: %w", key, err)

			return ""
		}

		if v := strings.TrimSpace(string(b)); v != "" {
			return v
		}

		l.failf("%s_FILE: file is empty", key)

		return ""
	case strings.TrimSpace(direct) != "":
		return strings.TrimSpace(direct)
	default:
		l.failf("%s (or %s_FILE) is required and must not be blank", key, key)

		return ""
	}
}

func (l *loader) id(key string) string {
	v := l.str(key, "")
	if !cfID.MatchString(v) {
		l.failf("%s must be a 32-character lowercase hex Cloudflare ID", key)
	}

	return v
}

func (l *loader) list(key, def string) []string {
	var out []string

	for part := range strings.SplitSeq(l.str(key, def), ",") {
		if p := strings.TrimSpace(part); p != "" {
			out = append(out, p)
		}
	}

	if len(out) == 0 {
		l.failf("%s must list at least one origin", key)
	}

	return out
}

func (l *loader) duration(key, def string, minimum time.Duration) time.Duration {
	v := l.str(key, def)

	d, err := time.ParseDuration(v)
	if err != nil {
		l.failf("%s must be a duration with a unit (e.g. 30s, 5m), got %q", key, v)

		return 0
	}

	if d < minimum {
		l.failf("%s must be at least %s, got %s", key, minimum, d)
	}

	return d
}

func (l *loader) integer(key, def string, minimum, maximum int) int {
	v := l.str(key, def)

	n, err := strconv.Atoi(v)
	if err != nil || n < minimum || n > maximum {
		l.failf("%s must be an integer between %d and %d, got %q", key, minimum, maximum, v)

		return 0
	}

	return n
}

func (l *loader) oneOf(key, def string, allowed ...string) string {
	v := l.str(key, def)
	if slices.Contains(allowed, v) {
		return v
	}

	l.failf("%s must be one of %s, got %q", key, strings.Join(allowed, ", "), v)

	return ""
}
