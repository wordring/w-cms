// 見積書を作る——装置フォルダから選ぶ（2026-10-03・ext/toho/estimate_folder.go・assets/app.js の wireEstimateFolder）を
// 画面から確かめる。
//
// 利用者:「見積依頼フォルダと同じように、装置のフォルダのページIDを入れると加工製品を一時的な表に列挙します。必要な加工製品に
// チェックを入れてボタンを押すと、見積もりページが出来て、対応する加工製品ページから確定単価を収集して見積明細表を作ります。
// ボタンを押すとPDFが作られます」。
//
//   ① 装置フォルダの番号で「一覧を出す」→ 見積計算表1枚1行（ロット・確定単価・備考）。計算できない表・表の無い加工製品は
//      選べない行で理由が出る。見積先にその装置の客先が入る
//   ② 2行を選んで「見積書を作る」→ 見積書ページが開き、見積明細に一覧の順で確定単価が入る。PDF のボタンがある
//   ③ 開き直しても、憶えた番号で一覧がまた並ぶ
//
// 当て先（【E2E】の置き場・装置フォルダ・加工製品3つ）は自分で作り、できた見積書（本物の 見積／年／月 の下）と、試験が作った
// 年月のフォルダは最後に消します。本物の置き場の本文には触りません。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-estimate-folder.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, childrenOf } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

const BOX = '<h1>【E2E】見積の置き場（フォルダ）</h1><section data-mirror="見積書を作る"></section>';
const FOLDER = '<h1>【E2E】見積の装置フォルダ</h1><dl data-type="tags"><dt>客先</dt><dd>【E2E】客先</dd></dl>';
const tags = (no) => '<dl data-type="tags"><dt>品番</dt><dd>' + no + '</dd></dl>';
const calc = (rows) => '<table><caption>見積計算表</caption><tbody><tr><th>工程</th><th>数</th><th>単位</th><th>備考</th></tr>' +
  rows.map((r) => '<tr>' + r.map((c) => '<td>' + c + '</td>').join('') + '</tr>').join('') + '</tbody></table>';
const P1 = '<h1>【E2E】見積の部品1</h1>' + tags('E2E-ESTF-1') +
  calc([['ロット', '20', '個', ''], ['材料', '55', '円', ''], ['板金', '310', '円', '']]) +
  calc([['ロット', '40', '個', ''], ['単価', '500', '円', '塗装あり']]);
const P2 = '<h1>【E2E】見積の部品2</h1>' + tags('E2E-ESTF-2') +
  '<table><caption>見積計算表</caption><tbody><tr><th>工程</th><th>ロット20</th><th>ロット40</th></tr>' +
  '<tr><td>単価</td><td>500</td><td>450</td></tr></tbody></table>';
const P3 = '<h1>【E2E】見積の部品3</h1>' + tags('E2E-ESTF-3');

const parentOf = (page, id) => page.evaluate(async (x) => (await (await fetch('/api/page-meta?id=' + x)).json()).parent_id, id);

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1400, height: 1000 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let box = '', folder = '', p1 = '', p2 = '', p3 = '', est = '';
  try {
    await login(page, BASE);
    box = await makePage(page, BOX);
    folder = await makePage(page, FOLDER);
    p1 = folder && await makePage(page, P1, folder);
    p2 = folder && await makePage(page, P2, folder);
    p3 = folder && await makePage(page, P3, folder);
    check('当て先を作れた', !!box && !!folder && !!p1 && !!p2 && !!p3);
    await page.goto(BASE + '/' + box);
    await page.waitForTimeout(800);
    check('見積先などの欄は一覧が出るまで隠れている', !(await page.locator('[data-estf-make]').isVisible()));

    // ①
    await page.locator('[data-estf="folder"]').fill(folder);
    await page.locator('[data-estf-list]').click();
    await page.waitForSelector('.estimate-folder-table', { timeout: 8000 }).catch(() => {});
    const picks = await page.locator('.estimate-folder-table input[data-estf-pick]').evaluateAll((els) => els.map((e) => e.getAttribute('data-estf-pick')));
    check('① 計算できる見積計算表だけ選べる（部品1の2枚）', picks.length === 2 && picks.includes(p1 + ':0') && picks.includes(p1 + ':1'), picks.join(','));
    const text = (await page.locator('[data-estf-box]').innerText()).replace(/\s+/g, ' ');
    check('① ロットと確定単価が出る（20個 402円・40個 550円 塗装あり）', text.includes('402円') && text.includes('550円') && text.includes('塗装あり'), text.slice(0, 300));
    check('① 計算できない表・表の無い加工製品も理由つきで並ぶ', text.includes('確定単価が出ません') && text.includes('見積計算表なし'));
    check('① 見積先にその装置の客先が入る', await page.locator('[data-estf="client"]').inputValue() === '【E2E】客先');
    check('① 一時的な表は本文に入らない', !(await page.evaluate(() => document.getElementById('w-html-preview').value)).includes('E2E-ESTF'));

    // ③（作る前に）開き直しても一覧がまた並ぶ。
    await page.reload();
    await page.waitForSelector('.estimate-folder-table input[data-estf-pick]', { timeout: 8000 }).catch(() => {});
    check('③ 開き直しても憶えた番号で一覧がまた並ぶ', await page.locator('.estimate-folder-table input[data-estf-pick]').count() === 2);

    // ② 40個の表 → 20個の表の順に押しても、見積明細は一覧の順（20個 → 40個）。
    await page.locator('input[data-estf-pick="' + p1 + ':1"]').check();
    await page.locator('input[data-estf-pick="' + p1 + ':0"]').check();
    await Promise.all([
      page.waitForURL((u) => !u.pathname.endsWith('/' + box), { timeout: 15000 }).catch(() => {}),
      page.locator('[data-estf-make-go]').click(),
    ]);
    await page.waitForTimeout(1200);
    est = (new URL(page.url())).pathname.replace(/^\//, '');
    check('② 見積書ページが開く', /^\d{6}$/.test(est) && est !== box, page.url());
    const raw = await page.evaluate(async (id) => (await fetch('/api/load?id=' + id)).text(), est);
    const a = raw.indexOf('<td>20</td><td>個</td><td>402</td>');
    const b = raw.indexOf('<td>40</td><td>個</td><td>550</td><td>塗装あり</td>');
    check('② 見積明細に一覧の順で確定単価が入る', a > 0 && b > a, 'a=' + a + ' b=' + b);
    check('② 見積先が入る', raw.includes('<dt>見積先</dt><dd>【E2E】客先</dd>'));
    check('② 見積書の「📄 PDFを作る」がある', await page.locator('#w-editor-content .estimate-pdf-go').count() === 1);
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    // 見積書と、試験が作った年月のフォルダ（作った当て先より新しい番号で、空のもの）を消す。
    const base = Number(box || 0);
    if (est && est !== box) {
      const up = await parentOf(page, est).catch(() => '');
      await deletePage(page, est).catch(() => {});
      let f = up;
      for (let i = 0; i < 2 && f && Number(f) > base; i++) {
        const kids = await childrenOf(page, f).catch(() => [1]);
        if (kids.length !== 0) break;
        const next = await parentOf(page, f).catch(() => '');
        await deletePage(page, f).catch(() => {});
        f = next;
      }
    }
    for (const id of [est, p1, p2, p3, folder, box]) {
      if (!id) continue;
      if (id !== est) await deletePage(page, id).catch(() => {});
      const st = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).status, id).catch(() => 0);
      check('作ったページを消した /' + id, st === 404, 'status ' + st);
    }
    await browser.close();
  }
  console.log('\n結果: ' + (fails ? fails + ' 件失敗' : 'すべて通りました'));
  process.exit(fails ? 1 : 0);
})();
