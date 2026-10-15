variable "tunnel_id" {
  type        = string
  description = "Generated Obot tunnel ID to look up."
}

data "obot_mcp_tunnel" "existing" {
  id = var.tunnel_id
}

# Metadata and token_preview are available. Complete tokens cannot be read.
