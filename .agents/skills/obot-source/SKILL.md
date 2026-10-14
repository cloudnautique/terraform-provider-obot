---
name: obot-source
description: Obtain and inspect the Obot source used by this Terraform provider, using main, a release tag, or an existing local checkout. Use when investigating API behavior or building the Obot CLI from source.
---

# Obot source

Run commands from the Terraform provider repository root.

- Main: `python3 scripts/obot.py source --ref main`
- Release: `python3 scripts/obot.py source --ref v0.26.3`
- Refresh a managed checkout: add `--refresh`.
- Existing checkout: add `--directory /path/to/obot` or set `OBOT_SOURCE_DIR`. This reads the checkout without fetching or changing it.

The command prints the checkout path to stdout and resolved commit to stderr.
Managed checkouts live under ignored `.obot/sources/`. Cached main checkouts
advance only with `--refresh`; refresh refuses local changes.

Read the Obot checkout's `AGENTS.md` before working there. Useful locations:
`pkg/api/router/` and `pkg/gateway/server/` for routes,
`pkg/api/handlers/` for resource behavior, `apiclient/types/` for models,
and `pkg/cli/` for CLI behavior. This provider owns its own changes; source
acquisition does not authorize modifying or pushing the Obot repository.
