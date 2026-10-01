// 顧客へ出す見積書（2026-10-01・ext/toho/estimate_doc.go・estimate_pdf.go）を画面から確かめる。
//
// 利用者:「顧客へので良いです」→「見積計算表から」・宛名は「会社＋担当者」。
//
//   ① 加工製品ページの見積計算表の足元に「📄 見積書に入れる」（客先は加工製品の客先が入っている）
//   ② 担当者を書いて「入れる」→ 新しい見積書ができ（見積／年／月）、見積明細に確定単価・ロット・品番・品名が入る
//   ③ 見積書の足元に「合計（税抜）」・「📄 PDFを作る」・「✉️ メールで送る」（送らない）
//   ④ 「PDFを作る」で紙ができ、見積書ページに開く
//
// 当て先（加工製品）と、できた見積書・空になった年月のフォルダは最後に消します。東邦の拡張が無い組では飛ばします。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-estimate-doc.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, childrenOf } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1400, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let product = '', est = '';
  try {
    await login(page, BASE);
    const exts = await page.evaluate(async () => (await (await fetch('/api/tag-schema')).json()).extensions || []);
    if (!exts.includes('toho')) {
      console.log('東邦の拡張が無いので飛ばします');
      await browser.close();
      process.exit(0);
    }
    product = await makePage(page, '<h1>【E2E】見積の品</h1><dl data-type="tags"><dt>品番</dt><dd>E2E-EST-1</dd>' +
      '<dt>品名</dt><dd>【E2E】カバー</dd><dt>客先</dt><dd>【E2E】客先</dd></dl>' +
      '<table><caption>見積計算表</caption><tbody><tr><th>工程</th><th>数</th><th>単位</th><th>備考</th></tr>' +
      '<tr><td>ロット</td><td>20</td><td>個</td><td></td></tr><tr><td>材料</td><td>55</td><td>円</td><td></td></tr>' +
      '<tr><td>板金</td><td>310</td><td>円</td><td></td></tr><tr><td>単価</td><td>365</td><td>円</td><td>塗装無し</td></tr>' +
      '</tbody></table>');
    await page.goto(BASE + '/' + product);
    const form = page.locator('#w-editor-content .estimate-add-form');
    await form.waitFor({ timeout: 10000 }).catch(() => {});
    check('① 見積計算表の足元に「見積書に入れる」', await form.count() === 1);
    check('客先は加工製品の客先', (await form.locator('.estimate-add-client').inputValue()) === '【E2E】客先');

    // ②
    await form.locator('.estimate-add-person').fill('山川');
    await form.locator('.estimate-add-go').click();
    const link = form.locator('.estimate-add-say a');
    await link.waitFor({ timeout: 10000 }).catch(() => {});
    est = ((await link.getAttribute('href').catch(() => '')) || '').replace(/^\//, '');
    check('② 新しい見積書ができた', /^\d{6}$/.test(est), est);
    await page.goto(BASE + '/' + est);
    await page.waitForSelector('#w-editor-content .estimate-total', { timeout: 10000 }).catch(() => {});
    const view = await page.evaluate(() => {
      const t = [...document.querySelectorAll('#w-editor-content table')].find(x => (x.querySelector('caption') || {}).textContent === '見積明細');
      return {
        title: document.querySelector('#w-editor-content h1').textContent,
        table: t ? t.innerText.replace(/\s+/g, ' ') : '',
        total: (document.querySelector('#w-editor-content .estimate-total') || {}).textContent || '',
      };
    });
    check('題は「見積　客先」', view.title === '見積　【E2E】客先', view.title);
    check('見積明細に品番・品名・ロット・確定単価・備考', view.table.includes('E2E-EST-1') && view.table.includes('【E2E】カバー') &&
      view.table.includes('20') && view.table.includes('402') && view.table.includes('塗装無し'), view.table.slice(0, 200));
    check('③ 合計（税抜）', view.total.includes('合計（税抜）: 8,040円'), view.total);
    check('メールで送る欄がある', await page.locator('#w-editor-content details.estimate-mail [data-mail-compose="見積書"]').count() === 1);
    // 置き場は 見積／年／月——親を3段たどって題を見る。
    const chain = await page.evaluate(async (id) => {
      const out = [];
      let cur = id;
      for (let i = 0; i < 3; i++) {
        cur = (await (await fetch('/api/page-meta?id=' + cur)).json()).parent_id;
        const html = await (await fetch('/api/load?id=' + cur)).text();
        const m = html.match(/<h1[^>]*>([^<]*)<\/h1>/);
        out.push(m ? m[1] : '');
      }
      return out;
    }, est);
    check('置き場は 見積／年／月', /^\d{2}月$/.test(chain[0]) && /^\d{4}年$/.test(chain[1]) && chain[2] === '見積', chain.join(' ← '));

    // ④ PDF
    await Promise.all([
      page.waitForNavigation({ timeout: 20000 }).catch(() => {}),
      page.locator('#w-editor-content .estimate-pdf-go').click(),
    ]);
    await page.waitForSelector('#w-editor-content section[data-type="file-view"]', { timeout: 15000 }).catch(() => {});
    const fv = await page.locator('#w-editor-content section[data-type="file-view"]').getAttribute('data-ref').catch(() => '');
    check('④ PDFを作ると見積書ページに開く', !!fv && fv.startsWith(est + '-'), fv || '');
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    // 見積書と、空になった年月のフォルダを消す（作ったのが試験なら）。
    if (est) {
      const meta = await page.evaluate(async (id) => (await (await fetch('/api/page-meta?id=' + id)).json()).parent_id, est).catch(() => '');
      await deletePage(page, est).catch(() => {});
      let folder = meta;
      for (let i = 0; i < 2 && folder; i++) {
        const kids = await childrenOf(page, folder).catch(() => [1]);
        if (kids.length !== 0) break;
        const up = await page.evaluate(async (id) => (await (await fetch('/api/page-meta?id=' + id)).json()).parent_id, folder).catch(() => '');
        await deletePage(page, folder).catch(() => {});
        folder = up;
      }
    }
    if (product) await deletePage(page, product).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
