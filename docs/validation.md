# Local validation evidence

Updated: 2026-10-26. This records CMS implementation tests and separately labelled actual-client observations; none implies production readiness. Tests use synthetic content and fixture keys. No Supabase or historical federation database was contacted.

## Verified

- Go 1.26.4, macOS arm64: build and `go vet ./...` pass.
- `go test -race -count=1 -v ./...`: domain validation/source preservation, publication-scoped HTTP create/read/edit behavior, public draft exclusion, key/JWT failure cases, unsupported formats/fields/queries, body limits, error redaction, migration/runtime credential separation, and schema/lock validation pass.
- Real PostgreSQL 18.6 in a dedicated loopback-only tmpfs Docker container: concurrent and repeated goose migrations, installation-ID mismatch rejection, post/revision persistence across reconnect, publication/slug scope, public-only reads/counts, duplicate rejection and forced-outbox-failure transaction rollback pass.

The posts-html-v1 milestone's staged and final repository runs of `sh scripts/test-postgres.sh` passed all fourteen top-level checks (including one example), plus fourteen database subtests, with the race detector enabled and no skips. The earlier create/read binary also passed a separate process smoke test: explicit migration, readyz 200, Admin creation 201, persisted Content read 200 and SIGTERM exit 0. That older process smoke is not evidence of a PUT request to the newly built binary. The edit HTTP handlers ran against real PostgreSQL in the race suite. All checks used only disposable local test storage. The next run can be reproduced using `sh scripts/test-postgres.sh`. A normal `go test ./...` explicitly skips the database test without both opt-in variables.

## Restricted-role follow-up

The additional PostgreSQL 18.6 run verified minimum runtime grants, denied permanent/temporary DDL, migration-role separation, cross-schema denial, application-level publication reads/writes, namespaced lock exclusion/progress, timeout cleanup and concurrent first-installation claims. It exposed and fixed a no-op migration-lock gap. See [tested grants and exact limits](database-roles.md); this does not imply per-publication database RLS or verified production ACLs. Temporary test roles and their dedicated database are removed by the fixture, and the container is removed by the harness.

## Post-edit milestone

The 2026-10-26 race run passed all six PostgreSQL edit subtests: partial edits/history; draft/publish/edit/withdraw/republish visibility; competing writers (one success, one 409); forced-outbox-failure rollback; cross-publication/unsupported-field/slug-conflict denial; monotonic millisecond tokens under clock skew and no-ops. A separate upgrade test replays the unchanged first migration, upgrades twice, preserves source/identity/timestamps, leaves unknown historical metadata unknown and rejects new revisions without metadata. Runtime tests additionally deny changing immutable post IDs/scope/creation time and historical revisions/events.

## Durable publishing adapter milestone

The combined `scripts/test-publishing.sh` run uses real PostgreSQL 18.6 for CMS persistence/roles and the actual AP-owned Node 24.19.0 HTTP receiver with synthetic in-memory PGlite state. It tests Go-to-Node acceptance, not remote delivery or both modules on one PostgreSQL server. The AP module's own real-PostgreSQL suite is separate evidence.

Verified: frozen shared v2 positive/negative cases; immutable prepared payloads; stable UUID and new republish instance; publish/edit/withdraw/republish; a lost acknowledgment followed by byte-identical duplicate retry; receiver scope/event-ID conflict rejection; restricted worker/API grants; lease expiry and stale-token fencing; retry delay, quarantine ordering and independent-stream progress; enrollment/preparation rollback; preserved un-enrolled history; projection failure without CMS data loss; HTTP timeout/redirect refusal; disabled startup without configuration/credentials or I/O. The built `content deliver` exits 0 with an empty environment and reports disabled.

The initial adapter milestone's combined race suite passed 23 top-level checks (including the existing example) and 49 subtests, with no skips. Build and vet pass. Test fixture keys are synthetic; the Node fixture blocks external fetches and starts no delivery worker. No historical identity keys, Supabase resources or remote federation endpoints were accessed. [Reproduction and precise limitations](publishing-delivery.md).

## Not established

Broad Ghost differential corpus; Ulysses workflow and remaining iA formats/media/error paths; taxonomy/staff-author APIs or rich-format fidelity; scheduling and exact Ghost update/no-op/error parity; PostgreSQL 17; backup/restore; Railway/Supabase pooling/TLS/deployment; remote ActivityPub delivery, historical identity/ownership migration and rendering acceptance. These are distinct later gates, not implied by passing local tests.

Only disposable test schemas/containers created by this task may be cleaned up. Existing Supabase containers, federation implementation/identity state, original website checkout and unrelated documentation worktree remain unchanged.

## Migration namespace audit regression

The foreign-schema bug was reproduced before correction: unrelated occupied namespaces acquired CMS objects, and a synthetic AP schema acquired a Goose ledger before failing. The migration-correction combined race suite passed 26 top-level checks and 61 subtests with no skips. This includes ten zero-mutation rejection cases, recognized legacy CMS 1/2/3 upgrades, unchanged post/revision/outbox data, explicit CMS module binding and the existing namespaced-lock and Go/Fedify adapter checks. Build and vet pass. Earlier 23/49 counts above describe the completed adapter milestone before these additional migration regressions.

The tests use only disposable PostgreSQL and synthetic AP-shaped tables/loopback state; this task does not edit the AP module or claim to validate its separate audit fixes. [Guard behavior and remaining recovery boundaries](database-roles.md#foreign-schema-audit-correction).

## Coordinated Unicode title alignment

Latest combined result: **26 top-level checks and 76 subtests pass, with no skips**, using real CMS PostgreSQL 18.6 and the fixed AP module's synthetic Node 24.19.0 loopback receiver. Build/vet pass. This includes all migration audit regressions above, the existing lifecycle/retry checks and the expanded shared title corpus.

Profile `content-activitypub-v2-2026-10-26-r2-unicode-title` aligns CMS and publisher validation at 1–255 Unicode code points, nonblank under Unicode White_Space and no NUL. Manifest hashes and fixture copies were verified and updated. Tests cover 251/255/256 emoji, ASCII/mixed/combining boundaries, empty/blank/Unicode-blank/NUL rejection, embedded whitespace and BOM preservation. The full publish/edit/withdraw/republish pipeline carries a 255-emoji title and checks the receiver's exact stored title; no trimming, normalization or truncation is introduced. Durable blocking remains tested for a genuine serialized-payload size overflow, with unchanged CMS source.

## Publishing-client discovery investigation

The bounded 2026-10-26 client investigation adds the unauthenticated site object and public metadata configuration. Combined local race run: **32 top-level checks and 101 subtests pass with no skips**, including all prior namespace, restricted-role, editing and Go/AP tests. Build/vet pass. The new ten-request documented HTML scenario runs over actual loopback HTTP with restricted PostgreSQL access and independently signed synthetic JWTs; it verifies two posts, four revisions, one CMS publication intent and zero delivery enrollment. Fifteen separate gap assertions make unsupported client requirements visible without claiming those failures match Ghost.

At this historical discovery checkpoint only: iA Writer 8.0.9 (80058), no Ulysses found in the two standard application folders. No app was launched, account added, credential generated or real-client request recorded. See [client evidence, sources, exact implementation and next verification gate](client-compatibility-checkpoint.md). Earlier suite counts remain historical milestones; the current run includes their regressions. Discovery adds no migration, dependency or AP protocol change.

## Actual iA diagnosis and native review milestone

The final actual-repository combined race run passes **49 top-level checks and 123 subtests, zero failures/skips**, with Go 1.26.4, real disposable PostgreSQL 18.6, and the AP-owned Node 24.19.0 loopback receiver. Build/vet pass. This includes measured legacy route/audience/empty-tag requests, a one-second iat-only boundary with strict expiry, metadata-only diagnostic-log redaction, persisted public author attribution, native browser authentication/CSRF/concurrency, and a complete real-PostgreSQL create/review/save/publish/Public-Content sequence. Successful UI writes preserve revisions and the atomic publication intent; delivery enrollment remains zero. No AP-owned protocol file, migration or dependency was changed.

The first review integration test incorrectly formatted a database timestamp using local time with a literal Z; it produced an expected stale conflict. Correcting that test to UTC made the real-HTTP flow pass. The UI already formats UTC. This failure and correction did not weaken concurrency checks.

Actual iA 8.0.9 Default-mode response reduction proved `primary_author.id` resolves its incomplete-response rejection and revealed the exact editor hash handoff. The final run against the native review page is tracked in [the iA checkpoint](ia-writer-local-checkpoint.md), separately from these deterministic tests. Ulysses 42 is now installed and was inspected read-only, but no Ulysses account or publishing workflow has been tested. Earlier availability/no-launch statements describe their dated initial stage.

## Image-storage milestone

The 2026-10-26 repository run passed build, vet and `sh scripts/test-publishing.sh` with the race detector: 57 top-level tests/examples and 139 subtests, no failures/skips, using disposable PostgreSQL 18.6 and the existing internal Node module. No ActivityPub-owned files changed. New checks include real multipart upload, raster validation, size limit, unsafe SVG/MIME rejection, sequential/concurrent idempotency, publication scope, private drafts/unattached images, atomic publication rollback, published-image retrieval/withdrawal, storage acknowledgement loss/recovery and pending cleanup. A separate transient maintenance role passes denied DDL, unrelated-schema reads, post reads and immutable-key writes. S3 SDK signing, private Put/Get/Delete, retries and redirect refusal run against a local synthetic HTTP fixture, not a real S3 provider.

A separate actual-binary HTTP process smoke using the existing disposable iA database passed upload 201, anonymous private-image 404, separate operator login, owned-image preview, save 200, stale save 409, explicit local publish 200/public image 200, and withdrawal/private image 404. It left the synthetic preview as a draft, preserved the earlier iA draft/key and kept AP disabled. This is HTTP evidence, not a native-browser or iA-image acceptance result. Final iA image upload and Safari enhanced-save flow remain pending coordinated GUI access; no safe preview screenshot has yet been delivered. No bucket or production storage credentials were created.

## Quota and S3 addressing follow-up

Both staged and final-repository quota suites passed 59 top-level checks/examples and 141 subtests with the Go race detector, disposable PostgreSQL18.6 and existing Go/Fedify loopback fixture. It adds a 1,000,000,000-byte default, independent-store concurrent reservations across publications, full-quota deduplication, denied runtime quota/size mutation, pending-byte accounting, cleanup capacity recovery, HTTP507 and both S3 addressing styles. Provider evidence is recorded separately in [Railway storage](deployment-profiles/railway-media.md). No staging HTTP test substitutes for actual iA image verification.

Actual Railway provider verification also passed: synthetic PNG upload201, exact-byte authenticated/public-proxy retrieval200, anonymous raw-bucket403, draft404, duplicate-upload idempotency and over-quota507. The test object/server/local schema were removed; Railway read-back reports zero objects/bytes. This does not imply CMS deployment or actual iA image acceptance.

## Go staging probe, 2026-10-26

See [probe execution and evidence limits](staging-go-probe.md). The new command/package passes build, vet and unit/race checks. The approved real-PostgreSQL preflight used separate CMS API/delivery roles and a local Node receiver: draft/publish/edit/withdraw/republish, five durable accepted events, identical retry after a post-commit503 and history/privacy assertions passed. The deployed public-peer gate is separate; native iA image/review completion also remains pending.

### 2026-10-26: read-only cloud recovery and two-mode editor

- Read-only completion: actual CMS staging final revision6, five accepted immutable outbox mappings, retry receipts, stable/republication identities, silent history and both current public HTTPS410 Tombstones passed from native Go. One receipt GET, zero publishing requests. Original cloud missing-CA failure remains recorded; Linux wrapper verification is AP-owned.
- New editor: Rich text/HTML only, no separate preview or redundant captions. Hidden WebKit UI passed owned image, heading/link/list/table rendering, exact untouched source round-trip, unsupported-source preservation, rich editing/save, stale-tab409 with retained edits, explicit publish; local API withdrawal restored anonymous content/image404. Native Safari had already verified iA handoff/login/save/publish before the two-mode redesign. Native iA local-file upload remains unverified.
- Focused real local PostgreSQL TestBrowserDraftReviewPostgres passed with race detection using the existing disposable client-test database; no new container. Existing auth/CSRF/publication/security regressions passed.
- Prepared one-post production-test command: race unit tests/vet/static Linux build passed. Its new database/role-creating integration test was denied before execution; no production run and no workaround.

### 2026-10-26: editor replacement

- Branch `editor-tiptap`. `sh scripts/test-postgres.sh` passed with the race detector on disposable PostgreSQL 18.6: 70 top-level tests and 173 subtests, no failures. Three tests skipped because they need the separate publishing harness or the previously denied one-post harness. The disposable container was removed.
- New Go checks: escaping-only sanitizer differences keep exact source while removed elements, attributes, unsafe or rewritten URLs, comments and handlers still fail; the editor page binds a nonce to a content-addressed script and submits no client-only fields; the asset route serves exact bytes with immutable caching and rejects unknown paths and POST.
- Browser suite (`internal/review/editor/test/browser`): 43 conversion cases plus 9 page scenarios pass in Google Chrome and in Playwright WebKit 26.6. Every conversion output passed the real CMS validator. Scenarios are listed in [draft review](draft-review.md). Emulated touch is not a physical iPhone; native iA image upload and Ulysses remain unverified.
