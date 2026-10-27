# ADR 0003: Preserve source and bound format conversion

Date: 2026-10-25. Status: proposed; Phase 0 can revise the exact node set.

## Context

Ghost uses Lexical and legacy clients may submit Mobiledoc or HTML/Markdown cards. Its mature converters are JavaScript. Pure Go and full Koenig rendering are competing scope demands.

## Decision

Retain immutable original submitted source with each revision. Use a versioned supported representation and renderer for known client formats. HTML source import and a small captured node/card set come first. Unknown destructive conversions fail atomically; migration-only imports can be quarantined without publication. Do not silently discard nodes, invent a general lossless converter or add a Node runtime to the CMS core.

## Consequences

Fidelity is inspectable and later conversion can improve without losing originals. General lexical editing, bidirectional Mobiledoc and arbitrary cards remain unclaimed. Sanitization can differ from Ghost and needs visible fixture diffs. If a required client mode cannot fit the supported subset, revisit scope before implementation expansion.

See [format matrix](../ghost-compatibility.md), [security](../security.md) and [revisions](../data-model.md).
