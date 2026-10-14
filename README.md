# Terraform Provider for Obot

A Terraform provider for Obot, built with the Terraform Plugin Framework.

The provider is bootstrapped for development. API authentication, resources,
and data sources will be added next. It is not yet published to the Terraform
Registry.

## Provider identity

- Source address: `obot-platform/obot`
- Go module: `github.com/obot-platform/terraform-provider-obot`
- Binary: `terraform-provider-obot`

## Requirements

- Go >= 1.25.8, as specified in `go.mod`
- Terraform >= 1.13 for the development example
- GNU Make
- golangci-lint v2.14.0 for `make lint`

## Build and check

```shell
make build
make test
make lint
```

`make build` writes the provider binary to `bin/terraform-provider-obot`.
`make install` installs it to `GOBIN`, or `$(go env GOPATH)/bin` when
`GOBIN` is unset.

The example acceptance tests from the upstream scaffold have been removed.
Acceptance tests will be added with real resources and run with
`make testacc`.

## Local development with Terraform

From the repository root:

```shell
make dev
export TF_CLI_CONFIG_FILE="$PWD/dev.tfrc"
terraform -chdir=examples/dev providers schema -json
terraform -chdir=examples/dev plan
```

`make dev` builds the provider and writes an ignored `dev.tfrc` file that
points Terraform at this checkout's `bin/` directory. Re-run it after changing
the provider or moving the checkout. The configuration uses
[Terraform development overrides](https://developer.hashicorp.com/terraform/cli/config/config-file#development-overrides-for-provider-developers).

Run these development commands directly; `terraform init` still tries to
resolve the unpublished provider in the Registry. Terraform prints an expected
warning when development overrides are active. With no resources implemented,
the development example produces an empty plan.

To run the schema and plan checks together without exporting a variable:

```shell
make dev-check
```

After using the exported configuration, run `unset TF_CLI_CONFIG_FILE` to
return to your normal Terraform CLI settings.

## Documentation

Schema descriptions and examples are the source for Registry documentation:

```shell
make generate
```

The generator is pinned in `tools/go.mod`. Commit updated files in `docs/`
alongside schema or example changes.

## CI and releases

CI builds and lints the provider, runs Go tests, checks generated documentation,
and runs the local Terraform schema and plan checks.

The tag-triggered release workflow and GoReleaser configuration are retained
for future publication. Before the first release, configure the repository's
`GPG_PRIVATE_KEY` and `PASSPHRASE` secrets, confirm the Registry namespace,
and register the provider. Version tags use the form `v0.1.0`.

## License

MPL-2.0. This project is derived from
[HashiCorp's Terraform Plugin Framework scaffold](https://github.com/hashicorp/terraform-provider-scaffolding-framework).
