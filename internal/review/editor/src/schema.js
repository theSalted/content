// Editor schema and conversions. Output must stay inside the CMS safe HTML
// profile (bluemonday UGC): links carry rel="nofollow" and no target, and no
// inline styles, classes or colgroups are emitted.
import { Extension, createDocument } from '@tiptap/core';
import { DOMSerializer } from '@tiptap/pm/model';
import StarterKit from '@tiptap/starter-kit';
import Link from '@tiptap/extension-link';
import CodeBlock from '@tiptap/extension-code-block';
import Image from '@tiptap/extension-image';
import { Table, TableRow, TableHeader, TableCell } from '@tiptap/extension-table';
import { Markdown } from '@tiptap/markdown';

const PRIVATE_IMAGE = /^\/content\/images\/([0-9a-f]{24})\/image\.(png|jpg)$/;

// Image sources the CMS can store: http(s) or relative, never this editor's
// own authenticated proxy path.
export function storableImageSource(value, base) {
  if (!value || /[\u0000- \u007f]/.test(value)) return false;
  try {
    const url = new URL(value, base);
    if (url.username || url.password || !['http:', 'https:'].includes(url.protocol)) return false;
    return !(url.origin === new URL(base).origin && url.pathname.startsWith('/ghost/'));
  } catch {
    return false;
  }
}

// Private CMS images display through the authenticated review proxy.
export function displayImageSource(value, base, mediaOrigins = []) {
  try {
    const url = new URL(value, base);
    const trusted = url.origin === new URL(base).origin || mediaOrigins.includes(url.origin);
    const match = trusted && !url.username && !url.password && !url.search && !url.hash && url.pathname.match(PRIVATE_IMAGE);
    return match ? `/ghost/images/${match[1]}/image.${match[2]}` : value;
  } catch {
    return value;
  }
}

export function safeLink(value) {
  if (!value || /[\u0000- \u007f]/.test(value)) return false;
  if (/^(\/(?!\/)|#|\?|\.\.?\/)/.test(value)) return true;
  try {
    const url = new URL(value);
    return !url.username && !url.password && ['http:', 'https:', 'mailto:'].includes(url.protocol);
  } catch {
    return false;
  }
}

const preserved = (name) => ({
  default: null,
  parseHTML: (element) => element.getAttribute(name),
  renderHTML: (attributes) => (attributes[name] == null ? {} : { [name]: attributes[name] }),
});

// Standard attributes the CMS profile accepts on block content.
const PreservedAttributes = Extension.create({
  name: 'preservedAttributes',
  addGlobalAttributes() {
    return [{
      types: ['paragraph', 'heading', 'blockquote', 'bulletList', 'orderedList', 'listItem', 'codeBlock', 'table', 'tableRow', 'tableCell', 'tableHeader', 'horizontalRule'],
      attributes: { id: preserved('id'), title: preserved('title'), lang: preserved('lang'), dir: preserved('dir') },
    }];
  },
});

const ProfileLink = Link.extend({
  addAttributes() {
    const parent = this.parent?.() || {};
    return {
      href: parent.href,
      ...parent,
      rel: { default: 'nofollow', parseHTML: () => 'nofollow' },
      target: { default: null, parseHTML: () => null, renderHTML: () => ({}) },
      class: { default: null, parseHTML: () => null, renderHTML: () => ({}) },
    };
  },
  renderHTML({ HTMLAttributes }) {
    const { href, rel, target, class: className, ...rest } = HTMLAttributes;
    return ['a', safeLink(href) ? { href, ...rest, rel: 'nofollow' } : { ...rest, rel: 'nofollow' }, 0];
  },
}).configure({
  openOnClick: false,
  autolink: true,
  linkOnPaste: true,
  defaultProtocol: 'https',
  HTMLAttributes: { target: null, rel: 'nofollow', class: null },
  isAllowedUri: (url) => safeLink(url),
  shouldAutoLink: (url) => safeLink(url) || safeLink('https://' + url),
});

// Code language classes are outside the profile. A Markdown fence keeps its
// language while editing Markdown, but HTML output has no class.
const ProfileCodeBlock = CodeBlock.extend({
  addAttributes() {
    return { language: { default: null, parseHTML: () => null, rendered: false } };
  },
  renderHTML({ HTMLAttributes }) {
    return ['pre', HTMLAttributes, ['code', 0]];
  },
});

function profileImage(base, display) {
  return Image.extend({
    parseHTML() {
      return [{ tag: 'img[src]', getAttrs: (el) => (storableImageSource(el.getAttribute('src'), base) ? null : false) }];
    },
    addNodeView() {
      if (!display) return null;
      return ({ node }) => {
        const dom = document.createElement('img');
        const apply = (n) => {
          for (const name of ['alt', 'title', 'width', 'height']) {
            if (n.attrs[name] == null) dom.removeAttribute(name);
            else dom.setAttribute(name, n.attrs[name]);
          }
          dom.setAttribute('src', display(n.attrs.src));
        };
        apply(node);
        return {
          dom,
          update: (next) => {
            if (next.type !== node.type) return false;
            apply(next);
            return true;
          },
        };
      };
    },
  }).configure({ inline: true, allowBase64: false });
}

const ProfileTable = Table.extend({
  renderHTML({ HTMLAttributes }) {
    const { style, ...attributes } = HTMLAttributes;
    return ['table', attributes, ['tbody', 0]];
  },
}).configure({ resizable: false, renderWrapper: false });

const profileCell = (cell) => cell.extend({
  addAttributes() {
    return {
      ...this.parent?.(),
      colwidth: { default: null, parseHTML: () => null, rendered: false },
      align: {
        default: null,
        parseHTML: (el) => (['left', 'right', 'center'].includes(el.getAttribute('align')) ? el.getAttribute('align') : null),
        renderHTML: (attributes) => (attributes.align ? { align: attributes.align } : {}),
      },
    };
  },
});

export function extensions({ base, display } = {}) {
  return [
    StarterKit.configure({ link: false, codeBlock: false, trailingNode: false }),
    ProfileLink,
    ProfileCodeBlock,
    profileImage(base, display),
    ProfileTable,
    TableRow,
    profileCell(TableHeader),
    profileCell(TableCell),
    PreservedAttributes,
    Markdown.configure({ indentation: { style: 'space', size: 2 } }),
  ];
}

// Leading all-header rows go back into <thead>, matching common source tables.
function restoreTableHeads(container) {
  const document = container.ownerDocument;
  for (const body of container.querySelectorAll('table > tbody')) {
    const head = [];
    for (const row of body.children) {
      if (row.localName === 'tr' && row.children.length && [...row.children].every((c) => c.localName === 'th')) head.push(row);
      else break;
    }
    if (!head.length) continue;
    const thead = document.createElement('thead');
    thead.append(...head);
    body.before(thead);
    if (!body.children.length) body.remove();
  }
}

// Serialize into a document without a browsing context: elements built in the
// live page would start loading their images.
let inert = null;
function serialize(schema, fragment) {
  inert ||= document.implementation.createHTMLDocument('');
  const container = inert.createElement('div');
  container.append(DOMSerializer.fromSchema(schema).serializeFragment(fragment, { document: inert }));
  restoreTableHeads(container);
  return container.innerHTML;
}

export function toHTML(schema, doc) {
  return serialize(schema, doc.content);
}

export function fromHTML(schema, html) {
  return createDocument(html, schema);
}

// The Markdown parser can place inline nodes (images) directly in the
// document; normalize such content through the schema's HTML parser.
export function fromMarkdown(editor, markdown) {
  const doc = editor.schema.nodeFromJSON(editor.markdown.parse(markdown));
  try {
    doc.check();
    return doc;
  } catch {
    return fromHTML(editor.schema, serialize(editor.schema, doc.content));
  }
}

export function toMarkdown(editor, doc) {
  return editor.markdown.serialize(doc.toJSON());
}
