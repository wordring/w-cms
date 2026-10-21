// 部材見積依頼の返事ページの「🤖 Gemini で読む（OCR）」（2026-10-21 夜・ext/toho/rfq_reply_ocr.go）を画面から確かめる。
//
// 利用者:「部材見積依頼の返事ページからGeminiにOCRを依頼できるようにしてください」
//
//   ① 返事ページの欄に「🤖 読む」と、読む候補（原本: メールの本文）が出る
//   ② 「読んだまま」の表に値があれば、置き換える前に確かめる
//   ③ 押すと /api/rfq-reply/ocr に、返事ページ・選んだ候補（ページとファイル）を送る
//      ⚠ Gemini は呼ばない——口は試験のブラウザの中で止め、送った値だけを見る。
//
// 当て先は全部自分で作って最後に消します。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-rfq-reply-ocr.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, writeBody } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let box = '', mail = '', reply = '';
  try {
    await login(page, BASE);
    box = await makePage(page, '<h1>【E2E】返事を読む</h1><p>x</p>');
    mail = await makePage(page, '<h1>【E2E】お見積りの件</h1><p>x</p>', box);
    await writeBody(page, mail, '<h1>【E2E】お見積りの件</h1><dl data-type="tags"><dt>向き</dt><dd>受信</dd></dl><p>SS400 t6 は 1枚 1,200円 です。</p>');
    reply = await makePage(page, '<h1>【E2E】部材見積依頼の返事</h1><p>x</p>', mail);
    await writeBody(page, reply, '<h1>【E2E】部材見積依頼の返事</h1><dl data-type="tags"><dt>仕入先</dt><dd><br/></dd><dt>回答日</dt><dd>2026-10-21</dd>' +
      '<dt>読んだ部材見積依頼番号</dt><dd><br/></dd><dt>部材見積依頼</dt><dd><br/></dd><dt>受信元</dt><dd>' + mail + '</dd></dl>' +
      '<section data-mirror="部材見積依頼の返事の突き合わせ"></section>' +
      '<details open><summary>業者の返事（読んだまま）</summary><table><tbody><tr><th>品番</th><th>単価</th></tr><tr><td>前の値</td><td>100</td></tr></tbody></table></details>');

    let sent = null;
    await page.route('**/api/rfq-reply/ocr', async (route) => {
      sent = JSON.parse(route.request().postData() || '{}');
      await route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ success: true, rows: 1, linked: '', say: '読みました（試験）' }) });
    });

    await page.goto(BASE + '/' + reply);
    const btn = page.locator('#w-editor-content .rfq-reply-ocr');
    await btn.waitFor({ timeout: 10000 }).catch(() => {});
    const opts = await page.locator('#w-editor-content .rfq-reply-ocr-src option').allTextContents();
    check('① 「🤖 読む」と、読む候補（原本: メールの本文）が出る', (await btn.count()) === 1 && opts.some((o) => o.includes('原本: メールの本文')), JSON.stringify(opts));

    let asked = '';
    page.once('dialog', (d) => { asked = d.message(); d.accept(); });
    await btn.click();
    await page.waitForFunction(() => /読みました/.test((document.querySelector('#w-editor-content .rfq-reply-ocr-say') || {}).textContent || ''), null, { timeout: 8000 }).catch(() => {});
    check('② 表に値があるので、置き換える前に確かめる', asked.includes('置き換えます'), asked.slice(0, 60));
    check('③ 返事ページと選んだ候補を送る', !!sent && sent.page_id === reply && sent.source_page === mail && sent.source_file === '', JSON.stringify(sent));
    await page.waitForTimeout(1500); // 読み直しを待つ
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    for (const id of [reply, mail, box]) if (id) await deletePage(page, id).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
