// 必要部材表の「🚫 不要にする」と手配不要の表の「↩ 戻す」（2026-10-01・ext/toho/skip.go）を画面から確かめる。
//
// 利用者:「必要部材表はチェックして発注部材表に入れますが、必要なくなった時に消すのはどうしましょう？」→「不要にする」。
//
//   ① 自分で作った受注（加工製品の材料2行）の部材が必要部材表に出る
//   ② 1行を選び理由を書いて「🚫 不要にする」→ 必要部材表から消え、手配不要の表に理由・受注つきで入る（もう1行は残る）
//   ③ 手配不要の表の「↩ 戻す」→ 必要部材表へ戻り、空になった手配不要の表は消える
//
// ⚠ 本物の発注フォルダには触りません——必要部材表の印を置いた自分のページで押します（手配不要の表はそのページにできる）。
// 当て先は自分で作り、最後に消します。東邦の拡張が無い組では飛ばします。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-skip.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const MAT = 'E2E-SKIP-' + Date.now().toString(36).toUpperCase();

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1400, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  const made = [];
  try {
    await login(page, BASE);
    const exts = await page.evaluate(async () => (await (await fetch('/api/tag-schema')).json()).extensions || []);
    if (!exts.includes('toho')) {
      console.log('東邦の拡張が無いので飛ばします');
      await browser.close();
      process.exit(0);
    }
    const product = await makePage(page, '<h1>【E2E】加工製品（手配不要）</h1>' +
      '<table><caption>材料</caption><tbody><tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>' +
      '<tr><td>' + MAT + '-A</td><td>板</td><td>t3.2</td><td>2</td></tr>' +
      '<tr><td>' + MAT + '-B</td><td>板</td><td>t1.5</td><td>1</td></tr></tbody></table>');
    made.push(product);
    const order = await makePage(page, '<h1>【E2E】受注（手配不要）</h1>' +
      '<dl data-type="tags"><dt>発注元</dt><dd>【E2E】客先</dd><dt>納期</dt><dd>2026-12-01</dd></dl>' +
      '<table><caption>受注明細</caption><tbody><tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th></tr>' +
      '<tr><td>' + product + '</td><td></td><td>E2E カバー</td><td>3</td></tr></tbody></table>');
    made.push(order);
    const host = await makePage(page, '<h1>【E2E】必要部材表の置き場</h1><section data-mirror="必要部材表"></section>');
    made.push(host);

    const mine = (suffix) => '#w-editor-content .unorder-row[data-material="' + MAT + suffix + '"]';
    await page.goto(BASE + '/' + host);
    await page.waitForSelector('#w-editor-content .unorder-table', { timeout: 15000 }).catch(() => {});
    check('① 自分の受注の部材2行が必要部材表に出る', await page.locator(mine('-A')).count() === 1 &&
      await page.locator(mine('-B')).count() === 1);
    check('行に受注が載る（受注ごとに数える）', (await page.locator(mine('-A')).getAttribute('data-for-order')) === order);

    // ② A を不要にする
    await page.locator(mine('-A') + ' .unorder-check').check();
    await page.locator('#w-editor-content [data-unorder="reason"]').fill('在庫あり（E2E）');
    await Promise.all([
      page.waitForNavigation({ timeout: 15000 }).catch(() => {}),
      page.locator('#w-editor-content [data-unorder-skip]').click(),
    ]);
    await page.waitForSelector('#w-editor-content .unorder-table', { timeout: 15000 }).catch(() => {});
    check('② 不要にした行は必要部材表から消え、もう1行は残る', await page.locator(mine('-A')).count() === 0 &&
      await page.locator(mine('-B')).count() === 1);
    const skip = await page.evaluate(() => {
      const t = [...document.querySelectorAll('#w-editor-content table')].find(x => (x.querySelector('caption') || {}).textContent === '手配不要');
      return t ? t.innerText : '';
    });
    check('手配不要の表に理由・受注つきで入る', skip.includes('在庫あり（E2E）') && skip.includes(order) && skip.includes(MAT + '-A'),
      skip.replace(/\s+/g, ' ').slice(0, 200));

    // ③ 戻す
    await Promise.all([
      page.waitForNavigation({ timeout: 15000 }).catch(() => {}),
      page.locator('#w-editor-content .skip-row-back').first().click(),
    ]);
    await page.waitForSelector('#w-editor-content .unorder-table', { timeout: 15000 }).catch(() => {});
    check('③ 戻すと必要部材表へ戻る', await page.locator(mine('-A')).count() === 1);
    const left = await page.evaluate(() => [...document.querySelectorAll('#w-editor-content caption')].some(c => c.textContent === '手配不要'));
    check('空になった手配不要の表は消える', !left);
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    for (const id of made.reverse()) await deletePage(page, id).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
