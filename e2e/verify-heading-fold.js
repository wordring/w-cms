// 見出しを押して畳めるかを**ブラウザから**確かめる（2026-10-04）。
//
// 利用者:「すべての見出しについて、クリックすると、次の同レベル見出し（H2なら次のH2）の前まで閉じれるようにしたいです」
// （同じ日の午前の「部材の節をまとめて畳む」を置き換えた）。
//
//   ① ページの題（最初の H1）は畳まない・2つ目の H1 は畳める
//   ② H2 を押すと次の H2 の手前まで隠れる（中の H3 も）・前は見える
//   ③ 中の H3 を畳んでから外の H2 を畳んで開くと、H3 の中は畳んだまま
//   ④ 節の最初の見出しは、節の残りと節の後ろの続き（折りたたみなど）も次の見出しまで隠す
//   ⑤ 上の段の見出し（H1）で止まる
//   ⑥ 編集モードでは全部見える・書き出し（保存されるもの）に畳む印が入らない ⑦ 閲覧へ戻ると畳んだ形に戻る
//
// 自分で作ったページで動かし、最後に消します。
//
// 使い方: WCMS_BASE=https://localhost:8443 node verify-heading-fold.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

const BODY = '<h1>【E2E】見出しで畳む</h1>' +
  '<p>HF-前書き</p>' +
  '<h2>HF-A</h2>' +
  '<p>HF-A本文</p>' +
  '<table><tbody><tr><th>x</th></tr><tr><td>HF-A表</td></tr></tbody></table>' +
  '<h3>HF-A1</h3>' +
  '<p>HF-A1本文</p>' +
  '<section><h2>HF-B</h2><p>HF-B本文</p><table><tbody><tr><th>y</th></tr><tr><td>HF-B表</td></tr></tbody></table></section>' +
  '<details><summary>HF-資料</summary><p>HF-B続き</p></details>' +
  '<h2>HF-C</h2>' +
  '<section><h3>HF-C1</h3><p>HF-C1本文</p></section>' +
  '<p>HF-C本文</p>' +
  '<h1>HF-第二章</h1>' +
  '<p>HF-第二章本文</p>' +
  '<section><h3>HF-D1</h3><p>HF-D1本文</p><h2>HF-D2</h2><p>HF-D2本文</p></section>'; // 節の中で上の段の見出しに当たる

const ALL = ['HF-前書き', 'HF-A本文', 'HF-A表', 'HF-A1', 'HF-A1本文', 'HF-B', 'HF-B本文', 'HF-B表', 'HF-資料', 'HF-C', 'HF-C1',
  'HF-C1本文', 'HF-C本文', 'HF-第二章', 'HF-第二章本文', 'HF-D1', 'HF-D1本文', 'HF-D2', 'HF-D2本文'];

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
  const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);

  // 隠れている文字の一覧（本文の要素で、自分の文字がちょうどその言葉のもの）
  const hidden = () => page.evaluate((all) => {
    const els = Array.from(document.querySelectorAll('#w-editor-content p, #w-editor-content td, #w-editor-content summary, #w-editor-content h1, #w-editor-content h2, #w-editor-content h3'));
    return all.filter((t) => {
      const el = els.find((e) => e.textContent.trim().replace(/ …$/, '') === t);
      return !el || el.getClientRects().length === 0;
    });
  }, ALL);
  const head = (text) => page.locator('#w-editor-content :is(h1, h2, h3)', { hasText: new RegExp('^' + text + '$') });

  const id = await makePage(page, BODY);
  const run = async () => {
    if (!id) { check(false, '当て先のページを作れません'); return; }
    await page.goto(BASE + '/' + id);
    await page.waitForTimeout(800);
    check(same(await hidden(), []), 'はじめは全部見える', JSON.stringify(await hidden()));

    // ① ページの題は畳まない
    const foldable = await page.evaluate(() => Array.from(document.querySelectorAll('#w-editor-content :is(h1, h2, h3)'))
      .filter((h) => h.classList.contains('w-foldable')).map((h) => h.textContent.trim()));
    check(same(foldable, ['HF-A', 'HF-A1', 'HF-B', 'HF-C', 'HF-C1', 'HF-第二章', 'HF-D1', 'HF-D2']), 'ページの題（最初の H1）は畳めない・ほかの見出しは畳める',
      JSON.stringify(foldable));
    await page.locator('#w-editor-content h1').first().click();
    check(same(await hidden(), []), 'ページの題を押しても何も隠れない');

    // ② H2 は次の H2 の手前まで（中の H3 も）
    await head('HF-A').click();
    check(same(await hidden(), ['HF-A本文', 'HF-A表', 'HF-A1', 'HF-A1本文']), 'HF-A を押すと次の H2（HF-B）の手前まで隠れる（中の H3 も）',
      JSON.stringify(await hidden()));
    check(await head('HF-A').evaluate((h) => getComputedStyle(h, '::before').content.includes('▸')), '畳んだ見出しに ▸');
    await head('HF-A').click();
    check(same(await hidden(), []), 'もう一度押すと開く');

    // ③ 入れ子
    await head('HF-A1').click();
    check(same(await hidden(), ['HF-A1本文']), 'HF-A1（H3）は自分の下だけ・次の H2 で止まる', JSON.stringify(await hidden()));
    await head('HF-A').click();
    await head('HF-A').click();
    check(same(await hidden(), ['HF-A1本文']), '外の HF-A を畳んで開いても、中の HF-A1 は畳んだまま', JSON.stringify(await hidden()));
    await head('HF-A1').click();

    // ④ 節の最初の見出し
    await head('HF-B').click();
    check(same(await hidden(), ['HF-B本文', 'HF-B表', 'HF-資料']), '節の最初の見出し HF-B は、節の残りと後ろの折りたたみも HF-C の手前まで隠す',
      JSON.stringify(await hidden()));
    await head('HF-B').click();
    await head('HF-C1').click();
    check(same(await hidden(), ['HF-C1本文', 'HF-C本文']), '節の最初の H3 も、節の後ろの続きを次の見出しまで隠す', JSON.stringify(await hidden()));
    await head('HF-C1').click();

    // ⑤ 上の段で止まる
    await head('HF-C').click();
    check(same(await hidden(), ['HF-C1', 'HF-C1本文', 'HF-C本文']), 'HF-C（H2）は上の段の H1（第二章）で止まる', JSON.stringify(await hidden()));

    await head('HF-D1').click();
    check(same(await hidden(), ['HF-C1', 'HF-C1本文', 'HF-C本文', 'HF-D1本文']), '節の中の H3（HF-D1）は、すぐ後ろの H2（HF-D2）で止まる',
      JSON.stringify(await hidden()));
    await head('HF-D1').click();

    // ⑥ 編集モード
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 5000 }).catch(() => {});
    await page.waitForTimeout(500);
    check(await page.evaluate(() => document.body.hasAttribute('edit-mode')), '編集モードに入れる');
    check(same(await hidden(), []), '編集モードでは畳んだところも全部見える', JSON.stringify(await hidden()));
    const preview = await page.locator('#w-html-preview').inputValue();
    check(preview.includes('HF-C本文') && !/w-fold|w-heading-folded|w-foldable/.test(preview), '書き出し（保存されるもの）に畳む印が入らない');

    // ⑦ 閲覧へ戻ると畳んだ形
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForFunction(() => !document.body.hasAttribute('edit-mode'), null, { timeout: 5000 }).catch(() => {});
    await page.waitForTimeout(500);
    check(same(await hidden(), ['HF-C1', 'HF-C1本文', 'HF-C本文']), '閲覧へ戻ると畳んだ形に戻る', JSON.stringify(await hidden()));
  };
  try {
    await run();
  } finally {
    if (id) {
      await deletePage(page, id);
      const gone = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).status, id);
      check(gone === 404, '作ったページを消した', 'status ' + gone);
    }
  }
  check(errs.length === 0, 'ページのエラーが無い', errs.join(' / '));
  await browser.close();
  console.log(bad ? `✗ ${bad} 件` : '✓ すべて合格');
  process.exit(bad ? 1 : 0);
})();
