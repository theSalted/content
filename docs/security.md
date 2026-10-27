# Security and authentication design

Broader security design; implemented local behavior is identified separately below. The package supports explicitly scoped configured staff/integration principals; a single-owner installation is one deployment option, not a hard-coded account model. Security behavior is part of compatibility; any deliberate divergence must appear in the profile and acceptance report.

## Three different authentication contracts

| Contract | Purpose | Initial disposition |
| --- | --- | --- |
| Ghost integration key → JWT | iA Writer and automation identity | Implement with endpoint capability allowlist and revocation |
| Ghost staff key → JWT | Ulysses and role-bound desktop clients | Implement active-user/role checks; never equate it with an owner-wide integration |
| Ghost email/password → session cookie | Ghost Admin-style login and MFA protocol | Deferred unless traces require it; native review UI gets its own explicitly named session routes |

Public Content API keys identify access to the public projection; they are not user login, preview authorization or write credentials. Hash these equality-checked keys in storage. Redact query keys in logs even though they are meant for public clients.

## Admin-key storage and JWT validation

Ghost clients possess a symmetric signing secret, so the server must recover that secret to verify an HMAC. **A password-style one-way hash alone cannot verify these client-created JWTs.** Encrypt secrets at rest with standard-library AEAD, a versioned encryption format and a root key held in a protected file outside PostgreSQL. Back up that root key separately. Revoking a key must invalidate requests promptly; bound any cache and invalidate it on rotation.

Provision per-client keys through a future local operator command, showing them once. Do not expose an unauthenticated web setup endpoint or a default password. Staff tokens reference a real staff account; integration keys reference a separate integration. Preserve restricted write permissions even if clients request broader metadata during discovery.

Fix the accepted algorithm to HS256, decode `kid` only as an untrusted lookup key, verify the signature before authorization, bound token/header size and require validated time and audience claims according to the declared profile. Start with documented `/admin/` and short expiry; capture legitimate audience spellings, missing-claim behavior and skew before claiming exact parity. Reject `none`, algorithm confusion, content keys used as Admin keys, revoked credentials, inactive staff and malformed IDs. Do not accept an Admin key itself as a bearer token.

The public verifier in Ghost 6.72 has no `jti` one-use storage. Our adapter should not introduce a replay cache that rejects legitimate short-lived token reuse; TLS, short lifetime, revocation and rate limits are the practical protections. Do not implement query-string Admin tokens generally: Ghost uses special URL-token paths for internal schedules, while our scheduler invokes domain services locally.

## Native review UI

The minimal review/publish page is necessary for the iA workflow, but is not a clone of Ghost's UI or security system. The [implemented bounded page](draft-review.md) uses Go templates, separately configured operator identity, PBKDF2-HMAC-SHA256/600,000-iteration password hashes, opaque sessions, HttpOnly/Secure/SameSite cookies, CSRF tokens, exact Origin checks and expiration/logout. This supersedes the earlier Argon2id proposal for this slice. HTTPS is required except literal local testing; sessions are process-local and restart revokes them. Staff roles, multi-replica sessions and MFA remain unimplemented. No JWT signing secret enters JavaScript, HTML, localStorage or preview URLs.

If Ghost session parity is later required, the documented wire flow is unwrapped `POST /ghost/api/admin/session/` with `username` and `password`; success is an empty 201 with `ghost-admin-api-session`. A verification challenge is a 403 `Needs2FAError` plus the pending cookie; `PUT session/verify/` sends `{"token":"..."}`, and `POST session/verify/` requests another code. Origin/Referer checks are integral. Ghost 6 removed `GET session/`; use the staff identity route. Exact cookie path/expiry, challenge handling and deletion response require reference fixtures. [Ghost sessions](https://docs.ghost.org/admin-api#user-authentication), [changes](https://docs.ghost.org/changes).

Email/device verification needs an actual mail delivery strategy. Do not pretend to deliver codes or disable MFA to make login appear compatible. A self-hosted SMTP option could be evaluated later; it adds operations and is not required by the current token-based publishing goal. Native TOTP, if selected, is a separate feature and does not establish Ghost email-MFA parity.

## Authorization and publication privacy

Enforce operation, role and resource ownership in application services as well as routing. Begin with owner provisioning and the minimum tested roles; never return fabricated permissions simply to satisfy a client probe. Contributor/author/editor differences need their own corpus before being advertised. Changing roles or disabling a user revokes/invalidates effective access.

Separate Admin and Content DTOs. Public reads must exclude email, password data, keys, unpublished content, internal tags, revision sources and security metadata. Reject unsupported member/paid visibility rather than downgrading it. Draft previews require authenticated access; an unguessable URL by itself is not the native privacy mechanism. Ghost-shaped media may be public before publication; document this to the user and test independently of post visibility.

## Untrusted content and uploads

Preserve original text for fidelity, but render previews with a versioned sanitizer/CSP and no arbitrary script execution. Do not pass raw HTML into trusted template contexts by accident. Track disallowed elements as conversion diagnostics and surface publish-blocking issues. Unsupported HTML/SVG, code injection and embed cards must not bypass this policy. If exact Ghost rendering needs a different trust policy, decide it explicitly with the owner.

Stream multipart files with request/file/pixel limits; verify MIME by bytes; bound decompression and parsing; normalize image metadata; use server-generated storage keys; block path traversal, symlink escapes and executable content. Keep the original filename only as metadata. Uploads first land in private staging, then become immutable finalized objects; a failed transaction leaves a reclaimable orphan, not an apparently committed asset. Use `nosniff` and correct content types. Serve active formats from an isolated origin or reject them until tested. A safe raster-only initial mode is intentionally narrower than Ghost's documented SVG/ICO support.

Do not automatically fetch remote images or oEmbed URLs in initial imports. When such a feature is added, SSRF controls must cover resolution and redirects: block loopback/private/link-local/metadata destinations, validate every redirect, pin allowed resolved addresses for a request, cap response size/time and avoid credential forwarding.

## Outbound work and secrets

Webhooks and future federation delivery need the same SSRF controls. Require configured destination policy, authenticated endpoint management, bounded retries, and separate per-subscriber secrets. Persist sanitized failure classes, not response bodies with secrets. TLS validation is always on. Reject unapproved redirects rather than forwarding signatures to a new host.

Never log Authorization/Cookie headers, submitted credentials, root keys, raw rich-text content or old federation keys. Audit key lifecycle and role changes by identifiers only. No telemetry exporter is enabled by default; logs and metrics can remain local. Use a non-root service account, restrictive media/secret file ownership, read-only app image, a writable media/temp volume, database TLS on external links, and separate runtime/migration roles.

Before first production use: verify secret-free logs and fixtures, Content API non-disclosure across all query combinations, cross-user write denial, stale-write rollback, malicious upload rejection, SSRF protections where enabled, backup restoration and key-revocation behavior. These are concrete acceptance gates, not completed checks.

## Shared PostgreSQL ownership boundary

The current deployment has one database with separately owned CMS, ActivityPub and consumer schemas. Schema-qualified queries/migrations and restricted runtime roles are required; schemas do not isolate capacity/outages/restores. No shared admin credential, provider service-role bypass, writable default search path or unrestricted cross-module access is implied. Role/schema provisioning and any provider exposure settings remain unperformed deployment work. Internal Node/Fedify is approved; bounded publication trust/acknowledgment tests pass in the Go/Node loopback corpus; this does not establish remote delivery or production authorization.
