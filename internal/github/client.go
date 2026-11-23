// Package github is Percolate's only network boundary. It fetches a
// repository on explicit call and assembles repo.Snapshot from REST lists
// plus batched GraphQL pull-request details; it never draws a scene.
package github

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

var ErrNoToken = errors.New("GitHub token required: set GITHUB_TOKEN or run gh auth login")

// ResolveToken prefers GITHUB_TOKEN, falling back to the local gh credential
// helper for the host named by PERCOLATE_GITHUB_URL (github.com by default).
// Helper output and errors are never included in errors or persisted.
func ResolveToken(ctx context.Context) (string, error) {
	return resolveToken(ctx, os.Getenv("GITHUB_TOKEN"), os.Getenv("PERCOLATE_GITHUB_URL"),
		func(ctx context.Context, hostname string) ([]byte, error) {
			return exec.CommandContext(ctx, "gh", "auth", "token", "--hostname", hostname).Output()
		})
}

// tokenHostname maps the configured API base URL to the hostname gh stores
// credentials under. The default api.github.com, including an explicit :443,
// maps back to github.com so the usual credential keeps working. Hostnames are
// case-insensitive, so the result is lowercased; any other host keeps its port
// but loses path, query and userinfo.
func tokenHostname(base string) string {
	u, err := url.Parse(strings.TrimSpace(base))
	if err != nil || u.Host == "" || u.User != nil {
		return "github.com"
	}
	host := strings.ToLower(u.Hostname())
	if host == "api.github.com" && (u.Port() == "" || u.Port() == "443") {
		return "github.com"
	}
	return strings.ToLower(u.Host)
}

func resolveToken(ctx context.Context, env, base string, helper func(context.Context, string) ([]byte, error)) (string, error) {
	if token := strings.TrimSpace(env); token != "" {
		return token, nil
	}
	hostname := tokenHostname(base)
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	data, err := helper(ctx, hostname)
	if err == nil {
		if token := strings.TrimSpace(string(data)); token != "" {
			return token, nil
		}
	}
	return "", ErrNoToken
}

type cachedPage struct {
	etag string
	body []byte
}

// Client caches individual REST list pages in memory. It is safe for
// concurrent calls; ListAll provides bounded pagination and BuildSnapshot
// owns assembly. Credentials and cached responses are never written to disk.
type Client struct {
	token    string
	http     *http.Client
	baseURL  string
	mu       sync.Mutex
	pages    map[string]cachedPage
	requests int
}

func NewClient(token string) (*Client, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrNoToken
	}
	base := strings.TrimRight(os.Getenv("PERCOLATE_GITHUB_URL"), "/")
	if base == "" {
		base = "https://api.github.com"
	}
	if !allowedBaseURL(base) {
		return nil, errors.New("PERCOLATE_GITHUB_URL must be https, or http on loopback for local testing")
	}
	return &Client{token: token, http: &http.Client{Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		baseURL: base, pages: make(map[string]cachedPage)}, nil
}

// allowedBaseURL accepts https everywhere and plain http only on loopback.
// Every request carries a bearer token, so a mis-set http override must not
// send it in cleartext; loopback http exists for the local httptest servers.
func allowedBaseURL(base string) bool {
	u, err := url.Parse(base)
	if err != nil || u.Host == "" || u.User != nil {
		return false
	}
	if u.Scheme == "https" {
		return true
	}
	if u.Scheme != "http" {
		return false
	}
	if strings.EqualFold(u.Hostname(), "localhost") {
		return true
	}
	ip := net.ParseIP(u.Hostname())
	return ip != nil && ip.IsLoopback()
}

// Requests counts every HTTP request issued so far, REST and GraphQL
// combined. Callers use it to hold a read inside its request budget.
func (c *Client) Requests() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests
}

// RateLimitError is a refused read whose response recorded when GitHub will
// admit the next read. ResetAt is the latest deadline its rate headers name,
// in UTC, never a guess and never response text; message is the one safe line
// built by responseError. Refresh schedulers use ResetTime to wait for that
// recorded deadline, and the CLI uses it to say that a read is paused rather
// than ordinarily retrying.
type RateLimitError struct {
	ResetAt time.Time
	message string
}

func (e *RateLimitError) Error() string { return e.message }

// ResetTime reports the recorded instant a retry may resume.
func (e *RateLimitError) ResetTime() time.Time { return e.ResetAt }

// responseError turns a non-200 status into one safe line. Rate-limit
// responses report when to come back; response bodies never appear. The caller
// supplies now so tests can hold the clock still without waiting on it.
func responseError(resp *http.Response, kind string, now time.Time) error {
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			// The primary read budget is exhausted. Its recorded reset is the
			// deadline GitHub stands behind, and a Retry-After naming a later
			// instant extends it: scheduling keeps the latest valid deadline,
			// so a shorter reset never fires early and a missing or malformed
			// one cannot discard a usable Retry-After.
			deadline, ok := rateLimitResetAt(resp.Header.Get("X-RateLimit-Reset"), now)
			if after, hasAfter := retryAfterAt(resp.Header.Get("Retry-After"), now); hasAfter && (!ok || after.After(deadline)) {
				deadline, ok = after, true
			}
			if ok {
				wait := deadline.Sub(now).Round(time.Second)
				if wait < 0 {
					wait = 0
				}
				return &RateLimitError{ResetAt: deadline.UTC(),
					message: fmt.Sprintf("GitHub rate limit exhausted; resets in %s", wait)}
			}
			return errors.New("GitHub rate limit exhausted")
		}
		if reset, ok := retryAfterAt(resp.Header.Get("Retry-After"), now); ok {
			// Scheduling waits for the recorded instant, which retryAfterAt
			// floors at now; only the reported wait is rounded, so a reset
			// inside the current second still holds to the recorded moment.
			return &RateLimitError{ResetAt: reset.UTC(),
				message: fmt.Sprintf("GitHub rate limited; retry after %s", reset.Sub(now).Round(time.Second))}
		}
	}
	return fmt.Errorf("GitHub %s returned HTTP %d", kind, resp.StatusCode)
}

// maxWaitSeconds is the largest whole number of seconds a time.Duration can
// hold. GitHub never records a reset that far ahead; a deadline past it would
// saturate time.Sub and schedule a pause of roughly 292 years, so it is as
// unactionable as an absurd Retry-After date.
const maxWaitSeconds = int64(1<<63-1) / int64(time.Second)

// rateLimitResetAt parses X-RateLimit-Reset, the Unix second GitHub records
// for the primary budget's refill. A past instant is still a recorded
// deadline, merely one already due; a timestamp so far ahead that time.Sub
// would saturate is not, by the same sanity check the HTTP-date Retry-After
// path applies.
func rateLimitResetAt(header string, now time.Time) (time.Time, bool) {
	seconds, err := strconv.ParseInt(strings.TrimSpace(header), 10, 64)
	if err != nil {
		return time.Time{}, false
	}
	resetAt := time.Unix(seconds, 0).UTC()
	if resetAt.Sub(now) > time.Duration(maxWaitSeconds)*time.Second {
		return time.Time{}, false
	}
	return resetAt, true
}

// retryAfterAt parses a Retry-After header in either form GitHub sends:
// delta-seconds or an HTTP date (used by secondary rate limits). It returns
// the instant a retry may resume: the parsed date itself, never a rounded
// wait, so a date whose second boundary falls inside the current one is
// honored to the recorded moment. An instant already past is floored at now,
// so it never yields a negative wait. Missing, malformed or absurd values
// are not actionable.
func retryAfterAt(header string, now time.Time) (time.Time, bool) {
	header = strings.TrimSpace(header)
	if header == "" {
		return time.Time{}, false
	}
	if seconds, err := strconv.ParseInt(header, 10, 64); err == nil {
		if seconds < 0 || seconds > maxWaitSeconds {
			return time.Time{}, false
		}
		return now.Add(time.Duration(seconds) * time.Second), true
	}
	date, err := http.ParseTime(header)
	if err != nil {
		return time.Time{}, false
	}
	if date.Before(now) {
		return now, true
	}
	// time.Sub saturates at the largest duration, so an HTTP date beyond
	// ~292 years would otherwise schedule a nonsense instant instead of the
	// bare HTTP error used for every other unactionable value.
	if date.Sub(now) > time.Duration(maxWaitSeconds)*time.Second {
		return time.Time{}, false
	}
	return date, true
}

// ListPage returns JSON from a relative REST path, including its query. Repeat
// requests send If-None-Match and reuse the body on 304. Returned bytes belong
// to the caller. Only complete, valid JSON responses enter the cache.
func (c *Client) ListPage(ctx context.Context, path string) (json.RawMessage, error) {
	u, err := url.Parse(path)
	if err != nil || u.IsAbs() || u.Host != "" || !strings.HasPrefix(path, "/") || strings.HasPrefix(path, "//") || u.Fragment != "" {
		return nil, errors.New("GitHub REST path must be relative to the API host")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+path, nil)
	if err != nil {
		return nil, errors.New("invalid GitHub REST request")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("User-Agent", "percolate")
	cached := c.pages[path]
	if cached.etag != "" {
		req.Header.Set("If-None-Match", cached.etag)
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("GitHub REST request failed")
	}
	defer resp.Body.Close()
	c.requests++
	if resp.StatusCode == http.StatusNotModified {
		if cached.etag == "" {
			return nil, errors.New("GitHub returned 304 without a cached page")
		}
		return append(json.RawMessage(nil), cached.body...), nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, responseError(resp, "REST", time.Now())
	}
	const limit = 16 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, errors.New("cannot read GitHub REST response")
	}
	if len(body) > limit {
		return nil, errors.New("GitHub REST response exceeds 16 MiB")
	}
	if !json.Valid(body) {
		return nil, errors.New("GitHub REST returned invalid JSON")
	}
	c.pages[path] = cachedPage{etag: resp.Header.Get("ETag"), body: append([]byte(nil), body...)}
	return body, nil
}
