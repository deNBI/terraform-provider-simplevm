// Package client is a small HTTP client for the SimpleVM REST API.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand/v2"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/hashicorp/terraform-plugin-log/tflog"
)

// DefaultEndpoint is the SimpleVM portal API used when no endpoint is given.
const DefaultEndpoint = "https://simplevm.denbi.de/portal/api/"

const maxErrorBody = 2048

// ErrNotFound is returned by higher-level helpers when an object does not exist.
var ErrNotFound = errors.New("not found")

// Config configures a Client.
type Config struct {
	Endpoint   string
	APIKey     string
	UserAgent  string
	HTTPClient *http.Client
	// MaxAttempts is how often an idempotent request (GET, HEAD, DELETE) is
	// sent when it fails with a transient error (see IsTransient). 0 selects
	// DefaultMaxAttempts, 1 disables retries. POST is always sent once.
	MaxAttempts int
	// RetryWait is the base wait before the first retry; it doubles with
	// every further one, up to MaxRetryWait, and is randomised to between
	// half and all of that (jitter). 0 selects DefaultRetryWait.
	RetryWait time.Duration
}

const (
	// DefaultMaxAttempts is the default of Config.MaxAttempts.
	DefaultMaxAttempts = 4
	// DefaultRetryWait is the default of Config.RetryWait.
	DefaultRetryWait = time.Second
	// MaxRetryWait caps the wait between two attempts.
	MaxRetryWait = 30 * time.Second
	// MaxRetryAfter is the longest Retry-After the client waits for; a
	// longer one ends the request with the error instead.
	MaxRetryAfter = time.Minute
)

// Client talks to the SimpleVM API.
type Client struct {
	baseURL     *url.URL
	apiKey      string
	userAgent   string
	httpClient  *http.Client
	maxAttempts int
	retryWait   time.Duration

	// pauseUntil is shared by all requests of the client: after a transient
	// error no request is sent before it, so parallel operations back off
	// together instead of hammering a struggling server.
	mu         sync.Mutex
	pauseUntil time.Time
}

// New validates cfg and returns a Client. An empty Endpoint selects
// DefaultEndpoint; a trailing slash is added when missing so that requests
// never trigger Django's redirect (which would drop POST bodies).
func New(cfg Config) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("api key must not be empty")
	}
	endpoint := cfg.Endpoint
	if endpoint == "" {
		endpoint = DefaultEndpoint
	}
	if !strings.HasSuffix(endpoint, "/") {
		endpoint += "/"
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return nil, fmt.Errorf("invalid endpoint %q: %w", cfg.Endpoint, err)
	}
	if u.Scheme == "" || u.Host == "" {
		return nil, fmt.Errorf("invalid endpoint %q: must be an absolute URL", cfg.Endpoint)
	}
	if u.Scheme != "https" && !isLoopback(u.Hostname()) {
		return nil, fmt.Errorf("invalid endpoint %q: the API key would be sent unencrypted, use https", cfg.Endpoint)
	}
	var hc *http.Client
	if cfg.HTTPClient == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	} else {
		cp := *cfg.HTTPClient // never mutate the caller's client
		hc = &cp
	}
	// Never follow redirects: Go would forward X-API-KEY cross-origin and turn
	// POST into GET. A 3xx is reported as an APIError instead.
	prev := hc.CheckRedirect
	hc.CheckRedirect = func(req *http.Request, via []*http.Request) error {
		if prev != nil {
			if err := prev(req, via); err != nil {
				return err
			}
		}
		return http.ErrUseLastResponse
	}
	ua := cfg.UserAgent
	if ua == "" {
		ua = "terraform-provider-simplevm"
	}
	c := &Client{baseURL: u, apiKey: cfg.APIKey, userAgent: ua, httpClient: hc,
		maxAttempts: cfg.MaxAttempts, retryWait: cfg.RetryWait}
	if c.maxAttempts <= 0 {
		c.maxAttempts = DefaultMaxAttempts
	}
	if c.retryWait <= 0 {
		c.retryWait = DefaultRetryWait
	}
	return c, nil
}

func isLoopback(host string) bool {
	return host == "localhost" || host == "127.0.0.1" || host == "::1"
}

// APIError is returned for any non-2xx response.
type APIError struct {
	StatusCode int
	Method     string
	URL        string
	Body       string
	// Location is the Location header of a 3xx response.
	Location string
	// RetryAfter is the parsed Retry-After header, 0 when absent or invalid.
	RetryAfter time.Duration
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("SimpleVM API %s %s returned HTTP %d", e.Method, e.URL, e.StatusCode)
	if e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden {
		msg += ": check that the API key is valid, not expired and has the required scope"
	}
	if e.StatusCode >= 300 && e.StatusCode < 400 {
		msg += fmt.Sprintf(": the endpoint redirected to %q; check the endpoint URL and its trailing slash", e.Location)
	}
	if e.Body != "" {
		msg += ": " + e.Body
	}
	return msg
}

// IsNotFound reports whether err means the object does not exist.
func IsNotFound(err error) bool {
	if errors.Is(err, ErrNotFound) {
		return true
	}
	var ae *APIError
	return errors.As(err, &ae) && ae.StatusCode == http.StatusNotFound
}

// IsTransient reports whether err is worth retrying while polling: an HTTP
// 5xx or 429 answer, or a network error (connection refused or reset, client
// timeout, ...). Context cancellation and deadline expiry are not transient,
// nor is any other API error (4xx, including 401/403 and 404).
func IsTransient(err error) bool {
	if err == nil || errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) {
		return false
	}
	var ae *APIError
	if errors.As(err, &ae) {
		return ae.StatusCode >= 500 || ae.StatusCode == http.StatusTooManyRequests
	}
	// httpClient.Do reports every transport failure as *url.Error.
	var ue *url.Error
	var ne net.Error
	return errors.As(err, &ue) || errors.As(err, &ne)
}

// do performs a request against a path relative to the base URL (no leading
// slash, e.g. "vms/"). body, if non-nil, is sent as JSON; out, if non-nil,
// receives the decoded JSON response.
func (c *Client) do(ctx context.Context, method, path string, body any, out any) error {
	return c.doURL(ctx, method, c.baseURL.String()+path, body, out)
}

// doURL is like do but takes an absolute URL.
//
// Idempotent requests (GET, HEAD, DELETE) that fail with a transient error
// (see IsTransient) are retried, up to maxAttempts requests in total: the
// SimpleVM gateway occasionally stalls or answers 502/504 for a single
// request. A retried DELETE whose first attempt did go through gets 404,
// which callers already treat as deleted. POST is never retried, because a
// create that timed out at the gateway may still have been carried out.
//
// To stay gentle with a struggling server, retries back off exponentially
// with jitter (see retryDelay), honour Retry-After (up to MaxRetryAfter; a
// longer one is not waited for), and every transient error pauses all
// requests of the client, not only the failed one.
func (c *Client) doURL(ctx context.Context, method, rawURL string, body any, out any) error {
	var b []byte
	if body != nil {
		var err error
		if b, err = json.Marshal(body); err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
	}
	attempts := 1
	if method == http.MethodGet || method == http.MethodHead || method == http.MethodDelete {
		attempts = c.maxAttempts
	}
	for attempt := 1; ; attempt++ {
		if err := c.waitForPause(ctx); err != nil {
			return err
		}
		err := c.send(ctx, method, rawURL, b, out)
		if err == nil || !IsTransient(err) {
			return err
		}
		delay := retryDelay(c.retryWait, attempt, rand.Int64N)
		var ae *APIError
		if errors.As(err, &ae) && ae.RetryAfter > 0 {
			if ae.RetryAfter > MaxRetryAfter {
				c.pause(MaxRetryAfter)
				return err
			}
			delay = max(delay, ae.RetryAfter)
		}
		c.pause(delay)
		if attempt >= attempts {
			return err
		}
		tflog.Warn(ctx, "SimpleVM API request failed, retrying", map[string]any{
			"method": method, "url": rawURL, "attempt": attempt, "max_attempts": attempts,
			"retry_in": delay.String(), "error": err.Error(),
		})
		if werr := c.waitForPause(ctx); werr != nil {
			return fmt.Errorf("%w; last error: %w", werr, err)
		}
	}
}

// retryDelay is the wait after the attempt-th failed attempt: base doubled
// per attempt, capped at MaxRetryWait, then randomised to between half and
// all of it so that parallel requests do not retry in lockstep. rnd returns
// a number in [0, n), like rand.Int64N.
func retryDelay(base time.Duration, attempt int, rnd func(n int64) int64) time.Duration {
	d := MaxRetryWait
	if attempt-1 < 32 {
		d = min(base<<(attempt-1), MaxRetryWait)
	}
	if d <= 0 {
		d = MaxRetryWait
	}
	half := d / 2
	return half + time.Duration(rnd(int64(d-half)+1))
}

// pause makes all requests of the client wait at least d from now.
func (c *Client) pause(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if until := time.Now().Add(d); until.After(c.pauseUntil) {
		c.pauseUntil = until
	}
}

// waitForPause blocks until the client-wide pause is over or ctx ends.
func (c *Client) waitForPause(ctx context.Context) error {
	for {
		c.mu.Lock()
		d := time.Until(c.pauseUntil)
		c.mu.Unlock()
		if d <= 0 {
			return nil
		}
		t := time.NewTimer(d)
		select {
		case <-ctx.Done():
			t.Stop()
			return ctx.Err()
		case <-t.C:
		}
	}
}

// parseRetryAfter parses a Retry-After header (seconds or an HTTP date)
// relative to now; 0 when absent, invalid or in the past.
func parseRetryAfter(v string, now time.Time) time.Duration {
	v = strings.TrimSpace(v)
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil {
		return max(time.Duration(secs)*time.Second, 0)
	}
	if t, err := http.ParseTime(v); err == nil {
		return max(t.Sub(now), 0)
	}
	return 0
}

// send performs one request; body is the encoded JSON body or nil.
func (c *Client) send(ctx context.Context, method, rawURL string, body []byte, out any) error {
	var rdr io.Reader
	if body != nil {
		rdr = bytes.NewReader(body)
	}
	req, err := http.NewRequestWithContext(ctx, method, rawURL, rdr)
	if err != nil {
		return err
	}
	req.Header.Set("X-API-KEY", c.apiKey)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.userAgent)
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	tflog.Debug(ctx, "SimpleVM API request", map[string]any{
		"method": method, "url": rawURL, "x-api-key": "***",
	})
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return fmt.Errorf("SimpleVM API %s %s: %w", method, rawURL, err)
	}
	defer func() { _ = resp.Body.Close() }()

	if resp.StatusCode < 200 || resp.StatusCode > 299 {
		loc := resp.Header.Get("Location")
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		tflog.Debug(ctx, "SimpleVM API error response", map[string]any{
			"method": method, "url": rawURL, "status": resp.StatusCode,
		})
		return &APIError{StatusCode: resp.StatusCode, Method: method, URL: rawURL, Body: strings.TrimSpace(string(b)), Location: loc,
			RetryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), time.Now())}
	}
	tflog.Debug(ctx, "SimpleVM API response", map[string]any{
		"method": method, "url": rawURL, "status": resp.StatusCode,
	})
	if out == nil {
		return nil
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return fmt.Errorf("reading response: %w", err)
	}
	if len(bytes.TrimSpace(data)) == 0 {
		return nil
	}
	if err := json.Unmarshal(data, out); err != nil {
		return fmt.Errorf("decoding response of %s %s: %w", method, rawURL, err)
	}
	return nil
}
