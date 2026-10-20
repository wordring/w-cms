// 拡張の画面の JS（2026-10-20・docs/考察/【考察】画面の JS のコアと拡張.md）が読み込まれ、コアの window.wcms の口で飾りを付けることを確かめます。
//
//   ① 殻に、載っている拡張の JS の <script> が差し込まれ、コアの app.js のあとにある
//   ② window.wcms があり、凍っている（拡張が書き換えられない）
//   ③ 通信の JS: `電話番号` のタグのあるページに「☎」の飾り（refreshPhoneChrome）
//   ④ 連絡帳の JS: `メールアドレス` のタグのあるページに「未分類へ戻す」（refreshContactUnfile）
//   ⑤ ページのエラーが無い
//
// ⚠ 自分で作ったページの上だけで動きます（職場は実運用）。最後に消します。
const { chromium } = require('playwright');
const lib = require('./lib');
const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
let fail = 0;
const ok = (c, m, x) => { console.log((c ? '  ✓ ' : '  ✗ ') + m + (x ? '  ' + x : '')); if (!c) fail++; };

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 1000 }, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  await lib.login(page, BASE);
  let id = '';
  try {
    id = await lib.makePage(page, '<h1>【E2E】拡張の画面の JS</h1><dl data-type="tags"><dt>電話番号</dt><dd>000-0000-0000</dd>' +
      '<dt>メールアドレス</dt><dd>e2e-ext-scripts@example.com</dd></dl><p>本文</p>');
    ok(!!id, 'ページを作った', id);
    await page.goto(BASE + '/' + id);
    await page.waitForTimeout(1500);

    const order = await page.evaluate(() => Array.from(document.scripts).map((s) => s.getAttribute('src') || ''));
    const app = order.indexOf('/assets/app.js');
    const ext = order.filter((s) => s.startsWith('/assets/ext/'));
    ok(app >= 0 && ext.length > 0, '① 拡張の JS が差し込まれている', ext.join(' '));
    ok(ext.every((s) => order.indexOf(s) > app), '① 拡張の JS はコアの app.js のあと');

    const wcms = await page.evaluate(() => ({ has: !!window.wcms, frozen: Object.isFrozen(window.wcms || {}), version: (window.wcms || {}).version }));
    ok(wcms.has && wcms.frozen, '② window.wcms があり、凍っている', 'version ' + wcms.version);

    // 閲覧と書ける形の両方で飾りが付く（モードを切り替えるたびに付け直す口）。
    for (const mode of ['閲覧', '書ける形']) {
      const got = await page.evaluate(() => ({
        phone: !!document.querySelector('#w-editor-content .phone-chrome'),
        unfile: !!document.querySelector('#w-editor-content .contact-unfile'),
      }));
      ok(got.phone, '③ 通信の JS: ☎ の飾り（' + mode + '）');
      ok(got.unfile, '④ 連絡帳の JS: 未分類へ戻す（' + mode + '）');
      await page.evaluate(() => { const t = document.getElementById('w-mode-toggle'); if (t) t.click(); });
      await page.waitForTimeout(800);
    }
    ok(errs.length === 0, '⑤ ページのエラーが無い', errs.join(' | '));
  } finally {
    await lib.deletePage(page, id);
    await browser.close();
  }
  console.log(fail ? '結果: ' + fail + ' 件落ちました' : '結果: すべて通りました');
  process.exit(fail ? 1 : 0);
})();
