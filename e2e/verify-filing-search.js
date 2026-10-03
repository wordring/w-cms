// 整理の「行き先を探す」と確かめのダイアログ（2026-10-03・ext/toho/filing_search.go・assets/app.js の整理の欄）を画面から確かめる。
//
// 利用者:「図面を追加してもどこに入れるか入力する欄が無いので、『⚠ 行き先に同じ加工製品がありません…』と出ます」
// 「どの加工製品に追加するか人間が指定する必要があります。図面追加で同じ図面が在ったら警告、図面改定で同じ図面が無ければ
// 警告ということになります。警告ダイアログが出て、追加するかやめるか選んではどうでしょうか」。
//
//   ① 図面追加を選ぶと「行き先を探す」が出る（番号も名前も違う二つ目の図面は、機械の候補に出ない）
//   ② 題の一部で探すと既にある加工製品が出て、押すと行き先になる（✓）——実行すると、その加工製品へ図面追加される
//   ③ 同じ図面番号（版の印だけ違う）を図面追加すると、確かめのダイアログ——「やめる」なら入らない、「追加する」なら入る
//   ④ 「何もしない」を選ぶと、実行しても動かさない（このメールの下にそのまま残る）
//
// 当て先は全部自分で作って最後に消します（取引先の下の【E2E】の会社・通信箱の下の記録）。本物の加工製品には触りません。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-filing-search.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, findMailbox, childrenOf, bodyOf } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

const CUSTOMER = '【E2E】探す会社';
const MACHINE = '【E2E】探す装置';
const NAME = '【E2E】探す部品';

const drawingBlock = (no, name, machine) =>
  '<section><h2>図面</h2><dl data-type="tags"><dt>図面番号</dt><dd>' + no + '</dd><dt>図面名称</dt><dd>' + name +
  '</dd><dt>装置名称</dt><dd>' + machine + '</dd><dt>客先</dt><dd>' + CUSTOMER + '</dd></dl></section>';

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
    const partners = (top.find(c => (c.Title || '').trim() === '取引先') || {}).ID || '';
    if (!box || !partners) {
      console.log('通信箱か取引先が無いので飛ばします');
      await browser.close();
      process.exit(0);
    }
    const partnersBefore = (await childrenOf(page, partners)).map(c => c.ID).join(',');

    // 既にある加工製品（取引先／【E2E】探す会社／加工製品／【E2E】探す装置／【E2E】探す部品・図面 E2E-FS-1）。
    cust = await makePage(page, '<h1>' + CUSTOMER + '</h1>', partners);
    const prodBox = await makePage(page, '<h1>加工製品</h1>', cust);
    const mach = await makePage(page, '<h1>' + MACHINE + '</h1>', prodBox);
    const first = await makePage(page, '<h1>' + NAME + '</h1>' + drawingBlock('E2E-FS-1', NAME, MACHINE), mach);

    // 受信メールの記録と、解析が作った仮のページ——溶接図（番号も名前も装置名称も違う）。
    record = await makePage(page, '<h1>【E2E】行き先を探す図面</h1><dl data-type="tags"><dt>向き</dt><dd>受信</dd>' +
      '<dt>チャネル</dt><dd>メール</dd><dt>差出人</dt><dd>試験 &lt;e2e-fs@invalid.example&gt;</dd></dl>', box);
    const weld = await makePage(page, '<h1>E2E-FS-9W 溶接</h1>' + drawingBlock('E2E-FS-9W', '【E2E】溶接の図', '【E2E】読めた別の名前'), record);
    check('当て先を作れた', !!first && !!record && !!weld);

    const card = (id) => page.locator('#w-editor-content .filing-card[data-page-id="' + id + '"]');
    const openFiling = async () => {
      await page.goto(BASE + '/' + record);
      await page.locator('#w-editor-content .filing-open').click();
      await card(weld).waitFor({ timeout: 10000 }).catch(() => {});
    };

    // ①
    await openFiling();
    const search = card(weld).locator('.filing-search');
    check('① 新規のときは「行き先を探す」を出さない', !(await search.isVisible()));
    await card(weld).locator('label.filing-merge-opt', { hasText: '図面追加' }).click();
    check('① 図面追加を選ぶと「行き先を探す」が出る', await search.isVisible());

    // ②
    await card(weld).locator('.filing-search-input').fill('探す部');
    const pick = card(weld).locator('.filing-search-pick', { hasText: '/' + first });
    await pick.waitFor({ timeout: 10000 }).catch(() => {});
    check('② 題の一部で既にある加工製品が見つかる', await pick.count() === 1);
    await pick.click();
    await page.waitForFunction((id) => {
      const n = document.querySelector('.filing-card[data-page-id="' + id + '"] .filing-choice-note');
      return n && n.textContent.includes('既にあります');
    }, weld, { timeout: 10000 }).catch(() => {});
    const chosen = (await card(weld).locator('.filing-search-pick.is-chosen').textContent().catch(() => '')) || '';
    check('② 押した行き先に ✓ が付く', chosen.startsWith('✓') && chosen.includes('/' + first), chosen);
    const name = await card(weld).locator('input').evaluateAll((els) => els.map((e) => e.value));
    check('② 欄に行き先の装置名称・題が入る', name.includes(MACHINE) && name.includes(NAME), name.join(' | '));
    await page.locator('#w-editor-content .filing-run').click();
    await page.waitForFunction(() => !document.querySelector('#w-editor-content .filing-panel'), null, { timeout: 10000 }).catch(() => {});
    console.log('   （整理の知らせ: ' + ((await page.locator('#w-toast-host').textContent().catch(() => '')) || '').slice(0, 300) + '）');
    check('② 実行すると、探して押した加工製品へ図面追加される', (await bodyOf(page, first)).includes('E2E-FS-9W'));

    // ③ 同じ図面番号（版の印だけ違う）を図面追加——確かめのダイアログ。
    const same = await makePage(page, '<h1>E2E-FS-1_rev1 ' + NAME + '</h1>' + drawingBlock('E2E-FS-1_rev1', NAME, MACHINE), record);
    await openFiling();
    await card(same).locator('label.filing-merge-opt', { hasText: '図面追加' }).click();
    await page.waitForTimeout(800);
    await page.locator('#w-editor-content .filing-run').click();
    const dlg = page.locator('dialog.w-confirm');
    await dlg.waitFor({ timeout: 10000 }).catch(() => {});
    if (!(await dlg.count())) console.log('   （整理の知らせ: ' + ((await page.locator('#w-toast-host').textContent().catch(() => '')) || '').slice(0, 300) + '）');
    const msg = (await dlg.locator('.w-confirm-msg').textContent().catch(() => '')) || '';
    check('③ 同じ図面が在ると確かめのダイアログが出る', msg.includes('図面追加ですが') && msg.includes('E2E-FS-1'), msg.slice(0, 80));
    const labels = await dlg.locator('button').allTextContents().catch(() => []);
    check('③ ダイアログの釦は「やめる」と「追加する」', labels.join('|') === 'やめる|追加する', labels.join('|'));
    await dlg.locator('button', { hasText: 'やめる' }).click();
    await page.waitForTimeout(800);
    check('③ 「やめる」なら入らない', !(await bodyOf(page, first)).includes('E2E-FS-1_rev1'));
    await page.locator('#w-editor-content .filing-run').click();
    await dlg.waitFor({ timeout: 10000 }).catch(() => {});
    await dlg.locator('button', { hasText: '追加する' }).click();
    await page.waitForFunction(() => !document.querySelector('#w-editor-content .filing-panel'), null, { timeout: 10000 }).catch(() => {});
    check('③ 「追加する」なら入る', (await bodyOf(page, first)).includes('E2E-FS-1_rev1'));

    // ④ 何もしない（2026-10-03 利用者:「図面を解析しても何もしない選択肢も必要です」）——送らず、このメールの下にそのまま残る。
    const keepMe = await makePage(page, '<h1>E2E-FS-SKIP ' + NAME + '</h1>' + drawingBlock('E2E-FS-SKIP', '【E2E】残す図面', '【E2E】残す装置'), record);
    await openFiling();
    await card(keepMe).locator('label.filing-merge-opt', { hasText: '何もしない' }).click();
    const skipNote = (await card(keepMe).locator('.filing-choice-note').textContent().catch(() => '')) || '';
    check('④ 「何もしない」を選ぶと「そのまま残します」と言う', skipNote.includes('そのまま残します'), skipNote);
    await page.locator('#w-editor-content .filing-run').click();
    await page.waitForFunction(() => !document.querySelector('#w-editor-content .filing-panel'), null, { timeout: 10000 }).catch(() => {});
    const kids = (await childrenOf(page, record)).map((c) => c.ID);
    check('④ 実行しても動かさない（このメールの下に残る）', kids.includes(keepMe) && !(await bodyOf(page, first)).includes('E2E-FS-SKIP'), kids.join(','));
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
    await cleanup();
    const partnersAfter = (await childrenOf(page, partners)).map(c => c.ID).join(',');
    check('取引先の子が元どおり', partnersAfter === partnersBefore);
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    await cleanup().catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
