// Source-fidelity checks for editor conversions.
//
// The saved HTML source is authoritative. Before an editing mode replaces the
// source with its own serialization, compare a canonical form of the source
// with the converted result. Differences that cannot change meaning (escaping,
// attribute order, b/strong aliases, implied paragraphs, whitespace collapsing
// and mark nesting order) are ignored. Anything else is reported so the author
// can make an explicit choice.

const ALIASES = { b: 'strong', i: 'em', strike: 's' };
const BLOCKS = new Set(('address article aside blockquote caption col colgroup dd details div dl dt fieldset figcaption ' +
  'figure footer form h1 h2 h3 h4 h5 h6 header hr li main nav ol p pre section summary table tbody td tfoot th thead tr ul').split(' '));
const TEXTBLOCKS = new Set('p h1 h2 h3 h4 h5 h6 pre dt figcaption caption summary'.split(' '));
const ATOMS = new Set(['br', 'img', 'hr', 'col', 'wbr']);
const SPACE = /[ \t\n\r\f]+/g;

function tagOf(el) {
  const name = el.localName;
  return ALIASES[name] || name;
}

function attributesOf(el) {
  const out = [];
  for (const attr of el.attributes) {
    if ((attr.name === 'colspan' || attr.name === 'rowspan') && attr.value.trim() === '1') continue;
    out.push([attr.name, attr.value]);
  }
  out.sort((a, b) => (a[0] < b[0] ? -1 : a[0] > b[0] ? 1 : 0));
  return out;
}

// Wrappers that carry no meaning of their own when they have no attributes.
function transparent(el) {
  const tag = el.localName;
  if (tag === 'tbody' || tag === 'thead') return true;
  return (tag === 'span' || tag === 'div') && el.attributes.length === 0;
}

function key(tag, attrs) {
  return attrs.length ? tag + '[' + attrs.map(([n, v]) => n + '=' + JSON.stringify(v)).join(' ') + ']' : tag;
}

class Canon {
  constructor() {
    this.features = new Map();
    this.text = [];
  }

  count(feature) {
    this.features.set(feature, (this.features.get(feature) || 0) + 1);
  }

  element(el) {
    const tag = tagOf(el);
    const attrs = attributesOf(el);
    this.count('<' + tag + '>');
    for (const [name] of attrs) this.count(name + ' on <' + tag + '>');
    return key(tag, attrs);
  }

  // Inline content of one textblock as a flat list of marked segments, so
  // nesting order of marks does not matter.
  inline(nodes, preformatted) {
    const segments = [];
    let space = true;
    const push = (text, marks) => {
      const last = segments[segments.length - 1];
      if (last && last.text !== undefined && last.marks === marks) last.text += text;
      else segments.push({ text, marks });
    };
    const walk = (node, marks) => {
      if (node.nodeType === 3) {
        let value = node.data;
        if (!preformatted) {
          value = value.replace(SPACE, ' ');
          if (space && value.startsWith(' ')) value = value.slice(1);
          if (value) space = value.endsWith(' ');
        }
        if (value) {
          push(value, marks);
          this.text.push(value);
        }
        return;
      }
      if (node.nodeType !== 1) return;
      if (ATOMS.has(node.localName)) {
        segments.push({ atom: this.element(node), marks });
        if (node.localName === 'br') space = true;
        return;
      }
      if (transparent(node)) {
        for (const child of node.childNodes) walk(child, marks);
        return;
      }
      const mark = this.element(node);
      const next = marks ? marks.split('\u0000').concat(mark).sort().join('\u0000') : mark;
      for (const child of node.childNodes) walk(child, next);
    };
    for (const node of nodes) walk(node, '');
    // Trailing whitespace at the end of a block is not significant.
    const last = segments[segments.length - 1];
    if (!preformatted && last && last.text !== undefined) {
      last.text = last.text.replace(/ $/, '');
      if (!last.text) segments.pop();
    }
    return segments.map((s) => (s.atom ? '{' + s.atom + '|' + s.marks + '}' : JSON.stringify(s.text) + '|' + s.marks)).join(',');
  }

  textblock(el) {
    const label = this.element(el);
    let nodes = el.childNodes;
    const pre = el.localName === 'pre';
    if (pre) {
      const elements = [...el.children];
      const onlyCode = elements.length === 1 && elements[0].localName === 'code' &&
        [...el.childNodes].every((n) => n === elements[0] || (n.nodeType === 3 && !n.data.trim()));
      if (onlyCode) nodes = elements[0].childNodes;
    }
    const content = this.inline(nodes, pre);
    if (label === 'p' && !content) return '';
    return label + '(' + content + ')';
  }

  // Children of a block container: runs of inline content become implied
  // paragraphs, which is how the editor schema represents them.
  children(parent) {
    const out = [];
    let run = [];
    const flush = () => {
      if (!run.length) return;
      const content = this.inline(run, false);
      if (content) out.push('p(' + content + ')');
      run = [];
    };
    const visit = (node) => {
      if (node.nodeType === 1 && transparent(node) && node.localName !== 'span') {
        flush();
        for (const child of node.childNodes) visit(child);
        flush();
        return;
      }
      if (node.nodeType === 1 && BLOCKS.has(node.localName)) {
        flush();
        const block = this.block(node);
        if (block) out.push(block);
        return;
      }
      if (node.nodeType === 3 || node.nodeType === 1) run.push(node);
    };
    for (const node of parent.childNodes) visit(node);
    flush();
    return out.join(',');
  }

  block(el) {
    if (TEXTBLOCKS.has(el.localName)) return this.textblock(el);
    const label = this.element(el);
    if (ATOMS.has(el.localName)) return label;
    return label + '[' + this.children(el) + ']';
  }
}

function parse(html) {
  const doc = new DOMParser().parseFromString('<!doctype html><html><body>' + html, 'text/html');
  return doc.body;
}

export function canonical(html) {
  const canon = new Canon();
  const form = canon.children(parse(html));
  return { form, features: canon.features, text: canon.text.join('').replace(/\s+/g, '') };
}

// compare(source, converted) => { equivalent, losses }
// losses lists features present in source but missing from converted, such as
// "<figure>" or "class on <p>", plus "text" or "structure" when needed.
export function compare(source, converted) {
  const a = canonical(source);
  const b = canonical(converted);
  if (a.form === b.form) return { equivalent: true, losses: [] };
  const losses = [];
  for (const [feature, count] of a.features) {
    const kept = b.features.get(feature) || 0;
    if (kept < count) losses.push(count - kept > 1 ? `${feature} ×${count - kept}` : feature);
  }
  if (a.text !== b.text) losses.push('text');
  if (!losses.length) losses.push('structure');
  return { equivalent: false, losses };
}
