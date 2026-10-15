variable "mcp_catalog_sources" {
  description = "Initially copy the live sources; later replace with the desired source URLs."
  type        = set(string)
}

variable "mcp_catalog_git_credential_ids" {
  description = "Existing Obot Git credential IDs keyed by source URL. Copy live references during adoption."
  type        = map(string)
  default     = {}
}

resource "obot_mcp_catalog_sources" "default" {
  sources                   = var.mcp_catalog_sources
  source_git_credential_ids = var.mcp_catalog_git_credential_ids
}

import {
  to = obot_mcp_catalog_sources.default
  id = "default"
}
