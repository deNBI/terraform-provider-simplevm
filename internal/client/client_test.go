package client

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
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
