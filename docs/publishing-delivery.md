# Durable Go → internal Fedify delivery

Implemented locally on 2026-10-26. `content deliver` sends the frozen internal v2 event contract to the module-owned `POST /internal/v2/events` endpoint. Both enrollment and delivery are disabled by default. This is internal durable acceptance; it does not claim delivery to a remote ActivityPub server.

## Enrollment and persistence

`CONTENT_PUBLISHING_ENROLL_NEW=true` on `content serve` enrolls only posts **created while that flag is enabled**, including newly created drafts. The server stores the destination identifier, source identifier, canonical URL base and initial source epoch 1 in `publishing_streams`, in the same transaction as creating the post/revision/outbox intent. Turning the flag on does not enroll an existing post. Turning it off stops new enrollment; previously enrolled articles still have durable intent history for the separate worker.

Migration 00003 creates stream/delivery tables and performs no backfill. Existing posts and outbox records remain intact and un-enrolled. Silent/imported history and ownership transfer have no automatic enrollment path. An operator migration with explicit identity/authority review remains necessary for those cases. Changing configuration cannot silently transfer authority or increment an enrolled source's epoch.

The worker reads immutable CMS outbox snapshots for enrolled streams, in revision order. In a short transaction it locks one stream, maps the next event, assigns any required object-instance UUID, saves the **exact outgoing UTF-8 bytes** in a bytea column and advances the preparation cursor. It never looks up the current mutable post to construct an older event. The original CMS outbox stays version 1; a separate persisted payload carries wire version 2. Failed preparation rolls back both cursor and payload.

| CMS intent | v2 event | Identity |
| --- | --- | --- |
| First post.published | content.published | Stable post UUID becomes content_id and initial object_instance |
| post.updated | content.updated | Same content_id and object_instance |
| post.unpublished | content.withdrawn | Same instance; no document or draft body |
| Republished post.published | content.published | Same content_id, new persisted object_instance UUID |

All payloads retain event UUID, decimal-string revision/epoch, occurred_at and immutable public snapshot. The canonical HTTPS URL derives from the stream's frozen base and that intent's slug. Subsequent configuration/slug/content edits cannot alter a queued payload. Instance history remains in retained payloads. There is no CMS deletion endpoint or automated historical authority-transfer implementation.

## Delivery, retries and acknowledgment

The worker claims one due payload using `FOR UPDATE SKIP LOCKED` and a random lease token. Only one event per article can progress until earlier events have been accepted. Requests run outside PostgreSQL transactions. The command's defaults are a 10-second queue-operation budget, 10-second HTTP budget, 30-second lease, 1-second idle poll, 12 attempts and exponential jitter capped at five minutes.

- HTTP 202 is accepted only with a validated `applied`, `stale`, or `duplicate` acknowledgment. `stale` remains visible as the accepted result; a duplicate preserves its original result. None means remote delivery.
- Network errors, timeouts, 5xx, 408, 429 and malformed 202 acknowledgments retry identical stored bytes and event ID. The last allowed failed attempt quarantines the event.
- Other statuses, including auth/scope errors, conflicts, validation failures and redirects, quarantine. Redirects are never followed; an environment HTTP proxy is not used.
- An expired lease retries the same payload after process loss. An old lease token cannot acknowledge a reclaimed job. Cancellation may leave a lease to expire; it does not discard the event.
- A quarantined predecessor blocks later events for that article. Other articles can continue. Recovery must inspect the cause and preserve identity/order; do not skip a failed publish to force a later update or withdrawal through.

The worker logs opaque event IDs, attempt numbers, outcome codes and durable acceptance results. It never logs bodies, bearer tokens, DSNs or remote error bodies. Database failures exit with a redacted error for the supervisor to restart; bounded leases preserve unfinished work.

## Running and configuration

Build with `go build -o bin/content ./cmd/content`. With no configuration, `bin/content deliver` reports disabled and exits successfully without reading credentials, connecting to PostgreSQL or sending HTTP.

When separately configured for a local test or authorized deployment:

| Setting | Purpose |
| --- | --- |
| CONTENT_PUBLISHING_ENROLL_NEW | `true` opts the serve process into enrolling newly created posts only; default false |
| CONTENT_PUBLISHING_DESTINATION | Stable logical destination ID, e.g. `activitypub`; used by server and worker |
| CONTENT_PUBLISHING_SOURCE | Source ID matching the receiving module's authorized publisher; only needed for enrollment |
| CONTENT_PUBLISHING_DELIVERY_ENABLED | `true` enables the separate worker; default false |
| CONTENT_DELIVERY_DATABASE_URL | Explicit worker-role DSN; no fallback to API or migration credentials |
| CONTENT_SCHEMA / CONTENT_INSTALLATION_ID / CONTENT_PUBLICATION_ID | Configured CMS namespace and trusted publication scope |
| CONTENT_PUBLISHING_ENDPOINT | Full `/internal/v2/events` URL, HTTPS or loopback HTTP; no userinfo/query/fragment |
| CONTENT_PUBLISHING_TOKEN | Existing caller-supplied scoped bearer token; no key generation |
| CONTENT_SITE_URL | Existing server canonical HTTPS base, frozen at new-post enrollment |

The destination ID is an operator-controlled logical receiver identity. Moving its endpoint must preserve the receiver's durable state and identity; changing a URL is not a safe migration to an unrelated empty receiver. This milestone does not verify Railway private networking, Supabase pooling, production TLS or deployment credentials.

## Restricted database roles

Migrations remain separate. The ordinary API role retains the [existing grants](database-roles.md), plus INSERT on `publishing_streams` only when new-post enrollment is enabled. It gets no SELECT/UPDATE on delivery rows or stream cursors.

The dedicated delivery role needs CONNECT, CMS schema USAGE, SELECT on installation/outbox_events/publishing_streams/publishing_deliveries, INSERT on publishing_deliveries, UPDATE(last_revision,object_instance,lifecycle,blocked_reason) on publishing_streams and UPDATE(state,attempts,next_attempt_at,lease_token,lease_until,last_error_code,accepted_result) on publishing_deliveries. It receives no posts/revision reads, mutable payload/source/identity rights, DDL, migration-ledger or AP/consumer schema access. These grants are tested with a transient non-owner PostgreSQL role; the application never provisions them.

Publication scope is an application/compound-key boundary within the CMS schema. These CMS worker grants do not establish per-publication database RLS. Direct untrusted SQL access would require a stronger design. The AP module has its own independently tested role/RLS boundary.

## Inspecting and recovering failures

Read `publishing_streams.blocked_reason` for mapping failures and `publishing_deliveries.state`, attempts, last_error_code and accepted_result for transport outcomes, always scoped by publication and destination. Neither table stores secrets. A blocked mapping preserves the CMS save and original outbox snapshot. A quarantined event retains exact bytes for diagnosis/retry.

No generic reset/requeue/skip endpoint is exposed. A reviewed repair must fix the destination/configuration or agree a compatible projection, and preserve immutable event IDs/payloads once the receiver may have accepted them. Reusing an accepted event ID with changed data causes a receiver conflict. Unblocking ownership/history issues is an explicit migration, not a retry flag. A blocked mapping or quarantined event also holds subsequent withdrawal for that article, so monitoring and an operator recovery workflow are required before live activation.

Current representation limits: the AP receiver sanitizes a narrower projection and omits images/unsupported markup. CMS and AP now share 1–255 Unicode code points, nonblank under Unicode White_Space, with no NUL. Combining marks count separately; no normalization, trimming or truncation occurs. Profile `content-activitypub-v2-2026-10-26-r2-unicode-title` resolves the earlier 251-emoji mismatch; 255 emoji survive the combined persistence/HTTP lifecycle unchanged. HTML limits align at 1 MiB of UTF-8; encoded bodies must fit 2 MiB. Oversized/unsupported content is never truncated. No real publishing-client or rendering-acceptance claim follows from this transport milestone.

## Reproduce combined tests

Use existing Node 24, installed AP module dependencies, Docker and a cached PostgreSQL image. The script downloads/installs nothing:

```sh
CONTENT_TEST_NODE=/absolute/path/to/node sh scripts/test-publishing.sh
```

The script runs the race-enabled Go suite on disposable PostgreSQL 18.6 and starts the AP-owned loopback contract oracle with synthetic keys. The oracle uses in-memory PGlite, forbids external fetches and starts no remote delivery. The test process terminates it, and the PostgreSQL harness removes its own container and transient roles. AP-module real-PostgreSQL tests are separate evidence; the combined test does not put both modules on the same PostgreSQL server.

Shared positive/negative fixtures and the generated v2 schema are pinned by SHA-256 in `publishing/testdata/activitypub-v2-pins.json`. The AP-owned manifest is copied alongside the fixtures; its hashes are verified before repinning. Contract drift fails visibly. Tests verify immutable snapshots, lifecycle identity, lost-ack duplicate handling, scope/ID conflict rejection, lease recovery, retry delay, quarantine ordering, restricted roles and transaction rollback. Historical imports, remote delivery, both-runtime process-kill recovery, backup/restore and production activation remain separate gates.
