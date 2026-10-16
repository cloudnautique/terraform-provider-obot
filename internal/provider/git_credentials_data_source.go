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
	_ datasource.DataSource              = &gitCredentialsDataSource{}
	_ datasource.DataSourceWithConfigure = &gitCredentialsDataSource{}
)

type gitCredentialsDataSource struct {
	client *client.Client
}

type gitCredentialsDataModel struct {
	Credentials types.List `tfsdk:"credentials"`
}

func newGitCredentialsDataSource() datasource.DataSource {
	return &gitCredentialsDataSource{}
}

func (d *gitCredentialsDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_git_credentials"
}

func (d *gitCredentialsDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Lists Git credentials, sorted by ID, with the objects that use each one. Tokens are never returned.",
		Attributes: map[string]schema.Attribute{
			"credentials": schema.ListNestedAttribute{
				Computed:            true,
				MarkdownDescription: "Git credentials. An installation with none returns an empty list.",
				NestedObject: schema.NestedAttributeObject{
					Attributes: gitCredentialDataAttributes(),
				},
			},
		},
	}
}

func (d *gitCredentialsDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if conn := configuredClient(req.ProviderData, &resp.Diagnostics); conn != nil {
		d.client = conn
	}
}

func (d *gitCredentialsDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	credentials, err := d.client.ListGitCredentials(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error Listing Git Credentials", err.Error())
		return
	}
	sort.Slice(credentials, func(i, j int) bool { return credentials[i].ID < credentials[j].ID })
	models := make([]gitCredentialDataModel, 0, len(credentials))
	for _, credential := range credentials {
		models = append(models, newGitCredentialDataModel(ctx, credential, &resp.Diagnostics))
	}
	list, diags := types.ListValueFrom(ctx, types.ObjectType{AttrTypes: gitCredentialDataModel{}.attrTypes()}, models)
	resp.Diagnostics.Append(diags...)
	if resp.Diagnostics.HasError() {
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &gitCredentialsDataModel{Credentials: list})...)
}
