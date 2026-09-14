// やりとりの前後（In-Reply-To の鎖）が画面に出るか（2026-09-14）
const { chromium } = require('playwright');
const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
let fail = 0;
const ok = (c, m, x) => { console.log((c ? '  OK ' : '  NG ') + m + (x ? '  ' + x : '')); if (!c) fail++; };

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 1000 }, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  await page.goto(BASE + '/login');
  await page.fill('#username', 'a'); await page.fill('#password', 'a');
  await page.click('button[type=submit]'); await page.waitForLoadState('networkidle');

  const look = async (id) => {
    await page.goto(BASE + '/' + id);
    await page.waitForTimeout(1800);
    return await page.evaluate(() => {
      const w = document.querySelector('#w-editor-content .mail-thread');
      return {
        shown: !!w,
        head: w ? (w.querySelector('.mail-thread-head') || {}).textContent : '',
        rows: w ? Array.from(w.querySelectorAll('li')).map(li => ({
          text: li.textContent.trim(),
          href: (li.querySelector('a') || {}).getAttribute ? li.querySelector('a').getAttribute('href') : '',
        })) : [],
        overflow: document.documentElement.scrollWidth > window.innerWidth,
      };
    });
  };

  // 010271「RE: Q210…」——次（この記録への返り）が 010272 にある。
  const a = await look('010271');
  console.log('    010271: ' + JSON.stringify(a.rows, null, 1));
  ok(a.shown, '010271 に「やりとりの前後」が出る');
  ok(a.rows.some(r => r.href === '/010272'), '次（010272）へのリンクがある');
  ok(a.rows.some(r => /↓/.test(r.text)), '次は下向きの矢印');
  ok(!a.overflow, '横にはみ出さない');

  // 010272「追伸）Q210…」——前（返信元）が 010271。
  const b = await look('010272');
  console.log('    010272: ' + JSON.stringify(b.rows, null, 1));
  ok(b.shown, '010272 に「やりとりの前後」が出る');
  ok(b.rows.some(r => r.href === '/010271'), '前（010271）へのリンクがある');
  ok(b.rows.some(r => /↑/.test(r.text)), '前は上向きの矢印');

  // 実際に押して移れること（「移動できると便利」がご要望そのもの）。
  await page.click('#w-editor-content .mail-thread a[href="/010271"]');
  await page.waitForTimeout(1500);
  ok(/\/010271$/.test(page.url()), '押すと前のメールへ移れる', page.url());

  // 鎖の無い記録では何も出さない（1通で終わる記録のほうが多い）。
  const c = await look('010266');
  ok(!c.shown, '前後の無い記録には出さない');

  ok(errs.length === 0, 'JSエラーなし', errs[0] || '');
  await browser.close();
  console.log(fail ? '\n' + fail + ' 件失敗' : '\n全項目OK');
  process.exit(fail ? 1 : 0);
})();
