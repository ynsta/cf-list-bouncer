// Package lapi reads active decisions from the CrowdSec Local API, as a bouncer.
package lapi

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// maxErrorBody bounds how much of an error response is quoted in the returned error.
const maxErrorBody = 512

// maxResponseBody bounds a decisions response. Local decisions are a few thousand at
// most; anything larger means the origin filter is not applied.
const maxResponseBody = 64 << 20

// Decision is one active decision as returned by the LAPI.
type Decision struct {
	ID       int64  `json:"id"`
	Origin   string `json:"origin"`
	Scenario string `json:"scenario"`
	Scope    string `json:"scope"`
	Type     string `json:"type"`
	Value    string `json:"value"`
	Duration string `json:"duration"`
}

// Client is a minimal LAPI bouncer client.
type Client struct {
	baseURL   string
	apiKey    string
	origins   []string
	userAgent string
	http      *http.Client
}

// ErrStatus is returned when the LAPI answers with a non-2xx status.
var ErrStatus = errors.New("unexpected LAPI status")

// New returns a client. origins filters decisions by origin (for example "crowdsec",
// "cscli"); empty means every origin, community blocklists included.
func New(baseURL, apiKey string, origins []string, userAgent string, hc *http.Client) *Client {
	return &Client{
		baseURL:   strings.TrimRight(baseURL, "/"),
		apiKey:    apiKey,
		origins:   origins,
		userAgent: userAgent,
		http:      hc,
	}
}

// ActiveDecisions returns every active ip/range decision matching the origin filter.
//
// It asks the stream endpoint with startup=true on every call, which returns the full
// current set rather than a delta. The caller replaces the whole Cloudflare list each
// time, so a full snapshot is what it needs, and a missed delta can never cause drift.
func (c *Client) ActiveDecisions(ctx context.Context) ([]Decision, error) {
	q := url.Values{}
	q.Set("startup", "true")
	q.Set("scopes", "ip,range")

	if len(c.origins) > 0 {
		q.Set("origins", strings.Join(c.origins, ","))
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet,
		c.baseURL+"/v1/decisions/stream?"+q.Encode(), nil)
	if err != nil {
		return nil, fmt.Errorf("build LAPI request: %w", err)
	}

	req.Header.Set("X-Api-Key", c.apiKey)
	req.Header.Set("User-Agent", c.userAgent)
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("query LAPI: %w", err)
	}
	defer func() {
		// Read-only body, fully consumed or abandoned on error: a close error carries
		// nothing actionable.
		_ = resp.Body.Close()
	}()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))

		return nil, fmt.Errorf("%w %d: %s", ErrStatus, resp.StatusCode, strings.TrimSpace(string(body)))
	}

	var payload struct {
		New []Decision `json:"new"`
	}

	err = json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(&payload)
	if err != nil {
		return nil, fmt.Errorf("decode LAPI response: %w", err)
	}

	if payload.New == nil {
		return []Decision{}, nil
	}

	return payload.New, nil
}
