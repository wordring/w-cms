// 表の編集メニュー（行・列のツールバー）をドラッグで動かせることを画面から確かめる（2026-09-27）。
//
// 利用者:「表の編集時に、メニューが邪魔な場所に出がちなので、メニューをドラッグして位置変更が
// できるようにしてください」。
//
//   ① 左端のつまみ（⠿）をドラッグすると動き、その画面位置に留まる（別の行へ移っても・スクロールしても）
//   ② 動かしたあともボタンが効く（どの行への操作かを失わない）
//   ③ ページを開き直しても留めた位置に出る（UI ストア `toolbars.table`）
//   ④ つまみのダブルクリックで元の「行に付いて回る」出し方へ戻る
//   ⑤ つまみを押しただけ（動かさない）では留まらない
//
// 当て先は自分で作って最後に消します（トップ直下に1枚）。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-toolbar-drag.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const near = (a, b) => Math.abs(a - b) <= 2;

const BODY = '<h1>【E2E】ツールバーを動かす</h1>' +
  '<table><tbody><tr><th>品名</th><th>数量</th></tr>' +
  '<tr><td>一</td><td>1</td></tr><tr><td>二</td><td>2</td></tr><tr><td>三</td><td>3</td></tr>' +
  '</tbody></table><p>あとがき</p>';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 800 } });
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  let id = '';
  const bar = () => page.locator('#w-table-toolbar');
  const barBox = async () => bar().boundingBox();
  const pinned = async () => bar().evaluate(el => el.classList.contains('pinned'));
  const openEditor = async () => {
    await page.goto(BASE + '/' + id + '?edit=true');
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
  };
  const clickCell = async (text) => {
    await page.locator('#w-editor-content td', { hasText: text }).first().click();
    await page.waitForSelector('#w-table-toolbar.active', { timeout: 4000 });
    // ⚠ ツールバーは selectionchange で**遅れて**置き直される——待たずに読むと前の位置を読み、
    //    「別の行へ移っても留まる」が何も見分けなくなる（変異で空振りした・2026-09-27）。
    await page.waitForTimeout(250);
  };
  try {
    await login(page, BASE);
    // 前に留めた位置が残っていれば消す（この試験は「行に付いて回る」から始める）。
    await page.evaluate(() => {
      try {
        const o = JSON.parse(localStorage.getItem('wcms.ui') || '{}');
        delete o.toolbars;
        localStorage.setItem('wcms.ui', JSON.stringify(o));
      } catch (e) { /* 使えない環境は既定のまま */ }
    });
    id = await makePage(page, BODY);
    check('当て先を作れた', !!id, id);
    await openEditor();

    // ── はじめは行に付いて回る ──
    await clickCell('一');
    const auto1 = await barBox();
    check('ツールバーにつまみがある', await page.locator('#w-table-toolbar .toolbar-grip').count() === 1);
    check('はじめは留めていない', !(await pinned()));

    // ⑤ 押しただけでは留まらない。
    const g0 = await page.locator('#w-table-toolbar .toolbar-grip').boundingBox();
    await page.mouse.click(g0.x + g0.width / 2, g0.y + g0.height / 2);
    check('つまみを押しただけでは留まらない', !(await pinned()));

    // ① ドラッグで動かす。
    const g = await page.locator('#w-table-toolbar .toolbar-grip').boundingBox();
    const sx = g.x + g.width / 2, sy = g.y + g.height / 2;
    await page.mouse.move(sx, sy);
    await page.mouse.down();
    await page.mouse.move(sx - 150, sy + 220, { steps: 8 });
    await page.mouse.up();
    const dropped = await barBox();
    check('ドラッグで動いた', near(dropped.x, auto1.x - 150) && near(dropped.y, auto1.y + 220),
      `(${Math.round(auto1.x)},${Math.round(auto1.y)}) → (${Math.round(dropped.x)},${Math.round(dropped.y)})`);
    check('留めた（pinned）', await pinned());
    const saved = await page.evaluate(() => { try { return JSON.parse(localStorage.getItem('wcms.ui')).toolbars.table; } catch (e) { return null; } });
    check('位置を覚えた（UI ストア toolbars.table）', !!saved && typeof saved.x === 'number');

    // 別の行へ移っても動かない。
    await clickCell('三');
    const afterRow = await barBox();
    check('別の行へ移っても留まる', near(afterRow.x, dropped.x) && near(afterRow.y, dropped.y));

    // ② 動かしたあともボタンが効く（キャレットのある行＝「三」の下に行が増える）。
    const before = await page.locator('#w-editor-content tr').count();
    await page.locator('#w-tt-add').click();
    const after = await page.locator('#w-editor-content tr').count();
    check('動かしたあとも「＋行」が効く', after === before + 1, `${before} → ${after}`);

    // スクロールしても画面の同じ場所。
    await page.evaluate(() => window.scrollBy(0, 120));
    const afterScroll = await barBox();
    check('スクロールしても画面の同じ場所に留まる', near(afterScroll.y, dropped.y));
    await page.evaluate(() => window.scrollTo(0, 0));

    // ③ 開き直しても留めた位置に出る。
    await page.waitForTimeout(2000); // 自動保存（1.5秒）を流してから離れる
    await openEditor();
    await clickCell('二');
    const reopened = await barBox();
    check('開き直しても留めた位置に出る', (await pinned()) && near(reopened.x, dropped.x) && near(reopened.y, dropped.y));

    // ④ ダブルクリックで元の出し方へ。
    const g2 = await page.locator('#w-table-toolbar .toolbar-grip').boundingBox();
    await page.mouse.dblclick(g2.x + g2.width / 2, g2.y + g2.height / 2);
    const back = await barBox();
    const cell = await page.locator('#w-editor-content td', { hasText: '二' }).first().boundingBox();
    check('ダブルクリックで留めるのをやめる', !(await pinned()));
    check('行に付いて回る位置へ戻る', Math.abs(back.y - cell.y) < 80, `ツールバー y=${Math.round(back.y)} 行 y=${Math.round(cell.y)}`);
    const cleared = await page.evaluate(() => { try { return JSON.parse(localStorage.getItem('wcms.ui')).toolbars.table; } catch (e) { return 'err'; } });
    check('覚えた位置を消した', cleared == null);
    check('JSエラーなし', errs.length === 0, errs.slice(0, 2).join(' | '));
  } catch (e) {
    check('最後まで到達', false, String(e));
  } finally {
    await deletePage(page, id);
    await browser.close();
  }
  console.log(fails ? `\n✗ ${fails} 件の失敗` : '\n全項目OK');
  process.exit(fails ? 1 : 0);
})();
