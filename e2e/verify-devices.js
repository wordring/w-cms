// 端末の登録を、本物のブラウザで確かめます（2026-10-31・docs/考察/【考察】端末の登録.md）。
//
// 社内と社外の両方が要るので、この試験は自分で一時のデータの w-cms を起こします（本番にも試験用にも触りません）。
//   - 同じ名前 wcms.test を、ブラウザの名前解決で切り替えます。
//   - 社内: この PC の LAN の住所へつなぐ（直に来る要求）。
//   - 社外: 127.0.0.1 へつなぐ（ループバックは「前段のプロキシ」と同じ扱い）。
// LAN の住所が無い機械では飛ばします。
//
// 流し方（リポジトリの e2e/ で）: node verify-devices.js
const { chromium } = require('playwright');
const { spawn, spawnSync } = require('child_process');
const fs = require('fs');
const os = require('os');
const path = require('path');

const PORT = 18943;
const HOST = 'wcms.test';
const BASE = `http://${HOST}:${PORT}`;
const REPO = path.resolve(__dirname, '..');

let failed = 0;
function check(name, ok, detail) {
  console.log((ok ? '  ✓ ' : '  ✗ ') + name + (!ok && detail ? ' — ' + detail : ''));
  if (!ok) failed++;
}

function lanAddress() {
  for (const list of Object.values(os.networkInterfaces())) {
    for (const a of list || []) {
      if (a.family === 'IPv4' && !a.internal && !a.address.startsWith('169.254.')) return a.address;
    }
  }
  return '';
}

async function waitForServer(url) {
  for (let i = 0; i < 100; i++) {
    try {
      const r = await fetch(url);
      if (r.ok) return true;
    } catch (e) { /* まだ起きていない */ }
    await new Promise((res) => setTimeout(res, 200));
  }
  return false;
}

async function login(page, extra) {
  await page.goto(`${BASE}/login`);
  await page.fill('#username', 'a');
  await page.fill('#password', 'a');
  for (const [k, v] of Object.entries(extra || {})) await page.fill(k, v);
  await page.click('button[type=submit]');
  await page.waitForLoadState('domcontentloaded');
}

// meOf は、そのブラウザの名前解決で /api/me を聞きます（page.request は Node の名前解決を使うので使えない）。
async function meOf(page) {
  await page.goto(`${BASE}/login`);
  return page.evaluate(() => fetch('/api/me').then((r) => r.json()));
}

(async () => {
  const lan = lanAddress();
  if (!lan) {
    console.log('LAN の住所が無いので飛ばします');
    return;
  }
  const tmp = fs.mkdtempSync(path.join(os.tmpdir(), 'wcms-devices-'));
  const exe = path.join(tmp, process.platform === 'win32' ? 'w-cms.exe' : 'w-cms');
  const built = spawnSync('go', ['build', '-o', exe, './cmd/w-cms'], { cwd: REPO, encoding: 'utf8' });
  if (built.status !== 0) throw new Error('組み立てに失敗: ' + built.stderr);

  const server = spawn(exe, [], {
    cwd: REPO,
    stdio: 'ignore',
    env: {
      ...process.env,
      WCMS_ADDR: `0.0.0.0:${PORT}`,
      WCMS_DATA_DIR: path.join(tmp, 'data'),
      WCMS_ADMIN_USER: 'a',
      WCMS_ADMIN_PASSWORD: 'a',
      WCMS_SECURE_COOKIES: '0',
      WCMS_DEVICE_REQUIRED: '1',
      WCMS_TRUSTED_PROXIES: '',
      WCMS_TLS_CERT: '',
      WCMS_TLS_KEY: '',
      WCMS_LOCAL_SOCKET: '',
      WCMS_LOCAL_SOCKET_USER: '',
      WCMS_MACHINE_TOKEN: '',
      WCMS_MAIL_AUTO_ADDRESS: '',
    },
  });

  let inside, outside;
  try {
    if (!await waitForServer(`http://127.0.0.1:${PORT}/login`)) throw new Error('サーバーが起きません');
    inside = await chromium.launch({ args: [`--host-resolver-rules=MAP ${HOST} ${lan}`] });
    outside = await chromium.launch({ args: [`--host-resolver-rules=MAP ${HOST} 127.0.0.1`] });

    console.log('社内で登録する');
    const inCtx = await inside.newContext();
    const page = await inCtx.newPage();
    const errors = [];
    page.on('console', (m) => { if (m.type() === 'error' && !/401|404/.test(m.text())) errors.push(m.text()); });
    page.on('pageerror', (e) => errors.push(String(e)));
    await page.goto(`${BASE}/login`);
    check('社内のログインに番号の欄が無い', await page.locator('#device_code').count() === 0);
    await login(page);
    await page.waitForSelector('#w-devices-link:not(.is-hidden)');
    check('上のメニューに端末の入口がある', true);
    await page.click('#w-devices-link');
    await page.waitForSelector('#dev-state:not(:empty)');
    check('社内の案内が出る', (await page.textContent('#dev-state')).includes('番号なしで登録できます'));
    check('社内では番号の欄を出さない', !await page.isVisible('#dev-code'));
    await page.fill('#dev-name', '事務所のパソコン');
    await page.click('#dev-register-btn');
    await page.waitForFunction(() => document.getElementById('dev-state').textContent.includes('登録されています'));
    check('一覧にこの端末が出る', (await page.textContent('#dev-table')).includes('事務所のパソコン（この端末）'));
    check('登録したら登録の欄を隠す', !await page.isVisible('#dev-register'));

    console.log('数字の住所では登録しない');
    const ipPage = await (await inside.newContext()).newPage();
    await ipPage.goto(`http://${lan}:${PORT}/login`);
    await ipPage.fill('#username', 'a');
    await ipPage.fill('#password', 'a');
    await ipPage.click('button[type=submit]');
    await ipPage.waitForLoadState('domcontentloaded');
    await ipPage.goto(`http://${lan}:${PORT}/assets/devices.html`);
    await ipPage.waitForSelector('#dev-state:not(:empty)');
    check('数字の住所の案内が出る', (await ipPage.textContent('#dev-state')).includes('数字の住所'));
    check('数字の住所では登録の欄を出さない', !await ipPage.isVisible('#dev-register'));

    console.log('番号を出す');
    await page.click('#dev-issue-btn');
    await page.waitForFunction(() => /^\d{8}$/.test(document.getElementById('dev-issued').textContent));
    const code = await page.textContent('#dev-issued');
    check('番号が8桁で出る', /^\d{8}$/.test(code));

    console.log('社外の登録のない端末');
    const outCtx = await outside.newContext();
    const outPage = await outCtx.newPage();
    await outPage.goto(`${BASE}/login`);
    check('番号の欄が出る', await outPage.locator('#device_code').count() === 1);
    await login(outPage);
    check('番号なしでは断る', outPage.url().includes('error=device'), outPage.url());
    check('断った文が出る', (await outPage.textContent('.error')).includes('登録されていない'));
    await login(outPage, { '#device_code': '00000000' });
    check('違う番号では断る', outPage.url().includes('error=code') || code === '00000000', outPage.url());

    console.log('番号で端末を足す');
    await login(outPage, { '#device_code': code, '#device_name': '家のパソコン' });
    check('番号で入れる', !outPage.url().includes('/login'), outPage.url());
    await outPage.goto(`${BASE}/assets/devices.html`);
    await outPage.waitForSelector('#dev-state:not(:empty)');
    check('足した端末が登録されている', (await outPage.textContent('#dev-table')).includes('家のパソコン（この端末）'));

    console.log('登録した端末は社外から入れる');
    const state = await inCtx.storageState();
    const carried = await outside.newContext({ storageState: state });
    const carriedPage = await carried.newPage();
    check('社内で登録した端末を社外へ持ち出しても通る', (await meOf(carriedPage)).authenticated === true);

    console.log('登録していない端末のセッションを社外へ持ち出す');
    const bare = await inside.newContext();
    const barePage = await bare.newPage();
    await login(barePage);
    const leaked = await outside.newContext({ storageState: await bare.storageState() });
    const leakedPage = await leaked.newPage();
    check('登録のない端末のセッションは社外で通らない', (await meOf(leakedPage)).authenticated === false);

    console.log('なくした端末を取り消す');
    await page.reload();
    await page.waitForSelector('#dev-table tbody tr');
    page.once('dialog', (d) => d.accept());
    await page.locator('#dev-table tbody tr', { hasText: '家のパソコン' }).locator('button').click();
    await page.waitForFunction(() => !document.getElementById('dev-table').textContent.includes('家のパソコン'));
    check('取り消した端末のログインがすぐに切れる', (await meOf(outPage)).authenticated === false);

    check('画面に誤りが出ない', errors.length === 0, errors.join(' / '));
    const shot = path.join(tmp, 'devices.png');
    await page.setViewportSize({ width: 375, height: 700 });
    await page.screenshot({ path: shot, fullPage: true });
    const overflow = await page.evaluate(() => document.documentElement.scrollWidth > window.innerWidth);
    check('スマホ幅で横にはみ出さない', !overflow);
    const keep = process.env.DEVICES_SHOT;
    if (keep) fs.copyFileSync(shot, keep);
  } finally {
    if (inside) await inside.close();
    if (outside) await outside.close();
    server.kill();
    await new Promise((res) => setTimeout(res, 500));
    try { fs.rmSync(tmp, { recursive: true, force: true }); } catch (e) { /* 掴まれていれば OS の片付けに任せる */ }
  }
  console.log(failed ? `失敗 ${failed} 件` : '全部合格');
  process.exit(failed ? 1 : 0);
})().catch((e) => { console.error(e); process.exit(1); });
