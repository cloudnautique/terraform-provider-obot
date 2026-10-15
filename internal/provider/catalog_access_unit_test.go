// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/obot-platform/terraform-provider-obot/internal/client"
)

func testManagementClient(t *testing.T, handler http.HandlerFunc) *client.Client {
	t.Helper()
	server := httptest.NewServer(handler)
	t.Cleanup(server.Close)
	conn, err := client.New(server.URL, "test-key", "test")
	if err != nil {
		t.Fatal(err)
	}
	return conn
}
func testManagementState(t *testing.T, r resource.Resource, model any) tfsdk.State {
	t.Helper()
	var schema resource.SchemaResponse
	r.Schema(t.Context(), resource.SchemaRequest{}, &schema)
	state := tfsdk.State{Schema: schema.Schema}
	if diagnostics := state.Set(t.Context(), model); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	return state
}
func testRuleModel(t *testing.T) accessRuleModel {
	t.Helper()
	var diagnostics diag.Diagnostics
	model := accessRuleModel{}
	model.fromAPI(t.Context(), &client.AccessRule{ID: "acr1-everything", CatalogID: "default", AccessRuleManifest: client.AccessRuleManifest{DisplayName: "Everything", Subjects: []client.RuleReference{{Type: "obotGroup", ID: "admin"}}, Resources: []client.RuleReference{{Type: "selector", ID: "*"}}}}, &diagnostics)
	if diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	return model
}
func testSourcesModel(t *testing.T) catalogSourcesModel {
	t.Helper()
	var diagnostics diag.Diagnostics
	model := catalogSourcesModel{}
	model.fromAPI(t.Context(), &client.Catalog{ID: "default", Sources: []string{"https://example.com/shipped.yaml"}, GitCredentialIDs: map[string]string{"https://example.com/shipped.yaml": "gc1-test"}}, &diagnostics)
	if diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	return model
}
func TestManagementImportIdentifiers(t *testing.T) {
	for _, test := range []struct {
		name, id    string
		rule, valid bool
	}{
		{"default catalog", "default", false, true}, {"other catalog", "other", false, false}, {"catalog path traversal", "../default", false, false},
		{"policy", "default/acr1-everything", true, true}, {"missing scope", "acr1-everything", true, false}, {"empty ID", "default/", true, false}, {"extra slash", "default/../acr1", true, false}, {"query injection", "default/acr1?x=y", true, false},
	} {
		t.Run(test.name, func(t *testing.T) {
			var r resource.Resource
			var state tfsdk.State
			if test.rule {
				r = newMCPAccessRuleResource()
				state = testManagementState(t, r, testRuleModel(t))
			} else {
				r = newMCPCatalogSourcesResource()
				state = testManagementState(t, r, testSourcesModel(t))
			}
			response := resource.ImportStateResponse{State: state}
			importer, ok := r.(resource.ResourceWithImportState)
			if !ok {
				t.Fatal("missing import support")
			}
			importer.ImportState(t.Context(), resource.ImportStateRequest{ID: test.id}, &response)
			if response.Diagnostics.HasError() == test.valid {
				t.Fatal(response.Diagnostics)
			}
		})
	}
}
func TestManagementReadAndDeleteErrors(t *testing.T) {
	for _, status := range []int{400, 401, 403, 404, 500} {
		for _, kind := range []string{"catalog", "rule"} {
			t.Run(kind+http.StatusText(status), func(t *testing.T) {
				conn := testManagementClient(t, func(w http.ResponseWriter, _ *http.Request) { w.WriteHeader(status) })
				var r resource.Resource
				var state tfsdk.State
				if kind == "catalog" {
					r = &mcpCatalogSourcesResource{client: conn}
					state = testManagementState(t, r, testSourcesModel(t))
				} else {
					r = &mcpAccessRuleResource{client: conn}
					state = testManagementState(t, r, testRuleModel(t))
				}
				read := resource.ReadResponse{State: state}
				r.Read(t.Context(), resource.ReadRequest{State: state}, &read)
				if read.Diagnostics.HasError() != (status != 404) || read.State.Raw.IsNull() != (status == 404) {
					t.Fatalf("incorrect missing-object read: %v", read.Diagnostics)
				}
				deleted := resource.DeleteResponse{State: state}
				r.Delete(t.Context(), resource.DeleteRequest{State: state}, &deleted)
				if deleted.Diagnostics.HasError() != (status != 404) {
					t.Fatal(deleted.Diagnostics)
				}
			})
		}
	}
}
func TestGeneratedRuleCannotBeManaged(t *testing.T) {
	mutations := 0
	conn := testManagementClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			mutations++
		}
		rule := client.AccessRule{ID: "acr1-everything", CatalogID: "default", Generated: true}
		_ = json.NewEncoder(w).Encode(rule)
	})
	r := &mcpAccessRuleResource{client: conn}
	state := testManagementState(t, r, testRuleModel(t))
	read := resource.ReadResponse{State: state}
	r.Read(t.Context(), resource.ReadRequest{State: state}, &read)
	updated := resource.UpdateResponse{State: state}
	r.Update(t.Context(), resource.UpdateRequest{Plan: tfsdk.Plan(state), State: state}, &updated)
	deleted := resource.DeleteResponse{State: state}
	r.Delete(t.Context(), resource.DeleteRequest{State: state}, &deleted)
	if !read.Diagnostics.HasError() || !updated.Diagnostics.HasError() || !deleted.Diagnostics.HasError() || mutations != 0 {
		t.Fatal("generated rule was managed")
	}
}
func TestCatalogAcceptedWriteSurvivesRefreshFailure(t *testing.T) {
	conn := testManagementClient(t, func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/refresh") {
			w.WriteHeader(403)
			return
		}
		sources := []string{}
		if r.Method == "PUT" {
			var input client.CatalogSources
			if err := json.NewDecoder(r.Body).Decode(&input); err != nil {
				t.Error(err)
			}
			sources = input.Sources
		}
		_ = json.NewEncoder(w).Encode(client.Catalog{ID: "default", Sources: sources, LastSynced: "2000-01-01T00:00:00Z"})
	})
	r := &mcpCatalogSourcesResource{client: conn}
	plan := testManagementState(t, r, testSourcesModel(t))
	response := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(t.Context(), resource.CreateRequest{Plan: tfsdk.Plan(plan)}, &response)
	if !response.Diagnostics.HasError() {
		t.Fatal("refresh failure ignored")
	}
	var saved catalogSourcesModel
	if diagnostics := response.State.Get(t.Context(), &saved); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	if saved.CatalogID.ValueString() != "default" || saved.Sources.IsNull() {
		t.Fatal("accepted singleton write lost from state")
	}
}
func TestCatalogAdoptionAndReadAreSideEffectFree(t *testing.T) {
	mutations := 0
	model := testSourcesModel(t)
	conn := testManagementClient(t, func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "GET" {
			mutations++
		}
		_ = json.NewEncoder(w).Encode(client.Catalog{ID: "default", Sources: []string{"https://example.com/shipped.yaml"}, GitCredentialIDs: map[string]string{"https://example.com/shipped.yaml": "gc1-test"}})
	})
	r := &mcpCatalogSourcesResource{client: conn}
	state := testManagementState(t, r, model)
	created := resource.CreateResponse{State: state}
	r.Create(t.Context(), resource.CreateRequest{Plan: tfsdk.Plan(state)}, &created)
	read := resource.ReadResponse{State: created.State}
	r.Read(t.Context(), resource.ReadRequest{State: created.State}, &read)
	if created.Diagnostics.HasError() || read.Diagnostics.HasError() || mutations != 0 {
		t.Fatalf("matching adoption or read mutated the catalog: %v %v", created.Diagnostics, read.Diagnostics)
	}
}
func TestRuleReferenceValidation(t *testing.T) {
	for _, test := range []struct {
		kind, id        string
		subjects, valid bool
	}{
		{"selector", "*", true, true}, {"selector", "everyone", true, false}, {"user", "1", true, true}, {"group", "entra/group-id", true, true}, {"obotGroup", "admin", true, true}, {"profile", "foo", true, false}, {"user", "", true, false},
		{"selector", "*", false, true}, {"mcpServerCatalogEntry", "entry1", false, true}, {"mcpServer", "server1", false, true}, {"mcpCatalog", "default", false, false}, {"selector", "anything", false, false},
	} {
		t.Run(test.kind+test.id, func(t *testing.T) {
			var response validator.SetResponse
			value := types.SetValueMust(types.ObjectType{AttrTypes: referenceAttrTypes}, []attr.Value{types.ObjectValueMust(referenceAttrTypes, map[string]attr.Value{"type": types.StringValue(test.kind), "id": types.StringValue(test.id)})})
			(ruleReferencesValidator{subjects: test.subjects}).ValidateSet(t.Context(), validator.SetRequest{ConfigValue: value}, &response)
			if response.Diagnostics.HasError() == test.valid {
				t.Fatal(response.Diagnostics)
			}
		})
	}
	for _, value := range []types.Set{types.SetNull(types.ObjectType{AttrTypes: referenceAttrTypes}), types.SetUnknown(types.ObjectType{AttrTypes: referenceAttrTypes}), types.SetValueMust(types.ObjectType{AttrTypes: referenceAttrTypes}, []attr.Value{})} {
		var response validator.SetResponse
		(ruleReferencesValidator{}).ValidateSet(t.Context(), validator.SetRequest{ConfigValue: value}, &response)
		if response.Diagnostics.HasError() {
			t.Fatal(response.Diagnostics)
		}
	}
}
func TestCatalogSourceValidation(t *testing.T) {
	for _, test := range []struct {
		source string
		valid  bool
	}{{"https://github.com/org/catalog/tree/main", true}, {"https://example.com/catalog.yaml", true}, {"github.com/org/catalog", false}, {"http://example.com/catalog.yaml", false}, {"https://user:secret@example.com/catalog.yaml", false}, {"", false}, {" https://example.com/catalog", false}} {
		var response validator.SetResponse
		(catalogSourcesValidator{}).ValidateSet(t.Context(), validator.SetRequest{ConfigValue: types.SetValueMust(types.StringType, []attr.Value{types.StringValue(test.source)})}, &response)
		if response.Diagnostics.HasError() == test.valid {
			t.Errorf("unexpected validation for %q: %v", test.source, response.Diagnostics)
		}
	}
}
func TestCatalogDataSourceInventory(t *testing.T) {
	for _, empty := range []bool{true, false} {
		t.Run(map[bool]string{true: "empty", false: "sorted"}[empty], func(t *testing.T) {
			calls := 0
			conn := testManagementClient(t, func(w http.ResponseWriter, r *http.Request) {
				calls++
				if r.Method != "GET" {
					t.Error("lookup mutated API")
				}
				if strings.HasSuffix(r.URL.Path, "/entries") {
					if r.URL.Query().Get("all") != "true" {
						t.Error("inventory must bypass access-policy filtering for administrators")
					}
					if empty {
						_, _ = w.Write([]byte(`{"items":null}`))
					} else {
						_, _ = w.Write([]byte(`{"items":[{"id":"entry2","manifest":{"name":"Second"},"sourceURL":"https://example.com"},{"id":"entry1","manifest":{"name":"First"}}]}`))
					}
					return
				}
				_, _ = w.Write([]byte(`{"id":"default","sourceURLs":null,"sourceURLCredentials":{"url":"secret-token"}}`))
			})
			d := &mcpCatalogDataSource{client: conn}
			var schema datasource.SchemaResponse
			d.Schema(t.Context(), datasource.SchemaRequest{}, &schema)
			state := tfsdk.State{Schema: schema.Schema}
			model := catalogDataModel{ID: types.StringValue("default"), Sources: types.SetNull(types.StringType), GitCredentialIDs: types.MapNull(types.StringType), SyncErrors: types.MapNull(types.StringType), Entries: types.ListNull(types.ObjectType{AttrTypes: map[string]attr.Type{"id": types.StringType, "name": types.StringType, "source_url": types.StringType}})}
			if ds := state.Set(t.Context(), &model); ds.HasError() {
				t.Fatal(ds)
			}
			response := datasource.ReadResponse{State: state}
			d.Read(t.Context(), datasource.ReadRequest{Config: tfsdk.Config(state)}, &response)
			if response.Diagnostics.HasError() {
				t.Fatal(response.Diagnostics)
			}
			if ds := response.State.Get(t.Context(), &model); ds.HasError() {
				t.Fatal(ds)
			}
			if calls != 2 || model.Sources.IsNull() || model.Entries.IsNull() {
				t.Fatal("incorrect empty collection semantics")
			}
			if !empty {
				first, ok := model.Entries.Elements()[0].(types.Object)
				if !ok || !first.Attributes()["id"].Equal(types.StringValue("entry1")) {
					t.Fatal("inventory is not sorted")
				}
			}
		})
	}
}

// Verify the framework schema of every new object, including nested collections.
func TestManagementSchemas(t *testing.T) {
	for _, r := range []resource.Resource{newMCPCatalogSourcesResource(), newMCPAccessRuleResource()} {
		var response resource.SchemaResponse
		r.Schema(t.Context(), resource.SchemaRequest{}, &response)
		if d := response.Schema.ValidateImplementation(context.Background()); d.HasError() {
			t.Fatal(d)
		}
	}
	for _, d := range []datasource.DataSource{newMCPCatalogDataSource(), newCurrentAuthProviderDataSource(), newMCPAccessRuleDataSource(), newMCPAccessRulesDataSource()} {
		var response datasource.SchemaResponse
		d.Schema(t.Context(), datasource.SchemaRequest{}, &response)
		if ds := response.Schema.ValidateImplementation(context.Background()); ds.HasError() {
			t.Fatal(ds)
		}
	}
}

func TestAccessRuleDataSourcesCollections(t *testing.T) {
	for _, test := range []struct {
		name, body        string
		plural, wantError bool
		count             int
	}{
		{"empty", `{"items":null}`, true, false, 0},
		{"sorted", `{"items":[{"id":"acr2","mcpCatalogID":"default","displayName":"Second"},{"id":"acr1","mcpCatalogID":"default","displayName":"First"}]}`, true, false, 2},
		{"single", `{"id":"acr1","mcpCatalogID":"default","displayName":"First","subjects":[{"type":"group","id":"okta/team"}],"resources":[]}`, false, false, 0},
		{"scope mismatch", `{"id":"acr1","mcpCatalogID":"other"}`, false, true, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			conn := testManagementClient(t, func(w http.ResponseWriter, r *http.Request) {
				if r.Method != "GET" {
					t.Error("data source mutated API")
				}
				_, _ = w.Write([]byte(test.body))
			})
			d := &mcpAccessRuleDataSource{client: conn, plural: test.plural}
			var schema datasource.SchemaResponse
			d.Schema(t.Context(), datasource.SchemaRequest{}, &schema)
			var diagnostics diag.Diagnostics
			object := accessRuleDataValue(t.Context(), client.AccessRule{ID: "acr1", CatalogID: "default"}, &diagnostics)
			var state tfsdk.State
			if test.plural {
				raw, err := types.ObjectValueMust(map[string]attr.Type{"catalog_id": types.StringType, "rules": types.ListType{ElemType: object.Type(t.Context())}}, map[string]attr.Value{"catalog_id": types.StringNull(), "rules": types.ListNull(object.Type(t.Context()))}).ToTerraformValue(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				state = tfsdk.State{Schema: schema.Schema, Raw: raw}
			} else {
				attrs := object.Attributes()
				attrs["catalog_id"] = types.StringNull()
				raw, err := types.ObjectValueMust(object.AttributeTypes(t.Context()), attrs).ToTerraformValue(t.Context())
				if err != nil {
					t.Fatal(err)
				}
				state = tfsdk.State{Schema: schema.Schema, Raw: raw}
			}
			response := datasource.ReadResponse{State: state}
			d.Read(t.Context(), datasource.ReadRequest{Config: tfsdk.Config(state)}, &response)
			if response.Diagnostics.HasError() != test.wantError {
				t.Fatal(response.Diagnostics)
			}
			if test.wantError {
				return
			}
			if test.plural {
				var list types.List
				if ds := response.State.GetAttribute(t.Context(), path.Root("rules"), &list); ds.HasError() {
					t.Fatal(ds)
				}
				if list.IsNull() || len(list.Elements()) != test.count {
					t.Fatal("incorrect plural collection shape")
				}
				if test.count > 0 {
					first, ok := list.Elements()[0].(types.Object)
					if !ok || !first.Attributes()["id"].Equal(types.StringValue("acr1")) {
						t.Fatal("policies not sorted")
					}
				}
			} else {
				var subjects types.Set
				if ds := response.State.GetAttribute(t.Context(), path.Root("subjects"), &subjects); ds.HasError() {
					t.Fatal(ds)
				}
				if len(subjects.Elements()) != 1 {
					t.Fatal("subject metadata missing")
				}
			}
		})
	}
}
func TestCatalogCredentialReferencesValidation(t *testing.T) {
	r := newMCPCatalogSourcesResource()
	for _, test := range []struct {
		name      string
		ids       types.Map
		sources   types.Set
		wantError bool
	}{
		{"matching", types.MapValueMust(types.StringType, map[string]attr.Value{"https://example.com/catalog": types.StringValue("gc1-test")}), types.SetValueMust(types.StringType, []attr.Value{types.StringValue("https://example.com/catalog")}), false},
		{"missing source", types.MapValueMust(types.StringType, map[string]attr.Value{"https://example.com/catalog": types.StringValue("gc1-test")}), types.SetValueMust(types.StringType, []attr.Value{}), true},
		{"bad ID", types.MapValueMust(types.StringType, map[string]attr.Value{"https://example.com/catalog": types.StringValue("../credential")}), types.SetValueMust(types.StringType, []attr.Value{types.StringValue("https://example.com/catalog")}), true},
		{"unknown credential ID", types.MapValueMust(types.StringType, map[string]attr.Value{"https://example.com/catalog": types.StringUnknown()}), types.SetValueMust(types.StringType, []attr.Value{types.StringValue("https://example.com/catalog")}), false},
		{"unknown source", types.MapValueMust(types.StringType, map[string]attr.Value{"https://example.com/catalog": types.StringValue("gc1-test")}), types.SetValueMust(types.StringType, []attr.Value{types.StringUnknown()}), false},
	} {
		t.Run(test.name, func(t *testing.T) {
			state := testManagementState(t, r, catalogSourcesModel{CatalogID: types.StringValue("default"), Sources: test.sources, GitCredentialIDs: test.ids})
			response := resource.ValidateConfigResponse{}
			(&mcpCatalogSourcesResource{}).ValidateConfig(t.Context(), resource.ValidateConfigRequest{Config: tfsdk.Config(state)}, &response)
			if response.Diagnostics.HasError() != test.wantError {
				t.Fatal(response.Diagnostics)
			}
		})
	}
}
