---
page_title: "Manage MCP catalog sources and access policies"
subcategory: ""
description: |-
  Add, adopt, and remove MCP catalog sources, inspect the authentication provider, and import or remove the Everything access policy.
---

# Manage MCP catalog sources and access policies

This guide covers five installation workflows. Use an Obot **Admin or Owner**
API-scoped token. Configure the provider as described on its landing page.
The examples are module snippets; declare `obot-platform/obot` in the root
module. These resources manage individual sources of the default MCP catalog and the
policies shown under **MCP Servers > Access Policies**. vMCP profiles are
separate and are outside this guide.

## 1. Manage catalog sources

Each `obot_mcp_catalog_source` manages one source of the default catalog,
exactly as adding a source under **MCP Servers** in the Obot UI does. Terraform
changes only the sources it manages: sources added in the UI, and their
credentials, are left as they are, and do not appear in plans. Git continues to
own entry definitions; Terraform does not take ownership of individual entries.

Add a source:

```terraform
resource "obot_mcp_catalog_source" "team" {
  url = "https://github.com/example/obot-catalog/tree/main/mcp"

  # For a private source, reference an existing Obot Git credential:
  # git_credential_id = "gc1example"
}
```

```shell
terraform plan -out=add-source.tfplan
terraform apply add-source.tfplan
```

The provider adds the source, requests a sync, and waits up to five minutes for
it to complete. Use a complete HTTPS URL, including the desired Git ref and
subdirectory; the URL identifies the source, so use one URL per resource and
change it only by replacing the resource. Private sources need a Git
credential; see below.

A sync error for the source fails apply and names the error. Correct the URL,
credential, or connectivity and apply again. Errors in other sources do not
affect the resource. Obot may keep the entries from a source's last successful
sync, so a failed apply is not proof that entries changed.

### Private sources

A Git credential holds a token Obot uses to read private repositories on one
host. Manage it with `obot_git_credential` and reference it from the source:

```terraform
variable "github_token" {
  description = "Token with read access to the private catalog repository."
  type        = string
  sensitive   = true
  ephemeral   = true
}

resource "obot_git_credential" "github" {
  display_name = "GitHub catalogs"
  host         = "github.com"

  # Write-only: never stored in state. Increase the version to send a new token.
  token_wo         = var.github_token
  token_wo_version = 1
}

resource "obot_mcp_catalog_source" "private" {
  url               = "https://github.com/example/private-catalog"
  git_credential_id = obot_git_credential.github.id
}
```

Obot sends the token as the HTTPS password, so use a token with read access,
such as a GitHub fine-grained personal access token. Obot never returns it.
`token_wo` keeps it out of Terraform state and needs Terraform 1.11 or later;
increase `token_wo_version` to send a new token. `token` works with older
Terraform versions but is stored, marked sensitive, in state. If Obot loses the
stored token, the next plan sends it again.

To use a credential created in the Obot UI, look up its ID:

```terraform
# Find the ID of a credential created in the Obot UI.
data "obot_git_credential" "github" {
  display_name = "GitHub catalogs"
}

resource "obot_mcp_catalog_source" "private" {
  url               = "https://github.com/example/private-catalog"
  git_credential_id = data.obot_git_credential.github.id
}
```

Obot checks the credential when the source is saved: the source must be a Git
repository URL on the credential's host. A URL on a known Git host, such as
`github.com`, or whose path contains `.git`, is treated as a Git repository.
A wrong or expired token fails apply with the source's sync error.

Obot refuses to delete a credential while a source uses it. Terraform removes
or updates the sources that reference a managed credential first. Changing a
credential's `host` replaces it, so add
`lifecycle { create_before_destroy = true }` to the credential, letting the
sources switch to the new credential before the old one is deleted. When
`git_credential_id` is unset, Obot keeps any token entered for the source in
the UI; Terraform never reads it.

## 2. Adopt or remove existing sources

Creating a source that already exists fails, so Terraform never takes over a
source someone else added. To bring an existing source, such as the shipped
catalog, under management, import it. First inspect the live configuration:

```terraform
data "obot_mcp_catalog" "default" {
  id = "default"
}
```

```terraform
data "obot_mcp_access_rules" "current" {}
```

Run `terraform plan`, then use `terraform console` to inspect:

```text
data.obot_mcp_catalog.default.sources
data.obot_mcp_catalog.default.source_git_credential_ids
data.obot_mcp_catalog.default.entries
data.obot_mcp_access_rules.current.rules
```

Copy the source URL and any Git credential ID into the configuration, and add
an import block:

```terraform
# Adopt a source that already exists in Obot, such as the shipped catalog.
# Copy its URL and any Git credential ID from data.obot_mcp_catalog.default.
resource "obot_mcp_catalog_source" "shipped" {
  url = "https://github.com/obot-platform/mcp-catalog/v2-schema"
}

import {
  to = obot_mcp_catalog_source.shipped
  id = "https://github.com/obot-platform/mcp-catalog/v2-schema"
}

# Add your own source. Sources added in the Obot UI are left unchanged.
resource "obot_mcp_catalog_source" "team" {
  url               = "https://github.com/example/obot-catalog/tree/main/mcp"
  git_credential_id = var.team_catalog_git_credential_id
}

variable "team_catalog_git_credential_id" {
  description = "ID of an existing Obot Git credential for the private team catalog, or null for a public one."
  type        = string
  default     = null
}
```

```shell
terraform plan -out=adopt.tfplan
terraform apply adopt.tfplan
```

The plan should show an import with **no remote updates**. A differing
credential ID means the plan also changes Obot, so correct the configuration
first if you intend a pure adoption. After adoption, remove the import block.
CLI import takes the source URL as the ID:

```shell
terraform import obot_mcp_catalog_source.shipped https://github.com/obot-platform/mcp-catalog/v2-schema
```

Obot replaces the legacy `https://github.com/obot-platform/mcp-catalog` URL
with its configured default catalog path every time it starts, so the provider
rejects it. Import the URL the catalog currently lists instead.

To remove a source and the entries it provides, delete its resource and apply.
Only that source is removed. Running servers and API-created entries are kept.
A source URL participates in entry identity; references to its entries,
including those in existing vMCPs, are not rewritten. To replace the shipped
catalog with your own, adopt the shipped source, add yours, and then remove the
shipped resource. Removing every source does not restore shipped defaults on
restart: on Obot v0.26.3 and the tested main image, startup only creates the
default catalog's sources when the catalog does not exist.

If someone removes a managed source in the UI, the next plan adds it back.
`terraform state rm`, or a `removed` block with `destroy = false`, stops
managing a source and leaves it in Obot.

Obot only accepts the complete source list, so the provider reads the list,
changes its source, and writes it back, as the UI does. Sources in one apply
are written one at a time, and an edit replaced by a simultaneous change is
applied again. A change made in the UI at the same moment as an apply can
still be lost, so avoid editing sources in the UI during an apply.

## 3. Get the current authentication provider

```terraform
data "obot_current_auth_provider" "current" {}
```

The data source exposes `id`, `name`, `namespace`, and `group_id_prefix`.
It reads public metadata, never authentication-provider credentials. Use the
prefix when interpreting provider group IDs; it does not enumerate groups
or users.

The API does not supply an authoritative active-provider flag. This lookup
requires exactly one configured provider after excluding staged replacements.
It errors if none or multiple match and requires an Admin or Owner token so
staging information is visible. It describes installation configuration,
not the authentication provider originally used to obtain an API key.

## 4. Bring the Everything MCP access policy under management

Find the rule in `data.obot_mcp_access_rules.current.rules`. The shipped ID
is normally `acr1-everything`; inspect the actual instance rather than
selecting by display name, which is not unique.

```terraform
data "obot_mcp_access_rule" "everything" {
  id = "acr1-everything"
}
```

Copy the returned display name, `subjects`, and `resources` into the following
example's variable values. On the tested Obot versions, the shipped Everything
policy grants the built-in `admin` group access to the wildcard resource.
Other installations may grant everyone using `{ type = "selector", id = "*" }`
as the subject. **Everything is a display name, not a guarantee of everyone
access.** Import preserves whichever subjects actually exist.

```terraform
variable "everything_policy_id" {
  description = "Existing Everything policy ID, discovered through obot_mcp_access_rules."
  type        = string
  default     = "acr1-everything"
}

variable "everything_policy_display_name" {
  description = "Copy the existing policy display name."
  type        = string
  default     = "Everything"
}

variable "everything_policy_subjects" {
  description = "Copy actual subjects from the live policy; do not infer everyone from its name."
  type        = set(object({ type = string, id = string }))
}

variable "everything_policy_resources" {
  description = "Copy actual resource selectors from the live policy."
  type        = set(object({ type = string, id = string }))
}

resource "obot_mcp_access_rule" "everything" {
  display_name = var.everything_policy_display_name
  subjects     = var.everything_policy_subjects
  resources    = var.everything_policy_resources
}

import {
  to = obot_mcp_access_rule.everything
  id = var.everything_policy_id
}
```

Use the same plan-and-apply adoption flow as above. The plan should import
the rule without updating or replacing the existing policy.
Import makes no policy API writes. After adoption, remove the import block;
future changes to the resource update that policy in place.

CLI import is also supported:

```shell
terraform import obot_mcp_access_rule.everything acr1-everything
terraform plan
```

Generated policies can be inspected through data sources but cannot be
managed by the resource. Manage their owning Obot object instead.

## 5. Remove the Everything MCP access policy

Import it first as above. Remove **both** its resource block and its import
block. Also remove any singular data source or reference that requires the
deleted rule to exist. Then run:

```shell
terraform plan -out=remove-everything.tfplan
terraform apply remove-everything.tfplan
```

The plan should show deletion of that one `obot_mcp_access_rule`. Other
policies remain in place. Grants are additive, so deleting Everything does
not revoke access granted by another policy. Users who have no other policy
lose access to every catalog server, and the catalog looks empty to them;
create the policies you need before removing Everything. On the tested Obot versions,
startup does not recreate this seed policy while the default MCP catalog exists.

`terraform state rm` merely stops Terraform tracking the rule and leaves it
in Obot. A `removed` block with `destroy = false` also leaves it in place.
To preview deletion of every managed object in this workspace, use
`terraform plan -destroy`; `terraform destroy` performs that broader teardown
and is unnecessary for removing just this policy.
