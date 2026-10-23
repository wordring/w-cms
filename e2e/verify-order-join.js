// 相乗り（2026-10-21・ext/toho/order_join.go・【考察】製造単位の部材発注 §6.3 の6番）を画面から確かめる。
//
// 2026-10-21 利用者:「相乗りする発注書のページIDを指定で良いと思います。「同じ仕入れ先の～」のような相乗り候補を知らせてくれる機能があればさらに良いです」
//
//   ① 製造A（8台）の製造明細表で行を選んで「🧾 部材作業」→ 必要部材表で行を選んで「🛒 発注作業」→ 発注部材表の下の「部材発注書を作る」で
//      部材発注書（材料 2個/台 × 8 = 16）
//   ② 製造B（5台）も「🧾 部材作業」→「🛒 発注作業」→ 発注部材表の下の欄に、①の部材発注書が相乗りの候補として出る・押すと欄にページ番号が入る
//   ③ 「部材発注書を作る」で、新しく作らず①の部材発注書に足す——同じ部材の行の数量は 26、「製造」の列に A と B、製造Bの発注部材表は消える
//   ④ 製造Bの「この製造の書類」にも①の部材発注書が並ぶ
//
// 当て先は全部自分で作って最後に消します（案件ページ・加工製品・製造ページ2枚・部材発注書）。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-order-join.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const titleOf = (page, id) => page.evaluate(async (pid) => ((await (await fetch('/api/load?id=' + pid)).text()).match(/<h1[^>]*>([^<]*)</) || [, ''])[1], id);
const docsOf = (page, id) => page.evaluate(async (pid) => {
  const v = await (await fetch('/api/load?view=1&id=' + pid)).text();
  const at = v.indexOf('class="mfg-docs"');
  if (at < 0) return [];
  return [...v.slice(at, v.indexOf('</div>', at)).matchAll(/href="\/(\d{6})"/g)].map((m) => m[1]);
}, id);
const SUP = '【E2E】相乗りの業者';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1400, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let root = '', product = '', order = '', mfgA = '', mfgB = '', po = '';
  const docs = new Set();
  try {
    await login(page, BASE);
    root = await makePage(page, '<h1>【E2E】相乗り</h1>');
    product = await makePage(page, '<h1>【E2E】相乗りの品物</h1><dl data-type="tags"><dt>ページ種類</dt><dd>加工製品</dd></dl><table><caption>材料表</caption><tbody><tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>' +
      '<tr><td>【E2E】鋼</td><td>FB</td><td>t7*77*777</td><td>2</td></tr></tbody></table>', root);
    order = await makePage(page, '<h1>【E2E】相乗りの案件</h1><dl data-type="tags"><dt>ページ種類</dt><dd>顧客発注書</dd><dt>発注書番号</dt><dd>E2E-OJ-1</dd>' +
      '<dt>発注元</dt><dd>【E2E】相乗りの客先</dd><dt>納期</dt><dd>2026-12-31</dd></dl>' +
      '<table><caption>案件明細表</caption><tbody><tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th><th>状態</th><th>製造完了</th></tr>' +
      '<tr><td>' + product + '</td><td>E2E-OJ-A</td><td>【E2E】部品</td><td>20</td><td>個</td><td>未着手</td><td></td></tr></tbody></table>', root);
    const start = (qty) => page.evaluate(async (a) => {
      const r = await fetch('/api/mfg/new', { method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ items: [{ order: a.oid, row: 0, item: 'E2E-OJ-A', qty: a.qty }] }) });
      return r.json();
    }, { oid: order, qty });
    mfgA = (await start('8')).page_id || '';
    mfgB = (await start('5')).page_id || '';
    check('当て先を作れた', !!root && !!product && !!order && !!mfgA && !!mfgB, mfgA + ' ' + mfgB);

    // 製造ページを開き、picks のチェックを全部入れて btn を押す（押すと読み直す）。
    const press = async (mfg, picks, btn) => {
      await page.goto(BASE + '/' + mfg);
      const b = page.locator('#w-editor-content ' + btn);
      await b.first().waitFor({ timeout: 10000 }).catch(() => {});
      for (const c of await page.locator('#w-editor-content ' + picks).all()) await c.check();
      await Promise.all([
        page.waitForNavigation({ timeout: 15000 }).catch(() => {}),
        b.first().click().catch(() => {}),
      ]);
    };
    // 「🧾 部材作業」で必要部材表 →「🛒 発注作業」で発注部材表（下に「部材発注書を作る」の欄）。
    const parts = async (mfg) => {
      await press(mfg, '.mfg-pick', '.mfg-act-parts');
      await press(mfg, '.mfg-needs-pick', '.mfg-needs-order');
      await page.waitForSelector('#w-editor-content .draft-form', { timeout: 10000 }).catch(() => {});
    };

    // ①
    await parts(mfgA);
    const signer = page.locator('#w-editor-content .draft-form select[data-unorder="signer"] option');
    if ((await signer.count()) < 2) {
      console.log('- 差出人（署名のある人）が連絡帳に無いので、部材発注書を作る所から先は飛ばします');
    } else {
      await page.selectOption('#w-editor-content .draft-form select[data-unorder="signer"]', { index: 1 });
      await page.fill('#w-editor-content .draft-form input[data-draft="supplier"]', SUP);
      await Promise.all([
        page.waitForNavigation({ timeout: 20000 }).catch(() => {}),
        page.click('#w-editor-content .draft-form [data-draft-go]'),
      ]);
      for (const d of await docsOf(page, mfgA)) {
        docs.add(d);
        if ((await titleOf(page, d)).includes(SUP)) po = d;
      }
      check('① 製造Aから部材発注書ができた', !!po);

      // ②
      if (po) {
        await parts(mfgB);
        const pick = page.locator('#w-editor-content .draft-join-pick[data-join="' + po + '"]');
        check('② 製造Bの発注部材表の足元に、①の部材発注書が相乗りの候補', (await pick.count()) === 1);
        await pick.first().click().catch(() => {});
        const v = await page.inputValue('#w-editor-content .draft-form input[data-draft="join"]').catch(() => '');
        check('② 候補を押すと欄にページ番号', v === po, v);

        // ③
        await page.fill('#w-editor-content .draft-form input[data-draft="supplier"]', SUP);
        await Promise.all([
          page.waitForNavigation({ timeout: 20000 }).catch(() => {}),
          page.click('#w-editor-content .draft-form [data-draft-go]'),
        ]);
        const body = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).text(), po);
        const at = body.indexOf('【E2E】鋼');
        const row = at < 0 ? '' : body.slice(body.lastIndexOf('<tr', at), body.indexOf('</tr>', at));
        check('③ 同じ部材の行の数量は 26（16 + 10）', />\s*26\s*</.test(row), row.replace(/\s+/g, ' '));
        check('③ 「製造」の列に A と B', row.includes(mfgA + '、' + mfgB), row.replace(/\s+/g, ' '));
        const bBody = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).text(), mfgB);
        check('③ 製造Bの発注部材表は消えた', !bBody.includes('<caption>発注部材表</caption>'));
        // ④
        check('④ 製造Bの「この製造の書類」にも①の部材発注書', (await docsOf(page, mfgB)).includes(po));
      }
    }
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    for (const m of [mfgA, mfgB]) if (m) for (const d of await docsOf(page, m).catch(() => [])) docs.add(d);
    for (const d of docs) await deletePage(page, d).catch(() => {});
    for (const id of [mfgA, mfgB, order, product, root]) if (id) await deletePage(page, id).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
