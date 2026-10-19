// 製造ページの作業の帯（2026-10-19・ext/toho/mfg_view.go・assets/app.js）を画面から確かめる。
//
// 利用者:「一つの製造ページに案件の見積もり依頼、部材発注、製造記録（工数など）、納品書、請求書を並べるのはどうですか？」
// 「2．勧め通りで」（製造完了は製造ページで付け、案件明細の製造完了は鏡）。
//
//   ① 製造ページの製造明細表の「製造完了」にチェック——押すと本文の欄に「済」
//   ② 案件残表の製造完了は 8/20（製造完了の製造に入ったのは8）・案件ページの製造完了の欄にも 8/20 の鏡
//   ③ 「納品」の欄で選んで「🚚 選んだ行で納品書を作る」——今回納める数の欄（まだ納めていない 8 が入っている）を 5 にして作る
//   ④ 納品書はこの製造ページの子にできる・製造ページの「納品」の欄は 残り 3
//
// 当て先は全部自分で作って最後に消します（案件残表を置いたページ・案件ページ・製造ページ・納品書）。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-mfg-page.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const parentOf = (page, id) => page.evaluate(async (pid) => (await (await fetch('/api/page-meta?id=' + pid)).json()).parent_id, id);

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1400, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let root = '', order = '', mfg = '', dlv = '';
  try {
    await login(page, BASE);
    root = await makePage(page, '<h1>【E2E】製造ページの帯</h1><section data-mirror="案件残"></section>');
    order = await makePage(page, '<h1>【E2E】製造ページの案件</h1><dl data-type="tags"><dt>ページ種類</dt><dd>顧客発注書</dd><dt>発注書番号</dt><dd>E2E-MP-1</dd>' +
      '<dt>発注元</dt><dd>【E2E】製造ページの客先</dd><dt>納期</dt><dd>2026-12-31</dd></dl>' +
      '<table><caption>案件明細表</caption><tbody><tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th><th>状態</th><th>製造完了</th></tr>' +
      '<tr><td></td><td>E2E-MP-A</td><td>【E2E】部品</td><td>20</td><td>個</td><td>未着手</td><td></td></tr></tbody></table>', root);
    const made = await page.evaluate(async (oid) => {
      const r = await fetch('/api/mfg/new', { method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ items: [{ order: oid, row: 0, item: 'E2E-MP-A', qty: '8' }] }) });
      return r.json();
    }, order);
    mfg = made.page_id || '';
    check('当て先を作れた', !!root && !!order && !!mfg, JSON.stringify(made));

    // ①
    await page.goto(BASE + '/' + mfg);
    const box = page.locator('#w-editor-content input.mfg-made');
    await box.waitFor({ timeout: 10000 }).catch(() => {});
    await box.click();
    await page.waitForTimeout(1500);
    const raw = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).text(), mfg);
    check('① 製造完了のチェックで、本文の欄に「済」', /<td>\s*済/.test(raw.replace(/<input[^>]*>/g, '')), raw.replace(/\s+/g, ' ').slice(0, 400));

    // ②
    await page.goto(BASE + '/' + root);
    await page.waitForSelector('#w-editor-content tr[data-order-page="' + order + '"]', { timeout: 10000 }).catch(() => {});
    const bl = await page.evaluate((oid) => {
      const td = document.querySelector('#w-editor-content tr[data-order-page="' + oid + '"] td[title="製造ページの製造完了から数えた"]');
      return td ? td.textContent.trim() : null;
    }, order);
    check('② 案件残表の製造完了は 8/20', bl === '8/20', String(bl));
    const view = await page.evaluate(async (pid) => (await fetch('/api/load?view=1&id=' + pid)).text(), order);
    const m = view.match(/class="vocab-chrome made-mirror"[^>]*>([^<]*)</);
    check('② 案件ページの製造完了の欄にも 8/20 の鏡', !!m && m[1] === '8/20', m ? m[1] : '');

    // ③
    await page.goto(BASE + '/' + mfg);
    await page.locator('#w-editor-content .mfg-dlv-pick').check();
    await page.click('#w-editor-content .mfg-dlv-go');
    const qty = page.locator('#w-editor-content .mfg-dlv-panel .mfg-dlv-qty');
    await qty.waitFor({ timeout: 5000 }).catch(() => {});
    check('③ 今回納める数の欄に、まだ納めていない 8', (await qty.inputValue().catch(() => '')) === '8');
    await qty.fill('5');
    await Promise.all([
      page.waitForURL((u) => !u.pathname.endsWith('/' + mfg), { timeout: 15000 }).catch(() => {}),
      page.click('#w-editor-content .mfg-dlv-make'),
    ]);
    dlv = (page.url().match(/\/(\d{6})(?:[?#]|$)/) || [])[1] || '';
    check('④ 納品書ページが開く', !!dlv && dlv !== mfg, page.url());
    if (dlv) {
      check('④ 納品書は製造ページの子', (await parentOf(page, dlv)) === mfg);
      await page.goto(BASE + '/' + mfg);
      const left = await page.evaluate(() => {
        const td = document.querySelector('#w-editor-content td.mfg-dlv');
        return td ? td.textContent.trim() : null;
      });
      check('④ 製造ページの「納品」の欄は 残り 3', (left || '').includes('残り 3'), String(left));
    }
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    if (dlv) await deletePage(page, dlv).catch(() => {});
    if (mfg) await deletePage(page, mfg).catch(() => {});
    if (order) await deletePage(page, order).catch(() => {});
    if (root) await deletePage(page, root).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
