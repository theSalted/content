# Publishing identity compatibility

Ulysses42 was observed requesting `GET /ghost/api/v4/admin/site/`, followed by
`GET /ghost/api/v4/admin/users/me/`. The second request had no query parameters;
a404 stopped account setup. This measured sequence is covered by HTTP tests.
Native Ulysses42 account setup subsequently succeeded with the same scoped key.
This confirms connection setup, not complete publishing compatibility.

Ghost staff access tokens carry a user context; integration keys do not become
staff automatically. This CMS exposes a bounded read-only **publishing profile**
when `CONTENT_PUBLISHING_IDENTITY_ENABLED=true` and all three existing public
`CONTENT_AUTHOR_ID`, `CONTENT_AUTHOR_NAME`, `CONTENT_AUTHOR_SLUG` values are set.
The existing single-publication Admin key authenticates this projection. It is
not an owner/browser session, a new staff token or a Ghost role assignment.

Response: `{"users":[{"id":"...","name":"...","slug":"...","roles":[],...}]}`.
Only configured public identity and nullable public-profile fields are returned.
No email, password, token, private staff settings or invented timestamps are read
or generated. Empty roles deliberately claim no Owner/Administrator authority.
`X-Content-Identity: publishing-profile` identifies this compatibility extension.
Do not describe it as full Ghost staff API parity.

Modern, v2 and v4 Admin aliases enforce their existing exact JWT audiences.
Only GET users/me is available. Optional fields selects public response fields;
include=roles returns the same empty array. Other includes/queries fail explicitly.
User enumeration, arbitrary user lookup, mutations, token management, staff login
and role management remain unavailable. Responses are no-store. Default is off.

Tests cover observed sequence, binding, private-field exclusion, JWT and audience
failures, wrong-method denial, fields/includes, explicit configuration, process
isolation and unchanged mutation surface. If a client requires actual staff
privileges or private fields, do not silently fabricate them or expand credentials.

Sources:
- [Ulysses publishing guide](https://help.ulysses.app/export-publishing/publishing)
- [Ghost4 user context resolution](https://github.com/TryGhost/Ghost/blob/v4.48.0/core/server/api/canary/utils/serializers/input/users.js)
- [Ghost4 API key authentication](https://github.com/TryGhost/Ghost/blob/v4.48.0/core/server/services/auth/api-key/admin.js)
- [Ghost4 user response model](https://github.com/TryGhost/Ghost/blob/v4.48.0/core/server/models/user.js)

## Observed draft-upload fields

The native Ulysses42 draft request includes `featured` (boolean),
`published_at:null`, `tags:[]`, title, slug, HTML and draft status. These previously
failed strict JSON decoding before persistence. Create and update now preserve
`featured` as revisioned display metadata; write responses return the boolean.
An omitted flag preserves its value on edit. Empty tags explicitly clear tags on
edit; nonempty tag writes remain unsupported.

`published_at:null` means no date supplied on create. On edit it is accepted only
when the locked current record has no publication date; it never erases historical
publication dates. Explicit dates/scheduling remain unsupported in ordinary writes;
the dedicated importer remains responsible for preserving historical dates.
`updated_at` concurrency, authorization, publication scope and draft privacy remain
enforced. Tests cover observed request shapes, read/edit/conflict, persistence,
immutable old revisions, no draft outbox and no partial writes on rejection.

The client upload success and next native edit sequence still require verification.
Safe rejection diagnostics retain only fixed reason codes and allowlisted field
types, never values, raw payloads, headers, credentials or arbitrary field names.
Content-level rejection diagnostics report all fixed validation categories and a
bounded inventory of recognized HTML element/attribute types and URL schemes.
They never log text, attribute values, URL hosts/paths or custom names. This does
not relax HTML safety or silently sanitize content. A later native attempt reached
content validation (422) without saving; the exact HTML cause still needs measured
request evidence. A local preview URL is not proof of the outgoing HTML.

Sources: [Ghost post creation](https://docs.ghost.org/admin-api/posts/creating-a-post),
[Ghost update/concurrency](https://docs.ghost.org/admin-api/posts/updating-a-post),
[Ghost4 model defaults](https://github.com/TryGhost/Ghost/blob/v4.48.0/core/server/models/post.js).
