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
// helper. Helper output is never included in errors or persisted.
func ResolveToken(ctx context.Context) (string, error) {
	return resolveToken(ctx, os.Getenv("GITHUB_TOKEN"), func(ctx context.Context) ([]byte, error) {
		return exec.CommandContext(ctx, "gh", "auth", "token", "--hostname", "github.com").Output()
	})
}

func resolveToken(ctx context.Context, env string, helper func(context.Context) ([]byte, error)) (string, error) {
	if token := strings.TrimSpace(env); token != "" {
		return token, nil
	}
	ctx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	data, err := helper(ctx)
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
	if u, err := url.Parse(base); err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
		return nil, errors.New("PERCOLATE_GITHUB_URL must be an absolute http(s) URL")
	}
	return &Client{token: token, http: &http.Client{Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		baseURL: base, pages: make(map[string]cachedPage)}, nil
}

// Requests counts every HTTP request issued so far, REST and GraphQL
// combined. Callers use it to hold a read inside its request budget.
func (c *Client) Requests() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.requests
}

// responseError turns a non-200 status into one safe line. Rate-limit
// responses report when to come back; response bodies never appear.
func responseError(resp *http.Response, kind string) error {
	if resp.StatusCode == http.StatusForbidden || resp.StatusCode == http.StatusTooManyRequests {
		if resp.Header.Get("X-RateLimit-Remaining") == "0" {
			if reset, err := strconv.ParseInt(resp.Header.Get("X-RateLimit-Reset"), 10, 64); err == nil {
				wait := time.Until(time.Unix(reset, 0)).Round(time.Second)
				if wait < 0 {
					wait = 0
				}
				return fmt.Errorf("GitHub rate limit exhausted; resets in %s", wait)
			}
			return errors.New("GitHub rate limit exhausted")
		}
		if retry, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && retry > 0 {
			return fmt.Errorf("GitHub rate limited; retry after %s", (time.Duration(retry) * time.Second).Round(time.Second))
		}
	}
	return fmt.Errorf("GitHub %s returned HTTP %d", kind, resp.StatusCode)
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
		return nil, responseError(resp, "REST")
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
