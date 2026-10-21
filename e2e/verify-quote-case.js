// 見積の依頼を案件にする（2026-10-19・ext/toho/quote_case.go）を画面から確かめる。
//
// 利用者:「見積もりは、見積もりを受注したということにして、受注ページに並べてそこから作るのはどうですか？」
//
//   ① 受信の記録の「作るページ」に「見積依頼ページ」——作ると、見積依頼ページ（ページ種類：顧客見積依頼）が 記録／年／月／依頼 にできて開く
//   ② 案件残表に「見積」として並ぶ・納品の欄は押せない・製造の欄は選べる
//   ③ 案件ページの足元は「見積の依頼の案件です」（受注残高は出さない）
//   ④ 製造を始めて、製造ページの製造明細表の下の「📄 見積書」——見積書は 記録／年／月／依頼 にでき（2026-10-21 から）、「この製造の書類」に並び、行に案件が写る（quote_done.go）
//   ⑤ 見積書に送付日を付けると、案件残表から外れ、案件ページの足元に「完了」と出る
//
// 当て先は全部自分で作って最後に消します（記録・見積依頼ページ・案件残表を置いたページ）。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-quote-case.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, findMailbox } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const titleOf = (page, id) => page.evaluate(async (pid) => {
  const m = (await (await fetch('/api/load?id=' + pid)).text()).match(/<h1[^>]*>([^<]*)<\/h1>/);
  return m ? m[1] : '';
}, id);
const parentOf = (page, id) => page.evaluate(async (pid) => (await (await fetch('/api/page-meta?id=' + pid)).json()).parent_id, id);

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1400, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let mail = '', quote = '', root = '', mfg = '', est = '';
  try {
    await login(page, BASE);
    const box = await findMailbox(page);
    if (!box) { console.log('通信箱が無いので飛ばします'); await browser.close(); process.exit(0); }
    mail = await makePage(page, '<h1>【E2E】見積のお願い</h1><dl data-type="tags"><dt>向き</dt><dd>受信</dd><dt>チャネル</dt><dd>メール</dd>' +
      '<dt>差出人</dt><dd>試験 &lt;e2e@invalid.example&gt;</dd></dl><section><h2>本文</h2><pre>図面の品を 50 個で見積もってください。</pre></section>' +
      // 「作るページ」は記録の箱の中に出る（2026-10-20 から・印の無い記録には出ない——ext/comm/record_box.go）。
      '<section data-mirror="記録の箱"></section>', box);
    check('当て先を作れた', !!mail);

    // ①
    await page.goto(BASE + '/' + mail);
    const sel = page.locator('#w-editor-content .record-make-kind');
    await sel.waitFor({ timeout: 8000 }).catch(() => {});
    await sel.selectOption('見積依頼ページ');
    await Promise.all([
      page.waitForURL((u) => !u.pathname.endsWith('/' + mail), { timeout: 15000 }).catch(() => {}),
      page.locator('#w-editor-content .record-make-go').click(),
    ]);
    quote = (page.url().match(/\/(\d{6})(?:[?#]|$)/) || [])[1] || '';
    check('① 見積依頼ページができて開く', !!quote && quote !== mail, page.url());
    if (quote) {
      const raw = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).text(), quote);
      check('① 題は「【顧客】見積依頼　<件名>」・種類は顧客見積依頼・受信元は記録', raw.includes('<h1>【顧客】見積依頼　【E2E】見積のお願い</h1>') &&
        />\s*顧客見積依頼\s*</.test(raw) && raw.includes(mail), raw.replace(/\s+/g, ' ').slice(0, 400));
      const p1 = await parentOf(page, quote);
      check('① 置き場は 記録／年／月／依頼', (await titleOf(page, p1)) === '依頼', p1);
      // 案件明細表に1行書く（数量 50）——保存の口で（画面の打ち込みは試験の外）。
      const body = raw;
      // ⚠ 読んだ本文には鏡の足元（tfoot）が付いているので、案件明細表の </tbody> の手前に足す。
      const at = body.indexOf('</tbody>', body.indexOf('<caption>案件明細表</caption>'));
      const withRow = body.slice(0, at) + '<tr><td></td><td>E2E-QC-1</td><td>【E2E】見積の品</td><td>50</td><td>個</td><td></td><td></td><td></td><td></td><td>未着手</td></tr>' + body.slice(at);
      const saved = await page.evaluate(async (arg) => {
        const lr = await fetch('/api/lock?id=' + arg.id, { method: 'POST' });
        const lj = await lr.json().catch(() => ({}));
        const sr = await fetch('/api/save', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ page_id: arg.id, html: arg.html, token: lj.token || '' }) });
        await fetch('/api/unlock?id=' + arg.id + '&token=' + encodeURIComponent(lj.token || ''), { method: 'POST' });
        return sr.status;
      }, { id: quote, html: withRow });
      check('案件明細表に1行書けた', saved === 200, String(saved));

      // ②
      root = await makePage(page, '<h1>【E2E】見積の案件残</h1><section data-mirror="案件残"></section>');
      await page.goto(BASE + '/' + root);
      const row = '#w-editor-content tr[data-order-page="' + quote + '"]';
      await page.waitForSelector(row, { timeout: 10000 }).catch(() => {});
      const r = await page.evaluate((sel) => {
        const tr = document.querySelector(sel);
        if (!tr) return null;
        return {
          label: [...tr.querySelectorAll('a')].map((a) => a.textContent.trim()).find((t) => t.startsWith('見積')) || '',
          dlv: !!tr.querySelector('input[disabled][title^="見積の依頼です"]'),
          mfg: !!tr.querySelector('.backlog-wait-pick'),
          row: Number((tr.querySelector('.backlog-wait-pick') || { dataset: {} }).dataset.row || 0),
        };
      }, row);
      check('② 案件残表に「見積」として並ぶ・納品は押せない・製造は選べる', !!r && r.label.startsWith('見積') && r.dlv && r.mfg, JSON.stringify(r));

      // ③
      const view = await page.evaluate(async (pid) => (await fetch('/api/load?view=1&id=' + pid)).text(), quote);
      check('③ 案件ページの足元は「見積の依頼の案件です」・受注残高は出さない', view.includes('見積の依頼の案件です') && !view.includes('受注残高:'), view.replace(/\s+/g, ' ').slice(-300));

      // ④ 製造を始めて（口で）、製造ページのボタンを画面で押す。
      const mr = await page.evaluate(async ([q, rowNo]) => {
        const r = await fetch('/api/mfg/new', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ items: [{ order: q, row: rowNo, item: 'E2E-QC-1', qty: '50' }] }) });
        return { status: r.status, data: await r.json().catch(() => ({})) };
      }, [quote, (r && r.row) || 0]);
      mfg = (mr.data && mr.data.page_id) || '';
      check('④ 製造ページを始められた', mr.status === 200 && !!mfg, JSON.stringify(mr));
      if (mfg) {
        await page.goto(BASE + '/' + mfg);
        const go = page.locator('#w-editor-content .mfg-items-bar .mfg-act-estimate');
        await go.waitFor({ timeout: 8000 }).catch(() => {});
        check('④ 製造明細表の下の帯に「📄 見積書」', await go.count() === 1);
        await go.click().catch(() => {});
        // 弊社品番の無い行なので、単価が無いと言って、開くリンクを出す。
        const link = page.locator('#w-editor-content .mfg-items-bar .mfg-bar-say a');
        await link.waitFor({ timeout: 10000 }).catch(() => {});
        const sayText = await page.locator('#w-editor-content .mfg-items-bar .mfg-bar-say').textContent().catch(() => '');
        est = ((await link.getAttribute('href').catch(() => '')) || '').replace(/^\//, '');
        check('④ 見積書ができて、単価の無い行を知らせる', !!est && sayText.includes('単価の無い行が 1 行'), sayText);
        if (est) {
          const pe = await parentOf(page, est);
          check('④ 見積書は 記録／年／月／依頼', (await titleOf(page, pe)) === '依頼', String(pe));
          const listed = await page.evaluate(async (pid) => {
            const v = await (await fetch('/api/load?view=1&id=' + pid)).text();
            const at = v.indexOf('class="mfg-docs"');
            return at < 0 ? [] : [...v.slice(at, v.indexOf('</div>', at)).matchAll(/href="\/(\d{6})"/g)].map((m) => m[1]);
          }, mfg);
          check('④ 製造ページの「この製造の書類」に見積書', listed.includes(est), listed.join(','));
          const eb = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).text(), est);
          check('④ 見積明細表の行に案件が写る', eb.includes('<th>案件</th>') && eb.includes('>' + quote + '<') && eb.includes('E2E-QC-1'), eb.replace(/\s+/g, ' ').slice(0, 600));
          // ⑤ 送付日を付ける（口で）。
          const mk = await page.evaluate(async (pid) => (await fetch('/api/estimate/mark', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ page_id: pid, mark: 'sent' }) })).status, est);
          check('⑤ 送付日を付けた', mk === 200, String(mk));
          await page.goto(BASE + '/' + root);
          await page.waitForSelector('#w-editor-content table', { timeout: 10000 }).catch(() => {});
          check('⑤ 見積を出したので、案件残表から外れる', await page.locator(row).count() === 0);
          const v2 = await page.evaluate(async (pid) => (await fetch('/api/load?view=1&id=' + pid)).text(), quote);
          check('⑤ 案件ページの足元に「完了」', v2.includes('を出したので完了です'), v2.replace(/\s+/g, ' ').slice(-300));
        }
      }
    }
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    if (est) await deletePage(page, est).catch(() => {});
    if (mfg) await deletePage(page, mfg).catch(() => {});
    if (quote) await deletePage(page, quote).catch(() => {});
    if (root) await deletePage(page, root).catch(() => {});
    if (mail) await deletePage(page, mail).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
