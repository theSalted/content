# ADR 0008: One reusable content package; consumer-owned integration

Date: 2026-10-25. Status: **accepted boundaries; runtime-pending portion superseded by ADR 0009**. Supersedes the product/runtime/deployment assumptions in ADRs 0001 and 0004. At this decision, CMS was architecture-only. Subsequent explicit approvals authorized [local CMS create/read/edit](../implementation.md) and internal Node/Fedify in this product. [ADR 0009](0009-internal-fedify-runtime.md) resolves the runtime question; the package/database boundary remains in force.

## Decision

One `content` repository/package contains CMS and ActivityPub internal modules. The website is a separate consumer of generic contracts. Third parties can use the package with their own domains, identity, frontend, importers and hosting. Standard PostgreSQL and configurable schema ownership are the storage contract; no provider SDK or consumer-specific data model is required.

For this deployment, applications run on Railway and share ONE Supabase PostgreSQL database. Example owned schemas are `content_cms`, `content_activitypub` and a website-owned schema. These names are configuration examples, not fixed global names. Restrict runtime and migration roles, schema-qualify SQL, and namespace migration ledgers/advisory locks by installation/module/schema. Shared schemas are not independent database failure domains: capacity, connections, outages and full recovery affect all modules.

At the time of this ADR, ActivityPub runtime remained open between all Go and internal Node/Fedify. That historical pending decision is preserved here and subsequently resolved by ADR 0009. This boundary correction itself moved no runtime code.

## Consumer boundary

The core exposes generic publication, media, identity, typed-field and persistence interfaces. The consuming application owns its field registrations, import source-of-truth policy, feed/book data, frontend and presentation. ADRs 0006/0007 are relocated to website-owned docs; their concrete keys and external-source choices do not define the package. The [decision index](README.md) records their corrected repository provenance; the initial placement in an unrelated repository is superseded.

## Contract and release gate

The existing CMS publication-event example is a proposal, not the ActivityPub prototype's implemented contract. The audit reports a mismatch. Consolidation must record a mapping for identity, source, revision/generation, operation/visibility, snapshot body, authorization, idempotency and acknowledgment semantics, then prove it through fixtures. Preserve durable outbox behavior and historic identity keys while correcting that interface. Package co-location alone is not compatibility.

## Superseded history

The earlier plan used a Go monolith, separate per-site databases and a low-cost single-server option; it subsequently selected a separate Node/Fedify service/product. Those decisions are retained as superseded history in ADRs 0001/0004. This boundary approval replaced them and reopened the runtime choice at that time. Later runtime and local implementation approvals are recorded separately; no resource creation or production change follows from this ADR.
