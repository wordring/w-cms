// 見積依頼の返事（2026-10-05・ext/toho/rfq_reply.go・assets/app.js）を画面から確かめる。
//
// 利用者:「両方」——✓ 回答を記録 と 🤖 返事を読む。返事は「見積依頼書に手書きで FAX」「業者の見積書 PDF をメール」「メールの本文に値段」。
//
//   ① 見積依頼明細の足元に「返事:」の行（✓ 回答を記録・🤖 返事を読む）
//   ② 手で単価を書いた行で「✓ 回答を記録」→ その行が回答あり・回答日に今日（単価の無い行は未回答のまま）
//   ③ 「🤖 返事を読む」→ このページに置いた返事のファイルが候補に並ぶ（作った見積依頼書の PDF は並ばない）
//   ④ 「🤖 読む」→ 選んだ返事（ページとファイル）を送り、案の表（読んだ単価が入った行に印）を出す
//   ⑤ 人が単価を直して「✍ 印の付いた行を書き込む」→ その行の単価・回答あり
//
// ⚠ **Gemini は呼びません**——読む口（/api/rfq/read-reply）だけは試験のブラウザの中で止めて案を返します（サーバー側は Go の
//    rfq_reply_test.go）。回答を記録・書き込むは本物の口で、**自分で作ったページ**に書きます。作ったページは最後に消します。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-rfq-reply.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const today = () => {
  const d = new Date();
  return d.getFullYear() + '-' + String(d.getMonth() + 1).padStart(2, '0') + '-' + String(d.getDate()).padStart(2, '0');
};

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 1000 } });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  const made = [];
  const reads = [];
  const row = (mat, size, qty, price) => '<tr><td>000031</td><td>材料</td><td>' + mat + '</td><td>板</td><td>' + size + '</td><td>' + qty +
    '</td><td>枚</td><td>' + price + '</td><td></td><td>未回答</td></tr>';
  const WHAT2 = '材料 A5052 板 t3*50*50 ×2枚';
  await page.route('**/api/rfq/read-reply', async (route) => {
    reads.push(route.request().postDataJSON());
    await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({
      success: true, summary: 'E2E: A5052 は 1200円',
      rows: [{ row: 1, what: '材料 SS400 板 t6*80*120 ×6枚', price: '900', status: '回答あり' }, { row: 2, what: WHAT2, price: '', status: '未回答' }],
      guesses: [{ row: 2, unit_price: '1200', declined: false, note: '' }],
    }) });
  });
  const saveBody = async (id, html) => {
    await page.evaluate(async (arg) => {
      const lr = await fetch('/api/lock?id=' + arg.id, { method: 'POST' });
      const lj = await lr.json().catch(() => ({}));
      await fetch('/api/save', { method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ page_id: arg.id, html: arg.html, token: lj.token || '' }) });
      await fetch('/api/lock/force?id=' + arg.id, { method: 'POST' });
    }, { id, html });
  };
  const upload = async (pageId, name) => {
    const lock = await page.request.post(BASE + '/api/lock?id=' + pageId, { headers: { Origin: BASE } });
    const token = (await lock.json()).token;
    const res = await page.request.post(BASE + '/api/upload-pdf', {
      headers: { Origin: BASE, 'X-Lock-Token': token },
      multipart: { page_id: pageId, pdf_file: { name, mimeType: 'application/pdf', buffer: Buffer.from('%PDF-1.4 e2e ' + name) } },
    });
    await page.request.post(BASE + '/api/lock/force?id=' + pageId, { headers: { Origin: BASE } });
    return ((await res.json().catch(() => ({}))).file_name) || '';
  };
  const statusOf = async (id) => page.evaluate(async (pid) => {
    const html = await (await fetch('/api/load?id=' + pid)).text();
    const doc = new DOMParser().parseFromString(html, 'text/html');
    const t = Array.from(doc.querySelectorAll('table')).find((x) => (x.querySelector('caption') || {}).textContent === '見積依頼明細');
    const trs = Array.from(t.querySelectorAll('tbody tr')).slice(1).filter((tr) => !tr.closest('tfoot'));
    const dd = Array.from(doc.querySelectorAll('dl[data-type="tags"] dt')).find((d) => d.textContent === '回答日');
    return { rows: trs.map((tr) => { const c = tr.querySelectorAll('td'); return c[7].textContent + '/' + c[9].textContent; }), answered: dd ? dd.nextElementSibling.textContent.trim() : '' };
  }, id);

  try {
    await login(page, BASE);
    const box = await makePage(page, '<h1>【E2E】見積依頼の返事</h1><p>x</p>');
    made.push(box);
    const rfq = await makePage(page, '<h1>見積依頼　【E2E】業者</h1><p>x</p>', box);
    made.unshift(rfq);
    const reply = await upload(rfq, '【E2E】FAX返事.pdf');
    await upload(rfq, '見積依頼書 ' + rfq + ' 20261005-000000.pdf');
    await saveBody(rfq, '<h1>見積依頼　【E2E】業者</h1><dl data-type="tags"><dt>見積依頼番号</dt><dd>' + rfq + '</dd>' +
      '<dt>仕入先</dt><dd>【E2E】業者</dd><dt>回答日</dt><dd><br/></dd></dl>' +
      '<table><caption>見積依頼明細</caption><tbody><tr><th>弊社品番</th><th>種類</th><th>材質</th><th>形状</th><th>寸法</th><th>数量</th><th>単位</th><th>単価</th><th>備考</th><th>状態</th></tr>' +
      row('SS400', 't6*80*120', '6', '900') + row('A5052', 't3*50*50', '2', '') + '</tbody></table>');

    await page.goto(BASE + '/' + rfq);
    await page.locator('#w-editor-content .rfq-answered-go').waitFor({ timeout: 10000 }).catch(() => {});
    // ①
    check('① 「返事:」の行に ✓ 回答を記録・🤖 返事を読む', (await page.locator('#w-editor-content .rfq-answered-go').count()) === 1 &&
      (await page.locator('#w-editor-content .rfq-read-go').count()) === 1);
    // ②
    await Promise.all([page.waitForNavigation({ timeout: 10000 }).catch(() => {}), page.click('#w-editor-content .rfq-answered-go')]);
    let st = await statusOf(rfq);
    check('② 単価を書いた行だけ回答あり・回答日に今日', st.rows.join(',') === '900/回答あり,/未回答' && st.answered === today(), JSON.stringify(st));
    // ③
    await page.locator('#w-editor-content .rfq-read-go').waitFor({ timeout: 10000 });
    await page.click('#w-editor-content .rfq-read-go');
    const srcs = page.locator('#w-editor-content .rfq-reply-sources label');
    await srcs.first().waitFor({ timeout: 8000 }).catch(() => {});
    const labels = await srcs.allTextContents();
    check('③ このページに置いた返事のファイルが候補に並ぶ（作った見積依頼書の PDF は並ばない）',
      labels.some((l) => /このページ: 【E2E】FAX返事\.pdf/.test(l)) && !labels.some((l) => /見積依頼書 /.test(l)), JSON.stringify(labels));
    // ④
    await page.click('#w-editor-content .rfq-reply-read');
    await page.locator('#w-editor-content .rfq-reply-table').waitFor({ timeout: 8000 }).catch(() => {});
    check('④ 選んだ返事（このページ・そのファイル）を送る', reads.length === 1 && reads[0].page_id === rfq && reads[0].source_page === rfq && reads[0].source_file === reply,
      JSON.stringify(reads));
    const prop = await page.evaluate(() => Array.from(document.querySelectorAll('#w-editor-content .rfq-reply-table tr')).slice(1).map((tr) => {
      const i = tr.querySelectorAll('input');
      return (i[0].checked ? '☑' : '☐') + tr.children[1].textContent + ':' + i[1].value;
    }));
    check('④ 案の表——読んだ単価の入った行に印', JSON.stringify(prop) === JSON.stringify(['☐1:', '☑2:1200']), JSON.stringify(prop));
    // ⑤
    await page.locator('#w-editor-content .rfq-reply-table tr').nth(2).locator('input.rfq-reply-price').fill('1250');
    await Promise.all([page.waitForNavigation({ timeout: 10000 }).catch(() => {}), page.click('#w-editor-content .rfq-reply-apply')]);
    st = await statusOf(rfq);
    check('⑤ 直した単価で書き込む（回答あり）——印の無い行は触らない', st.rows.join(',') === '900/回答あり,1250/回答あり', JSON.stringify(st));
  } finally {
    for (const id of made) await deletePage(page, id);
    const gone = await page.evaluate(async (ids) => Promise.all(ids.map(async (pid) => (await fetch('/api/load?id=' + pid)).status)), made);
    check('作ったページを消した', gone.every((s) => s === 404), gone.join(','));
  }
  check('ページのエラーが無い', errs.length === 0, errs.join(' / '));
  await browser.close();
  console.log(fails ? `✗ ${fails} 件` : '✓ すべて合格');
  process.exit(fails ? 1 : 0);
})();
