# Confirmed federation discovery

Implemented and disabled by default. The Content API's public published posts
include `activitypub:null` until the CMS has an authenticated current public-object
confirmation. Confirmed shape:

```json
{"activitypub":{"object_id":"https://example.org/ap/objects/publication/opaque-instance","state":"public","acknowledged":true}}
```

The exact HTTPS URL originates in the receiver's receipt-resolution response.
Neither the website nor CMS derives it from a UUID or preparation cursor. Explicit
`fields` selection applies. Pages, drafts/private records and Admin read responses
do not expose discovery metadata. Post and projection read under one database
snapshot, scoped to publication/destination/current public generation.

A same-instance edit retains its confirmed URI while an update is queued. A
trigger-maintained public generation hides the old URI immediately on withdrawal
and while republish is pending. A delayed response for an earlier generation cannot
restore it. An authoritative nonpublic resolution invalidates only its exact
instance/current generation. A public URI cannot change within one lifecycle.

## Read-only resolution

The receiver contract is `POST /internal/v2/receipts/resolve` on its private listener,
with the exact stored event/archive inside `{"schema_version":1,"event":...}`.
This is a query: never publish or replay events from this route. Existing event,
archive and202acknowledgement formats remain unchanged.

The response binds schema version, event/publication/content/source/epoch/revision,
canonical event hash and current public instance. Go's canonical hashing is tested
against the actual Node implementation, including HTML escaping, Unicode, arrays
and line separators. `applied` with a matching current public projection confirms;
`stale`, `superseded` and `not_public` cannot invent a link. The CMS also checks its
current source and lifecycle. Current supported source epoch is1; authority transfer
requires a separate explicit migration contract.

Accepted deliveries are copied once into a separate durable resolution queue. A
failed query retries only immutable read bytes with leases, timeouts, jittered
backoff and a12attempt budget. It never rewinds accepted delivery state. Projection
and completed query state commit atomically. Quarantined queries are retained for
explicit operator recovery, without modifying event IDs/payloads or replaying writes.
Already accepted archive records can resolve through this same read path with zero
historical broadcasts. No automatic import or archive enrollment is introduced.

## Deployment gates

`CONTENT_FEDERATION_PROJECTION_ENABLED` defaults false. Both the CMS reader and Go
sender require the explicitly configured `CONTENT_PUBLISHING_DESTINATION`. The
sender additionally requires `CONTENT_RECEIPT_ENDPOINT` at the fixed private route;
its existing publisher token remains only in that child. The CMS HTTP process and
website never receive the publisher credential.

Before activation, apply migration10 and explicit scoped grants, deploy the actual
receiver endpoint, pin its response fixtures and pass combined lifecycle tests. The
internal AP supervisor must allow the optional `resolution` lane, its progress and
counts. Older supervisor builds reject unknown protocol events. Never enable
this child flag before the coordinated supervisor update. No new service is needed.

API role: projection SELECT only for publication/post/destination/generation/URL/
confirmed fields. Delivery role: its existing accepted queue reads plus narrow
post identity/generation reads, projection SELECT/INSERT/narrow UPDATE and resolution
queue SELECT/INSERT/lease-state UPDATE. No article-body edit, AP schema, DDL or DELETE
authority. Migration owner alone owns tables/functions. No grants are automatic.

Disable the flag to roll back read exposure, preserving ordinary publishing and
additive storage. Never drop content, replay archives or reset accepted deliveries.
Until production confirmation covers the existing archive, a consumer may retain
its verified static fallback separately; no personal-site exception belongs here.

## Current evidence and limits

Local tests cover pending vs confirmed discovery, query outage independent of
publishing, exact hash/source/instance binding, stale leases, atomic rollback,
publication isolation, role denial, silent archive resolution, concurrent withdrawal,
republish and late acknowledgements. The actual AP HTTP resolver is also exercised
through its synthetic local server: publish/edit/withdraw/republish, seven pinned
receipt fixtures, superseded instances, exact-payload conflicts and repeated queries
without new activities. The AP supervisor/runtime remains separately owned; local
interoperability tests do not replace deployment and restricted-role verification.
Authored tags remain a separate opt-in feature. Feedback collection and private
moderation APIs belong to the internal ActivityPub module; the Go CMS has no
moderation client, route, UI or token configuration.
