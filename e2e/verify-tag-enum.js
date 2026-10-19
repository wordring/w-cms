// タグの値に enum の選択肢を出す（assets/app.js の updateEnumMenu・tagValueColumn）を画面から確かめる（2026-10-09）。
//
// 利用者:「タグにはコンボボックスで候補を出す設定が無かったでしょうか？」→「enum のタグ全部に出す」——
// 【要求】タグと表 §11「タグの値にも、型に応じた入力補助を」。それまで選択肢は表のセルにだけ出ていた。
//
//   ① 編集モードで enum のタグ（設定の語彙の `在籍`）の値を触ると、語彙の選択肢が出る
//   ② 選ぶと値が替わって保存され、選択肢の中にある印（tag-known）が付く
//   ③ 空の値（`移行中` の <br>）でも出て、選べる
//   ④ どのページにも値の無いタグでは出ない
//   ⑤ 閲覧モードでは出ない
//   ⑥ 選択肢の無いタグでも、DB にある値を多い順に出す（2026-10-19・/api/tag-values）
//      ——利用者:「この値の数を数えて多い方から順に、ページ種類のタグを編集するときに値のコンボボックスとして出します。」
//      ① の enum の選択肢も、DB の数の順に並ぶ。
//
// 当て先はトップ直下に自分で作るページ（試すページ1枚と、⑥の値を持たせる3枚）で、最後に消します。
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
const COLOR = MARK + '色'; // ⑥ のタグ（この試験のページにしか無い名前）
const NOTE = MARK + 'メモ'; // ④ のタグ（どのページにも値が無い）
const BODY = '<h1>' + MARK + ' タグの選択肢</h1><dl data-type="tags">' +
  '<dt>名前</dt><dd>みなと 太郎</dd>' +
  '<dt>' + COLOR + '</dt><dd><br></dd>' +
  '<dt>' + NOTE + '</dt><dd><br></dd>' +
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

// menuWait は、候補が届いて開くのを待ってから並びを返します（2026-10-19 から候補は口から遅れて届く）。出なければ空。
const menuWait = async (page, ms = 3000) => {
  const until = Date.now() + ms;
  for (;;) {
    const items = await menuItems(page);
    if (items.length || Date.now() > until) return items;
    await page.waitForTimeout(150);
  }
};

// caretIn はタグの値にキャレットを置きます（帯が値の上に重なって、押すと帯に当たることがある）。
const caretIn = (page, sel) => page.$eval(sel, (el) => {
  el.focus();
  const r = document.createRange();
  r.selectNodeContents(el);
  r.collapse(false);
  const s = window.getSelection();
  s.removeAllRanges();
  s.addRange(r);
});

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  let id = '';
  const helpers = [];
  try {
    await login(page, BASE);
    // ⑥ の値を持たせるページ（赤 2枚・青 1枚）——試すページより先に作る。
    for (const v of ['赤', '青', '赤']) {
      helpers.push(await makePage(page, '<h1>' + MARK + ' 色の見本</h1><dl data-type="tags"><dt>' + COLOR + '</dt><dd>' + v + '</dd></dl>'));
    }
    id = await makePage(page, BODY);
    check('ページを作った', !!id, id);

    await page.goto(BASE + '/' + id + '?edit=true');
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    await page.waitForTimeout(600);

    // ①
    await page.click(await ddOf(page, '在籍'));
    const items = await menuWait(page);
    // 並びは DB の数の順（同じ数なら語彙の順）——数は口から取って、期待を組む。
    const vocab = ['在籍', '休職', '出向', '退社'];
    const counts = await page.evaluate(async () => (await (await fetch('/api/tag-values?name=' + encodeURIComponent('在籍'))).json()).values || []);
    const n = (v) => (counts.find((x) => x.value === v) || { count: 0 }).count;
    const want = vocab.map((v, i) => ({ v, i })).sort((a, b) => (n(b.v) - n(a.v)) || (a.i - b.i)).map((x) => x.v);
    check('① enum のタグの値で語彙の選択肢が、DB の数の順に出る',
      JSON.stringify(items) === JSON.stringify(want), JSON.stringify(items) + ' 期待 ' + JSON.stringify(want));
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
    await caretIn(page, await ddOf(page, '移行中'));
    const empty = await menuWait(page);
    check('③ 空の値でも出る', JSON.stringify(empty) === JSON.stringify(['確認待ち']), JSON.stringify(empty));
    await page.locator('#w-enum-menu button', { hasText: '確認待ち' }).click();
    check('③ 選べる', (await page.$eval(await ddOf(page, '移行中'), (d) => d.textContent.trim())) === '確認待ち');

    // ④
    await caretIn(page, await ddOf(page, NOTE));
    await page.waitForTimeout(1500);
    check('④ どのページにも値の無いタグでは出ない', (await menuItems(page)).length === 0);

    // ⑥
    await caretIn(page, await ddOf(page, COLOR));
    const colors = await menuWait(page);
    check('⑥ 選択肢の無いタグでも、DB の値が多い順に出る（赤 2・青 1）', JSON.stringify(colors) === JSON.stringify(['赤', '青']), JSON.stringify(colors));
    await page.locator('#w-enum-menu button', { hasText: '青' }).click();
    check('⑥ 選ぶと値が替わる', (await page.$eval(await ddOf(page, COLOR), (d) => d.textContent.trim())) === '青');

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
    for (const h of helpers) await deletePage(page, h);
    await browser.close();
  }
  console.log(fails ? '\n' + fails + ' 件の失敗' : '\n全項目OK');
  process.exit(fails ? 1 : 0);
})();
