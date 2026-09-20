// 解析で生まれた子ページが、押した直後に画面へ出るか（2026-09-14）
//
// **Gemini は呼びません。** `/api/analyze-attachment` を横取りして「図面と判定した」
// 応答だけを返し、画面の取り直しの配線を見ます——解析そのものは人間ゲート型で、
// 検証のたびに有料の呼び出しを起こすべきではないためです。
//
// 子ページは**本物を作ります**（サーバーが実際に返すものを見るため）。
const { chromium } = require('playwright');
const lib = require('./lib');
const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
// ⚠ **当て先は焼き込みません**（2026-09-20）。`000021` と書いてありましたが、
// データを入れ直すたびにページIDは総入れ替えになります——実際、初期化の翌日に
// `000021` は「RE: お見積り、製作依頼」（添付は .eml だけ）になり、**PDFが無いので
// 解析ボタンが出ず**に落ちました。走るときに `lib.findRecordWithPDF` で探します。
let PAGE = process.env.WCMS_PAGE || '';
let fail = 0;
const ok = (c, m, x) => { console.log((c ? '  OK ' : '  NG ') + m + (x ? '  ' + x : '')); if (!c) fail++; };

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 1000 }, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  await page.goto(BASE + '/login');
  await page.fill('#username', 'a'); await page.fill('#password', 'a');
  await page.click('button[type=submit]'); await page.waitForLoadState('networkidle');

  if (!PAGE) {
    const rec = await lib.findRecordWithPDF(page);
    PAGE = rec.pageID;
  }
  if (!PAGE) {
    console.log('  -- PDFを持つ通信記録がありません（取り込み前なら正常）。飛ばします');
    await browser.close();
    process.exit(0);
  }
  await page.goto(BASE + '/' + PAGE);
  await page.waitForTimeout(1800);

  const before = await page.evaluate(() => ({
    nav: Array.from(document.querySelectorAll('#w-child-nav-list a')).map(a => a.textContent),
    mirror: Array.from(document.querySelectorAll('#w-editor-content section[data-type="child-list"] a')).map(a => a.textContent),
    analyze: document.querySelectorAll('#w-editor-content .attach-analyze').length,
  }));
  console.log('    前: 左レール ' + before.nav.length + '件 / 本文の鏡 ' + before.mirror.length + '件');
  ok(before.analyze > 0, '🤖 解析ボタンが出ている', before.analyze + '個');
  if (!before.analyze) { await browser.close(); process.exit(1); }

  // **本物の子ページを作る**（この時点では画面に出ていないのが「今の不具合」）。
  // `/api/new-page` は新しいページへ**302で飛ばします**（JSONではない）ので、
  // 追いかけた先のURLからIDを採ります。
  const made = await page.evaluate(async (parent) => {
    const res = await fetch('/api/new-page', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: 'parent=' + encodeURIComponent(parent),
    });
    return { status: res.status, url: res.url };
  }, PAGE);
  console.log('    子ページ作成: ' + made.status + ' ' + made.url);
  const newId = (made.url.match(/\/(\d{6})/) || [])[1];
  ok(!!newId, '子ページができた', newId || made.url);
  if (!newId) { await browser.close(); process.exit(1); }

  // 作っただけでは画面に出ないこと（＝報告された症状そのもの）を先に確かめる。
  // 題は `/api/new-page` の既定（「新しいページ」）。以後これを目印にします。
  const MARK = '新しいページ';
  const stale = await page.evaluate((id) => ({
    nav: Array.from(document.querySelectorAll('#w-child-nav-list a'))
      .some(a => (a.getAttribute('href') || '').endsWith(id)),
  }), newId);
  ok(!stale.nav, '作った直後は画面に出ていない（取り直しが要る状態）');

  // 解析の応答だけを差し替える。
  await page.route('**/api/analyze-attachment', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({ success: true, doc_type: 'drawing', page_id: newId, title: MARK, matched_dxf: 0 }),
  }));

  await page.click('#w-editor-content .attach-analyze');
  await page.waitForTimeout(2500);

  const after = await page.evaluate((id) => {
    const has = sel => Array.from(document.querySelectorAll(sel))
      .some(a => (a.getAttribute('href') || '').endsWith(id));
    return {
      nav: Array.from(document.querySelectorAll('#w-child-nav-list a')).map(a => a.textContent),
      mirror: Array.from(document.querySelectorAll('#w-editor-content section[data-type="child-list"] a')).map(a => a.textContent),
      navHit: has('#w-child-nav-list a'),
      mirrorHit: has('#w-editor-content section[data-type="child-list"] a'),
      analyze: document.querySelectorAll('#w-editor-content .attach-analyze').length,
    };
  }, newId);
  console.log('    後: 左レール ' + after.nav.length + '件 / 本文の鏡 ' + after.mirror.length + '件');
  ok(after.navHit, '左レールの子ページ一覧に出た');
  if (before.mirror.length > 0) {
    ok(after.mirrorHit, '本文の子ページ一覧の鏡にも出た');
  } else {
    console.log('  — このページに本文の鏡はありません（左レールだけを見ます）');
  }
  ok(after.analyze > 0, '解析ボタンは残っている（押し直せる）', after.analyze + '個');
  // ── 1つのPDFに複数の図面（2026-09-20）──
  //
  // ⚠ **できた全部を見せているか。** `page_id`（1枚目）だけ読んでいると、3枚できても
  // 画面には1枚しか出ません——子ページ一覧には出るので消えはしませんが、**押した
  // 直後の手応えが嘘**になります。
  await page.unroute('**/api/analyze-attachment');
  await page.route('**/api/analyze-attachment', route => route.fulfill({
    status: 200,
    contentType: 'application/json',
    body: JSON.stringify({
      success: true, doc_type: 'drawing', matched_dxf: 0,
      pages: [
        { page_id: '000901', title: 'ブラケット図' },
        { page_id: '000902', title: 'カバー図' },
        { page_id: '000903', title: '軸受け図' },
      ],
    }),
  }));
  await page.click('#w-editor-content .attach-analyze');
  await page.waitForTimeout(1500);
  const multi = await page.evaluate(() =>
    Array.from(document.querySelectorAll('.toast'))
      .map(n => n.textContent).join(' | '));
  ok(/ブラケット図/.test(multi) && /カバー図/.test(multi) && /軸受け図/.test(multi),
     '3枚できたら3枚とも画面に出る', multi.replace(/\s+/g, ' ').slice(0, 90));
  ok(/3枚/.test(multi), '何枚できたかを言う');

  ok(errs.length === 0, 'JSエラーなし', errs[0] || '');

  // 後始末——作った検証用ページを消す。
  const del = await page.evaluate(async (id) => {
    const res = await fetch('/api/delete-page?id=' + encodeURIComponent(id), { method: 'POST' });
    return res.status + ' ' + (await res.text()).slice(0, 80);
  }, newId);
  console.log('    後始末: 削除 ' + del + '（' + newId + '）');

  await browser.close();
  console.log(fail ? '\n' + fail + ' 件失敗' : '\n全項目OK');
  process.exit(fail ? 1 : 0);
})();
