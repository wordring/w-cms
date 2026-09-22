// 発注部材表の「入れる先」が本当に効くかを、**ブラウザから**確かめる（2026-09-22）。
//
// ⚠ **これは実機の失敗から生まれた番人です。** ユーザー報告:「**一枚目の発注部材表に
// 入れるを選択しても別の部材表が出来ます**」——**サーバーの口は正しく、画面が `into` を
// 送っていませんでした**。Go の試験は全部緑で、API を直に叩く確認も通っていました。
//
// ⚠ **画面が値を送っているかは、画面からしか確かめられません。**
//
// ⚠ **未手配の行は自分で作ります**（2026-09-23）。加工製品ページ（材料表2行）と
// 受注ページ（受注明細1行・`品番` で結ぶ）を置くと、`発注` の未手配の一覧に**その2行**が
// 出ます。09-22 までは環境の実データ頼みで、自宅では「未手配の行がありません」で
// 終わっていました。押すのは**自分の行だけ**（`data-product` で選ぶ）で、足す先も
// **自分が作った表**です——職場の実データの行を発注部材表へ入れて帰らないため。
// 最後に2枚とも消します（発注部材表は③で空になって消えます）。
//
// 使い方: WCMS_BASE=https://localhost:8443 node verify-order-draft.js
//         （発注部材表を置けるページを WCMS_PAGE で指す。既定は「発注」を探す）
const { chromium } = require('playwright');
const { login, childrenOf, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

// ⚠ 品番は走るたびに変えます——前の失敗で消し残った加工製品と同じ品番だと、
//    「2枚に当たるので結ばない」（`productByCode`）で未手配に出なくなります。
const CODE = 'E2E-DRAFT-' + Date.now().toString(36).toUpperCase();
const PRODUCT_BODY = '<h1>【E2E】加工製品（発注部材表）</h1>' +
  '<dl data-type="tags"><dt>部品番号</dt><dd>' + CODE + '</dd></dl>' +
  '<section><h2>材料</h2><table><tbody>' +
  '<tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th><th>備考</th></tr>' +
  '<tr><td>E2E-DRAFT-A</td><td>板</td><td>t3.2</td><td>2</td><td></td></tr>' +
  '<tr><td>E2E-DRAFT-B</td><td>板</td><td>t1.5</td><td>1</td><td></td></tr>' +
  '</tbody></table></section>';
const ORDER_BODY = '<h1>【E2E】受注 テスト商店（発注部材表）</h1>' +
  '<dl data-type="tags"><dt>発注書番号</dt><dd>E2E-DRAFT-PO</dd>' +
  '<dt>発注元</dt><dd>テスト商店</dd><dt>発注日</dt><dd>2026-09-23</dd></dl>' +
  '<table data-type="client-order-items"><caption>受注明細</caption><tbody>' +
  '<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>状態</th></tr>' +
  '<tr><td></td><td>' + CODE + '</td><td>E2E ブラケット</td><td>2</td><td>未着手</td></tr>' +
  '</tbody></table>';

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(e.message));
  await login(page, BASE);

  // ⚠ **当て先は焼き込みません**——トップ直下で題が「発注」のページを探します。
  let target = process.env.WCMS_PAGE || '';
  if (target) {
    const m = /(\d{6})\s*$/.exec(target);
    target = m ? '/' + m[1] : '';
  }
  if (!target) {
    const hit = (await childrenOf(page, '000000')).find((c) => (c.Title || c.title) === '発注');
    if (hit) target = '/' + (hit.ID || hit.id);
  }
  if (!target) {
    console.log('飛ばします: 「発注」ページが見つかりません（まだ作っていないだけかもしれません）');
    await browser.close();
    return;
  }

  // 未手配の行の元を作る（加工製品 → 受注明細の順は問わない・索引は保存のたびに入る）。
  const ids = [];
  const productID = await makePage(page, PRODUCT_BODY);
  ids.push(productID);
  const orderID = await makePage(page, ORDER_BODY);
  ids.push(orderID);

  let bad = 0;
  let skipped = false;
  // ⚠ 本体は関数にして、途中で `return` しても**後片付け（finally）と締めの行**が
  //    必ず走るようにします。
  const run = async () => {
    if (!productID || !orderID) {
      console.log('✗ 当て先（加工製品・受注）を作れません');
      bad++;
      return;
    }
    await page.goto(BASE + target);
    await page.waitForTimeout(800);
    if (await page.locator('[data-unorder-draft]').count() === 0) {
      console.log('飛ばします: ' + target + ' に未手配の一覧がありません（既にある箱には新しい作業面が入らない）');
      skipped = true;
      return;
    }
    // ⚠ **自分の行だけを押します**（実データの行を発注部材表へ入れて帰らない）。
    const mine = '.unorder-table tr[data-product="' + productID + '"] .unorder-check';
    const rows = await page.locator(mine).count();
    if (rows !== 2) {
      console.log('✗ 自分の未手配の行が2行出るはずです（' + rows + ' 行・加工製品 /' + productID + '）');
      bad++;
      return;
    }
    console.log('当て先: ' + target + '（自分の未手配 ' + rows + ' 行）');

    const tables = () => page.locator('table[data-type="order-draft"]').count();
    const before = await tables();

    // ① 1枚作る
    await page.locator(mine).first().check();
    await page.locator('[data-unorder-draft]').click();
    await page.waitForTimeout(1500);
    const afterNew = await tables();
    if (afterNew !== before + 1) {
      console.log('✗ 「新しく作る」で表が増えていません（' + before + ' → ' + afterNew + '）');
      bad++;
      return;
    }
    console.log('✓ 新しく作る → 表が1枚増えた');

    // ② 「◯枚目へ足す」（＝いま作った表）を選んで押す —— ⚠ **ここが実機で壊れていた**
    if (await page.locator(mine).count() === 0) {
      console.log('✗ 2行目が未手配に残っているはずです');
      bad++;
    } else {
      const sel = page.locator('[data-unorder="into"]');
      const opts = await sel.locator('option').allTextContents();
      if (!opts.some((o) => o.includes('枚目の発注部材表へ足す'))) {
        console.log('✗ 「◯枚目へ足す」の選択肢が出ていません: ' + JSON.stringify(opts));
        bad++;
      } else {
        await sel.selectOption(String(afterNew));
        await page.locator(mine).first().check();
        await page.locator('[data-unorder-draft]').click();
        await page.waitForTimeout(1500);
        const afterInto = await tables();
        if (afterInto !== afterNew) {
          console.log('✗ 「' + afterNew + '枚目へ足す」なのに表が増えました（' + afterNew + ' → ' +
            afterInto + '）。⚠ 画面が into を送っていませんか');
          bad++;
        } else console.log('✓ ' + afterNew + '枚目へ足す → 表は増えない（行が足される）');
      }
    }

    // ③ 戻すと、最後の1行で表ごと消える（⚠ 押すのは自分の表の「戻す」だけ）
    const back = '.draft-row-back[data-draft-table="' + afterNew + '"]';
    const backs = await page.locator(back).count();
    if (backs === 0) {
      console.log('✗ 「戻す」ボタンが出ていません');
      bad++;
    } else {
      for (let i = 0; i < backs; i++) {
        const b = page.locator(back).first();
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
  };
  try {
    await run();
  } finally {
    for (const id of ids) await deletePage(page, id);
  }

  if (errs.length) {
    console.log('✗ JSエラー: ' + errs.join(' / '));
    bad++;
  } else console.log('✓ JSエラーなし');

  console.log(skipped ? '\n結果: 飛ばした' : bad === 0 ? '\n結果: 合格' : '\n結果: ' + bad + '件の不合格');
  await browser.close();
  process.exitCode = bad === 0 ? 0 : 1;
})();
