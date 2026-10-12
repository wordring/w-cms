// 保存の機会と、ほかの人のキャレットのあるブロック（2026-10-12 昼）を2つのタブで確かめる。
//
// 利用者:「保存は、ブロックを去る時とページを去る時に限定して、細かい通信を抑制してはどうでしょう？一文字打っただけで競合を検出し
// マージしろと言われても困ります。一方で、他人が同じブロックにキャレットを置いている、あるいはすでに文字を打ち込んでいるような
// 場合は、早期に警告して欲しいです」→ 保険の保存は置かない・タブを隠したら保存・「置いただけで背景色を黄色系の彩度の低い色に…
// シンプルに」・表は表ごと（【考察】編集モードの再考 §8）。
//
//   ① A がブロックにキャレットを置くと、B の画面でそのブロックが薄い黄色（文字の説明は出さない）
//   ② A が動けば黄色も動く・表は表ごと（セルを移っても表のまま）
//   ③ A が閲覧モードにすると消え、書ける形で置き直すとまた出る・A がタブを閉じると消える
//   ④ 打っているあいだは保存しない（3秒待っても送らない・「未保存」）——ブロックを去ると保存
//   ⑤ タブを隠すと保存
//   ⑥ 相手の保存を受け取りに行くときも、書いている最中のブロックは送らない
//   ⑦ 書いている最中のブロックを相手が保存しても、書いている間は枠を出さない——去って送ったときに並べる（最後の守り）
//
// 2つのタブは同じ利用者（a）——キャレットの場所はタブごと。ページは自分で作り、最後に消す。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-presence.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const MARK = 'E2E' + Date.now().toString(36).toUpperCase();
const BODY = '<h1>' + MARK + ' いる</h1><p data-id="pa01">一</p><p data-id="pb02">二</p><p data-id="pc03">三</p>' +
  '<table data-id="tb04"><tbody><tr><th>品</th><th>数</th></tr><tr><td>甲</td><td>1</td></tr></tbody></table>';

async function open(browser, id) {
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 1000 } });
  const p = await ctx.newPage();
  p.errs = [];
  p.on('pageerror', (e) => p.errs.push(String(e)));
  p.syncs = [];
  p.on('request', (r) => { if (new URL(r.url()).pathname === '/api/sync') p.syncs.push(r.postData() || ''); });
  await login(p, BASE);
  await p.goto(BASE + '/' + id);
  await p.evaluate(() => { const t = document.getElementById('w-mode-toggle'); if (!t.checked) { t.checked = true; t.dispatchEvent(new Event('change', { bubbles: true })); } });
  await p.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
  await sleep(800);
  return p;
}
const caretAt = (p, sel) => p.evaluate((q) => {
  const el = document.querySelector(q);
  const ed = el.closest('[contenteditable="true"]') || el;
  ed.focus();
  const r = document.createRange(); r.selectNodeContents(el); r.collapse(false);
  const s = getSelection(); s.removeAllRanges(); s.addRange(r);
}, sel);
// tinted は B の画面で薄い黄色のブロック（data-id）を返す。
const tinted = (p) => p.evaluate(() => Array.from(document.querySelectorAll('#w-editor-content .editor-block.w-block-others'))
  .map((w) => { const b = w.querySelector(':scope > .block-content > *'); return b ? b.getAttribute('data-id') : '?'; }).sort().join(','));
async function waitTint(p, want, ms = 5000) {
  const t0 = Date.now();
  let got = '';
  while (Date.now() - t0 < ms) {
    got = await tinted(p);
    if (got === want) return got;
    await sleep(150);
  }
  return got;
}
const stored = (p, id) => p.evaluate(async (i) => (await (await fetch('/api/load?id=' + i)).text()).replace(/\s+/g, ' '), id);
const textOf = (p, did) => p.evaluate((d) => (document.querySelector('#w-editor-content [data-id="' + d + '"]') || {}).textContent || '', did);
const setHidden = (p, hidden) => p.evaluate((h) => {
  Object.defineProperty(document, 'visibilityState', { value: h ? 'hidden' : 'visible', configurable: true });
  document.dispatchEvent(new Event('visibilitychange'));
}, hidden);
const sentChange = (p, from, text) => p.syncs.slice(from).some((b) => b.includes(text));

(async () => {
  const browser = await chromium.launch();
  let id = '';
  let A = null, B = null;
  try {
    const setup = await (await browser.newContext({ ignoreHTTPSErrors: true })).newPage();
    await login(setup, BASE);
    id = await makePage(setup, BODY);
    check('ページを作った', !!id, id);
    A = await open(browser, id);
    B = await open(browser, id);

    // ①
    await caretAt(A, '#w-editor-content [data-id="pb02"]');
    check('① A が置いたブロックが B の画面で薄い黄色', (await waitTint(B, 'pb02')) === 'pb02', await tinted(B));
    const look = await B.evaluate(() => {
      const w = document.querySelector('#w-editor-content .editor-block.w-block-others');
      return w ? { bg: getComputedStyle(w).backgroundColor, text: w.innerText.includes('書いています') || w.innerText.includes('見ています'), title: w.title } : null;
    });
    check('① 背景の色だけ（文字の説明は無い・名前はマウスを載せると出る）', !!look && look.bg !== 'rgba(0, 0, 0, 0)' && !look.text && look.title === 'a', JSON.stringify(look));
    check('① A 自身の画面には出ない', (await tinted(A)) === '', await tinted(A));

    // ②
    await caretAt(A, '#w-editor-content table td');
    check('② 表へ動くと表が黄色・前のブロックは消える', (await waitTint(B, 'tb04')) === 'tb04', await tinted(B));
    await caretAt(A, '#w-editor-content table tr:nth-child(2) td:nth-child(2)');
    await sleep(800);
    check('② 表の中のセルを移っても表のまま', (await tinted(B)) === 'tb04', await tinted(B));

    // ③
    await A.evaluate(() => { const t = document.getElementById('w-mode-toggle'); t.checked = false; t.dispatchEvent(new Event('change', { bubbles: true })); });
    check('③ A が閲覧モードにすると消える', (await waitTint(B, '')) === '', await tinted(B));
    await A.evaluate(() => { const t = document.getElementById('w-mode-toggle'); t.checked = true; t.dispatchEvent(new Event('change', { bubbles: true })); });
    await A.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    await sleep(500);
    await caretAt(A, '#w-editor-content [data-id="pa01"]');
    check('③ 書ける形で置き直すとまた出る', (await waitTint(B, 'pa01')) === 'pa01', await tinted(B));

    // ④ 打っているあいだは保存しない——去ると保存
    let n0 = A.syncs.length;
    await A.keyboard.type('-A4');
    await sleep(3000);
    const status4 = await A.evaluate(() => document.getElementById('w-save-status').innerText);
    check('④ 打って3秒待っても送らない（未保存）', !sentChange(A, n0, '-A4') && status4.includes('未保存'), status4);
    await caretAt(A, '#w-editor-content [data-id="pb02"]');
    await A.evaluate(() => document.querySelector('#w-editor-content [data-id="pb02"]').dispatchEvent(new FocusEvent('focusin', { bubbles: true })));
    await sleep(1500);
    check('④ ブロックを去ると保存', sentChange(A, n0, '-A4') && (await stored(A, id)).includes('一-A4'), (await stored(A, id)).slice(0, 160));
    check('④ 保存された書き込みが B に届く', await B.waitForFunction(() => (document.querySelector('#w-editor-content [data-id="pa01"]') || {}).textContent === '一-A4', null, { timeout: 6000 }).then(() => true).catch(() => false));

    // ⑤ タブを隠すと保存
    n0 = A.syncs.length;
    await A.keyboard.type('-A5');
    await sleep(500);
    await setHidden(A, true);
    await sleep(1500);
    check('⑤ タブを隠すと保存', sentChange(A, n0, '-A5') && (await stored(A, id)).includes('二-A5'), (await stored(A, id)).slice(0, 200));
    check('⑤ 隠したタブの黄色は消える', (await waitTint(B, '')) === '', await tinted(B));
    await setHidden(A, false);
    await sleep(500);

    // ⑥ 受け取りに行くときも、書いている最中のブロックは送らない
    await caretAt(A, '#w-editor-content [data-id="pc03"]');
    await A.keyboard.type('-A6');
    await sleep(300);
    n0 = A.syncs.length;
    await caretAt(B, '#w-editor-content [data-id="pa01"]');
    await B.keyboard.type('-B6');
    await B.evaluate(() => window.wcmsFlushSave());
    const got6 = await A.waitForFunction(() => (document.querySelector('#w-editor-content [data-id="pa01"]') || {}).textContent.includes('-B6'), null, { timeout: 6000 }).then(() => true).catch(() => false);
    await sleep(500);
    check('⑥ 相手の保存を受け取った', got6);
    check('⑥ 受け取りに行っても、書いている最中のブロックは送らない', A.syncs.length > n0 && !sentChange(A, n0, '-A6') && !(await stored(A, id)).includes('-A6'),
      A.syncs.length - n0 + ' 回送った');
    check('⑥ 書いている最中の字は消えていない', (await textOf(A, 'pc03')) === '三-A6', await textOf(A, 'pc03'));

    // ⑦ 書いている最中のブロックを相手が保存したら並べる
    await caretAt(B, '#w-editor-content [data-id="pc03"]');
    check('⑦ B が入ったブロックが A の画面で黄色', (await waitTint(A, 'pc03')) === 'pc03', await tinted(A));
    await B.keyboard.type('-B7');
    await B.evaluate(() => window.wcmsFlushSave());
    await sleep(2500);
    check('⑦ 書いている間は枠を出さない（書きかけはそのまま）', !(await A.$('.w-conflict')) && (await textOf(A, 'pc03')) === '三-A6', await textOf(A, 'pc03'));
    await caretAt(A, '#w-editor-content [data-id="pa01"]'); // A が去って送る
    const panel = await A.waitForSelector('.w-conflict', { timeout: 6000 }).catch(() => null);
    check('⑦ 去って送ると並べる（上は相手の版・名前つき）', !!panel && (await panel.textContent()).includes('-B7') && (await panel.textContent()).includes('「a」'));
    if (panel) await A.click('.w-conflict .w-conflict-theirs-btn');
    await sleep(800);

    // ③ の続き——A がタブを閉じると消える
    await caretAt(A, '#w-editor-content [data-id="pb02"]');
    await caretAt(B, '#w-editor-content [data-id="pa01"]');
    check('③ 閉じる前は黄色', (await waitTint(B, 'pb02')) === 'pb02', await tinted(B));
    await A.context().close();
    A = null;
    check('③ A がタブを閉じると消える', (await waitTint(B, '')) === '', await tinted(B));
    check('ページのエラーが無い', B.errs.length === 0, B.errs.join(' / '));
    await deletePage(setup, id);
    id = '';
  } catch (e) {
    check('例外なし: ' + e.message, false);
  } finally {
    if (id) {
      const p = await (await browser.newContext({ ignoreHTTPSErrors: true })).newPage();
      await login(p, BASE);
      await deletePage(p, id);
    }
    await browser.close();
  }
  console.log(fails ? '\n' + fails + ' 件の失敗' : '\nすべて合格');
  process.exit(fails ? 1 : 0);
})();
