# Find the ID of a credential created in the Obot UI.
data "obot_git_credential" "github" {
  display_name = "GitHub catalogs"
}

resource "obot_mcp_catalog_source" "private" {
  url               = "https://github.com/example/private-catalog"
  git_credential_id = data.obot_git_credential.github.id
}
