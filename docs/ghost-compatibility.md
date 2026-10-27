# Ghost compatibility research and matrix

Research date: 2026-10-25; actual iA/client follow-up 2026-10-26. The posts-html-v1 subset has real PostgreSQL tests. A disposable Ghost 6.72.0 response and actual iA Writer 8.0.9 traffic now establish a bounded text-draft handshake/response profile; broad parity remains unverified. See [implemented scope](implementation.md) and [actual client checkpoint](ia-writer-local-checkpoint.md). `D` means official documentation, `S` means pinned public source, and `T` denotes trace work still required where not superseded by the dated checkpoint.

## Baseline and claim boundaries

Reference Ghost release: [6.72.0](https://github.com/TryGhost/Ghost/releases/tag/v6.72.0), commit `412dff5a21cc8ffbfec0b15554eebcb53194ad15`. Primary requests use unversioned API URLs and `Accept-Version: v6.0`. The baseline is exact; documentation is a 2026-10-25 observation and can drift. Record the reference image digest and real app builds during Phase 0.

Acceptance tiers:

1. **Client workflow compatibility:** named app/build/platform and named content modes pass add-account, publish/draft, edit where supported, image and error scenarios.
2. **Core publishing API compatibility:** the specified endpoint/query/format corpus passes against the pinned Ghost server.
3. **Broad parity:** a separately approved expansion across the remaining Admin/Content endpoints and permission/format combinations. No claim until the complete agreed inventory has fixtures and results.

“Both apps can manage all blogs” is not an appropriate initial claim. The client itself limits what the backend can offer.

## Client evidence

| Client | Current official evidence | Initial target | Unknown until T |
| --- | --- | --- | --- |
| Ulysses on Mac/iPhone/iPad | Staff token + URL; Ghost post update supported. Matching existing tags, excerpt and feature image are documented. | Add account, create, publish and update the same sheet; image and tag flow | Probe order, role reads, supplied version, format, ID persistence, draft/schedule controls and duplicate retry behavior |
| iA Writer on Mac | Integration Admin key + API URL; shares a fresh draft and opens browser every time | Add account, create draft, browser review/publish; prove each offered formatting mode | Default HTML handshake and deep link measured in the checkpoint; HTML/Markdown card modes, media, metadata and retry behavior remain open |
| iA Writer on iPad/iPhone | Official platform support pages describe blog publishing | Separate follow-up acceptance runs | Do not assume identical requests to Mac |
| iA Writer on Windows | Official support says direct blog publishing is unavailable | Not supported by this client workflow | Backend API parity cannot enable a missing client feature |

Sources: [Ulysses publishing](https://help.ulysses.app/export-publishing/publishing), [iA Mac](https://ia.net/writer/support/preview/blog), [iA iPad](https://ia.net/writer/support/preview/blog/publish-to-blogging-platforms-ipad), [iA Windows](https://ia.net/writer/support/preview/blog/publish-to-blogging-platforms-windows). Ghost's [iA integration guide](https://ghost.org/integrations/ia-writer/) instead uses a staff token and shows Default, HTML Card and Markdown Card modes. These guides agree on draft handoff but are not an HTTP trace. No authoritative public implementation source for either client's current Ghost adapter was found in this research; do not infer protocol behavior from unrelated GitHub repositories.

## Endpoint matrix

All Admin suffixes below are under `/ghost/api/admin/`; Content suffixes are under `/ghost/api/content/`. Trailing slash handling, headers, response nulls and default fields are contract tests, not cosmetic details.

| Surface | Exact methods / suffixes | Priority and evidence |
| --- | --- | --- |
| Public discovery | `GET site/` | Implemented limited local slice; D/S: unauthenticated `{"site":{...}}`, not an array; title/description/logo/frontend URL/version |
| Staff identity | `GET users/`, `users/{id}/`, `users/slug/{slug}/`, `users/email/{email}/`; `users/me/` for current staff principal | P1 candidate probes; D/S; integration `me` semantics and roles/includes require T |
| Configuration | `GET config/`, `GET settings/`, `GET roles/` | T-driven additions if clients probe; actual shapes differ, do not invent an Admin settings object from Content settings |
| Posts | `GET posts/`, `GET posts/{id}/`, `GET posts/slug/{slug}/`, `POST posts/`, `PUT posts/{id}/`, `DELETE posts/{id}/` | P1 D/S; 201 create, 200 read/edit, 204 empty successful delete; no PATCH replacement |
| Tags | `GET/POST tags/`; `GET/PUT/DELETE tags/{id}/`; `GET tags/slug/{slug}/` | P1 browse and create/configure; P2 full CRUD. Routes S; standalone detailed shape/permissions T |
| Images | `POST images/upload/` | Implemented bounded PNG/JPEG subset; multipart, 201, URL/ref envelope, private draft handling; [media evidence](media.md). Client image trace pending |
| Pages | Same six method/path patterns as posts using `pages` | P2 D/S; promote only if an actual app requires them |
| Session auth | `POST session/`, `DELETE session/`, `PUT session/verify/`, `POST session/verify/` | P3 unless T shows required; not needed for current documented token setup. `GET session/` removed in Ghost 6 |
| Public content | `GET posts/`, `posts/{id}/`, `posts/slug/{slug}/`; likewise `pages`, `tags`, `authors`; `GET settings/` | P2 D; public-key query; published public projection; never use Admin DTOs |
| Webhooks | `POST webhooks/`, `PUT webhooks/{id}/`, `DELETE webhooks/{id}/` | P2 D; no independent browse/read endpoint in documented API |
| Other files / audio / video | `POST files/upload/`, `POST media/upload/` | P3 S; media route accepts `file`/`thumbnail`; exact envelopes, formats and checks require T |
| Expanded product | Members, tiers, offers, newsletters, themes, invites, analytics, billing, etc. | Deferred separate scope; not initial parity |

Read [Admin overview](https://docs.ghost.org/admin-api), [posts](https://docs.ghost.org/admin-api/posts/overview), [site](https://docs.ghost.org/admin-api/site/overview), [Content overview](https://docs.ghost.org/content-api), and [pinned routes](https://github.com/TryGhost/Ghost/blob/v6.72.0/ghost/core/server/web/api/endpoints/admin/routes.js).

## Authentication and negotiation contract

- Both key types use `id:hex-secret`, a JWT signed with the decoded secret and `Authorization: Ghost <jwt>`. The documented header is `alg=HS256`, `kid=<id>`, `typ=JWT`; claims include second-based `iat`, `exp` and `aud=/admin/`, with a five-minute lifetime. They identify different principals, not different JWT wire formats. See [auth](https://docs.ghost.org/admin-api#token-authentication).
- Pinned [verification code](https://github.com/TryGhost/Ghost/blob/v6.72.0/ghost/core/server/auth/api-key/admin.js) checks HS256 and five-minute max age, selects the secret using `kid`, and resolves active staff users for user-bound keys. Its audience match is more permissive than the single documented `/admin/` spelling. It does not implement a one-use `jti` replay cache in this verifier despite “single-use” wording in docs. Do not add one-use rejection that breaks client retries; measure audience variants, expiry and clock-skew boundaries.
- Integration capabilities and staff roles differ. Never treat all valid keys as owner. Exact role/route behavior is fixture work, including `users/me/` under an integration key.
- `Accept-Version` indicates the client's minimum expected version, not a selectable `/v6` route or a guarantee of all historical behavior. [Versioning](https://docs.ghost.org/faq/api-versioning) and pinned middleware show `Content-Version` and `Vary: Accept-Version`. Record absent/old/new/malformed headers and `X-Ghost-Version` separately. Do not invent a blanket 406 behavior.
- Source retains rewrites for `/ghost/api/v2/`, `/v3/`, `/v4/`, `/canary/`, adds deprecation metadata, and handles versioned JWT audiences. These are optional compatibility shims, activated only for a recorded client need. Do not substitute `/v5` or `/v6` URL paths. [Rewrite source](https://github.com/TryGhost/Ghost/blob/v6.72.0/ghost/core/server/web/api/middleware/api-version-compatibility/mw-version-rewrites.js).
- The compatibility adapter can eventually emit a documented compatibility version for clients while an own-product status endpoint identifies content/build/profile. Do not report a tested Ghost 6.72 profile before the matrix passes; public status and README must retain the subset declaration.

## Posts, concurrency and states

Write JSON is `{"posts":[{...}]}` with `Content-Type: application/json`. New posts need a title. `POST posts/?source=html` accepts `html`; without that conversion mode the documented source is a JSON **string** in `lexical`. Updating uses `PUT posts/{id}/` with the previously read `updated_at`. Relation arrays replace prior tag/author relations. [Create](https://docs.ghost.org/admin-api/posts/creating-a-post), [update](https://docs.ghost.org/admin-api/posts/updating-a-post).

The initial state set is `draft`, `published`, `scheduled`. Publish via a status edit. Schedule by setting `status=scheduled` and future `published_at`. Persist UTC instants and keep a site IANA timezone for UI interpretation. Test rescheduling, cancellation, restart catch-up, backdating and simultaneous edit/worker races. Pinned post source rejects a direct published-to-scheduled transition; include that case. Email-only `sent`, paid visibility, member-gated content and newsletter side effects are unsupported and must not be silently published as public content. [Publish](https://docs.ghost.org/admin-api/posts/publishing-a-post), [schedule](https://docs.ghost.org/admin-api/posts/scheduling-a-post), [post model](https://github.com/TryGhost/Ghost/blob/v6.72.0/ghost/core/server/models/post.js).

Atomic collision detection is mandatory: compare the client's millisecond wire timestamp while holding the row lock, then increment revision and advance the returned timestamp monotonically even within one clock millisecond. A stale update changes neither content nor outbox. The exact Ghost collision status/type/message and missing-token error must be captured before freezing expected fixtures; do not fabricate those as observed responses.

Tags accept names or identifying objects; authors accept email strings or identifying objects. Creation docs describe creating unmatched tags and falling back to the owner when no author matches. Preserve relation ordering and primary values. This fallback must be constrained by the authenticated staff role rather than accidentally allowing privilege escalation. Capture mixed valid/invalid authors and omitted/null/empty arrays.

## Content formats and data-loss boundary

| Input/output | Initial policy | Fidelity evidence needed |
| --- | --- | --- |
| HTML via `source=html` | Go parser, retain exact submitted source, produce versioned supported representation | Headings, lists, tables, links, code, images, captions, Unicode, whitespace; compare semantic output and known client reads |
| Lexical JSON string | Retain raw source; implement only captured node types and versions | `extended-text`/text, paragraphs, headings, lists, links, image, HTML/Markdown cards as required |
| Raw HTML card | Preserve raw card in source; sanitize render according to explicit policy | Ghost conversion markers, rendered structure, blocked scripting and preview safety |
| Markdown card | Retain original Markdown; use pinned Go renderer only if required | Client sends a card or another representation; tables, footnotes, raw HTML and escaping |
| Mobiledoc | Optional narrow legacy input adapter after traces; no general lossless reverse conversion promise | Shape/version, card names, null precedence and conversion behavior |
| Admin output | `formats=html,lexical`, default and explicit variants | Default field presence and nulls; docs say Lexical-only, source defaults include `mobiledoc,lexical` |
| Content output | HTML default; optional plaintext | Source JSON is never exposed through public projection |

The pinned model still accepts Mobiledoc format and converts remaining Mobiledoc to Lexical; deprecation is not equivalent to “removed input.” Preserve the original separately for migration/export, not as a second editable canonical body. Unknown blocks may be retained in a quarantined import, but normal client writes requiring an unsupported renderer must fail explicitly and leave the previous revision intact. Never report a successful publish for content that would be dropped. [Model](https://github.com/TryGhost/Ghost/blob/v6.72.0/ghost/core/server/models/post.js), [serializer](https://github.com/TryGhost/Ghost/blob/v6.72.0/ghost/core/server/api/endpoints/utils/serializers/input/posts.js).

HTML-to-Lexical conversion is documented as lossy. Byte-identical rendering of all Ghost cards is not an initial goal. Native exports should preserve raw source, renderer version, IDs and media manifest. Ghost-compatible export/import is a separately tested file-format feature, not implied by HTTP API compatibility.

## Images and URL handling

`POST images/upload/` uses one multipart `file`, optional `ref`, and `purpose` (`image`, `profile_image`, `icon`). Echo `ref` unchanged in `{"images":[{"url":"...","ref":"..."}]}`; omit/preserve absent fields as the captured server does. Docs allow WEBP/JPEG/GIF/PNG/SVG, with square profile/icon validation and ICO for icons. Initial safe raster support is a stated subset; SVG/ICO require deliberate validation and tests. [Images](https://docs.ghost.org/admin-api/images/overview), [upload fields](https://docs.ghost.org/admin-api/images/uploading-an-image).

Configure `admin_url`, `site_url` and `media_base_url` separately. Use HTTPS and a publicly trusted certificate for real devices; never instruct clients to disable TLS validation. Preserve trailing slashes and optional subdirectory bases. API redirects must not drop authentication or convert POST to GET; test supplied root URL, `/ghost` suffix, redirects and percent-encoded slugs. Published media links must remain usable without an Admin JWT. The implemented [private-draft media extension](media.md) deliberately requires authorization before publication; client acceptance of that extension remains unverified. Record what iA opens and what Ulysses treats as a preview. Upload assets may be publicly reachable by URL while the post is a draft; the UI must state this, or use a separately tested private-media extension rather than assuming draft privacy extends to Ghost media URLs.

## Queries, pagination and errors

Default browse page size is 15. Ghost 6 clamps values above 100 and `limit=all` to 100. Return `meta.pagination` with `page`, `limit`, `pages`, `total`, `next`, `prev`. Match reference behavior on empty/out-of-range pages. Public post order is `published_at desc` with deterministic tie behavior; Admin defaults differ and must be pinned separately. [Parameters](https://docs.ghost.org/content-api/parameters), [Ghost 6 changes](https://docs.ghost.org/changes).

Implement a parsed NQL subset, not SQL concatenation: initial candidates are equality, negation, in-lists, `+` AND, `,` OR and parentheses for IDs/slugs/status/tags/authors/featured, then timestamps if observed. Whitelist order/fields/include. Bound expression depth and list sizes. Invalid or unsupported filters return an explicit wire error, never an unfiltered success. Filtering docs contradict their introduction about partial matching while listing `~`, `~^`, `~$`; defer those operators to source/reference tests. [Filtering](https://docs.ghost.org/content-api/filtering).

Errors use an `errors` array with `message` and `errorType`; Admin may add `code`, `context`, `help`, `details`, `property`, and `id`. Capture exact omissions/nulls per route. Documented categories include 400 malformed query, 401 auth, 403 permission, 404 absent/nonpublic content, 500 server failure. [Errors](https://docs.ghost.org/content-api/errors).

Pinned token verifier provides these concrete cases: malformed undecodable JWT → 400 `BadRequestError` / `INVALID_JWT`; missing `kid` → 400 / `MISSING_ADMIN_API_KID`; unknown key → 401 `UnauthorizedError` / `UNKNOWN_ADMIN_API_KEY`; wrong signature or expiry → 401 / `INVALID_JWT`; wrong authorization scheme → 401 / `INVALID_AUTH_HEADER`. These are source observations, not captured full responses. Include 422-style validation, collisions, upload size/type errors, rate limiting, unknown routes and methods in the test inventory without guessing their full Ghost envelope.

## Webhooks and eventual broader parity

The documented management API creates/edits/deletes webhooks; it does not independently list/read them. `event` and `target_url` are required; user-context creation also needs `integration_id`; `name`, `secret`, `api_version` are optional. Start with post lifecycle and `site.changed` only when needed. HTTP 2xx marks delivery success. Actual payload snapshots, signature headers/algorithm, retry policy and event order must be captured before claiming webhook compatibility. [Webhook create](https://docs.ghost.org/admin-api/webhooks/creating-a-webhook), [events](https://docs.ghost.org/webhooks).

Our internal outbox is always durable and versioned; the Ghost-shaped webhook is an adapter. Do not call Ghost webhooks exactly-once. Broad parity requires a new inventory for pages, all formats/operators/roles, media, sessions/MFA, settings, imports, members, newsletters and other product functions. Rebuilding that inventory is explicitly outside P1.
