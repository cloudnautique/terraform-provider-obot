// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestClientConnection(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer private-test-key" {
			t.Error("request used unexpected credentials")
		}
		if r.Header.Get("User-Agent") != "terraform-provider-obot/test Terraform/1.14.0" {
			t.Error("request did not identify the provider")
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/prefix/api/api-keys/auth":
			if r.Method != http.MethodPost {
				t.Error("scope validation must use POST")
			}
			_, _ = w.Write([]byte(`{"allowed":true,"scopes":{"canAccessAPI":true}}`))
		case "/prefix/api/me":
			_, _ = w.Write([]byte(`{"id":"42","email":"test@example.com","username":"test"}`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	for _, suffix := range []string{"/prefix", "/prefix/", "/prefix/api", "/prefix/api/"} {
		t.Run(suffix, func(t *testing.T) {
			conn, err := New(server.URL+suffix, "private-test-key", "terraform-provider-obot/test Terraform/1.14.0")
			if err != nil {
				t.Fatal(err)
			}
			if err := conn.Validate(t.Context()); err != nil {
				t.Fatal(err)
			}
			user, err := conn.CurrentUser(t.Context())
			if err != nil || user.ID != "42" {
				t.Fatalf("current user: %v, error: %v", user, err)
			}
			for _, output := range []string{fmt.Sprintf("%v", conn), fmt.Sprintf("%+v", conn), fmt.Sprintf("%#v", conn), fmt.Sprintf("%+v", *conn)} {
				if strings.Contains(output, "private-test-key") {
					t.Fatal("client formatting exposed credentials")
				}
			}
		})
	}
}

func TestClientAuthenticationErrors(t *testing.T) {
	for _, test := range []struct {
		name   string
		status int
		body   string
		want   string
	}{
		{name: "rejected", status: 200, body: `{"allowed":false}`, want: "rejected"},
		{name: "missing scope", status: 200, body: `{"allowed":true,"scopes":{"canAccessAPI":false}}`, want: "missing API access"},
		{name: "forbidden", status: 403, body: "private-test-key", want: "HTTP 403"},
		{name: "login redirect", status: 302, body: "private-test-key", want: "HTTP 302"},
		{name: "invalid JSON", status: 200, body: "private-test-key", want: "decoding"},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				w.Header().Set("Location", "/login")
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			}))
			defer server.Close()
			conn, err := New(server.URL, "private-test-key", "test")
			if err != nil {
				t.Fatal(err)
			}
			err = conn.Validate(t.Context())
			if err == nil || !strings.Contains(err.Error(), test.want) {
				t.Fatalf("expected %q, got %v", test.want, err)
			}
			if strings.Contains(err.Error(), "private-test-key") {
				t.Fatal("error exposed credentials")
			}
		})
	}
}

func TestClientRejectsUnsafeURLs(t *testing.T) {
	for _, value := range []string{"", "localhost:8080", "ftp://localhost", "https://user:password@localhost", "https://localhost?token=secret", "https://localhost/#secret"} {
		_, err := New(value, "test-key", "test")
		if err == nil {
			t.Errorf("accepted invalid URL %q", value)
		}
	}
}

func TestClientCancellation(t *testing.T) {
	conn, err := New("http://127.0.0.1:1", "test-key", "test")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	_, err = conn.CurrentUser(ctx)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected cancellation, got %v", err)
	}
}
