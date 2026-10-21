// 案件残表 →「🏭 製造」→ 製造待ち →「▶ 開始」→ 製造ページ と、取り消し（2026-10-21・ext/toho/mfg_wait.go）を画面から確かめる。
//
// 利用者:「表の先頭にチェックボックスを付け、表の下に「製造」ボタンを付けます。チェックしてボタンを押すと「製造」フォルダへ入ります」
// 「製造フォルダに入った案件は表で分かるように表示します。この表の先頭にはチェックボックスがあり、表の下には「開始」ボタンがあります。」
// 「開始した案件はいつでも単体で取り消せるようにします。」「製造フォルダの表に入った案件もキャンセルして案件フォルダに戻すことが出来ます。」
//
//   ① 案件残表の先頭の列はチェック（紙に出さない）・「進み」の列・表の下に「🏭 製造」。古い「🏭 選んだ行で製造開始」は無い
//   ② 何も選ばずに「🏭 製造」を押すと、選ぶように言う
//   ③ 選んで押すと、その行が打ち消し線になり、「進み」に「製造待ち 20」
//   ④ 製造待ちのビューに並ぶ——選んで「▶ 選んだ行で開始」で製造ページが開く（製造明細表に 20）
//   ⑤ 製造明細表で行を選んで、表の下の「↩ 取り消し」（確かめで「はい」）で、行が外れて製造待ちへ戻る
//   ⑥ 製造待ちの「↩ 選んだ行を案件へ戻す」で、案件残表で選べる形に戻る（打ち消し線が消える）
//
// 当て先は全部自分で作って最後に消します（ビューを置いたページ・案件ページ・できた製造ページ）。置き場「製造」は消しません。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-mfg-wait.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, bodyOf } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1400, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  page.on('dialog', (d) => d.accept()); // 取り消しの確かめは「はい」
  let root = '', order = '', mfg = '';
  try {
    await login(page, BASE);
    root = await makePage(page, '<h1>【E2E】製造待ち</h1><section data-mirror="案件残"></section><section data-mirror="製造待ち"></section>');
    order = await makePage(page, '<h1>案件 E2E-WAIT</h1><dl data-type="tags"><dt>ページ種類</dt><dd>顧客発注書</dd><dt>発注書番号</dt><dd>E2E-WAIT-1</dd>' +
      '<dt>発注元</dt><dd>【E2E】製造待ちの客先</dd><dt>発注日</dt><dd>2026-10-21</dd><dt>納期</dt><dd>2026-12-31</dd></dl>' +
      '<table><caption>案件明細表</caption><tbody><tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th><th>単価</th><th>状態</th><th>製造待ち</th></tr>' +
      '<tr><td></td><td>E2E-WAIT-A</td><td>【E2E】製造待ちの部品</td><td>20</td><td>個</td><td>1500</td><td>未着手</td><td></td></tr></tbody></table>', root);
    check('当て先を作れた', !!root && !!order);

    const row = '#w-editor-content .backlog-table tr[data-order-page="' + order + '"]';
    // ⚠ has の中は表の中からの相対（#w-editor-content を付けない）。
    const sheetOf = (sel) => page.locator('#w-editor-content .backlog-sheet', { has: page.locator(sel.replace('#w-editor-content ', '')) });
    await page.goto(BASE + '/' + root);
    await page.waitForSelector(row, { timeout: 10000 }).catch(() => {});
    // ①
    const head = await page.evaluate(() => {
      const t = document.querySelector('#w-editor-content .backlog-table:not(.mfgwait-table)');
      const ths = t ? Array.from(t.querySelectorAll('tr:first-child th')) : [];
      return { first: ths[0] ? ths[0].textContent.trim() + '|' + ths[0].className : '', heads: ths.map((x) => x.textContent.trim()) };
    });
    check('① 先頭の列は製造へのチェック（紙に出さない）', /^製造|.*no-print/.test(head.first), head.first);
    check('① 「進み」の列があり、状態・材料発注などの列は無い', head.heads.includes('進み') && !head.heads.includes('状態') && !head.heads.includes('材料発注'), head.heads.join('|'));
    check('① 表の下に「🏭 製造」・古い「製造開始」は無い',
      (await sheetOf(row).locator('.backlog-wait-go').count()) === 1 && (await page.locator('.backlog-mfg-go').count()) === 0);
    // ②
    await sheetOf(row).locator('.backlog-wait-go').click();
    await page.waitForTimeout(300);
    const say = (await sheetOf(row).locator('.backlog-wait-say').textContent().catch(() => '')) || '';
    check('② 何も選ばずに押すと、選ぶように言う', /選んでください/.test(say), say);
    // ③
    await page.locator(row + ' .backlog-wait-pick').check();
    await Promise.all([page.waitForNavigation({ timeout: 10000 }).catch(() => {}), sheetOf(row).locator('.backlog-wait-go').click()]);
    await page.waitForSelector(row, { timeout: 10000 }).catch(() => {});
    const r3 = await page.evaluate((sel) => { const tr = document.querySelector(sel); return tr ? { cls: tr.className, text: tr.textContent } : null; }, row);
    check('③ 押した行が打ち消し線（backlog-in-mfg）', !!r3 && r3.cls.includes('backlog-in-mfg'), r3 && r3.cls);
    check('③ 「進み」に「製造待ち 20」', !!r3 && r3.text.includes('製造待ち 20'));
    // ④
    const wpick = '#w-editor-content .mfgwait-pick[data-order="' + order + '"]';
    check('④ 製造待ちのビューに並ぶ', (await page.locator(wpick).count()) === 1);
    await page.locator(wpick).check();
    await Promise.all([page.waitForURL((u) => !u.toString().endsWith('/' + root), { timeout: 10000 }).catch(() => {}), sheetOf(wpick).locator('.mfgwait-start-go').click()]);
    mfg = (page.url().match(/\/(\d{6})(?:[?#]|$)/) || [])[1] || '';
    check('④ 「▶ 開始」で製造ページが開く', !!mfg && mfg !== root, page.url());
    const mbody = mfg ? await bodyOf(page, mfg) : '';
    check('④ 製造明細表に製造待ちの数（20）', /<td>E2E-WAIT-A<\/td>[\s\S]*?<td>20<\/td>/.test(mbody));
    // ⑤
    await page.waitForSelector('#w-editor-content .mfg-items-bar .mfg-act-back', { timeout: 10000 }).catch(() => {});
    await page.locator('#w-editor-content input.mfg-pick[data-item="E2E-WAIT-A"]').check();
    await Promise.all([page.waitForNavigation({ timeout: 10000 }).catch(() => {}), page.locator('#w-editor-content .mfg-items-bar .mfg-act-back').first().click()]);
    const after = mfg ? await bodyOf(page, mfg) : '';
    check('⑤ 「↩ 取り消し」で製造明細から外れる', !after.includes('E2E-WAIT-A'));
    const obody = await bodyOf(page, order);
    check('⑤ 案件明細の製造待ちに 20 が戻る', /<td>未着手<\/td>\s*<td>20<\/td>/.test(obody));
    // ⑥
    await page.goto(BASE + '/' + root);
    await page.waitForSelector(wpick, { timeout: 10000 }).catch(() => {});
    await page.locator(wpick).check();
    await Promise.all([page.waitForNavigation({ timeout: 10000 }).catch(() => {}), sheetOf(wpick).locator('.mfgwait-back-go').click()]);
    await page.waitForSelector(row, { timeout: 10000 }).catch(() => {});
    const r6 = await page.evaluate((sel) => { const tr = document.querySelector(sel); return tr ? tr.className : null; }, row);
    check('⑥ 案件へ戻すと打ち消し線が消え、また選べる', r6 !== null && !r6.includes('backlog-in-mfg') && (await page.locator(row + ' .backlog-wait-pick').count()) === 1, String(r6));
    check('画面のエラーが無い', errs.length === 0, errs.join(' / '));
  } catch (e) {
    check('途中で止まらない', false, String(e));
  } finally {
    // 後片付け——作ったものは全部消す（製造ページ・案件ページ・ビューのページ）。
    for (const id of [mfg, order, root]) await deletePage(page, id);
    await browser.close();
  }
  console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
  process.exit(fails ? 1 : 0);
})();
