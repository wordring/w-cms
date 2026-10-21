// 記録の箱の「🤖 解析」が、本文にリンクの無い添付（人がメモに入れた PDF）にも出て、押すと解析の口へ送ることを、画面から確かめる
// （2026-10-21 夜・ext/toho/record_sections.go・【考察】通信箱の改善 §4.1）。
//
// 利用者:「どの方法で作られたとしても解析ボタンが必要です。そしてメモのように人が書き込んで作るものは、人がPDFを挿入する場合があります。それにも解析ボタンが必要になります。」
//
//   ① メモの記録（チャネル：メモ・記録の箱の印・本文に添付のリンクは無い）に PDF を置くと、箱の「🤖 解析（添付）」にその PDF とボタンが出る
//   ② 押すと /api/analyze-attachment に、その記録と PDF の保存名を送る（⚠ Gemini は呼ばない——口は試験のブラウザの中で止める）
//
// 当て先は自分で作って最後に消します。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-record-analyze.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, minimalPDF } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let rec = '';
  try {
    await login(page, BASE);
    rec = await makePage(page, '<h1>【E2E】メモの記録</h1><dl data-type="tags"><dt>チャネル</dt><dd>メモ</dd><dt>対応</dt><dd>未処理</dd></dl>' +
      '<p>電話で聞いたこと</p><section data-mirror="記録の箱"></section>');
    const up = await page.evaluate(async (a) => {
      const lr = await fetch('/api/lock?id=' + a.pid, { method: 'POST' });
      const lj = await lr.json().catch(() => ({}));
      const fd = new FormData();
      fd.append('page_id', a.pid);
      fd.append('pdf_file', new File([a.pdf], '人が入れた注文書.pdf', { type: 'application/pdf' }));
      const r = await fetch('/api/upload-pdf', { method: 'POST', body: fd, headers: { 'X-Lock-Token': lj.token || '' } });
      const d = await r.json().catch(() => ({}));
      await fetch('/api/unlock?id=' + a.pid + '&token=' + encodeURIComponent(lj.token || ''), { method: 'POST' });
      return { status: r.status, d };
    }, { pid: rec, pdf: minimalPDF().toString('latin1') });
    check('PDF を置けた', up.status === 200, JSON.stringify(up).slice(0, 160));

    let sent = null;
    await page.route('**/api/analyze-attachment', async (route) => {
      sent = JSON.parse(route.request().postData() || '{}');
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ success: true, kind: 'other', message: '（試験）どれでもありません' }) });
    });
    await page.goto(BASE + '/' + rec);
    const btn = page.locator('#w-editor-content .mail-chrome .attach-analyze');
    await btn.first().waitFor({ timeout: 10000 }).catch(() => {});
    const list = await page.locator('#w-editor-content .record-files').textContent().catch(() => '');
    check('① 本文にリンクの無い PDF にも「🤖 解析」が出る', (await btn.count()) === 1 && (list || '').includes('人が入れた注文書.pdf'), (list || '').slice(0, 120));
    await btn.first().click().catch(() => {});
    await page.waitForTimeout(1500);
    check('② 押すと、その記録と PDF を解析の口へ送る', !!sent && sent.page_id === rec && /\.pdf$/.test(sent.file || ''), JSON.stringify(sent));
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    if (rec) await deletePage(page, rec).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
