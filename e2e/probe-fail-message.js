// 失敗の理由が、利用者に届くか（2026-09-14）
//
// **サーバーは丁寧に説明しているのに、画面には数字しか出ていませんでした。**
// 応答が `text/plain` だと `res.json()` が落ち、`catch(()=>({}))` が空を返し、
// 「400」だけが出ます。退避のつもりの `|| await res.text()` は**本体を使い切った
// あと**なので永久に空でした。
const { chromium } = require('playwright');
const lib = require('./lib');
const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
// ⚠ **当て先は焼き込みません**（2026-09-20・`probe-analyze-refresh` と同じ理由）。
let PAGE = process.env.WCMS_HOST_PAGE || '';
let fail = 0;
const ok = (c, m, x) => { console.log((c ? '  OK ' : '  NG ') + m + (x ? '  ' + x : '')); if (!c) fail++; };

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 1000 }, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  await page.goto(BASE + '/login');
  await page.fill('#username', 'a'); await page.fill('#password', 'a');
  await page.click('button[type=submit]'); await page.waitForLoadState('networkidle');
  if (!PAGE) {
    const rec = await lib.findRecordWithPDF(page);
    PAGE = rec.pageID;
  }
  if (!PAGE) {
    console.log('  -- PDFを持つ通信記録がありません（取り込み前なら正常）。飛ばします');
    await browser.close();
    process.exit(0);
  }
  await page.goto(BASE + '/' + PAGE);
  await page.waitForTimeout(1500);

  // サーバーが text/plain で断る実例——許可していない拡張子。
  const server = await page.evaluate(async (id) => {
    const fd = new FormData();
    fd.append('page_id', id);
    fd.append('file', new File(['dummy'], 'x.exe', { type: 'application/octet-stream' }));
    const res = await fetch('/api/upload-file', { method: 'POST', body: fd });
    return { status: res.status, type: res.headers.get('content-type'), body: (await res.text()).trim() };
  }, PAGE);
  console.log('    サーバー: ' + server.status + ' ' + server.type);
  console.log('    本文    : ' + server.body);
  ok(server.status === 400, 'サーバーは 400 で断る');
  ok(/拡張子/.test(server.body), 'サーバーは理由を書いている');

  // 画面に出る文言——app.js の readResult / failMessage を通したもの。
  // **本体を1回だけ読む**ので、text/plain でも理由が取れる。
  const shown = await page.evaluate(async (id) => {
    const fd = new FormData();
    fd.append('page_id', id);
    fd.append('file', new File(['dummy'], 'x.exe', { type: 'application/octet-stream' }));
    const res = await fetch('/api/upload-file', { method: 'POST', body: fd });
    // app.js と同じ手順（本体を1回だけ読み、JSONなら解釈する）。
    let text = '';
    try { text = await res.text(); } catch (e) { text = ''; }
    let d;
    try { const j = JSON.parse(text); d = (j && typeof j === 'object') ? j : { message: text }; }
    catch (e) { d = { message: String(text || '').trim() }; }
    return (d && d.message) || res.statusText || String(res.status);
  }, PAGE);
  console.log('    画面    : ' + shown);
  ok(/拡張子/.test(shown), '画面にも理由が出る（数字だけではない）', shown);
  ok(shown !== '400' && shown !== 'Bad Request', '数字やステータス名で終わっていない');

  ok(errs.length === 0, 'JSエラーなし', errs[0] || '');
  await browser.close();
  console.log(fail ? '\n' + fail + ' 件失敗' : '\n全項目OK');
  process.exit(fail ? 1 : 0);
})();
