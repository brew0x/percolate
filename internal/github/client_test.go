package github

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
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
		{"default api host with explicit port", " \n", "https://api.github.com:443", "helper-token\n", false, true, "github.com", "helper-token"},
		{"enterprise host", " \n", "https://github.example.com", "helper-token\n", false, true, "github.example.com", "helper-token"},
		{"mixed-case enterprise host", " \n", "https://GHE.example.com/api/v3", "helper-token\n", false, true, "ghe.example.com", "helper-token"},
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
		{"https://api.github.com:443", "github.com"},
		{"https://API.GITHUB.COM:443/api/v3", "github.com"},
		{"https://api.github.com:8443", "api.github.com:8443"},
		{"https://github.example.com", "github.example.com"},
		{"https://ghe.example.com/api/v3", "ghe.example.com"},
		{"https://GHE.example.com", "ghe.example.com"},
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

// Both the parser and the error line are checked against a fixed clock, so
// flooring, the parsed date and the display rounding are exact regardless of
// when CI runs.
func TestRetryAfterAt(t *testing.T) {
	now := time.Date(2026, time.November, 20, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name, header string
		now          time.Time
		want         time.Time
		wantOK       bool
	}{
		{"seconds", "120", now, now.Add(2 * time.Minute), true},
		{"zero seconds", "0", now, now, true},
		{"future HTTP date", now.Add(2 * time.Minute).UTC().Format(http.TimeFormat), now, now.Add(2 * time.Minute), true},
		{"past HTTP date", now.Add(-90 * time.Second).UTC().Format(http.TimeFormat), now, now, true},
		// A date at 12:00:02 arriving at 12:00:00.600 must keep 12:00:02:
		// rounding the wait for display once moved the reset 400 ms early,
		// to 12:00:01.600, before GitHub permits the retry.
		{"date inside the current second",
			time.Date(2026, time.November, 20, 12, 0, 2, 0, time.UTC).Format(http.TimeFormat),
			now.Add(600 * time.Millisecond),
			time.Date(2026, time.November, 20, 12, 0, 2, 0, time.UTC), true},
		{"negative seconds", "-5", now, time.Time{}, false},
		{"absurd seconds", "999999999999999999999", now, time.Time{}, false},
		{"absurd HTTP date", "Fri, 01 Jan 9999 00:00:00 GMT", now, time.Time{}, false},
		{"invalid", "tomorrow", now, time.Time{}, false},
		{"empty", "", now, time.Time{}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := retryAfterAt(tt.header, tt.now)
			if ok != tt.wantOK || !got.Equal(tt.want) {
				t.Fatalf("retryAfterAt(%q) = %s, %t; want %s, %t", tt.header, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}

// A refused read carries the reset GitHub recorded, so refresh scheduling can
// wait for that instant instead of guessing a delay. Both headers are resolved
// against the caller-supplied now; when both name a deadline, the later one
// wins. A Retry-After never cuts a recorded reset short, and a missing,
// malformed or absurd reset cannot discard a usable Retry-After. Zero want
// marks the responses that record no deadline at all.
func TestRateLimitErrorCarriesResetTime(t *testing.T) {
	now := time.Date(2026, time.November, 20, 12, 0, 0, 0, time.UTC)
	reset := now.Add(2 * time.Minute)
	absurdReset := strconv.FormatInt(time.Date(9999, time.January, 1, 0, 0, 0, 0, time.UTC).Unix(), 10)
	for _, tt := range []struct {
		name, remaining, reset, retryAfter string
		want                               time.Time
	}{
		{"core limit", "0", strconv.FormatInt(reset.Unix(), 10), "", reset},
		{"core reset later than retry-after", "0", strconv.FormatInt(reset.Unix(), 10), "30", reset},
		{"retry-after later than core reset", "0", strconv.FormatInt(now.Add(30*time.Second).Unix(), 10), "120", now.Add(2 * time.Minute)},
		{"core reset missing", "0", "", "120", now.Add(2 * time.Minute)},
		{"core reset malformed", "0", "tomorrow", "120", now.Add(2 * time.Minute)},
		// A reset past the largest representable wait would saturate the
		// scheduling duration, a pause of roughly 292 years; it is unactionable
		// on its own and never hides a usable Retry-After.
		{"core reset absurd", "0", absurdReset, "", time.Time{}},
		{"core reset absurd, retry-after usable", "0", absurdReset, "120", now.Add(2 * time.Minute)},
		{"past core reset, later retry-after", "0", strconv.FormatInt(now.Add(-time.Minute).Unix(), 10), "120", now.Add(2 * time.Minute)},
		{"secondary limit", "412", "", "120", now.Add(2 * time.Minute)},
		{"secondary limit date", "412", "", now.Add(2 * time.Minute).Format(http.TimeFormat), now.Add(2 * time.Minute)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			header := make(http.Header)
			header.Set("X-RateLimit-Remaining", tt.remaining)
			header.Set("X-RateLimit-Reset", tt.reset)
			header.Set("Retry-After", tt.retryAfter)
			resp := &http.Response{
				StatusCode: http.StatusForbidden,
				Header:     header,
				Body:       io.NopCloser(strings.NewReader("secret body for test-token")),
			}
			err := responseError(resp, "REST", now)
			var limited *RateLimitError
			if errors.As(err, &limited) {
				if tt.want.IsZero() {
					t.Fatalf("unusable headers carried a reset: %s", limited.ResetTime())
				}
				if got := limited.ResetTime(); !got.Equal(tt.want) {
					t.Fatalf("reset time %s, want %s", got, tt.want)
				}
			} else if !tt.want.IsZero() {
				t.Fatalf("error %v carries no reset time", err)
			}
			if strings.Contains(err.Error(), "secret") || strings.Contains(err.Error(), "test-token") {
				t.Fatalf("body or token leaked into error: %q", err)
			}
		})
	}
}

func TestRateLimitResponseError(t *testing.T) {
	const prefix = "GitHub rate limited; retry after "
	const exhausted = "GitHub rate limit exhausted; resets in "
	now := time.Date(2026, time.November, 20, 12, 0, 0, 0, time.UTC)
	later := now.Add(600 * time.Millisecond)
	for _, tt := range []struct {
		name, header, want string
		status             int
		now                time.Time
		remaining, reset   string
	}{
		{"seconds", "120", prefix + "2m0s", 429, now, "412", ""},
		{"future HTTP date", now.Add(2 * time.Minute).Format(http.TimeFormat), prefix + "2m0s", 429, now, "412", ""},
		// Only the reported wait is rounded: the reset keeps 12:00:02 and the
		// 1.4s wait reports as 1s without moving the reset itself.
		{"sub-second HTTP date", now.Add(2 * time.Second).Format(http.TimeFormat), prefix + "1s", 429, later, "412", ""},
		{"past HTTP date", now.Add(-time.Hour).Format(http.TimeFormat), prefix + "0s", 403, now, "412", ""},
		{"absurd HTTP date", "Fri, 01 Jan 9999 00:00:00 GMT", "GitHub REST returned HTTP 429", 429, now, "412", ""},
		{"malformed", "tomorrow", "GitHub REST returned HTTP 429", 429, now, "412", ""},
		{"missing", "", "GitHub REST returned HTTP 403", 403, now, "412", ""},
		// An exhausted budget reports its effective wait: a Retry-After naming
		// a later instant extends the pause, and one naming an earlier
		// instant cannot cut the recorded reset short.
		{"retry-after extends the reset", "120", exhausted + "2m0s", 403, now, "0", strconv.FormatInt(now.Add(30*time.Second).Unix(), 10)},
		{"retry-after cannot shorten the reset", "10", exhausted + "30s", 403, now, "0", strconv.FormatInt(now.Add(30*time.Second).Unix(), 10)},
		{"exhausted without a usable deadline", "", "GitHub rate limit exhausted", 403, now, "0", "tomorrow"},
		{"absurd reset without a usable deadline", "", "GitHub rate limit exhausted", 403, now, "0",
			strconv.FormatInt(time.Date(9999, time.January, 1, 0, 0, 0, 0, time.UTC).Unix(), 10)},
	} {
		t.Run(tt.name, func(t *testing.T) {
			// Set stores each key in the canonical form Header.Get looks up;
			// a raw map literal would spell "Ratelimit" differently and
			// silently never match.
			header := make(http.Header)
			header.Set("Retry-After", tt.header)
			header.Set("X-RateLimit-Remaining", tt.remaining)
			header.Set("X-RateLimit-Reset", tt.reset)
			resp := &http.Response{
				StatusCode: tt.status,
				Header:     header,
				Body:       io.NopCloser(strings.NewReader("secret body for test-token")),
			}
			got := responseError(resp, "REST", tt.now).Error()
			if got != tt.want {
				t.Fatalf("error %q, want %q", got, tt.want)
			}
			if strings.Contains(got, "secret") || strings.Contains(got, "test-token") {
				t.Fatalf("body or token leaked into error: %q", got)
			}
		})
	}
}

// The reset header is a bare Unix second with no alternate form, so its own
// sanity check is what needs pinning: a timestamp beyond the largest
// representable wait is unactionable, exactly like an absurd Retry-After
// date, while past timestamps stay recorded deadlines that are simply due.
func TestRateLimitResetAt(t *testing.T) {
	now := time.Date(2026, time.November, 20, 12, 0, 0, 0, time.UTC)
	for _, tt := range []struct {
		name, header string
		want         time.Time
		wantOK       bool
	}{
		{"unix seconds", strconv.FormatInt(now.Add(2*time.Minute).Unix(), 10), now.Add(2 * time.Minute), true},
		{"past reset", strconv.FormatInt(now.Add(-time.Minute).Unix(), 10), now.Add(-time.Minute), true},
		{"missing", "", time.Time{}, false},
		{"malformed", "tomorrow", time.Time{}, false},
		{"absurd reset", strconv.FormatInt(time.Date(9999, time.January, 1, 0, 0, 0, 0, time.UTC).Unix(), 10), time.Time{}, false},
	} {
		t.Run(tt.name, func(t *testing.T) {
			got, ok := rateLimitResetAt(tt.header, now)
			if ok != tt.wantOK || !got.Equal(tt.want) {
				t.Fatalf("rateLimitResetAt(%q) = %s, %t; want %s, %t", tt.header, got, ok, tt.want, tt.wantOK)
			}
		})
	}
}
