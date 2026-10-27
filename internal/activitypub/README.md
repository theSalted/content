# Internal ActivityPub module of content

Node 24/Fedify module within the content product. CMS stays Go; both modules use one standard PostgreSQL database with separate owned schemas and roles. The personal website is a separate consumer. No provider SDK is required. Current deployment planning is Railway applications and a future new, dedicated Supabase organization/project; this module provisions nothing.

The original standalone prototype at commit 969d97e is preserved. This directory replaces its assumptions rather than serving as a second product. No live keys, followers, identities or remote activities are used by tests.

## Local checks

Node 24 is required. Run `npm ci --ignore-scripts`, `npm run check`, and `npm test`. Fast tests use synthetic fixtures and PGlite with forced RLS. Real PostgreSQL tests require an existing Docker daemon and cached PostgreSQL image: `sh scripts/test-postgres.sh`. The script creates only its own disposable, loopback-only tmpfs database/container and removes it on exit. `NODE_BIN` may point to a Node 24 binary. No image pull occurs.

## Configuration and ownership

With `FEDERATION_ENABLED` absent/false, `npm start` provides explicit disabled-module liveness/readiness without accessing a database, publisher credential or identity. The Go CMS does not depend on this process. Activation requires explicit configuration; no personal domain/name defaults exist.

Required when enabled: `DATABASE_URL`, `ACTIVITYPUB_SCHEMA`, `CONTENT_INSTALLATION_ID`, `PUBLICATION_ID`, `ACTOR_ID`, `ACTOR_KEY_ID`, `ACTOR_NAME`, `ACTOR_PROFILE_URL`, `EXPECTED_PUBLIC_KEY_SHA256`, `PUBLISHER_ID`, `PUBLISHER_TOKEN`. Optional `PUBLIC_CONTENT_ORIGINS` (comma-separated HTTPS origins, defaults to actor origin), `DATABASE_SSL_CA`, `HOST`, `PORT`, `SHUTDOWN_GRACE_MS`, `DATABASE_POOL_SIZE` (5..20, default 5; includes queue/relay transaction headroom). This is one configured publication/actor per process; multiple unrelated publication processes may share the module schema. This is not dynamic tenant administration.

Actor route is derived from its explicit canonical path (for example /actors/editor or /users/editor). The supported imported RSA key profile is explicitly #main-key; other historical key IDs require a reviewed adapter, never implicit replacement. Actor canonical origin is not taken from request/forwarded headers. Website routing/proxy configuration is outside this module.

`npm run migrate` requires `ACTIVITYPUB_MIGRATION_DATABASE_URL`, an already-owned `ACTIVITYPUB_SCHEMA`, and `CONTENT_INSTALLATION_ID`. It binds the schema to that installation, namespaces locks/ledger, and never creates a database, schema, role or key. First migration refuses a nonempty namespace without its own ledger, including a CMS schema accidentally supplied as the AP schema. Runtime cannot migrate. All SQL is schema-qualified through a validated source-template placeholder; no writable search_path dependency.

Every data table has publication_id and FORCE RLS. The runtime role must be non-owner, NOSUPERUSER, NOBYPASSRLS. An operator must authorize (publication_id,role_name) in publication_roles; the same role must not be shared across unrelated publications. Context is transaction-local, and comes from trusted configured scope, not a request field. Runtime startup rejects direct/inherited ownership, reachable SET ROLE or membership-administration elevation, schema CREATE and dangerous table/column privileges on protected state. Grant schema USAGE, SELECT on installation/publication_roles/identities, operational DML on state/queue/cache tables and sequence USAGE only. Grant SELECT and INSERT only on object_id_reservations, never UPDATE/DELETE/TRUNCATE/REFERENCES/TRIGGER. Do not grant runtime writes to identities, installation, publication_roles, migration ledger or DDL. CMS/website roles receive no access to AP key or transport tables.

Identity import verifies configured schema/publication/installation against database metadata before insertion. Identity loading is explicit and fail-closed, with fingerprint and public/private pair verification. `identity:import` is operator-only, insert-only and never run by startup; importing real material remains separately authorized. Both identities and transport_jobs contain secret key material and need protected storage/backups. Status responses expose counts, not payloads.

## Contract and integration

[INTEGRATION.md](contracts/INTEGRATION.md), [JSON Schema](contracts/publishing-event.v2.schema.json), [cross-runtime fixtures](contracts/fixtures/v2-cases.json) and [lock vectors](contracts/fixtures/lock-ids.json) are the Go handoff. Go adapter/end-to-end confirmation remains a separate integration gate; file co-location is not compatibility.

Titles use the shared CMS rule: 1–255 Unicode code points, nonblank under Unicode White_Space and no NUL. Combining marks count separately; accepted titles are not normalized or truncated.

The CMS commits immutable public snapshots/outbox rows before delivery. AP persists object/activity/delivery intents transactionally; retries retain activity IDs. 202 means durable acceptance, not external delivery. Withdrawal preserves a terminal old object; republishing requires a new object_instance while keeping the same CMS content_id. Permanent deletion stays final. Shadow import cannot use the live publication endpoint.

The federation projection sanitizes HTML and exposes up to 16 safe HTTPS image attachments with author alt text; CMS source remains unchanged. Unsupported markup and embedded media are omitted. Profile revision3 adds optional authored public tags to immutable snapshots; feature-image metadata is not yet a separate wire field. This is not full Ghost or ActivityPub feature parity. The server-to-server public-blog profile does not implement ActivityPub client-to-server posting or a complete social network.

## Operations and remaining release gates

Queue jobs are acknowledged after handling and keep a transaction open through the handler. Use bounded workers and a tested direct/session connection; transaction pooler compatibility is not established. Use separate narrow runtime/migration credentials, trusted TLS and explicit connection budgets. Disabled modules must not fail CMS readiness. Stable IDs, receipts, object tombstones, key material, all module schemas and media must be reconciled in coordinated backup/restore drills.

Production ingress must protect /internal/*, preserve exact signed body/headers, constrain trusted canonical routing, and avoid cached inbox/errors. Later release gates include actual Supabase connection-mode tests, railway proxy signature verification, approved identity recovery/initialization, measured remote Create/Update/Delete/Follow interoperability, image/rendering policy and restore drills. No production compliance claim follows from local synthetic tests.

## Audit fixes and schema upgrade

Migration 002 adds a permanent, publication-scoped object_id_reservations table. Initial publication and republish atomically claim each URL through its unique primary key; current and retired records reference that reservation. Rollback releases only claims from the failed transaction. The successful claim survives withdrawal, retirement and deletion. Existing exact legacy IDs must be reserved together with their imported object in an operator-reviewed transaction.

The additive upgrade backfills all publications under table locks inside one transaction. An existing current/retired URL collision aborts the upgrade without deleting or rewriting either record. It restores the original RLS state on rollback. Never delete a reservation to reuse a tombstoned URL. Runtime credentials need only SELECT/INSERT on this new table; migration and identity-import privileges remain separate.

## Controlled peer verification

`NODE_BIN=<node24> sh scripts/test-peer.sh` runs real loopback HTTP delivery against an independent minimal signature checker with the real PostgreSQL transport queue. Nine peer scenarios pass, alongside the existing database suite. See [PEER-VALIDATION.md](PEER-VALIDATION.md) for exact coverage, test-only transport isolation and remaining independent-server/TLS/deployment gates. Network guard defaults are unchanged. [RFC9421-VALIDATION.md](RFC9421-VALIDATION.md) records the application signature policy, reproduced missing-coverage/expiry cases and their fix.

The [real local Mastodon gate](MASTODON-VALIDATION.md) passes discovery, HTTPS/signatures, Follow/Accept/Undo, Create/retry/duplicate, Update and Delete against pinned Mastodon 4.5.8. Its explicit opt-in test is skipped by the normal suite. Mastodon projects Articles as title and URL; the full body remains at the source.

Public object edit timestamps increase by at least one millisecond across accepted revisions when source timestamps repeat or move backward. This keeps Mastodon from silently ignoring a valid edit. Source event timestamps, hashes and original publication time stay unchanged; see the tested behavior in the Mastodon report.

## Railway deployment preparation

[Local packaging, process health, resource evidence and staging/rollback gates](deploy/RAILWAY-STAGING.md) keep both AP processes inside content and the one shared PostgreSQL database. [Validation](deploy/PACKAGING-VALIDATION.md) records the isolated built-image tests. The default Docker image runs the API; the same image with `node src/worker-main.ts` runs the worker. The separate `migrator` build target has no server or key initialization, and normal runtime images omit migration/operator entrypoints. No cloud configuration was applied.

API readiness now checks its database independently of worker availability. The worker exposes its own process-local health server. Explicitly disabled processes return `{"status":"disabled"}` with readiness 200 and perform no identity/database access; this must not be reported as active federation. Both processes support bounded SIGTERM drain. Provider draining time, continuous delivery monitoring and the shared connection budget must be confirmed before staging.


## Blog archive, presentation and moderated feedback

`POST /internal/v2/archive` is a separate, authenticated initial-enrollment contract: [archive schema](contracts/archive-v1/schema.json) and [example](contracts/archive-v1/example.json). `ACTIVITYPUB_ARCHIVE_ENABLED` defaults to false. An archive snapshot must be public and published; it retains original publication time and website permalink, creates a persistent Article and historical outbox entry, and creates zero delivery jobs. Persist the exact event ID/payload plus CMS content/object-instance/source/revision mapping. Exact retries are idempotent; another archive event cannot overwrite current content or a tombstone. Later live edits and withdrawal use the same mapping. Do not send historical snapshots through the live event endpoint. Enable only for a bounded, version-checked import and disable afterward. Remote applications decide whether to fetch old outbox items; archive availability is not a promise of automatic profile-history backfill.

Optional profile variables: `ACTOR_SUMMARY`, `ACTOR_ICON_URL`, `ACTOR_ICON_ALT`, `ACTOR_IMAGE_URL`, `ACTOR_IMAGE_ALT`. `ACTOR_IMAGE_URLS` accepts a JSON array of 1–32 HTTPS URLs and overrides the single banner URL. Each actor representation independently chooses a banner. Dynamic responses use `Cache-Control: no-store` without stale validators; there are no cache-busting query strings or Update broadcasts. Remote caches can still retain a profile. The canonical actor and key stay unchanged. Browser requests for public Article IDs redirect to the canonical website permalink; withdrawn objects remain 410.

`ACTIVITYPUB_FEEDBACK_ENABLED` defaults to false. Migration 003 adds `feedback`, `feedback_tombstones` and `feedback_blocks` with publication-scoped FORCE RLS. Grant the existing narrow runtime role SELECT/INSERT/UPDATE on feedback, SELECT/INSERT on feedback_tombstones, and SELECT/INSERT/DELETE on feedback_blocks before enabling, without ownership, DDL or protected-key writes. Enabled startup checks table presence, FORCE RLS and these required grants. Signed public Note replies, Likes and public Announces targeting an exact local public Article enter a private moderation queue. Only approved items appear in `/ap/objects/{publication}/{id}/replies`, `/likes` or `/shares`. Remote reply edits return to pending; signer-owned Delete/Undo clears content; blocks hide all prior items by that actor. Publication/actor/object claims are serialized. Initial unmoderated limits are 20 per actor and 1000 total, 16 KiB reply HTML, and bounded unknown-object deletion tombstones. These are basic safeguards, not comprehensive abuse detection.

Moderation belongs entirely to the Fedify module. Its private listener exposes GET `/internal/v2/feedback?state=pending|approved|rejected|deleted`, POST `/internal/v2/feedback/moderate` with `{id,decision:"approved"|"rejected",expected_activity_id}`, and POST `/internal/v2/feedback/block` with `{actor,blocked}`. These routes require the dedicated publication-scoped `MODERATION_TOKEN`; the publishing token cannot moderate, and the moderation token cannot publish or resolve receipts. Neither token belongs in a browser. The Go CMS framework has no moderation UI, client, configuration or reaction-review responsibility. Removing a client does not approve or expose pending/rejected items: they remain private until an explicit authorized API decision. No replacement UI is required. This module does not provide a social-network UI, remote-account following, outbound replies, emoji reactions, quote authorization or automatic conversation backfill.


Authored tags and owner moderation have exact integration contracts in [tag profile revision3](contracts/INTEGRATION.md) and [moderation API v1](contracts/moderation-v1/README.md). A moderation409 means the reply changed since review; reload instead of automatically approving the new text. New tag-producing CMS deployments must verify the receiver publishing_profile capability on the private status endpoint first. The previous payload corpus and stored retries remain valid and unchanged.

Current public media representations can opt into an exact origin alias via
`PUBLIC_MEDIA_ORIGIN` and comma-separated `PUBLIC_MEDIA_SOURCE_ORIGINS`. Both
must be canonical HTTPS origins without trailing slash. Only owned CMS image
paths `/content/images/<24 lowercase hex>/image.png|jpg` are eligible; arbitrary
external URLs, query strings, fragments, credentials, links and text are unchanged.
The destination must enforce current public visibility itself, including withdrawal
and private-image denial; aliasing does not grant access to image bytes.

The current Article and paginated outbox view normalize image attributes and Image
attachments without modifying stored payloads, dates, IDs, event hashes or queues.
Historical activity-ID responses remain original snapshots. New authorized normal
publishing events normalize their presentation before persisting new activities,
while their original submitted event hashes stay intact. This setting causes no
Update/backfill delivery, cannot refresh already-cached remote copies, and defaults
to disabled. Projected public responses use no-store without stale validators.
