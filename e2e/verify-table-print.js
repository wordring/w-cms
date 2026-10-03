// どの表にも「🖨 この表を印刷」が付き、押すとその表だけが紙に出るかを**ブラウザから**確かめる（2026-10-02）。
//
// 利用者:「こういった表を印刷できるようになりませんか？」（Excel から貼った表）→「どの表にも『🖨 この表を印刷』」。
//
// 押すと見えない枠（`#w-print-frame`）に写しが入り、その枠が刷られる——枠の中身と print() を呼んだ印（`data-printed`）を見ます
// （ヘッドレスの print() は何もしない）。
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

    // 押すと、見えない枠（#w-print-frame）にその表の写しだけが入り、その枠が刷られる（2026-10-03——ページそのものは刷らない）。
    const frameOf = async () => {
      await page.waitForFunction(() => {
        const f = document.getElementById('w-print-frame');
        return f && f.getAttribute('data-printed') === '1';
      }, null, { timeout: 4000 }).catch(() => {});
      return page.evaluate(() => {
        const f = document.getElementById('w-print-frame');
        if (!f || f.getAttribute('data-printed') !== '1') return null;
        const d = f.contentDocument;
        const area = d.getElementById('w-print-area');
        return {
          html: area ? area.innerHTML : '',
          text: area ? area.textContent : '',
          printing: d.body.classList.contains('w-printing-sheet'),
          css: !!d.querySelector('link[rel="stylesheet"][href$="/assets/app.css"]'),
          pageMarked: document.body.classList.contains('w-printing-sheet'),
        };
      });
    };
    await page.locator('#w-editor-content .w-table-print').nth(1).click();
    const p = await frameOf();
    check(!!p, '押すと見えない枠が刷られる');
    if (p) {
      check(p.printing && p.css, '枠の中は紙の決まり（app.css・w-printing-sheet）で刷る');
      check(!p.pageMarked, 'ページそのものには印を付けない（ページは刷らない）');
      check(p.text.includes('E2E-PRINT-B'), '押した表が紙に出る');
      check(!p.text.includes('E2E-PRINT-A') && !p.text.includes('前の段落'), 'ほかの表・本文は紙に出ない', p.text.slice(0, 120));
      check(p.text.includes('【E2E】表の印刷'), '紙の頭にページの題が出る');
      check(!/<button|この表を印刷/.test(p.html), '紙に印刷ボタンが出ない');
    }

    // ページに埋め込み（図面の PDF など）があっても、触らない（2026-10-03——10-02 夜は刷るあいだだけ外していたが、職場で
    // 「二回押さないと開きません」。いまは別の枠を刷るので、ページの埋め込みは外さない）。
    await page.evaluate(() => {
      const e = document.createElement('embed');
      e.id = 'e2e-embed';
      e.type = 'application/pdf';
      document.querySelector('#w-editor-content p').appendChild(e);
      const f = document.getElementById('w-print-frame');
      if (f) f.remove();
    });
    await page.locator('#w-editor-content .w-table-print').first().click();
    const q = await frameOf();
    check(!!q, '埋め込みのあるページでも、押すと枠が刷られる');
    if (q) check(q.text.includes('E2E-PRINT-A'), '押した表が紙に出る（埋め込みのあるページ）');
    const kept = await page.evaluate(() => {
      const e = document.getElementById('e2e-embed');
      return !!(e && e.closest('#w-editor-content p'));
    });
    check(kept, 'ページの埋め込みは外さない（元の場所のまま）');
    await page.evaluate(() => { const e = document.getElementById('e2e-embed'); if (e) e.remove(); });

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
