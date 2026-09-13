// 分類の取り消し（2026-09-13）——「間違えてアドレスを分類した場合、どうやって未分類に戻しますか？」
const { chromium } = require('playwright');
const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
let fail = 0;
const ok = (c, m, x) => { console.log((c ? '  ✓ ' : '  ✗ ') + m + (x ? '  ' + x : '')); if (!c) fail++; };

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 1000 }, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  await page.goto(BASE + '/login');
  await page.fill('#username', 'a'); await page.fill('#password', 'a');
  await page.click('button[type=submit]'); await page.waitForLoadState('networkidle');

  // ── 下ごしらえ: わざと間違えて分類する
  const addr = 'cloud-noreply@google.com';
  const made = await page.evaluate(async (a) => {
    const res = await fetch('/api/contacts/register', {
      method: 'POST', headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ name: 'まちがい会社', relation: '顧客', addresses: [a] }),
    });
    return res.json();
  }, addr);
  ok(made.success, '間違えて分類した（下ごしらえ）', made.page_id);

  const gone = await page.evaluate(async (a) => {
    const b = await (await fetch('/api/load?id=010263')).text();
    return !b.includes(a);
  }, addr);
  ok(gone, '未登録の一覧から消えている');

  // ── 取り消す
  await page.goto(BASE + '/' + made.page_id);
  await page.waitForTimeout(1200);
  const btn = await page.$('#w-editor-content .contact-unfile');
  ok(!!btn, '「未分類へ戻す」が出ている');
  if (btn) {
    ok(await btn.textContent() === '未分類へ戻す', 'ラベルが読める');
    await btn.click();
    await page.waitForTimeout(1200);
  }

  const back = await page.evaluate(async (a) => {
    const b = await (await fetch('/api/load?id=010263')).text();
    const own = await (await fetch('/api/load?id=' + location.pathname.slice(1))).text();
    return { inList: b.includes(a), stillOnPage: own.includes(a) };
  }, addr);
  ok(back.inList, '未登録の一覧へ戻った');
  ok(!back.stillOnPage, '分類先のページから消えた');

  // ページは消さない（押し間違いの取り消しでページを消さない）
  const alive = await page.evaluate(async (id) => (await fetch('/api/load?id=' + id)).ok, made.page_id);
  ok(alive, 'ページは消えていない（消すかは人が決める）');
  ok(errs.length === 0, 'JSエラーなし', errs[0] || '');

  // ── 片付け（検証用に作ったページは自分で消す。実データに残骸を積まない）
  const cleaned = await page.evaluate(async (id) => {
    // **`/api/lock/force` はロックが無いと204**（本文なし）なので、まず素のロック。
    let token = '';
    for (const path of ['/api/lock?id=', '/api/lock/force?id=']) {
      const body = await (await fetch(path + id, { method: 'POST' })).text();
      const m = /"token"\s*:\s*"([^"]+)"/.exec(body);
      if (m) { token = m[1]; break; }
    }
    const r = await fetch('/api/delete-page?id=' + id + '&token=' + token, { method: 'POST' });
    return r.status;
  }, made.page_id);
  console.log('  片付け ' + made.page_id + ': ' + (cleaned === 200 ? '削除' : 'status ' + cleaned));

  await browser.close();
  console.log(fail ? '\n' + fail + ' 件失敗' : '\n全項目OK');
  process.exit(fail ? 1 : 0);
})();
