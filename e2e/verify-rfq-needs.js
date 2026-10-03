// 見積依頼の段1（2026-10-03・ext/toho/rfq.go・rfq_api.go・assets/app.js の wireRFQ）を画面から確かめる。
//
// 利用者:「まず、見積もりを依頼する部材は見積依頼必要部材表に入ります。…2.再見積依頼フォームの弊社品番テキストボックスに
// 書き込みボタンを押す。…重複するものは見積依頼必要部材表に入れるが重複とわかるように背景を赤くします。そして消すことが
// 出来るようにするのです」「見積依頼必要部材表のチェックボックスを選択して入れる先をえらびボタンを押し、見積依頼部材表に入れます」。
//
//   ① 再見積依頼フォームに弊社品番を書いて「集める」→ 見積計算表のロットごと（20・40）に部材が入る
//   ② ロット20でもう一度集めると、同じもの・同じ数の行が赤＋「⚠ 重複」になる。臨時部材表の行も並ぶ
//   ③ 必要部材表の1行と臨時部材表の1行を選んで「見積依頼部材表へ入れる」→ 見積依頼部材表ができ、元から消える
//   ④ 見積依頼部材表の「↩ 戻す」で見積依頼必要部材表へ戻る（最後の1行なら表ごと消える）
//   ⑤ 重複の行を選んで「不要（消す）」→ 確かめて消える
//
// 当て先は全部自分で作って最後に消します（トップ直下の【E2E】の見積依頼の置き場と、【E2E】の加工製品）。本物の置き場には触りません。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-rfq-needs.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, bodyOf } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

const row = (labels, tag) => '<tr>' + labels.map((l) => '<' + tag + '>' + l + '</' + tag + '>').join('') + '</tr>';
const TEMP = ['品番', '品名', '材質', '形状', '寸法', '表面', '数量', '単位', '備考'];
const NEEDS = ['弊社品番', '受注', '種類', '番号', '品番', '品名', '加工内容', '材質', '形状', '寸法', '表面', '仕様', '支給', '数量', '単位', '備考'];
const BOX = '<h1>【E2E】見積依頼の置き場</h1>' +
  '<section data-mirror="再見積依頼"></section>' +
  '<table><caption>見積依頼の臨時部材表</caption><tbody>' + row(TEMP, 'th') +
  '<tr><td></td><td></td><td>E2E-RFQ-真鍮</td><td>板</td><td>t1</td><td></td><td>3</td><td></td><td></td></tr></tbody></table>' +
  '<section><table><caption>見積依頼必要部材表</caption><tbody>' + row(NEEDS, 'th') + '<tr>' + '<td></td>'.repeat(NEEDS.length) + '</tr></tbody></table></section>';
const PRODUCT = '<h1>【E2E】見積依頼の加工製品</h1>' +
  '<table><caption>材料</caption><tbody><tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>' +
  '<tr><td>E2E-RFQ-鉄</td><td>板</td><td>t3.2*100*200</td><td>2</td></tr>' +
  '<tr><td>E2E-RFQ-SUS</td><td>丸棒</td><td>φ20*50</td><td>1</td></tr></tbody></table>' +
  '<table><caption>見積計算表</caption><tbody><tr><th>工程</th><th>数</th><th>単位</th><th>備考</th></tr>' +
  '<tr><td>ロット</td><td>20</td><td>個</td><td></td></tr></tbody></table>' +
  '<table><caption>見積計算表</caption><tbody><tr><th>工程</th><th>数</th><th>単位</th><th>備考</th></tr>' +
  '<tr><td>ロット</td><td>40</td><td>個</td><td></td></tr></tbody></table>';

// tableRows は本文 HTML の、キャプション caption の n 枚目の表の行（見出しを除く・空の行を除く）を文字の配列で返す。
const tableRows = (html, caption, n = 1) => {
  const tables = [...html.matchAll(/<table[\s\S]*?<\/table>/g)].map((m) => m[0])
    .filter((t) => t.includes('<caption>' + caption + '</caption>'));
  const t = tables[n - 1];
  if (!t) return null;
  // bodyOf は鏡を描いた形（クロームと見た目の class 付き）——行は class 付きでも数え、臨時部材の行（クローム）と
  // クロームのセル（選ぶ欄）は除く。
  return [...t.matchAll(/<tr([^>]*)>([\s\S]*?)<\/tr>/g)].slice(1)
    .filter((m) => !/rfq-temp-row/.test(m[1]))
    .map((m) => [...m[2].matchAll(/<td([^>]*)>([\s\S]*?)<\/td>/g)].filter((c) => !/vocab-chrome/.test(c[1])).map((c) => c[2]).join('|'))
    .filter((s) => s.replace(/\|/g, '').trim() !== '');
};

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1400, height: 1000 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  page.on('dialog', (d) => d.accept());
  let box = '', product = '';
  try {
    await login(page, BASE);
    product = await makePage(page, PRODUCT);
    box = await makePage(page, BOX);
    check('当て先を作れた', !!product && !!box);
    const open = async () => { await page.goto(BASE + '/' + box); await page.waitForTimeout(800); };
    const waitReload = async () => { await page.waitForLoadState('load'); await page.waitForTimeout(1200); };

    // ①
    await open();
    await page.locator('[data-rfq="product"]').fill(product);
    await Promise.all([page.waitForEvent('load', { timeout: 15000 }).catch(() => {}), page.locator('[data-rfq-collect]').click()]);
    await waitReload();
    let rows = tableRows(await bodyOf(page, box), '見積依頼必要部材表') || [];
    check('① 見積計算表のロット（20・40）× 部材の数量で4行入る', rows.length === 4 &&
      rows.some((r) => r.includes('E2E-RFQ-鉄') && r.includes('|40|')) && rows.some((r) => r.includes('E2E-RFQ-鉄') && r.includes('|80|')), rows.join(' / '));

    // ②
    await page.locator('[data-rfq="product"]').fill(product);
    await page.locator('[data-rfq="lot"]').fill('20');
    await Promise.all([page.waitForEvent('load', { timeout: 15000 }).catch(() => {}), page.locator('[data-rfq-collect]').click()]);
    await waitReload();
    const dupRows = await page.locator('#w-editor-content tr.rfq-dup').count();
    const dupMarks = await page.locator('#w-editor-content .rfq-dup-mark').count();
    check('② 同じもの・同じ数の行が赤＋「⚠ 重複」（ロット20の2行×2回＝4行）', dupRows === 4 && dupMarks === 4, dupRows + '行・' + dupMarks + '印');
    check('② 臨時部材表の行も並ぶ', await page.locator('#w-editor-content tr.rfq-temp-row').count() === 1);

    // ③ 必要部材表の1行目と臨時の1行を選んで入れる。
    await page.locator('#w-editor-content input.rfq-check[data-rfq-row="1"]').check();
    await page.locator('#w-editor-content input.rfq-check[data-rfq-temp-row]').first().check();
    await Promise.all([page.waitForEvent('load', { timeout: 15000 }).catch(() => {}), page.locator('[data-rfq-move]').click()]);
    await waitReload();
    let body = await bodyOf(page, box);
    const draft = tableRows(body, '見積依頼部材表') || [];
    check('③ 見積依頼部材表に2行（必要部材表の行と臨時の行）', draft.length === 2 && draft.some((r) => r.includes('E2E-RFQ-真鍮')), draft.join(' / '));
    check('③ 元の表から消える', (tableRows(body, '見積依頼必要部材表') || []).length === 5 &&
      (tableRows(body, '見積依頼の臨時部材表') || []).length === 0);

    // ④ ↩ 戻す——2行とも戻すと表ごと消える。
    for (let i = 0; i < 2; i++) {
      await Promise.all([page.waitForEvent('load', { timeout: 15000 }).catch(() => {}), page.locator('#w-editor-content .rfq-draft-back').first().click()]);
      await waitReload();
    }
    body = await bodyOf(page, box);
    check('④ ↩ 戻すで見積依頼必要部材表へ戻り、空の見積依頼部材表は残らない',
      !tableRows(body, '見積依頼部材表') && (tableRows(body, '見積依頼必要部材表') || []).length === 7);

    // ⑤ 重複の行を全部選んで不要。
    const dupChecks = page.locator('#w-editor-content tr.rfq-dup input.rfq-check');
    const nDup = await dupChecks.count();
    for (let i = 0; i < nDup; i++) await dupChecks.nth(i).check();
    await Promise.all([page.waitForEvent('load', { timeout: 15000 }).catch(() => {}), page.locator('[data-rfq-remove]').click()]);
    await waitReload();
    const left = tableRows(await bodyOf(page, box), '見積依頼必要部材表') || [];
    check('⑤ 選んだ重複の行が「不要」で消える', nDup >= 4 && left.length === 7 - nDup, nDup + '行を消して残り ' + left.length);
    check('⑤ 重複が無くなると赤も消える', await page.locator('#w-editor-content tr.rfq-dup').count() === 0);
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    if (box) await deletePage(page, box).catch(() => {});
    if (product) await deletePage(page, product).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
