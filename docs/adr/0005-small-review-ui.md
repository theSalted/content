# ADR 0005: Small native review/publish UI

Proposed 2026-10-25; accepted and locally implemented 2026-10-26 after the user's explicit draft-page request. Earlier handoff uncertainty is resolved by an actual iA trace.

## Decision

Use Go templates and native HTML controls for a publication-scoped title/body review, sandboxed preview, Save draft and explicit Publish. iA Writer 8.0.9 opens `/ghost/#/editor/post/<id>/`; a strict landing parser routes that fragment to authenticated `/ghost/posts/<id>/`. This is part of the reusable content package, not the consuming website or the disposable Ghost reference.

A separately configured operator/password hash bootstraps the bounded native browser session. An integration key is never a browser credential. Session rotation/expiry/logout, CSRF plus Origin checks, optimistic concurrency and draft preservation are required. The current password format is PBKDF2-HMAC-SHA256/600,000 iterations using the Go standard library; this supersedes the earlier unimplemented Argon2id suggestion for this slice. Sessions are bounded process memory and restart revokes them; distributed/staff/MFA parity is not claimed.

The earlier proposal also mentioned tag administration and client-credential management. Those remain deferred; they are not needed to complete the measured text-draft handoff. No Ghost Admin bundle, proprietary UI or rich editor is copied.

## Consequences

The small page can finish the tested iA workflow without turning the package into a full dashboard. Public author attribution is persisted separately from operator authentication; it claims no staff role. Every write goes through existing revision/outbox transactions. The UI's single-operator and deployment constraints remain explicit.

See [configuration, behavior, security and tests](../draft-review.md), [actual client evidence](../ia-writer-local-checkpoint.md) and [security](../security.md).
