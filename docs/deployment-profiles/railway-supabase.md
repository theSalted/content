# Current deployment profile: Railway apps and one PostgreSQL database

Accepted target for this user's deployment; not a reusable package requirement and not provisioned by this correction.

Applications run on Railway. Create a **NEW Supabase organization dedicated to this project** when deployment is authorized; do not reuse an existing organization. The user prefers the Supabase CLI. One PostgreSQL project/database within that organization hosts separately owned CMS, ActivityPub and website schemas/roles. Example names `content_cms`, `content_activitypub`, `website_content` are configuration placeholders. Website import/source-specific data stays in its owned schema; it is not a package table model. The approved internal Node/Fedify module runs alongside the Go CMS; this does not split the product or require a second database.

Use standard PostgreSQL connections and SQL. No Railway/Supabase SDK, Supabase Auth/Storage/Functions, public API exposure of owned tables or privileged browser key is required. Choose secrets, network/TLS settings, schema privileges and migration connections in a later approved deployment; do not create them now. Paid plans, billing and new credentials require specific approval. No organization, project or credential has been created by this local work.

Apply [operations](../operations.md): separate runtime/migration roles, schema-qualified SQL and migration ledgers, installation/module/schema lock namespacing, bounded pools/workers, coordinated backup/restore. Schemas share CPU, memory, disk, connections, outages and full-database recovery. A schema-only restore cannot be treated as an independent consistent module rollback when publication identities/events cross schemas.

Local Docker PostgreSQL is a development/test option only. Other consumers can choose different PostgreSQL hosts, schema names, storage adapters and domains without changing package contracts. The prior VPS/per-publication database proposal is superseded by [ADR 0008](../adr/0008-package-and-database-boundaries.md).
