// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

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
	_ datasource.DataSource              = &mcpAccessRuleDataSource{}
	_ datasource.DataSourceWithConfigure = &mcpAccessRuleDataSource{}
)

type mcpAccessRuleDataSource struct {
	client *client.Client
}

// accessRuleDataModel is shared by the single rule data source and each
// element of the rule list.
type accessRuleDataModel struct {
	ID          types.String `tfsdk:"id"`
	CatalogID   types.String `tfsdk:"catalog_id"`
	DisplayName types.String `tfsdk:"display_name"`
	Subjects    types.Set    `tfsdk:"subjects"`
	Resources   types.Set    `tfsdk:"resources"`
	Generated   types.Bool   `tfsdk:"generated"`
}

func (accessRuleDataModel) attrTypes() map[string]attr.Type {
	references := types.SetType{ElemType: types.ObjectType{AttrTypes: referenceAttrTypes}}
	return map[string]attr.Type{
		"id":           types.StringType,
		"catalog_id":   types.StringType,
		"display_name": types.StringType,
		"subjects":     references,
		"resources":    references,
		"generated":    types.BoolType,
	}
}

func newAccessRuleDataModel(ctx context.Context, rule client.AccessRule, diagnostics *diag.Diagnostics) accessRuleDataModel {
	return accessRuleDataModel{
		ID:          types.StringValue(rule.ID),
		CatalogID:   types.StringValue(rule.CatalogID),
		DisplayName: types.StringValue(rule.DisplayName),
		Subjects:    referencesValue(ctx, rule.Subjects, diagnostics),
		Resources:   referencesValue(ctx, rule.Resources, diagnostics),
		Generated:   types.BoolValue(rule.Generated),
	}
}

func accessRuleDataAttributes() map[string]schema.Attribute {
	references := func(description string) schema.SetNestedAttribute {
		return schema.SetNestedAttribute{
			Computed:            true,
			MarkdownDescription: description,
			NestedObject: schema.NestedAttributeObject{
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "The reference type.",
					},
					"id": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "The referenced ID or wildcard `*`.",
					},
				},
			},
		}
	}
	return map[string]schema.Attribute{
		"id": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "The access-control rule identifier.",
		},
		"catalog_id": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "The catalog containing this policy, always `default`.",
		},
		"display_name": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "The policy's display name; names need not be unique.",
		},
		"subjects":  references("Actual subjects receiving this grant. Inspect these before importing; Everything is a name, not an everyone selector."),
		"resources": references("Actual resources covered by this policy."),
		"generated": schema.BoolAttribute{
			Computed:            true,
			MarkdownDescription: "Whether Obot generated this rule. Generated rules cannot be managed by the Terraform resource.",
		},
	}
}

func newMCPAccessRuleDataSource() datasource.DataSource {
	return &mcpAccessRuleDataSource{}
}

func (d *mcpAccessRuleDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_access_rule"
}

func (d *mcpAccessRuleDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attributes := accessRuleDataAttributes()
	attributes["id"] = schema.StringAttribute{
		Required:            true,
		MarkdownDescription: "The exact rule identifier, for example `acr1-everything`. A missing rule is an error.",
		Validators: []validator.String{
			stringvalidator.RegexMatches(client.ObjectIDPattern, "must be an Obot access-control rule ID"),
		},
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads one catalog MCP access policy by exact ID, preserving its actual subjects and resources for inspection. " +
			"This lookup never creates or updates a policy and does not read vMCP profiles.",
		Attributes: attributes,
	}
}

func (d *mcpAccessRuleDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if conn := configuredClient(req.ProviderData, &resp.Diagnostics); conn != nil {
		d.client = conn
	}
}

func (d *mcpAccessRuleDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data accessRuleDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	rule, err := d.client.GetAccessRule(ctx, defaultCatalogID, data.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error Reading MCP Access Rule", err.Error())
		return
	}
	data = newAccessRuleDataModel(ctx, *rule, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}
