package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestResolveToken(t *testing.T) {
	for _, tt := range []struct {
		name, env, output string
		fail, wantHelper  bool
		want              string
	}{
		{"environment", " env-token\n", "", false, false, "env-token"},
		{"helper", " \n", "helper-token\n", false, true, "helper-token"},
		{"missing", "", "", true, true, ""},
		{"empty helper", "", " \n", false, true, ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			token, err := resolveToken(context.Background(), tt.env, func(context.Context) ([]byte, error) {
				called = true
				if tt.fail {
					return nil, errors.New("sensitive helper output")
				}
				return []byte(tt.output), nil
			})
			if token != tt.want || called != tt.wantHelper {
				t.Fatalf("unexpected token resolution")
			}
			if tt.want == "" && !errors.Is(err, ErrNoToken) {
				t.Fatalf("error: %v", err)
			}
			if tt.want != "" && err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestConditionalPages(t *testing.T) {
	calls := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer test-token" {
			t.Error("missing authentication")
		}
		if r.URL.RequestURI() != "/repos/example/project/pulls?page=1" {
			t.Error("lost query")
		}
		switch calls {
		case 1:
			if r.Header.Get("If-None-Match") != "" {
				t.Error("unexpected validator")
			}
			w.Header().Set("ETag", `"first"`)
			_, _ = w.Write([]byte(`[1]`))
		case 2:
			if r.Header.Get("If-None-Match") != `"first"` {
				t.Error("missing validator")
			}
			w.WriteHeader(304)
		case 3:
			w.Header().Set("ETag", `"second"`)
			_, _ = w.Write([]byte(`[2]`))
		default:
			if r.Header.Get("If-None-Match") != `"second"` {
				t.Error("stale validator")
			}
			w.WriteHeader(304)
		}
	}))
	defer server.Close()
	c, _ := NewClient("test-token")
	c.baseURL = server.URL
	for _, want := range []string{"[1]", "[1]", "[2]", "[2]"} {
		body, err := c.ListPage(context.Background(), "/repos/example/project/pulls?page=1")
		if err != nil || string(body) != want {
			t.Fatalf("body %s, error %v", body, err)
		}
		body[1] = '9' // Caller mutation must not change cached bytes.
	}
}

func TestPageFailures(t *testing.T) {
	for _, tt := range []struct {
		name   string
		status int
		body   string
	}{
		{"unauthorized", 401, "secret"}, {"rate limited", 403, "secret"},
		{"server failure", 500, "secret"}, {"uncached 304", 304, ""}, {"invalid JSON", 200, "secret"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			c, _ := NewClient("test-token")
			c.baseURL = server.URL
			_, err := c.ListPage(context.Background(), "/list")
			if err == nil || strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "test-token") {
				t.Fatalf("unsafe or missing error: %v", err)
			}
			if len(c.pages) != 0 {
				t.Fatal("failed response cached")
			}
		})
	}
	c, _ := NewClient("test-token")
	for _, path := range []string{"https://example.invalid/list", "//example.invalid/list", "list", "/list#fragment"} {
		if _, err := c.ListPage(context.Background(), path); err == nil {
			t.Fatalf("accepted %q", path)
		}
	}
	if _, err := NewClient(" "); !errors.Is(err, ErrNoToken) {
		t.Fatal("empty token accepted")
	}
}

func TestPageIsolationAndRedirect(t *testing.T) {
	leaked := false
	destination := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { leaked = true }))
	defer destination.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("If-None-Match") != "" {
			t.Error("validator crossed page boundary")
		}
		if r.URL.Path == "/redirect" {
			http.Redirect(w, r, destination.URL, http.StatusFound)
			return
		}
		w.Header().Set("ETag", `"page"`)
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()
	c, _ := NewClient("test-token")
	c.baseURL = server.URL
	for _, path := range []string{"/list?page=1", "/list?page=2"} {
		if _, err := c.ListPage(context.Background(), path); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := c.ListPage(context.Background(), "/redirect"); err == nil {
		t.Fatal("redirect accepted")
	}
	if leaked {
		t.Fatal("followed redirect")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.ListPage(ctx, "/cancelled"); err == nil {
		t.Fatal("cancelled request succeeded")
	}
}
