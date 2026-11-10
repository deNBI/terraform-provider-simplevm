// Package client is a small HTTP client for the SimpleVM REST API.
package client

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
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
	hc := cfg.HTTPClient
	if hc == nil {
		hc = &http.Client{Timeout: 60 * time.Second}
	}
	ua := cfg.UserAgent
	if ua == "" {
		ua = "terraform-provider-simplevm"
	}
	return &Client{baseURL: u, apiKey: cfg.APIKey, userAgent: ua, httpClient: hc}, nil
}

// APIError is returned for any non-2xx response.
type APIError struct {
	StatusCode int
	Method     string
	URL        string
	Body       string
}

func (e *APIError) Error() string {
	msg := fmt.Sprintf("SimpleVM API %s %s returned HTTP %d", e.Method, e.URL, e.StatusCode)
	if e.StatusCode == http.StatusUnauthorized || e.StatusCode == http.StatusForbidden {
		msg += ": check that the API key is valid, not expired and has the required scope"
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
		b, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorBody))
		tflog.Debug(ctx, "SimpleVM API error response", map[string]any{
			"method": method, "url": rawURL, "status": resp.StatusCode,
		})
		return &APIError{StatusCode: resp.StatusCode, Method: method, URL: rawURL, Body: strings.TrimSpace(string(b))}
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
