// 逃げ道（2026-10-21・ext/toho/order_unsend.go・【考察】製造単位の部材発注 §6.4）を画面から確かめる。
//
//   ① 製造明細表で行を選んで「🧾 部材」→ 必要部材表で「🛒 発注」→「部材発注書を作る」で部材発注書（材料 2個/台 × 6 = 12）。
//      もう一度「🧾 部材」を押しても集まらない
//   ② 行を選んで「➕ 追加の発注」（確かめで「はい」）→ 手配済みを引かずに必要部材表（12・備考「追加」）
//      →「🛒 発注」で発注部材表（備考「追加」のまま）
//   ③ 部材発注書で「📠 FAXで送った」→「↩ 未送付に戻す」が出る → 押すと行が未発注に戻る
//
// 当て先は全部自分で作って最後に消します（案件ページ・加工製品・製造ページ・部材発注書）。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-mfg-extra.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const bodyOf = (page, id) => page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).text(), id);
const docsOf = (page, id) => page.evaluate(async (pid) => {
  const v = await (await fetch('/api/load?view=1&id=' + pid)).text();
  const at = v.indexOf('class="mfg-docs"');
  if (at < 0) return [];
  return [...v.slice(at, v.indexOf('</div>', at)).matchAll(/href="\/(\d{6})"/g)].map((m) => m[1]);
}, id);

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1400, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let root = '', product = '', order = '', mfg = '';
  const docs = new Set();
  try {
    await login(page, BASE);
    root = await makePage(page, '<h1>【E2E】追加の発注</h1>');
    product = await makePage(page, '<h1>【E2E】追加の発注の品物</h1><dl data-type="tags"><dt>ページ種類</dt><dd>加工製品</dd></dl><table><caption>材料表</caption><tbody><tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>' +
      '<tr><td>【E2E】真鍮</td><td>FB</td><td>t5*55*555</td><td>2</td></tr></tbody></table>', root);
    order = await makePage(page, '<h1>【E2E】追加の発注の案件</h1><dl data-type="tags"><dt>ページ種類</dt><dd>顧客発注書</dd><dt>発注書番号</dt><dd>E2E-EX-1</dd>' +
      '<dt>発注元</dt><dd>【E2E】追加の発注の客先</dd><dt>納期</dt><dd>2026-12-31</dd></dl>' +
      '<table><caption>案件明細表</caption><tbody><tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th><th>状態</th><th>製造完了</th></tr>' +
      '<tr><td>' + product + '</td><td>E2E-EX-A</td><td>【E2E】部品</td><td>6</td><td>個</td><td>未着手</td><td></td></tr></tbody></table>', root);
    mfg = (await page.evaluate(async (oid) => {
      const r = await fetch('/api/mfg/new', { method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ items: [{ order: oid, row: 0, item: 'E2E-EX-A', qty: '6' }] }) });
      return r.json();
    }, order)).page_id || '';
    check('当て先を作れた', !!root && !!product && !!order && !!mfg);

    // 製造ページを開き、picks のチェックを全部入れて sel を押す（dialog なら確かめで「はい」）。
    const press = async (picks, sel, dialog, wait) => {
      await page.goto(BASE + '/' + mfg);
      const btn = page.locator('#w-editor-content ' + sel);
      await btn.first().waitFor({ timeout: 8000 }).catch(() => {});
      for (const c of await page.locator('#w-editor-content ' + picks).all()) await c.check();
      if (dialog) page.once('dialog', (d) => d.accept());
      await Promise.all([
        page.waitForNavigation({ timeout: wait || 15000 }).catch(() => {}),
        btn.first().click().catch(() => {}),
      ]);
    };
    const tableIn = (body, caption) => {
      const at = body.indexOf('<caption>' + caption + '</caption>');
      return at < 0 ? '' : body.slice(at, body.indexOf('</table>', at));
    };
    // ①
    await press('.mfg-pick', '.mfg-act-parts');
    await press('.mfg-needs-pick', '.mfg-needs-order');
    await page.waitForSelector('#w-editor-content .draft-form', { timeout: 10000 }).catch(() => {});
    const signer = page.locator('#w-editor-content .draft-form select[data-unorder="signer"] option');
    if ((await signer.count()) < 2) {
      console.log('- 差出人（署名のある人）が連絡帳に無いので、部材発注書を作る所から先は飛ばします');
    } else {
      await page.selectOption('#w-editor-content .draft-form select[data-unorder="signer"]', { index: 1 });
      await page.fill('#w-editor-content .draft-form input[data-draft="supplier"]', '【E2E】追加の発注の業者');
      await Promise.all([
        page.waitForNavigation({ timeout: 20000 }).catch(() => {}),
        page.click('#w-editor-content .draft-form [data-draft-go]'),
      ]);
      const po = (await docsOf(page, mfg))[0] || '';
      if (po) docs.add(po);
      check('① 部材発注書ができた', !!po);
      await press('.mfg-pick', '.mfg-act-parts', false, 4000);
      const b1 = await bodyOf(page, mfg);
      check('① もう一度「🧾 部材」を押しても集まらない', !b1.includes('<caption>必要部材表</caption>') && !b1.includes('<caption>発注部材表</caption>'));
      // ②
      await press('.mfg-pick', '.mfg-act-extra', true);
      let tbl = tableIn(await bodyOf(page, mfg), '必要部材表');
      check('② 「➕ 追加の発注」で必要部材表（【E2E】真鍮 12・備考「追加」）', tbl.includes('【E2E】真鍮') && />\s*12\s*</.test(tbl) && tbl.includes('>追加<'), tbl.replace(/\s+/g, ' ').slice(0, 500));
      await press('.mfg-needs-pick', '.mfg-needs-order');
      tbl = tableIn(await bodyOf(page, mfg), '発注部材表');
      check('② 「🛒 発注」で発注部材表（【E2E】真鍮 12・備考「追加」のまま）', tbl.includes('【E2E】真鍮') && />\s*12\s*</.test(tbl) && tbl.includes('>追加<'), tbl.replace(/\s+/g, ' ').slice(0, 500));
      // ③
      if (po) {
        await page.goto(BASE + '/' + po);
        const fax = page.locator('#w-editor-content [data-order-sent="FAX"]');
        await fax.waitFor({ timeout: 8000 }).catch(() => {});
        page.once('dialog', (d) => d.accept());
        await Promise.all([page.waitForNavigation({ timeout: 10000 }).catch(() => {}), fax.click().catch(() => {})]);
        check('③ FAXで送った → 発注済', (await bodyOf(page, po)).includes('>発注済<'));
        await page.goto(BASE + '/' + po);
        const un = page.locator('#w-editor-content [data-order-unsend]');
        await un.waitFor({ timeout: 8000 }).catch(() => {});
        check('③ 「↩ 未送付に戻す」が出る', (await un.count()) === 1);
        page.once('dialog', (d) => d.accept());
        await Promise.all([page.waitForNavigation({ timeout: 10000 }).catch(() => {}), un.click().catch(() => {})]);
        const b3 = await bodyOf(page, po);
        check('③ 押すと未発注に戻る', !b3.includes('>発注済<') && b3.includes('>未発注<'));
      }
    }
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    if (mfg) for (const d of await docsOf(page, mfg).catch(() => [])) docs.add(d);
    for (const d of docs) await deletePage(page, d).catch(() => {});
    for (const id of [mfg, order, product, root]) if (id) await deletePage(page, id).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
