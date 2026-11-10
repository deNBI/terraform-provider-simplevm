package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
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
	for target != "" {
		var raw json.RawMessage
		if err := c.doURL(ctx, http.MethodGet, target, nil, &raw); err != nil {
			return nil, err
		}
		items, next, err := decodeList[T](raw)
		if err != nil {
			return nil, err
		}
		all = append(all, items...)
		target = next
	}
	return all, nil
}
