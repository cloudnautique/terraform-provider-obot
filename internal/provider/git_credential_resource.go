// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"

	"github.com/hashicorp/terraform-plugin-framework-validators/resourcevalidator"
	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
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

var (
	_ resource.Resource                     = &gitCredentialResource{}
	_ resource.ResourceWithConfigure        = &gitCredentialResource{}
	_ resource.ResourceWithConfigValidators = &gitCredentialResource{}
	_ resource.ResourceWithImportState      = &gitCredentialResource{}
	_ resource.ResourceWithIdentity         = &gitCredentialResource{}
)

type gitCredentialResource struct {
	client *client.Client
}

type gitCredentialModel struct {
	ID             types.String `tfsdk:"id"`
	DisplayName    types.String `tfsdk:"display_name"`
	Host           types.String `tfsdk:"host"`
	Token          types.String `tfsdk:"token"`
	TokenWO        types.String `tfsdk:"token_wo"`
	TokenWOVersion types.Int64  `tfsdk:"token_wo_version"`
}

type gitCredentialIdentityModel struct {
	ID types.String `tfsdk:"id"`
}

func newGitCredentialResource() resource.Resource {
	return &gitCredentialResource{}
}

func (r *gitCredentialResource) Metadata(_ context.Context, req resource.MetadataRequest, resp *resource.MetadataResponse) {
	resp.TypeName = req.ProviderTypeName + "_git_credential"
}

// gitHost validates a host as Obot stores it, so the configured value never drifts.
func gitHost() validator.String {
	return stringvalidator.RegexMatches(client.GitHostPattern,
		"must be a lowercase host name with an optional port, such as github.com or git.example.com:8443, without a scheme or path")
}

func (r *gitCredentialResource) Schema(_ context.Context, _ resource.SchemaRequest, resp *resource.SchemaResponse) {
	resp.Schema = schema.Schema{
		MarkdownDescription: "Manages a Git credential: a token Obot uses to read private HTTPS Git sources on one host, " +
			"such as catalog sources that set `git_credential_id`. Obot never returns the token. " +
			"Set exactly one of `token`, which is stored in Terraform state, or `token_wo`, which is never stored and needs Terraform 1.11 or later. " +
			"Obot refuses to delete a credential while a source uses it.",
		Attributes: map[string]schema.Attribute{
			"id": schema.StringAttribute{
				Computed:            true,
				MarkdownDescription: "The generated Obot Git credential ID.",
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.UseStateForUnknown(),
				},
			},
			"display_name": schema.StringAttribute{
				Required:            true,
				MarkdownDescription: "Human-readable name. Names need not be unique.",
				Validators: []validator.String{
					trimmedString(false),
				},
			},
			"host": schema.StringAttribute{
				Required: true,
				MarkdownDescription: "Git host the credential is used for, such as `github.com`, with an optional port. " +
					"Sources must be on this host. Changing it replaces the credential.",
				Validators: []validator.String{
					gitHost(),
				},
				PlanModifiers: []planmodifier.String{
					stringplanmodifier.RequiresReplace(),
				},
			},
			"token": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				MarkdownDescription: "Token Obot sends as the HTTPS password, such as a GitHub personal access token. " +
					"Stored in Terraform state; prefer `token_wo` with Terraform 1.11 or later. Changing it updates the stored token.",
				Validators: []validator.String{
					trimmedString(false),
				},
			},
			"token_wo": schema.StringAttribute{
				Optional:  true,
				Sensitive: true,
				WriteOnly: true,
				MarkdownDescription: "Write-only token, never stored in Terraform state. Requires Terraform 1.11 or later and `token_wo_version`. " +
					"Change `token_wo_version` to send a new token.",
				Validators: []validator.String{
					trimmedString(false),
				},
			},
			"token_wo_version": schema.Int64Attribute{
				Optional:            true,
				MarkdownDescription: "Version of `token_wo`. Terraform cannot compare write-only values, so change this to send `token_wo` again.",
			},
		},
	}
}

func (r *gitCredentialResource) ConfigValidators(context.Context) []resource.ConfigValidator {
	return []resource.ConfigValidator{
		resourcevalidator.ExactlyOneOf(path.MatchRoot("token"), path.MatchRoot("token_wo")),
		resourcevalidator.RequiredTogether(path.MatchRoot("token_wo"), path.MatchRoot("token_wo_version")),
	}
}

func (r *gitCredentialResource) IdentitySchema(_ context.Context, _ resource.IdentitySchemaRequest, resp *resource.IdentitySchemaResponse) {
	resp.IdentitySchema = identityschema.Schema{
		Attributes: map[string]identityschema.Attribute{
			"id": identityschema.StringAttribute{
				RequiredForImport: true,
				Description:       "The generated Obot Git credential ID.",
			},
		},
	}
}

func (r *gitCredentialResource) Configure(_ context.Context, req resource.ConfigureRequest, resp *resource.ConfigureResponse) {
	if conn := configuredClient(req.ProviderData, &resp.Diagnostics); conn != nil {
		r.client = conn
	}
}

func (m *gitCredentialModel) setMetadata(credential *client.GitCredential) {
	m.ID = types.StringValue(credential.ID)
	m.DisplayName = types.StringValue(credential.DisplayName)
	m.Host = types.StringValue(credential.Host)
	m.TokenWO = types.StringNull()
}

func setGitCredentialIdentity(ctx context.Context, identity *tfsdk.ResourceIdentity, id types.String, diagnostics *diag.Diagnostics) {
	if identity == nil {
		return
	}
	diagnostics.Append(identity.Set(ctx, gitCredentialIdentityModel{ID: id})...)
}

// configuredToken returns the token from token or, being write-only, from the configuration.
func configuredToken(ctx context.Context, plan gitCredentialModel, config tfsdk.Config, diagnostics *diag.Diagnostics) string {
	if !plan.Token.IsNull() {
		return plan.Token.ValueString()
	}
	var token types.String
	diagnostics.Append(config.GetAttribute(ctx, path.Root("token_wo"), &token)...)
	return token.ValueString()
}

func (r *gitCredentialResource) Create(ctx context.Context, req resource.CreateRequest, resp *resource.CreateResponse) {
	var plan gitCredentialModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	if resp.Diagnostics.HasError() {
		return
	}
	token := configuredToken(ctx, plan, req.Config, &resp.Diagnostics)
	if resp.Diagnostics.HasError() {
		return
	}
	credential, err := r.client.CreateGitCredential(ctx, client.GitCredentialManifest{
		DisplayName: plan.DisplayName.ValueString(),
		Host:        plan.Host.ValueString(),
		Token:       token,
	})
	if err != nil {
		resp.Diagnostics.AddError("Error Creating Git Credential", err.Error())
		return
	}
	plan.setMetadata(credential)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	setGitCredentialIdentity(ctx, resp.Identity, plan.ID, &resp.Diagnostics)
}

func (r *gitCredentialResource) Read(ctx context.Context, req resource.ReadRequest, resp *resource.ReadResponse) {
	var state gitCredentialModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	credential, err := r.client.GetGitCredential(ctx, state.ID.ValueString())
	if client.IsNotFound(err) {
		resp.State.RemoveResource(ctx)
		return
	}
	if err != nil {
		resp.Diagnostics.AddError("Error Reading Git Credential", err.Error())
		return
	}
	state.setMetadata(credential)
	if !credential.TokenConfigured {
		// Obot has lost the token, so plan to send the configured one again.
		state.Token = types.StringNull()
		state.TokenWOVersion = types.Int64Null()
	}
	resp.Diagnostics.Append(resp.State.Set(ctx, &state)...)
	setGitCredentialIdentity(ctx, resp.Identity, state.ID, &resp.Diagnostics)
}

func (r *gitCredentialResource) Update(ctx context.Context, req resource.UpdateRequest, resp *resource.UpdateResponse) {
	var plan, state gitCredentialModel
	resp.Diagnostics.Append(req.Plan.Get(ctx, &plan)...)
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	manifest := client.GitCredentialManifest{
		DisplayName: plan.DisplayName.ValueString(),
		Host:        plan.Host.ValueString(),
	}
	// An empty token keeps the stored one; send a token only when it changed.
	if !plan.Token.Equal(state.Token) || !plan.TokenWOVersion.Equal(state.TokenWOVersion) {
		manifest.Token = configuredToken(ctx, plan, req.Config, &resp.Diagnostics)
	}
	if resp.Diagnostics.HasError() {
		return
	}
	credential, err := r.client.UpdateGitCredential(ctx, plan.ID.ValueString(), manifest)
	if err != nil {
		resp.Diagnostics.AddError("Error Updating Git Credential", err.Error())
		return
	}
	plan.setMetadata(credential)
	resp.Diagnostics.Append(resp.State.Set(ctx, &plan)...)
	setGitCredentialIdentity(ctx, resp.Identity, plan.ID, &resp.Diagnostics)
}

func (r *gitCredentialResource) Delete(ctx context.Context, req resource.DeleteRequest, resp *resource.DeleteResponse) {
	var state gitCredentialModel
	resp.Diagnostics.Append(req.State.Get(ctx, &state)...)
	if resp.Diagnostics.HasError() {
		return
	}
	if err := r.client.DeleteGitCredential(ctx, state.ID.ValueString()); err != nil {
		resp.Diagnostics.AddError("Error Deleting Git Credential", err.Error())
	}
}

// ImportState accepts an import ID or an identity. Obot never returns the
// token, so the next apply sends the configured one.
func (r *gitCredentialResource) ImportState(ctx context.Context, req resource.ImportStateRequest, resp *resource.ImportStateResponse) {
	id := req.ID
	if id == "" && resp.Identity != nil {
		var identity gitCredentialIdentityModel
		resp.Diagnostics.Append(resp.Identity.Get(ctx, &identity)...)
		id = identity.ID.ValueString()
	}
	if !client.ValidObjectID(id) {
		resp.Diagnostics.AddError("Invalid Git Credential Import ID", "Import using the generated Obot Git credential ID, such as gc1abc123.")
		return
	}
	resp.Diagnostics.Append(resp.State.SetAttribute(ctx, path.Root("id"), id)...)
	setGitCredentialIdentity(ctx, resp.Identity, types.StringValue(id), &resp.Diagnostics)
	resp.Diagnostics.AddWarning("Git Credential Token Not Imported",
		"Obot never returns a Git credential's token. The next apply sends the configured token, replacing the stored one.")
}
