// 文字の帯の出る場所（2026-10-07——assets/app.js の positionContextToolbar・positionDockedToolbar）を画面から確かめる。
//
// 利用者:「帯が出ません」→「一番下では出ます。ブロックを追加する丸に+も一番下しか出ません」——写真を何十枚も持つ節は高さが
// 7万px にもなり、帯をブロックの上端の上に置いていたので、その下の方を触ると帯は画面のはるか上（実測 -68,624px）に出ていた。
//
// 2026-10-19 から、PC の帯は本文の上に固定した1本（#w-fixed-bar）の中です（app.js の initFixedBar）。
// 利用者:「…テーブルの編集では、列の見出しを見ながら書くことが多いからです。いっそのこと、普通のアプリケーションのように。
// メニューを上部に固定してしまってはどうでしょう？」——それまでの①〜③（帯が行のすぐ上についてくる）は、固定の形に書き直した。
//
//   ① PC: 背の高い節の下の方の段落を押すと、文字の帯は固定の1本の中で押せる（画面の中・押した行に重ならない）
//   ② PC: 同じ節の中でキャレットを別の段落へ移しても、1本は動かない（行についてこない）・押した行に重ならない
//   ③ PC: 表のセルを押すと、表の帯が同じ1本の中で押せる（行の横に浮かない）・押したセルに重ならない。文字の帯は押せるまま、タグの帯は押せない
//   ④ スマホ（375px・390px）: 帯は上端に1列で、ボタンが全部画面に入る（固定の1本は無い）
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
  body += '</section><p data-id="zz09">最後の段落</p>' +
    '<table><caption>見本の表</caption><tbody><tr><th>品名</th><th>数量</th></tr><tr><td>板</td><td>2</td></tr></tbody></table><p>あと</p>';
  // st は固定の1本（strip）と、押した行（sel）の位置・各帯の押せる／押せないを返す。
  const st = (pg, sel) => pg.evaluate((s) => {
    const strip = document.getElementById('w-fixed-bar');
    const r = strip ? strip.getBoundingClientRect() : { top: -1, bottom: -1, width: 0 };
    const c = document.querySelector(s).getBoundingClientRect();
    const t = document.getElementById('w-context-toolbar');
    const tr = t.getBoundingClientRect();
    const at = tr.width ? document.elementFromPoint(tr.left + 12, tr.top + tr.height / 2) : null;
    const on = (id) => document.getElementById(id).classList.contains('active');
    return { inStrip: !!strip && t.parentElement === strip && document.getElementById('w-table-toolbar').parentElement === strip,
      ctx: on('w-context-toolbar'), table: on('w-table-toolbar'), dl: on('w-dl-toolbar'),
      top: Math.round(r.top), bottom: Math.round(r.bottom), lineTop: Math.round(c.top), lineBottom: Math.round(c.bottom),
      seen: at ? (t.contains(at) ? '帯' : at.tagName) : 'なし', vh: innerHeight };
  }, sel);
  const clear = (x) => x.lineTop >= x.bottom - 1 || x.lineBottom <= x.top + 1; // 押した行と1本が重ならない
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
    check('① 背の高い節の下の方を押すと、文字の帯は固定の1本の中で押せる（画面の中・行に重ならない）',
      s1.inStrip && s1.ctx && s1.seen === '帯' && s1.top >= 0 && s1.bottom <= s1.vh && clear(s1), JSON.stringify(s1));

    // ② 矢印キーで同じ節の中の5行下へ（マウスで押すと選択がいったん外れて帯を出し直すので、置き直しの枝を通らない）。
    const p145 = '#w-editor-content section[data-id="ss01"] p:nth-of-type(146)';
    for (let i = 0; i < 5; i++) await page.keyboard.press('ArrowDown');
    await sleep(400);
    const s2 = await st(page, p145);
    check('② 同じ節の中で別の段落へ移っても、1本は動かない・行に重ならない', s2.ctx && s2.top === s1.top && clear(s2), JSON.stringify(s2));

    // ③ 表のセル——表の帯が同じ1本の中で押せる（行の横に浮かない）。
    const cell = '#w-editor-content table tr:nth-of-type(2) > td:first-of-type'; // td の属性は本文で落ちるので、形で指す
    await page.locator(cell).scrollIntoViewIfNeeded();
    await page.click(cell);
    await sleep(400);
    const s3 = await st(page, cell);
    check('③ 表のセルでは、表の帯が同じ1本の中で押せる・セルに重ならない（文字の帯は押せる・タグの帯は押せない）',
      s3.inStrip && s3.table && s3.ctx && !s3.dl && clear(s3), JSON.stringify(s3));
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
      const noStrip = await pp.evaluate(() => !document.getElementById('w-fixed-bar'));
      check('④ スマホ ' + w + 'px: 帯は上端に1列で、ボタンが全部入る（固定の1本は無い）', s4.top <= 20 && s4.height <= 56 && s4.buttons >= 12 && s4.out.length === 0 && noStrip, JSON.stringify(s4));
      await pp.goto(BASE + '/000000').catch(() => {});
      await phone.close();
    }
  } finally {
    await page.goto(BASE + '/000000').catch(() => {});
    await deletePage(page, id);
    // ⚠ 2026-10-19 に、消したはずのページが残ったことが2回あった（去った直後の消し方が間に合わない——試験の側の間合い）。
    //    残っていたら少し待ってもう一度消す（実運用の木に試験のページを残さない）。
    if (await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).status, id).catch(() => 0) === 200) {
      await sleep(800);
      await deletePage(page, id);
    }
    const st2 = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).status, id);
    check('作ったページを消した', st2 === 404, String(st2));
  }
  check('ページのエラーが無い', errs.length === 0, errs.join(' / '));
  await browser.close();
  console.log(fails ? `✗ ${fails} 件` : '✓ すべて合格');
  process.exit(fails ? 1 : 0);
})();
