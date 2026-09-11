// 整理パネルの「装置名称の候補」（2026-09-11）。
//
// 顧客名で解いたのと同じ問題が一段下に残っていた——1通のメールの5枚が
// `φ410 2輪` / `2輪シュート改良` / `φ410-2輪` / `2軸シュート改良`（輪→軸の誤読）に
// 割れ、そのまま流すと1台の装置が4フォルダに散る。
//
// **検証用に作ったページは最後に片付けます**（実データに残骸を積まない）。
const { chromium } = require('playwright');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';
const MAILBOX = process.env.WCMS_MAILBOX || '010153';
let fail = 0;
const ok = (c, m, x) => { console.log((c ? '  ✓ ' : '  ✗ ') + m + (x ? '  ' + x : '')); if (!c) fail++; };

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 1000 } });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  await page.goto(BASE + '/login');
  await page.fill('#username', 'a'); await page.fill('#password', 'a');
  await page.click('button[type=submit]'); await page.waitForLoadState('networkidle');

  // ── 下ごしらえ: 通信箱の下に仮の記録ページ、その下に図面ブロックを持つ部品ページ
  const api = (path, opts) => page.evaluate(async ([p, o]) => {
    const res = await fetch(p, o || {});
    return { status: res.status, text: await res.text() };
  }, [path, opts]);

  const mk = async (parent) => {
    const r = await page.evaluate(async (pid) => {
      const fd = new URLSearchParams(); fd.set('parent', pid);
      const res = await fetch('/api/new-page', { method: 'POST', body: fd });
      return { status: res.status, url: res.url };
    }, parent);
    const m = /\/(\d{6})/.exec(r.url || '');
    return m ? m[1] : null;
  };
  const save = async (id, html) => page.evaluate(async ([pid, body]) => {
    const lk = await (await fetch('/api/lock?id=' + pid, { method: 'POST' })).json();
    const res = await fetch('/api/save', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ page_id: pid, html: body, token: lk.token }),
    });
    await fetch('/api/unlock?id=' + pid, { method: 'POST' });
    return res.status;
  }, [id, html]);

  const recordID = await mk(MAILBOX);
  const partID = await mk(recordID);
  if (!recordID || !partID) { console.log('下ごしらえに失敗しました'); process.exit(1); }
  // **整理ボタンは通信記録にしか出ません**（`チャネル` タグで判定）。年フォルダや
  // 部品ページに出しても行き場がないため（2026-09-03 ユーザー指摘）。
  await save(recordID, '<h1>[検証用] 装置名称の候補</h1>'
    + '<dl data-type="tags"><dt>チャネル</dt><dd>メール</dd>'
    + '<dt>向き</dt><dd>受信</dd></dl>');
  await save(partID,
    '<h1>[検証用] 部品</h1><section data-id="tst1"><h2>図面</h2><dl>' +
    '<dt>図面番号</dt><dd>TEST-1</dd><dt>図面名称</dt><dd>[検証用] 部品</dd>' +
    '<dt>装置名称</dt><dd>2輪シュート改良</dd>'
    + '<dt>客先</dt><dd>トーアスポーツマシーン</dd></dl></section>');

  try {
    await page.goto(BASE + '/' + recordID);
    await page.waitForSelector('.filing-open', { timeout: 5000 });
    await page.click('.filing-open');
    await page.waitForSelector('.filing-panel', { timeout: 5000 });

    const r = await page.evaluate(() => {
      const panel = document.querySelector('.filing-panel');
      const cust = panel.querySelector('input[aria-label="customer"]');
      const mach = panel.querySelector('input[aria-label="machine_name"]');
      const listID = mach.getAttribute('list');
      const dl = listID ? document.getElementById(listID) : null;
      return {
        custList: cust.getAttribute('list'),
        machList: listID,
        custValue: cust.value,
        options: dl ? Array.from(dl.options).map(o => o.value) : null,
      };
    });
    ok(!!r.machList, '装置名称の欄に候補リストが付いている', r.machList);
    ok(r.custValue === 'トーアスポーツマシーン', '顧客名の推奨値が入っている', r.custValue);
    ok(r.options && r.options.length > 0, 'その顧客の装置が候補に出る', (r.options || []).join(' / '));
    ok(r.options && r.options.includes('φ410 2輪'), '既にある装置名が候補にある');

    // 顧客名を別の会社へ打ち替えると、候補が入れ替わる（空になる）
    await page.fill('.filing-panel input[aria-label="customer"]', '知らない会社');
    await page.waitForTimeout(200);
    const after = await page.evaluate(() => {
      const mach = document.querySelector('.filing-panel input[aria-label="machine_name"]');
      const dl = document.getElementById(mach.getAttribute('list'));
      return Array.from(dl.options).map(o => o.value);
    });
    ok(after.length === 0, '顧客を打ち替えると候補も追随する', '候補 ' + after.length + '件');

    // 戻すと復活する
    await page.fill('.filing-panel input[aria-label="customer"]', 'トーアスポーツマシーン');
    await page.waitForTimeout(200);
    const back = await page.evaluate(() => {
      const mach = document.querySelector('.filing-panel input[aria-label="machine_name"]');
      return Array.from(document.getElementById(mach.getAttribute('list')).options).map(o => o.value);
    });
    ok(back.length > 0, '顧客を戻すと候補も戻る', back.join(' / '));
    ok(errs.length === 0, 'JSエラーなし', errs[0] || '');
  } finally {
    // ── 片付け（子から先に消す。子持ちは削除できない）
    for (const id of [partID, recordID]) {
      // **削除には編集ロックが要ります**（handler_delete.go）。取らずに投げると409。
      // **`/api/lock` では取り直せません**——同じ利用者でも、別の口から握った
      // ロックは `ok:false, same_user:true` で断られます。片付けは強制取得で。
      // 応答がJSONで返らないこともあるので、素のテキストから拾います。
      const res = await page.evaluate(async (pid) => {
        let token = '';
        try {
          const lr = await fetch('/api/lock/force?id=' + pid, { method: 'POST' });
          const body = await lr.text();
          const m = /"token"\s*:\s*"([^"]+)"/.exec(body);
          if (m) token = m[1];
        } catch (e) { /* 取れなければトークン無しで試す */ }
        const r = await fetch('/api/delete-page?id=' + pid + '&token=' + token, { method: 'POST' });
        return { status: r.status, text: (await r.text()).slice(0, 60) };
      }, id);
      console.log('  片付け ' + id + ': ' + (res.status === 200 ? '削除' : res.status + ' ' + res.text));
    }
  }
  await browser.close();
  console.log(fail ? '\n' + fail + ' 件失敗' : '\n全項目OK');
  process.exit(fail ? 1 : 0);
})();
