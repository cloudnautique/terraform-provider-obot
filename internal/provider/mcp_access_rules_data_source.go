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
	_ datasource.DataSource              = &mcpAccessRulesDataSource{}
	_ datasource.DataSourceWithConfigure = &mcpAccessRulesDataSource{}
)

type mcpAccessRulesDataSource struct {
	client *client.Client
}

type accessRulesDataModel struct {
	CatalogID types.String `tfsdk:"catalog_id"`
	Rules     types.List   `tfsdk:"rules"`
}

func newMCPAccessRulesDataSource() datasource.DataSource {
	return &mcpAccessRulesDataSource{}
}

func (d *mcpAccessRulesDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_access_rules"
}

func (d *mcpAccessRulesDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists zero or more policies under MCP Servers > Access Policies, sorted by ID. " +
			"Use to discover Everything and its actual subjects before import. This lookup makes no changes and does not read vMCP profiles.",
		Attributes: map[string]schema.Attribute{
			"catalog_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The catalog containing the policies, always `default`.",
			},
			"rules": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Current catalog access policies, including generated rules, sorted by ID. An empty catalog returns an empty list.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: accessRuleDataAttributes(),
				},
			},
		},
	}
}

func (d *mcpAccessRulesDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if conn := configuredClient(req.ProviderData, &resp.Diagnostics); conn != nil {
		d.client = conn
	}
}

func (d *mcpAccessRulesDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	rules, err := d.client.ListAccessRules(ctx, defaultCatalogID)
	if err != nil {
		resp.Diagnostics.AddError("Error Listing MCP Access Rules", err.Error())
		return
	}
	sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
	models := make([]accessRuleDataModel, 0, len(rules))
	for _, rule := range rules {
		models = append(models, newAccessRuleDataModel(ctx, rule, &resp.Diagnostics))
	}
	list, diags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: accessRuleDataModel{}.attrTypes()}, models)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &accessRulesDataModel{CatalogID: types.StringValue(defaultCatalogID), Rules: list})...)
}
