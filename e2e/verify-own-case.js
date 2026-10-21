// 自社案件（2026-10-21・ext/toho/own_case.go・【考察】製造単位の部材発注 §6.6 の11番）を画面から確かめる。
//
// 利用者:「自社案件として、案件ページに追加するようにしては？通信箱の手書きメモからスタートするのが良いかもしれませんね」
//
//   ① 手書きのメモの記録（向きの無い記録）の「作るページ」に「自社案件ページ」が出る
//   ② 選んで作ると、記録／年／月／依頼 に「【自社】案件　<メモの題>」（ページ種類：自社案件・受信元はメモ）ができる
//   ③ 案件ページの足元は「自社案件です」（受注残高・検算は出さない）
//
// 当て先は全部自分で作って最後に消します（メモの記録・自社案件ページ）。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-own-case.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
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
  let memo = '', made = '';
  try {
    await login(page, BASE);
    memo = await makePage(page, '<h1>【E2E】治具の材料</h1><dl data-type="tags"><dt>チャネル</dt><dd>メモ</dd><dt>対応</dt><dd>未処理</dd></dl><p>治具を作る。材料を買う。</p>');
    check('当て先を作れた', !!memo);
    const kinds = await page.evaluate(async (pid) => (await (await fetch('/api/record-makers?page_id=' + pid)).json()), memo).catch(() => ({}));
    const names = ((kinds && kinds.kinds) || []).map((k) => k.name || k.Name);
    check('① メモの「作るページ」に「自社案件ページ」', names.includes('自社案件ページ'), names.join('・'));
    const r = await page.evaluate(async (pid) => {
      const res = await fetch('/api/record-make', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ page_id: pid, kind: '自社案件ページ' }) });
      return res.json();
    }, memo);
    made = (r && (r.open || r.Open)) || ((r && r.pages && r.pages[0] && (r.pages[0].page_id || r.pages[0].PageID)) || '');
    check('② 自社案件ページができた', !!made, JSON.stringify(r).slice(0, 300));
    if (made) {
      const body = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).text(), made);
      check('② 題は「【自社】案件　【E2E】治具の材料」', body.includes('<h1>【自社】案件　【E2E】治具の材料</h1>'), (body.match(/<h1[^>]*>[^<]*<\/h1>/) || [''])[0]);
      check('② ページ種類は自社案件・受信元はメモ', body.includes('<dd>自社案件</dd>') && body.includes(memo));
      const meta = await page.evaluate(async (pid) => (await fetch('/api/page-meta?id=' + pid)).json(), made);
      const parentTitle = await page.evaluate(async (pid) => ((await (await fetch('/api/load?id=' + pid)).text()).match(/<h1[^>]*>([^<]*)</) || [, ''])[1], meta.parent_id);
      check('② 置き場は 記録／年／月／依頼', parentTitle === '依頼', parentTitle);
      const view = await page.evaluate(async (pid) => (await fetch('/api/load?view=1&id=' + pid)).text(), made);
      check('③ 足元は「自社案件です」', view.includes('自社案件です'));
    }
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    for (const id of [made, memo]) if (id) await deletePage(page, id).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
