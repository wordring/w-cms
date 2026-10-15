// どの表にも「⋯」が付き、そのメニューの「🖨 この表を印刷」でその表だけが紙に出るかを**ブラウザから**確かめる（2026-10-02・10-15 に「⋯」へ）。
//
// 利用者:「こういった表を印刷できるようになりませんか？」（Excel から貼った表）→「どの表にも『🖨 この表を印刷』」。
// 2026-10-15 利用者:「表に付く「この表を印刷」というボタンをよくある点3つのボタンに変えて開くメニューの中に納めてください。この点3つのボタンは
// 表のキャプションと同じ高さの右側に配置できると良いと思います。表に対するコマンドは印刷以外にも増えると思うからです」
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

    const n = await page.locator('#w-editor-content .w-table-menu-btn').count();
    check(n === 2, '表2つにそれぞれ「⋯」が付く', n + ' 個');
    check(await page.locator('#w-editor-content .w-table-print').count() === 0, '「🖨 この表を印刷」のボタンは表の上に出ない');
    // 「⋯」はキャプションと同じ高さの右側（キャプションのある表）。
    const pos = await page.evaluate(() => {
      const t = document.querySelector('#w-editor-content table');
      const c = t.querySelector('caption').getBoundingClientRect();
      const b = document.querySelector('#w-editor-content .w-table-menu-btn').getBoundingClientRect();
      const tb = t.getBoundingClientRect();
      return { capMid: (c.top + c.bottom) / 2, btnTop: b.top, btnBottom: b.bottom, btnRight: b.right, tblRight: tb.right, capText: t.querySelector('caption').textContent };
    });
    check(pos.btnTop <= pos.capMid && pos.capMid <= pos.btnBottom, '「⋯」はキャプションと同じ高さ', JSON.stringify(pos));
    check(Math.abs(pos.btnRight - pos.tblRight) < 4, '「⋯」は右の端', JSON.stringify(pos));
    check(pos.capText === 'E2E 部品表', 'キャプションの文字は変わらない（「⋯」はキャプションの外）', pos.capText);
    // メニューを開く・閉じる。
    await page.locator('#w-editor-content .w-table-menu-btn').first().click();
    const items = await page.locator('#w-table-menu-list.active .file-menu-item').allInnerTexts();
    check(items.length === 1 && items[0].includes('この表を印刷'), '「⋯」を押すとメニューに「🖨 この表を印刷」', items.join(','));
    const mb = await page.evaluate(() => { const m = document.getElementById('w-table-menu-list').getBoundingClientRect(); return { left: m.left, right: m.right, vw: document.documentElement.clientWidth }; });
    check(mb.left >= 0 && mb.right <= mb.vw, 'メニューは画面の中に開く', JSON.stringify(mb));
    await page.keyboard.press('Escape');
    check(await page.locator('#w-table-menu-list.active').count() === 0, 'Esc で閉じる');
    const printVia = async (i) => {
      await page.locator('#w-editor-content .w-table-menu-btn').nth(i).click();
      await page.locator('#w-table-menu-list.active .file-menu-item', { hasText: 'この表を印刷' }).click();
    };

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
    await printVia(1);
    const p = await frameOf();
    check(!!p, '押すと見えない枠が刷られる');
    if (p) {
      check(p.printing && p.css, '枠の中は紙の決まり（app.css・w-printing-sheet）で刷る');
      check(!p.pageMarked, 'ページそのものには印を付けない（ページは刷らない）');
      check(p.text.includes('E2E-PRINT-B'), '押した表が紙に出る');
      check(!p.text.includes('E2E-PRINT-A') && !p.text.includes('前の段落'), 'ほかの表・本文は紙に出ない', p.text.slice(0, 120));
      check(p.text.includes('【E2E】表の印刷'), '紙の頭にページの題が出る');
      check(!/<button|この表を印刷|⋯/.test(p.html), '紙に「⋯」も印刷のボタンも出ない');
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
    await printVia(0);
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
    const inEdit = await page.locator('#w-editor-content .w-table-menu').count();
    check(inEdit === 0, '編集モードでは出ない', inEdit + ' 個');
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForFunction(() => !document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    await page.waitForTimeout(500);
    const back = await page.locator('#w-editor-content .w-table-menu-btn').count();
    check(back === 2, '閲覧モードに戻ると、また付く', back + ' 個');
    const saved = await page.evaluate(async (pid) => (await (await fetch('/api/load?id=' + pid)).text()), id);
    check(!saved.includes('w-table-menu'), '本文に保存されない');
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
