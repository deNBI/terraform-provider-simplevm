package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
)

// decodeList accepts either a bare JSON array or a DRF page object
// ({"count":..,"next":..,"results":[..]}).
func decodeList[T any](data []byte) (items []T, next string, err error) {
	trimmed := strings.TrimSpace(string(data))
	if strings.HasPrefix(trimmed, "[") {
		if err := json.Unmarshal(data, &items); err != nil {
			return nil, "", fmt.Errorf("decoding list: %w", err)
		}
		return items, "", nil
	}
	var page struct {
		Next    *string `json:"next"`
		Results []T     `json:"results"`
	}
	if err := json.Unmarshal(data, &page); err != nil {
		return nil, "", fmt.Errorf("decoding list: %w", err)
	}
	if page.Next != nil {
		next = *page.Next
	}
	return page.Results, next, nil
}

// listAll fetches every item of a list endpoint, following DRF "next" links.
func listAll[T any](ctx context.Context, c *Client, path string) ([]T, error) {
	sep := "?"
	if strings.Contains(path, "?") {
		sep = "&"
	}
	target := c.baseURL.String() + path + sep + "page_size=100"
	var all []T
	seen := map[string]bool{}
	for pages := 0; target != ""; pages++ {
		if pages >= maxPages {
			return nil, fmt.Errorf("pagination of %s exceeded %d pages", path, maxPages)
		}
		if seen[target] {
			return nil, fmt.Errorf("pagination of %s loops: %q repeated", path, target)
		}
		seen[target] = true
		var raw json.RawMessage
		if err := c.doURL(ctx, http.MethodGet, target, nil, &raw); err != nil {
			return nil, err
		}
		items, next, err := decodeList[T](raw)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		if next == "" {
			break
		}
		if target, err = c.safeNext(next); err != nil {
			return nil, err
		}
	}
	return all, nil
}

const maxPages = 1000

// safeNext rebuilds a server-supplied "next" URL on the client's own scheme
// and host, so a hostile or misconfigured value cannot redirect the API key
// elsewhere or downgrade to http.
func (c *Client) safeNext(next string) (string, error) {
	u, err := url.Parse(next)
	if err != nil {
		return "", fmt.Errorf("invalid pagination URL %q: %w", next, err)
	}
	if u.Host != "" && u.Host != c.baseURL.Host {
		return "", fmt.Errorf("pagination URL %q points to a different host than the endpoint %q", next, c.baseURL.Host)
	}
	if !strings.HasPrefix(u.Path, c.baseURL.Path) {
		return "", fmt.Errorf("pagination URL %q is outside the endpoint path %q", next, c.baseURL.Path)
	}
	out := url.URL{Scheme: c.baseURL.Scheme, Host: c.baseURL.Host, Path: u.Path, RawPath: u.RawPath, RawQuery: u.RawQuery}
	return out.String(), nil
}
