# Contract inventory

`baseline.json` pins the research reference and separates implemented local tests from pending Ghost/client verification. The JSON files in `examples/` are **synthetic, authored request/shape examples** on reserved `.invalid` domains. They are not complete response schemas, credential material, a generated API specification or recorded test outputs.

Use these examples to discuss the adapter boundary. After an implementation is authorized, create a separate measured corpus with request/response/state/provenance records described in [the test plan](../docs/contract-testing.md). Never silently promote these illustrative values to golden reference results.

- `create-html.request.json`: one draft request body; use with `POST /ghost/api/admin/posts/?source=html`.
- `update-post.request.json`: illustrative edit requiring the actual timestamp returned by the last read.
- `upload.response-shape.json`: illustrative image URL/ref envelope; HTTP status 201 is documented in pinned source.
- `publication-event.example.json`: historical CMS-side proposal, not the existing ActivityPub prototype contract; consolidation is unresolved inside the single package.

The local posts-html-v1 implementation now has executable unit/HTTP and real PostgreSQL tests. They are our own behavioral tests, not recorded Ghost responses; no publishing app was connected. See [implementation](../docs/implementation.md) and [validation](../docs/validation.md). The pre-existing examples remain illustrative.

[Publication consolidation](publication-consolidation.md) records the audit-reported mismatch and required mapping/tests. No same-package or same-database compatibility claim is implied.

## Executable client investigation corpus

`client-probe/documented-html-workflow.json` drives a synthetic documented API workflow against disposable PostgreSQL. `client-probe/current-gaps.json` separately asserts unfinished features remain explicit failures. Neither is captured Ulysses/iA traffic or a Ghost golden corpus. See [client checkpoint](../docs/client-compatibility-checkpoint.md).
