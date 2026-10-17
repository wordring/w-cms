// 受注残表から納品書を作る（2026-10-17・ext/toho/delivery.go・assets/app.js）を画面から確かめる。
//
// 利用者:「分納はよくあります。」「納品書をまとめることはあります。まとめない場合もあります。」。
//
//   ① 受注残表の右端に「納品」の欄・表の上に「🚚 選んだ行で納品書を作る」（紙に出さない）
//   ② 何も選ばずに押すと、選ぶように言う
//   ③ 選んで押すと、今回納める数の欄が開く（残りが初めから入っている）——数を直して「🚚 納品書を作る」
//   ④ 納品書ページが開く——題は【弊社】納品書　<客先>・明細は受注明細の写しで数は直した数・置き場は 記録／年／月／製造記録
//
// 当て先は全部自分で作って最後に消します（受注残表を置いたページ・受注ページ・できた納品書と、試験が作った空のフォルダ）。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-delivery-new.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, childrenOf } = require('./lib');
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
  let root = '', order = '', dlv = '';
  try {
    await login(page, BASE);
    root = await makePage(page, '<h1>【E2E】受注残から納品書</h1><section data-mirror="受注残"></section>');
    order = await makePage(page, '<h1>受注 E2E-DLV</h1><dl data-type="tags"><dt>ページ種類</dt><dd>顧客発注書</dd><dt>発注書番号</dt><dd>E2E-DLV-1</dd>' +
      '<dt>発注元</dt><dd>【E2E】納品先</dd><dt>発注日</dt><dd>2026-10-17</dd><dt>納期</dt><dd>2026-12-31</dd></dl>' +
      '<table><caption>受注明細表</caption><tbody><tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th><th>単価</th><th>出荷済数量</th><th>状態</th></tr>' +
      '<tr><td></td><td>E2E-DLV-A</td><td>【E2E】部品A</td><td>20</td><td>個</td><td>1500</td><td></td><td>未着手</td></tr></tbody></table>', root);
    check('当て先を作れた', !!root && !!order);

    await page.goto(BASE + '/' + root);
    await page.waitForSelector('#w-editor-content .backlog-table', { timeout: 10000 }).catch(() => {});
    const sheet = page.locator('#w-editor-content section.backlog-sheet', { hasText: '【E2E】納品先' });
    // ①
    const head = await page.evaluate(() => {
      const th = Array.from(document.querySelectorAll('#w-editor-content .backlog-table th')).find((x) => x.textContent.trim() === '納品');
      return th ? th.className : null;
    });
    check('① 受注残表の右端に「納品」の欄（紙に出さない印）', head !== null && head.includes('no-print'), String(head));
    check('① 表の上に「🚚 選んだ行で納品書を作る」', (await page.locator('#w-editor-content .backlog-dlv-go').count()) === 1);
    // ②
    await page.click('#w-editor-content .backlog-dlv-go');
    await page.waitForTimeout(300);
    const say = (await page.textContent('#w-editor-content .backlog-dlv-say').catch(() => '')) || '';
    check('② 何も選ばずに押すと、選ぶように言う', /選んでください/.test(say), say);
    // ③
    await sheet.locator('.backlog-dlv-pick').check();
    await page.click('#w-editor-content .backlog-dlv-go');
    const qty = page.locator('#w-editor-content .backlog-dlv-panel .backlog-dlv-qty');
    await qty.waitFor({ timeout: 5000 }).catch(() => {});
    check('③ 今回納める数の欄が開き、残り（20）が入っている', (await qty.inputValue().catch(() => '')) === '20');
    await qty.fill('8');
    await Promise.all([
      page.waitForURL((u) => !u.pathname.endsWith('/' + root), { timeout: 15000 }).catch(() => {}),
      page.click('#w-editor-content .backlog-dlv-make'),
    ]);
    dlv = (page.url().match(/\/(\d{6})(?:[?#]|$)/) || [])[1] || '';
    check('④ 納品書ページが開く', !!dlv && dlv !== root, page.url());
    if (dlv) {
      const raw = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).text(), dlv);
      check('④ 題は「【弊社】納品書　<客先>」', raw.includes('<h1>【弊社】納品書　【E2E】納品先</h1>'), raw.slice(0, 120));
      check('④ 明細は受注明細の写し・数は直した数（8）', raw.includes('<caption>納品明細表</caption>') && raw.includes('E2E-DLV-A') &&
        raw.includes('【E2E】部品A') && />\s*8\s*</.test(raw) && raw.includes('1500') && raw.includes('E2E-DLV-1'), raw.replace(/\s+/g, ' ').slice(0, 400));
      const chain = [];
      let cur = dlv;
      for (let i = 0; i < 4; i++) { cur = await parentOf(page, cur); chain.push(await titleOf(page, cur)); }
      check('④ 置き場は 記録／年／月／製造記録', chain[0] === '製造記録' && /^\d{2}月$/.test(chain[1]) && /^\d{4}年$/.test(chain[2]) && chain[3] === '記録', chain.join(' ← '));

      // ⑤ 紙（2026-10-17・delivery_pdf.go）——足元に「状態: 未発行」。「📄 PDFを作る」で PDF がページに出て、発行日に今日・「発行済」。
      await page.goto(BASE + '/' + dlv);
      const state0 = (await page.textContent('#w-editor-content .delivery-state').catch(() => '')) || '';
      check('⑤ 作ったときは「未発行」', state0.includes('未発行'), state0);
      await Promise.all([
        page.waitForNavigation({ timeout: 20000 }).catch(() => {}),
        page.locator('#w-editor-content .delivery-pdf-go').click(),
      ]);
      await page.waitForSelector('#w-editor-content section[data-type="file-view"]', { timeout: 15000 }).catch(() => {});
      const fv = await page.locator('#w-editor-content section[data-type="file-view"]').getAttribute('data-ref').catch(() => '');
      const state1 = (await page.textContent('#w-editor-content .delivery-state').catch(() => '')) || '';
      check('⑤ PDFを作ると、ページに出て「発行済」', !!fv && fv.startsWith(dlv + '-') && state1.includes('発行済'), (fv || '') + ' ' + state1);
    }
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    if (dlv) {
      let f = await parentOf(page, dlv).catch(() => '');
      await deletePage(page, dlv).catch(() => {});
      for (let i = 0; i < 3 && f && Number(f) > Number(order || 0); i++) {
        if ((await childrenOf(page, f).catch(() => [1])).length !== 0) break;
        const up = await parentOf(page, f).catch(() => '');
        await deletePage(page, f).catch(() => {});
        f = up;
      }
    }
    if (order) await deletePage(page, order).catch(() => {});
    if (root) await deletePage(page, root).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
