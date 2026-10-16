resource "obot_mcp_catalog_source" "team" {
  url = "https://github.com/example/obot-catalog/tree/main/mcp"

  # For a private source, reference an existing Obot Git credential:
  # git_credential_id = "gc1example"
}
