// probe-fax-buttons.js — 部材発注書・部材見積依頼書・見積書の送る欄の「📠 FAX で送る」が出て、押すと送り先を尋ねるかを見ます
// （2026-10-25 夜・comm.FaxSendButtonHTML・ext/comm/assets/comm.js）。
// ⚠ 送りません——番号の問いで「やめる」を選ぶ（電話をかけない）。ページは書き換えない。
// 使い方: WCMS_BASE=https://<試験用>:9443 PAGES=002201,002217,002223 node probe-fax-buttons.js
const { chromium } = require('playwright');
const { login, sendWay } = require('./lib');

(async () => {
  const base = process.env.WCMS_BASE || 'https://localhost:9443';
  const pages = (process.env.PAGES || '').split(',').filter(Boolean);
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 1000 } });
  const page = await ctx.newPage();
  let bad = 0;
  const sent = [];
  // 万一押し進んでも送らないよう、送る口は試験のブラウザの中で止める。
  await page.route('**/api/fax/send', (route) => { sent.push(route.request().postData()); route.fulfill({ status: 503, body: '{"success":false,"message":"試験で止めた"}' }); });
  let prompts = 0;
  page.on('dialog', (d) => { prompts++; d.dismiss(); }); // 番号を打つ問い（prompt）は「やめる」
  await login(page, base);
  for (const id of pages) {
    await page.goto(base + '/' + id);
    await page.waitForTimeout(1500);
    const btn = page.locator('[data-fax-purpose]');
    const n = await btn.count();
    if (n !== 1) { console.log('✗ /' + id + ': FAX のボタンが ' + n + ' 個'); bad++; continue; }
    const purpose = await btn.getAttribute('data-fax-purpose');
    const numbers = await page.evaluate(async (pid) => (await (await fetch('/api/fax/numbers?page_id=' + pid)).json()).numbers, id);
    const before = prompts;
    // 送り方の欄で FAX を選んで「▶ 送る」（2026-10-26 から、FAX のボタンは隠してある）。
    await sendWay(page, '[data-fax-purpose]');
    await page.waitForTimeout(800);
    // 候補があれば選ぶ欄が出る。出ていれば閉じる（Escape）。
    const dlg = await page.locator('dialog[open], .w-dialog, [role="dialog"]').count();
    if (!dlg && prompts === before) { console.log('✗ /' + id + ': FAX を選んで押しても、番号を尋ねません'); bad++; continue; }
    console.log('✓ /' + id + ': 「' + purpose + '」の FAX のボタン・候補 ' + JSON.stringify(numbers) + '・押すと問い ' + (dlg ? 'あり（選ぶ欄）' : '（番号を打つ欄・やめた）'));
    await page.keyboard.press('Escape');
    await page.waitForTimeout(300);
  }
  if (sent.length) { console.log('✗ 送る口が呼ばれた: ' + sent.join(' / ')); bad++; }
  console.log(bad ? '✗ ' + bad + ' 件' : '✓ ぜんぶ');
  await browser.close();
  process.exit(bad ? 1 : 0);
})();
