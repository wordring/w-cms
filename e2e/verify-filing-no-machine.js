// 整理で装置名称を空にしたとき（2026-10-16・ext/toho/filing.go・assets/app.js の整理の欄）を画面から確かめる。
//
// 利用者:「装置名は新仕様とし、文書も修正して下さい」（装置名称は無くてもよい——社名の直下に入れる）
// 「装置名のフォルダがあるのに、装置名を空にしている場合は、警告して保存の可否を問い合わせてください」。
//
//   ① その会社に装置名のフォルダがあるのに装置名称が空だと、確かめのダイアログが出る（装置名のフォルダの名前を出す）
//   ② 「やめる」なら動かさない（解析で作ったページは記録の下に残る）
//   ③ 「社名の直下に入れる」なら、加工製品ページが社名の直下に入る
//
// 当て先は全部自分で作って最後に消します（製造台帳の下の【E2E】の会社・通信箱の下の記録）。本物の加工製品には触りません。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-filing-no-machine.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, childrenOf, findMailbox } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const CUSTOMER = '【E2E】装置名なしの会社';
const MACHINE = '【E2E】ある装置';
const NAME = '【E2E】装置なしの部品';
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

    // 装置名のフォルダが1つある会社（製造台帳／【E2E】装置名なしの会社／【E2E】ある装置）。
    cust = await makePage(page, '<h1>' + CUSTOMER + '</h1><dl data-type="tags"><dt>ページ種類</dt><dd>社名</dd></dl>', ledger);
    const mach = await makePage(page, '<h1>' + MACHINE + '</h1><dl data-type="tags"><dt>ページ種類</dt><dd>装置</dd></dl>', cust);
    // 受信メールの記録と、解析が作った仮のページ（装置名称が空）。
    record = await makePage(page, '<h1>【E2E】装置名なしの図面</h1><dl data-type="tags"><dt>向き</dt><dd>受信</dd>' +
      '<dt>チャネル</dt><dd>メール</dd><dt>差出人</dt><dd>試験 &lt;e2e-nm@invalid.example&gt;</dd></dl>', box);
    const part = await makePage(page, '<h1>E2E-NM-1 ' + NAME + '</h1><dl data-type="tags"><dt>ページ種類</dt><dd>加工製品</dd></dl>' +
      drawingBlock('E2E-NM-1', NAME), record);
    check('当て先を作れた', !!cust && !!mach && !!record && !!part);

    const card = page.locator('#w-editor-content .filing-card[data-page-id="' + part + '"]');
    await page.goto(BASE + '/' + record);
    await page.locator('#w-editor-content .filing-open').click();
    await card.waitFor({ timeout: 10000 }).catch(() => {});
    // 欄: 顧客名はこの会社・装置名称は空・製品名称は部品の名前。
    const fill = async (sel, v) => { const f = card.locator(sel); if (await f.count()) await f.fill(v); };
    await fill('input[aria-label="customer"]', CUSTOMER);
    await fill('input[aria-label="machine_name"]', '');
    await fill('input[aria-label="drawing_name"]', NAME);
    const vals = await card.evaluate((c) => Array.from(c.querySelectorAll('input[type="text"], input:not([type])')).map((i) => (i.getAttribute("aria-label") || "") + "=" + i.value));
    check('欄の値（顧客名・装置名称は空・製品名称）', vals.some((v) => v === 'machine_name=') && vals.some((v) => v.endsWith(CUSTOMER)), vals.join(' | '));

    // ①
    await page.locator('#w-editor-content .filing-run').click();
    const dlg = page.locator('dialog.w-confirm');
    await dlg.waitFor({ timeout: 10000 }).catch(() => {});
    const msg = (await dlg.locator('.w-confirm-msg').textContent().catch(() => '')) || '';
    check('① 装置名のフォルダがあるのに空なら確かめのダイアログ（フォルダの名前つき）', msg.includes(MACHINE) && msg.includes('装置名称が空'), msg.slice(0, 160));
    const yesLabel = (await dlg.locator('button', { hasText: '社名の直下に入れる' }).count()) === 1;
    check('① 押す名前は「社名の直下に入れる」と「やめる」', yesLabel);

    // ②
    await dlg.locator('button', { hasText: 'やめる' }).click().catch(() => {});
    await page.waitForTimeout(800);
    const stillHere = (await childrenOf(page, record)).map((c) => c.ID).includes(part);
    const custKids = (await childrenOf(page, cust)).map((c) => c.ID);
    check('② 「やめる」なら動かさない', stillHere && custKids.length === 1 && custKids[0] === mach, JSON.stringify({ stillHere, custKids }));

    // ③
    await page.locator('#w-editor-content .filing-run').click();
    await dlg.waitFor({ timeout: 10000 }).catch(() => {});
    await dlg.locator('button', { hasText: '社名の直下に入れる' }).click().catch(() => {});
    await page.waitForFunction(() => !document.querySelector('#w-editor-content .filing-panel'), null, { timeout: 10000 }).catch(() => {});
    await page.waitForTimeout(500);
    const after = (await childrenOf(page, cust)).map((c) => c.ID);
    const toast = (await page.locator('#w-toast-host').textContent().catch(() => '')) || '';
    check('③ 「社名の直下に入れる」なら社名の直下に入る', after.includes(part), after.join(','));
    check('③ 知らせの行き先は「社名／製品名称」', toast.includes(CUSTOMER + '／' + NAME), toast.slice(0, 200));

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
