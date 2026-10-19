// 受注残表から製造を始める（2026-10-19・ext/toho/mfg.go・assets/app.js）を画面から確かめる。
//
// 利用者:「受注残表でチェックして製造開始ボタンを押すと、製造フォルダに入るのはどうですか？」
// 「残りの数の一部だけで案件を始めまることがあります」。
//
//   ① 受注残表に「製造」の欄・表の上に「🏭 選んだ行で製造開始」（紙に出さない）
//   ② 何も選ばずに押すと、選ぶように言う
//   ③ 選んで押すと、今回作る数の欄が開く（まだ製造に入っていない数が初めから入っている）——数を直して「🏭 製造を始める」
//   ④ 製造ページが開く——題は品名（装置名が無いので）・製造明細表に受注明細の写しと直した数・置き場はトップ直下の「製造」
//   ⑤ 受注残表に戻ると、その行に「製造中」のリンクと、残りの数（20 − 8 = 12）の選ぶ欄
//
// 当て先は全部自分で作って最後に消します（受注残表を置いたページ・受注ページ・できた製造ページ）。置き場「製造」は消しません。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-mfg-new.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

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

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1400, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let root = '', order = '', mfg = '';
  try {
    await login(page, BASE);
    root = await makePage(page, '<h1>【E2E】案件残から製造</h1><section data-mirror="案件残"></section>');
    order = await makePage(page, '<h1>受注 E2E-MFG</h1><dl data-type="tags"><dt>ページ種類</dt><dd>顧客発注書</dd><dt>発注書番号</dt><dd>E2E-MFG-1</dd>' +
      '<dt>発注元</dt><dd>【E2E】製造の客先</dd><dt>発注日</dt><dd>2026-10-19</dd><dt>納期</dt><dd>2026-12-31</dd></dl>' +
      '<table><caption>案件明細表</caption><tbody><tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th><th>単価</th><th>状態</th></tr>' +
      '<tr><td></td><td>E2E-MFG-A</td><td>【E2E】製造の部品</td><td>20</td><td>個</td><td>1500</td><td>未着手</td></tr></tbody></table>', root);
    check('当て先を作れた', !!root && !!order);

    await page.goto(BASE + '/' + root);
    await page.waitForSelector('#w-editor-content .backlog-table', { timeout: 10000 }).catch(() => {});
    const row = '#w-editor-content tr[data-order-page="' + order + '"]';
    // ①
    const head = await page.evaluate(() => {
      const th = Array.from(document.querySelectorAll('#w-editor-content .backlog-table th')).find((x) => x.textContent.trim() === '製造');
      return th ? th.className : null;
    });
    check('① 案件残表に「製造」の欄（紙に出さない印）', head !== null && head.includes('no-print'), String(head));
    check('① 表の上に「🏭 選んだ行で製造開始」', (await page.locator('#w-editor-content .backlog-mfg-go').count()) === 1);
    // ②
    await page.click('#w-editor-content .backlog-mfg-go');
    await page.waitForTimeout(300);
    const say = (await page.textContent('#w-editor-content .backlog-mfg-say').catch(() => '')) || '';
    check('② 何も選ばずに押すと、選ぶように言う', /選んでください/.test(say), say);
    // ③
    await page.locator(row + ' .backlog-mfg-pick').check();
    await page.click('#w-editor-content .backlog-mfg-go');
    const qty = page.locator('#w-editor-content .backlog-mfg-panel .backlog-mfg-qty');
    await qty.waitFor({ timeout: 5000 }).catch(() => {});
    check('③ 今回作る数の欄が開き、まだ製造に入っていない数（20）が入っている', (await qty.inputValue().catch(() => '')) === '20');
    await qty.fill('8');
    await Promise.all([
      page.waitForURL((u) => !u.pathname.endsWith('/' + root), { timeout: 15000 }).catch(() => {}),
      page.click('#w-editor-content .backlog-mfg-make'),
    ]);
    mfg = (page.url().match(/\/(\d{6})(?:[?#]|$)/) || [])[1] || '';
    check('④ 製造ページが開く', !!mfg && mfg !== root, page.url());
    if (mfg) {
      const raw = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).text(), mfg);
      check('④ 題は品名（装置名が無いので）', raw.includes('<h1>【E2E】製造の部品</h1>'), raw.slice(0, 120));
      check('④ 製造明細表に案件明細の写しと直した数（8）', raw.includes('<caption>製造明細表</caption>') && raw.includes('E2E-MFG-A') &&
        />\s*8\s*</.test(raw) && raw.includes(order) && raw.includes('<dd>製造</dd>'), raw.replace(/\s+/g, ' ').slice(0, 500));
      const p1 = await parentOf(page, mfg);
      const p2 = await parentOf(page, p1);
      check('④ 置き場はトップ直下の「製造」', (await titleOf(page, p1)) === '製造' && p2 === '000000', p1 + ' ' + p2);
      // ⑤
      await page.goto(BASE + '/' + root);
      await page.waitForSelector(row, { timeout: 10000 }).catch(() => {});
      const cell = await page.evaluate((sel) => {
        const tr = document.querySelector(sel);
        const link = tr && tr.querySelector('a.backlog-mfg-link');
        const pick = tr && tr.querySelector('.backlog-mfg-pick');
        return { link: link ? link.getAttribute('href') : null, free: pick ? pick.dataset.free : null };
      }, row);
      check('⑤ 案件残表に「製造中」のリンクと、残りの 12', cell.link === '/' + mfg && cell.free === '12', JSON.stringify(cell));
    }
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    if (mfg) await deletePage(page, mfg).catch(() => {});
    if (order) await deletePage(page, order).catch(() => {});
    if (root) await deletePage(page, root).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
