// 管理画面の「タグの値を置き換える」（2026-10-01・internal/cms/tag_rename.go）を画面から確かめる。
//
// 利用者:「見積もりという見出しや値は見積と短くした方がよいと思います。これは設定ファイルも変える必要があると思います。」
// ——選択肢の言葉を変えたら、既にあるページのタグも揃える。
//
//   ① 試験用のタグ（名前も値も試験だけのもの）を持つページを2枚作る
//   ② 管理画面で名前・今の値・新しい値を書いて押す → 数を見せて確かめる（2 ページ）→ 置き換えた
//   ③ 2枚とも新しい値になり、地の文の同じ言葉は残る
//
// 作ったページは最後に消します。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-tag-rename.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, bodyOf } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  const made = [];
  try {
    await login(page, BASE);
    const body = (t) => `<h1>【E2E】${t}</h1><dl data-type="tags"><dt>E2E区分</dt><dd>E2E旧</dd></dl><p>E2E旧</p>`;
    made.push(await makePage(page, body('置き換え甲')));
    made.push(await makePage(page, body('置き換え乙')));
    check('① 試験のページを2枚作った', made.every((id) => /^\d{6}$/.test(id)), made.join(','));

    await page.goto(BASE + '/assets/admin.html');
    await page.waitForSelector('#tr-go', { state: 'visible', timeout: 10000 });
    await page.fill('#tr-name', 'E2E区分');
    await page.fill('#tr-from', 'E2E旧');
    await page.fill('#tr-to', 'E2E新');
    let asked = '';
    page.once('dialog', async (d) => { asked = d.message(); await d.accept(); });
    await page.click('#tr-go');
    await page.waitForFunction(() => /置き換えました|ありません|られませんでした/.test(document.getElementById('tr-msg').textContent), null, { timeout: 15000 }).catch(() => {});
    const said = await page.textContent('#tr-msg');
    check('② 押す前に数を見せて確かめる', asked.includes('2 ページ') && asked.includes('E2E区分：E2E旧'), asked);
    check('② 置き換えたと言う', said.includes('2 ページを置き換えました'), said);

    for (const id of made) {
      const b = await bodyOf(page, id);
      check('③ ' + id + ' のタグが新しい値・地の文は元のまま', b.includes('<dd>E2E新</dd>') && !b.includes('<dd>E2E旧</dd>') && b.includes('<p>E2E旧</p>'), b.slice(0, 160));
    }
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    for (const id of made) await deletePage(page, id).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
