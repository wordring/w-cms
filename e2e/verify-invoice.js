// 請求書（2026-10-17・ext/toho/invoice.go・invoice_pdf.go）を画面から確かめる。
//
// 2026-10-17 利用者:「1．締め日でまとめる 2．会社ごとに違う 3．集金は板金部でやっていないので、今回の市絵の請求のみでよいです。
// 4．わかりません。 5．決まるまで請求書に入れません。」
//
//   ① 「請求書を作る」の欄に、発行済の納品書の客先が出る（締め日・締めの日付・載せる行・金額・単価がまだ）
//   ② 締めを選んで「🧾 請求書を作る」——請求書ページが開く（題・請求日・請求期間・明細・置き場は 記録／年／月／製造記録）
//   ③ 載せた行は、欄から消える（二重に請求しない）。単価の空いた行は「単価がまだ」に残る
//   ④ 「📄 PDFを作る」で紙がページに出て「発行済」
//
// 当て先は全部自分で作って最後に消します（欄を置いたページ・納品書・できた請求書と、試験が作った空のフォルダ）。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-invoice.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, childrenOf } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';
const CLIENT = '【E2E】請求先';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const parentOf = (page, id) => page.evaluate(async (pid) => (await (await fetch('/api/page-meta?id=' + pid)).json()).parent_id, id);
const titleOf = (page, id) => page.evaluate(async (pid) => {
  const m = (await (await fetch('/api/load?id=' + pid)).text()).match(/<h1[^>]*>([^<]*)<\/h1>/);
  return m ? m[1] : '';
}, id);
const rowSel = '#w-editor-content tr.invoice-make-row[data-client="' + CLIENT + '"]';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1400, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let root = '', dlv = '', inv = '';
  try {
    await login(page, BASE);
    root = await makePage(page, '<h1>【E2E】請求書を作る</h1><section data-mirror="請求書を作る"></section>');
    dlv = await makePage(page, '<h1>【弊社】納品書　' + CLIENT + '</h1><dl data-type="tags"><dt>ページ種類</dt><dd>弊社納品書</dd>' +
      '<dt>客先</dt><dd>' + CLIENT + '</dd><dt>納品日</dt><dd>2026-09-25</dd><dt>発行日</dt><dd>2026-09-25</dd></dl>' +
      '<table><caption>納品明細表</caption><tbody><tr><th>受注</th><th>発注書番号</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th><th>単価</th></tr>' +
      '<tr><td>000001</td><td>E2E-PO</td><td>E2E-INV-A</td><td>【E2E】部品A</td><td>2</td><td>個</td><td>1500</td></tr>' +
      '<tr><td>000001</td><td>E2E-PO</td><td>E2E-INV-B</td><td>【E2E】部品B</td><td>1</td><td>個</td><td></td></tr></tbody></table>', root);
    check('当て先を作れた', !!root && !!dlv);

    await page.goto(BASE + '/' + root);
    await page.waitForSelector(rowSel, { timeout: 10000 }).catch(() => {});
    const r1 = await page.evaluate((sel) => {
      const tr = document.querySelector(sel);
      return tr ? [...tr.children].map((td) => td.textContent.trim()) : null;
    }, rowSel);
    check('① 客先の行が出る（締め日は末日・単価がまだ 1行）', !!r1 && r1[1].startsWith('末日') && r1[5] === '1行', JSON.stringify(r1));

    await page.fill(rowSel + ' .invoice-close', '2026-09-30');
    await Promise.all([
      page.waitForURL((u) => !u.pathname.endsWith('/' + root), { timeout: 15000 }).catch(() => {}),
      page.click(rowSel + ' .invoice-make'),
    ]);
    inv = (page.url().match(/\/(\d{6})(?:[?#]|$)/) || [])[1] || '';
    check('② 請求書ページが開く', !!inv && inv !== root, page.url());
    if (inv) {
      const raw = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).text(), inv);
      const flat = raw.replace(/\s+/g, ' ');
      check('② 題・請求日・請求期間', raw.includes('<h1>【弊社】請求書　' + CLIENT + '</h1>') && />\s*2026-09-30\s*</.test(raw) &&
        raw.includes('2026年9月1日〜2026年9月30日'), flat.slice(0, 300));
      check('② 明細は単価の決まった行だけ（部品A を載せ、部品B は載せない）', raw.includes('<caption>請求明細表</caption>') &&
        raw.includes('【E2E】部品A') && !raw.includes('【E2E】部品B') && raw.includes(dlv), flat.slice(0, 600));
      const chain = [];
      let cur = inv;
      for (let i = 0; i < 4; i++) { cur = await parentOf(page, cur); chain.push(await titleOf(page, cur)); }
      check('② 置き場は 記録／年／月／製造記録', chain[0] === '製造記録' && /^\d{2}月$/.test(chain[1]) && /^\d{4}年$/.test(chain[2]) && chain[3] === '記録', chain.join(' ← '));

      // ④ 紙
      await page.goto(BASE + '/' + inv);
      const state0 = (await page.textContent('#w-editor-content .delivery-state').catch(() => '')) || '';
      check('④ 作ったときは「未発行」', state0.includes('未発行'), state0);
      await Promise.all([
        page.waitForNavigation({ timeout: 20000 }).catch(() => {}),
        page.locator('#w-editor-content .invoice-pdf-go').click(),
      ]);
      await page.waitForSelector('#w-editor-content section[data-type="file-view"]', { timeout: 15000 }).catch(() => {});
      const fv = await page.locator('#w-editor-content section[data-type="file-view"]').getAttribute('data-ref').catch(() => '');
      const state1 = (await page.textContent('#w-editor-content .delivery-state').catch(() => '')) || '';
      check('④ PDFを作ると、ページに出て「発行済」', !!fv && fv.startsWith(inv + '-') && state1.includes('発行済'), (fv || '') + ' ' + state1);

      // ③ 欄に戻ると、部品A は載せたので消え、部品B だけ「単価がまだ」に残る。
      await page.goto(BASE + '/' + root);
      await page.waitForSelector(rowSel, { timeout: 10000 }).catch(() => {});
      const r2 = await page.evaluate((sel) => {
        const tr = document.querySelector(sel);
        return tr ? [...tr.children].map((td) => td.textContent.trim()) : null;
      }, rowSel);
      check('③ 載せた行は欄から消える（載せる行 0・単価がまだ 1行）', !!r2 && r2[3].startsWith('0行') && r2[5] === '1行', JSON.stringify(r2));
    }
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    if (inv) {
      let f = await parentOf(page, inv).catch(() => '');
      await deletePage(page, inv).catch(() => {});
      for (let i = 0; i < 3 && f && Number(f) > Number(dlv || 0); i++) {
        if ((await childrenOf(page, f).catch(() => [1])).length !== 0) break;
        const up = await parentOf(page, f).catch(() => '');
        await deletePage(page, f).catch(() => {});
        f = up;
      }
    }
    if (dlv) await deletePage(page, dlv).catch(() => {});
    if (root) await deletePage(page, root).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
