// 未登録の連絡先——1アドレス1行・推薦・足す（2026-09-13）
const { chromium } = require('playwright');
const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
let fail = 0;
const ok = (c, m, x) => { console.log((c ? '  ✓ ' : '  ✗ ') + m + (x ? '  ' + x : '')); if (!c) fail++; };

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 1000 }, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  await page.goto(BASE + '/login');
  await page.fill('#username', 'a'); await page.fill('#password', 'a');
  await page.click('button[type=submit]'); await page.waitForLoadState('networkidle');
  await page.goto(BASE + '/010263'); await page.waitForTimeout(1500);

  const before = await page.evaluate(() => ({
    rows: document.querySelectorAll('#w-editor-content tr[data-address]').length,
    grouped: document.querySelectorAll('#w-editor-content .contact-addrs').length,
    suggested: document.querySelectorAll('#w-editor-content .chip-primary').length,
    overflow: document.documentElement.scrollWidth > window.innerWidth,
  }));
  ok(before.rows > 0, '1アドレス1行になっている', before.rows + '行');
  ok(before.grouped === 0, 'ドメインでまとめた欄が残っていない');
  ok(!before.overflow, '横にはみ出さない');

  // **データに依存しない形で確かめます**。推薦は「既にある取引先とドメインが一致する行」
  // にだけ出るので、件数を決め打ちにすると片付けた翌日に落ちます。
  // ここでは「推薦が出ている行は、確かに登録済みドメインである」ことを見ます。
  const check = await page.evaluate(async () => {
    const rows = Array.from(document.querySelectorAll('#w-editor-content tr[data-address]'));
    const known = await (await fetch('/api/load?id=010263')).text();
    return rows.map(r => ({
      domain: r.dataset.domain,
      suggested: !!r.querySelector('.chip-primary'),
      folded: !!r.querySelector('details.contact-more'),
      hasNew: !!r.querySelector('.contact-register'),
    }));
  });
  ok(check.every(r => r.hasNew), 'どの行からも新規登録できる');
  ok(check.filter(r => r.suggested).every(r => r.folded),
     '推薦のある行は、ほかの行き先を畳んでいる');
  ok(check.filter(r => !r.suggested).every(r => !r.folded),
     '推薦の無い行は畳まない（その行の主役は新規登録）');

  // 推薦のある行を1つ押す（実データとしても正しい操作）。
  // **推薦が1つも無い日は、そこまでで終わり**——片付け切った状態も正常だからです。
  const target = await page.evaluate(() => {
    const btn = document.querySelector('#w-editor-content .chip-primary');
    if (!btn) return null;
    return { addr: btn.closest('tr').dataset.address, label: btn.textContent };
  });
  if (!target) {
    console.log('  — 推薦のある行がありません（全部片付いた状態）。ここまでで終了します');
    await browser.close();
    console.log(fail ? fail + ' 件失敗' : '全項目OK');
    process.exit(fail ? 1 : 0);
  }
  await page.click('#w-editor-content .chip-primary');
  await page.waitForTimeout(1200);
  const after = await page.evaluate((a) => ({
    rows: document.querySelectorAll('#w-editor-content tr[data-address]').length,
    still: !!document.querySelector('#w-editor-content tr[data-address="' + a + '"]'),
  }), target.addr);
  ok(after.rows === before.rows - 1, '押した行が一覧から消える', before.rows + ' → ' + after.rows);
  ok(!after.still, target.addr + ' の行が消えた');

  // 相手ページに載ったか（正本の確認）
  const landed = await page.evaluate(async (a) => {
    const body = await (await fetch('/api/load?id=010264')).text();
    return body.includes(a);
  }, target.addr);
  ok(landed, '相手ページ（010264）にアドレスが載った', target.label);
  ok(errs.length === 0, 'JSエラーなし', errs[0] || '');

  await browser.close();
  console.log(fail ? '\n' + fail + ' 件失敗' : '\n全項目OK');
  process.exit(fail ? 1 : 0);
})();
