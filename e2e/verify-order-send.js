// 部材発注書の「送る」欄と、行ごとの発注済みの印を**ブラウザから**確かめる（2026-09-22）。
//
// ⚠ **この一週間で2度、API を叩くだけの確認では気づけない壊れ方が出ました**
// （空の発注部材表が残る／画面が `into` を送っていない）。どちらも Playwright で
// **押して**初めて分かりました。⚠ **画面が値を送っているかは、画面からしか
// 確かめられません。**
//
// 見るのは4つ:
//   ① 部材発注書ページに送信の欄が出る（メール・FAX・手渡し）
//   ② 行の先頭のチェックで選び、表の下の「✓ 発注済」を押すと、本文の `状態` が変わる（2026-10-21 夕から——表の中にボタンを置かない）
//   ③ 「✕ 取消」を押した行は、まとめ書きに巻き込まれない
//   ④ 部材発注ページのリンクの「まだ発注していません」が、出したあと消える
//
// ⚠ **当て先は自分で作り、最後に消します**（2026-09-23）。09-22 までは `部材発注` の下から
// 実物の部材発注書を探して**その行の印を書き換えていました**——1枚も無い環境（自宅）では
// 「飛ばす」で終わり、あれば見本の紙が書き換わります。いまは `部材発注` の下に【E2E】の
// 部材発注書を1枚置いて確かめ、消して帰ります。2026-10-21 からは、当て先をトップ直下に置き、置き場「製造」は④で読むだけです
// （本物の置き場の子を増やさない）。
//
// 使い方: WCMS_BASE=https://localhost:8443 node verify-order-send.js
const { chromium } = require('playwright');
const { login, childrenOf, makePage, deletePage, sendWay } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

// ⚠ 2行とも `未発注` で始めます——① 1行目に「✓ 発注済」→ ③ 2行目に「✕ 取消」→
//    「手渡し」のまとめ書き、の順に押すためです。
// ⚠ **行に弊社品番を入れます**（無いページ `999999`・2026-09-28）——弊社品番の無い行を「必要部材表へ戻す」と、
//    **本物の部材発注ページの臨時部材表へ書き込まれ**、流すたびに `E2E-SEND-A` の行が残っていました。弊社品番の
//    ある行は外すだけで、ほかのページへは何も書きません（order_return.go）。
const ORDER_BODY = '<h1>【E2E】発注 テスト商店（送る）</h1>' +
  '<dl data-type="tags"><dt>部材発注書番号</dt><dd>E2E-SEND</dd>' +
  '<dt>仕入先</dt><dd>テスト商店</dd><dt>部材発注日</dt><dd>2026-09-23</dd></dl>' +
  '<table data-type="our-order-items"><caption>部材発注明細表</caption><tbody>' +
  '<tr><th>弊社品番</th><th>材質</th><th>形状</th><th>寸法</th><th>数量</th><th>単位</th><th>単価</th><th>状態</th></tr>' +
  '<tr><td>999999</td><td>E2E-SEND-A</td><td>板</td><td>t3.2</td><td>2</td><td>枚</td><td>800</td><td>未発注</td></tr>' +
  '<tr><td>999999</td><td>E2E-SEND-B</td><td>板</td><td>t1.5</td><td>1</td><td>枚</td><td>900</td><td>未発注</td></tr>' +
  '</tbody></table>';

// statusesOf は本文の部材発注明細表から `状態` の列を読みます（鏡の足元の行は除く）。
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

  // 置き場は `製造`（題が機能・2026-10-19 から。それまでは `部材発注`——未発注の部材発注書の一覧はいまここ）。④で読むだけで、当て先はトップ直下に置きます。
  const top = await childrenOf(page, '000000');
  const box = top.find((c) => (c.Title || c.title) === '製造');
  const boxID = box ? String(box.ID || box.id) : '';
  if (!boxID) console.log('「製造」ページが無いので、④は飛ばします');

  const found = await makePage(page, ORDER_BODY, '000000');
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
      ['[data-mail-compose="部材発注書"]', 'メールの送る欄（部品・2026-09-30）'],
      ['[data-fax-purpose="部材発注書"]', 'w-cms の FAX で送る（2026-10-25 夜）'],
      ['[data-order-sent="FAX"]', '別の FAX 機で送った'],
      ['[data-order-sent="手渡し"]', '手渡した'],
      ['.send-way-pick', '送り方の欄（2026-10-26）'],
      ['.send-way-go', '「▶ 送る」（2026-10-26）'],
      ['.order-pick', '行の先頭のチェック'],
      ['.order-bar .order-sel-act', '表の下の行の操作'],
    ]) {
      if ((await page.locator(sel).count()) === 0) {
        console.log('✗ ' + name + ' が出ていません（' + sel + '）');
        bad++;
      }
    }
    if (bad === 0) console.log('✓ 送信の欄と行ごとのボタンが出ている');

    const before = await statusesOf(page);
    if (!before || before.length < 2) {
      console.log('✗ 部材発注明細表に2行あるはずです: ' + JSON.stringify(before));
      bad++;
    } else {
      console.log('  いまの状態: ' + JSON.stringify(before));

      // act は、rows（1始まり）の行を選んで、表の下の操作 name を押します。押すと読み直しになるので、待ってから状態を読みます。
      const act = async (rows, name, onDialog) => {
        await page.goto(BASE + '/' + found);
        await page.waitForSelector('#w-editor-content .order-pick', { timeout: 8000 }).catch(() => {});
        for (const r of rows) await page.locator('#w-editor-content .order-pick[data-row="' + r + '"]').check();
        // ⚠ 確かめの窓が出なかったときに、待ち受けを次の操作まで残さない（残すと、次の窓に二重に応えて落ちる）。
        if (onDialog) page.once('dialog', onDialog);
        await Promise.all([page.waitForNavigation({ timeout: 8000 }).catch(() => {}),
          page.click('#w-editor-content .order-sel-act[data-act="' + name + '"]')]);
        if (onDialog) page.off('dialog', onDialog);
        await page.waitForTimeout(500);
        return statusesOf(page);
      };

      // ② 1行目に「✓ 発注済」
      const after = await act([1], 'sent');
      if (!after || after[0] !== '発注済') {
        console.log('✗ 1行目が発注済になっていません: ' + JSON.stringify(after));
        bad++;
      } else console.log('✓ 選んだ行の「発注済」が本文に入る');

      // ③ 2行目を取消（未発注からなので確認は出ない）→ 手渡しのまとめ書きに巻き込まれないこと
      let askedCancel = '';
      await act([2], 'cancel', (d) => { askedCancel = d.message(); d.accept(); });
      if (askedCancel) {
        console.log('✗ 発注前の行の取消で確認が出ました: ' + JSON.stringify(askedCancel));
        bad++;
      }
      await page.goto(BASE + '/' + found);
      // 送り方の欄で「手渡し」を選んで「✓ 記録する」（2026-10-26 から、いままでのボタンは隠してある）。
      page.once('dialog', (d) => d.accept());
      await Promise.all([page.waitForNavigation({ timeout: 8000 }).catch(() => {}),
        sendWay(page, '[data-order-sent="手渡し"]')]);
      await page.waitForTimeout(800);
      const done = await statusesOf(page);
      if (!done || done[1] !== '取消') {
        console.log('✗ ⚠ まとめ書きが取消の行を巻き込んでいます: ' + JSON.stringify(done));
        bad++;
      } else console.log('✓ まとめ書きが取消を巻き込まない');

      // ⑤ 1行目（発注済）を「↩ 発注から外す」（2026-10-21 までの名前は「必要部材表へ戻す」）——確認に「再送」が出て、行が本文から消えること。
      //    （2026-10-21 夕から、行の操作は閲覧モードでも書ける形でも出る——【要求】製造 §8。編集モードで隠れることは見ない）
      let asked = '';
      const left = await act([1], 'return', (d) => { asked = d.message(); d.accept(); });
      if (!asked.includes('再送')) {
        console.log('✗ 送付後の行を外す確認に「再送」がありません: ' + JSON.stringify(asked));
        bad++;
      }
      if (!left || left.length !== 1 || left[0] !== '取消') {
        console.log('✗ 外した行が本文から消えていません: ' + JSON.stringify(left));
        bad++;
      } else console.log('✓ 「↩ 発注から外す」で行が消える（確認に再送の案内）');
    }

    // ④ 部材発注ページのリンク——「まだ発注していません」が**本文に焼き込まれていない**こと
    //
    // ⚠ **鏡が出ているかで見ます**（文字の有無ではなく）。まだ出していない部材発注書が
    //    他にもあれば、その文は**正しく**残るからです。
    if (boxID) {
      await page.goto(BASE + '/' + boxID);
      await page.waitForTimeout(600);
      const n = await page.locator('section[data-type="our-order-link"]').count();
      if (n === 0) {
        console.log('飛ばします: 部材発注ページに部材発注書へのリンクがありません');
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
