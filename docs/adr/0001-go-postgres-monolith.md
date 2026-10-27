# ADR 0001: Original Go/PostgreSQL monolith proposal

Date: 2026-10-25. Status: **partially superseded by [ADR 0008](0008-package-and-database-boundaries.md)**.

## Historical decision

The original headless CMS proposal used one Go process, PostgreSQL, disk media and bounded workers. One publication per database was suggested for isolation; inexpensive single-server hosting was considered, with managed PostgreSQL optional. A separate federation service was later treated as an external product boundary.

## Retained

Go for CMS, standard PostgreSQL, explicit transactions, durable publication events, configurable storage and no provider-specific auth/storage lock-in remain useful decisions.

## Superseded

Do not require a database per publication, a VPS, or a separate federation product. The accepted target is one reusable `content` package with CMS and ActivityPub modules. The current deployment uses Railway apps and one Supabase PostgreSQL database with owned schemas/roles. All-Go versus internal Node/Fedify for ActivityPub remains pending; the number of processes is not fixed here.

See [architecture](../architecture.md) and [operations](../operations.md). This historical ADR is not authorization to implement or provision its former topology.
