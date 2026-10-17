// 受注残表の「製造完了」（2026-10-17・ext/toho/backlog.go・order_edit.go）を画面から確かめる。
//
// 2026-10-16 利用者:「では、作業の進捗は工数を測定したいときだけにして、もっと大雑把に製造完了だけにします。」
//
//   ① 受注残表に「製造完了」の欄がある（材料発注の右・紙に出さない）
//   ② 押すと、受注ページの受注明細表の「製造完了」に「済」が入る
//   ③ もう一度押して外すと、空に戻る
//
// 当て先は全部自分で作って最後に消します（受注残表を置いたページ・受注ページ）。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-backlog-made.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const madeCell = (page, id) => page.evaluate(async (pid) => {
  const raw = await (await fetch('/api/load?id=' + pid)).text();
  const doc = new DOMParser().parseFromString(raw, 'text/html');
  const t = [...doc.querySelectorAll('table')].find((x) => (x.querySelector('caption') || {}).textContent === '受注明細表');
  if (!t) return null;
  const rows = [...t.querySelectorAll('tr')];
  const i = [...rows[0].children].findIndex((c) => c.textContent.trim() === '製造完了');
  return i < 0 ? null : rows[1].children[i].textContent.trim();
}, id);

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1400, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let root = '', order = '';
  try {
    await login(page, BASE);
    root = await makePage(page, '<h1>【E2E】受注残の製造完了</h1><section data-mirror="受注残"></section>');
    order = await makePage(page, '<h1>受注 E2E-MADE</h1><dl data-type="tags"><dt>ページ種類</dt><dd>顧客発注書</dd><dt>発注書番号</dt><dd>E2E-MADE-1</dd>' +
      '<dt>発注元</dt><dd>【E2E】製造完了の客先</dd><dt>発注日</dt><dd>2026-10-17</dd><dt>納期</dt><dd>2026-12-31</dd></dl>' +
      '<table><caption>受注明細表</caption><tbody><tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>出荷済数量</th><th>状態</th><th>材料発注</th><th>製造完了</th><th>納品書発行</th></tr>' +
      '<tr><td></td><td>E2E-MADE-A</td><td>【E2E】部品</td><td>5</td><td></td><td>加工中</td><td></td><td></td><td></td></tr></tbody></table>', root);
    check('当て先を作れた', !!root && !!order);

    await page.goto(BASE + '/' + root);
    await page.waitForSelector('#w-editor-content .backlog-table', { timeout: 10000 }).catch(() => {});
    const row = page.locator('#w-editor-content tr[data-order-page="' + order + '"]');
    const head = await page.evaluate(() => {
      const ths = [...document.querySelector('#w-editor-content .backlog-table tr').children].map((x) => x.textContent.trim());
      const th = [...document.querySelectorAll('#w-editor-content .backlog-table th')].find((x) => x.textContent.trim() === '製造完了');
      return { at: ths.indexOf('製造完了') - ths.indexOf('材料発注'), cls: th ? th.className : null };
    });
    check('① 「製造完了」の欄が材料発注の右・紙に出さない', head.at === 1 && (head.cls || '').includes('no-print'), JSON.stringify(head));
    const box = row.locator('input[data-field="製造完了"]');
    check('① 行に「製造完了」のチェックがある', (await box.count()) === 1);

    await box.check();
    await page.waitForTimeout(1500);
    check('② 押すと受注ページの「製造完了」に「済」', (await madeCell(page, order)) === '済', String(await madeCell(page, order)));
    await box.uncheck();
    await page.waitForTimeout(1500);
    check('③ 外すと空に戻る', (await madeCell(page, order)) === '', String(await madeCell(page, order)));
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    if (order) await deletePage(page, order).catch(() => {});
    if (root) await deletePage(page, root).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
