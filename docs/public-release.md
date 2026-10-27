# Public source baseline

This initial public history consolidates the CMS management/scheduling/media implementation and the internal ActivityPub module. Earlier private operational history is not part of this repository. No real content, database exports, private handoffs, infrastructure credentials or signing keys are included. A project license remains an explicit owner decision.

Validation for this baseline:

- Go race suite and vet pass. The complete local disposable PostgreSQL/combined Node run passes 182 Go tests with no skips, including role isolation, migration boundaries, scheduling, stale edits, private media, exact outbox retry bytes, archive silence and the real Go child lifecycle/shutdown protocol.
- ActivityPub TypeScript checks pass; 116 fast tests pass, with 6 separately opt-in scenarios skipped. Its real PostgreSQL suite passes 18 scenarios. Fourteen supervisor/wrapper boundary tests pass.
- All three pinned source Dockerfiles build for Linux/amd64; non-root/runtime separation and disabled-without-credentials smoke tests pass. The integrated image compiles its Go sender from this tree rather than copying an old operator binary.
- The committed editor bundle is byte-identical when rebuilt with the locked dependencies and Bun 1.3.13.
- Native writing-client, physical-device, full Ghost parity, optional Mastodon/container suites and live deployment outcomes are separate evidence. Passing local tests is not a claim that every external workflow is supported.

Two older synthetic probe fixtures needed their narrow API grants updated for the existing scheduling/slug/Trash schema. The application runtime and production permissions were not changed for that correction. Old production-specific test wrappers were omitted; generic contract and loopback fixtures remain. The optional Mastodon test uses a repository-relative dependency mount instead of a developer's machine path.

Source builds are documented in [deploy/README.md](../deploy/README.md). Connecting hosting to this repository must preserve the existing runtime configuration, separate credentials, disabled on-follow history replay and explicit archive enrollment policy. No migration or content mutation runs automatically during build or startup.
