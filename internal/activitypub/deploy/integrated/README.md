# Integrated CMS delivery inside the AP API container

One content product, no extra service/database. `entrypoint.ts` keeps the existing
public/private ingress split and starts the Go-owned `content deliver` binary
only after both listeners are ready. Ordinary AP API and remote-worker entrypoints
remain available. The Go sender translates only explicitly enrolled immutable CMS
outbox records and the optional reviewed archive queue. Startup never enrolls history.

`CMS_DELIVERY_ENABLED=true` opts in. Required aliases: CMS_DELIVERY_DATABASE_URL,
CMS_DELIVERY_SCHEMA, CMS_DELIVERY_INSTALLATION_ID. CMS_DELIVERY_DATABASE_SSL_CA is
public trust material written to a temporary0600 file inside a0700 directory;
the connection must use sslmode=verify-full. Existing PUBLICATION_ID,
PUBLISHER_TOKEN and INTERNAL_PORT determine the child scope and fixed loopback
endpoint. AP/CMS schemas and DSNs must differ. The child receives no owner, API,
cleanup, bucket, moderation or AP database credentials. Moderation remains an AP-only API capability: no CMS moderation client/UI or moderator credential belongs in this child. The existing ingress token stays in
the same container. This allowlist prevents accidental inheritance; it is not an
OS sandbox between processes.

The shared Go command owns a two-connection pool, GOMAXPROCS1/GOMEMLIMIT64MiB,
5second idle polling, immutable retry, leases and quarantine. Archive delivery is
off unless both CMS_ARCHIVE_DELIVERY_ENABLED=true and ACTIVITYPUB_ARCHIVE_ENABLED=true.
The child uses CONTENT_PUBLISHING_ARCHIVE_DELIVERY_ENABLED and the fixed sibling
/internal/v2/archive URL only in that explicit window. Disable both flags after
accepted receipts and zero historical recipient intents are verified. Never ship
the operator enrollment binary or migration credentials in this image.

Public /health/ready retains AP database readiness; a failed publishing child does
not take profile/inbox offline. Authenticated private GET
/internal/v2/publishing-health reports liveness, progress age per enabled lane,
pause state and queue counts. Public ingress denies this entire private route.
Progress requires a completed queue cycle within45seconds; it is not remote delivery.
Quarantine/block counters require attention even when the queue loop is healthy.

The supervisor consumes content-delivery-supervisor-v1 JSONL with a bounded16KiB
buffer and an explicit safe-field parser. It drains stderr without echoing driver
text. Unexpected exit schedules child-only retries5–60seconds, at most5restarts
in10minutes, then visibly pauses. A deployment restart or reviewed operator action
is needed after that pause. No row resets/skips or new keys are generated. SIGTERM
stops restart timers and waits for the child, killing it after15seconds if needed.
Durable lease expiry preserves exact unfinished bytes. Logs expose opaque event
IDs/outcomes and counts, never bodies/DSNs/tokens.

Build a source-only Docker context with installed Go1.26 and dependencies:
`internal/activitypub/deploy/integrated/package.sh /absolute/new-output-directory`.
It cross-compiles the shared Go command to Linux/amd64, copies only AP source and
locked dependency manifests, and uses the pinned Node24 image. Record binary/source
hashes with each release. No provider configuration is embedded. The existing
AP-only image can be restored by setting startCommand to node src/main.ts and
removing the CMS delivery aliases/flags. Keep receipts, stream mappings and
archive barriers; rollback of processes is not a rollback of durable state.

Checks: `node --test deploy/integrated/delivery-child.test.mjs`, `npm run check`,
plus the Go owner's delivery process/role/queue tests. Local synthetic archive
HTTP tests prove Go persistence→Node acceptance and no archive recipient intents;
external remote delivery is a separate gate.
