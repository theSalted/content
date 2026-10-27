# Historical archive integration — original preparation boundary

Status: contract preparation, not implemented Go routes/workers or activation.
Ghost import is deliberately silent. Neither successful import nor the ordinary
outbox worker automatically makes historical posts federated.

## Archive enrollment

Consume `internal/activitypub/contracts/archive-v1/schema.json` (SHA-256
`d32477f3775458e7a2c55b9b9924c08e671fcafb322f495388d4c13e2cbd29ff`).
Use only validated public **posts**, excluding pages, drafts and other publications.
Preserve CMS UUID, original publication/update times and canonical consumer URL.
Persist one immutable schema_version1/content.archived payload and stable event ID,
with delivery_policy=archive, source_state=published, decimal revision/epoch and
initial object_instance=CMS UUID. Send to the fixed private `/internal/v2/archive`
with existing publisher/publication scope. Exact retries reuse stored bytes.

The Go integration needs an atomic enrollment job and paused publishing stream:
lock the expected current public revision, persist snapshot and object mapping,
then allow network delivery outside the transaction. Only after durable archive
ack may the stream become public with the accepted object instance/last revision.
A lost ack retries the same event; it must not create a new public object. Concurrent
edits/withdrawal must either be held until ack or durably queued with ordered
catch-up. Never leave a withdrawn record publicly archived. Test this race before
activation. AP acceptance is not remote follower delivery; archive creates zero
broadcast delivery intents. Do not replay old outbox records.

## Authored tags (additive profile revision 3)

The receiver profile is `content-activitypub-v2-2026-10-26-r3-authored-tags`.
Schema hash: `e2005ddf097f830deb229141729f71dd99608b9423c2078223b6ff1c509d02aa`.
Shared 23-case corpus: `internal/activitypub/contracts/fixtures/v2-tags.json`,
hash `3a3c4d172ff5e9db6a738a7ec0814979fe3dc5d21feb13803a5bbd5ed5be4847`.

Optional immutable `document.tags` contains 0–32 exact `{name,href}` values.
Names: 1–100 valid Unicode code points, nonblank under Unicode White_Space, no
NUL/unpaired surrogate or leading # after whitespace. URLs: HTTPS, configured
public content origin, no credentials/query/fragment, <=2048 UTF-16 units, no
exact duplicate href. Copy public authored tags only; never infer hashtags or
normalize/truncate names. Resolve actual consumer tag-page URLs; missing routes
are an integration gate. Omission/[] means none in the complete snapshot. Edits
must create a new revision/event; immutable deliveries are never regenerated.
Deploy receiver and verify its private status capability before producer tags.
Go's previous profile pin/stored v2 events are not silently upgraded by this note.

## Feedback ownership — superseding boundary

Feedback storage, private moderation APIs and their authorization remain owned by
`internal/activitypub`. The Go CMS does not proxy those APIs or expose a review
page, client or moderation-token configuration. Removing the CMS integration must
not approve pending feedback, revoke the AP token, delete feedback or affect
ordinary publishing and acknowledged public-object discovery. This supersedes
the earlier Go owner-dashboard proposal; its source history remains in Git.

## Scoped live verification handoff

AP owns its test-recipient fence. Go must first create a fresh draft and bind its
actual UUID to that fence before publication; never reuse an earlier publication
or assume a generated placeholder UUID. Await independently observed Create
receipt before Update and withdrawal. Persist a mandatory cleanup obligation and
reserve time for Delete/Undo/unfollow in a finally path. Never pre-withdraw to make
an undelivered Create appear successful. This note does not run production writes.
