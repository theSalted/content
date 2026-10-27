# Deployment, jobs and recovery proposal

No resources or settings are changed here. The selected deployment is Railway applications and ONE Supabase PostgreSQL database; Go CMS and internal Node/Fedify run as separate processes within one product; the target database must be in a new dedicated Supabase organization. This profile does not mandate either provider for package users. The former VPS/per-publication database proposal is superseded by [ADR 0008](adr/0008-package-and-database-boundaries.md).

## One database, owned schemas

Configure a separate owned schema for each CMS/ActivityPub/website responsibility. Names such as `content_cms`, `content_activitypub` and `website_content` are examples, not globals. Consumer-specific data belongs to website ownership. Runtime roles get only required schema USAGE and object permissions; use separate migration/owner roles. Do not grant broad cross-schema access, DDL, superuser/BYPASSRLS privileges or browser database credentials. Cross-module access requires a narrow documented contract/grant, not shared table ownership.

Schema-qualified queries and migrations are mandatory. Validate and quote configurable identifiers. Do not rely on a writable/default `search_path` or automatically expose tables through a provider API. Own migration ledgers per module/schema; namespace advisory-lock keys by installation/module/schema, stable across release versions. Specify one deterministic lock-key derivation for both runtimes if Node is selected. Use a dedicated appropriate direct connection for migration/session locks; test pooler behavior rather than assuming session affinity.

Schemas share database CPU, memory, disk, connection limits, maintenance, outage and restore boundaries. Bound each role's application pools, queries and worker concurrency; monitor total pressure as well as per-module backlog. Logical ownership does not provide physical isolation. Do not require a database per site/module; publication IDs and configured schemas must preserve scoped access within the shared database.

## Application and storage configuration

The deployment profile is [Railway + Supabase PostgreSQL](deployment-profiles/railway-supabase.md). Require ordinary PostgreSQL/TLS configuration, schema names, public/admin/media URLs, protected key references and storage roots/adapters. No provider SDK or provider-specific auth/storage contract enters core. Select persistent media storage explicitly; container ephemeral disks are not durable media or backup storage.

Local Docker PostgreSQL examples are development/test-only, not a second production database. Generic package users may choose different PostgreSQL/container/edge providers. Public endpoints need trusted TLS; bind proxy trust and canonical URL configuration explicitly. Provider networking, credentials, volume choices and runtime app count await the deployment/runtime gates. No settings are changed now.

## Storage abstraction

Define `MediaStore.Put`, `Open`, `Stat`, `Delete` and stable logical-key-to-public-URL resolution. A filesystem adapter is a simple option when explicitly backed by persistent storage; the deployed adapter/volume is still to be selected. Keep opaque object keys and media metadata in PostgreSQL; do not embed absolute filesystem paths, temporary signed provider URLs or bucket-specific IDs in the domain.

Use immutable finalized media: write a bounded temporary file, verify checksum/type, atomically rename into the final filesystem location, then commit the media row. Filesystem and database writes are not one atomic transaction; reconcile orphan files and failed staged rows explicitly. Never return a success URL before the file is durable and retrievable. A later S3-compatible adapter must satisfy the same semantics against a chosen self-hosted or hosted store; it is optional and no object-server dependency is selected now.

Preserve logical public paths across backend changes using a URL mapping layer. Garbage collection runs after a grace period, considering all retained revisions, drafts, exports, tombstones and pending delivery snapshots. Avoid eager deletion of an image just because the latest post body no longer references it.

## Scheduling and outbox workers

Use PostgreSQL due rows, `FOR UPDATE SKIP LOCKED`, finite leases and bounded batches. `SKIP LOCKED` is suitable for queues, not a general consistent content read. [PostgreSQL SELECT](https://www.postgresql.org/docs/current/sql-select.html).

The scheduler claims due work, locks the post, checks status/revision/due time and commits its publication event with the state change. Unpublished or edited posts cannot be published by stale jobs. Expired leases recover after a crash. Use database time; monitor system clock skew for JWT/TLS, but don't trust a client clock to determine due work.

Deliver outbox snapshots outside the database transaction with per-subscriber state. Retries use exponential backoff, jitter, attempt ceilings and a visible dead-letter queue. Delivery is at least once; consumers must deduplicate stable event IDs and reject stale revisions/ownership generations. Preserve per-content order where it matters, including deletes. A worker restart or successful external send followed by a crash must not produce a new event identity. A `LISTEN/NOTIFY` wake-up is optional; polling persisted due work remains authoritative.

Do not enable any outbound webhook or federation subscriber until separately configured and authorized. A migration shadow import emits no external publication side effects.

## Observability and operating limits

- Structured `slog` JSON with request ID, route template, status, elapsed time, key/principal IDs and classified errors; redact URLs' credential query parameters and bodies.
- Local health endpoints: liveness tests process responsiveness; readiness checks database connectivity/migration compatibility and writable storage. Worker backlog and scheduler lag are independent signals, not reasons to flap API readiness.
- Metrics: request latency/error rate, auth rejects, database pool saturation, query timeouts, upload failures/storage bytes, due-job lag, oldest outbox age, retries/dead letters, backup age and restore-test age. Avoid high-cardinality post/user labels.
- Native runtime metrics plus bounded operational counters can be exposed on a private endpoint first. Add an OSS Prometheus client/exporter only when a collector is selected and license/version reviewed; no hosted observability dependency.
- Bound request/header/body sizes, connection/read/write timeouts, database statement time, active uploads, rendered document depth, worker concurrency and queue retention. Capacity-test those limits before giving a service guarantee.

## Backups

An application backup is **database + media + configuration + decrypting key material + version manifest**. A dump alone cannot restore HMAC secrets encrypted with an external key or recover missing images. Do not place backup keys in the same unprotected bundle as encrypted backups.

For the first deployment, coordinate a consistent checkpoint across every participating schema and media store: pause writes and workers, allow in-flight transactions/uploads to finish, take a PostgreSQL custom-format logical dump, snapshot/copy finalized media and record a checksum manifest, then resume. Encrypt and copy the backup to an independently recoverable target, such as another owned machine or removable storage. An optional restic repository over local disk/SFTP avoids required cloud services. A backup inside the same failure domain is not independently recoverable. Provider-managed recovery features must be verified for the chosen plan; do not assume a particular retention or PITR entitlement.

PostgreSQL logical dumps are consistent at their database snapshot, but do not coordinate external files. Once a pause becomes unacceptable, design coordinated immutable-media retention/checkpoints or filesystem snapshots and prove consistency. For a tighter recovery-point target, add WAL archiving and base backups with restore drills; this is later operational scope. [PostgreSQL dumps](https://www.postgresql.org/docs/current/backup-dump.html).

Proposed initial targets, subject to owner acceptance: daily backups (RPO up to 24 hours) and restoration within four hours (RTO measured, not guaranteed). Keep seven daily and four weekly sets initially, sized to available storage. Validate backup completion, encryption, off-server availability and checksums, and test restore monthly and before a major upgrade. Retention is adjustable; do not delete historical federation stores as part of CMS retention.

## Restore drill and upgrade order

1. Restore the coordinated database/schema set into an isolated recovery environment with outbound delivery disabled and no production routing changes. This test environment is not an additional production database.
2. Verify manifest, PostgreSQL/app/schema versions, checksums, root key access and all media references.
3. Restore rows and files. Revoke or safely reconcile restored sessions/keys if the recovery scenario requires it; avoid unexpected key loss.
4. Check counts, IDs/UUIDs, slugs, draft secrecy, published HTML, media retrieval and timestamp collision behavior.
5. Inspect scheduled jobs and outbox leases. A restored checkpoint may replay already delivered events; preserve event IDs and do not blindly turn workers on. Reconcile publication ownership and remote snapshot history before federation resumes.
6. Run the synthetic contract and smoke suite. Record actual recovery time. Only an explicit cutover step may enable writes, delivery or DNS.

Before upgrading: verify a restorable backup, review migration compatibility, apply expand changes with a migration role, roll out the pinned app, then validate before any contract migration. Keep the previous binary usable during the expansion window. A destructive down-migration is not a generic rollback. The selected Supabase PostgreSQL deployment and alternative providers must pass the same export/restore drill and direct-connection migration requirements. A full restore affects CMS, ActivityPub and website schemas together; a partial restore needs explicit reconciliation of revisions, outbox events, actor/object IDs and consumer references before traffic resumes.
