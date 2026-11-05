package config_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/ynsta/cf-list-bouncer/internal/config"
)

const (
	acct = "0123456789abcdef0123456789abcdef"
	list = "fedcba9876543210fedcba9876543210"
)

func base() map[string]string {
	return map[string]string{
		"CROWDSEC_LAPI_URL": "http://crowdsec:8080",
		"CROWDSEC_LAPI_KEY": "lapi-key",
		"CF_API_TOKEN":      "cf-token",
		"CF_ACCOUNT_ID":     acct,
		"CF_LIST_ID":        list,
	}
}

func load(env map[string]string) (config.Config, error) {
	return config.Load(func(k string) string { return env[k] })
}

func TestLoad_Defaults(t *testing.T) {
	t.Parallel()

	c, err := load(base())
	if err != nil {
		t.Fatal(err)
	}

	want := config.Config{
		LAPIURL:                "http://crowdsec:8080",
		LAPIKey:                "lapi-key",
		CFAPIURL:               "https://api.cloudflare.com/client/v4",
		CFToken:                "cf-token",
		CFAccountID:            acct,
		CFListID:               list,
		Origins:                []string{"crowdsec", "cscli"},
		SyncInterval:           30 * time.Second,
		ResyncInterval:         time.Hour,
		IPv6Prefix:             64,
		MaxItems:               10000,
		ListenAddr:             ":9090",
		MaxConsecutiveFailures: 20,
		LogLevel:               "info",
	}
	if !reflect.DeepEqual(c, want) {
		t.Fatalf("got  %+v\nwant %+v", c, want)
	}
}

func TestLoad_Overrides(t *testing.T) {
	t.Parallel()

	env := base()
	env["ORIGINS"] = " crowdsec , cscli,lists:firehol "
	env["SYNC_INTERVAL"] = "1m"
	env["RESYNC_INTERVAL"] = "0s"
	env["IPV6_PREFIX"] = "48"
	env["MAX_ITEMS"] = "500"
	env["LISTEN_ADDR"] = "127.0.0.1:9191"
	env["MAX_CONSECUTIVE_FAILURES"] = "0"
	env["LOG_LEVEL"] = "debug"

	c, err := load(env)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(c.Origins, []string{"crowdsec", "cscli", "lists:firehol"}) ||
		c.SyncInterval != time.Minute || c.ResyncInterval != 0 || c.IPv6Prefix != 48 ||
		c.MaxItems != 500 || c.ListenAddr != "127.0.0.1:9191" || c.MaxConsecutiveFailures != 0 ||
		c.LogLevel != "debug" {
		t.Fatalf("got %+v", c)
	}
}

func TestLoad_SecretsFromFiles(t *testing.T) {
	t.Parallel()

	dir := t.TempDir()
	lapiFile := filepath.Join(dir, "lapi")
	cfFile := filepath.Join(dir, "cf")
	if err := os.WriteFile(lapiFile, []byte("file-lapi-key\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(cfFile, []byte("file-cf-token\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	env := base()
	delete(env, "CROWDSEC_LAPI_KEY")
	delete(env, "CF_API_TOKEN")
	env["CROWDSEC_LAPI_KEY_FILE"] = lapiFile
	env["CF_API_TOKEN_FILE"] = cfFile

	c, err := load(env)
	if err != nil {
		t.Fatal(err)
	}
	if c.LAPIKey != "file-lapi-key" || c.CFToken != "file-cf-token" {
		t.Fatalf("secrets not read from files (trailing newline must be trimmed): %q %q", c.LAPIKey, c.CFToken)
	}
}

func TestLoad_Rejects(t *testing.T) {
	t.Parallel()

	emptyFile := filepath.Join(t.TempDir(), "empty")
	if err := os.WriteFile(emptyFile, []byte("  \n"), 0o600); err != nil {
		t.Fatal(err)
	}

	cases := map[string]struct {
		set   map[string]string
		unset []string
		want  string
	}{
		"missing LAPI URL":          {unset: []string{"CROWDSEC_LAPI_URL"}, want: "CROWDSEC_LAPI_URL"},
		"LAPI URL without scheme":   {set: map[string]string{"CROWDSEC_LAPI_URL": "crowdsec:8080"}, want: "CROWDSEC_LAPI_URL"},
		"LAPI URL with query":       {set: map[string]string{"CROWDSEC_LAPI_URL": "http://crowdsec:8080/?x=1"}, want: "CROWDSEC_LAPI_URL"},
		"missing LAPI key":          {unset: []string{"CROWDSEC_LAPI_KEY"}, want: "CROWDSEC_LAPI_KEY"},
		"both key and key file":     {set: map[string]string{"CROWDSEC_LAPI_KEY_FILE": "/x"}, want: "both"},
		"empty key file":            {set: map[string]string{"CF_API_TOKEN_FILE": emptyFile}, unset: []string{"CF_API_TOKEN"}, want: "empty"},
		"missing key file":          {set: map[string]string{"CF_API_TOKEN_FILE": "/nonexistent"}, unset: []string{"CF_API_TOKEN"}, want: "CF_API_TOKEN_FILE"},
		"whitespace token":          {set: map[string]string{"CF_API_TOKEN": "  "}, want: "CF_API_TOKEN"},
		"plain http Cloudflare API": {set: map[string]string{"CF_API_URL": "http://api.cloudflare.com/client/v4"}, want: "https"},
		"bad account id":            {set: map[string]string{"CF_ACCOUNT_ID": "acme"}, want: "CF_ACCOUNT_ID"},
		"bad list id":               {set: map[string]string{"CF_LIST_ID": strings.ToUpper(list)}, want: "CF_LIST_ID"},
		"duration without unit":     {set: map[string]string{"SYNC_INTERVAL": "60"}, want: "SYNC_INTERVAL"},
		"sync interval too short":   {set: map[string]string{"SYNC_INTERVAL": "1s"}, want: "SYNC_INTERVAL"},
		"negative resync":           {set: map[string]string{"RESYNC_INTERVAL": "-1h"}, want: "RESYNC_INTERVAL"},
		"IPv6 prefix too long":      {set: map[string]string{"IPV6_PREFIX": "128"}, want: "IPV6_PREFIX"},
		"IPv6 prefix not a number":  {set: map[string]string{"IPV6_PREFIX": "64bits"}, want: "IPV6_PREFIX"},
		"zero max items":            {set: map[string]string{"MAX_ITEMS": "0"}, want: "MAX_ITEMS"},
		"empty origins":             {set: map[string]string{"ORIGINS": " , "}, want: "ORIGINS"},
		"bad log level":             {set: map[string]string{"LOG_LEVEL": "verbose"}, want: "LOG_LEVEL"},
		"negative failures":         {set: map[string]string{"MAX_CONSECUTIVE_FAILURES": "-1"}, want: "MAX_CONSECUTIVE_FAILURES"},
	}

	for name, tc := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			env := base()
			for _, k := range tc.unset {
				delete(env, k)
			}
			for k, v := range tc.set {
				env[k] = v
			}

			_, err := load(env)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want it to mention %q", err, tc.want)
			}
			for _, secret := range []string{"lapi-key", "cf-token"} {
				if strings.Contains(err.Error(), secret) {
					t.Fatalf("error leaks a secret: %v", err)
				}
			}
		})
	}
}
