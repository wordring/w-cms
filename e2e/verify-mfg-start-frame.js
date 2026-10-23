// 製造ページの「作業開始」を枠で囲むこと（2026-10-23・ext/toho/assets/toho.js の frameMfgStart・toho.css）を確かめます。
// 利用者:「作業開始の項目ですが、ほかの項目とは違い作業ではなく、さぎょする案件を指定するものですので、目立つように枠で囲みます」
//
// 自分で作ったページの上だけで動き、最後に消します。
//   ① 閲覧: 見出し「作業開始」から次の見出しの手前まで（製造明細表と、その下の帯）が枠に入る。次の見出しは入らない。
//   ② 書ける形でも同じ。本文は変わらない（枠の印は保存されない）。
//   ③ 閲覧で「作業開始」を畳むと、見出しの段だけで枠が閉じる。
//   ④ 製造ページでないページには付かない。
// WCMS_BASE=https://localhost:8443 node verify-mfg-start-frame.js
const path = require('path');
const { chromium } = require(path.join(process.cwd(), 'node_modules', 'playwright'));
const { login, makePage, deletePage, bodyOf } = require('./lib');

const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
let fails = 0;
function check(label, ok, note) {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
}
const mfgBody = (kind) => '<h1>【E2E】作業開始の枠</h1><dl data-type="tags"><dt>ページ種類</dt><dd>' + kind + '</dd></dl>' +
  '<h2>作業開始</h2><table><caption>製造明細表</caption><tbody><tr><th>弊社品番</th><th>案件</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th><th>製造完了</th></tr>' +
  '<tr><td></td><td></td><td>E2E-F1</td><td>【E2E】枠の部品</td><td>3</td><td>個</td><td></td></tr></tbody></table>' +
  '<h2>部材作業</h2><p>部材作業の段落</p>';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1400, height: 1000 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let mfg = '', other = '';
  // 枠に入ったブロックの中身（見出しの文字・表のキャプション・帯の class）と、1枚目と最後の印。
  const framed = () => page.evaluate(() => {
    const blocks = [...document.querySelectorAll('#w-editor-content > .mfg-start-frame')];
    const what = (b) => {
      const h = b.querySelector('h1, h2'); if (h) return 'h:' + h.textContent.trim();
      const c = b.querySelector('caption'); if (c) return 'table:' + c.textContent.trim();
      return 'other:' + (b.className || '').replace(/\s+/g, '.');
    };
    return {
      list: blocks.map(what),
      first: blocks.length ? blocks[0].classList.contains('mfg-start-first') : false,
      last: blocks.length ? blocks[blocks.length - 1].classList.contains('mfg-start-last') : false,
      border: blocks.length ? getComputedStyle(blocks[0]).borderTopWidth : '',
    };
  });
  try {
    await login(page, BASE);
    mfg = await makePage(page, mfgBody('製造'));
    other = await makePage(page, mfgBody('加工製品'));
    check('当て先を作れた', !!(mfg && other));
    const saved = await bodyOf(page, mfg);

    // ①
    await page.goto(BASE + '/' + mfg);
    await page.waitForTimeout(1200);
    let f = await framed();
    check('① 閲覧: 見出し「作業開始」から始まり、製造明細表が入る', f.list[0] === 'h:作業開始' && f.list.includes('table:製造明細表') && f.first && f.last, JSON.stringify(f));
    check('① 次の見出し「部材作業」は入らない', !f.list.some((x) => x.includes('部材作業')), JSON.stringify(f.list));
    check('① 枠の線が描かれている', f.border === '1px', f.border);
    // ②
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    await page.waitForTimeout(600);
    f = await framed();
    check('② 書ける形でも枠に入る', f.list[0] === 'h:作業開始' && f.list.includes('table:製造明細表') && !f.list.some((x) => x.includes('部材作業')), JSON.stringify(f.list));
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForFunction(() => !document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    await page.waitForTimeout(600);
    check('② 本文は変わらない（枠の印は保存されない）', (await bodyOf(page, mfg)) === saved);
    // ③
    await page.evaluate(() => [...document.querySelectorAll('#w-editor-content h2')].find((h) => h.textContent.trim() === '作業開始').click());
    await page.waitForTimeout(400);
    const folded = await page.evaluate(() => {
      const b = document.querySelector('#w-editor-content > .mfg-start-first');
      return b ? { bottom: getComputedStyle(b).borderBottomWidth, hidden: !!document.querySelector('#w-editor-content > .mfg-start-frame.w-fold-hidden, #w-editor-content > .mfg-start-frame .w-fold-hidden') } : null;
    });
    check('③ 畳むと見出しの段だけで枠が閉じる', !!folded && folded.bottom === '1px' && folded.hidden, JSON.stringify(folded));
    // ④
    await page.goto(BASE + '/' + other);
    await page.waitForTimeout(1200);
    f = await framed();
    check('④ 製造ページでないページには付かない', f.list.length === 0, JSON.stringify(f.list));
    check('画面のエラーが無い', errs.length === 0, errs.join(' / '));
  } catch (e) {
    check('途中で止まらない', false, String(e));
  } finally {
    for (const id of [mfg, other]) await deletePage(page, id);
    await browser.close();
  }
  console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
  process.exit(fails ? 1 : 0);
})();
