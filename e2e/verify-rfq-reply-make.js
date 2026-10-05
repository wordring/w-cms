// 見積依頼の記録が無い返事から見積依頼書ページを作る（2026-10-05・移行期——ext/toho/rfq_reply_make.go・assets/app.js）を画面から確かめる。
//
// 利用者:「移行期なので見積依頼の記録が無い。見積回答があります」→「作る」・品名の無い最後の行は「（上の行の品物）の20個の単価」。
//
//   ① 結ばれていない返事ページに「この返事から見積依頼書ページを作る」
//   ② 押すと案の表——読んだままの表を見積依頼明細の列に読み替え、弊社品番は図面番号から当てて題を添える・品名の無い行は上の行の数量違い・
//      合計の行は入れない
//   ③ 人が直して「見積依頼書ページを作る」→ 直した中身（印の付いた行だけ）を送る
//
// ⚠ 「作る」口（/api/rfq-reply/make-rfq）は本物の見積依頼の置き場にページを作るので、試験のブラウザの中で止めて送る中身だけを見ます
//    （作る側は Go の rfq_reply_make_test.go）。案を出す口は本物（読むだけ）。ページは自分で作り、最後に消します。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-rfq-reply-make.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, childrenOf } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1400, height: 1000 } });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let box = '';
  let sent = null;
  const saveBody = async (id, html) => {
    await page.evaluate(async (arg) => {
      const lr = await fetch('/api/lock?id=' + arg.id, { method: 'POST' });
      const lj = await lr.json().catch(() => ({}));
      await fetch('/api/save', { method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ page_id: arg.id, html: arg.html, token: lj.token || '' }) });
      await fetch('/api/lock/force?id=' + arg.id, { method: 'POST' });
    }, { id, html });
  };
  try {
    await login(page, BASE);
    box = await makePage(page, '<h1>【E2E】返事から見積依頼書</h1><p>x</p>');
    await page.route('**/api/rfq-reply/make-rfq', (route) => {
      sent = route.request().postDataJSON();
      route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ success: true, page_id: box, rows: 2 }) });
    });
    const prod = await makePage(page, '<h1>【E2E】台座</h1><p>x</p>', box);
    await saveBody(prod, '<h1>【E2E】台座</h1><dl data-type="tags"><dt>品番</dt><dd>E2E-RM-0009_rev0</dd><dt>図面番号</dt><dd>E2E-RM-0009_rev0</dd></dl>');
    const mail = await makePage(page, '<h1>【E2E】御見積書</h1><p>x</p>', box);
    await saveBody(mail, '<h1>【E2E】御見積書</h1><dl data-type="tags"><dt>向き</dt><dd>受信</dd><dt>チャネル</dt><dd>メール</dd></dl><p>x</p>');
    const reply = await makePage(page, '<h1>見積依頼の返事　【E2E】業者</h1><p>x</p>', mail);
    const row = (...c) => '<tr><td>' + c.join('</td><td>') + '</td></tr>';
    await saveBody(reply, '<h1>見積依頼の返事　【E2E】業者</h1><dl data-type="tags"><dt>仕入先</dt><dd>【E2E】業者</dd><dt>回答日</dt><dd>2026-08-25</dd>' +
      '<dt>読んだ見積依頼番号</dt><dd>1104</dd><dt>見積依頼</dt><dd><br/></dd><dt>受信元</dt><dd>' + mail + '</dd></dl>' +
      '<section data-mirror="見積依頼の返事の突き合わせ"></section>' +
      '<details open><summary>業者の返事（読んだまま）</summary><table><tbody><tr><th>詳 細</th><th>数 量</th><th>単位</th><th>単 価</th><th>金 額</th></tr>' +
      row('台座 E2E-RM-0009_rev0/φ131*41L/SS400', '1', '', '43,000', '43,000') + row('', '20', '', '6,400', '128,000') +
      row('合計', '', '', '', '171,000') + '</tbody></table></details>');

    await page.goto(BASE + '/' + reply);
    const makeBtn = page.locator('#w-editor-content .rfq-reply-make');
    await makeBtn.waitFor({ timeout: 10000 }).catch(() => {});
    check('① 「この返事から見積依頼書ページを作る」がある', (await makeBtn.count()) === 1);
    await makeBtn.click();
    await page.locator('#w-editor-content .rfq-reply-make-table').waitFor({ timeout: 8000 }).catch(() => {});
    const rows = await page.evaluate(() => Array.from(document.querySelectorAll('#w-editor-content .rfq-reply-make-table tr')).slice(1).map((tr) => ({
      pid: tr.querySelector('.rfq-mk-pid').value, hint: (tr.querySelector('.rfq-mk-hint') || {}).textContent || '', id: tr.querySelector('.rfq-mk-id').value,
      qty: tr.querySelector('.rfq-mk-qty').value, cost: tr.querySelector('.rfq-mk-cost').value, note: tr.querySelector('.rfq-mk-note').value,
    })));
    check('② 案は2行（合計の行は入れない）', rows.length === 2, JSON.stringify(rows));
    check('② 弊社品番は図面番号から当てて題を添える', rows[0] && rows[0].pid === prod && rows[0].hint === '【E2E】台座' && rows[0].id === 'E2E-RM-0009_rev0' &&
      rows[0].qty === '1' && rows[0].cost === '43000', JSON.stringify(rows[0]));
    check('② 品名の無い行は上の行の数量違い', rows[1] && rows[1].pid === prod && rows[1].qty === '20' && rows[1].cost === '6400' && /上の行と同じ品物/.test(rows[1].note),
      JSON.stringify(rows[1]));
    // ③ 2行目の単価を直して作る。
    await page.locator('#w-editor-content .rfq-reply-make-table tr').nth(2).locator('.rfq-mk-cost').fill('6300');
    await Promise.all([page.waitForNavigation({ timeout: 10000 }).catch(() => {}), page.click('#w-editor-content .rfq-reply-make-go')]);
    const got = sent && (sent.rows || []).map((r) => r.product_id + '/' + r.quantity + '/' + r.cost);
    check('③ 直した中身を送る（返事ページ・行）', !!sent && sent.page_id === reply && JSON.stringify(got) === JSON.stringify([prod + '/1/43000', prod + '/20/6300']),
      JSON.stringify(sent && { page_id: sent.page_id, got }));
    check('③ 作ったら見積依頼書へ移る', page.url().endsWith('/' + box), page.url());
  } finally {
    const all = [];
    const walk = async (id) => { for (const k of (await childrenOf(page, id).catch(() => [])) || []) { await walk(k.ID); } all.push(id); };
    if (box) await walk(box);
    for (const id of all) await deletePage(page, id);
    const gone = await page.evaluate(async (ids) => Promise.all(ids.map(async (pid) => (await fetch('/api/load?id=' + pid)).status)), all);
    check('作ったページを消した', gone.length > 0 && gone.every((s) => s === 404), gone.join(','));
  }
  check('ページのエラーが無い', errs.length === 0, errs.join(' / '));
  await browser.close();
  console.log(fails ? `✗ ${fails} 件` : '✓ すべて合格');
  process.exit(fails ? 1 : 0);
})();
