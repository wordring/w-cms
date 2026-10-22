// verify-per-unit-fraction.js — 1台あたりの個数が小数の行に警告が出るか（2026-10-22・ext/toho/per_unit_warn.go）
//
// 利用者:「一台あたりの数が少数の場合、警告してください」
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
  try {
    await login(page, BASE);
    id = await makePage(page, '<h1>【E2E】小数の個数</h1>' +
      '<table><caption>材料表</caption><tbody>' +
      '<tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>' +
      '<tr><td>SS400</td><td>板</td><td>t3</td><td>2</td></tr>' +
      '<tr><td>SUS304</td><td>板</td><td>t2</td><td>0.5</td></tr>' +
      '</tbody></table>');
    check(!!id, 'ページを作った ' + id);
    await page.goto(BASE + '/' + id);
    await page.waitForSelector('#w-editor-content table');
    const foot = await page.locator('#w-editor-content tr.per-unit-fraction').innerText().catch(() => '');
    check(foot.includes('2行目 0.5'), '足元に警告が出る: ' + foot);
    const marked = await page.locator('#w-editor-content td.cell-fraction').allInnerTexts();
    check(marked.length === 1 && marked[0].trim() === '0.5', '小数のセルだけに印: ' + JSON.stringify(marked));
    if (process.env.SHOT) await page.screenshot({ path: process.env.SHOT, fullPage: true });
    // 書ける形で別のセルを直して保存しても、警告と印は本文のファイルに入らない（/api/load は鏡を埋めて返すので、ファイルを読む）。
    const ed = page;
    await ed.evaluate(() => document.getElementById('w-mode-toggle').click());
    await ed.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 5000 });
    await ed.waitForSelector('#w-editor-content tr.per-unit-fraction');
    check(await ed.locator('#w-editor-content tr.per-unit-fraction').count() === 1, '書ける形でも警告が出る');
    await ed.locator('#w-editor-content td', { hasText: 'SS400' }).first().click();
    await ed.keyboard.press('End');
    await ed.keyboard.type('B');
    await ed.locator('#w-editor-content h1').click();
    await saveNow(ed);
    const file = path.join(__dirname, '..', 'data', 'master', id.slice(0, 2), id, id + '.html');
    const saved = fs.readFileSync(file, 'utf8');
    check(saved.includes('SS400B'), '直したセルは保存された');
    check(!saved.includes('per-unit-fraction') && !saved.includes('cell-fraction') && !saved.includes('小数の行'),
      '警告と印は本文に保存されていない');
  } finally {
    await deletePage(page, id);
    await browser.close();
  }
  console.log(ok ? '合格' : '不合格');
  process.exit(ok ? 0 : 1);
})();
