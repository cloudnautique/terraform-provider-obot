#!/bin/sh
#---
# name: Obot development bootstrap
# description: Prepare DNS, proxy forwarding, and TLS trust for nested Docker.
#---
set -eu
cd "$(dirname "$0")/../.."
exec make discobox-bootstrap
