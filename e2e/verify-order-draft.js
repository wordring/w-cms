// 発注部材表の「入れる先」が本当に効くかを、**ブラウザから**確かめる（2026-09-22）。
//
// ⚠ **これは実機の失敗から生まれた番人です。** ユーザー報告:「**一枚目の発注部材表に
// 入れるを選択しても別の部材表が出来ます**」——**サーバーの口は正しく、画面が `into` を
// 送っていませんでした**。Go の試験は全部緑で、API を直に叩く確認も通っていました。
//
// ⚠ **画面が値を送っているかは、画面からしか確かめられません。**
//
// 使い方: WCMS_BASE=https://localhost:8443 node verify-order-draft.js
//         （発注部材表を置けるページを WCMS_PAGE で指す。既定は「発注」を探す）
const { chromium } = require('playwright');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(e.message));

  await page.goto(BASE + '/login');
  await page.fill('#username', 'a');
  await page.fill('#password', 'a');
  await page.click('button[type=submit]');
  await page.waitForLoadState('networkidle');

  // ⚠ **当て先は焼き込みません**——トップ直下で題が「発注」のページを探します。
  let target = process.env.WCMS_PAGE || '';
  if (target) {
    const m = /(\d{6})\s*$/.exec(target);
    target = m ? '/' + m[1] : '';
  }
  if (!target) {
    const kids = await page.evaluate(async () => {
      const r = await fetch('/api/children?parent_id=000000', { credentials: 'same-origin' });
      const d = await r.json();
      return Array.isArray(d) ? d.map((c) => [c.ID || c.id, c.Title || c.title]) : [];
    });
    const hit = kids.find((k) => k[1] === '発注');
    if (hit) target = '/' + hit[0];
  }
  if (!target) {
    console.log('飛ばします: 「発注」ページが見つかりません（まだ作っていないだけかもしれません）');
    await browser.close();
    return;
  }

  await page.goto(BASE + target);
  await page.waitForTimeout(600);
  if (await page.locator('[data-unorder-draft]').count() === 0) {
    console.log('飛ばします: ' + target + ' に未手配の一覧がありません');
    await browser.close();
    return;
  }
  const rows = await page.locator('.unorder-check').count();
  if (rows === 0) {
    console.log('飛ばします: 未手配の行がありません（全部手配済みかもしれません）');
    await browser.close();
    return;
  }
  console.log('当て先: ' + target + '（未手配 ' + rows + ' 行）');

  let bad = 0;
  const tables = () => page.locator('table[data-type="order-draft"]').count();
  const before = await tables();

  // ① 1枚作る
  await page.locator('.unorder-check').first().check();
  await page.locator('[data-unorder-draft]').click();
  await page.waitForTimeout(1500);
  const afterNew = await tables();
  if (afterNew !== before + 1) {
    console.log('✗ 「新しく作る」で表が増えていません（' + before + ' → ' + afterNew + '）');
    bad++;
  } else console.log('✓ 新しく作る → 表が1枚増えた');

  // ② 「1枚目へ足す」を選んで押す —— ⚠ **ここが実機で壊れていた**
  if (await page.locator('.unorder-check').count() === 0) {
    console.log('飛ばします: 足す行が残っていません');
  } else {
    const sel = page.locator('[data-unorder="into"]');
    const opts = await sel.locator('option').allTextContents();
    if (!opts.some((o) => o.includes('枚目の発注部材表へ足す'))) {
      console.log('✗ 「◯枚目へ足す」の選択肢が出ていません: ' + JSON.stringify(opts));
      bad++;
    } else {
      await sel.selectOption('1');
      await page.locator('.unorder-check').first().check();
      await page.locator('[data-unorder-draft]').click();
      await page.waitForTimeout(1500);
      const afterInto = await tables();
      if (afterInto !== afterNew) {
        console.log('✗ 「1枚目へ足す」なのに表が増えました（' + afterNew + ' → ' + afterInto +
          '）。⚠ 画面が into を送っていませんか');
        bad++;
      } else console.log('✓ 1枚目へ足す → 表は増えない（行が足される）');
    }
  }

  // ③ 戻すと、最後の1行で表ごと消える
  const backs = await page.locator('.draft-row-back').count();
  if (backs === 0) {
    console.log('✗ 「戻す」ボタンが出ていません');
    bad++;
  } else {
    for (let i = 0; i < backs; i++) {
      const b = page.locator('.draft-row-back').first();
      if (await b.count() === 0) break;
      await b.click();
      await page.waitForTimeout(1200);
    }
    const left = await tables();
    if (left >= afterNew) {
      console.log('✗ 全部戻したのに表が残っています（' + left + ' 枚）');
      bad++;
    } else console.log('✓ 全部戻す → 空の表は残らない（' + left + ' 枚）');
  }

  if (errs.length) {
    console.log('✗ JSエラー: ' + errs.join(' / '));
    bad++;
  } else console.log('✓ JSエラーなし');

  console.log(bad === 0 ? '\n結果: 合格' : '\n結果: ' + bad + '件の不合格');
  await browser.close();
  process.exitCode = bad === 0 ? 0 : 1;
})();
