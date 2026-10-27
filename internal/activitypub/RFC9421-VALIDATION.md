# RFC 9421 signature gate — 2026-10-26

Node 24.19.0 / Fedify 2.4.5 / actual PostgreSQL 18.6. `NODE_BIN=<node24> sh scripts/test-peer.sh` passes nine HTTP peer scenarios and 15 PostgreSQL scenarios (26 results including wrappers). TypeScript passes; the fast suite passes 61 tests, with four opt-in groups skipped.

## Independent evidence

`test/rfc9421.fixture.ts` uses Node crypto for a bounded, single-signature RSA profile. It does not call Fedify signing, verification, canonicalization or structured-field parsing. Requests cross actual HTTP sockets; the peer fetches the actor public key and checks owner/key ID, SHA-256 Content-Digest, creation/expiration, covered components and RSA signature base. Synthetic identities only. The DNS/socket isolation boundary remains as documented in [PEER-VALIDATION.md](PEER-VALIDATION.md).

Accepted inbound: independently signed Follow/Undo, duplicate Follow, arbitrary signature labels, omitted optional `alg`, optional future `expires`. Accepted outbound: actual Fedify Accept/Create/Update/Delete independently verified as RFC 9421. Existing Cavage Follow/Accept, lifecycle, retry/lost-ack and modern-to-legacy negotiation cases remain green.

Twenty-one negative HTTP fixtures are rejected before durable queue acceptance:

- Missing signed method, target URI or content digest; duplicate covered component.
- Past/future creation outside one hour, missing creation, expired signature, expiration preceding creation.
- Wrong digest signed deliberately, modified body, missing digest header, modified digest after signing.
- Wrong signed target or method, wrong algorithm declaration, mismatched signature label.
- Activity actor differs from signing key owner, wrong private key, key ID not published by its claimed owner.
- Cryptographically valid expired signature combined with a fresh but invalid signature: neither candidate can borrow the other's eligibility.

Five focused policy tests additionally cover malformed/empty modern headers, no legacy downgrade, parameterized component refusal, filtering of mixed candidates, maximum candidate count and bounded header size. The existing loader/private-address/redirect regression cases still pass. No publishing contract or Go-owned file changed.

## Reproduced gap and fix

Before the fix, actual requests missing each of `@method`, `@target-uri`, `content-digest`, and a correctly signed but expired request, returned 202. The same requests now return 401. Wrong cryptographic signatures, digest mismatches, actor ownership and creation-window violations already failed and remain enforced.

`src/inbox-signature-policy.ts` adds application policy before Fedify's verifier. It requires the three delivery components, unique parameterless component identifiers, integer creation within one hour and an unexpired optional expiration no earlier than creation. It considers the first three candidates (the existing Fedify bound), retaining only eligible label/signature pairs. Every retained candidate still undergoes Fedify cryptographic, body-digest and actor-ownership checks. An empty eligible set fails closed. Legacy requests retain the existing verifier path. No network guard, production allow-private setting or test bypass was introduced.

The policy uses the existing structured-field-values 2.0.4 parser, now declared as an exact direct dependency. It does not implement an alternate RSA verifier in production. The independent crypto implementation is test-only and excluded from the runtime image.

## Profile and limits

This proves the common parameterless RSA delivery profile, not every variant permitted by RFC 9421. Parameterized covered components (`;sf`, `;key`, `;bs`, `;req`, etc.) are rejected because Fedify 2.4.5's current canonicalizer does not fully implement their semantics. A compatible sender should use plain `@method`, `@target-uri`, `content-digest`, optionally other plain covered headers. Do not strip component parameters or bypass verification to accept unsupported variants. Supporting them safely requires a verified upstream implementation and additional independent vectors.

Other algorithms, request/response binding, arbitrary structured fields, multi-signature independent peer verification and real network TLS/proxy behavior are not claimed. The module filters up to three inbound candidates; the independent receiver deliberately tests a single signature, matching the documented Mastodon profile. Replay protection combines the one-hour creation window with durable ActivityPub activity-ID deduplication; signatures alone are not a replay database.

## Primary sources

- [RFC 9421](https://www.rfc-editor.org/rfc/rfc9421.html), particularly sections 1.4, 2.3, 2.5 and 3.2: the signature toolkit requires application-level policy for coverage, keys and time. It does not itself define an ActivityPub profile.
- [RFC 9530](https://www.rfc-editor.org/rfc/rfc9530.html): Content-Digest serialization and message-content hashing.
- [Mastodon security documentation](https://docs.joinmastodon.org/spec/security/#http-message-signatures-rfc9421): modern delivery requires created, signed method/target and signed Content-Digest; legacy support remains relevant. Versioned behavior must be checked against the selected release; documentation also describes unreleased versions.
- [Fedify sending documentation](https://fedify.dev/manual/send): modern-first delivery and remembered double-knocking fallback.
- [Social Web CG guidance](https://swicg.github.io/activitypub-http-signature/): non-normative interoperability and upgrade/key-ownership guidance, not an ActivityPub conformance certification.

An actual Mastodon instance, other independent implementations, deployed ingress/TLS, production identity activation and backup/restore remain distinct release gates. No full ActivityPub compliance claim follows from this test.
