resource "obot_mcp_tunnel" "customer" {
  display_name           = "Customer private network"
  allowed_urls           = ["https://mcp.internal.example/mcp"]
  token_rotation_version = 0
}

# Configure the Vault provider and an existing KV v2 mount named "secret".
resource "vault_kv_secret_v2" "tunnel" {
  mount     = "secret"
  name      = "obot/customer/mcp-tunnel"
  data_json = jsonencode({ token = obot_mcp_tunnel.customer.token })

  lifecycle {
    precondition {
      condition     = obot_mcp_tunnel.customer.token != null
      error_message = "The tunnel token is unavailable. Explicitly increase token_rotation_version to rotate and capture a new token before publishing it."
    }
  }
}
