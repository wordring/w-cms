// 再見積依頼の「装置フォルダから選ぶ」（2026-10-03・ext/toho/rfq_api.go・assets/app.js の wireRFQ）を画面から確かめる。
//
// 利用者:「装置フォルダのページ番号を入力する欄を作り、その下にある加工製品を列挙する一時的な表を作ります。その表で
// チェックした加工製品から見積依頼必要部材表に追加するように出来ると。作業がはかどります」。
//
//   ① 装置フォルダの番号を書いて「一覧を出す」→ その下の加工製品が一時的な表に並ぶ（本文には入らない）
//   ② 2つ選んで（1つはロットを書く）入れる → 部材のある方が見積依頼必要部材表に入り、部材の無い方は理由が出る
//   ③ 読み直したあとも、憶えた番号で一覧がまた並ぶ（続けて選べる）
//   ④ 見出しのチェックで全部選べる
//
// 当て先は全部自分で作って最後に消します（トップ直下の【E2E】の置き場・【E2E】の装置フォルダとその下の加工製品2つ）。
// 本物の置き場には触りません。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-rfq-folder.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, bodyOf } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

const row = (labels, tag) => '<tr>' + labels.map((l) => '<' + tag + '>' + l + '</' + tag + '>').join('') + '</tr>';
const NEEDS = ['弊社品番', '受注', '種類', '番号', '品番', '品名', '加工内容', '材質', '形状', '寸法', '表面', '仕様', '支給', '数量', '単位', '備考'];
const BOX = '<h1>【E2E】見積依頼の置き場（フォルダ）</h1>' +
  '<section data-mirror="再見積依頼"></section>' +
  '<section><table><caption>見積依頼必要部材表</caption><tbody>' + row(NEEDS, 'th') + '<tr>' + '<td></td>'.repeat(NEEDS.length) + '</tr></tbody></table></section>';
const FOLDER = '<h1>【E2E】装置フォルダ</h1><p>見積依頼の試験</p>';
const tags = (no) => '<dl data-type="tags"><dt>品番</dt><dd>' + no + '</dd></dl>';
const PRODUCT1 = '<h1>【E2E】フォルダの部品1</h1>' + tags('E2E-RFQF-1') +
  '<table><caption>材料</caption><tbody><tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>' +
  '<tr><td>E2E-RFQF-鉄</td><td>板</td><td>t2.3*50*60</td><td>2</td></tr></tbody></table>';
const PRODUCT2 = '<h1>【E2E】フォルダの部品2</h1>' + tags('E2E-RFQF-2') + '<p>部材の表なし</p>';

const tableRows = (html, caption) => {
  const t = [...html.matchAll(/<table[\s\S]*?<\/table>/g)].map((m) => m[0])
    .find((x) => x.includes('<caption>' + caption + '</caption>'));
  if (!t) return null;
  return [...t.matchAll(/<tr([^>]*)>([\s\S]*?)<\/tr>/g)].slice(1)
    .map((m) => [...m[2].matchAll(/<td([^>]*)>([\s\S]*?)<\/td>/g)].filter((c) => !/vocab-chrome/.test(c[1])).map((c) => c[2]).join('|'))
    .filter((s) => s.replace(/\|/g, '').trim() !== '');
};

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1400, height: 1000 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  const dialogs = [];
  page.on('dialog', (d) => { dialogs.push(d.message()); d.accept(); });
  let box = '', folder = '', p1 = '', p2 = '';
  try {
    await login(page, BASE);
    folder = await makePage(page, FOLDER);
    p1 = folder && await makePage(page, PRODUCT1, folder);
    p2 = folder && await makePage(page, PRODUCT2, folder);
    box = await makePage(page, BOX);
    check('当て先を作れた', !!box && !!folder && !!p1 && !!p2);
    await page.goto(BASE + '/' + box);
    await page.waitForTimeout(800);

    // ①
    await page.locator('[data-rfq="folder"]').fill(folder);
    await page.locator('[data-rfq-folder]').click();
    await page.waitForSelector('.rfq-folder-table input[data-rfq-folder-pick]', { timeout: 8000 }).catch(() => {});
    const picks = await page.locator('.rfq-folder-table input[data-rfq-folder-pick]').evaluateAll((els) => els.map((e) => e.getAttribute('data-rfq-folder-pick')));
    check('① 装置フォルダの下の加工製品2つが一時的な表に並ぶ', picks.length === 2 && picks.includes(p1) && picks.includes(p2), picks.join(','));
    const listText = await page.locator('[data-rfq-folder-list]').textContent();
    check('① 品番と「見積計算表が無ければ1個分」が出る', listText.includes('E2E-RFQF-1') && listText.includes('1個分'));
    check('① 一時的な表は本文に入らない', !(await bodyOf(page, box)).includes('rfq-folder-table') &&
      !(await page.evaluate(() => document.getElementById('w-html-preview').value)).includes('E2E-RFQF'));

    // ④（入れる前に）見出しのチェックで全部選べ、外せる。
    await page.locator('.rfq-folder-table [data-pick-all]').check();
    check('④ 見出しのチェックで全部選べる', await page.locator('.rfq-folder-table input[data-rfq-folder-pick]:checked').count() === 2);
    await page.locator('.rfq-folder-table [data-pick-all]').uncheck();
    check('④ もう一度押すと全部外れる', await page.locator('.rfq-folder-table input[data-rfq-folder-pick]:checked').count() === 0);

    // ② 部品1をロット3・部品2はそのまま。
    const tr1 = page.locator('.rfq-folder-table tr', { has: page.locator('input[data-rfq-folder-pick="' + p1 + '"]') });
    await tr1.locator('input[data-rfq-folder-pick]').check();
    await tr1.locator('input[data-rfq-folder-lot]').fill('3');
    await page.locator('.rfq-folder-table input[data-rfq-folder-pick="' + p2 + '"]').check();
    await Promise.all([page.waitForEvent('load', { timeout: 15000 }).catch(() => {}), page.locator('[data-rfq-folder-add]').click()]);
    await page.waitForLoadState('load');
    await page.waitForTimeout(1500);
    const rows = tableRows(await bodyOf(page, box), '見積依頼必要部材表') || [];
    check('② 部材のある部品1がロット3 × 個数2 で入る', rows.length === 1 && rows[0].includes('E2E-RFQF-鉄') && rows[0].includes('|6|') && rows[0].includes(p1), rows.join(' / '));
    check('② 部材の無い部品2は入れられない理由が出る', dialogs.some((m) => m.includes(p2) && m.includes('入れられなかった')), dialogs.join(' / '));

    // ③ 読み直したあとも憶えた番号で並ぶ。
    await page.waitForSelector('.rfq-folder-table input[data-rfq-folder-pick]', { timeout: 8000 }).catch(() => {});
    check('③ 読み直したあとも一覧がまた並ぶ（番号を憶えている）',
      await page.locator('[data-rfq="folder"]').inputValue() === folder &&
      await page.locator('.rfq-folder-table input[data-rfq-folder-pick]').count() === 2);

    // 無い番号は断る。
    await page.locator('[data-rfq="folder"]').fill('999999');
    await page.locator('[data-rfq-folder]').click();
    await page.waitForTimeout(800);
    check('無い番号では「ページがありません」', ((await page.locator('[data-rfq-folder-list]').textContent()) || '').includes('ありません'));
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なし', false, String(e));
  } finally {
    for (const id of [p1, p2, folder, box]) {
      if (!id) continue;
      await deletePage(page, id).catch(() => {});
      const st = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).status, id).catch(() => 0);
      check('作ったページを消した /' + id, st === 404, 'status ' + st);
    }
    await browser.close();
  }
  console.log('\n結果: ' + (fails ? fails + ' 件失敗' : 'すべて通りました'));
  process.exit(fails ? 1 : 0);
})();
