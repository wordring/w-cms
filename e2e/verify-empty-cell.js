// 空の表のセルが1行ぶんの高さを持つことを画面から確かめる（2026-09-28）。
//
// 利用者:「空の表はセルの高さがずいぶん狭くて編集が困難です。最低高さをキャレットの高さか、
// 文字の高さ以上に出来ないでしょうか」。
//
//   ① 空のセル（<td></td>・<td><br></td>）の高さが、字の入ったセルと同じ（1行ぶん）
//   ② 空のセルを押してそのまま打てる（キャレットが入る）
//   ③ 閲覧モードでも潰れない
//
// 当て先は自分で作って最後に消します（トップ直下に1枚）。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-empty-cell.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

const BODY = '<h1>【E2E】空のセル</h1>' +
  '<table><caption>材料</caption><tbody>' +
  '<tr><th>材質</th><th>形状</th><th>寸法</th></tr>' +
  '<tr><td>鉄</td><td>板</td><td>t3.2</td></tr>' +
  '<tr><td></td><td></td><td></td></tr>' +
  '<tr><td><br></td><td><br></td><td><br></td></tr>' +
  '</tbody></table><table><tbody><tr><th></th><th></th></tr><tr><td></td><td></td></tr></tbody></table>';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  let id = '';
  const heights = () => page.evaluate(() => {
    const t = document.querySelectorAll('#w-editor-content table');
    const rowH = (tbl, i) => tbl.querySelectorAll('tr')[i].getBoundingClientRect().height;
    return { text: rowH(t[0], 1), empty: rowH(t[0], 2), br: rowH(t[0], 3),
      emptyTh: rowH(t[1], 0), emptyTd: rowH(t[1], 1) };
  });
  try {
    await login(page, BASE);
    id = await makePage(page, BODY);
    check('当て先を作れた', !!id, id);
    await page.goto(BASE + '/' + id + '?edit=true');
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    await page.waitForTimeout(300);

    const h = await heights();
    console.log('  高さ（px）:', JSON.stringify(h));
    // ⚠ 字の入った行は和文の代わりの字形（フォールバック）で行の箱が 2〜3px 高くなるので、4px までは同じとみなす
    //    （見たいのは「1行ぶんあるか」——直す前は全部が空の表で 9px に潰れていた）。
    const same = (a, b) => Math.abs(a - b) <= 4;
    check('空のセル（<td></td>）が字の入ったセルと同じ高さ', same(h.empty, h.text));
    check('空のセル（<td><br></td>）が字の入ったセルと同じ高さ', same(h.br, h.text));
    check('見出しも中身も空の表が潰れない', same(h.emptyTh, h.text) && same(h.emptyTd, h.text));

    // ② 空のセルを押してそのまま打てる
    const cell = page.locator('#w-editor-content table').first().locator('tr').nth(2).locator('td').nth(1);
    await cell.click();
    await page.keyboard.type('丸棒');
    await page.waitForTimeout(200);
    check('空のセルを押して打てる', (await cell.textContent()).includes('丸棒'));

    // ③ 閲覧モード
    await page.waitForTimeout(2500); // 自動保存
    await page.goto(BASE + '/' + id);
    await page.waitForSelector('#w-editor-content table', { timeout: 8000 });
    const v = await heights();
    check('閲覧モードでも空のセルが潰れない', same(v.br, v.text), JSON.stringify(v));
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
