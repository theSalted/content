#!/bin/sh
# Existing local tools/dependencies only; no downloads or live service access.
set -eu
cd "$(dirname "$0")/.."
node=${CONTENT_TEST_NODE:-}
if [ -z "$node" ]; then
 node=$(command -v node) || { echo 'Set CONTENT_TEST_NODE to an existing Node 24 executable.' >&2; exit 1; }
fi
[ -x "$node" ] || { echo 'CONTENT_TEST_NODE is not executable.' >&2; exit 1; }
CONTENT_TEST_NODE=$node
CONTENT_TEST_ACTIVITYPUB=1
CONTENT_TEST_ACTIVITYPUB_MODULE=${CONTENT_TEST_ACTIVITYPUB_MODULE:-$(pwd)/internal/activitypub}
export CONTENT_TEST_NODE CONTENT_TEST_ACTIVITYPUB CONTENT_TEST_ACTIVITYPUB_MODULE
exec sh scripts/test-postgres.sh
