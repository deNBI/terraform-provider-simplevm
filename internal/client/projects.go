package client

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// ListProjects returns all projects visible to the API key.
func (c *Client) ListProjects(ctx context.Context) ([]Project, error) {
	return listAll[Project](ctx, c, "projects/")
}

// GetProjectByAPIKey returns the project the API key is scoped to. The API may
// answer with a single object or a one-element list.
// Endpoint assumed from the SimpleVM wiki (not in OpenAPI schema).
func (c *Client) GetProjectByAPIKey(ctx context.Context) (*Project, error) {
	var raw json.RawMessage
	if err := c.do(ctx, http.MethodGet, "projects/by-api-key/", nil, &raw); err != nil {
		return nil, err
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) > 0 && raw[0] == '[' {
		var list []Project
		if err := json.Unmarshal(raw, &list); err != nil {
			return nil, fmt.Errorf("decoding project: %w", err)
		}
		if len(list) == 0 {
			return nil, ErrNotFound
		}
		return &list[0], nil
	}
	var p Project
	if err := json.Unmarshal(raw, &p); err != nil {
		return nil, fmt.Errorf("decoding project: %w", err)
	}
	return &p, nil
}

// GetProject returns the project with the given id, or ErrNotFound.
func (c *Client) GetProject(ctx context.Context, id int64) (*Project, error) {
	projects, err := c.ListProjects(ctx)
	if err != nil {
		return nil, err
	}
	for i := range projects {
		if projects[i].ID == id {
			return &projects[i], nil
		}
	}
	return nil, ErrNotFound
}

// ListFlavors returns the flavors available in a project.
func (c *Client) ListFlavors(ctx context.Context, projectID int64) ([]Flavor, error) {
	return listAll[Flavor](ctx, c, fmt.Sprintf("projects/%d/flavors/", projectID))
}

// ListImages returns the base images available in a project.
func (c *Client) ListImages(ctx context.Context, projectID int64) ([]Image, error) {
	return listAll[Image](ctx, c, fmt.Sprintf("projects/%d/images/", projectID))
}

// ListSnapshotImages returns the snapshot images of a project.
func (c *Client) ListSnapshotImages(ctx context.Context, projectID int64) ([]Image, error) {
	return listAll[Image](ctx, c, fmt.Sprintf("projects/%d/snapshots/images/", projectID))
}
