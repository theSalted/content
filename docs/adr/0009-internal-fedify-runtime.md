# ADR 0009: Internal Node/Fedify alongside the Go CMS

Date: 2026-10-26. Status: accepted by explicit user approval. Resolves the runtime-pending portion of ADR 0008; retains one reusable content repository/product, standard PostgreSQL and consumer independence.

CMS remains Go. ActivityPub is an internal Node/Fedify module with its own process, migrations, restricted runtime role and owned schema. It is not a separate product or database requirement. Its implementation task owns `internal/activitypub`; CMS work does not copy, edit or delete the standalone prototype or identity material.

The durable CMS outbox communicates through an authenticated versioned internal API. The proposed v2 contract lives in `internal/activitypub/contracts`. Runtime selection does not establish contract compatibility. Freeze fixtures and test stable CMS identity, withdrawal/republish instances, revision/ownership fences, authorization, duplicate/retry behavior and durable acknowledgment before wiring delivery. No direct cross-module table writes.

The current deployment profile remains Railway apps and one Supabase PostgreSQL project/database, in a **new dedicated organization** when separately provisioned. The user prefers the CLI. This records a future deployment requirement; no organization, credentials, paid plan or production activation is authorized by this runtime ADR.

Superseded history: a separate federation product was first proposed; ADR 0008 consolidated the product boundary and reopened the language choice; this decision selects an internal multi-runtime implementation. Preserve identity/data, not obsolete code for its own sake. Missing historical keys still block activation.
