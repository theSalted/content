# Publishing contract consolidation: unresolved

Status: historical audit mismatch, now resolved by the frozen internal v2 contract and a bounded tested Go adapter. Runtime is Go plus internal Node/Fedify. The legacy proposal below is retained as history; it is not an accepted v2 payload. See [current delivery contract and evidence](../docs/publishing-delivery.md).

The CMS `publication-event.example.json` was authored as an architecture example. The existing ActivityPub prototype has its own publishing contract. The independent audit found a mismatch; this correction has not re-inspected or modified prototype code. Exact prototype field names/types/routes are therefore not invented here.

| Contract dimension | CMS proposal / invariant | Required consolidation evidence |
| --- | --- | --- |
| Version and envelope | `schema_version`, `event_id`, publication/source identity | Record prototype accepted version/envelope; decide canonical mapping and reject unknown versions |
| Article identity | Stable `content_id`, source mapping; optional Ghost UUID provenance | Preserve existing object identity across sources; no title/slug-based merge |
| Ordering/ownership | `revision`, `ownership_generation` | Prove old/out-of-order/duplicate writes cannot republish stale content |
| Operation/visibility | Explicit lifecycle/visibility | Map operation vocabulary, unpublish/delete/private semantics without leaks |
| Body/snapshot | Immutable `snapshot_id` plus retrievable content/provenance | Align inline versus reference payload, required fields and durability before acknowledgment |
| Authorization | No credentials/keys in example | Select internal trust boundary after runtime answer; schema sharing is not authorization |
| Acknowledgment/retry | Durable acceptance differs from remote delivery | Align response/error/idempotency rules and retry retention |
| Migration | Shadow imports never broadcast | Test import, generation transfer and rollback without duplicate Create events |

Historical gate (subsequently satisfied for internal v2): do not rename fields to appear compatible without reference fixtures and state-transition tests. The required work was to record both actual contracts, propose a versioned mapping, resolve behavior differences, add positive/negative/replay fixtures and review the result. The initial CMS milestone wrote only internal version-1 intents. The later adapter preserves those rows and produces separately persisted v2 delivery bytes only for explicitly enrolled new posts. The original standalone prototype remains untouched. Combined tests verify the new internal module, not the legacy example.
