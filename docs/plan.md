# Phased plan after boundary correction

The user subsequently authorized a bounded local CMS implementation. The create/read/edit persistence milestone is now implemented; see [working slice](implementation.md) and [verification](validation.md). Internal Node/Fedify has been approved and is being implemented by its owning task. Live provisioning, credentials, deployment, commit and push remain outside this local scope.

| Gate / phase | Next work when separately authorized | Required evidence |
| --- | --- | --- |
| Runtime decision — resolved | Go CMS plus internal Node/Fedify in one content repository | [ADR 0009](adr/0009-internal-fedify-runtime.md); independent runtime credentials/schema boundaries remain |
| Internal contract — locally verified | Frozen v2 Go adapter and Node receiver pass the bounded lifecycle/retry corpus | [Adapter evidence](publishing-delivery.md); remote delivery/identity activation remain separate |
| 0 — Ghost/client evidence | Capture installed client/build handshakes, formats, errors and browser handoff against pinned Ghost reference | Sanitized fixture corpus; minimal supported client profile |
| 1 — CMS vertical slice | Go CMS/auth/storage, schema-qualified migrations, scoped JWT principals, create/read/edit, bounded rendering, media and review handoff | Client loops, atomic collision handling, rejected lossy input; no consumer-specific dependency |
| 2 — Reliability and reuse | Schedules, public projections, generic typed fields, namespace/role tests and backup/recovery | Two unrelated consumer configurations with different domains/schemas; restricted-role and migration-lock tests; restored data |
| 3 — Internal ActivityPub consolidation | Implement/organize selected runtime within `content`, reconcile publication contract, add durable snapshots/delivery | Contract tests, idempotency/reordering/crash tests, shadow imports silent, historical identity recovery before activation |
| 4 — Deployment profile | Configure Railway apps and ONE Supabase PostgreSQL project/database inside a NEW dedicated organization with separately owned schemas/roles | Separate deployment authorization, tested pooling/TLS, durable media, coordinated restore and bounded shared resource usage |
| 5 — Gradual migration | Transfer chosen publication cohorts with stable IDs/URLs and ownership fencing | Reviewed content/identity comparisons, one writer per article, rollback with higher generation |

The approved Node module has a separate owner task; CMS work must not change it. The prior standalone prototype and website runtime remain outside this task. Completing CMS editing does not complete the broader Phase 1 client workflow. The frozen v2 fixture mapping and Go adapter now pass combined lifecycle/retry tests; see [adapter outcome](cms-federation-adapter-review.md). Historical identity recovery, rendering acceptance and remote delivery remain separate gates.

Completed first slice: executable service, configured-schema migrations, publication-scoped create/read/edit storage, immutable revision metadata, optimistic concurrency, draft/published transitions, strict HTML intake and an atomic internal publication outbox. The measured iA Default-mode handshake, author attribution and native review page are now implemented; see the [dated client evidence](ia-writer-local-checkpoint.md). Next CMS acceptance work: remaining client modes/Ulysses, broader DTO/error corpus, required taxonomy/feature-image metadata and scheduling. Keep outbox transaction boundaries from the first publication write rather than adding nontransactional callbacks later.

Website-specific field registrations, imported books/feeds and frontend UI live in the website repository. They consume generic package contracts and do not determine the CMS schema or ActivityPub runtime. The current single-database profile does not imply a universal host/provider dependency.

Deferred scope remains full Ghost Admin, all Koenig cards/NQL/history versions, newsletters/billing/members, distributed infrastructure and broad parity without a corpus. Generic deployment portability is a requirement; full SaaS tenancy is not an initial product goal. Publication scoping and schema/role isolation are still mandatory wherever multiple publications share storage.

Superseded planning history is recorded in [ADRs 0001/0004/0008](adr/README.md). See [open questions](open-questions.md) for remaining decisions.

The [image storage slice](media.md) is implemented locally with filesystem/S3 adapters, migration 00005 and privacy/recovery tests. Remaining media gates are actual iA image behavior, provider acceptance/approved bucket and server key, backup/restore, and broader raster/feature-image support.
