# Verified local database-role boundary

The next publishing milestone requires the [explicit claim/schedule grants](publishing-management.md#exact-additional-runtime-permissions).
Its integration branch now passes schema/data/concurrency and least-privilege
tests using transient local roles, including per-grant startup refusal and denied
identity mutation/DDL/cross-schema access. No new live grant has been applied or
validated; production approval remains separate.

The isolation tests, extended on 2026-10-26, uses PostgreSQL 18.6, a dedicated disposable database and three transient roles: two separate migration owners and one CMS runtime. They have no superuser, role/database creation, inheritance or RLS-bypass attributes. The schemas and roles are test fixtures, not deployment changes or fixed package names. See `storage/postgres/isolation_test.go`.

## Minimum grants exercised

| Principal | Allowed | Denied in the test |
| --- | --- | --- |
| CMS migration owner | CONNECT; ownership of its configured CMS schema; numbered migrations and installation binding | Another owner's CMS schema |
| CMS runtime | CONNECT; USAGE on its CMS schema; SELECT installation/posts/post_revisions; INSERT posts/post_revisions/outbox_events; UPDATE only posts.title/slug/status/visibility/revision/updated_at/published_at | Schema/table/temp DDL; ALTER/DROP/TRUNCATE; migration ledger reads/writes; installation updates; revision update/deletion; event update; immutable post ID/UUID/publication/created_at updates; SET ROLE to migration owner; other module/consumer reads/writes |
| Other migration owner | Its own schema and separate installation | No runtime grants are inherited from it |

The test's new database revokes database-level PUBLIC privileges, grants CONNECT explicitly, and revokes PUBLIC access to its public schema. PostgreSQL normally grants TEMPORARY through PUBLIC; revoking it only from an individual role does not remove an inherited PUBLIC grant. Consequently, denying temporary DDL needs the database-level PUBLIC grant handled as part of a reviewed shared-database policy. No ACL in an existing database is changed by the test. [PostgreSQL privileges](https://www.postgresql.org/docs/18/ddl-priv.html).

Each runtime process uses a role that owns no schema or objects and has no membership in a migration owner. Serving never migrates. `Migrate` with the restricted runtime role fails with PostgreSQL insufficient_privilege; the non-superuser migration owner succeeds. The application does not provision or automatically enforce deployment grants: production must apply and verify equivalent grants under its separately authorized setup.

Queries still reach the intended CMS schema when the runtime's search_path points at an inaccessible consumer schema and public. A schema-qualified read does not bypass privileges; direct reads/writes of the neighboring schemas fail. Schema/catalog names are not promised secret. [PostgreSQL schemas](https://www.postgresql.org/docs/18/ddl-schemas.html).

## Publication isolation and its precise limit

Two authenticated API instances share the same restricted runtime role but bind different publication IDs and distinct API keys. Tests prove separate browse/count/slug/ID results, rejection of another publication's keys, rejection of publication fields in write payloads, unchanged row counts after denied writes, and correct revision joins even when external post IDs collide across publications. Both valid publication-scoped create/read/edit paths continue working under minimum grants.

This is application-level authorization backed by explicitly scoped SQL and compound keys. The runtime database role can access CMS rows for all publications in its schema; there is no PostgreSQL RLS policy or per-publication database role in this milestone. Treating untrusted SQL/database credentials as a per-publication boundary would require a separate isolation design. No such stronger claim is made here.

## Migration lock fix

The new test exposed a no-op migration gap: goose skips its internal lock when there is no pending SQL, but our wrapper still checks/binds installation metadata. The wrapper now holds a dedicated direct PostgreSQL session across all checks, goose operations and installation binding. It acquires two advisory locks in a fixed order:

1. CMS/schema ownership guard: one physical schema cannot be claimed concurrently by different installation IDs.
2. Installation/CMS/schema migration lock: stable across release versions, with the existing `MigrationLockID` namespace retained.

Both lock domains use SHA-256-derived signed 64-bit IDs and fixed domain prefixes. Cleanup releases them in reverse order even after request cancellation; closing the per-operation database pool supplies a final session-lifetime boundary. Goose's nested session lock is disabled so a second connection cannot deadlock against the wrapper's own lock. SQL migration execution/version tracking still uses goose.

Real database tests hold a namespace lock, show that a second migration times out, verify the ownership guard is released, then successfully migrate after release. Another schema's migration proceeds while the first remains locked. Concurrent first claims return exactly one success and one installation mismatch. Advisory locks require cooperation and are not an authorization mechanism or protection against shared-database resource contention. Use direct connections, not transaction-pooling, for migrations. [PostgreSQL advisory locks](https://www.postgresql.org/docs/18/explicit-locking.html#ADVISORY-LOCKS).

## Reproduce and remaining checks

`sh scripts/test-postgres.sh` uses a cached PostgreSQL image, transient trust-auth roles and loopback-only tmpfs storage. The role test additionally requires `CONTENT_TEST_ALLOW_ROLE_CREATE=1`; the harness sets it only for its disposable server. It creates/removes its own random database and roles, and never inspects or reuses live credentials. A manually supplied schema-test DSN does not opt into role/database creation by itself.

Verified: PostgreSQL 18.6 and the documented minimal grants. Still unverified: provider-created role memberships/default privileges, production connection pooling/TLS, PostgreSQL 17, operational restore, and privileges needed by future delete/job features. Those future features must extend the grants and regression tests explicitly.

## Internal publishing worker

The later delivery milestone adds a distinct non-owner worker role and column-level grants, tested against real PostgreSQL. It can read immutable CMS intents and update delivery leases/outcomes and stream cursors, but cannot read posts/revisions, change payload bytes/source identity, perform DDL or access another module/consumer schema. The API role only gains INSERT on stream enrollment when that feature is explicitly enabled. See [exact grants and tests](publishing-delivery.md). No deployed grants were changed.

## Foreign-schema audit correction

The audit exposed a migration boundary bug: an occupied namespace with no CMS installation table was previously treated as fresh. Goose could create its ledger and CMS tables beside foreign objects; an AP-shaped schema with a matching installation ID failed only after a Goose ledger had been created. Disposable regression tests reproduced both behaviors before the fix.

The migrator now performs read-only namespace inspection **before constructing the Goose provider**, under the existing CMS/schema ownership and installation/module/schema locks. A fresh namespace must be empty, including functions, types and sequences. An occupied namespace must have the recognized CMS base tables/column types, a complete known forward Goose history and exactly one matching installation binding. Foreign tables, mixed AP/CMS objects, standalone types/functions, missing/ambiguous bindings and unknown migration histories are rejected before any DDL or ledger write.

Forward migration 00004 adds `installation.module_id = 'cms'` with a non-null check constraint. Migration and runtime startup validate both module and installation IDs. Earlier migrations remain immutable. Existing successful CMS versions 1, 2 and 3 are recognized through their ledger and known layout, then acquire the explicit module binding during the forward upgrade. The catalog/ledger recognition policy must be reviewed alongside future CMS migrations; it must not silently accept unknown layouts. This is a configuration/adoption guard, not authentication against a schema owner deliberately forging CMS metadata.

Real PostgreSQL regressions snapshot schema-local object inventories, table contents and sequence state before/after rejection. Unrelated sentinel tables, function-only/enum-only/sequence-only schemas and synthetic AP metadata remain unchanged, with no new Goose objects. Wrong module/installation, missing binding, mixed modules and inconsistent module/ledger states also remain unchanged. Version 1/2/3 upgrades, repeat migrations, restricted runtime denial, namespaced locks and the combined Go/Fedify tests pass. Legacy post/revision/outbox bytes remain intact; upgrades do not enroll historical content.

An incomplete or manually modified namespace without a provable installation binding requires an explicit operator repair; the migrator will not claim it merely because its table names look familiar. Advisory locks serialize cooperating CMS migrators; schema ownership and separate roles remain the boundary against other actors performing DDL. No live schema, role or data was changed by this correction.
