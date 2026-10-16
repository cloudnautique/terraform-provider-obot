// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"
	"time"

	"github.com/hashicorp/terraform-plugin-framework/attr"
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

// defaultCatalogID is the only catalog with access policies.
const defaultCatalogID = "default"

var (
	_ resource.Resource                = &mcpAccessRuleResource{}
	_ resource.ResourceWithConfigure   = &mcpAccessRuleResource{}
	_ resource.ResourceWithImportState = &mcpAccessRuleResource{}
	_ resource.ResourceWithIdentity    = &mcpAccessRuleResource{}
)

type mcpAccessRuleResource struct {
	client *client.Client
}

type accessRuleModel struct {
	ID          types.String `tfsdk:"id"`
	CatalogID   types.String `tfsdk:"catalog_id"`
	DisplayName types.String `tfsdk:"display_name"`
	Subjects    types.Set    `tfsdk:"subjects"`
	Resources   types.Set    `tfsdk:"resources"`
}

type referenceModel struct {
	Type types.String `tfsdk:"type"`
	ID   types.String `tfsdk:"id"`
}

type accessRuleIdentityModel struct {
	ID types.String `tfsdk:"id"`
}

var referenceAttrTypes = map[string]attr.Type{
	"type": types.StringType,
	"id":   types.StringType,
}

func newMCPAccessRuleResource() resource.Resource {
	return &mcpAccessRuleResource{}
}

func (r *mcpAccessRuleResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_mcp_access_rule"
}

func referenceAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"type": schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "Reference type. See the parent attribute for supported types.",
		},
		"id": schema.StringAttribute{
			Required:            true,
			MarkdownDescription: "Referenced object or group ID. A `selector` must use `*`.",
		},
	}
}

func (r *mcpAccessRuleResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages one policy shown under MCP Servers > Access Policies. Supports importing existing rules, including Everything. " +
			"Policies grant access additively: deleting this rule leaves other grants in effect. This resource does not manage vMCP profiles. " +
			"See the [catalog and access-policy guide](../guides/catalogs-and-access-policies.md) for adopting and removing Everything.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The Obot access-control rule identifier.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"catalog_id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The catalog containing this policy, always `default`.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"display_name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "The policy's display name, for example `Everything`. The name does not determine its subjects or grants.",
				Validators: []validator.String{
					trimmedString(false),
				},
			},
			"subjects": schema.SetNestedAttribute{
				Required: true,
				MarkdownDescription: "Subjects receiving access: `user` (Obot user ID), `group` (identity-provider group ID), " +
					"`obotGroup` (Obot built-in group ID), or `selector` with ID `*` (everyone). An empty set grants no access. " +
					"Preserve actual subjects when importing; a rule named Everything need not grant everyone access.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: referenceAttributes(),
				},
				Validators: []validator.Set{
					ruleReferencesValidator{subjects: true},
				},
			},
			"resources": schema.SetNestedAttribute{
				Required: true,
				MarkdownDescription: "Targets receiving the grant: `mcpServerCatalogEntry`, `mcpServer`, or `selector` with ID `*` (everything). " +
					"Referenced entries and servers must belong to the default catalog. An empty set grants no access.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: referenceAttributes(),
				},
				Validators: []validator.Set{
					ruleReferencesValidator{},
				},
			},
		},
	}
}

func (r *mcpAccessRuleResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"id": identityschema.StringAttribute{
				RequiredForImport: true,
				Description:       "The Obot access-control rule identifier.",
			},
		},
	}
}

func (r *mcpAccessRuleResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if conn := configuredClient(req.ProviderData, &resp.Diagnostics); conn != nil {
		r.client = conn
	}
}

func referencesValue(ctx context.Context, refs []client.RuleReference, diagnostics *diag.Diagnostics) types.Set {
	values := make([]referenceModel, 0, len(refs))
	for _, ref := range refs {
		values = append(values, referenceModel{Type: types.StringValue(ref.Type), ID: types.StringValue(ref.ID)})
	}
	result, d := types.SetValueFrom(ctx, types.ObjectType{AttrTypes: referenceAttrTypes}, values)
	diagnostics.Append(d...)
	return result
}

func (m *accessRuleModel) fromAPI(ctx context.Context, rule *client.AccessRule, diagnostics *diag.Diagnostics) {
	m.ID = types.StringValue(rule.ID)
	m.CatalogID = types.StringValue(rule.CatalogID)
	m.DisplayName = types.StringValue(rule.DisplayName)
	m.Subjects = referencesValue(ctx, rule.Subjects, diagnostics)
	m.Resources = referencesValue(ctx, rule.Resources, diagnostics)
}

func (m *accessRuleModel) manifest(ctx context.Context, diagnostics *diag.Diagnostics) client.AccessRuleManifest {
	result := client.AccessRuleManifest{DisplayName: m.DisplayName.ValueString(), Subjects: []client.RuleReference{}, Resources: []client.RuleReference{}}
	var subjects, resources []referenceModel
	diagnostics.Append(m.Subjects.ElementsAs(ctx, &subjects, false)...)
	diagnostics.Append(m.Resources.ElementsAs(ctx, &resources, false)...)
	for _, ref := range subjects {
		result.Subjects = append(result.Subjects, client.RuleReference{Type: ref.Type.ValueString(), ID: ref.ID.ValueString()})
	}
	for _, ref := range resources {
		result.Resources = append(result.Resources, client.RuleReference{Type: ref.Type.ValueString(), ID: ref.ID.ValueString()})
	}
	return result
}

func setAccessRuleIdentity(ctx context.Context, identity *tfsdk.ResourceIdentity, id types.String, diagnostics *diag.Diagnostics) {
	if identity == nil {
		return
	}
	diagnostics.Append(identity.Set(ctx, accessRuleIdentityModel{ID: id})...)
}

func addGeneratedRuleError(diagnostics *diag.Diagnostics) {
	diagnostics.AddError("Generated MCP Access Rule Cannot Be Managed", "This policy is generated by Obot. Manage its owning object instead.")
}

func (r *mcpAccessRuleResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var model accessRuleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	manifest := model.manifest(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	rule, err := r.client.CreateAccessRule(ctx, defaultCatalogID, manifest)
	if err != nil {
		resp.Diagnostics.AddError("Error Creating MCP Access Rule", err.Error())
		return
	}
	model.fromAPI(ctx, rule, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	setAccessRuleIdentity(ctx, resp.Identity, model.ID, &resp.Diagnostics)
}

func (r *mcpAccessRuleResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var model accessRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	rule, err := r.client.GetAccessRule(ctx, defaultCatalogID, model.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error Reading MCP Access Rule", err.Error())
		return
	}
	if rule.Generated {
		addGeneratedRuleError(&resp.Diagnostics)
		return
	}
	model.fromAPI(ctx, rule, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	setAccessRuleIdentity(ctx, resp.Identity, model.ID, &resp.Diagnostics)
}

func (r *mcpAccessRuleResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var model accessRuleModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	before, err := r.client.GetAccessRule(ctx, defaultCatalogID, model.ID.ValueString())
	if err != nil {
		resp.Diagnostics.AddError("Error Reading MCP Access Rule", err.Error())
		return
	}
	if before.Generated {
		addGeneratedRuleError(&resp.Diagnostics)
		return
	}
	manifest := model.manifest(ctx, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	rule, err := r.client.UpdateAccessRule(ctx, defaultCatalogID, model.ID.ValueString(), manifest)
	if err != nil {
		resp.Diagnostics.AddError("Error Updating MCP Access Rule", err.Error())
		return
	}
	model.fromAPI(ctx, rule, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
	setAccessRuleIdentity(ctx, resp.Identity, model.ID, &resp.Diagnostics)
}

func (r *mcpAccessRuleResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var model accessRuleModel
	resp.Diagnostics.Append(req.State.Get(ctx, &model)...)
	if resp.Diagnostics.HasError() {
		return
	}
	id := model.ID.ValueString()
	before, err := r.client.GetAccessRule(ctx, defaultCatalogID, id)
	if client.IsNotFound(err) {
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error Reading MCP Access Rule", err.Error())
		return
	}
	if before.Generated {
		addGeneratedRuleError(&resp.Diagnostics)
		return
	}
	if err := r.client.DeleteAccessRule(ctx, defaultCatalogID, id); err != nil {
		resp.Diagnostics.AddError("Error Deleting MCP Access Rule", err.Error())
		return
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	for {
		_, err := r.client.GetAccessRule(ctx, defaultCatalogID, id)
		if client.IsNotFound(err) {
			return
		}
		if err != nil {
			resp.Diagnostics.AddError("Error Verifying MCP Access Rule Deletion", err.Error())
			return
		}
		timer := time.NewTimer(200 * time.Millisecond)
		select {
		case <-ctx.Done():
			timer.Stop()
			resp.Diagnostics.AddError("Error Waiting for MCP Access Rule Deletion", ctx.Err().Error())
			return
		case <-timer.C:
		}
	}
}

// ImportState accepts the rule ID, optionally prefixed with "default/", or an identity.
func (r *mcpAccessRuleResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := req.ID
	if id == "" && resp.Identity != nil {
		var identity accessRuleIdentityModel
		resp.Diagnostics.Append(resp.Identity.Get(ctx, &identity)...)
		id = identity.ID.ValueString()
	}
	id = strings.TrimPrefix(id, defaultCatalogID+"/")
	if !client.ValidObjectID(id) {
		resp.Diagnostics.AddError("Invalid MCP Access Rule Import ID", "Use the rule ID, for example acr1-everything, optionally as default/acr1-everything.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("catalog_id"), defaultCatalogID)...)
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	setAccessRuleIdentity(ctx, resp.Identity, types.StringValue(id), &resp.Diagnostics)
}
