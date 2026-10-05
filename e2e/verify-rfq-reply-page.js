// 見積依頼の返事ページ（2026-10-05・ext/toho/rfq_reply_page.go・assets/app.js）を画面から確かめる。
//
// 利用者:「見積依頼の返事は通信記録に入るので、解析ボタンを押して解析し、見積依頼の返事とわかれば、見積依頼の返事ページを作り、
// GeminiによってOCRして、どの見積依頼の返事か突き合わせる形になると思います」。
//
//   ① メールの添付の 🤖 解析が「見積依頼の返事」と答えたら、返事ページを作ったと知らせる（結んだか・選ぶか）
//   ② 結ばれていない返事ページに「見積依頼書との突き合わせ」——同じ仕入先の回答待ちの見積依頼書が候補に並ぶ
//   ③ 「この見積依頼書への返事」で結ぶ → 返事ページは見積依頼書の子へ移り、欄は見積依頼書へのリンクになる
//   ④ 「見積依頼書で単価を写す（🤖 返事を読む）」→ 見積依頼書で「🤖 返事を読む」の欄が開き、いちばん上の候補が返事の原本
//
// ⚠ **Gemini は呼びません**——①の解析の口だけは試験のブラウザの中で止めて答えを返します（ページを作り結ぶのは Go の
//    rfq_reply_page_test.go）。②〜④の返事ページは試験が自分で組みます。ページは全部自分で作り、最後に消します。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-rfq-reply-page.js
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
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 1000 } });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  const SUP = '【E2E】返事の業者';
  let box = '';
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
    box = await makePage(page, '<h1>【E2E】見積依頼の返事ページ</h1><p>x</p>');
    const rfq = await makePage(page, '<h1>見積依頼　' + SUP + '</h1><p>x</p>', box);
    await saveBody(rfq, '<h1>見積依頼　' + SUP + '</h1><dl data-type="tags"><dt>見積依頼番号</dt><dd>' + rfq + '</dd><dt>仕入先</dt><dd>' + SUP +
      '</dd><dt>見積依頼日</dt><dd>2026-10-05</dd><dt>回答日</dt><dd><br/></dd></dl>' +
      '<table><caption>見積依頼明細</caption><tbody><tr><th>弊社品番</th><th>種類</th><th>材質</th><th>形状</th><th>寸法</th><th>数量</th><th>単位</th><th>単価</th><th>備考</th><th>状態</th></tr>' +
      '<tr><td>000031</td><td>材料</td><td>SS400</td><td>板</td><td>t6*80*120</td><td>6</td><td>枚</td><td></td><td></td><td>未回答</td></tr></tbody></table>');
    const mail = await makePage(page, '<h1>お見積りの件</h1><p>x</p>', box);
    const lock = await page.request.post(BASE + '/api/lock?id=' + mail, { headers: { Origin: BASE } });
    const token = (await lock.json()).token;
    const up = await page.request.post(BASE + '/api/upload-pdf', {
      headers: { Origin: BASE, 'X-Lock-Token': token },
      multipart: { page_id: mail, pdf_file: { name: '【E2E】返事.pdf', mimeType: 'application/pdf', buffer: Buffer.from('%PDF-1.4 e2e 返事') } },
    });
    await page.request.post(BASE + '/api/lock/force?id=' + mail, { headers: { Origin: BASE } });
    const attach = ((await up.json().catch(() => ({}))).id) || '';
    await saveBody(mail, '<h1>お見積りの件</h1><dl data-type="tags"><dt>向き</dt><dd>受信</dd><dt>チャネル</dt><dd>メール</dd></dl>' +
      '<section data-type="file-view" data-ref="' + mail + '-' + attach + '"></section>');

    // ① 解析の口は止めて「返事」と答える。
    await page.route('**/api/analyze-attachment', (route) => route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({
      success: true, is_client_order: false, doc_type: 'rfq_reply', page_id: '009998', title: '見積依頼の返事　' + SUP, linked_rfq: '',
      candidates: [rfq], say: 'どの見積依頼書への返事か、候補が 2 つあります——返事ページで選んでください。' }) }));
    await page.goto(BASE + '/' + mail);
    await page.locator('#w-editor-content .file-menu-btn').first().waitFor({ timeout: 10000 }).catch(() => {});
    await page.locator('#w-editor-content .file-menu-btn').first().click();
    await page.locator('#w-file-menu.active .file-menu-item', { hasText: '解析' }).click().catch(() => {});
    await page.waitForFunction(() => /見積依頼の返事ページを作りました/.test((document.getElementById('w-toast-host') || {}).textContent || ''), null, { timeout: 8000 }).catch(() => {});
    const toast = await page.evaluate(() => (document.getElementById('w-toast-host') || {}).textContent || '');
    check('① 解析が返事と答えたら、返事ページを作ったと知らせる', /見積依頼の返事ページを作りました/.test(toast) && /\/009998/.test(toast) && /選んでください/.test(toast), toast.slice(0, 120));
    await page.unroute('**/api/analyze-attachment');

    // ②〜④ 返事ページ（メールの子・まだ結ばれていない）。
    const reply = await makePage(page, '<h1>見積依頼の返事　' + SUP + '</h1><p>x</p>', mail);
    await saveBody(reply, '<h1>見積依頼の返事　' + SUP + '</h1><dl data-type="tags"><dt>仕入先</dt><dd>' + SUP + '</dd><dt>回答日</dt><dd>2026-10-05</dd>' +
      '<dt>読んだ見積依頼番号</dt><dd><br/></dd><dt>見積依頼</dt><dd><br/></dd><dt>受信元</dt><dd>' + mail + '-' + attach + '</dd></dl>' +
      '<section data-mirror="見積依頼の返事の突き合わせ"></section>');
    await page.goto(BASE + '/' + reply);
    const linkBtn = page.locator('#w-editor-content .rfq-reply-link[data-rfq="' + rfq + '"]');
    await linkBtn.waitFor({ timeout: 10000 }).catch(() => {});
    check('② 見積依頼書との突き合わせ——同じ仕入先の回答待ちが候補に並ぶ', (await linkBtn.count()) === 1);
    await Promise.all([page.waitForNavigation({ timeout: 10000 }).catch(() => {}), linkBtn.click()]);
    const toRFQ = page.locator('#w-editor-content a[href="/' + rfq + '#rfq-reply"]');
    await toRFQ.waitFor({ timeout: 10000 }).catch(() => {});
    const kids = await childrenOf(page, rfq);
    check('③ 結ぶと見積依頼書の子へ移り、欄は見積依頼書へのリンクになる', (await toRFQ.count()) === 1 && kids.some((k) => k.ID === reply), JSON.stringify(kids));

    // ④
    await Promise.all([page.waitForNavigation({ timeout: 10000 }).catch(() => {}), toRFQ.click()]);
    const first = page.locator('#w-editor-content .rfq-reply-sources label').first();
    await first.waitFor({ timeout: 10000 }).catch(() => {});
    const label = await first.textContent().catch(() => '');
    check('④ 見積依頼書で「🤖 返事を読む」の欄が開き、いちばん上が返事の原本', /返事ページ: 見積依頼の返事　【E2E】返事の業者 の 【E2E】返事\.pdf/.test(label), label);
    check('④ 開いたあとは印（#rfq-reply）を消す', !page.url().includes('#rfq-reply'), page.url());
  } finally {
    // 子ページから消す（返事ページは見積依頼書の子へ移っている）。
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
