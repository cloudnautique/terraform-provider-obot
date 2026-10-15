resource "obot_mcp_tunnel" "customer" {
  display_name           = "Customer private network"
  allowed_urls           = ["https://mcp.internal.example/mcp"]
  token_rotation_version = 0
}

resource "aws_secretsmanager_secret" "tunnel" {
  name = "obot/customer/mcp-tunnel"
}

resource "aws_secretsmanager_secret_version" "tunnel" {
  secret_id     = aws_secretsmanager_secret.tunnel.id
  secret_string = obot_mcp_tunnel.customer.token

  lifecycle {
    precondition {
      condition     = obot_mcp_tunnel.customer.token != null
      error_message = "The tunnel token is unavailable. Explicitly increase token_rotation_version to rotate and capture a new token before publishing it."
    }
  }
}
