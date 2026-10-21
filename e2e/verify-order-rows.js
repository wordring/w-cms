// 部材発注書の部材発注明細表の行の操作（2026-10-21 夕・ext/toho/our_order_mirror.go）を画面から確かめる。
//
// 利用者:「部材発注書の部材発注明細表も、行の先頭のチェックと表の下のボタンにしてください。」
//
//   ① 表の中にボタンが無い・行の先頭にチェック・表の下に「選んだ行を: …」と送る欄
//   ② 1行目を選んで「✓ 発注済」→ 発注済、「📦 納品済」→ 納品済
//   ③ 2行目（未発注）だけを選んで「📦 納品済」→ 効かない（そう言う・何も変わらない）
//   ④ 両方を選んで「✕ 取消」→ 納品済の行があるので確かめる → 両方とも取消
//   ⑤ 両方を選んで「↩ 取消をやめる」→ 両方とも未発注
//   ⑥ 1行目を選んで「↩ 発注から外す」（確かめる）→ 行が1つになる
//
// 当て先は全部自分で作って最後に消します（案件ページ・加工製品・製造ページ・部材発注書）。部材発注書を作るには、署名のある人が要ります。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-order-rows.js
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
// 部材発注明細表の行の「状態」を、上から順に返す。
const statuses = (body) => {
  const at = body.indexOf('<caption>部材発注明細表</caption>');
  if (at < 0) return [];
  const tbl = body.slice(at, body.indexOf('</table>', at));
  const rows = tbl.match(/<tr\b[\s\S]*?<\/tr>/g) || [];
  const head = (rows[0] || '').match(/<th\b[^>]*>([\s\S]*?)<\/th>/g) || [];
  const si = head.findIndex((h) => h.replace(/<[^>]*>/g, '').trim() === '状態');
  return rows.slice(1).map((r) => ((r.match(/<td\b[^>]*>([\s\S]*?)<\/td>/g) || [])[si] || '').replace(/<[^>]*>/g, '').trim());
};

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1400, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let root = '', product = '', order = '', mfg = '', po = '';
  const docs = new Set();
  try {
    await login(page, BASE);
    root = await makePage(page, '<h1>【E2E】部材発注書の行</h1>');
    product = await makePage(page, '<h1>【E2E】部材発注書の行の品物</h1><dl data-type="tags"><dt>ページ種類</dt><dd>加工製品</dd></dl><table><caption>材料表</caption><tbody><tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>' +
      '<tr><td>【E2E】鋼</td><td>FB</td><td>t4*44*444</td><td>1</td></tr><tr><td>【E2E】鋼</td><td>丸棒</td><td>φ8*88</td><td>2</td></tr></tbody></table>', root);
    order = await makePage(page, '<h1>【E2E】部材発注書の行の案件</h1><dl data-type="tags"><dt>ページ種類</dt><dd>顧客発注書</dd><dt>発注書番号</dt><dd>E2E-OR-1</dd>' +
      '<dt>発注元</dt><dd>【E2E】部材発注書の行の客先</dd><dt>納期</dt><dd>2026-12-31</dd></dl>' +
      '<table><caption>案件明細表</caption><tbody><tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th><th>状態</th><th>製造完了</th></tr>' +
      '<tr><td>' + product + '</td><td>E2E-OR-A</td><td>【E2E】部品</td><td>3</td><td>個</td><td>未着手</td><td></td></tr></tbody></table>', root);
    mfg = (await page.evaluate(async (oid) => {
      const r = await fetch('/api/mfg/new', { method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ items: [{ order: oid, row: 0, item: 'E2E-OR-A', qty: '3' }] }) });
      return r.json();
    }, order)).page_id || '';
    check('当て先を作れた', !!root && !!product && !!order && !!mfg);

    // 製造ページで 部材 → 発注 → 部材発注書。
    const pressOn = async (id, picks, sel, dialog) => {
      await page.goto(BASE + '/' + id);
      const btn = page.locator('#w-editor-content ' + sel);
      await btn.first().waitFor({ timeout: 8000 }).catch(() => {});
      for (const c of await page.locator('#w-editor-content ' + picks).all()) await c.check();
      if (dialog) page.once('dialog', (d) => d.accept());
      await Promise.all([page.waitForNavigation({ timeout: 10000 }).catch(() => {}), btn.first().click().catch(() => {})]);
      await page.waitForTimeout(500);
    };
    await pressOn(mfg, '.mfg-pick', '.mfg-act-parts');
    await pressOn(mfg, '.mfg-needs-pick', '.mfg-needs-order');
    await page.goto(BASE + '/' + mfg);
    await page.waitForSelector('#w-editor-content .draft-form', { timeout: 10000 }).catch(() => {});
    const signer = page.locator('#w-editor-content .draft-form select[data-unorder="signer"] option');
    if ((await signer.count()) < 2) {
      console.log('- 差出人（署名のある人）が連絡帳に無いので、部材発注書から先は飛ばします');
    } else {
      await page.selectOption('#w-editor-content .draft-form select[data-unorder="signer"]', { index: 1 });
      await page.fill('#w-editor-content .draft-form input[data-draft="supplier"]', '【E2E】部材発注書の行の業者');
      await Promise.all([page.waitForNavigation({ timeout: 20000 }).catch(() => {}), page.click('#w-editor-content .draft-form [data-draft-go]')]);
      po = (await docsOf(page, mfg))[0] || '';
      if (po) docs.add(po);
      check('部材発注書ができた（2行）', !!po && statuses(await bodyOf(page, po)).length === 2, JSON.stringify(statuses(po ? await bodyOf(page, po) : '')));
    }
    if (po) {
      // ①
      await page.goto(BASE + '/' + po);
      await page.waitForSelector('#w-editor-content .order-pick', { timeout: 8000 }).catch(() => {});
      const layout = await page.evaluate(() => {
        const c = document.querySelector('#w-editor-content');
        return { inTable: c.querySelectorAll('table button').length, picks: c.querySelectorAll('.order-pick').length,
          bar: c.querySelectorAll('.order-bar .order-sel-act').length, fax: !!c.querySelector('.order-bar [data-order-sent="FAX"]') };
      });
      check('① 表の中にボタンが無い・行の先頭にチェック 2・表の下に操作 5 と送る欄', layout.inTable === 0 && layout.picks === 2 && layout.bar === 5 && layout.fax, JSON.stringify(layout));

      const act = async (rows, actName, dialog) => {
        await page.goto(BASE + '/' + po);
        await page.waitForSelector('#w-editor-content .order-pick', { timeout: 8000 }).catch(() => {});
        for (const r of rows) await page.locator('#w-editor-content .order-pick[data-row="' + r + '"]').check();
        let asked = '';
        if (dialog) page.once('dialog', (d) => { asked = d.message(); d.accept(); });
        await Promise.all([page.waitForNavigation({ timeout: 8000 }).catch(() => {}),
          page.click('#w-editor-content .order-sel-act[data-act="' + actName + '"]')]);
        await page.waitForTimeout(500);
        return asked;
      };
      // ②
      await act([1], 'sent');
      check('② 1行目 ✓ 発注済', statuses(await bodyOf(page, po))[0] === '発注済', JSON.stringify(statuses(await bodyOf(page, po))));
      await act([1], 'delivered');
      check('② 1行目 📦 納品済', statuses(await bodyOf(page, po))[0] === '納品済', JSON.stringify(statuses(await bodyOf(page, po))));
      // ③
      await page.goto(BASE + '/' + po);
      await page.waitForSelector('#w-editor-content .order-pick', { timeout: 8000 }).catch(() => {});
      await page.locator('#w-editor-content .order-pick[data-row="2"]').check();
      await page.click('#w-editor-content .order-sel-act[data-act="delivered"]');
      await page.waitForTimeout(600);
      const say = await page.locator('#w-editor-content .order-row-say').textContent().catch(() => '');
      check('③ 未発注の行に 📦 納品済 は効かない（そう言う）', (say || '').includes('できません') && statuses(await bodyOf(page, po))[1] !== '納品済', say);
      // ④
      const asked = await act([1, 2], 'cancel', true);
      check('④ 納品済の行があるので確かめる', asked.includes('納品済'), asked.slice(0, 80));
      check('④ 両方とも取消', JSON.stringify(statuses(await bodyOf(page, po))) === JSON.stringify(['取消', '取消']), JSON.stringify(statuses(await bodyOf(page, po))));
      // ⑤
      await act([1, 2], 'unsent');
      check('⑤ 両方とも未発注', JSON.stringify(statuses(await bodyOf(page, po))) === JSON.stringify(['未発注', '未発注']), JSON.stringify(statuses(await bodyOf(page, po))));
      // ⑥
      const asked6 = await act([1], 'return', true);
      check('⑥ 外す前に確かめる', asked6.includes('外します'), asked6.slice(0, 60));
      check('⑥ 行が1つになる', statuses(await bodyOf(page, po)).length === 1, JSON.stringify(statuses(await bodyOf(page, po))));
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
