# Offline Ghost import and bounded read compatibility

This is an operator-controlled archive/import command, separate from ordinary
Admin API creation. It is not a completed Ghost replacement or cutover tool.
Ghost remains the source until full inventory, media, rendering, private-content
checks and restore/rollback verification pass.

## Archive and apply

Build `go build ./cmd/content-import`. Plan first:

```sh
content-import -file /private/export.json -source original-blog -publication trial \
  -source-url https://original.example \
  -public-snapshot /private/matching-public-snapshot.json
```

The input may be a standard Ghost JSON export (`db[0].data`) or the explicitly
labelled public Content API snapshot envelope used by the migration audit.
Public snapshots must never be described as complete backups. Input is bounded
to 64 MiB. Supply an already-owned, migrated **isolated trial schema** and the
operator DSN through `CONTENT_MIGRATION_DATABASE_URL`, `CONTENT_SCHEMA` and
`CONTENT_INSTALLATION_ID`, then repeat with `-apply`. Enrollment and delivery
must be disabled. The command does not provision schemas, databases or roles.

Forward migration 00007 adds two operator-only tables; 00008 adds the post/page discriminator. Runtime and delivery roles
must receive **no privileges** on `source_import_batches` or
`source_import_records`. Do not use blanket grants on every table. Original
export bytes, SHA-256, source record JSON, per-record hash, disposition/reason and
source-to-CMS ID mapping remain available to the operator. Whole exports can
contain staff secrets and private content: store them outside Git, restrict
filesystem permissions, encrypt backups, and never put them in public fixtures.

Compatible posts and pages preserve source 24-hex ID, UUID, slug, source/rendered HTML,
created/updated/published instants and initial revision metadata. Public excerpts,
custom excerpts, feature-image/SEO fields and allowlisted public tag/author data
are revisioned. Database-export author/tag joins are resolved in source order.
Passwords, email, role assignments, raw Lexical/Mobiledoc and unknown fields are
retained only in the protected archive, never passed through to Content API.

Only draft/published posts or pages with public visibility and the supported HTML profile
can become active records. Scheduled/private/member-only records, invalid
identities, missing rendered HTML, multiple-author editing and unsupported HTML
are quarantined with a reason. Their originals remain intact. Supporting a field
in the archive does not imply that its product behavior (newsletter, redirects,
memberships, scheduling, code injection, theme settings) has been implemented.

One transaction covers the archive batch, imported posts/revisions and mapping.
A namespaced advisory lock serializes imports for that publication. Exact reruns
are no-ops, including after an author edits the imported post. A changed source
record or ID/UUID/slug collision requires explicit reconciliation and rolls back
the entire batch; the importer never silently overwrites edits. Quarantined
records are not automatically promoted by rerunning a newer binary.

Imports create **zero** outbox events, publishing streams or delivery records.
Historical federation archives require the separately versioned archive
contract and a verified public-only manifest; importing is not an instruction
to broadcast historical Creates. Existing origins/media stay live during trial.
No files are remotely ingested by this command. Database-export numeric 0/1
featured flags become API booleans. With an explicit HTTPS `-source-url`, only
Ghost URL placeholders in HTML URL attributes and selected image/URL metadata
expand to that original source base. Original HTML, Lexical/Mobiledoc and whole
export bytes remain unchanged in the private archive. No destination URL rewrite
or binary media transfer occurs. The mapping is recorded and cannot change on rerun.

The optional `-public-snapshot` preserves Ghost-computed public excerpts/display
metadata missing from a database export. Its source origin, public state and every
post/page identity, title, slug, body and historical timestamp must match exactly.
The original snapshot and checksum are also archived. Drafts never receive public
snapshot data. Missing/mismatched public revisions reject the plan; a newer capture
cannot silently overwrite an existing mapping.

## Read contract

The bounded post read profile accepts `Accept-Version: v5.0`, `v6.0`, or omission;
the observed iA v2 Admin alias still accepts `v2.0`. This is a declared subset,
not full version parity. `Content-Version` remains `v6.0` and
`X-Content-Compatibility` remains `posts-html-v1`.

GET post and page browse/read supports `fields`, `include=authors,tags` and Content API
`formats=html,plaintext`. Field projection never grants access to raw source or
private fields. Unknown field names are ignored. Included relation arrays survive
field projection, while primary relation fields require explicit selection when
`fields` is present. These details were measured against Ghost 6.39 using v5.0;
the current documentation example includes an ID even when not selected, unlike
the observed source behavior. Unsupported filters/order/includes still fail.
Pagination remains bounded at 100; consumers must follow `meta.pagination.next`.

Imported excerpts remain exact until a body change. Body edits discard the old
generated excerpt, retain an explicit custom excerpt, and use a documented
50-word plaintext fallback. This fallback is not a reimplementation of every
Ghost excerpt/card rule. Canonical frontend base paths come from
`CONTENT_SITE_URL`; optional `CONTENT_PAGE_URL` supplies a separate page base.
Pages use `/ghost/api/{content,admin}/pages/`, ID/slug reads and title-order browse;
page creation/updates through the Ghost Admin API remain unsupported. Existing
imported pages can be edited at authenticated `/ghost/pages/{id}/`, with the same
CSRF/concurrency rules. Pages never enter post feeds or federation outbox. Personal
routes never belong in core code.

The HTML profile additionally preserves inert Ghost image classes/loading/
strict HTTPS or root-relative width/density srcsets, bounded sizes, code classes,
heading anchors, exact `white-space: pre-wrap` spans and inert Ghost HTML-card
boundary comments. Ordinary HTTPS links need no forced SEO `nofollow` mutation.
The explicit URL expansion above is the only imported-body transformation.
Table-cell left/center/right alignment and HTTPS links with an empty fragment are
preserved. HTTPS YouTube/YouTube-nocookie `/embed/{videoID}` iframes with a bounded
attribute/query allowlist are accepted unchanged; duplicate attributes, arbitrary
hosts, executable attributes and active permissions fail. The editor CSP does not
execute embedded frames. Scripts, event handlers, active SVG, arbitrary CSS and
data/javascript srcsets still fail. Tiptap may not represent
all accepted HTML; its existing source-fidelity guard must continue to prevent
lossy mode changes. HTML mode remains authoritative when conversion is rejected.

## Verification and remaining release gates

Unit/HTTP tests cover source identity/date preservation, metadata privacy,
quarantine, v5 field/include projection and excerpt updates. Opt-in PostgreSQL
`TestGhostImportPersistence` verifies exact archive bytes, idempotence, late
collision rollback, zero event/enrollment/delivery rows, publication scoping,
subsequent edits/stale tokens and denial of archive access to an existing
restricted runtime role. Tests create/drop only a uniquely named schema in an
explicitly supplied disposable loopback database; no source connection is used.

A release still requires a complete authenticated source export, status and page
inventory, verified referenced/unreferenced media backup and ownership mapping,
unsupported card resolution, actual website comparison, full backup/restore and
an approved production runtime. A public snapshot plus synthetic test passes
does not satisfy these gates.

Primary references: [Ghost Content API parameters](https://docs.ghost.org/content-api/parameters),
[Ghost Content API versioning](https://docs.ghost.org/content-api/versioning),
[Ghost pages](https://docs.ghost.org/content-api/pages),
[Ghost export contents](https://docs.ghost.org/migration/ghost).
