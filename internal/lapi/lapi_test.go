package lapi_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/ynsta/cf-list-bouncer/internal/lapi"
)

func TestActiveDecisions_RequestAndDecode(t *testing.T) {
	t.Parallel()

	var gotPath, gotQuery, gotKey, gotUA string

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotPath, gotQuery = r.URL.Path, r.URL.RawQuery
		gotKey, gotUA = r.Header.Get("X-Api-Key"), r.Header.Get("User-Agent")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"new":[
			{"id":1,"origin":"crowdsec","scenario":"crowdsecurity/http-probing","scope":"Ip","type":"ban","value":"34.84.140.206","duration":"3h"},
			{"id":2,"origin":"cscli","scenario":"manual","scope":"Range","type":"ban","value":"203.0.113.0/24","duration":"5m"}
		],"deleted":null}`))
	}))
	t.Cleanup(srv.Close)

	c := lapi.New(srv.URL+"/", "secret-key", []string{"crowdsec", "cscli"}, "cf-list-bouncer/test", srv.Client())

	got, err := c.ActiveDecisions(context.Background())
	if err != nil {
		t.Fatal(err)
	}

	if gotPath != "/v1/decisions/stream" {
		t.Errorf("path = %q", gotPath)
	}
	for _, want := range []string{"startup=true", "dedup=false", "scopes=ip%2Crange", "origins=crowdsec%2Ccscli"} {
		if !strings.Contains(gotQuery, want) {
			t.Errorf("query %q lacks %q", gotQuery, want)
		}
	}
	if gotKey != "secret-key" || gotUA != "cf-list-bouncer/test" {
		t.Errorf("headers: key=%q ua=%q", gotKey, gotUA)
	}
	if len(got) != 2 || got[0].Value != "34.84.140.206" || got[1].Scope != "Range" || got[1].Origin != "cscli" {
		t.Fatalf("decisions = %+v", got)
	}
}

func TestActiveDecisions_NullNewIsEmpty(t *testing.T) {
	t.Parallel()

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"new":null,"deleted":null}`))
	}))
	t.Cleanup(srv.Close)

	got, err := lapi.New(srv.URL, "k", nil, "ua", srv.Client()).ActiveDecisions(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got == nil || len(got) != 0 {
		t.Fatalf("got %v, want empty non-nil slice", got)
	}
}

func TestActiveDecisions_Errors(t *testing.T) {
	t.Parallel()

	cases := map[string]http.HandlerFunc{
		"forbidden": func(w http.ResponseWriter, _ *http.Request) {
			http.Error(w, `{"message":"access forbidden"}`, http.StatusForbidden)
		},
		"bad json": func(w http.ResponseWriter, _ *http.Request) {
			_, _ = w.Write([]byte(`{"new":[`))
		},
	}

	for name, h := range cases {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			srv := httptest.NewServer(h)
			t.Cleanup(srv.Close)

			got, err := lapi.New(srv.URL, "k", nil, "ua", srv.Client()).ActiveDecisions(context.Background())
			if err == nil {
				t.Fatalf("want error, got %v", got)
			}
			if strings.Contains(err.Error(), "k\"") {
				t.Fatalf("error leaks the API key: %v", err)
			}
		})
	}
}
