// Package cloudflare replaces the items of a Cloudflare account IP List.
package cloudflare

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/ynsta/cf-list-bouncer/internal/blocklist"
)

// DefaultBaseURL is the Cloudflare API v4 root.
const DefaultBaseURL = "https://api.cloudflare.com/client/v4"

const maxResponseBody = 1 << 20

// Bulk operation statuses, as reported by the API.
const (
	statusCompleted = "completed"
	statusFailed    = "failed"
)

// Errors returned by Client.
var (
	ErrAPI              = errors.New("cloudflare API error")
	ErrOperationFailed  = errors.New("cloudflare bulk operation failed")
	ErrOperationTimeout = errors.New("cloudflare bulk operation timed out")
)

// Options configures a Client.
type Options struct {
	BaseURL   string
	Token     string
	AccountID string
	ListID    string
	UserAgent string
	HTTP      *http.Client
	// PollInterval is the delay between bulk-operation status checks.
	PollInterval time.Duration
	// PollTimeout bounds the wait for one bulk operation to finish.
	PollTimeout time.Duration
}

// Client talks to the Cloudflare Lists API.
type Client struct {
	opts Options
}

// New returns a client. Options are used as given: validate them before calling.
func New(opts Options) *Client {
	opts.BaseURL = strings.TrimRight(opts.BaseURL, "/")

	return &Client{opts: opts}
}

type envelope struct {
	Success bool `json:"success"`
	Errors  []struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"errors"`
	Result json.RawMessage `json:"result"`
}

type listItem struct {
	IP      string `json:"ip"`
	Comment string `json:"comment,omitempty"`
}

// ReplaceItems replaces the whole list with items and waits until Cloudflare has applied
// it. Replacing rather than adding and deleting keeps the operation idempotent: any
// drift, including manual edits in the dashboard, is undone by the next call.
func (c *Client) ReplaceItems(ctx context.Context, items []blocklist.Item) error {
	body := make([]listItem, 0, len(items))
	for _, it := range items {
		body = append(body, listItem{IP: it.IP, Comment: it.Comment})
	}

	payload, err := json.Marshal(body)
	if err != nil {
		return fmt.Errorf("encode list items: %w", err)
	}

	var started struct {
		OperationID string `json:"operation_id"`
	}

	path := fmt.Sprintf("/accounts/%s/rules/lists/%s/items", c.opts.AccountID, c.opts.ListID)
	err = c.do(ctx, http.MethodPut, path, payload, &started)
	if err != nil {
		return fmt.Errorf("replace list items: %w", err)
	}

	return c.wait(ctx, started.OperationID)
}

// wait polls the bulk operation until it completes, fails, or PollTimeout elapses.
func (c *Client) wait(ctx context.Context, operationID string) error {
	ctx, cancel := context.WithTimeout(ctx, c.opts.PollTimeout)
	defer cancel()

	path := fmt.Sprintf("/accounts/%s/rules/lists/bulk_operations/%s", c.opts.AccountID, operationID)

	for {
		var op struct {
			Status string `json:"status"`
			Error  string `json:"error"`
		}

		err := c.do(ctx, http.MethodGet, path, nil, &op)
		if err != nil {
			if ctx.Err() != nil {
				return fmt.Errorf("%w: %s", ErrOperationTimeout, operationID)
			}

			return fmt.Errorf("poll bulk operation %s: %w", operationID, err)
		}

		switch op.Status {
		case statusCompleted:
			return nil
		case statusFailed:
			return fmt.Errorf("%w: %s: %s", ErrOperationFailed, operationID, op.Error)
		}

		select {
		case <-ctx.Done():
			return fmt.Errorf("%w: %s (last status %q)", ErrOperationTimeout, operationID, op.Status)
		case <-time.After(c.opts.PollInterval):
		}
	}
}

func (c *Client) do(ctx context.Context, method, path string, body []byte, result any) error {
	var reader io.Reader
	if body != nil {
		reader = bytes.NewReader(body)
	}

	req, err := http.NewRequestWithContext(ctx, method, c.opts.BaseURL+path, reader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}

	req.Header.Set("Authorization", "Bearer "+c.opts.Token)
	req.Header.Set("User-Agent", c.opts.UserAgent)
	req.Header.Set("Accept", "application/json")

	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	resp, err := c.opts.HTTP.Do(req)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}
	defer func() {
		// Read-only body, fully consumed or abandoned on error: a close error carries
		// nothing actionable.
		_ = resp.Body.Close()
	}()

	err = decode(resp, result)
	if err != nil {
		return fmt.Errorf("%s %s: %w", method, path, err)
	}

	return nil
}

// decode unwraps the Cloudflare response envelope into result, or returns the API
// errors it carries.
func decode(resp *http.Response, result any) error {
	var env envelope

	err := json.NewDecoder(io.LimitReader(resp.Body, maxResponseBody)).Decode(&env)
	if err != nil {
		return fmt.Errorf("status %d, undecodable body: %w", resp.StatusCode, err)
	}

	if !env.Success || resp.StatusCode < 200 || resp.StatusCode > 299 {
		msgs := make([]string, 0, len(env.Errors))
		for _, e := range env.Errors {
			msgs = append(msgs, fmt.Sprintf("%d %s", e.Code, e.Message))
		}

		return fmt.Errorf("%w: status %d: %s", ErrAPI, resp.StatusCode, strings.Join(msgs, "; "))
	}

	err = json.Unmarshal(env.Result, result)
	if err != nil {
		return fmt.Errorf("decode result: %w", err)
	}

	return nil
}
