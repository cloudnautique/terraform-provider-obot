// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"regexp"
	"strings"
)

// TunnelIDPattern matches the Kubernetes-style names Obot generates for tunnels.
var TunnelIDPattern = regexp.MustCompile(`^[a-z0-9](?:[a-z0-9-]{0,61}[a-z0-9])?$`)

// TunnelManifest contains the mutable tunnel definition.
type TunnelManifest struct {
	DisplayName string   `json:"displayName"`
	Description string   `json:"description"`
	AllowedURLs []string `json:"allowedURLs"`
}

// Tunnel carries a full token only in create and rotate responses.
type Tunnel struct {
	ID       string         `json:"id"`
	Manifest TunnelManifest `json:"manifest"`
	Token    string         `json:"token"`
}

func (t Tunnel) String() string   { return fmt.Sprintf("Obot tunnel (%s, token=[REDACTED])", t.ID) }
func (t Tunnel) GoString() string { return t.String() }

// ValidTunnelID checks the identifier format shared by API lookup and import.
func ValidTunnelID(id string) bool { return TunnelIDPattern.MatchString(id) }

func tunnelPath(id string) (string, error) {
	if !ValidTunnelID(id) {
		return "", fmt.Errorf("invalid Obot tunnel ID: use 1-63 lowercase letters, digits, or hyphens, starting and ending with a letter or digit")
	}
	return "/mcp-tunnels/" + id, nil
}

// TunnelTokenPreview derives the server's preview without retaining extra secret material.
func TunnelTokenPreview(token string) (string, error) {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(token)
	if err != nil || len(raw) != 64 {
		return "", fmt.Errorf("obot did not return a complete tunnel token; explicitly rotate the secret to recover")
	}
	return base64.RawURLEncoding.EncodeToString(raw[:16]) + "...", nil
}

// IsKnownTunnelTokenPreview reports whether a preview uses the format that
// TunnelTokenPreview derives, so a cached token can be compared with it.
func IsKnownTunnelTokenPreview(preview string) bool {
	raw, err := base64.RawURLEncoding.Strict().DecodeString(strings.TrimSuffix(preview, "..."))
	return strings.HasSuffix(preview, "...") && err == nil && len(raw) == 16
}

// validPreview accepts any nonempty preview so a server format change does
// not break reads, but never a complete token, which must not be stored in
// the non-sensitive token_preview attribute.
func validPreview(preview string) bool {
	if preview == "" {
		return false
	}
	_, err := TunnelTokenPreview(preview)
	return err != nil
}

func (c *Client) writeTunnel(ctx context.Context, method, path string, manifest *TunnelManifest, complete bool) (*Tunnel, error) {
	body := "{}"
	if manifest != nil {
		encoded, err := json.Marshal(manifest)
		if err != nil {
			return nil, fmt.Errorf("encoding tunnel manifest: %w", err)
		}
		body = string(encoded)
	}
	var result Tunnel
	if err := c.request(ctx, method, path, body, &result); err != nil {
		return nil, err
	}
	if !ValidTunnelID(result.ID) {
		// Name the ID so an operator can remove an object the provider cannot track.
		return nil, fmt.Errorf("obot returned an invalid tunnel ID %q; remove it in Obot if it was created", result.ID)
	}
	if complete {
		if _, err := TunnelTokenPreview(result.Token); err != nil {
			return &result, err
		}
	} else if !validPreview(result.Token) {
		return &result, fmt.Errorf("obot did not return a tunnel token preview")
	}
	return &result, nil
}

// CreateTunnel creates a definition and captures its one-time token.
func (c *Client) CreateTunnel(ctx context.Context, manifest TunnelManifest) (*Tunnel, error) {
	return c.writeTunnel(ctx, http.MethodPost, "/mcp-tunnels", &manifest, true)
}

// GetTunnel reads a definition and a non-secret preview.
func (c *Client) GetTunnel(ctx context.Context, id string) (*Tunnel, error) {
	path, err := tunnelPath(id)
	if err != nil {
		return nil, err
	}
	var result Tunnel
	if err := c.request(ctx, http.MethodGet, path, "", &result); err != nil {
		return nil, err
	}
	if result.ID != id || !validPreview(result.Token) {
		return nil, fmt.Errorf("obot returned invalid tunnel metadata or token preview")
	}
	return &result, nil
}

// ListTunnels returns configured definitions, never complete tokens.
func (c *Client) ListTunnels(ctx context.Context) ([]Tunnel, error) {
	var result struct {
		Items []Tunnel `json:"items"`
	}
	if err := c.request(ctx, http.MethodGet, "/mcp-tunnels", "", &result); err != nil {
		return nil, err
	}
	for _, item := range result.Items {
		if !ValidTunnelID(item.ID) || !validPreview(item.Token) {
			return nil, fmt.Errorf("obot returned invalid tunnel metadata or token preview")
		}
	}
	return result.Items, nil
}

// UpdateTunnel changes metadata without rotating credentials.
func (c *Client) UpdateTunnel(ctx context.Context, id string, manifest TunnelManifest) (*Tunnel, error) {
	path, err := tunnelPath(id)
	if err != nil {
		return nil, err
	}
	result, err := c.writeTunnel(ctx, http.MethodPut, path, &manifest, false)
	if result != nil && result.ID != id {
		return nil, fmt.Errorf("obot changed a tunnel ID during update")
	}
	return result, err
}

// RotateTunnelToken explicitly replaces the credential and disconnects old clients.
func (c *Client) RotateTunnelToken(ctx context.Context, id string) (*Tunnel, error) {
	path, err := tunnelPath(id)
	if err != nil {
		return nil, err
	}
	result, err := c.writeTunnel(ctx, http.MethodPost, path+"/rotate-secret", nil, true)
	if result != nil && result.ID != id {
		return nil, fmt.Errorf("obot changed a tunnel ID during rotation")
	}
	return result, err
}

// DeleteTunnel removes a definition, tolerating an object already removed elsewhere.
func (c *Client) DeleteTunnel(ctx context.Context, id string) error {
	path, err := tunnelPath(id)
	if err != nil {
		return err
	}
	err = c.request(ctx, http.MethodDelete, path, "", nil)
	if IsNotFound(err) {
		return nil
	}
	return err
}
