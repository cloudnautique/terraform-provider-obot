# Examples

- `provider/provider.tf` is included in the generated provider documentation.
- `dev/main.tf` is the local development smoke configuration. Run it using
  the development overrides described in the root README.

- `resources/obot_mcp_tunnel/` shows creation and both import forms. Replace
  the example import ID with an existing tunnel's generated ID before importing.
- `data-sources/obot_mcp_tunnel/` and `data-sources/obot_mcp_tunnels/` read
  metadata and token previews.
- `guides/tunnel-aws/` and `guides/tunnel-vault/` publish a newly captured token.
  Use one destination example at a time and configure its provider credentials.

The resource/data-source/guide examples are module snippets. Your root module
must declare `obot-platform/obot` as the Obot provider source. The
[tunnel guide](../docs/guides/mcp-tunnels.md) explains import, missing tokens,
rotation downtime, and state protection.
