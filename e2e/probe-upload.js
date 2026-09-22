// 添付3経路（汎用・画像・PDF）が実際に保存できるか（2026-09-14）
//
// 保存の作法を1箇所（`cms.SaveAttachment`）へ寄せたので、**3本とも通ること**を
// 見ます。Goの試験はハンドラの入口までしか見ていないので、ここが要ります。
//
// 確かめるのは4つ: ①保存できる ②URLが「きれいな形」（/<6桁>/<生成ID>.<拡張子>）
// ③実際に取り出せる ④許可していない拡張子は理由つきで断られる。
const { chromium } = require('playwright');
const lib = require('./lib');
const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
// ⚠ **当て先を焼き込まず、自分で1枚作って最後に消します**（2026-09-20）。
// `000021` と書いてありましたが、データを入れ直すとそれは**実在の通信記録**になり、
// この試験は**実データのページへ添付を書き込んで**いました（初期化で消えるとはいえ、
// 届いた事実の記録に試験用のファイルが積もります）。
// `verify-filing-machines` などと同じく、下ごしらえは自分で用意します。
let PAGE = process.env.WCMS_HOST_PAGE || '';
let madePage = '';
let fail = 0;
const ok = (c, m, x) => { console.log((c ? '  OK ' : '  NG ') + m + (x ? '  ' + x : '')); if (!c) fail++; };

// 1x1 の PNG（マジックナンバーの検査を通る本物）。
const PNG_B64 = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==';

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 1000 }, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  await lib.login(page, BASE);
  if (!PAGE) {
    // トップの下に作業用の1枚を作る（題で分かるようにしておく）。
    madePage = await page.evaluate(async () => {
      const fd = new URLSearchParams({ parent: '000000' });
      const res = await fetch('/api/new-page', { method: 'POST', body: fd });
      const m = (res.url || '').match(/\/(\d{6})/);
      return m ? m[1] : '';
    });
    PAGE = madePage;
  }
  if (!PAGE) {
    console.log('  -- 作業用のページを作れません。飛ばします');
    await browser.close();
    process.exit(0);
  }
  await page.goto(BASE + '/' + PAGE);
  await page.waitForTimeout(1200);

  const CLEAN = /^\/\d{6}\/[0-9a-z]+\.[a-z0-9]+$/;

  const cases = [
    { name: '汎用（.dxf）', url: '/api/upload-file', field: 'file',
      file: ['0\nSECTION\n0\nENDSEC\n0\nEOF\n', 'x.dxf', 'application/octet-stream'], key: 'href' },
    { name: '画像（.png）', url: '/api/upload-image', field: 'image_file',
      file: [null, 'x.png', 'image/png'], key: 'src' },
    { name: 'PDF（.pdf）', url: '/api/upload-pdf', field: 'pdf_file',
      file: ['%PDF-1.4\n1 0 obj\n<<>>\nendobj\ntrailer\n<<>>\n%%EOF\n', 'x.pdf', 'application/pdf'], key: 'href' },
  ];

  for (const c of cases) {
    const r = await page.evaluate(async (a) => {
      let blob;
      if (a.file[0] === null) {
        const bin = atob(a.png);
        const arr = new Uint8Array(bin.length);
        for (let i = 0; i < bin.length; i++) arr[i] = bin.charCodeAt(i);
        blob = new Blob([arr], { type: a.file[2] });
      } else {
        blob = new Blob([a.file[0]], { type: a.file[2] });
      }
      const fd = new FormData();
      fd.append('page_id', a.pageID);
      fd.append(a.field, new File([blob], a.file[1], { type: a.file[2] }));
      const res = await fetch(a.url, { method: 'POST', body: fd });
      let text = ''; try { text = await res.text(); } catch (e) {}
      let d = {}; try { d = JSON.parse(text) || {}; } catch (e) { d = { message: text }; }
      return { status: res.status, d };
    }, { ...c, pageID: PAGE, png: PNG_B64 });

    ok(r.status === 200 && r.d.success, c.name + ' を保存できた',
       r.status + ' ' + (r.d.message || ''));
    const u = r.d[c.key] || '';
    ok(CLEAN.test(u), c.name + ' のURLがきれいな形', u);
    if (CLEAN.test(u)) {
      const got = await page.evaluate(async (x) => {
        const res = await fetch(x);
        return { status: res.status, len: (await res.arrayBuffer()).byteLength };
      }, u);
      ok(got.status === 200 && got.len > 0, c.name + ' を取り出せる', got.status + ' ' + got.len + 'バイト');
    }
  }

  // 許可していない拡張子は、理由つきで断られる（作法を寄せても検査は残っている）。
  const bad = await page.evaluate(async (id) => {
    const fd = new FormData();
    fd.append('page_id', id);
    fd.append('file', new File(['x'], 'x.exe', { type: 'application/octet-stream' }));
    const res = await fetch('/api/upload-file', { method: 'POST', body: fd });
    return { status: res.status, body: (await res.text()).trim() };
  }, PAGE);
  ok(bad.status === 400, '許可していない拡張子は断られる', String(bad.status));
  ok(/拡張子/.test(bad.body), '断る理由が書いてある', bad.body.slice(0, 60));

  ok(errs.length === 0, 'JSエラーなし', errs[0] || '');

  // **自分で作ったものは自分で片付けます**（添付ごとゴミ箱へ）。
  if (madePage) {
    const gone = await page.evaluate(async (id) => {
      const res = await fetch('/api/delete-page?id=' + id, { method: 'POST' });
      return res.status;
    }, madePage);
    console.log('  片付け ' + madePage + ': ' + (gone === 200 ? '削除' : 'HTTP ' + gone));
  }
  await browser.close();
  console.log(fail ? '\n' + fail + ' 件失敗' : '\n全項目OK');
  process.exit(fail ? 1 : 0);
})();
