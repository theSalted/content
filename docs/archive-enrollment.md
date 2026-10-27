# Explicit durable archive enrollment

This integration is opt-in. Import, schema migration, application startup and the
ordinary delivery worker never enroll history. No production migration or import
is performed by this patch. Apply migration9 through the existing CMS migrator;
no new schema, role, credential, service or provider dependency is introduced.

An operator calls `Store.EnqueueArchive(ctx, ArchiveRequest{Publication, PostID,
ExpectedRevision, Enrollment})` for each reviewed public post. One call handles
one explicit ID/revision. The source must be kind=post, published and public;
drafts/pages and other publications fail. A pre-existing ordinary publishing
stream cannot be overwritten. The operator must supply the final consumer URL
base, source ID and destination; there are no personal defaults.

The post row is locked separately from its revision join, so a concurrent edit
cannot produce a mixed snapshot. One transaction saves the exact archive bytes
and event ID, a paused ordinary stream, and an edit barrier. Repeating the same
request returns the same receipt; changed revision/source/destination/URL base
fails. Original timestamps and CMS UUID are preserved, with initial federation
object instance equal to that UUID. Archive uses the same permalink mapping as
live updates (currently a trailing slash). Align the consumer's canonical URL
policy before enrollment; do not rewrite immutable saved payloads on retry.

Use the existing `publishing.Worker` with `Queue: store.ArchiveQueue()` and a
`publishing.NewArchiveHTTPClient` targeting the fixed private
`/internal/v2/archive`. It uses the same lease/backoff/attempt limits and explicit
enabled flag as the live worker. The archive queue never prepares posts itself.
No network runs inside a database transaction. Only the initial operator snapshot
creates a job; its schema_version1/content.archived payload cannot be accepted by
the live schema_version2 event endpoint. The AP receiver archives silently with
zero follower delivery intents. The API's existing archive feature flag must be
enabled for the reviewed import window, then disabled when complete.

Only an applied acknowledgment (including duplicate/original_result=applied)
atomically accepts the receipt, unpauses the normal stream at the archived revision
and instance, and releases the edit barrier. Lost acknowledgments retry identical
bytes. Old outbox rows at or before the accepted revision are skipped. Later edits
produce Update to the same object; withdrawal produces the normal body-free
withdrawal event and AP Delete/Tombstone. No extra live Create is generated.
An exact old archive retry after withdrawal cannot reactivate the stream.

While pending, leased or quarantined, normal CMS edits and withdrawal return409
and retain the editor's unsaved changes. This deliberately holds the reviewed
public revision until acceptance rather than racing a withdrawal with its archive.
A quarantined job needs operator reconciliation using the same event and payload;
never clear the barrier or delete the receipt after an uncertain remote outcome.
This patch adds no automatic cancel/replay command or mass-enrollment endpoint.
The CMS currently exposes withdrawal, not a hard-delete API; future hard delete
must honor this barrier and emit the existing explicit deletion contract.

## Explicit privileges for the existing owned schema

No grants execute in migration9. Existing API table SELECT already covers
posts.archive_pending; its column-limited UPDATE must NOT include that column.
API must not receive SELECT on archive payloads or UPDATE on archive/stream state.

Operator enrollment needs existing post/revision/installation SELECT, SELECT on
publishing_streams and publishing_archives, INSERT on both tables, and
UPDATE(archive_pending) on posts. Use the approved operator context, never put its
DSN into a web process.

The delivery role needs SELECT on installation, publishing_streams and
publishing_archives; SELECT(publication_id,id,archive_pending) and
UPDATE(archive_pending) on posts for the scoped row-lock/acknowledgment; the
existing stream state UPDATE columns; and only
UPDATE(state,attempts,next_attempt_at,lease_token,lease_until,last_error_code,
accepted_result) on publishing_archives. No payload/event/content/source/URL
updates, archive INSERT/DELETE, post-body reads, schema ownership or other-module
access. Existing normal outbox/delivery privileges stay unchanged. Verify these
grants against restricted roles before production activation.

## Validation status

Pure contract/HTTP classification tests use an in-memory RoundTripper, not a
network listener. PostgreSQL tests target only unique synthetic schemas in an
explicit existing loopback *_test database; no new databases or roles. They cover
concurrent enrollment/edit arbitration, draft/page/scope rejection, exact retries,
stale lease rejection, zero historical live delivery, later Update/withdrawal
continuity and a quarantined acknowledgment retaining the barrier. Their actual
execution status is recorded in the task handoff, not implied by their presence.

## Operator command and continuous sender

Build `go build ./cmd/content-archive`. The operator requires the explicit migration DSN/schema/installation. `content-archive --manifest manifest.json` is read-only. Apply requires `--apply --sha256 <exact file hash>`; the strict manifest pins publication, source, destination, frontend URL and each public post ID/UUID/revision/slug/publication date. It validates all records before writing, then rechecks each under its row lock. Preserve receipts after any partial failure and rerun the same manifest; never clear edit barriers or generate replacement event IDs. The operator is excluded from runtime images.

`content deliver` remains disabled by default. The optional `CONTENT_PUBLISHING_ARCHIVE_DELIVERY_ENABLED=true` lane requires an explicit archive endpoint and shares the same two-connection pool/process as live delivery. It only consumes persisted receipts; it never discovers history. Disable that lane after bounded enrollment completes. Versioned JSONL supervision reports local progress, queue counts and durable receiver acceptance, never article bodies, credentials or remote-peer delivery success. SIGTERM cancels both lanes and closes their shared pool; interrupted leases recover immutable bytes.
