package github

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestResolveToken(t *testing.T) {
	for _, tt := range []struct {
		name, env, base, output string
		fail, wantHelper        bool
		wantHost, want          string
	}{
		{"environment", " env-token\n", "https://github.example.com", "", false, false, "", "env-token"},
		{"default host", " \n", "", "helper-token\n", false, true, "github.com", "helper-token"},
		{"default api host", " \n", "https://api.github.com/", "helper-token\n", false, true, "github.com", "helper-token"},
		{"enterprise host", " \n", "https://github.example.com", "helper-token\n", false, true, "github.example.com", "helper-token"},
		{"enterprise host with api path", " \n", "https://ghe.example.com/api/v3", "helper-token\n", false, true, "ghe.example.com", "helper-token"},
		{"helper failure", "", "https://github.example.com", "", true, true, "github.example.com", ""},
		{"empty helper", "", "", " \n", false, true, "github.com", ""},
	} {
		t.Run(tt.name, func(t *testing.T) {
			called := false
			gotHost := ""
			token, err := resolveToken(context.Background(), tt.env, tt.base,
				func(_ context.Context, hostname string) ([]byte, error) {
					called = true
					gotHost = hostname
					if tt.fail {
						return nil, errors.New("sensitive helper output")
					}
					return []byte(tt.output), nil
				})
			if token != tt.want || called != tt.wantHelper || gotHost != tt.wantHost {
				t.Fatalf("token %q, helper called %t for host %q", token, called, gotHost)
			}
			if tt.want == "" && !errors.Is(err, ErrNoToken) {
				t.Fatalf("error: %v", err)
			}
			if tt.want != "" && err != nil {
				t.Fatal(err)
			}
			if err != nil && (strings.Contains(err.Error(), "sensitive") || strings.Contains(err.Error(), "helper-token")) {
				t.Fatalf("helper output leaked into error: %v", err)
			}
		})
	}
}

// The credential helper keeps its five-second deadline, independent of any
// deadline on the caller's context. No helper output is persisted.
func TestResolveTokenHelperDeadline(t *testing.T) {
	_, err := resolveToken(context.Background(), "", "https://github.example.com",
		func(ctx context.Context, hostname string) ([]byte, error) {
			if hostname != "github.example.com" {
				t.Fatalf("helper host %q", hostname)
			}
			deadline, ok := ctx.Deadline()
			if !ok {
				t.Fatal("helper has no deadline")
			}
			if remaining := time.Until(deadline); remaining <= 3*time.Second || remaining > 5*time.Second {
				t.Fatalf("helper deadline in %s, want about 5s", remaining)
			}
			return nil, errors.New("sensitive helper output")
		})
	if !errors.Is(err, ErrNoToken) || strings.Contains(err.Error(), "sensitive") {
		t.Fatalf("unsafe or missing error: %v", err)
	}
}

func TestTokenHostname(t *testing.T) {
	for _, tt := range []struct{ base, want string }{
		{"", "github.com"},
		{"  ", "github.com"},
		{"https://api.github.com", "github.com"},
		{"https://API.GITHUB.COM/api/v3", "github.com"},
		{"https://github.example.com", "github.example.com"},
		{"https://ghe.example.com/api/v3", "ghe.example.com"},
		{"https://github.example.com:8443", "github.example.com:8443"},
		{"https://user:secret@github.example.com", "github.com"},
		{"https://", "github.com"},
		{"not a url", "github.com"},
	} {
		if got := tokenHostname(tt.base); got != tt.want {
			t.Errorf("tokenHostname(%q) = %q, want %q", tt.base, got, tt.want)
		}
	}
}

// A bearer token rides on every request, so only https or loopback http may
// override the API host; a mis-set plain-http host must not leak the token.
func TestNewClientBaseURL(t *testing.T) {
	for _, tt := range []struct {
		url  string
		want bool
	}{
		{"https://api.github.com", true},
		{"http://127.0.0.1:8080", true},
		{"http://localhost:8080", true},
		{"http://[::1]:8080", true},
		{"http://example.com", false},
		{"http://10.0.0.5", false},
		{"ftp://example.com", false},
		{"https://", false},
		{"not a url", false},
	} {
		t.Run(tt.url, func(t *testing.T) {
			t.Setenv("PERCOLATE_GITHUB_URL", tt.url)
			_, err := NewClient("test-token")
			if (err == nil) != tt.want {
				t.Fatalf("url %q: err %v, want accepted=%t", tt.url, err, tt.want)
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
