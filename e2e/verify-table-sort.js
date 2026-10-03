// 表の列の題を押して並べ替えられるかを**ブラウザから**確かめる（2026-10-03）。
//
// 利用者:「表の列の題をクリックして並び順を変えるように出来ませんか？」→「表示だけ」「この端末で憶える」。
//
//   ① 見出しの行のある表だけ、題が押せる（見出しの無い表・結合したセルのある表は押せない）
//   ② 押すたびに 昇順 → 降順 → 元の並び（数は数として・日付の数字の部分も数として・全角は半角に畳む・空は最後）
//   ③ 開き直しても並べ替えを憶えている——そのとき書き出し（#w-html-preview）は**元の並び**（本文に保存させない）
//   ④ 編集モードでは元の並び・押せない。編集して保存しても本文は元の並び
//
// 表は自分で作ったページに置き、最後に消します。
//
// 使い方: WCMS_BASE=https://localhost:8443 node verify-table-sort.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

const BODY = '<h1>【E2E】表の並べ替え</h1>' +
  '<p>前の段落</p>' +
  '<table><caption>E2E 並べ替え</caption><tbody>' +
  '<tr><th>品名</th><th>数量</th><th>納期</th></tr>' +
  '<tr><td>E2E-SORT-B</td><td>10</td><td>2026/10/15</td></tr>' +
  '<tr><td>E2E-SORT-A</td><td>9</td><td>2026/9/5</td></tr>' +
  '<tr><td>E2E-SORT-C</td><td></td><td>2026/11/1</td></tr>' +
  '<tr><td>E2E-SORT-D</td><td>１，２００</td><td></td></tr>' +
  '</tbody></table>' +
  '<table><tbody>' +
  '<tr><td>見出しでない</td><td>行</td></tr>' +
  '<tr><td>E2E-NOHEAD-1</td><td>2</td></tr>' +
  '<tr><td>E2E-NOHEAD-2</td><td>1</td></tr>' +
  '</tbody></table>' +
  '<table><tbody>' +
  '<tr><th>結合</th><th>あり</th></tr>' +
  '<tr><td colspan="2">E2E-SPAN</td></tr>' +
  '<tr><td>x</td><td>y</td></tr>' +
  '</tbody></table>';

const ORIGINAL = ['E2E-SORT-B', 'E2E-SORT-A', 'E2E-SORT-C', 'E2E-SORT-D'];

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

  // 1つ目の表の行の並び（品名の列）
  const order = () => page.evaluate(() => {
    const t = document.querySelectorAll('#w-editor-content table')[0];
    return Array.from(t.querySelectorAll('tr')).slice(1).map((r) => r.cells[0].textContent.trim());
  });
  const head = (name) => page.locator('#w-editor-content table').first().locator('th', { hasText: name });
  // 本文の書き出し（保存されるもの）の中の行の並び
  const savedOrder = (html) => [...html.matchAll(/E2E-SORT-[A-D]/g)].map((m) => m[0]);

  const id = await makePage(page, BODY);
  const run = async () => {
    if (!id) { check(false, '当て先のページを作れません'); return; }
    await page.goto(BASE + '/' + id);
    await page.waitForTimeout(800);

    // ① 押せる題
    const marks = await page.evaluate(() => Array.from(document.querySelectorAll('#w-editor-content table'))
      .map((t) => t.querySelectorAll('th.w-sortable').length));
    check(same(marks, [3, 0, 0]), '見出しの行のある表だけ題が押せる（見出しの無い表・結合のある表は押せない）', JSON.stringify(marks));

    // ② 昇順 → 降順 → 元の並び
    await head('数量').click();
    check(same(await order(), ['E2E-SORT-A', 'E2E-SORT-B', 'E2E-SORT-D', 'E2E-SORT-C']),
      '数量で昇順（9 < 10 < １，２００・空は最後）', JSON.stringify(await order()));
    check(await head('数量').locator('.w-sort-mark').textContent() === '▲', '並べた列に ▲');
    await head('数量').click();
    check(same(await order(), ['E2E-SORT-D', 'E2E-SORT-B', 'E2E-SORT-A', 'E2E-SORT-C']),
      'もう一度押すと降順（空はやはり最後）', JSON.stringify(await order()));
    check(await head('数量').locator('.w-sort-mark').textContent() === '▼', '降順は ▼');
    await head('数量').click();
    check(same(await order(), ORIGINAL), 'もう一度押すと元の並び', JSON.stringify(await order()));
    check(await page.locator('#w-editor-content .w-sort-mark').count() === 0, '元の並びでは印が消える');

    await head('納期').click();
    check(same(await order(), ['E2E-SORT-A', 'E2E-SORT-B', 'E2E-SORT-C', 'E2E-SORT-D']),
      '納期で昇順（2026/9/5 < 2026/10/15 < 2026/11/1・空は最後）', JSON.stringify(await order()));

    // ③ 開き直しても憶えている・書き出しは元の並び
    await page.reload();
    await page.waitForTimeout(800);
    check(same(await order(), ['E2E-SORT-A', 'E2E-SORT-B', 'E2E-SORT-C', 'E2E-SORT-D']),
      '開き直しても納期の昇順のまま（この端末に憶える）', JSON.stringify(await order()));
    const preview = await page.locator('#w-html-preview').inputValue();
    check(same(savedOrder(preview), ORIGINAL), '並べ替えて見えていても、書き出し（保存されるもの）は元の並び', JSON.stringify(savedOrder(preview)));
    check(!/w-sort-mark|▲/.test(preview), '書き出しに並べ替えの印が入らない');

    // ④ 編集モードでは元の並び・押せない。編集して保存しても本文は元の並び
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 5000 }).catch(() => {});
    check(await page.evaluate(() => document.body.hasAttribute('edit-mode')), '編集モードに入れる');
    check(same(await order(), ORIGINAL), '編集モードでは元の並び', JSON.stringify(await order()));
    check(await page.locator('#w-editor-content th.w-sortable').count() === 0, '編集モードでは題は押せない（文字を直す場所）');
    const p = page.locator('#w-editor-content p', { hasText: '前の段落' });
    await p.click();
    await page.keyboard.press('End');
    await page.keyboard.type('（直した）');
    await page.waitForTimeout(2500); // 自動保存（1.5秒）を待つ
    const saved = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).text(), id);
    check(saved.includes('（直した）'), '編集は保存された');
    check(same(savedOrder(saved), ORIGINAL), '保存された本文の行は元の並び', JSON.stringify(savedOrder(saved)));

    // 閲覧モードへ戻すと、憶えた並べ替えがまた効く
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForFunction(() => !document.body.hasAttribute('edit-mode'), null, { timeout: 5000 }).catch(() => {});
    await page.waitForTimeout(500);
    check(same(await order(), ['E2E-SORT-A', 'E2E-SORT-B', 'E2E-SORT-C', 'E2E-SORT-D']),
      '閲覧モードへ戻すと憶えた並べ替えがまた効く', JSON.stringify(await order()));
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
