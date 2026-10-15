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
	"github.com/hashicorp/terraform-plugin-framework/path"
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
	plural bool
}

func newMCPAccessRuleDataSource() datasource.DataSource { return &mcpAccessRuleDataSource{} }
func newMCPAccessRulesDataSource() datasource.DataSource {
	return &mcpAccessRuleDataSource{plural: true}
}
func (d *mcpAccessRuleDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_access_rule"
	if d.plural {
		resp.TypeName += "s"
	}
}
func accessRuleDataAttributes() map[string]schema.Attribute {
	refs := func(description string) schema.SetNestedAttribute {
		return schema.SetNestedAttribute{Computed: true, MarkdownDescription: description, NestedObject: schema.NestedAttributeObject{Attributes: map[string]schema.Attribute{
			"type": schema.StringAttribute{Computed: true, MarkdownDescription: "The reference type."}, "id": schema.StringAttribute{Computed: true, MarkdownDescription: "The referenced ID or wildcard `*`."},
		}}}
	}
	return map[string]schema.Attribute{
		"id":           schema.StringAttribute{Computed: true, MarkdownDescription: "The access-control rule identifier."},
		"catalog_id":   schema.StringAttribute{Computed: true, MarkdownDescription: "The catalog containing this policy."},
		"display_name": schema.StringAttribute{Computed: true, MarkdownDescription: "The policy's display name; names need not be unique."},
		"subjects":     refs("Actual subjects receiving this grant. Inspect these before importing; Everything is a name, not an everyone selector."),
		"resources":    refs("Actual resources covered by this policy."),
		"generated":    schema.BoolAttribute{Computed: true, MarkdownDescription: "Whether Obot generated this rule. Generated rules cannot be managed by the Terraform resource."},
	}
}
func (d *mcpAccessRuleDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	catalog := schema.StringAttribute{Optional: true, Computed: true, Validators: []validator.String{obotStringValidator{identifier: true}}, MarkdownDescription: "The catalog to inspect; defaults to `default`."}
	if d.plural {
		resp.Schema = schema.Schema{MarkdownDescription: "Lists zero or more policies under MCP Servers > Access Policies, sorted by ID. Use to discover Everything and its actual subjects before import. This lookup makes no changes and does not read vMCP profiles.", Attributes: map[string]schema.Attribute{
			"catalog_id": catalog,
			"rules":      schema.ListNestedAttribute{Computed: true, MarkdownDescription: "Current catalog access policies, including generated rules, sorted by ID. An empty catalog returns an empty list.", NestedObject: schema.NestedAttributeObject{Attributes: accessRuleDataAttributes()}},
		}}
		return
	}
	attrs := accessRuleDataAttributes()
	attrs["catalog_id"] = catalog
	attrs["id"] = schema.StringAttribute{Required: true, Validators: []validator.String{obotStringValidator{identifier: true}}, MarkdownDescription: "The exact rule identifier, for example `acr1-everything`. A missing rule is an error."}
	resp.Schema = schema.Schema{MarkdownDescription: "Reads one catalog MCP access policy by exact ID, preserving its actual subjects and resources for inspection. This lookup never creates or updates a policy and does not read vMCP profiles.", Attributes: attrs}
}
func (d *mcpAccessRuleDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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
func accessRuleDataValue(ctx context.Context, rule client.AccessRule, diagnostics *diag.Diagnostics) types.Object {
	model := accessRuleModel{}
	model.fromAPI(ctx, &rule, diagnostics)
	return types.ObjectValueMust(map[string]attr.Type{"id": types.StringType, "catalog_id": types.StringType, "display_name": types.StringType, "subjects": types.SetType{ElemType: types.ObjectType{AttrTypes: referenceAttrTypes}}, "resources": types.SetType{ElemType: types.ObjectType{AttrTypes: referenceAttrTypes}}, "generated": types.BoolType}, map[string]attr.Value{
		"id": model.ID, "catalog_id": model.CatalogID, "display_name": model.DisplayName, "subjects": model.Subjects, "resources": model.Resources, "generated": types.BoolValue(rule.Generated),
	})
}
func (d *mcpAccessRuleDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var catalog types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("catalog_id"), &catalog)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if catalog.IsNull() {
		catalog = types.StringValue("default")
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("catalog_id"), catalog)...)
	if d.plural {
		rules, err := d.client.ListAccessRules(ctx, catalog.ValueString())
		if err != nil {
			resp.Diagnostics.AddError("Error Listing MCP Access Rules", err.Error())
			return
		}
		sort.Slice(rules, func(i, j int) bool { return rules[i].ID < rules[j].ID })
		elements := make([]attr.Value, 0, len(rules))
		for _, rule := range rules {
			elements = append(elements, accessRuleDataValue(ctx, rule, &resp.Diagnostics))
		}
		elementType := accessRuleDataValue(ctx, client.AccessRule{}, &resp.Diagnostics).Type(ctx)
		value, diagnostics := types.ListValue(elementType, elements)
		resp.Diagnostics.Append(diagnostics...)
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("rules"), value)...)
		return
	}
	var id types.String
	resp.Diagnostics.Append(req.Config.GetAttribute(ctx, path.Root("id"), &id)...)
	if resp.Diagnostics.HasError() {
		return
	}
	rule, err := d.client.GetAccessRule(ctx, catalog.ValueString(), id.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error Reading MCP Access Rule", err.Error())
		return
	}
	value := accessRuleDataValue(ctx, *rule, &resp.Diagnostics)
	for name, val := range value.Attributes() {
		resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root(name), val)...)
	}
}
