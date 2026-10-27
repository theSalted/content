// Browser tests for the review editor against the synthetic loopback
// devserver. Run from internal/review/editor:
//   node test/browser/run.js [name filter]              installed Google Chrome
//   BROWSER=webkit node test/browser/run.js [filter]    Playwright WebKit
//     (one-time: node node_modules/playwright-core/cli.js install webkit)
import { chromium, webkit } from 'playwright-core';
import { spawn, execFileSync } from 'node:child_process';
import { mkdtempSync, readFileSync, rmSync } from 'node:fs';
import { tmpdir } from 'node:os';
import { join, resolve, dirname } from 'node:path';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';
import { scenarios } from './scenarios.js';

const here = dirname(fileURLToPath(import.meta.url));
const editorDir = resolve(here, '../..');
const repo = resolve(editorDir, '../../..');
const work = mkdtempSync(join(tmpdir(), 'review-editor-test-'));
const bin = join(work, 'devserver');
const only = process.argv[2];

execFileSync('bun', ['run', 'build'], { cwd: editorDir, stdio: 'ignore' });
execFileSync('go', ['build', '-o', bin, './internal/review/editor/devserver'], { cwd: repo, stdio: 'inherit' });
execFileSync('bun', ['build', join(here, 'cases.js'), '--outfile', join(work, 'cases.js'), '--format', 'iife', '--target', 'browser'], { cwd: editorDir, stdio: 'ignore' });

export function startServer() {
  return new Promise((resolveStart, reject) => {
    const proc = spawn(bin, ['-addr', '127.0.0.1:0'], { stdio: ['ignore', 'pipe', 'inherit'] });
    let buffer = '';
    proc.stdout.on('data', (chunk) => {
      buffer += chunk;
      const end = buffer.indexOf('\n');
      if (end >= 0) resolveStart({ proc, ...JSON.parse(buffer.slice(0, end)) });
    });
    proc.on('exit', (code) => reject(new Error('devserver exited ' + code)));
  });
}

function accepted(sources) {
  const out = execFileSync(bin, ['-validate'], { input: JSON.stringify(sources) });
  return JSON.parse(out);
}

const failures = [];
let passed = 0;
async function test(name, fn) {
  if (only && !name.includes(only)) return;
  try {
    await fn();
    passed++;
    console.log('ok   ' + name);
  } catch (error) {
    failures.push(name);
    console.log('FAIL ' + name + '\n     ' + String(error.stack || error).split('\n').slice(0, 6).join('\n     '));
  }
}

const engine = process.env.BROWSER || 'chrome';
const browser = engine === 'webkit' ? await webkit.launch({ headless: true }) : await chromium.launch({ channel: 'chrome', headless: true });
console.log(`${engine} ${browser.version()}`);
try {
  await test('conversion and fidelity cases', async () => {
    const page = await browser.newPage();
    await page.setContent('<!doctype html><body></body>');
    await page.addScriptTag({ content: readFileSync(join(work, 'cases.js'), 'utf8') });
    const { results, outputs } = await page.evaluate(() => window.runCases());
    await page.close();
    const bad = results.filter((r) => !r.ok);
    for (const r of bad) console.log('     case failed: ' + r.name + '\n       ' + JSON.stringify(r.detail));
    console.log(`     ${results.length - bad.length}/${results.length} cases`);
    const verdicts = accepted(outputs);
    const rejected = outputs.filter((_, i) => !verdicts[i]);
    for (const html of rejected) console.log('     CMS rejects editor output: ' + html);
    assert.equal(bad.length + rejected.length, 0);
  });

  for (const [name, fn] of scenarios) {
    await test(name, () => fn({ browser, startServer }));
  }
} finally {
  await browser.close();
  rmSync(work, { recursive: true, force: true });
}

console.log(`\n${passed} passed, ${failures.length} failed`);
process.exit(failures.length ? 1 : 0);
