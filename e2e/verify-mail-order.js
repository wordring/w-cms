// メールの記録から作るページ（2026-10-01・ext/comm/record_make.go・assets/app.js の recordMakeControl）を画面から確かめる。
//
// 利用者:「発注書が無くても、メールから簡単に発注ページを作れませんか？」→ 受注ページ。
// 利用者:「「受注ページ」「加工製品ページ」をコンボボックスで選択して「作成」ボタンを押せばいいかも」。
//
//   ① 受信メールの記録に「作るページ [受注ページ／加工製品ページ] [作成]」が出る（送信の控えには出ない）
//   ② 受注ページを選んで作成 → 口（POST /api/record-make）へこのページと種類を渡し、できたページを知らせる
//      ——⚠ 口は差し止める（Gemini を呼ばない・本物の受注ページを作らない）
//   ③ このメールから作った受注ページ（受信元＝このページ）があれば、横に「✓ 受注」の印が出る
//   ④ 加工製品ページを選んで作成 → 空の加工製品ページがメールの子にでき、編集モードで開く（Gemini は呼ばない・本当に作る）
//
// 当て先は通信箱の下に自分で作り、最後に消します。東邦の拡張が無い組では飛ばします。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-mail-order.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, findMailbox } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

const record = (dir, subject) => '<h1>' + subject + '</h1><dl data-type="tags">' +
  '<dt>向き</dt><dd>' + dir + '</dd><dt>チャネル</dt><dd>メール</dd>' +
  '<dt>差出人</dt><dd>試験 &lt;e2e@invalid.example&gt;</dd></dl>' +
  '<section><h2>本文</h2><pre>K120-3 取付ベース 5個 お願いします。</pre></section>';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  const made = [];
  try {
    await login(page, BASE);
    const exts = await page.evaluate(async () => (await (await fetch('/api/tag-schema')).json()).extensions || []);
    const box = await findMailbox(page);
    if (!box || !exts.includes('toho')) {
      console.log('通信箱か東邦の拡張が無いので飛ばします');
      await browser.close();
      process.exit(0);
    }
    const inMail = await makePage(page, record('受信', '【E2E】注文のお願い'), box);
    const outMail = await makePage(page, record('送信', '【E2E】送った控え'), box);
    made.push(inMail, outMail);

    // ① 受信には出る・送信には出ない
    await page.goto(BASE + '/' + outMail);
    await page.waitForSelector('#w-editor-content .mail-chrome', { timeout: 8000 });
    await page.waitForTimeout(800); // 種類を引く問い合わせを待つ
    check('送信の控えには出ない', await page.locator('#w-editor-content .record-make-kind').count() === 0);
    await page.goto(BASE + '/' + inMail);
    const sel = page.locator('#w-editor-content .record-make-kind');
    await sel.waitFor({ timeout: 8000 }).catch(() => {});
    const opts = await sel.locator('option').allTextContents().catch(() => []);
    check('受信メールに「作るページ」の選ぶ欄（受注ページ・加工製品ページ）と「作成」',
      opts.join('・') === '受注ページ・加工製品ページ' && await page.locator('#w-editor-content .record-make-go').count() === 1, opts.join('・'));

    // ② 受注ページ——口は差し止める
    let sent = null;
    await page.route('**/api/record-make', async (route) => {
      sent = JSON.parse(route.request().postData() || '{}');
      await route.fulfill({ status: 200, contentType: 'application/json',
        body: JSON.stringify({ success: true, pages: [{ page_id: '999998', title: '受注（試験）', kind: '受注' }], say: '加工製品と 1 行を結びました。' }) });
    });
    await sel.selectOption('受注ページ');
    await page.locator('#w-editor-content .record-make-go').click();
    for (let i = 0; i < 40 && !sent; i++) await page.waitForTimeout(200);
    check('このページと種類を口へ渡す', !!sent && sent.page_id === inMail && sent.kind === '受注ページ', JSON.stringify(sent));
    const toast = page.locator('.toast, #w-toasts, [id^="w-toast"]').filter({ hasText: '受注ページを作りました' });
    await toast.first().waitFor({ timeout: 5000 }).catch(() => {});
    check('できたページを知らせる', await toast.count() > 0);
    await page.unrouteAll({ behavior: 'ignoreErrors' });

    // ③ 既に作った受注ページがあれば印が出る
    const order = await makePage(page, '<h1>【E2E】受注（メールから）</h1><dl data-type="tags">' +
      '<dt>発注元</dt><dd>【E2E】客先</dd><dt>受信元</dt><dd>' + inMail + '</dd></dl>', inMail);
    made.unshift(order);
    await page.goto(BASE + '/' + inMail);
    const mark = page.locator('#w-editor-content .record-make a[href="/' + order + '"]');
    await mark.waitFor({ timeout: 8000 }).catch(() => {});
    check('作った受注ページの印（✓ 受注）が横に出る', await mark.count() === 1 && ((await mark.textContent()) || '').includes('受注'));

    // ④ 加工製品ページ——本当に作る（Gemini は呼ばない）
    await page.locator('#w-editor-content .record-make-kind').selectOption('加工製品ページ');
    await Promise.all([
      page.waitForURL(/\/\d{6}\?edit=true/, { timeout: 15000 }).catch(() => {}),
      page.locator('#w-editor-content .record-make-go').click(),
    ]);
    const product = ((page.url().match(/\/(\d{6})\?edit=true/) || [])[1]) || '';
    if (product) made.unshift(product);
    const meta = product ? await page.evaluate(async (id) => (await (await fetch('/api/page-meta?id=' + id)).json()), product) : {};
    const body = product ? await page.evaluate(async (id) => (await (await fetch('/api/load?id=' + id)).text()), product) : '';
    check('④ 加工製品ページができて編集モードで開く', /^\d{6}$/.test(product) && meta.parent_id === inMail, product + ' 親=' + meta.parent_id);
    check('空の加工製品ページ（題・受信元＝メール・図面の枠）', body.includes('<h1>新しい加工製品</h1>') &&
      body.includes('<dt>受信元</dt><dd>' + inMail + '</dd>') && body.includes('data-type="file-view"'), body.slice(0, 200));
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    await page.unrouteAll({ behavior: 'ignoreErrors' }).catch(() => {});
    for (const id of made) await deletePage(page, id).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
