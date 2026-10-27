# Real Mastodon verification — 2026-10-26

**Passed:** five integration scenarios, six results including the enclosing test. This ran the official Mastodon 4.5.8 Ruby/Puma application and Sidekiq workers, Redis and actual PostgreSQL 18.6. The content endpoint used Node 24.19.0 and Fedify 2.4.5. All identities, users, posts and databases were disposable synthetic fixtures.

Reproduce with `CONTENT_ACTIVITYPUB_MASTODON_TEST=1 /path/to/node24 --test test/mastodon-peer.test.ts`. The normal suite skips this explicit opt-in. [Setup and resource details](MASTODON-LOCAL-PLAN.md).

## Observed behavior

| Scenario | Verified result |
| --- | --- |
| HTTPS boundary | An actual request from the Mastodon image traverses the container-local CONNECT proxy and verifies the synthetic certificate. An untrusted CA fails; an unlisted origin is refused. No system trust settings change. |
| Discovery and relationship | Mastodon's FetchRemoteActorService fetches the actor and performs the WebFinger round trip. Its FollowService/Sidekiq sends signed Follow over HTTPS. Content durably processes it and sends an RFC 9421 Accept, which Mastodon accepts and persists as one relationship. Replayed signed Follow produces no second Accept. |
| Publication, retry and edit | The HTTPS ingress proxy returns actual 503 responses for initial Create attempts. The content queue retains the job; retry succeeds. Across failed/retried/replayed deliveries, the activity ID and body hash stay identical. Mastodon persists one status. A higher-revision Update with the same source timestamp changes that status after the projection-clock fix. |
| Delete | Withdrawal sends Delete; Mastodon removes its status and the public status API returns 404. The original content object URL returns 410 Tombstone. |
| Undo | Mastodon's UnfollowService/Sidekiq sends signed Undo. Content removes the follower. Replaying Undo is harmless. Final content follower count and transport queue depth are zero. |

Observed successful signature formats in this run: RFC 9421 Accept and Create replay; Cavage Create retry, Update and Delete. The injected transient failure caused Fedify's negotiation state to use Cavage for later deliveries. Both formats were accepted by the real peer; do not misreport every delivered activity as modern-signed. Independent tests separately cover RFC 9421 Update/Delete verification and malformed-signature rejection.

## Article projection and edit fix

Mastodon's public status API returned the changed title and article URL, with its normal HTML formatting. It did not include the full Article body. The content object's public ActivityPub representation retained the complete sanitized body. This is measured Article projection behavior, not a browser screenshot or full-body rendering claim.

The original store copied `occurred_at` directly into every object's `updated`. The publishing contract orders events by revision and permits equal timestamps; Mastodon treats an unchanged edit timestamp as an implicit update and does not replace the displayed text. A focused store regression reproduced this unchanged timestamp before the fix.

`src/projection-time.ts` now computes the public edit timestamp as the later of the source event time and the previous public edit time plus one millisecond. The store applies this under its existing content transaction/lock. Original source event bytes/hash, receipt ordering, activity publication timestamp and original object publication time are preserved. Exact retries reuse the persisted object/activity rather than advancing the clock again. Tests cover equal, backward, submillisecond and newer event times. The real peer applies an equal-source-time title edit with public `updated` advancing from `2026-10-25T12:00:00Z` to `2026-10-25T12:00:00.001Z`.

This is an internal projection fix. Publishing wire shape, schema version, shared fixture hashes and Go code did not change.

## Isolation and limits

The host orchestrates five uniquely labelled containers in an **internal-only Docker network**. Node contains the synthetic endpoint, TLS termination and a CONNECT proxy restricted to two fixture origins. There are no published ports or Docker socket mounts. The module mounts read-only. Mastodon receives only a public fixture certificate; temporary private material stays in the task's fixture directory/Node mount and disposable database. Its local user is explicitly confirmed/approved, and `.invalid` email validation is handled only during fixture creation; no mail is sent.

The Node fixture substitutes public DNS answers and final sockets only in test code. The ordinary Fedify URL/DNS/redirect guards still run; production `allowPrivateAddress`, TLS verification and application network defaults are unchanged. Final Node delivery uses actual HTTPS with certificate validation, then the test ingress proxies HTTP into Puma, preserving canonical Host/body bytes. This verifies the controlled TLS/proxy boundary, not public DNS pinning or deployed Railway proxy configuration.

The proxy refused attempted external JSON-LD context fetches such as `w3id.org`; it never opened external egress. No linked-data proof interoperability claim is made. The verified authentication gate is HTTP signatures.

The fixture invokes the actual Node publishing store with versioned synthetic events; it does not run the Go CMS in the same process. Go-to-Node contract/HTTP tests are a separate gate. One peer release and a public-Article profile do not establish full ActivityPub conformance, all clients, media rendering, moderation, private posts or other servers.

After completion, task containers, network, temporary TLS/app values and tmpfs data were removed. Downloaded official images remain cached. No production identity, existing database, account, cloud deployment or paid resource was changed.

## Reproducible runtime pins and final regressions

- Mastodon ARM64 v4.5.8: `ghcr.io/mastodon/mastodon@sha256:04ef0661180345f0b370ca13bf8fd6034f25c8caf9f018bfeb2f793b22a8c44c`.
- Node: `node@sha256:a9f5f7c91a432850b2a8a7797adf5eadb6c733ceed61167806cee7ea7fbc29df` (24.19.0, selected ARM64).
- Redis: `redis@sha256:57e4255cc971b1c8d6f8a6ca283ac01bc3ecdd914273ef6bfaede87aaecd6541` (Redis 7, selected ARM64).
- Cached `postgres:18-bookworm`, reporting PostgreSQL 18.6.

TypeScript passes. Fast suite: **63 pass, four opt-in skips**. Independent signature/HTTP/PostgreSQL gate: **26 pass**. Publishing HTTP fixture: **one pass**. Actual Mastodon gate: **six results pass**. The original standalone federation repository remains untouched.

Official source checks: [versioned container topology](https://github.com/mastodon/mastodon/blob/v4.5.8/docker-compose.yml), [actor discovery/WebFinger](https://github.com/mastodon/mastodon/blob/v4.5.8/app/services/activitypub/fetch_remote_actor_service.rb), [Article projection](https://github.com/mastodon/mastodon/blob/v4.5.8/app/lib/activitypub/parser/status_parser.rb), [edit timestamp handling](https://github.com/mastodon/mastodon/blob/v4.5.8/app/services/activitypub/process_status_update_service.rb), [signature guidance](https://docs.joinmastodon.org/spec/security/#http-message-signatures-rfc9421).

Remaining release gates: separately approved public HTTPS staging and ingress verification, Supabase connection/role behavior and backup/restore, explicit production identity decision, and a complete CMS-to-peer deployment run. Local success does not authorize activation.
