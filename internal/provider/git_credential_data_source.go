// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/datasourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
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
	_ datasource.DataSource                     = &gitCredentialDataSource{}
	_ datasource.DataSourceWithConfigure        = &gitCredentialDataSource{}
	_ datasource.DataSourceWithConfigValidators = &gitCredentialDataSource{}
)

type gitCredentialDataSource struct {
	client *client.Client
}

// gitCredentialDataModel is shared by the single credential data source and
// each element of the credential list.
type gitCredentialDataModel struct {
	ID              types.String `tfsdk:"id"`
	DisplayName     types.String `tfsdk:"display_name"`
	Host            types.String `tfsdk:"host"`
	TokenConfigured types.Bool   `tfsdk:"token_configured"`
	UsedBy          types.List   `tfsdk:"used_by"`
}

type gitCredentialUseModel struct {
	Type        types.String `tfsdk:"type"`
	ID          types.String `tfsdk:"id"`
	DisplayName types.String `tfsdk:"display_name"`
}

func (gitCredentialUseModel) attrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"type":         types.StringType,
		"id":           types.StringType,
		"display_name": types.StringType,
	}
}

func (gitCredentialDataModel) attrTypes() map[string]attr.Type {
	return map[string]attr.Type{
		"id":               types.StringType,
		"display_name":     types.StringType,
		"host":             types.StringType,
		"token_configured": types.BoolType,
		"used_by":          types.ListType{ElemType: types.ObjectType{AttrTypes: gitCredentialUseModel{}.attrTypes()}},
	}
}

func newGitCredentialDataModel(ctx context.Context, credential client.GitCredential, diagnostics *diag.Diagnostics) gitCredentialDataModel {
	uses := []gitCredentialUseModel{}
	for _, group := range []struct {
		kind string
		uses []client.GitCredentialUse
	}{
		{"mcp_catalog", credential.Uses.MCPCatalogs},
		{"system_mcp_catalog", credential.Uses.SystemMCPCatalogs},
		{"skill_repository", credential.Uses.SkillRepositories},
	} {
		for _, use := range group.uses {
			uses = append(uses, gitCredentialUseModel{Type: types.StringValue(group.kind), ID: types.StringValue(use.ID), DisplayName: types.StringValue(use.DisplayName)})
		}
	}
	usedBy, d := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: gitCredentialUseModel{}.attrTypes()}, uses)
	diagnostics.Append(d...)
	return gitCredentialDataModel{
		ID:              types.StringValue(credential.ID),
		DisplayName:     types.StringValue(credential.DisplayName),
		Host:            types.StringValue(credential.Host),
		TokenConfigured: types.BoolValue(credential.TokenConfigured),
		UsedBy:          usedBy,
	}
}

func gitCredentialDataAttributes() map[string]schema.Attribute {
	return map[string]schema.Attribute{
		"id": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "The generated Obot Git credential ID, for a catalog source's `git_credential_id`.",
		},
		"display_name": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Human-readable name.",
		},
		"host": schema.StringAttribute{
			Computed:            true,
			MarkdownDescription: "Git host the credential is used for.",
		},
		"token_configured": schema.BoolAttribute{
			Computed:            true,
			MarkdownDescription: "Whether Obot has a token stored for the credential. The token itself is never returned.",
		},
		"used_by": schema.ListNestedAttribute{
			Computed:            true,
			MarkdownDescription: "Objects that read sources with the credential, as last recorded by Obot.",
			NestedObject: schema.NestedAttributeObject{
				Attributes: map[string]schema.Attribute{
					"type": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "Kind of object: `mcp_catalog`, `system_mcp_catalog`, or `skill_repository`.",
					},
					"id": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "The object's ID, such as `default` for the default MCP catalog.",
					},
					"display_name": schema.StringAttribute{
						Computed:            true,
						MarkdownDescription: "The object's display name.",
					},
				},
			},
		},
	}
}

func newGitCredentialDataSource() datasource.DataSource {
	return &gitCredentialDataSource{}
}

func (d *gitCredentialDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_git_credential"
}

func (d *gitCredentialDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	attributes := gitCredentialDataAttributes()
	attributes["id"] = schema.StringAttribute{
		Optional:            true,
		Computed:            true,
		MarkdownDescription: "Git credential ID to look up. Set exactly one of `id` or `display_name`.",
		Validators: []validator.String{
			stringvalidator.RegexMatches(client.ObjectIDPattern, "must be an Obot Git credential ID"),
		},
	}
	attributes["display_name"] = schema.StringAttribute{
		Optional:            true,
		Computed:            true,
		MarkdownDescription: "Display name to look up. Exactly one credential must have it.",
		Validators: []validator.String{
			trimmedString(false),
		},
	}
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads one Git credential's metadata, by ID or by a unique display name, for example to find the ID " +
			"of a credential created in the Obot UI. Errors if no credential, or more than one, matches. The token is never returned.",
		Attributes: attributes,
	}
}

func (d *gitCredentialDataSource) ConfigValidators(context.Context) []datasource.ConfigValidator {
	return []datasource.ConfigValidator{
		datasourcevalidator.ExactlyOneOf(path.MatchRoot("id"), path.MatchRoot("display_name")),
	}
}

func (d *gitCredentialDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if conn := configuredClient(req.ProviderData, &resp.Diagnostics); conn != nil {
		d.client = conn
	}
}

func (d *gitCredentialDataSource) Read(ctx context.Context, req datasource.ReadRequest, resp *datasource.ReadResponse) {
	var data gitCredentialDataModel
	resp.Diagnostics.Append(req.Config.Get(ctx, &data)...)
	if resp.Diagnostics.HasError() {
		return
	}
	credential, err := d.find(ctx, data)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Git Credential", err.Error())
		return
	}
	data = newGitCredentialDataModel(ctx, *credential, &resp.Diagnostics)
	resp.Diagnostics.Append(resp.State.Set(ctx, &data)...)
}

func (d *gitCredentialDataSource) find(ctx context.Context, data gitCredentialDataModel) (*client.GitCredential, error) {
	if !data.ID.IsNull() {
		credential, err := d.client.GetGitCredential(ctx, data.ID.ValueString())
		if client.IsNotFound(err) {
			return nil, fmt.Errorf("no Git credential has ID %q", data.ID.ValueString())
		}
		return credential, err
	}
	credentials, err := d.client.ListGitCredentials(ctx)
	if err != nil {
		return nil, err
	}
	name := data.DisplayName.ValueString()
	var matches []client.GitCredential
	var ids []string
	for _, credential := range credentials {
		if credential.DisplayName == name {
			matches = append(matches, credential)
			ids = append(ids, credential.ID)
		}
	}
	switch len(matches) {
	case 0:
		return nil, fmt.Errorf("no Git credential has display name %q", name)
	case 1:
		return &matches[0], nil
	default:
		return nil, fmt.Errorf("%d Git credentials have display name %q (%s); look one up by id instead", len(matches), name, strings.Join(ids, ", "))
	}
}
