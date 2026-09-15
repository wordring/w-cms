// 通信箱へ .eml を落とすと取り込み係が記録ページを作る（受け口のフック・2026-09-15）
//
// 2026-09-15 に、汎用のアップロード口が `MailBoxPageID()` を直接呼んでいたのを
// **受け口のフック**（cms.RegisterUploadInterceptor）へ裏返しました。通信の語彙を
// ext/comm へ出すための下ごしらえで、**経路の形が変わったので実際に通して確かめます**:
//
//   ① 通信箱へ .eml を落とす → `intake:true` で記録ページができる
//   ② 同じ .eml をもう一度 → `duplicate:true`（2枚目は作らない）
//   ③ 通信箱でない普通のページへ同じ .eml → 受け口は引き受けず、ただの添付になる
//      （`.eml` が attachment_extensions に在るので 200・`intake` なし）
//
//   WCMS_MAILBOX … 通信箱のページID（既定 010153）
const { chromium } = require('playwright');
const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
const MAILBOX = process.env.WCMS_MAILBOX || '010153';
let fail = 0;
const ok = (c, m, x) => { console.log((c ? '  OK ' : '  NG ') + m + (x ? '  ' + x : '')); if (!c) fail++; };

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  await page.goto(BASE + '/login');
  await page.fill('#username', 'a'); await page.fill('#password', 'a');
  await page.click('button[type=submit]'); await page.waitForLoadState('networkidle');

  // 毎回違う Message-ID にする（前回の残りと重複判定されないように）。
  const mid = '<probe-intake-' + Date.now() + '@example.jp>';
  const eml = [
    'From: 試験 <probe@example.jp>',
    'To: minami@example.jp',
    'Subject: =?UTF-8?B?' + Buffer.from('受け口の試験').toString('base64') + '?=',
    'Date: Mon, 15 Sep 2026 10:00:00 +0900',
    'Message-ID: ' + mid,
    'MIME-Version: 1.0',
    'Content-Type: text/plain; charset=UTF-8',
    '',
    'これは受け口のフックを確かめるための試験メールです。',
    '',
  ].join('\r\n');

  const upload = (pageID, text) => page.evaluate(async (a) => {
    const fd = new FormData();
    fd.append('page_id', a.pageID);
    fd.append('file', new Blob([a.text], { type: 'message/rfc822' }), 'probe.eml');
    const res = await fetch('/api/upload-file', { method: 'POST', body: fd });
    const body = await res.text();
    let d = {}; try { d = JSON.parse(body); } catch (e) {}
    return { status: res.status, d, body: body.slice(0, 160) };
  }, { pageID, text });

  const created = [];
  let host = '';
  try {
    // ① 通信箱へ
    const r1 = await upload(MAILBOX, eml);
    ok(r1.status === 200 && r1.d.intake === true && !!r1.d.page_id,
       '通信箱へ落とすと取り込み係が記録ページを作る', r1.status + ' ' + r1.body);
    if (r1.d.page_id) created.push(r1.d.page_id);

    // ② 同じものをもう一度
    const r2 = await upload(MAILBOX, eml);
    ok(r2.status === 200 && r2.d.duplicate === true, '同じメールは2枚目を作らない（重複検知）', r2.body);
    ok(!r2.d.page_id || r2.d.page_id === r1.d.page_id, '重複は既存の記録を指す', String(r2.d.page_id));

    // ③ 普通のページへ——受け口は引き受けない
    const made = await page.evaluate(async () => {
      const res = await fetch('/api/new-page', {
        method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' }, body: 'parent=000000',
      });
      return res.url;
    });
    host = (made.match(/\/(\d{6})/) || [])[1] || '';
    ok(!!host, '普通のページを作れた', host);
    if (host) {
      // 普通の添付は編集ロックが要る。
      await page.evaluate(async (id) => { await fetch('/api/lock?id=' + id, { method: 'POST' }); }, host);
      const tok = await page.evaluate(async (id) => {
        await fetch('/api/lock/force?id=' + id, { method: 'POST' });
        const r = await fetch('/api/lock?id=' + id, { method: 'POST' });
        return (await r.json().catch(() => ({}))).token || '';
      }, host);
      const r3 = await page.evaluate(async (a) => {
        const fd = new FormData();
        fd.append('page_id', a.id);
        fd.append('file', new Blob([a.text], { type: 'message/rfc822' }), 'probe.eml');
        const res = await fetch('/api/upload-file', { method: 'POST', body: fd, headers: { 'X-Lock-Token': a.tok } });
        const body = await res.text();
        let d = {}; try { d = JSON.parse(body); } catch (e) {}
        return { status: res.status, d, body: body.slice(0, 160) };
      }, { id: host, text: eml, tok });
      ok(r3.status === 200 && r3.d.success === true && !r3.d.intake,
         '通信箱でないページでは受け口が引き受けない（ただの添付になる）', r3.status + ' ' + r3.body);
    }
  } finally {
    for (const id of created.concat(host ? [host] : [])) {
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
