// Review editor: Rich text (Tiptap/ProseMirror), HTML and Markdown modes over
// one authoritative HTML source. The #body textarea is the only submitted
// body field. A mode writes to it only after the author edits in that mode,
// so switching modes never rewrites saved source.
import { Editor, Extension } from '@tiptap/core';
import { EditorState } from '@tiptap/pm/state';
import { extensions, toHTML, fromHTML, fromMarkdown, toMarkdown, storableImageSource, displayImageSource, safeLink } from './schema.js';
import { compare } from './fidelity.js';

const MODES = { rich: 'Rich text', html: 'HTML', markdown: 'Markdown' };
const MODE_KEY = 'content-editor-mode';

function storedMode() {
  try {
    const value = localStorage.getItem(MODE_KEY);
    return value in MODES ? value : 'rich';
  } catch {
    return 'rich';
  }
}

function rememberMode(value) {
  try {
    localStorage.setItem(MODE_KEY, value);
  } catch {
    // Storage can be unavailable; the mode is a convenience only.
  }
}

const SVG = 'http://www.w3.org/2000/svg';
const ICONS = {
  code: ['M9 8l-4 4 4 4', 'M15 8l4 4-4 4'],
  link: ['M10 14a4 4 0 0 0 5.66 0l3-3a4 4 0 0 0-5.66-5.66l-1 1', 'M14 10a4 4 0 0 0-5.66 0l-3 3a4 4 0 0 0 5.66 5.66l1-1'],
  bullets: ['M9 7h11', 'M9 12h11', 'M9 17h11', 'M4.5 7h.01', 'M4.5 12h.01', 'M4.5 17h.01'],
  numbers: ['M10 7h10', 'M10 12h10', 'M10 17h10', 'M4 5.5h1.5v4', 'M4 9.5h3', 'M4 14.5h3l-3 4h3'],
  quote: ['M5 11h4v6H5z', 'M5 11c0-3 1-5 4-6', 'M14 11h4v6h-4z', 'M14 11c0-3 1-5 4-6'],
  codeblock: ['M4 5h16v14H4z', 'M10 10l-2 2 2 2', 'M14 10l2 2-2 2'],
  rule: ['M4 12h16'],
  image: ['M4 5h16v14H4z', 'M4 16l5-5 4 4 2-2 5 5', 'M15 9h.01'],
  table: ['M4 5h16v14H4z', 'M4 10h16', 'M4 15h16', 'M10 5v14'],
  undo: ['M9 14L4 9l5-5', 'M4 9h10a6 6 0 0 1 0 12h-3'],
  redo: ['M15 14l5-5-5-5', 'M20 9H10a6 6 0 0 0 0 12h3'],
};

function icon(name) {
  const svg = document.createElementNS(SVG, 'svg');
  svg.setAttribute('viewBox', '0 0 24 24');
  svg.setAttribute('aria-hidden', 'true');
  svg.setAttribute('focusable', 'false');
  for (const d of ICONS[name]) {
    const path = document.createElementNS(SVG, 'path');
    path.setAttribute('d', d);
    svg.append(path);
  }
  return svg;
}

// Toolbar groups. `run` receives a command chain; `active` names a mark/node.
const TOOLS = [
  [
    { name: 'Bold', text: 'B', className: 'tool-bold', run: (c) => c.toggleBold(), active: ['bold'] },
    { name: 'Italic', text: 'I', className: 'tool-italic', run: (c) => c.toggleItalic(), active: ['italic'] },
    { name: 'Underline', text: 'U', className: 'tool-underline', run: (c) => c.toggleUnderline(), active: ['underline'] },
    { name: 'Strikethrough', text: 'S', className: 'tool-strike', run: (c) => c.toggleStrike(), active: ['strike'] },
    { name: 'Inline code', icon: 'code', run: (c) => c.toggleCode(), active: ['code'] },
    { name: 'Link', icon: 'link', dialog: 'link', active: ['link'] },
  ],
  [
    { name: 'Heading 2', text: 'H2', run: (c) => c.toggleHeading({ level: 2 }), active: ['heading', { level: 2 }] },
    { name: 'Heading 3', text: 'H3', run: (c) => c.toggleHeading({ level: 3 }), active: ['heading', { level: 3 }] },
    { name: 'Bulleted list', icon: 'bullets', run: (c) => c.toggleBulletList(), active: ['bulletList'] },
    { name: 'Numbered list', icon: 'numbers', run: (c) => c.toggleOrderedList(), active: ['orderedList'] },
    { name: 'Quote', icon: 'quote', run: (c) => c.toggleBlockquote(), active: ['blockquote'] },
    { name: 'Code block', icon: 'codeblock', run: (c) => c.toggleCodeBlock(), active: ['codeBlock'] },
    { name: 'Divider', icon: 'rule', run: (c) => c.setHorizontalRule() },
  ],
  [
    { name: 'Image', icon: 'image', dialog: 'image' },
    { name: 'Table', icon: 'table', run: (c) => c.insertTable({ rows: 3, cols: 3, withHeaderRow: true }) },
  ],
  [
    { name: 'Add row', text: '+ Row', table: true, run: (c) => c.addRowAfter() },
    { name: 'Add column', text: '+ Column', table: true, run: (c) => c.addColumnAfter() },
    { name: 'Delete row', text: '− Row', table: true, run: (c) => c.deleteRow() },
    { name: 'Delete column', text: '− Column', table: true, run: (c) => c.deleteColumn() },
    { name: 'Header row', text: 'Header', table: true, run: (c) => c.toggleHeaderRow() },
    { name: 'Delete table', text: 'Delete table', table: true, run: (c) => c.deleteTable() },
  ],
  [
    { name: 'Undo', icon: 'undo', run: (c) => c.undo() },
    { name: 'Redo', icon: 'redo', run: (c) => c.redo() },
  ],
];

// Parse only the <main> of a response page in an inert template. Parsing the
// whole page would evaluate its <style> against this page's CSP nonce.
function parseMain(text) {
  const start = text.indexOf('<main');
  const end = text.lastIndexOf('</main>');
  if (start < 0 || end < start) return null;
  const template = document.createElement('template');
  template.innerHTML = text.slice(start, end + '</main>'.length);
  return template.content.querySelector('main');
}

function lossLabel(loss) {
  if (loss === 'text') return 'Some text';
  if (loss === 'structure') return 'Layout or nesting';
  return loss;
}

function init(main) {
  const form = main.querySelector('#post-form');
  if (!form) return null;
  const source = form.querySelector('#body');
  const markdown = form.querySelector('#markdown');
  const richHost = form.querySelector('#rich-body');
  const bar = form.querySelector('.editor-bar');
  const tools = bar.querySelector('.tools');
  const modeButtons = [...bar.querySelectorAll('[data-mode]')];
  const title = form.querySelector('#title');
  const metadata = () => ({ slug: main.querySelector('#slug')?.value ?? '', published: main.querySelector('#published_time')?.value ?? '', scheduled: main.querySelector('#scheduled_time')?.value ?? '' });
  const linkDialog = main.querySelector('#link-dialog');
  const imageDialog = main.querySelector('#image-dialog');
  const convertDialog = main.querySelector('#convert-dialog');
  const base = location.href;
  let mediaOrigins = [];
  try {
    const configured = JSON.parse(form.dataset.mediaOrigins || "[]");
    if (Array.isArray(configured) && configured.length <= 9) mediaOrigins = configured.filter((value) => typeof value === "string");
  } catch { /* Old or invalid configuration cannot expand trusted origins. */ }

  let mode = 'html';
  let saving = false;
  let richTouched = false;
  let markdownTouched = false;
  let richEntry = null; // { source, json } when Rich text was last loaded
  let markdownEntry = null; // { source, text }
  let richLeft = null; // source when Rich text was left with its state intact
  let markdownLeft = null;
  let saved = { html: source.value, title: title.value, ...metadata() };

  function message(text) {
    let box = form.querySelector('#editor-error');
    if (!box) {
      box = document.createElement('p');
      box.id = 'editor-error';
      box.className = 'error';
      box.setAttribute('role', 'alert');
      form.prepend(box);
    }
    box.textContent = text;
  }

  function clearMessage() {
    form.querySelector('#editor-error')?.remove();
  }

  const Shortcuts = Extension.create({
    name: 'reviewShortcuts',
    addKeyboardShortcuts() {
      return { 'Mod-k': () => (openLink(), true) };
    },
  });

  const editor = new Editor({
    element: richHost,
    extensions: [...extensions({ base, display: (value) => displayImageSource(value, base, mediaOrigins) }), Shortcuts],
    injectCSS: false,
    editorProps: {
      attributes: { class: 'rich', role: 'textbox', 'aria-multiline': 'true', 'aria-label': 'Body', spellcheck: 'true' },
      handlePaste: (_view, event) => fileOnly(event.clipboardData),
      handleDrop: (_view, event) => fileOnly(event.dataTransfer),
    },
    onUpdate: () => {
      richTouched = true;
    },
    onTransaction: () => scheduleToolbar(),
  });

  function fileOnly(data) {
    if (!data || !data.files || !data.files.length) return false;
    if (data.getData('text/html') || data.getData('text/plain')) return false;
    message('Insert images by URL.');
    return true;
  }

  function resetRich(doc) {
    editor.view.updateState(EditorState.create({ doc, plugins: editor.state.plugins }));
    richTouched = false;
    scheduleToolbar();
  }

  // Write the current mode's edits to the source. Unchanged content restores
  // the exact source the mode was entered with.
  function flush() {
    if (mode === 'rich' && richTouched) {
      const json = JSON.stringify(editor.getJSON());
      source.value = json === richEntry.json ? richEntry.source : toHTML(editor.schema, editor.state.doc);
      richTouched = false;
    } else if (mode === 'markdown' && markdownTouched) {
      const text = markdown.value;
      source.value = text === markdownEntry.text ? markdownEntry.source : toHTML(editor.schema, fromMarkdown(editor, text));
      markdownTouched = false;
    }
  }

  function confirmLoss(target, losses) {
    convertDialog.querySelector('#convert-title').textContent = `${target} can’t keep`;
    const list = convertDialog.querySelector('#convert-losses');
    list.replaceChildren(...losses.map((loss) => {
      const item = document.createElement('li');
      item.textContent = lossLabel(loss);
      return item;
    }));
    convertDialog.returnValue = '';
    convertDialog.showModal();
    return new Promise((resolve) => {
      convertDialog.addEventListener('close', () => resolve(convertDialog.returnValue === 'convert'), { once: true });
    });
  }

  async function switchMode(next, { prompt = true } = {}) {
    if (next === mode || saving) return;
    try {
      flush();
    } catch {
      message('This Markdown could not be converted. Your text remains here.');
      return;
    }
    const current = source.value;
    try {
      if (next === 'rich' && richLeft !== current) {
        const doc = fromHTML(editor.schema, current);
        const report = compare(current, toHTML(editor.schema, doc));
        if (!report.equivalent && !(prompt && (await confirmLoss(MODES.rich, report.losses)))) return;
        resetRich(doc);
        richEntry = { source: current, json: JSON.stringify(editor.getJSON()) };
      } else if (next === 'markdown' && markdownLeft !== current) {
        const text = toMarkdown(editor, fromHTML(editor.schema, current));
        const report = compare(current, toHTML(editor.schema, fromMarkdown(editor, text)));
        if (!report.equivalent && !(prompt && (await confirmLoss(MODES.markdown, report.losses)))) return;
        markdown.value = text;
        markdownEntry = { source: current, text };
        markdownTouched = false;
      }
    } catch {
      message(`This content could not be opened in ${MODES[next]}. Your source is unchanged.`);
      return;
    }
    clearMessage();
    if (mode === 'rich') richLeft = current;
    if (mode === 'markdown') markdownLeft = current;
    mode = next;
    source.hidden = mode !== 'html';
    markdown.hidden = mode !== 'markdown';
    richHost.hidden = mode !== 'rich';
    tools.hidden = mode !== 'rich';
    for (const button of modeButtons) button.setAttribute('aria-pressed', String(button.dataset.mode === mode));
    const area = mode === 'html' ? source : mode === 'markdown' ? markdown : null;
    if (area) fit(area);
    if (prompt) {
      rememberMode(mode);
      if (area) {
        area.setSelectionRange(0, 0);
        area.focus({ preventScroll: true });
      } else editor.commands.focus();
    }
    scheduleToolbar();
  }

  // Text areas grow with their content so long posts scroll with the page
  // and the toolbar stays in view.
  function fit(area) {
    if (area.hidden) return;
    area.style.height = 'auto';
    area.style.height = area.scrollHeight + 2 + 'px';
  }
  for (const area of [source, markdown]) area.addEventListener('input', () => fit(area));
  let resizeFrame = 0;
  function onResize() {
    cancelAnimationFrame(resizeFrame);
    resizeFrame = requestAnimationFrame(() => { fit(mode === 'markdown' ? markdown : source); bar.style.top = Math.ceil(main.querySelector('#post-controls')?.getBoundingClientRect().height || 0) + 'px'; });
  }
  addEventListener('resize', onResize);
  const headerObserver = new ResizeObserver(onResize);
  function observeHeader() { headerObserver.disconnect(); const header = main.querySelector('#post-controls'); if (header) headerObserver.observe(header); onResize(); }
  observeHeader();

  // Toolbar.
  const buttons = [];
  for (const group of TOOLS) {
    const box = document.createElement('div');
    box.className = 'tool-group';
    if (group[0].table) box.dataset.table = '';
    for (const tool of group) {
      const button = document.createElement('button');
      button.type = 'button';
      button.className = 'tool' + (tool.className ? ' ' + tool.className : '') + (tool.text && tool.text.length > 2 ? ' wide' : '');
      button.setAttribute('aria-label', tool.name);
      button.title = tool.name;
      if (tool.icon) button.append(icon(tool.icon));
      else button.textContent = tool.text;
      if (tool.active) button.setAttribute('aria-pressed', 'false');
      // Keep focus and selection in the document, including on touch devices.
      button.addEventListener('mousedown', (event) => event.preventDefault());
      button.addEventListener('click', () => {
        if (tool.dialog === 'link') openLink();
        else if (tool.dialog === 'image') openImage();
        else tool.run(editor.chain().focus()).run();
      });
      buttons.push({ tool, button });
      box.append(button);
    }
    tools.append(box);
  }
  const tableGroup = tools.querySelector('[data-table]');

  let toolbarFrame = 0;
  function scheduleToolbar() {
    if (toolbarFrame) return;
    toolbarFrame = requestAnimationFrame(() => {
      toolbarFrame = 0;
      if (mode !== 'rich' || editor.isDestroyed) return;
      const inTable = editor.isActive('table');
      tableGroup.hidden = !inTable;
      for (const { tool, button } of buttons) {
        if (tool.active) button.setAttribute('aria-pressed', String(editor.isActive(...tool.active)));
        let enabled = !saving;
        if (enabled && tool.run) enabled = tool.run(editor.can().chain()).run();
        if (enabled && tool.dialog === 'image') enabled = editor.can().setImage({ src: 'https://example.invalid/' });
        button.disabled = !enabled;
      }
    });
  }

  // Dialogs.
  function dialogError(dialog, text) {
    const box = dialog.querySelector('.dialog-error');
    box.textContent = text;
    box.hidden = !text;
  }

  function openLink() {
    if (mode !== 'rich' || saving) return;
    const href = editor.getAttributes('link').href || '';
    const empty = editor.state.selection.empty;
    linkDialog.querySelector('#link-url').value = href;
    linkDialog.querySelector('#link-text').value = '';
    linkDialog.querySelector('.link-text').hidden = !(empty && !href);
    linkDialog.querySelector('[data-remove]').hidden = !href;
    linkDialog.querySelector('[value=confirm]').textContent = href ? 'Update' : 'Insert';
    dialogError(linkDialog, '');
    linkDialog.returnValue = '';
    linkDialog.showModal();
  }

  linkDialog.addEventListener('close', () => {
    const action = linkDialog.returnValue;
    const chain = editor.chain().focus();
    if (action === 'remove') chain.extendMarkRange('link').unsetLink().run();
    else if (action === 'confirm') {
      const href = linkDialog.querySelector('#link-url').value.trim();
      const text = linkDialog.querySelector('#link-text').value;
      if (editor.state.selection.empty && !editor.isActive('link')) {
        chain.insertContent({ type: 'text', text: text || href, marks: [{ type: 'link', attrs: { href } }] }).run();
      } else chain.extendMarkRange('link').setLink({ href }).run();
    } else chain.run();
  });

  linkDialog.querySelector('form').addEventListener('submit', (event) => {
    if (event.submitter && event.submitter.value !== 'confirm') return;
    if (!safeLink(linkDialog.querySelector('#link-url').value.trim())) {
      event.preventDefault();
      dialogError(linkDialog, 'Enter an http, https, mailto or relative link.');
    }
  });

  function openImage() {
    if (mode !== 'rich' || saving) return;
    imageDialog.querySelector('#image-url').value = '';
    imageDialog.querySelector('#image-alt').value = '';
    dialogError(imageDialog, '');
    imageDialog.returnValue = '';
    imageDialog.showModal();
  }

  imageDialog.addEventListener('close', () => {
    const chain = editor.chain().focus();
    if (imageDialog.returnValue === 'confirm') {
      const src = imageDialog.querySelector('#image-url').value.trim();
      const alt = imageDialog.querySelector('#image-alt').value;
      chain.setImage({ src, alt: alt || null }).run();
    } else chain.run();
  });

  imageDialog.querySelector('form').addEventListener('submit', (event) => {
    if (event.submitter && event.submitter.value !== 'confirm') return;
    if (!storableImageSource(imageDialog.querySelector('#image-url').value.trim(), base)) {
      event.preventDefault();
      dialogError(imageDialog, 'Enter an http or https image URL.');
    }
  });

  for (const dialog of [linkDialog, imageDialog, convertDialog]) {
    dialog.querySelector('[data-close]').addEventListener('click', () => dialog.close('cancel'));
  }
  linkDialog.querySelector('[data-remove]').addEventListener('click', () => linkDialog.close('remove'));

  for (const button of modeButtons) button.addEventListener('click', () => switchMode(button.dataset.mode));
  markdown.addEventListener('input', () => {
    markdownTouched = true;
  });


  function setPublishTiming(later) {
    const dialog = main.querySelector('#publish-dialog');
    if (!dialog) return;
    const input = dialog.querySelector('#scheduled_time');
    const fields = dialog.querySelector('[data-schedule-fields]');
    const confirm = dialog.querySelector('[data-publish-confirm]');
    later = later && !!input;
    if (input) { input.disabled = !later; input.required = later; fields.hidden = !later; }
    confirm.value = later ? 'schedule' : 'publish';
    confirm.textContent = later ? 'Schedule' : 'Publish now';
    for (const radio of dialog.querySelectorAll('[name=publish_timing]')) radio.checked = radio.value === (later ? 'later' : 'now');
  }
  function closePublish() {
    const dialog = main.querySelector('#publish-dialog');
    if (!dialog) return;
    const input = dialog.querySelector('#scheduled_time');
    if (input) input.value = form.dataset.scheduleTime || '';
    setPublishTiming(false);
    dialog.close();
  }
  main.addEventListener('click', (event) => {
    if (saving || !(event.target instanceof Element)) return;
    if (event.target.closest('[data-settings-open]')) main.querySelector('#settings-dialog').showModal();
    if (event.target.closest('[data-settings-close]')) main.querySelector('#settings-dialog').close();
    if (event.target.closest('[data-publish-open]')) {
      const dialog = main.querySelector('#publish-dialog');
      if (!dialog) return;
      const input = dialog.querySelector('#scheduled_time');
      setPublishTiming(form.dataset.status === 'scheduled');
      if (input) input.min = new Date(Math.ceil((Date.now() + 60000) / 60000) * 60000).toISOString().slice(0, 16);
      dialog.showModal();
    }
    if (event.target.closest('[data-publish-close]')) closePublish();
  });
  main.addEventListener('change', (event) => {
    if (event.target.matches('[name=publish_timing]')) setPublishTiming(event.target.value === 'later');
  });
  main.addEventListener('cancel', (event) => {
    if (event.target.id === 'publish-dialog') { event.preventDefault(); closePublish(); }
  }, true);
  // Saving keeps this page, its editor state and undo history; only the
  // revision token, status controls and notices come from the response.
  function freeze(on) {
    saving = on;
    for (const el of main.querySelectorAll('button')) el.disabled = on;
    for (const el of main.querySelectorAll('input, textarea')) el.readOnly = on;
    editor.setEditable(!on, false);
    if (!on) scheduleToolbar();
  }

  function showNotices(next) {
    for (const old of main.querySelectorAll(':scope > .notice, :scope > .error')) old.remove();
    clearMessage();
    const notices = [...next.querySelectorAll(':scope > .notice, :scope > .error')].map((n) => document.importNode(n, true));
    form.before(...notices);
    const first = notices[0];
    if (first) {
      first.tabIndex = -1;
      first.focus({ preventScroll: false });
    }
  }

  form.addEventListener('submit', async (event) => {
    event.preventDefault();
    if (saving) return;
    try {
      flush();
    } catch {
      message('This Markdown could not be converted. Your text remains here.');
      return;
    }
    // Only the explicit Schedule action sends the proposed date.
    const dueField = main.querySelector('#scheduled_time');
    if (dueField && event.submitter?.value !== 'schedule') dueField.disabled = true;
    const fields = new URLSearchParams(new FormData(form));
    if (event.submitter && event.submitter.name) fields.set(event.submitter.name, event.submitter.value);
    const html = fields.get('html');
    const sentTitle = fields.get('title');
    const sentMetadata = metadata();
    freeze(true);
    try {
      const response = await fetch(form.getAttribute('action'), {
        method: 'POST',
        headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
        body: fields.toString(),
        credentials: 'same-origin',
        redirect: 'error',
      });
      if (response.status === 401) {
        message('Session expired. Sign in in another tab, then save again. Your edits remain here.');
        return;
      }
      const next = parseMain(await response.text());
      const nextForm = next && next.querySelector('#post-form');
      if (!nextForm) {
        message('Save not confirmed. Your edits remain here. Check the latest saved version before retrying.');
        return;
      }
      if (response.ok && (nextForm.querySelector('#body').value !== html || nextForm.querySelector('#title').value !== sentTitle)) {
        // The server stored something other than what was sent; show it.
        replace(next);
        return;
      }
      for (const dialog of main.querySelectorAll('#settings-dialog, #publish-dialog')) if (dialog.open) dialog.close();
      showNotices(next);
      if (response.ok) {
        for (const field of main.querySelectorAll('[name=updated_at]')) field.value = nextForm.querySelector('[name=updated_at]').value;
        for (const id of ['post-controls', 'settings-dialog', 'publish-dialog']) {
          const old = main.querySelector('#' + id);
          const fresh = next.querySelector('#' + id);
          if (old && fresh) old.replaceWith(document.importNode(fresh, true));
          else if (old) old.remove();
          else if (fresh) form.after(document.importNode(fresh, true));
        }
        for (const key of ['status', 'scheduleTime', 'originalSlug', 'hasPublished']) form.dataset[key] = nextForm.dataset[key];
        observeHeader();
        saved = { html, title: sentTitle, ...metadata() };
      }
    } catch {
      message('Connection interrupted. Your edits remain here. Check the latest saved version before retrying; the save may have completed.');
    } finally {
      if (!editor.isDestroyed) freeze(false);
    }
  });

  function replace(next) {
    editor.destroy();
    removeEventListener('beforeunload', guard);
    removeEventListener('resize', onResize);
    headerObserver.disconnect();
    const fresh = document.importNode(next, true);
    main.replaceWith(fresh);
    init(fresh);
  }

  function guard(event) {
    if (saving) return;
    try {
      flush();
    } catch {
      // Unconvertible Markdown is still an unsaved edit.
      event.preventDefault();
      event.returnValue = '';
      return;
    }
    if (source.value !== saved.html || title.value !== saved.title || metadata().slug !== saved.slug || metadata().published !== saved.published || metadata().scheduled !== saved.scheduled) {
      event.preventDefault();
      event.returnValue = '';
    }
  }
  addEventListener('beforeunload', guard);

  bar.hidden = false;
  const preferred = storedMode();
  if (preferred !== 'html') switchMode(preferred, { prompt: false });
  fit(source);
  return editor;
}

const main = document.querySelector('main');
if (main) {
  window.contentEditor = init(main);
}
