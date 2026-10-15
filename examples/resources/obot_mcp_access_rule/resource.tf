resource "obot_mcp_access_rule" "administrators" {
  display_name = "Administrator MCP access"
  subjects = [{
    type = "obotGroup"
    id   = "admin"
  }]
  resources = [{
    type = "selector"
    id   = "*"
  }]
}
