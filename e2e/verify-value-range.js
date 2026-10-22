// verify-value-range.js — 語の値の範囲（設定の vocabulary の min・max・integer）の外の値に警告が出るか（2026-10-22）
//
// 利用者:「個数の値が小数点の場合、警告として背景色を変えては？これを一般化して、名前と値が対応する場合に、
// ある名前の値の範囲を設定できるように出来ませんか」
// 前提: config/settings.json の `個数` が `{"type": "number", "integer": true, "min": 0}`。
// 自分で作ったページ（【E2E】）の上だけで動かし、最後に消す。
const { chromium } = require('playwright');
const fs = require('fs');
const path = require('path');
const { login, makePage, deletePage, saveNow } = require('./lib');

const BASE = process.env.WCMS_BASE || 'https://localhost:8443';

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  let ok = true;
  const check = (cond, msg) => { console.log((cond ? '✓ ' : '✗ ') + msg); if (!cond) ok = false; };
  let id = '';
  // 範囲の外の印が付いたセルとタグの値（文字と理由）
  const marks = () => page.evaluate(() => ({
    cells: Array.from(document.querySelectorAll('#w-editor-content td.cell-out-of-range'))
      .map((c) => ({ text: c.textContent.trim(), title: c.title, bg: getComputedStyle(c).backgroundColor,
        before: getComputedStyle(c, '::before').content })),
    tags: Array.from(document.querySelectorAll('#w-editor-content dd.tag-out-of-range'))
      .map((d) => ({ text: d.textContent.trim(), title: d.title })),
  }));
  try {
    await login(page, BASE);
    id = await makePage(page, '<h1>【E2E】値の範囲</h1>' +
      '<dl data-type="tags"><dt>個数</dt><dd>1.5</dd></dl>' +
      '<table><caption>材料表</caption><tbody>' +
      '<tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>' +
      '<tr><td>SS400</td><td>板</td><td>t3</td><td>2</td></tr>' +
      '<tr><td>SUS304</td><td>板</td><td>t2</td><td>0.5</td></tr>' +
      '<tr><td>A5052</td><td>板</td><td>t1</td><td>-1</td></tr>' +
      '</tbody></table>');
    check(!!id, 'ページを作った ' + id);
    await page.goto(BASE + '/' + id);
    await page.waitForSelector('#w-editor-content table');
    let m = await marks();
    const texts = m.cells.map((c) => c.text).sort();
    check(JSON.stringify(texts) === JSON.stringify(['-1', '0.5']), '閲覧: 範囲の外のセルだけに印（0.5 と -1）: ' + JSON.stringify(texts));
    const half = m.cells.find((c) => c.text === '0.5') || {};
    check(/小数/.test(half.title || ''), '理由がマウスを載せると出る: ' + half.title);
    check(half.bg === 'rgb(255, 237, 213)' && /⚠/.test(half.before || ''), '背景色と ⚠: ' + half.bg + ' ' + half.before);
    const neg = m.cells.find((c) => c.text === '-1') || {};
    check(/0 より小さい/.test(neg.title || ''), 'min の外: ' + neg.title);
    check(m.tags.length === 1 && m.tags[0].text === '1.5', 'タグの値にも印: ' + JSON.stringify(m.tags));
    if (process.env.SHOT) await page.screenshot({ path: process.env.SHOT, fullPage: true });

    // 書ける形でも出る。0.5 を 3 に直すと印が消え、直して保存しても印と理由は本文のファイルに入らない。
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 5000 });
    await page.waitForTimeout(500);
    m = await marks();
    check(m.cells.length === 2, '書ける形でも印が出る: ' + m.cells.length);
    const cell = page.locator('#w-editor-content td', { hasText: '0.5' }).first();
    await cell.click();
    await page.keyboard.press('End');
    await page.keyboard.press('Backspace');
    await page.keyboard.press('Backspace');
    await page.keyboard.press('Backspace');
    await page.keyboard.type('3');
    await page.locator('#w-editor-content h1').click();
    await page.waitForTimeout(300);
    m = await marks();
    check(m.cells.map((c) => c.text).join() === '-1', '直したセルの印が消える: ' + JSON.stringify(m.cells.map((c) => c.text)));
    await saveNow(page);
    const file = path.join(__dirname, '..', 'data', 'master', id.slice(0, 2), id, id + '.html');
    const saved = fs.readFileSync(file, 'utf8');
    check(/<td>3<\/td>/.test(saved), '直したセルは保存された');
    check(!/out-of-range|title=|⚠/.test(saved), '印と理由は本文に保存されていない');
  } finally {
    await deletePage(page, id);
    await browser.close();
  }
  console.log(ok ? '合格' : '不合格');
  process.exit(ok ? 0 : 1);
})();
