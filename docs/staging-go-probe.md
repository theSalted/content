# Synthetic Go-to-ActivityPub staging probe

`cmd/staging-go-probe` is an explicit verification executable, never the CMS server entrypoint. It drives the real authenticated CMS handler in-process, PostgreSQL storage, immutable publishing outbox/mapper and delivery worker. Its outgoing requests use the ordinary HTTP sender against the co-located private AP receiver. It never provisions, migrates, creates credentials, clears failed-run data or reads AP tables.

Build for the temporary AP verification image:

```sh
CGO_ENABLED=0 GOOS=linux GOARCH=amd64 go build -trimpath -ldflags='-s -w' -o staging-go-probe ./cmd/staging-go-probe
```

The AP-owned [execution contract](../internal/activitypub/deploy/staging/GO-HANDOFF.md) supplies the exact environment allowlist and packaging location. Use separate CMS API/delivery roles with verified TLS; never pass the migration owner, AP DB settings or media/storage credentials. The probe requires an empty target publication and an explicit staging binding. A unique run ID is not permission to erase or overwrite previous evidence.

Assertions cover draft/public visibility, immutable original snapshots despite later edits, persisted byte-identical retry after a genuine post-commit503, queue reconnect/backoff, duplicate acknowledgment, edit/withdraw/republish with stable CMS identity and a new publication instance, and no implicit history enrollment. Its peer checks require independently verified signed delivery, durable AP retry and final anonymous410 Tombstones. Durable internal202 acceptance alone does not satisfy the public-delivery gate.

`internal/stagingprobe/integration_test.go` adds an opt-in disposable local PostgreSQL + module-owned Node fixture test. It uses the existing test harness environment and restricted roles, injects one503 after actual receiver commit, then verifies five receipts. Like the existing integration suite, it skips unless explicitly enabled. Config and peer-evidence regressions run with ordinary unit tests.

2026-10-26 validation: build/vet and46 top-level unit/race tests passed;16 database/Node integration tests were skipped in that invocation. Automatic review blocked creation of the new local PostgreSQL test container. A separate approved preflight instead exercised the actual runner against the isolated CMS staging schema in the existing shared PostgreSQL database and the local Node receiver: all seven CMS/outbox assertions and five receiver activities passed. Its separate synthetic publication retains evidence. This is not evidence of remote federation; the final existing-container cloud peer run remains a separate gate.

## Original cloud run and read-only completion

The original cloud write-run delivered all five lifecycle events and the controlled peer verified signatures/retries. Its final Go HTTPS tombstone request failed with `tombstone_transport` because the Node container lacked OS CA roots. The AP-owned wrapper now supplies Node public roots through child-only SSL_CERT_FILE, retaining normal TLS verification. The original failure is preserved.

`cmd/staging-go-complete` reads the USED publication without replaying writes, worker steps or Follow. Explicit REPEATABLE READ READ ONLY transactions are verified on both restricted connections because a pooler may drop startup parameters. It compares accepted immutable payloads/outbox snapshots and request hashes, original reconstructed peer evidence, final draft/history isolation, retry counts and instance identities, then fetches both public410 tombstones with normal HTTPS. Native execution against the actual existing CMS staging rows and public HTTPS passed. AP owner subsequently ran the static Linux artifact in the cloud with the public CA bundle: all eight checks passed, with one receipt GET and zero publishing/activity POSTs. The original failure and recovered-evidence distinction remain recorded. No owner/AP/storage credentials are used.

`cmd/production-one-post` is a separate explicitly approved, fixed-scope test executable; it does not relax this original writer's publication/replay guard. See [one-post fixture](production-one-post-test.md).
