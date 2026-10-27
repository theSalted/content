# Generic typed content-field interface

Proposed reusable core capability, outside Ghost API compatibility. The consumer owns concrete field registrations and publication UX. There are no built-in page keys, import sources, frontend components or consumer account assumptions.

A server-owned registry maps a stable field identifier to owner/entity, writers, text or explicit structured type, schema version, limits and visibility policy. Unknown/unregistered IDs fail. Dynamic entity identifiers resolve against authorized existing objects. Clients cannot choose ownership, grant capabilities, register schemas or submit SQL paths.

Use standard PostgreSQL field/revision tables in the configured CMS schema, not an external KV service. Plain values are text; JSONB is for a validated structured schema only. Enforce the appropriate representation and retain immutable revisions with actor, schema version, mutation identifier and commit time.

A generic `readFields(ids)` returns authorized values and revision tokens. `saveField({fieldId, baseRevision, mutationId, value})` authenticates, authorizes, validates and compares the base revision atomically before appending history. Conflicts return authorized current state without overwriting the draft; successful responses return the committed canonical value/revision. Identical mutation retries reuse the recorded result; changed commands with the same mutation ID fail.

Consumer transports preserve unsaved edits after failures/uncertain responses and do not clear edits made during an in-flight save. Undo before save is local. Undo after save is a new validated authorized revision against the current base; history is never rewound or deleted. HTTP routes/auth adapters and atomic batch operations remain to be specified.

Render text as escaped text. Structured data uses validated types and safe URL schemes; no implicit raw HTML. The core enforces configured draft/publication policy; it does not infer Save means Publish. Separate draft/published pointers if staged publication is chosen. Consumer UX, publication authorization and cache invalidation must agree with the production response contract.

Acceptance gates cover two unrelated consumers, unknown/unauthorized fields, concurrency, retry deduplication, draft preservation, non-destructive undo, XSS and configured publication semantics. No implementation is supplied by this document.
