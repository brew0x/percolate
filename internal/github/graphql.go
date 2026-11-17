package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
)

type graphqlError struct {
	Message string `json:"message"`
}

type graphqlResult struct {
	Data   json.RawMessage `json:"data"`
	Errors []graphqlError  `json:"errors"`
}

// GraphQL runs one query with variables against /graphql and returns the data
// member only. Each call counts toward Requests(); responses are not cached.
// Any server-side error rejects the whole call, so no partial result can be
// mistaken for complete data, and server text is flattened to one safe line.
func (c *Client) GraphQL(ctx context.Context, query string, variables map[string]any) (json.RawMessage, error) {
	payload, err := json.Marshal(map[string]any{"query": query, "variables": variables})
	if err != nil {
		return nil, errors.New("invalid GitHub GraphQL request")
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL+"/graphql", bytes.NewReader(payload))
	if err != nil {
		return nil, errors.New("invalid GitHub GraphQL request")
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "percolate")
	resp, err := c.http.Do(req)
	if err != nil {
		return nil, errors.New("GitHub GraphQL request failed")
	}
	defer resp.Body.Close()
	c.requests++
	if resp.StatusCode != http.StatusOK {
		return nil, responseError(resp, "GraphQL")
	}
	const limit = 16 << 20
	body, err := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	if err != nil {
		return nil, errors.New("cannot read GitHub GraphQL response")
	}
	if len(body) > limit {
		return nil, errors.New("GitHub GraphQL response exceeds 16 MiB")
	}
	var result graphqlResult
	if err := json.Unmarshal(body, &result); err != nil {
		return nil, errors.New("GitHub GraphQL returned invalid JSON")
	}
	if len(result.Errors) > 0 {
		return nil, fmt.Errorf("GitHub GraphQL error: %s", oneLine(result.Errors[0].Message))
	}
	if len(result.Data) == 0 {
		return nil, errors.New("GitHub GraphQL returned no data")
	}
	return result.Data, nil
}

// oneLine flattens server text into a short single-line message.
func oneLine(s string) string {
	s = strings.Map(func(r rune) rune {
		if r == '\n' || r == '\r' || r == '\t' {
			return ' '
		}
		return r
	}, strings.TrimSpace(s))
	if runes := []rune(s); len(runes) > 200 {
		s = string(runes[:200])
	}
	if s == "" {
		return "unknown error"
	}
	return s
}
