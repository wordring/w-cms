// 発注書の「送る」欄と、行ごとの発注済みの印を**ブラウザから**確かめる（2026-09-22）。
//
// ⚠ **この一週間で2度、API を叩くだけの確認では気づけない壊れ方が出ました**
// （空の発注部材表が残る／画面が `into` を送っていない）。どちらも Playwright で
// **押して**初めて分かりました。⚠ **画面が値を送っているかは、画面からしか
// 確かめられません。**
//
// 見るのは4つ:
//   ① 発注書ページに送信の欄が出る（メール・FAX・手渡し）
//   ② 行ごとの「✓ 発注済」を押すと、本文の `状態` が変わる
//   ③ 「✕ 取消」を押した行は、まとめ書きに巻き込まれない
//   ④ 発注ページのリンクの「まだ発注していません」が、出したあと消える
//
// ⚠ **当て先は自分で作り、最後に消します**（2026-09-23）。09-22 までは `発注` の下から
// 実物の発注書を探して**その行の印を書き換えていました**——1枚も無い環境（自宅）では
// 「飛ばす」で終わり、あれば見本の紙が書き換わります。いまは `発注` の下に【E2E】の
// 発注書を1枚置いて確かめ、消して帰ります（`発注` が無ければトップ直下・④は飛ばす）。
//
// 使い方: WCMS_BASE=https://localhost:8443 node verify-order-send.js
const { chromium } = require('playwright');
const { login, childrenOf, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

// ⚠ 2行とも `未発注` で始めます——① 1行目に「✓ 発注済」→ ③ 2行目に「✕ 取消」→
//    「手渡し」のまとめ書き、の順に押すためです。
const ORDER_BODY = '<h1>【E2E】発注 テスト商店（送る）</h1>' +
  '<dl data-type="tags"><dt>発注書番号</dt><dd>E2E-SEND</dd>' +
  '<dt>仕入先</dt><dd>テスト商店</dd><dt>発注日</dt><dd>2026-09-23</dd></dl>' +
  '<table data-type="our-order-items"><caption>発注明細</caption><tbody>' +
  '<tr><th>材質</th><th>形状</th><th>寸法</th><th>数量</th><th>単位</th><th>単価</th><th>状態</th></tr>' +
  '<tr><td>E2E-SEND-A</td><td>板</td><td>t3.2</td><td>2</td><td>枚</td><td>800</td><td>未発注</td></tr>' +
  '<tr><td>E2E-SEND-B</td><td>板</td><td>t1.5</td><td>1</td><td>枚</td><td>900</td><td>未発注</td></tr>' +
  '</tbody></table>';

// statusesOf は本文の発注明細から `状態` の列を読みます（鏡の足元の行は除く）。
async function statusesOf(page) {
  return page.evaluate(() => {
    const t = document.querySelector('table[data-type="our-order-items"]');
    if (!t) return null;
    const rows = [...t.querySelectorAll('tr')].filter((r) => !r.closest('tfoot'));
    if (rows.length < 2) return [];
    const head = [...rows[0].children].map((c) => c.textContent.trim());
    const i = head.indexOf('状態');
    if (i < 0) return [];
    return rows.slice(1).map((r) => (r.children[i] ? r.children[i].textContent.trim() : ''));
  });
}

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(e.message));
  await login(page, BASE);

  // 置き場は `発注`（題が機能）。無ければトップ直下に置き、④だけ飛ばします。
  const top = await childrenOf(page, '000000');
  const box = top.find((c) => (c.Title || c.title) === '発注');
  const boxID = box ? String(box.ID || box.id) : '';
  if (!boxID) console.log('「発注」ページが無いので、当て先はトップ直下に置きます（④は飛ばします）');

  const found = await makePage(page, ORDER_BODY, boxID || '000000');
  if (!found) {
    console.log('✗ 当て先を作れません');
    await browser.close();
    process.exit(1);
  }
  console.log('当て先: /' + found + '（作って、最後に消します）');

  let bad = 0;
  try {
    await page.goto(BASE + '/' + found);
    await page.waitForTimeout(700);

    // ① 送信の欄
    for (const [sel, name] of [
      ['[data-order-send]', 'メールの送信ボタン'],
      ['[data-order-sent="FAX"]', 'FAXで送った'],
      ['[data-order-sent="手渡し"]', '手渡した'],
      ['.order-row-set', '行ごとの印のボタン'],
    ]) {
      if ((await page.locator(sel).count()) === 0) {
        console.log('✗ ' + name + ' が出ていません（' + sel + '）');
        bad++;
      }
    }
    if (bad === 0) console.log('✓ 送信の欄と行ごとのボタンが出ている');

    const before = await statusesOf(page);
    if (!before || before.length < 2) {
      console.log('✗ 発注明細に2行あるはずです: ' + JSON.stringify(before));
      bad++;
    } else {
      console.log('  いまの状態: ' + JSON.stringify(before));

      // ② 1行目に「✓ 発注済」
      const setBtn = page.locator('.order-row-set[data-order-row="1"][data-order-status="発注済"]');
      if ((await setBtn.count()) === 0) {
        console.log('✗ 1行目に「✓ 発注済」が出ていません（' + before[0] + '）');
        bad++;
      } else {
        await setBtn.first().click();
        await page.waitForTimeout(1500);
        const after = await statusesOf(page);
        if (!after || after[0] !== '発注済') {
          console.log('✗ 1行目が発注済になっていません: ' + JSON.stringify(after));
          bad++;
        } else console.log('✓ 行ごとの「発注済」が本文に入る');

        // ③ 2行目を取消（未発注からなので確認は出ない）→ 手渡しのまとめ書きに
        //    巻き込まれないこと
        const cancel = page.locator('.order-row-set[data-order-row="2"][data-order-status="取消"]');
        if ((await cancel.count()) === 0) {
          console.log('✗ 2行目に「✕ 取消」が出ていません');
          bad++;
        } else {
          await cancel.first().click();
          await page.waitForTimeout(1500);
          page.once('dialog', (d) => d.accept());
          await page.locator('[data-order-sent="手渡し"]').first().click();
          await page.waitForTimeout(1800);
          const done = await statusesOf(page);
          if (!done || done[1] !== '取消') {
            console.log('✗ ⚠ まとめ書きが取消の行を巻き込んでいます: ' + JSON.stringify(done));
            bad++;
          } else console.log('✓ まとめ書きが取消を巻き込まない');
        }
      }
    }

    // ④ 発注ページのリンク——「まだ発注していません」が**本文に焼き込まれていない**こと
    //
    // ⚠ **鏡が出ているかで見ます**（文字の有無ではなく）。まだ出していない発注書が
    //    他にもあれば、その文は**正しく**残るからです。
    if (boxID) {
      await page.goto(BASE + '/' + boxID);
      await page.waitForTimeout(600);
      const n = await page.locator('section[data-type="our-order-link"]').count();
      if (n === 0) {
        console.log('飛ばします: 発注ページに発注書へのリンクがありません');
      } else if ((await page.locator('.order-link-state').count()) !== n) {
        console.log('✗ ⚠ リンクの進み具合が鏡になっていません（本文に焼き込まれている？）');
        bad++;
      } else console.log('✓ リンクの進み具合が鏡で出ている（' + n + '本）');
    }
  } finally {
    await deletePage(page, found);
  }

  if (errs.length) {
    console.log('✗ JavaScript エラー: ' + JSON.stringify(errs));
    bad++;
  }
  console.log(bad === 0 ? '結果: すべて通りました' : '結果: ' + bad + ' 件の失敗');
  await browser.close();
  process.exit(bad === 0 ? 0 : 1);
})();
