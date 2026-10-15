// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"maps"
	"slices"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/path"
	"github.com/hashicorp/terraform-plugin-framework/resource"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/mapdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/planmodifier"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringdefault"
	"github.com/hashicorp/terraform-plugin-framework/resource/schema/stringplanmodifier"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/obot-platform/terraform-provider-obot/internal/client"
)

const catalogSyncTimeout = 5 * time.Minute

var (
	_ resource.Resource                   = &mcpCatalogSourcesResource{}
	_ resource.ResourceWithConfigure      = &mcpCatalogSourcesResource{}
	_ resource.ResourceWithImportState    = &mcpCatalogSourcesResource{}
	_ resource.ResourceWithValidateConfig = &mcpCatalogSourcesResource{}
)

type mcpCatalogSourcesResource struct{ client *client.Client }
type catalogSourcesModel struct {
	CatalogID        types.String `tfsdk:"catalog_id"`
	Sources          types.Set    `tfsdk:"sources"`
	GitCredentialIDs types.Map    `tfsdk:"source_git_credential_ids"`
}

func newMCPCatalogSourcesResource() resource.Resource { return &mcpCatalogSourcesResource{} }
func (r *mcpCatalogSourcesResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_catalog_sources"
}
func (r *mcpCatalogSourcesResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages the complete source list of the default MCP catalog. Import to adopt existing sources without changing them. Each source continues to own its entries. Changing sources waits up to five minutes for a new catalog sync. Destroy clears the sources and their Git credential references, triggering removal of source-managed entries; it preserves the default catalog. Use only one instance of this resource per Obot installation. See the [catalog and access-policy guide](../guides/catalogs-and-access-policies.md) for discovery, adoption, replacement, and deletion workflows.",
		Attributes: map[string]schema.Attribute{
			"catalog_id":                schema.StringAttribute{Optional: true, Computed: true, Default: stringdefault.StaticString("default"), Validators: []validator.String{obotStringValidator{defaultCatalog: true}}, PlanModifiers: []planmodifier.String{stringplanmodifier.RequiresReplace()}, MarkdownDescription: "The MCP catalog identifier. Only `default` is supported; defaults to `default`. Also the import identifier."},
			"sources":                   schema.SetAttribute{Required: true, ElementType: types.StringType, Validators: []validator.Set{catalogSourcesValidator{}}, MarkdownDescription: "The authoritative set of catalog source HTTPS URLs, including any Git ref and subdirectory. URLs must be complete to avoid API normalization. Set to `[]` to remove shipped source-managed entries without registering replacements. API-created entries are preserved."},
			"source_git_credential_ids": schema.MapAttribute{Optional: true, Computed: true, ElementType: types.StringType, Default: mapdefault.StaticValue(types.MapValueMust(types.StringType, map[string]attr.Value{})), MarkdownDescription: "Existing Obot Git credential IDs keyed by source URL. Every key must appear in `sources`. Defaults to an empty map. Include existing references when importing. Legacy inline credentials for retained sources are preserved by Obot and never read into Terraform state."},
		},
	}
}
func (r *mcpCatalogSourcesResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	conn, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Resource Configure Type", fmt.Sprintf("Expected an Obot API client, got %T.", req.ProviderData))
		return
	}
	r.client = conn
}
func (r *mcpCatalogSourcesResource) ValidateConfig(ctx context.Context, req resource.ValidateConfigRequest, resp *resource.ValidateConfigResponse) {
	var model catalogSourcesModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() || model.Sources.IsUnknown() || model.GitCredentialIDs.IsUnknown() {
		return
	}
	sources := map[string]bool{}
	unknownSources := false
	for _, value := range model.Sources.Elements() {
		source, ok := value.(types.String)
		if !ok || source.IsNull() {
			continue
		}
		if source.IsUnknown() {
			unknownSources = true
			continue
		}
		sources[source.ValueString()] = true
	}
	for key, value := range model.GitCredentialIDs.Elements() {
		id, ok := value.(types.String)
		if ok && id.IsUnknown() {
			continue
		}
		if (!unknownSources && !sources[key]) || !ok || id.IsNull() || !client.ValidObjectID(id.ValueString()) {
			resp.Diagnostics.AddAttributeError(path.Root("source_git_credential_ids"), "Invalid Catalog Git Credential Reference", "Each key must match a configured source URL and each value must be an Obot Git credential ID.")
		}
	}
}
func (m *catalogSourcesModel) fromAPI(ctx context.Context, result *client.Catalog, diagnostics *diag.Diagnostics) {
	m.CatalogID = types.StringValue(result.ID)
	var d diag.Diagnostics
	if result.Sources == nil {
		result.Sources = []string{}
	}
	if result.GitCredentialIDs == nil {
		result.GitCredentialIDs = map[string]string{}
	}
	m.Sources, d = types.SetValueFrom(ctx, types.StringType, result.Sources)
	diagnostics.Append(d...)
	m.GitCredentialIDs, d = types.MapValueFrom(ctx, types.StringType, result.GitCredentialIDs)
	diagnostics.Append(d...)
}
func (m *catalogSourcesModel) input(ctx context.Context, diagnostics *diag.Diagnostics) client.CatalogSources {
	var result client.CatalogSources
	diagnostics.Append(m.Sources.ElementsAs(ctx, &result.Sources, false)...)
	diagnostics.Append(m.GitCredentialIDs.ElementsAs(ctx, &result.GitCredentialIDs, false)...)
	slices.Sort(result.Sources)
	return result
}
func (r *mcpCatalogSourcesResource) write(ctx context.Context, model *catalogSourcesModel, save func(), diagnostics *diag.Diagnostics) {
	ctx, cancel := context.WithTimeout(ctx, catalogSyncTimeout)
	defer cancel()
	input := model.input(ctx, diagnostics)
	if diagnostics.HasError() {
		return
	}
	before, err := r.client.WaitCatalogIdle(ctx, model.CatalogID.ValueString())
	if err != nil {
		diagnostics.AddError("Error Reading MCP Catalog", err.Error())
		return
	}
	oldSources := slices.Clone(before.Sources)
	slices.Sort(oldSources)
	if slices.Equal(input.Sources, oldSources) && maps.Equal(input.GitCredentialIDs, before.GitCredentialIDs) && len(before.SyncErrors) == 0 {
		model.fromAPI(ctx, before, diagnostics)
		save()
		return
	}
	result, err := r.client.UpdateCatalogSources(ctx, model.CatalogID.ValueString(), input)
	if err != nil {
		diagnostics.AddError("Error Updating MCP Catalog Sources", err.Error())
		return
	}
	model.fromAPI(ctx, result, diagnostics)
	save() // Preserve the adopted singleton after an accepted write, even if synchronization fails.
	_, err = r.client.RefreshCatalog(ctx, result.ID, before.LastSynced)
	if err != nil {
		diagnostics.AddError("Error Synchronizing MCP Catalog Sources", err.Error())
	}
}
func (r *mcpCatalogSourcesResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var model catalogSourcesModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.write(ctx, &model, func() { resp.Diagnostics.Append(resp.State.Set(ctx, &model)...) }, &resp.Diagnostics)
}
func (r *mcpCatalogSourcesResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var model catalogSourcesModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	r.write(ctx, &model, func() { resp.Diagnostics.Append(resp.State.Set(ctx, &model)...) }, &resp.Diagnostics)
}
func (r *mcpCatalogSourcesResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var model catalogSourcesModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	result, err := r.client.GetCatalog(ctx, model.CatalogID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error Reading MCP Catalog Sources", err.Error())
		return
	}
	model.fromAPI(ctx, result, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}
func (r *mcpCatalogSourcesResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var model catalogSourcesModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	ctx, cancel := context.WithTimeout(ctx, catalogSyncTimeout)
	defer cancel()
	before, err := r.client.WaitCatalogIdle(ctx, model.CatalogID.ValueString())
	if client.IsNotFound(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error Reading MCP Catalog", err.Error())
		return
	}
	result, err := r.client.UpdateCatalogSources(ctx, model.CatalogID.ValueString(), client.CatalogSources{})
	if client.IsNotFound(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error Clearing MCP Catalog Sources", err.Error())
		return
	}
	if _, err = r.client.RefreshCatalog(ctx, result.ID, before.LastSynced); err != nil {
		resp.Diagnostics.AddError("Error Synchronizing Empty MCP Catalog", err.Error())
	}
}
func (r *mcpCatalogSourcesResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	if req.ID != "default" {
		resp.Diagnostics.AddError("Invalid MCP Catalog Import ID", "Use default. Only the default catalog supports source management.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("catalog_id"), req.ID)...)
}
