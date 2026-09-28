// 表の名前・見出しの「SQL で引くとき気をつける所」を薄赤で示すことを画面から確かめる（2026-09-28）。
//
// 利用者:「警告文を出すより、問題個所の背景を薄赤にする方が分かりやすいかもしれませんね」。
//
//   ① 編集モードで、キャプションのある表の 名前・見出し のうち引用符の要るもの（空白・記号・数字で始まる・
//      予約語）と、予約した列（page_id など）が薄赤になる。要らないもの（品名・数量）はならない
//   ② キャプションの無い表は見ない（DB に入らない）
//   ③ 直せばその場で消える（保存を待たない）
//   ④ 閲覧モードでは付かない・保存しても本文に残らない・保存の告知（文）は出ない
//
// 当て先は自分で作って最後に消します（トップ直下に1枚）。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-sql-names.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, bodyOf } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

const BODY = '<h1>【E2E】SQL の名前</h1>' +
  '<table><caption>受注 明細</caption><tbody>' +
  '<tr><th>品名</th><th>Order</th><th>単価(円)</th><th>1列目</th><th>row_id</th><th>数量</th></tr>' +
  '<tr><td>ブラケット</td><td>1</td><td>100</td><td>x</td><td>y</td><td>3</td></tr></tbody></table>' +
  '<table><tbody><tr><th>Order</th><th>No.</th></tr><tr><td>1</td><td>2</td></tr></tbody></table>';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  let id = '';
  const marked = () => page.evaluate(() => Array.from(document.querySelectorAll('#w-editor-content .sql-name-warn'))
    .map((el) => el.textContent.trim()));
  try {
    await login(page, BASE);
    id = await makePage(page, BODY);
    check('当て先を作れた', !!id, id);
    await page.goto(BASE + '/' + id + '?edit=true');
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    await page.waitForTimeout(300);

    // ①②
    const m = await marked();
    const want = ['受注 明細', 'Order', '単価(円)', '1列目', 'row_id'];
    check('引用符の要る名前と予約した列が薄赤', want.every((w) => m.includes(w)), JSON.stringify(m));
    check('要らない見出し（品名・数量）は薄赤にならない', !m.includes('品名') && !m.includes('数量'));
    check('キャプションの無い表は見ない', m.filter((t) => t === 'Order').length === 1 && !m.includes('No.'),
      JSON.stringify(m));
    const bg = await page.locator('#w-editor-content .sql-name-warn').first()
      .evaluate((el) => getComputedStyle(el).backgroundColor);
    check('背景が薄赤', bg === 'rgb(253, 234, 234)', bg);
    const title = await page.locator('#w-editor-content th', { hasText: 'row_id' }).getAttribute('title');
    check('予約した列には何になるかの説明', (title || '').includes('row_id_列'), title);

    // ③ 直せばその場で消える
    const cell = page.locator('#w-editor-content th', { hasText: '単価(円)' });
    await cell.click();
    await page.keyboard.press('End');
    for (let i = 0; i < 3; i++) await page.keyboard.press('Backspace');
    await page.waitForTimeout(300);
    const m2 = await marked();
    check('直すとその場で薄赤が消える', !m2.some((t) => t.startsWith('単価')), JSON.stringify(m2));

    // ④ 保存しても本文に残らない・告知の文は出ない
    await page.waitForTimeout(2500);
    const body = await bodyOf(page, id);
    check('保存: 印は本文に残らない', !body.includes('sql-name-warn') && !body.includes('⚠'));
    check('保存: 直した見出しが入った', body.includes('<th>単価</th>'));
    check('保存の告知（文）は出ない', await page.locator('text=引用符で囲む必要があります').count() === 0);
    await page.goto(BASE + '/' + id);
    await page.waitForSelector('#w-editor-content table', { timeout: 8000 });
    check('閲覧モードでは付かない', (await marked()).length === 0);
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    if (id) await deletePage(page, id).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
