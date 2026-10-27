# Local consolidation validation — 2026-10-26

Scope: `content/internal/activitypub`, Node 24.19.0, Fedify 2.4.5. Original standalone repository remains clean at `969d97e5a97ed20e88957872854e2ccfef07eba8`. No production identity, credential, data, infrastructure or federation activity was used.

- TypeScript `npm run check`: passed.
- `npm test`: 56 fast tests passed; three explicitly opt-in integration groups skipped. This retains the original protocol/store/queue behavior and adds disabled operation, unrelated publications, schema/installation isolation, int64 counters, shared hashes, UTF-8 limits and terminal republish lifecycle.
- `NODE_BIN=<node24> sh scripts/test-postgres.sh`: PostgreSQL 18.6; all 15 database scenarios passed (16 results with the parent test). Includes real driver concurrency, killed-worker rollback/recovery, signed synthetic Follow and durable Accept handoff, restricted-role startup/publishing/actor/cache/queue, forbidden identity writes, distinct publications and schemas, repeat/concurrent migration and refusal to migrate another module's occupied namespace. The disposable tmpfs container is removed on exit; no image is pulled.
- `CONTENT_ACTIVITYPUB_HTTP_TEST=1 node --test test/contract-server.test.ts`: synthetic loopback HTTP fixture passed. It checks actual HTTP acceptance, exact replay and scoped authorization. It uses PGlite and is NOT real PostgreSQL or Go adapter evidence.

The contract is ready for the Go adapter: `contracts/INTEGRATION.md`, `contracts/publishing-event.v2.schema.json`, `contracts/fixtures/manifest.json`, `contracts/fixtures/v2-cases.json` and `contracts/fixtures/v2-lifecycle.json`. Checksum changes require an intentional contract revision and Go coordination. The lifecycle fixture includes lost-202 retries, immutable updates, withdrawal, republish with stable CMS identity/new AP instance, stale replay, and final deletion.

The Go owner reported baseline combined mapping/outbox/HTTP/replay tests passing before this coordinated title revision. Its revision-2 fixture/pin refresh and combined rerun remain with that owner. No Go file or shared documentation was edited by the ActivityPub task. The AP-owned HTTP fixture independently passes all title cases after this revision.

Production gates remain: explicit actor/key decision and any authorized import; scoped role/schema provisioning; one shared database in a new dedicated Supabase organization; tested connection mode/TLS/pool budgets; Railway routing/signature preservation and internal endpoint protection; backups/restore; approved content enrollment and rendering policy; remote-server Create/Update/Delete/Follow interoperability. Local tests do not establish full ActivityPub conformance, Ghost parity or production readiness.

## Independent audit regressions

Before fixes, synthetic tests reproduced retired-URL reuse by another content UUID and installation-mismatched identity import. Real PostgreSQL tests reproduced accepted inherited migration ownership and protected-column write privileges. All now fail closed. Regression coverage includes concurrent initial/republish URL claims, multi-publication upgrade backfill, atomic refusal of an ambiguous historical URL, inherited/SET ROLE/ADMIN elevation, protected table/column and reservation-mutation grants, and installation mismatch with no identity insertion. Original insert-only, fingerprint and keypair tests continue passing.

Role checks use PostgreSQL's effective privilege inquiries, including column grants and session-user SET ROLE reachability: [privilege functions](https://www.postgresql.org/docs/18/functions-info.html#FUNCTIONS-INFO-ACCESS-TABLE), [role membership](https://www.postgresql.org/docs/18/role-membership.html). This is configuration validation at startup, not continuous monitoring against later administrator grant changes.

The wire schema and shared fixture checksums were not changed by these three fixes. The coordinated title revision described below subsequently updates the schema and title fixtures.

## Coordinated Unicode title revision

Contract profile `content-activitypub-v2-2026-10-26-r2-unicode-title`, contract_revision 2, retains wire schema_version 2 and the same fields/lifecycle. Runtime and portable JSON Schema now use 1–255 Unicode code points, nonblank under Unicode White_Space and no NUL. Titles are preserved exactly, including combining marks and embedded whitespace. Node also rejects unpaired surrogates.

Shared fixtures cover 251/255/256 emoji; 255/256 ASCII, mixed BMP/non-BMP and combining-codepoint boundaries; empty/blank/Unicode-blank/NUL rejection; preserved embedded whitespace; and U+FEFF (not White_Space, matching Go). Unit tests verify runtime acceptance, JSON Schema limits/pattern and deterministic hashes. The HTTP fixture posts every title case and checks exact persisted public titles for accepted values.

Changed SHA256: schema `27764da04a8a47ccf0fa0574a00c641c67d8c87aee2bbaa9656e9c264010e61a`; v2-cases `21808642c9e6f4fa837bc30846104655341b25f42009bef151cefe2e55efd2d9`. Lifecycle/lock/example fixtures remain unchanged. The Go owner updates its fixture copies/pins and runs the combined suite using its existing cache; no Go files are changed by this task.

## Controlled peer milestone

The follow-on [controlled peer verification](PEER-VALIDATION.md) passed 7 real-HTTP peer scenarios plus 15 PostgreSQL scenarios (24 reported results). It uses an independent minimal Cavage RSA verifier, synthetic public-origin mapping to loopback sockets, real default loaders and real durable queue processing. Application source and frozen publishing fixtures did not change. Full fast suite: 56 pass, three opt-in groups skipped; TypeScript passes. See the linked report for the precise limits, including no independent RFC 9421 receiver, real public DNS/TLS, deployed proxy or actual Mastodon rendering verification.


## Independent RFC 9421 milestone

The follow-on [RFC 9421 gate](RFC9421-VALIDATION.md) passes nine real-HTTP peer scenarios plus 15 PostgreSQL scenarios (26 results). Independent Node crypto signs inbound Follow/Undo and verifies outbound Accept/Create/Update/Delete. Twenty-one negative cases are rejected, including missing required coverage, stale/future creation, expiration, body/digest tampering and mismatched ownership. Fast suite: 61 pass, four opt-in groups skipped; TypeScript passes.

The test reproduced accepted missing method/target/body coverage and accepted expired RFC signatures in the previous receiver. An application-level inbox policy now filters ineligible signature candidates before Fedify verification, preserving the original cryptographic, actor-ownership and network checks. Legacy behavior stays covered. This adds an exact direct declaration for the already installed structured-field-values 2.0.4 parser; publishing schemas and fixture hashes remain unchanged. See the dedicated report for parameterized-component limits and remaining real-server/deployment gates.


## Actual Mastodon milestone

The [pinned Mastodon 4.5.8 gate](MASTODON-VALIDATION.md) passes all five scenarios (six results including the wrapper) using actual Puma/Sidekiq, PostgreSQL, Redis and container-to-container HTTPS. It verifies discovery, signed Follow/Accept and duplicate Follow, transient HTTPS 503/retry and duplicate Create, Update, public status API projection, Delete/API removal and source Tombstone, and Undo/duplicate Undo. Final queues and followers are empty.

The integration exposed equal-timestamp edits being ignored by Mastodon. A regression first reproduced the unchanged public updated value; the store now advances only the public projection clock while preserving source events/hashes. Tests also cover backward/submillisecond event times and duplicate replay stability. No Go files or publishing schema/fixture hashes changed.

Final local regression results: TypeScript passes; fast suite 63 pass/four opt-in skips; independent signature/HTTP/PostgreSQL suite 26 pass; publishing HTTP fixture passes; actual Mastodon suite six results pass. These are distinct gates, not one full production-system test.

## Local deployment-packaging milestone

[Packaging validation](deploy/PACKAGING-VALIDATION.md) records the pinned runtime/migrator builds, real container API/worker readiness and SIGTERM tests, disposable PostgreSQL outage, role separation, and measured resource samples. [Staging/rollback checklist](deploy/RAILWAY-STAGING.md) is preparation only. API readiness is independent of worker health; a deliberately disabled module reports disabled-ready without database access. No Go contract or cloud configuration changed.
