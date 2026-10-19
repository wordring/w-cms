// 外注加工表の「資料」の列に並べたファイルの ID を、部材発注書が拾うことを画面から確かめる（2026-10-19・ext/toho/order_docs.go）。
//
// 利用者:「資料の置き場をページ内に一つにして、資料一つ一つにいつものIDを付けましょう。ページID-いつものIDで資料を特定し、その連結IDを空白文字や「、」で区切って表の該当セルに羅列しておけば良いのでは？」
//
//   ① 加工製品の折りたたみ「資料」（ページ内に1つ）に、書式の帯の 📎 で図面を2つ上げる
//   ② 外注加工表の「資料」のセルに、2つの ID を「、」で区切って書く → 閲覧で、1つずつのリンクになる
//   ③ 部材発注書（行は 種類＝外注加工・番号なし・加工内容で当たる）のメールの送る欄に、2つとも候補（チェック済み）で出る
//
// 当て先は自分で作って最後に消します（トップ直下に2枚）。メールは送りません（送る欄を開くだけ）。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-order-docs-column.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, minimalPDF, saveNow } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let product = '', order = '';
  try {
    await login(page, BASE);
    product = await makePage(page, '<h1>【E2E】資料の列の加工製品</h1>' +
      '<table><caption>外注加工表</caption><tbody><tr><th>番号</th><th>加工内容</th><th>個数</th><th>資料</th></tr>' +
      '<tr><td></td><td>塗装</td><td>1</td><td>あとで</td></tr></tbody></table>' +
      '<details open><summary>資料</summary><p>図面</p></details>');
    check('加工製品を作れた', !!product, product);

    // ① 折りたたみ「資料」の中に図面を2つ
    await page.goto(BASE + '/' + product + '?edit=true');
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    for (const name of ['図面A.pdf', '図面B.pdf']) {
      await page.locator('#w-editor-content details > p', { hasText: '図面' }).first().click();
      await page.waitForSelector('#w-context-toolbar.active #w-ctx-file', { timeout: 4000 });
      const [chooser] = await Promise.all([
        page.waitForEvent('filechooser'),
        page.locator('#w-context-toolbar #w-ctx-file').click(),
      ]);
      const before = await page.locator('#w-editor-content details section[data-type="file-view"][data-ref]').count();
      await chooser.setFiles([{ name, mimeType: 'application/pdf', buffer: minimalPDF() }]);
      await page.waitForFunction((n) => document.querySelectorAll(
        '#w-editor-content details section[data-type="file-view"][data-ref]').length > n, before, { timeout: 8000 });
    }
    const refs = await page.$$eval('#w-editor-content details section[data-type="file-view"][data-ref]', (els) => els.map((e) => e.getAttribute('data-ref')));
    check('図面が2つ「資料」に入った', refs.length === 2, JSON.stringify(refs));

    // ② 資料のセルに ID を2つ「、」で並べる（td の属性は本文で落ちるので、形で指す）
    const cell = page.locator('#w-editor-content table tr:nth-of-type(2) > td:nth-of-type(4)');
    await cell.click();
    // セルの文字だけを選ぶ（Ctrl+A は表の中では表の選び方になる）。
    await page.evaluate(() => {
      const td = document.querySelector('#w-editor-content table tr:nth-of-type(2) > td:nth-of-type(4)');
      const r = document.createRange();
      r.selectNodeContents(td);
      const s = getSelection();
      s.removeAllRanges();
      s.addRange(r);
    });
    await page.keyboard.type(refs.join('、'));
    await saveNow(page);
    await page.evaluate(() => localStorage.setItem('wcms.mode', 'view'));
    await page.goto(BASE + '/' + product);
    await page.waitForSelector('#w-editor-content table', { timeout: 8000 });
    const links = await page.$$eval('#w-editor-content table tr:nth-of-type(2) > td:nth-of-type(4) a.ref-link', (as) => as.map((a) => a.getAttribute('href')));
    check('閲覧で、並べた ID が1つずつのリンクになる', links.length === 2 &&
      links[0] === '/' + refs[0].replace('-', '#') && links[1] === '/' + refs[1].replace('-', '#'), JSON.stringify(links));
    check('セルは薄赤でない', await page.locator('#w-editor-content table tr:nth-of-type(2) > td.ref-missing').count() === 0);

    // ③ 部材発注書——番号の無い外注加工の行（加工内容で当たる）
    order = await makePage(page, '<h1>【E2E】資料の列の発注書</h1>' +
      '<dl data-type="tags"><dt>仕入先</dt><dd>ふじ鍍金</dd></dl>' +
      '<table><caption>部材発注明細表</caption><tbody>' +
      '<tr><th>弊社品番</th><th>種類</th><th>番号</th><th>品名</th><th>加工内容</th><th>数量</th><th>状態</th></tr>' +
      '<tr><td>' + product + '</td><td>外注加工</td><td></td><td>ブラケット</td><td>塗装</td><td>1</td><td>未発注</td></tr>' +
      '</tbody></table>');
    check('部材発注書を作れた', !!order, order);
    await page.goto(BASE + '/' + order);
    await page.waitForSelector('.order-send', { timeout: 8000 });
    check('FAX・印刷用のボタンが出る', await page.locator('[data-order-pdf-docs]').count() === 1);
    await page.locator('details.order-mail > summary').click();
    await page.waitForSelector('.order-mail .mail-compose [data-mc="to"]', { timeout: 8000 });
    const box = page.locator('.order-mail .mail-compose input[data-mc-file]');
    const n = await box.count();
    let allChecked = n > 0;
    for (let i = 0; i < n; i++) allChecked = allChecked && await box.nth(i).isChecked();
    check('送る欄の添付の候補に図面が2つ（チェック済み）', n === 2 && allChecked, String(n));
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    await page.goto(BASE + '/000000').catch(() => {});
    if (order) await deletePage(page, order).catch(() => {});
    if (product) await deletePage(page, product).catch(() => {});
    for (const id of [order, product].filter(Boolean)) {
      const st = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).status, id).catch(() => 0);
      if (st === 200) { await page.waitForTimeout(800); await deletePage(page, id).catch(() => {}); }
      const st2 = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).status, id).catch(() => 0);
      check('作ったページ ' + id + ' を消した', st2 === 404, String(st2));
    }
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
