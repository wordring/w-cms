// 表のセルを矩形で選べるかを**ブラウザから**確かめる（2026-10-04）。
//
// 利用者:「表のセルの選択ですが、ドラッグ矩形の範囲にあるセルを選択するように出来ますか？」。
//
//   閲覧モード: ① 別のセルへドラッグすると矩形で選ぶ（文字の選択は外れる）② コピーはタブ区切りと表の HTML（本物の Ctrl+C でも）
//     ③ Esc・ほかの所を押すと外れる ④ Shift を押しながら押すと、前に押したセルからの矩形 ⑤ 見出しも写せる（th のまま）
//     ⑥ 同じセルの中のドラッグは文字の選択のまま
//   編集モード: ⑦ Delete で消す ⑧ 値が1つなら選んだセル全部へ貼る ⑨ 切り取り ⑩ 選んだ印は保存されない
//
// 自分で作ったページで動かし、最後に消します。
//
// 使い方: WCMS_BASE=https://localhost:8443 node verify-cell-select.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

const BODY = '<h1>【E2E】セルを選ぶ</h1>' +
  '<p>CS-前の段落</p>' +
  '<table><caption>E2E 選ぶ</caption><tbody>' +
  '<tr><th>品名</th><th>数</th><th>備考</th></tr>' +
  '<tr><td>CS-a</td><td>1</td><td>x</td></tr>' +
  '<tr><td>CS-b</td><td>2</td><td>y</td></tr>' +
  '<tr><td>CS-c</td><td>3</td><td>z</td></tr>' +
  '</tbody></table>';

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
  await ctx.grantPermissions(['clipboard-read', 'clipboard-write'], { origin: BASE }).catch(() => {});
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

  const center = (r, c) => page.evaluate(([r, c]) => {
    const td = document.querySelector('#w-editor-content table').rows[r].cells[c];
    const b = td.getBoundingClientRect();
    return { x: b.left + b.width / 2, y: b.top + b.height / 2, left: b.left + 4, right: b.right - 4 };
  }, [r, c]);
  const drag = async (r0, c0, r1, c1) => {
    const a = await center(r0, c0);
    const b = await center(r1, c1);
    await page.mouse.move(a.x, a.y);
    await page.mouse.down();
    await page.mouse.move(b.x, b.y, { steps: 6 });
    await page.mouse.up();
  };
  const selected = () => page.evaluate(() => {
    const t = document.querySelector('#w-editor-content table');
    const out = [];
    Array.from(t.rows).forEach((tr, r) => Array.from(tr.cells).forEach((c, i) => {
      if (c.classList.contains('w-cell-selected')) out.push(r + ',' + i);
    }));
    return out;
  });
  const grid = () => page.evaluate(() => Array.from(document.querySelector('#w-editor-content table').rows)
    .map((r) => Array.from(r.cells).map((c) => c.textContent.trim())));
  // 出来事を起こして、画面が書いたクリップボードの中身を読む（本物のクリップボードを使わない）。
  const fire = (type, data) => page.evaluate(([type, data]) => {
    const dt = new DataTransfer();
    Object.entries(data || {}).forEach(([k, v]) => dt.setData(k, v));
    const target = document.activeElement && document.activeElement !== document.body
      ? document.activeElement : document.querySelector('#w-editor-content table');
    target.dispatchEvent(new ClipboardEvent(type, { clipboardData: dt, bubbles: true, cancelable: true }));
    return { text: dt.getData('text/plain'), html: dt.getData('text/html') };
  }, [type, data]);
  const setMode = async (edit) => {
    const now = await page.evaluate(() => document.body.hasAttribute('edit-mode'));
    if (now === edit) return;
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForFunction((e) => document.body.hasAttribute('edit-mode') === e, edit, { timeout: 5000 }).catch(() => {});
    await page.waitForTimeout(600);
  };

  const id = await makePage(page, BODY);
  const run = async () => {
    if (!id) { check(false, '当て先のページを作れません'); return; }
    await page.goto(BASE + '/' + id);
    await page.waitForTimeout(800);

    // ① 矩形
    await drag(1, 0, 2, 1);
    check(same(await selected(), ['1,0', '1,1', '2,0', '2,1']), '別のセルへドラッグすると矩形で選ぶ', JSON.stringify(await selected()));
    check(await page.evaluate(() => String(window.getSelection())) === '', '文字の選択は外れる');

    // ② コピー
    let c = await fire('copy');
    check(c.text === 'CS-a\t1\nCS-b\t2\n', 'コピーはタブ区切り', JSON.stringify(c.text));
    check(/<table>/.test(c.html) && /<td>CS-b<\/td>/.test(c.html), 'コピーは表の HTML も', c.html);
    await page.keyboard.press('Control+C');
    const real = await page.evaluate(() => navigator.clipboard.readText().catch((e) => 'ERR ' + e.message));
    check(real === 'CS-a\t1\nCS-b\t2\n', '本物の Ctrl+C でも同じ中身がクリップボードへ', JSON.stringify(real));

    // ③ Esc・ほかの所
    await page.keyboard.press('Escape');
    check(same(await selected(), []), 'Esc で外れる');
    await drag(1, 0, 2, 1);
    await page.locator('#w-editor-content p', { hasText: 'CS-前の段落' }).click();
    check(same(await selected(), []), 'ほかの所を押すと外れる');

    // ④ Shift
    const a = await center(1, 0);
    await page.mouse.click(a.x, a.y);
    const b = await center(3, 2);
    await page.keyboard.down('Shift');
    await page.mouse.click(b.x, b.y);
    await page.keyboard.up('Shift');
    check((await selected()).length === 9, 'Shift を押しながら押すと、前に押したセルからの矩形（3×3）', JSON.stringify(await selected()));
    await page.keyboard.press('Escape');

    // ⑤ 見出しも
    await drag(0, 0, 1, 1);
    c = await fire('copy');
    check(c.text === '品名\t数\nCS-a\t1\n' && /<th>品名<\/th>/.test(c.html), '見出しも写せる（表の HTML では th のまま）', JSON.stringify(c));
    await page.keyboard.press('Escape');

    // ⑥ 同じセルの中
    const one = await center(2, 2);
    await page.mouse.move(one.left, one.y);
    await page.mouse.down();
    await page.mouse.move(one.right, one.y, { steps: 4 });
    await page.mouse.up();
    check(same(await selected(), []), '同じセルの中のドラッグはセルを選ばない（文字の選択のまま）', JSON.stringify(await selected()));

    // ⑦〜⑩ 編集モード
    await setMode(true);
    check(await page.evaluate(() => document.body.hasAttribute('edit-mode')), '編集モードに入れる');
    await drag(1, 1, 2, 2);
    await page.keyboard.press('Delete');
    let g = await grid();
    check(same([g[1][1], g[1][2], g[2][1], g[2][2]], ['', '', '', '']) && g[1][0] === 'CS-a' && g[3][1] === '3',
      'Delete で選んだセルだけ消える', JSON.stringify(g));
    await drag(1, 0, 3, 0);
    await fire('paste', { 'text/plain': 'Z' });
    g = await grid();
    check(same([g[1][0], g[2][0], g[3][0]], ['Z', 'Z', 'Z']), '値が1つなら選んだセル全部へ貼る', JSON.stringify(g.map((r) => r[0])));
    await drag(3, 1, 3, 2);
    c = await fire('cut');
    g = await grid();
    check(c.text === '3\tz\n' && g[3][1] === '' && g[3][2] === '', '切り取りは写して消す', JSON.stringify([c.text, g[3]]));
    await page.keyboard.press('Escape');
    await page.waitForTimeout(2500); // 自動保存
    const preview = await page.locator('#w-html-preview').inputValue();
    check(!/w-cell-selected|w-cell-dragging/.test(preview), '選んだ印は書き出し（保存されるもの）に入らない');
    const saved = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).text(), id);
    check(saved.includes('<td>Z</td>') && !saved.includes('CS-a'), '消した・貼った中身は保存された');
    await setMode(false);
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
