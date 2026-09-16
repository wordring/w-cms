// 参照タグが指すPDFを、そのタグの下でそのまま開く（2026-09-14）
//
// **業務の言葉に依らないこと**が眼目です。2026-09-14 まではコアの app.js が
// `<section><h2>図面</h2>` という板金の語彙で分岐していました——ユーザー:
// 「PDFを埋め込み表示するのはコア機能で、タグを見て表示するのが素直に思うのです」。
//
// だからこの試験は**「図面」という語を1つも使わない**ページで確かめます。
// 語に頼った実装へ戻ったら、ここが落ちます。
const { chromium } = require('playwright');
const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
const lib = require('./lib');
// **当て先は走るときに探します**（2026-09-16）——ページIDも添付IDもデータを入れ直す
// たびに変わるため（詳しくは lib.js の冒頭）。環境変数を渡せば探索を飛ばします。
let HOST = process.env.WCMS_HOST_PAGE || '';
let ATTACH = process.env.WCMS_ATTACH || '';
let fail = 0;
const ok = (c, m, x) => { console.log((c ? '  OK ' : '  NG ') + m + (x ? '  ' + x : '')); if (!c) fail++; };

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 1000 }, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  await lib.login(page, BASE);
  if (!HOST || !ATTACH) {
    const rec = await lib.findRecordWithPDF(page);
    HOST = HOST || rec.pageID; ATTACH = ATTACH || rec.attachID;
  }
  ok(!!HOST && !!ATTACH, '当て先を見つけた（PDFを持つ通信記録）', HOST + '-' + ATTACH);
  if (!HOST || !ATTACH) { console.log('通信箱にPDF付きの記録がありません'); await browser.close(); process.exit(1); }

  // 参照タグを1つだけ持つページを作ります。**見出しは業務語ではありません。**
  const made = await page.evaluate(async (host) => {
    const res = await fetch('/api/new-page', {
      method: 'POST',
      headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: 'parent=' + encodeURIComponent(host),
    });
    return { status: res.status, url: res.url };
  }, HOST);
  const id = (made.url.match(/\/(\d{6})/) || [])[1];
  ok(!!id, '検証用ページを作れた', id || made.url);
  if (!id) { await browser.close(); process.exit(1); }

  let done = false;
  try {
    // 本文を差し替える（ロックを取ってから保存）。
    const saved = await page.evaluate(async (arg) => {
      const lr = await fetch('/api/lock?id=' + arg.id, { method: 'POST' });
      const lj = await lr.json().catch(() => ({}));
      // **配線は属性1つ**（2026-09-15）。中の参照タグで指す形はやめました。
      const body = '<h1>しるし</h1>'
        + '<p>ここから編集を始めてください。</p>'
        + '<section data-type="file-view" data-ref="' + arg.host + '-' + arg.attach + '"></section>';
      const res = await fetch('/api/save', {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ page_id: arg.id, html: body, token: lj.token || '' }),
      });
      return { lock: lr.status, save: res.status, msg: (await res.text()).slice(0, 160) };
    }, { id, host: HOST, attach: ATTACH });
    console.log('    保存: lock=' + saved.lock + ' save=' + saved.save + ' ' + saved.msg);
    ok(saved.save === 200, '参照タグ1つのページを保存できた');

    await page.goto(BASE + '/' + id);
    await page.waitForTimeout(2500);

    const r = await page.evaluate(() => {
      const sec = document.querySelector('#w-editor-content section[data-type="file-view"]');
      const w = sec && sec.querySelector('.file-view');
      return {
        marker: !!sec,
        shown: !!w,
        // **配線が本文に残っていること**——属性が落ちると、マーカーは残るのに
        // 何を開くか分からなくなり、図面が黙って消えます。
        ref: sec ? sec.getAttribute('data-ref') : '',
        head: w ? (w.querySelector('.file-view-head') || {}).textContent : '',
        src: w ? (w.querySelector('embed') || {}).getAttribute('src') : '',
        fits: (() => {
          if (!w) return false;
          const e = w.querySelector('.file-view-body');
          return e && e.getBoundingClientRect().bottom <= w.getBoundingClientRect().bottom + 1;
        })(),
        // 本文HTMLにはマーカーだけが残ること（枠はクロームなので保存されない）。
        preview: document.getElementById('w-html-preview').value || '',
        overflow: document.documentElement.scrollWidth > window.innerWidth,
      };
    });
    ok(r.marker, 'マーカー（section[data-type="file-view"]）が本文に在る');
    ok(r.shown, '「図面」の語が無くてもPDFが開く（＝マーカー駆動）');
    ok(r.ref === HOST + '-' + ATTACH, '配線（data-ref）が本文に残っている', r.ref);
    ok((r.src || '').indexOf('/' + HOST + '/' + ATTACH + '.pdf') === 0, 'PDFのURLが参照から導かれている', r.src);
    ok(r.fits, 'PDFが枠に収まっている（見出しのぶんで溢れない）');
    ok(/data-type="file-view"/.test(r.preview), '本文にはマーカーが残る');
    ok(/data-ref="/.test(r.preview), '本文に配線の属性が保存される');
    ok(!/<embed|file-view-body/.test(r.preview), '本文に埋め込みは残らない（クロームなので保存されない）');
    ok(!r.overflow, '横にはみ出さない');

    // **編集モードを出入りしても消えないこと。** サーバー側へ移した理由そのものです
    // ——client 実装のころは `a.ref-link` に頼っていて、`/api/load` では作られないため
    // 編集モードから戻ると図面が黙って消えていました。
    // ⚠ モードのチェックボックスは見た目を作り替えてあるので `page.click` では
    // 届きません（30秒待って落ちます）。他のE2Eと同じく DOM の click を呼びます。
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForTimeout(1500);
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForTimeout(1800);
    const after = await page.evaluate(() => {
      const sec = document.querySelector('#w-editor-content section[data-type="file-view"]');
      return {
        shown: !!document.querySelector('#w-editor-content .file-view'),
        listeners: document.querySelectorAll('#w-editor-content .file-view[data-w-resize="1"]').length,
        // **シリアライザが配線を落としていないこと。** 属性の許可リストは
        // `/api/tag-schema` 由来なので、サーバーで許しても語彙に載せ忘れると
        // 編集モードを1往復しただけで data-ref が消え、図面が黙って落ちます。
        ref: sec ? sec.getAttribute('data-ref') : '',
      };
    });
    ok(after.shown, '編集モードを出入りしても枠が残る');
    ok(after.ref === HOST + '-' + ATTACH, '出入りしても配線が残る（シリアライザが落とさない）', after.ref);
    ok(after.listeners === 1, 'つまみの配線が二重にならない', String(after.listeners));

    ok(errs.length === 0, 'JSエラーなし', errs[0] || '');
    done = true;
  } finally {
    // ⚠ **`/api/unlock` では外れません**——別の口から握ったロックは、同じ利用者でも
    // 取り直せないためです（実測。削除が 409 で落ちて検証用ページが残りました）。
    // 強制解放（`/api/lock/force`）を通してから消します。**ロックが無ければ 204**
    // （本文なし）なので、JSONとして読まないこと。
    const del = await page.evaluate(async (i) => {
      await fetch('/api/lock/force?id=' + i, { method: 'POST' });
      const res = await fetch('/api/delete-page?id=' + encodeURIComponent(i), { method: 'POST' });
      return res.status;
    }, id);
    console.log('    後始末: 削除 ' + del + '（' + id + '）');
  }

  await browser.close();
  console.log(fail ? '\n' + fail + ' 件失敗' : '\n全項目OK');
  process.exit(fail || !done ? 1 : 0);
})();
