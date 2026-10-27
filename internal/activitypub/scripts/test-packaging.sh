#!/bin/sh
# Local only. Builds locked public dependencies; tests use an isolated network
# and an existing PostgreSQL image. No cloud service, push or key import.
set -eu
cd "$(dirname "$0")/.."
node_bin=${NODE_BIN:-node}
docker image inspect postgres:18-bookworm >/dev/null
docker build --pull=false --target runtime -t content-ap-prep:local .
docker build --pull=false --target migrator -t content-ap-migrator-prep:local .
CONTENT_ACTIVITYPUB_PACKAGING_TEST=1 "$node_bin" --test test/packaging.test.ts
