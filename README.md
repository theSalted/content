# content

A self-hosted publishing backend built with Go and PostgreSQL, with an internal Node/Fedify module for ActivityPub. Websites are separate consumers. Core storage and APIs do not require a hosting provider or frontend framework.

## Included

- Authenticated posts/pages list, search, filters and pagination; Tiptap rich text, HTML and Markdown editing.
- Explicit save, publish, unpublish and recoverable Trash; revision checks prevent stale writes.
- Editable slugs and publication dates, durable scheduled publishing, and retained slug claims for consumer redirects.
- A measured Ghost Admin/Content API subset, offline Ghost import, image uploads and private filesystem/S3-compatible storage.
- Durable publishing outbox, idempotent delivery, acknowledged public-object discovery and internal Fedify API/worker processes.
- Configurable PostgreSQL schemas, separate migration/runtime roles and persistent opaque browser sessions.

This is not full Ghost API parity or a stable Go library API. Client compatibility is measured by workflow; native iA Writer and Ulysses coverage is incomplete. Memberships, newsletters, payments and a theme builder are outside the current scope. Imported history does not automatically broadcast. On-follow history replay is disabled by default. Moderation APIs and data belong to the ActivityPub module, not the CMS editor.

## Run

Go 1.26.4 and PostgreSQL are required for the CMS. Node 24 is required only for ActivityPub. Browser assets are committed with their source, lockfile and dependency notices; rebuilding them uses Bun 1.3.13.

```sh
go build -o bin/content ./cmd/content
go test -race ./...
go vet ./...
```

Supply configuration from [.env.example](.env.example); the executable does not load dotenv files itself. Create an owned schema and restricted roles using your database administration process. Run `bin/content migrate` with the separate migration DSN, then `bin/content serve` with the runtime DSN. Migrations never run automatically on server startup. The default listener is `127.0.0.1:8080`; the owner UI is `/ghost/posts/` after `/ghost/login/`. Do not reuse synthetic test credentials.

[Docker source builds](deploy/README.md) include CMS, integrated ActivityPub API plus Go sender, and a separate ActivityPub worker. No database, credentials or public routes are provisioned by these builds. Scheduling, slug redirects, publication enrollment, federation and archive import each require deliberate configuration; keep historical replay off unless you explicitly want it.

## Documentation

| Topic | Reference |
| --- | --- |
| Configuration and API foundation | [Implementation](docs/implementation.md), [Ghost compatibility](docs/ghost-compatibility.md) |
| Editor and publishing | [Editor](docs/editor.md), [Publishing management](docs/publishing-management.md), [Post edits](docs/post-editing.md) |
| Media privacy and CDN boundary | [Media](docs/media.md), [Response projection](docs/contracts/owned-media-projection.md) |
| Import and federation | [Ghost import](docs/ghost-import.md), [Durable delivery](docs/publishing-delivery.md), [ActivityPub](internal/activitypub/README.md) |
| Database and security | [Roles](docs/database-roles.md), [Security](docs/security.md), [Operations](docs/operations.md) |
| Design history | [ADRs](docs/adr/README.md), [Architecture](docs/architecture.md) |

Architecture and milestone documents retain dated design/test history; newer implemented contracts and configuration take precedence over early proposed scope. Deployment-specific operator records, actual content, credentials, storage objects and private handoffs are not part of this repository.

## Tests

Plain Go/Node tests skip explicitly opt-in database, container and native-client scenarios. `scripts/test-postgres.sh` uses its own disposable local PostgreSQL container, never a live database. `scripts/test-publishing.sh` also exercises Go-to-Node contracts after installing the locked ActivityPub dependencies. See [source-build checks](deploy/README.md) for commands. Do not point test harnesses at production data.

## License

A project distribution license has not been selected. Public visibility does not itself grant a license to this project's code. Dependency licenses and notices are retained in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md), the [editor notices](internal/review/assets/editor-notices.md) and the ActivityPub dependency lockfile. No proprietary publishing-client implementation is included.
