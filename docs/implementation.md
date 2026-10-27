# Local CMS milestone: posts-html-v1

Implemented 2026-10-25; edits added 2026-10-26: a runnable Go service, PostgreSQL persistence and an explicitly limited Ghost-shaped post creation/read/edit adapter. This is the first complete local slice; client-specific evidence is recorded separately in the [iA checkpoint](ia-writer-local-checkpoint.md). The wider architecture remains a proposal beyond the files described here. Internal Node/Fedify is approved; the subsequent [Go adapter milestone](publishing-delivery.md) adds tested internal v2 durable acceptance.

The [client compatibility checkpoint](client-compatibility-checkpoint.md) adds public site discovery and executable documented-workflow/gap corpora. The actual iA Default-mode create and browser handoff are measured; remaining browser and image acceptance is tracked in that checkpoint.

## Implemented behavior

- `content serve`: HTTP timeouts/body limits, graceful shutdown, `/healthz` and database `/readyz`; default listener `127.0.0.1:8080`. No automatic migrations.
- `content migrate`: embedded numbered SQL through goose, separate migration DSN, existing owned schema, schema-qualified ledger/SQL and stable installation/CMS/schema advisory lock plus a CMS/schema ownership guard, held across the full operation even when no SQL is pending. No databases, schemas, users or credentials are created by this command. Before constructing Goose it rejects occupied foreign schemas and validates recognized CMS ownership; migration 00004 adds an explicit module binding. It refuses a different module or bound installation. See [migration audit correction](database-roles.md#foreign-schema-audit-correction). A small ledger wrapper uses parameterized `to_regclass` to handle quoted schemas correctly with goose.
- Create one draft or published post, then retrieve it by ID/slug or browse it. Preserve the accepted HTML source and revision 1. Authenticated edits preserve omitted fields, require the last-read millisecond `updated_at`, and append a revision with metadata and source. See [exact edit semantics](post-editing.md). Publication scope is always explicit; slugs are unique within a publication. Published creation writes a CMS-only versioned outbox snapshot in the same transaction. Failed outbox insertion rolls back the post and revision. A separate, disabled-by-default worker now prepares and delivers v2 payloads for explicitly enrolled new posts.
- Content reads restrict rows and counts to published/public posts; raw source, internal scope and revision metadata have a separate internal model. The service never returns an Admin struct directly as a public DTO.

| Method and path | Current input/output |
| --- | --- |
| `GET /ghost/api/admin/site/` | Public site object; configured title/description/logo/frontend URL and limited wire version; no authentication |
| `POST /ghost/api/admin/posts/?source=html` | `Authorization: Ghost <JWT>`; one `posts` item with title, optional slug/HTML/status/visibility; 201 with `posts` array and Location |
| `PUT /ghost/api/admin/posts/{id}/?source=html` | Same token; one partial `posts` item plus `updated_at`; 200 or 409 stale collision; HTML edits require source=html |
| `GET /ghost/api/admin/posts/` | Same token; all statuses in the configured publication; `meta.pagination` |
| `GET /ghost/api/admin/posts/{id}/` or `posts/slug/{slug}/` | Same token; one-item `posts` array or 404 |
| `GET /ghost/api/content/posts/` | `?key=<content key>`; published/public only; page/limit and metadata |
| `GET /ghost/api/content/posts/{id}/` or `posts/slug/{slug}/` | Same key; unpublished or other-publication content returns 404 |

HTML is the only format. `formats=html` or omitted reads return HTML. Creation requires `source=html`; title-only creation under that mode is valid. Reject any HTML that bluemonday would alter, before saving, rather than silently removing content. The comparison uses decoded tokens: escaping, attribute order and void-element syntax may differ from the sanitizer's serialization, and the original bytes are stored. This strict first profile may reject safe input that merely needs HTML normalization; it is not Ghost's HTML-to-Lexical converter.

The configured Admin key is one publication-scoped create/read/edit principal. Require HS256, matching `kid`, JWT type, audience `/admin/`, issued-at and expiration within five minutes. The optional [native review page](draft-review.md) has separate operator/session authentication. No Ghost staff-role model or key provisioning is implemented. Caller-supplied secrets stay in configuration; connection strings and request query strings are not logged. Content keys are compared through constant-time digest comparison.

## Deliberate limits and deviations

Read-only `fields`/`include` projection, v5.0 acceptance, revisioned imported excerpts and an offline archival importer are described in [Ghost import](ghost-import.md).

Only `draft`/`published` creation and transitions, timestamp collision checks, and public visibility exist. No deletion, scheduling, taxonomy CRUD, general file/audio/video uploads, config/settings/users, Lexical/Mobiledoc, NQL or Ghost session flows. Unsupported inputs fail explicitly. Optional explicitly configured public author attribution is snapshotted with new posts and preserved by edits; this is not a staff-role model or complete Ghost serializer. A bounded v2 alias, empty tags array and native review page follow actual iA evidence; [the checkpoint](ia-writer-local-checkpoint.md) distinguishes measured client behavior from untested modes.

Missing slugs become `post-<id>` rather than Ghost's title-derived slug. Response DTOs are a small documented field subset; HTML is also the Admin default. Empty browse results report zero pages. Unknown fields/query parameters are rejected. Errors use the Ghost-style array but exact types/statuses/messages, auth edge cases and version negotiation are not measured Ghost parity. Only absent or `v6.0` Accept-Version is accepted on canonical routes; the measured `/ghost/api/v2/admin/` alias also accepts `v2.0` and uses only the `/v2/admin/` JWT audience. Its one-second iat rounding allowance does not relax exp/nbf; `X-Content-Compatibility: posts-html-v1` identifies this limitation. No X-Ghost-Version claim is emitted.

The outbox schema is internal CMS persistence, not the earlier publication-event example or the federation prototype contract. It has publication scope, a fresh event UUID, revision, schema version and an inline snapshot. Draft-to-published, published edits and published-to-draft write distinct internal events; withdrawal carries no HTML. The [v2 adapter](publishing-delivery.md) persists exact retry bytes and object-instance mappings separately. Internal schema version 1 is not AP wire version 2. Existing history, silent imports and ownership transfer are never automatically delivered. Only synthetic internal loopback acceptance has been exercised; no remote federation ran.

## Build and configure

Tested locally with Go 1.26.4 on macOS/arm64 and PostgreSQL 18.6. The local module path is `content`; a publishable module path and this project's distribution license remain later repository-release decisions. Pinned dependencies/checksums are in go.mod/go.sum; bundled runtime dependency license notices are in [third-party notices](../THIRD_PARTY_NOTICES.md).

```sh
go build -o bin/content ./cmd/content
go test ./...
go vet ./...
```

`.env.example` describes required variables; the binary does not automatically read a dotenv file. Supply values through the process environment using your existing local configuration. The configuration template contains no key or password; test files use explicitly synthetic fixture keys only. `CONTENT_SCHEMA`, `CONTENT_INSTALLATION_ID`, and `CONTENT_PUBLICATION_ID` identify distinct storage/installation/publication boundaries. A process serves one configured publication; multiple scoped processes can share the CMS schema. `CONTENT_SITE_URL` supplies canonical frontend URL origin/base path, independent of hosting provider. Optional `CONTENT_SITE_TITLE`, `CONTENT_SITE_DESCRIPTION` and `CONTENT_SITE_LOGO` configure public discovery metadata; they must contain no secrets. Logo metadata is independent of the opt-in [image storage](media.md). Optional author attribution and separately authenticated browser review are configured as described in [draft review](draft-review.md); both are explicit opt-ins. These author/review options need no new migration. The subsequent [image-storage slice](media.md) adds migrations 00005/00006 and the OSS AWS SDK for Go S3 transport.

Run `bin/content migrate` with `CONTENT_MIGRATION_DATABASE_URL`, schema and installation ID after the owned schema and migration role exist. Use a direct connection for session advisory locks; transaction-pooling is unsupported for migrations. Then run `bin/content serve` with `CONTENT_DATABASE_URL` and the runtime settings. The runtime never falls back to migration credentials; the migration command never falls back to runtime credentials.

Runtime-role grants should cover schema USAGE, SELECT on installation/posts/revisions, INSERT on posts/revisions/outbox, column-level UPDATE(title,slug,status,visibility,revision,updated_at,published_at) on posts, and no DDL or migration-ledger mutation. Role creation/grants are deliberately not automated by the service. The minimum grants and denial properties now pass in disposable PostgreSQL; see [database-role evidence and limits](database-roles.md). Deployment ACLs still require separate verification. Do not expose this local milestone publicly before production auth, rate limits, TLS edge, operational and compatibility gates are met.

## Reproduce real PostgreSQL tests

```sh
# Requires Docker plus a pre-existing locally cached postgres:18-bookworm image.
# Never pulls an image, starts Supabase, or reads existing credentials.
sh scripts/test-postgres.sh
```

The harness starts its own loopback-only tmpfs container with test-only trust auth, runs the race-enabled suite, and removes that exact container on exit. No persistent volume or existing instance is used. The persistence test creates/removes a unique random schema, accepts only a loopback database ending in `_test`, and requires explicit `CONTENT_TEST_ALLOW_SCHEMA_CREATE=1`. A separate restricted-role fixture needs `CONTENT_TEST_ALLOW_ROLE_CREATE=1` and creates/removes its own random database and transient roles; it never changes an existing database ACL. A plain `go test ./...` skips that database test when no disposable DSN is provided. Never supply a live database URL.

Real database verification covers concurrent/repeat migrations, installation mismatch, reconnect persistence, publication and draft isolation, duplicate-slug rejection and transactional rollback on outbox failure. The validation report separates that evidence from remaining client modes/reference corpus, production-ACL, PostgreSQL 17, restore and deployment checks. No migration or test contacted Supabase or historical federation storage.

## Change discipline

Keep applied migration files immutable; goose tracks versions rather than enforcing SQL-content checksums. Add a new forward migration for changes; no destructive Down command is exposed. Record file checksums with release evidence. Never edit the schema or reset a live database to make an integration test pass. The original schema owns installation/posts/revisions/outbox. Migration 00003 adds the tested publishing stream/delivery tables without backfill; broader architecture tables are not empty stubs.
