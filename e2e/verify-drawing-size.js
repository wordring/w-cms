// 図面インライン表示：飾りを外した見え方・つまんでの伸縮・高さの記憶（2026-09-10）
const { chromium } = require('playwright');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';
const PAGE = process.env.WCMS_PAGE || '/010268';
let fail = 0;
function ok(c, m, extra) { console.log((c ? '  ✓ ' : '  ✗ ') + m + (extra ? '  ' + extra : '')); if (!c) fail++; }

(async () => {
  const browser = await chromium.launch({ channel: 'chrome' });
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 1000 } });
  const page = await ctx.newPage();
  await page.goto(BASE + '/login');
  await page.fill('#username', 'a'); await page.fill('#password', 'a');
  await page.click('button[type=submit]'); await page.waitForLoadState('networkidle');

  await page.evaluate(() => { try { localStorage.removeItem('wcms.ui'); } catch (e) {} });
  await page.goto(BASE + PAGE); await page.waitForTimeout(2500);

  const a = await page.evaluate(() => {
    const w = document.querySelector('#w-editor-content .drawing-inline');
    const e = w && w.querySelector('embed');
    if (!w) return null;
    const s = getComputedStyle(w);
    return { h: Math.round(w.getBoundingClientRect().height), resize: s.resize, overflow: s.overflow,
             src: e ? e.getAttribute('src') : null, title: w.title,
             embedH: e ? Math.round(e.getBoundingClientRect().height) : 0 };
  });
  console.log('■ 既定'); 
  ok(!!a, '図面枠がある');
  ok(a && a.resize === 'vertical', 'つまんで縦に変えられる', a && a.resize);
  ok(a && /navpanes=0&view=FitH/.test(a.src || ''), 'サムネイル欄を閉じ幅に合わせている（ツールバーは残す）', a && (a.src || '').split('#')[1]);
  ok(a && a.title.length > 0, '操作の手掛かり（tooltip）がある');
  ok(a && a.h > 400, '既定の高さは 70vh 相当', a && a.h + 'px');
  ok(a && a.embedH > 0 && a.embedH < a.h, 'つまみの余地を残して PDF が収まっている',
     a && ('枠' + a.h + ' / PDF' + a.embedH));

  // ── つまんで伸ばす
  console.log('■ つまんで伸ばす');
  // **つまみが画面の中に来るまでスクロールします**——枠は700px あるので、
  // そのままだと下端が表示領域の外で、マウスが届きません（1度これで空振りしました）。
  await page.locator('#w-editor-content .drawing-inline').scrollIntoViewIfNeeded();
  await page.evaluate(() => window.scrollBy(0, 120));
  await page.waitForTimeout(300);
  const box = await page.locator('#w-editor-content .drawing-inline').boundingBox();
  console.log('    つまみの位置 y=' + Math.round(box.y + box.height) + ' / 表示領域 ' + 1000);
  await page.mouse.move(box.x + box.width - 6, box.y + box.height - 6);
  await page.mouse.down();
  await page.mouse.move(box.x + box.width - 6, box.y + box.height + 250, { steps: 12 });
  await page.mouse.up();
  await page.waitForTimeout(400);
  const b = await page.evaluate(() => ({
    h: Math.round(document.querySelector('#w-editor-content .drawing-inline').getBoundingClientRect().height),
    saved: (JSON.parse(localStorage.getItem('wcms.ui') || '{}').drawing || {}).height,
    pe: getComputedStyle(document.querySelector('#w-editor-content .drawing-inline embed')).pointerEvents,
  }));
  ok(b.h > a.h + 150, '高さが伸びた', a.h + ' → ' + b.h + 'px');
  ok(typeof b.saved === 'number' && Math.abs(b.saved - b.h) <= 2, '高さを憶えた', 'saved=' + b.saved);
  ok(b.pe !== 'none', '離したらPDFへの操作が戻る', b.pe);

  // ── 開き直しても同じ高さか
  console.log('■ 開き直す');
  await page.goto(BASE + PAGE); await page.waitForTimeout(2500);
  const c = await page.evaluate(() => Math.round(
    document.querySelector('#w-editor-content .drawing-inline').getBoundingClientRect().height));
  ok(Math.abs(c - b.h) <= 2, '前の高さで開く', c + 'px');

  // ── 別の部品ページにも効くか
  await page.goto(BASE + '/010272'); await page.waitForTimeout(2500);
  const d = await page.evaluate(() => Math.round(
    document.querySelector('#w-editor-content .drawing-inline').getBoundingClientRect().height));
  ok(Math.abs(d - b.h) <= 2, '他の部品ページも同じ高さ', d + 'px');

  await browser.close();
  console.log(fail ? '\n' + fail + ' 件失敗' : '\n全項目OK');
  process.exit(fail ? 1 : 0);
})();
