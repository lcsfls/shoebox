// End-to-end run of Shoebox in a real browser, with screenshots of every
// screen (phone and desktop). Runs in CI against a freshly started server:
//   SB_URL=http://127.0.0.1:8080 SB_SETUP_CODE=… SB_ARCHIVE=… node e2e.mjs
import { chromium } from 'playwright';
import fs from 'node:fs';
import path from 'node:path';
import { startPaperless } from './paperless-mock.mjs';

const B = process.env.SB_URL || 'http://127.0.0.1:8080';
const CODE = process.env.SB_SETUP_CODE;
const ARCHIVE = process.env.SB_ARCHIVE;
const LETTER_PNG = process.env.SB_LETTER_PNG;
const LETTER_PDF = process.env.SB_LETTER_PDF;
const OUT = path.resolve('out');
fs.mkdirSync(OUT, { recursive: true });

const problems = [];
const log = (...a) => console.log('·', ...a);
let lastPage = null; // for a screenshot when something fails
function watch(page, tag) {
  lastPage = page;
  page.on('pageerror', (e) => problems.push(`${tag}: page error: ${e.message}`));
  page.on('console', (m) => { if (m.type() === 'error' && !/favicon|404 \(Not Found\)/.test(m.text())) problems.push(`${tag}: console: ${m.text()}`); });
}
async function shot(page, name, full = false) {
  await page.waitForTimeout(350);
  await page.screenshot({ path: path.join(OUT, name + '.png'), fullPage: full });
}
async function until(page, fn, what, ms = 90000) {
  const end = Date.now() + ms;
  while (Date.now() < end) {
    if (await fn()) return;
    await page.waitForTimeout(800);
  }
  throw new Error('timed out waiting for ' + what);
}
const api = (page, p, opts = {}) => page.evaluate(async ([p, o]) => {
  const r = await fetch(p, { ...o, headers: { 'X-Shoebox': '1', 'Content-Type': 'application/json' } });
  return r.json();
}, [p, opts]);

const browser = await chromium.launch({ args: ['--use-fake-ui-for-media-stream', '--use-fake-device-for-media-stream'] });
try {
  // ---------- desktop ----------
  const desk = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const d = await desk.newPage();
  watch(d, 'desktop');
  d.setDefaultTimeout(20000);

  await d.goto(B + '/');
  await d.getByLabel('Setup code').waitFor();
  await shot(d, '01-setup');
  await d.getByLabel('Setup code').fill(CODE);
  await d.getByLabel('Username').fill('anna');
  await d.getByLabel('Your name').fill('Anna');
  await d.getByLabel('Password', { exact: false }).first().fill('a-long-password');
  await d.getByLabel('Password again').fill('a-long-password');
  await d.getByRole('button', { name: 'Create account' }).click();
  await d.getByText('Scan a document').waitFor();
  await shot(d, '02-home-empty');
  log('account created');

  // upload a photo of a letter and a PDF
  await d.locator('input[type=file][accept^="application/pdf"]').setInputFiles([LETTER_PNG, LETTER_PDF]);
  await until(d, async () => {
    const o = await api(d, '/api/overview');
    return o.inbox + o.filed >= 2 && o.busy === 0;
  }, 'two documents');
  await d.reload();
  await d.getByText('To review').waitFor();
  await shot(d, '03-home-review');
  log('uploads processed');

  // review: sender, tags, file it (the electricity bill first)
  await d.goto(B + '/review');
  await d.locator('a.doccard').first().waitFor();
  await shot(d, '04-review-list');
  const inbox = await api(d, '/api/docs?status=inbox');
  const bill = inbox.docs.find((x) => x.date === '2026-10-24');
  if (!bill) problems.push('review: no document with the date 24.10.2026 ' + JSON.stringify(inbox.docs.map((x) => [x.title, x.date])));
  await d.goto(B + '/review/' + (bill ? bill.id : inbox.docs[0].id));
  await d.getByLabel('Sender', { exact: true }).waitFor();
  await d.waitForTimeout(800);
  await shot(d, '05-review-item');
  const title = await d.getByLabel('Title', { exact: true }).inputValue();
  const date = await d.getByLabel('Document date', { exact: true }).inputValue();
  if (date !== '2026-10-24') problems.push(`review: date recognized as "${date}"`);
  await d.getByLabel('Title', { exact: true }).fill('Stromrechnung Oktober');
  await d.getByLabel('Sender', { exact: true }).fill('Stadtwerke Musterstadt');
  await d.locator('#rv-tags').fill('Strom');
  await d.locator('#rv-tags').press('Enter');
  await d.getByRole('button', { name: 'File it' }).click();
  await d.getByText(/Filed: 2026\/10\//).waitFor();
  log('filed', title, date);
  // the second letter, from the property management
  const billId = bill ? bill.id : inbox.docs[0].id;
  await d.waitForURL((u) => u.pathname !== '/review/' + billId);
  await d.waitForTimeout(800);
  if (await d.getByRole('button', { name: 'File it' }).count()) {
    await d.getByLabel('Sender', { exact: true }).fill('Hausverwaltung Beispiel');
    await d.getByRole('button', { name: 'File it' }).click();
    await d.getByText(/Filed:/).waitFor();
  }

  // a sorting rule
  await d.goto(B + '/settings/rules');
  await d.getByRole('button', { name: 'New rule' }).click();
  await d.getByLabel('Look for').fill('Stadtwerke Musterstadt');
  await d.getByLabel('Match').selectOption('exact');
  await d.getByLabel('Then set the sender').fill('Stadtwerke Musterstadt');
  await d.locator('#rule-tags').fill('Strom');
  await d.locator('#rule-tags').press('Enter');
  await d.getByRole('button', { name: 'Save rule' }).click();
  await d.getByText('Rule saved').waitFor();
  await d.getByLabel('Paste the text of a letter').fill('Stadtwerke Musterstadt GmbH\nRechnungsdatum 24.10.2026\nGesamtbetrag 84,20 EUR');
  await d.getByRole('button', { name: 'Check' }).click();
  await d.getByText(/84[.,]20/).first().waitFor();
  await shot(d, '06-settings-rules', true);

  // archive and search
  await d.goto(B + '/archive');
  await d.locator('a.doccard').first().waitFor();
  await shot(d, '07-archive');
  await d.getByLabel('Search', { exact: true }).fill('gesamtbetrag');
  await d.waitForTimeout(900);
  await d.locator('mark').first().waitFor();
  await shot(d, '08-archive-search');
  await d.locator('a.doccard').first().click();
  await d.getByRole('button', { name: 'Edit', exact: true }).waitFor();
  await d.waitForTimeout(1200);
  await shot(d, '09-document');

  // a PDF copied onto the "NAS" by hand
  const target = path.join(ARCHIVE, '2026', '01');
  fs.mkdirSync(target, { recursive: true });
  fs.copyFileSync(LETTER_PDF, path.join(target, 'Nebenkosten-Abrechnung.pdf'));
  const old = new Date(Date.now() - 60000);
  fs.utimesSync(path.join(target, 'Nebenkosten-Abrechnung.pdf'), old, old);
  // the copy has the same content as an uploaded file: it must be recognized as a duplicate, so make it unique
  fs.appendFileSync(path.join(target, 'Nebenkosten-Abrechnung.pdf'), '\n% copied by hand\n');
  fs.utimesSync(path.join(target, 'Nebenkosten-Abrechnung.pdf'), old, old);
  await d.goto(B + '/settings/storage');
  await d.getByRole('button', { name: 'Look through now' }).click();
  await until(d, async () => {
    const r = await api(d, '/api/docs?year=2026&month=01');
    return r.total === 1 && r.docs[0].source === 'folder' && r.docs[0].path === '2026/01/Nebenkosten-Abrechnung.pdf';
  }, 'the hand-copied file in the library (dated by its folder 2026/01)');
  await d.reload();
  await d.getByText('Shoebox can write here').waitFor();
  await shot(d, '10-settings-storage', true);
  log('folder watcher ok');

  // Paperless import against a stand-in API
  const mock = await startPaperless(LETTER_PDF);
  await d.goto(B + '/settings/import');
  await d.getByLabel('Paperless address').fill('http://127.0.0.1:18000');
  await d.getByLabel('API token').fill('test-token');
  await d.getByRole('button', { name: 'Connect and count' }).click();
  await d.getByText('matching rules').waitFor();
  await d.getByRole('button', { name: 'Import everything' }).click();
  await until(d, async () => {
    const s = await api(d, '/api/import/status');
    return !s.running && s.finished;
  }, 'the import');
  const st = await api(d, '/api/import/status');
  if (st.done !== 3 || st.failed) problems.push(`import: ${JSON.stringify(st)}`);
  const imported = await api(d, '/api/docs?q=' + encodeURIComponent('Stromrechnung Juli'));
  const jul = imported.docs?.[0];
  if (!jul || jul.sender !== 'Stadtwerke Musterstadt' || !jul.tags.includes('type:Rechnung') || jul.fields?.Vertragsnummer !== 'V-4711' || !jul.path.startsWith('2026/07/')) {
    problems.push(`import: unexpected document ${JSON.stringify(jul)}`);
  }
  await d.reload();
  await d.getByText(/imported/).first().waitFor();
  await shot(d, '11-settings-import', true);
  // again: nothing new
  await api(d, '/api/import/paperless/start', { method: 'POST', body: JSON.stringify({ url: 'http://127.0.0.1:18000', token: 'test-token' }) });
  await until(d, async () => { const s = await api(d, '/api/import/status'); return !s.running && s.finished; }, 'the second import');
  const st2 = await api(d, '/api/import/status');
  if (st2.done !== 0 || st2.skipped !== 3) problems.push(`second import should skip everything: ${JSON.stringify(st2)}`);
  mock.close();
  log('paperless import ok');

  // tag colors
  await d.goto(B + '/settings/names');
  await d.getByRole('button', { name: 'Color of Strom' }).click();
  await d.getByRole('radio', { name: 'sky' }).click();
  await until(d, async () => (await api(d, '/api/tags')).find((t) => t.name === 'Strom')?.color === 'sky', 'the tag color');
  await d.getByRole('button', { name: 'Color of Strom' }).click();
  await d.waitForTimeout(300);
  await shot(d, '15-tag-colors', true);
  await d.getByRole('button', { name: 'Color of Strom' }).click();

  // mark the date on the page instead of typing it
  const billDoc = (await api(d, '/api/docs?q=' + encodeURIComponent('Stromrechnung Oktober'))).docs?.[0];
  await d.goto(B + '/doc/' + billDoc.id);
  await d.getByRole('button', { name: 'Edit', exact: true }).click();
  await d.getByLabel('Document date', { exact: true }).fill('');
  await d.getByRole('button', { name: 'Mark the date on the page' }).click();
  await d.getByRole('dialog', { name: /Mark the date/ }).waitFor();
  const words = await api(d, `/api/docs/${billDoc.id}/words/1`);
  const w = words.find((x) => /24\.10\.2026/.test(x.t));
  if (!w) problems.push('marker: the date is not among the words of page 1');
  else {
    const img = await d.locator('.marker .sheet img').boundingBox();
    const x0 = img.x + (w.x - 0.01) * img.width, y0 = img.y + (w.y - 0.01) * img.height;
    const x1 = img.x + (w.x + w.w + 0.01) * img.width, y1 = img.y + (w.y + w.h + 0.01) * img.height;
    await d.mouse.move(x0, y0);
    await d.mouse.down();
    await d.mouse.move(x1, y1, { steps: 8 });
    await d.mouse.up();
    await d.getByRole('button', { name: 'Use it' }).waitFor();
    await until(d, async () => await d.getByRole('button', { name: 'Use it' }).isEnabled(), 'the marked date', 10000);
    await shot(d, '16-marker');
    await d.getByRole('button', { name: 'Use it' }).click();
    const v = await d.getByLabel('Document date', { exact: true }).inputValue();
    if (v !== '2026-10-24') problems.push(`marker: date field is "${v}"`);
  }
  await d.getByRole('dialog', { name: /Mark the date/ }).waitFor({ state: 'detached' });
  await d.getByRole('button', { name: 'Cancel' }).click();
  log('marker ok');

  // statistics, a backup, rule suggestions, an API token
  await d.goto(B + '/stats');
  await d.getByRole('heading', { name: 'Statistics' }).waitFor();
  await d.waitForTimeout(600);
  await shot(d, '17-stats', true);
  await d.goto(B + '/settings/storage');
  await d.getByRole('button', { name: 'Back up now' }).click();
  await d.getByText(/Backup saved/).waitFor();
  if (!fs.readdirSync(path.join(ARCHIVE, '.shoebox-backup')).some((f) => f.endsWith('.db'))) problems.push('backup: no file in the archive folder');
  await shot(d, '18-backup', true);
  await d.goto(B + '/settings/api');
  await d.getByLabel('Name', { exact: false }).first().fill('Scanner im Büro');
  await d.getByRole('button', { name: 'Create token' }).click();
  const tokenValue = await d.getByLabel('New API token').inputValue();
  const viaToken = await fetch(B + '/api/docs?q=strom', { headers: { Authorization: 'Bearer ' + tokenValue } });
  if (viaToken.status !== 200 || !(await viaToken.json()).total) problems.push(`api token: ${viaToken.status}`);
  await shot(d, '19-api', true);
  log('stats, backup, api ok');

  for (const tab of ['general', 'account', 'ocr', 'names', 'users', 'rules']) {
    await d.goto(B + '/settings/' + tab);
    await d.waitForTimeout(700);
    await shot(d, `12-settings-${tab}`, true);
  }

  // dark mode
  await d.emulateMedia({ colorScheme: 'dark' });
  await d.goto(B + '/archive');
  await d.locator('a.doccard').first().waitFor();
  await shot(d, '13-archive-dark');
  await d.goto(B + '/');
  await shot(d, '14-home-dark');
  await d.emulateMedia({ colorScheme: 'light' });

  // ---------- phone ----------
  const phone = await browser.newContext({
    viewport: { width: 390, height: 844 }, deviceScaleFactor: 2, isMobile: true, hasTouch: true,
    storageState: await desk.storageState(), permissions: ['camera'],
  });
  const p = await phone.newPage();
  watch(p, 'phone');
  p.setDefaultTimeout(20000);
  await p.goto(B + '/');
  await p.getByText('Scan a document').waitFor();
  await shot(p, '20-phone-home');
  await p.goto(B + '/archive?q=strom');
  await p.locator('a.doccard').first().waitFor();
  await shot(p, '21-phone-archive');
  await p.getByRole('button', { name: 'Filter', exact: true }).click();
  await p.waitForTimeout(400);
  await shot(p, '22-phone-filters');
  await p.goto(B + '/archive');
  await p.locator('a.doccard').first().click();
  await p.getByRole('button', { name: 'Edit', exact: true }).waitFor();
  await p.waitForTimeout(1200);
  await shot(p, '23-phone-document');
  await p.goto(B + '/settings/ocr');
  await p.waitForTimeout(600);
  await shot(p, '24-phone-settings');

  // scanning with the (fake) camera
  await p.goto(B + '/scan');
  await p.locator('video').waitFor();
  await p.waitForFunction(() => document.querySelector('video')?.videoWidth > 0);
  await p.waitForTimeout(800);
  await shot(p, '25-phone-scan-camera');
  await p.getByRole('button', { name: 'Take photo' }).click();
  await p.getByText('Adjust the corners').waitFor();
  await shot(p, '26-phone-scan-crop');
  // move a corner
  const h = p.locator('circle.handle').first();
  const box = await h.boundingBox();
  await p.mouse.move(box.x + box.width / 2, box.y + box.height / 2);
  await p.mouse.down();
  await p.mouse.move(box.x + 30, box.y + 40, { steps: 5 });
  await p.mouse.up();
  await p.getByRole('button', { name: 'Use page' }).click();
  await p.locator('.strip .pg').first().waitFor();
  await p.getByRole('button', { name: 'Take photo' }).click();
  await p.getByRole('button', { name: 'Use page' }).click();
  await until(p, async () => (await p.locator('.strip .pg').count()) === 2, 'two pages');
  await shot(p, '27-phone-scan-pages');
  await p.getByRole('button', { name: /Done/ }).click();
  await p.getByText(/Scan sent \(2 pages\)/).waitFor();
  await until(p, async () => {
    const jobs = await api(p, '/api/jobs');
    const scan = jobs.find((j) => j.kind === 'scan');
    return scan && (scan.state === 'done' || scan.state === 'failed');
  }, 'the scan job');
  const scanJob = (await api(p, '/api/jobs')).find((j) => j.kind === 'scan');
  if (scanJob.state !== 'done') problems.push(`scan job: ${JSON.stringify(scanJob)}`);
  const scanned = await api(p, `/api/docs/${scanJob.doc_id}`);
  if (scanned.doc?.pages !== 2) problems.push(`scan: expected 2 pages, got ${scanned.doc?.pages}`);
  await p.goto(B + `/review/${scanJob.doc_id}`);
  await p.getByLabel('Sender', { exact: true }).waitFor();
  await p.waitForTimeout(1200);
  await shot(p, '28-phone-review');
  log('scan ok');

  // change the pages afterwards: turn the first, swap them (desktop)
  await d.goto(B + `/doc/${scanJob.doc_id}`);
  await d.getByRole('button', { name: 'Edit pages' }).click();
  await d.getByRole('dialog', { name: 'Edit pages' }).waitFor();
  await d.getByRole('button', { name: 'Turn page 1' }).click();
  await d.getByRole('button', { name: 'Move page 1 later' }).click();
  await d.waitForTimeout(500);
  await shot(d, '31-page-editor');
  await d.getByRole('button', { name: 'Crop page 2' }).click();
  await d.getByRole('button', { name: 'Crop', exact: true }).waitFor();
  await d.waitForTimeout(400);
  await shot(d, '32-page-crop');
  await d.getByRole('button', { name: 'Crop', exact: true }).click();
  await d.getByRole('button', { name: 'Save pages' }).click();
  await d.getByText('Pages saved').waitFor();
  const edited = await api(d, `/api/docs/${scanJob.doc_id}`);
  if (edited.doc?.pages !== 2) problems.push(`page editor: ${edited.doc?.pages} pages after saving`);
  log('page editor ok');

  // without HTTPS the camera app is used instead (simulated: no getUserMedia)
  const plain = await browser.newContext({ viewport: { width: 390, height: 844 }, isMobile: true, hasTouch: true, storageState: await desk.storageState() });
  const q = await plain.newPage();
  await q.addInitScript(() => { Object.defineProperty(window, 'isSecureContext', { value: false }); });
  await q.goto(B + '/scan');
  await q.getByRole('button', { name: 'Take a photo' }).waitFor();
  await shot(q, '29-phone-scan-no-https');
  await q.locator('input[capture]').setInputFiles(LETTER_PNG);
  await q.getByText('Adjust the corners').waitFor();
  await shot(q, '30-phone-scan-photo-crop');

  // passkeys: a virtual authenticator; WebAuthn needs a host name, so use localhost
  const L = B.replace('127.0.0.1', 'localhost');
  const pk = await browser.newContext({ viewport: { width: 1440, height: 900 } });
  const k = await pk.newPage();
  watch(k, 'passkey');
  k.setDefaultTimeout(20000);
  const cdp = await pk.newCDPSession(k);
  await cdp.send('WebAuthn.enable');
  await cdp.send('WebAuthn.addVirtualAuthenticator', { options: { protocol: 'ctap2', transport: 'internal', hasResidentKey: true, hasUserVerification: true, isUserVerified: true, automaticPresenceSimulation: true } });
  await k.goto(L + '/');
  await k.getByLabel('Username').fill('anna');
  await k.getByLabel('Password').fill('a-long-password');
  await k.getByRole('button', { name: 'Sign in', exact: true }).click();
  await k.locator('a[href="/archive"]').first().waitFor();
  await k.goto(L + '/settings/account');
  await k.getByLabel('Name of this device').fill('Test laptop');
  await k.getByRole('button', { name: 'Add a passkey' }).click();
  await k.getByText('Passkey added').waitFor();
  await k.getByText('Test laptop').waitFor();
  await shot(k, '33-passkeys');
  await k.goto(L + '/settings/general');
  await k.getByRole('button', { name: 'Sign out' }).click();
  await k.getByRole('button', { name: 'Sign in with a passkey' }).click();
  await k.locator('a[href="/archive"]').first().waitFor();
  const me = await api(k, '/api/me');
  if (me.user?.username !== 'anna') problems.push(`passkey sign-in: ${JSON.stringify(me)}`);
  await shot(k, '34-login-passkey');
  log('passkeys ok');

  // PWA basics
  const manifest = await (await fetch(B + '/manifest.webmanifest')).json();
  if (manifest.display !== 'standalone') problems.push('manifest');
  const sw = await fetch(B + '/sw.js');
  if (!sw.ok) problems.push('service worker missing');
} catch (e) {
  problems.push('FAILED: ' + (e.stack || e.message));
  try { await lastPage?.screenshot({ path: path.join(OUT, '99-failure.png') }); } catch {}
} finally {
  await browser.close();
}

fs.writeFileSync(path.join(OUT, 'report.txt'), problems.length ? problems.join('\n') : 'ok');
if (problems.length) {
  console.error(problems.join('\n'));
  process.exit(1);
}
console.log('e2e ok');
process.exit(0);
