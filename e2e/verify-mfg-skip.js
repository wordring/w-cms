// 製造ページの手配不要（2026-10-21・ext/toho/mfg_skip.go・【考察】製造単位の部材発注 §6.3 の7番）を画面から確かめる。
//
//   ① 製造明細表で行を選んで「🧾 部材作業」→ 必要部材表（材料 2個/台 × 6 = 12）
//   ② 必要部材表で行を選んで「🚫 不要」（理由を入れる）→ 手配不要表へ移る（理由・日付）・必要部材表は消える
//   ③ もう一度「🧾 部材作業」を押しても集まらない（手配不要のぶんを引く）・「ありません」と言う
//   ④ 手配不要表で行を選んで「↩ 戻す」→ 表が消え、「🧾 部材作業」でまた必要部材表に出る
//   ⑤ 「🛒 発注作業」で発注部材表へ移し、発注部材表で行を選んで下の欄の「🚫 不要」→ 手配不要表へ移る・発注部材表は消える
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

    // 製造ページを開き、picks のチェックを全部入れて btn を押す（押すと読み直す。prompt があれば理由を入れる）。
    const press = async (picks, btn, opts = {}) => {
      await page.goto(BASE + '/' + mfg);
      const bt = page.locator('#w-editor-content ' + btn);
      await bt.first().waitFor({ timeout: 10000 }).catch(() => {});
      for (const c of await page.locator('#w-editor-content ' + picks).all()) await c.check();
      if (opts.prompt !== undefined) page.once('dialog', (d) => d.accept(opts.prompt));
      const nav = page.waitForNavigation({ timeout: opts.wait || 15000 }).catch(() => {});
      await bt.first().click().catch(() => {});
      // 「🧾 部材作業」の警告（もう部材を手配した行・2026-10-23）が出たら opts.choice 番目を選ぶ。
      if (opts.choice !== undefined) {
        const opt = page.locator('dialog.w-confirm .w-choice-list button');
        await opt.first().waitFor({ timeout: 6000 }).catch(() => {});
        opts.shown = (await opt.count()) > 0;
        if (opts.shown) await opt.nth(opts.choice).click();
      }
      await nav;
    };
    const parts = (wait) => press('.mfg-pick', '.mfg-act-parts', { wait });
    const has = (body, caption) => body.includes('<caption>' + caption + '</caption>');
    // ①
    await parts();
    let b = await bodyOf(page, mfg);
    check('① 必要部材表（【E2E】銅 を 12）', has(b, '必要部材表') && b.includes('【E2E】銅') && />\s*12\s*</.test(b));
    // ②
    await page.goto(BASE + '/' + mfg);
    check('② 必要部材表の下に「🚫 不要」', (await page.locator('#w-editor-content .mfg-needs-bar .mfg-needs-skip').count()) === 1);
    await press('.mfg-needs-pick', '.mfg-needs-skip', { prompt: '【E2E】在庫で足りる' });
    b = await bodyOf(page, mfg);
    let at = b.indexOf('<caption>手配不要表</caption>');
    check('② 手配不要表へ移った（理由・日付つき）', at >= 0 && b.slice(at).includes('【E2E】在庫で足りる') && b.slice(at).includes('【E2E】銅') && /\d{4}-\d{2}-\d{2}/.test(b.slice(at, b.indexOf('</table>', at))));
    check('② 必要部材表は消えた', !has(b, '必要部材表'));
    // ③
    const again = { wait: 4000, choice: 0 }; // 手配不要にしたぶんも手配済み——警告で「まだ手配していない部材だけ」を選ぶ
    await press('.mfg-pick', '.mfg-act-parts', again);
    check('③ もう一度「🧾 部材作業」を押すと、もう始めていると警告する', again.shown === true);
    b = await bodyOf(page, mfg);
    check('③ 「まだ手配していない部材だけ」では集まらない', !has(b, '必要部材表'));
    const say = await page.locator('#w-editor-content .mfg-items-bar .mfg-bar-say').textContent().catch(() => '');
    check('③ 「手配する部材はありません」と言う', (say || '').includes('ありません'), say);
    // ④
    await page.goto(BASE + '/' + mfg);
    check('④ 手配不要表の行の先頭にチェック・下に「↩ 戻す」', (await page.locator('#w-editor-content table input.skip-pick').count()) === 1 &&
      (await page.locator('#w-editor-content .mfg-skip-bar .skip-sel-back').count()) === 1);
    await press('.skip-pick', '.skip-sel-back');
    b = await bodyOf(page, mfg);
    check('④ 「↩ 戻す」で手配不要表が消えた', !has(b, '手配不要表'));
    await parts();
    b = await bodyOf(page, mfg);
    check('④ 「🧾 部材作業」でまた必要部材表に出る', has(b, '必要部材表') && b.includes('【E2E】銅'));
    // ⑤
    await press('.mfg-needs-pick', '.mfg-needs-order');
    b = await bodyOf(page, mfg);
    check('⑤ 「🛒 発注作業」で発注部材表へ', has(b, '発注部材表') && !has(b, '必要部材表'));
    await press('.draft-pick', '.draft-form-box .draft-sel-skip', { prompt: '【E2E】自社で作る' });
    b = await bodyOf(page, mfg);
    at = b.indexOf('<caption>手配不要表</caption>');
    check('⑤ 発注部材表の「🚫 不要」で手配不要表へ（理由つき）', at >= 0 && b.slice(at).includes('【E2E】自社で作る') && b.slice(at).includes('【E2E】銅'));
    check('⑤ 発注部材表は消えた', !has(b, '発注部材表'));
    const inTable = await page.evaluate(() => document.querySelectorAll('#w-editor-content table button').length);
    check('⑤ 表の中にボタンは無い', inTable === 0, String(inTable));
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
