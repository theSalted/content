# Local Mastodon gate: completed

The earlier Mac-loopback networking blocker is resolved. The Node fixture endpoint and allowlisted HTTPS proxy now run in the same internal Docker network as the real Mastodon peer. See [MASTODON-VALIDATION.md](MASTODON-VALIDATION.md) for passed assertions, the edit-timestamp fix, precise limits and image pins.

Run from this module with the existing Docker engine and cached pinned images:

```sh
CONTENT_ACTIVITYPUB_MASTODON_TEST=1 \
  /path/to/node24 --test test/mastodon-peer.test.ts
```

No deployment or external account is required. The explicit opt-in creates and cleans its own PostgreSQL, Redis, Node fixture, Mastodon Puma and Sidekiq containers. The normal fast suite skips it. Docker orchestration remains on the host; no container receives the Docker socket. Module files mount read-only. The harness was verified on this Mac's OrbStack engine, where the host can reach its own internal-container IPs. Other Docker desktop backends may require adapting the test control connection.

## Superseded setup attempt

The first attempt kept the Node endpoint/proxy on the Mac. Internal Docker networking could not resolve/reach that loopback proxy, even with host-gateway mapping; application assertions did not execute. The revised topology removed that dependency without changing host DNS, trust settings, production SSRF checks or opening external egress. The initial account fixture also needed a complete confirmed/approved Mastodon user, rather than an orphan Account record; its synthetic `.invalid` email domain is allowed only in the one fixture-creation runner.

## Resource boundary

Five containers use aggregate memory limits of 3968 MiB: Node 768, Puma 1536, Sidekiq 1024, PostgreSQL 512 and Redis 128. Tmpfs caps total 448 MiB for PostgreSQL, Redis and the two peer media mounts. Cached public images occupy roughly 2.3 GB uncompressed in total. Temporary identities, TLS material, application values and database state are removed after the run; public images remain cached. No SMTP delivery, Elasticsearch, streaming service, object storage or paid service is needed for this bounded server-to-server gate.

Next release work is separately authorized staging: real public DNS/TLS and Railway ingress, Supabase connection/role budgets in the new organization, backup/restore, approved identity activation and a full CMS-to-peer run. Local success does not authorize provisioning or production cutover.
