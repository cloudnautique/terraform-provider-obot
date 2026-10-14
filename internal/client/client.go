// SPDX-License-Identifier: MPL-2.0

// Package client provides the Obot API connection used by the provider.
package client

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Client owns its credentials and HTTP settings independently of other provider instances.
type Client struct {
	baseURL string
	token   string
	version string
	http    *http.Client
}

// User describes the authenticated Obot identity.
type User struct {
	ID       string `json:"id"`
	Email    string `json:"email"`
	Username string `json:"username"`
}

// New creates a client accepting an Obot app URL or API base URL.
func New(baseURL, token, version string) (*Client, error) {
	u, err := url.Parse(strings.TrimSpace(baseURL))
	if err != nil || u == nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") ||
		u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("base_url must be an absolute HTTP or HTTPS URL without credentials, a query, or a fragment")
	}
	u.Path = strings.TrimRight(u.Path, "/")
	if !strings.HasSuffix(u.Path, "/api") {
		u.Path += "/api"
	}
	u.RawPath = ""

	return &Client{
		baseURL: u.String(),
		token:   token,
		version: version,
		http: &http.Client{
			Timeout: 30 * time.Second,
			CheckRedirect: func(_ *http.Request, _ []*http.Request) error {
				return http.ErrUseLastResponse
			},
		},
	}, nil
}

// String prevents credentials appearing when a client is formatted in diagnostics.
func (c Client) String() string {
	return fmt.Sprintf("Obot client (%s, token=[REDACTED])", c.baseURL)
}

// GoString prevents credentials appearing in Go-syntax formatting.
func (c Client) GoString() string {
	return c.String()
}

func (c *Client) request(ctx context.Context, method, path, body string, result any) error {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+path, strings.NewReader(body))
	if err != nil {
		return fmt.Errorf("building Obot request: %w", err)
	}
	req.Header.Set("Authorization", "Bearer "+c.token)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "terraform-provider-obot/"+c.version)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("connecting to Obot: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return fmt.Errorf("the Obot API %s %s returned HTTP %d; check base_url, token validity, API scope, and user permissions",
			method, path, resp.StatusCode)
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(result); err != nil {
		return fmt.Errorf("decoding Obot %s response: %w", path, err)
	}
	return nil
}

// Validate checks API-key validity, API scope, and authenticated identity.
func (c *Client) Validate(ctx context.Context) error {
	var validation struct {
		Allowed bool `json:"allowed"`
		Scopes  struct {
			CanAccessAPI bool `json:"canAccessAPI"`
		} `json:"scopes"`
	}
	if err := c.request(ctx, http.MethodPost, "/api-keys/auth", `{"validateOnly":true}`, &validation); err != nil {
		return err
	}
	if !validation.Allowed {
		return fmt.Errorf("the Obot API rejected the API key; obtain a valid key using obot login --scope api --print-token")
	}
	if !validation.Scopes.CanAccessAPI {
		return fmt.Errorf("the Obot API key is missing API access; obtain a key using obot login --scope api --print-token")
	}
	_, err := c.CurrentUser(ctx)
	return err
}

// CurrentUser returns the user identified by this client's API key.
func (c *Client) CurrentUser(ctx context.Context) (*User, error) {
	var user User
	if err := c.request(ctx, http.MethodGet, "/me", "", &user); err != nil {
		return nil, err
	}
	if user.ID == "" || user.Email == "" || user.Username == "anonymous" {
		return nil, fmt.Errorf("the Obot API did not return an authenticated user identity")
	}
	return &user, nil
}
