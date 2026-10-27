# Owned image URL response projection

When media is configured, HTML in Admin and Content API responses uses the primary
CMS media origin for image URLs on explicitly configured owned aliases. Only exact
`/content/images/<24 lowercase hex>/image.png|jpg` paths in `img` `src` and `srcset`
are projected. Quotes, whitespace, text, links, other attributes and unrelated URLs
are retained. Relative, credential-bearing, query/fragment, encoded-path, unknown
origin and malformed URLs remain unchanged. No URL is fetched by this projection.

This is a view: stored HTML/source/revisions, IDs, timestamps, CAS tokens and
publishing events are unchanged. Browser editing still uses original source and
authenticated `/ghost/images/` display URLs. An explicit client save of a projected
representation may create a normal revision; merely reading does not. Immutable
import/history payloads remain evidence and retain their original URLs.

The primary CMS origin is used for private as well as public representations;
private images are never projected to a public-only CDN host. The existing media
handler still requires published public eligibility or valid Admin authentication.
Owner cookies remain confined to their existing origin. API responses remain
`Cache-Control: no-store`; image HEAD still performs only the metadata/privacy check.
A public website may map eligible CMS media URLs to its separate gated CDN.

Keep recognized legacy aliases configured after retiring their hosting resources.
No new schema, grants, credentials, object storage access or migration is required.
This does not redirect arbitrary historical links or repair already-downloaded
remote copies. AP representation projection is owned by the ActivityPub module.
