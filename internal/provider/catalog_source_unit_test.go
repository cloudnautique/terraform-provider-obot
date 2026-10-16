// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"encoding/json"
	"net/http"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/obot-platform/terraform-provider-obot/internal/client"
)

const (
	testSourceURL = "https://example.com/terraform.yaml"
	testOtherURL  = "https://example.com/ui-added.yaml"
)

func testSourceModel() catalogSourceModel {
	return catalogSourceModel{ID: types.StringValue(testSourceURL), URL: types.StringValue(testSourceURL), GitCredentialID: types.StringValue("gc1-test")}
}

// fakeCatalog keeps default catalog state across requests, as Obot does.
type fakeCatalog struct {
	mu      sync.Mutex
	t       *testing.T
	catalog client.Catalog
	// writes holds each PUT body, to check exactly what was sent.
	writes []map[string]json.RawMessage
	// failing lists sources the next sync reports errors for.
	failing map[string]string
	// afterSync runs after each sync, to simulate another writer.
	afterSync  func(*client.Catalog)
	refreshErr int
}

func newFakeCatalog(t *testing.T, sources []string, ids map[string]string) (*fakeCatalog, *client.Client) {
	t.Helper()
	fake := &fakeCatalog{t: t, catalog: client.Catalog{ID: "default", Sources: sources, GitCredentialIDs: ids}}
	return fake, testManagementClient(t, fake.serve)
}

func (f *fakeCatalog) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	switch {
	case r.Method == http.MethodGet && r.URL.Path == "/api/mcp-catalogs/default":
	case r.Method == http.MethodPut && r.URL.Path == "/api/mcp-catalogs/default":
		var body map[string]json.RawMessage
		var sources client.CatalogSources
		raw := json.NewDecoder(r.Body)
		if err := raw.Decode(&body); err != nil {
			f.t.Error(err)
		}
		encoded, _ := json.Marshal(body)
		if err := json.Unmarshal(encoded, &sources); err != nil {
			f.t.Error(err)
		}
		f.writes = append(f.writes, body)
		f.catalog.Sources, f.catalog.GitCredentialIDs = sources.Sources, sources.GitCredentialIDs
	case r.Method == http.MethodPost && r.URL.Path == "/api/mcp-catalogs/default/refresh":
		if f.refreshErr != 0 {
			w.WriteHeader(f.refreshErr)
			return
		}
		f.catalog.LastSynced = time.Now().UTC().Format(time.RFC3339Nano)
		f.catalog.SyncErrors = map[string]string{}
		for url, message := range f.failing {
			if slices.Contains(f.catalog.Sources, url) {
				f.catalog.SyncErrors[url] = message
			}
		}
		if f.afterSync != nil {
			f.afterSync(&f.catalog)
		}
		w.WriteHeader(http.StatusNoContent)
		return
	default:
		f.t.Errorf("unexpected request %s %s", r.Method, r.URL.Path)
		w.WriteHeader(http.StatusNotFound)
		return
	}
	_ = json.NewEncoder(w).Encode(f.catalog)
}

func testCreateSource(t *testing.T, conn *client.Client, model catalogSourceModel) resource.CreateResponse {
	t.Helper()
	r := &mcpCatalogSourceResource{client: conn}
	plan := testManagementState(t, r, model)
	response := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(t.Context(), resource.CreateRequest{Plan: tfsdk.Plan(plan)}, &response)
	return response
}

func TestCatalogSourceCreateKeepsOtherSources(t *testing.T) {
	fake, conn := newFakeCatalog(t, []string{testOtherURL}, map[string]string{testOtherURL: "gc1-other"})
	response := testCreateSource(t, conn, testSourceModel())
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	if !slices.Equal(fake.catalog.Sources, []string{testOtherURL, testSourceURL}) {
		t.Fatalf("sources = %v, want the UI-added source kept and this one appended", fake.catalog.Sources)
	}
	if fake.catalog.GitCredentialIDs[testOtherURL] != "gc1-other" || fake.catalog.GitCredentialIDs[testSourceURL] != "gc1-test" {
		t.Fatalf("credential references = %v", fake.catalog.GitCredentialIDs)
	}
	if len(fake.writes) != 1 {
		t.Fatalf("writes = %d, want 1", len(fake.writes))
	}
	if _, ok := fake.writes[0]["sourceURLCredentials"]; ok {
		t.Fatal("sent inline credentials, which would replace tokens stored for other sources")
	}
	var saved catalogSourceModel
	if diagnostics := response.State.Get(t.Context(), &saved); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	if saved != testSourceModel() {
		t.Fatalf("state = %v", saved)
	}
}

func TestCatalogSourceCreateRejectsExistingSource(t *testing.T) {
	fake, conn := newFakeCatalog(t, []string{testSourceURL}, nil)
	response := testCreateSource(t, conn, testSourceModel())
	if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics.Errors()[0].Detail(), "Import") {
		t.Fatalf("expected an import hint, got %v", response.Diagnostics)
	}
	if len(fake.writes) != 0 || !response.State.Raw.IsNull() {
		t.Fatal("took over a source Terraform did not create")
	}
}

func TestCatalogSourceDeleteRemovesOnlyItself(t *testing.T) {
	fake, conn := newFakeCatalog(t, []string{testOtherURL, testSourceURL}, map[string]string{testOtherURL: "gc1-other", testSourceURL: "gc1-test"})
	r := &mcpCatalogSourceResource{client: conn}
	state := testManagementState(t, r, testSourceModel())
	response := resource.DeleteResponse{State: state}
	r.Delete(t.Context(), resource.DeleteRequest{State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	if !slices.Equal(fake.catalog.Sources, []string{testOtherURL}) || len(fake.catalog.GitCredentialIDs) != 1 || fake.catalog.GitCredentialIDs[testOtherURL] != "gc1-other" {
		t.Fatalf("catalog after delete = %v %v", fake.catalog.Sources, fake.catalog.GitCredentialIDs)
	}

	// Deleting a source already removed elsewhere changes nothing.
	r.Delete(t.Context(), resource.DeleteRequest{State: state}, &response)
	if response.Diagnostics.HasError() || len(fake.writes) != 1 {
		t.Fatalf("repeated delete wrote again: %v", response.Diagnostics)
	}
}

func TestCatalogSourceUpdateChangesOnlyItsCredential(t *testing.T) {
	fake, conn := newFakeCatalog(t, []string{testSourceURL, testOtherURL}, map[string]string{testSourceURL: "gc1-test", testOtherURL: "gc1-other"})
	r := &mcpCatalogSourceResource{client: conn}
	state := testManagementState(t, r, testSourceModel())
	model := testSourceModel()
	model.GitCredentialID = types.StringNull()
	plan := testManagementState(t, r, model)
	response := resource.UpdateResponse{State: state}
	r.Update(t.Context(), resource.UpdateRequest{Plan: tfsdk.Plan(plan), State: state}, &response)
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	if !slices.Equal(fake.catalog.Sources, []string{testSourceURL, testOtherURL}) {
		t.Fatalf("update reordered or changed sources: %v", fake.catalog.Sources)
	}
	if _, ok := fake.catalog.GitCredentialIDs[testSourceURL]; ok || fake.catalog.GitCredentialIDs[testOtherURL] != "gc1-other" {
		t.Fatalf("credential references = %v", fake.catalog.GitCredentialIDs)
	}
}

func TestCatalogSourceReadIgnoresOtherSources(t *testing.T) {
	for _, test := range []struct {
		name    string
		sources []string
		ids     map[string]string
		removed bool
		wantID  types.String
	}{
		{name: "present", sources: []string{testOtherURL, testSourceURL}, ids: map[string]string{testSourceURL: "gc1-new", testOtherURL: "gc1-other"}, wantID: types.StringValue("gc1-new")},
		{name: "credential removed", sources: []string{testSourceURL}, wantID: types.StringNull()},
		{name: "removed in UI", sources: []string{testOtherURL}, removed: true},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake, conn := newFakeCatalog(t, test.sources, test.ids)
			r := &mcpCatalogSourceResource{client: conn}
			state := testManagementState(t, r, testSourceModel())
			response := resource.ReadResponse{State: state}
			r.Read(t.Context(), resource.ReadRequest{State: state}, &response)
			if response.Diagnostics.HasError() || len(fake.writes) != 0 {
				t.Fatalf("read failed or wrote: %v", response.Diagnostics)
			}
			if response.State.Raw.IsNull() != test.removed {
				t.Fatalf("removed = %v, want %v", response.State.Raw.IsNull(), test.removed)
			}
			if test.removed {
				return
			}
			var saved catalogSourceModel
			if diagnostics := response.State.Get(t.Context(), &saved); diagnostics.HasError() {
				t.Fatal(diagnostics)
			}
			if !saved.GitCredentialID.Equal(test.wantID) {
				t.Fatalf("git_credential_id = %v, want %v", saved.GitCredentialID, test.wantID)
			}
		})
	}
}

func TestCatalogSourceSyncErrors(t *testing.T) {
	for _, test := range []struct {
		name      string
		failing   string
		wantError bool
	}{
		{name: "this source", failing: testSourceURL, wantError: true},
		{name: "another source", failing: testOtherURL},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake, conn := newFakeCatalog(t, []string{testOtherURL}, nil)
			fake.failing = map[string]string{test.failing: "repository not found"}
			response := testCreateSource(t, conn, testSourceModel())
			if response.Diagnostics.HasError() != test.wantError {
				t.Fatalf("diagnostics = %v", response.Diagnostics)
			}
			if test.wantError && !strings.Contains(response.Diagnostics.Errors()[0].Detail(), "repository not found") {
				t.Fatal("sync error detail missing")
			}
			if response.State.Raw.IsNull() {
				t.Fatal("accepted source lost from state")
			}
		})
	}
}

func TestCatalogSourceAcceptedWriteSurvivesRefreshFailure(t *testing.T) {
	fake, conn := newFakeCatalog(t, nil, nil)
	fake.refreshErr = http.StatusForbidden
	response := testCreateSource(t, conn, testSourceModel())
	if !response.Diagnostics.HasError() {
		t.Fatal("refresh failure ignored")
	}
	if response.State.Raw.IsNull() {
		t.Fatal("accepted source lost from state")
	}
}

// Obot has no conflict detection, so an edit replaced by another writer is re-applied.
func TestCatalogSourceReappliesLostEdit(t *testing.T) {
	fake, conn := newFakeCatalog(t, []string{testOtherURL}, nil)
	clobbered := false
	fake.afterSync = func(catalog *client.Catalog) {
		if !clobbered {
			clobbered = true
			catalog.Sources = []string{testOtherURL, "https://example.com/concurrent.yaml"}
		}
	}
	response := testCreateSource(t, conn, testSourceModel())
	if response.Diagnostics.HasError() {
		t.Fatal(response.Diagnostics)
	}
	want := []string{testOtherURL, "https://example.com/concurrent.yaml", testSourceURL}
	if !slices.Equal(fake.catalog.Sources, want) || len(fake.writes) != 2 {
		t.Fatalf("sources = %v after %d writes, want %v", fake.catalog.Sources, len(fake.writes), want)
	}
}

// Resources in one apply share a client; serialized edits must not lose each other.
func TestCatalogSourceConcurrentCreates(t *testing.T) {
	fake, conn := newFakeCatalog(t, []string{testOtherURL}, nil)
	urls := []string{"https://example.com/a.yaml", "https://example.com/b.yaml", "https://example.com/c.yaml"}
	var wg sync.WaitGroup
	for _, url := range urls {
		wg.Go(func() {
			model := catalogSourceModel{ID: types.StringUnknown(), URL: types.StringValue(url), GitCredentialID: types.StringNull()}
			if response := testCreateSource(t, conn, model); response.Diagnostics.HasError() {
				t.Error(response.Diagnostics)
			}
		})
	}
	wg.Wait()
	for _, url := range append(urls, testOtherURL) {
		if !slices.Contains(fake.catalog.Sources, url) {
			t.Errorf("lost source %s: %v", url, fake.catalog.Sources)
		}
	}
}

func TestCatalogSourceURLValidation(t *testing.T) {
	for _, test := range []struct {
		source string
		valid  bool
	}{{"https://github.com/org/catalog/tree/main", true}, {"https://example.com/catalog.yaml", true}, {"github.com/org/catalog", false}, {"http://example.com/catalog.yaml", false}, {"https://user:secret@example.com/catalog.yaml", false}, {"", false}, {" https://example.com/catalog", false}, {"https://github.com/obot-platform/mcp-catalog", false}, {"https://github.com/obot-platform/mcp-catalog/v2-schema", true}} {
		var response validator.StringResponse
		(catalogSourceURL{}).ValidateString(t.Context(), validator.StringRequest{ConfigValue: types.StringValue(test.source)}, &response)
		if response.Diagnostics.HasError() == test.valid {
			t.Errorf("unexpected validation for %q: %v", test.source, response.Diagnostics)
		}
	}
}
