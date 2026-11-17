package github

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"net/url"
	"strconv"
)

// ListAll collects a REST array endpoint in page order, requesting 100 items per
// page. It preserves filters, replacing page and per_page. maxPages bounds the
// request count, including the final short (possibly empty) page. Errors return
// no partial result, so an incomplete list cannot masquerade as a snapshot.
// Each page uses ListPage's ETag cache. This assumes a stable remote collection;
// pagination cannot provide an atomic view of concurrent GitHub changes.
func (c *Client) ListAll(ctx context.Context, path string, maxPages int) ([]json.RawMessage, error) {
	if maxPages < 1 {
		return nil, errors.New("GitHub list page limit must be positive")
	}
	u, err := url.Parse(path)
	if err != nil || u.IsAbs() || u.Host != "" || len(path) == 0 || path[0] != '/' || (len(path) > 1 && path[1] == '/') || u.Fragment != "" {
		return nil, errors.New("GitHub REST path must be relative to the API host")
	}
	query, err := url.ParseQuery(u.RawQuery)
	if err != nil {
		return nil, errors.New("invalid GitHub list query")
	}
	items := make([]json.RawMessage, 0)
	for page := 1; page <= maxPages; page++ {
		query.Set("per_page", "100")
		query.Set("page", strconv.Itoa(page))
		u.RawQuery = query.Encode()
		body, err := c.ListPage(ctx, u.String())
		if err != nil {
			return nil, err
		}
		var batch []json.RawMessage
		// Unmarshal accepts null as a nil slice; require an array explicitly.
		body = bytes.TrimSpace(body)
		if len(body) == 0 || body[0] != '[' {
			return nil, errors.New("GitHub list response must be an array")
		}
		if err := json.Unmarshal(body, &batch); err != nil || len(batch) > 100 {
			return nil, errors.New("invalid GitHub list page")
		}
		items = append(items, batch...)
		if len(batch) < 100 {
			return items, nil
		}
	}
	return nil, errors.New("GitHub list exceeds page limit")
}
