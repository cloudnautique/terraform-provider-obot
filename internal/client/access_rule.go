// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
)

// RuleReference identifies a subject or resource in an MCP access rule.
type RuleReference struct {
	Type string `json:"type"`
	ID   string `json:"id"`
}

// AccessRuleManifest is the independently managed MCP access policy definition.
type AccessRuleManifest struct {
	DisplayName string          `json:"displayName"`
	Subjects    []RuleReference `json:"subjects"`
	Resources   []RuleReference `json:"resources"`
}

// AccessRule is catalog scoped, separate from vMCP profiles.
type AccessRule struct {
	ID        string `json:"id"`
	CatalogID string `json:"mcpCatalogID"`
	Generated bool   `json:"generated"`
	AccessRuleManifest
}

func accessRulePath(catalogID, id string) (string, error) {
	path, err := catalogPath(catalogID)
	if err != nil {
		return "", err
	}
	path += "/access-control-rules"
	if id != "" {
		if !ValidObjectID(id) {
			return "", fmt.Errorf("invalid MCP access rule ID")
		}
		path += "/" + id
	}
	return path, nil
}

// ListAccessRules returns zero or more catalog policies.
func (c *Client) ListAccessRules(ctx context.Context, catalogID string) ([]AccessRule, error) {
	path, err := accessRulePath(catalogID, "")
	if err != nil {
		return nil, err
	}
	var result struct {
		Items []AccessRule `json:"items"`
	}
	if err := c.request(ctx, http.MethodGet, path, "", &result); err != nil {
		return nil, err
	}
	for _, rule := range result.Items {
		if !ValidObjectID(rule.ID) || rule.CatalogID != catalogID {
			return nil, fmt.Errorf("obot returned an invalid or incorrectly scoped MCP access rule")
		}
	}
	return result.Items, nil
}

// GetAccessRule reads one policy without changing its subjects or selectors.
func (c *Client) GetAccessRule(ctx context.Context, catalogID, id string) (*AccessRule, error) {
	path, err := accessRulePath(catalogID, id)
	if err != nil {
		return nil, err
	}
	var result AccessRule
	if err := c.request(ctx, http.MethodGet, path, "", &result); err != nil {
		return nil, err
	}
	if result.ID != id || result.CatalogID != catalogID {
		return nil, fmt.Errorf("obot returned an invalid or incorrectly scoped MCP access rule")
	}
	return &result, nil
}

func (c *Client) writeAccessRule(ctx context.Context, catalogID, id string, manifest AccessRuleManifest) (*AccessRule, error) {
	path, err := accessRulePath(catalogID, id)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(manifest)
	if err != nil {
		return nil, err
	}
	method := http.MethodPut
	if id == "" {
		method = http.MethodPost
	}
	var result AccessRule
	if err := c.request(ctx, method, path, string(body), &result); err != nil {
		return nil, err
	}
	if !ValidObjectID(result.ID) || result.CatalogID != catalogID || (id != "" && result.ID != id) {
		return nil, fmt.Errorf("obot returned an invalid or incorrectly scoped MCP access rule")
	}
	return &result, nil
}

// CreateAccessRule creates a new policy with a server-generated identifier.
func (c *Client) CreateAccessRule(ctx context.Context, catalogID string, manifest AccessRuleManifest) (*AccessRule, error) {
	return c.writeAccessRule(ctx, catalogID, "", manifest)
}

// UpdateAccessRule replaces the definition of one existing policy.
func (c *Client) UpdateAccessRule(ctx context.Context, catalogID, id string, manifest AccessRuleManifest) (*AccessRule, error) {
	return c.writeAccessRule(ctx, catalogID, id, manifest)
}

// DeleteAccessRule removes only the addressed policy, tolerating prior deletion.
func (c *Client) DeleteAccessRule(ctx context.Context, catalogID, id string) error {
	path, err := accessRulePath(catalogID, id)
	if err != nil {
		return err
	}
	err = c.request(ctx, http.MethodDelete, path, "", nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}
