# Source builds and deployment

All three Git source builds use the **repository root** as their context. Dependency manifests/lockfiles and base image digests are pinned. No prebuilt binary, dotenv file, private operator directory or build-time credential is required. Go embeds the committed editor asset. Rebuild that asset from its locked source when changing the editor.

| Process | Dockerfile | Default command | Readiness |
| --- | --- | --- | --- |
| CMS | `Dockerfile` | `/content serve` | `/readyz` |
| AP API + Go outbox sender | `deploy/Dockerfile.activitypub-api` | `node deploy/integrated/entrypoint.ts` | `/health/ready` |
| AP worker | `deploy/Dockerfile.activitypub-worker` | `node src/worker-main.ts` | `/health/ready` |

```sh
docker build -t content-cms:local .
docker build -f deploy/Dockerfile.activitypub-api -t content-ap-api:local .
docker build -f deploy/Dockerfile.activitypub-worker -t content-ap-worker:local .
```

For Railway, select this repository and the intended reviewed branch/commit on the existing services, keep root directory `/`, and set each Dockerfile path from the table. Leave the start-command override empty or use the exact listed command. Preserve existing region, ports, domains, healthchecks, drain policy and runtime configuration. Connecting a source can trigger a build/deployment immediately; stage source/config changes together until the source has passed tests. There is no repository configuration that creates services or silently runs migrations.

Supply runtime-only credentials via the hosting secret store. The CMS gets only its API role and its own media credentials. The integrated AP process keeps AP and CMS-delivery roles distinct; its child environment uses an allowlist and must not inherit owner/API/media/moderation credentials. The worker gets only its AP runtime settings. Migrations and identity setup are deliberate operator actions with separate privileges. Do not put migration commands in automatic startup or pre-deploy hooks.

The CMS image includes an optional [public PostgreSQL trust anchor](certs/README.md) for existing DSNs using `/etc/content/supabase-ca.crt`. Other providers are supported without that DSN option. The AP supervisor can use the configured public CA text for the Go delivery child's verified-TLS connection. No TLS verification bypass is needed.

Scheduling and retained slug claims require the documented migrations/grants and explicit activation flags. Slug redirects also require the website's resolver contract. Keep on-follow history replay off. Archives require an explicit reviewed enrollment window; a source deploy must never enroll existing content or broadcast history.

## Validation

```sh
go test -race ./...
go vet ./...
(cd internal/activitypub && npm ci --ignore-scripts && npm run check && npm test)
(cd internal/activitypub && node --test deploy/integrated/delivery-child.test.mjs deploy/staging/go-runner.test.mjs)
(cd internal/review/editor && bun install --frozen-lockfile && bun run build)
sh scripts/test-postgres.sh
# After installing Node dependencies, enable combined disposable-DB checks:
CONTENT_TEST_NODE=/absolute/path/to/node24 sh scripts/test-publishing.sh
```

Container/database/native-client scenarios remain opt-in. Review which tests ran rather than interpreting skipped tests as passes. Never reuse production URLs, user credentials or real content in a local test.
