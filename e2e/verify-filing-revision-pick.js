// 整理の「図面改定」で、行き先に図面が2つ以上あるとき、どの図面の改定かを編集者が選ぶ（2026-10-29・ext/toho/revision.go の
// mergeAsRevision・filing.go の needs_drawing・toho.js の runFiling）を画面から確かめる。
//
// 利用者:「これは編集者が選択すると良いと思います」（図面改定で、並べた図面のどれを旧版へ移すか）。
//
//   ① 図面が2つある加工製品へ図面改定すると、どの図面の改定かを選ぶダイアログが出る（図面の番号と名前が並ぶ・やめるもある）
//   ② 「やめる」なら動かさない（加工製品ページはそのまま・仮のページはメールの下に残る）
//   ③ 選ぶと、選んだ図面だけが旧版の子ページへ移り、新しい図面がその場所へ入る（溶接図はそのまま）
//
// 当て先は全部自分で作って最後に消します（製造台帳の下の【E2E】の会社・通信箱の下の記録）。本物の加工製品には触りません。
// 使い方: WCMS_BASE=https://localhost:9443 node verify-filing-revision-pick.js
const { chromium } = require('playwright');
const { login, makePage, findMailbox, childrenOf, bodyOf, deleteTree, topChildID } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

const CUSTOMER = '【E2E】改定を選ぶ会社';
const MACHINE = '【E2E】改定を選ぶ装置';
const NAME = '【E2E】改定を選ぶ部品';

const drawingBlock = (no, name) =>
  '<section><h2>図面</h2><dl data-type="tags"><dt>図面番号</dt><dd>' + no + '</dd><dt>図面名称</dt><dd>' + name +
  '</dd><dt>装置名称</dt><dd>' + MACHINE + '</dd><dt>客先</dt><dd>' + CUSTOMER + '</dd></dl></section>';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let record = '', cust = '';
  const cleanup = async () => {
    if (record) await deleteTree(page, record);
    if (cust) await deleteTree(page, cust);
    record = cust = '';
  };
  try {
    await login(page, BASE);
    const box = await findMailbox(page);
    const partners = await topChildID(page, '製造台帳');
    if (!box || !partners) {
      console.log('通信箱か製造台帳が無いので飛ばします');
      await browser.close();
      process.exit(0);
    }
    const partnersBefore = (await childrenOf(page, partners)).map((c) => c.ID).join(',');

    // 既にある加工製品——部品図 E2E-FR-1 と溶接図 E2E-FR-W の2つが並ぶ。
    cust = await makePage(page, '<h1>' + CUSTOMER + '</h1>', partners);
    const mach = await makePage(page, '<h1>' + MACHINE + '</h1>', cust);
    const first = await makePage(page, '<h1>' + NAME + '</h1>' + drawingBlock('E2E-FR-1', NAME) + drawingBlock('E2E-FR-W', '【E2E】溶接の図'), mach);

    // 受信メールの記録と、解析が作った仮のページ——部品図の改定（版の印だけ違う）。
    record = await makePage(page, '<h1>【E2E】改定を選ぶ図面</h1><dl data-type="tags"><dt>向き</dt><dd>受信</dd>' +
      '<dt>チャネル</dt><dd>メール</dd><dt>差出人</dt><dd>試験 &lt;e2e-fr@invalid.example&gt;</dd></dl><section data-mirror="記録の箱"></section>', box);
    const rev = await makePage(page, '<h1>E2E-FR-1_rev1 ' + NAME + '</h1>' + drawingBlock('E2E-FR-1_rev1', NAME), record);
    check('当て先を作れた', !!first && !!record && !!rev);

    const card = page.locator('#w-editor-content .filing-card[data-page-id="' + rev + '"]');
    const openAndRun = async () => {
      await page.goto(BASE + '/' + record);
      await page.locator('#w-editor-content .filing-open').click();
      await card.waitFor({ timeout: 10000 }).catch(() => {});
      await card.locator('label.filing-merge-opt', { hasText: '図面改定' }).click();
      await card.locator('.filing-search-input').fill('改定を選ぶ部');
      const pick = card.locator('.filing-search-pick', { hasText: '/' + first });
      await pick.waitFor({ timeout: 10000 }).catch(() => {});
      await pick.click();
      await page.waitForTimeout(800);
      await page.locator('#w-editor-content .filing-run').click();
    };
    const dlg = page.locator('dialog.w-confirm');

    // ① ②
    const before = await bodyOf(page, first);
    await openAndRun();
    await dlg.waitFor({ timeout: 10000 }).catch(() => {});
    if (!(await dlg.count())) console.log('   （整理の知らせ: ' + ((await page.locator('#w-toast-host').textContent().catch(() => '')) || '').slice(0, 300) + '）');
    const msg = (await dlg.locator('.w-confirm-msg').textContent().catch(() => '')) || '';
    check('① どの図面の改定かを聞くダイアログが出る', msg.includes('どの図面の改定ですか'), msg.slice(0, 80));
    const labels = await dlg.locator('button').allTextContents().catch(() => []);
    check('① 図面の番号と名前が並び、「やめる」もある',
      labels.some((l) => l.includes('E2E-FR-1') && l.includes(NAME)) && labels.some((l) => l.includes('E2E-FR-W')) && labels.includes('やめる'), labels.join('|'));
    await dlg.locator('button', { hasText: 'やめる' }).click();
    await page.waitForTimeout(800);
    check('② 「やめる」なら加工製品ページは変わらない', (await bodyOf(page, first)) === before);
    check('② 仮のページはメールの下に残る', (await childrenOf(page, record)).map((c) => c.ID).includes(rev));

    // ③
    await openAndRun();
    await dlg.waitFor({ timeout: 10000 }).catch(() => {});
    await dlg.locator('.w-choice-list button', { hasText: 'E2E-FR-1' }).first().click();
    await page.waitForFunction(() => !document.querySelector('#w-editor-content .filing-panel'), null, { timeout: 15000 }).catch(() => {});
    console.log('   （整理の知らせ: ' + ((await page.locator('#w-toast-host').textContent().catch(() => '')) || '').slice(0, 300) + '）');
    const after = await bodyOf(page, first);
    check('③ 新しい図面が入り、溶接図は残る', after.includes('E2E-FR-1_rev1') && after.includes('E2E-FR-W'));
    check('③ 選んだ古い図面は加工製品ページから外れる', !after.includes('<dd>E2E-FR-1</dd>'));
    check('③ 新しい図面は古い図面のいた場所（溶接図より上）', after.indexOf('E2E-FR-1_rev1') < after.indexOf('E2E-FR-W'));
    const kids = await childrenOf(page, first);
    const old = kids.find((c) => (c.Title || '').startsWith('旧版 E2E-FR-1'));
    check('③ 選んだ図面だけが旧版の子ページへ移る', !!old && !kids.some((c) => (c.Title || '').startsWith('旧版 E2E-FR-W')), kids.map((c) => c.Title).join(' | '));
    if (old) check('③ 旧版ページに古い図面がある', (await bodyOf(page, old.ID)).includes('E2E-FR-1'));

    check('JSエラーなし', errs.length === 0, errs.join(' | '));
    await cleanup();
    const partnersAfter = (await childrenOf(page, partners)).map((c) => c.ID).join(',');
    check('製造台帳の子が元どおり', partnersAfter === partnersBefore);
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    await cleanup().catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
