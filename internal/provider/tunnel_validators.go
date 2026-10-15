// SPDX-License-Identifier: MPL-2.0

package provider

import (
	"context"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework-validators/stringvalidator"
	"github.com/hashicorp/terraform-plugin-framework/schema/validator"
	"github.com/obot-platform/terraform-provider-obot/internal/client"
)

var (
	trimmedNonempty = regexp.MustCompile(`(?s)^\S(.*\S)?$`)
	trimmedOrEmpty  = regexp.MustCompile(`(?s)^(\S(.*\S)?)?$`)
)

// trimmedString rejects surrounding whitespace, which Obot would strip and
// so report back as drift.
func trimmedString(allowEmpty bool) validator.String {
	if allowEmpty {
		return stringvalidator.RegexMatches(trimmedOrEmpty, "must not have leading or trailing whitespace")
	}
	return stringvalidator.RegexMatches(trimmedNonempty, "must not be empty or have leading or trailing whitespace")
}

// tunnelID validates a user-supplied tunnel ID before it reaches an API path.
func tunnelID() validator.String {
	return stringvalidator.RegexMatches(client.TunnelIDPattern,
		"must be an Obot-generated tunnel ID: 1-63 lowercase letters, digits, or hyphens, starting and ending with a letter or digit")
}

// allowedURLRule mirrors Obot's rule that a pattern has at most one wildcard,
// at its beginning or end.
type allowedURLRule struct{}

func (allowedURLRule) Description(context.Context) string {
	return "must contain at most one wildcard, at the beginning or end"
}

func (v allowedURLRule) MarkdownDescription(ctx context.Context) string {
	return v.Description(ctx)
}

func (v allowedURLRule) ValidateString(ctx context.Context, req validator.StringRequest, resp *validator.StringResponse) {
	if req.ConfigValue.IsNull() || req.ConfigValue.IsUnknown() {
		return
	}
	s := req.ConfigValue.ValueString()
	switch strings.Count(s, "*") {
	case 0:
	case 1:
		if strings.HasPrefix(s, "*") || strings.HasSuffix(s, "*") {
			return
		}
		fallthrough
	default:
		resp.Diagnostics.AddAttributeError(req.Path, "Invalid Tunnel Allowed URL", "Allowed URL "+v.Description(ctx)+", got: "+s)
	}
}
