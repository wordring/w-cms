// 受注残表から見積依頼へ送れるかを**ブラウザから**確かめる（2026-10-04・見積依頼の段3）。
//
// 【要求】見積依頼 §2「受注フォルダの受注残表から集める——選んだものだけ」「数は受注残の数量そのまま（数量 − 出荷済み）× 部材の数量」
// 「⚠ 受注残表は1枚ずつ印刷する表——選ぶ印を紙に出さないこと」。
//
//   ① 受注残表の右端に「見積依頼」の列——選ぶ欄は弊社品番・受注残・受注ページを持つ／弊社品番が空の行は選べない
//   ② 紙（印刷）には選ぶ列もボタンも出ない
//   ③ 何も選ばずに押すと、選ぶように言う
//   ④ 選んで押すと /api/rfq/collect へ {items:[{product, lot＝受注残, for_order}]}——送り先は送らない（サーバーが置き場を探す）・
//      結果の一言と「見積依頼を開く」・選んだ印は外れる
//
// ⚠ **本物の見積依頼の置き場には書かない**——送る口（/api/rfq/collect）は試験のブラウザの中で止め、送った中身だけを見る。
// 受注残表は自分で作ったページ（印「受注残」）の下に置いた受注ページで出す。作ったページは最後に消す。
//
// 使い方: WCMS_BASE=https://localhost:8443 node verify-backlog-rfq.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(e.message));
  await login(page, BASE);

  let bad = 0;
  const check = (ok, msg, detail) => {
    if (ok) console.log('✓ ' + msg);
    else { console.log('✗ ' + msg + (detail ? '（' + detail + '）' : '')); bad++; }
  };

  const root = await makePage(page, '<h1>【E2E】受注残から見積依頼</h1><section data-mirror="受注残"></section>');
  let order = '';
  const sent = [];
  const run = async () => {
    if (!root) { check(false, '当て先のページを作れません'); return; }
    order = await makePage(page, '<h1>受注 E2E-1</h1><dl data-type="tags"><dt>発注書番号</dt><dd>E2E-1</dd><dt>発注元</dt><dd>E2E商店</dd>' +
      '<dt>発注日</dt><dd>2026-10-04</dd><dt>納期</dt><dd>2026-10-31</dd></dl>' +
      '<table><caption>受注明細</caption><tbody><tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>出荷済み</th><th>状態</th></tr>' +
      `<tr><td>${root}</td><td>E2E-BL-1</td><td>E2E部品</td><td>10</td><td>3</td><td>未着手</td></tr>` +
      '<tr><td></td><td>E2E-BL-NONE</td><td>E2E部品2</td><td>5</td><td></td><td>未着手</td></tr></tbody></table>', root);
    if (!order) { check(false, '受注ページを作れません'); return; }
    await page.route('**/api/rfq/collect', async (route) => {
      sent.push(JSON.parse(route.request().postData() || '{}'));
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ success: true, rows: 2, page_id: '000050', skipped: [] }) });
    });
    await page.goto(BASE + '/' + root);
    await page.waitForSelector('#w-editor-content .backlog-table', { timeout: 8000 }).catch(() => {});

    // ① 列と選ぶ欄
    const head = await page.evaluate(() => {
      const th = Array.from(document.querySelectorAll('#w-editor-content .backlog-table th')).find((x) => x.textContent.trim() === '見積依頼');
      return th ? th.className : null;
    });
    check(head !== null && head.includes('no-print'), '受注残表の右端に「見積依頼」の列（紙に出さない印）', String(head));
    const picks = await page.evaluate(() => Array.from(document.querySelectorAll('#w-editor-content .backlog-table input[type=checkbox]'))
      .filter((c) => c.classList.contains('backlog-rfq-pick') || c.title.includes('見積依頼'))
      .map((c) => ({ enabled: !c.disabled, product: c.dataset.product || '', lot: c.dataset.lot || '', order: c.dataset.order || '', title: c.title })));
    const ok = picks.find((p) => p.enabled);
    check(!!ok && ok.product === root && ok.lot === '7' && ok.order === order, '選ぶ欄は弊社品番・受注残（10 − 3 = 7）・受注ページを持つ', JSON.stringify(picks));
    check(picks.some((p) => !p.enabled && /弊社品番が空/.test(p.title)), '弊社品番が空の行は選べない（理由を出す）', JSON.stringify(picks));

    // ② 紙には出ない——受注残表の 🖨 は1枚の写しを見えない枠（#w-print-frame）に置いて刷る。枠の中で、印刷の見た目のとき
    //    選ぶ列が消えること・送るボタン（表の外）は写しに入らないことを見る。
    await page.click('#w-editor-content .backlog-print');
    await page.waitForFunction(() => {
      const f = document.getElementById('w-print-frame');
      return f && f.getAttribute('data-printed') === '1';
    }, null, { timeout: 5000 }).catch(() => {});
    await page.emulateMedia({ media: 'print' });
    const printed = await page.evaluate(() => {
      const f = document.getElementById('w-print-frame');
      const d = f && f.contentDocument;
      if (!d) return null;
      const th = Array.from(d.querySelectorAll('#w-print-area th')).find((x) => x.textContent.trim() === '見積依頼');
      const pick = d.querySelector('#w-print-area .backlog-rfq-pick');
      return { th: th ? d.defaultView.getComputedStyle(th).display : 'なし', pick: pick ? d.defaultView.getComputedStyle(pick.closest('td')).display : 'なし',
        bar: !!d.querySelector('#w-print-area .backlog-rfq-bar') };
    });
    await page.emulateMedia({ media: 'screen' });
    check(!!printed && printed.th === 'none' && printed.pick === 'none' && !printed.bar, '紙（受注残表の 🖨）には選ぶ列も送るボタンも出ない', JSON.stringify(printed));

    // ③ 選ばずに押す
    await page.click('#w-editor-content .backlog-rfq-go');
    await page.waitForTimeout(300);
    let say = await page.textContent('#w-editor-content .backlog-rfq-say');
    check(/選んでください/.test(say) && sent.length === 0, '何も選ばずに押すと選ぶように言う（送らない）', say);

    // ④ 選んで押す
    await page.check('#w-editor-content .backlog-rfq-pick');
    await page.click('#w-editor-content .backlog-rfq-go');
    await page.waitForFunction(() => /行入れました/.test((document.querySelector('#w-editor-content .backlog-rfq-say') || {}).textContent || ''), null, { timeout: 5000 }).catch(() => {});
    const body = sent[0] || {};
    check(sent.length === 1 && !body.page_id && JSON.stringify(body.items) === JSON.stringify([{ product: root, lot: '7', for_order: order }]),
      '選んだ行を {items:[{product, lot＝受注残, for_order}]} で送る（送り先は送らない）', JSON.stringify(sent));
    say = await page.textContent('#w-editor-content .backlog-rfq-say');
    const link = await page.getAttribute('#w-editor-content .backlog-rfq-say a', 'href').catch(() => null);
    check(/2 行入れました/.test(say) && link === '/000050', '結果の一言と「見積依頼を開く」', say + ' ' + link);
    check(!(await page.isChecked('#w-editor-content .backlog-rfq-pick')), '送ったら選んだ印は外れる');
  };
  try {
    await run();
  } finally {
    if (order) await deletePage(page, order);
    if (root) {
      await deletePage(page, root);
      const gone = await page.evaluate(async (ids) => Promise.all(ids.map(async (pid) => (await fetch('/api/load?id=' + pid)).status)), [root, order].filter(Boolean));
      check(gone.every((s) => s === 404), '作ったページを消した', gone.join(','));
    }
  }
  check(errs.length === 0, 'ページのエラーが無い', errs.join(' / '));
  await browser.close();
  console.log(bad ? `✗ ${bad} 件` : '✓ すべて合格');
  process.exit(bad ? 1 : 0);
})();
