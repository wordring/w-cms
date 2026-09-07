// スマホ幅で表がどう見えるかを測る（2026-09-07）。
//
// 問い（ユーザー）:「テーブルは横スクロールできるようにして、横幅を確保するほうが
// 見やすいのでは？」——`@media (max-width: 760px)` に
// `table { display:block; overflow-x:auto }` は**既にある**。効いているか、
// そして**列が潰れていないか**を見る。
//
// 使い方: node probe-table.js
const { chromium } = require('playwright');

const BASE = process.env.WCMS_BASE || 'http://localhost:8080';
const PAGE = process.env.WCMS_PAGE || '/010153'; // 通信箱（未処理一覧＝列の多い表）

(async () => {
  const browser = await chromium.launch();
  for (const width of [320, 390, 760, 1200]) {
    const ctx = await browser.newContext({ viewport: { width, height: 800 } });
    const page = await ctx.newPage();
    await page.goto(BASE + '/login');
    await page.fill('#username', 'a'); await page.fill('#password', 'a');
    await page.click('button[type=submit]');
    await page.waitForLoadState('networkidle');
    await page.goto(BASE + PAGE);
    await page.waitForTimeout(600);

    const r = await page.evaluate(() => {
      const t = document.querySelector('#w-editor-content table');
      if (!t) return { ok: false };
      const s = getComputedStyle(t);
      // 1行目のセルの実寸（潰れているかを見る）
      const cells = Array.from(t.querySelectorAll('tr')[1] ? t.querySelectorAll('tr')[1].children : [])
        .map((c) => Math.round(c.getBoundingClientRect().width));
      return {
        ok: true,
        display: s.display,
        overflowX: s.overflowX,
        clientWidth: t.clientWidth,   // 見えている幅
        scrollWidth: t.scrollWidth,   // 中身の幅
        scrollable: t.scrollWidth > t.clientWidth + 1,
        cells,
        docOverflow: document.documentElement.scrollWidth > window.innerWidth,
      };
    });

    if (!r.ok) { console.log('幅' + width + ': 表が見つかりません'); await ctx.close(); continue; }
    console.log('\n=== 幅 ' + width + 'px ===');
    console.log('  display=' + r.display + ' overflow-x=' + r.overflowX);
    console.log('  見えている幅 ' + r.clientWidth + ' / 中身の幅 ' + r.scrollWidth +
      ' → ' + (r.scrollable ? '横スクロールできる' : '**スクロールしない（＝収まるまで潰れている）**'));
    console.log('  セルの幅: ' + r.cells.join(', '));
    console.log('  ページ全体の横はみ出し: ' + (r.docOverflow ? '⚠ あり' : 'なし'));
    await ctx.close();
  }
  await browser.close();
})();
