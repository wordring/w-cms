// 現品票（2026-10-30・ext/toho/genpin.go・toho.js の openGenpinPanel）を画面から確かめる。
//
//   ① 納品書（自分で作ったページ）の納品明細表の下に「🏷 現品票」が出て、押すと行ごとの欄が開く
//   ② 開くと1行目の見本の絵（横長・高さ 696 点）が出る。箱の分け方を書いて「👁 見本」を押すと描き直す
//   ③ 箱の合計が数量と合わないと、見本の欄に断りが出る
//   ④ プリンターの道具が無い（試験用）ときは「🖨 QL-800 で刷る」が押せず、そう書いてある
//   ⑤ 部材発注書（自分で作ったページ）は外注加工の行だけが並ぶ・欄は本文に入らない
//
// 当て先はトップ直下に自分で作るページ2枚で、最後に消します。本物の札は刷りません（試験用は道具の組に入っていない）。
// 使い方: WCMS_BASE=https://localhost:9443 node verify-genpin.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, bodyOf, saveNow } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

const MARK = 'E2E' + Date.now().toString(36).toUpperCase();
const delivery = '<h1>【E2E】納品書 ' + MARK + '</h1><dl data-type="tags"><dt>納品書番号</dt><dd>D-' + MARK + '</dd>' +
  '<dt>客先</dt><dd>みなと商店</dd><dt>納品日</dt><dd>2026-10-30</dd></dl>' +
  '<table><caption>納品明細表</caption><tbody><tr><th>発注書番号</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th></tr>' +
  '<tr><td>PO-1</td><td>A100-B01-001</td><td>カバー</td><td>40</td><td>個</td></tr>' +
  '<tr><td>PO-1</td><td>A100-B01-002</td><td>ステー</td><td>5</td><td>個</td></tr></tbody></table>';
const order = '<h1>【E2E】部材発注 ' + MARK + '</h1><dl data-type="tags"><dt>仕入先</dt><dd>ふじ鍍金</dd><dt>納期</dt><dd>2026-11-10</dd></dl>' +
  '<table><caption>部材発注明細表</caption><tbody><tr><th>種類</th><th>品番</th><th>品名</th><th>加工部品番号</th><th>表面</th><th>数量</th><th>単位</th><th>状態</th></tr>' +
  '<tr><td>材料</td><td>K120-1</td><td>板</td><td></td><td></td><td>3</td><td>枚</td><td>未発注</td></tr>' +
  '<tr><td>外注加工</td><td>K120-2</td><td>ブラケット</td><td>K120-2-01</td><td>三価ユニクロ</td><td>20</td><td>個</td><td>発注済</td></tr>' +
  '</tbody></table>';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true });
  await login(page, BASE);
  const ids = [];
  try {
    const did = await makePage(page, delivery);
    ids.push(did);
    await page.goto(BASE + '/' + did);
    const go = page.locator('.genpin-bar .genpin-go');
    await go.waitFor({ timeout: 15000 });
    check('① 納品明細表の下に「🏷 現品票」', await go.count() === 1);
    await go.click();
    const panel = page.locator('.genpin-panel');
    await panel.locator('.genpin-table tr').nth(2).waitFor({ timeout: 15000 });
    check('① 行ごとの欄（2行）', await panel.locator('.genpin-table tr').count() === 3);
    const img = panel.locator('img.genpin-preview');
    await page.waitForFunction(() => { const i = document.querySelector('img.genpin-preview'); return i && i.complete && i.naturalWidth > 0; }, null, { timeout: 30000 });
    const size = await img.evaluate((i) => [i.naturalWidth, i.naturalHeight]);
    check('② 1行目の見本の絵（横長・高さ 696 点）', size[1] === 696 && size[0] > size[1], size.join('x'));

    const first = panel.locator('.genpin-table tr').nth(1);
    await first.locator('.genpin-boxes').fill('20,20');
    const before = await img.getAttribute('src');
    await first.locator('.genpin-see').click();
    await page.waitForFunction((b) => { const i = document.querySelector('img.genpin-preview'); return i && i.getAttribute('src') !== b && i.complete && i.naturalWidth > 0; }, before, { timeout: 30000 });
    check('② 箱の分け方を書いて見本を描き直す', (await img.getAttribute('src')).includes('boxes=20%2C20'));

    await first.locator('.genpin-boxes').fill('20,10');
    await first.locator('.genpin-see').click();
    await page.waitForFunction(() => (document.querySelector('.genpin-preview-say') || {}).textContent.includes('⚠'), null, { timeout: 30000 });
    check('③ 箱の合計が合わないと断る', (await panel.locator('.genpin-preview-say').textContent()).includes('合いません'));

    const printer = await panel.locator('.genpin-printer').textContent();
    const disabled = await panel.locator('.genpin-print').isDisabled();
    check('④ 道具が無ければ刷るボタンは押せない', printer.startsWith('⚠') ? disabled : !disabled, printer);

    const oid = await makePage(page, order);
    ids.push(oid);
    await page.goto(BASE + '/' + oid);
    const ogo = page.locator('.genpin-go');
    await ogo.waitFor({ timeout: 15000 });
    await ogo.click();
    await page.locator('.genpin-panel .genpin-table tr').nth(1).waitFor({ timeout: 15000 });
    const items = await page.locator('.genpin-panel .genpin-table tr td:nth-child(2)').allTextContents();
    check('⑤ 部材発注書は外注加工の行だけ', items.join(',') === 'K120-2', items.join(','));
    await saveNow(page);
    const body = await bodyOf(page, oid);
    check('⑤ 欄は本文に入らない', body.includes('K120-2') && !body.includes('genpin-panel') && !body.includes('genpin-table'));
  } finally {
    for (const id of ids) await deletePage(page, id).catch(() => {});
    await browser.close();
  }
  console.log(fails ? '✗ ' + fails + ' 件の失敗' : '✓ すべて合格');
  process.exit(fails ? 1 : 0);
})();
