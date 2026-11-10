package client

import (
	"context"
	"net/http"
	"net/url"
)

// ListVMs returns all VMs visible to the API key.
func (c *Client) ListVMs(ctx context.Context) ([]VM, error) {
	return listAll[VM](ctx, c, "vms/")
}

// GetVM returns the VM with the given uuid. ErrNotFound is returned when it is
// not listed or is in the deleted state.
func (c *Client) GetVM(ctx context.Context, uuid string) (*VM, error) {
	vms, err := c.ListVMs(ctx)
	if err != nil {
		return nil, err
	}
	for i := range vms {
		if vms[i].UUID == uuid {
			if vms[i].VMState == "deleted" {
				return nil, ErrNotFound
			}
			return &vms[i], nil
		}
	}
	return nil, ErrNotFound
}

// CreateVM starts creation of a VM.
func (c *Client) CreateVM(ctx context.Context, req CreateVMRequest) (*VMActionResponse, error) {
	var out VMActionResponse
	if err := c.do(ctx, http.MethodPost, "vms/", req, &out); err != nil {
		return nil, err
	}
	return &out, nil
}

// DeleteVM deletes a VM. Errors, including 404, are returned as is.
func (c *Client) DeleteVM(ctx context.Context, uuid string) error {
	return c.do(ctx, http.MethodDelete, "vms/"+url.PathEscape(uuid)+"/", nil, nil)
}

// VMAction runs an action (see VMActionStop, VMActionResume) on a VM.
func (c *Client) VMAction(ctx context.Context, uuid, action string) (*VMActionResponse, error) {
	var out VMActionResponse
	body := map[string]string{"os_action": action}
	if err := c.do(ctx, http.MethodPost, "vms/"+url.PathEscape(uuid)+"/action/", body, &out); err != nil {
		return nil, err
	}
	return &out, nil
}
