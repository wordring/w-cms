// 見積依頼書の「📠 FAX・印刷用（資料を綴じる）」（2026-10-05・ext/toho/rfq_pdf.go の /api/rfq-pdf-docs・assets/app.js）を
// 画面から確かめる。【要求】見積依頼 §1・§2「外注加工の見積依頼には図面を綴じる」「📠 FAX（図面を綴じた FAX・印刷用）」。
//
//   ① 外注加工の行の加工製品に「資料 1」の図面があると、見積依頼書ページの「見積依頼を送る」にボタンが出る
//   ② 押すと、見積依頼書のうしろに図面を綴じた1本ができ、開くリンクが出る（PDF・見積依頼書＋図面のページ）
//   ③ 見積依頼書の PDF（表示中のもの）は差し替えない——ページは読み直さない
//
// 自分で作ったページ（入れ物・加工製品・見積依頼書）の上だけで動き、最後に全部消します。本物の見積依頼の置き場には触りません。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-rfq-pdf-docs.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, writeBody, minimalPDF } = require('./lib');
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
  const made = [];
  const saveBody = (id, html) => writeBody(page, id, html);
  try {
    await login(page, BASE);
    const box = await makePage(page, '<h1>【E2E】見積依頼の資料</h1><p>x</p>');
    made.push(box);
    const prod = await makePage(page, '<h1>【E2E】ブラケット</h1><p>x</p>', box);
    made.unshift(prod);
    const lock = await page.request.post(BASE + '/api/lock?id=' + prod, { headers: { Origin: BASE } });
    const token = (await lock.json()).token;
    const up = await page.request.post(BASE + '/api/upload-pdf', {
      headers: { Origin: BASE, 'X-Lock-Token': token },
      multipart: { page_id: prod, pdf_file: { name: '【E2E】図面.pdf', mimeType: 'application/pdf', buffer: minimalPDF() } },
    });
    await page.request.post(BASE + '/api/lock/force?id=' + prod, { headers: { Origin: BASE } });
    const drawing = ((await up.json().catch(() => ({}))).id) || '';
    check('図面を置けた', !!drawing, drawing);
    await saveBody(prod, '<h1>【E2E】ブラケット</h1>' +
      '<table><caption>外注加工</caption><tbody><tr><th>番号</th><th>加工内容</th><th>個数</th></tr><tr><td>1</td><td>レーザー切断</td><td>1</td></tr></tbody></table>' +
      '<details open><summary>資料 1</summary><section data-type="file-view" data-ref="' + prod + '-' + drawing + '"></section></details>');
    const rfq = await makePage(page, '<h1>見積依頼　【E2E】業者</h1><p>x</p>', box);
    made.unshift(rfq);
    await saveBody(rfq, '<h1>見積依頼　【E2E】業者</h1><dl data-type="tags"><dt>見積依頼番号</dt><dd>' + rfq + '</dd>' +
      '<dt>仕入先</dt><dd>【E2E】業者</dd><dt>見積依頼日</dt><dd>2026-10-05</dd></dl>' +
      '<table><caption>見積依頼明細</caption><tbody><tr><th>弊社品番</th><th>種類</th><th>番号</th><th>加工内容</th><th>数量</th><th>単位</th><th>単価</th><th>状態</th></tr>' +
      '<tr><td>' + prod + '</td><td>外注加工</td><td>1</td><td>レーザー切断</td><td>10</td><td>個</td><td></td><td>未回答</td></tr></tbody></table>' +
      '<section><h2>備考</h2><p>【E2E】</p></section><section data-mirror="見積依頼を送る"></section>');

    await page.goto(BASE + '/' + rfq);
    const btn = page.locator('#w-editor-content .rfq-pdf-docs-go');
    await btn.waitFor({ timeout: 10000 }).catch(() => {});
    check('① 「📠 FAX・印刷用（資料を綴じる）」が出る', (await btn.count()) === 1 && /資料を綴じる/.test(await btn.textContent().catch(() => '')));

    let navigated = false;
    page.on('framenavigated', (f) => { if (f === page.mainFrame()) navigated = true; });
    await btn.click();
    const link = page.locator('#w-editor-content .rfq-pdf-docs-say a');
    await link.waitFor({ timeout: 15000 }).catch(() => {});
    const href = await link.getAttribute('href').catch(() => null);
    const say = await page.locator('#w-editor-content .rfq-pdf-docs-say').textContent().catch(() => '');
    check('② 綴じた1本ができ、開くリンクが出る', !!href && href.startsWith('/' + rfq + '/') && /FAX・印刷用を作りました/.test(say), say + ' ' + href);
    if (href) {
      const res = await page.request.get(BASE + href);
      const buf = await res.body();
      const pages = (buf.toString('latin1').match(/\/Type\s*\/Page[^s]/g) || []).length;
      check('② PDF で、見積依頼書＋図面のページがある（2枚以上・A3 横の図面）', res.status() === 200 && buf.slice(0, 5).toString() === '%PDF-' &&
        pages >= 2 && /\/MediaBox\s*\[\s*0 0 1190\.55/.test(buf.toString('latin1')), res.status() + ' pages=' + pages);
    }
    check('③ ページは読み直さない（見積依頼書の PDF を差し替えない）', !navigated);
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
