// 外注加工の資料を発注書に添えることを画面から確かめる（2026-09-28・ext/toho/order_docs.go）。
//
// 利用者:「加工製品のページに、外注加工ごとに資料のブロック（開いたり閉じたりできる）を用意して、
// そこに保存したファイルをメールやFAX、印刷等に追加できるようにしてはどうでしょう？」。
//
//   ① 加工製品の「資料 1」の折りたたみに「＋ ファイル」で図面（A3 横のPDF）を上げる
//   ② 発注書（行が 弊社品番＋番号 1 を指す）のメールの送る欄（部品）に、その図面の候補（チェック済み）が出る
//   ③ 「📠 FAX・印刷用（資料を綴じる）」を押すと、発注書のうしろに図面を綴じた1本ができる
//   ④ メールの送信に図面が添付として載る——⚠ **送信の口は画面の中で差し止める**（本物のメールは出さない）
//
// 当て先は自分で作って最後に消します（トップ直下に2枚）。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-order-docs.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

// minimalPDF は A3 横・1ページの素のPDFを組みます（xref の位置も数える）。
function minimalPDF() {
  const stream = '40 40 m 1100 800 l S';
  const objs = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 1190.55 841.89] /Contents 4 0 R /Resources << >> >>',
    '<< /Length ' + stream.length + ' >>\nstream\n' + stream + '\nendstream',
  ];
  let out = '%PDF-1.4\n';
  const offs = [];
  objs.forEach((o, i) => { offs.push(out.length); out += (i + 1) + ' 0 obj\n' + o + '\nendobj\n'; });
  const xref = out.length;
  out += 'xref\n0 ' + (objs.length + 1) + '\n0000000000 65535 f \n' +
    offs.map((o) => String(o).padStart(10, '0') + ' 00000 n \n').join('');
  out += 'trailer\n<< /Size ' + (objs.length + 1) + ' /Root 1 0 R >>\nstartxref\n' + xref + '\n%%EOF\n';
  return Buffer.from(out, 'latin1');
}

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  let product = '', order = '';
  try {
    await login(page, BASE);
    product = await makePage(page, '<h1>【E2E】資料の加工製品</h1>' +
      '<table><caption>外注加工</caption><tbody><tr><th>番号</th><th>加工内容</th><th>個数</th></tr>' +
      '<tr><td>1</td><td>レーザー切断</td><td>1</td></tr></tbody></table>' +
      '<details open><summary>資料 1</summary><p>図面</p></details>');
    check('加工製品を作れた', !!product, product);

    // ① 「＋ ファイル」で図面を上げる
    await page.goto(BASE + '/' + product + '?edit=true');
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    const [chooser] = await Promise.all([
      page.waitForEvent('filechooser'),
      page.locator('#w-editor-content details .fold-add-file').click(),
    ]);
    await chooser.setFiles([{ name: '図面A3.pdf', mimeType: 'application/pdf', buffer: minimalPDF() }]);
    await page.waitForFunction(() => document.querySelector(
      '#w-editor-content details section[data-type="file-view"][data-ref]'), null, { timeout: 8000 });
    await page.waitForTimeout(2500); // 自動保存
    check('図面が「資料 1」に入った', await page.locator('#w-editor-content details section[data-type="file-view"]').count() === 1);

    order = await makePage(page, '<h1>【E2E】資料の発注書</h1>' +
      '<dl data-type="tags"><dt>仕入先</dt><dd>ひかりレーザー</dd></dl>' +
      '<table><caption>発注明細</caption><tbody>' +
      '<tr><th>弊社品番</th><th>種類</th><th>番号</th><th>品名</th><th>加工内容</th><th>数量</th><th>状態</th></tr>' +
      '<tr><td>' + product + '</td><td>外注加工</td><td>1</td><td>ブラケット</td><td>レーザー切断</td><td>1</td><td>未発注</td></tr>' +
      '</tbody></table>');
    check('発注書を作れた', !!order, order);

    // ② メールの送る欄（部品・2026-09-30）を開くと、添付の候補に図面（チェック済み）
    await page.goto(BASE + '/' + order);
    await page.waitForSelector('.order-send', { timeout: 8000 });
    check('FAX・印刷用のボタンが出る', await page.locator('[data-order-pdf-docs]').count() === 1);
    await page.locator('details.order-mail > summary').click();
    await page.waitForSelector('.order-mail .mail-compose [data-mc="to"]', { timeout: 8000 });
    const box = page.locator('.order-mail .mail-compose input[data-mc-file]');
    check('送る欄の添付の候補に図面が出る', await box.count() === 1);
    check('はじめからチェック済み', await box.first().isChecked());

    // ③ FAX・印刷用
    await page.locator('[data-order-pdf-docs]').click();
    const link = page.locator('[data-order-result] a');
    await link.waitFor({ timeout: 15000 });
    const href = await link.getAttribute('href');
    const info = await page.evaluate(async (u) => {
      const res = await fetch(u);
      const s = new TextDecoder('latin1').decode(await res.arrayBuffer());
      return { ok: res.ok, pages: (s.match(/\/Type \/Page\s/g) || []).length, a3: /\/MediaBox \[\s*0 0 1190\.55/.test(s) };
    }, href);
    check('綴じた1本が開ける', info.ok, href);
    check('発注書のうしろに図面が綴じてある（2ページ・A3 横のまま）', info.pages === 2 && info.a3, JSON.stringify(info));

    // ④ メールに図面が載る（⚠ 送信の口は差し止める——本物のメールは出さない）。
    //    2026-09-30 から発注書のPDFはサーバーが送る直前に作って足す（用件「発注書」）ので、画面が送るのは
    //    選んだ資料と用件だけ——ここでは「図面が選ばれて、用件が発注書であること」を見る。
    let mailBody = null;
    await page.route('**/api/mail/send', async (route) => {
      mailBody = JSON.parse(route.request().postData() || '{}');
      await route.fulfill({ status: 200, contentType: 'application/json', body: '{"success":true,"after_error":"（試験なので送っていません）"}' });
    });
    const sendBtn = page.locator('.order-mail .mail-compose [data-mc-send]');
    if (await sendBtn.isDisabled()) {
      // メールにサインインしていない環境では送信が押せない——押せるようにして口を差し止めたまま流す。
      await page.evaluate(() => { document.querySelector('.order-mail .mail-compose [data-mc-send]').disabled = false; });
    }
    await page.locator('.order-mail .mail-compose [data-mc="to"]').fill('e2e@invalid.example');
    await sendBtn.click();
    for (let i = 0; i < 50 && !mailBody; i++) await page.waitForTimeout(200);
    const files = (mailBody && mailBody.attachments || []).map((a) => a.page_id + '/' + a.file);
    check('メールに図面が載る（発注書のPDFはサーバーが足す）', files.length === 1 &&
      files.some((f) => f.startsWith(product + '/')), JSON.stringify(files));
    check('用件は発注書・元のページはこの発注書', mailBody && mailBody.purpose === '発注書' && mailBody.page_id === order,
      JSON.stringify(mailBody && { purpose: mailBody.purpose, page_id: mailBody.page_id }));
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    await page.unrouteAll({ behavior: 'ignoreErrors' }).catch(() => {});
    if (order) await deletePage(page, order).catch(() => {});
    if (product) await deletePage(page, product).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
