# Internal publishing contract v2 (implementation handoff)
Status: baseline Go-to-Node integration has combined loopback evidence. Coordinated contract revision 2 below requires refreshed Go fixture pins and its combined rerun.
Owned path: content/internal/activitypub. Original federation repository remains unchanged.

POST /internal/v2/events with Content-Type application/json and Bearer publisher token.
Body limit 2 MiB; document HTML limit 1 MiB of UTF8. Oversize content is rejected, never truncated.
The configured publisher token is bound to one publication_id and source.id. It cannot transfer authority.
schema_version=2; event_id, content_id and object_instance are UUIDs; publication_id is an explicit publication namespace.
source={id,epoch}; epoch and revision are canonical positive decimal strings <=9223372036854775807.
occurred_at is an ISO8601 timestamp with offset. delivery_policy must be "live".
type is content.published, content.updated, content.withdrawn or content.deleted.
For published/updated only: document={visibility:"public",title,html,url,published_at,language?,tags?} under profile revision3 below.
No private/draft body or document on withdrawal/deletion. Unknown fields/versions rejected.

CMS owns stable content_id. First publish normally uses content_id as object_instance, persisted in its delivery state.
Updates retain object_instance. Withdrawal sends Delete and makes that object terminal.
Republishing a withdrawn article uses content.published, higher revision and a NEW persisted object_instance UUID;
the content_id stays unchanged and the old object remains a dereferenceable tombstone.
Permanent content.deleted remains final for this content_id. A late event cannot replace the current instance.
Source epoch is an operator-controlled generation fence; unknown content starts at epoch "1".
A producer cannot increment epoch to seize ownership.

Silent/shadow import MUST NOT emit this live event. This endpoint rejects delivery_policy:"silent".
Silent historical enrollment uses the distinct private /internal/v2/archive contract in archive-v1; it never broadcasts historical Create activities. Identity import remains a separate operator operation.
Do not silently translate the old CMS-only outbox or illustrative schema_version=1 event.

Snapshot: the event includes the immutable rendered public snapshot, never a lookup of current mutable post state.
Canonical public URLs are HTTPS without credentials/query/fragment and must be within the configured publication origin policy.
Node sanitizes the public federation projection; it does not change stored CMS source.
The current projection exposes safe HTTPS image attachments/alt text from authored HTML and omits unsupported markup. Explicit feature-image metadata is not yet carried separately.

202 {result:"applied"|"stale",object_id} or {result:"duplicate",original_result} acknowledges durable AP storage, NOT remote delivery.
401/403 authorization or scope failure; 409 revision/event/ownership/object-instance conflict -> quarantine;
400/413/415/422 invalid payload -> quarantine; network/5xx -> bounded backoff/jitter, resend identical event_id/payload.
CMS mutation/revision/outbox insert must commit together before asynchronous delivery. Persist exact v2 bytes/mapping before first attempt.
Node hashes recursively sorted JSON object keys after parsing: UTF8, no whitespace, arrays preserve order,
decimal counters remain strings, JSON strings use JSON.stringify escapes. A Go producer need not compute the hash:
it must preserve semantic values and stable event bytes across retries. Hash fixtures will cover Unicode/HTML escaping/order.

Shared DB: CMS owns its schema; AP owns a separately configured dedicated schema. No provider SDK or Supabase Auth dependency.
AP uses transaction-local publication scope with FORCE RLS and non-owner NOBYPASSRLS runtime credentials;
migration role is separate. Migration lock = signed big-endian first 8 bytes SHA256 of
"content\0" + installation + "\0activitypub\0" + schema, matching Go CMS derivation with its module name.
No identity generation exists. AP disabled starts without touching DB/keys; CMS does not depend on AP readiness.
Deployment profile remains Railway apps + one PostgreSQL database in a future NEW dedicated Supabase organization. No provisioning performed.

Go integration oracle: run Node 24 on test/contract-server.fixture.ts with CONTENT_ACTIVITYPUB_TEST_SERVER=1.
Optional AP_TEST_PUBLICATION_ID and AP_TEST_ORIGIN (HTTPS origin) select synthetic scope; defaults publication-example and https://federation.example.
The server binds only 127.0.0.1:0 and prints one JSON line with url/publication_id/actor_id.
Publisher is content; token is the synthetic literal isolated-test-publisher-token-never-use-live.
POST v2 events, then GET /_test/state to inspect public objects/activities/receipts.
It uses in-memory PGlite and generated synthetic test keys, rejects external fetches and never starts delivery.
Terminate its child process after the test. This oracle tests Go-to-Node HTTP/contracts, not real PostgreSQL worker behavior.

Adapter review resolutions (2026-10-26): this v2 wire profile is frozen for the current integration milestone.
fixtures/v2-lifecycle.json provides ordered publish/update/withdraw/republish/delete and lost-response retries;
fixtures/manifest.json records SHA256 of the schema and shared corpus. Verify those files before implementing a conflicting contract revision.
202 stale is a terminal superseded acceptance, with duplicate.original_result preserving it on replay; it is not remote delivery.
For a fresh, explicitly enrolled publication, persist source epoch "1"; historical rows must not be automatically enrolled or broadcast.
Enrollment must use an explicit event boundary and current public snapshot, with existing mapped objects handled by a reviewed import.
Canonical URL, source binding, instance and exact serialized event belong in the immutable delivery record before network I/O.
Changing URL configuration affects only new records. Permanent malformed/auth/scope/ownership errors quarantine or pause delivery, never fail a committed CMS edit.
No content deletion endpoint is required for the current Go slice; reserve the final content.deleted mapping for later implementation.
The JSON Schema describes the portable structure; runtime validation additionally enforces signed int64, UTF8 byte limits and canonical origin rules.

Contract revision 2 — coordinated Unicode title correction (2026-10-26): wire schema_version and field shape remain v2.
Title is 1–255 Unicode code points (Go rune count), contains a non-White_Space code point and no NUL.
Combining marks count separately. No NFC normalization, trimming or truncation is applied.
Unicode White_Space matches Go strings.TrimSpace: U+0085 is blank, whereas U+FEFF is not whitespace under this rule.
The JSON Schema now expresses minLength=1/maxLength=255 in Unicode code points. The runtime also rejects unpaired surrogates.
fixtures/v2-cases.json includes 251/255/256 emoji, ASCII/mixed/combining boundaries, empty/blank/Unicode-blank/NUL rejection and exact preservation cases.
Profile: content-activitypub-v2-2026-10-26-r2-unicode-title. Update the Go fixture copy/pins from fixtures/manifest.json before combined tests.

## Public edit timestamp projection

The event revision remains authoritative. Source occurred_at values may repeat or move backward; the receiver preserves those values and event hashes. For an accepted public edit it projects object.updated as max(occurred_at, previous public object.updated + 1 millisecond), so timestamp-sensitive peers such as Mastodon apply the edit. Exact event retries retain the persisted projection. This changes no wire field, schema version, shared fixture hash or CMS event requirement. See ../MASTODON-VALIDATION.md for real peer evidence.


## Contract revision3 — authored tags (2026-10-26)

Profile `content-activitypub-v2-2026-10-26-r3-authored-tags`; wire schema_version remains2. Optional `document.tags` is a0–32 array of strict `{name,href}`. The same optional field is accepted by archivev1. Name is1–100 valid Unicode code points, nonblank under Unicode White_Space, no NUL/unpaired surrogate, and must not begin with # after Unicode White_Space. Values are preserved exactly. Href is HTTPS without credentials/query/fragment, <=2048UTF16 code units, unique within the snapshot, and within configured publication content origins. CMS filters explicitly public tags before snapshotting; internal/private tags are never silently renamed into public ones.

The array is the complete authored set: omitted or[] clears tags on an update. Do not merge with mutable CMS metadata or rewrite old stored delivery payloads. Array order remains part of the canonical event hash. Hashtag-shaped names (Unicode letters/numbers/marks/underscore only) become AS Hashtag with a # prefix and explicit JSON-LD mapping. Other labels become ordinary AS Link preserving exact spelling. Remote applications may normalize hashtags or ignore generic Link tags; no remote tag-index guarantee is implied. Resolve real canonical tag pages at the consumer; do not invent broken URLs or infer labels from prose.

New corpus: fixtures/v2-tags.json contains23 cases with expected validity and hashes. Updated schema and corpus hashes are in fixtures/manifest.json. Existing v2-cases/lifecycle/example/lock files remain unchanged. This is additive for an upgraded receiver, but old strict receivers reject the new field. Deploy receiver first and verify private /internal/v2/status publishing_profile, then update Go fixture pins/Document/validator/immutable snapshot mapper and enable producer tags. A receiver failure must not silently drop tags. Go integration remains pending its owner-side implementation and combined test.

Moderation is a separate Fedify-owned API contract in moderation-v1/README.md plus request JSON Schemas, not part of the Go CMS framework or its publishing adapter. Its dedicated moderator credential is separate from publishing/receipt resolution. Approval requires the exact reviewed activity_id to prevent an intervening remote edit becoming public without review. The private AP status response advertises moderation_contract and feedback_enabled only as module metadata; publishers need not implement moderation. Exact migration003 grants, API authorization and public-collection privacy must pass before explicit activation. Removing a CMS client must not change stored decisions or expose pending/rejected data.
