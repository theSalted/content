# Internal ActivityPub module and migration boundary

Current decision: [ADR 0008](adr/0008-package-and-database-boundaries.md) selects ONE reusable `content` package/repository with CMS and ActivityPub modules. The website is a separate consumer. The former separate Node/Fedify product is superseded, and the later approval selects **internal Node/Fedify alongside Go**, recorded in [ADR 0009](adr/0009-internal-fedify-runtime.md). The independent module task owns its implementation. No live federation is activated by CMS work.

## Identity is configuration and preserved data

No personal actor, domain or key is a core default. Every deployment supplies its canonical actor/WebFinger/public URL configuration and securely retained keys. Before migration or activation, verify the historical actor ID, key ID/public and private key availability, object URLs and relationships. A renamed repository or changed backend is not a new actor identity.

Preserve the unavailable historical federation database and backing recovery material. Missing connectivity is not an empty installation and must not trigger initialization, replacement-key generation, reset or deletion. CMS authoring can be considered independently of federation activation; outbound publication stays disabled until identity recovery and contract gates pass.

The earlier audit reported inconsistent legacy follower stores and no complete blog lifecycle flow. Preserve necessary identity/data, not obsolete code for its own sake. Those findings do not demonstrate a working replacement or authorize a runtime merge.

## Internal publication seam: consolidation unresolved

The CMS transaction writes revisions and durable normalized outbox records. ActivityPub owns protocol projection, actor keys, follower/delivery state and immutable public snapshots in its owned schema. Direct mutable-table coupling is not justified by sharing a repository/database. The internal module proposes `POST /internal/v2/events`; a Go outbox adapter must first agree and test the persisted identity/lifecycle/retry mapping. No direct writes to the AP schema are permitted.

The audit identified a **publishing-contract mismatch** between the CMS proposal and existing ActivityPub prototype. The example in `contracts/examples/publication-event.example.json` remains illustrative and must not be treated as a valid prototype request. See [the consolidation ledger](../contracts/publication-consolidation.md). Exact field/version/auth/acknowledgment mapping and runtime tests remain pending; this correction does not invent an adapter or claim compatibility.

Required semantic invariants to preserve through that review:

- Stable event ID for retries, immutable content identity, source namespace, ownership generation and revision. Ghost UUIDs are external adapter mappings, not universal core primary keys.
- Explicit operation/visibility; a public-to-private transition withdraws prior public publication without exposing the new private body. Deleted objects retain necessary identity/tombstone history.
- Immutable snapshot body/checksum and provenance; never re-read a newer mutable post and label it as an old event.
- Commit-before-delivery; acknowledgment distinguishes durable acceptance from remote delivery. At-least-once retries require deduplication, generation/revision fencing and per-content ordering.
- Shadow import does not broadcast. Consumers reject old-writer generations; source changes do not reannounce historical articles as fresh objects.

## One database, separate owned schemas

The current deployment uses one Supabase PostgreSQL database with CMS, ActivityPub and website-owned schemas/roles. Core accepts standard PostgreSQL and configurable schema names. Module tables and migration ledgers are owned separately; narrow contract access is explicit. Qualify SQL and namespace migration/advisory locks by installation/module/schema, stable across versions and any Go/Node implementation. Avoid schema-dependent default search paths and global lock constants.

Schemas share resources, connection budgets, outage and database restore boundaries. Whole-database recovery must coordinate revisions, outbox/delivery history and website references. Schema-only recovery is not automatically a safe isolated module rollback. See [operations](operations.md).

## Gradual coexistence without duplicate publication

1. Later authorized inventory records each source publication's IDs/UUIDs, routes/media and existing external object IDs. Consumer-specific domains remain in consumer configuration.
2. Shadow-import a selected subset with source namespace + external ID mappings, original timestamps/URLs and no outbound events.
3. Compare content, metadata, media, visibility and identities. Quarantine unsupported content rather than silently changing it.
4. Assign one active writer per article; legacy sources retain existing content until explicitly transferred. Shared presentation can be designed by the independent consumer.
5. For a transfer, freeze a batch, import its final revision, verify it, increment ownership generation, fence the old producer, and only then enable the new producer.
6. Preserve canonical URLs/aliases and federation object identities. A backend move must not become a new actor/article.
7. Rollback transfers ownership with a new higher generation and reconciles already accepted work before restoring routing. Never re-enable a stale producer generation.

Historical product decisions remain in [ADR 0004](adr/0004-durable-outbox-federation.md). Legacy databases/keys, production routing and prototype code are untouched by these documentation corrections.
