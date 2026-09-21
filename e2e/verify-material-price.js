// 材料の参考単価が、材料表の右に**見える幅で**出ることを確かめる（2026-09-21）
//
// ユーザー:「材料の単価は**検索して最新情報をひくべきもの**で、本来はここに
// あるべきものではありません」。中身の正しさ（どの発注を採るか・読めないページを
// 混ぜないか）は Go の試験が見ています。**ここでしか見られないのは「画面で読めるか」**です。
//
// ⚠ **これは実際に2度踏んだ罠の番人です**:
//   ① 3列（最新単価・時点・仕入先）にしたら**必ずはみ出した**——文書の欄は
//      1920px の画面でも 666px までしか広がらない（左右のレールぶん）。
//   ② 1列に畳んでも直らなかった——`validateCell`（app.js）が**クロームのセルにも**
//      `cell-atomic`（1行に保つ）を付けていて、**断り文「⚠ 買った記録がありません」の
//      1行幅（実測200px）で列が決まっていた**。値より断り文のほうが列を広げる、
//      という逆転です。**検算の `<tfoot>` も同じ経路を通っていました。**
//
// 使い方: WCMS_BASE=https://localhost:8443 node verify-material-price.js
const { chromium } = require('playwright');
const { login } = require('./lib');

const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

const MAT_BODY = '<h1>【E2E】材料の参考単価</h1>' +
  '<dl data-type="tags"><dt>部品番号</dt><dd>E2E-MATPRICE</dd></dl>' +
  '<section><h2>材料</h2><table><tbody>' +
  '<tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th><th>備考</th></tr>' +
  '<tr><td>E2E-SS400</td><td>板</td><td>t3.2</td><td>2</td><td>買った記録があるはず</td></tr>' +
  '<tr><td>E2E-SUS304</td><td>板</td><td>t1.5</td><td>4</td><td>買った記録は無いはず</td></tr>' +
  '</tbody></table></section>';

const ORDER_BODY = '<h1>【E2E】発注 テスト商店</h1>' +
  '<dl data-type="tags"><dt>発注書番号</dt><dd>E2E-PO</dd>' +
  '<dt>仕入先</dt><dd>テスト商店</dd><dt>発注日</dt><dd>2026-08-19</dd></dl>' +
  '<table data-type="our-order-items"><caption>発注明細</caption><tbody>' +
  '<tr><th>材質</th><th>形状</th><th>寸法</th><th>数量</th><th>単位</th><th>単価</th></tr>' +
  '<tr><td>E2E-SS400</td><td>板</td><td>t3.2</td><td>2</td><td>枚</td><td>800</td></tr>' +
  '</tbody></table>';

// makePage は当て先を自分で作ります（⚠ 焼き込まない・最後に消す）。
async function makePage(page, html) {
  const url = await page.evaluate(async () => {
    const res = await fetch('/api/new-page', {
      method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: 'parent=000000',
    });
    return res.url;
  });
  const id = (url.match(/\/(\d{6})/) || [])[1];
  if (!id) return '';
  await page.evaluate(async (arg) => {
    const lr = await fetch('/api/lock?id=' + arg.id, { method: 'POST' });
    const lj = await lr.json().catch(() => ({}));
    await fetch('/api/save', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ page_id: arg.id, html: arg.html, token: lj.token || '' }),
    });
    // **握ったロックは必ず外します**——残すと削除にも入れません。
    await fetch('/api/lock/force?id=' + arg.id, { method: 'POST' });
  }, { id, html });
  return id;
}

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1600, height: 900 }, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  await login(page, BASE);

  let bad = 0;
  const ids = [];
  const say = (ok, msg) => { console.log((ok ? '  OK ' : '  NG ') + msg); if (!ok) bad++; };
  try {
    // ⚠ **発注書を先に作ります**（材料ページを描くときに索引へ入っている必要がある）。
    const orderID = await makePage(page, ORDER_BODY);
    const matID = await makePage(page, MAT_BODY);
    if (!orderID || !matID) { console.log('NG ページを作れません'); process.exit(1); }
    ids.push(matID, orderID);

    await page.goto(BASE + '/' + matID);
    await page.waitForTimeout(1200); // 印が付くのを待つ

    const r = await page.evaluate(() => {
      const h = [...document.querySelectorAll('#w-editor-content h2')]
        .find((e) => e.textContent.trim() === '材料');
      if (!h) return null;
      const t = h.closest('section').querySelector('table');
      const rows = [...t.querySelectorAll('tr')];
      const head = [...rows[0].children].map((c) => c.textContent.trim());
      const lastOf = (i) => rows[i].lastElementChild;
      return {
        head,
        priceCol: head[head.length - 1],
        hit: lastOf(1).textContent.replace(/\s+/g, ' ').trim(),
        miss: lastOf(2).textContent.replace(/\s+/g, ' ').trim(),
        // ⚠ クロームのセルに本文の印が付いていないこと（今日の罠）
        atomic: [lastOf(0), lastOf(1), lastOf(2)].some((c) => c.classList.contains('cell-atomic')),
        chrome: [lastOf(0), lastOf(1), lastOf(2)].every((c) => c.classList.contains('vocab-chrome')),
        missWrap: getComputedStyle(lastOf(2)).whiteSpace,
        priceW: Math.round(lastOf(1).getBoundingClientRect().width),
        fits: t.scrollWidth <= t.clientWidth + 1,
        pageScrolls: document.documentElement.scrollWidth > document.documentElement.clientWidth,
      };
    });
    if (!r) { console.log('NG 材料の節が見つかりません'); process.exit(1); }

    say(r.priceCol === '最新単価', '参考単価の列が出る（末尾は「' + r.priceCol + '」）');
    say(/800円/.test(r.hit), '買った記録がある行に単価が出る（' + r.hit + '）');
    // ⚠ **出所を消さない**——値段だけだと「いつの・誰からの値段か分からない数」になり、
    //    ワンノートの `単価（ロット1）みなと` と同じ問題を作り直します。
    say(/2026-08-19/.test(r.hit) && /テスト商店/.test(r.hit), '出所（時点・仕入先）が添えてある');
    say(/買った記録がありません/.test(r.miss), "引けない行は黙らずに理由を出す（" + r.miss + "）");
    say(r.chrome, '足した列はクローム（本文に焼き付かない）');
    // ⚠ ここが今日の罠。付くと断り文の1行幅で列が決まり、値より断り文が列を広げる。
    say(!r.atomic, 'クロームのセルに本文の折り返しの印（cell-atomic）が付かない');
    say(r.missWrap === 'normal', '断り文は折り返す（' + r.missWrap + '）');
    say(r.priceW <= 160, '参考単価の列が広がりすぎない（' + r.priceW + 'px）');
    say(r.fits, '1600px で表が収まる（横スクロールせずに読める）');
    say(!r.pageScrolls, 'ページは横へ揺れない');
  } finally {
    for (const id of ids) {
      await page.evaluate(async (pid) => {
        await fetch('/api/lock/force?id=' + pid, { method: 'POST' });
        await fetch('/api/delete-page?id=' + encodeURIComponent(pid), { method: 'POST' });
      }, id);
    }
  }
  await browser.close();
  console.log(bad === 0 ? 'PASS' : 'FAIL (' + bad + ')');
  process.exit(bad === 0 ? 0 : 1);
})();
