// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/attr"
	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/diag"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/obot-platform/terraform-provider-obot/internal/client"
)

var (
	_ datasource.DataSource              = &mcpTunnelDataSource{}
	_ datasource.DataSourceWithConfigure = &mcpTunnelDataSource{}
)

type mcpTunnelDataSource struct {
	client *client.Client
}

// tunnelDataModel is shared by the single tunnel data source and each
// element of the tunnel list.
type tunnelDataModel struct {
	ID           types.String `tfsdk:"id"`
	DisplayName  types.String `tfsdk:"display_name"`
	Description  types.String `tfsdk:"description"`
	AllowedURLs  types.Set    `tfsdk:"allowed_urls"`
	TokenPreview types.String `tfsdk:"token_preview"`
}

func (tunnelDataModel) attrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"id":            types.StringType,
		"display_name":  types.StringType,
		"description":   types.StringType,
		"allowed_urls":  types.SetType{ElemType: types.StringType},
		"token_preview": types.StringType,
	}
}

func newTunnelDataModel(ctx context.Context, tunnel client.Tunnel, diagnostics *diag.Diagnostics) tunnelDataModel {
	return tunnelDataModel{
		ID:           types.StringValue(tunnel.ID),
		DisplayName:  types.StringValue(tunnel.Manifest.DisplayName),
		Description:  types.StringValue(tunnel.Manifest.Description),
		AllowedURLs:  allowedURLsValue(ctx, tunnel.Manifest.AllowedURLs, diagnostics),
		TokenPreview: types.StringValue(tunnel.Token),
	}
}

// allowedURLsValue converts an API allowlist, where nil means empty.
func allowedURLsValue(ctx context.Context, urls []string, diagnostics *diag.Diagnostics) types.Set {
	if urls == nil {
		urls = []string{}
	}
	value, d := types.SetValueFrom(ctx, types.StringType, urls)
	diagnostics.Append(d...)
	return value
}

func tunnelDataAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "The generated Obot tunnel ID.",
		},
		"display_name": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Human-readable tunnel display name.",
		},
		"description": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Description of the tunnel's purpose.",
		},
		"allowed_urls": schema.SetAttribute{
			Computed:            true,
			ElementType:         types.StringType,
			MarkdownDescription: "Permitted target URL or hostname patterns. An empty set denies all targets.",
		},
		"token_preview": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Non-secret shortened preview. The complete token cannot be read from Obot.",
		},
	}
}

func configuredClient(providerData any, diagnostics *diag.Diagnostics) *client.Client {
	if providerData == nil {
		return nil
	}
	conn, ok := providerData.(*client.Client)
	if !ok {
		diagnostics.AddError("Unexpected Data Source Configure Type", fmt.Sprintf("Expected an Obot API client, got %T.", providerData))
	}
	return conn
}

func newMCPTunnelDataSource() datasource.DataSource {
	return &mcpTunnelDataSource{}
}

func (d *mcpTunnelDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_tunnel"
}

func (d *mcpTunnelDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attributes := tunnelDataAttributes()
	attributes["id"] = schema.StringAttribute{
		Required:            true,
		MarkdownDescription: "Generated Obot tunnel ID to look up. Display names are not unique identifiers.",
		Validators: []validator.String{
			tunnelID(),
		},
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads one MCP tunnel definition by its generated ID. Errors if it does not exist. " +
			"Returns a preview only; creating or explicitly rotating a managed tunnel is required to capture a complete token.",
		Attributes: attributes,
	}
}

func (d *mcpTunnelDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if conn := configuredClient(req.ProviderData, &resp.Diagnostics); conn != nil {
		d.client = conn
	}
}

func (d *mcpTunnelDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data tunnelDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	tunnel, err := d.client.GetTunnel(ctx, data.ID.ValueString())
	if client.IsNotFound(err) {
		resp.Diagnostics.AddError("MCP Tunnel Not Found", "No MCP tunnel matches the supplied ID. Verify the ID and the configured Obot instance.")
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error Reading MCP Tunnel", err.Error())
		return
	}
	data = newTunnelDataModel(ctx, *tunnel, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
