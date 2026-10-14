---
name: obot-test-instance
description: Start, authenticate, inspect, and remove one or more isolated Obot Docker instances for this Terraform provider's tests, using main images or release tags and local-auth test users.
---

# Obot test instances

Run commands from the Terraform provider repository root. Docker must be
running locally. Test instances use embedded PostgreSQL, loopback ports, a
separate labeled data volume per instance, and local authentication.

Inside Discobox, first run `make discobox-bootstrap`. Its boot hook lives in
`.discobox/services/10-bootstrap.sh`. The bootstrap manages a shared DNS and
proxy forwarding service; the launcher adds matching DNS/proxy/CA settings to
new containers automatically. Other environments keep their normal networking.
Existing disposable instances must be recreated after enabling this setup.
Inspect failures with `journalctl -u obot-discobox-network.service`. It forwards
through the sandbox's existing proxy and keeps certificate verification enabled.
The proxy is on a private bridge address, so Discobox containers use
`OBOT_SERVER_DISALLOW_PRIVATE_IPMCP=false`. Account for that setting when
designing tests for private-address restrictions.

```sh
python3 scripts/obot.py up dev --ref main
python3 scripts/obot.py up comparison --ref v0.26.3
python3 scripts/obot.py status dev
python3 scripts/obot.py logs dev
python3 scripts/obot.py user-add dev test_obot_user@example.com --role basic
python3 scripts/obot.py exec dev -- COMMAND...
python3 scripts/obot.py down dev
python3 scripts/obot.py down comparison
```

For tests, prefer the Make targets: `make test` runs all unit tests without
Obot; `make test-acceptance OBOT_INSTANCE=dev` authenticates against an existing
named instance and leaves it running; `make test-acceptance-fresh` creates and
removes fresh instances. Use `OBOT_INSTANCES=2` with the fresh target to cover
multiple instances, and `OBOT_REF=v0.26.3` to select a release. `make help`
lists the targets. Make also provides workspace-local Go caches in Discobox.

For manual Terraform use `make NAME-plan`, `make NAME-apply`,
`make NAME-plan-destroy`, and `make NAME-destroy`, replacing NAME with an
existing instance name, for example `make dev-plan`. These targets build the
provider and authenticate automatically. They share `examples/dev` configuration
and keep separate local state and plugin data in `.obot/terraform/NAME/`.
Apply/destroy retain the interactive approval prompt; plan-destroy only
previews deletion. The Docker instance remains running. Pass extra Terraform
options via `TFARGS`. Keep this development example on the local backend.

Use distinct names for simultaneous instances. Ports are chosen automatically;
use `--port` for a fixed port. Both main and tags use published Docker images.
Use `--image` to test a custom image. Startup reports the actual image ID and
digest, and completes only after local-owner activation, password setup, and
default-catalog readiness.

The default owner is `test_obot_admin@example.com` with password
`TestObotAdmin123!`. These are public test credentials. The initial account
has Obot's Owner role, which includes administrative permissions. Set
`OBOT_TEST_PASSWORD` consistently for setup and subsequent logins to override
the password. Additional test users use that password and the requested role.

The Docker backend requires its daemon socket even for initial startup.
The tooling mounts `/var/run/docker.sock`; use `--docker-socket` or
`OBOT_TEST_DOCKER_SOCKET` for another path on the Docker host. These commands
are for local test instances.

`python3 scripts/obot.py smoke --ref main --instances 2` checks owner/basic-user
authentication and isolation, then removes both instances.
With multiple instances, it also verifies aliases using the compiled provider.
`python3 scripts/obot.py testacc --ref main --instances 2` prepares the same
environment for Terraform acceptance tests. `OBOT_TEST_INSTANCES` supplies
JSON connection records to the child test process. Add `--keep` for debugging.

Failed manual startup retains its container and metadata for diagnosis;
inspect it and run `down NAME` when done. Teardown checks ownership labels
before removing containers or volumes. Stop only the named test instances,
and preserve unrelated Docker resources.
