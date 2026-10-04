// 表のセルの式と、複数のセルの貼り付けを**ブラウザから**確かめる（2026-10-04）。
//
// 利用者:「=個数*単価みたいにセルに書きます。編集時には書いたままを表示して編集でき、閲覧時には計算結果を表示できるのは
// どうですか？数値に変換できない場合、エラーと赤背景赤太文字を表示してはどうでしょう？」「小数も残す」
// 「表のコピペを一つ一つしなくてもキャレットのある位置から右下に向かってペーストできませんか？」「複数セルをコピーしたときの話です」。
//
//   式: ① 閲覧では結果（小数も残す）・数でない値は赤の「⚠ エラー」・空を参照したら空・マウスで式
//       ② 書き出し（保存されるもの）は式のまま ③ 列の題で並べると計算した値で並ぶ
//       ④ 編集では式のまま（式の印）⑤ 編集で個数を変えて閲覧へ戻ると計算し直す
//   貼り付け: ⑥ タブ区切りの塊がキャレットのセルから右下へ・足りない行は足す ⑦ はみ出した列は貼らない
//       ⑧ 見出しの行にも貼れる（利用者「見出しもうまいことコピペできませんか？」→「見出しの行にも貼れる」）
//       ⑨ 1つのセルだけ（Excel の1セル＝表の HTML）は文字だけ ⑩ 保存される
//
// 表は自分で作ったページに置き、最後に消します。
//
// 使い方: WCMS_BASE=https://localhost:8443 node verify-table-formula.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

const BODY = '<h1>【E2E】表の式と貼り付け</h1>' +
  '<table><caption>E2E 式</caption><tbody>' +
  '<tr><th>品名</th><th>単価</th><th>個数</th><th>価格</th></tr>' +
  '<tr><td>E2E-F-板</td><td>12.5</td><td>3</td><td>=個数*単価</td></tr>' +
  '<tr><td>E2E-F-棒</td><td>100</td><td>たくさん</td><td>=個数*単価</td></tr>' +
  '<tr><td>E2E-F-釘</td><td>10</td><td></td><td>＝個数×単価</td></tr>' +
  '<tr><td>E2E-F-鋲</td><td>2</td><td>5</td><td>=単価*個数</td></tr>' + // 10——並べ替えで板より前に来る
  '</tbody></table>';

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

  // 表の行（見出しを除く）の文字
  const grid = () => page.evaluate(() => {
    const t = document.querySelector('#w-editor-content table');
    return Array.from(t.rows).slice(1).map((r) => Array.from(r.cells).map((c) => c.textContent.trim()));
  });
  const cellInfo = (r, c) => page.evaluate(([r, c]) => {
    const td = document.querySelector('#w-editor-content table').rows[r].cells[c];
    const span = td.querySelector('.w-formula-shown');
    const cs = getComputedStyle(span || td);
    return { text: td.textContent.trim(), cls: td.className, title: span ? span.title : '',
      bg: getComputedStyle(td).backgroundColor, color: cs.color, weight: cs.fontWeight };
  }, [r, c]);
  const setMode = async (edit) => {
    const now = await page.evaluate(() => document.body.hasAttribute('edit-mode'));
    if (now === edit) return;
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForFunction((e) => document.body.hasAttribute('edit-mode') === e, edit, { timeout: 5000 }).catch(() => {});
    await page.waitForTimeout(600);
  };
  // キャレットを (r, c) のセルの末尾に置き、クリップボードの中身で貼り付けを起こす。
  const paste = (r, c, data) => page.evaluate(([r, c, data]) => {
    const td = document.querySelector('#w-editor-content table').rows[r].cells[c];
    const range = document.createRange();
    range.selectNodeContents(td);
    range.collapse(false);
    const sel = getSelection();
    sel.removeAllRanges();
    sel.addRange(range);
    const dt = new DataTransfer();
    Object.entries(data).forEach(([k, v]) => dt.setData(k, v));
    td.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true }));
  }, [r, c, data]);

  const id = await makePage(page, BODY);
  const run = async () => {
    if (!id) { check(false, '当て先のページを作れません'); return; }
    await page.goto(BASE + '/' + id);
    await page.waitForTimeout(800);

    // ① 閲覧の表示
    let g = await grid();
    check(g[0][3] === '37.5', '閲覧では結果（12.5 × 3 = 37.5・小数も残す）', g[0][3]);
    const e = await cellInfo(2, 3);
    check(/⚠ エラー/.test(e.text) && /「個数」が数ではありません/.test(e.text), '数でない値は「⚠ エラー」と理由', e.text);
    check(e.bg === 'rgb(254, 226, 226)' && e.color === 'rgb(185, 28, 28)' && Number(e.weight) >= 700, 'エラーは赤い背景に赤の太字',
      JSON.stringify([e.bg, e.color, e.weight]));
    check(g[2][3] === '', '空のセル（個数）を参照したら空（エラーにしない）・全角の式も読む', g[2][3]);
    check((await cellInfo(1, 3)).title === '=個数*単価', 'マウスを載せると式が出る（title）');

    // ② 書き出しは式のまま
    let preview = await page.locator('#w-html-preview').inputValue();
    check((preview.match(/=個数\*単価|＝個数×単価/g) || []).length === 3 && !/37\.5|data-w-|w-formula/.test(preview),
      '書き出し（保存されるもの）は式のまま・結果も印も入らない');

    // ③ 計算した値で並ぶ（昇順・空とエラーは最後）
    await page.locator('#w-editor-content th', { hasText: '価格' }).click();
    g = await grid();
    check(same(g.map((r) => r[0]), ['E2E-F-鋲', 'E2E-F-板', 'E2E-F-棒', 'E2E-F-釘']),
      '価格で並べると計算した値で並ぶ（10 < 37.5・空とエラーは後）', JSON.stringify(g.map((r) => r[0])));
    await page.locator('#w-editor-content th', { hasText: '価格' }).click();
    g = await grid();
    check(same(g.map((r) => r[0]), ['E2E-F-板', 'E2E-F-鋲', 'E2E-F-棒', 'E2E-F-釘']), '降順（37.5 > 10）でもエラーと空は最後',
      JSON.stringify(g.map((r) => r[0])));
    await page.locator('#w-editor-content th', { hasText: '価格' }).click(); // 元の並びへ

    // ④ 編集では式のまま
    await setMode(true);
    check(await page.evaluate(() => document.body.hasAttribute('edit-mode')), '編集モードに入れる');
    g = await grid();
    check(g[0][3] === '=個数*単価' && g[2][3] === '＝個数×単価', '編集では式のまま見える', JSON.stringify(g.map((r) => r[3])));
    check((await cellInfo(1, 3)).cls.includes('cell-formula'), '式のセルに式の印');
    check((await cellInfo(2, 3)).cls.includes('cell-formula-error'), '前回エラーだった式のセルは赤（編集でも分かる）');

    // ⑤ 個数を直して閲覧へ戻ると計算し直す
    await page.evaluate(() => {
      const td = document.querySelector('#w-editor-content table').rows[2].cells[2];
      td.textContent = '4';
    });
    await page.waitForTimeout(2500); // 自動保存（1.5秒）
    await setMode(false);
    await page.waitForFunction(() => {
      const td = document.querySelector('#w-editor-content table').rows[2].cells[3];
      return td && td.textContent.trim() === '400';
    }, null, { timeout: 8000 }).catch(() => {});
    g = await grid();
    check(g[1][3] === '400', '個数を 4 に直して閲覧へ戻ると 100 × 4 = 400', g[1][3]);

    // ⑥〜⑩ 貼り付け
    await setMode(true);
    await paste(1, 0, { 'text/plain': 'P-A\t1\t2\nP-B\t3\t4\nP-C\t5\t6\nP-D\t7\t8\nP-E\t9\t10\n' });
    await page.waitForTimeout(300);
    g = await grid();
    check(same(g.map((r) => r.slice(0, 3)), [['P-A', '1', '2'], ['P-B', '3', '4'], ['P-C', '5', '6'], ['P-D', '7', '8'], ['P-E', '9', '10']]),
      'タブ区切りの 5行×3列がキャレットのセルから右下へ・足りない1行は足した', JSON.stringify(g));
    check(g[0][3] === '=個数*単価' && g[4].length === 4, '貼らなかった列（価格の式）はそのまま・足した行も列の数がそろう', JSON.stringify(g[4]));
    await paste(1, 2, { 'text/plain': '9\tX\tY\tZ\n', 'text/html': '' });
    await page.waitForTimeout(300);
    g = await grid();
    check(g[0][2] === '9' && g[0][3] === 'X', 'はみ出した列は貼らない（個数・価格にだけ入る）', JSON.stringify(g[0]));
    const toast = await page.evaluate(() => Array.from(document.querySelectorAll('.toast')).map((t) => t.textContent).join(' / '));
    check(/はみ出した 2 列/.test(toast), 'はみ出した列の数を知らせる', toast);
    await paste(1, 3, { 'text/plain': '=個数*単価' }); // 1つのセルだけ（タブ無し）は普通の貼り付け
    await page.evaluate(() => { document.querySelector('#w-editor-content table').rows[1].cells[3].textContent = '=個数*単価'; });
    await paste(0, 0, { 'text/plain': 'H1\tH2\nv1\tv2\n' });
    await page.waitForTimeout(300);
    const head = await page.evaluate(() => Array.from(document.querySelector('#w-editor-content table').rows[0].cells).map((c) => c.textContent.trim()));
    g = await grid();
    check(same(head, ['H1', 'H2', '個数', '価格']) && same(g[0].slice(0, 2), ['v1', 'v2']),
      '見出しの行にも貼れる（1行目が見出しを上書き・2行目からデータの行）', JSON.stringify([head, g[0]]));
    await paste(2, 0, { 'text/html': '<table><tr><td>ONE-CELL</td></tr></table>', 'text/plain': 'ONE-CELL\n' });
    await page.waitForTimeout(300);
    const one = await page.evaluate(() => {
      const td = document.querySelector('#w-editor-content table').rows[2].cells[0];
      return { text: td.textContent.trim(), nested: !!td.querySelector('table') };
    });
    check(one.text.endsWith('ONE-CELL') && !one.nested, 'Excel の1セル（表の HTML）は文字だけ入る（表の中に表を作らない）', JSON.stringify(one));
    await page.waitForTimeout(2500); // 自動保存
    const saved = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).text(), id);
    check(saved.includes('P-E') && saved.includes('=個数*単価') && saved.includes('ONE-CELL'), '貼った値と式は保存された');
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
