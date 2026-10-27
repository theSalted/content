// Conversion and fidelity cases, run inside a real browser by run.js.
import { Editor } from '@tiptap/core';
import { extensions, toHTML, fromHTML, fromMarkdown, toMarkdown, displayImageSource } from '../../src/schema.js';
import { compare } from '../../src/fidelity.js';

const LOSSLESS = [
  '<p>Plain</p>',
  `<p>Don't "quote" &amp; a&nbsp;b</p>`,
  '<h2>Head</h2><p><b>B</b> <i>I</i> <strong><em>both</em></strong> <em><strong>nested</strong></em> <u>u</u> <s>s</s> <strike>k</strike> <code>c</code></p>',
  '<p><a href="https://example.invalid/" rel="nofollow">link</a> and <a href="/relative" rel="nofollow">relative</a></p>',
  '<ul><li>One</li><li>Two<ul><li>Nested</li></ul></li></ul><ol><li>Three</li></ol>',
  '<blockquote><p>Quote</p></blockquote><hr><pre><code>x &lt; y\n  indented</code></pre><pre>bare pre</pre>',
  '<p><img src="https://example.invalid/a.png" alt="Alt"></p>',
  '<p><img src="https://example.invalid/a.png" alt="Alt" /></p>',
  '<img src="/content/images/0123456789abcdef01234567/image.png" alt="">',
  '<table><thead><tr><th>H</th><th>I</th></tr></thead><tbody><tr><td>A</td><td>B</td></tr></tbody></table>',
  '<table><tr><td>Implied body</td></tr></table>',
  '<p>Line<br>break</p>',
  '<h2 id="intro" lang="en" dir="ltr">Attributes</h2><p title="Title">x</p>',
  '',
  '<p>   spaced    text  </p>\n\n<p>next</p>',
  '<p>emoji 🎉 and <a href="mailto:a@example.invalid" rel="nofollow">mail</a></p>',
  'Loose text <em>inline</em>',
  '<ul><li><p>Already wrapped</p></li></ul>',
  '<div><p>Bare div</p></div><p><span>bare span</span></p>',
];

const LOSSY = [
  ['<p>H<sub>2</sub>O</p>', '<sub>'],
  ['<p><abbr title="x">A</abbr></p>', '<abbr>'],
  ['<dl><dt>T</dt><dd>D</dd></dl>', '<dl>'],
  ['<p class="lead">x</p>', 'class on <p>'],
  ['<figure><img src="https://example.invalid/a.png" alt=""><figcaption>Cap</figcaption></figure>', '<figure>'],
  ['<table><caption>Cap</caption><tbody><tr><td>A</td></tr></tbody></table>', '<caption>'],
  ['<table><tbody><tr><td>A</td></tr></tbody><tfoot><tr><td>F</td></tr></tfoot></table>', '<tfoot>'],
  ['<p><del>gone</del></p>', '<del>'],
  ['<p><img src="data:image/png;base64,AAAA" alt="d"></p>', '<img>'],
  ['<p><a href="javascript:alert(1)">x</a></p>', '<a>'],
  ['<div id="wrap"><p>x</p></div>', 'id on <div>'],
  ['<p><strong lang="fr">oui</strong></p>', 'lang on <strong>'],
  ['<p><img src="/ghost/images/0123456789abcdef01234567/image.png" alt="proxy"></p>', '<img>'],
];

const MARKDOWN = [
  '<h2>Head</h2><p>Para with <strong>bold</strong>, <em>em</em> and <a href="https://example.invalid/" rel="nofollow">link</a>.</p>',
  '<ul><li>One</li><li>Two<ul><li>Nested</li></ul></li></ul><ol><li>A</li><li>B</li></ol>',
  '<blockquote><p>Q</p></blockquote><pre><code>code &lt;x&gt;\n  two</code></pre><hr><p>after</p>',
  '<p><img src="https://example.invalid/a.png" alt="Alt"></p>',
  '<table><thead><tr><th>H</th><th>I</th></tr></thead><tbody><tr><td>A</td><td>B</td></tr></tbody></table>',
  `<p>Don't escape * stars _ or [brackets] or #hash</p>`,
  '<p>Line<br>break</p>',
];

const TYPED_MARKDOWN = '# Title\n\nSome *em* and **strong** and [link](https://example.invalid/) and <https://example.invalid/auto>.\n\n' +
  '- a\n- b\n\n1. one\n2. two\n\n> quote\n\n```js\ncode\n```\n\n| A | B |\n|---|---|\n| 1 | 2 |\n\n![alt](https://example.invalid/i.png)\n';

window.runCases = () => {
  const host = document.createElement('div');
  document.body.append(host);
  const editor = new Editor({ element: host, extensions: extensions({ base: 'https://cms.example.invalid/' }), injectCSS: false });
  const results = [];
  const outputs = [];
  const check = (name, ok, detail) => results.push({ name, ok: Boolean(ok), detail });
  const imagePath = '/content/images/0123456789abcdef01234567/image.png';
  const oldImage = 'https://old.example.invalid' + imagePath;
  check('explicit media alias displays authenticated proxy', displayImageSource(oldImage, 'https://cms.example.invalid', ['https://old.example.invalid']) === '/ghost/images/0123456789abcdef01234567/image.png');
  for (const value of [oldImage, 'https://foreign.invalid' + imagePath, 'https://old.example.invalid.evil' + imagePath]) {
    check('unknown image origin is not rebound ' + value, displayImageSource(value, 'https://cms.example.invalid') === value);
  }
  for (const value of [oldImage+'?token=x', oldImage+'#x', 'https://user:pass@old.example.invalid'+imagePath]) {
    check('ambiguous aliased image is not rebound ' + value, displayImageSource(value, 'https://cms.example.invalid', ['https://old.example.invalid']) === value);
  }
  const rich = (html) => toHTML(editor.schema, fromHTML(editor.schema, html));
  const md = (html) => toMarkdown(editor, fromHTML(editor.schema, html));
  const mdBack = (text) => toHTML(editor.schema, fromMarkdown(editor, text));

  for (const html of LOSSLESS) {
    const out = rich(html);
    const report = compare(html, out);
    outputs.push(out);
    check('rich keeps ' + JSON.stringify(html.slice(0, 60)), report.equivalent, { out, losses: report.losses });
  }
  for (const [html, expected] of LOSSY) {
    const out = rich(html);
    const report = compare(html, out);
    outputs.push(out);
    check('rich reports ' + expected + ' in ' + JSON.stringify(html.slice(0, 50)), !report.equivalent && report.losses.some((l) => l.startsWith(expected)), { out, losses: report.losses });
  }
  for (const html of MARKDOWN) {
    const text = md(html);
    const back = mdBack(text);
    const report = compare(html, back);
    outputs.push(back);
    check('markdown round trip ' + JSON.stringify(html.slice(0, 50)), report.equivalent, { text, back, losses: report.losses });
  }
  const underline = '<p><u>under</u> and H<sub>2</sub>O</p>';
  const underlineReport = compare(underline, mdBack(md(underline)));
  check('markdown reports what it cannot keep', !underlineReport.equivalent, underlineReport.losses);

  const typed = mdBack(TYPED_MARKDOWN);
  outputs.push(typed);
  check('typed markdown converts', /<h1>Title<\/h1>/.test(typed) && /<a href="https:\/\/example.invalid\/" rel="nofollow">link<\/a>/.test(typed) && /<p><img /.test(typed) &&
    /<thead>/.test(typed) && /<img src="https:\/\/example.invalid\/i.png" alt="alt">/.test(typed) && !/class=|style=|target=/.test(typed), typed);

  const table = rich('<table><tr><td align="center">A</td><td colspan="2">B</td></tr></table>');
  check('table output stays in profile', !/style=|colgroup|class=/.test(table) && /align="center"/.test(table) && /colspan="2"/.test(table), table);
  const link = rich('<p><a href="https://example.invalid/" rel="noopener" target="_blank" class="x">x</a></p>');
  check('links always nofollow, never targeted', /<a href="https:\/\/example.invalid\/" rel="nofollow">x<\/a>/.test(link), link);
  outputs.push(table, link);

  editor.destroy();
  return { results, outputs };
};
