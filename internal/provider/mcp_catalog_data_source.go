// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
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

type mcpCatalogDataSource struct {
	client *client.Client
}

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

func (catalogEntryModel) attrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"id":         types.StringType,
		"name":       types.StringType,
		"source_url": types.StringType,
	}
}

func newMCPCatalogDataSource() datasource.DataSource {
	return &mcpCatalogDataSource{}
}

func (d *mcpCatalogDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_catalog"
}

func (d *mcpCatalogDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads an MCP catalog's sources, synchronization status, and entry inventory. Use to discover configuration before import. " +
			"Reading never refreshes or changes the catalog. Git-owned entry definitions remain in Git; inline catalog credentials and server configurations are not exposed. " +
			"Use an Admin, Owner, or Auditor token: Obot limits other tokens to the entries their access policies allow.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Catalog identifier, usually `default`.",
				Validators: []validator.String{
					stringvalidator.RegexMatches(client.ObjectIDPattern, "must be an Obot catalog ID"),
				},
			},
			"display_name": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The catalog's display name.",
			},
			"sources": schema.SetAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Current catalog source URLs.",
			},
			"source_git_credential_ids": schema.MapAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Existing Git credential IDs keyed by source URL; contains no token values.",
			},
			"last_synced": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "Timestamp of the last completed synchronization, or an empty string if none has completed.",
			},
			"is_syncing": schema.BoolAttribute{
				Computed:            true,
				MarkdownDescription: "Whether synchronization is running or queued.",
			},
			"sync_errors": schema.MapAttribute{
				Computed:            true,
				ElementType:         types.StringType,
				MarkdownDescription: "Last synchronization errors keyed by source URL.",
			},
			"entries": schema.ListNestedAttribute{
				Computed: true,
				MarkdownDescription: "Current catalog entries sorted by ID. Source-managed entries have a nonempty source URL; API-created entries have an empty one. " +
					"This is the current inventory and does not trigger synchronization.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: map[string]schema.Attribute{
						"id": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The catalog entry identifier.",
						},
						"name": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The catalog entry's name.",
						},
						"source_url": schema.StringAttribute{
							Computed:            true,
							MarkdownDescription: "The owning source URL, empty for API-created entries.",
						},
					},
				},
			},
		},
	}
}

func (d *mcpCatalogDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if conn := configuredClient(req.ProviderData, &resp.Diagnostics); conn != nil {
		d.client = conn
	}
}

// stringMapValue converts an API map, where nil means empty.
func stringMapValue(ctx context.Context, values map[string]string, diagnostics *diag.Diagnostics) types.Map {
	if values == nil {
		values = map[string]string{}
	}
	result, d := types.MapValueFrom(ctx, types.StringType, values)
	diagnostics.Append(d...)
	return result
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
	model.DisplayName = types.StringValue(result.DisplayName)
	model.Sources = stringSetValue(ctx, result.Sources, &resp.Diagnostics)
	model.GitCredentialIDs = stringMapValue(ctx, result.GitCredentialIDs, &resp.Diagnostics)
	model.LastSynced = types.StringValue(result.LastSynced)
	model.IsSyncing = types.BoolValue(result.IsSyncing)
	model.SyncErrors = stringMapValue(ctx, result.SyncErrors, &resp.Diagnostics)
	sort.Slice(entries, func(i, j int) bool { return entries[i].ID < entries[j].ID })
	items := make([]catalogEntryModel, 0, len(entries))
	for _, entry := range entries {
		items = append(items, catalogEntryModel{ID: types.StringValue(entry.ID), Name: types.StringValue(entry.Manifest.Name), SourceURL: types.StringValue(entry.SourceURL)})
	}
	var diagnostics diag.Diagnostics
	model.Entries, diagnostics = types.ListValueFrom(ctx, types.ObjectType{AttrTypes: catalogEntryModel{}.attrTypes()}, items)
	resp.Diagnostics.Append(diagnostics...)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}
