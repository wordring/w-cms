// 整理の欄の「同じ図面が既にあります」と「重複（取り込まない）」を画面から確かめる（2026-09-30）。
//
// 利用者:「同じものがあるということを提示して欲しいのと、同じものがあるので取り込まない選択肢が欲しいです」
// 「PDFの中身も比較してくれるなら、それに越したことは無いです」。
//
//   ① 同じ図面番号・同じ中身のPDF → 知らせに「同じ図面番号・同じファイル」、「重複（取り込まない）」が初めから選ばれている
//   ② 同じ図面番号・違う中身 → 知らせに「同じ図面番号です」、「🤖 中身を比べる」が出る（口は差し止めて、表示だけ見る）。
//      「重複」は選ばれていない——人が選ぶ
//   ③ 実行すると2枚ともごみ箱へ、既にある加工製品にこのメールの受信元が2つ足され、添付の「解析済み」が加工製品を指す
//
// 当て先は全部自分で作って最後に消します——取引先の下に【E2E】の会社（加工製品／装置／品目）、通信箱の下に
// 受信メールの記録と、解析が作ったのと同じ形の仮のページ2枚。本物の加工製品には触りません。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-filing-duplicate.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, findMailbox, childrenOf, bodyOf } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

// upload は page へPDFを1つ置き、添付IDを返します。
async function upload(page, pageId, name, content) {
  const lock = await page.request.post(BASE + '/api/lock?id=' + pageId, { headers: { Origin: BASE } });
  const token = (await lock.json()).token;
  const res = await page.request.post(BASE + '/api/upload-pdf', {
    headers: { Origin: BASE, 'X-Lock-Token': token },
    multipart: { page_id: pageId, pdf_file: { name, mimeType: 'application/pdf', buffer: Buffer.from(content) } },
  });
  await page.request.post(BASE + '/api/lock/force?id=' + pageId, { headers: { Origin: BASE } });
  const d = await res.json().catch(() => ({}));
  return d.id || '';
}

// saveBody は本文を書き直します（makePage と同じ作法）。
async function saveBody(page, id, html) {
  await page.evaluate(async (arg) => {
    const lr = await fetch('/api/lock?id=' + arg.id, { method: 'POST' });
    const lj = await lr.json().catch(() => ({}));
    await fetch('/api/save', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ page_id: arg.id, html: arg.html, token: lj.token || '' }),
    });
    await fetch('/api/lock/force?id=' + arg.id, { method: 'POST' });
  }, { id, html });
}

const CUSTOMER = '【E2E】重複の会社';
const NAME = '【E2E】取付ベース';
const NO = 'E2E-DUP-1';

const drawingBlock = (ref, source) =>
  '<section><h2>図面</h2><dl data-type="tags"><dt>図面番号</dt><dd>' + NO + '</dd><dt>図面名称</dt><dd>' + NAME +
  '</dd><dt>客先</dt><dd>' + CUSTOMER + '</dd>' + (source ? '<dt>受信元</dt><dd>' + source + '</dd>' : '') +
  '</dl><section data-type="file-view" data-ref="' + ref + '"></section></section>';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  const made = []; // 消す順（子から）に積む
  let record = '', product = '';
  // cleanup は作ったものを子から消します（何度呼んでもよい）。記録の下に残った仮のページも消す。
  const cleanup = async () => {
    if (record) {
      for (const c of await childrenOf(page, record)) await deletePage(page, c.ID);
      await deletePage(page, record);
    }
    for (const id of made) await deletePage(page, id);
    made.length = 0;
    record = '';
  };
  try {
    await login(page, BASE);
    const box = await findMailbox(page);
    const top = await childrenOf(page, '000000');
    const partners = (top.find(c => (c.Title || '').trim() === '取引先') || {}).ID || '';
    if (!box || !partners) {
      console.log('通信箱か取引先が無いので飛ばします');
      await browser.close();
      process.exit(0);
    }
    const partnersBefore = (await childrenOf(page, partners)).map(c => c.ID).join(',');

    // 既にある加工製品（取引先／【E2E】の会社／加工製品／【E2E】装置／品目）。
    const cust = await makePage(page, '<h1>' + CUSTOMER + '</h1>', partners);
    const prodBox = await makePage(page, '<h1>加工製品</h1>', cust);
    const mach = await makePage(page, '<h1>【E2E】装置</h1>', prodBox);
    product = await makePage(page, '<h1>' + NAME + '</h1>', mach);
    made.push(product, mach, prodBox, cust);
    const pid = await upload(page, product, 'e2e-dup.pdf', '%PDF-1.4 e2e 同じ図面');
    await saveBody(page, product, '<h1>' + NAME + '</h1>' + drawingBlock(product + '-' + pid, ''));
    check('既にある加工製品を作れた', !!product && !!pid, product + '-' + pid);

    // 受信メールの記録と、解析が作ったのと同じ形の仮のページ2枚。
    record = await makePage(page, '<h1>【E2E】図面の送付</h1><dl data-type="tags"><dt>向き</dt><dd>受信</dd>' +
      '<dt>チャネル</dt><dd>メール</dd><dt>差出人</dt><dd>試験 &lt;e2e-dup@invalid.example&gt;</dd></dl>', box);
    const same = await upload(page, record, 'e2e-dup.pdf', '%PDF-1.4 e2e 同じ図面');
    const remade = await upload(page, record, 'e2e-dup-remade.pdf', '%PDF-1.4 e2e 作り直した図面');
    const draftSame = await makePage(page, '<h1>' + NO + ' ' + NAME + '</h1>' + drawingBlock(record + '-' + same, record + '-' + same), record);
    const draftRemade = await makePage(page, '<h1>' + NO + ' ' + NAME + '（2）</h1>' + drawingBlock(record + '-' + remade, record + '-' + remade), record);
    check('メールの記録と仮のページ2枚を作れた', !!record && !!draftSame && !!draftRemade);

    await page.goto(BASE + '/' + record);
    await page.locator('#w-editor-content .filing-open').click();
    const card = (id) => page.locator('#w-editor-content .filing-card[data-page-id="' + id + '"]');
    await card(draftSame).locator('.filing-dup').waitFor({ timeout: 8000 });
    await card(draftRemade).locator('.filing-dup').waitFor({ timeout: 8000 });

    // ①
    const t1 = await card(draftSame).locator('.filing-dup').textContent();
    check('① 同じ図面が既にあると知らせる', t1.includes('同じ図面が既にあります') && t1.includes(NAME) && t1.includes('同じ図面番号・同じファイル'), t1);
    check('① 「重複（取り込まない）」が初めから選ばれている', await card(draftSame).locator('input[value="duplicate"]').isChecked());
    check('① 中身を比べるボタンは出さない（同じファイル）', await card(draftSame).locator('.filing-compare').count() === 0);

    // ②
    const t2 = await card(draftRemade).locator('.filing-dup').textContent();
    check('② 同じ図面番号・違う中身と知らせる', t2.includes('同じ図面番号ですが、ファイルの中身は違います'), t2);
    const dupOpt = card(draftRemade).locator('input[value="duplicate"]');
    check('② 「重複」は出ているが選ばれていない', await dupOpt.isVisible() && !(await dupOpt.isChecked()));
    let compared = null;
    await page.route('**/api/compare-drawings', async (route) => {
      compared = JSON.parse(route.request().postData() || '{}');
      await route.fulfill({ status: 200, contentType: 'application/json',
        body: JSON.stringify({ success: true, same: false, differences: ['寸法 120 → 125'], summary: '寸法が違う' }) });
    });
    await card(draftRemade).locator('.filing-compare').click();
    await page.waitForFunction((id) => {
      const o = document.querySelector('.filing-card[data-page-id="' + id + '"] .filing-compare-out');
      return o && o.textContent.includes('違いがあります');
    }, draftRemade, { timeout: 8000 }).catch(() => {});
    const out = await card(draftRemade).locator('.filing-compare-out').textContent();
    check('② 🤖 中身を比べると、違いを出す', out.includes('寸法 120 → 125'), out);
    check('② 比べる口へ仮のページと相手を送る', compared && compared.page_id === draftRemade && compared.with === product, JSON.stringify(compared));
    await page.unrouteAll({ behavior: 'ignoreErrors' });
    await card(draftRemade).locator('label.filing-merge-opt', { hasText: '重複' }).click();

    // ③ 実行
    await page.locator('#w-editor-content .filing-run').click();
    await page.waitForFunction(() => !document.querySelector('#w-editor-content .filing-panel'), null, { timeout: 10000 }).catch(() => {});
    const kids = (await childrenOf(page, record)).map(c => c.ID);
    check('③ 仮のページ2枚がごみ箱へ', !kids.includes(draftSame) && !kids.includes(draftRemade), kids.join(','));
    const pbody = await bodyOf(page, product);
    check('③ 加工製品にこのメールの受信元が2つ足された', pbody.includes(record + '-' + same) && pbody.includes(record + '-' + remade));
    const an = await page.evaluate(async (id) => (await (await fetch('/api/analyzed?page_id=' + id)).json()), record);
    check('③ 添付の「解析済み」が既にある加工製品を指す', an.analyzed && an.analyzed[same] && an.analyzed[same].page_id === product,
      JSON.stringify(an.analyzed || {}));
    check('JSエラーなし', errs.length === 0, errs.join(' | '));

    // 後片付けのあと、取引先の子が元どおりか。
    await cleanup();
    const partnersAfter = (await childrenOf(page, partners)).map(c => c.ID).join(',');
    check('取引先の子が元どおり', partnersAfter === partnersBefore);
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    await page.unrouteAll({ behavior: 'ignoreErrors' }).catch(() => {});
    await cleanup().catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
