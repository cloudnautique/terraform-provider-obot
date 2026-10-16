// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"fmt"
	"net/http"
)

// AuthProvider holds public identity metadata, never credential configuration.
type AuthProvider struct {
	ID            string `json:"id"`
	Name          string `json:"name"`
	Namespace     string `json:"namespace"`
	GroupIDPrefix string `json:"groupIDPrefix"`
	Configured    bool   `json:"configured"`
	Staged        bool   `json:"staged"`
}

// CurrentAuthProvider resolves the sole configured, non-staged provider.
// The API does not expose an authoritative active-provider field, so ambiguity is an error.
func (c *Client) CurrentAuthProvider(ctx context.Context) (*AuthProvider, error) {
	user, err := c.CurrentUser(ctx)
	if err != nil {
		return nil, err
	}
	if !user.IsAdmin() {
		return nil, fmt.Errorf("current auth provider lookup requires an Obot Admin or Owner token so staged replacement configuration is visible")
	}
	var result struct {
		Items []AuthProvider `json:"items"`
	}
	if err := c.request(ctx, http.MethodGet, "/auth-providers", "", &result); err != nil {
		return nil, err
	}
	var candidates []AuthProvider
	for _, provider := range result.Items {
		if provider.Configured && !provider.Staged {
			candidates = append(candidates, provider)
		}
	}
	if len(candidates) != 1 {
		return nil, fmt.Errorf("expected exactly one configured, non-staged auth provider; found %d. The Obot API does not expose an authoritative active-provider flag; resolve the configuration ambiguity before using this data source", len(candidates))
	}
	if !ValidObjectID(candidates[0].ID) || candidates[0].Name == "" || candidates[0].Namespace == "" {
		return nil, fmt.Errorf("obot returned incomplete auth provider metadata")
	}
	return &candidates[0], nil
}
