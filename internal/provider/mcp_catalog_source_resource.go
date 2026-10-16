// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"slices"
	"time"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/identityschema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/tfsdk"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/obot-platform/terraform-provider-obot/internal/client"
)

const catalogSyncTimeout = 5 * time.Minute

var (
	_ resource.Resource                = &mcpCatalogSourceResource{}
	_ resource.ResourceWithConfigure   = &mcpCatalogSourceResource{}
	_ resource.ResourceWithImportState = &mcpCatalogSourceResource{}
	_ resource.ResourceWithIdentity    = &mcpCatalogSourceResource{}
)

type mcpCatalogSourceResource struct {
	client *client.Client
}

type catalogSourceModel struct {
	ID              types.String `tfsdk:"id"`
	URL             types.String `tfsdk:"url"`
	GitCredentialID types.String `tfsdk:"git_credential_id"`
}

type catalogSourceIdentityModel struct {
	URL types.String `tfsdk:"url"`
}

func newMCPCatalogSourceResource() resource.Resource {
	return &mcpCatalogSourceResource{}
}

func (r *mcpCatalogSourceResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_catalog_source"
}

func (r *mcpCatalogSourceResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages one source of the default MCP catalog, as adding a source in the Obot UI does. " +
			"Other sources, including ones added in the UI, are left unchanged. The source owns the entries it provides. " +
			"Creating, changing, or destroying a source waits up to five minutes for a new catalog sync. " +
			"Destroy removes only this source; Obot then removes the entries it provided, while running servers and API-created entries are kept. " +
			"A source that already exists in Obot must be imported. " +
			"See the [catalog and access-policy guide](../guides/catalogs-and-access-policies.md).",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The source URL, which identifies the source.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"url": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Complete HTTPS URL of the catalog source, including any Git ref and subdirectory. " +
					"Changing it replaces the source. Also the import identifier.",
				Validators: []validator.String{
					catalogSourceURL{},
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"git_credential_id": schema.StringAttribute{
				Optional: true,
				MarkdownDescription: "ID of an existing Obot Git credential used to read a private source. " +
					"When unset, Obot keeps any token stored for the source in the UI; Terraform never reads it.",
				Validators: []validator.String{
					stringvalidator.RegexMatches(client.ObjectIDPattern, "must be an Obot Git credential ID"),
				},
			},
		},
	}
}

func (r *mcpCatalogSourceResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"url": identityschema.StringAttribute{
				RequiredForImport: true,
				Description:       "The catalog source URL.",
			},
		},
	}
}

func (r *mcpCatalogSourceResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if conn := configuredClient(req.ProviderData, &resp.Diagnostics); conn != nil {
		r.client = conn
	}
}

// ensureSource adds the source if it is missing and sets its credential
// reference, leaving every other source as it is.
func ensureSource(url, credentialID string) func(*client.CatalogSources) {
	return func(sources *client.CatalogSources) {
		if !slices.Contains(sources.Sources, url) {
			sources.Sources = append(sources.Sources, url)
		}
		if credentialID == "" {
			delete(sources.GitCredentialIDs, url)
		} else {
			sources.GitCredentialIDs[url] = credentialID
		}
	}
}

// removeSource removes only the source and its credential reference.
func removeSource(url string) func(*client.CatalogSources) {
	return func(sources *client.CatalogSources) {
		sources.Sources = slices.DeleteFunc(sources.Sources, func(s string) bool { return s == url })
		delete(sources.GitCredentialIDs, url)
	}
}

// setFromCatalog records the source as configured in Obot, reporting whether it exists.
func (m *catalogSourceModel) setFromCatalog(catalog *client.Catalog) bool {
	url := m.URL.ValueString()
	if !slices.Contains(catalog.Sources, url) {
		return false
	}
	m.ID = types.StringValue(url)
	m.GitCredentialID = types.StringNull()
	if id := catalog.GitCredentialIDs[url]; id != "" {
		m.GitCredentialID = types.StringValue(id)
	}
	return true
}

func setCatalogSourceIdentity(ctx context.Context, identity *tfsdk.ResourceIdentity, url types.String, diagnostics *diag.Diagnostics) {
	if identity == nil {
		return
	}
	diagnostics.Append(identity.Set(ctx, catalogSourceIdentityModel{URL: url})...)
}

// write applies the planned source and saves it. A write Obot accepted is
// saved even when synchronization then fails, so Terraform keeps tracking it.
func (r *mcpCatalogSourceResource) write(ctx context.Context, plan catalogSourceModel, state *tfsdk.State, identity *tfsdk.ResourceIdentity, diagnostics *diag.Diagnostics) {
	ctx, cancel := context.WithTimeout(ctx, catalogSyncTimeout)
	defer cancel()
	url := plan.URL.ValueString()
	catalog, err := r.client.ModifyCatalogSources(ctx, ensureSource(url, plan.GitCredentialID.ValueString()))
	if err != nil {
		diagnostics.AddError("Error Updating MCP Catalog Source", err.Error())
		catalog, err = r.client.GetCatalog(context.WithoutCancel(ctx), defaultCatalogID)
		if err != nil {
			return
		}
	}
	if !plan.setFromCatalog(catalog) {
		return
	}
	diagnostics.Append(state.Set(ctx, &plan)...)
	setCatalogSourceIdentity(ctx, identity, plan.URL, diagnostics)
	if message, failed := catalog.SyncErrors[url]; failed && !diagnostics.HasError() {
		diagnostics.AddAttributeError(path.Root("url"), "MCP Catalog Source Failed to Sync",
			fmt.Sprintf("Obot added the source but could not read it: %s\n\n"+
				"Correct the URL, credential, or connectivity and apply again. Obot may keep the entries from the last successful sync.",
				r.client.Redact(message)))
	}
}

func (r *mcpCatalogSourceResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan catalogSourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	current, err := r.client.GetCatalog(ctx, defaultCatalogID)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading MCP Catalog", err.Error())
		return
	}
	if slices.Contains(current.Sources, plan.URL.ValueString()) {
		resp.Diagnostics.AddAttributeError(path.Root("url"), "MCP Catalog Source Already Exists",
			"The default MCP catalog already has this source. Import it to manage it with Terraform, "+
				"using an import block or terraform import with the source URL as the ID.")
		return
	}
	r.write(ctx, plan, &resp.State, resp.Identity, &resp.Diagnostics)
}

func (r *mcpCatalogSourceResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state catalogSourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	catalog, err := r.client.GetCatalog(ctx, defaultCatalogID)
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error Reading MCP Catalog Source", err.Error())
		return
	}
	if !state.setFromCatalog(catalog) {
		resp.State.RemoveResource(ctx)
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	setCatalogSourceIdentity(ctx, resp.Identity, state.URL, &resp.Diagnostics)
}

func (r *mcpCatalogSourceResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan catalogSourceModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.write(ctx, plan, &resp.State, resp.Identity, &resp.Diagnostics)
}

func (r *mcpCatalogSourceResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state catalogSourceModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, catalogSyncTimeout)
	defer cancel()
	_, err := r.client.ModifyCatalogSources(ctx, removeSource(state.URL.ValueString()))
	if err != nil && !client.IsNotFound(err) {
		resp.Diagnostics.AddError("Error Removing MCP Catalog Source", err.Error())
	}
}

// ImportState accepts the source URL as an import ID or an identity. The
// following read fails the import if Obot has no such source.
func (r *mcpCatalogSourceResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	url := req.ID
	if url == "" && resp.Identity != nil {
		var identity catalogSourceIdentityModel
		resp.Diagnostics.Append(resp.Identity.Get(ctx, &identity)...)
		url = identity.URL.ValueString()
	}
	if !validCatalogSourceURL(url) {
		resp.Diagnostics.AddError("Invalid MCP Catalog Source Import ID",
			"Import using the complete HTTPS source URL exactly as the Obot catalog lists it.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("url"), url)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), url)...)
	setCatalogSourceIdentity(ctx, resp.Identity, types.StringValue(url), &resp.Diagnostics)
}
