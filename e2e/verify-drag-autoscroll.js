// ブロックを動かしているあいだ、画面の端でページを送る（2026-10-27・assets/app.js の setupDragAutoScroll）を画面から確かめる。
//
// 利用者:「ブロックを移動するとき、画面に表示されている範囲外に移動させることが出来ません。可能にする方法はありませんか？」
//
//   ① ブロックの ⠿ を掴んで画面の下の端に置いておくと、ページが下へ送られる
//   ② そのまま、はじめは画面の外にあったブロックの後ろへ落とせる
//   ③ 画面の上の端（固定の帯の下）に置いておくと、ページが上へ送られる
//
// 作ったページは最後に消します。
// 使い方: WCMS_BASE=https://localhost:9443 node verify-drag-autoscroll.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 800 } });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let id = '';
  try {
    await login(page, BASE);
    let body = '<h1>【E2E】ブロックを遠くへ動かす</h1>';
    for (let i = 0; i < 60; i++) body += '<p>段落' + i + '</p>';
    id = await makePage(page, body);
    await page.goto(BASE + '/' + id + '?edit');
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 10000 });
    await page.waitForTimeout(800);
    const order = () => page.evaluate(() => [...document.querySelectorAll('#w-editor-content p')].map((p) => p.textContent));

    // ①② 段落0 を掴んで、画面の下の端に置いておく。
    const block = page.locator('#w-editor-content .editor-block').filter({ has: page.locator('p', { hasText: /^段落0$/ }) }).first();
    await block.hover();
    const handle = block.locator(':scope > .block-controls .drag-handle');
    const hb = await handle.boundingBox();
    const y0 = await page.evaluate(() => window.scrollY);
    await page.mouse.move(hb.x + hb.width / 2, hb.y + hb.height / 2);
    await page.mouse.down();
    await page.mouse.move(hb.x + hb.width / 2 + 5, hb.y + 40, { steps: 5 });
    const x = 640;
    // 下の端（800 の 10 px 手前）で少しずつ動かす——ドラッグの知らせ（dragover）を届かせる。長く置くとページの終わりまで送り、
    // 画面の真ん中がブロックの無い余白になるので、10回（約 0.8 秒）で止める。
    for (let i = 0; i < 10; i++) {
      await page.mouse.move(x + (i % 2), 790, { steps: 1 });
      await page.waitForTimeout(80);
    }
    const y1 = await page.evaluate(() => window.scrollY);
    check('① 画面の下の端に置くと、ページが下へ送られる', y1 > y0 + 500, y0 + ' → ' + y1);
    // 画面の真ん中あたりのブロックの上で離す。
    await page.mouse.move(x, 400, { steps: 4 });
    await page.mouse.move(x, 402, { steps: 1 });
    await page.mouse.up();
    await page.waitForTimeout(500);
    const after = await order();
    const at = after.indexOf('段落0');
    console.log('    並び（頭の5つ）: ' + after.slice(0, 5).join(' ') + '・段落0 の場所 ' + at + '・全部 ' + after.length);
    check('② はじめは画面の外にあったブロックのそばへ落とせた', at > 15, '段落0 は ' + at + ' 番目');

    // ③ 上の端へ——先にページを下へ送ってから、画面の真ん中のブロックを掴む。
    await page.evaluate(() => window.scrollTo(0, 1500));
    await page.waitForTimeout(300);
    const mid = await page.evaluate(() => {
      const el = document.elementFromPoint(640, 450);
      const p = el && el.closest('.editor-block') && el.closest('.editor-block').querySelector('p');
      return p ? p.textContent : '';
    });
    const b2 = page.locator('#w-editor-content .editor-block').filter({ has: page.locator('p', { hasText: new RegExp('^' + mid + '$') }) }).first();
    await b2.hover();
    const h2 = await b2.locator(':scope > .block-controls .drag-handle').boundingBox();
    const y2 = await page.evaluate(() => window.scrollY);
    await page.mouse.move(h2.x + h2.width / 2, h2.y + h2.height / 2);
    await page.mouse.down();
    await page.mouse.move(h2.x + h2.width / 2 + 5, h2.y - 40, { steps: 5 });
    const top = await page.evaluate(() => {
      let edge = 0;
      for (const el of document.elementsFromPoint(innerWidth / 2, 2)) {
        const st = getComputedStyle(el).position;
        if (st === 'fixed' || st === 'sticky') edge = Math.max(edge, el.getBoundingClientRect().bottom);
      }
      return edge;
    });
    for (let i = 0; i < 25; i++) {
      await page.mouse.move(x + (i % 2), top + 8, { steps: 1 });
      await page.waitForTimeout(80);
    }
    const y3 = await page.evaluate(() => window.scrollY);
    await page.keyboard.press('Escape');
    await page.mouse.up();
    check('③ 画面の上の端（固定の帯の下）に置くと、ページが上へ送られる', y3 < y2 - 300, y2 + ' → ' + y3 + '（帯の下の端 ' + Math.round(top) + '）');
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    if (id) await deletePage(page, id).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
