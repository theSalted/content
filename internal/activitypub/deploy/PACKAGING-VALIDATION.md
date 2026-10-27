# Local AP packaging validation — 2026-10-26

This validates preparation in `content/internal/activitypub`, not a Railway or Supabase deployment. No credentials, persistent keys, accounts, cloud services, public posts or production data were created/changed. Synthetic key material existed only in the owned test process and disposable tmpfs PostgreSQL. No old identity was loaded.

## Built-image gate

`NODE_BIN=<node24> sh scripts/test-packaging.sh` builds `runtime` and `migrator` targets and runs six scenarios (seven results including the wrapper). All pass:

- The dedicated operator image applies migrations twice without data/identity generation. A runtime-role migration attempt fails. The runtime filesystem has no migration SQL, migrator/import CLI, tests or TypeScript compiler and runs non-root.
- Both disabled API and worker images become explicitly disabled-ready without any database, identity or publisher configuration. SIGTERM exits successfully.
- Enabled API startup with a missing identity exits nonzero and leaves the identity table empty.
- With an ephemeral synthetic identity, API readiness succeeds before the worker starts. The v2 publishing event is durably accepted; exact retry is a duplicate and the canonical actor is served.
- The actual worker entrypoint starts both transport and relay polls; its independent health server becomes ready. Combined runtime connections remain within the configured cap. SIGTERM drains and exits successfully.
- Stopping only the owned PostgreSQL container leaves API liveness healthy, makes readiness fail within its HTTP budget, and allows successful API shutdown.

All containers use an isolated internal Docker network, no published ports and an owned tmpfs PostgreSQL database. Runtime containers were read-only, non-root, capabilities dropped, with no Docker socket or mounted secrets. The test waited for PostgreSQL TCP readiness (the first harness attempt checked its temporary Unix socket too early; that harness race was fixed). Containers and network are removed on exit. Built images/dependency layers stay in the local cache. No cloud tooling was invoked.

## Measurements and provisional allocation

Linux ARM64, Node 24.19.0, PostgreSQL 18.6; one synthetic publish plus exact retry, no followers/delivery load in this packaging sample. Actual federation behavior is covered separately by the controlled-peer and real Mastodon suites.

| Sample | Main process RSS | Container cgroup memory peak |
| --- | ---: | ---: |
| API after one publish/retry and actor read | 137,440 KiB (134.2 MiB) | 88,641,536 bytes (84.5 MiB) |
| Worker after idle startup | 133,096 KiB (130.0 MiB) | 85,135,360 bytes (81.2 MiB) |

RSS includes resident mappings/shared pages; cgroup accounting is different and the probe also runs briefly inside the container. These are sample observations, not a sustained/production peak. Each runtime passed under an enforced **256 MiB memory limit and one CPU quota**. No CPU utilization benchmark was made. A candidate initial staging ceiling is 256 MiB per AP process (512 MiB combined) with one replica each; this is an inference requiring public staging burst/backlog measurement and headroom review. CMS, its publisher, website and shared database resources are additional. Earlier Mastodon harness limits included an entire peer stack and are not AP production requirements.

Three runtime database sessions were observed against **10 configured maximum** (5 API + 5 worker). A simultaneous old/new rollout can require 20 AP sessions; the separate one-shot migrator caps at 2. Do not provision until the combined CMS/website/provider budget and session pool limits are checked.

This is not a bill estimate. No new paid Railway service spending was approved or initiated. Exact service/region/resources, hosting approval and observed resource billing remain with the parent deployment decision.

## Regression evidence

- TypeScript: passed.
- Fast suite: **69 passed, five explicit opt-in skips**.
- Independent HTTP signatures and real PostgreSQL: **26 passed**.
- Publishing HTTP oracle: **one passed**.
- Built-image packaging suite: **seven passed**.
- Real Mastodon 4.5.8 lifecycle: **six results passed**, including actual HTTPS, Follow/Accept/Undo, Create/retry/duplicates, Update and Delete. Final follower and transport queue counts were zero.

The extra regression attempts initially hit local fixture constraints (sandboxed loopback binding and a staging-directory dependency symlink unavailable inside Docker). Both were resolved by granting only local test execution and rerunning from the actual module dependency directory. There is no remaining permission/resource blocker.

Final local Linux ARM64 image configuration IDs: runtime `sha256:f7b542d305d1d2eeeff0cddf4c7b616cf711e1dd94cbca5c5dae5760d210f361`; migrator `sha256:8999ebda040355e0a35fa3c40d894f57b126ccb133b0222e22f8ed8efd2597d9`. These identify the tested local image configurations; no image was pushed. Build provenance/index digests can differ between builds.

No publishing schema, profile, shared fixture hashes, SQL migration content or Go files changed. New runtime behavior is process health/drain, readiness separation, optional verified database CA handling, explicit connection timeouts and worker-local progress. No SSRF/signature policy was relaxed.

Remaining gates are [the public staging checklist](RAILWAY-STAGING.md): approved paid-service resources and synthetic staging identity, actual Supabase TLS/session/role/backup behavior, public proxy signature preservation, a complete deployed CMS-to-peer run and shared recovery drill. Prepared manifest is review-only; it is not an applied Railway IaC configuration.
