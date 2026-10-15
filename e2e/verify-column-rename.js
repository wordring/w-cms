// 管理画面の「表の列の名前を置き換える」（2026-10-15・internal/cms/column_rename.go）を画面から確かめる。
//
// 利用者:「受注フォルダの表の「出荷済み」を出荷済数量に変更します」→ 列ごと替える
// ——語彙で列の名前を変えたら、既にあるページの表の見出しも揃える。
//
//   ① 試験用のキャプションの表を持つページを2枚作る（キャプションも列の名前も試験だけのもの）
//   ② 管理画面でキャプション・今の名前・新しい名前を書いて押す → 数を見せて確かめる（2 ページ）→ 置き換えた
//   ③ 2枚とも見出しが新しい名前になり、セルの値・地の文の同じ言葉は残る
//
// 作ったページは最後に消します。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-column-rename.js
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
    const body = (t) => `<h1>【E2E】${t}</h1><table><caption>E2E明細</caption><tbody><tr><th>品名</th><th>E2E旧</th></tr><tr><td>E2E旧</td><td>1</td></tr></tbody></table><p>E2E旧</p>`;
    made.push(await makePage(page, body('置き換え甲')));
    made.push(await makePage(page, body('置き換え乙')));
    check('① 試験のページを2枚作った', made.every((id) => /^\d{6}$/.test(id)), made.join(','));

    await page.goto(BASE + '/assets/admin.html');
    await page.waitForSelector('#cr-go', { state: 'visible', timeout: 10000 });
    await page.fill('#cr-caption', 'E2E明細');
    await page.fill('#cr-from', 'E2E旧');
    await page.fill('#cr-to', 'E2E新');
    let asked = '';
    page.once('dialog', async (d) => { asked = d.message(); await d.accept(); });
    await page.click('#cr-go');
    await page.waitForFunction(() => /置き換えました|ありません|られませんでした/.test(document.getElementById('cr-msg').textContent), null, { timeout: 15000 }).catch(() => {});
    const said = await page.textContent('#cr-msg');
    check('② 押す前に数を見せて確かめる', asked.includes('2 ページ') && asked.includes('「E2E明細」の列「E2E旧」'), asked);
    check('② 置き換えたと言う', said.includes('2 ページを置き換えました'), said);

    for (const id of made) {
      const b = await bodyOf(page, id);
      check('③ ' + id + ' の見出しが新しい名前・セルと地の文は元のまま', b.includes('<th>E2E新</th>') && !b.includes('<th>E2E旧</th>') && b.includes('<td>E2E旧</td>') && b.includes('<p>E2E旧</p>'), b.slice(0, 160));
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
