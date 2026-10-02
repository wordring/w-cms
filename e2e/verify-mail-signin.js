// 通信箱の「✉️ メールにサインイン」が、サインインしていないときに出て、押すと番号とリンクを出し、
// 済んだら消えるかを**ブラウザから**確かめる（2026-10-02）。
//
// 利用者:「メールの画面からもう一度サインインを教えてください」——サインインの口（POST /api/mail/signin）はあったが、
// 押す道が画面に無かった。
//
// ⚠ **Microsoft には繋ぎません**——`/api/mail/status` と `/api/mail/signin` をブラウザの中で差し替えます
// （サインインしていない → 番号を返す → 2回目の status でサインインした、の筋書き）。本物の保管には触りません。
// 通信箱は読むだけ（何も書かない）。
//
// 使い方: WCMS_BASE=https://localhost:8443 node verify-mail-signin.js
const { chromium } = require('playwright');
const { login, childrenOf } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(e.message));
  await login(page, BASE);

  let bad = 0;
  const check = (ok, msg, detail) => {
    if (ok) console.log('✓ ' + msg);
    else { console.log('✗ ' + msg + (detail ? '（' + detail + '）' : '')); bad++; }
  };

  const box = (await childrenOf(page, '000000')).find((c) => (c.Title || c.title) === '通信箱');
  if (!box) {
    console.log('飛ばします: 「通信箱」ページが見つかりません');
    await browser.close();
    return;
  }
  const target = BASE + '/' + (box.ID || box.id);

  // ① サインインしているとき——ボタンは出ない（本物の status をそのまま使う・押さない）。
  //    サインインしていない環境なら飛ばす。
  await page.goto(target);
  await page.waitForTimeout(1200);
  const real = await page.evaluate(async () => (await fetch('/api/mail/status')).json());
  if (real && real.configured && real.address) {
    check(await page.locator('#w-mail-signin').count() === 0, 'サインインしているときは「✉️ メールにサインイン」を出さない');
  } else {
    console.log('（この環境はサインインしていないので①は飛ばします）');
  }

  // ② サインインしていない筋書き——status を差し替える。
  let signedIn = false;
  let started = 0;
  await page.route('**/api/mail/status', (route) => route.fulfill({
    status: 200, contentType: 'application/json',
    body: JSON.stringify({ success: true, configured: true, address: signedIn ? 'e2e@example.com' : '' }),
  }));
  await page.route('**/api/mail/signin', (route) => {
    started++;
    // 番号を返した直後に「サインインした」にする（次の status で済む）。
    signedIn = true;
    route.fulfill({
      status: 200, contentType: 'application/json',
      body: JSON.stringify({ success: true, verification_uri: 'https://example.com/devicelogin', user_code: 'E2ECODE1', expires_in: 60 }),
    });
  });
  await page.goto(target);
  await page.waitForTimeout(1500);
  const btn = page.locator('#w-mail-signin');
  check(await btn.count() === 1, 'サインインしていないときは「✉️ メールにサインイン」が出る');
  if (await btn.count() === 1) {
    await btn.click();
    await page.waitForTimeout(500);
    const said = await page.locator('#w-mail-signin-box').textContent();
    const href = await page.locator('#w-mail-signin-box a').getAttribute('href').catch(() => '');
    check(started === 1, '押すとサインインを始める（口を1回呼ぶ）', started + ' 回');
    check(said.includes('E2ECODE1') && href === 'https://example.com/devicelogin', '番号と Microsoft の画面へのリンクを出す', said);
    // 3秒おきの status で済んだと分かる。
    await page.waitForTimeout(4000);
    const after = await page.locator('#w-mail-signin-box').textContent();
    check(await page.locator('#w-mail-signin').count() === 0 && after.includes('e2e@example.com'),
      '済んだらボタンが消え、サインインしたアドレスを出す', after);
  }
  await page.unroute('**/api/mail/status');
  await page.unroute('**/api/mail/signin');

  check(errs.length === 0, 'JSエラーなし', errs.join(' / '));
  console.log(bad === 0 ? '\n結果: 合格' : '\n結果: ' + bad + '件の不合格');
  await browser.close();
  process.exitCode = bad === 0 ? 0 : 1;
})();
