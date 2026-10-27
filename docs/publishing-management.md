# Scheduling and retained URLs

Local implementation, 2026-10-26. Requires migrations 13–14 and the grants below.
Deployment is separate. No historical dates become jobs, no imports are enrolled,
and on-follow backfill remains off. This is a bounded publishing profile, not
full Ghost parity or native-client scheduling certification.

## Editor

The canvas contains title and body, with Rich text / HTML / Markdown modes. The
top bar contains Back to Content, actual state, Save draft / Update, Publish or
Edit schedule, and Settings. Published **Update** immediately changes the live
revision. There is no staged published revision or autosave.

Settings contains the slug and, only for published content, **Publish date (UTC)**.
That field edits history; it does not schedule or unpublish. Display is to the
minute, but leaving it unchanged preserves stored seconds and milliseconds.
The settings drawer becomes a full-width sheet on mobile.

Publish opens **Publish now** / **Schedule for later**. The latter reveals a
future date/time in explicitly named UTC and a **Schedule** button. UTC is the
only editor timezone in this release; there is no implicit browser timezone or
DST conversion. Scheduled content can be saved, rescheduled, cancelled to draft,
published now or moved to recoverable Trash. Ordinary saves retain its due time.
Published content must be explicitly unpublished before scheduling it again.
Stale actions return 409 and preserve unsaved editor text.

## Wire and durable execution

With `CONTENT_SCHEDULER_ENABLED=true`, use the existing authenticated Admin PUT
and exact `updated_at` token, with `status:"scheduled"` and a future,
timezone-qualified `published_at`. Admin responses project the due instant as
`published_at` for scheduled records. Creating directly as scheduled remains
unsupported: create a draft, then update it. Supported historical date edits,
HTML validation, publication scope and version aliases remain unchanged.

The stored `posts.published_at` retains actual publication history until the job
runs. `publication_schedules` holds the due instant, generation, expected
revision and bounded retry state. Revision metadata records `scheduled_for`;
the job generation is internal operational state, not a content identifier.
Cancel retains the previous actual publication date. Due activation sets the
publication date to the chosen due instant, including after a late restart.
Publish now uses processing time. Public API, alias resolution and anonymous
media reads exclude scheduled content.

`content serve` runs the worker in its existing process/pool: immediate check,
then every 10 seconds, at most 20 jobs per batch, five seconds per transaction.
It locks post then job, uses PostgreSQL time and `SKIP LOCKED`, rechecks revision,
state and due time, and commits the post, immutable revision, media visibility,
outbox event and job removal atomically. No network delivery occurs in that
transaction. Concurrent workers and lost commit acknowledgments cannot create a
second publication. Failure rolls back, then retries the same generation with
2-second to 1-hour backoff; another due job can proceed. Shutdown cancels and
waits for the worker before closing the pool. Pages do not create federation
events. Existing post/AP identity rules are unchanged.

The flag defaults off and controls worker plus scheduling UI/API. With it off,
existing schedules remain durable and private; cancellation, Trash and explicit
publish-now remain available, while ordinary scheduled edits are refused. Do not
disable it expecting pending jobs to run. Operators can inspect counts/lag and
`last_error_code` in the private schedule table; no new metrics service or public
job endpoint is added. Failures log a fixed message without content or secrets.

## Retained slug contract for consumers

`post_slug_claims` reserves each publication-wide post/page slug to one stable
post ID. A schema-qualified invoker trigger applies to every writer. A reserved
slug cannot transfer to another post, including after Trash. Renames keep prior
claims, resolve directly to the current slug, and preserve CMS UUID and the
federated object instance. A public rename is an ordinary Update.

Migration seeds current slugs only. Public eligibility comes from current
public state or matching-slug publication revision evidence, never a non-null
date. It cannot reconstruct unknown earlier aliases. A historical alias resolves
only if it was public and the target is currently published/public/not trashed.
Unpublish/Trash immediately suppress all aliases. Draft-only former slugs are
never exposed. Alias collisions return 409 without partial writes.

Consumer extension, **not a Ghost endpoint**:

`GET /content/api/v1/resolve/?type=post&slug=old-slug&key=<existing-read-key>`

```json
{"resolution":{"match":"alias","post_id":"0123456789abcdef01234567","slug":"current-slug","url":"https://site.example/blog/current-slug/","redirect_status":301}}
```

Canonical matches return `match:"canonical"` and `redirect_status:null`.
`type=page` uses the configured page base. GET returns 200 JSON; HEAD checks the
same visibility without a body. All responses are `Cache-Control: no-store`.
Missing/private/trashed/wrong-kind return 404; invalid input 400, invalid read key
401, unavailable storage 503. The API itself never redirects a credential-bearing
request or returns private content, an alias inventory or a SEO override as the
redirect target.

The website must resolve on its server, validate its expected owned origin/path,
read the target by stable ID through the public API, then issue 301 for an alias.
A withdrawal between lookup and read must return 404. Use no-store lookups and
redirects initially; distinguish outages from missing posts. Never forward the
read key. Current URLs feed canonical/OG metadata, links, RSS and sitemap; omit
aliases. Preserve intentional SEO canonical overrides separately. Test chains,
rename-back, collisions, page/post scope, concurrent withdrawal and unsafe
upstream URLs. Scheduling also requires public routes/feeds to discover new
content without a website rebuild or indefinite negative cache.

`CONTENT_SLUG_REDIRECTS_ENABLED=true` only removes the editor's old-link warning
after this consumer integration is verified. It does not install website
redirects. Keep false until the consumer is ready.

## Exact additional runtime permissions

Use the existing migration owner and schema-qualified migrations. Substitute the
configured schema/role; these example grants have **not** been applied live:

```sql
GRANT SELECT, INSERT ON content_cms.post_slug_claims TO content_cms_api;
GRANT UPDATE(ever_public) ON content_cms.post_slug_claims TO content_cms_api;
GRANT SELECT, INSERT, DELETE ON content_cms.publication_schedules TO content_cms_api;
GRANT UPDATE(schedule_id,due_at,expected_revision,attempts,next_attempt_at,last_error_code)
  ON content_cms.publication_schedules TO content_cms_api;
```

No new login, credential, service, default privilege, SECURITY DEFINER function,
alias DELETE/ownership transfer, post DELETE or neighboring-schema grant is
needed. Existing post/revision/media/outbox permissions perform publication.
Delivery, cleanup and website database roles gain nothing. The HTTP writer
checks these rights at startup even with scheduling off, because normal reads
and writes now consult schedule/claim tables. The separate sender does not run
this writer check. Both new tables revoke PUBLIC access; the trigger runs with
caller rights and needs no runtime direct function EXECUTE grant.

Migrate 13–14, apply narrowly reviewed grants, and deploy compatible code before
enabling scheduling. Once schedules exist, an old binary is not a safe rollback:
retain compatible code, or explicitly cancel/drain jobs first. Do not drop tables
or silently convert scheduled records. The local integration branch retains the
independently released media-HEAD change; no runtime/media configuration is changed.

## Verification and limits

Go race tests, vet and Linux build pass. Real local PostgreSQL 18.6 tests cover
claims/chains/collisions, publication scope, unchanged AP identity, cancel/edit
races, multiple workers, restart catch-up, forced outbox failure, terminated
transaction rollback, private image eligibility, pages and management-version
upgrade preservation. Migration creates zero jobs and does not alter existing
post bytes. Chrome and WebKit each pass 11 editor scenarios and 50 source-fidelity
cases, including schedule/reschedule/cancel, mobile controls and stale saves.

The subsequent local integration run exercises the exact new grants with
transient, non-owner PostgreSQL roles and retains the media-HEAD change. Each
required permission is individually revoked to prove startup refusal, then
restored. Runtime scheduling/cancel/reschedule/publication, alias lookup/rename,
atomic event counts and media HEAD privacy pass. Alias ownership mutation/deletion,
schedule identity mutation, post deletion, DDL, direct trigger execution and
neighboring-schema access remain denied. Existing role, migration, browser session,
client-contract, delivery and discovery database suites also pass. All test roles
and databases are removed and the existing local container state restored.

Combined Node integration tests were not rerun. Execution against the deployed
restricted role still needs verification after production grant approval.
No native iA/Ulysses scheduling test, production scheduler run, website redirect
integration, IANA timezone selector or exact-time delivery SLA is claimed.

Ghost references: [scheduling API](https://docs.ghost.org/admin-api/posts/scheduling-a-post),
[publishing](https://ghost.org/help/publishing-content/),
[post settings](https://ghost.org/help/post-settings/),
[editor](https://ghost.org/help/using-the-editor/),
[redirects](https://ghost.org/help/redirects/). Retained automatic slug aliases
are our deliberate link-preservation behavior; Ghost documents redirects
separately. The interface follows its publishing/settings separation, not its
branding or newsletter workflow.
