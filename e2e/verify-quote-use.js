// 貰った単価（見積依頼の返事）の使い道（2026-10-05）を画面から確かめる——【要求】見積依頼 §4 の未決5、利用者の答え
// 「最新単価に見積も出す」「見積計算表へ写すボタン」。
//
//   ① 材料表の「最新単価」——買った記録の無い材料に、回答ありの見積の値段が「見積」の印・業者・日付つきで出る
//   ② 「💴 見積回答」の単価のある行に「計算表へ」
//   ③ 押すと写す欄——表（ロット）と行（「何の値段か」と同じ工程を最初から選ぶ）と単価
//   ④ 「写す」で見積計算表のその行の数に単価・備考に出所（見積 業者 日付）
//
// 自分で作ったページ（入れ物・加工製品・見積依頼書）の上だけで動き、最後に全部消します。材料は実データに無い名前（E2E材）にする
// ——最新単価は全社の発注を引くので、本物の材料だと買った値段が出て見積が出ない。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-quote-use.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, writeBody } = require('./lib');
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
    const box = await makePage(page, '<h1>【E2E】貰った単価の使い道</h1><p>x</p>');
    made.push(box);
    const prod = await makePage(page, '<h1>【E2E】カバー</h1><p>x</p>', box);
    made.unshift(prod);
    await saveBody(prod, '<h1>【E2E】カバー</h1>' +
      '<section><h2>材料</h2><table><caption>材料</caption><tbody><tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>' +
      '<tr><td>E2E材</td><td>板</td><td>t9.9</td><td>1</td></tr></tbody></table></section>' +
      '<section><h2>支給部品</h2><table><caption>支給部品</caption><tbody><tr><th>品名</th><th>仕様</th><th>個数</th><th>備考</th></tr>' +
      '<tr><td></td><td></td><td></td><td></td></tr></tbody></table></section>' +
      '<table><caption>見積計算表</caption><tbody><tr><th>工程</th><th>数</th><th>単位</th><th>備考</th></tr>' +
      '<tr><td>ロット</td><td>20</td><td>個</td><td></td></tr><tr><td>材料</td><td>1200</td><td>円</td><td></td></tr>' +
      '<tr><td>塗装</td><td></td><td></td><td></td></tr><tr><td>単価</td><td></td><td>円</td><td></td></tr></tbody></table>');
    const rfq = await makePage(page, '<h1>見積依頼　【E2E】業者</h1><p>x</p>', box);
    made.unshift(rfq);
    await saveBody(rfq, '<h1>見積依頼　【E2E】業者</h1><dl data-type="tags"><dt>見積依頼番号</dt><dd>' + rfq + '</dd>' +
      '<dt>仕入先</dt><dd>【E2E】業者</dd><dt>回答日</dt><dd>2026-10-05</dd></dl>' +
      '<table><caption>見積依頼明細</caption><tbody><tr><th>弊社品番</th><th>種類</th><th>加工内容</th><th>材質</th><th>形状</th><th>寸法</th><th>数量</th><th>単価</th><th>状態</th></tr>' +
      '<tr><td>' + prod + '</td><td>外注加工</td><td>塗装</td><td></td><td></td><td></td><td>20</td><td>298</td><td>回答あり</td></tr>' +
      '<tr><td>' + prod + '</td><td>材料</td><td></td><td>E2E材</td><td>板</td><td>t9.9</td><td>5</td><td>1500</td><td>回答あり</td></tr></tbody></table>');

    await page.goto(BASE + '/' + prod);
    await page.locator('#w-editor-content .rfq-quote-put').first().waitFor({ timeout: 10000 }).catch(() => {});
    // ①
    const price = await page.locator('#w-editor-content td.mat-price').first().textContent().catch(() => '');
    check('① 最新単価に見積（印・業者・日付つき）', /見積 1,500円/.test(price) && /2026-10-05/.test(price) && /【E2E】業者/.test(price), price);
    // ②
    const btns = page.locator('#w-editor-content .rfq-quote-put');
    check('② 見積回答の単価のある行に「計算表へ」', (await btns.count()) === 2);
    // ③
    const paint = page.locator('#w-editor-content .rfq-quote-put[data-what="塗装"]');
    await paint.click();
    const form = page.locator('#w-editor-content .rfq-quote-put-form');
    await form.waitFor({ timeout: 8000 }).catch(() => {});
    const sel = await page.evaluate(() => ({
      table: (document.querySelector('#w-editor-content .rfq-quote-put-table option:checked') || {}).textContent,
      row: (document.querySelector('#w-editor-content .rfq-quote-put-row') || {}).value,
      value: (document.querySelector('#w-editor-content .rfq-quote-put-value') || {}).value,
    }));
    check('③ 写す欄——表（ロット）・同じ工程の行・単価', sel.table === '1枚目（ロット 20）' && sel.row === '塗装' && sel.value === '298', JSON.stringify(sel));
    // ④
    await Promise.all([page.waitForNavigation({ timeout: 10000 }).catch(() => {}), page.click('#w-editor-content .rfq-quote-put-go')]);
    const rows = await page.evaluate(async (pid) => {
      const html = await (await fetch('/api/load?id=' + pid)).text();
      const doc = new DOMParser().parseFromString(html, 'text/html');
      const t = Array.from(doc.querySelectorAll('table')).find((x) => (x.querySelector('caption') || {}).textContent === '見積計算表');
      return Array.from(t.querySelectorAll('tbody tr')).slice(1).map((tr) => Array.from(tr.children).map((c) => c.textContent).join('|'));
    }, prod);
    check('④ 見積計算表の塗装の行に単価と出所', rows.includes('塗装|298|円|見積 【E2E】業者 2026-10-05'), JSON.stringify(rows));
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
