# Working on content

- Keep this a reusable Go/PostgreSQL CMS with an internal Node/Fedify module. Websites, personal domains, provider credentials and consumer-specific data belong outside core.
- Preserve existing user changes. Read module contracts before changing the CMS/ActivityPub boundary. Do not replace a runtime implementation or overwrite another worktree's edits as part of an unrelated change.
- Use standard PostgreSQL, configurable safely quoted schemas, schema-qualified migrations and namespaced locks. Runtime roles must never own migrations or fall back to migration credentials.
- Keep HTML source fidelity, optimistic concurrency, explicit publication actions and draft/media privacy. Never silently discard unsupported markup. Use Tiptap rather than a homemade rich-text engine; rebuild the embedded asset when its source changes.
- Keep moderation in ActivityPub, not the Go CMS UI. Durable internal acceptance is not proof of remote delivery. Preserve immutable payload/idempotency contracts and stable identities. Startup/import must not automatically broadcast historical posts; on-follow replay defaults off.
- Never regenerate signing keys because a database is unavailable. Production content, credentials, exports, local handoffs and follower data must never enter Git or logs. Fixtures must be synthetic.
- Run focused Go tests, vet and applicable Node/contract tests. Database/container tests are opt-in, use disposable localhost storage only and must never read production credentials. Distinguish skipped tests and native-client limits in reports.
- Keep applied migrations immutable; add forward migrations. Changes to runtime permissions, service configuration, deployments and external resources need appropriate task authorization. Documentation is not authorization.
- Preserve third-party notices. Do not select or change this project's license without the owner's decision.
