package client

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestNew_EndpointNormalisation(t *testing.T) {
	for _, suffix := range []string{"/portal/api", "/portal/api/"} {
		t.Run(suffix, func(t *testing.T) {
			var gotPath string
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				gotPath = r.URL.Path
				_, _ = w.Write([]byte(`{}`))
			}))
			defer srv.Close()
			c, err := New(Config{Endpoint: srv.URL + suffix, APIKey: "k"})
			if err != nil {
				t.Fatal(err)
			}
			if err := c.do(context.Background(), http.MethodGet, "vms/", nil, nil); err != nil {
				t.Fatal(err)
			}
			if gotPath != "/portal/api/vms/" {
				t.Fatalf("path = %q", gotPath)
			}
		})
	}
	t.Run("default", func(t *testing.T) {
		c, err := New(Config{APIKey: "k"})
		if err != nil {
			t.Fatal(err)
		}
		if got := c.baseURL.String(); got != "https://simplevm.denbi.de/portal/api/" {
			t.Fatalf("baseURL = %q", got)
		}
	})
}

func TestNew_RequiresAPIKey(t *testing.T) {
	_, err := New(Config{})
	if err == nil || !strings.Contains(err.Error(), "api key") {
		t.Fatalf("err = %v", err)
	}
}

func TestDo_SetsHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-API-KEY") != "secret" {
			t.Errorf("X-API-KEY = %q", r.Header.Get("X-API-KEY"))
		}
		if r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("Content-Type = %q", r.Header.Get("Content-Type"))
		}
		if r.Header.Get("Accept") != "application/json" {
			t.Errorf("Accept = %q", r.Header.Get("Accept"))
		}
		if r.Header.Get("User-Agent") != "ua/1" {
			t.Errorf("User-Agent = %q", r.Header.Get("User-Agent"))
		}
		b, _ := io.ReadAll(r.Body)
		if strings.TrimSpace(string(b)) != `{"a":1}` {
			t.Errorf("body = %q", b)
		}
		_, _ = w.Write([]byte(`{"ok":true}`))
	}))
	defer srv.Close()
	c, err := New(Config{Endpoint: srv.URL, APIKey: "secret", UserAgent: "ua/1"})
	if err != nil {
		t.Fatal(err)
	}
	var out struct {
		OK bool `json:"ok"`
	}
	if err := c.do(context.Background(), http.MethodPost, "vms/", map[string]int{"a": 1}, &out); err != nil {
		t.Fatal(err)
	}
	if !out.OK {
		t.Fatal("response not decoded")
	}
}

func TestDo_AuthErrors(t *testing.T) {
	for _, code := range []int{401, 403} {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
			w.WriteHeader(code)
			_, _ = w.Write([]byte(`{"detail":"nope"}`))
		}))
		c, _ := New(Config{Endpoint: srv.URL, APIKey: "k"})
		err := c.do(context.Background(), http.MethodGet, "vms/", nil, nil)
		srv.Close()
		var ae *APIError
		if !errors.As(err, &ae) || ae.StatusCode != code {
			t.Fatalf("code %d: err = %v", code, err)
		}
		if IsNotFound(err) {
			t.Fatalf("code %d treated as not found", code)
		}
		if !strings.Contains(err.Error(), "scope") || !strings.Contains(err.Error(), "nope") {
			t.Fatalf("message = %q", err.Error())
		}
	}
}

func TestDo_NotFound(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.NotFound(w, nil)
	}))
	defer srv.Close()
	c, _ := New(Config{Endpoint: srv.URL, APIKey: "k"})
	err := c.do(context.Background(), http.MethodGet, "vms/x/", nil, nil)
	if !IsNotFound(err) {
		t.Fatalf("err = %v", err)
	}
	if !IsNotFound(ErrNotFound) {
		t.Fatal("ErrNotFound should be not found")
	}
}

func TestDo_DoesNotFollowRedirect(t *testing.T) {
	contacted := false
	other := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { contacted = true }))
	defer other.Close()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, other.URL+"/x/", http.StatusFound)
	}))
	defer srv.Close()
	orig := &http.Client{}
	c, _ := New(Config{Endpoint: srv.URL, APIKey: "k", HTTPClient: orig})
	if orig.CheckRedirect != nil {
		t.Fatal("caller's client was mutated")
	}
	err := c.do(context.Background(), http.MethodPost, "vms/", map[string]int{"a": 1}, nil)
	var ae *APIError
	if !errors.As(err, &ae) || ae.StatusCode != 302 || !strings.Contains(err.Error(), "redirect") || !strings.Contains(err.Error(), other.URL) {
		t.Fatalf("err = %v", err)
	}
	if contacted {
		t.Fatal("redirect target was contacted")
	}
}

func TestNew_RequiresHTTPS(t *testing.T) {
	for ep, ok := range map[string]bool{
		"https://example.org/api": true,
		"http://example.org/api":  false,
		"http://127.0.0.1:8080/":  true,
		"http://localhost/api":    true,
		"http://[::1]:8080/":      true,
	} {
		_, err := New(Config{Endpoint: ep, APIKey: "k"})
		if (err == nil) != ok {
			t.Errorf("%s: err = %v", ep, err)
		}
		if err != nil && !strings.Contains(err.Error(), "https") {
			t.Errorf("%s: message lacks https: %v", ep, err)
		}
	}
}

func TestIsTransient(t *testing.T) {
	netErr := &url.Error{Op: "Get", URL: "https://x/", Err: errors.New("connection reset by peer")}
	for name, tc := range map[string]struct {
		err  error
		want bool
	}{
		"nil":               {nil, false},
		"502":               {&APIError{StatusCode: http.StatusBadGateway}, true},
		"503 wrapped":       {fmt.Errorf("listing: %w", &APIError{StatusCode: http.StatusServiceUnavailable}), true},
		"500":               {&APIError{StatusCode: http.StatusInternalServerError}, true},
		"429":               {&APIError{StatusCode: http.StatusTooManyRequests}, true},
		"400":               {&APIError{StatusCode: http.StatusBadRequest}, false},
		"401":               {&APIError{StatusCode: http.StatusUnauthorized}, false},
		"403":               {&APIError{StatusCode: http.StatusForbidden}, false},
		"404":               {&APIError{StatusCode: http.StatusNotFound}, false},
		"not found":         {ErrNotFound, false},
		"network":           {fmt.Errorf("SimpleVM API GET x: %w", netErr), true},
		"deadline":          {&url.Error{Op: "Get", URL: "https://x/", Err: context.DeadlineExceeded}, false},
		"canceled":          {fmt.Errorf("x: %w", context.Canceled), false},
		"other plain error": {errors.New("VM entered failure state"), false},
	} {
		t.Run(name, func(t *testing.T) {
			if got := IsTransient(tc.err); got != tc.want {
				t.Errorf("IsTransient(%v) = %v, want %v", tc.err, got, tc.want)
			}
		})
	}
}
