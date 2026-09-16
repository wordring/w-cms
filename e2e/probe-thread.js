// やりとりの前後（In-Reply-To の鎖）が画面に出るか（2026-09-14）
const { chromium } = require('playwright');
const lib = require('./lib');
const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
// **当て先は走るときに探します**（2026-09-16）——返信の鎖でつながった2枚を
// 取り込み済みのメールから見つけます（詳しくは lib.js の冒頭）。
let fail = 0;
const ok = (c, m, x) => { console.log((c ? '  OK ' : '  NG ') + m + (x ? '  ' + x : '')); if (!c) fail++; };

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 1000 }, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  await lib.login(page, BASE);
  const pair = await lib.findThreadPair(page);
  const PREV = process.env.WCMS_THREAD_PREV || pair.prev;
  const NEXT = process.env.WCMS_THREAD_NEXT || pair.next;
  ok(!!PREV && !!NEXT, '返信の鎖でつながった2枚を見つけた', PREV + ' -> ' + NEXT);
  if (!PREV || !NEXT) { console.log('取り込んだメールに In-Reply-To の鎖がありません'); await browser.close(); process.exit(1); }

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

  // 前の記録——次（この記録への返り）が NEXT にある。
  const a = await look(PREV);
  console.log('    ' + PREV + ': ' + JSON.stringify(a.rows, null, 1));
  ok(a.shown, PREV + ' に「やりとりの前後」が出る');
  ok(a.rows.some(r => r.href === '/' + NEXT), '次（' + NEXT + '）へのリンクがある');
  ok(a.rows.some(r => /↓/.test(r.text)), '次は下向きの矢印');
  ok(!a.overflow, '横にはみ出さない');

  // 次の記録——前（返信元）が PREV。
  const b = await look(NEXT);
  console.log('    ' + NEXT + ': ' + JSON.stringify(b.rows, null, 1));
  ok(b.shown, NEXT + ' に「やりとりの前後」が出る');
  ok(b.rows.some(r => r.href === '/' + PREV), '前（' + PREV + '）へのリンクがある');
  ok(b.rows.some(r => /↑/.test(r.text)), '前は上向きの矢印');

  // 実際に押して移れること（「移動できると便利」がご要望そのもの）。
  await page.click('#w-editor-content .mail-thread a[href="/' + PREV + '"]');
  await page.waitForTimeout(1500);
  ok(page.url().endsWith('/' + PREV), '押すと前のメールへ移れる', page.url());

  // 鎖の無い記録では何も出さない（1通で終わる記録のほうが多い）。
  // **通信箱の下から、鎖に居ない記録を1枚選びます**（当て先を焼き込まない）。
  const mb = await lib.findMailbox(page);
  let lone = '';
  for (const y of await lib.childrenOf(page, mb)) {
    for (const m of await lib.childrenOf(page, y.ID)) {
      for (const rec of await lib.childrenOf(page, m.ID)) {
        if (lone || rec.ID === PREV || rec.ID === NEXT) continue;
        const body = await lib.bodyOf(page, rec.ID);
        if (!/<dt>返信元メッセージID<\/dt>/.test(body)) lone = rec.ID;
      }
    }
  }
  if (lone) {
    const c = await look(lone);
    ok(!c.shown, '前後の無い記録には出さない（' + lone + '）');
  } else {
    console.log('  -- 鎖に居ない記録が見つからないので、この項目は飛ばします');
  }

  ok(errs.length === 0, 'JSエラーなし', errs[0] || '');
  await browser.close();
  console.log(fail ? '\n' + fail + ' 件失敗' : '\n全項目OK');
  process.exit(fail ? 1 : 0);
})();
