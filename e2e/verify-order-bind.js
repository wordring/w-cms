// 部材発注書の行を加工製品の部材の行に結ぶ（2026-10-23・ext/toho/order_bind.go）と、外注加工表の最新単価（part_price.go）を画面から確かめる。
//
// 利用者:「加工製品の行への結びは人が選び、外注加工表にも最新単価を出してください。」
//
//   ① 業者の納品書から作ったような部材発注書（行は納品済・品名と寸法だけ）で、1行を選んで「🔗 加工製品の行に結ぶ」→ 候補の欄が開き、
//      寸法の数が合う外注加工の行が出る
//   ② 2行を選んで押すと「1つだけ」と言う
//   ③ 候補を押す（確かめる）→ 行に弊社品番・種類・加工内容が入り、もとの記載は備考へ
//   ④ 加工製品ページの外注加工表に「最新単価」1,234円が出る
//
// 当て先は全部自分で作って最後に消します（加工製品・部材発注書）。🤖 解析（Gemini）は呼びません（Go の試験が判定を差し替えて確かめる）。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-order-bind.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const flat = (s) => s.replace(/\s+/g, '');

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1400, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let root = '', product = '', po = '';
  try {
    await login(page, BASE);
    root = await makePage(page, '<h1>【E2E】行を結ぶ</h1>');
    product = await makePage(page, '<h1>【E2E】行を結ぶ品物</h1><dl data-type="tags"><dt>ページ種類</dt><dd>加工製品</dd></dl>' +
      '<table><caption>外注加工表</caption><tbody><tr><th>番号</th><th>加工部品番号</th><th>加工内容</th><th>材質</th><th>個数</th><th>推奨業者</th></tr>' +
      '<tr><td>1</td><td>t9*7771*8882</td><td>【E2E】溶断</td><td>SS</td><td>1</td><td>【E2E】業者</td></tr></tbody></table>', root);
    po = await makePage(page, '<h1>【E2E】行を結ぶ部材発注書</h1><dl data-type="tags"><dt>ページ種類</dt><dd>弊社部材発注書</dd><dt>仕入先</dt><dd>【E2E】業者</dd>' +
      '<dt>納品日</dt><dd>2026-10-06</dd></dl>' +
      '<table><caption>部材発注明細表</caption><tbody><tr><th>弊社品番</th><th>種類</th><th>番号</th><th>品名</th><th>加工部品番号</th><th>加工内容</th><th>材質</th><th>寸法</th><th>数量</th><th>単価</th><th>備考</th><th>状態</th></tr>' +
      '<tr><td></td><td></td><td></td><td>型切</td><td></td><td></td><td></td><td>9x7771x8882</td><td>3</td><td>1234</td><td></td><td>納品済</td></tr>' +
      '<tr><td></td><td></td><td></td><td>寸法切</td><td></td><td></td><td></td><td>9x10x20</td><td>3</td><td>380</td><td></td><td>納品済</td></tr>' +
      '</tbody></table>', root);
    check('当て先を作れた', !!root && !!product && !!po);

    const open = async (rows) => {
      await page.goto(BASE + '/' + po);
      await page.waitForSelector('#w-editor-content .order-pick', { timeout: 8000 }).catch(() => {});
      for (const r of rows) await page.locator('#w-editor-content .order-pick[data-row="' + r + '"]').check();
      await page.click('#w-editor-content .order-bind-open');
      await page.waitForTimeout(800);
    };
    // ②
    await open([1, 2]);
    const say = await page.locator('#w-editor-content .order-row-say').textContent().catch(() => '');
    check('② 2行を選ぶと「1つだけ」と言う', (say || '').includes('1つだけ') && !(await page.locator('#w-editor-content .order-bind-panel').count()), say);
    // ①
    await open([1]);
    await page.waitForSelector('#w-editor-content .order-bind-cand', { timeout: 15000 }).catch(() => {});
    const cands = await page.locator('#w-editor-content .order-bind-cand').allTextContents();
    const mine = cands.findIndex((c) => c.includes('/' + product) && c.includes('【E2E】溶断'));
    check('① 候補の欄が開き、寸法の数が合う外注加工の行が出る', mine >= 0, JSON.stringify(cands.slice(0, 3)));
    // ③
    if (mine >= 0) {
      let asked = '';
      page.once('dialog', (d) => { asked = d.message(); d.accept(); });
      await Promise.all([page.waitForNavigation({ timeout: 10000 }).catch(() => {}),
        page.locator('#w-editor-content .order-bind-cand').nth(mine).click()]);
      await page.waitForTimeout(500);
      const body = flat(await page.evaluate(async (id) => (await fetch('/api/load?id=' + id)).text(), po));
      check('③ 押す前に確かめる', asked.includes('結びます'), asked.slice(0, 60));
      check('③ 行に弊社品番・種類・加工内容が入り、もとの記載は備考へ',
        body.includes('<td>' + product + '</td><td>外注加工</td><td>1</td>') && body.includes('【E2E】溶断') && body.includes('納品書の記載:型切9x7771x8882'),
        body.slice(body.indexOf('部材発注明細表'), body.indexOf('部材発注明細表') + 700));
    }
    // ④
    await page.goto(BASE + '/' + product);
    await page.waitForSelector('#w-editor-content table', { timeout: 8000 }).catch(() => {});
    const tbl = await page.evaluate(() => {
      const t = [...document.querySelectorAll('#w-editor-content table')].find((x) => (x.caption && x.caption.textContent.trim()) === '外注加工表');
      return t ? t.innerText : '';
    });
    check('④ 外注加工表に最新単価 1,234円（2026-10-06・業者）', tbl.includes('最新単価') && tbl.includes('1,234円') && tbl.includes('2026-10-06'), tbl.replace(/\s+/g, ' ').slice(0, 200));
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    for (const id of [po, product, root]) if (id) await deletePage(page, id).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
