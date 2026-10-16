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

// GitHostPattern matches a host as Obot stores it: lowercase, with an optional
// port, and without a scheme, path, or user information.
var GitHostPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9.-]*[a-z0-9])?(?::[1-9][0-9]{0,4})?$`)

// GitCredentialUse names an object that reads a source with the credential.
type GitCredentialUse struct {
	ID          string `json:"id"`
	DisplayName string `json:"displayName"`
}

// GitCredentialUses lists the objects that reference a credential, by kind.
type GitCredentialUses struct {
	SkillRepositories []GitCredentialUse `json:"skillRepositories"`
	MCPCatalogs       []GitCredentialUse `json:"mcpCatalogs"`
	SystemMCPCatalogs []GitCredentialUse `json:"systemMcpCatalogs"`
}

// GitCredential is a shared credential for HTTPS Git sources. Obot never
// returns its token.
type GitCredential struct {
	ID              string            `json:"id"`
	DisplayName     string            `json:"displayName"`
	Host            string            `json:"host"`
	TokenConfigured bool              `json:"tokenConfigured"`
	Uses            GitCredentialUses `json:"uses"`
}

// GitCredentialManifest is the writable definition. An empty Token on update
// keeps the stored token.
type GitCredentialManifest struct {
	DisplayName string `json:"displayName"`
	Host        string `json:"host"`
	Token       string `json:"token,omitempty"`
}

// String prevents a token appearing when a manifest is formatted in diagnostics.
func (m GitCredentialManifest) String() string {
	return fmt.Sprintf("Git credential manifest (%s, %s, token=[REDACTED])", m.DisplayName, m.Host)
}

// GoString prevents a token appearing in %#v formatting.
func (m GitCredentialManifest) GoString() string { return m.String() }

func gitCredentialPath(id string) (string, error) {
	if !ValidObjectID(id) {
		return "", fmt.Errorf("invalid Obot Git credential ID %q", id)
	}
	return "/git-credentials/" + id, nil
}

func (c *Client) writeGitCredential(ctx context.Context, method, path string, manifest GitCredentialManifest) (*GitCredential, error) {
	body, err := json.Marshal(manifest)
	if err != nil {
		return nil, fmt.Errorf("encoding Git credential: %w", err)
	}
	var result GitCredential
	if err := c.request(ctx, method, path, string(body), &result); err != nil {
		return nil, err
	}
	if !ValidObjectID(result.ID) {
		return nil, fmt.Errorf("obot returned an invalid Git credential ID %q", result.ID)
	}
	return &result, nil
}

// CreateGitCredential stores a new credential and its token.
func (c *Client) CreateGitCredential(ctx context.Context, manifest GitCredentialManifest) (*GitCredential, error) {
	if manifest.Token == "" {
		return nil, fmt.Errorf("a Git credential requires a token")
	}
	return c.writeGitCredential(ctx, http.MethodPost, "/git-credentials", manifest)
}

// GetGitCredential reads a credential's metadata.
func (c *Client) GetGitCredential(ctx context.Context, id string) (*GitCredential, error) {
	path, err := gitCredentialPath(id)
	if err != nil {
		return nil, err
	}
	var result GitCredential
	if err := c.request(ctx, http.MethodGet, path, "", &result); err != nil {
		return nil, err
	}
	if result.ID != id {
		return nil, fmt.Errorf("obot returned an unexpected Git credential ID")
	}
	return &result, nil
}

// ListGitCredentials returns every credential's metadata.
func (c *Client) ListGitCredentials(ctx context.Context) ([]GitCredential, error) {
	var result struct {
		Items []GitCredential `json:"items"`
	}
	if err := c.request(ctx, http.MethodGet, "/git-credentials", "", &result); err != nil {
		return nil, err
	}
	for _, item := range result.Items {
		if !ValidObjectID(item.ID) {
			return nil, fmt.Errorf("obot returned an invalid Git credential ID")
		}
	}
	return result.Items, nil
}

// UpdateGitCredential changes the display name and, when Token is set, the token.
// The host cannot change.
func (c *Client) UpdateGitCredential(ctx context.Context, id string, manifest GitCredentialManifest) (*GitCredential, error) {
	path, err := gitCredentialPath(id)
	if err != nil {
		return nil, err
	}
	result, err := c.writeGitCredential(ctx, http.MethodPatch, path, manifest)
	if err != nil {
		return nil, err
	}
	if result.ID != id {
		return nil, fmt.Errorf("obot changed a Git credential ID during update")
	}
	return result, nil
}

// DeleteGitCredential removes a credential and waits until Obot has finished
// deleting it. Obot refuses while a source still uses the credential.
func (c *Client) DeleteGitCredential(ctx context.Context, id string) error {
	path, err := gitCredentialPath(id)
	if err != nil {
		return err
	}
	if err := c.request(ctx, http.MethodDelete, path, "", nil); err != nil {
		if IsNotFound(err) {
			return nil
		}
		return err
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	for {
		_, err := c.GetGitCredential(ctx, id)
		if IsNotFound(err) {
			return nil
		}
		if err != nil {
			return fmt.Errorf("verifying Git credential deletion: %w", err)
		}
		if err := pollDelay(ctx); err != nil {
			return fmt.Errorf("waiting for Git credential deletion: %w", err)
		}
	}
}
