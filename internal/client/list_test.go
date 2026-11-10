package client

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

type item struct {
	ID int `json:"id"`
}

func TestDecodeList_ArrayAndPage(t *testing.T) {
	items, next, err := decodeList[item]([]byte(`[{"id":1}]`))
	if err != nil || len(items) != 1 || next != "" {
		t.Fatalf("array: %v %v %q", items, err, next)
	}
	items, next, err = decodeList[item]([]byte(`{"count":1,"next":null,"results":[{"id":1}]}`))
	if err != nil || len(items) != 1 || next != "" {
		t.Fatalf("page: %v %v %q", items, err, next)
	}
	items, next, err = decodeList[item]([]byte(`{"count":2,"next":"https://x/vms/?page=2","results":[{"id":1}]}`))
	if err != nil || len(items) != 1 || next != "https://x/vms/?page=2" {
		t.Fatalf("next: %v %v %q", items, err, next)
	}
	if _, _, err = decodeList[item]([]byte(`"junk"`)); err == nil {
		t.Fatal("expected error")
	}
}

func TestListAll_FollowsNext(t *testing.T) {
	var reqs []string
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs = append(reqs, r.URL.RequestURI())
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`{"next":null,"results":[{"id":4},{"id":5}]}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"next":%q,"results":[{"id":1},{"id":2},{"id":3}]}`, srv.URL+"/api/vms/?page=2&page_size=100")
	}))
	defer srv.Close()
	c, _ := New(Config{Endpoint: srv.URL + "/api", APIKey: "k"})
	items, err := listAll[item](context.Background(), c, "vms/")
	if err != nil {
		t.Fatal(err)
	}
	if len(items) != 5 {
		t.Fatalf("items = %v", items)
	}
	if len(reqs) != 2 || reqs[0] != "/api/vms/?page_size=100" || reqs[1] != "/api/vms/?page=2&page_size=100" {
		t.Fatalf("reqs = %v", reqs)
	}
}

func TestListAll_RejectsForeignNextHost(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"next":"https://evil.example/api/vms/?page=2","results":[{"id":1}]}`))
	}))
	defer srv.Close()
	c, _ := New(Config{Endpoint: srv.URL + "/api", APIKey: "k"})
	if _, err := listAll[item](context.Background(), c, "vms/"); err == nil {
		t.Fatal("expected error for foreign host")
	}
}

func TestListAll_UpgradesHTTPNextToBaseScheme(t *testing.T) {
	var reqs []string
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		reqs = append(reqs, r.URL.RequestURI())
		if r.URL.Query().Get("page") == "2" {
			_, _ = w.Write([]byte(`{"next":null,"results":[{"id":2}]}`))
			return
		}
		_, _ = fmt.Fprintf(w, `{"next":"http://%s/api/vms/?page=2&x=y","results":[{"id":1}]}`, srv.Listener.Addr())
	}))
	defer srv.Close()
	c, _ := New(Config{Endpoint: srv.URL + "/api", APIKey: "k", HTTPClient: srv.Client()})
	items, err := listAll[item](context.Background(), c, "vms/")
	if err != nil || len(items) != 2 {
		t.Fatalf("items=%v err=%v", items, err)
	}
	if len(reqs) != 2 || reqs[1] != "/api/vms/?page=2&x=y" {
		t.Fatalf("reqs = %v", reqs)
	}
}

func TestListAll_DetectsRepeatedNext(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_, _ = fmt.Fprintf(w, `{"next":%q,"results":[{"id":1}]}`, srv.URL+"/api/vms/?page=2")
	}))
	defer srv.Close()
	c, _ := New(Config{Endpoint: srv.URL + "/api", APIKey: "k"})
	if _, err := listAll[item](context.Background(), c, "vms/"); err == nil || !strings.Contains(err.Error(), "loops") {
		t.Fatalf("err = %v", err)
	}
}
