// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/base64"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/obot-platform/terraform-provider-obot/internal/client"
)

func tunnelTestToken(seed byte) string {
	raw := make([]byte, 64)
	for i := range raw {
		raw[i] = byte(i) + seed
	}
	return base64.RawURLEncoding.EncodeToString(raw)
}

func tunnelTestState(t *testing.T, model tunnelResourceModel) tfsdk.State {
	t.Helper()
	if model.AllowedURLs.ElementType(t.Context()) == nil {
		model.AllowedURLs = types.SetNull(types.StringType)
	}
	var schema resource.SchemaResponse
	newMCPTunnelResource().Schema(t.Context(), resource.SchemaRequest{}, &schema)
	state := tfsdk.State{Schema: schema.Schema}
	if diagnostics := state.Set(t.Context(), &model); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	return state
}

func tunnelTestModel() tunnelResourceModel {
	token := tunnelTestToken(1)
	preview, _ := client.TunnelTokenPreview(token)
	return tunnelResourceModel{ID: types.StringValue("mt1"), DisplayName: types.StringValue("private"), Description: types.StringValue(""), AllowedURLs: types.SetValueMust(types.StringType, []attr.Value{types.StringValue("http://internal/*")}), Token: types.StringValue(token), TokenPreview: types.StringValue(preview), TokenRotationVersion: types.Int64Value(0)}
}

func TestTunnelRefreshToken(t *testing.T) {
	for _, test := range []struct {
		name              string
		token             types.String
		wantNull, warning bool
	}{
		{"same", types.StringValue(tunnelTestToken(1)), false, false},
		{"import", types.StringNull(), true, false},
		{"external rotation", types.StringValue(tunnelTestToken(2)), true, true},
		{"invalid cached token", types.StringValue("preview..."), true, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			preview, _ := client.TunnelTokenPreview(tunnelTestToken(1))
			model := tunnelResourceModel{Token: test.token}
			var diagnostics diag.Diagnostics
			model.refreshToken(preview, &diagnostics)
			if model.Token.IsNull() != test.wantNull || (len(diagnostics.Warnings()) > 0) != test.warning || model.TokenPreview.ValueString() != preview {
				t.Fatal("incorrect refresh token handling")
			}
		})
	}
}

func TestTunnelModifyPlan(t *testing.T) {
	for _, test := range []struct {
		name                         string
		old, next                    types.Int64
		imported, unknown, wantError bool
	}{
		{name: "metadata", old: types.Int64Value(0), next: types.Int64Value(0)},
		{name: "imported metadata", old: types.Int64Value(0), next: types.Int64Value(0), imported: true},
		{name: "rotation", old: types.Int64Value(0), next: types.Int64Value(1), unknown: true},
		{name: "unknown rotation", old: types.Int64Value(0), next: types.Int64Unknown(), unknown: true},
		{name: "decrease", old: types.Int64Value(2), next: types.Int64Value(1), wantError: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			model := tunnelTestModel()
			model.TokenRotationVersion = test.old
			if test.imported {
				model.Token = types.StringNull()
			}
			state := tunnelTestState(t, model)
			model.TokenRotationVersion = test.next
			model.Token = types.StringUnknown()
			model.TokenPreview = types.StringUnknown()
			planned := tunnelTestState(t, model)
			plan := tfsdk.Plan(planned)
			response := resource.ModifyPlanResponse{Plan: plan}
			(&mcpTunnelResource{}).ModifyPlan(t.Context(), resource.ModifyPlanRequest{State: state, Plan: plan}, &response)
			if response.Diagnostics.HasError() != test.wantError {
				t.Fatal(response.Diagnostics)
			}
			if test.wantError {
				return
			}
			if d := response.Plan.Get(t.Context(), &model); d.HasError() {
				t.Fatal(d)
			}
			if model.Token.IsUnknown() != test.unknown || (!test.unknown && model.Token.IsNull() != test.imported) {
				t.Fatal("incorrect planned token")
			}
		})
	}
}

func TestTunnelImport(t *testing.T) {
	for _, id := range []string{"mt1", "../mt1", "display name"} {
		t.Run(id, func(t *testing.T) {
			response := resource.ImportStateResponse{State: tunnelTestState(t, tunnelResourceModel{})}
			(&mcpTunnelResource{}).ImportState(t.Context(), resource.ImportStateRequest{ID: id}, &response)
			if response.Diagnostics.HasError() != (id != "mt1") {
				t.Fatal(response.Diagnostics)
			}
			if id != "mt1" {
				return
			}
			var model tunnelResourceModel
			if d := response.State.Get(t.Context(), &model); d.HasError() {
				t.Fatal(d)
			}
			if !model.Token.IsNull() || model.TokenRotationVersion.ValueInt64() != 0 || model.ID.ValueString() != id || len(response.Diagnostics.Warnings()) != 1 {
				t.Fatal("import did not preserve safe token semantics")
			}
		})
	}
}

func TestTunnelUpdatePartialFailure(t *testing.T) {
	for _, test := range []struct {
		name                           string
		metadataStatus, rotationStatus int
		version                        int64
		wantName                       string
		wantVersion                    int64
		wantError                      bool
	}{
		{"metadata failure", 400, 200, 1, "private", 0, true},
		{"rotation failure", 200, 500, 1, "updated", 0, true},
		{"success", 200, 200, 1, "updated", 1, false},
		{"decrease at apply", 200, 200, -1, "private", 0, true},
	} {
		t.Run(test.name, func(t *testing.T) {
			calls := 0
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls++
				result := client.Tunnel{ID: "mt1", Manifest: client.TunnelManifest{DisplayName: "updated", AllowedURLs: []string{"http://internal/*"}}}
				if r.Method == http.MethodPut {
					w.WriteHeader(test.metadataStatus)
					result.Token, _ = client.TunnelTokenPreview(tunnelTestToken(1))
				} else {
					w.WriteHeader(test.rotationStatus)
					result.Token = tunnelTestToken(2)
				}
				_ = json.NewEncoder(w).Encode(result)
			}))
			defer server.Close()
			conn, err := client.New(server.URL, "test-key", "test")
			if err != nil {
				t.Fatal(err)
			}
			model := tunnelTestModel()
			state := tunnelTestState(t, model)
			model.DisplayName = types.StringValue("updated")
			model.TokenRotationVersion = types.Int64Value(test.version)
			plan := tunnelTestState(t, model)
			response := resource.UpdateResponse{State: state}
			(&mcpTunnelResource{client: conn}).Update(t.Context(), resource.UpdateRequest{State: state, Plan: tfsdk.Plan(plan)}, &response)
			if response.Diagnostics.HasError() != test.wantError {
				t.Fatal(response.Diagnostics)
			}
			if d := response.State.Get(t.Context(), &model); d.HasError() {
				t.Fatal(d)
			}
			if model.DisplayName.ValueString() != test.wantName || model.TokenRotationVersion.ValueInt64() != test.wantVersion {
				t.Fatal("successful metadata or prior rotation trigger was not retained")
			}
			if test.version < 0 && calls != 0 {
				t.Fatal("decreasing version mutated the API")
			}
		})
	}
}

func TestTunnelReadAndDeleteErrors(t *testing.T) {
	for _, status := range []int{400, 403, 404, 500} {
		server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) }))
		conn, err := client.New(server.URL, "test-key", "test")
		if err != nil {
			t.Fatal(err)
		}
		r := &mcpTunnelResource{client: conn}
		state := tunnelTestState(t, tunnelTestModel())
		read := resource.ReadResponse{State: state}
		r.Read(t.Context(), resource.ReadRequest{State: state}, &read)
		if read.Diagnostics.HasError() != (status != 404) || read.State.Raw.IsNull() != (status == 404) {
			t.Fatal("read must remove state only on 404")
		}
		deleted := resource.DeleteResponse{State: state}
		r.Delete(t.Context(), resource.DeleteRequest{State: state}, &deleted)
		if deleted.Diagnostics.HasError() != (status != 404) {
			t.Fatal("delete must preserve dependency and permission errors")
		}
		server.Close()
	}
}

func TestTunnelCreateIncompleteSecret(t *testing.T) {
	preview, _ := client.TunnelTokenPreview(tunnelTestToken(1))
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		_ = json.NewEncoder(w).Encode(client.Tunnel{ID: "mt1", Token: preview, Manifest: client.TunnelManifest{DisplayName: "private", AllowedURLs: []string{"http://internal/*"}}})
	}))
	defer server.Close()
	conn, err := client.New(server.URL, "test-key", "test")
	if err != nil {
		t.Fatal(err)
	}
	model := tunnelTestModel()
	model.ID = types.StringUnknown()
	model.Token = types.StringUnknown()
	model.TokenPreview = types.StringUnknown()
	plan := tunnelTestState(t, model)
	response := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	(&mcpTunnelResource{client: conn}).Create(t.Context(), resource.CreateRequest{Plan: tfsdk.Plan(plan)}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("create accepted a preview as a complete secret")
	}
	if d := response.State.Get(t.Context(), &model); d.HasError() {
		t.Fatal(d)
	}
	if model.ID.ValueString() != "mt1" || !model.Token.IsNull() {
		t.Fatal("failed secret capture lost the recoverable ID or stored a preview as a token")
	}
}

func TestTunnelDataSources(t *testing.T) {
	preview, _ := client.TunnelTokenPreview(tunnelTestToken(1))
	for _, test := range []struct {
		name, body        string
		plural, wantError bool
		count             int
	}{
		{"empty", `{"items":[]}`, true, false, 0},
		{"null items", `{"items":null}`, true, false, 0},
		{"sorted", `{"items":[{"id":"mt2","token":"` + preview + `"},{"id":"mt1","token":"` + preview + `"}]}`, true, false, 2},
		{"reject full tokens", `{"items":[{"id":"mt1","token":"` + tunnelTestToken(1) + `"}]}`, true, true, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte(test.body)) }))
			defer server.Close()
			conn, err := client.New(server.URL, "test-key", "test")
			if err != nil {
				t.Fatal(err)
			}
			d := &mcpTunnelDataSource{client: conn, plural: test.plural}
			var schema datasource.SchemaResponse
			d.Schema(t.Context(), datasource.SchemaRequest{}, &schema)
			response := datasource.ReadResponse{State: tfsdk.State{Schema: schema.Schema}}
			d.Read(t.Context(), datasource.ReadRequest{}, &response)
			if response.Diagnostics.HasError() != test.wantError {
				t.Fatal(response.Diagnostics)
			}
			if test.wantError {
				return
			}
			var model tunnelsDataModel
			if ds := response.State.Get(t.Context(), &model); ds.HasError() {
				t.Fatal(ds)
			}
			if model.Tunnels.IsNull() || len(model.Tunnels.Elements()) != test.count {
				t.Fatal("incorrect list shape")
			}
			if test.count > 0 {
				first, ok := model.Tunnels.Elements()[0].(types.Object)
				if !ok {
					t.Fatal("expected an object")
				}
				id, ok := first.Attributes()["id"].(types.String)
				if !ok || id.ValueString() != "mt1" {
					t.Fatal("list is not sorted")
				}
			}
		})
	}
}

func TestTunnelValidators(t *testing.T) {
	for _, test := range []struct {
		value string
		valid bool
	}{{"private", true}, {"", false}, {" trailing ", false}} {
		var resp validator.StringResponse
		(tunnelStringValidator{nonempty: true}).ValidateString(t.Context(), validator.StringRequest{ConfigValue: types.StringValue(test.value)}, &resp)
		if resp.Diagnostics.HasError() == test.valid {
			t.Errorf("unexpected name validation for %q", test.value)
		}
	}
	for _, test := range []struct {
		value types.String
		valid bool
	}{{types.StringValue("*"), true}, {types.StringValue("*.internal"), true}, {types.StringValue("http://internal/*"), true}, {types.StringValue(""), false}, {types.StringValue("a*b"), false}, {types.StringValue("*a*"), false}, {types.StringValue(" x"), false}, {types.StringNull(), false}, {types.StringUnknown(), true}} {
		var resp validator.SetResponse
		(tunnelURLsValidator{}).ValidateSet(t.Context(), validator.SetRequest{ConfigValue: types.SetValueMust(types.StringType, []attr.Value{test.value})}, &resp)
		if resp.Diagnostics.HasError() == test.valid {
			t.Error("unexpected allowlist validation")
		}
	}
	for _, version := range []int64{-1, 0, 1} {
		var resp validator.Int64Response
		(tunnelRotationValidator{}).ValidateInt64(t.Context(), validator.Int64Request{ConfigValue: types.Int64Value(version)}, &resp)
		if resp.Diagnostics.HasError() != (version < 0) {
			t.Error("unexpected rotation validation")
		}
	}
}
