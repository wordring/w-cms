// 右レールのページ情報カードのQR（2026-09-10）
const { chromium } = require('playwright');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';
let fail = 0;
const ok = (c, m, x) => { console.log((c ? '  ✓ ' : '  ✗ ') + m + (x ? '  ' + x : '')); if (!c) fail++; };

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 1000 } });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  await page.goto(BASE + '/login');
  await page.fill('#username', 'a'); await page.fill('#password', 'a');
  await page.click('button[type=submit]'); await page.waitForLoadState('networkidle');
  await page.goto(BASE + '/010268'); await page.waitForTimeout(1500);

  const r = await page.evaluate(() => {
    const img = document.getElementById('w-pi-qr');
    const label = document.getElementById('w-pi-qr-url');
    if (!img) return null;
    const b = img.getBoundingClientRect();
    return {
      hidden: img.hidden, src: img.getAttribute('src'), alt: img.alt,
      w: Math.round(b.width), h: Math.round(b.height),
      natural: img.naturalWidth + 'x' + img.naturalHeight,
      label: label ? label.textContent : '',
      inPageInfo: !!img.closest('.rail-right'),
    };
  });
  ok(!!r, 'QRの枠がある');
  ok(r && !r.hidden, '表示されている');
  ok(r && r.inPageInfo, '右レールの中にある');
  ok(r && /^\/api\/qr\?page_id=010268$/.test(r.src || ''), 'このページのIDで取っている', r && r.src);
  ok(r && r.w >= 100 && Math.abs(r.w - r.h) <= 2, '正方形で十分な大きさ', r && (r.w + 'x' + r.h));
  ok(r && r.label === BASE + '/010268', 'URLが文字でも出る（目で確かめられる）', r && r.label);
  ok(r && (r.alt || '').length > 0, '代替テキストがある');
  ok(errs.length === 0, 'JSエラーなし', errs[0] || '');

  // 別のページへ移ると、そのページのQRになるか
  await page.goto(BASE + '/010153'); await page.waitForTimeout(1500);
  const s = await page.evaluate(() => document.getElementById('w-pi-qr').getAttribute('src'));
  ok(/page_id=010153$/.test(s || ''), 'ページを移ると追随する', s);

  await browser.close();
  console.log(fail ? '\n' + fail + ' 件失敗' : '\n全項目OK');
  process.exit(fail ? 1 : 0);
})();
