# AP deployment gate within content

Preparation only; no Railway service, project link, remote repository, deployment, paid resource, Supabase change or persistent identity was created. The Go CMS and internal Fedify module remain one product/repository. Use the ONE shared PostgreSQL project in the approved NEW Supabase organization, with separately owned AP/CMS/website schemas and runtime roles. Website stays a consumer. The CMS remains usable when AP is disabled.

## Reproducible packaging and parent handoff

Build context is `content/internal/activitypub`. `Dockerfile` pins the Node 24.19.0 OCI index and uses the committed npm lockfile with production-only dependencies and install scripts disabled. Both API and worker run as the non-root `node` user; no writable volume is needed. The build-context allowlist excludes dotenv, local dependencies and fixtures. Runtime has no migration SQL, migrator entrypoint or identity-import CLI. The explicit `migrator` target contains only its required source/SQL and dependencies; it has no server or identity-import entrypoint. No environment credentials are build arguments.

From this directory:

```sh
docker build --pull=false --target runtime -t content-ap-prep:local .
docker build --pull=false --target migrator -t content-ap-migrator-prep:local .
NODE_BIN=/path/to/node24 sh scripts/test-packaging.sh
```

These are local commands. Later release tooling should build once, record the immutable resulting image digest, and run that same runtime image for both entrypoints. Platform architecture and registry publication are later gates. The completed local container tests use Linux ARM64; an amd64 image is not yet exercised.

[railway-service-plan.json](railway-service-plan.json) is a review manifest, **not an executable Railway configuration**. It deliberately has no project/service IDs, domain values, credentials, resource creation or automatic deploy hook. Railway currently directs new configuration toward project-level IaC. The parent/Go owner should fold these two process settings into the single approved content project configuration after service cost approval, rather than applying an AP-only project plan. No `.railway` linking/init/apply was run here.

Parent/Go handoff: source root `/internal/activitypub`, default Dockerfile, API `node src/main.ts`, worker `node src/worker-main.ts`, both listen on injected PORT; set HOST to a dual-stack bind address when Railway private IPv6 routing requires it. CMS adapter uses API private networking for `/internal/v2/events` with its existing scoped bearer token and publication binding. No publishing wire/schema/fixture changes. The API does not require worker readiness and the CMS does not require AP readiness. Do not expose `/internal/*` through the canonical public proxy; application bearer checks remain defense in depth. No Go/root deployment files were edited.

## Health, drain and delivery

| Process/path | Meaning |
| --- | --- |
| API `/health/live` | Event loop can answer; no DB check. |
| API `/health/ready` | Startup already verified scope/role/imported identity; DB currently answers. Independent of worker. |
| Worker `/health/live` | Health server/event loop can answer; no DB check. |
| Worker `/health/ready` | This process completed both relay and transport polls within 120 seconds and DB answers. Another replica cannot mask its failure. |
| Disabled API/worker `/health/ready` | HTTP 200 with explicit `disabled`; no DB/token/key is needed and no delivery occurs. Verify the body before claiming AP activation. |
| API `/internal/v2/status` | Authenticated queue/failure counts and `relay_recent`; neither a recent relay heartbeat nor 202 proves external delivery. |

Health responses are uncached; HEAD is supported and other methods are rejected. Readiness has a two-second response deadline with one outstanding DB probe per handler. An HTTP timeout does not cancel the underlying SQL; bounded driver/statement timeouts finish it. Configure Railway rollout health on `/health/ready`. Its documented check is deployment-time, not continuous monitoring: separately monitor stale worker progress, queue oldest age/dead jobs and delivery failures before release. Queue oldest-age telemetry is an operational follow-up, not yet a built-in endpoint.

SIGTERM stops API acceptance and aborts worker polling. In-flight work gets a 20-second application grace period by default (configurable 1–60 seconds); a forced exit leaves uncommitted PostgreSQL work available for retry. Set provider draining time longer than the application deadline (proposed 30 seconds), and keep one worker replica initially. Overlapping deployments can double the connection budget. Jobs/receipts are durable; retries may repeat a remote request with the SAME activity ID. Never clear queues to make a deploy green.

## Connections, schemas and migration

Runtime DATABASE_POOL_SIZE is bounded 5–20, default 5 per process; one API plus one worker has a configured AP maximum of 10. A simultaneous old/new rollout can need 20, plus at most 2 for the separate migrator. Add actual Go CMS, Go publishing worker, website, operator and provider reserves to this one database budget before approving replicas. The driver opens connections lazily. Do not treat the pooler's client limit as the database's connection budget.

Use a verified direct or session-mode PostgreSQL connection. Transaction-mode pooling is not validated for this worker: jobs hold transactions across handlers and the driver uses prepared statements. Supabase documents session mode for persistent applications where direct IPv6 is unavailable. Verify the actual role-specific connection and pool limits after provisioning; this package makes no plan-limit assumption. Runtime connection/statement/lock/idle-transaction timeouts are 10/15/10/120 seconds. The one-shot migrator uses cap 2 and 60-second statements with the same 10-second lock wait; investigate timeouts rather than silently raising limits.

For hosted connections require certificate and hostname verification. Use a verify-full DSN with trusted system roots, or DATABASE_SSL_CA containing the reviewed public CA PEM; the latter explicitly enforces rejectUnauthorized. Use ACTIVITYPUB_MIGRATION_SSL_CA only for the operator connection. Never set rejectUnauthorized false or use an unverified SSL mode. Provider CA/hostname trust remains an unexecuted staging gate. Local tmpfs tests intentionally use plaintext only within their internal Docker network.

The operator must first establish the owned schema and restricted runtime role/publication binding using the existing [privilege contract](../README.md). The migrator creates neither databases, schemas, roles nor keys. Run the separately built migrator as a bounded one-shot job with ONLY its migration connection/scope/optional CA environment. Do not configure migration as an application pre-deploy command: Railway pre-deploy receives application variables, which would place owner credentials in the normal service's environment. Runtime receives only the narrow runtime connection. Identity initialization/import is a separate explicitly approved action and is not packaged into this gate.

Migration ledger/checksums and installation/schema advisory locks remain unchanged. Apply additive migrations once before runtime rollout. Runtime-role migration attempts must fail. Do not automate reverse SQL migrations; restoring one schema independently can invalidate CMS/AP outbox receipts and object history.

[environment-names.txt](environment-names.txt) records names only. No configuration values or credentials are supplied. Enabled startup still fails closed on missing/mismatched keys; disabled startup is not permission to initialize an actor.

## Public staging smoke test — requires later authorization

1. Record approval for the exact Railway service resources, stage hostname and synthetic identity plan. Confirm dedicated staging publication/schema/roles in the same shared DB do not point at production state. Record release digests, schema checksums, backup/restore evidence and the previous known-good release. Keep Go enrollment/delivery disabled.
2. Verify outbound TLS to the actual Supabase endpoint and narrow role; run owner migration once. Confirm protected-state writes, role elevation, other publication/schema reads and runtime DDL fail. Confirm connection totals during overlapping startup stay within the shared budget.
3. Start API/worker disabled first; inspect explicit disabled health responses. After separately approved synthetic identity initialization, set matching scope/fingerprint and enable only staging. Require API and both worker loops ready. Assert wrong/missing fingerprint fails; never substitute or regenerate keys to fix startup.
4. Verify public DNS, certificate chain/SAN/renewal route, canonical HTTPS origin and path preservation. Fetch WebFinger, actor, public key, outbox and a synthetic object through the actual external proxy. Reject account mismatch, stray preview-host actor minting, unwanted redirects and publicly reachable `/internal/*`. Verify Accept/Vary caching and uncached inbox, tombstone/error/health paths.
5. Through the actual public TLS/proxy path, repeat controlled RFC 9421 and legacy signature fixtures: valid Follow/Undo accepted; tampered body/content-digest, wrong method/target URI, wrong owner/key, missing required coverage, stale/expired timestamps rejected without queue insertion. Preserve exact request body, path/query, Digest/Content-Digest, Signature/Signature-Input, Date and relevant content headers. Preserve the canonical authority across proxies; never trust arbitrary forwarded-host headers. No redirect/re-serialization/body decompression may alter signed bytes.
6. Run a single complete Go CMS → immutable v2 event → AP durable receipt → worker → authorized disposable real peer lifecycle. Assert Follow/Accept, Create, same-ID/body retry after actual 503, duplicate handling, equal-time Update, withdrawal/Delete, public 410 Tombstone, republish with a new object instance, Undo and no unintended historical enrollment. Record peer version and Article title/link projection limits.
7. Restart a worker mid-flight and verify persisted job recovery/at-least-once behavior; exercise DB outage, readiness failure, recovery and provider SIGTERM drain. Observe public errors and pool/CPU/RSS/queue lag through representative bursts. No claim of production capacity from the local single-post sample.

## Rollback checklist

- Stop Go publisher enrollment/delivery; pause/stop the AP worker after bounded drain. Preserve API reads/tombstones where safe. Record pending receipts/intents/transport failures without dumping private payloads or keys. FEDERATION_ENABLED=false also disables actor serving; it is a deliberate emergency shutdown, not a transparent rollback.
- Route back to a recorded compatible API/worker image/config pair. Preserve canonical actor/key IDs, object reservations, tombstones and key fingerprint. Retain additive schema migrations unless a reviewed compatibility check says the old image cannot run them. Verify health bodies, canonical discovery, one existing synthetic object and pending-job ordering before resuming.
- Resume the same persisted events/activities; never regenerate event/activity IDs, empty queues, reset schema bindings or replace a database to suppress failures. Remote delivery may have happened before acknowledgement; expect safe duplicates. A rollback cannot unsend an already public activity.
- If data restore is necessary, stop all writers and coordinate one consistent shared-database/media recovery across CMS/AP/website. Reconcile outbox receipts, keys and remote observations before re-enabling. AP schema-only restore is not an independent rollback.
- Verify continuous delivery monitoring after resumption, then record what was restored and all remaining external effects. Do not describe a healthy process as successful federation.

Official guidance consulted: [Railway Dockerfiles](https://docs.railway.com/builds/dockerfiles), [monorepo roots](https://docs.railway.com/deployments/monorepo), [current IaC](https://docs.railway.com/infrastructure-as-code), [health checks](https://docs.railway.com/deployments/healthchecks), [teardown/draining](https://docs.railway.com/deployments/deployment-teardown), [pre-deploy environment](https://docs.railway.com/deployments/pre-deploy-command), [Supabase connection modes](https://supabase.com/docs/guides/database/connecting-to-postgres), [shared connection budgeting](https://supabase.com/docs/guides/database/connection-management). These establish platform behavior; no platform deployment was tested here.

### Public and private publishing listeners

Set `INTERNAL_PORT=3001` on the API and expose only `PORT=3000` at public ingress. The public listener returns 404 for `/internal/*`; the private listener serves only authenticated internal routes and health. The worker does not expose publishing. Go uses the API private hostname on port 3001. Do not create a public domain or TCP proxy for this port. Omitting `INTERNAL_PORT` preserves a single listener for entirely private deployments. The process rejects equal public/private ports and drains both listeners on SIGTERM.
