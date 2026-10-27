# Authored tags in federation snapshots

This opt-in projection implements internal profile
`content-activitypub-v2-2026-10-26-r3-authored-tags`. It is separate from a general
Ghost tag-management API. Existing stored events and archive payloads remain
immutable; upgrading the adapter does not reserialize or broadcast history.

Set `CONTENT_PUBLISHING_TAGS_ENABLED=true` and a comma-separated
`CONTENT_PUBLISHING_TAG_ORIGINS` list only after both the receiving ActivityPub
module and the Go delivery process support revision3, and the consumer serves
real canonical tag URLs. Without the flag, snapshot bytes retain their prior
shape. No URL or hashtag is inferred from a label or from article prose.

The source is the post revision's `presentation.tags`. Only records explicitly
marked `visibility: "public"` are eligible; internal `#` labels and records with
missing/private visibility are excluded. Every eligible record must have a valid
name and exact canonical `url` on a configured HTTPS origin. Missing/invalid
public URLs fail the enclosing publish/edit transaction instead of silently
losing metadata. Withdrawals omit the document and all tags, so malformed public
metadata cannot prevent withdrawal.

The wire field is optional `document.tags`, an array of0–32 `{name,href}` pairs.
Names contain1–100 Unicode code points, are nonblank, have no NUL/unpaired
surrogate or leading internal-tag marker, and preserve authored spelling. URLs
are HTTPS without userinfo/query/fragment, at most2048 UTF16 units, with no exact
duplicates. The complete snapshot replaces prior tags; omission/empty means none.
The receiving module chooses AS Hashtag versus Link without changing the source
label. Remote clients may display ordinary Link tags differently.

Tests cover the AP-owned23-case corpus, Unicode boundaries, explicit visibility,
immutable mapping and retry bytes, PostgreSQL transaction rollback, Go→Node
Hashtag/Link output, removal and withdrawal. The consumer must provide its own
canonical tag routes before production enablement. Existing archive enrollment
is never replayed merely to add tags.
