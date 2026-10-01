// 整理で、同じ品名（図面名称）の別の品物を「新規」で置ける・同じ題が2枚あるときは行き先を押して図面追加する
// （2026-10-01・ext/toho/filing.go・assets/app.js の整理の欄）を画面から確かめる。
//
// 利用者:「メールで図面を解析して整理するときに、品名がかぶると加工製品ページが追加できないようですが、実際には同じ品名があります」。
//
//   ① 行き先に同じ題の加工製品があっても「新規」を選べる（既定では選ばれていない）——選んで実行すると隣に置かれる
//   ② 同じ題が2枚になったあと、図面追加では「行き先」のボタンが2つ出て、押すまで「下から行き先を押して」と言う
//   ③ 押した方（2枚目）へ図面追加され、1枚目には入らない
//
// 当て先は全部自分で作って最後に消します（取引先の下の【E2E】の会社・通信箱の下の記録）。本物の加工製品には触りません。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-filing-same-name.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, findMailbox, childrenOf, bodyOf } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

const CUSTOMER = '【E2E】同名の会社';
const MACHINE = '【E2E】同名の装置';
const NAME = '【E2E】取付ベース';

const drawingBlock = (no, name) =>
  '<section><h2>図面</h2><dl data-type="tags"><dt>図面番号</dt><dd>' + no + '</dd><dt>図面名称</dt><dd>' + name +
  '</dd><dt>装置名称</dt><dd>' + MACHINE + '</dd><dt>客先</dt><dd>' + CUSTOMER + '</dd></dl></section>';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let record = '', cust = '';
  // cleanup は作ったものを子から消します（何度呼んでもよい）。
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

    // 既にある加工製品（取引先／【E2E】の会社／加工製品／【E2E】装置／【E2E】取付ベース）。
    cust = await makePage(page, '<h1>' + CUSTOMER + '</h1>', partners);
    const prodBox = await makePage(page, '<h1>加工製品</h1>', cust);
    const mach = await makePage(page, '<h1>' + MACHINE + '</h1>', prodBox);
    const first = await makePage(page, '<h1>' + NAME + '</h1>' + drawingBlock('E2E-SN-1', NAME), mach);

    // 受信メールの記録と、解析が作ったのと同じ形の仮のページ（同じ品名・別の図面番号）。
    record = await makePage(page, '<h1>【E2E】同じ品名の図面</h1><dl data-type="tags"><dt>向き</dt><dd>受信</dd>' +
      '<dt>チャネル</dt><dd>メール</dd><dt>差出人</dt><dd>試験 &lt;e2e-sn@invalid.example&gt;</dd></dl>', box);
    const other = await makePage(page, '<h1>E2E-SN-2 ' + NAME + '</h1>' + drawingBlock('E2E-SN-2', NAME), record);
    check('当て先を作れた', !!first && !!record && !!other);

    const card = (id) => page.locator('#w-editor-content .filing-card[data-page-id="' + id + '"]');
    const note = (id) => card(id).locator('.filing-choice-note');
    const openFiling = async () => {
      await page.goto(BASE + '/' + record);
      await page.locator('#w-editor-content .filing-open').click();
    };

    // ①
    await openFiling();
    await page.waitForFunction((id) => {
      const n = document.querySelector('.filing-card[data-page-id="' + id + '"] .filing-choice-note');
      return n && n.textContent.includes('既にあります');
    }, other, { timeout: 10000 }).catch(() => {});
    const newOpt = card(other).locator('input[value="new"]');
    check('① 同じ題があっても「新規」を選べる（既定では選ばれていない）', await newOpt.isEnabled() && !(await newOpt.isChecked()));
    await card(other).locator('label.filing-merge-opt', { hasText: '新規' }).click();
    const n1 = (await note(other).textContent()) || '';
    check('① 新規を選ぶと「別の品物として隣に置く」と言う', n1.includes('別の品物として'), n1);
    await page.locator('#w-editor-content .filing-run').click();
    await page.waitForFunction(() => !document.querySelector('#w-editor-content .filing-panel'), null, { timeout: 10000 }).catch(() => {});
    const siblings = (await childrenOf(page, mach)).filter(c => (c.Title || '').trim() === NAME).map(c => c.ID);
    check('① 実行すると隣に置かれ、同じ題が2枚になる', siblings.length === 2 && siblings.includes(other), siblings.join(','));

    // ② 同じ品名の溶接図が届いた——2枚目へ図面追加したい。
    const weld = await makePage(page, '<h1>E2E-SN-2W ' + NAME + '溶接</h1>' + drawingBlock('E2E-SN-2W', NAME), record);
    await openFiling();
    await card(weld).locator('.filing-same-pick').first().waitFor({ timeout: 10000 }).catch(() => {});
    check('② 行き先のボタンが2つ出る', await card(weld).locator('.filing-same-pick').count() === 2);
    await card(weld).locator('label.filing-merge-opt', { hasText: '図面追加' }).click();
    const n2 = (await note(weld).textContent()) || '';
    check('② 押すまで「下から行き先を押して」と言う', n2.includes('行き先を押してください'), n2);
    await card(weld).locator('.filing-same-pick', { hasText: '/' + other }).click();
    await page.waitForFunction((id) => {
      const b = document.querySelector('.filing-card[data-page-id="' + id + '"] .filing-same-pick.is-chosen');
      return !!b;
    }, weld, { timeout: 10000 }).catch(() => {});
    const chosen = (await card(weld).locator('.filing-same-pick.is-chosen').textContent().catch(() => '')) || '';
    check('② 押した行き先に ✓ が付く', chosen.includes('/' + other) && chosen.startsWith('✓'), chosen);

    // ③
    await page.locator('#w-editor-content .filing-run').click();
    await page.waitForFunction(() => !document.querySelector('#w-editor-content .filing-panel'), null, { timeout: 10000 }).catch(() => {});
    const ob = await bodyOf(page, other);
    const fb = await bodyOf(page, first);
    check('③ 押した2枚目に図面追加され、1枚目には入らない', ob.includes('E2E-SN-2W') && !fb.includes('E2E-SN-2W'));
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
