package client

import (
	"context"
	"net/http"
)

// CreateSnapshot creates a snapshot of a VM.
func (c *Client) CreateSnapshot(ctx context.Context, req CreateSnapshotRequest) (*Snapshot, error) {
	var out Snapshot
	if err := c.do(ctx, http.MethodPost, "snapshots/", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
