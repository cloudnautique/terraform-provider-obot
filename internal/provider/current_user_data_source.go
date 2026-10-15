// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/obot-platform/terraform-provider-obot/internal/client"
)

var (
	_ datasource.DataSource              = &currentUserDataSource{}
	_ datasource.DataSourceWithConfigure = &currentUserDataSource{}
)

type currentUserDataSource struct {
	client *client.Client
}

type currentUserModel struct {
	ID       types.String `tfsdk:"id"`
	Email    types.String `tfsdk:"email"`
	Username types.String `tfsdk:"username"`
}

func newCurrentUserDataSource() datasource.DataSource {
	return &currentUserDataSource{}
}

func (d *currentUserDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_current_user"
}

func (d *currentUserDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Reads the Obot user authenticated by the provider's API key.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed: true, MarkdownDescription: "The Obot user identifier.",
			},
			"email": schema.StringAttribute{
				Computed: true, MarkdownDescription: "The authenticated user's email address.",
			},
			"username": schema.StringAttribute{
				Computed: true, MarkdownDescription: "The authenticated user's Obot username.",
			},
		},
	}
}

func (d *currentUserDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
	if conn := configuredClient(req.ProviderData, &resp.Diagnostics); conn != nil {
		d.client = conn
	}
}

func (d *currentUserDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	user, err := d.client.CurrentUser(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Current Obot User", err.Error())
		return
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &currentUserModel{
		ID:       types.StringValue(user.ID),
		Email:    types.StringValue(user.Email),
		Username: types.StringValue(user.Username),
	})...)
}
