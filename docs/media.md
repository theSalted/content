# Image storage: images-png-jpeg-v1

Implemented locally on 2026-10-26. The Go CMS owns image validation, PostgreSQL metadata and publication visibility; `media.Objects` supplies private object bytes. Filesystem and S3-compatible adapters are implemented. A private Railway bucket has subsequently been provisioned for the approved deployment; see the dated [Railway storage record](deployment-profiles/railway-media.md) for provider verification status. This is a bounded Ghost-shaped image endpoint, not complete Ghost media parity or measured iA image interoperability.

## HTTP contract and privacy

Authenticated `POST /ghost/api/admin/images/upload/` and the measured `/ghost/api/v2/admin/images/upload/` alias accept one multipart `file`, optional `ref` and `purpose`. Each field appears once; unknown fields and query parameters fail. The corresponding Admin JWT audience is required. A successful response is 201 `{"images":[{"url":"https://cms.example.invalid/content/images/<id>/image.png","ref":"original-reference"}]}`. `ref` is echoed only if supplied. The filename never becomes a filesystem path or object key. The profile is returned as `X-Content-Media-Profile: images-png-jpeg-v1`.

PNG and JPEG are checked by magic/MIME, dimensions and a complete raster decode. Limit: 10 MiB, 24 million pixels, 12,000 pixels per side, and two concurrent validations per process. `image` is the default purpose; `profile_image` and `icon` require square images. SVG, GIF, WebP, ICO, generic files/audio/video and remote URL ingestion are rejected or unsupported. Original accepted raster bytes are retained, including EXIF or other metadata; this milestone does not strip location metadata, resize, recompress, scan for malware or generate variants. Headers use the verified MIME, `nosniff`, a fixed safe filename and `no-store`.

An unattached or draft-only image is private. Admin JWT reads are publication-scoped; anonymous reads succeed only while at least one current published, public post in this publication references that asset through an `img src`. The browser review session renders owned raster images as bounded data URLs inside its sandboxed preview, after authenticating and loading the post. It never fetches content-supplied remote URLs. Direct image URLs are not bearer capabilities. Clients which demand anonymous access to a draft image may therefore need an explicit compatibility change; do not silently weaken privacy. Publicly downloaded bytes cannot be recalled after withdrawal.

Post/revision/outbox updates and `post_media` references commit together. Cross-publication, missing, unfinished, wrong-extension and malformed owned-image references fail without partial writes. At most 32 owned images are supported per post. Relative owned paths and exact configured media-origin URLs are recognized; Owned URLs in `img` srcsets are tracked; picture sources, CSS image references, feature images and URL aliases are not managed attachment types in this slice. External HTML image links are preserved but not ingested, cached or preview-fetched. Content consumers still own safe rendering of externally supplied HTML.

## Configure and run

Run the existing `content migrate` with the separate migration role first; migration 00005 creates media metadata and relations without changing or auto-importing old content. Then add explicit runtime grants below. Media remains disabled unless `CONTENT_MEDIA_BACKEND` is supplied. A missing migration/grant produces failure rather than a successful upload without metadata. The binary does not load dotenv files automatically.

For local development, set `CONTENT_MEDIA_BACKEND=filesystem`, `CONTENT_MEDIA_ORIGIN=http://127.0.0.1:8080` and `CONTENT_MEDIA_DIRECTORY` to an absolute, dedicated private directory. `content serve` creates/opens that directory with restrictive permissions. Do not serve it as a static directory or share it with another application. The HTTP origin must route `/content/images/` to the configured CMS publication. Review origin, CMS public origin and storage endpoint are distinct settings.

For S3, set `CONTENT_MEDIA_BACKEND=s3` and `CONTENT_MEDIA_ORIGIN`, then explicitly supply `CONTENT_MEDIA_S3_ENDPOINT`, `CONTENT_MEDIA_S3_REGION`, `CONTENT_MEDIA_S3_BUCKET`, `CONTENT_MEDIA_S3_ACCESS_KEY`, `CONTENT_MEDIA_S3_SECRET_KEY` and optional `CONTENT_MEDIA_S3_SESSION_TOKEN`. The adapter uses the OSS AWS SDK for Go v2, SigV4 and configurable S3 addressing; it does not require AWS, Supabase SDKs or Supabase Auth. Set `CONTENT_MEDIA_S3_URL_STYLE=path` (default) or `virtual-host` exactly as reported by the provider; virtual-host requires a root endpoint and undotted DNS-safe bucket name. It never searches credential profiles, IMDS, subprocesses or ambient cloud credentials. Endpoint must be HTTPS (literal loopback/localhost HTTP is for tests). Redirects and proxy discovery are disabled; calls have bounded timeouts and two maximum attempts. The configured endpoint is trusted operator configuration, never content input.

Keep the bucket private with no public ACL or anonymous policy. The CMS serves authorized bytes and does not issue public presigned URLs. Give the upload/read process only PutObject/GetObject within its deployment prefix when the provider supports that policy. The separately run cleanup process additionally needs DeleteObject. No ListBuckets/CreateBucket/lifecycle/ACL operation is used. Both local transport fixtures and the [actual Railway provider test](deployment-profiles/railway-media.md) pass for this adapter. Other S3 providers need their own acceptance check.

The existing runtime role needs these **additional** grants, replacing identifiers with safely quoted owned names; this is documentation, not automatically executed provisioning:

```sql
GRANT SELECT ON "cms_example".media_quota TO "cms_runtime";
GRANT SELECT, INSERT ON "cms_example".media_objects TO "cms_runtime";
GRANT UPDATE(state) ON "cms_example".media_objects TO "cms_runtime";
GRANT SELECT, INSERT, DELETE ON "cms_example".post_media TO "cms_runtime";
```

No runtime DDL, media key mutation, metadata deletion, schema ownership or migration-ledger access is added. As with existing CMS data, publication separation is enforced by the scoped application API; a shared runtime DB role is not row-level isolation against a compromised process.

## Atomic application quota

Migration 00006 adds an owned `media_quota` row with a **1 GB decimal limit: 1,000,000,000 bytes**. This is the application limit across all publications sharing this CMS schema, including every pending reservation and ready asset, attached or unattached. It is not a provider hard billing cap. Direct bucket writes, other schemas/installations, other buckets, retained backups or future object versions are outside this counter; use one dedicated bucket with this CMS as its sole writer. Object metadata/transport overhead and service egress are not included.

Each new reservation acquires a transaction-level installation/CMS/schema quota lock, reads committed total metadata bytes, and commits the pending intent only if capacity remains. Independent processes share that lock. Repeated bytes in the same publication reuse the existing reservation even at the limit. Another publication consumes its own space. A quota rejection returns HTTP 507 `InsufficientStorageError` before object upload. Pending failures retain their reservation until successful retry or cleanup deletes the object and commits row deletion; failed/uncertain cleanup does not grant speculative capacity. Existing over-limit data is retained; new unique uploads stop. Runtime roles may SELECT the quota but cannot modify or delete it. Migration 00006 must precede new media-enabled runtimes; stop old upload writers during upgrade, since old binaries do not enforce this quota.

The generic package keeps this limit in operator-owned PostgreSQL configuration, not a Railway SDK or per-process environment value. An unrelated deployment can choose another positive limit through its migration/operator role while upload writers are stopped. No live database quota was changed by local verification. The approved deployment retains 1,000,000,000 bytes.

## Retry, recovery and backup

Object keys contain a hash of installation/schema/publication scope and the content SHA256. Equal bytes in the same publication reuse one ID/key; other publications do not share that identity. A committed `pending` record precedes storage I/O. A row lock serializes duplicate finalization and cleanup; the ready transition commits only after the object write succeeds. Lost storage acknowledgement or uncertain DB commit leaves a durable intent for retry. The service never deletes an object merely because a commit result is uncertain. Reads verify length and SHA256; corruption fails closed.

`content media-cleanup` uses its own explicit `CONTENT_MEDIA_CLEANUP_DATABASE_URL`, schema, installation, publication and object configuration; it does not reuse the serve or migration connection. Its restricted DB role needs CONNECT/schema USAGE, SELECT on installation/media_objects, DELETE on media_objects and UPDATE(state) for row locking. It requires no post/revision/outbox access. The command deletes at most 50 pending objects older than 24 hours, skipping locked uploads. Filesystem cleanup also removes at most 100 matching stale scratch files older than 24 hours. It never deletes ready objects, even if unattached; ready orphan reclamation needs a future reference/retention policy. Run manually or via the operator's scheduler; no background service is installed.

Filesystem writes use an OS-rooted directory, safe internal keys, 0600 scratch files, fsync and rename. A crash before metadata finalization is recoverable through retry or pending cleanup. Back up PostgreSQL plus the object namespace and its checksum manifest together; the database contains no image bytes. Restore objects and metadata into isolation, verify checksums/private/public state, then enable serving. Keep original namespace identifiers or explicitly migrate object keys when restoring into a different installation. Do not assume an object store supplies versioning or an undo window. Automatic cross-store snapshots, restore drills and ready-object retention remain operational work.

## Superseded Supabase storage candidate

This earlier candidate is superseded by the explicitly approved private Railway bucket. Supabase remains the shared PostgreSQL provider. No Supabase storage was provisioned by this work. Its S3 endpoint shape is `https://<project-ref>.storage.supabase.co/storage/v1/s3`, with the project's actual region and a dedicated **private** bucket, proposed name `content-media`. No paid plan is required by this implementation; actual quota/cost acceptance must be checked before provisioning or real uploads.

A Supabase generated S3 access-key pair is **project-wide full S3 access across buckets and bypasses RLS**, not a bucket-scoped key. The one-time setup approval must explicitly cover creating the private bucket and that server-only key with its actual broad scope, storage of the secret in the approved server secret store, and rotation/revocation ownership. Never claim a bucket-limited key or expose it to iA, browser JavaScript or public logs. A dedicated project containing only this product's storage reduces accidental sharing, but does not reduce the key's technical scope. The alternate user-JWT session-token flow respects RLS but introduces deployment-specific auth policy work; it is not a core dependency.

Supabase supports the Put/Get/Delete operations used here, but S3 versioning/lifecycle support is not assumed and deletion can be permanent. Keep independent backups. No Supabase storage bucket or credential was created. The subsequent approved Railway bucket and synthetic provider test are recorded separately.

Sources: [Ghost image fields](https://docs.ghost.org/admin-api/images/uploading-an-image), [Ghost response](https://docs.ghost.org/admin-api/images/overview), [Supabase S3 authentication and key scope](https://supabase.com/docs/guides/storage/s3/authentication), [Supabase S3 compatibility](https://supabase.com/docs/guides/storage/s3/compatibility), [AWS SDK endpoint configuration](https://docs.aws.amazon.com/sdk-for-go/v2/developer-guide/configure-endpoints.html).

## Hostname transitions

Keep existing image URLs intact when changing the CMS hostname. Configure the new `CONTENT_MEDIA_ORIGIN` and list prior trusted root origins in `CONTENT_MEDIA_ORIGIN_ALIASES` (comma separated, maximum eight). Storage association checks recognize exactly these origins; unknown hosts remain external. New uploads use the primary origin. The editor displays owned images through its authenticated same-origin proxy without mutating saved source URLs. No remote URL is fetched by this mapping. Keep the former hostname serving the application until old published links are deliberately migrated.

For browser login/forms, `CONTENT_REVIEW_ORIGIN_ALIASES` allows explicit prior HTTPS origins while keeping session cookies and CSRF validation. Host-only cookies require sign-in again on the new hostname. Verify DNS/TLS before switching settings. Frontend `CONTENT_SITE_URL`/`CONTENT_PAGE_URL` remain article/page permalinks and must not be changed to the administration hostname.

## Public visibility HEAD (`visibility-head-v1`)

`HEAD /content/images/<24 lowercase hex ID>/image.png|jpg` performs the same
publication-scoped, ready-object/current published-public reference check as GET,
without an object-store request or hashing/downloading the image. A200 response
returns the immutable stored `Content-Type`, `Content-Length`, strong
`ETag: "sha256-<64 lowercase hex>"`, `X-Content-Media-Profile: visibility-head-v1`
and `Cache-Control: no-store`; it has no body, cookie, object key or bucket URL.
GET returns the same ETag after verifying actual bytes. Conditional headers never
skip current eligibility or produce304. Metadata corruption/DB failure returns503;
missing, private-only, withdrawn or wrong-extension assets return404. Malformed or
encoded paths and any query suffix are rejected. All responses remain no-store.

HEAD is an eligibility/identity assertion, not an object-store health probe: an
eligible object's backing store could be unreachable or missing and HEAD still
returns200; GET then fails503. Consumers must not treat that as an image payload
or cache an upstream GET failure. Immutable metadata is committed only after the
original verified upload/finalization; it is not recomputed during HEAD.

An Admin JWT on this route can read private media, as before. Such HEAD/GET
responses MUST NOT populate a public cache. A public CDN gate must make a new
anonymous HEAD with no caller Authorization/cookies, no credentials or redirects,
validate success/profile/type/length/ETag, and fail closed on every other outcome.
A browser private image under `/ghost/images/` remains session-gated/no-store.
A shared response cache must never bypass the fresh visibility gate. Checking
only at cache fill time would leave withdrawn images accessible until expiry.
A successful check can race a later withdrawal; it authorizes that in-flight
request, not future requests. Already downloaded bytes cannot be recalled.

This CMS change enables a consumer CDN gate; it does not enable CDN caching,
add transformations, change bucket privacy, or require migrations/new grants.
