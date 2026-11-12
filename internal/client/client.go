// Package client is a small HTTP client for the SimpleVM REST API.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
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
}

// Client talks to the SimpleVM API.
type Client struct {
	baseURL    *url.URL
	apiKey     string
	userAgent  string
	httpClient *http.Client
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
	return &Client{baseURL: u, apiKey: cfg.APIKey, userAgent: ua, httpClient: hc}, nil
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
func (c *Client) doURL(ctx context.Context, method, rawURL string, body any, out any) error {
	var rdr io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encoding request body: %w", err)
		}
		rdr = bytes.NewReader(b)
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
		return &APIError{StatusCode: resp.StatusCode, Method: method, URL: rawURL, Body: strings.TrimSpace(string(b)), Location: loc}
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
