// メールの送る欄（部品）と下書きを画面から確かめる（2026-09-30・assets/mail-compose.js）。
//
// 利用者:「メール表示、メール送信、メール編集などを部品化したら良いと思います」「メール編集は下書きのことです」。
//
//   ⓪ メールのページで対応（未処理・済・不要）を押すと、印と未処理の一覧が付いてくる（2026-09-30）
//   ① 受信メールの記録で「✉️ 返信」を押すと、送る欄に宛先（素のアドレス）・RE: の件名・引用が入る
//   ② 本文を足して「📝 下書きに保存」——通信箱にページができ、未処理の一覧に「📝 下書き」で並ぶ
//   ③ 下書きのページを開くと、送る欄が書いたとおりに開いている（直して保存し直すと同じページ）
//   ④ メール一覧に「📝 下書き」で並び、「✉️ 新しいメール」で空の送る欄が開く
//   ⑤ 送信は口を差し止めて（本物のメールは出さない）、下書きと用件が送られることだけを見る
//   ⑥ 別のページのファイル表示の「🔗 ID」で写した ID を送る欄に貼ると、添付に印付きで並ぶ（2026-10-01）——
//      下書きにも残り、送るときに渡る。引けない ID は理由を出して欄に残す
//
// 当て先（受信メールの記録と、ファイルを置くページ）は通信箱の下に自分で作り、最後に下書きと一緒に消します。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-mail-compose.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, findMailbox } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 },
    permissions: ['clipboard-read', 'clipboard-write'] });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let record = '', draft = '', doc = '', docFile = '';
  try {
    await login(page, BASE);
    const box = await findMailbox(page);
    if (!box) {
      console.log('通信箱が無いので飛ばします');
      await browser.close();
      process.exit(0);
    }
    record = await makePage(page, '<h1>【E2E】見積のお願い</h1><dl data-type="tags">' +
      '<dt>向き</dt><dd>受信</dd><dt>チャネル</dt><dd>メール</dd>' +
      '<dt>差出人</dt><dd>試験 &lt;e2e@invalid.example&gt;</dd></dl>' +
      '<section><h2>本文</h2><pre>E2E の本文です\n2行目</pre></section>', box);
    check('受信メールの記録を作れた', !!record, record);

    // ⓪ 対応はメールのページで選ぶ（2026-09-30 利用者:「そのメールへの対応が終わったかどうかは、受信フォルダではなく、
    // メールページで選択したいです」）——未処理 → 不要 → 未処理 と押し、印と未処理の一覧が付いてくる
    const current = async () => (await page.locator('#w-editor-content .mail-handled-btn.is-current').textContent() || '').trim();
    const listedInBox = async () => {
      await page.goto(BASE + '/' + box);
      await page.waitForSelector('#w-editor-content .unhandled-table', { timeout: 8000 }).catch(() => {});
      return page.locator('#w-editor-content .unhandled-table tr[data-page-id="' + record + '"]').count();
    };
    await page.goto(BASE + '/' + record);
    await page.waitForSelector('#w-editor-content .mail-handled', { timeout: 8000 });
    check('対応の札が出て、いまは未処理', (await current()) === '● 未処理', await current());
    await page.locator('#w-editor-content .mail-handled-btn', { hasText: '不要' }).click();
    await page.waitForFunction(() => {
      const b = document.querySelector('#w-editor-content .mail-handled-btn.is-current');
      return b && b.textContent.includes('不要');
    }, null, { timeout: 8000 }).catch(() => {});
    check('「不要」を押すと札が不要になる', (await current()) === '● 不要', await current());
    const tagged = await page.evaluate(async (id) => (await (await fetch('/api/load?id=' + id)).text()), record);
    check('本文のタグに 対応：不要（1つだけ）', (tagged.match(/<dt>対応<\/dt>/g) || []).length === 1 && tagged.includes('<dd>不要</dd>'));
    check('未処理の一覧から消える', (await listedInBox()) === 0);
    await page.goto(BASE + '/' + record);
    await page.waitForSelector('#w-editor-content .mail-handled', { timeout: 8000 });
    await page.locator('#w-editor-content .mail-handled-btn', { hasText: '未処理' }).click();
    await page.waitForFunction(() => {
      const b = document.querySelector('#w-editor-content .mail-handled-btn.is-current');
      return b && b.textContent.includes('未処理');
    }, null, { timeout: 8000 }).catch(() => {});
    check('「未処理」で印が外れる', (await current()) === '● 未処理', await current());
    check('未処理の一覧へ戻る', (await listedInBox()) === 1);

    // ① 返信の送る欄
    await page.goto(BASE + '/' + record);
    await page.locator('#w-editor-content .mail-reply-open').click();
    await page.waitForSelector('#w-editor-content .mail-compose [data-mc="to"]', { timeout: 8000 });
    const val = (k) => page.locator('#w-editor-content .mail-compose [data-mc="' + k + '"]').inputValue();
    check('宛先は素のアドレス', (await val('to')) === 'e2e@invalid.example', await val('to'));
    check('件名に RE: が1つ', (await val('subject')) === 'RE: 【E2E】見積のお願い', await val('subject'));
    const quoted = await val('body');
    check('本文に引用が入る', quoted.includes('> E2E の本文です') && quoted.includes('> 2行目'), JSON.stringify(quoted.slice(0, 120)));

    // ⑥ 別のページのファイルを ID で添付に足す（2026-10-01）
    doc = await makePage(page, '<h1>【E2E】添付の置き場</h1><p>置き場</p>', record);
    const lock = await page.request.post(BASE + '/api/lock?id=' + doc, { headers: { Origin: BASE } });
    const lockToken = (await lock.json()).token;
    const up = await page.request.post(BASE + '/api/upload-pdf', {
      headers: { Origin: BASE, 'X-Lock-Token': lockToken },
      multipart: { page_id: doc, pdf_file: { name: 'E2E資料.pdf', mimeType: 'application/pdf', buffer: Buffer.from('%PDF-1.4\n%%EOF\n') } },
    });
    const upID = ((await up.json().catch(() => ({}))).id || '').replace(/\.[^.]+$/, '');
    await page.evaluate(async (arg) => {
      await fetch('/api/save', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ page_id: arg.id, html: arg.html, token: arg.token }),
      });
    }, { id: doc, token: lockToken, html: '<h1>【E2E】添付の置き場</h1><section data-type="file-view" data-ref="' + doc + '-' + upID + '"></section>' });
    await page.request.post(BASE + '/api/lock/force?id=' + doc, { headers: { Origin: BASE } });
    const wantRef = doc + '-' + upID;
    check('ファイルを置いたページを作れた', !!doc && !!upID, wantRef);
    await page.goto(BASE + '/' + doc);
    // 2026-10-01 からファイル表示の頭は「⋯」1つ——「🔗 ID を写す」はそのメニューの中。
    const dots = page.locator('#w-editor-content section[data-type="file-view"] .file-menu-btn');
    await dots.waitFor({ timeout: 8000 }).catch(() => {});
    await dots.click();
    const copyBtn = page.locator('#w-file-menu.active .file-menu-item', { hasText: 'ID を写す' });
    await copyBtn.waitFor({ timeout: 5000 }).catch(() => {});
    check('ファイル表示の「⋯」に「🔗 ID を写す（' + wantRef + '）」', ((await copyBtn.textContent().catch(() => '')) || '').includes(wantRef),
      await copyBtn.textContent().catch(() => ''));
    await copyBtn.click();
    await page.waitForTimeout(300);
    const clip = await page.evaluate(() => navigator.clipboard.readText()).catch((e) => 'ERR ' + e);
    check('押すと ID がクリップボードへ', clip === wantRef, clip);
    check('押しても畳まれない', await page.locator('#w-editor-content .file-view-fold[open]').count() === 1);

    await page.goto(BASE + '/' + record);
    await page.locator('#w-editor-content .mail-reply-open').click();
    const idBox = page.locator('#w-editor-content .mail-compose [data-mc-attach-id]');
    await idBox.waitFor({ timeout: 8000 });
    const addMsg = page.locator('#w-editor-content .mail-compose [data-mc-attach-msg]');
    await idBox.fill('abc');
    await idBox.press('Enter');
    await addMsg.locator('.mc-ng').waitFor({ timeout: 8000 }).catch(() => {});
    check('引けない ID は理由を出し、欄に残す', (await addMsg.textContent()).includes('ID ではありません') && (await idBox.inputValue()) === 'abc',
      await addMsg.textContent());
    await idBox.fill('');
    await idBox.focus();
    await page.keyboard.press('Control+V'); // 貼った時点で足す
    const picked = page.locator('#w-editor-content .mail-compose [data-mc-file="' + doc + '/' + upID + '.pdf"]');
    await picked.waitFor({ timeout: 8000 }).catch(() => {});
    check('貼ると添付に印付きで並ぶ', (await picked.count()) === 1 && (await picked.isChecked()), await addMsg.textContent());
    check('並んだ名前は届いたときの名前', ((await picked.locator('xpath=..').textContent()) || '').includes('E2E資料.pdf'));
    check('足したら欄は空に', (await idBox.inputValue()) === '');
    await idBox.fill(wantRef);
    await idBox.press('Enter');
    await page.waitForTimeout(800);
    check('同じ ID をもう一度貼っても1つ', (await page.locator('#w-editor-content .mail-compose [data-mc-file="' + doc + '/' + upID + '.pdf"]').count()) === 1,
      await addMsg.textContent());
    docFile = upID + '.pdf';

    // ② 下書きに保存
    await page.locator('#w-editor-content .mail-compose [data-mc="body"]').fill('E2E の下書きです。\n\n' + quoted);
    await page.locator('#w-editor-content .mail-compose [data-mc-save]').click();
    const link = page.locator('#w-editor-content .mail-compose [data-mc-result] a');
    await link.waitFor({ timeout: 8000 });
    draft = ((await link.getAttribute('href')) || '').replace(/^\//, '');
    check('下書きのページができた', /^\d{6}$/.test(draft), draft);

    // ③ 下書きのページ——送る欄が開いている
    await page.goto(BASE + '/' + draft);
    await page.waitForSelector('#w-editor-content .mail-compose [data-mc="body"]', { timeout: 8000 });
    check('下書きのページで送る欄が開いている', (await val('body')).startsWith('E2E の下書きです。'), JSON.stringify((await val('body')).slice(0, 40)));
    check('件名も戻る', (await val('subject')) === 'RE: 【E2E】見積のお願い');
    check('返信ボタンではなく下書きの欄', await page.locator('#w-editor-content .mail-reply-open').count() === 0);
    check('下書きには対応の札を出さない', await page.locator('#w-editor-content .mail-handled').count() === 0);
    const keptPick = page.locator('#w-editor-content .mail-compose [data-mc-file="' + doc + '/' + docFile + '"]');
    check('ID で足した添付が下書きに残る（印付き）', (await keptPick.count()) === 1 && (await keptPick.isChecked()));
    await page.locator('#w-editor-content .mail-compose [data-mc="subject"]').fill('RE: 【E2E】見積のお願い（直した）');
    await page.locator('#w-editor-content .mail-compose [data-mc-save]').click();
    await link.waitFor({ timeout: 8000 });
    const again = ((await link.getAttribute('href')) || '').replace(/^\//, '');
    check('直して保存し直すと同じページ', again === draft, again);

    // 未処理の一覧（通信箱）に「📝 下書き」で並ぶ
    await page.goto(BASE + '/' + box);
    await page.waitForSelector('#w-editor-content .unhandled-table', { timeout: 8000 }).catch(() => {});
    const row = page.locator('#w-editor-content .unhandled-table tr[data-page-id="' + draft + '"]');
    check('未処理の一覧に並ぶ', await row.count() === 1);
    check('「📝 下書き」の印', (await row.locator('.unhandled-draft').count()) === 1);

    // ④ メール一覧
    await page.goto(BASE + '/assets/mails.html');
    await page.waitForFunction(() => document.querySelectorAll('#ml-body tr').length > 0, null, { timeout: 15000 });
    const listed = await page.evaluate((id) => {
      const a = document.querySelector('#ml-body a[href="/' + id + '"]');
      const tr = a && a.closest('tr');
      return tr ? tr.textContent : '';
    }, draft);
    check('メール一覧に「📝 下書き」で並ぶ', listed.includes('📝 下書き'), listed.slice(0, 80));
    await page.locator('#ml-new').click();
    await page.waitForSelector('#ml-compose .mail-compose [data-mc="subject"]', { timeout: 8000 });
    check('「✉️ 新しいメール」で空の送る欄', (await page.locator('#ml-compose [data-mc="subject"]').inputValue()) === '' &&
      (await page.locator('#ml-compose [data-mc="to"]').inputValue()) === '');

    // ⑤ 送る——口は差し止める（本物のメールは出さない）
    await page.goto(BASE + '/' + draft);
    await page.waitForSelector('#w-editor-content .mail-compose [data-mc-send]', { timeout: 8000 });
    let sent = null;
    await page.route('**/api/mail/send', async (route) => {
      sent = JSON.parse(route.request().postData() || '{}');
      await route.fulfill({ status: 200, contentType: 'application/json', body: '{"success":true,"draft_error":"（試験なので送っていません）"}' });
    });
    await page.evaluate(() => { document.querySelector('#w-editor-content .mail-compose [data-mc-send]').disabled = false; });
    await page.locator('#w-editor-content .mail-compose [data-mc-send]').click();
    for (let i = 0; i < 40 && !sent; i++) await page.waitForTimeout(200);
    check('下書きから送ると用件と下書きが渡る', sent && sent.purpose === '返信' && sent.page_id === record && sent.draft_id === draft,
      JSON.stringify(sent && { purpose: sent.purpose, page_id: sent.page_id, draft_id: sent.draft_id }));
    check('ID で足した添付が送るときに渡る', !!sent && (sent.attachments || []).some((a) => a.page_id === doc && a.file === docFile),
      JSON.stringify(sent && sent.attachments));
    await page.waitForTimeout(500);
    const told = await page.locator('#w-editor-content .mail-compose [data-mc-result]').textContent();
    check('送れたことと、片付けられなかったことを両方言う', told.includes('送信しました') && told.includes('試験なので'), told);
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    await page.unrouteAll({ behavior: 'ignoreErrors' }).catch(() => {});
    if (draft) await deletePage(page, draft).catch(() => {});
    if (doc) await deletePage(page, doc).catch(() => {});
    if (record) await deletePage(page, record).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
