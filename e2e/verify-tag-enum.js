// タグの値に enum の選択肢を出す（assets/app.js の updateEnumMenu・tagValueColumn）を画面から確かめる（2026-10-09）。
//
// 利用者:「タグにはコンボボックスで候補を出す設定が無かったでしょうか？」→「enum のタグ全部に出す」——
// 【要求】タグと表 §11「タグの値にも、型に応じた入力補助を」。それまで選択肢は表のセルにだけ出ていた。
//
//   ① 編集モードで enum のタグ（設定の語彙の `在籍`）の値を触ると、語彙の選択肢が出る
//   ② 選ぶと値が替わって保存され、選択肢の中にある印（tag-known）が付く
//   ③ 空の値（`移行中` の <br>）でも出て、選べる
//   ④ 語彙に選択肢の無いタグ（`名前`）では出ない
//   ⑤ 閲覧モードでは出ない
//
// 当て先はトップ直下に自分で作るページ1枚で、最後に消します。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-tag-enum.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

const MARK = 'E2E' + Date.now().toString(36).toUpperCase();
const BODY = '<h1>' + MARK + ' タグの選択肢</h1><dl data-type="tags">' +
  '<dt>名前</dt><dd>みなと 太郎</dd>' +
  '<dt>在籍</dt><dd>在籍</dd>' +
  '<dt>移行中</dt><dd><br></dd>' +
  '</dl><p>本文</p>';

// ddOf は名前 name のタグの値に印を付け、そのセレクタを返します。
const ddOf = (page, name) => page.evaluate((name) => {
  const dt = [...document.querySelectorAll('#w-editor-content dl[data-type="tags"] > dt')]
    .find((x) => x.textContent.trim() === name);
  const dd = dt && dt.nextElementSibling;
  if (!dd) return null;
  dd.setAttribute('data-e2e', 'tag-' + name);
  return '[data-e2e="tag-' + name + '"]';
}, name);

const menuItems = (page) => page.evaluate(() => {
  const m = document.getElementById('w-enum-menu');
  if (!m || !m.classList.contains('active')) return [];
  return [...m.querySelectorAll('button')].map((b) => b.textContent.trim());
});

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  let id = '';
  try {
    await login(page, BASE);
    id = await makePage(page, BODY);
    check('ページを作った', !!id, id);

    await page.goto(BASE + '/' + id + '?edit=true');
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    await page.waitForTimeout(600);

    // ①
    await page.click(await ddOf(page, '在籍'));
    await page.waitForTimeout(400);
    const items = await menuItems(page);
    check('① enum のタグの値で語彙の選択肢が出る',
      JSON.stringify(items) === JSON.stringify(['在籍', '休職', '出向', '退社']), JSON.stringify(items));
    if (process.env.SHOT) await page.screenshot({ path: process.env.SHOT });

    // ②
    await page.locator('#w-enum-menu button', { hasText: '休職' }).click();
    const sel = await ddOf(page, '在籍');
    check('② 選ぶと値が替わる', (await page.$eval(sel, (d) => d.textContent.trim())) === '休職');
    check('② 選択肢の中にある印が付く', await page.$eval(sel, (d) => d.classList.contains('tag-known')));
    check('② 選ぶとメニューは閉じる', (await menuItems(page)).length === 0);
    await page.waitForFunction(() => document.getElementById('w-save-status').innerText.includes('保存済'), null, { timeout: 8000 });
    const saved = await page.evaluate(async (id) => (await fetch('/api/load?id=' + id)).text(), id);
    check('② 保存される', /<dt>\s*在籍\s*<\/dt>\s*<dd>\s*休職\s*<\/dd>/.test(saved));

    // ③
    await page.click(await ddOf(page, '移行中'));
    await page.waitForTimeout(400);
    const empty = await menuItems(page);
    check('③ 空の値でも出る', JSON.stringify(empty) === JSON.stringify(['確認待ち']), JSON.stringify(empty));
    await page.locator('#w-enum-menu button', { hasText: '確認待ち' }).click();
    check('③ 選べる', (await page.$eval(await ddOf(page, '移行中'), (d) => d.textContent.trim())) === '確認待ち');

    // ④
    await page.click(await ddOf(page, '名前'));
    await page.waitForTimeout(400);
    check('④ 選択肢の無いタグでは出ない', (await menuItems(page)).length === 0);

    // ⑤
    await page.waitForFunction(() => document.getElementById('w-save-status').innerText.includes('保存済'), null, { timeout: 8000 });
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForFunction(() => !document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    await page.click(await ddOf(page, '在籍'));
    await page.waitForTimeout(400);
    check('⑤ 閲覧モードでは出ない', (await menuItems(page)).length === 0);
    check('ページエラーなし', errs.length === 0, errs.join(' / '));
  } catch (e) {
    check('例外なし: ' + e.message, false);
  } finally {
    await deletePage(page, id);
    await browser.close();
  }
  console.log(fails ? '\n' + fails + ' 件の失敗' : '\n全項目OK');
  process.exit(fails ? 1 : 0);
})();
