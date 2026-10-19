// 整理の「新規」で、同じ客先に同じ品番の加工製品ページが既にあるとき（2026-10-19・ext/toho/filing_same_code.go・assets/app.js の整理の欄）を画面から確かめる。
//
// 2026-10-01 利用者:「とすると、二つの加工製品ページが同じ品番を持つのはマズいので、加工製品ページを作る前にチェックし、編集者に選択させる必要があります」
// 2026-10-19 利用者の選択: 確かめるのは整理の「新規」のとき・ダイアログは「作る／やめる」。
//
//   ① 同じ会社に同じ図面番号の加工製品ページがあると、確かめのダイアログが出る（既にあるページの番号と品番を出す）
//   ② 「やめる」なら動かさない（解析で作ったページは記録の下に残る）
//   ③ 「別の品物として作る」なら、加工製品ページが社名の直下に入る
//
// 当て先は全部自分で作って最後に消します（製造台帳の下の【E2E】の会社・通信箱の下の記録）。本物の加工製品には触りません。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-filing-same-code.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, childrenOf, findMailbox } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const CUSTOMER = '【E2E】同じ品番の会社';
const CODE = 'E2E-SC-1';
const OLD = '【E2E】既にある部品';
const NAME = '【E2E】同じ品番の別の部品';
const drawingBlock = (no, name) =>
  '<section><h2>図面</h2><dl data-type="tags"><dt>図面番号</dt><dd>' + no + '</dd><dt>図面名称</dt><dd>' + name +
  '</dd><dt>装置名称</dt><dd><br/></dd><dt>客先</dt><dd>' + CUSTOMER + '</dd></dl></section>';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let record = '', cust = '';
  const wipe = async (id) => {
    for (const c of await childrenOf(page, id)) await wipe(c.ID);
    await deletePage(page, id);
  };
  const cleanup = async () => {
    if (record) await wipe(record);
    if (cust) await wipe(cust);
    record = cust = '';
  };
  try {
    await login(page, BASE);
    const box = await findMailbox(page);
    const top = await childrenOf(page, '000000');
    const ledger = (top.find(c => (c.Title || '').trim() === '製造台帳') || {}).ID || '';
    if (!box || !ledger) {
      console.log('通信箱か製造台帳が無いので飛ばします');
      await browser.close();
      process.exit(0);
    }
    const before = (await childrenOf(page, ledger)).map(c => c.ID).join(',');

    // 既にある加工製品ページ（製造台帳／【E2E】同じ品番の会社／既にある部品・図面番号 E2E-SC-1）。
    cust = await makePage(page, '<h1>' + CUSTOMER + '</h1><dl data-type="tags"><dt>ページ種類</dt><dd>社名</dd></dl>', ledger);
    const old = await makePage(page, '<h1>' + OLD + '</h1><dl data-type="tags"><dt>ページ種類</dt><dd>加工製品</dd></dl>' +
      drawingBlock(CODE, OLD), cust);
    // 受信メールの記録と、解析が作った仮のページ（同じ図面番号・違う名前）。
    record = await makePage(page, '<h1>【E2E】同じ品番の図面</h1><dl data-type="tags"><dt>向き</dt><dd>受信</dd>' +
      '<dt>チャネル</dt><dd>メール</dd><dt>差出人</dt><dd>試験 &lt;e2e-sc@invalid.example&gt;</dd></dl>', box);
    const part = await makePage(page, '<h1>' + CODE + ' ' + NAME + '</h1><dl data-type="tags"><dt>ページ種類</dt><dd>加工製品</dd></dl>' +
      drawingBlock(CODE, NAME), record);
    check('当て先を作れた', !!cust && !!old && !!record && !!part);

    const card = page.locator('#w-editor-content .filing-card[data-page-id="' + part + '"]');
    await page.goto(BASE + '/' + record);
    await page.locator('#w-editor-content .filing-open').click();
    await card.waitFor({ timeout: 10000 }).catch(() => {});
    const fill = async (sel, v) => { const f = card.locator(sel); if (await f.count()) await f.fill(v); };
    await fill('input[aria-label="customer"]', CUSTOMER);
    await fill('input[aria-label="machine_name"]', '');
    await fill('input[aria-label="drawing_name"]', NAME);
    await page.waitForTimeout(600);

    // ①
    await page.locator('#w-editor-content .filing-run').click();
    const dlg = page.locator('dialog.w-confirm');
    await dlg.waitFor({ timeout: 10000 }).catch(() => {});
    const msg = (await dlg.locator('.w-confirm-msg').textContent().catch(() => '')) || '';
    check('① 同じ会社に同じ品番があれば確かめのダイアログ（既にあるページと品番つき）', msg.includes('/' + old) && msg.includes(CODE) && msg.includes('同じ品番'), msg.slice(0, 200));
    check('① 押す名前は「別の品物として作る」と「やめる」', (await dlg.locator('button', { hasText: '別の品物として作る' }).count()) === 1 &&
      (await dlg.locator('button', { hasText: 'やめる' }).count()) === 1);

    // ②
    await dlg.locator('button', { hasText: 'やめる' }).click().catch(() => {});
    await page.waitForTimeout(800);
    const stillHere = (await childrenOf(page, record)).map((c) => c.ID).includes(part);
    const custKids = (await childrenOf(page, cust)).map((c) => c.ID);
    check('② 「やめる」なら動かさない', stillHere && custKids.length === 1 && custKids[0] === old, JSON.stringify({ stillHere, custKids }));

    // ③
    await page.locator('#w-editor-content .filing-run').click();
    await dlg.waitFor({ timeout: 10000 }).catch(() => {});
    await dlg.locator('button', { hasText: '別の品物として作る' }).click().catch(() => {});
    await page.waitForFunction(() => !document.querySelector('#w-editor-content .filing-panel'), null, { timeout: 10000 }).catch(() => {});
    await page.waitForTimeout(500);
    const after = (await childrenOf(page, cust)).map((c) => c.ID);
    check('③ 「別の品物として作る」なら社名の直下に入る', after.includes(part) && after.includes(old), after.join(','));

    check('JSエラーなし', errs.length === 0, errs.join(' | '));
    await cleanup();
    check('製造台帳の子が元どおり', (await childrenOf(page, ledger)).map(c => c.ID).join(',') === before);
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    await cleanup().catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
