// 図面の PDF の枠があるページで「🖨 この表を印刷」を押すと、**本物の Chrome で**印刷の画面が開くかを見る（2026-10-02）。
//
// 利用者:「この表を印刷を押しても何も起きません」「材料表です。クロームです。Ctrl+Pを押したら正常にプリント画面が
// 出てきました」——ページに PDF の埋め込み（embed）が残っていると、Chrome は print() を「まだ読み込み中」として後回しにし、
// 印刷の画面を開かなかった（エラーも出ない）。いまは刷るあいだだけ埋め込みを外す（app.js の printOnly）。
//
// ⚠ **ヘッドレスの Chromium では起きません**（PDF の表示部品が無い）。だから verify-table-print.js は「外したこと・戻したこと」
// だけを見ていて、**開くこと**はこの道具でしか見られません。インストールされた Chrome（`channel: 'chrome'`）を画面つきで
// 立ち上げます——印刷の画面が開くと JS が止まるので、「ページが応答しない」を「開いた」と読みます。
//
// 当て先は走るときに探します（PDF を持つ通信記録・lib.js）。表と PDF の枠を置いたページを1枚作り、最後に消します。
// Chrome が無い・PDF を持つ記録が無いときは「飛ばす」（失敗にしない）。
//
// 使い方: WCMS_BASE=https://localhost:8443 node probe-print-chrome.js
const { chromium } = require('playwright');
const lib = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';
const wait = (ms, v) => new Promise((res) => setTimeout(() => res(v), ms));

(async () => {
  let browser;
  try {
    browser = await chromium.launch({ channel: 'chrome', headless: false });
  } catch (e) {
    console.log('飛ばす: Chrome を立ち上げられません（' + String(e.message).split('\n')[0] + '）');
    return;
  }
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  await lib.login(page, BASE);

  const rec = await lib.findRecordWithPDF(page);
  if (!rec.pageID) {
    console.log('飛ばす: PDF を持つ通信記録がありません');
    await browser.close();
    return;
  }
  const id = await lib.makePage(page,
    '<h1>【E2E】PDF のあるページの表の印刷</h1>' +
    '<section data-type="file-view" data-ref="' + rec.pageID + '-' + rec.attachID + '"></section>' +
    '<table><caption>E2E 部品表</caption><tbody>' +
    '<tr><th>品名</th><th>数量</th></tr><tr><td>E2E-PRINT-C</td><td>1</td></tr>' +
    '</tbody></table>');

  let bad = 0;
  const check = (ok, msg, detail) => {
    if (ok) console.log('✓ ' + msg);
    else { console.log('✗ ' + msg + (detail ? '（' + detail + '）' : '')); bad++; }
  };
  try {
    check(!!id, '当て先のページを作る');
    if (id) {
      await page.goto(BASE + '/' + id);
      await page.waitForTimeout(3000);
      const embeds = await page.evaluate(() => document.querySelectorAll('#w-editor-content embed, #w-editor-content iframe, #w-editor-content object').length);
      check(embeds > 0, 'ページに PDF の埋め込みがある（これが無いと試しにならない）', embeds + ' 個');
      // 押す（print() は一拍おいて呼ばれるので、click はすぐ戻る）→ 少し待ってページが応答するかを見る。
      await Promise.race([page.locator('#w-editor-content .w-table-print').first().click().catch(() => {}), wait(4000)]);
      await page.waitForTimeout(500);
      const r = await Promise.race([page.evaluate(() => 'responded'), wait(4000, 'blocked')]);
      check(r === 'blocked', '押すと印刷の画面が開く（ページが止まる）', r === 'responded' ? 'ページが応答した＝開いていない' : '');
    }
  } finally {
    // 印刷の画面が開いたページは止まっているので、別のタブから消す。
    const other = await ctx.newPage();
    await other.goto(BASE + '/');
    await lib.deletePage(other, id);
    await browser.close();
  }
  console.log(bad === 0 ? '\n結果: 合格' : '\n結果: ' + bad + '件の不合格');
  process.exitCode = bad === 0 ? 0 : 1;
})();
