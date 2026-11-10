package client

import (
	"context"
	"net/http"
	"net/url"
)

// ListVolumes returns all volumes visible to the API key.
func (c *Client) ListVolumes(ctx context.Context) ([]Volume, error) {
	return listAll[Volume](ctx, c, "volumes/by-api-key/")
}

// GetVolume returns the volume with the given uuid, or ErrNotFound when it
// does not exist or has status deleted.
func (c *Client) GetVolume(ctx context.Context, uuid string) (*Volume, error) {
	var v Volume
	if err := c.do(ctx, http.MethodGet, "volumes/"+url.PathEscape(uuid)+"/", nil, &v); err != nil {
		return nil, err
	}
	if v.Status == VolumeStatusDeleted {
		return nil, ErrNotFound
	}
	return &v, nil
}

// CreateVolume starts creation of a volume.
func (c *Client) CreateVolume(ctx context.Context, req CreateVolumeRequest) (*VolumeActionResponse, error) {
	var out VolumeActionResponse
	if err := c.do(ctx, http.MethodPost, "volumes/", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteVolume deletes a volume. Errors, including 404, are returned as is.
func (c *Client) DeleteVolume(ctx context.Context, uuid string) error {
	return c.do(ctx, http.MethodDelete, "volumes/"+url.PathEscape(uuid)+"/", nil, nil)
}

// VolumeAction runs an action (attach, detach, rename, extend...) on a volume.
func (c *Client) VolumeAction(ctx context.Context, uuid string, req VolumeActionRequest) (*Volume, error) {
	var out Volume
	if err := c.do(ctx, http.MethodPost, "volumes/"+url.PathEscape(uuid)+"/action/", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
