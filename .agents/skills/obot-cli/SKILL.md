---
name: obot-cli
description: Obtain and use the Obot CLI for Terraform provider development and tests, building main from source or downloading a versioned GitHub release with checksum verification.
---

# Obot CLI

Run commands from the Terraform provider repository root.

- Build main: `python3 scripts/obot.py cli --ref main`.
- Download a release: `python3 scripts/obot.py cli --ref v0.26.3`.
- Rebuild a tag: add `--build`.
- Build an existing checkout: add `--directory /path/to/obot`.
- Refresh main source or re-download a release: add `--refresh`.

The command prints the executable path to stdout and version to stderr.
Binaries are cached under ignored `.obot/cli/`, by source commit or release
and platform. Source builds use the upstream Go requirement; main currently
requires Go 1.27. Keep provider and CLI build requirements distinct.

For an instance created by the test tooling:

```sh
eval "$(python3 scripts/obot.py env dev)"
OBOT_TOKEN="$(python3 scripts/obot.py token dev)" && export OBOT_TOKEN
```

The token helper uses known local-auth credentials and invokes
`obot login --scope api --print-token` with an API-scoped seed key. It works
without a browser or OS keyring. An interactive login without a seed key
requires a working OS keyring; an `org.freedesktop.secrets` error means the
Linux Secret Service is unavailable. For headless tests use
`python3 scripts/obot.py exec dev -- COMMAND...`; the helper logs in through
local auth, creates an API-scoped seed key, and invokes the same CLI login
command with that key in its environment. This supports CI without a desktop
keyring. The provider consumes `OBOT_TOKEN` and connects directly to the API.

Treat stdout from login/token commands as credentials: capture it, keep it out
of logs, and never commit it. Never enable CLI debug logging while obtaining
tokens. An API key needs the `api` scope for provider operations.
