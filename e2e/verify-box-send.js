// 通信箱の「☎ 電話をかける」「📠 FAX を送る」を画面から確かめる（2026-10-28・ext/comm/view_unhandled.go の boxSendChromeHTML・
// ext/comm/assets/comm.js の dialFromBox・faxFromBox）。
//
// 利用者:「FAXの送信と電話のダイヤルを通信フォルダから出来るようにしたいです。」
// 「そのパソコンによってかけ方が違うので、ブラウザにかけ方を記憶させておくのはどうですか？」
//
//   ① 名前を打って「☎ かける」——初めはかけ方を聞き、「会社の電話機で押す」を選ぶとブラウザに憶え、発信の記録を作り、番号を大きく出す
//   ② もう一度かけると、かけ方は聞かない（憶えている）
//   ③ PDF を選び、番号を打って「📠 FAX を送る」——確かめてから、ファイルと番号を送る
//
// ⚠ **本物の記録も FAX も作らない**——候補（/api/comm/numbers）・記録を作る口（/api/intake/memo）・FAX を送る口
//    （/api/fax/send-file・/api/fax/status）は、試験のブラウザの中で止めて答えを返す。開くのは本物の通信箱だが、何も書かない。
// 使い方: WCMS_BASE=https://localhost:9443 node verify-box-send.js
const { chromium } = require('playwright');
const { login, findMailbox } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  const memos = [], faxes = [];
  await page.route('**/api/comm/numbers?kind=phone', (route) => route.fulfill({ json: { success: true, numbers: [{ number: '03-1111-2222', label: '【E2E】みなと商店', page: '' }] } }));
  await page.route('**/api/comm/numbers?kind=fax', (route) => route.fulfill({ json: { success: true, numbers: [{ number: '03-1111-3333', label: '【E2E】みなと商店', page: '' }] } }));
  await page.route('**/api/intake/memo', (route) => { memos.push(route.request().postDataJSON()); route.fulfill({ json: { success: true, page_id: '999998', title: 'x' } }); });
  await page.route('**/api/fax/send-file', (route) => { faxes.push(route.request().postData() || ''); route.fulfill({ json: { success: true, job: '1', number: '0311113333', record: '999997' } }); });
  await page.route('**/api/fax/status*', (route) => route.fulfill({ json: { success: true, state: 'done', line: '' } }));

  try {
    await login(page, BASE);
    const box = await findMailbox(page);
    if (!box) { console.log('- 通信箱が無いので飛ばします'); return; }
    await page.goto(BASE + '/' + box);
    await page.evaluate(() => { try { localStorage.removeItem('wcms.dialHow'); } catch (e) {} });
    await page.locator('#w-dial-go').waitFor({ timeout: 15000 });
    check('⓪ 通信箱に「☎ かける」と「📠 FAX を送る」がある', await page.locator('#w-dial-go').count() === 1 && await page.locator('#w-fax-go').count() === 1);

    // ①
    await page.fill('#w-dial-to', '【E2E】みなと');
    await page.click('#w-dial-go');
    const dlg = page.locator('dialog.w-confirm[open]');
    await dlg.waitFor({ timeout: 8000 });
    check('① 初めはかけ方を聞く', (await dlg.innerText()).includes('どの電話を使いますか'));
    await dlg.locator('.w-choice-list button', { hasText: '会社の電話機' }).click();
    await page.waitForFunction(() => document.querySelector('dialog.w-confirm[open]') && document.querySelector('dialog.w-confirm[open]').innerText.includes('押してください'), null, { timeout: 8000 });
    const shown = await page.locator('dialog.w-confirm[open]').innerText();
    check('① 番号を大きく出す（相手の名前つき）', shown.includes('03-1111-2222') && shown.includes('【E2E】みなと商店'), shown.replace(/\s+/g, ' '));
    check('① 発信の記録を作った（向き送信・番号）', memos.length === 1 && memos[0].direction === '送信' && memos[0].phone === '03-1111-2222' && memos[0].channel === '電話', JSON.stringify(memos));
    check('① かけ方をブラウザに憶えた', await page.evaluate(() => localStorage.getItem('wcms.dialHow')) === 'show');
    await page.locator('dialog.w-confirm[open] .w-confirm-bar button', { hasText: 'やめる' }).click();

    // ②
    await page.uncheck('#w-dial-record');
    await page.fill('#w-dial-to', '06-7777-8888');
    await page.click('#w-dial-go');
    await page.waitForFunction(() => document.querySelector('dialog.w-confirm[open]'), null, { timeout: 8000 });
    const second = await page.locator('dialog.w-confirm[open]').innerText();
    check('② 2回目はかけ方を聞かず、打った番号を出す・記録を作らない', second.includes('06-7777-8888') && !second.includes('どの電話') && memos.length === 1, second.replace(/\s+/g, ' '));
    await page.locator('dialog.w-confirm[open] button', { hasText: '閉じる' }).first().click();

    // ③
    await page.setInputFiles('#w-fax-file', { name: '【E2E】見積.pdf', mimeType: 'application/pdf', buffer: Buffer.from('%PDF-1.4\n%%EOF\n') });
    await page.fill('#w-fax-to', '03-1111-3333');
    await page.click('#w-fax-go');
    await page.waitForFunction(() => document.querySelector('dialog.w-confirm[open]'), null, { timeout: 8000 });
    const confirm = await page.locator('dialog.w-confirm[open]').innerText();
    check('③ 送る前に確かめる（ファイル・番号・相手）', confirm.includes('【E2E】見積.pdf') && confirm.includes('03-1111-3333') && confirm.includes('【E2E】みなと商店'), confirm.replace(/\s+/g, ' '));
    await page.locator('dialog.w-confirm[open] .w-confirm-bar button', { hasText: '送る' }).click();
    await page.waitForFunction(() => document.body.innerText.includes('FAX を送る仕事を受けました'), null, { timeout: 8000 }).catch(() => {});
    check('③ ファイルと番号を送った', faxes.length === 1 && faxes[0].includes('name="number"') && faxes[0].includes('03-1111-3333') && faxes[0].includes('%PDF-1.4'), String(faxes.length));
    check('③ 受けたと知らせる', (await page.locator('body').innerText()).includes('FAX を送る仕事を受けました'));
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } finally {
    await browser.close();
    console.log(fails ? '\n結果: ' + fails + ' 件の失敗' : '\n結果: すべて通りました');
    process.exit(fails ? 1 : 0);
  }
})();
