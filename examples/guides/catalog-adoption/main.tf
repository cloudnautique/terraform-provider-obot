# Adopt a source that already exists in Obot, such as the shipped catalog.
# Copy its URL and any Git credential ID from data.obot_mcp_catalog.default.
resource "obot_mcp_catalog_source" "shipped" {
  url = "https://github.com/obot-platform/mcp-catalog/v2-schema"
}

import {
  to = obot_mcp_catalog_source.shipped
  id = "https://github.com/obot-platform/mcp-catalog/v2-schema"
}

# Add your own source. Sources added in the Obot UI are left unchanged.
resource "obot_mcp_catalog_source" "team" {
  url               = "https://github.com/example/obot-catalog/tree/main/mcp"
  git_credential_id = var.team_catalog_git_credential_id
}

variable "team_catalog_git_credential_id" {
  description = "ID of an existing Obot Git credential for the private team catalog, or null for a public one."
  type        = string
  default     = null
}
