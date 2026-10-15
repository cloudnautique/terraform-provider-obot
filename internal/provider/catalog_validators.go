// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"net/url"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/hashicorp/terraform-plugin-framework/types"
	"github.com/obot-platform/terraform-provider-obot/internal/client"
)

type obotStringValidator struct{ identifier, defaultCatalog bool }

func (v obotStringValidator) Description(context.Context) string {
	if v.defaultCatalog {
		return "Only the default MCP catalog supports source management; use default."
	}
	if v.identifier {
		return "Use an Obot object ID consisting of lowercase letters, digits, or hyphens, starting and ending with a letter or digit."
	}
	return "Use a nonempty value without surrounding whitespace."
}
func (v obotStringValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (v obotStringValidator) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()
	if s == "" || s != strings.TrimSpace(s) || (v.identifier && !client.ValidObjectID(s)) || (v.defaultCatalog && s != "default") {
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Obot Configuration", v.Description(ctx))
	}
}

type catalogSourcesValidator struct{}

func (catalogSourcesValidator) Description(context.Context) string {
	return "Sources must be complete HTTPS URLs without embedded credentials or surrounding whitespace. Use an empty set to remove all source-managed entries."
}
func (v catalogSourcesValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (v catalogSourcesValidator) ValidateSet(ctx context.Context, req validator.SetRequest, resp *validator.SetResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	for _, element := range req.ConfigValue.Elements() {
		value, ok := element.(types.String)
		if ok && value.IsUnknown() {
			continue
		}
		if !ok || value.IsNull() {
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid Catalog Source", v.Description(ctx))
			continue
		}
		s := value.ValueString()
		u, err := url.Parse(s)
		if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || strings.TrimSpace(s) != s {
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid Catalog Source", v.Description(ctx))
		}
	}
}

type ruleReferencesValidator struct{ subjects bool }

func (v ruleReferencesValidator) Description(context.Context) string {
	return "Use valid subject or resource types and nonempty IDs; selector IDs must be *. Empty sets grant no access."
}
func (v ruleReferencesValidator) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}
func (v ruleReferencesValidator) ValidateSet(ctx context.Context, req validator.SetRequest, resp *validator.SetResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	for _, element := range req.ConfigValue.Elements() {
		obj, ok := element.(types.Object)
		if ok && obj.IsUnknown() {
			continue
		}
		if !ok || obj.IsNull() {
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid MCP Access Rule Reference", v.Description(ctx))
			continue
		}
		typeValue, tok := obj.Attributes()["type"].(types.String)
		idValue, iok := obj.Attributes()["id"].(types.String)
		if !tok || !iok || typeValue.IsNull() || idValue.IsNull() {
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid MCP Access Rule Reference", v.Description(ctx))
			continue
		}
		if typeValue.IsUnknown() || idValue.IsUnknown() {
			continue
		}
		kind, id := typeValue.ValueString(), idValue.ValueString()
		validType := kind == "selector"
		if v.subjects {
			validType = validType || kind == "user" || kind == "group" || kind == "obotGroup"
		} else {
			validType = validType || kind == "mcpServerCatalogEntry" || kind == "mcpServer"
		}
		if !validType || id == "" || strings.TrimSpace(id) != id || (kind == "selector" && id != "*") {
			resp.Diagnostics.AddAttributeError(req.Path, "Invalid MCP Access Rule Reference", v.Description(ctx))
		}
	}
}
