resource "obot_mcp_catalog_sources" "default" {
  sources = ["https://github.com/example/obot-catalog/tree/main/mcp"]

  # Include every desired source: this resource owns the complete collection.
  # For a private source, reference an existing Obot Git credential:
  # source_git_credential_ids = {
  #   "https://github.com/example/obot-catalog/tree/main/mcp" = "gc1example"
  # }
}
