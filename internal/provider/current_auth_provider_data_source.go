// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"fmt"

	"github.com/hashicorp/terraform-plugin-framework/datasource"
	"github.com/hashicorp/terraform-plugin-framework/datasource/schema"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/obot-platform/terraform-provider-obot/internal/client"
)

var (
	_ datasource.DataSource              = &currentAuthProviderDataSource{}
	_ datasource.DataSourceWithConfigure = &currentAuthProviderDataSource{}
)

type currentAuthProviderDataSource struct{ client *client.Client }
type authProviderModel struct {
	ID            types.String `tfsdk:"id"`
	Name          types.String `tfsdk:"name"`
	Namespace     types.String `tfsdk:"namespace"`
	GroupIDPrefix types.String `tfsdk:"group_id_prefix"`
}

func newCurrentAuthProviderDataSource() datasource.DataSource {
	return &currentAuthProviderDataSource{}
}
func (d *currentAuthProviderDataSource) Metadata(_ context.Context, req datasource.MetadataRequest, resp *datasource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_current_auth_provider"
}
func (d *currentAuthProviderDataSource) Schema(_ context.Context, _ datasource.SchemaRequest, resp *datasource.SchemaResponse) {
	resp.Schema = schema.Schema{MarkdownDescription: "Reads the sole configured, non-staged authentication provider, exposing public metadata only. Errors when none or multiple match: Obot does not expose an authoritative active-provider flag. Requires an administrator token so staged replacements are visible. This is installation configuration, not the login provenance of the current API key.", Attributes: map[string]schema.Attribute{
		"id":              schema.StringAttribute{Computed: true, MarkdownDescription: "The Obot authentication provider identifier."},
		"name":            schema.StringAttribute{Computed: true, MarkdownDescription: "The authentication provider's display name."},
		"namespace":       schema.StringAttribute{Computed: true, MarkdownDescription: "The Obot namespace containing this authentication provider."},
		"group_id_prefix": schema.StringAttribute{Computed: true, MarkdownDescription: "The identity-provider group ID prefix; empty when the provider does not declare one. Older Obot versions may not expose this field."},
	}}
}
func (d *currentAuthProviderDataSource) Configure(_ context.Context, req datasource.ConfigureRequest, resp *datasource.ConfigureResponse) {
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
func (d *currentAuthProviderDataSource) Read(ctx context.Context, _ datasource.ReadRequest, resp *datasource.ReadResponse) {
	result, err := d.client.CurrentAuthProvider(ctx)
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Current Auth Provider", err.Error())
		return
	}
	model := authProviderModel{ID: types.StringValue(result.ID), Name: types.StringValue(result.Name), Namespace: types.StringValue(result.Namespace), GroupIDPrefix: types.StringValue(result.GroupIDPrefix)}
	resp.Diagnostics.Append(resp.State.Set(ctx, &model)...)
}
