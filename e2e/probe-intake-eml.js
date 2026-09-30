// .eml を通信箱の記録にする口（/api/intake/eml・2026-09-30）と、通信箱へ落としても記録ができないこと
//
// 2026-09-15〜09-30 は、通信箱へ .eml を落とすと取り込み係が記録ページを作っていました（アップロードの受け口）。
// 利用者:「通信箱ページのファイルをドロップすると子ページが作られる機能はもはや必要ないでしょう　ページを作るボタンが
// 新設されたからです」——受け口を無くし、.eml を上げる道具（tools/mail/push）は専用の口を叩きます:
//
//   ① /api/intake/eml へ .eml → `intake:true` で記録ページができる
//   ② 同じ .eml をもう一度 → `duplicate:true`（2枚目は作らない）
//
// 「通信箱へ落としても記録にならない（ただの添付）」は Go の試験（ext/comm の TestDropOnMailboxIsJustAttachment）が見ます
// ——ここで本物の通信箱へ上げると、本物のページに添付が残る（2026-09-30 に一度やって、手で消した）。
//
//   WCMS_MAILBOX … 通信箱のページID（省略すると走るときに探します）
const { chromium } = require('playwright');
const lib = require('./lib');
const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
// ⚠ **当て先は焼き込みません**（2026-09-18）。`000001` と書いてあったので、データを
// 入れ直してテンプレート置き場が `000001` になった日から、**テンプレート領域へ**
// 落としていました（あそこは索引に載らないので、黙って通る壊れ方をします）。
// 通信箱は「トップ直下・題が通信箱」で探します。
let MAILBOX = process.env.WCMS_MAILBOX || '';
let fail = 0;
const ok = (c, m, x) => { console.log((c ? '  OK ' : '  NG ') + m + (x ? '  ' + x : '')); if (!c) fail++; };

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  await lib.login(page, BASE);
  if (!MAILBOX) {
    MAILBOX = await lib.findMailbox(page);
    if (!MAILBOX) { console.log('通信箱がありません（管理画面の「置き場」で作れます）'); process.exit(1); }
  }

  // 毎回違う Message-ID にする（前回の残りと重複判定されないように）。
  const mid = '<probe-intake-' + Date.now() + '@example.jp>';
  const eml = [
    'From: 試験 <probe@example.jp>',
    'To: admin@example.jp',
    'Subject: =?UTF-8?B?' + Buffer.from('受け口の試験').toString('base64') + '?=',
    'Date: Mon, 15 Sep 2026 10:00:00 +0900',
    'Message-ID: ' + mid,
    'MIME-Version: 1.0',
    'Content-Type: text/plain; charset=UTF-8',
    '',
    'これは受け口のフックを確かめるための試験メールです。',
    '',
  ].join('\r\n');

  const post = (url, pageID, text) => page.evaluate(async (a) => {
    const fd = new FormData();
    if (a.pageID) fd.append('page_id', a.pageID);
    fd.append('file', new Blob([a.text], { type: 'message/rfc822' }), 'probe.eml');
    const res = await fetch(a.url, { method: 'POST', body: fd });
    const body = await res.text();
    let d = {}; try { d = JSON.parse(body); } catch (e) {}
    return { status: res.status, d, body: body.slice(0, 160) };
  }, { url, pageID, text });
  const upload = (text) => post('/api/intake/eml', '', text);

  const created = [];

  try {
    // ① 口へ
    const r1 = await upload(eml);
    ok(r1.status === 200 && r1.d.intake === true && !!r1.d.page_id,
       '/api/intake/eml で記録ページができる', r1.status + ' ' + r1.body);
    if (r1.d.page_id) created.push(r1.d.page_id);

    // ② 同じものをもう一度
    const r2 = await upload(eml);
    ok(r2.status === 200 && r2.d.duplicate === true, '同じメールは2枚目を作らない（重複検知）', r2.body);
    ok(!r2.d.page_id || r2.d.page_id === r1.d.page_id, '重複は既存の記録を指す', String(r2.d.page_id));

  } finally {
    for (const id of created) {
      const del = await page.evaluate(async (i) => {
        await fetch('/api/lock/force?id=' + i, { method: 'POST' });
        return (await fetch('/api/delete-page?id=' + encodeURIComponent(i), { method: 'POST' })).status;
      }, id);
      console.log('    後始末: 削除 ' + del + '（' + id + '）');
    }
  }

  await browser.close();
  console.log(fail ? '\n' + fail + ' 件失敗' : '\n全項目OK');
  process.exit(fail ? 1 : 0);
})();
