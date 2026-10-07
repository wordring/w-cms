// ブロックの種類を替える・スラッシュを打たずに足す（2026-10-07——assets/app.js の「ブロックの種類を替える」）を画面から確かめる。
//
// 利用者:「既存の文字列ブロックを見出しに変えたり段落に変えたりしたい」「スラッシュを入力するのは、日本語環境ではめんどう」
//
//   ① 段落にキャレット → 帯の ¶ に印・H2 を押すと見出し2（文字・ブロック ID はそのまま・キャレットも残る）
//   ② 見出しで ¶ を押すと段落
//   ③ 節の中の段落も替わる（節はそのまま）
//   ④ 表の中では押せない
//   ⑤ 帯の「＋」で下に段落が足され、スラッシュメニューと同じ一覧が開く
//   ⑥ 全角の「／」でもメニューが開く
//   ⑦ 保存した本文に替えた種類が残る・Ctrl+Z で戻る
//   ⑧ スマホ幅（触る画面）では下の帯が画面からはみ出さない
//
// ページは自分で作り、最後に消します。本物の置き場には書きません。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-block-type.js
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
  let id = '';
  const body = '<h1>【E2E】ブロックの種類</h1><p data-id="aa01">段落A</p><h2 data-id="bb02">見出しB</h2>' +
    '<section data-id="cc03"><h2>節の名前</h2><p>節の中の段落</p></section>' +
    '<table data-id="dd04"><tbody><tr><th>名前</th></tr><tr><td>セル</td></tr></tbody></table><p data-id="ee05">最後</p>';
  // caretIn は、その文字の要素の末尾へキャレットを置く（クリックしてから End）。
  const caretIn = async (selector) => {
    await page.click(selector, { timeout: 5000 });
    await page.keyboard.press('End');
    await sleep(200);
  };
  const types = () => page.evaluate(() => Array.from(document.querySelectorAll('#w-context-toolbar .ctx-type'))
    .map((b) => b.dataset.tag + (b.classList.contains('is-active') ? '*' : '') + (b.disabled ? '-' : '')).join(' '));
  const typeBtn = (tag) => page.locator('#w-context-toolbar .ctx-type[data-tag="' + tag + '"]');
  try {
    await login(page, BASE);
    id = await makePage(page, body);
    await page.goto(BASE + '/' + id + '?edit=true');
    await page.waitForSelector('#w-editor-content p[contenteditable="true"]', { timeout: 10000 });

    // ①
    await caretIn('#w-editor-content p[data-id="aa01"]');
    const t1 = await types();
    await typeBtn('H2').click({ timeout: 5000 });
    await page.keyboard.type('X');
    const a1 = await page.evaluate(() => {
      const el = document.querySelector('#w-editor-content [data-id="aa01"]');
      return el ? el.tagName + ':' + el.textContent : 'なし';
    });
    check('① 段落で ¶ に印・H2 を押すと見出し2（文字・ID・キャレットが残る）', /P\*/.test(t1) && a1 === 'H2:段落AX', t1 + ' → ' + a1);
    check('① いまの種類の印が H2 へ移る', /H2\*/.test(await types()), await types());

    // ②
    await caretIn('#w-editor-content h2[data-id="bb02"]');
    await typeBtn('P').click({ timeout: 5000 });
    const a2 = await page.evaluate(() => document.querySelector('#w-editor-content [data-id="bb02"]').tagName);
    check('② 見出しで ¶ を押すと段落', a2 === 'P', a2);

    // ③
    await caretIn('#w-editor-content section[data-id="cc03"] p');
    await typeBtn('H3').click({ timeout: 5000 });
    const a3 = await page.evaluate(() => {
      const sec = document.querySelector('#w-editor-content section[data-id="cc03"]');
      return Array.from(sec.children).filter((c) => !c.classList.contains('vocab-chrome')).map((c) => c.tagName + ':' + c.textContent.trim()).join('・');
    });
    check('③ 節の中の段落も替わる（節はそのまま）', a3 === 'H2:節の名前・H3:節の中の段落', a3);

    // ④
    await caretIn('#w-editor-content td');
    const t4 = await types();
    check('④ 表の中では押せない', t4.split(' ').every((s) => s.endsWith('-')), t4);

    // ⑤
    await caretIn('#w-editor-content p[data-id="ee05"]');
    await page.locator('#w-ctx-add').click({ timeout: 5000 });
    await sleep(500);
    const a5 = await page.evaluate(() => {
      const menu = document.getElementById('w-slash-menu');
      const shown = !!menu && getComputedStyle(menu).display !== 'none' && menu.querySelectorAll('.slash-menu-item').length > 3;
      const last = Array.from(document.querySelectorAll('#w-editor-content .editor-block > .block-content > *')).map((e) => e.tagName + ':' + e.textContent.trim());
      return { shown, tail: last.slice(-2) };
    });
    check('⑤ 「＋」で下に段落が足され、一覧が開く', a5.shown && JSON.stringify(a5.tail) === '["P:最後","P:/"]', JSON.stringify(a5));
    await page.keyboard.press('Escape');
    // ⑥ 全角の「／」。いま足した段落の「/」を消して打ち直す。
    await page.keyboard.press('End');
    await page.keyboard.press('Backspace');
    await sleep(200);
    const closed = await page.evaluate(() => { const m = document.getElementById('w-slash-menu'); return !m || getComputedStyle(m).display === 'none'; });
    await page.keyboard.insertText('／');
    await sleep(400);
    const a6 = await page.evaluate(() => { const m = document.getElementById('w-slash-menu'); return !!m && getComputedStyle(m).display !== 'none'; });
    check('⑥ 全角の「／」でもメニューが開く', closed && a6, JSON.stringify({ closed, a6 }));
    await page.keyboard.press('Escape');
    await page.keyboard.press('Backspace');

    // ⑦ 保存と Ctrl+Z。
    await sleep(2500);
    const saved = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).text(), id);
    check('⑦ 保存した本文に替えた種類が残る', /<h2 data-id="aa01">段落AX<\/h2>/.test(saved) && /<p data-id="bb02">見出しB<\/p>/.test(saved) &&
      /<h3>節の中の段落<\/h3>/.test(saved), saved.replace(/\s+/g, ' ').slice(0, 400));
    // 上の段の節と表の ID は編集して保存しても変わらない（2026-10-07 まで、編集して保存するたびに振り直されていた）。
    check('⑦ 上の段の節と表の ID は変わらない', /<section data-id="cc03">/.test(saved) && /<table data-id="dd04">/.test(saved),
      (saved.match(/<(section|table)[^>]*>/g) || []).join(' '));
    await caretIn('#w-editor-content [data-id="bb02"]');
    await typeBtn('H1').click({ timeout: 5000 });
    await sleep(2500); // 保存（＝アンドゥの区切り）を待つ
    const before = await page.evaluate(() => document.querySelector('#w-editor-content [data-id="bb02"]').tagName);
    await page.keyboard.press('Control+z');
    await sleep(500);
    const after = await page.evaluate(() => document.querySelector('#w-editor-content [data-id="bb02"]').tagName);
    check('⑦ Ctrl+Z で戻る', before === 'H1' && after === 'P', before + ' → ' + after);
    await sleep(2000);

    // ⑧ スマホ幅（触る画面）。PC のタブが編集権を持ったままだと、スマホでは編集に入れない——先に離れる。
    await page.goto(BASE + '/000000');
    await sleep(500);
    const phone =await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 375, height: 740 }, isMobile: true, hasTouch: true });
    const pp = await phone.newPage();
    await login(pp, BASE);
    await pp.goto(BASE + '/' + id + '?edit=true');
    await pp.waitForSelector('#w-editor-content p[contenteditable="true"]', { timeout: 10000 });
    await pp.tap('#w-editor-content [data-id="ee05"]');
    await sleep(800);
    const box = await pp.evaluate(() => {
      const t = document.getElementById('w-context-toolbar');
      const r = t.getBoundingClientRect();
      return { active: t.classList.contains('active'), docked: t.classList.contains('is-docked'), left: Math.round(r.left), right: Math.round(r.right),
        top: Math.round(r.top), height: Math.round(r.height), buttons: t.querySelectorAll('button').length, vw: window.innerWidth };
    });
    // 2026-10-07 から画面の上・全幅の1列（利用者:「スマホでは帯が書き込みを邪魔するので上の方に出した方がよい」）。
    check('⑧ スマホでは帯が画面の上に1列で出て、はみ出さない', box.active && box.docked && box.top <= 20 && box.height <= 56 &&
      box.left >= 0 && box.right <= box.vw, JSON.stringify(box));
    // ⑨ キーボードが出て見えている高さが縮んだ形で、下の方の段落の「＋」——メニューは見えている範囲（帯の下）に収まる
    //    （利用者:「ブロック挿入のメニューは下層キーボードに隠れるので、これも何とかしたいです」）。
    await pp.setViewportSize({ width: 375, height: 380 });
    await pp.evaluate(() => {
      const el = document.querySelector('#w-editor-content [data-id="ee05"]');
      window.scrollBy(0, el.getBoundingClientRect().bottom - (window.innerHeight - 40));
    });
    await pp.tap('#w-editor-content [data-id="ee05"]');
    await sleep(500);
    await pp.tap('#w-ctx-add');
    await sleep(700);
    const m9 = await pp.evaluate(() => {
      const menu = document.getElementById('w-slash-menu');
      const r = menu.getBoundingClientRect();
      const bar = document.getElementById('w-context-toolbar').getBoundingClientRect();
      return { active: menu.classList.contains('active'), top: Math.round(r.top), bottom: Math.round(r.bottom), barBottom: Math.round(bar.bottom),
        vh: window.innerHeight, items: menu.querySelectorAll('.slash-menu-item').length };
    });
    check('⑨ キーボードで狭いときも、メニューは見えている範囲（帯の下）に収まる', m9.active && m9.items > 3 && m9.top >= m9.barBottom &&
      m9.bottom <= m9.vh, JSON.stringify(m9));
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
