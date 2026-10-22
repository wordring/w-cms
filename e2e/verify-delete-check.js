// 消す前の確かめ（2026-10-23・internal/cms/delete_impact.go）を、画面の「削除」ボタンから確かめます。
// 利用者:「すべてのページに効く仕組みとして、消す前に①参照のタグと表のセル②本文のリンクとファイル表示（保存のときに索引へ入れる）
// ③そのページの表が集計に使われているか、を調べて警告する形をコアに作ってください。警告が出ても、それでも消せるのは管理者だけにしてください。」
//
// 自分で作ったページの上だけで動き、最後に消します。
//   ① 指されているページで「削除」を押すと、指しているページ（/番号・リンク）を出して確かめる。やめれば消えない。
//   ② 管理者が「それでも」を選ぶと消える（force=1）。
//   ③ 自分の表（索引に入る表）があるページも、表の名前を出して確かめる。
//   ④ 管理者でない人には「管理者に頼んでください」と出て、消しに行かない（確かめの答えを試験のブラウザの中で差し替える）。
//   ⑤ 何も起きないページは、今までどおり1回の確かめで消える。
// WCMS_BASE=https://localhost:8443 node verify-delete-check.js
const { chromium } = require(require('path').join(process.cwd(), 'node_modules', 'playwright'));
const { login, makePage, deletePage } = require('./lib');

const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
let fails = 0;
function check(label, ok, note) {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
}

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let answer = true; // 次の確かめに「はい」と答えるか
  const dialogs = [];
  page.on('dialog', (d) => { dialogs.push(d.type() + ':' + d.message()); answer ? d.accept() : d.dismiss(); });
  let root = '', target = '', referrer = '', withTable = '', plain = '', viewer = '';
  const status = async (id) => page.evaluate(async (x) => (await fetch('/api/load?id=' + x)).status, id);
  const pressDelete = async (id) => {
    await page.goto(BASE + '/' + id);
    await page.evaluate(() => { const t = document.getElementById('w-mode-toggle'); if (!document.body.hasAttribute('edit-mode')) t.click(); });
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    await page.waitForTimeout(300);
    dialogs.length = 0;
    await page.locator('#w-delete-page-btn').click();
    await page.waitForTimeout(1200);
  };
  try {
    await login(page, BASE);
    root = await makePage(page, '<h1>【E2E】消す前の確かめ</h1>');
    target = await makePage(page, '<h1>【E2E】指されるページ</h1><p>本文</p>', root);
    referrer = await makePage(page, '<h1>【E2E】指すページ</h1><p><a href="/' + target + '">指されるページへ</a></p>', root);
    withTable = await makePage(page, '<h1>【E2E】表のあるページ</h1><table><caption>材料表</caption><tbody><tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>' +
      '<tr><td>SS400</td><td>板</td><td>t3</td><td>1</td></tr></tbody></table>', root);
    plain = await makePage(page, '<h1>【E2E】何も無いページ</h1><p>本文だけ</p>', root);
    check('当て先を作れた', !!(root && target && referrer && withTable && plain));

    // ①
    answer = false;
    await pressDelete(target);
    const d1 = dialogs.join(' / ');
    check('① 指しているページを出して確かめる', /confirm:/.test(d1) && d1.includes('/' + referrer) && d1.includes('リンク'), d1.slice(0, 160));
    check('① やめれば消えない', (await status(target)) === 200);
    // ②
    answer = true;
    await pressDelete(target);
    check('② 管理者が「それでも」を選ぶと消える', (await status(target)) === 404);
    // ③
    answer = false;
    await pressDelete(withTable);
    const d3 = dialogs.join(' / ');
    check('③ 自分の表の名前を出して確かめる', d3.includes('材料表') && d3.includes('1 行'), d3.slice(0, 200));
    check('③ やめれば消えない', (await status(withTable)) === 200);
    // ④ 管理者でない人（確かめの答えの can_force を試験のブラウザの中で false にする）。
    let deleteCalls = 0;
    await page.route('**/api/delete-check*', async (route) => {
      const res = await route.fetch();
      const body = await res.json();
      body.can_force = false;
      await route.fulfill({ response: res, json: body });
    });
    page.on('request', (r) => { if (r.url().includes('/api/delete-page')) deleteCalls++; });
    answer = true;
    await pressDelete(withTable);
    const d4 = dialogs.join(' / ');
    check('④ 管理者でない人には「管理者に頼んで」と出す', /alert:/.test(d4) && d4.includes('管理者に頼んで'), d4.slice(0, 160));
    check('④ 消しに行かない', deleteCalls === 0 && (await status(withTable)) === 200);
    await page.unroute('**/api/delete-check*');
    // ⑤
    answer = true;
    await pressDelete(plain);
    const d5 = dialogs.join(' / ');
    check('⑤ 何も起きないページは、今までどおりの1回の確かめで消える', dialogs.length === 1 && !d5.includes('⚠') && (await status(plain)) === 404, d5.slice(0, 120));
    viewer = page.url();
    check('画面のエラーが無い', errs.length === 0, errs.join(' / '));
  } catch (e) {
    check('途中で止まらない', false, String(e));
  } finally {
    for (const id of [target, referrer, withTable, plain, root]) await deletePage(page, id);
    await browser.close();
  }
  console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
  process.exit(fails ? 1 : 0);
})();
