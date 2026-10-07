// 編集中のアンドゥ・リドゥ（2026-10-07 に本格化——assets/app.js の「編集中のアンドゥ・リドゥ」）を画面から確かめる。
//
// 利用者:「アンドゥ・リドゥを本格化してください」
//
//   ① 打ってすぐ（保存の前）でも Ctrl+Z で戻り、Ctrl+Y で打った文字が戻る
//   ② 打つのを少し止めると区切り——続けて打った2回は Ctrl+Z 2回で1つずつ戻る
//   ③ 打ってすぐの操作（帯で見出しにする）は別の区切り——操作だけが戻り、打った文字は残る
//   ④ 戻しても、変わっていないブロック（ファイル表示の枠——サーバーが描いた中身）はそのまま
//   ⑤ 消したファイル表示を戻すと、描いた中身ごと戻る
//   ⑥ 消した段落を戻す・やり直す（ブロック ID もそのまま）
//   ⑦ 帯の ↶ ↷ でも戻す・やり直す（やり直せないときは押せない・新しく打つとやり直せなくなる）
//   ⑧ 戻した中身が保存される
//   ⑨ スマホの帯にも ↶ ↷（画面に収まる）
//
// ページは自分で作り、最後に消します。本物の置き場には書きません。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-undo.js
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
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 1000 } });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  // 保存の要求の中身（見出しにする途中の空の段落を送らないかを見る）。
  const saves = [];
  page.on('request', (r) => { if (/\/api\/save(-block)?$/.test(new URL(r.url()).pathname)) saves.push(r.postData() || ''); });
  let id = '';
  const text = (did) => page.evaluate((d) => { const el = document.querySelector('#w-editor-content [data-id="' + d + '"]'); return el ? el.tagName + ':' + el.textContent : 'なし'; }, did);
  const caretEnd = async (did) => {
    await page.click('#w-editor-content [data-id="' + did + '"]', { timeout: 5000 });
    await page.keyboard.press('End');
    await sleep(150);
  };
  const fv = () => page.evaluate(() => {
    const s = document.querySelector('#w-editor-content section[data-id="ff09"]');
    return s ? { keep: s.__keep === 1, img: !!s.querySelector('img') } : null;
  });
  const btn = (bid) => page.evaluate((b) => { const el = document.getElementById(b); return el ? (el.disabled ? '押せない' : '押せる') : '無い'; }, bid);
  try {
    await login(page, BASE);
    id = await makePage(page, '<h1>【E2E】アンドゥ</h1><p>x</p>');
    // 画像を添付してファイル表示で開く（中身はサーバーが描く）。
    const att = await page.evaluate(async (pid) => {
      const c = document.createElement('canvas'); c.width = 60; c.height = 40;
      const g = c.getContext('2d'); g.fillStyle = '#22c55e'; g.fillRect(0, 0, 60, 40);
      const blob = await new Promise((r) => c.toBlob(r, 'image/png'));
      const lr = await fetch('/api/lock?id=' + pid, { method: 'POST' }); const lj = await lr.json().catch(() => ({}));
      const fd = new FormData(); fd.append('page_id', pid); fd.append('image_file', new File([blob], 'g.png', { type: 'image/png' }));
      const r = await fetch('/api/upload-image', { method: 'POST', body: fd, headers: { 'X-Lock-Token': lj.token || '' } });
      const d = await r.json().catch(() => ({}));
      await fetch('/api/unlock?id=' + pid + '&token=' + encodeURIComponent(lj.token || ''), { method: 'POST' });
      return d.id || '';
    }, id);
    const body = '<h1>【E2E】アンドゥ</h1><p data-id="aa01">段落A</p><section data-id="ff09" data-type="file-view" data-ref="' + id + '-' + att + '"></section>' +
      '<p data-id="bb02">段落B</p><p data-id="cc03">段落C</p>' +
      '<section data-id="ss05"><h2>図の節</h2><p data-id="dd04">説明</p><section data-type="file-view" data-ref="' + id + '-' + att + '"></section></section>';
    await page.evaluate(async (a) => {
      const lr = await fetch('/api/lock?id=' + a.id, { method: 'POST' }); const lj = await lr.json().catch(() => ({}));
      await fetch('/api/save', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ page_id: a.id, html: a.html, token: lj.token || '' }) });
      await fetch('/api/unlock?id=' + a.id + '&token=' + encodeURIComponent(lj.token || ''), { method: 'POST' });
    }, { id, html: body });

    await page.goto(BASE + '/' + id + '?edit=true');
    await page.waitForSelector('#w-editor-content p[contenteditable="true"]', { timeout: 10000 });
    await page.waitForSelector('#w-editor-content section[data-type="file-view"] img', { timeout: 10000 }).catch(() => {});
    check('ファイル表示の枠が描かれている', !!(await fv()) && (await fv()).img, JSON.stringify(await fv()));
    await page.evaluate(() => { document.querySelector('#w-editor-content section[data-id="ff09"]').__keep = 1; });

    // ①
    await caretEnd('aa01');
    await page.keyboard.type('xyz');
    await page.keyboard.press('Control+z');
    await sleep(300);
    const u1 = await text('aa01');
    await page.keyboard.press('Control+y');
    await sleep(300);
    const r1 = await text('aa01');
    check('① 打ってすぐ Ctrl+Z で戻り、Ctrl+Y で戻る', u1 === 'P:段落A' && r1 === 'P:段落Axyz', u1 + ' → ' + r1);

    // ②
    await sleep(1500);
    await caretEnd('aa01');
    await page.keyboard.type('p');
    await sleep(1200);
    await page.keyboard.type('q');
    await sleep(1200);
    await page.keyboard.press('Control+z');
    await sleep(300);
    const u2a = await text('aa01');
    await page.keyboard.press('Control+z');
    await sleep(300);
    const u2b = await text('aa01');
    check('② 少し止めると区切り——1つずつ戻る', u2a === 'P:段落Axyzp' && u2b === 'P:段落Axyz', u2a + ' → ' + u2b);

    // ③
    await caretEnd('aa01');
    await page.keyboard.type('r');
    await page.locator('#w-context-toolbar .ctx-type[data-tag="H2"]').click({ timeout: 5000 });
    await sleep(200);
    const c3 = await text('aa01');
    await page.keyboard.press('Control+z');
    await sleep(300);
    const u3a = await text('aa01');
    await page.keyboard.press('Control+z');
    await sleep(300);
    const u3b = await text('aa01');
    check('③ 打ってすぐの操作は別の区切り——操作だけが戻り、打った文字は残る', c3 === 'H2:段落Axyzr' && u3a === 'P:段落Axyzr' && u3b === 'P:段落Axyz',
      [c3, u3a, u3b].join(' → '));
    // 見出しにする途中（中身を移した空の段落）の姿を保存しない（focusout がその瞬間に保存していた——2026-10-07）。
    const emptySaved = saves.filter((b) => /data-id=\\"aa01\\"><\/p>|data-id=\\"aa01\\"><br\/?><\/p>/.test(b));
    check('③ 見出しにする途中の空の段落を保存しない', emptySaved.length === 0, emptySaved.map((b) => b.slice(0, 120)).join(' / '));

    // ④
    const f4 = await fv();
    check('④ 戻しても、変わっていないファイル表示の枠はそのまま（描いた中身ごと）', f4 && f4.keep && f4.img, JSON.stringify(f4));

    // ⑤
    await sleep(1000);
    await page.evaluate(() => document.querySelector('#w-editor-content section[data-id="ff09"]').closest('.editor-block').querySelector('.delete-btn').click());
    await sleep(300);
    const gone5 = await fv();
    await caretEnd('bb02');
    await page.keyboard.press('Control+z');
    await sleep(500);
    const f5 = await fv();
    check('⑤ 消したファイル表示を戻すと、描いた中身ごと戻る', gone5 === null && f5 && f5.img, JSON.stringify([gone5, f5]));

    // ⑥
    await sleep(1000);
    await page.evaluate(() => document.querySelector('#w-editor-content [data-id="cc03"]').closest('.editor-block').querySelector('.delete-btn').click());
    await sleep(300);
    const g6 = await text('cc03');
    await caretEnd('bb02');
    await page.keyboard.press('Control+z');
    await sleep(400);
    const u6 = await text('cc03');
    await page.keyboard.press('Control+y');
    await sleep(400);
    const r6 = await text('cc03');
    await page.keyboard.press('Control+z');
    await sleep(400);
    const u6b = await text('cc03');
    check('⑥ 消した段落を戻す・やり直す（ID もそのまま）', g6 === 'なし' && u6 === 'P:段落C' && r6 === 'なし' && u6b === 'P:段落C', [g6, u6, r6, u6b].join(' → '));

    // ⑦ 帯の ↶ ↷。
    await sleep(1000);
    await caretEnd('bb02');
    await page.keyboard.type('s');
    await sleep(1000);
    const b7a = await btn('w-ctx-redo');
    await page.locator('#w-ctx-undo').click({ timeout: 5000 });
    await sleep(400);
    const u7 = await text('bb02');
    const b7b = await btn('w-ctx-redo');
    await page.locator('#w-ctx-redo').click({ timeout: 5000 });
    await sleep(400);
    const r7 = await text('bb02');
    await page.locator('#w-ctx-undo').click({ timeout: 5000 });
    await sleep(400);
    await caretEnd('bb02');
    await page.keyboard.type('t');
    await sleep(900);
    const b7c = await btn('w-ctx-redo');
    check('⑦ 帯の ↶ ↷ で戻す・やり直す', u7 === 'P:段落B' && r7 === 'P:段落Bs', [u7, r7].join(' → '));
    check('⑦ やり直せないときは ↷ が押せない・新しく打つとやり直せなくなる', b7a === '押せない' && b7b === '押せる' && b7c === '押せない', [b7a, b7b, b7c].join(' → '));

    // ⑧
    await sleep(2500);
    const saved = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).text(), id);
    check('⑧ 戻した中身が保存される', /段落Bt</.test(saved) && /段落C</.test(saved) && /data-type="file-view"/.test(saved) && /段落Axyz</.test(saved) && !/段落Axyzr/.test(saved),
      saved.replace(/\s+/g, ' ').slice(0, 300));

    // ⑩ 節の中の段落を直して戻す——節（同じブロック）は作り直さず中身を合わせるので、節の中のファイル表示の枠はそのまま。
    await page.evaluate(() => { document.querySelector('#w-editor-content section[data-id="ss05"] section[data-type="file-view"]').__keep = 1; });
    await page.click('#w-editor-content section[data-id="ss05"] p', { timeout: 5000 });
    await page.keyboard.press('End');
    await page.keyboard.type('w');
    await sleep(1000);
    await page.keyboard.press('Control+z');
    await sleep(400);
    const t10 = await page.evaluate(() => {
      const sec = document.querySelector('#w-editor-content section[data-id="ss05"]');
      const inner = sec && sec.querySelector('section[data-type="file-view"]');
      return { text: sec ? sec.querySelector('p').textContent : 'なし', keep: !!inner && inner.__keep === 1, img: !!inner && !!inner.querySelector('img') };
    });
    check('⑩ 節の中を直して戻しても、節の中のファイル表示の枠はそのまま', t10.text === '説明' && t10.keep && t10.img, JSON.stringify(t10));
    await sleep(2000);

    // ⑨ スマホ。
    await page.goto(BASE + '/000000');
    await sleep(500);
    const phone = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 375, height: 740 }, isMobile: true, hasTouch: true });
    const pp = await phone.newPage();
    await login(pp, BASE);
    await pp.goto(BASE + '/' + id + '?edit=true');
    await pp.waitForSelector('#w-editor-content p[contenteditable="true"]', { timeout: 10000 });
    await pp.tap('#w-editor-content [data-id="bb02"]');
    await sleep(700);
    const ph = await pp.evaluate(() => {
      const vw = window.innerWidth;
      const inView = (b) => { const el = document.getElementById(b); if (!el) return false; const r = el.getBoundingClientRect(); return r.width > 0 && r.left >= 0 && r.right <= vw; };
      return { undo: inView('w-ctx-undo'), redo: inView('w-ctx-redo') };
    });
    check('⑨ スマホの帯にも ↶ ↷（画面に収まる）', ph.undo && ph.redo, JSON.stringify(ph));
    await pp.goto(BASE + '/000000').catch(() => {});
    await phone.close();
  } finally {
    await page.goto(BASE + '/000000').catch(() => {});
    await deletePage(page, id);
    const st = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).status, id);
    check('作ったページを消した', st === 404, String(st));
  }
  check('ページのエラーが無い', errs.length === 0, errs.join(' / '));
  await browser.close();
  console.log(fails ? `✗ ${fails} 件` : '✓ すべて合格');
  process.exit(fails ? 1 : 0);
})();
