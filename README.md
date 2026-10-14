# Terraform Provider for Obot

A Terraform provider for Obot, built with the Terraform Plugin Framework.

The provider connects with an API-scoped key and exposes the
`obot_current_user` data source. Managed resources will follow. It is not yet
published to the Terraform Registry.

## Requirements

- Go >= 1.25.8 for the provider, as specified in `go.mod`
- Go >= 1.27 to build the current Obot CLI from source
- Terraform >= 1.13
- Python >= 3.10 for local test tooling
- Docker running locally for Obot test instances
- GNU Make and golangci-lint v2.14.0

Provider source: `obot-platform/obot`. Go module:
`github.com/obot-platform/terraform-provider-obot`.

## Connect to Obot

Configure the CLI for the target server and obtain an API-scoped token:

```shell
export OBOT_BASE_URL="https://obot.example.com/api"
export OBOT_TOKEN="$(obot login --scope api --print-token)"
```

Then use the provider:

```hcl
terraform {
  required_providers {
    obot = {
      source = "obot-platform/obot"
    }
  }
}

provider "obot" {}

data "obot_current_user" "current" {}
```

For local provider development, use the development overrides below because
the provider has not been published yet.

The provider accepts `base_url` and sensitive `token` attributes; explicit
values take precedence over `OBOT_BASE_URL` and `OBOT_TOKEN`. Supply an app
URL or API base URL; a trailing `/api` is added when needed. Configuration
validates API-key scope and the authenticated identity before reads.
Each provider configuration owns an independent client, including aliases
pointing at different Obot instances.

## Obtain Obot source and CLI

Two modes are supported:

| Ref | Server | CLI |
| --- | --- | --- |
| `main` (default) | Published `ghcr.io/obot-platform/obot:main` image | Built from the main source checkout |
| Version tag, such as `v0.26.3` | Published image for that tag | GitHub release binary verified against its published checksum |

```shell
make obot-source
make obot-cli
make obot-source OBOT_REF=v0.26.3
make obot-cli OBOT_REF=v0.26.3
```

Source and CLI caches live under ignored `.obot/`. Main is intentionally a
moving development target. Cached source advances with an explicit refresh;
each CLI build is cached by the resolved source commit. The image is pulled
when an instance starts, and its actual ID and digest are recorded. The main
image and source can be published at different times.

```shell
python3 scripts/obot.py cli --ref main --refresh
python3 scripts/obot.py cli --ref v0.26.3 --build
python3 scripts/obot.py source --ref main --directory /path/to/obot
python3 scripts/obot.py cli --ref main --directory /path/to/obot
```

An existing checkout is used without changing its files or Git refs. You can
also set `OBOT_SOURCE_DIR`. Managed source refresh refuses local changes.
CLI/source commands print the resulting path to stdout and progress to stderr.
Using a release CLI does not require checking out or building Obot.

## Run one or more test instances

```shell
make obot-up OBOT_INSTANCE=dev
make obot-up OBOT_INSTANCE=release OBOT_REF=v0.26.3
python3 scripts/obot.py status dev
python3 scripts/obot.py status release
```

Each named instance gets its own loopback port, data volume, database, local
auth sessions, and API keys. The default initial account is:

- Email: `test_obot_admin@example.com`
- Password: `TestObotAdmin123!`
- Role: Owner, including administrative permissions

These are public credentials for disposable local test instances.
Set `OBOT_TEST_PASSWORD` consistently during creation and subsequent commands
to use another password. Use `up --email` to choose another initial owner.
The automatic multiple-instance test runner uses
`test_obot_admin_2@example.com`, `test_obot_admin_3@example.com`, etc. for
additional owners so tests can detect connections to the wrong instance.

Startup activates the local owner, sets the password, and waits for the
default catalog. No external identity provider or model-provider key is needed.

```shell
eval "$(python3 scripts/obot.py env dev)"
OBOT_TOKEN="$(python3 scripts/obot.py token dev)" && export OBOT_TOKEN
```

This works in a sandbox or terminal without a desktop keyring. The helper
signs in with the known local-auth credentials, creates an API-scoped key,
and invokes `obot login --scope api --print-token` with that key in its
environment. The assignment captures the token without printing it.

On a desktop with a working OS keyring, you can instead use a fresh
interactive login:

```shell
OBOT_TOKEN="$(obot login --scope api --print-token)" && export OBOT_TOKEN
```

Sign in using the credentials above and complete its device-code prompt.
The CLI stores the key in the OS keyring. An error mentioning
`org.freedesktop.secrets` means the Linux Secret Service is unavailable;
use the test helper command above for the named local instance.

For headless tests, run a child command with credentials prepared automatically:

```shell
make test-acceptance
```

The helper signs in with local auth, creates an API-scoped seed key, and runs
`obot login --scope api --print-token` with that key. It validates the
resulting identity and passes the token only through the child environment.
This works without a browser or desktop keyring in CI.

Add users as tests need them:

```shell
python3 scripts/obot.py user-add dev test_obot_user@example.com --role basic
python3 scripts/obot.py exec dev --email test_obot_user@example.com -- make test-acceptance-env
```

Additional users use the same test password. Available roles are `basic`,
`admin`, `owner`, `auditor`, `power-user`, and `power-user-plus`.

Use `up --port 8080` for a fixed port, or `up --image IMAGE` for a custom
image. Obot's Docker backend requires its daemon socket during startup.
The tooling mounts `/var/run/docker.sock`; set `OBOT_TEST_DOCKER_SOCKET`
or use `up --docker-socket` when the socket path on the Docker host differs.

Inspect failures and remove instances by name:

```shell
python3 scripts/obot.py logs dev
make obot-down OBOT_INSTANCE=dev
make obot-down OBOT_INSTANCE=release
```

Failed manual startup preserves its container and metadata for diagnosis.
The logs command saves sanitized output to `.obot/logs/`. Teardown checks
project ownership labels and removes only the named instance's container and
volume.

## Run tests

Run `make help` to see the commands. The three usual choices are:

| Command | Obot instance | Authentication and cleanup |
| --- | --- | --- |
| `make test` | None | Runs Go and Python unit tests; always disables acceptance tests |
| `make test-acceptance` | Existing `dev` instance | Logs in automatically; leaves the instance running |
| `make test-acceptance-fresh` | Creates fresh instances | Logs in automatically; removes the instances afterward |

```shell
make test
make test-acceptance
make test-acceptance OBOT_INSTANCE=release
make test-acceptance-fresh OBOT_REF=main OBOT_INSTANCES=2
make test-acceptance-fresh OBOT_REF=v0.26.3 OBOT_INSTANCES=2
```

Choose individual acceptance tests with
`TESTARGS='-run=TestAccCurrentUser_basic'` on any acceptance target.
An existing instance must first have been created with `make obot-up` or
`scripts/obot.py up`; its saved metadata supplies the URL and owner email.
For an external instance, set `OBOT_BASE_URL`, `OBOT_TOKEN`, and
`OBOT_TEST_EMAIL`, then run `make test-acceptance-env`.

`make test-smoke OBOT_INSTANCES=2` creates fresh instances and checks Owner
and Basic-user login, denied administrative access for Basic users, and
isolation between instances. With multiple instances it also runs a Terraform plan with the
compiled provider and aliases, checking each server's distinct identity.

The fresh acceptance runner prepares the matching CLI, starts fresh
instances, runs Terraform acceptance tests, and removes the instances.
Use `python3 scripts/obot.py testacc --ref main --instances 2 --keep` to
preserve them for diagnosis. Both runners save sanitized logs on failure.
A custom test command can follow `--`.

Tests receive `OBOT_BASE_URL`, `OBOT_TOKEN`, `OBOT_TEST_EMAIL`, `TF_ACC=1`,
and `OBOT_TEST_INSTANCES` (a JSON array of named connections). Tokens are not
written to instance metadata.

Inside Discobox, Make puts Go module/build and lint caches under ignored
`.obot/cache/` on the workspace disk. This avoids the shared cache volume,
which can fill independently of the workspace. Explicit `GOMODCACHE`,
`GOCACHE`, and `GOLANGCI_LINT_CACHE` settings still take precedence.
Outside Discobox, the tools keep their normal cache defaults.

The older `testacc`, `testacc-local`, and `obot-smoke` targets remain aliases
for `test-acceptance-env`, `test-acceptance-fresh`, and `test-smoke`.

The in-process HashiCorp acceptance harness does not isolate provider aliases;
the multi-instance acceptance test uses separate factory names. The compiled
provider's actual alias behavior is checked separately by the smoke suite.

## Build and local Terraform development

Use the existing instance's name as the target prefix:

```shell
make dev-plan
make dev-apply
make dev-plan
make dev-plan-destroy
make dev-destroy
```

For an instance named `release`, use `make release-plan`, `make release-apply`,
and so on. Create the named instance with `make obot-up OBOT_INSTANCE=release`
first if needed. These targets rebuild the provider, configure its local
development override, and authenticate automatically without a desktop keyring.
They leave the Obot Docker instance running.

The shared configuration is `examples/dev/main.tf`, which reads and outputs
the authenticated user's email. Each instance has independent local Terraform
state and plugin data under ignored `.obot/terraform/NAME/`. The helper uses
Terraform's local backend; keep the development example on that backend.

`plan` previews changes. `apply` generates a fresh plan and asks for approval.
`plan-destroy` runs `terraform plan -destroy` to preview removal; `destroy`
asks for approval before removing Terraform-managed objects. By default, the
helpers do not save plan files. The current example only reads a data source,
so destruction removes its state and output without deleting the Obot user.
Pass extra Terraform options with `TFARGS`, for example
`make dev-plan TFARGS=-no-color`.

Lower-level build and configuration commands are also available:

```shell
make build
make lint
make dev
export TF_CLI_CONFIG_FILE="$PWD/dev.tfrc"
terraform -chdir=examples/dev providers schema -json
```

`make build` writes `bin/terraform-provider-obot`; `make install` installs
to `GOBIN`, or `$(go env GOPATH)/bin` when unset. `make dev` writes an ignored
checkout-specific CLI configuration using
[Terraform development overrides](https://developer.hashicorp.com/terraform/cli/config/config-file#development-overrides-for-provider-developers).
Re-run it after rebuilding or moving the checkout.

Skip `terraform init` for this example: it still attempts to resolve the
unpublished provider in the Registry. Terraform's development-override warning
is expected. `make dev-check` checks the schema and validates the configuration
without connecting to Obot. Prefer the named Make targets for plan/apply so
authentication and Terraform state stay associated with the same instance.

Use `unset TF_CLI_CONFIG_FILE` to return to your normal Terraform settings.

## Skills and documentation

The project skills are versioned under `.agents/skills/`:

- `obot-source`: acquire and inspect upstream source
- `obot-cli`: obtain the CLI and authenticate
- `obot-test-instance`: manage isolated Docker instances and test users

Run `make generate` after schema or example changes and commit the generated
`docs/` files. The generator is pinned in `tools/go.mod`.

## CI and releases

CI builds and lints the provider, runs Go and tooling unit tests, and checks
generated documentation. The acceptance workflow exercises both `main` and
`v0.26.3` with two Docker instances using the same commands as local development.

Before the first provider release, configure `GPG_PRIVATE_KEY` and `PASSPHRASE`
repository secrets, confirm the Registry namespace, and register the provider.
Release tags use the form `v0.1.0`.

## License

MPL-2.0. Derived from
[HashiCorp's Terraform Plugin Framework scaffold](https://github.com/hashicorp/terraform-provider-scaffolding-framework).
