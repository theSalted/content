# Contract and client verification plan

**Reference/client verification is not executed.** The later local CMS implementation ran its own unit/HTTP and real PostgreSQL tests in disposable storage; see [results](validation.md). It did not install Ghost, run either publishing app, intercept traffic, create production credentials or submit external content. Files in `contracts/examples/` are synthetic authored examples, not recorded Ghost responses or passing tests.

## Reference environment gate

When implementation and disposable infrastructure are authorized, create an isolated Ghost **6.72.0** reference with the release commit in `contracts/baseline.json`, a recorded container/package digest, its supported MySQL runtime, and synthetic content. MySQL is a test oracle dependency only; it must never become a production dependency of content. Do not reuse production publishing or historical identity databases. Record all reference configuration that affects output: timezone, URLs, roles, visibility, content, locale and API keys by redacted identifier.

The current repository does not package Ghost or vendor upstream implementation/tests. Establish our own fixture corpus from public API observations and user-authorized client sessions. If upstream MIT tests are later reused, preserve their license/attribution and distinguish those from our original tests.

## Real app discovery

Record app name/build, OS/version, platform, content mode, account setup fields, date and scenario. Use synthetic documents with known IDs and images. Capture requests at the test server/reverse proxy under a proper trusted TLS certificate. Avoid system-wide interception, production accounts and TLS validation bypasses. Server-side capture is preferred; redact keys, signatures, cookies, email and body content before anything is committed.

For each app perform add-account, first draft/publish, restart/reconnect, edit if supported, add/replace an image, tags, server failure and credential revocation. Record the entire request sequence: discovery, identity/config/settings probes, version headers, JWT audience/header shape, MIME/boundaries, post fields, relation shape, retry behavior and returned browser URL. Test macOS first; iOS/iPadOS independently. An iA “share again” should be tested as another draft, not assumed to update the prior post.

## Fixture record

Each captured scenario should contain:

- Provenance: reference version/commit/digest, app/build/OS, fixture schema, scenario purpose and capture date.
- Sanitized request: method, full relative path/query, allowlisted headers and body; multipart file SHA-256 and metadata instead of opaque unreviewed captures.
- Response: HTTP status, relevant headers, full JSON/body, or explicitly empty body.
- State assertions: stored rows/projection, revision, public visibility, media availability, job/outbox effects and denied side effects.
- Ordered dependencies: stable symbolic names for generated IDs, timestamps, tokens, URLs and browser handoff.

Normalize nondeterministic values consistently, not structural differences. Keep absent vs null, array ordering, scalar types, stringified Lexical JSON, numeric precision and timestamp shape. Token samples use synthetic keys only; store derived claim expectations, not live secrets. Preserve meaningful header capitalization only where a client demonstrably mishandles HTTP case-insensitivity.

## Minimum corpus

| Group | Required scenarios |
| --- | --- |
| Discovery/URLs | `site/` without auth; correct/wrong root; trailing slash; subdirectory; public site different from Admin; trusted TLS; redirects; hash-based editor handoff |
| Token principals | Integration and staff success; same request under different role; unknown/revoked key; missing kid; expired/bad signature; algorithm confusion; wrong audience; malformed/scheme cases; inactive staff |
| Versioning | Absent header; v6.0; observed app version; old/future/malformed values; `Content-Version`, `Vary`; `X-Ghost-Version`; legacy URL/audience pairs if observed |
| Draft round trip | Create title-only; HTML source; each captured card mode; fetch default and explicit formats; Unicode and mixed CJK/Latin text; blank/long fields |
| Rich content | Heading/list/table/code/link/image/caption; HTML entities; nested lists; footnotes if sent; Markdown card; unsupported node; malformed JSON; competing source fields |
| Mutation | Update with current token; simultaneous stale update; missing token; two edits same millisecond; no-op update; tag/author replace vs omitted/null/empty; slug collision |
| Publication | Draft/published/scheduled; future/past/offset times; cancellation/reschedule; restart catch-up; published-to-scheduled rejection; unpublish/delete; unsupported email/member visibility |
| Taxonomy | Existing/new tag; internal tag visibility; ordered authors; ID vs email lookup; owner default; invalid author; no private user data in public authors |
| Pagination/query | Empty/1/15/100/101 items; `limit=all`; page out of range; stable order ties; include/fields interaction; valid/invalid/unsupported NQL; injection strings; AST limits |
| Media | Valid raster; ref echo; missing file; invalid MIME; oversized body/pixels; path traversal; duplicate filename; replaced image; square purpose validation; SVG/ICO explicitly blocked or tested |
| Errors | HTTP status, array envelope, type/code/message, optional fields; unknown route; bad method; permissions; stale update; validation; body limit; server failure; rate limit |
| Content API | Valid/invalid key; published-only; all ID/slug/query paths; internal tags hidden; no source JSON/email; settings public projection; cache headers; visibility filters cannot widen scope |
| Jobs/outbox | Transaction rollback leaves no event; commit leaves event; duplicate/reordered delivery; crash after external success; lease expiry; stale schedule; poison event; delete tombstone |
| Recovery/migration | Fresh/upgrade schema; backup restore; keyring restore; ID/UUID mappings; shadow import silence; one writer per article; generation fence |

Webhooks add a second corpus for management, exact event payloads, signing, ordering, retries and authorization. Ghost session parity adds cookies, origin checks, pending verification, code resend/expiry, logout and new-device behavior. Neither is claimed from basic CRUD tests.

## Test layers and current evidence

1. Pure Go unit/fuzz tests for JWT handling, NQL parsing, bounded renderers, URL normalization and error translation. Fuzz conversion inputs without network.
2. PostgreSQL integration tests for transactions, constraints, collision handling, row locks, schedules, leases, ownership and outbox atomicity. Use the same supported major as production.
3. Golden contract runs against Ghost and CMS: same scenarios, semantic/structural comparison, reviewed allowlist of deliberate differences. Never auto-update golden outputs on failures.
4. Real desktop/mobile app walkthroughs against CMS. HTTP fixture success alone does not prove the client accepts discovery, retries or browser navigation.
5. Operational exercise: process kill/restart, restore into isolation, media failure and outbound timeout. Tests must not contact production destinations.

“Client-compatible” release evidence names app build/platform/mode and scenario results, plus documented deviations. “Core Ghost profile” names release and fixture count/hash. Full parity requires an endpoint inventory with no untested claimed rows, not a handful of happy-path requests.

## Phase 0 outputs and stop conditions

Deliver an endpoint sequence per client, a format/card inventory, sanitizer diff examples, permission probe list, browser handoff route and immutable fixture manifest. Stop scope expansion if clients require unbounded editor behavior, unsupported paid/email functions, or a session/MFA flow not covered by the plan. Present the concrete incompatibility and the smallest solution; do not silently weaken auth or drop body nodes to meet a deadline.

## Package and shared-database gates

In addition to Ghost fixtures, prove two unrelated consumers can configure different domains/schemas without frontend/importer dependencies. Test schema-qualified SQL, restricted runtime roles, publication scope, namespaced migration ledgers/locks and coordinated recovery across the shared database. Local Docker PostgreSQL is development/test-only. Internal Node/Fedify is approved, and the audit-reported CMS/prototype publishing-contract mismatch must receive explicit mapping and positive/negative/replay fixtures before consolidation. No runtime parity is established by moving documentation into one package.
