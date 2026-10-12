// アンドゥ・リドゥのコマンド（2026-10-12 に作り直した——assets/app.js の「編集中のアンドゥ・リドゥ（コマンド）」）を画面から確かめる。
//
// 利用者:「アンドゥが効いていないようです。まずアンドゥ・リドゥを本格化しましょう。コマンドパターンを使った実装はどうでしょう？」
// 「一文字ずつコマンドを収集し、同じコマンドが来たら結合して圧縮していくのは、ブラウザでは処理が追いつきませんか？」
// 「日本語入力で壊れやすいのであれば、日本語文字列が確定したときにコマンドとして収集すればよいのでは？」
//
//   ① キャレットが飛んだら結合しない（続けて打っても別のコマンド）
//   ② 打つと消すは別のコマンド（消した分だけ戻り、もう一度で打った分）
//   ③ 日本語は変換を確定したときに1つ（確定ごとに1つずつ戻る）
//   ④ 🗑 で消したブロックを戻すと、画面にもサーバーにも戻る（それまでは保存されず、4秒後に画面からも消えた）
//   ⑤ 本文の外（ボタン）にフォーカスがあっても Ctrl+Z はこのアンドゥ（それまではブラウザのアンドゥが1文字だけ消した）
//   ⑥ 本文の外の入力欄の Ctrl+Z は奪わない（その欄のアンドゥ）
//   ⑦ ↶ の説明にコマンドの名前（入力・押したボタン）
//   ⑧ 同じタブなら読み直しても戻せる
//   ⑨ 戻した中身が保存される
//
// ページは自分で作り、最後に消します。本物の置き場には書きません。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-undo-commands.js
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

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 1000 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let id = '';
  const text = (d) => page.evaluate((x) => { const el = document.querySelector('#w-editor-content [data-id="' + x + '"]'); return el ? el.textContent : '(無い)'; }, d);
  const caretAt = (d) => page.evaluate((x) => {
    const el = document.querySelector('#w-editor-content [data-id="' + x + '"]');
    el.focus();
    const r = document.createRange(); r.selectNodeContents(el); r.collapse(false);
    const s = getSelection(); s.removeAllRanges(); s.addRange(r);
  }, d);
  const undo = async () => { await page.keyboard.press('Control+z'); await sleep(300); };
  const redo = async () => { await page.keyboard.press('Control+y'); await sleep(300); };
  const stored = async () => (await page.evaluate(async (i) => (await fetch('/api/load?id=' + i)).text(), id)).replace(/\s+/g, ' ');
  const waitSaved = () => page.waitForFunction(() => document.getElementById('w-save-status').innerText.includes('保存済'), null, { timeout: 10000 });
  try {
    await login(page, BASE);
    id = await makePage(page, '<h1>' + MARK + ' アンドゥのコマンド</h1>' +
      '<p data-id="pa01">段落A</p><p data-id="pb02">段落B</p><p data-id="pc03">段落C</p><p data-id="pd04">段落D</p>');
    check('ページを作った', !!id, id);
    // lib.js の login は閲覧モードを憶えさせるので、書ける形にする。
    await page.goto(BASE + '/' + id);
    await page.evaluate(() => { const t = document.getElementById('w-mode-toggle'); if (!t.checked) { t.checked = true; t.dispatchEvent(new Event('change', { bubbles: true })); } });
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    await page.waitForSelector('#w-editor-content p[contenteditable="true"]', { timeout: 8000 });
    await sleep(500);

    // ① キャレットが飛んだら結合しない
    await caretAt('pa01');
    await page.keyboard.type('ab');
    await caretAt('pb02');
    await caretAt('pa01');
    await page.keyboard.type('cd');
    await undo();
    const u1 = await text('pa01');
    await undo();
    const u1b = await text('pa01');
    check('① キャレットが飛んだら結合しない', u1 === '段落Aab' && u1b === '段落A', u1 + ' → ' + u1b);
    await redo(); await redo();
    check('① やり直すと両方戻る', (await text('pa01')) === '段落Aabcd', await text('pa01'));

    // ② 打つと消すは別のコマンド
    await caretAt('pb02');
    await page.keyboard.type('xyz');
    await page.keyboard.press('Backspace');
    await page.keyboard.press('Backspace');
    const c2 = await text('pb02');
    await undo();
    const u2 = await text('pb02');
    await undo();
    const u2b = await text('pb02');
    check('② 消した分だけ戻り、もう一度で打った分が戻る', c2 === '段落Bx' && u2 === '段落Bxyz' && u2b === '段落B', [c2, u2, u2b].join(' → '));

    // ③ 日本語は変換を確定したときに1つ（Chrome の入力の道具で変換を真似る）
    const cdp = await page.context().newCDPSession(page);
    await caretAt('pc03');
    let imeOK = true;
    try {
      await cdp.send('Input.imeSetComposition', { text: 'きょう', selectionStart: 3, selectionEnd: 3 });
      await cdp.send('Input.insertText', { text: '今日' });
      await cdp.send('Input.imeSetComposition', { text: 'は', selectionStart: 1, selectionEnd: 1 });
      await cdp.send('Input.insertText', { text: 'は' });
    } catch (e) { imeOK = false; }
    await sleep(200);
    const c3 = await text('pc03');
    await undo();
    const u3 = await text('pc03');
    await undo();
    const u3b = await text('pc03');
    check('③ 変換の確定ごとに1つずつ戻る', imeOK && c3 === '段落C今日は' && u3 === '段落C今日' && u3b === '段落C', [imeOK, c3, u3, u3b].join(' → '));

    // ④ 🗑 で消したブロックを戻す——画面にもサーバーにも
    await page.hover('#w-editor-content [data-id="pd04"]');
    await page.evaluate(() => document.querySelector('#w-editor-content [data-id="pd04"]').closest('.editor-block')
      .querySelector('.block-controls button[title*="削除"]').click());
    await sleep(300);
    await waitSaved();
    await sleep(500);
    const gone = !(await stored()).includes('pd04');
    await undo();
    await sleep(2500);
    await waitSaved();
    check('④ 消したブロックを戻すと画面にもサーバーにも戻る', gone && (await text('pd04')) === '段落D' && (await stored()).includes('段落D'),
      [gone, await text('pd04')].join(' / '));

    // ⑤ 本文の外（ボタン）にフォーカスがあっても Ctrl+Z はこのアンドゥ
    await caretAt('pa01');
    await page.keyboard.type('Q');
    await sleep(1200);
    await page.evaluate(() => { const b = document.getElementById('w-mode-toggle').closest('label') || document.body; });
    await page.focus('#w-ctx-undo').catch(() => {});
    await page.evaluate(() => { const b = document.querySelector('header button, .site-header button, button'); if (b) b.focus(); });
    const active = await page.evaluate(() => document.activeElement && document.activeElement.tagName);
    await undo();
    check('⑤ 本文の外にフォーカスがあっても戻す', (await text('pa01')) === '段落Aabcd', active + ' / ' + await text('pa01'));

    // ⑥ 本文の外の入力欄の Ctrl+Z は奪わない
    await caretAt('pb02');
    await page.keyboard.type('W');
    await sleep(1200);
    const lp = await page.$('#w-pi-parent-input');
    let sixth = 'なし';
    if (lp && await lp.isVisible()) {
      await lp.click();
      await page.keyboard.press('End');
      await page.keyboard.type('9');
      await page.keyboard.press('Control+z');
      await sleep(300);
      sixth = await text('pb02');
      await lp.evaluate((el) => { el.value = el.defaultValue; el.blur(); });
    }
    check('⑥ 本文の外の入力欄の Ctrl+Z は本文を戻さない', sixth === 'なし' || sixth === '段落BW', sixth);

    // ⑦ ↶ の説明にコマンドの名前
    await caretAt('pc03');
    await page.keyboard.type('T');
    await sleep(200);
    const t1 = await page.evaluate(() => (document.getElementById('w-ctx-undo') || {}).title || '');
    await page.locator('#w-context-toolbar .ctx-type[data-tag="H2"]').click({ timeout: 5000 });
    await sleep(300);
    const t2 = await page.evaluate(() => (document.getElementById('w-ctx-undo') || {}).title || '');
    check('⑦ ↶ の説明にコマンドの名前（入力・押したボタン）', t1.includes('入力') && t2.includes('：') && !t2.includes('入力'), t1 + ' / ' + t2);
    await undo();
    check('⑦ 見出しにしたのを戻す', (await page.evaluate(() => document.querySelector('#w-editor-content [data-id="pc03"]').tagName)) === 'P');

    // ⑧ 同じタブなら読み直しても戻せる
    await caretAt('pa01');
    await page.keyboard.type('R');
    await sleep(1200);
    await waitSaved();
    await page.reload();
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 }).catch(() => {});
    if (!(await page.evaluate(() => document.body.hasAttribute('edit-mode')))) {
      await page.evaluate(() => { const t = document.getElementById('w-mode-toggle'); t.checked = true; t.dispatchEvent(new Event('change', { bubbles: true })); });
      await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    }
    await sleep(800);
    await caretAt('pb02');
    const before8 = await text('pa01');
    await undo();
    check('⑧ 読み直しても戻せる', before8 === '段落AabcdR' && (await text('pa01')) === '段落Aabcd', before8 + ' → ' + await text('pa01'));

    // ⑨ 戻した中身が保存される
    await sleep(2500);
    await waitSaved();
    const s9 = await stored();
    check('⑨ 戻した中身が保存される', s9.includes('段落Aabcd<') && !s9.includes('段落AabcdR') && s9.includes('段落D'), s9.slice(0, 300));
    check('ページのエラーが無い', errs.length === 0, errs.join(' / '));
  } catch (e) {
    check('例外なし: ' + e.message, false);
  } finally {
    await deletePage(page, id);
    await browser.close();
  }
  console.log(fails ? '\n' + fails + ' 件の失敗' : '\nすべて合格');
  process.exit(fails ? 1 : 0);
})();
