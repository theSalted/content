#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
CONTENT_ACTIVITYPUB_PEER_TEST=1
export CONTENT_ACTIVITYPUB_PEER_TEST
exec sh scripts/test-postgres.sh
