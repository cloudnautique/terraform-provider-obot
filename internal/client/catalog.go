// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"time"
)

var objectIDPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,251}[a-z0-9])?$`)

// ValidObjectID accepts an Obot object name without URL path delimiters.
func ValidObjectID(id string) bool { return objectIDPattern.MatchString(id) }

// Catalog contains public configuration only; inline legacy credentials are deliberately ignored.
type Catalog struct {
	ID               string            `json:"id"`
	DisplayName      string            `json:"displayName"`
	Sources          []string          `json:"sourceURLs"`
	GitCredentialIDs map[string]string `json:"sourceURLGitCredentialIDs,omitempty"`
	LastSynced       string            `json:"lastSynced"`
	IsSyncing        bool              `json:"isSyncing"`
	SyncErrors       map[string]string `json:"syncErrors"`
}

// CatalogSources is the complete source configuration, with no inline secret values.
type CatalogSources struct {
	Sources          []string          `json:"sourceURLs"`
	GitCredentialIDs map[string]string `json:"sourceURLGitCredentialIDs"`
}

// CatalogEntry is a read-only inventory item, whose definition remains owned by its source.
type CatalogEntry struct {
	ID        string `json:"id"`
	SourceURL string `json:"sourceURL"`
	Manifest  struct {
		Name string `json:"name"`
	} `json:"manifest"`
}

func catalogPath(id string) (string, error) {
	if !ValidObjectID(id) {
		return "", fmt.Errorf("invalid catalog ID")
	}
	return "/mcp-catalogs/" + id, nil
}

// GetCatalog reads catalog configuration and synchronization status.
func (c *Client) GetCatalog(ctx context.Context, id string) (*Catalog, error) {
	path, err := catalogPath(id)
	if err != nil {
		return nil, err
	}
	var result Catalog
	if err := c.request(ctx, http.MethodGet, path, "", &result); err != nil {
		return nil, err
	}
	if result.ID != id {
		return nil, fmt.Errorf("obot returned an unexpected catalog ID")
	}
	return &result, nil
}

// UpdateCatalogSources replaces the default catalog's complete source collection.
func (c *Client) UpdateCatalogSources(ctx context.Context, id string, sources CatalogSources) (*Catalog, error) {
	if id != "default" {
		return nil, fmt.Errorf("only the default MCP catalog supports source management")
	}
	if sources.Sources == nil {
		sources.Sources = []string{}
	}
	if sources.GitCredentialIDs == nil {
		sources.GitCredentialIDs = map[string]string{}
	}
	body, err := json.Marshal(sources)
	if err != nil {
		return nil, err
	}
	var result Catalog
	if err := c.request(ctx, http.MethodPut, "/mcp-catalogs/default", string(body), &result); err != nil {
		return nil, err
	}
	if result.ID != id {
		return nil, fmt.Errorf("obot returned an unexpected catalog ID")
	}
	return &result, nil
}

// ListCatalogEntries returns the inventory, without server configuration or secrets.
func (c *Client) ListCatalogEntries(ctx context.Context, id string) ([]CatalogEntry, error) {
	path, err := catalogPath(id)
	if err != nil {
		return nil, err
	}
	var result struct {
		Items []CatalogEntry `json:"items"`
	}
	if err := c.request(ctx, http.MethodGet, path+"/entries?minimal=true&all=true", "", &result); err != nil {
		return nil, err
	}
	for _, entry := range result.Items {
		if !ValidObjectID(entry.ID) {
			return nil, fmt.Errorf("obot returned an invalid catalog entry ID")
		}
	}
	return result.Items, nil
}

// WaitCatalogIdle avoids changing sources during an existing reconciliation.
func (c *Client) WaitCatalogIdle(ctx context.Context, id string) (*Catalog, error) {
	for {
		result, err := c.GetCatalog(ctx, id)
		if err != nil {
			return nil, err
		}
		if !result.IsSyncing {
			return result, nil
		}
		if err := waitCatalogPoll(ctx); err != nil {
			return nil, err
		}
	}
}

// RefreshCatalog waits for a new, completed reconciliation, never accepting stale idle status.
// Obot timestamps have second precision, so the trigger must be issued after the prior timestamp.
func (c *Client) RefreshCatalog(ctx context.Context, id, previous string) (*Catalog, error) {
	path, err := catalogPath(id)
	if err != nil {
		return nil, err
	}
	var before time.Time
	if previous != "" {
		before, err = time.Parse(time.RFC3339Nano, previous)
		if err != nil {
			return nil, fmt.Errorf("obot returned an invalid catalog sync timestamp")
		}
		delay := time.Until(before.Truncate(time.Second).Add(time.Second))
		if delay > 0 {
			timer := time.NewTimer(delay)
			defer timer.Stop()
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-timer.C:
			}
		}
	}
	if err := c.request(ctx, http.MethodPost, path+"/refresh", "{}", nil); err != nil {
		return nil, err
	}
	for {
		result, err := c.GetCatalog(ctx, id)
		if err != nil {
			return nil, err
		}
		completed, parseErr := time.Parse(time.RFC3339Nano, result.LastSynced)
		if !result.IsSyncing && parseErr == nil && completed.After(before) {
			if len(result.SyncErrors) > 0 {
				return result, fmt.Errorf("catalog synchronization failed for %d source(s); inspect the catalog's sync_errors or the Obot UI, correct the sources or credentials, and apply again", len(result.SyncErrors))
			}
			return result, nil
		}
		if err := waitCatalogPoll(ctx); err != nil {
			return nil, fmt.Errorf("waiting for a new catalog synchronization: %w", err)
		}
	}
}

func waitCatalogPoll(ctx context.Context) error {
	timer := time.NewTimer(500 * time.Millisecond)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
