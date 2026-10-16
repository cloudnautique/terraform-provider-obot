// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"testing"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/obot-platform/terraform-provider-obot/internal/client"
)

// fakeGitCredentials keeps Git credentials across requests, as Obot does.
type fakeGitCredentials struct {
	mu          sync.Mutex
	t           *testing.T
	credentials map[string]*client.GitCredential
	tokens      map[string]string
	// writes holds each create or update body.
	writes []map[string]string
	inUse  bool
	nextID int
}

func newFakeGitCredentials(t *testing.T) (*fakeGitCredentials, *client.Client) {
	t.Helper()
	fake := &fakeGitCredentials{t: t, credentials: map[string]*client.GitCredential{}, tokens: map[string]string{}}
	return fake, testManagementClient(t, fake.serve)
}

func (f *fakeGitCredentials) add(id, name, token string) {
	f.credentials[id] = &client.GitCredential{ID: id, DisplayName: name, Host: "github.com", TokenConfigured: token != ""}
	f.tokens[id] = token
}

func (f *fakeGitCredentials) serve(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if !strings.HasPrefix(r.URL.Path, "/api/git-credentials") {
		w.WriteHeader(http.StatusNotFound)
		return
	}
	id := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, "/api/git-credentials"), "/")
	var body map[string]string
	if r.Method == http.MethodPost || r.Method == http.MethodPatch {
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			f.t.Error(err)
		}
		f.writes = append(f.writes, body)
	}
	switch {
	case r.Method == http.MethodGet && id == "":
		items := []client.GitCredential{}
		for _, credential := range f.credentials {
			items = append(items, *credential)
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"items": items})
		return
	case r.Method == http.MethodPost:
		f.nextID++
		id = fmt.Sprintf("gc1test%d", f.nextID)
		f.add(id, body["displayName"], body["token"])
		f.credentials[id].Host = body["host"]
		w.WriteHeader(http.StatusCreated)
	case f.credentials[id] == nil:
		http.Error(w, "not found", http.StatusNotFound)
		return
	case r.Method == http.MethodPatch:
		f.credentials[id].DisplayName = body["displayName"]
		if body["token"] != "" {
			f.tokens[id] = body["token"]
			f.credentials[id].TokenConfigured = true
		}
	case r.Method == http.MethodDelete:
		if f.inUse {
			http.Error(w, "Git credential is still used by 1 resources", http.StatusConflict)
			return
		}
		delete(f.credentials, id)
		w.WriteHeader(http.StatusNoContent)
		return
	}
	_ = json.NewEncoder(w).Encode(f.credentials[id])
}

func testGitCredentialModel() gitCredentialModel {
	return gitCredentialModel{
		ID:             types.StringValue("gc1test1"),
		DisplayName:    types.StringValue("GitHub"),
		Host:           types.StringValue("github.com"),
		Token:          types.StringValue("first-token"),
		TokenWO:        types.StringNull(),
		TokenWOVersion: types.Int64Null(),
	}
}

func TestGitCredentialCreateAndUpdateSendTokenOnlyWhenChanged(t *testing.T) {
	fake, conn := newFakeGitCredentials(t)
	r := &gitCredentialResource{client: conn}
	model := testGitCredentialModel()
	model.ID = types.StringUnknown()
	plan := testManagementState(t, r, model)
	created := resource.CreateResponse{State: tfsdk.State{Schema: plan.Schema}}
	r.Create(t.Context(), resource.CreateRequest{Plan: tfsdk.Plan(plan), Config: tfsdk.Config(plan)}, &created)
	if created.Diagnostics.HasError() || fake.tokens["gc1test1"] != "first-token" {
		t.Fatalf("create: %v %v", created.Diagnostics, fake.tokens)
	}

	update := func(change func(*gitCredentialModel)) {
		t.Helper()
		var state gitCredentialModel
		if diagnostics := created.State.Get(t.Context(), &state); diagnostics.HasError() {
			t.Fatal(diagnostics)
		}
		next := state
		change(&next)
		planned := testManagementState(t, r, next)
		response := resource.UpdateResponse{State: created.State}
		r.Update(t.Context(), resource.UpdateRequest{Plan: tfsdk.Plan(planned), Config: tfsdk.Config(planned), State: created.State}, &response)
		if response.Diagnostics.HasError() {
			t.Fatal(response.Diagnostics)
		}
		created.State = response.State
	}
	update(func(m *gitCredentialModel) { m.DisplayName = types.StringValue("Renamed") })
	if token, sent := fake.writes[1]["token"]; sent || token != "" || fake.credentials["gc1test1"].DisplayName != "Renamed" {
		t.Fatalf("rename sent a token or lost the name: %v", fake.writes[1])
	}
	update(func(m *gitCredentialModel) { m.Token = types.StringValue("second-token") })
	if fake.tokens["gc1test1"] != "second-token" {
		t.Fatal("changed token not sent")
	}
}

func TestGitCredentialWriteOnlyTokenSentOnVersionChange(t *testing.T) {
	fake, conn := newFakeGitCredentials(t)
	fake.add("gc1test1", "GitHub", "old-token")
	r := &gitCredentialResource{client: conn}
	stateModel := testGitCredentialModel()
	stateModel.Token = types.StringNull()
	stateModel.TokenWOVersion = types.Int64Value(1)
	state := testManagementState(t, r, stateModel)
	for _, test := range []struct {
		version int64
		want    string
	}{{1, "old-token"}, {2, "write-only-token"}} {
		planModel := stateModel
		planModel.DisplayName = types.StringValue("Renamed")
		planModel.TokenWOVersion = types.Int64Value(test.version)
		plan := testManagementState(t, r, planModel)
		configModel := planModel
		configModel.ID = types.StringNull()
		configModel.TokenWO = types.StringValue("write-only-token")
		config := testManagementState(t, r, configModel)
		response := resource.UpdateResponse{State: state}
		r.Update(t.Context(), resource.UpdateRequest{Plan: tfsdk.Plan(plan), Config: tfsdk.Config(config), State: state}, &response)
		if response.Diagnostics.HasError() {
			t.Fatal(response.Diagnostics)
		}
		if fake.tokens["gc1test1"] != test.want {
			t.Fatalf("version %d: token = %q, want %q", test.version, fake.tokens["gc1test1"], test.want)
		}
		var saved gitCredentialModel
		if diagnostics := response.State.Get(t.Context(), &saved); diagnostics.HasError() {
			t.Fatal(diagnostics)
		}
		if !saved.TokenWO.IsNull() {
			t.Fatal("write-only token stored in state")
		}
	}
}

func TestGitCredentialReadPlansTokenWhenObotLostIt(t *testing.T) {
	for _, configured := range []bool{true, false} {
		t.Run(fmt.Sprint("configured=", configured), func(t *testing.T) {
			fake, conn := newFakeGitCredentials(t)
			fake.add("gc1test1", "Renamed in UI", "first-token")
			fake.credentials["gc1test1"].TokenConfigured = configured
			r := &gitCredentialResource{client: conn}
			state := testManagementState(t, r, testGitCredentialModel())
			response := resource.ReadResponse{State: state}
			r.Read(t.Context(), resource.ReadRequest{State: state}, &response)
			var saved gitCredentialModel
			if diagnostics := response.State.Get(t.Context(), &saved); diagnostics.HasError() {
				t.Fatal(diagnostics)
			}
			if saved.DisplayName.ValueString() != "Renamed in UI" || saved.Token.IsNull() != !configured {
				t.Fatalf("state = %v", saved)
			}
		})
	}
}

func TestGitCredentialDeleteInUse(t *testing.T) {
	fake, conn := newFakeGitCredentials(t)
	fake.add("gc1test1", "GitHub", "token")
	fake.inUse = true
	r := &gitCredentialResource{client: conn}
	state := testManagementState(t, r, testGitCredentialModel())
	response := resource.DeleteResponse{State: state}
	r.Delete(t.Context(), resource.DeleteRequest{State: state}, &response)
	if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics.Errors()[0].Detail(), "still used") {
		t.Fatalf("expected Obot's in-use explanation, got %v", response.Diagnostics)
	}
	fake.inUse = false
	response = resource.DeleteResponse{State: state}
	r.Delete(t.Context(), resource.DeleteRequest{State: state}, &response)
	if response.Diagnostics.HasError() || fake.credentials["gc1test1"] != nil {
		t.Fatal(response.Diagnostics)
	}
}

func TestGitCredentialDataSourceLookup(t *testing.T) {
	for _, test := range []struct {
		name, id, displayName, wantID, wantError string
	}{
		{name: "by ID", id: "gc1test2", wantID: "gc1test2"},
		{name: "by unique name", displayName: "GitLab", wantID: "gc1test3"},
		{name: "ambiguous name", displayName: "GitHub", wantError: "2 Git credentials"},
		{name: "missing name", displayName: "Bitbucket", wantError: "no Git credential"},
		{name: "missing ID", id: "gc1missing", wantError: "no Git credential"},
	} {
		t.Run(test.name, func(t *testing.T) {
			fake, conn := newFakeGitCredentials(t)
			fake.add("gc1test1", "GitHub", "token")
			fake.add("gc1test2", "GitHub", "token")
			fake.add("gc1test3", "GitLab", "token")
			fake.credentials["gc1test3"].Uses.MCPCatalogs = []client.GitCredentialUse{{ID: "default", DisplayName: "Default"}}
			d := &gitCredentialDataSource{client: conn}
			var schema datasource.SchemaResponse
			d.Schema(t.Context(), datasource.SchemaRequest{}, &schema)
			config := gitCredentialDataModel{ID: types.StringNull(), DisplayName: types.StringNull(), Host: types.StringNull(), TokenConfigured: types.BoolNull(), UsedBy: types.ListNull(types.ObjectType{AttrTypes: gitCredentialUseModel{}.attrTypes()})}
			if test.id != "" {
				config.ID = types.StringValue(test.id)
			} else {
				config.DisplayName = types.StringValue(test.displayName)
			}
			state := tfsdk.State{Schema: schema.Schema}
			if diagnostics := state.Set(t.Context(), &config); diagnostics.HasError() {
				t.Fatal(diagnostics)
			}
			response := datasource.ReadResponse{State: state}
			d.Read(t.Context(), datasource.ReadRequest{Config: tfsdk.Config(state)}, &response)
			if test.wantError != "" {
				if !response.Diagnostics.HasError() || !strings.Contains(response.Diagnostics.Errors()[0].Detail(), test.wantError) {
					t.Fatalf("expected %q, got %v", test.wantError, response.Diagnostics)
				}
				return
			}
			var model gitCredentialDataModel
			if diagnostics := response.State.Get(t.Context(), &model); diagnostics.HasError() {
				t.Fatal(diagnostics)
			}
			if model.ID.ValueString() != test.wantID || !model.TokenConfigured.ValueBool() {
				t.Fatalf("model = %v", model)
			}
			if test.wantID == "gc1test3" && len(model.UsedBy.Elements()) != 1 {
				t.Fatal("uses missing")
			}
		})
	}
}

func TestGitCredentialsDataSourceList(t *testing.T) {
	fake, conn := newFakeGitCredentials(t)
	fake.add("gc1b", "B", "token")
	fake.add("gc1a", "A", "")
	d := &gitCredentialsDataSource{client: conn}
	var schema datasource.SchemaResponse
	d.Schema(t.Context(), datasource.SchemaRequest{}, &schema)
	state := tfsdk.State{Schema: schema.Schema}
	if diagnostics := state.Set(t.Context(), &gitCredentialsDataModel{Credentials: types.ListNull(types.ObjectType{AttrTypes: gitCredentialDataModel{}.attrTypes()})}); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	response := datasource.ReadResponse{State: state}
	d.Read(t.Context(), datasource.ReadRequest{Config: tfsdk.Config(state)}, &response)
	var model gitCredentialsDataModel
	if diagnostics := response.State.Get(t.Context(), &model); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	var credentials []gitCredentialDataModel
	if diagnostics := model.Credentials.ElementsAs(t.Context(), &credentials, false); diagnostics.HasError() {
		t.Fatal(diagnostics)
	}
	if len(credentials) != 2 || credentials[0].ID.ValueString() != "gc1a" || credentials[0].TokenConfigured.ValueBool() {
		t.Fatalf("credentials = %v", credentials)
	}
}

func TestGitCredentialSchemaAndImport(t *testing.T) {
	r := &gitCredentialResource{}
	var schema resource.SchemaResponse
	r.Schema(t.Context(), resource.SchemaRequest{}, &schema)
	if d := schema.Schema.ValidateImplementation(context.Background()); d.HasError() {
		t.Fatal(d)
	}
	for _, d := range []datasource.DataSource{newGitCredentialDataSource(), newGitCredentialsDataSource()} {
		var response datasource.SchemaResponse
		d.Schema(t.Context(), datasource.SchemaRequest{}, &response)
		if ds := response.Schema.ValidateImplementation(context.Background()); ds.HasError() {
			t.Fatal(ds)
		}
	}
	for id, valid := range map[string]bool{"gc1abc": true, "GitHub": false, "../gc1abc": false, "": false} {
		state := testManagementState(t, r, testGitCredentialModel())
		response := resource.ImportStateResponse{State: state}
		r.ImportState(t.Context(), resource.ImportStateRequest{ID: id}, &response)
		if response.Diagnostics.HasError() == valid {
			t.Errorf("import %q: %v", id, response.Diagnostics)
		}
	}
	if s := fmt.Sprintf("%v %#v", client.GitCredentialManifest{Token: "secret"}, client.GitCredentialManifest{Token: "secret"}); strings.Contains(s, "secret") {
		t.Fatal("manifest formatting revealed the token")
	}
}

func TestGitHostValidation(t *testing.T) {
	for host, valid := range map[string]bool{
		"github.com": true, "git.example.com:8443": true, "localhost": true,
		"GitHub.com": false, "https://github.com": false, "github.com/org": false, "user@github.com": false, "github.com:0": false, "": false,
	} {
		if client.GitHostPattern.MatchString(host) != valid {
			t.Errorf("host %q valid = %v, want %v", host, !valid, valid)
		}
	}
}
