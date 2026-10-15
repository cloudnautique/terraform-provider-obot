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
	_ datasource.DataSource              = &mcpTunnelDataSource{}
	_ datasource.DataSourceWithConfigure = &mcpTunnelDataSource{}
)

type mcpTunnelDataSource struct {
	client *client.Client
	plural bool
}
type tunnelDataModel struct {
	ID           types.String `tfsdk:"id"`
	DisplayName  types.String `tfsdk:"display_name"`
	Description  types.String `tfsdk:"description"`
	AllowedURLs  types.Set    `tfsdk:"allowed_urls"`
	TokenPreview types.String `tfsdk:"token_preview"`
}
type tunnelsDataModel struct {
	Tunnels types.List `tfsdk:"tunnels"`
}

func newMCPTunnelDataSource() datasource.DataSource  { return &mcpTunnelDataSource{} }
func newMCPTunnelsDataSource() datasource.DataSource { return &mcpTunnelDataSource{plural: true} }
func (d *mcpTunnelDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_tunnel"
	if d.plural {
		resp.TypeName += "s"
	}
}
func tunnelDataAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id":            schema.StringAttribute{Computed: true, MarkdownDescription: "The generated Obot tunnel ID."},
		"display_name":  schema.StringAttribute{Computed: true, MarkdownDescription: "Human-readable tunnel display name."},
		"description":   schema.StringAttribute{Computed: true, MarkdownDescription: "Description of the tunnel's purpose."},
		"allowed_urls":  schema.SetAttribute{Computed: true, ElementType: types.StringType, MarkdownDescription: "Permitted target URL or hostname patterns. An empty set denies all targets."},
		"token_preview": schema.StringAttribute{Computed: true, MarkdownDescription: "Non-secret shortened preview. The complete token cannot be read from Obot."},
	}
}
func (d *mcpTunnelDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	if d.plural {
		resp.Schema = schema.Schema{MarkdownDescription: "Lists configured MCP tunnel definitions, sorted by ID. An empty installation returns an empty list. Includes token previews only; complete tokens cannot be recovered by a data source.", Attributes: map[string]schema.Attribute{
			"tunnels": schema.ListNestedAttribute{Computed: true, MarkdownDescription: "Configured tunnel definitions; connection status is not part of the definition.", NestedObject: schema.NestedAttributeObject{Attributes: tunnelDataAttributes()}},
		}}
		return
	}
	attributes := tunnelDataAttributes()
	attributes["id"] = schema.StringAttribute{Required: true, MarkdownDescription: "Generated Obot tunnel ID to look up. Display names are not unique identifiers.", Validators: []validator.String{tunnelStringValidator{identifier: true}}}
	resp.Schema = schema.Schema{MarkdownDescription: "Reads one MCP tunnel definition by its generated ID. Errors if it does not exist. Returns a preview only; creating or explicitly rotating a managed tunnel is required to capture a complete token.", Attributes: attributes}
}
func (d *mcpTunnelDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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
func tunnelData(ctx context.Context, tunnel client.Tunnel, diagnostics *diag.Diagnostics) tunnelDataModel {
	urls := tunnel.Manifest.AllowedURLs
	if urls == nil {
		urls = []string{}
	}
	allowed, converted := types.SetValueFrom(ctx, types.StringType, urls)
	diagnostics.Append(converted...)
	return tunnelDataModel{ID: types.StringValue(tunnel.ID), DisplayName: types.StringValue(tunnel.Manifest.DisplayName), Description: types.StringValue(tunnel.Manifest.Description), AllowedURLs: allowed, TokenPreview: types.StringValue(tunnel.Token)}
}
func (d *mcpTunnelDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	if d.plural {
		tunnels, err := d.client.ListTunnels(ctx)
		if err != nil {
			resp.Diagnostics.AddError("Error Listing MCP Tunnels", err.Error())
			return
		}
		sort.Slice(tunnels, func(i, j int) bool { return tunnels[i].ID < tunnels[j].ID })
		models := make([]tunnelDataModel, 0, len(tunnels))
		for _, tunnel := range tunnels {
			models = append(models, tunnelData(ctx, tunnel, &resp.Diagnostics))
		}
		object := types.ObjectType{AttrTypes: map[string]attr.Type{"id": types.StringType, "display_name": types.StringType, "description": types.StringType, "allowed_urls": types.SetType{ElemType: types.StringType}, "token_preview": types.StringType}}
		values, converted := types.ListValueFrom(ctx, object, models)
		resp.Diagnostics.Append(converted...)
		resp.Diagnostics.Append(resp.State.Set(ctx, &tunnelsDataModel{Tunnels: values})...)
		return
	}
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
	data = tunnelData(ctx, *tunnel, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
