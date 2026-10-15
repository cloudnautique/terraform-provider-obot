// SPDX-License-Identifier: MPL-2.0

// Package client provides the Obot API connection used by the provider.
package client

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

const (
	// maxResponseBytes bounds decoded API responses.
	maxResponseBytes = 16 << 20
	// maxErrorMessageBytes bounds the server message kept in an HTTPError.
	maxErrorMessageBytes = 512
)

// tokenLike matches base64url runs as long as a tunnel token (86 characters)
// so one is never copied from a response body into diagnostics. It is long
// enough to leave object names, which users need to see, intact.
var tokenLike = regexp.MustCompile(`[A-Za-z0-9_-]{80,}`)

// Client owns its credentials and HTTP settings independently of other provider instances.
type Client struct {
	baseURL   string
	token     string
	userAgent string
	http      *http.Client
}

// User describes the authenticated Obot identity.
type User struct {
	ID            string `json:"id"`
	Role          int    `json:"role"`
	EffectiveRole int    `json:"effectiveRole"`
	Email         string `json:"email"`
	Username      string `json:"username"`
}

// HTTPError identifies API failures. Message is the server's explanation,
// shortened and with credentials and token-like values redacted.
type HTTPError struct {
	Method     string
	Path       string
	StatusCode int
	Message    string
}

func (e *HTTPError) Error() string {
	if e.Message == "" {
		return fmt.Sprintf("the Obot API %s %s returned HTTP %d", e.Method, e.Path, e.StatusCode)
	}
	return fmt.Sprintf("the Obot API %s %s returned HTTP %d: %s", e.Method, e.Path, e.StatusCode, e.Message)
}

// IsNotFound distinguishes missing objects from authentication and transport failures.
func IsNotFound(err error) bool {
	var failure *HTTPError
	return errors.As(err, &failure) && failure.StatusCode == http.StatusNotFound
}

// New creates a client accepting an Obot app URL or API base URL. The user
// agent identifies the provider and Terraform versions to the server.
func New(baseURL, token, userAgent string) (*Client, error) {
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
		baseURL:   u.String(),
		token:     token,
		userAgent: userAgent,
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
	req.Header.Set("User-Agent", c.userAgent)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := c.http.Do(req)
	if err != nil {
		return fmt.Errorf("connecting to Obot: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message, _ := io.ReadAll(io.LimitReader(resp.Body, maxErrorMessageBytes))
		return &HTTPError{Method: method, Path: path, StatusCode: resp.StatusCode, Message: c.sanitize(message)}
	}
	if result == nil {
		return nil
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseBytes+1))
	if err != nil {
		return fmt.Errorf("reading Obot %s response: %w", path, err)
	}
	if len(data) > maxResponseBytes {
		return fmt.Errorf("the Obot %s response exceeded %d MiB", path, maxResponseBytes>>20)
	}
	if err := json.Unmarshal(data, result); err != nil {
		return fmt.Errorf("decoding Obot %s response: %w", path, err)
	}
	return nil
}

// sanitize turns an error body into a single diagnostic line. Redirect bodies
// are dropped because they are HTML pages, not explanations.
func (c *Client) sanitize(body []byte) string {
	message := strings.Join(strings.Fields(string(body)), " ")
	if strings.HasPrefix(message, "<") {
		return ""
	}
	if c.token != "" {
		message = strings.ReplaceAll(message, c.token, "[REDACTED]")
	}
	return tokenLike.ReplaceAllString(message, "[REDACTED]")
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
