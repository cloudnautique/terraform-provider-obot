// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/obot-platform/terraform-provider-obot/internal/client"
)

type tunnelStringValidator struct{ nonempty, identifier bool }

func (v tunnelStringValidator) Description(context.Context) string {
	if v.identifier {
		return "Use an Obot-generated tunnel ID: 1-63 lowercase letters, digits, or hyphens, starting and ending with a letter or digit."
	}
	return "Do not include leading or trailing whitespace; display names must not be empty."
}
func (v tunnelStringValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (v tunnelStringValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()
	if (v.identifier && !client.ValidTunnelID(s)) || (!v.identifier && (s != strings.TrimSpace(s) || (v.nonempty && s == ""))) {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Tunnel Configuration", v.Description(ctx))
	}
}

type tunnelURLsValidator struct{}

func (tunnelURLsValidator) Description(context.Context) string {
	return "Allowed URLs must be nonempty, have no surrounding whitespace, and contain at most one wildcard, at the beginning or end. An empty set denies all destinations."
}
func (v tunnelURLsValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (v tunnelURLsValidator) ValidateSet(ctx context.Context, req validator.SetRequest, resp *validator.SetResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	for _, element := range req.ConfigValue.Elements() {
		value, ok := element.(types.String)
		if !ok || value.IsNull() {
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid Tunnel Allowed URL", v.Description(ctx))
			continue
		}
		if value.IsUnknown() {
			continue
		}
		s := value.ValueString()
		stars := strings.Count(s, "*")
		if s == "" || s != strings.TrimSpace(s) || stars > 1 || (stars == 1 && !strings.HasPrefix(s, "*") && !strings.HasSuffix(s, "*")) {
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid Tunnel Allowed URL", v.Description(ctx))
		}
	}
}

type tunnelRotationValidator struct{}

func (tunnelRotationValidator) Description(context.Context) string {
	return "The token rotation version must be a nonnegative integer."
}
func (v tunnelRotationValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (v tunnelRotationValidator) ValidateInt64(ctx context.Context, req validator.Int64Request, resp *validator.Int64Response) {
	if !req.ConfigValue.IsNull() && !req.ConfigValue.IsUnknown() && req.ConfigValue.ValueInt64() < 0 {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Tunnel Rotation Version", v.Description(ctx))
	}
}
