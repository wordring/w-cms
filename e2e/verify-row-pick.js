// 押した行に色を付ける（2026-10-29・assets/app.js「押した行に色を付ける」・app.css の w-row-picked）をブラウザから確かめる。
//
// 利用者:「表を横スクロールしたときの問題点として、どの行を見ていたかわからなくなることがあります。表全般について、
// クリックした行に背景に色を付けると良いのかもしれません」。
//
//   閲覧モード: ① 押した行に印（背景に薄い灰色を重ね、左の端に濃い帯——セルの矩形の選択の青と見分けがつく）② 印は1つ——ほかの行を押すと移る ③ 見出しだけの行は印を付けない
//     ④ Esc で外れる ⑤ 横に長い表を横へ送っても、印の行はそのまま
//   書ける形: ⑥ 押した行に印・矢印キーで動いた行にも印 ⑦ 印は書き出し（保存されるもの）に入らず、「未保存」にもならない
//
// 自分で作ったページで動かし、最後に消します。
// 使い方: WCMS_BASE=https://localhost:9443 node verify-row-pick.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

const wide = Array.from({ length: 14 }, (_, i) => '<th>列' + i + '</th>').join('');
// 1列目は短く（長いと折り返し、矢印キーが同じセルの次の行へ動く）。ほかの列は長くして、表を横へはみ出させる。
const row = (k) => '<tr>' + Array.from({ length: 14 }, (_, i) => '<td>' + k + '-' + i + (i ? ' のやや長い値' : '') + '</td>').join('') + '</tr>';
const BODY = '<h1>【E2E】押した行</h1>' +
  '<table><caption>E2E 行</caption><tbody><tr>' + wide + '</tr>' + row('RP-a') + row('RP-b') + row('RP-c') + '</tbody></table>';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 900, height: 800 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(e.message));
  await login(page, BASE);

  let bad = 0;
  const check = (ok, msg, detail) => {
    if (ok) console.log('✓ ' + msg);
    else { console.log('✗ ' + msg + (detail ? '（' + detail + '）' : '')); bad++; }
  };
  const picked = () => page.evaluate(() => Array.from(document.querySelectorAll('#w-editor-content table tr'))
    .map((tr, i) => (tr.classList.contains('w-row-picked') ? i : -1)).filter((i) => i >= 0));
  const cell = (r, c) => page.locator('#w-editor-content table').first().locator('tr').nth(r).locator('td, th').nth(c);
  const setMode = async (edit) => {
    const now = await page.evaluate(() => document.body.hasAttribute('edit-mode'));
    if (now === edit) return;
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForFunction((e) => document.body.hasAttribute('edit-mode') === e, edit, { timeout: 5000 }).catch(() => {});
    await page.waitForTimeout(600);
  };

  const id = await makePage(page, BODY);
  try {
    await page.goto(BASE + '/' + id);
    await page.waitForSelector('#w-editor-content table');
    await setMode(false);

    // ① ②
    await cell(2, 1).click();
    check(JSON.stringify(await picked()) === '[2]', '① 押した行に印が付く', JSON.stringify(await picked()));
    const bg = await cell(2, 3).evaluate((td) => getComputedStyle(td).backgroundImage);
    check(bg.includes('gradient') && bg.includes('71, 85, 105'), '① 印の行のセルに薄い灰色を重ねる', bg);
    const bar = await cell(2, 0).evaluate((td) => getComputedStyle(td).boxShadow);
    check(/4px 0px 0px/.test(bar) && bar.includes('71, 85, 105'), '① 行の左の端に濃い帯', bar);
    // セルの矩形の選択（薄い青と青い枠）と見分けがつく（2026-10-29 利用者:「表のクリック行強調表示と矩形選択の見分けがつかなくなっています」）。
    const a = await cell(2, 2).boundingBox(), b = await cell(2, 4).boundingBox();
    await page.mouse.move(a.x + a.width / 2, a.y + a.height / 2);
    await page.mouse.down();
    await page.mouse.move(b.x + b.width / 2, b.y + b.height / 2, { steps: 6 });
    await page.mouse.up();
    const look = (c) => cell(2, c).evaluate((td) => {
      const s = getComputedStyle(td);
      return { sel: td.classList.contains('w-cell-selected'), color: s.backgroundColor, image: s.backgroundImage, shadow: s.boxShadow };
    });
    const sel = await look(3), rowOnly = await look(6);
    check(sel.sel && sel.color === 'rgb(219, 234, 254)' && sel.image === 'none' && /96, 165, 250/.test(sel.shadow),
      '① 矩形で選んだセルは薄い青と青い枠（行の灰色は重ねない）', JSON.stringify(sel));
    check(!rowOnly.sel && rowOnly.image.includes('71, 85, 105') && !/96, 165, 250/.test(rowOnly.shadow) && rowOnly.color !== sel.color,
      '① 同じ行の選んでいないセルは灰色だけ（青も枠も無い）', JSON.stringify(rowOnly));
    await page.keyboard.press('Escape');
    await cell(3, 0).click();
    check(JSON.stringify(await picked()) === '[3]', '② ほかの行を押すと印が移る（1つだけ）', JSON.stringify(await picked()));
    // ③
    await cell(0, 2).click();
    check(JSON.stringify(await picked()) === '[3]', '③ 見出しだけの行には印を付けない', JSON.stringify(await picked()));
    // ⑤ 横へ送っても印はそのまま
    await page.evaluate(() => {
      const t = document.querySelector('#w-editor-content table');
      let s = t.parentElement;
      while (s && s !== document.body && s.scrollWidth <= s.clientWidth) s = s.parentElement;
      if (s && s !== document.body) s.scrollLeft = s.scrollWidth;
    });
    await page.waitForTimeout(200);
    const far = await cell(3, 13).evaluate((td) => getComputedStyle(td).backgroundImage);
    check(JSON.stringify(await picked()) === '[3]' && far.includes('gradient'), '⑤ 横へ送っても、右の端のセルまで印の色', far);
    // ④
    await page.keyboard.press('Escape');
    check((await picked()).length === 0, '④ Esc で外れる');

    // ⑥ ⑦ 書ける形
    await setMode(true);
    await cell(1, 0).click();
    check(JSON.stringify(await picked()) === '[1]', '⑥ 書ける形でも押した行に印', JSON.stringify(await picked()));
    await page.keyboard.press('ArrowDown');
    await page.waitForTimeout(200);
    check(JSON.stringify(await picked()) === '[2]', '⑥ 矢印キーで動いた行に印が移る', JSON.stringify(await picked()));
    const dirty = await page.evaluate(() => document.body.classList.contains('is-dirty') ||
      /未保存/.test((document.getElementById('w-save-status') || {}).textContent || ''));
    check(!dirty, '⑦ 印だけでは「未保存」にならない');
    const preview = await page.locator('#w-html-preview').inputValue();
    check(!/w-row-picked/.test(preview), '⑦ 印は書き出し（保存されるもの）に入らない');
    await page.evaluate(() => document.activeElement && document.activeElement.blur());
    await page.waitForTimeout(1500);
    const saved = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).text(), id);
    check(!/w-row-picked/.test(saved) && saved.includes('RP-b-0'), '⑦ 保存された本文にも印は無い');
    await setMode(false);
  } catch (e) {
    check(false, '例外なく流れた', String(e));
  } finally {
    await deletePage(page, id);
  }
  check(errs.length === 0, 'JavaScript エラーなし', errs.join(' / '));
  console.log(bad === 0 ? '\n結果: すべて通りました' : '\n結果: ' + bad + ' 件落ちました');
  await browser.close();
  process.exitCode = bad === 0 ? 0 : 1;
})();
