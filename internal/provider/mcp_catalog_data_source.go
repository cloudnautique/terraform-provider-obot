// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/obot-platform/terraform-provider-obot/internal/client"
)

var (
	_ datasource.DataSource              = &mcpCatalogDataSource{}
	_ datasource.DataSourceWithConfigure = &mcpCatalogDataSource{}
)

type mcpCatalogDataSource struct{ client *client.Client }
type catalogDataModel struct {
	ID               types.String `tfsdk:"id"`
	DisplayName      types.String `tfsdk:"display_name"`
	Sources          types.Set    `tfsdk:"sources"`
	GitCredentialIDs types.Map    `tfsdk:"source_git_credential_ids"`
	LastSynced       types.String `tfsdk:"last_synced"`
	IsSyncing        types.Bool   `tfsdk:"is_syncing"`
	SyncErrors       types.Map    `tfsdk:"sync_errors"`
	Entries          types.List   `tfsdk:"entries"`
}
type catalogEntryModel struct {
	ID        types.String `tfsdk:"id"`
	Name      types.String `tfsdk:"name"`
	SourceURL types.String `tfsdk:"source_url"`
}

func newMCPCatalogDataSource() datasource.DataSource { return &mcpCatalogDataSource{} }
func (d *mcpCatalogDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_catalog"
}
func (d *mcpCatalogDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{MarkdownDescription: "Reads an MCP catalog's sources, synchronization status, and entry inventory. Use to discover configuration before import. Reading never refreshes or changes the catalog. Git-owned entry definitions remain in Git; inline catalog credentials and server configurations are not exposed.", Attributes: map[string]schema.Attribute{
		"id":                        schema.StringAttribute{Required: true, Validators: []validator.String{obotStringValidator{identifier: true}}, MarkdownDescription: "Catalog identifier, usually `default`."},
		"display_name":              schema.StringAttribute{Computed: true, MarkdownDescription: "The catalog's display name."},
		"sources":                   schema.SetAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Current catalog source URLs."},
		"source_git_credential_ids": schema.MapAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Existing Git credential IDs keyed by source URL; contains no token values."},
		"last_synced":               schema.StringAttribute{Computed: true, MarkdownDescription: "Timestamp of the last completed synchronization, or an empty string if none has completed."},
		"is_syncing":                schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether synchronization is running or queued."},
		"sync_errors":               schema.MapAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Last synchronization errors keyed by source URL."},
		"entries": schema.ListNestedAttribute{Computed: true, MarkdownDescription: "Current catalog entries sorted by ID. Source-managed entries have a nonempty source URL; API-created entries have an empty one. This is the current inventory and does not trigger synchronization.", NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"id":         schema.StringAttribute{Computed: true, MarkdownDescription: "The catalog entry identifier."},
			"name":       schema.StringAttribute{Computed: true, MarkdownDescription: "The catalog entry's name."},
			"source_url": schema.StringAttribute{Computed: true, MarkdownDescription: "The owning source URL, empty for API-created entries."},
		}}},
	}}
}
func (d *mcpCatalogDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if req.ProviderData == nil {
		return
	}
	conn, ok := req.ProviderData.(*client.Client)
	if !ok {
		resp.Diagnostics.AddError("Unexpected Data Source Configure Type", fmt.Sprintf("Expected an Obot API client, got %T.", req.ProviderData))
		return
	}
	d.client = conn
}
func (d *mcpCatalogDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var model catalogDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	result, err := d.client.GetCatalog(ctx, model.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error Reading MCP Catalog", err.Error())
		return
	}
	entries, err := d.client.ListCatalogEntries(ctx, model.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error Reading MCP Catalog Entries", err.Error())
		return
	}
	if result.Sources == nil {
		result.Sources = []string{}
	}
	if result.GitCredentialIDs == nil {
		result.GitCredentialIDs = map[string]string{}
	}
	var diagnostics diag.Diagnostics
	model.DisplayName = types.StringValue(result.DisplayName)
	model.Sources, diagnostics = types.SetValueFrom(ctx, types.StringType, result.Sources)
	resp.Diagnostics.Append(diagnostics...)
	model.GitCredentialIDs, diagnostics = types.MapValueFrom(ctx, types.StringType, result.GitCredentialIDs)
	resp.Diagnostics.Append(diagnostics...)
	model.LastSynced = types.StringValue(result.LastSynced)
	model.IsSyncing = types.BoolValue(result.IsSyncing)
	if result.SyncErrors == nil {
		result.SyncErrors = map[string]string{}
	}
	model.SyncErrors, diagnostics = types.MapValueFrom(ctx, types.StringType, result.SyncErrors)
	resp.Diagnostics.Append(diagnostics...)
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	items := make([]catalogEntryModel, 0, len(entries))
	for _, entry := range entries {
		items = append(items, catalogEntryModel{ID: types.StringValue(entry.ID), Name: types.StringValue(entry.Manifest.Name), SourceURL: types.StringValue(entry.SourceURL)})
	}
	model.Entries, diagnostics = types.ListValueFrom(ctx, types.ObjectType{AttrTypes: map[string]attr.Type{"id": types.StringType, "name": types.StringType, "source_url": types.StringType}}, items)
	resp.Diagnostics.Append(diagnostics...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}
