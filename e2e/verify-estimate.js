// 見積計算表と見積表（2026-10-15・ext/toho/estimate.go）を画面から確かめる。
//
// 利用者:「この計算表はもしかするとDBに入れる必要が無く、結果の単価のみDBに入れるべきかもしれません」「見積もり計算表に
// 総計は必要ないです」「ロットごとに計算表を分けてはどうでしょう？」「提案通りの構成でお願いします」。
//
//   ① 見積表の足元に「計算表から: 原価 365円 × 利益率 10%（既定） = 利益 37円 → 単価 402円」と「まだ記録していません」・📌
//   ② 「📌 この単価を記録」で見積表に 原価・利益率・単価 が入り、足元が ✓（記録と合っています）
//   ③ 見積表の無い見積計算表の下の「📌 見積表を作って記録」で、すぐ後ろに見積表ができる（原価 565・単価 622）
//   ④ 記録と計算表が合わない見積表は「⚠ 記録が計算表と合いません」——価格の式（=材料*2）も計算に入る
//   ⑤ 「⧉ この組を写す」で、見出し（写し）・見積計算表・見積表が組ごと増える（写しのロット・単価は空）
//   ⑥ 見積表の足元に「📄 見積書に入れる」
//
// 当て先はトップ直下に自分で作り、最後に消します。東邦の拡張が無い組では飛ばします。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-estimate.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

// 単価の欄は、テンプレート「見積計算書」の見積表に式があれば式（2026-10-19・=ROUND(原価*(1+利益率/100))）、無ければ数。
// ⚠ 環境（テンプレート）しだいなので、どちらでも通す——数なら期待の数、式なら式の字（値は足元の「記録と合っています」で確かめる）。
const unitOk = (got, want) => got === want || /^[=＝]/.test(got);
const cellsOk = (cells, want) => {
  const g = (cells || '').split('|'), w = want.split('|');
  return g.length === w.length && g.every((v, i) => (i === 4 ? (w[4] === '' ? (v === '' || /^[=＝]/.test(v)) : unitOk(v, w[4])) : v === w[i]));
};
const calc = (rows) => '<table><caption>見積計算表</caption><tbody><tr><th>工程</th><th>価格</th><th>備考</th></tr>' +
  rows.map(r => '<tr><td>' + r[0] + '</td><td>' + r[1] + '</td><td>' + (r[2] || '') + '</td></tr>').join('') +
  '</tbody></table>';
const record = (cells) => '<table><caption>見積表</caption><tbody><tr><th>個数</th><th>表面処理</th><th>原価</th><th>利益率</th><th>単価</th><th>備考</th></tr><tr>' +
  [0, 1, 2, 3, 4, 5].map(i => '<td>' + (cells[i] || '') + '</td>').join('') + '</tr></tbody></table>';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let id = '';
  const open = async () => {
    await page.goto(BASE + '/' + id);
    await page.waitForSelector('#w-editor-content .estimate-plan', { timeout: 8000 }).catch(() => {});
  };
  const recs = async () => page.evaluate(() => [...document.querySelectorAll('#w-editor-content table')]
    .filter(t => (t.querySelector('caption') || {}).textContent === '見積表')
    .map(t => ({
      cells: [...t.querySelectorAll('tbody tr')].slice(1, 2).map(tr => [...tr.children].map(c => c.textContent.trim()).join('|'))[0] || '',
      plan: [...t.querySelectorAll('tfoot .estimate-plan')].map(x => x.textContent).join(' / '),
      stale: [...t.querySelectorAll('tfoot .estimate-stale')].map(x => x.textContent).join(' / '),
      rec: !!t.querySelector('tfoot .estimate-record-go'),
      dup: !!t.querySelector('tfoot .estimate-dup-go'),
      add: !!t.querySelector('tfoot .estimate-add-form'),
    })));
  const click = async (loc) => {
    await Promise.all([page.waitForEvent('load', { timeout: 15000 }).catch(() => {}), loc.click()]);
    await page.waitForSelector('#w-editor-content .estimate-plan', { timeout: 8000 }).catch(() => {});
  };
  try {
    await login(page, BASE);
    const exts = await page.evaluate(async () => (await (await fetch('/api/tag-schema')).json()).extensions || []);
    if (!exts.includes('toho')) {
      console.log('東邦の拡張が無いので飛ばします');
      await browser.close();
      process.exit(0);
    }
    id = await makePage(page, '<h1>【E2E】見積の品</h1><h2>見積もり計算</h2>' +
      '<h3>1個</h3>' + calc([['材料', '55'], ['板金', '310'], ['ブランク', '']]) + record(['1']) +
      '<h3>20個</h3>' + calc([['材料', '55'], ['板金', '310'], ['塗装', '200']]) +
      '<h3>5個</h3>' + calc([['材料', '100'], ['塗装', '=材料*2']]) + record(['5', '黒', '300', '10', '999']));
    check('当て先を作れた', !!id, id);
    await open();
    let r = await recs();
    check('① 見積表の足元に計算', r[0] && r[0].plan === '計算表から: 原価 365円 × 利益率 10%（既定） = 利益 37円 → 単価 402円', JSON.stringify(r[0]));
    check('① まだ記録していない・📌・⧉・📄', r[0] && r[0].stale === 'まだ記録していません' && r[0].rec && r[0].dup && r[0].add, JSON.stringify(r[0]));
    check('④ 合わない記録は ⚠（価格の式も計算に入る: 原価 300）', r[1] && r[1].plan.includes('原価 300円') &&
      r[1].stale.includes('⚠ 記録が計算表と合いません（記録の単価: 999）'), JSON.stringify(r[1]));
    const calcFoot = page.locator('#w-editor-content table:has(caption:text-is("見積計算表")) tfoot .estimate-record-go');
    check('③ 見積表の無い見積計算表の下にだけ「📌 見積表を作って記録」', await calcFoot.count() === 1 &&
      (await calcFoot.first().innerText()).includes('見積表を作って記録'));

    // ② 記録
    await click(page.locator('#w-editor-content table:has(caption:text-is("見積表")) tfoot .estimate-record-go').first());
    r = await recs();
    check('② 見積表に 原価・利益率・単価', r[0] && cellsOk(r[0].cells, '1||365|10|402|'), JSON.stringify(r[0]));
    check('② 足元は ✓ 記録と合っています・📌 は消える', r[0] && r[0].plan.startsWith('✓ ') && r[0].plan.endsWith('（記録と合っています）') && !r[0].rec,
      JSON.stringify(r[0]));

    // ③ 見積表を作って記録
    await click(page.locator('#w-editor-content table:has(caption:text-is("見積計算表")) tfoot .estimate-record-go').first());
    r = await recs();
    check('③ 見積表ができて記録される（原価 565・単価 622）', r.length === 3 && cellsOk(r[1].cells, '||565|10|622|') && r[1].plan.endsWith('（記録と合っています）'), JSON.stringify(r));
    const order = await page.evaluate(() => [...document.querySelectorAll('#w-editor-content h3, #w-editor-content table > caption')]
      .map(e => e.tagName === 'H3' ? e.textContent : '[' + e.textContent + ']').join(' '));
    check('③ 見積表は見積計算表のすぐ後ろ', order.startsWith('1個 [見積計算表] [見積表] 20個 [見積計算表] [見積表] 5個'), order);

    // ⑤ 写す
    await click(page.locator('#w-editor-content .estimate-dup-go').first());
    r = await recs();
    const order2 = await page.evaluate(() => [...document.querySelectorAll('#w-editor-content h3')].map(e => e.textContent).join(','));
    check('⑤ 見出し（写し）と組が増える', r.length === 4 && order2 === '1個,1個（写し）,20個,5個', order2);
    check('⑤ 写しのロット・原価・単価は空（利益率は残す・単価の式は残す）', r[1] && cellsOk(r[1].cells, '|||10||'), JSON.stringify(r[1]));
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    if (id) await deletePage(page, id).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
