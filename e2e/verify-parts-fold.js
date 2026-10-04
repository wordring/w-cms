// 部材の節をまとめて畳めるかを**ブラウザから**確かめる（2026-10-04）。
//
// 利用者:「材料、外注加工などの部材項目全体について開閉できるようにしたい」。鏡（ext/toho/drawing_mirror.go）が部材の節に
// `w-fold w-fold-parts` を付け、画面（app.js「節をまとめて畳む」）が見出しを押したときに同じ組の節をまとめて開け閉めする。
//
//   ① 部材の4つの節にだけ印（図面・メモの節には付かない）・見出しに ▾
//   ② 部材のどれかの見出しを押すと、4つとも見出しだけになる（表と見積回答が隠れる）・ほかの節はそのまま・▸ に変わる
//   ③ 別の部材の見出しを押すと4つとも開く
//   ④ 表の列の題を押しても畳まれない（並べ替えだけ）
//   ⑤ 畳んだまま編集モードへ入ると全部見える・書き出し（保存されるもの）に畳む印が入らない
//
// 表は自分で作ったページに置き、最後に消します。
//
// 使い方: WCMS_BASE=https://localhost:8443 node verify-parts-fold.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

const part = (name, head, row) => `<section><h2>${name}</h2><table><caption>${name}</caption><tbody>` +
  `<tr>${head.map((h) => `<th>${h}</th>`).join('')}</tr>` +
  row.map((r) => `<tr>${r.map((c) => `<td>${c}</td>`).join('')}</tr>`).join('') + '</tbody></table></section>';

const BODY = '<h1>【E2E】部材の節を畳む</h1>' +
  '<section><h2>図面</h2><p>E2E-FOLD-DRAWING</p></section>' +
  part('材料', ['材質', '形状', '寸法', '個数'], [['E2E-FOLD-M2', '板', 't3.2', '2'], ['E2E-FOLD-M1', '板', 't1.6', '1']]) +
  part('外注加工', ['番号', '加工内容'], [['1', 'E2E-FOLD-PAINT']]) +
  part('購入部品', ['品名', '個数'], [['E2E-FOLD-BOLT', '4']]) +
  part('支給部品', ['品名', '仕様', '個数', '備考'], [['E2E-FOLD-SUP', '', '1', '']]) +
  '<section><h2>メモ</h2><p>E2E-FOLD-MEMO</p></section>';

const PARTS = ['材料', '外注加工', '購入部品', '支給部品'];

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
  const same = (a, b) => JSON.stringify(a) === JSON.stringify(b);

  // 節の見出しの言葉 → { marked, shut, tableShown, headShown, marker }
  const state = () => page.evaluate(() => {
    const out = {};
    document.querySelectorAll('#w-editor-content section').forEach((sec) => {
      const h = sec.querySelector(':scope > h2');
      if (!h) return;
      const t = sec.querySelector('table');
      const visible = (el) => !!el && el.getClientRects().length > 0;
      out[h.textContent.trim()] = {
        marked: sec.classList.contains('w-fold') && sec.classList.contains('w-fold-parts'),
        shut: sec.classList.contains('w-fold-shut'),
        bodyShown: visible(t || sec.querySelector(':scope > p')),
        headShown: visible(h),
        marker: getComputedStyle(h, '::before').content,
      };
    });
    return out;
  });
  const heading = (name) => page.locator('#w-editor-content section > h2', { hasText: new RegExp('^' + name + '$') });
  const quotesShown = () => page.evaluate(() => {
    const q = document.querySelector('#w-editor-content .rfq-quotes');
    return !!q && q.getClientRects().length > 0;
  });

  const id = await makePage(page, BODY);
  const run = async () => {
    if (!id) { check(false, '当て先のページを作れません'); return; }
    await page.goto(BASE + '/' + id);
    await page.waitForTimeout(800);

    // ① 印
    let s = await state();
    check(PARTS.every((n) => s[n] && s[n].marked), '部材の4つの節に畳む印', JSON.stringify(PARTS.map((n) => s[n] && s[n].marked)));
    check(!s['図面'].marked && !s['メモ'].marked, '図面・メモの節には印が付かない');
    check(PARTS.every((n) => s[n].marker.includes('▾')), '部材の見出しに ▾', JSON.stringify(PARTS.map((n) => s[n].marker)));
    check(s['図面'].marker === 'none' || s['図面'].marker === 'normal', 'ほかの節の見出しには何も付かない', s['図面'].marker);
    check(await quotesShown(), '見積回答（支給部品の節の中）が見えている');

    // ④ 表の列の題を押しても畳まれない
    await page.locator('#w-editor-content section table').first().locator('th', { hasText: '材質' }).click(); // 材料の表
    s = await state();
    check(PARTS.every((n) => !s[n].shut && s[n].bodyShown), '列の題を押しても畳まれない（並べ替えだけ）');

    // ② 1つの見出しで4つとも畳む
    await heading('外注加工').click();
    s = await state();
    check(PARTS.every((n) => s[n].shut && !s[n].bodyShown && s[n].headShown), '外注加工の見出しを押すと、部材の4つとも見出しだけになる',
      JSON.stringify(PARTS.map((n) => [s[n].shut, s[n].bodyShown, s[n].headShown])));
    check(PARTS.every((n) => s[n].marker.includes('▸')), '畳むと ▸', JSON.stringify(PARTS.map((n) => s[n].marker)));
    check(!(await quotesShown()), '見積回答も一緒に隠れる');
    check(s['図面'].bodyShown && s['メモ'].bodyShown, 'ほかの節はそのまま見える');

    // ③ 別の見出しで4つとも開く
    await heading('材料').click();
    s = await state();
    check(PARTS.every((n) => !s[n].shut && s[n].bodyShown), '材料の見出しを押すと、4つとも開く');

    // ⑤ 畳んだまま編集モードへ——全部見える・書き出しに印が入らない
    await heading('支給部品').click();
    s = await state();
    check(PARTS.every((n) => s[n].shut && !s[n].bodyShown), '支給部品の見出しでも4つとも畳む（このまま編集モードへ）');
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 5000 }).catch(() => {});
    check(await page.evaluate(() => document.body.hasAttribute('edit-mode')), '編集モードに入れる');
    const shownInEdit = await page.evaluate(() => Array.from(document.querySelectorAll('#w-editor-content section table'))
      .every((t) => t.getClientRects().length > 0));
    check(shownInEdit, '編集モードでは畳んだ節も全部見える');
    const preview = await page.locator('#w-html-preview').inputValue();
    check(preview.includes('E2E-FOLD-SUP') && !/w-fold/.test(preview), '書き出し（保存されるもの）に畳む印が入らない');
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForFunction(() => !document.body.hasAttribute('edit-mode'), null, { timeout: 5000 }).catch(() => {});
  };
  try {
    await run();
  } finally {
    if (id) {
      await deletePage(page, id);
      const gone = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).status, id);
      check(gone === 404, '作ったページを消した', 'status ' + gone);
    }
  }
  check(errs.length === 0, 'ページのエラーが無い', errs.join(' / '));
  await browser.close();
  console.log(bad ? `✗ ${bad} 件` : '✓ すべて合格');
  process.exit(bad ? 1 : 0);
})();
