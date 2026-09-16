// 拡張が要る置き場が管理画面に並ぶ（2026-09-16）
//
// ユーザー:「拡張プラグインが必要とするフォルダなどは、管理画面でボタンを押して
// 作成する仕組みにしてはどうでしょう？」——正本は internal/cms/required_pages.go。
//
// **読むだけです**（ページを作りません）。このスクリプトが置き場を作ってしまうと、
// 実データのトップ直下に箱が増え、**E2Eがデータを汚す**ことになります
// ——2026-09-16 に一式を流してトップ直下へ43枚積もらせた反省。
//
// 期待値は**サーバーの名簿から導きます**——`/api/tag-schema` の `extensions` に
// 載っている拡張の置き場だけが出るはずなので、通常・`-tags nomail`・`-tags minimal`
// のどのビルドに当てても通ります。
const { chromium } = require('playwright');
const lib = require('./lib');
const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
let fail = 0;
const ok = (c, m, x) => { console.log((c ? '  OK ' : '  NG ') + m + (x ? '  ' + x : '')); if (!c) fail++; };

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 1200 }, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  await lib.login(page, BASE);

  const exts = await page.evaluate(async () => (await (await fetch('/api/tag-schema')).json()).extensions);
  const has = id => Array.isArray(exts) && exts.includes(id);
  console.log('    このビルド: ' + (exts && exts.length ? exts.join(', ') : '素の w-cms'));

  const d = await page.evaluate(async () => {
    const res = await fetch('/api/admin/pages');
    return { status: res.status, body: await res.json().catch(() => ({})) };
  });
  ok(d.status === 200 && d.body.success === true, '置き場の一覧を取れる', String(d.status));
  const pages = d.body.pages || [];
  const titles = pages.map(p => p.title);

  // **コアの持ち物は必ず在ります**——拡張が1つも無いビルドでも表が空にならないように。
  ok(titles.includes('テンプレート'), 'コアの置き場（テンプレート）が出る', titles.join(' / '));
  const core = pages.find(p => p.title === 'テンプレート');
  ok(core && core.extension === '', 'コアの持ち物は拡張IDが空', core && JSON.stringify(core.extension));

  // **載っている拡張のぶんだけ出ます。**
  ok(titles.includes('通信箱') === has('comm'),
     has('comm') ? '通信が載っている → 通信箱が出る' : '通信が無い → 通信箱は出ない');
  ok(titles.includes('取引先') === has('comm/contacts'),
     has('comm/contacts') ? 'アドレス帳が載っている → 取引先が出る' : 'アドレス帳が無い → 取引先は出ない');
  ok(titles.includes('受注') === has('subcon'),
     has('subcon') ? '下請けが載っている → 受注が出る' : '下請けが無い → 受注は出ない');

  // **理由は必ず添える**（押す人が、押してよいかを自分で判断できるように）。
  ok(pages.every(p => (p.why || '').trim().length > 0), 'どの行にも「何のために」が書いてある');
  // 在るものはページIDを持つ（画面がリンクにする）。
  ok(pages.every(p => !p.exists || /^\d{6}$/.test(p.page_id || '')),
     '在る置き場は6桁のページIDを返す', pages.filter(p => p.exists).map(p => p.title + ':' + p.page_id).join(' '));

  // ── 画面 ──
  await page.goto(BASE + '/assets/admin.html');
  await page.waitForTimeout(1200);
  const view = await page.evaluate(() => ({
    rows: document.querySelectorAll('#reqpages-table tbody tr').length,
    btn: (document.getElementById('reqpages-create') || {}).textContent || '',
    disabled: !!(document.getElementById('reqpages-create') || {}).disabled,
    links: document.querySelectorAll('#reqpages-table tbody a').length,
  }));
  ok(view.rows === pages.length, '表の行数が一覧と合う', view.rows + ' / ' + pages.length);
  const missing = pages.filter(p => !p.exists).length;
  // **押せないときは押せないと分かること**——足りないものが無いのにボタンが押せると、
  // 「押したのに何も起きない」になります（冪等なので害はないが、不安になる）。
  ok(view.disabled === (missing === 0),
     missing === 0 ? '足りないものが無ければボタンは押せない' : '足りないものがあればボタンは押せる',
     view.btn);
  ok(view.links === pages.filter(p => p.exists).length,
     '在る置き場は開けるリンクになっている', String(view.links));
  ok(errs.length === 0, 'JSエラーなし', errs[0] || '');

  await browser.close();
  console.log(fail ? '\n' + fail + ' 件失敗' : '\n全項目OK');
  process.exit(fail ? 1 : 0);
})();
