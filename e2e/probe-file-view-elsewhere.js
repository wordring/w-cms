// 添付を「ほかのページ」に出す一連（2026-09-14）
//
// ユーザー:「PDFがほかの部品の一部で、部品ページを作らず、ほかのページに
// 埋め込む場合はどうしますか？」
//
// 確かめるのは3つです:
//   ① 添付の隣に「🔗 参照」が出て、`ページID-添付ID` を写せる
//   ② スラッシュメニューの 📄 ファイル表示 が、**貼る場所のある**骨格を挿す
//   ③ 無関係なページに、**宣言していないタグ名**で貼っても開く
const { chromium } = require('playwright');
const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
const HOST = process.env.WCMS_HOST_PAGE || '010272';
const ATTACH = process.env.WCMS_ATTACH || 'c3p7';
let fail = 0;
const ok = (c, m, x) => { console.log((c ? '  OK ' : '  NG ') + m + (x ? '  ' + x : '')); if (!c) fail++; };

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({
    viewport: { width: 1400, height: 1000 }, ignoreHTTPSErrors: true,
    permissions: ['clipboard-read', 'clipboard-write'],
  });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  await page.goto(BASE + '/login');
  await page.fill('#username', 'a'); await page.fill('#password', 'a');
  await page.click('button[type=submit]'); await page.waitForLoadState('networkidle');

  // ① 添付の隣の「🔗 参照」
  await page.goto(BASE + '/' + HOST);
  await page.waitForTimeout(2000);
  const hasCopy = await page.evaluate(() =>
    document.querySelectorAll('#w-editor-content .attach-copyref').length);
  ok(hasCopy > 0, '添付の隣に「🔗 参照」が出る', hasCopy + '個');
  let copied = '';
  if (hasCopy) {
    await page.click('#w-editor-content .attach-copyref');
    await page.waitForTimeout(500);
    copied = await page.evaluate(() => navigator.clipboard.readText());
  }
  ok(copied === HOST + '-' + ATTACH, '参照が写せた', copied);

  // ② 📄 ファイル表示 の骨格に、貼る場所があるか（レジストリの宣言から）
  const skeleton = await page.evaluate(async () => {
    const d = await (await fetch('/api/tag-schema')).json();
    const def = (d.vocab || []).find(v => v.type === 'file-view');
    return def ? { name: def.display_name, cols: (def.columns || []).map(c => c.label + ':' + c.type) } : null;
  });
  ok(!!skeleton, '📄 ファイル表示 がレジストリに在る', skeleton ? skeleton.name : '');
  ok(skeleton && skeleton.cols.length === 1 && /:ref$/.test(skeleton.cols[0]),
     '貼る場所（ref の欄）が1つある', skeleton ? skeleton.cols.join(',') : '');

  // ③ 無関係なページへ、宣言していないタグ名で貼る
  const made = await page.evaluate(async () => {
    const res = await fetch('/api/new-page', {
      method: 'POST', headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
      body: 'parent=000000',
    });
    return res.url;
  });
  const id = (made.match(/\/(\d{6})/) || [])[1];
  ok(!!id, '無関係なページを作れた', id || made);
  if (!id) { await browser.close(); process.exit(1); }

  try {
    const saved = await page.evaluate(async (arg) => {
      const lr = await fetch('/api/lock?id=' + arg.id, { method: 'POST' });
      const lj = await lr.json().catch(() => ({}));
      // **`参考図` は語彙に宣言していません**——名前は書く人が決めます。
      const body = '<h1>装置まるごと</h1><p>子部品の図面をここに出します。</p>'
        + '<section data-type="file-view">'
        + '<dl><dt>参考図</dt><dd>' + arg.ref + '</dd></dl></section>';
      const res = await fetch('/api/save', {
        method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ page_id: arg.id, html: body, token: lj.token || '' }),
      });
      return res.status;
    }, { id, ref: HOST + '-' + ATTACH });
    ok(saved === 200, '貼ったページを保存できた', String(saved));

    await page.goto(BASE + '/' + id);
    await page.waitForTimeout(2200);
    const r = await page.evaluate(() => {
      const w = document.querySelector('#w-editor-content .file-view');
      return {
        shown: !!w,
        head: w ? (w.querySelector('.file-view-head') || {}).textContent : '',
        src: w ? (w.querySelector('embed') || {}).getAttribute('src') : '',
        tagKept: !!document.querySelector('section[data-type="file-view"] dl'),
      };
    });
    ok(r.shown, '部品ページでなくてもPDFが開く');
    ok(/参考図/.test(r.head || ''), '宣言していないタグ名でも出どころに出る', (r.head || '').trim());
    ok((r.src || '').indexOf('/' + HOST + '/' + ATTACH + '.pdf') === 0, 'URLが参照から導かれている', r.src);
    ok(r.tagKept, '書いたタグが消えていない');
    ok(errs.length === 0, 'JSエラーなし', errs[0] || '');
  } finally {
    const del = await page.evaluate(async (i) => {
      await fetch('/api/lock/force?id=' + i, { method: 'POST' });
      const res = await fetch('/api/delete-page?id=' + encodeURIComponent(i), { method: 'POST' });
      return res.status;
    }, id);
    console.log('    後始末: 削除 ' + del + '（' + id + '）');
  }

  await browser.close();
  console.log(fail ? '\n' + fail + ' 件失敗' : '\n全項目OK');
  process.exit(fail ? 1 : 0);
})();
