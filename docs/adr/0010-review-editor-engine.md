# ADR 0010: Review editor engine and source authority

Accepted and implemented 2026-10-26. Supersedes the homemade `execCommand` editing in [ADR 0005](0005-small-review-ui.md)'s page; the page's authentication, session and publishing decisions are unchanged.

## Decision

The review page's Rich text mode uses self-hosted, MIT-licensed Tiptap 3 on ProseMirror in vanilla JavaScript (StarterKit, Image, TableKit), with `@tiptap/markdown` for a Markdown mode. No hosted editor service, paid extension or consuming-website framework is involved. The bundle is built with Bun into an embedded asset; Go builds need no JavaScript toolchain.

The stored HTML source stays authoritative. Editor documents are views of it: a mode writes the source only after an edit in that mode, and conversions are compared against the source before display. Any detected loss requires an explicit Convert choice. Arbitrary HTML is never assumed to survive Rich text or Markdown.

Editor output is constrained to the CMS safe HTML profile. The profile check now compares decoded tokens instead of bytes, so escaping, attribute order and void-element syntax may differ from the sanitizer's own serialization; removed elements, attributes, characters or rewritten URLs still reject the write, and original bytes are stored.

## Consequences

Formatting, selection, undo, paste and tables come from a maintained engine instead of deprecated browser commands. Undo history survives mode switches and saves. Markdown support inherits Tiptap's beta status; unsupported structures are reported, not silently dropped. Ordinary prose with straight apostrophes or quotes can now be saved through every client, including the Ghost Admin API. The bundle adds about 170 KB gzipped, cached immutably. Native iOS Safari, native iA image upload and Ulysses still need real-client verification.

See [the editor README](../../internal/review/editor/README.md) and [draft review](../draft-review.md).
