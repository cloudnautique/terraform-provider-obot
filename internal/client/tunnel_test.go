// SPDX-License-Identifier: MPL-2.0

package client

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func testTunnelToken() string {
	raw := make([]byte, 64)
	for i := range raw {
		raw[i] = byte(i + 1)
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func TestTunnelTokenPreview(t *testing.T) {
	token := testTunnelToken()
	preview, err := TunnelTokenPreview(token)
	if err != nil || preview != "AQIDBAUGBwgJCgsMDQ4PEA..." {
		t.Fatal("incorrect token preview derivation")
	}
	for _, invalid := range []string{"", preview, token + "=", token[:len(token)-1], strings.Repeat("!", 86)} {
		if _, err := TunnelTokenPreview(invalid); err == nil {
			t.Error("accepted an incomplete or noncanonical token")
		}
	}
	for _, output := range []string{fmt.Sprintf("%v", Tunnel{ID: "mt1", Token: token}), fmt.Sprintf("%#v", Tunnel{Token: token})} {
		if strings.Contains(output, token) {
			t.Fatal("formatting exposed a token")
		}
	}
}

func TestTunnelClientLifecycle(t *testing.T) {
	token := testTunnelToken()
	preview, _ := TunnelTokenPreview(token)
	manifest := TunnelManifest{DisplayName: "private", Description: "test", AllowedURLs: []string{"http://internal/*"}}
	var requests []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests = append(requests, r.Method+" "+r.URL.Path)
		result := Tunnel{ID: "mt1", Manifest: manifest, Token: preview}
		switch {
		case r.Method == http.MethodDelete:
			w.WriteHeader(http.StatusNoContent)
			return
		case r.Method == http.MethodPost && strings.HasSuffix(r.URL.Path, "/rotate-secret"):
			result.Token = token
		case r.Method == http.MethodPost || r.Method == http.MethodPut:
			var received TunnelManifest
			if err := json.NewDecoder(r.Body).Decode(&received); err != nil {
				t.Error(err)
			}
			if received.DisplayName != manifest.DisplayName || len(received.AllowedURLs) != 1 {
				t.Error("incorrect manifest request")
			}
			if r.Method == http.MethodPost {
				result.Token = token
			}
		case r.URL.Path == "/api/mcp-tunnels":
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []Tunnel{result}})
			return
		}
		_ = json.NewEncoder(w).Encode(result)
	}))
	defer server.Close()
	conn, err := New(server.URL, "test-key", "test")
	if err != nil {
		t.Fatal(err)
	}
	created, err := conn.CreateTunnel(t.Context(), manifest)
	if err != nil || created.Token != token {
		t.Fatal("create did not capture token")
	}
	read, err := conn.GetTunnel(t.Context(), "mt1")
	if err != nil || read.Token != preview {
		t.Fatal("read did not return preview")
	}
	list, err := conn.ListTunnels(t.Context())
	if err != nil || len(list) != 1 {
		t.Fatal("list failed")
	}
	if _, err := conn.UpdateTunnel(t.Context(), "mt1", manifest); err != nil {
		t.Fatal(err)
	}
	rotated, err := conn.RotateTunnelToken(t.Context(), "mt1")
	if err != nil || rotated.Token != token {
		t.Fatal("rotation did not capture token")
	}
	if err := conn.DeleteTunnel(t.Context(), "mt1"); err != nil {
		t.Fatal(err)
	}
	if len(requests) != 6 {
		t.Fatalf("unexpected request count: %d", len(requests))
	}
	for _, id := range []string{"../me", "mt1?token=secret", "", "Name", strings.Repeat("a", 64)} {
		if _, err := conn.GetTunnel(t.Context(), id); err == nil {
			t.Error("unsafe ID accepted")
		}
	}
	if len(requests) != 6 {
		t.Fatal("unsafe ID issued a request")
	}
}

func TestTunnelClientFailures(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 409, 500} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.WriteHeader(status)
				_, _ = w.Write([]byte("private-secret-response"))
			}))
			defer server.Close()
			conn, err := New(server.URL, "test-key", "test")
			if err != nil {
				t.Fatal(err)
			}
			_, err = conn.GetTunnel(t.Context(), "mt1")
			if err == nil || IsNotFound(err) != (status == 404) || strings.Contains(err.Error(), "private-secret-response") {
				t.Fatal("incorrect or unsafe HTTP error")
			}
			err = conn.DeleteTunnel(t.Context(), "mt1")
			if (err == nil) != (status == 404) {
				t.Fatal("delete suppressed an error other than not-found")
			}
		})
	}
	for _, test := range []struct {
		name, body string
		complete   bool
	}{
		{"malformed", "private-secret-response", false},
		{"wrong ID", `{"id":"mt2","token":"AQIDBAUGBwgJCgsMDQ4PEA..."}`, false},
		{"full token on read", fmt.Sprintf(`{"id":"mt1","token":%q}`, testTunnelToken()), false},
		{"preview on create", `{"id":"mt1","token":"AQIDBAUGBwgJCgsMDQ4PEA..."}`, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(test.body)) }))
			defer server.Close()
			conn, err := New(server.URL, "test-key", "test")
			if err != nil {
				t.Fatal(err)
			}
			if test.complete {
				result, err := conn.CreateTunnel(t.Context(), TunnelManifest{})
				if err == nil || result == nil || result.ID != "mt1" {
					t.Fatal("create must retain a recoverable ID when the secret response is invalid")
				}
			} else if _, err := conn.GetTunnel(t.Context(), "mt1"); err == nil {
				t.Fatal("accepted an invalid read response")
			}
		})
	}
}
