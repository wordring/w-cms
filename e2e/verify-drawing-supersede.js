// あとから改定にする（2026-10-04・assets/app.js の supersedeDrawingFrom・POST /api/drawing/supersede）を画面から確かめる。
//
// 利用者:「加工製品に図面を追加しました。するともともとあった図面と変更が見つかりました。そこで追加した図面を改定図面とし、
// 元々あった図面を古い図面として子ページにしたいのです」。
//
//   ① 図面が2つ並んだページの、図面のファイルの「⋯」に「📐 この図面で改定する」——図面の外のファイルには出ない
//   ② 押すと両方の番号と名前を出して確かめる——「やめる」なら何も送らない
//   ③ 「旧版へ移す」で、押した図面が残り、もう片方は「旧版 <番号> <名称>」の子ページへ
//      （改訂明細に新しい図面の行・古い図面の行は旧版ページへのリンク）
//   ④ 図面が1つになったら、もう出ない
//
// 自分で作ったページの上だけで動き、作ったページ（旧版の子ページも）は最後に消します。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-drawing-supersede.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, childrenOf } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 1000 } });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  const made = [];
  const sent = [];
  page.on('request', (r) => { if (r.url().includes('/api/drawing/supersede')) sent.push(r.postData() || ''); });

  const upload = async (pageId, name) => {
    const lock = await page.request.post(BASE + '/api/lock?id=' + pageId, { headers: { Origin: BASE } });
    const token = (await lock.json()).token;
    const res = await page.request.post(BASE + '/api/upload-pdf', {
      headers: { Origin: BASE, 'X-Lock-Token': token },
      multipart: { page_id: pageId, pdf_file: { name, mimeType: 'application/pdf', buffer: Buffer.from('%PDF-1.4 e2e ' + name) } },
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
  const drawing = (no, name, ref) => '<section><h2>図面</h2><dl data-type="tags"><dt>図面番号</dt><dd>' + no +
    '</dd><dt>図面名称</dt><dd>' + name + '</dd></dl>' + fv(ref) + '</section>';
  // menuOf は n 番目のファイル表示の「⋯」を押して、メニューの項目の文字を返します（開いたまま返す——押すなら呼び手が押す）。
  const openMenu = async (ref) => {
    await page.locator('#w-editor-content section[data-ref="' + ref + '"] .file-menu-btn').click();
    await page.locator('#w-file-menu.active').waitFor({ timeout: 5000 }).catch(() => {});
    return (await page.locator('#w-file-menu.active .file-menu-item').allTextContents()).join('｜');
  };

  try {
    await login(page, BASE);
    const id = await makePage(page, '<h1>【E2E】あとから改定</h1><p>x</p>');
    made.push(id);
    const a = await upload(id, '【E2E】図面A.pdf');
    const b = await upload(id, '【E2E】図面B.pdf');
    const c = await upload(id, '【E2E】資料.pdf');
    check('添付を3つ置けた', !!a && !!b && !!c, [a, b, c].join(','));
    await saveBody(id, '<h1>【E2E】あとから改定</h1>' + drawing('E2E-A1', '試験の台', id + '-' + a) + drawing('E2E-A2', '試験の台', id + '-' + b) +
      '<table><caption>改訂明細</caption><tbody><tr><th>版</th><th>図面番号</th><th>受領日</th></tr>' +
      '<tr><td>1</td><td>E2E-A1</td><td>2026-10-01</td></tr></tbody></table>' +
      '<section><h2>データ</h2>' + fv(id + '-' + c) + '</section>');

    await page.goto(BASE + '/' + id);
    await page.locator('#w-editor-content .file-menu-btn').first().waitFor({ timeout: 10000 }).catch(() => {});

    // ①
    const onDrawing = await openMenu(id + '-' + b);
    await page.keyboard.press('Escape');
    check('① 図面のファイルの「⋯」に「この図面で改定する」', /この図面で改定する/.test(onDrawing), onDrawing);
    const outside = await openMenu(id + '-' + c);
    await page.keyboard.press('Escape');
    check('① 図面の外のファイルには出ない', !/改定/.test(outside), outside);

    // ② やめる
    await openMenu(id + '-' + b);
    await page.locator('#w-file-menu.active .file-menu-item', { hasText: 'この図面で改定する' }).click();
    const dlg = page.locator('dialog.w-confirm[open]');
    await dlg.waitFor({ timeout: 5000 }).catch(() => {});
    const msg = (await dlg.locator('.w-confirm-msg').textContent().catch(() => '')) || '';
    check('② 両方の番号と名前を出して確かめる', /E2E-A1 試験の台/.test(msg) && /E2E-A2 試験の台/.test(msg) && msg.indexOf('E2E-A1') < msg.indexOf('E2E-A2'), msg);
    await dlg.getByRole('button', { name: 'やめる' }).click();
    await page.waitForTimeout(400);
    check('② 「やめる」なら何も送らない', sent.length === 0, String(sent.length));

    // ③ 旧版へ移す
    await openMenu(id + '-' + b);
    await page.locator('#w-file-menu.active .file-menu-item', { hasText: 'この図面で改定する' }).click();
    await page.locator('dialog.w-confirm[open]').getByRole('button', { name: '旧版へ移す' }).click();
    // 図面の節（見出し「図面」）——閲覧の画面では節がブロックの枠に包まれている。
    await page.waitForFunction(() => Array.from(document.querySelectorAll('#w-editor-content section'))
      .filter((s) => { const h = s.querySelector(':scope > h2'); return h && h.textContent.trim() === '図面'; }).length === 1,
    null, { timeout: 8000 }).catch(() => {});
    const body = await page.evaluate(() => {
      const secs = Array.from(document.querySelectorAll('#w-editor-content section'))
        .filter((s) => { const h = s.querySelector(':scope > h2'); return h && h.textContent.trim() === '図面'; });
      const link = Array.from(document.querySelectorAll('#w-editor-content table a')).map((x) => x.getAttribute('href') + ' ' + x.textContent);
      return { drawings: secs.map((s) => s.textContent.includes('E2E-A2') ? 'A2' : s.textContent.includes('E2E-A1') ? 'A1' : '?'), link };
    });
    const kids = await childrenOf(page, id);
    const old = (kids || []).find((k) => k.Title === '旧版 E2E-A1 試験の台');
    if (old) made.unshift(old.ID);
    check('③ 押した図面（A2）だけが残る', JSON.stringify(body.drawings) === '["A2"]', JSON.stringify(body.drawings));
    check('③ もう片方は「旧版 E2E-A1 試験の台」の子ページへ', !!old, JSON.stringify(kids));
    check('③ 改訂明細の E2E-A1 は旧版ページへのリンク', !!old && body.link.includes('/' + old.ID + ' E2E-A1'), JSON.stringify(body.link));
    const html = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).text(), id);
    check('③ 改訂明細に E2E-A2 の行', /<td>E2E-A2<\/td>/.test(html));
    if (old) {
      const oldHtml = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).text(), old.ID);
      check('③ 旧版ページに古い図面（ファイルごと）', oldHtml.includes('E2E-A1') && oldHtml.includes(id + '-' + a), '');
    }

    // ④
    const after = await openMenu(id + '-' + b);
    await page.keyboard.press('Escape');
    check('④ 図面が1つになったら、もう出ない', !/改定/.test(after), after);
  } finally {
    // 子ページ（旧版）は題に頼らず全部消してから親を消す——押し間違い（変異）で別の題の旧版ができても残さない。
    for (const pid of made.slice()) {
      for (const k of (await childrenOf(page, pid).catch(() => [])) || []) if (!made.includes(k.ID)) made.unshift(k.ID);
    }
    for (const pid of made) await deletePage(page, pid);
    const gone = await page.evaluate(async (ids) => Promise.all(ids.map(async (pid) => (await fetch('/api/load?id=' + pid)).status)), made);
    check('作ったページを消した', gone.every((s) => s === 404), gone.join(','));
  }
  check('ページのエラーが無い', errs.length === 0, errs.join(' / '));
  await browser.close();
  console.log(fails ? `✗ ${fails} 件` : '✓ すべて合格');
  process.exit(fails ? 1 : 0);
})();
