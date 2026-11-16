// Package github contains the network boundary for future snapshot adapters.
// It does not fetch data until explicitly called, and never draws a scene.
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

// Client caches individual REST list pages in memory. It is safe for concurrent
// calls; callers own pagination and snapshot assembly. Credentials and cached
// responses are never written to disk.
type Client struct {
	token   string
	http    *http.Client
	baseURL string
	mu      sync.Mutex
	pages   map[string]cachedPage
}

func NewClient(token string) (*Client, error) {
	token = strings.TrimSpace(token)
	if token == "" {
		return nil, ErrNoToken
	}
	return &Client{token: token, http: &http.Client{Timeout: 30 * time.Second,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }},
		baseURL: "https://api.github.com", pages: make(map[string]cachedPage)}, nil
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
	if resp.StatusCode == http.StatusNotModified {
		if cached.etag == "" {
			return nil, errors.New("GitHub returned 304 without a cached page")
		}
		return append(json.RawMessage(nil), cached.body...), nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("GitHub REST returned HTTP %d", resp.StatusCode)
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
