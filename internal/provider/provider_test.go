// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/provider"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-go/tftypes"
	"github.com/obot-platform/terraform-provider-obot/internal/client"
)

func TestProviderConfigure(t *testing.T) {
	var requests int
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Header.Get("Authorization") != "Bearer configured-test-key" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		if strings.HasSuffix(r.URL.Path, "/api-keys/auth") {
			_, _ = w.Write([]byte(`{"allowed":true,"scopes":{"canAccessAPI":true}}`))
			return
		}
		_, _ = w.Write([]byte(`{"id":"1","email":"owner@example.com","username":"owner"}`))
	}))
	defer server.Close()
	for _, test := range []struct {
		name       string
		baseURL    any
		token      any
		envURL     string
		envToken   string
		wantError  string
		noRequests bool
	}{
		{name: "environment fallback", envURL: server.URL, envToken: "configured-test-key"},
		{name: "explicit configuration wins", baseURL: server.URL, token: "configured-test-key", envURL: "http://127.0.0.1:1", envToken: "wrong-key"},
		{name: "missing credentials", envURL: server.URL, wantError: "Missing Obot Token", noRequests: true},
		{name: "missing URL", envToken: "configured-test-key", wantError: "Missing Obot URL", noRequests: true},
		{name: "explicit empty token", token: "", envURL: server.URL, envToken: "configured-test-key", wantError: "Missing Obot Token", noRequests: true},
		{name: "unknown token", token: tftypes.UnknownValue, envURL: server.URL, envToken: "configured-test-key", wantError: "Unknown Obot Token", noRequests: true},
		{name: "unknown URL", baseURL: tftypes.UnknownValue, envURL: server.URL, envToken: "configured-test-key", wantError: "Unknown Obot URL", noRequests: true},
		{name: "invalid URL", baseURL: "https://user:secret@localhost", envToken: "configured-test-key", wantError: "Invalid Obot URL", noRequests: true},
		{name: "invalid token", envURL: server.URL, envToken: "invalid-test-key", wantError: "Unable to Connect to Obot"},
	} {
		t.Run(test.name, func(t *testing.T) {
			t.Setenv("OBOT_BASE_URL", test.envURL)
			t.Setenv("OBOT_TOKEN", test.envToken)
			p := New("test")()
			var schema provider.SchemaResponse
			p.Schema(t.Context(), provider.SchemaRequest{}, &schema)
			raw := tftypes.NewValue(tftypes.Object{
				AttributeTypes: map[string]tftypes.Type{"base_url": tftypes.String, "token": tftypes.String},
			}, map[string]tftypes.Value{
				"base_url": tftypes.NewValue(tftypes.String, test.baseURL),
				"token":    tftypes.NewValue(tftypes.String, test.token),
			})
			before := requests
			var response provider.ConfigureResponse
			p.Configure(t.Context(), provider.ConfigureRequest{Config: tfsdk.Config{Schema: schema.Schema, Raw: raw}}, &response)
			if test.wantError == "" {
				if response.Diagnostics.HasError() {
					t.Fatal(response.Diagnostics)
				}
				if _, ok := response.DataSourceData.(*client.Client); !ok {
					t.Fatal("provider did not configure the API client")
				}
				if response.ResourceData != response.DataSourceData {
					t.Fatal("resources and data sources must share the configured client")
				}
			} else {
				if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics[0].Summary(), test.wantError) {
					t.Fatalf("expected %q, got %v", test.wantError, response.Diagnostics)
				}
				if strings.Contains(response.Diagnostics.Errors()[0].Detail(), "invalid-test-key") {
					t.Fatal("diagnostic exposed credentials")
				}
			}
			if test.noRequests && requests != before {
				t.Fatal("invalid configuration made an HTTP request")
			}
		})
	}
}
