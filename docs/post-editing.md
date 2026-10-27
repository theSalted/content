# Local post edits: posts-html-v1

This page records the original edit milestone. Later management work adds
historical `published_at` edits, `featured` and empty-tag clearing; the next
[publishing milestone](publishing-management.md) adds opt-in scheduled PUT,
retained slug claims and the current editor workflow. Those contracts supersede
the original unsupported-field/date statements below. Read current source and
client evidence before treating this historical profile as a parity claim.

Implemented and tested on 2026-10-26. This is a bounded Ghost-shaped Admin API slice, not verified publishing-client compatibility. The create/read behavior in [implementation](implementation.md) still applies.

## Request and concurrency

Authenticate with the configured publication-scoped Ghost HS256 JWT. Read the post, then send its exact `updated_at` token to `PUT /ghost/api/admin/posts/{id}/`. Include `source=html` when changing HTML. The request contains exactly one post; omitted fields retain their values.

```json
{
  "posts": [{
    "updated_at": "2026-10-26T10:00:00.123Z",
    "title": "Revised title",
    "html": "<p>Revised body</p>"
  }]
}
```

Supported fields are `title`, `slug`, `html`, `status` and `visibility`; fields must be strings, not null. An empty HTML string clears the body. Status is `draft` or `published`, visibility is `public`. Unknown fields (including IDs, publication scope, `published_at`, authors/tags, Lexical and Mobiledoc), lossy/unsafe HTML, scheduling and unsupported query parameters fail before any persisted change. Metadata-only edits do not require a `source` parameter. `formats=html` and `save_revision=true` are accepted; other revision options are rejected.

A successful edit returns 200 and the updated one-item `posts` array. `updated_at` is an exact millisecond RFC3339 value and strictly increases, even if the server clock moves backward. The database locks the publication-scoped row before comparing the token. A stale edit returns 409:

```json
{"errors":[{"message":"Saving failed! Someone else is editing this post.","errorType":"UpdateCollisionError","code":"UPDATE_COLLISION"}]}
```

The token is a precondition, not a requested timestamp. Re-read after a conflict and preserve the caller's unsaved draft while reconciling changes. The backend does not silently merge edits. A post belonging to another publication returns 404. Authentication runs before the edit handler; body content cannot override publication scope.

## Persistence and visibility

Each accepted edit appends an immutable revision containing the accepted source, rendered HTML, renderer version and a metadata snapshot, then advances the current post pointer. Post ID, UUID, publication and creation time stay unchanged. Column-level runtime grants permit only the mutable post columns; runtime roles cannot update old revisions or outbox events.

The post update, revision and any publication event share one transaction:

| Transition | Internal CMS intent | Content API |
| --- | --- | --- |
| draft → draft | No public event | Excluded from reads/list/count |
| draft → published | `post.published` snapshot | Committed revision visible |
| published → published | `post.updated` snapshot | Committed edited revision visible |
| published → draft | `post.unpublished`, no HTML | Excluded immediately after commit |

The first publication time survives withdrawal and republishing. A republish keeps the same CMS identity and creates a new publication intent. Internal outbox rows remain schema version 1; they are **not** ActivityPub v2 requests. The subsequent [disabled-by-default adapter](publishing-delivery.md) prepares separate v2 payloads for newly enrolled posts. Existing history is not automatically delivered.

Migration `00002_post_edits.sql` adds revision metadata and allows a draft to retain its previous publication time. Original migration 00001 remains unchanged. Known current metadata is backfilled; unknown historical metadata is explicitly left unknown. New writes require an object snapshot. Tests replay migration 00001, upgrade twice and verify identity/source/timestamp preservation and successful unpublishing afterward.

## Ghost evidence and declared differences

The official [update guide](https://docs.ghost.org/admin-api/posts/updating-a-post) specifies PUT, the posts envelope and `updated_at`, and documents `save_revision=true`. The reference remains Ghost 6.72.0: its [post model](https://github.com/TryGhost/Ghost/blob/v6.72.0/ghost/core/server/models/post.js) preserves an existing publication date and sets one on first publication.

The pinned [release lockfile](https://github.com/TryGhost/Ghost/blob/v6.72.0/pnpm-lock.yaml) identifies `@tryghost/bookshelf-collision` 2.3.19 and `@tryghost/errors` 3.4.4. Read-only inspection of their public npm archives established `UpdateCollisionError`, `UPDATE_COLLISION`, the response message above and HTTP 409: [collision archive](https://registry.npmjs.org/@tryghost/bookshelf-collision/-/bookshelf-collision-2.3.19.tgz), [errors archive](https://registry.npmjs.org/@tryghost/errors/-/errors-3.4.4.tgz). No implementation was copied or dependency installed from those archives.

This is documentation/OSS-source evidence, not a captured Ghost server response. Ghost's collision check depends on changed fields; this implementation rejects stale tokens even for no-op edits. Every successful local edit stores a revision and advances the token, including no-ops. Ghost's optional revision retention and richer error details are not reproduced. Null handling, strict HTML rejection, DTO fields and auth/client behavior remain declared subset limitations. No Ulysses or iA Writer session has been verified.

Real PostgreSQL tests cover success, stale/no-op tokens, simultaneous writers, clock skew, cross-publication denial, unsupported inputs, slug conflicts, immutable metadata, rollback on forced outbox failure, draft/public transitions and upgrade safety. [Validation](validation.md) distinguishes these from unrun interoperability tests.
