package client

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
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
