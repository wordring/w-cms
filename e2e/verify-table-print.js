// どの表にも「🖨 この表を印刷」が付き、押すとその表だけが紙に出るかを**ブラウザから**確かめる（2026-10-02）。
//
// 利用者:「こういった表を印刷できるようになりませんか？」（Excel から貼った表）→「どの表にも『🖨 この表を印刷』」。
//
// ⚠ **本物の印刷はしません**——`window.print` を差し替え、呼ばれた瞬間の `#w-print-area` の中身を控えて見ます。
// 表は自分で作ったページに置き、最後に消します。
//
// 使い方: WCMS_BASE=https://localhost:8443 node verify-table-print.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

// キャプションのある表と、Excel から貼ったような形（div で包まれ・1行目も普通のセル・キャプション無し）の2つ。
const BODY = '<h1>【E2E】表の印刷</h1>' +
  '<p>前の段落</p>' +
  '<table><caption>E2E 部品表</caption><tbody>' +
  '<tr><th>品名</th><th>数量</th></tr>' +
  '<tr><td>E2E-PRINT-A</td><td>3</td></tr>' +
  '</tbody></table>' +
  '<div><table><colgroup><col/><col/></colgroup><tbody>' +
  '<tr><td>部品番号</td><td>使用数量</td></tr>' +
  '<tr><td>E2E-PRINT-B</td><td>1</td></tr>' +
  '</tbody></table></div>';

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(e.message));
  await login(page, BASE);

  let bad = 0;
  const check = (ok, msg, detail) => {
    if (ok) console.log('✓ ' + msg);
    else { console.log('✗ ' + msg + (detail ? '（' + detail + '）' : '')); bad++; }
  };
  const id = await makePage(page, BODY);
  const run = async () => {
    if (!id) { check(false, '当て先のページを作れません'); return; }
    await page.goto(BASE + '/' + id);
    await page.waitForTimeout(800);

    const n = await page.locator('#w-editor-content .w-table-print').count();
    check(n === 2, '表2つにそれぞれ「🖨 この表を印刷」が付く', n + ' 個');

    // 押した瞬間の紙の中身を控える（本物の印刷はしない）。
    await page.evaluate(() => {
      window.__printed = null;
      window.print = () => {
        const area = document.getElementById('w-print-area');
        window.__printed = {
          html: area ? area.innerHTML : '',
          text: area ? area.textContent : '',
          printing: document.body.classList.contains('w-printing-sheet'),
        };
      };
    });
    await page.locator('#w-editor-content .w-table-print').nth(1).click();
    const p = await page.evaluate(() => window.__printed);
    check(!!p, '押すと印刷が呼ばれる');
    if (p) {
      check(p.printing, '刷る間は他のものを隠す印が付く');
      check(p.text.includes('E2E-PRINT-B'), '押した表が紙に出る');
      check(!p.text.includes('E2E-PRINT-A') && !p.text.includes('前の段落'), 'ほかの表・本文は紙に出ない', p.text.slice(0, 120));
      check(p.text.includes('【E2E】表の印刷'), '紙の頭にページの題が出る');
      check(!/<button|この表を印刷/.test(p.html), '紙に印刷ボタンが出ない');
    }

    // 編集モードでは出さない（保存されない）。
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    await page.waitForTimeout(300);
    const inEdit = await page.locator('#w-editor-content .w-table-print').count();
    check(inEdit === 0, '編集モードでは出ない', inEdit + ' 個');
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForFunction(() => !document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    await page.waitForTimeout(500);
    const back = await page.locator('#w-editor-content .w-table-print').count();
    check(back === 2, '閲覧モードに戻ると、また付く', back + ' 個');
    const saved = await page.evaluate(async (pid) => (await (await fetch('/api/load?id=' + pid)).text()), id);
    check(!saved.includes('w-table-print'), '本文に保存されない');
  };
  try {
    await run();
  } finally {
    if (id) await deletePage(page, id);
  }

  check(errs.length === 0, 'JSエラーなし', errs.join(' / '));
  console.log(bad === 0 ? '\n結果: 合格' : '\n結果: ' + bad + '件の不合格');
  await browser.close();
  process.exitCode = bad === 0 ? 0 : 1;
})();
