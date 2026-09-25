// 表の列の型と選択肢を、エディタが**設定の語彙から**引いていることを確かめる（2026-09-26）。
//
// DBの日本語化 4-2。それまでエディタは**登録（表の種類の列の宣言）**から引いていて、
// 表の写し（`data/tables.db`・サーバーの `ColumnWord`）とは別の正本を読んでいました。
// ⚠ 実際に5か所ずれていて、**受注明細の `最短納期` が日付でないとして薄赤**になっていました
// （登録は `date` のまま・語彙は 09-23 から `text`）。
//
// 見るのは3つ——どれも**画面からしか確かめられません**（Go の試験は配る辞書までしか見ない）:
//   ① 受注明細の `納期` に `最短納期` と書いても薄赤にならない（語彙は `text`）
//   ② 受注明細の `単位` の選択肢が6つ（語彙の既定。登録は2つだった）
//   ③ `状態` は**表ごとの例外が先**——発注明細は `未発注…`、受注明細は既定の `未着手…`
//
// ⚠ **これは「画面に正しい答えが出るか」の番人で、「どちらの正本を読んでいるか」は
// 見分けません。** 4-2 と同時に登録を語彙へ合わせたので（`TestRegistryAgreesWithVocabulary`
// が以後ずれを止める）、**4-2 前の app.js でも通ります**——2つの正本が同じ答えを返すため。
// 読む先は 2026-09-26 に一度だけ別の方法で確かめました: 設定の `単位` に選択肢を1つ足して
// DB再構築（設定の読み直し）すると、新しい app.js は7つ、4-2 前は6つ（登録）を出しました。
//
// 当て先は自分で作って最後に消します（e2e/lib.js の makePage／deletePage）。
//
// 使い方: WCMS_BASE=https://localhost:8443 node verify-column-word.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

const BODY = '<h1>【E2E】列の語彙</h1>' +
  '<table><caption>受注明細</caption><tbody>' +
  '<tr><th>品番</th><th>数量</th><th>単位</th><th>納期</th><th>状態</th></tr>' +
  '<tr><td>E2E-CW-1</td><td>2</td><td>個</td><td>最短納期</td><td>未着手</td></tr>' +
  '</tbody></table>' +
  '<table><caption>発注明細</caption><tbody>' +
  '<tr><th>材質</th><th>数量</th><th>状態</th></tr>' +
  '<tr><td>E2E-CW-SS400</td><td>1</td><td>未発注</td></tr>' +
  '</tbody></table>';

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 900 }, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(e.message));
  await login(page, BASE);

  let bad = 0;
  const say = (ok, msg) => { console.log((ok ? '  ✓ ' : '  ✗ ') + msg); if (!ok) bad++; };

  const id = await makePage(page, BODY);
  if (!id) { console.log('✗ 当て先を作れません'); await browser.close(); process.exit(1); }
  console.log('当て先: /' + id + '（作って、最後に消します）');

  // cellOf は caption の表の、見出し label の列の1行目のセルを指す locator 用のセレクタを返します。
  const cellSel = (caption, col) => page.evaluate(({ caption, col }) => {
    const t = [...document.querySelectorAll('#w-editor-content table')]
      .find((x) => { const c = x.querySelector(':scope > caption'); return c && c.textContent.trim() === caption; });
    if (!t) return null;
    const head = [...t.rows[0].children].map((c) => c.textContent.trim());
    const i = head.indexOf(col);
    if (i < 0) return null;
    t.rows[1].children[i].setAttribute('data-e2e', caption + '-' + col);
    return '[data-e2e="' + caption + '-' + col + '"]';
  }, { caption, col });

  // menuFor は、そのセルにキャレットを置いたときに出る選択肢を返します。
  const menuFor = async (caption, col) => {
    const sel = await cellSel(caption, col);
    if (!sel) return null;
    await page.click(sel);
    await page.waitForTimeout(400);
    return page.evaluate(() => {
      const m = document.getElementById('w-enum-menu');
      if (!m || !m.classList.contains('active')) return [];
      return [...m.querySelectorAll('button')].map((b) => b.textContent.trim());
    });
  };

  try {
    await page.goto(BASE + '/' + id);
    await page.waitForTimeout(1200);

    // ① 閲覧モードで印が付く（validateTypedTables は閲覧でも走る）
    const dueSel = await cellSel('受注明細', '納期');
    const dueBad = dueSel && await page.$eval(dueSel, (c) => c.classList.contains('cell-invalid'));
    say(dueSel && !dueBad, '受注明細の「最短納期」が薄赤にならない（納期は text）');
    const qtySel = await cellSel('受注明細', '数量');
    const qtyKnown = qtySel && await page.$eval(qtySel, (c) => c.classList.contains('cell-known'));
    say(!!qtyKnown, '数量は読めた印が付く（型の検査そのものは効いている）');

    // ② ③ 選択肢は編集モードで、キャレットを置いたときに出る
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    await page.waitForTimeout(600);

    const units = await menuFor('受注明細', '単位');
    say(units && units.length === 6 && units.includes('kg'),
      '受注明細の単位の選択肢が語彙の6つ（' + JSON.stringify(units) + '）');

    const orderState = await menuFor('受注明細', '状態');
    say(orderState && orderState.includes('未着手') && !orderState.includes('未発注'),
      '受注明細の状態は既定の選択肢（' + JSON.stringify(orderState) + '）');

    const ourState = await menuFor('発注明細', '状態');
    say(ourState && ourState.includes('未発注') && !ourState.includes('未着手'),
      '発注明細の状態は表の例外が先（' + JSON.stringify(ourState) + '）');

    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForTimeout(800);
  } finally {
    await deletePage(page, id);
  }

  if (errs.length) { console.log('✗ JSエラー: ' + errs.join(' / ')); bad++; }
  console.log(bad === 0 ? '結果: すべて通りました' : '結果: ' + bad + ' 件の失敗');
  await browser.close();
  process.exit(bad === 0 ? 0 : 1);
})();
