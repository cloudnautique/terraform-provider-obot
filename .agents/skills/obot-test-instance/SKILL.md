---
name: obot-test-instance
description: Start, authenticate, inspect, and remove one or more isolated Obot Docker instances for this Terraform provider's tests, using main images or release tags and local-auth test users.
---

# Obot test instances

Run commands from the Terraform provider repository root. Docker must be
running locally. Test instances use embedded PostgreSQL, loopback ports, a
separate labeled data volume per instance, and local authentication.

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
