// 文字の帯の出る場所（2026-10-07——assets/app.js の positionContextToolbar・positionDockedToolbar）を画面から確かめる。
//
// 利用者:「帯が出ません」→「一番下では出ます。ブロックを追加する丸に+も一番下しか出ません」——写真を何十枚も持つ節は高さが
// 7万px にもなり、帯をブロックの上端の上に置いていたので、その下の方を触ると帯は画面のはるか上（実測 -68,624px）に出ていた。
//
//   ① PC: 背の高い節の下の方の段落を押すと、帯はその行のすぐ上に見える（画面の中・ほかの物に隠れない）
//   ② PC: 同じ節の中でキャレットを別の段落へ移すと、帯もついてくる
//   ③ PC: 画面のいちばん上の行（ヘッダーの下）を押すと、帯は行の下に出る（ヘッダーに隠れない）
//   ④ スマホ（375px・390px）: 帯は上端に1列で、ボタンが全部画面に入る
//
// ページは自分で作り、最後に消します。本物の置き場には書きません。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-toolbar-position.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1400, height: 900 } });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let id = '';
  // 背の高い節（段落 150 個——節そのものが1つの上の段のブロック）と、その後ろの段落。
  let body = '<h1>【E2E】帯の場所</h1><section data-id="ss01"><h2>写真の節</h2>';
  for (let i = 0; i < 150; i++) body += '<p>行' + i + '</p>';
  body += '</section><p data-id="zz09">最後の段落</p>';
  const st = (pg, sel) => pg.evaluate((s) => {
    const t = document.getElementById('w-context-toolbar');
    const r = t.getBoundingClientRect();
    const c = document.querySelector(s).getBoundingClientRect();
    const at = r.width ? document.elementFromPoint(r.left + 12, r.top + r.height / 2) : null;
    return { active: t.classList.contains('active'), top: Math.round(r.top), bottom: Math.round(r.bottom), lineTop: Math.round(c.top), lineBottom: Math.round(c.bottom),
      seen: at ? (t.contains(at) ? '帯' : at.tagName) : 'なし', vh: innerHeight };
  }, sel);
  try {
    await login(page, BASE);
    id = await makePage(page, body);
    await page.goto(BASE + '/' + id + '?edit=true');
    await page.waitForSelector('#w-editor-content section[contenteditable="true"]', { timeout: 10000 });
    await sleep(800);

    // ①
    const p140 = '#w-editor-content section[data-id="ss01"] p:nth-of-type(141)';
    await page.locator(p140).scrollIntoViewIfNeeded();
    await page.click(p140);
    await sleep(400);
    const s1 = await st(page, p140);
    check('① 背の高い節の下の方を押すと、帯はその行のすぐ上に見える', s1.active && s1.seen === '帯' && s1.top >= 0 && s1.bottom <= s1.lineTop + 2 && s1.lineTop - s1.bottom < 30,
      JSON.stringify(s1));

    // ② 矢印キーで同じ節の中の5行下へ（マウスで押すと選択がいったん外れて帯を出し直すので、置き直しの枝を通らない）。
    const p145 = '#w-editor-content section[data-id="ss01"] p:nth-of-type(146)';
    for (let i = 0; i < 5; i++) await page.keyboard.press('ArrowDown');
    await sleep(400);
    const s2 = await st(page, p145);
    check('② 同じ節の中で別の段落へ移ると、帯もついてくる', s2.active && s2.seen === '帯' && s2.bottom <= s2.lineTop + 2 && s2.lineTop - s2.bottom < 30, JSON.stringify(s2));

    // ③ 画面のいちばん上の行——ヘッダーを出してから、そのすぐ下の行を押す。
    await page.mouse.move(700, 500);
    await page.mouse.wheel(0, -300);
    await sleep(600);
    const topSel = await page.evaluate(() => {
      const h = document.querySelector('header.header').getBoundingClientRect().bottom;
      const ps = Array.from(document.querySelectorAll('#w-editor-content section[data-id="ss01"] p'));
      const el = ps.find((e) => e.getBoundingClientRect().top > Math.max(h, 0) + 2);
      el.setAttribute('data-probe', 'top');
      return '[data-probe="top"]';
    });
    await page.click(topSel);
    await sleep(400);
    const s3 = await st(page, topSel);
    check('③ 画面のいちばん上の行では、帯は行の下に出る（ヘッダーに隠れない）', s3.active && s3.seen === '帯' && s3.top >= s3.lineBottom - 2, JSON.stringify(s3));
    await page.goto(BASE + '/000000');
    await sleep(500);

    // ④ スマホ。
    for (const w of [375, 390]) {
      const phone = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: w, height: 740 }, isMobile: true, hasTouch: true });
      const pp = await phone.newPage();
      await login(pp, BASE);
      await pp.goto(BASE + '/' + id + '?edit=true');
      await pp.waitForSelector('#w-editor-content p[contenteditable="true"]', { timeout: 10000 });
      await pp.tap('#w-editor-content [data-id="zz09"]');
      await sleep(700);
      const s4 = await pp.evaluate(() => {
        const t = document.getElementById('w-context-toolbar');
        const r = t.getBoundingClientRect();
        const out = Array.from(t.querySelectorAll('button')).filter((b) => { const q = b.getBoundingClientRect(); return q.left < r.left || q.right > r.right + 0.5; }).map((b) => b.textContent);
        return { top: Math.round(r.top), height: Math.round(r.height), buttons: t.querySelectorAll('button').length, out };
      });
      check('④ スマホ ' + w + 'px: 帯は上端に1列で、ボタンが全部入る', s4.top <= 20 && s4.height <= 56 && s4.buttons >= 12 && s4.out.length === 0, JSON.stringify(s4));
      await pp.goto(BASE + '/000000').catch(() => {});
      await phone.close();
    }
  } finally {
    await page.goto(BASE + '/000000').catch(() => {});
    await deletePage(page, id);
    const st2 = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).status, id);
    check('作ったページを消した', st2 === 404, String(st2));
  }
  check('ページのエラーが無い', errs.length === 0, errs.join(' / '));
  await browser.close();
  console.log(fails ? `✗ ${fails} 件` : '✓ すべて合格');
  process.exit(fails ? 1 : 0);
})();
