package github

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestGraphQLRequest(t *testing.T) {
	var auth, contentType string
	var body []byte
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.URL.Path != "/graphql" {
			t.Errorf("unexpected %s %s", r.Method, r.URL.Path)
		}
		auth = r.Header.Get("Authorization")
		contentType = r.Header.Get("Content-Type")
		body, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"data":{"repository":{"p0":{"number":7}}}}`))
	}))
	defer server.Close()
	c, _ := NewClient("test-token")
	c.baseURL = server.URL
	data, err := c.GraphQL(context.Background(), "query($owner:String!){repository(owner:$owner){p0:pullRequest(number:7){number}}}",
		map[string]any{"owner": "acme"})
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != `{"repository":{"p0":{"number":7}}}` {
		t.Fatalf("data %s", data)
	}
	if auth != "Bearer test-token" || !strings.Contains(contentType, "application/json") {
		t.Fatal("missing headers")
	}
	if !strings.Contains(string(body), "pullRequest(number:7)") || !strings.Contains(string(body), `"owner":"acme"`) {
		t.Fatalf("lost query or variables: %s", body)
	}
	if c.Requests() != 1 {
		t.Fatalf("requests=%d", c.Requests())
	}
}

func TestGraphQLErrors(t *testing.T) {
	for _, tt := range []struct {
		name     string
		status   int
		headers  map[string]string
		body     string
		contains string
		absent   string
	}{
		{"server error message", 200, nil, `{"errors":[{"message":"bad\nmulti"}]}`, "bad multi", "\n"},
		{"unauthorized", 401, nil, "secret", "HTTP 401", "secret"},
		{"invalid json", 200, nil, "{oops", "invalid JSON", "oops"},
		{"no data", 200, nil, `{}`, "no data", "never present"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				for k, v := range tt.headers {
					w.Header().Set(k, v)
				}
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer server.Close()
			c, _ := NewClient("test-token")
			c.baseURL = server.URL
			_, err := c.GraphQL(context.Background(), "query{}", nil)
			if err == nil || !strings.Contains(err.Error(), tt.contains) || strings.Contains(err.Error(), tt.absent) {
				t.Fatalf("error %v", err)
			}
			if strings.Count(strings.TrimSuffix(err.Error(), "\n"), "\n") != 0 {
				t.Fatalf("error not one line: %q", err.Error())
			}
		})
	}
}

func TestRateLimitErrors(t *testing.T) {
	for _, tt := range []struct {
		name, remaining, reset, retryAfter string
		contains                           string
	}{
		{"core limit", "0", "9999999999", "", "rate limit exhausted"},
		{"retry-after", "5", "", "30", "retry after 30s"},
		{"plain forbidden", "", "", "", "HTTP 403"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.Header().Set("X-RateLimit-Remaining", tt.remaining)
				w.Header().Set("X-RateLimit-Reset", tt.reset)
				w.Header().Set("Retry-After", tt.retryAfter)
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte("secret body"))
			}))
			defer server.Close()
			c, _ := NewClient("test-token")
			c.baseURL = server.URL
			_, err := c.ListPage(context.Background(), "/list")
			if err == nil || !strings.Contains(err.Error(), tt.contains) || strings.Contains(err.Error(), "secret") {
				t.Fatalf("error %v", err)
			}
		})
	}
}

func TestOneLine(t *testing.T) {
	got := oneLine("a\nb\tc\r d \n")
	if got != "a b c  d" {
		t.Fatalf("oneLine %q", got)
	}
	long := strings.Repeat("x", 300)
	if len([]rune(oneLine(long))) != 200 {
		t.Fatal("did not truncate")
	}
	if oneLine(" \n ") != "unknown error" {
		t.Fatal("empty message not replaced")
	}
}
