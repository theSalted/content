# Decision index

| ADR | Current status |
| --- | --- |
| [0001](0001-go-postgres-monolith.md) | Original monolith/per-database deployment partially superseded by 0008 |
| [0002](0002-measured-ghost-profile.md) | Proposed measured Ghost/client profiles retained |
| [0003](0003-preserve-source-bound-rendering.md) | Proposed bounded CMS rendering retained; not an ActivityPub runtime decision |
| [0004](0004-durable-outbox-federation.md) | Separate product/Node selection superseded by 0008; durability/identity invariants retained |
| [0005](0005-small-review-ui.md) | Accepted native authenticated review; measured iA fragment handoff |
| 0006 | Relocated to the consuming website's `docs/content-integration/adr/0006-readwise-bookshelf-mirror.md`; not a core decision |
| 0007 | Relocated to the consuming website's `docs/content-integration/adr/0007-reusable-content-fields.md`; generic interface extracted to [content fields](../content-fields.md) |
| [0008](0008-package-and-database-boundaries.md) | Current accepted package/consumer/database boundary; runtime resolved by 0009 |
| [0009](0009-internal-fedify-runtime.md) | Accepted internal Node/Fedify alongside Go; no production activation |
| [0010](0010-review-editor-engine.md) | Accepted Tiptap/ProseMirror review editor; HTML source stays authoritative |

The 0006/0007 identifiers are intentionally reserved after relocation. Original text is retained in website-owned history; there is no second active copy in core. These external file locations identify the current consumer's docs, not a required website structure for third-party users.
