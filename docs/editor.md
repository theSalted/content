# Editor replacement record

Implemented 2026-10-26 on branch `editor-tiptap`. The homemade Rich text/HTML editor is gone. The review page now uses self-hosted Tiptap 3/ProseMirror with Rich text, HTML and Markdown modes, one integrated toolbar and one segmented mode control. See [ADR 0010](adr/0010-review-editor-engine.md), [draft review](draft-review.md) and [the editor README](../internal/review/editor/README.md).

## Requirements and how they are met

- **Source authority.** The HTML source is the only submitted body. Modes write it only after an edit, so switching modes never rewrites saved source.
- **Explicit lossy conversion.** Conversions are compared with the source before display. Losses are listed and need Convert anyway; HTML mode keeps unsupported markup verbatim.
- **No silent normalization.** Escaping and other meaning-preserving differences are ignored by the comparison, not written back.
- **Failed saves and undo.** Saving keeps the editor and its history. Conflicts, validation errors and network failures keep the edits.
- **Server contracts.** Authentication, CSRF, optimistic concurrency, revisions, explicit publish and media authorization are unchanged. No ActivityPub files changed.

## Still unverified

- A physical iPhone in iOS Safari. Touch was tested with emulated viewports in Chrome and WebKit.
- Native iA Writer automatic image upload, and Ulysses publishing. Test these with synthetic data against the real API.
- Browser publication controls now include unpublish and recoverable Trash; see [publishing management](publishing-management.md).
