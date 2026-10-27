# Actual Go outbox → cloud peer execution contract

The approved two-service Railway environment can run the Go verification without SSH, another paid service, a public publishing route, TLS bypass or changes to Go's HTTPS-or-loopback policy.

## Execution location

Temporarily include a statically linked Linux/amd64 Go probe binary in the AP API's disposable staging image. The ordinary AP API keeps port 3000 public and port 3001 private; the ordinary AP worker remains the second service. The Go probe runs as a bounded child process of the disposable API test entrypoint and sends via its real `publishing.NewHTTPClient` to `http://127.0.0.1:3001/internal/v2/events`. No host/SSH credential is needed. The child receives only CMS-scoped settings and the publishing token, never the AP DB connection or schema-owner connection.

The Go owner builds the runner using actual CMS create/update/storage, immutable outbox, `PrepareNext` mapper, persisted publishing deliveries, `publishing.Worker`, and real HTTP sender. No Node-generated event replay or fake queue is an acceptable substitute. The runner should call the real CMS API where practical and create only new synthetic posts; it must not enroll old data. The Go task owns its source, compilation and DB grants/migrations.

## Frozen runner input

Executable path inside image: `/app/staging-go-probe` (no arguments).

Settings passed from scoped Railway server configuration:

- `CONTENT_DATABASE_URL`: CMS API-role DSN, not AP or migration role.
- `CONTENT_DELIVERY_DATABASE_URL`: CMS outbox worker-role DSN, not API or migration role.
- `CONTENT_SCHEMA`: dedicated CMS staging schema.
- `CONTENT_INSTALLATION_ID`: CMS schema's bound installation.
- `CONTENT_STAGING_DATABASE_SSL_CA`: public CA PEM if Go needs a custom trust root; validate hostname and certificate.

Settings derived by the AP fixture in memory:

- `CONTENT_PUBLICATION_ID=content-staging`.
- `CONTENT_PUBLISHING_SOURCE=content`.
- `CONTENT_PUBLISHING_DESTINATION=activitypub`.
- `CONTENT_PUBLISHING_ENROLL_NEW=true` and `CONTENT_PUBLISHING_DELIVERY_ENABLED=true`.
- `CONTENT_PUBLISHING_ENDPOINT=http://127.0.0.1:3001/internal/v2/events`.
- `CONTENT_PUBLISHING_TOKEN`: existing AP publisher token, transferred only in process memory.
- `CONTENT_SITE_URL`: the approved synthetic public origin.
- `CONTENT_STAGING_PEER_STATUS_URL=http://127.0.0.1:3001/internal/staging/peer/status`.
- `CONTENT_STAGING_RUN_ID`: unique synthetic run identifier.

Use the publisher token for the private peer-status bearer auth. Its JSON includes `runId`, independently verified activity metadata, full synthetic public `activities`, public `objects`, CMS request ID/body hashes, and durable AP retry observation. It contains no private key or DB credential. The peer is already following when the runner starts.

Output: exit 0 and exactly one JSON object on stdout: `{"status":"passed","checks":["specific assertions"],"content_id":"synthetic UUID"}`. Emit only nonsecret diagnostics; no DSNs, tokens, credentials, raw key material or unredacted errors. The wrapper captures output and forwards only bounded verified report fields. A nonzero exit or malformed report fails the gate. A three-minute timeout terminates the probe. Initial child limits: `GOMEMLIMIT=48MiB`, `GOGC=50`, `GOMAXPROCS=1`; existing Railway resource caps remain unchanged.

## Required proof

Create newly enrolled synthetic post, edit, withdraw, republish with persisted new object instance, withdraw again. Assert stable CMS identity, immutable outbox snapshots, exact retained outgoing bytes, accepted delivery records, correct revisions/instance mapping, and no historical enrollment. The temporary private fixture will accept the first genuine Go event durably, then return one deliberate 503; the real Go worker must retry identical stored bytes/event ID and accept the receiver's duplicate acknowledgment. All future requests keep normal semantics. Separately the public peer fails both initial Create signature formats with 503, so the AP queue must persist a delayed retry before eventual delivery. Verify the public peer received Create/Update/Delete/Create/Delete with independently valid signatures; original and republished object URLs finish as HTTP 410 Tombstones. The wrapper removes its synthetic Follow afterward.

## Missing scope, if not already authorized/provisioned by the Go task

AP's `content_ap_staging_runtime` cannot access CMS tables and must not be broadened. A distinct empty CMS staging schema in the **same** Supabase database, CMS migration owner, CMS API role and CMS delivery role are required, with Go-owned migrations and documented least-privilege grants. No change to global/public/auth/website/AP privileges is needed. If these roles/schema do not already exist under approved CMS staging scope, parent must settle that exact narrow addition before execution. No additional Railway service/database or production actor/key change is implied.

Current state: AP public/private runtime and controlled cloud peer are proven; this Go-mode harness/runner integration is being prepared and has not executed. Parent owns the verified 48-hour stop and hourly usage watch. Retain the original October 28 03:24:10 UTC deadline and $5 operational threshold.

## Parent-service setting names (runner inputs unchanged)

To keep the AP installation binding unchanged, configure CMS inputs on the API container under dedicated aliases: `CONTENT_STAGING_CMS_API_DATABASE_URL`, `CONTENT_STAGING_CMS_DELIVERY_DATABASE_URL`, `CONTENT_STAGING_CMS_SCHEMA`, and `CONTENT_STAGING_CMS_INSTALLATION_ID`. The fixture maps those to the standard `CONTENT_DATABASE_URL`, `CONTENT_DELIVERY_DATABASE_URL`, `CONTENT_SCHEMA`, and `CONTENT_INSTALLATION_ID` only in the Go child's environment. `CONTENT_STAGING_DATABASE_SSL_CA` is copied as public trust material. Never overwrite the API container's AP `CONTENT_INSTALLATION_ID`. Two local boundary tests prove alias mapping, no AP/owner credential inheritance and fail-closed missing CMS settings. The runner's published input/output contract above is unchanged.
