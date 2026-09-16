// 載っている拡張だけが画面に出る（2026-09-15・組み替え §4.1 案A）
//
// 拡張の画面はコアの `assets/app.js` に居るので、ビルドタグでサーバーから外しても
// **ボタンは残り、押すと404がエラーの通知になっていました**（「🤖 解析」「✉️ 返信」
// 「未分類へ戻す」）。いまはサーバーが `/api/tag-schema` の `extensions` で知らせ、
// 画面が出し分けます。
//
// **期待値はサーバーの名簿から導きます**——同じスクリプトを、通常・`-tags nomail`・
// `-tags minimal` のどのビルドに当てても通るように。3つとも流して初めて確かめたことになります。
//
//   WCMS_HOST_PAGE … PDF の添付と `チャネル` タグを持つ通信記録（既定は探索）
//   WCMS_EMAIL_PAGE … `メールアドレス` タグを持つページ＝取引先ページ（既定は探索）
//
// ⚠ **当て先はデータを入れ直すたびに変わります**（2026-09-16 の一掃で 010272→000021・
// 010153→000001 と総入れ替えになりました。前回 09-13 の入れ直しでも同じことが起き、
// `verify-drawing-size` が落ちた理由の半分がこれでした）。**本当の直し方は、当て先を
// 走るときに探すこと**です（通信箱は「トップ直下の題が『通信箱』のページ」、
// 通信記録は「`チャネル` タグと .pdf の添付を持つページ」で引ける）——
// 共通のヘルパは [【考察】ファイル表示のコードレビュー.md] §3 の #15〜#17 が行き先。
//
// ⚠ **`WCMS_EMAIL_PAGE` は「連絡先を登録したあと」に決まります**。一掃直後は
// 取引先ページが1枚も無いので、この2項目は「前提」から落ちます——**壊れたのでは
// なく、まだ登録していないだけ**です。
const { chromium } = require('playwright');
const lib = require('./lib');
const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
let HOST = process.env.WCMS_HOST_PAGE || '';
let EMAIL = process.env.WCMS_EMAIL_PAGE || '';
let fail = 0;
const ok = (c, m, x) => { console.log((c ? '  OK ' : '  NG ') + m + (x ? '  ' + x : '')); if (!c) fail++; };

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 1000 }, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  const errs = [];
  const notFound = [];
  page.on('pageerror', e => errs.push(String(e)));
  page.on('response', r => { if (r.status() === 404 && r.url().includes('/api/')) notFound.push(r.url()); });

  await lib.login(page, BASE);
  if (!HOST) HOST = (await lib.findRecordWithPDF(page)).pageID;
  if (!EMAIL) EMAIL = await lib.findPageWithTag(page, 'メールアドレス');

  const exts = await page.evaluate(async () => (await (await fetch('/api/tag-schema')).json()).extensions);
  ok(Array.isArray(exts), '/api/tag-schema が載っている拡張を知らせる（null ではない）', JSON.stringify(exts));
  const has = id => Array.isArray(exts) && exts.includes(id);
  console.log('    このビルド: ' + (exts && exts.length ? exts.join(', ') : '素の w-cms'));

  // ── 通信記録（PDF の添付・チャネル）──
  await page.goto(BASE + '/' + HOST);
  await page.waitForSelector('#w-editor-content .attach-copyref', { timeout: 8000 }).catch(() => {});
  await page.waitForTimeout(800);
  const host = await page.evaluate(() => ({
    analyze: document.querySelectorAll('#w-editor-content .attach-analyze').length,
    reply: document.querySelectorAll('#w-editor-content .mail-reply-open').length,
    copyref: document.querySelectorAll('#w-editor-content .attach-copyref').length,
    chrome: document.querySelectorAll('#w-editor-content .mail-chrome').length,
    phoneCall: document.querySelectorAll('#w-editor-content .phone-call').length,
    phoneRec: document.querySelectorAll('#w-editor-content .phone-record').length,
  }));
  ok(host.copyref > 0, '前提: 添付が描かれている（コアの「🔗 参照」）', host.copyref + '個');
  ok((host.analyze > 0) === has('subcon'),
     has('subcon') ? '下請けが載っている → 「🤖 解析」が出る' : '下請けが無い → 「🤖 解析」は出ない', host.analyze + '個');
  ok((host.reply > 0) === has('comm/mail'),
     has('comm/mail') ? 'メールが載っている → 「✉️ 返信」が出る' : 'メールが無い → 「✉️ 返信」は出ない', host.reply + '個');
  // **箱ごと通信の持ち物**（2026-09-16）。中の2つ（やりとりの前後・この記録への返信）は
  // 通信の口を叩くので、通信が無いのに箱を作ると 404 が2本飛びます
  // ——下の「404で叩いていない」がその現れを見ます。
  ok((host.chrome > 0) === has('comm'),
     has('comm') ? '通信が載っている → 通信記録のクロームが出る' : '通信が無い → 通信記録のクロームは出ない',
     host.chrome + '個');
  // 電話は**掛けることと記録することが別**。`tel:` はただのリンクなので通信が無くても出し、
  // 「記録を作る」だけを外します。⚠ この当て先に電話番号のタグが無ければ、この行は飛ばします。
  if (host.phoneCall > 0) {
    ok((host.phoneRec > 0) === has('comm'),
       has('comm') ? '通信が載っている → ☎の「記録を作る」が出る' : '通信が無い → ☎は出るが「記録を作る」は出ない',
       host.phoneRec + '個');
  }

  // ── メールアドレスのタグがあるページ ──
  //
  // ⚠ **連絡先を1件も登録していない環境では、この当て先が在りません**（取引先ページは
  // 登録のときに作られるため）。**壊れているのではなく、まだ登録していないだけ**なので、
  // 失敗にせず飛ばします——一掃した直後が必ずこの状態になります（2026-09-16）。
  if (!EMAIL) {
    console.log('  -- 取引先ページがまだありません（連絡先を登録すると当て先ができます）。この2項目は飛ばします');
  } else {
    await page.goto(BASE + '/' + EMAIL);
    await page.waitForTimeout(1800);
    const email = await page.evaluate(() => ({
      tag: Array.from(document.querySelectorAll('#w-editor-content dl[data-type="tags"] > dt'))
        .some(dt => dt.textContent.trim() === 'メールアドレス'),
      unfile: document.querySelectorAll('#w-editor-content .contact-unfile').length,
    }));
    ok(email.tag, '前提: メールアドレスのタグがある', EMAIL);
    ok((email.unfile > 0) === has('comm/contacts'),
       has('comm/contacts') ? 'アドレス帳が載っている → 「未分類へ戻す」が出る' : 'アドレス帳が無い → 「未分類へ戻す」は出ない',
       email.unfile + '個');
  }

  // ── 載っていない拡張の API を叩いていないこと ──
  ok(notFound.length === 0, '拡張の API を404で叩いていない（載っていないものを問わない）', notFound.join(' '));
  ok(errs.length === 0, 'JSエラーなし', errs[0] || '');

  await browser.close();
  console.log(fail ? '\n' + fail + ' 件失敗' : '\n全項目OK');
  process.exit(fail ? 1 : 0);
})();
