# Source register and license assessment

Reviewed 2026-10-25. Primary public documentation and publicly licensed source were read; no private client code, production credentials or live blog data was used. Dynamic documentation is dated evidence, not an immutable test fixture. Source observations are not proof that a desktop app uses that route.

## Compatibility evidence

| Source | What it supports |
| --- | --- |
| [Ghost 6.72.0 release](https://github.com/TryGhost/Ghost/releases/tag/v6.72.0) | Released 2026-10-20; latest stable returned by the public release API at research time |
| [Pinned commit](https://github.com/TryGhost/Ghost/commit/412dff5a21cc8ffbfec0b15554eebcb53194ad15) | Target of the `v6.72.0` tag; precise source baseline |
| [Admin overview](https://docs.ghost.org/admin-api) | Admin route prefix, JSON conventions, integration/staff/session authentication |
| [Content overview](https://docs.ghost.org/content-api) | Read-only public content, query key and endpoint inventory |
| [API versioning](https://docs.ghost.org/faq/api-versioning) | `Accept-Version` and `Content-Version` meaning |
| [Breaking changes](https://docs.ghost.org/changes) | Ghost 6 page cap, removed GET session, Mobiledoc deprecation and versionless URLs |
| [Admin posts](https://docs.ghost.org/admin-api/posts/overview) | CRUD paths, format options and documented post projection |
| [Create post](https://docs.ghost.org/admin-api/posts/creating-a-post) | Title requirement, HTML conversion, stringified Lexical, relation inputs |
| [Update post](https://docs.ghost.org/admin-api/posts/updating-a-post) | `updated_at`, replacement relation arrays, revision query |
| [Publish](https://docs.ghost.org/admin-api/posts/publishing-a-post) | Status-based publishing |
| [Schedule](https://docs.ghost.org/admin-api/posts/scheduling-a-post) | Future `published_at`, scheduled status |
| [Delete](https://docs.ghost.org/admin-api/posts/deleting-a-post) | Empty successful 204 |
| [Pages](https://docs.ghost.org/admin-api/pages/overview) | Post-like static page resource |
| [Images](https://docs.ghost.org/admin-api/images/overview) | Upload result URL/ref and local storage concept |
| [Image upload](https://docs.ghost.org/admin-api/images/uploading-an-image) | Multipart fields, purposes, supported formats and square validation |
| [Site](https://docs.ghost.org/admin-api/site/overview) | Unauthenticated metadata object and frontend URL/version |
| [Users](https://docs.ghost.org/admin-api/users/overview) | Staff data and roles; not the public authors projection |
| [Content parameters](https://docs.ghost.org/content-api/parameters) | Include, fields, formats, pagination and ordering |
| [Filtering](https://docs.ghost.org/content-api/filtering) | NQL syntax and its documentation inconsistency about partial matching |
| [Errors](https://docs.ghost.org/content-api/errors) | Error envelope and basic HTTP classes |
| [Public authors](https://docs.ghost.org/content-api/authors) | Public author projection |
| [Public tags](https://docs.ghost.org/content-api/tags) | Public taxonomy routes and projection |
| [Public settings](https://docs.ghost.org/content-api/settings) | Public settings object |
| [Webhook management](https://docs.ghost.org/admin-api/webhooks/overview) | Create/edit/delete only in documented management surface |
| [Webhook creation](https://docs.ghost.org/admin-api/webhooks/creating-a-webhook) | Required and optional payload fields |
| [Webhook events](https://docs.ghost.org/webhooks) | Lifecycle names and delivery success class |
| [Ulysses publishing](https://help.ulysses.app/export-publishing/publishing) | Staff token setup, metadata and existing-sheet update |
| [Ghost Ulysses integration](https://ghost.org/integrations/ulysses/) | Official supported integration context |
| [iA Mac publishing](https://ia.net/writer/support/preview/blog) | Integration key setup and one new draft per share |
| [Ghost iA integration](https://ghost.org/integrations/ia-writer/) | Staff-token alternative, browser handoff, three format modes |
| [iA iPad publishing](https://ia.net/writer/support/preview/blog/publish-to-blogging-platforms-ipad) | Platform-specific publishing documentation |
| [iA Windows publishing](https://ia.net/writer/support/preview/blog/publish-to-blogging-platforms-windows) | Direct blog publishing currently unavailable |

## Pinned OSS observations

All Ghost links in this table are the reviewed release, not a moving `main` branch. The product contract remains the public API; these files help identify undocumented behavior and test questions.

| Source file | Observation |
| --- | --- |
| [Admin routes](https://github.com/TryGhost/Ghost/blob/v6.72.0/ghost/core/server/web/api/endpoints/admin/routes.js) | Exact tags/config/users/roles/session/media/file routes; more surface than stable docs |
| [Admin token verifier](https://github.com/TryGhost/Ghost/blob/v6.72.0/ghost/core/server/auth/api-key/admin.js) | HS256/maxAge/kid, active staff resolution, error codes, regex audience; no one-use nonce cache here |
| [Content key verifier](https://github.com/TryGhost/Ghost/blob/v6.72.0/ghost/core/server/auth/api-key/content.js) | Query-key validation and key-type error behavior |
| [Admin middleware](https://github.com/TryGhost/Ghost/blob/v6.72.0/ghost/core/server/web/api/endpoints/admin/middleware.js) | Integration allowlist vs staff-user permissions; additional staff restriction for authentication reset |
| [Posts endpoint](https://github.com/TryGhost/Ghost/blob/v6.72.0/ghost/core/server/api/endpoints/posts.js) | Create status 201, delete status 204, source/options inventory |
| [Post model](https://github.com/TryGhost/Ghost/blob/v6.72.0/ghost/core/server/models/post.js) | Legacy Mobiledoc-to-Lexical conversion, formats, published-to-scheduled restriction |
| [Post input serializer](https://github.com/TryGhost/Ghost/blob/v6.72.0/ghost/core/server/api/endpoints/utils/serializers/input/posts.js) | Admin defaults include Mobiledoc/Lexical; Content projection removes source formats |
| [Image endpoint](https://github.com/TryGhost/Ghost/blob/v6.72.0/ghost/core/server/api/endpoints/images.js) | Successful upload status 201 |
| [Version middleware](https://github.com/TryGhost/Ghost/blob/v6.72.0/ghost/core/server/web/api/middleware/api-version-compatibility/index.js) | Content-Version and Vary header behavior |
| [Legacy path match](https://github.com/TryGhost/Ghost/blob/v6.72.0/ghost/core/server/web/api/middleware/api-version-compatibility/legacy-api-path-match.js) | v2/v3/v4/canary recognition |
| [Version rewrites](https://github.com/TryGhost/Ghost/blob/v6.72.0/ghost/core/server/web/api/middleware/api-version-compatibility/mw-version-rewrites.js) | Internal rewrite/deprecation metadata, not a full historical implementation |
| [X-Ghost-Version middleware](https://github.com/TryGhost/Ghost/blob/v6.72.0/ghost/core/server/web/api/middleware/check-version-match.ts) | Different check from Accept-Version; do not conflate headers |

The most important evidence conflicts are retained deliberately: iA vendor integration key vs Ghost staff-token instructions; documented Lexical-only default vs source Mobiledoc/Lexical fields; docs calling JWTs single-use vs no nonce enforcement in the verifier; filtering introductory text vs its operators. The real reference/client corpus resolves the behavior to implement. No error body has been labeled “observed” just because its type was visible in source.

## Proposed dependencies and reuse boundaries

No dependencies or licenses were installed or copied into this repository. These are primary-license-file assessments for possible future adoption. Pin release versions, verify transitive notices and security status at adoption, then ship required notices with any distributed binary/image.

| Candidate | Reviewed license | Reason / reuse boundary |
| --- | --- | --- |
| [pgx](https://github.com/jackc/pgx/blob/master/LICENSE) | MIT | Pure Go PostgreSQL; preserve notice if distributed |
| [goose](https://github.com/pressly/goose/blob/main/LICENSE) | MIT | SQL migrations; do not import all database drivers unnecessarily |
| [golang-jwt](https://github.com/golang-jwt/jwt/blob/main/LICENSE) | MIT | Token verification library; explicit policy still required |
| [goldmark](https://github.com/yuin/goldmark/blob/v2/LICENSE) | MIT | Optional Markdown rendering, bounded by fixtures; no Ghost rendering equivalence implied |
| [bluemonday](https://github.com/microcosm-cc/bluemonday/blob/main/LICENSE.md) | BSD-3-Clause | HTML sanitation; retain notice/disclaimer, no endorsement use |
| [x/crypto](https://github.com/golang/crypto/blob/master/LICENSE) | BSD-3-Clause | Argon2id; preserve applicable notices |
| [x/net](https://github.com/golang/net/blob/master/LICENSE) | BSD-3-Clause | HTML parser; preserve applicable notices |
| [PostgreSQL](https://www.postgresql.org/about/licence/) | PostgreSQL License | Self-hosted database; permissive with notices |
| [Caddy](https://github.com/caddyserver/caddy/blob/master/LICENSE) | Apache-2.0 | Optional TLS proxy; retain license/required notices when redistributed |
| [restic](https://github.com/restic/restic/blob/master/LICENSE) | BSD-2-Clause | Optional encrypted backups to owned storage/SFTP |
| [Ghost 6.72 source](https://github.com/TryGhost/Ghost/blob/v6.72.0/LICENSE) | MIT | Public source can be inspected; no code copied here. Any later substantive reuse carries attribution/license obligations |
| [Koenig](https://github.com/TryGhost/Koenig) | MIT per its repository | Components moved into Ghost monorepo; any converter/node reuse needs exact package license/version review. Not a Go runtime dependency |
| [Lexical](https://github.com/facebook/lexical/blob/main/LICENSE) | MIT | Schema/rendering reference; JS runtime is not introduced into core CMS |

Some inspected license-file Git blob IDs, for reproducibility: pgx `5c486c39a2244d0637a7cd47626cbaee8ac92aa6`; goose `fd425e0d3936d5309d8561353916eadce7998dc5`; golang-jwt `35dbc252041eae41edbf286d56d48371bc670d39`; goldmark `dc5b2a6906a4c3ce57b21c447542a5889f0a8221`; bluemonday `f822458ed0c4f8707c940247e5a19bb5f0909aef`; x/crypto and x/net `2a7cf70da6e498df9c11ab6a5eaa2ddd7af34da4`; Ghost `d24c76dee6e7c4e376541210da7ea9f2ba683b4e`.

Historical note: a separate Node/Fedify product was previously selected, then superseded by ADR 0008. CMS and ActivityPub now belong to one package; the later runtime approval selects internal Node/Fedify (ADR 0009). The ActivityPub task owns its dependency/lockfile/license inventory; CMS dependency evidence does not claim to audit that module.

Ulysses and iA Writer are compatibility clients, not sources of reusable implementation. Use their public documentation and authorized black-box tests; do not copy proprietary code, assets or UI. Ghost's MIT license does not grant its trademarks. Name the project content and describe tested API compatibility without implying Ghost Foundation endorsement. A license for this new project's own code/distribution is still an owner decision; this architecture task intentionally does not assign one.

## Operational sources

- [Go release history](https://go.dev/doc/devel/release): proposed supported toolchain, recheck patch level before use.
- [PostgreSQL version policy](https://www.postgresql.org/support/versioning/): supported major selection and patch lifecycle.
- [PostgreSQL SQL dump](https://www.postgresql.org/docs/current/backup-dump.html): database backup behavior; external media coordination is our design responsibility.
- [PostgreSQL SELECT/locking](https://www.postgresql.org/docs/current/sql-select.html): row-locking queue primitives.
- [pgx project](https://github.com/jackc/pgx) and [goose project](https://github.com/pressly/goose): intended driver/migration capabilities.

Research limitations: official Markdown documentation was fetched read-only when HTML extraction omitted the body. The support docs do not expose complete client HTTP sequences. Exact app builds, existing installation versions, consumer routing, historical actor/key data and all measured interoperability results remain uninspected.

## Adopted local milestone dependencies

The subsequent local implementation pins pgx v5.11.0, goose v3.29.0, golang-jwt v5.3.1 and bluemonday v1.0.27. Their direct/runtime transitive license texts are preserved in [third-party notices](../THIRD_PARTY_NOTICES.md); go.mod/go.sum record selected versions and checksums. No Ghost/client implementation was copied. Build validation used installed Go 1.26.4. No claim of a current vulnerability audit or project distribution-license selection is implied.

## Review editor dependencies

Checked 2026-10-26. The review editor bundles Tiptap 3.31.4 (`@tiptap/core`, `starter-kit`, `extension-image`, `extension-table`, `extension-link`, `extension-code-block`, `markdown`, `pm`), ProseMirror packages, marked, linkifyjs, orderedmap, rope-sequence and w3c-keyname. All are MIT licensed; marked also carries John Gruber's original Markdown license. Exact versions and license texts are generated in [editor notices](../internal/review/assets/editor-notices.md). References: [Tiptap vanilla JavaScript install](https://tiptap.dev/docs/editor/getting-started/install/vanilla-javascript), [Markdown support and limitations](https://tiptap.dev/docs/editor/markdown) and [schema behavior](https://tiptap.dev/docs/editor/core-concepts/schema). `playwright-core` (Apache-2.0) is used only by tests and is not bundled.
