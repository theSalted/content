# Review editor

Browser editor for the native review page (`internal/review`). It is built into `../assets/editor.js`, which the Go binary embeds and serves at a content-addressed `/ghost/assets/editor-<hash>.js`. Go builds need no JavaScript toolchain; rebuild the asset only when this directory changes.

## Modes and source fidelity

The page has one bordered editor with a Rich text | HTML | Markdown control and, in Rich text, a formatting toolbar in its header. The `#body` textarea holds the authoritative HTML source and is the only submitted body field.

- Rich text is [Tiptap 3](https://tiptap.dev/docs/editor/getting-started/install/vanilla-javascript) on ProseMirror (StarterKit, Image, TableKit) in vanilla JavaScript. Markdown uses `@tiptap/markdown`, which Tiptap labels beta.
- A mode writes to the source only after an edit in that mode. Switching modes, or editing and then undoing back, leaves the saved source byte for byte.
- Before showing a source in Rich text or Markdown, the editor converts it and compares a canonical form of both (`src/fidelity.js`). Escaping, attribute order, `b`/`strong`-style aliases, implied paragraphs, whitespace and mark nesting are ignored. Anything else, such as `<sub>`, `<dl>`, a dropped attribute or changed text, is listed in a dialog, and the author must choose Convert anyway. Viewing a lossy conversion still changes nothing until the author edits.
- Output stays inside the CMS safe HTML profile (bluemonday UGC): links are `href` plus `rel="nofollow"` and never targeted, code blocks carry no language class, tables carry no styles or `colgroup`, and leading header rows return to `<thead>`.
- Private CMS images display through the authenticated `/ghost/images/` proxy while the source keeps `/content/images/` URLs. Serialization runs in an inert document so no image loads as a side effect.
- Saving keeps the page, editor instance and undo history. Only the revision token, status buttons and notices come from the response. Conflicts (409), validation errors and network failures keep the edits in place, and leaving with unsaved edits asks for confirmation.

No arbitrary HTML-to-Markdown or HTML-to-Rich-text conversion is lossless, and none is claimed. Unsupported HTML stays editable in HTML mode.

## Build and test

```sh
cd internal/review/editor
bun install --frozen-lockfile
bun run build                      # writes ../assets/editor.js
bun scripts/notices.js             # writes ../assets/editor-notices.md
node test/browser/run.js           # installed Google Chrome
BROWSER=webkit node test/browser/run.js
```

WebKit needs a one-time `node node_modules/playwright-core/cli.js install webkit`. The browser tests build `devserver/`, a loopback-only review server with synthetic in-memory posts and one generated private image. They run 43 conversion cases, check every output against the real CMS validator (`devserver -validate`), then drive the page: verbatim mode switching, undo restoring the exact source, toolbar and link dialog, save and reload, a stale-tab conflict, explicit lossy conversion, Markdown saves, tables, paste sanitizing, publish, and a touch viewport.

## Dependencies

Versions are exact. Every runtime package was at least two weeks old when pinned; `overrides` hold three transitive packages back for that reason. All bundled packages are MIT licensed; `scripts/notices.js` regenerates their license texts from the build metafile. `playwright-core` (Apache-2.0) is a test-only dependency and is not bundled.
