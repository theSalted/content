#!/bin/sh
set -eu
cd "$(dirname "$0")/.."
image=${CONTENT_TEST_POSTGRES_IMAGE:-postgres:18-bookworm}
node_bin=${NODE_BIN:-node}
container=
cleanup(){ if [ -n "$container" ]; then docker stop --time 5 "$container" >/dev/null; fi; }
trap cleanup EXIT
trap 'exit 130' INT
trap 'exit 143' TERM
container=$(docker run --detach --rm --pull=never --label content.task=disposable-activitypub-tests --publish 127.0.0.1::5432 --tmpfs /var/lib/postgresql:rw,noexec,nosuid,size=256m --env POSTGRES_HOST_AUTH_METHOD=trust --env POSTGRES_DB=content_activitypub_test "$image")
ready=0
for attempt in 1 2 3 4 5 6 7 8 9 10 11 12 13 14 15 16 17 18 19 20; do
 if docker exec "$container" pg_isready -h 127.0.0.1 -U postgres -d content_activitypub_test >/dev/null 2>&1; then ready=1;break;fi
 sleep 1
done
[ "$ready" = 1 ] || { echo 'Disposable PostgreSQL unavailable' >&2;exit 1; }
address=$(docker port "$container" 5432/tcp)
case "$address" in 127.0.0.1:*) port=${address#127.0.0.1:} ;; *) echo 'Non-loopback database refused' >&2;exit 1 ;; esac
case "$port" in ''|*[!0-9]*) exit 1 ;; esac
docker exec "$container" psql -U postgres -d content_activitypub_test -Atc 'SELECT version()'
if [ "${CONTENT_ACTIVITYPUB_PEER_TEST:-0}" = 1 ]; then
 FEDERATION_TEST_DATABASE_URL="postgres://postgres@127.0.0.1:$port/content_activitypub_test" "$node_bin" --test --test-concurrency=1 test/postgres.test.ts test/controlled-peer.test.ts
else
 FEDERATION_TEST_DATABASE_URL="postgres://postgres@127.0.0.1:$port/content_activitypub_test" "$node_bin" --test test/postgres.test.ts
fi
