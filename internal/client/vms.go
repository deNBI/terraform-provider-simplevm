package client

import (
	"context"
	"net/http"
	"net/url"
)

// ListVMs returns all VMs visible to the API key. It uses vms/by-api-key/
// because the general vms/ list answers HTTP 403 for API key users.
func (c *Client) ListVMs(ctx context.Context) ([]VM, error) {
	return listAll[VM](ctx, c, "vms/by-api-key/")
}

// GetVM returns the VM with the given uuid. ErrNotFound is returned when it
// does not exist (HTTP 404) or is in the deleted state.
func (c *Client) GetVM(ctx context.Context, uuid string) (*VM, error) {
	var vm VM
	if err := c.do(ctx, http.MethodGet, "vms/"+url.PathEscape(uuid)+"/", nil, &vm); err != nil {
		return nil, err
	}
	if vm.VMState == VMStateDeleted {
		return nil, ErrNotFound
	}
	return &vm, nil
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
