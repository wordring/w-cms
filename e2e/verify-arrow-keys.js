// 矢印キーでブロックの端を越える（2026-10-12 午後——assets/app.js の setupArrowNavigation）を画面から確かめる。
//
// 利用者:「テーブルのセルを矢印キーで移動していくと、最後のセルの次に移ったあとキャレットが無くなり移動できなくなります。
// ほかにもそういう要素があります」——ブロックごとに書ける欄が分かれているので、ブラウザは欄の端を越えられなかった。
//
//   ① 表の最後のセルで → —— 次のブロックの頭へ（欄の外に抜けない）・続けて ← で表へ戻れる
//   ② 表の ↓ は同じ列の下のセルへ・最後の行の ↓ は次のブロックへ・↑ は同じ列の上のセルへ
//   ③ 鏡の列（最新単価）のある材料の表——最後のセルの → は鏡を飛ばして次のブロックへ
//   ④ 段落の終わりの → ↓ は次のブロック・始めの ← ↑ は前のブロックの終わりへ
//   ⑤ 箇条書き・タグ・節・折りたたみの端の ↓ は次のブロックへ・タグは行ごと・折りたたみの見出しの ↓ は中身へ
//   ⑥ 閉じた折りたたみの中（見えない）にはキャレットを置かない
//   ⑦ いちばん最後のブロックの ↓ → は欄の中に残る
//
// ページは自分で作り、最後に消します。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-arrow-keys.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const BODY = '<h1>【E2E】矢印キー</h1><p data-id="pa01">前の段落</p>' +
  '<table data-id="tb02"><tbody><tr><th>品</th><th>数</th></tr><tr><td>甲</td><td>1</td></tr><tr><td>乙</td><td>2</td></tr></tbody></table>' +
  '<p data-id="pb03">間の段落</p>' +
  '<table data-id="tm04"><caption>材料表</caption><tbody><tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr><tr><td>SS400</td><td>板</td><td>t3</td><td>2</td></tr></tbody></table>' +
  '<ul data-id="ul05"><li>一つ目</li><li>二つ目</li></ul>' +
  '<dl data-type="tags" data-id="dl06"><dt>名前</dt><dd>みなと</dd><dt>区分</dt><dd>試作</dd></dl>' +
  '<section data-id="se07"><h2>節</h2><p>節の中</p></section>' +
  '<details data-id="de08" open><summary>畳み</summary><p>畳みの中</p></details>' +
  '<details data-id="dc09"><summary>閉じた畳み</summary><p>見えない</p></details>' +
  '<p data-id="pz10">後ろの段落</p>';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 1100 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let id = '';
  // at はキャレットのある場所（"上の段のブロックの ID|いちばん近い要素|文字|位置"）。欄の外なら "外"。
  const at = () => page.evaluate(() => {
    const s = getSelection();
    if (!s.rangeCount) return '外';
    const n = s.anchorNode;
    const el = n.nodeType === 3 ? n.parentElement : n;
    const host = el.closest('#w-editor-content .block-content > [contenteditable="true"]');
    if (!host) return '外';
    return host.getAttribute('data-id') + '|' + el.tagName + '|' + (n.nodeType === 3 ? n.nodeValue : el.textContent).trim() + '|' + s.anchorOffset;
  });
  // caretIn は q の要素の文字の頭（end なら終わり）にキャレットを置く（文字の上——人が押したときと同じ）。
  const caretIn = (q, end) => page.evaluate(([sel, e]) => {
    const el = document.querySelector('#w-editor-content ' + sel);
    const w = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
    let t = w.nextNode(), last = t;
    while (e && (t = w.nextNode())) last = t;
    const node = e ? last : el.firstChild && el.firstChild.nodeType === 3 ? el.firstChild : last;
    el.closest('[contenteditable="true"]').focus();
    const r = document.createRange();
    r.setStart(node, e ? node.nodeValue.length : 0);
    r.collapse(true);
    const s = getSelection(); s.removeAllRanges(); s.addRange(r);
  }, [q, !!end]);
  const press = async (k) => { await page.keyboard.press(k); await sleep(120); return at(); };
  try {
    await login(page, BASE);
    id = await makePage(page, BODY);
    await page.goto(BASE + '/' + id);
    await page.evaluate(() => { const t = document.getElementById('w-mode-toggle'); if (!t.checked) { t.checked = true; t.dispatchEvent(new Event('change', { bubbles: true })); } });
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    await page.waitForSelector('#w-editor-content [data-id="tm04"] .vocab-chrome', { timeout: 8000 }).catch(() => {});
    await sleep(800);

    // ①
    await caretIn('[data-id="tb02"] tr:last-child td:last-child', true);
    const a1 = await press('ArrowRight');
    const a2 = await press('ArrowRight');
    const a3 = await press('ArrowLeft');
    const a4 = await press('ArrowLeft');
    check('① 表の最後のセルの → は次のブロックの頭へ・続けて動ける・← で表へ戻れる',
      a1 === 'pb03|P|間の段落|0' && a2 === 'pb03|P|間の段落|1' && a3 === 'pb03|P|間の段落|0' && a4.startsWith('tb02|TD|2|'), [a1, a2, a3, a4].join(' → '));

    // ②
    await caretIn('[data-id="tb02"] tr:first-child th:first-child');
    const b1 = await press('ArrowDown');
    const b2 = await press('ArrowDown');
    const b3 = await press('ArrowDown');
    check('② 表の ↓ は同じ列の下のセル・最後の行の ↓ は次のブロックへ', b1.startsWith('tb02|TD|甲') && b2.startsWith('tb02|TD|乙') && b3.startsWith('pb03|'), [b1, b2, b3].join(' → '));
    await caretIn('[data-id="tb02"] tr:nth-child(2) td:first-child');
    const b4 = await press('ArrowUp');
    check('② 表の ↑ は同じ列の上のセルへ', b4.startsWith('tb02|TH|品'), b4);

    // ③
    await caretIn('[data-id="tm04"] tr:last-child td:nth-child(4)', true);
    const hasMirror = await page.evaluate(() => !!document.querySelector('#w-editor-content [data-id="tm04"] .vocab-chrome'));
    const c1 = await press('ArrowRight');
    check('③ 材料表の最後のセルの → は鏡の列を飛ばして次のブロックへ', hasMirror && c1.startsWith('ul05|LI|一つ目|0'), hasMirror + ' / ' + c1);

    // ④
    await caretIn('[data-id="pa01"]', true);
    const d1 = await press('ArrowRight');
    await caretIn('[data-id="pa01"]', true);
    const d2 = await press('ArrowDown');
    await caretIn('[data-id="pb03"]');
    const d3 = await press('ArrowLeft');
    await caretIn('[data-id="pb03"]');
    const d4 = await press('ArrowUp');
    check('④ 段落の終わりの → ↓ は次のブロック・始めの ← ↑ は前のブロックへ', d1.startsWith('tb02|TH|品|0') && d2.startsWith('tb02|') &&
      d3.startsWith('tb02|TD|2|') && d4.startsWith('tb02|'), [d1, d2, d3, d4].join(' / '));

    // ⑤
    await caretIn('[data-id="ul05"] li:last-child', true);
    const e1 = await press('ArrowDown');
    await caretIn('[data-id="dl06"] dt');
    const e2 = await press('ArrowDown');
    const e3 = await press('ArrowDown');
    await caretIn('[data-id="se07"] p', true);
    const e4 = await press('ArrowDown');
    const e5 = await press('ArrowDown');
    check('⑤ 箇条書きの終わりの ↓ は次のブロック・タグは行ごと・節の終わりの ↓ は次へ・折りたたみの見出しの ↓ は中身へ',
      e1.startsWith('dl06|DT|名前') && e2.startsWith('dl06|DT|区分') && e3.startsWith('se07|') && e4.startsWith('de08|SUMMARY') && e5.startsWith('de08|P|畳みの中'),
      [e1, e2, e3, e4, e5].join(' / '));

    // ⑥
    await caretIn('[data-id="de08"] p', true);
    const f1 = await press('ArrowDown');
    const f2 = await press('ArrowDown');
    check('⑥ 閉じた折りたたみの中（見えない）には置かない——見出しだけ', f1.startsWith('dc09|SUMMARY') && f2.startsWith('pz10|'), [f1, f2].join(' → '));

    // ⑦
    await caretIn('[data-id="pz10"]', true);
    const g1 = await press('ArrowDown');
    const g2 = await press('ArrowRight');
    check('⑦ いちばん最後のブロックの ↓ → は欄の中に残る', g1.startsWith('pz10|') && g2.startsWith('pz10|'), [g1, g2].join(' → '));
    check('ページのエラーが無い', errs.length === 0, errs.join(' / '));
  } catch (e) {
    check('例外なし: ' + e.message, false);
  } finally {
    await deletePage(page, id);
    await browser.close();
  }
  console.log(fails ? '\n' + fails + ' 件の失敗' : '\nすべて合格');
  process.exit(fails ? 1 : 0);
})();
