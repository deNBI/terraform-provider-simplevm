package client

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

// flaky serves failures answers with status fail, then 200 with body.
func flaky(t *testing.T, failures int, fail int, body string, cfg Config) (*Client, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if int(n.Add(1)) <= failures {
			w.WriteHeader(fail)
			_, _ = w.Write([]byte(`<html><body><h1>504 Gateway Time-out</h1></body></html>`))
			return
		}
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	cfg.Endpoint = srv.URL + "/portal/api/"
	cfg.APIKey = "k"
	if cfg.RetryWait == 0 {
		cfg.RetryWait = time.Millisecond
	}
	c, err := New(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c, &n
}

func TestRetry_GETRetriesTransientErrors(t *testing.T) {
	c, n := flaky(t, DefaultMaxAttempts-1, http.StatusGatewayTimeout, `{"uuid":"u1","vm_state":"active"}`, Config{})
	vm, err := c.GetVM(context.Background(), "u1")
	if err != nil || vm.UUID != "u1" {
		t.Fatalf("vm=%+v err=%v", vm, err)
	}
	if got := n.Load(); got != DefaultMaxAttempts {
		t.Fatalf("requests = %d, want %d", got, DefaultMaxAttempts)
	}
}

func TestRetry_GETGivesUpAfterMaxAttempts(t *testing.T) {
	c, n := flaky(t, 100, http.StatusGatewayTimeout, `{}`, Config{})
	_, err := c.GetVM(context.Background(), "u1")
	var ae *APIError
	if !errors.As(err, &ae) || ae.StatusCode != http.StatusGatewayTimeout {
		t.Fatalf("err = %v, want HTTP 504", err)
	}
	if got := n.Load(); got != DefaultMaxAttempts {
		t.Fatalf("requests = %d, want %d", got, DefaultMaxAttempts)
	}
}

func TestRetry_DELETERetriesTransientErrors(t *testing.T) {
	c, n := flaky(t, 1, http.StatusBadGateway, ``, Config{})
	if err := c.DeleteVM(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}
	if got := n.Load(); got != 2 {
		t.Fatalf("requests = %d, want 2", got)
	}
}

// POST is not idempotent: a create that timed out at the gateway may still
// have been carried out, so retrying could create a second VM.
func TestRetry_POSTIsNotRetried(t *testing.T) {
	c, n := flaky(t, 1, http.StatusGatewayTimeout, `{"uuid":"u1"}`, Config{})
	if _, err := c.CreateVM(context.Background(), CreateVMRequest{VMName: "n"}); err == nil {
		t.Fatal("want error")
	}
	if got := n.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}

func TestRetry_NonTransientErrorsAreNotRetried(t *testing.T) {
	for _, status := range []int{http.StatusNotFound, http.StatusForbidden, http.StatusBadRequest} {
		c, n := flaky(t, 1, status, `{}`, Config{})
		if _, err := c.GetVM(context.Background(), "u1"); err == nil {
			t.Fatalf("%d: want error", status)
		}
		if got := n.Load(); got != 1 {
			t.Fatalf("%d: requests = %d, want 1", status, got)
		}
	}
}

func TestRetry_MaxAttemptsOneDisablesRetries(t *testing.T) {
	c, n := flaky(t, 1, http.StatusServiceUnavailable, `{}`, Config{MaxAttempts: 1})
	if _, err := c.GetVM(context.Background(), "u1"); err == nil {
		t.Fatal("want error")
	}
	if got := n.Load(); got != 1 {
		t.Fatalf("requests = %d, want 1", got)
	}
}

func TestRetry_ContextCancelStopsBackoff(t *testing.T) {
	c, _ := flaky(t, 100, http.StatusServiceUnavailable, `{}`, Config{RetryWait: time.Hour})
	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := c.GetVM(ctx, "u1")
	if !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("err = %v, want deadline exceeded", err)
	}
	if time.Since(start) > 5*time.Second {
		t.Fatal("backoff ignored the context")
	}
}

func TestRetryDelay_ExponentialWithJitterAndCap(t *testing.T) {
	low := func(int64) int64 { return 0 }
	high := func(n int64) int64 { return n - 1 }
	for _, tc := range []struct {
		attempt  int
		min, max time.Duration
	}{
		{1, 500 * time.Millisecond, time.Second},
		{2, time.Second, 2 * time.Second},
		{3, 2 * time.Second, 4 * time.Second},
		{10, MaxRetryWait / 2, MaxRetryWait},
		{100, MaxRetryWait / 2, MaxRetryWait}, // no overflow
	} {
		if got := retryDelay(time.Second, tc.attempt, low); got != tc.min {
			t.Errorf("attempt %d low = %v, want %v", tc.attempt, got, tc.min)
		}
		if got := retryDelay(time.Second, tc.attempt, high); got != tc.max {
			t.Errorf("attempt %d high = %v, want %v", tc.attempt, got, tc.max)
		}
	}
}

func TestParseRetryAfter(t *testing.T) {
	now := time.Date(2026, 11, 12, 10, 0, 0, 0, time.UTC)
	for in, want := range map[string]time.Duration{
		"":                              0,
		"7":                             7 * time.Second,
		"-1":                            0,
		"soon":                          0,
		"Thu, 12 Nov 2026 10:00:30 GMT": 30 * time.Second,
		"Thu, 12 Nov 2026 09:00:00 GMT": 0,
	} {
		if got := parseRetryAfter(in, now); got != want {
			t.Errorf("parseRetryAfter(%q) = %v, want %v", in, got, want)
		}
	}
}

// retryAfterServer answers the first request with 503 and the given
// Retry-After header, then 200.
func retryAfterServer(t *testing.T, retryAfter string) (*Client, *atomic.Int32) {
	t.Helper()
	var n atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		if n.Add(1) == 1 {
			w.Header().Set("Retry-After", retryAfter)
			w.WriteHeader(http.StatusServiceUnavailable)
			return
		}
		_, _ = w.Write([]byte(`{"uuid":"u1"}`))
	}))
	t.Cleanup(srv.Close)
	c, err := New(Config{Endpoint: srv.URL + "/portal/api/", APIKey: "k", RetryWait: time.Millisecond})
	if err != nil {
		t.Fatal(err)
	}
	return c, &n
}

func TestRetry_HonoursRetryAfter(t *testing.T) {
	c, n := retryAfterServer(t, "1")
	start := time.Now()
	if _, err := c.GetVM(context.Background(), "u1"); err != nil {
		t.Fatal(err)
	}
	if el := time.Since(start); el < time.Second {
		t.Fatalf("retried after %v, want at least the 1s Retry-After", el)
	}
	if n.Load() != 2 {
		t.Fatalf("requests = %d, want 2", n.Load())
	}
}

func TestRetry_RetryAfterTooLongIsNotWaitedFor(t *testing.T) {
	c, n := retryAfterServer(t, "3600")
	start := time.Now()
	_, err := c.GetVM(context.Background(), "u1")
	var ae *APIError
	if !errors.As(err, &ae) || ae.StatusCode != http.StatusServiceUnavailable || ae.RetryAfter != time.Hour {
		t.Fatalf("err = %v, want HTTP 503 with RetryAfter 1h", err)
	}
	if n.Load() != 1 || time.Since(start) > 5*time.Second {
		t.Fatalf("requests = %d after %v, want 1 without waiting", n.Load(), time.Since(start))
	}
}

// TestRetry_TransientErrorPausesOtherRequests checks that after a transient
// error every request of the client, not only the retried one, waits: with
// Terraform's parallelism a struggling gateway must not get hit by all other
// operations at full speed.
func TestRetry_TransientErrorPausesOtherRequests(t *testing.T) {
	const wait = 200 * time.Millisecond
	var (
		n     atomic.Int32
		times = make(chan time.Time, 2)
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		times <- time.Now()
		if n.Add(1) == 1 {
			w.WriteHeader(http.StatusBadGateway)
			return
		}
		_, _ = w.Write([]byte(`{"uuid":"u1"}`))
	}))
	t.Cleanup(srv.Close)
	c, err := New(Config{Endpoint: srv.URL + "/portal/api/", APIKey: "k", MaxAttempts: 1, RetryWait: wait})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := c.GetVM(ctx, "u1"); err == nil {
		t.Fatal("want error")
	}
	// A POST is never retried, but still waits for the pause.
	if _, err := c.CreateVM(ctx, CreateVMRequest{VMName: "n"}); err != nil {
		t.Fatal(err)
	}
	first, second := <-times, <-times
	if gap := second.Sub(first); gap < wait/2 {
		t.Fatalf("second request %v after the failed one, want at least %v", gap, wait/2)
	}
}
