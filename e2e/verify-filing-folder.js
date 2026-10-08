// 整理の「装置のページへ」（2026-10-08・ext/toho/filing_folder.go・assets/app.js の整理の欄）を画面から確かめる。
//
// 利用者:「メールの整理について、図面をフォルダページに追加したいのですが、出来ません」（組立図）。
//
//   ① 「装置のページへ」を選ぶと、どの装置のページへ足すかを言う（加工製品ページは作らない）
//   ② 実行すると装置のページに図面が入り、加工製品ページはできず、解析で作ったページは消える
//   ③ 装置のページに同じファイルの表示が既にあると確かめのダイアログ——「やめる」なら入らない、「足す」なら入る
//   ④ 装置名称が既にある装置でなければ断る（作らない・解析で作ったページは残る）
//
// 当て先は全部自分で作って最後に消します（取引先の下の【E2E】の会社・通信箱の下の記録）。本物の加工製品には触りません。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-filing-folder.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, childrenOf, bodyOf, findMailbox } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const CUSTOMER = '【E2E】装置の会社';
const MACHINE = '【E2E】装置フォルダ';
const drawingBlock = (no, name, machine, ref) =>
  '<section><h2>図面</h2><dl data-type="tags"><dt>図面番号</dt><dd>' + no + '</dd><dt>図面名称</dt><dd>' + name +
  '</dd><dt>装置名称</dt><dd>' + machine + '</dd><dt>客先</dt><dd>' + CUSTOMER + '</dd></dl>' +
  (ref ? '<section data-type="file-view" data-ref="' + ref + '"></section>' : '') + '</section>';

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

    // 既にある装置のページ（取引先／【E2E】装置の会社／加工製品／【E2E】装置フォルダ）——中に手で貼ったファイル表示が1つ。
    cust = await makePage(page, '<h1>' + CUSTOMER + '</h1>', partners);
    const prodBox = await makePage(page, '<h1>加工製品</h1>', cust);
    const folder = await makePage(page, '<h1>' + MACHINE + '</h1><p>装置の説明</p><section data-type="file-view" data-ref="000000-zzzz"></section>', prodBox);

    // 受信メールの記録と、解析が作った仮のページ——組立図。
    record = await makePage(page, '<h1>【E2E】装置のページへ</h1><dl data-type="tags"><dt>向き</dt><dd>受信</dd>' +
      '<dt>チャネル</dt><dd>メール</dd><dt>差出人</dt><dd>試験 &lt;e2e-ff@invalid.example&gt;</dd></dl>', box);
    const asm = await makePage(page, '<h1>E2E-FF-00A 組立図</h1>' + drawingBlock('E2E-FF-00A', '【E2E】組立図', MACHINE), record);
    check('当て先を作れた', !!folder && !!record && !!asm);

    const card = (id) => page.locator('#w-editor-content .filing-card[data-page-id="' + id + '"]');
    const openFiling = async (id) => {
      await page.goto(BASE + '/' + record);
      await page.locator('#w-editor-content .filing-open').click();
      await card(id).waitFor({ timeout: 10000 }).catch(() => {});
    };
    const toast = async () => ((await page.locator('#w-toast-host').textContent().catch(() => '')) || '');

    // ①
    await openFiling(asm);
    await card(asm).locator('label.filing-merge-opt', { hasText: '装置のページへ' }).click();
    const note = (await card(asm).locator('.filing-choice-note').textContent().catch(() => '')) || '';
    check('① 「装置のページへ」を選ぶと、どの装置のページへ足すかを言う', note.includes('装置「' + MACHINE + '」のページ') && note.includes('加工製品ページは作りません'), note);

    // ②
    await page.locator('#w-editor-content .filing-run').click();
    await page.waitForFunction(() => !document.querySelector('#w-editor-content .filing-panel'), null, { timeout: 10000 }).catch(() => {});
    const t2 = await toast();
    const fbody = await bodyOf(page, folder);
    const kids = (await childrenOf(page, folder)).map((c) => c.ID);
    const gone = await page.evaluate(async (id) => (await fetch('/api/load?id=' + id)).status, asm);
    check('② 装置のページに図面が入る', fbody.includes('E2E-FF-00A') && fbody.includes('装置の説明'), fbody.replace(/\s+/g, ' ').slice(0, 200));
    check('② 加工製品ページはできず、解析で作ったページは消える', kids.length === 0 && gone === 404, JSON.stringify({ kids, gone }));
    check('② 知らせに行き先', t2.includes(CUSTOMER + '／加工製品／' + MACHINE), t2.slice(0, 200));

    // ③ 同じファイルの表示が既にある（装置のページに手で貼った 000000-zzzz）。
    const same = await makePage(page, '<h1>E2E-FF-00B 組立図2</h1>' + drawingBlock('E2E-FF-00B', '【E2E】組立図2', MACHINE, '000000-zzzz'), record);
    await openFiling(same);
    await card(same).locator('label.filing-merge-opt', { hasText: '装置のページへ' }).click();
    await page.locator('#w-editor-content .filing-run').click();
    const dlg = page.locator('dialog.w-confirm');
    await dlg.waitFor({ timeout: 10000 }).catch(() => {});
    const msg = (await dlg.locator('.w-confirm-msg').textContent().catch(() => '')) || '';
    check('③ 同じファイルの表示が在ると確かめのダイアログが出る', msg.includes('既にあります') && msg.includes('2つになります'), msg.slice(0, 120));
    await dlg.locator('button', { hasText: 'やめる' }).click().catch(() => {});
    await page.waitForTimeout(800);
    check('③ 「やめる」なら入らない', !(await bodyOf(page, folder)).includes('E2E-FF-00B'));
    await page.locator('#w-editor-content .filing-run').click();
    await dlg.waitFor({ timeout: 10000 }).catch(() => {});
    await dlg.locator('button', { hasText: '足す' }).click().catch(() => {});
    await page.waitForFunction(() => !document.querySelector('#w-editor-content .filing-panel'), null, { timeout: 10000 }).catch(() => {});
    check('③ 「足す」なら入る', (await bodyOf(page, folder)).includes('E2E-FF-00B'));

    // ④ 装置名称が既にある装置ではない。
    const lost = await makePage(page, '<h1>E2E-FF-00C 組立図3</h1>' + drawingBlock('E2E-FF-00C', '【E2E】組立図3', '【E2E】無い装置'), record);
    await openFiling(lost);
    await card(lost).locator('label.filing-merge-opt', { hasText: '装置のページへ' }).click();
    await page.locator('#w-editor-content .filing-run').click();
    await page.waitForTimeout(1500);
    const t4 = await toast();
    const kids4 = (await childrenOf(page, record)).map((c) => c.ID);
    check('④ 既にある装置でなければ断る（解析で作ったページは残る）', t4.includes('ありません') && kids4.includes(lost), t4.slice(0, 200));
    const prodKids = (await childrenOf(page, prodBox)).map((c) => c.ID);
    check('④ 装置のページを作らない', prodKids.length === 1 && prodKids[0] === folder, prodKids.join(','));

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
