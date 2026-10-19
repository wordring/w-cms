// 書ける形でも、業務の欄とボタンが出て使えるか（2026-10-19・【考察】文書の食い違い §3.1）を画面から確かめる。
//
// それまでは、見積書・部材見積依頼書の「備考の欄」、部材見積依頼明細の「返事:」の行、「💴 見積回答」の「計算表へ」、
// 部材発注書の「必要部材表へ戻す」が、書ける形では CSS で隠れていた（編集モードのころの決め）。
//
//   ① 書ける形で、見積明細表の足元に備考の欄が出る
//   ② 欄に打った字（改行・後退・「/」）は欄に入り、エディタは動かない（スラッシュメニューが開かない・本文のブロックが増えない）
//   ③ 「備考を保存」で、本文の「備考」の節に入る
//
// 当て先はトップ直下に自分で作るページ1枚で、最後に消します。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-writable-chrome.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

const BODY = '<h1>【E2E】書ける形の欄</h1><dl data-type="tags"><dt>見積番号</dt><dd>E2E-W</dd></dl>' + // 見積番号があると見積書ページ（備考の口が受ける）
  '<table><caption>見積明細表</caption><tbody><tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th><th>単価</th><th>備考</th></tr>' +
  '<tr><td></td><td>E2E-W1</td><td>部品</td><td>2</td><td>個</td><td>100</td><td></td></tr></tbody></table>' +
  '<section><h2>備考</h2><p><br></p></section><p>あと</p>';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let id = '';
  try {
    await page.context().addInitScript(() => { try { localStorage.setItem('wcms.mode', 'edit'); } catch (e) { /* 既定のまま */ } });
    await login(page, BASE, 'a', 'a', { keepMode: true });
    id = await makePage(page, BODY);
    check('ページを作った', !!id, id);
    await page.goto(BASE + '/' + id);
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    await page.waitForTimeout(600);

    // ①
    const box = page.locator('#w-editor-content .estimate-note-input');
    check('① 書ける形で備考の欄が出る', (await box.count()) === 1 && await box.isVisible());

    // ②
    const blocksBefore = await page.evaluate(() => document.querySelectorAll('#w-editor-content > *').length);
    await box.click();
    await page.keyboard.type('一行目');
    await page.keyboard.press('Enter');
    await page.keyboard.type('二行目/');
    await page.waitForTimeout(300);
    const slash = await page.evaluate(() => { const m = document.getElementById('w-slash-menu'); return !!m && m.offsetParent !== null && getComputedStyle(m).display !== 'none'; });
    await page.keyboard.press('Backspace');
    await page.keyboard.press('Backspace');
    const value = await box.inputValue();
    const blocksAfter = await page.evaluate(() => document.querySelectorAll('#w-editor-content > *').length);
    check('② 打った字は欄に入る（改行・後退）', value === '一行目\n二行', JSON.stringify(value));
    check('② 「/」でスラッシュメニューが開かない', !slash);
    check('② 本文のブロックは増えない', blocksBefore === blocksAfter, blocksBefore + ' → ' + blocksAfter);

    // ③
    await Promise.all([
      page.waitForResponse((r) => r.url().includes('/api/estimate/note'), { timeout: 10000 }).catch(() => null),
      page.locator('#w-editor-content .estimate-note-save').click(),
    ]);
    await page.waitForTimeout(800);
    const saved = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).text(), id);
    const sec = (saved.match(/<h2[^>]*>\s*備考\s*<\/h2>[\s\S]*?(<\/section>|$)/) || [''])[0];
    check('③ 備考の節に入る', sec.includes('一行目') && sec.includes('二行') && saved.includes('あと'), sec.replace(/\s+/g, ' ').slice(0, 200));
    check('ページのエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なし: ' + e.message, false);
  } finally {
    if (id) await deletePage(page, id);
    await browser.close();
  }
  console.log(fails ? '\n' + fails + ' 件の失敗' : '\n全項目OK');
  process.exit(fails ? 1 : 0);
})();
