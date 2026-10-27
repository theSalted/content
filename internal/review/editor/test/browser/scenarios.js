// End-to-end editor scenarios against the synthetic devserver.
import assert from 'node:assert/strict';

const PASSWORD = 'synthetic-review-password';
const IPHONE = {
  viewport: { width: 390, height: 844 },
  deviceScaleFactor: 3,
  isMobile: true,
  hasTouch: true,
  userAgent: 'Mozilla/5.0 (iPhone; CPU iPhone OS 18_0 like Mac OS X) AppleWebKit/605.1.15 (KHTML, like Gecko) Version/18.0 Mobile/15E148 Safari/604.1',
};

async function ready(page) {
  await page.waitForFunction(() => window.contentEditor && !document.querySelector('.editor-bar').hidden);
  await frame(page);
}

const frame = (page) => page.evaluate(() => new Promise((done) => requestAnimationFrame(() => requestAnimationFrame(done))));

function watch(page) {
  const errors = [];
  page.on('pageerror', (error) => errors.push(String(error)));
  page.on('response', (response) => {
    if (response.status() >= 400 && response.status() !== 409) errors.push(response.status() + ' ' + response.url());
  });
  page.on('console', (message) => {
    // Expected: remote images are refused by the review CSP, and a stale save
    // is answered with 409, which Chrome logs.
    const text = message.text();
    if (message.type() === 'error' && !text.includes('example.invalid') && !text.includes('status of 409')) errors.push(text);
  });
  return errors;
}

async function open(browser, server, name, options = {}) {
  const context = await browser.newContext(options);
  await context.addInitScript(() => document.addEventListener('securitypolicyviolation', (e) => {
    console.error(`CSP ${e.violatedDirective} ${e.sourceFile}:${e.lineNumber}:${e.columnNumber} ${e.sample}`);
  }));
  const page = await context.newPage();
  const errors = watch(page);
  const path = `/ghost/posts/${server.posts[name]}/`;
  await page.goto(server.origin + '/ghost/login/?next=' + encodeURIComponent(path));
  await page.fill('#username', 'synthetic');
  await page.fill('#password', PASSWORD);
  await Promise.all([page.waitForURL(server.origin + path), page.click('main button[type=submit]')]);
  await ready(page);
  return { context, page, errors, path };
}

async function second(context, server, path) {
  const page = await context.newPage();
  const errors = watch(page);
  await page.goto(server.origin + path);
  await ready(page);
  return { page, errors };
}

const source = (page) => page.$eval('#body', (el) => el.value);
const mode = (page) => page.$eval('[data-mode][aria-pressed=true]', (el) => el.dataset.mode);
async function switchTo(page, next) {
  await page.click(`[data-mode=${next}]`);
  await page.waitForFunction((m) => document.querySelector(`[data-mode=${m}]`).getAttribute('aria-pressed') === 'true' || document.querySelector('#convert-dialog').open, next);
  // The editor takes focus on the next animation frame.
  await frame(page);
}
async function press(page, selector) {
  await page.click(selector);
  await frame(page);
}
async function saved(page, text = 'Saved.') {
  await page.waitForFunction((t) => document.querySelector('main > .notice')?.textContent === t, text);
}

async function withServer(startServer, fn) {
  const server = await startServer();
  try {
    await fn(server);
  } finally {
    server.proc.kill();
  }
}

export const scenarios = [
  ['management create filter metadata publish trash restore', ({browser,startServer}) => withServer(startServer, async(server)=>{
    const context=await browser.newContext(IPHONE);const page=await context.newPage();
    await page.goto(server.origin+'/ghost/login/');await page.fill('#username','synthetic');await page.fill('#password',PASSWORD);
    await Promise.all([page.waitForURL('**/ghost/posts/'),page.getByRole('button',{name:'Sign in',exact:true}).click()]);
    await page.getByRole('link',{name:'New post',exact:true}).click();await page.fill('#title','Management synthetic');
    await Promise.all([page.waitForURL(/\/ghost\/posts\/[a-f0-9]{24}\//),page.getByRole('button',{name:'Create draft'}).click()]);
    const editorURL=page.url();await ready(page);await switchTo(page,'html');await page.fill('#body','<p>Preserved synthetic body.</p>');
    await page.click('[data-settings-open]');await page.fill('#slug','management-first');assert.equal(await page.locator('#published_time').count(),0);
    await page.click('#settings-dialog button[value=save]');await saved(page);await page.click('[data-publish-open]');await page.click('[data-publish-confirm]');await saved(page,'Published.');
    await page.click('[data-settings-open]');await page.fill('#slug','management-second');await page.check('[name=confirm_slug_change]');await page.fill('#published_time','2021-02-03T04:05');
    await page.click('#settings-dialog button[value=save]');await saved(page);await page.reload();await ready(page);
    await page.click('[data-settings-open]');assert.equal(await page.inputValue('#slug'),'management-second');assert.equal(await page.inputValue('#published_time'),'2021-02-03T04:05');
    await page.click('[data-settings-close]');await page.getByRole('link',{name:'Back to Content',exact:true}).click();await page.fill('[name=q]','Management');await page.selectOption('[name=status]','published');await page.getByRole('button',{name:'Apply',exact:true}).click();
    assert.equal(await page.locator('tbody tr').count(),1);await page.getByRole('link',{name:'Management synthetic',exact:true}).click();await ready(page);
    await page.click('[data-settings-open]');await page.check('[name=confirm_unpublish]');await page.click('button[value=unpublish]');await saved(page,'Unpublished.');
    await page.click('[data-settings-open]');await page.check('[name=confirm_trash]');await Promise.all([page.waitForNavigation(),page.getByRole('button',{name:'Move to Trash',exact:true}).click()]);
    assert.equal(page.url(),editorURL);assert.equal(await page.locator('#post-form').count(),0);
    await Promise.all([page.waitForNavigation(),page.getByRole('button',{name:'Restore draft'}).click()]);await ready(page);await switchTo(page,'html');
    assert.equal(await source(page),'<p>Preserved synthetic body.</p>');assert.equal(await page.getByRole('button',{name:'Save draft',exact:true}).count(),1);
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);
    await context.close();
  })],
  ['publishing dialog schedule reschedule cancel and save', ({browser,startServer}) => withServer(startServer, async(server)=>{
    const {page,context,errors}=await open(browser,server,'rich',IPHONE);
    assert.equal(await page.locator('#published_time').count(),0);
    await page.click('[data-publish-open]');
    assert.equal(await page.locator('[data-schedule-fields]').isVisible(),false);
    await page.check('[name=publish_timing][value=later]');
    await page.fill('#scheduled_time','2999-01-02T03:04');
    if(process.env.CONTENT_EDITOR_SCREENSHOT_DIR)await page.screenshot({path:process.env.CONTENT_EDITOR_SCREENSHOT_DIR+'/schedule-mobile.png'});
    assert.equal(await page.textContent('[data-publish-confirm]'),'Schedule');
    await page.click('[data-publish-confirm]');await saved(page,'Scheduled.');
    assert.match(await page.textContent('.post-status'),/Scheduled.*03:04 UTC/);
    await switchTo(page,'html');const body=await source(page);await page.fill('#body',body+'<p>Saved while scheduled.</p>');
    await page.click('#post-controls button[value=save]');await saved(page);
    assert.match(await page.textContent('.post-status'),/Scheduled.*03:04 UTC/);
    await page.click('[data-publish-open]');await page.fill('#scheduled_time','2999-01-03T04:05');await page.click('[data-publish-confirm]');await saved(page,'Scheduled.');
    assert.match(await page.textContent('.post-status'),/04:05 UTC/);
    await page.reload();await ready(page);assert.match(await page.textContent('.post-status'),/04:05 UTC/);
    await page.click('[data-publish-open]');await page.click('#publish-dialog button[value=cancel_schedule]');await saved(page,'Schedule cancelled.');
    assert.equal((await page.textContent('.post-status')).trim(),'Draft');
    await page.click('[data-publish-open]');await page.check('[name=publish_timing][value=later]');await page.fill('#scheduled_time','2999-02-03T05:06');await page.click('[data-publish-close]');
    await page.click('#post-controls button[value=save]');await saved(page);assert.equal((await page.textContent('.post-status')).trim(),'Draft');
    assert.equal(await page.evaluate(()=>document.documentElement.scrollWidth>innerWidth),false);assert.deepEqual(errors,[]);
    await context.close();
  })],
  ['switching modes without edits keeps the exact source', ({ browser, startServer }) => withServer(startServer, async (server) => {
    const { page, errors, context } = await open(browser, server, 'rich');
    const original = await source(page);
    assert.equal(await mode(page), 'rich');
    await page.waitForFunction(() => {
      const img = document.querySelector('.rich img[alt="Synthetic private image"]');
      return img && img.complete && img.naturalWidth === 160 && img.getAttribute('src').startsWith('/ghost/images/');
    });
    assert.match(original, /\/content\/images\/[0-9a-f]{24}\/image\.png/);
    for (const next of ['html', 'markdown', 'rich', 'markdown', 'html', 'rich', 'html']) {
      await switchTo(page, next);
      assert.equal(await page.$eval('#convert-dialog', (d) => d.open), false, 'unexpected conversion prompt for ' + next);
      if (next === 'markdown') assert.match(await page.$eval('#markdown', (t) => t.value), /^## Synthetic heading/);
    }
    assert.equal(await source(page), original);
    assert.deepEqual(errors, []);
    await context.close();
  })],

  ['rich edits update the source and undo restores it exactly', ({ browser, startServer }) => withServer(startServer, async (server) => {
    const { page, errors, context } = await open(browser, server, 'rich');
    const original = await source(page);
    await page.click('.rich > p:last-of-type');
    await page.keyboard.press('End');
    await page.keyboard.type(" Don't stop.");
    await switchTo(page, 'html');
    assert.match(await source(page), /Last paragraph\. Don't stop\.<\/p>$/);
    await switchTo(page, 'rich');
    await page.keyboard.press('ControlOrMeta+z');
    await switchTo(page, 'html');
    assert.equal(await source(page), original);
    await switchTo(page, 'rich');
    await page.keyboard.press('ControlOrMeta+Shift+z');
    await switchTo(page, 'html');
    assert.match(await source(page), /Don't stop\./);
    assert.deepEqual(errors, []);
    await context.close();
  })],

  ['toolbar, link dialog, save, undo after save and reload', ({ browser, startServer }) => withServer(startServer, async (server) => {
    const { page, errors, context } = await open(browser, server, 'rich');
    await page.dblclick('.rich > p:last-of-type', { position: { x: 12, y: 10 } });
    await press(page, 'button[aria-label="Bold"]');
    assert.equal(await page.getAttribute('button[aria-label="Bold"]', 'aria-pressed'), 'true');
    await page.keyboard.press('ControlOrMeta+k');
    await page.waitForSelector('#link-dialog[open]');
    await page.fill('#link-url', 'javascript:alert(1)');
    await page.keyboard.press('Enter');
    assert.equal(await page.$eval('#link-dialog', (d) => d.open), true);
    assert.match(await page.textContent('#link-dialog .dialog-error'), /link/);
    await page.fill('#link-url', 'https://example.invalid/new');
    await page.keyboard.press('Enter');
    await page.waitForFunction(() => !document.querySelector('#link-dialog').open);
    await frame(page);
    assert.equal(await page.getAttribute('button[aria-label="Link"]', 'aria-pressed'), 'true');

    const token = await page.inputValue('[name=updated_at]');
    await page.evaluate(() => (window.before = window.contentEditor));
    await page.click('button[value=save]');
    await saved(page);
    assert.notEqual(await page.inputValue('[name=updated_at]'), token);
    assert.ok(await page.evaluate(() => window.before === window.contentEditor), 'editor was replaced on save');
    await switchTo(page, 'html');
    const stored = await source(page);
    assert.ok(stored.includes('href="https://example.invalid/new" rel="nofollow"'), stored);
    assert.match(stored, /<strong>(<a [^>]+>)?Last|<a [^>]+><strong>Last/);

    await switchTo(page, 'rich');
    await page.keyboard.press('ControlOrMeta+z');
    await switchTo(page, 'html');
    assert.notEqual(await source(page), stored, 'undo history lost after save');

    let prompted = false;
    page.on('dialog', (dialog) => {
      prompted = prompted || dialog.type() === 'beforeunload';
      dialog.accept();
    });
    await page.reload();
    await ready(page);
    assert.ok(prompted, 'unsaved edit did not warn before leaving');
    assert.equal(await source(page), stored);
    assert.deepEqual(errors, []);
    await context.close();
  })],

  ['a stale save reports the conflict and keeps the edits', ({ browser, startServer }) => withServer(startServer, async (server) => {
    const a = await open(browser, server, 'rich');
    const b = await second(a.context, server, a.path);
    await a.page.click('.rich > p:last-of-type');
    await a.page.keyboard.press('End');
    await a.page.keyboard.type(' First writer.');
    await a.page.click('button[value=save]');
    await saved(a.page);

    await b.page.click('.rich h2');
    await b.page.keyboard.press('End');
    await b.page.keyboard.type(' conflicting edit');
    const token = await b.page.inputValue('[name=updated_at]');
    await b.page.click('button[value=save]');
    await b.page.waitForFunction(() => /changed elsewhere/.test(document.querySelector('main > .error')?.textContent || ''));
    assert.equal(await b.page.inputValue('[name=updated_at]'), token);
    assert.match(await b.page.textContent('.rich h2'), /conflicting edit$/);
    await switchTo(b.page, 'html');
    assert.match(await source(b.page), /conflicting edit<\/h2>/);
    assert.deepEqual([...a.errors, ...b.errors], []);
    await a.context.close();
  })],

  ['lossy source needs an explicit choice and stays verbatim', ({ browser, startServer }) => withServer(startServer, async (server) => {
    const { page, errors, context } = await open(browser, server, 'lossy');
    const original = await source(page);
    assert.equal(await mode(page), 'html');
    await switchTo(page, 'rich');
    await page.waitForSelector('#convert-dialog[open]');
    const losses = await page.$$eval('#convert-losses li', (items) => items.map((i) => i.textContent));
    for (const expected of ['<sub>', '<abbr>', 'title on <abbr>', '<dl>', '<dt>', '<dd>']) assert.ok(losses.includes(expected), losses.join(', '));
    await page.click('#convert-dialog [data-close]');
    assert.equal(await mode(page), 'html');
    assert.equal(await source(page), original);

    await switchTo(page, 'rich');
    await page.click('#convert-dialog button[value=convert]');
    await page.waitForFunction(() => document.querySelector('[data-mode=rich]').getAttribute('aria-pressed') === 'true');
    await switchTo(page, 'html');
    assert.equal(await source(page), original, 'viewing a lossy conversion rewrote the source');

    await switchTo(page, 'markdown');
    await page.waitForSelector('#convert-dialog[open]');
    await page.click('#convert-dialog [data-close]');
    assert.equal(await mode(page), 'html');
    assert.deepEqual(errors, []);
    await context.close();
  })],

  ['markdown edits convert to profile HTML when saved', ({ browser, startServer }) => withServer(startServer, async (server) => {
    const { page, errors, context } = await open(browser, server, 'empty');
    await switchTo(page, 'markdown');
    await page.fill('#markdown', '## From Markdown\n\nNew **bold** text with [a link](https://example.invalid/md).\n\n- one\n- two\n');
    await page.click('button[value=save]');
    await saved(page);
    await switchTo(page, 'html');
    assert.equal(await source(page), '<h2>From Markdown</h2><p>New <strong>bold</strong> text with <a href="https://example.invalid/md" rel="nofollow">a link</a>.</p><ul><li><p>one</p></li><li><p>two</p></li></ul>');
    await switchTo(page, 'rich');
    assert.match(await page.textContent('.rich h2'), /From Markdown/);
    assert.deepEqual(errors, []);
    await context.close();
  })],

  ['tables, paste sanitizing and explicit publish', ({ browser, startServer }) => withServer(startServer, async (server) => {
    const { page, errors, context } = await open(browser, server, 'empty');
    await page.click('.rich');
    await page.keyboard.type('Intro');
    await page.click('button[aria-label="Table"]');
    await page.waitForSelector('[data-table]:not([hidden])');
    await page.keyboard.type('Cell');
    const rows = await page.$$eval('.rich table tr', (r) => r.length);
    await page.click('button[aria-label="Add row"]');
    assert.equal(await page.$$eval('.rich table tr', (r) => r.length), rows + 1);
    await page.click('.rich > p');
    await page.keyboard.press('End');
    await frame(page);
    await page.evaluate(() => {
      const data = new DataTransfer();
      data.setData('text/html', '<p class="x" style="color:red">Pasted <a href="javascript:alert(1)">bad</a> <a href="https://example.invalid/ok" target="_blank" rel="noopener">ok</a>' +
        '<img src="data:image/png;base64,iVBORw0KGgo=" alt="d"></p><script>window.pwned=1</script><iframe src="https://example.invalid/"></iframe>');
      data.setData('text/plain', 'Pasted bad ok');
      document.querySelector('.rich').dispatchEvent(new ClipboardEvent('paste', { clipboardData: data, bubbles: true, cancelable: true }));
    });
    await switchTo(page, 'html');
    const html = await source(page);
    assert.ok(html.includes('Pasted') && html.includes('<a href="https://example.invalid/ok" rel="nofollow">ok</a>'), html);
    assert.doesNotMatch(html, /javascript:|data:|class=|style=|target=|<script|<iframe|colgroup/);
    assert.match(html, /<thead><tr><th><p>Cell<\/p><\/th>/);
    assert.equal(await page.evaluate(() => window.pwned), undefined);
    // ProseMirror parses pasted HTML in a detached document; Chrome reports the
    // blocked style attribute there. Nothing is applied or kept.
    const isPastedStyle = (e) => /style attributes|style-src-attr/.test(e);
    assert.ok(errors.filter(isPastedStyle).length <= 4);
    errors.splice(0, errors.length, ...errors.filter((e) => !isPastedStyle(e)));
    await page.click('[data-publish-open]');await page.click('[data-publish-confirm]');
    await saved(page, 'Published.');
    assert.equal(await page.textContent('#post-controls button[value=save]'), 'Update');
    assert.equal(await page.$('[data-publish-open]'), null);
    assert.deepEqual(errors, []);
    await context.close();
  })],

  ['mobile touch: toolbar keeps selection, sticks, undoes and saves', ({ browser, startServer }) => withServer(startServer, async (server) => {
    const { page, errors, context } = await open(browser, server, 'rich', IPHONE);
    assert.equal(await mode(page), 'rich');
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), 'page scrolls horizontally');
    const box = await page.$eval('button[aria-label="Bold"]', (b) => b.getBoundingClientRect().height);
    assert.ok(box >= 40, 'touch target ' + box);
    await page.tap('.rich > p:last-of-type');
    await page.keyboard.press('End');
    for (let i = 0; i < 4; i++) await page.keyboard.press('Shift+ArrowLeft');
    await page.tap('button[aria-label="Bold"]');
    await frame(page);
    assert.equal(await page.getAttribute('button[aria-label="Bold"]', 'aria-pressed'), 'true');
    assert.ok(await page.evaluate(() => document.activeElement.classList.contains('rich')), 'toolbar took focus');
    assert.equal(await page.evaluate(() => getSelection().toString()), 'aph.');
    // A short viewport, as with the on-screen keyboard open: the one-row tool
    // strip stays pinned while the document scrolls beneath it.
    await page.setViewportSize({ width: 390, height: 500 });
    const barHeight = await page.$eval('.editor-bar', (bar) => bar.getBoundingClientRect().height);
    assert.ok(barHeight <= 100, 'toolbar too tall for a phone: ' + barHeight);
    await page.evaluate(() => {
      const editor = document.querySelector('.editor');
      scrollTo(0, editor.getBoundingClientRect().top + scrollY + 300);
    });
    await frame(page);
    const top = await page.$eval('.editor-bar', (bar) => bar.getBoundingClientRect().top);
    const headerBottom = await page.$eval('#post-controls', (el) => el.getBoundingClientRect().bottom);
    assert.ok(Math.abs(top - headerBottom) <= 1, 'toolbar overlaps or detaches from publishing controls: ' + top + '/' + headerBottom);
    await page.setViewportSize(IPHONE.viewport);
    await page.tap('button[aria-label="Undo"]');
    await frame(page);
    assert.equal(await page.getAttribute('button[aria-label="Bold"]', 'aria-pressed'), 'false');
    await page.tap('button[aria-label="Redo"]');
    await page.tap('button[value=save]');
    await saved(page);
    await switchTo(page, 'html');
    assert.match(await source(page), /Last paragr<strong>aph\.<\/strong><\/p>$/);
    assert.deepEqual(errors, []);
    await context.close();
  })],
];
