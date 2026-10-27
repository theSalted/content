# Open questions and blockers for later implementation

These do not block the architecture deliverable. They do block the corresponding compatibility or activation claim. The package/database boundaries are settled; the ActivityPub runtime is selected in ADR 0009; contract integration and production semantics below remain open.

| Priority | Question | How to resolve | Consequence |
| --- | --- | --- | --- |
| Resolved locally | Frozen v2 outbox mapping, durable acceptance and retry lifecycle | [Combined adapter evidence](publishing-delivery.md) | Remote delivery and historical identity activation remain separate gates |
| P0 | Which schema/role names, grants and stable migration-lock namespace will this deployment use? | Owned-schema design and restricted-role tests | One shared database is selected; do not provision roles/schemas yet |
| P0 | Which Ulysses/iA builds, OS versions and iA formatting modes does the owner use? | Record in Phase 0 on owned devices | Defines the actual support profile; Windows iA lacks direct blog publishing |
| P0 | What do Ulysses and the remaining iA modes require? | iA 8.0.9 Default handshake/audience/author/deep-link are measured in the [checkpoint](ia-writer-local-checkpoint.md); capture the rest | Do not generalize text-draft evidence to all modes or staff roles |
| P0 | Do these clients submit HTML, Lexical or legacy Mobiledoc, and which cards/nodes? | Capture representative documents and all offered modes | Determines whether a bounded Go renderer is enough |
| P0 | Which production operator, recovery and session deployment model should review use? | Native review is implemented and database-tested; [actual-app evidence](ia-writer-local-checkpoint.md) is tracked separately | Current scope is one configured operator/process; no production identity provisioned |
| P0 | What data-loss differences are acceptable for HTML cards, embeds and sanitization? | Show concrete before/after examples | Unsupported content must fail or stay quarantined, not silently disappear |
| P1 | What exact stale-update, validation, rate-limit and upload errors do the clients depend on? | Reference fixture capture including null/omitted fields | Freeze error envelopes and statuses after evidence |
| P1 | How do current clients handle Ghost 6 `limit=all` clamping and old version paths? | Use >100-item test data and observed headers | May require a declared legacy adapter; never silently claim full v5 parity |
| P1 | What minimum staff roles, multi-author editing and tag administration are required? | Owner workflow + role test matrix | Avoid an overpowered integration or premature team-management product |
| P1 | Are uploaded draft images allowed to be public by URL? | Explain Ghost-like media behavior and choose policy | Private media requires a tested extension and different preview semantics |
| P1 | Which public website consumes Content API first, and what URL pattern should it use? | Later frontend integration design | Consumer-owned routing stays separate; no personal domain or canonical URL default in core |
| P1 | What are the exact historic actor/key/object IDs and can old identity data be recovered? | Separate federation recovery/audit | Federation activation blocked while keys are unavailable; never generate replacements |
| P1 | How will internal modules preserve revision/generation ordering across the selected runtime boundary? | Consolidated contract and crash/replay fixtures | Same repository/database does not establish delivery correctness |
| P2 | What coordinated backup RPO/RTO, retention and partial-restore policy can the deployment maintain? | Whole-database/media restore drill and storage budget | CMS, ActivityPub and website share failure/recovery boundaries; no provider entitlement assumed |
| P2 | Is session/MFA parity actually needed? | Client traces and future admin-client requirements | Email verification would add SMTP operations; not assumed in initial scope |
| P2 | Which broader Ghost features justify implementation cost? | Explicit new requirements plus endpoint inventory | Full parity is a separate program, not an automatic consequence of client support |

No public support guide establishes arbitrary existing-post download/synchronization in either app. Ulysses documents updating its already published sheet; iA documents new drafts. Keep “manage the blog” claims aligned with what the selected clients actually provide.

The website owns concrete field registration and whether its Save action publishes immediately or stages a draft. Core enforces a configured policy; the mock interface does not settle production authentication, publication or cache invalidation.
