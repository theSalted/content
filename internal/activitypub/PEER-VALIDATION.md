# Controlled local ActivityPub peer verification

2026-10-26. Node 24.19.0, Fedify 2.4.5, actual PostgreSQL 18.6. All **9 peer scenarios** and **15 existing PostgreSQL scenarios** pass (26 results including the two enclosing tests). TypeScript passes. The standard fast suite has 61 passing tests with four explicitly opt-in groups skipped.

## What ran

`NODE_BIN=<existing Node 24 executable> sh scripts/test-peer.sh`

The harness uses the already cached PostgreSQL image, one disposable tmpfs container, synthetic identities and temporary restricted roles/schemas. It starts two HTTP servers on random loopback ports and closes them after the run. This controlled-peer harness requires no image downloads, remote accounts, production keys, public posts, deployments or external network requests occur. No Go-owned files changed. The follow-on RFC 9421 gate added an application inbox policy described in RFC9421-VALIDATION.md.

No compatible Fediverse server image was present in the cached image inventory. The receiver is a **small controlled peer**, not Mastodon, Ghost, GoToSocial or an independently deployed Fediverse product. Its Cavage and RFC 9421 RSA-SHA256 signing/verification is implemented with Node crypto separately from Fedify. It fetches the sender actor's public PEM over real HTTP, verifies signed request-target/Host/Date/Digest and body bytes, applies article updates/deletes in a test-only map and deduplicates activity IDs.

## Passed cases

| Case | Observed result |
| --- | --- |
| Inbound signature bounds | Correctly signed requests two hours in the past or future and a digest-tampered body are rejected before queue acceptance. A current signature succeeds. |
| Follow / Accept | Independently signed Follow enters the real durable inbox queue. Processing creates one follower and one Accept; the Accept traverses HTTP and passes independent RSA verification. Duplicate Follow creates no second Accept. Actor/key discovery uses the default loaders. |
| Signature-format negotiation | An unknown peer rejects the default RFC 9421 attempt; Fedify falls back to the peer's supported Cavage format, which the peer verifies independently. |
| Create / Update and retries | Peer commits Create then returns synthetic 503 to simulate a lost acknowledgment. Retry preserves the activity ID and exact activity body; peer deduplicates it. A queued Update cannot overtake the delayed Create. An Update rejected transiently before application is retried and changes the same peer object. Failed HTTP attempts are recorded in delivery_failures. |
| Withdrawal / Delete | Signed Delete reaches the peer and marks its object deleted. The original object URL returns HTTP 410 with a Tombstone. Received activity order is Accept, Create, Update, Delete. |
| Signed Undo and actor ownership | A signature from one actor cannot claim another actor's activity. Valid Undo removes the authenticated relationship; duplicate Undo leaves it removed. |
| Modern signatures | Independent RFC 9421 Follow/Undo and duplicate handling succeed; outbound Accept/Create/Update/Delete pass independent verification. Missing required coverage, expired signatures, modified bytes and wrong keys are rejected. See the dedicated report for the 21 negative fixtures and policy fix. |
| Default network guards | Actual default loader rejects loopback IPv4/IPv6, private IPv4, link-local metadata addresses, localhost, private/mixed DNS results, credential-bearing URLs and a file URL before the fetch transport is reached. Public HTTP redirects and HTML alternate-document links pointing to private addresses are also rejected. An outbound inbox redirect to a private address is blocked before that destination is dispatched, and its job remains retryable. |

The original seven test subcases group the signature-format assertion with Follow/Accept. Retry failure records report the final attempted HTTP status; signature negotiation may attempt another format after an error, so a transient peer 503 does not imply every recorded final error is http_503.

## Test boundary and remaining gates

The module uses its ordinary default Fedify document/context/authenticated loaders, outbound sender and durable PostgreSQL queue. The test steps the registered real Fedify queue handler deterministically and advances only its own synthetic job availability timestamps. Runtime database privileges remain restricted.

Only `test/controlled-peer.test.ts` substitutes DNS answers and the final transport: a fixed allowlist of synthetic public HTTPS origins maps to actual loopback HTTP sockets through node:http. This preserves signed Host/body bytes. The public IP used as a DNS answer is never contacted. Unknown origins are refused, and private-destination tests assert that they never reach this transport. The test performs no OS DNS edits and introduces no allowPrivateAddress or benchmark-mode runtime configuration. Test code is excluded from the production Docker context/source copy.

This does **not** prove real DNS pinning/rebinding resistance at the socket layer, TLS validation, Railway proxy forwarding/signature behavior, or Supabase connection/pool behavior. It proves default URL/DNS-result/redirect guards under controlled DNS answers and real local HTTP transfer. The subsequent RFC 9421 gate independently verifies the bounded public-key profile, as recorded in [RFC9421-VALIDATION.md](RFC9421-VALIDATION.md). It is not a full peer implementation, moderation model or rendering test.

Remaining release gates include a separately approved identity/activation decision, backup/restore drills, deployed ingress and TLS checks, and authorized tests against real independent Fediverse implementations for Follow/Undo/Accept, Article rendering and links/images, Create/Update/Delete, signature negotiation and retry behavior. These local results do not constitute a full ActivityPub compliance claim.
