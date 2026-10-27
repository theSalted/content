# CMS → internal ActivityPub v2 review outcome

2026-10-26: the earlier review is resolved for the bounded local adapter. The AP owner froze the [integration contract](../internal/activitypub/contracts/INTEGRATION.md) and [v2 schema](../internal/activitypub/contracts/publishing-event.v2.schema.json). Go implementation and combined loopback tests now pass. [Delivery documentation](publishing-delivery.md) describes exact configuration, persistence, grants and limits. The standalone legacy example and prototype contract remain superseded; they were not silently renamed into v2.

| Earlier review issue | Implemented resolution |
| --- | --- |
| Stable content versus terminal AP object | Post UUID stays stable; withdrawal ends its instance; republishing persists a new instance UUID |
| Retry identity and mutable snapshots | Exact v2 payload bytes and event UUID persist before first send; retries and reconnect use the same bytes |
| Revision width/source authority | Positive decimal strings; new enrollment starts at epoch 1; no automatic authority transfer |
| Existing outbox/history | No backfill or existing-post enrollment; migrations preserve history unchanged |
| Canonical URL | HTTPS URL base frozen at new-post enrollment; event slug comes from immutable intent snapshot |
| HTML size | AP now accepts 1 MiB UTF-8, matching CMS; total encoded payload is bounded to 2 MiB |
| 202 semantics | Persist applied/stale, including duplicate original_result; acceptance is not remote delivery |
| Permanent errors | Durable quarantine blocks later events for that article; other articles continue; no automatic history rewrite |
| Combined testing | Real CMS PostgreSQL + actual AP Node HTTP receiver using synthetic PGlite state; full publish/edit/withdraw/republish and lost-ack retry pass |

The earlier title mismatch is resolved by profile `content-activitypub-v2-2026-10-26-r2-unicode-title`: both modules accept 1–255 Unicode code points, nonblank under Unicode White_Space and no NUL. Shared fixtures cover ASCII/emoji/mixed/combining boundaries, exact preservation, Unicode blanks and embedded whitespace. The combined test persists and publishes 255 emoji unchanged. The AP projection also intentionally omits images/unsupported markup. Those are explicit rendering/acceptance gates before live activation, not unresolved wire-field choices.

Historical checkpoint: this document initially recorded an unimplemented proposal and requested frozen fixtures, explicit enrollment, immutable URL/instance storage, aligned limits and error recovery. Subsequent AP-owner fixtures and the tests above supplied that evidence. No Node-owned files, existing identity keys, production endpoints, old prototype repository or historical outbox data were changed by the Go implementation.
