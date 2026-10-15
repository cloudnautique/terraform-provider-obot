// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"sort"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/obot-platform/terraform-provider-obot/internal/client"
)

var (
	_ datasource.DataSource              = &mcpTunnelsDataSource{}
	_ datasource.DataSourceWithConfigure = &mcpTunnelsDataSource{}
)

type mcpTunnelsDataSource struct {
	client *client.Client
}

type tunnelsDataModel struct {
	Tunnels types.List `tfsdk:"tunnels"`
}

func newMCPTunnelsDataSource() datasource.DataSource {
	return &mcpTunnelsDataSource{}
}

func (d *mcpTunnelsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_tunnels"
}

func (d *mcpTunnelsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists configured MCP tunnel definitions, sorted by ID. An empty installation returns an empty list. " +
			"Includes token previews only; complete tokens cannot be recovered by a data source.",
		Attributes: map[string]schema.Attribute{
			"tunnels": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Configured tunnel definitions; connection status is not part of the definition.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: tunnelDataAttributes(),
				},
			},
		},
	}
}

func (d *mcpTunnelsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if conn := configuredClient(req.ProviderData, &resp.Diagnostics); conn != nil {
		d.client = conn
	}
}

func (d *mcpTunnelsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	tunnels, err := d.client.ListTunnels(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error Listing MCP Tunnels", err.Error())
		return
	}
	sort.Slice(tunnels, func(i, j int) bool { return tunnels[i].ID < tunnels[j].ID })
	models := make([]tunnelDataModel, 0, len(tunnels))
	for _, tunnel := range tunnels {
		models = append(models, newTunnelDataModel(ctx, tunnel, &resp.Diagnostics))
	}
	list, diags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: tunnelDataModel{}.attrTypes()}, models)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &tunnelsDataModel{Tunnels: list})...)
}
