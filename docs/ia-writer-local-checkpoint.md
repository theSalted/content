# iA Writer local compatibility checkpoint

2026-10-26. **The incomplete-response cause is isolated: iA requires `primary_author.id`.** A controlled actual-app response reduction accepted that ID alone and rejected a name alone. Actual iA then opened `/ghost/#/editor/post/<24-hex-id>/`. The persisted attribution and native authenticated review page now pass HTTP/real-PostgreSQL tests; actual iA creation and handoff, native Safari sign-in/save/publish, and subsequent withdrawal have now succeeded with synthetic local data. No production account or publishing endpoint was used.

## Subsequent production checkpoint

The later release runs the maintained Tiptap editor with Rich text, HTML and Markdown modes. Go/PostgreSQL persistence, scoped owner sessions, private PNG/JPEG media through S3-compatible storage, source-preserving Ghost import, Content API v5/fields metadata reads, and durable internal federation delivery are implemented. Production synthetic login/save/stale-conflict/upload/privacy checks passed. Those HTTP checks are distinct from native authoring-app verification. The measured iA Default-mode text contract below remains useful; Ulysses handshake/update and iA automatic image attachment remain unverified. A new production publishing connection is a separate app configuration action and must not overwrite a user's existing Ghost account.

## Observed request contract

Mac iA Writer 8.0.9 (80058), Default publishing mode, temporary account labelled `TEMP LOCAL CMS TEST`, literal-loopback HTTP URL. The app form labels its signing key “Staff Access Token”; the test used a fixed synthetic fixture key scoped to one disposable publication, not a real staff identity. Ulysses Setapp 42 (84473) is installed; its workflow is not yet verified.

| Step | Measured result | Implementation/result |
| --- | --- | --- |
| Discover | `GET /ghost/api/v2/admin/site` without final slash or Accept-Version; Authorization present | Formerly 404; bounded v2 alias now returns the site object and the account connects |
| JWT | HS256, typ JWT, 24-hex kid shape, audience `/v2/admin/`, 120-second lifetime | Legacy audience accepted only on the v2 alias; modern `/admin/` remains distinct |
| JWT time | Rejected request's `iat` was ahead by at most one second; token was not expired | One-second allowance for iat only. Strict exp/nbf, signature, audience and maximum lifetime remain. Real PG and HTTP boundary tests pass |
| Create | `POST /ghost/api/v2/admin/posts/?source=html`, title/html/status/tags; tags is an empty array | Empty tags accepted and returned truthfully; nonempty/null/malformed tags rejected before persistence |
| Draft response | Initial 201 persisted but iA rejected it; controlled differential isolated `primary_author.id` | Explicit configured public attribution is now snapshotted into revisions and returned as primary_author/authors; no invented staff permissions |
| Browser handoff | Actual accepted response opens `/ghost/#/editor/post/<24-hex-id>/` | Implemented strict fragment landing and separate authenticated review; actual iA handoff and native Safari login/save/publish passed; later Rich text/HTML redesign verified in hidden WebKit |
| Update/media/other modes | Only new text drafts in Default mode tested | iA in-app update, media and HTML/Markdown card modes remain unverified |

The original “incomplete response” followed a successful authenticated 201. Separately, the intermittent 401 was an iat up to one second ahead of the server, not a key rotation or expiration problem. The same synthetic signing key remained configured throughout backend restarts. Deterministic boundary tests prove that the narrow iat allowance does not extend expiry or future nbf.

## Response diagnosis and authoritative v2 comparison

Historical Ghost **2.38.3** is used only to investigate the actual `/v2/` wire family; the project's proposed modern comparison baseline remains Ghost 6.72.0. Do not mistake the alias for complete v2 implementation.

| Property | Ghost source contract | Current response |
| --- | --- | --- |
| Envelope/status | Add returns 201 and a `posts` array with one object | Matches structure/status |
| ID/UUID | ID string up to 24 chars; UUID separate 36-char validated UUID | 24 lowercase hex ID and valid UUIDv4; not interchanged |
| Timestamps/nulls | ISO dates; published_at nullable for drafts | Millisecond UTC timestamps; published_at null |
| Default format | v2 add selects serialized Mobiledoc, including HTML-source conversion | HTML-only subset; a concrete fidelity gap, but not the cause of the tested Default-mode error |
| Relations | v2 eagerly includes tags, authors and author roles | Empty tags and an explicitly configured public author snapshot; staff roles remain absent. `primary_author.id` is the measured parser requirement |
| Draft URL | Admin draft fallback is frontend `/p/<uuid>/` | DTO still uses frontend slug (declared fidelity gap). Actual iA constructs the Admin editor hash route independently; native review now handles that route behind session auth |
| Create headers | v2 add plus shared headers code sets no Location header | Local profile includes relative canonical Admin Location. Actual 6.72.0 has an absolute legacy-path Location; either absolute-header experiment failed to resolve iA's error |

A network-disabled disposable official `ghost:6.72.0` instance (image digest `sha256:c0d83a8d45222b3f37a897572000050ab8e6e507d3eee5a35b3ef8345c7293d6`) returned an accepted synthetic response. It used only an invented reference owner and tmpfs development storage. A local differential proxy then forwarded the exact synthetic iA create to our CMS and varied only selected response properties:

| Response variation | Actual iA outcome |
| --- | --- |
| Ordinary CMS response, or absolute Location alone | Incomplete response |
| Reference defaults including authors array, but excluding primary_author | Incomplete response |
| Draft `/p/<uuid>/` URL alone | Incomplete response |
| primary_author full synthetic reference object | Accepted; browser handoff |
| primary_author containing only name | Incomplete response |
| primary_author containing only id | Accepted; browser handoff |

This establishes a parser requirement for this app/build/mode, not every Ghost field or author capability. The diagnostic response override was removed. The implementation persists truthful configured attribution rather than hard-coding an Owner role or fabricating a user account. Tests verify attribution survives edits and leaves old unattributed posts unchanged. The `url`/format/default-relation differences remain explicit compatibility limits.

The reference handoff opened its own login page. The test never required a real Ghost login; an autofilled real-account attempt was reported by the user. No credential value was inspected or submitted by the agent. The diagnostic logger excludes login bodies, cookies and Authorization values. The reference tab was closed and the exact disposable reference container/tmpfs destroyed; no reference login remains part of the workflow. Subsequent differential pages contained no login form. The new native review page uses a separately configured synthetic local operator for testing, never the publishing key or a real account password.

Primary sources: [v2 create controller](https://github.com/TryGhost/Ghost/blob/2.38.3/core/server/api/v2/posts.js), [v2 envelope](https://github.com/TryGhost/Ghost/blob/2.38.3/core/server/api/v2/utils/serializers/output/posts.js), [v2 schema](https://github.com/TryGhost/Ghost/blob/2.38.3/core/server/data/schema/schema.js), [v2 default format/relations](https://github.com/TryGhost/Ghost/blob/2.38.3/core/server/api/v2/utils/serializers/input/posts.js), [v2 draft URL](https://github.com/TryGhost/Ghost/blob/2.38.3/core/server/api/v2/utils/serializers/output/utils/url.js), [v2 header selection](https://github.com/TryGhost/Ghost/blob/2.38.3/core/server/api/shared/headers.js), [modern legacy-route rewrite](https://github.com/TryGhost/Ghost/blob/v6.72.0/ghost/core/server/web/api/middleware/api-version-compatibility/mw-version-rewrites.js). Public-source interface observations only; no proprietary client implementation copied or inspected.

## Local diagnostic tool

`go build -o bin/client-probe ./cmd/client-probe` builds a manual opt-in reverse proxy. Example, after starting an isolated synthetic backend:

```sh
bin/client-probe -listen 127.0.0.1:8081 -target http://127.0.0.1:8080 -log /tmp/new-client-trace.jsonl
```

Both addresses must be literal loopback with explicit ports. Logs are created exclusively with mode 0600; existing files are refused. The tool creates no credentials, database, accounts or trust settings. It forwards requests to that fixed origin and blocks external redirects. Use only synthetic documents and a dedicated test key. It records route shapes, selected enum query values, JSON field names/types, response status and coarse unverified JWT structure/timing categories. It excludes token values, key IDs/secrets, signatures, raw timestamps, cookies, document contents, filenames and raw query values. A failed log writer stops subsequent forwarding. Logs may still reveal workflow timing/order and are local test artifacts.

Regression entry points: `internal/httpapi/legacy_client_test.go`, `storage/postgres/ia_client_test.go`, `internal/clientprobe/proxy_test.go`. `sh scripts/test-publishing.sh` runs the combined disposable database suite. The observed request replay is deterministic HTTP evidence; actual iA outcomes above were observed separately. Native session/UI and real-PostgreSQL review tests are described in [draft review](draft-review.md).

## Media implementation and final UI checkpoint

The production-shaped local CMS accepted an actual iA text draft (201, no response override), opened its native review route and completed separate synthetic operator login. Safari form Origin policy and save-fetch CSP defects were identified and corrected with regressions. Final native edit/save/conflict/publish and iA image upload have not yet been observed after those corrections; they remain acceptance gates, not inferred passes. GUI was explicitly yielded for another authorized task.

The [image endpoint, PostgreSQL metadata, private filesystem and S3-compatible adapters](media.md) are implemented and tested using synthetic data. No live bucket exists from this task. The SDK transport uses a local fixture; provider acceptance and actual client image ordering/format remain unverified.

### Final native/media and editor checkpoint

Actual iA8.0.9 created the synthetic draft on the direct CMS backend through the observedv2 route (201), then opened the browser handoff. Native Safari saved and explicitly published it. The Save defect was the HTML form's `action` property being shadowed by buttons named action; using getAttribute fixes the actual request target. Subsequent authenticated API withdrawal restored both Content API and anonymous owned-image access to404.

The latest editor has Rich text and HTML only; no separate preview or redundant captions. Hidden WebKit tests passed image rendering, headings/link/lists/table, byte-identical unchanged mode switches, unsupported HTML preservation, rich save, competing-tab409 with retained edits and explicit local publish. Final state is private draft. Native iA automatic file upload is still unverified: local image syntax sent a filename without bytes and no upload call; an unlinked Content Block trial omitted the file. Media endpoint/filesystem/S3 verification is distinct from automatic native-client upload. No real user documents/accounts were modified.
