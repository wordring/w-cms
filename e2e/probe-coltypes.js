// 列型の一覧が、エディタとサーバーで揃っているか（2026-09-14）
//
// **手書きの一覧を置かない**という規律（語彙モデル §7 の原則1）の見張りです。
// 2026-09-14 まで `assets/app.js` と `assets/index.html` が6つを手で持っていて、
// サーバーが9つになったあとも古いままでした——`<th data-type="email">` と書いても
// エディタが黙って無視し、索引だけが `email` として扱う、というずれが出ていました。
const { chromium } = require('playwright');
const lib = require('./lib');
const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
let fail = 0;
const ok = (c, m, x) => { console.log((c ? '  OK ' : '  NG ') + m + (x ? '  ' + x : '')); if (!c) fail++; };

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1400, height: 1000 }, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  await lib.login(page, BASE);
  // **どのページでも構いません**——列設定の欄は殻（index.html）の持ち物で、
  // 本文には依りません。当て先を焼き込まないよう、必ず在るページを使います。
  await page.goto(BASE + '/' + (await lib.findMailbox(page) || '000000'));
  await page.waitForTimeout(1800);

  const got = await page.evaluate(async () => {
    const d = await (await fetch('/api/tag-schema')).json();
    const sel = document.getElementById('w-cp-type');
    return {
      server: d.column_types || [],
      options: sel ? Array.from(sel.options).map(o => o.value) : null,
      labels: sel ? Array.from(sel.options).map(o => o.textContent) : [],
    };
  });
  console.log('    サーバー: ' + got.server.join(' / '));
  console.log('    選択肢:   ' + (got.options || []).join(' / '));

  ok(got.server.length === 9, 'サーバーが9型を配っている', got.server.length + '型');
  ok(got.options !== null, '列設定ポップオーバの select がある');
  // 先頭は「（推論に任せる）」の空値。それを除くとサーバーの一覧と**同じ順で一致**。
  const picked = (got.options || []).slice(1);
  ok((got.options || [])[0] === '', '先頭は「推論に任せる」', got.labels[0] || '');
  ok(JSON.stringify(picked) === JSON.stringify(got.server),
     'エディタの選択肢がサーバーと完全に一致', picked.join(',') + ' vs ' + got.server.join(','));
  ['datetime', 'ref', 'email'].forEach(t => {
    ok(picked.indexOf(t) >= 0, t + ' を選べる');
  });
  // 説明の言葉は画面の持ち物——付いていること（型名だけになっていないこと）を見る。
  const emailLabel = got.labels[picked.indexOf('email') + 1] || '';
  ok(/（/.test(emailLabel), 'email に日本語の説明が付いている', emailLabel);

  ok(errs.length === 0, 'JSエラーなし', errs[0] || '');
  await browser.close();
  console.log(fail ? '\n' + fail + ' 件失敗' : '\n全項目OK');
  process.exit(fail ? 1 : 0);
})();
