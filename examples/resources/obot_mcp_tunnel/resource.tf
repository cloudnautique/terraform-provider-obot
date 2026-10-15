resource "obot_mcp_tunnel" "customer" {
  display_name = "Customer private network"
  description  = "Private MCP endpoints reached through the customer's agent"
  allowed_urls = ["https://mcp.internal.example/mcp"]

  # Increase this value to explicitly rotate and revoke the previous token.
  token_rotation_version = 0
}
