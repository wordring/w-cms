// ファイルの「⋯」——形式に合った操作のメニュー（2026-10-01・assets/app.js の openFileMenu）を画面から確かめる。
//
// 利用者:「ドロップやファイルダイアログで追加された画像やファイル等は形式を判定して、ファイル形式にあったボタンなり
// クリックメニューを搭載してはどうでしょう？」→（出し方を聞いて）「⋯」1つにまとめる・🤖 解析はメールのページだけ。
//
//   ① ファイル表示の頭は「⋯」1つ（それまでの「🔗 ID」「📝 ローカル編集」は並べない）
//   ② PDF: 🔗 ID を写す・⬇ 保存・📝 ローカル編集——ふつうのページには 🤖 解析を出さない
//   ③ STEP: 🔗・⬇・📝 CAD で開く／ZIP: 🔗・⬇・🗂 中身を見る
//   ④ ⬇ 保存は届いたときの名前で落ちる
//   ⑤ メールのページ（チャネルのタグ）に置いた PDF には 🤖 解析が出る（押さない——Gemini を呼ばない）
//
// 作ったページは最後に消します。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-file-menu.js
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
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, acceptDownloads: true, viewport: { width: 1280, height: 1000 } });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  const made = [];
  // upload はファイルを1つ置き、添付ID を返します（口は形式ごと——PDF は %PDF- 検査の口）。
  const upload = async (pageId, name, buffer, pdf) => {
    const lock = await page.request.post(BASE + '/api/lock?id=' + pageId, { headers: { Origin: BASE } });
    const token = (await lock.json()).token;
    const res = await page.request.post(BASE + (pdf ? '/api/upload-pdf' : '/api/upload-file'), {
      headers: { Origin: BASE, 'X-Lock-Token': token },
      multipart: pdf ? { page_id: pageId, pdf_file: { name, mimeType: 'application/pdf', buffer } }
        : { page_id: pageId, file: { name, mimeType: 'application/octet-stream', buffer } },
    });
    await page.request.post(BASE + '/api/lock/force?id=' + pageId, { headers: { Origin: BASE } });
    return ((await res.json().catch(() => ({}))).id) || '';
  };
  const saveBody = async (id, html) => {
    await page.evaluate(async (arg) => {
      const lr = await fetch('/api/lock?id=' + arg.id, { method: 'POST' });
      const lj = await lr.json().catch(() => ({}));
      await fetch('/api/save', { method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ page_id: arg.id, html: arg.html, token: lj.token || '' }) });
      await fetch('/api/lock/force?id=' + arg.id, { method: 'POST' });
    }, { id, html });
  };
  const fv = (ref) => '<section data-type="file-view" data-ref="' + ref + '"></section>';
  // menuOf は ref のファイル表示の「⋯」を押して、メニューの項目の文字を返します。
  const menuOf = async (ref) => {
    await page.locator('#w-editor-content section[data-ref="' + ref + '"] .file-menu-btn').click();
    await page.locator('#w-file-menu.active').waitFor({ timeout: 5000 }).catch(() => {});
    const items = await page.locator('#w-file-menu.active .file-menu-item').allTextContents();
    await page.keyboard.press('Escape');
    return items.join('｜');
  };
  try {
    await login(page, BASE);
    // ふつうのページ。
    const plain = await makePage(page, '<h1>【E2E】ファイルの操作</h1><p>x</p>');
    made.push(plain);
    const pdf = await upload(plain, '【E2E】図面.pdf', Buffer.from('%PDF-1.4 e2e'), true);
    const step = await upload(plain, '【E2E】カバー.step', Buffer.from('ISO-10303-21;\nEND-ISO-10303-21;\n'));
    const zip = await upload(plain, '【E2E】一式.zip', Buffer.from('PK\u0005\u0006' + '\u0000'.repeat(18)));
    await saveBody(plain, '<h1>【E2E】ファイルの操作</h1>' + fv(plain + '-' + pdf) + fv(plain + '-' + step) + fv(plain + '-' + zip));
    check('添付を3つ置けた', !!pdf && !!step && !!zip, [pdf, step, zip].join(','));

    await page.goto(BASE + '/' + plain);
    await page.locator('#w-editor-content .file-menu-btn').first().waitFor({ timeout: 10000 }).catch(() => {});
    // ①
    const heads = await page.evaluate(() => Array.from(document.querySelectorAll('#w-editor-content .file-view-head'))
      .map(h => Array.from(h.querySelectorAll('button')).map(b => b.textContent.trim()).join(',')));
    check('① ファイル表示の頭は「⋯」1つ', heads.length === 3 && heads.every(h => h === '⋯'), JSON.stringify(heads));
    // ②
    const pm = await menuOf(plain + '-' + pdf);
    check('② PDF: 🔗 ID・⬇ 保存・📝 ローカル編集', pm.includes('ID を写す（' + plain + '-' + pdf + '）') && pm.includes('保存') &&
      pm.includes('ローカル編集'), pm);
    check('② ふつうのページの PDF に 🤖 解析を出さない', !pm.includes('解析'), pm);
    // ③
    const sm = await menuOf(plain + '-' + step);
    check('③ STEP: 📝 CAD で開く', sm.includes('CAD で開く') && sm.includes('保存') && !sm.includes('解析'), sm);
    const zm = await menuOf(plain + '-' + zip);
    check('③ ZIP: 🗂 中身を見る（まとめて解析は出さない）', zm.includes('中身を見る') && !zm.includes('解析'), zm);
    // ④
    await page.locator('#w-editor-content section[data-ref="' + plain + '-' + step + '"] .file-menu-btn').click();
    const [dl] = await Promise.all([
      page.waitForEvent('download', { timeout: 10000 }).catch(() => null),
      page.locator('#w-file-menu.active .file-menu-item', { hasText: '保存' }).click(),
    ]);
    check('④ 保存は届いたときの名前で落ちる', dl && dl.suggestedFilename() === '【E2E】カバー.step', dl ? dl.suggestedFilename() : 'なし');

    // ⑤ メールのページ。
    const box = await findMailbox(page);
    const exts = await page.evaluate(async () => (await (await fetch('/api/tag-schema')).json()).extensions || []);
    if (box && exts.includes('toho')) {
      const mail = await makePage(page, '<h1>【E2E】図面の送付</h1><dl data-type="tags"><dt>向き</dt><dd>受信</dd>' +
        '<dt>チャネル</dt><dd>メール</dd></dl><p>x</p>', box);
      made.unshift(mail);
      const mpdf = await upload(mail, '【E2E】届いた図面.pdf', Buffer.from('%PDF-1.4 e2e mail'), true);
      await saveBody(mail, '<h1>【E2E】図面の送付</h1><dl data-type="tags"><dt>向き</dt><dd>受信</dd><dt>チャネル</dt><dd>メール</dd></dl>' +
        fv(mail + '-' + mpdf));
      await page.goto(BASE + '/' + mail);
      await page.locator('#w-editor-content .file-menu-btn').first().waitFor({ timeout: 10000 }).catch(() => {});
      const mm = await menuOf(mail + '-' + mpdf);
      check('⑤ メールのページの PDF に 🤖 解析', mm.includes('解析（受注・図面を読む）'), mm);
    } else {
      console.log('通信箱か東邦の拡張が無いので ⑤ は飛ばします');
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
