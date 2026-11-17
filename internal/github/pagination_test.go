package github

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strconv"
	"testing"
)

func TestListAllFiveHundred(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		q := r.URL.Query()
		page, _ := strconv.Atoi(q.Get("page"))
		if q.Get("state") != "all" || q.Get("per_page") != "100" || page < 1 || page > 6 {
			t.Errorf("unexpected query: %s", r.URL.RawQuery)
		}
		if r.Header.Get("If-None-Match") == `"cached"` {
			w.WriteHeader(http.StatusNotModified)
			return
		}
		w.Header().Set("ETag", `"cached"`)
		items := make([]int, 0)
		if page <= 5 {
			for i := 0; i < 100; i++ {
				items = append(items, (page-1)*100+i+1)
			}
		}
		_ = json.NewEncoder(w).Encode(items)
	}))
	defer server.Close()
	c, _ := NewClient("test-token")
	c.baseURL = server.URL
	for pass := 1; pass <= 2; pass++ {
		items, err := c.ListAll(context.Background(), "/pulls?state=all&page=99&per_page=1", 6)
		if err != nil || len(items) != 500 {
			t.Fatalf("items=%d error=%v", len(items), err)
		}
		for i, item := range items {
			if string(item) != strconv.Itoa(i+1) {
				t.Fatalf("item %d: %s", i, item)
			}
		}
		if calls != pass*6 {
			t.Fatalf("requests=%d", calls)
		}
		items[0][0] = '9'
	}
}

func TestListAllIncomplete(t *testing.T) {
	for _, mode := range []string{"limit", "failure", "object", "null", "oversized", "empty"} {
		t.Run(mode, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				if calls == 2 {
					switch mode {
					case "failure":
						w.WriteHeader(503)
						return
					case "object":
						_, _ = w.Write([]byte(`{}`))
						return
					case "null":
						_, _ = w.Write([]byte(`null`))
						return
					case "empty":
						_, _ = w.Write([]byte(` [] `))
						return
					}
				}
				size := 100
				if mode == "oversized" {
					size = 101
				}
				_ = json.NewEncoder(w).Encode(make([]int, size))
			}))
			defer server.Close()
			c, _ := NewClient("test-token")
			c.baseURL = server.URL
			items, err := c.ListAll(context.Background(), "/list", 2)
			if mode == "empty" {
				if err != nil || len(items) != 100 {
					t.Fatalf("items=%d error=%v", len(items), err)
				}
			} else if err == nil || items != nil {
				t.Fatalf("partial result: items=%d error=%v", len(items), err)
			}
			if calls > 2 {
				t.Fatalf("exceeded request bound: %d", calls)
			}
		})
	}
}

func TestListAllInvalidInput(t *testing.T) {
	c, _ := NewClient("test-token")
	c.http.Transport = rejectTransport{t}
	for _, path := range []string{"", "https://example.invalid/list", "//example.invalid/list", "/list#fragment", "/list?bad=%zz"} {
		if _, err := c.ListAll(context.Background(), path, 1); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	if _, err := c.ListAll(context.Background(), "/list", 0); err == nil {
		t.Fatal("accepted zero limit")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	// Use the standard transport to verify cancellation before any connection.
	c.http.Transport = nil
	if items, err := c.ListAll(ctx, "/list", 1); err == nil || items != nil {
		t.Fatal("accepted cancelled read")
	}
}

type rejectTransport struct{ t *testing.T }

func (r rejectTransport) RoundTrip(*http.Request) (*http.Response, error) {
	r.t.Fatal("invalid input issued a request")
	return nil, nil
}
