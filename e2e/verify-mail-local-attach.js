// 送る欄の「💻 パソコンから添付」（2026-10-04・assets/mail-compose.js・POST /api/mail/attach）を画面から確かめる。
//
// 利用者:「新しいメールを作成するときに、ローカルコンピュータから添付したいです。返信などの時も同様です」。
//
//   ① 送る欄に「💻 パソコンから添付」（ファイルを選ぶ欄は隠れている）
//   ② 選ぶと、まだ下書きでなければ先に下書きを保存し（draft_id なし）、そのあと1つずつ /api/mail/attach へ
//      （draft_id・file）、置けたものを印つきで並べ、下書きにも書き留める（draft_id あり・添付に入っている）
//   ③ 置けないもの（サーバーが断る）は ⚠ を出して並べない
//   ④ 欄へのドロップでも足せる（本文へのドロップ＝ページの添付にはならない）
//   ⑤ 送ると draft_id と、置いたファイル（下書きのページの）を添付に入れて送る・attach_error が返れば ⚠ を出す
//
// ⚠ **本物のメールは出さず、本物の通信箱に下書きも作りません**——下書きの保存・ファイルを置く・送るの3つの口は試験の
//    ブラウザの中で止めて、送ろうとした中身だけを見ます（サーバー側は Go の compose_attach_test.go）。送る欄は自分で作った
//    ページの上に開き、ページは最後に消します。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-mail-local-attach.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const FAKE_DRAFT = '009999';

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 1000 } });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));

  const drafts = [];
  const attaches = [];
  let sent = null;
  const pageUploads = [];
  page.on('request', (r) => { if (/\/api\/upload-(file|pdf|image)/.test(r.url())) pageUploads.push(r.url()); });
  await page.route('**/api/mail/draft', (route) => {
    drafts.push(route.request().postDataJSON());
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ success: true, draft_id: FAKE_DRAFT }) });
  });
  await page.route('**/api/mail/attach', (route) => {
    const body = route.request().postDataBuffer().toString('latin1');
    const draft = (body.match(/name="draft_id"\r\n\r\n([^\r]*)/) || [])[1] || '';
    const name = Buffer.from((body.match(/filename="([^"]*)"/) || [])[1] || '', 'latin1').toString('utf8');
    attaches.push({ draft, name });
    if (/\.exe$/.test(name)) {
      route.fulfill({ status: 400, contentType: 'application/json', body: JSON.stringify({ success: false, message: 'この種類のファイルはメールに添えられません（' + name + '）' }) });
      return;
    }
    const file = 'f' + attaches.length + name.slice(name.lastIndexOf('.'));
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ success: true, page_id: draft, file, name }) });
  });
  await page.route('**/api/mail/send', (route) => {
    sent = route.request().postDataJSON();
    route.fulfill({ status: 200, contentType: 'application/json',
      body: JSON.stringify({ success: true, sent: true, attach_error: 'E2E: 控えへ写せませんでした（下書きに残しています）' }) });
  });

  let host = '';
  try {
    await login(page, BASE);
    host = await makePage(page, '<h1>【E2E】パソコンから添付</h1><p>x</p>');
    await page.goto(BASE + '/' + host);
    await page.waitForFunction(() => !!window.wcmsMailCompose, null, { timeout: 10000 });
    await page.evaluate(() => {
      const box = document.createElement('div');
      box.id = 'e2e-compose';
      document.getElementById('w-editor-content').appendChild(box);
      window.wcmsMailCompose.open(box, { purpose: '新規' });
    });
    const form = page.locator('#e2e-compose .mail-compose');
    await form.waitFor({ timeout: 10000 });

    // ①
    const btn = form.locator('.mc-attach-local-btn');
    check('① 「💻 パソコンから添付」がある', (await btn.count()) === 1 && /パソコンから添付/.test(await btn.textContent()));
    check('① ファイルを選ぶ欄は隠れている', !(await form.locator('input[data-mc-local]').isVisible()));

    // ②
    await form.locator('input[data-mc="to"]').fill('e2e@example.jp');
    await form.locator('input[data-mc-local]').setInputFiles([
      { name: '図面A.pdf', mimeType: 'application/pdf', buffer: Buffer.from('%PDF-1.4 e2e') },
      { name: '手順.docx', mimeType: 'application/octet-stream', buffer: Buffer.from('PK e2e') },
    ]);
    await page.waitForFunction(() => document.querySelectorAll('#e2e-compose .mc-attach-item').length >= 2, null, { timeout: 8000 }).catch(() => {});
    await page.waitForTimeout(300);
    check('② 先に下書きを保存する（draft_id なし）', drafts.length >= 1 && !drafts[0].draft_id, JSON.stringify(drafts[0] || {}).slice(0, 120));
    check('② 1つずつ下書きへ置く（draft_id・ファイル名）', JSON.stringify(attaches) === JSON.stringify([{ draft: FAKE_DRAFT, name: '図面A.pdf' }, { draft: FAKE_DRAFT, name: '手順.docx' }]), JSON.stringify(attaches));
    const items = await form.locator('.mc-attach-item').evaluateAll((ls) => ls.map((l) => (l.querySelector('input').checked ? '☑' : '☐') + l.textContent.trim()));
    check('② 置けたものを印つきで並べる', items.includes('☑図面A.pdf') && items.includes('☑手順.docx'), JSON.stringify(items));
    const last = drafts[drafts.length - 1] || {};
    check('② 下書きにも書き留める（draft_id あり・添付に入っている）', drafts.length === 2 && last.draft_id === FAKE_DRAFT &&
      (last.attachments || []).filter((a) => a.page_id === FAKE_DRAFT).map((a) => a.name).join(',') === '図面A.pdf,手順.docx', JSON.stringify(last.attachments || []));
    check('② 欄の頭が下書きになる', /下書き/.test(await form.locator('.mc-head').textContent()));

    // ③
    await form.locator('input[data-mc-local]').setInputFiles([{ name: '動かす.exe', mimeType: 'application/octet-stream', buffer: Buffer.from('MZ') }]);
    await page.waitForFunction(() => /動かす\.exe/.test((document.querySelector('#e2e-compose .mc-attach-msg') || {}).textContent || ''), null, { timeout: 5000 }).catch(() => {});
    const msg = await form.locator('.mc-attach-msg').textContent();
    const items3 = await form.locator('.mc-attach-item').count();
    check('③ 置けないものは ⚠ を出して並べない', /⚠/.test(msg) && /動かす\.exe/.test(msg) && items3 === 2, msg + ' 並び ' + items3);
    check('③ 置けなかったら下書きを書き直さない', drafts.length === 2, String(drafts.length));

    // ④ ドロップ
    const dropped = await page.evaluate(() => {
      const f = document.querySelector('#e2e-compose .mail-compose');
      const dt = new DataTransfer();
      dt.items.add(new File(['%PDF-1.4 drop'], '落とした.pdf', { type: 'application/pdf' }));
      const over = new DragEvent('dragover', { dataTransfer: dt, bubbles: true, cancelable: true });
      f.dispatchEvent(over);
      const marked = f.classList.contains('mc-drop');
      const ev = new DragEvent('drop', { dataTransfer: dt, bubbles: true, cancelable: true });
      f.dispatchEvent(ev);
      return { marked, prevented: ev.defaultPrevented, overPrevented: over.defaultPrevented };
    });
    await page.waitForFunction(() => /落とした\.pdf/.test(Array.from(document.querySelectorAll('#e2e-compose .mc-attach-item')).map((l) => l.textContent).join(' ')), null, { timeout: 8000 }).catch(() => {});
    const items4 = await form.locator('.mc-attach-item').evaluateAll((ls) => ls.map((l) => l.textContent.trim()));
    check('④ 欄へのドロップでも足せる', dropped.prevented && dropped.overPrevented && dropped.marked && items4.includes('落とした.pdf') &&
      attaches.length === 4 && attaches[3].draft === FAKE_DRAFT, JSON.stringify({ dropped, items4, n: attaches.length }));
    check('④ ページの添付（本文へのドロップ）にはならない', pageUploads.length === 0, pageUploads.join(','));

    // ⑤ 送る
    await form.locator('textarea[data-mc="body"]').fill('図面を送ります');
    await form.locator('button[data-mc-send]').click();
    await page.waitForFunction(() => /送信しました/.test((document.querySelector('#e2e-compose .mc-result') || {}).textContent || ''), null, { timeout: 8000 }).catch(() => {});
    const sentNames = ((sent && sent.attachments) || []).filter((a) => a.page_id === FAKE_DRAFT).map((a) => a.name).sort().join(',');
    check('⑤ draft_id と、置いたファイル（下書きのページの）を添付に入れて送る', !!sent && sent.draft_id === FAKE_DRAFT && sentNames === '図面A.pdf,手順.docx,落とした.pdf', JSON.stringify(sent && { draft_id: sent.draft_id, sentNames }));
    const result = await form.locator('.mc-result').textContent();
    check('⑤ 控えへ写せなかったら ⚠ を出す（attach_error）', /控えへ写せませんでした/.test(result), result);
  } finally {
    if (host) await deletePage(page, host);
    const gone = host ? (await page.request.get(BASE + '/api/load?id=' + host)).status() : 404;
    check('作ったページを消した', gone === 404, String(gone));
  }
  check('ページのエラーが無い', errs.length === 0, errs.join(' / '));
  await browser.close();
  console.log(fails ? `✗ ${fails} 件` : '✓ すべて合格');
  process.exit(fails ? 1 : 0);
})();
