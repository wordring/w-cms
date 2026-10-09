// 機械に向けたタグを閲覧モードで「▸ 詳細」へ畳む（assets/app.js の foldMachineTags）を画面から確かめる。
//
// 2026-10-09 にメールのヘッダの写しを `Message-ID`・`In-Reply-To` の名前へ改めたとき、畳む判定が名前の末尾
// （`メッセージID`）で決めていたので、改めた名前が畳まれずに出ていた。それを直したときの番人。
// 2026-09-03 から verify-preview.js の中にあった畳み込みの確かめをここへ移した（あちらは解析のボタンを押すので
// 実運用の職場では流せない——こちらは自分で作ったページだけで動く）。
//
//   ① 編集モードでは畳まない
//   ② 閲覧モードで `差出人アドレス`（対の `差出人` がある）・`Message-ID`・`In-Reply-To`・直す前の名前
//      `返信元メッセージID` が畳まれ、`差出人`・`受信日時`・`親ページID`（人が前後をたどる参照）は見えるまま
//   ③ 「詳細」を押すと全部見え、もう一度押すと畳まれる
//   ④ 対の無い `メールアドレス`（連絡先のページ）は畳まない
//   ⑤ 畳んでも本文には対が残り、畳むための印は本文に漏れない
//
// 当て先はトップ直下に自分で作るページ2枚で、最後に消します。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-tag-fold.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

const MARK = 'E2E' + Date.now().toString(36).toUpperCase();
const rec = (title, pairs) => '<h1>' + title + '</h1><dl data-type="tags">' +
  pairs.map(([k, v]) => '<dt>' + k + '</dt><dd>' + v + '</dd>').join('') + '</dl>';

const visibleTagNames = (page) => page.evaluate(() => Array.from(
  document.querySelectorAll('#w-editor-content dl[data-type="tags"] dt'))
  .filter(el => getComputedStyle(el).display !== 'none')
  .map(el => el.textContent.trim()));

async function setEditMode(page, on) {
  await page.evaluate(v => {
    const t = document.getElementById('w-mode-toggle');
    if (t.checked !== v) { t.checked = v; t.dispatchEvent(new Event('change', { bubbles: true })); }
  }, on);
  await page.waitForFunction(v => document.body.hasAttribute('edit-mode') === v, on, { timeout: 8000 });
}

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  const ids = [];
  try {
    await login(page, BASE);
    ids.push(await makePage(page, rec(MARK + ' 畳む', [
      ['差出人', 'みなと商店'],
      ['差出人アドレス', 'minato@example.jp'],
      ['受信日時', '2020-01-01T10:00:00+09:00'],
      ['Message-ID', '&lt;' + MARK + '-b@x&gt;'],
      ['In-Reply-To', '&lt;' + MARK + '-a@x&gt;'],
      ['親ページID', '000000'],
      ['返信元メッセージID', '&lt;' + MARK + '-old@x&gt;'],
    ])));
    ids.push(await makePage(page, rec(MARK + ' 連絡先', [
      ['名前', 'みなと 太郎'],
      ['メールアドレス', 'taro@example.jp'],
    ])));
    check('ページを2枚作った', ids.every(Boolean), ids.join(','));

    await page.goto(BASE + '/' + ids[0] + '?edit=true');
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    check('① 編集モードでは畳まない', await page.locator('#w-editor-content .tag-detail-toggle').count() === 0);

    await setEditMode(page, false);
    await page.waitForSelector('#w-editor-content .tag-detail-toggle', { timeout: 8000 });
    const shown = await visibleTagNames(page);
    check('② 閲覧モードで機械向けのタグが畳まれる',
      JSON.stringify(shown) === JSON.stringify(['差出人', '受信日時', '親ページID']), JSON.stringify(shown));
    const detail = page.locator('#w-editor-content .tag-detail-toggle');
    check('② チップに件数が出る', (await detail.textContent()).includes('4件'), await detail.textContent());
    await detail.click();
    const opened = await visibleTagNames(page);
    check('③ 開くと全部見える', opened.length === 7, JSON.stringify(opened));
    await detail.click();
    check('③ もう一度押すと畳まれる', (await visibleTagNames(page)).length === 3);

    await page.goto(BASE + '/' + ids[1]);
    await page.waitForSelector('#w-editor-content dl[data-type="tags"]', { timeout: 8000 });
    check('④ 対の無いメールアドレスは畳まない',
      await page.locator('#w-editor-content .tag-detail-toggle').count() === 0 &&
      JSON.stringify(await visibleTagNames(page)) === JSON.stringify(['名前', 'メールアドレス']));

    const html = await page.evaluate(async (id) => (await fetch('/api/load?id=' + id)).text(), ids[0]);
    check('⑤ 畳んでも本文には対が残っている',
      /<dt>\s*Message-ID\s*<\/dt>\s*<dd>/.test(html) && /<dt>\s*In-Reply-To\s*<\/dt>\s*<dd>/.test(html));
    check('⑤ 畳むための印は本文に漏れない', !html.includes('tag-detail-toggle') && !html.includes('tag-folded'));
    check('ページエラーなし', errs.length === 0, errs.join(' / '));
  } catch (e) {
    check('例外なし: ' + e.message, false);
  } finally {
    for (const id of ids) await deletePage(page, id);
    await browser.close();
  }
  console.log(fails ? '\n' + fails + ' 件の失敗' : '\n全項目OK');
  process.exit(fails ? 1 : 0);
})();
