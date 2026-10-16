// 見積計算書を作る（2026-10-16・ext/toho/estimate_calc_doc.go・assets/app.js）を画面から確かめる。
//
// 利用者:「新しい見積計算書を作るボタンから始めてみましょう」「見積もり計算の記録は製造記録傘下になると思います」。
//
//   ① 加工製品ページの「🧮 見積計算書」に、まだ無いことと「➕ 新しい見積計算書を作る」が出る
//   ② 押すと、見積計算書（【弊社】見積計算書　<品物>）が 記録／年／月／製造記録 にでき、そのページが開く
//      ——中身は加工製品ページの「見積もり計算」の写し（見積計算表と見積表）・弊社品番は加工製品ページ
//   ③ 加工製品ページに戻ると、「最新:」にその見積計算書が出る（ロットと単価つき）
//
// 当て先は全部自分で作って最後に消します（製造台帳の下の【E2E】の会社と加工製品・できた見積計算書と、試験が作った空のフォルダ）。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-estimate-calc.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, childrenOf } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const parentOf = (page, id) => page.evaluate(async (pid) => (await (await fetch('/api/page-meta?id=' + pid)).json()).parent_id, id);
const titleOf = (page, id) => page.evaluate(async (pid) => {
  const m = (await (await fetch('/api/load?id=' + pid)).text()).match(/<h1[^>]*>([^<]*)<\/h1>/);
  return m ? m[1] : '';
}, id);

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let cust = '', product = '', calc = '';
  try {
    await login(page, BASE);
    const top = await childrenOf(page, '000000');
    const ledger = (top.find(c => (c.Title || '').trim() === '製造台帳') || {}).ID || '';
    if (!ledger) { console.log('製造台帳が無いので飛ばします'); await browser.close(); process.exit(0); }
    cust = await makePage(page, '<h1>【E2E】見積計算の会社</h1><dl data-type="tags"><dt>ページ種類</dt><dd>社名</dd></dl>', ledger);
    product = await makePage(page, '<h1>【E2E】見積計算の部品</h1><dl data-type="tags"><dt>ページ種類</dt><dd>加工製品</dd></dl>' +
      '<h2>見積もり計算</h2><h3>ロット1</h3>' +
      '<table><caption>見積計算表</caption><tbody><tr><th>工程</th><th>価格</th><th>備考</th></tr><tr><td>材料</td><td>620</td><td></td></tr></tbody></table>' +
      '<table><caption>見積表</caption><tbody><tr><th>ロット</th><th>表面処理</th><th>原価</th><th>利益率</th><th>単価</th><th>備考</th></tr>' +
      '<tr><td>1</td><td></td><td>620</td><td>10</td><td>682</td><td></td></tr></tbody></table>', cust);
    check('当て先を作れた', !!cust && !!product);

    // ①
    await page.goto(BASE + '/' + product);
    const box = page.locator('#w-editor-content .estimate-calcs');
    await box.waitFor({ timeout: 10000 }).catch(() => {});
    const t1 = (await box.textContent().catch(() => '')) || '';
    check('① 「🧮 見積計算書」に、まだ無いことと作るボタン', t1.includes('見積計算書はまだありません') && t1.includes('新しい見積計算書を作る'), t1.slice(0, 160));

    // ②
    await Promise.all([
      page.waitForURL((u) => !u.pathname.endsWith('/' + product), { timeout: 15000 }).catch(() => {}),
      box.locator('.estimate-calc-new').click(),
    ]);
    calc = (page.url().match(/\/(\d{6})(?:[?#]|$)/) || [])[1] || '';
    check('② 押すと見積計算書が開く', !!calc && calc !== product, page.url());
    if (calc) {
      const raw = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).text(), calc);
      check('② 題は「【弊社】見積計算書　<品物>」', raw.includes('<h1>【弊社】見積計算書　【E2E】見積計算の部品</h1>'), raw.slice(0, 120));
      check('② 中身は見積もり計算の写し・弊社品番は加工製品ページ', raw.includes('<caption>見積計算表</caption>') && raw.includes('<caption>見積表</caption>') &&
        raw.includes('<dt>弊社品番</dt>') && raw.includes(product) && raw.includes('<dd>弊社見積計算書</dd>'), raw.replace(/\s+/g, ' ').slice(0, 300));
      const chain = [];
      let cur = calc;
      for (let i = 0; i < 4; i++) { cur = await parentOf(page, cur); chain.push(await titleOf(page, cur)); }
      check('② 置き場は 記録／年／月／製造記録', chain[0] === '製造記録' && /^\d{2}月$/.test(chain[1]) && /^\d{4}年$/.test(chain[2]) && chain[3] === '記録', chain.join(' ← '));
    }

    // ③
    await page.goto(BASE + '/' + product);
    await box.waitFor({ timeout: 10000 }).catch(() => {});
    const t3 = (await box.textContent().catch(() => '')) || '';
    const link = await box.locator('.estimate-calc-latest a').getAttribute('href').catch(() => '');
    check('③ 戻ると「最新:」にその見積計算書（ロットと単価）', t3.includes('最新:') && link === '/' + calc && t3.includes('ロット1: 682円'), t3.slice(0, 160));

    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    // 見積計算書と、試験が作った空のフォルダ（当て先より新しい番号）を消す。
    if (calc) {
      let f = await parentOf(page, calc).catch(() => '');
      await deletePage(page, calc).catch(() => {});
      for (let i = 0; i < 3 && f && Number(f) > Number(product || 0); i++) {
        if ((await childrenOf(page, f).catch(() => [1])).length !== 0) break;
        const up = await parentOf(page, f).catch(() => '');
        await deletePage(page, f).catch(() => {});
        f = up;
      }
    }
    if (product) await deletePage(page, product).catch(() => {});
    if (cust) await deletePage(page, cust).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
