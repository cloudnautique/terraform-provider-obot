variable "github_token" {
  description = "Token with read access to the private catalog repository."
  type        = string
  sensitive   = true
  ephemeral   = true
}

resource "obot_git_credential" "github" {
  display_name = "GitHub catalogs"
  host         = "github.com"

  # Write-only: never stored in state. Increase the version to send a new token.
  token_wo         = var.github_token
  token_wo_version = 1
}

resource "obot_mcp_catalog_source" "private" {
  url               = "https://github.com/example/private-catalog"
  git_credential_id = obot_git_credential.github.id
}
