// 製造ページの手配不要（2026-10-21・ext/toho/mfg_skip.go・【考察】製造単位の部材発注 §6.3 の7番）を画面から確かめる。
//
//   ① 「📦 この製造の部材を発注部材表へ」で発注部材表（材料 2個/台 × 6 = 12）
//   ② 行の「🚫 不要」（理由を入れる）→ 手配不要表へ移る（理由・日付）・発注部材表は消える
//   ③ もう一度「📦」を押しても出ない（手配不要のぶんを引く）
//   ④ 手配不要表の「↩ 戻す」→ 表が消え、「📦」でまた発注部材表に出る
//
// 当て先は全部自分で作って最後に消します（案件ページ・加工製品・製造ページ）。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-mfg-skip.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const bodyOf = (page, id) => page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).text(), id);

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1400, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let root = '', product = '', order = '', mfg = '';
  try {
    await login(page, BASE);
    root = await makePage(page, '<h1>【E2E】手配不要</h1>');
    product = await makePage(page, '<h1>【E2E】手配不要の品物</h1><dl data-type="tags"><dt>ページ種類</dt><dd>加工製品</dd></dl><table><caption>材料表</caption><tbody><tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>' +
      '<tr><td>【E2E】銅</td><td>FB</td><td>t6*66*666</td><td>2</td></tr></tbody></table>', root);
    order = await makePage(page, '<h1>【E2E】手配不要の案件</h1><dl data-type="tags"><dt>ページ種類</dt><dd>顧客発注書</dd><dt>発注書番号</dt><dd>E2E-SK-1</dd>' +
      '<dt>発注元</dt><dd>【E2E】手配不要の客先</dd><dt>納期</dt><dd>2026-12-31</dd></dl>' +
      '<table><caption>案件明細表</caption><tbody><tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th><th>状態</th><th>製造完了</th></tr>' +
      '<tr><td>' + product + '</td><td>E2E-SK-A</td><td>【E2E】部品</td><td>6</td><td>個</td><td>未着手</td><td></td></tr></tbody></table>', root);
    const made = await page.evaluate(async (oid) => {
      const r = await fetch('/api/mfg/new', { method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ items: [{ order: oid, row: 0, item: 'E2E-SK-A', qty: '6' }] }) });
      return r.json();
    }, order);
    mfg = made.page_id || '';
    check('当て先を作れた', !!root && !!product && !!order && !!mfg, JSON.stringify(made));

    const parts = async () => {
      await page.goto(BASE + '/' + mfg);
      await Promise.all([
        page.waitForNavigation({ timeout: 8000 }).catch(() => {}),
        page.click('#w-editor-content .mfg-parts-go'),
      ]);
      await page.waitForTimeout(800);
    };
    // ①
    await parts();
    let b = await bodyOf(page, mfg);
    check('① 発注部材表（【E2E】銅 を 12）', b.includes('<caption>発注部材表</caption>') && b.includes('【E2E】銅') && />\s*12\s*</.test(b));
    // ②
    await page.goto(BASE + '/' + mfg);
    const skip = page.locator('#w-editor-content .draft-row-skip');
    await skip.first().waitFor({ timeout: 8000 }).catch(() => {});
    check('② 行に「🚫 不要」', (await skip.count()) === 1);
    page.once('dialog', (d) => d.accept('【E2E】在庫で足りる'));
    await Promise.all([
      page.waitForNavigation({ timeout: 15000 }).catch(() => {}),
      skip.first().click().catch(() => {}),
    ]);
    b = await bodyOf(page, mfg);
    const at = b.indexOf('<caption>手配不要表</caption>');
    check('② 手配不要表へ移った（理由つき）', at >= 0 && b.slice(at).includes('【E2E】在庫で足りる') && b.slice(at).includes('【E2E】銅'));
    check('② 発注部材表は消えた', !b.includes('<caption>発注部材表</caption>'));
    // ③
    await parts();
    b = await bodyOf(page, mfg);
    check('③ もう一度「📦」を押しても出ない', !b.includes('<caption>発注部材表</caption>'));
    const say = await page.locator('#w-editor-content .mfg-parts-say').textContent().catch(() => '');
    check('③ 「発注する部材はありません」と言う', (say || '').includes('ありません'), say);
    // ④
    await page.goto(BASE + '/' + mfg);
    const back = page.locator('#w-editor-content .skip-row-back');
    await back.first().waitFor({ timeout: 8000 }).catch(() => {});
    await Promise.all([
      page.waitForNavigation({ timeout: 15000 }).catch(() => {}),
      back.first().click().catch(() => {}),
    ]);
    b = await bodyOf(page, mfg);
    check('④ 「↩ 戻す」で手配不要表が消えた', !b.includes('手配不要表'));
    await parts();
    b = await bodyOf(page, mfg);
    check('④ 「📦」でまた発注部材表に出る', b.includes('<caption>発注部材表</caption>') && b.includes('【E2E】銅'));
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    for (const id of [mfg, order, product, root]) if (id) await deletePage(page, id).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
