# Private receipt resolver v1

POST `/internal/v2/receipts/resolve`, on private listener only. Authenticate using the publication's existing publisher token; moderation tokens cannot use this route. Content-Type application/json. Request is strictly `{schema_version:1,event:<exact saved v2 publishing or v1 archive event>}`. It does not process/replay the event or enable archive intake.

See fixtures.json and its SHA256 in manifest.json. The response preserves event_id/publication_id/content_id/source/revision and event_hash. Hash is SHA256 of UTF-8 canonicalJson of the validated event: recursive object-key sort, unchanged array order, JSON string encoding and no whitespace. Existing shared publishing hash fixtures are authoritative for cross-language encoding.

result is original receipt's applied/stale. projection is current public object_id/object_instance/revision/state, only if applied, current source/epoch/instance matches the exact queried event and current revision>=eventrevision. reason is confirmed, stale_receipt, superseded or not_public. Withdrawal yields null; later republish cannot rebind an older event. Changed exact event payload409, unknown receipt404, wrong publication/source403, bad credential401. A single database SELECT supplies consistent current state and receipt. Response Cache-Control no-store.

No changes to existing202ACK shape, migrations, grants, or publishing event schema. CMS must still validate its current publication/generation/instance before persisting projection, and clear public discovery atomically on withdrawal. Failed resolution must not cause already-accepted publication events to replay. Current25archive projections may be reconciled from immutable saved snapshots without archive enrollment or broadcasts.
