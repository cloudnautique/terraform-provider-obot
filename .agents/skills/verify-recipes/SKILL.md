---
name: verify-recipes
description: How to verify changes to this repository at runtime — the nightly compatibility tooling (scripts/nightly.py legs and report) against real Obot instances in Docker.
---

# Verify recipes

## Nightly tooling (`scripts/nightly.py`)

Drive a leg the way `.github/workflows/nightly.yml` runs it, from a scratch
directory laid out like the CI checkout, not from the repository root. Start
in the repository checkout; `$W` is not a git repository, so every git command
names the checkout with `git -C`:

```sh
REPO_DIR=$PWD
W=$(mktemp -d)
cp -r scripts "$W/"
git -C "$REPO_DIR" worktree add --detach "$W/release" v0.1.0   # released-provider leg only
cd "$W"
python3 scripts/nightly.py leg --kind released-provider --name "provider v0.1.0 / v0.28.1" \
  --ref v0.28.1 --out results -- make -C release test-acceptance-fresh OBOT_REF=v0.28.1 OBOT_INSTANCES=2
jq . results/result-*.json
python3 scripts/nightly.py report --results results --out report && cat report/report.md
```

- `report` without `--manage-issues` or `--previous` stays local; it writes
  `report.md` with the leg table, including the "Obot tested" column.
- To see a regression, run the base's `nightly.py` beside it, into its own
  results and report directories. A leg overwrites `result-*.json` and appends
  to its `*.provenance.jsonl`, so a second run into `results/` would show the
  first run's provenance:

  ```sh
  git -C "$REPO_DIR" show <base>:scripts/nightly.py > "$W/scripts/nightly-base.py"
  python3 scripts/nightly-base.py leg --kind released-provider --name "provider v0.1.0 / v0.28.1" \
    --ref v0.28.1 --out results-base -- make -C release test-acceptance-fresh OBOT_REF=v0.28.1 OBOT_INSTANCES=2
  python3 scripts/nightly-base.py report --results results-base --out report-base && cat report-base/report.md
  ```
- Clean up from the checkout:
  `cd "$REPO_DIR" && git worktree remove --force "$W/release"`.
  The fresh targets remove their own containers and volumes; check with
  `docker ps -a --filter name=obot-tf`.

## Gotchas

- Acceptance tests need the Terraform CLI. CI installs it with
  `setup-terraform`. Without one on `PATH`, terraform-plugin-testing's
  automatic install fails inside a discobox
  (`failed to find or install Terraform CLI ... chmod /tmp/plugintest-terraform...`).
  Download the version the workflow pins (`TERRAFORM_VERSION`) from
  `releases.hashicorp.com` into a scratch directory and put it on `PATH`.
- A leg retries once, so a failing leg costs two Obot startups (about 20s each
  with the image cached).
