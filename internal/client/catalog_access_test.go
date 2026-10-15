// SPDX-License-Identifier: MPL-2.0

package client

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"testing"
	"time"
)

func testCatalogClient(t *testing.T, handler http.HandlerFunc) *Client {
	t.Helper()
	// Discobox may probe newly listening ports at /. Keep those requests out of
	// the API fixtures and synchronization counters.
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.URL.Path, "/api/") {
			http.NotFound(w, r)
			return
		}
		handler(w, r)
	}))
	t.Cleanup(server.Close)
	conn, err := New(server.URL, "test-key", "test")
	if err != nil {
		t.Fatal(err)
	}
	return conn
}
func TestCurrentAuthProviderResolution(t *testing.T) {
	for _, test := range []struct {
		name, items string
		role        int
		wantError   bool
	}{
		{"local", `[{"id":"local","name":"Local","namespace":"default","configured":true}]`, 8, false},
		{"external", `[{"id":"okta","name":"Okta","namespace":"default","groupIDPrefix":"okta/","configured":true}]`, 16, false},
		{"staged replacement", `[{"id":"local","name":"Local","namespace":"default","configured":true},{"id":"okta","name":"Okta","namespace":"default","configured":true,"staged":true}]`, 8, false},
		{"none", `[]`, 8, true},
		{"not configured", `[{"id":"local","name":"Local","namespace":"default","configured":false}]`, 8, true},
		{"ambiguous", `[{"id":"local","name":"Local","namespace":"default","configured":true},{"id":"okta","name":"Okta","namespace":"default","configured":true}]`, 8, true},
		{"incomplete", `[{"id":"local","configured":true}]`, 8, true},
		{"basic cannot inspect staging", `[{"id":"local","name":"Local","namespace":"default","configured":true}]`, 4, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			conn := testCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" {
					t.Error("lookup mutated the API")
				}
				if r.URL.Path == "/api/me" {
					_ = json.NewEncoder(w).Encode(map[string]any{"id": "user1", "email": "test@example.com", "role": test.role})
					return
				}
				_, _ = w.Write([]byte(`{"items":` + test.items + `}`))
			})
			result, err := conn.CurrentAuthProvider(t.Context())
			if (err != nil) != test.wantError {
				t.Fatalf("result=%v error=%v", result, err)
			}
			if !test.wantError && (result == nil || result.ID == "") {
				t.Fatal("missing provider metadata")
			}
			if test.role == 4 && calls != 1 {
				t.Fatal("non-admin lookup continued after role rejection")
			}
		})
	}
}
func TestCatalogClientConfiguration(t *testing.T) {
	calls := 0
	conn := testCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
		calls++
		if r.Header.Get("Authorization") != "Bearer test-key" {
			t.Error("missing authentication")
		}
		if r.Method == http.MethodPut {
			var body map[string]any
			if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
				t.Error(err)
			}
			if _, ok := body["sourceURLCredentials"]; ok {
				t.Error("legacy inline credentials must not be sent")
			}
			if !reflect.DeepEqual(body["sourceURLs"], []any{}) {
				t.Error("clearing sources must send an empty array")
			}
			if !reflect.DeepEqual(body["sourceURLGitCredentialIDs"], map[string]any{}) {
				t.Error("clearing credential references must send an empty map")
			}
		}
		_, _ = w.Write([]byte(`{"id":"default","sourceURLs":[],"sourceURLCredentials":{"https://example.com":"private-token"}}`))
	})
	result, err := conn.GetCatalog(t.Context(), "default")
	if err != nil {
		t.Fatal(err)
	}
	encoded, _ := json.Marshal(result)
	if strings.Contains(string(encoded), "private-token") {
		t.Fatal("legacy credentials retained")
	}
	if _, err := conn.UpdateCatalogSources(t.Context(), "default", CatalogSources{}); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"../default", "default?x=y", "/default", ""} {
		if _, err := conn.GetCatalog(t.Context(), id); err == nil {
			t.Errorf("accepted %q", id)
		}
	}
	if _, err := conn.UpdateCatalogSources(t.Context(), "other", CatalogSources{}); err == nil {
		t.Fatal("managed unsupported catalog")
	}
	if calls != 2 {
		t.Fatal("invalid input reached API")
	}
}
func TestCatalogRefreshWaitsForNewSync(t *testing.T) {
	before := "2000-01-01T00:00:00Z"
	after := "2000-01-01T00:00:01Z"
	gets := 0
	posts := 0
	conn := testCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts++
			w.WriteHeader(204)
			return
		}
		gets++
		last := before
		syncing := false
		if gets == 2 {
			last = after
			syncing = true
		}
		if gets >= 3 {
			last = after
		}
		_ = json.NewEncoder(w).Encode(Catalog{ID: "default", LastSynced: last, IsSyncing: syncing})
	})
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Second)
	defer cancel()
	result, err := conn.RefreshCatalog(ctx, "default", before)
	if err != nil || result.LastSynced != after || gets != 3 || posts != 1 {
		t.Fatalf("stale status accepted: gets=%d posts=%d err=%v", gets, posts, err)
	}
}
func TestCatalogRefreshFailureAndCancellation(t *testing.T) {
	for _, test := range []struct {
		name             string
		status           int
		stale, syncError bool
	}{
		{name: "source failure", status: 200, syncError: true}, {name: "timeout", status: 200, stale: true}, {name: "forbidden", status: 403},
	} {
		t.Run(test.name, func(t *testing.T) {
			conn := testCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method == "POST" {
					w.WriteHeader(test.status)
					return
				}
				result := Catalog{ID: "default", LastSynced: "2000-01-01T00:00:01Z"}
				if test.stale {
					result.LastSynced = "2000-01-01T00:00:00Z"
				}
				if test.syncError {
					result.SyncErrors = map[string]string{"https://example.com": "private-detail"}
				}
				_ = json.NewEncoder(w).Encode(result)
			})
			ctx, cancel := context.WithTimeout(t.Context(), 100*time.Millisecond)
			defer cancel()
			_, err := conn.RefreshCatalog(ctx, "default", "2000-01-01T00:00:00Z")
			if err == nil {
				t.Fatal("failed synchronization accepted")
			}
			if strings.Contains(err.Error(), "private-detail") {
				t.Fatal("API details leaked into diagnostics")
			}
			if test.stale && !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("timeout cause lost")
			}
		})
	}
}
func TestAccessRuleClientScopeAndPaths(t *testing.T) {
	for _, test := range []struct {
		name, body string
		status     int
		wantError  bool
	}{
		{"success", `{"id":"acr1-test","mcpCatalogID":"default"}`, 200, false},
		{"wrong catalog", `{"id":"acr1-test","mcpCatalogID":"other"}`, 200, true},
		{"wrong ID", `{"id":"acr1-other","mcpCatalogID":"default"}`, 200, true},
		{"missing", `{}`, 404, true}, {"forbidden", `{"message":"permission denied for Bearer test-key"}`, 403, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			conn := testCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.URL.Path != "/api/mcp-catalogs/default/access-control-rules/acr1-test" {
					t.Error("unexpected route")
				}
				w.WriteHeader(test.status)
				_, _ = w.Write([]byte(test.body))
			})
			_, err := conn.GetAccessRule(t.Context(), "default", "acr1-test")
			if (err != nil) != test.wantError {
				t.Fatal(err)
			}
			if err != nil && strings.Contains(err.Error(), "test-key") {
				t.Fatal("API error leaked the configured credential")
			}
			if test.status == http.StatusForbidden && (err == nil || !strings.Contains(err.Error(), "permission denied")) {
				t.Fatal("API error lost the sanitized server explanation")
			}
			for _, ids := range [][2]string{{"../default", "acr1-test"}, {"default", "../acr1-test"}, {"default", "acr1-test?x=y"}} {
				if _, err := conn.GetAccessRule(t.Context(), ids[0], ids[1]); err == nil {
					t.Fatal("accepted unsafe path")
				}
			}
			if calls != 1 {
				t.Fatal("invalid input reached server")
			}
		})
	}
}
func TestAccessRuleClientWrites(t *testing.T) {
	manifest := AccessRuleManifest{DisplayName: "Everything", Subjects: []RuleReference{{Type: "selector", ID: "*"}}, Resources: []RuleReference{{Type: "selector", ID: "*"}}}
	methods := []string{}
	conn := testCatalogClient(t, func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Method == "POST" || r.Method == "PUT" {
			var got AccessRuleManifest
			if err := json.NewDecoder(r.Body).Decode(&got); err != nil {
				t.Error(err)
			}
			if !reflect.DeepEqual(got, manifest) {
				t.Error("manifest changed")
			}
		}
		if r.Method == "DELETE" {
			w.WriteHeader(404)
			return
		}
		_ = json.NewEncoder(w).Encode(AccessRule{ID: "acr1-test", CatalogID: "default", AccessRuleManifest: manifest})
	})
	if _, err := conn.CreateAccessRule(t.Context(), "default", manifest); err != nil {
		t.Fatal(err)
	}
	if _, err := conn.UpdateAccessRule(t.Context(), "default", "acr1-test", manifest); err != nil {
		t.Fatal(err)
	}
	if err := conn.DeleteAccessRule(t.Context(), "default", "acr1-test"); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(methods, []string{"POST", "PUT", "DELETE"}) {
		t.Fatal(methods)
	}
}
