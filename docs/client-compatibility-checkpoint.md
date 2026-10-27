# Publishing-client compatibility checkpoint

**Historical initial discovery checkpoint, 2026-10-26. Superseded for iA by [actual local client evidence](ia-writer-local-checkpoint.md); installation, authorization and implementation progressed afterward.**

Checked 2026-10-26. This is a bounded local API milestone, not a successful Ulysses or iA Writer connection. The reference remains Ghost 6.72.0; no reference Ghost process or client traffic was captured. Public documentation describes user workflows and API contracts, but does not reveal the clients' exact request order, headers or serialization.

## Verified client evidence

- **Ulysses:** its current guide uses the site URL and a staff access token. It supports updating posts previously published from a sheet. Heading, matching existing keywords, first note and attached image map to title, tags, excerpt and feature image. [Official guide](https://help.ulysses.app/export-publishing/publishing).
- **iA Writer for Mac:** its guide uses an integration Admin API key and API URL, offers HTML/Markdown options and creates a fresh draft with browser handoff for each share. [Vendor guide](https://ia.net/writer/support/preview/blog). Ghost's separate guide instead shows a staff token and Default/HTML Card/Markdown Card choices. Treat this as documentation drift, not proof that version 8 emits a particular format. Its URL guidance omits `/ghost`. [Ghost integration guide](https://ghost.org/integrations/ia-writer/).
- Installed here: `/Applications/iA Writer.app`, bundle `pro.writer.mac`, version **8.0.9**, build **80058**, read from its Info.plist. No Ulysses bundle found under `/Applications` or `~/Applications`; this is not a search of every possible installation location. Neither app was launched or reconfigured. No documents, account preferences, Keychain, license state or credentials were read.

## Minimum contract versus current implementation

`D` means the public API/workflow is documented; `U` means whether/how this client requests it is unknown. A documented endpoint is not automatically a proven client handshake requirement.

| Step | Documented request or workflow | Runnable content result | Client evidence/gate |
| --- | --- | --- | --- |
| Discover site | `GET /ghost/api/admin/site/`, no authentication, single `site` object | **Added**: 200 with configured title/description/logo, canonical frontend URL and wire-family version | D: discovery contract; U: either client's probe order/required fields |
| Authenticate | `Authorization: Ghost <HS256 JWT>`; header kid/typ, decoded hex secret, iat/exp ≤5 min, aud `/admin/` | Existing publication-scoped key verified by independently signed fixture | D: token wire format shared by integration/staff; U: audiences/headers each app actually sends. No staff principal/role/key provisioning model |
| Identity/settings | `GET /ghost/api/admin/users/me/`, `users/?include=roles`, `roles/`, `settings/`, `config/` are candidate probes | 404; no invented owner/user/settings responses | U: request necessity/order. Implement after trace; preserve staff-vs-integration permissions |
| Create text draft | `POST /ghost/api/admin/posts/?source=html` with `{"posts":[{"title":"…","html":"…"}]}` | 201; scoped ID, draft status, HTML, timestamps; retained in PostgreSQL | D: Ghost HTML input; U: app mode sends HTML, Lexical or Mobiledoc. Default author is still missing |
| Read/update | `GET .../posts/{id}/`; `PUT .../posts/{id}/` with `updated_at`; `source=html` for body replacement | 200; stored revisions; stale token 409 with no overwrite; draft stays private | D: Ulysses updates its published sheets; U: exact payload and Ghost error/no-op equivalence |
| Upload image | `POST .../images/upload/`, multipart `file`, optional `ref`, `purpose` (default `image`); 201 `images:[{url,ref}]` | Implemented PNG/JPEG subset with private draft assets; filesystem + S3 adapter. Actual iA/Ulysses image requests still unverified | D: image API, Ulysses feature image workflow. U: client multipart filenames/purpose, request ordering, retry behavior |
| Metadata/tags | Ulysses uses existing tags, excerpt, feature image; Ghost post writes accept relations | Tags routes 404; tag/excerpt/feature_image fields rejected before write | D: workflow gap. Requires durable taxonomy/media/post metadata, not empty arrays that pretend success |
| Browser review | iA opens the newly created draft in browser | `/ghost/` is 404; no review/publish UI | D: required to finish iA workflow. U: exact deep link, including fragment; fragments never reach server |
| Public read | `GET /ghost/api/content/posts/{id}/?key=…` | 404 while draft; 200 only published/public; private fields excluded | Documented publishing test; not proof either editor uses Content API |
| Version/TLS/base path | Ghost minimum-version header; Admin domain may differ from frontend and include a subdirectory | Only absent/`v6.0` header, root API paths, HTTPS frontend (loopback HTTP allowed); configured frontend path retained | U: actual client versions, URL normalization, redirects and local HTTP/trust acceptance. Older header/versioned route gaps are explicitly tested |

Primary API sources: [Admin/auth](https://docs.ghost.org/admin-api), [site](https://docs.ghost.org/admin-api/site/overview), [create](https://docs.ghost.org/admin-api/posts/creating-a-post), [update](https://docs.ghost.org/admin-api/posts/updating-a-post), [image fields](https://docs.ghost.org/admin-api/images/uploading-an-image), [image response](https://docs.ghost.org/admin-api/images/overview), [version semantics](https://docs.ghost.org/faq/api-versioning). Site unauthenticated behavior is also confirmed by [pinned OSS controller](https://github.com/TryGhost/Ghost/blob/v6.72.0/ghost/core/server/api/endpoints/site.js). Endpoint candidates beyond the documented subset retain the earlier [pinned route inventory](ghost-compatibility.md). No proprietary client implementation was copied or inspected.

## Implemented discovery slice

`CONTENT_SITE_TITLE` (default `Content`), `CONTENT_SITE_DESCRIPTION` (default empty), and `CONTENT_SITE_LOGO` (default null in JSON) expose only explicitly public metadata. Logo accepts a root-relative path or HTTPS URL, without userinfo/query/fragment. Metadata validation and lengths are local policy. The API does not fetch or serve that logo. `site.url` uses `CONTENT_SITE_URL`, preserves its frontend base path and adds the trailing slash; request Host/forwarded headers cannot change it. Each configured publication process has its own metadata. No database migration is needed.

The endpoint is read-only and independent of database access after service startup. It accepts no query parameters in this subset; invalid methods/queries fail explicitly. `site.version: "6.0"` and `Content-Version: v6.0` identify the selected wire family, **not a Ghost installation or broad compatibility claim**. `X-Content-Compatibility: posts-html-v1` identifies the limited adapter; no `X-Ghost-Version` is emitted. Existing restrictive Accept-Version behavior remains a documented gap, not an implementation of Ghost's minimum-version negotiation.

## Executable evidence

- [Documented HTML scenario](../contracts/client-probe/documented-html-workflow.json): ten synthetic requests, executed over loopback HTTP against real disposable PostgreSQL with a restricted runtime role. JWT is signed independently using standard HMAC code and fixed synthetic fixture material. Covers discovery, unauthorized access, create/read/edit, collision, publish/public eligibility and a second new draft. Asserts two posts, four revisions, one CMS outbox intent and zero enrolled deliveries.
- [Current gaps](../contracts/client-probe/current-gaps.json): fifteen separate local limitation assertions. Their 400/404 responses intentionally **do not** represent expected Ghost responses. Rejected workflows leave the store unchanged.
- Tests: `storage/postgres/client_contract_test.go`, `internal/httpapi/site_test.go`, `internal/httpapi/client_gaps_test.go`, config regression. Site tests include unauthenticated/invalid-token access, fixed frontend URL, process isolation, null logo, methods, version/query failures and metadata validation.

Run `sh scripts/test-postgres.sh` for disposable PostgreSQL plus Go regressions, or `CONTENT_TEST_NODE=/path/to/existing/node sh scripts/test-publishing.sh` for the full combined suite. Both reuse an existing cached PostgreSQL image; neither installs services or contacts production. Plain `go test ./...` explicitly skips PostgreSQL/Node checks without opt-in variables.

## Next verification gate

The smallest next investigation is an **approved disposable iA Writer 8.0.9 account setup** against a loopback test server, recording method/path/query/header names and synthetic request bodies while redacting Authorization. It requires permission to change the app's publishing-account configuration, a disposable endpoint and a scoped test key (not production credentials), and one synthetic draft. A password login is not required by the documented token setup. This turn authorized availability checks only, so no account was added and no key generated. Local HTTP/TLS acceptance remains to be measured; do not disable transport security or install trust certificates speculatively.

Ulysses needs an available licensed installation and separate approval for a disposable test account/sheet. Its guide says accounts may sync via iCloud; account creation must account for that. The exact browser destination and observed request/format corpus should select the next coherent implementation: discovery/auth probes if required, then draft handoff or media/tag persistence. Do not fabricate staff identities, declare rich-format fidelity, or claim a working client from these API tests.
