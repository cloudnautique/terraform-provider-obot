## 0.1.0 (Unreleased)

FEATURES:

- Add the `obot_mcp_tunnel` resource with safe import, one-time token capture,
  explicit rotation, external-rotation detection, and lifecycle acceptance tests.
- Add `obot_mcp_tunnel` and `obot_mcp_tunnels` metadata data sources and a
  tunnel guide covering import, agents, AWS Secrets Manager, and Vault.
- Support importing `obot_mcp_tunnel` by resource identity (Terraform 1.12+).

IMPROVEMENTS:

- Include the Obot server's explanation, with credentials and token-like
  values redacted, in API error diagnostics, such as the catalog entries that
  block a tunnel update or deletion.
- Use `terraform-plugin-framework-validators` for configuration validation.
- Identify the Terraform version in the provider's User-Agent.
- Tolerate future changes to the tunnel token-preview format: reads keep
  working and a cached token is kept with a warning instead of failing.

BUG FIXES:

- Send an empty tunnel `description` explicitly instead of omitting it.
- Clear the revoked token from state when a rotation succeeds but returns an
  unusable secret.
- Keep planned token values during a metadata update so an external rotation
  between refresh and apply cannot cause an inconsistent-result error.
- Report oversized API responses explicitly instead of as a decoding error.

- Add main/release Obot source and CLI tooling, isolated Docker test instances,
  local-auth test users, project skills, and acceptance-test CI.
- Add authenticated provider configuration and the `obot_current_user` data source.

- Bootstrap the Obot provider identity, build tooling, generated documentation,
  and local Terraform development workflow.
