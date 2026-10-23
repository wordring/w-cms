// 届いた部材を在庫表へ入れる（2026-10-23・ext/toho/stock_in.go）を画面から確かめる。
//
// 利用者:「納品された時に在庫が増えて、その時に作業が開始されている分が引かれる形ではどうでしょう？」
//
//   ① 部材発注書の2行（案件の必要 3 に 5 を頼んだ材料・案件の無い購入部品 4）を選んで「📦 納品済」
//      → 知らせが出て、置き場「在庫」の材料在庫表に 2、購入部品在庫表に 4 が入る
//   ② 2行を選んで「✕ 取消」→ 入れた数を引き、在庫のページが押す前と同じに戻る
//   ③ 製造ページの製造明細表: 数量の右に在庫数（鏡）。「✓ 製造完了」で空の製造数に数量が入り、在庫表は変わらない
//
// 当て先は自分で作って最後に消します（加工製品・案件ページ・部材発注書）。置き場「在庫」は本物なので、
// 【E2E】の名前の部材だけを足し引きし、最後に押す前の本文と比べます（戻っていなければ落とす）。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-stock-in.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const bodyOf = (page, id) => page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).text(), id);
// 空のセルは <td></td> と <td><br/></td> のどちらでもよい（エディタは <br/> を書く）。
const flat = (s) => s.replace(/\s+/g, '').replace(/<td><br\/?><\/td>/g, '<td></td>');

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1400, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let root = '', product = '', order = '', po = '', mfg = '';
  try {
    await login(page, BASE);
    const stock = await page.evaluate(async () => {
      const r = await (await fetch('/api/admin/pages')).json();
      return ((r.pages || []).find((p) => p.title === '在庫') || {}).page_id || '';
    });
    if (!stock) { console.log('- 置き場「在庫」が無いので飛ばします'); return; }
    const before = await bodyOf(page, stock);

    root = await makePage(page, '<h1>【E2E】在庫へ入れる</h1>');
    product = await makePage(page, '<h1>【E2E】在庫へ入れる品物</h1><dl data-type="tags"><dt>ページ種類</dt><dd>加工製品</dd></dl>' +
      '<table><caption>材料表</caption><tbody><tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>' +
      '<tr><td>【E2E】鋼</td><td>FB</td><td>t4*55*555</td><td>1</td></tr></tbody></table>', root);
    order = await makePage(page, '<h1>【E2E】在庫へ入れる案件</h1><dl data-type="tags"><dt>ページ種類</dt><dd>顧客発注書</dd><dt>発注書番号</dt><dd>E2E-ST-1</dd>' +
      '<dt>納期</dt><dd>2026-12-31</dd></dl>' +
      '<table><caption>案件明細表</caption><tbody><tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th><th>状態</th></tr>' +
      '<tr><td>' + product + '</td><td>E2E-ST-A</td><td>【E2E】部品</td><td>3</td><td>個</td><td>未着手</td></tr></tbody></table>', root);
    po = await makePage(page, '<h1>【E2E】在庫へ入れる部材発注書</h1><dl data-type="tags"><dt>ページ種類</dt><dd>弊社部材発注書</dd><dt>仕入先</dt><dd>【E2E】業者</dd></dl>' +
      '<table><caption>部材発注明細表</caption><tbody><tr><th>弊社品番</th><th>案件</th><th>種類</th><th>品名</th><th>仕様</th><th>材質</th><th>形状</th><th>寸法</th><th>数量</th><th>単位</th><th>状態</th></tr>' +
      '<tr><td>' + product + '</td><td>' + order + '</td><td>材料</td><td></td><td></td><td>【E2E】鋼</td><td>FB</td><td>t4*55*555</td><td>5</td><td>本</td><td>発注済</td></tr>' +
      '<tr><td>' + product + '</td><td></td><td>購入部品</td><td>【E2E】ねじ</td><td>M5</td><td></td><td></td><td></td><td>4</td><td>個</td><td>発注済</td></tr>' +
      '</tbody></table>', root);
    check('当て先を作れた', !!root && !!product && !!order && !!po);

    const act = async (actName, needAsk) => {
      await page.goto(BASE + '/' + po);
      await page.waitForSelector('#w-editor-content .order-pick', { timeout: 8000 }).catch(() => {});
      for (const c of await page.locator('#w-editor-content .order-pick').all()) await c.check();
      const said = [];
      const onDialog = (d) => { said.push(d.message()); d.accept(); };
      page.on('dialog', onDialog);
      await Promise.all([page.waitForNavigation({ timeout: 10000 }).catch(() => {}),
        page.click('#w-editor-content .order-sel-act[data-act="' + actName + '"]')]);
      await page.waitForTimeout(800);
      page.off('dialog', onDialog);
      return said;
    };
    // ①
    const said = await act('delivered');
    check('① 在庫表を直したことを知らせる', said.some((m) => m.includes('在庫表を直しました（2 件）')), JSON.stringify(said));
    const after = flat(await bodyOf(page, stock));
    check('① 材料在庫表に 5 − 3 = 2', after.includes(flat('<td>【E2E】鋼</td><td>FB</td><td>t4*55*555</td><td>2</td><td>本</td>')), after.slice(0, 300));
    check('① 購入部品在庫表に 案件の無い行の 4', after.includes(flat('<td>【E2E】ねじ</td><td>M5</td><td>4</td><td>個</td>')));
    // ②
    const said2 = await act('cancel');
    check('② 取消で在庫表から引いたことを知らせる', said2.some((m) => m.includes('在庫表を直しました（2 件）')), JSON.stringify(said2));
    check('② 在庫のページが押す前と同じに戻った', flat(await bodyOf(page, stock)) === flat(before));

    // ③ 製造ページ: 製造明細表の数量の右に「在庫数」（鏡）。製造数が空のまま「✓ 製造完了」→ 製造数に数量が入り、在庫表は変わらない。
    mfg = (await page.evaluate(async (oid) => {
      const r = await fetch('/api/mfg/new', { method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ items: [{ order: oid, row: 0, item: 'E2E-ST-A', qty: '3' }] }) });
      return r.json();
    }, order)).page_id || '';
    check('③ 製造ページを作れた', !!mfg);
    if (mfg) {
      await page.goto(BASE + '/' + mfg);
      await page.waitForSelector('#w-editor-content .mfg-pick', { timeout: 8000 }).catch(() => {});
      const heads = await page.evaluate(() => {
        const t = [...document.querySelectorAll('#w-editor-content table')].find((x) => (x.caption && x.caption.textContent.trim()) === '製造明細表');
        return t ? [...t.rows[0].cells].map((c) => c.textContent.trim()) : [];
      });
      const qi = heads.indexOf('数量');
      check('③ 数量の右に在庫数、その右に製造数', qi >= 0 && heads[qi + 1] === '在庫数' && heads[qi + 2] === '製造数', JSON.stringify(heads));
      await page.locator('#w-editor-content .mfg-pick').first().check();
      const said3 = [];
      const onDialog = (d) => { said3.push(d.message()); d.accept(); };
      page.on('dialog', onDialog);
      await Promise.all([page.waitForNavigation({ timeout: 10000 }).catch(() => {}), page.click('#w-editor-content .mfg-act-made')]);
      await page.waitForTimeout(800);
      page.off('dialog', onDialog);
      // /api/load は鏡も描くので、鏡のセル（.vocab-chrome）を落として本文の列だけで見る。
      const mb = flat(await bodyOf(page, mfg)).replace(/<t[hd]class="vocab-chrome[^"]*"[^>]*>.*?<\/t[hd]>/g, '');
      check('③ 製造数に数量 3 が入り、製造完了は済', mb.includes(flat('<td>3</td><td>3</td>')) && mb.includes('<td>済</td>'), mb.slice(mb.indexOf('製造明細表'), mb.indexOf('製造明細表') + 600));
      check('③ 数量どおり作ったので、在庫表は直さない（知らせも出ない）', said3.length === 0 && flat(await bodyOf(page, stock)) === flat(before), JSON.stringify(said3));
    }
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    for (const id of [mfg, po, order, product, root]) if (id) await deletePage(page, id).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
