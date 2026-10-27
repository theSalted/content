# ADR 0004: Original separate Fedify-service decision

Date: 2026-10-25. Status: **product/runtime boundary superseded by [ADR 0008](0008-package-and-database-boundaries.md)**.

## Historical decision

Following the federation audit, the user initially accepted a separate OSS Node/Fedify service alongside the Go CMS. The design retained durable publication snapshots, outbox delivery, stable identities and generation fencing. Legacy code could be replaced while identity/data were preserved.

## Current correction

The latest accepted target is one `content` package/repository containing CMS and ActivityPub internal modules; the website is a separate consumer. ADR 0008 temporarily reopened the runtime decision; the later [ADR 0009](0009-internal-fedify-runtime.md) selects internal Node/Fedify alongside Go. This does not restore the superseded separate-product decision.

The outbox, deduplication, immutable snapshots, shadow-import silence and one-writer/generation invariants remain. They are module contracts, not a mandate for an independently branded service or separate database. Owned schemas share the one database in the selected deployment.

## Remaining gate

The audit found the proposed CMS publishing envelope and existing ActivityPub prototype contract do not align. Their mapping and test corpus are unresolved; no interoperability claim follows from this ADR. Missing historic identity keys still blocks activation and never triggers replacements.

See [ActivityPub and migration](../federation-and-migration.md) and [publication contract gap](../../contracts/publication-consolidation.md).
