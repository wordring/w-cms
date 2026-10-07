// ページの更新の知らせ（2026-10-07——internal/cms/page_events.go・assets/app.js の「ページの更新の知らせ」）を画面から確かめる。
//
// 利用者:「誰かが書き込むと、同じページを見ているほかの人に再読み込み通知が送られて、編集されたブロックだけ
// 再読み込みできますか？」
//
//   ① 中身の変わらない保存では知らせない（開いたときの姿と読み直したものが同じ描き方——見出しのアンカー・参照リンク）
//   ② 書き込むと閲覧中のタブに知らせ（N か所）→ 押すと変わったブロックだけ差し替える（変わっていないブロックは
//      要素ごと残る・差し替えたブロックに色）
//   ③ 差し替えたあとも、中身の変わらない保存では知らせない
//   ④ 入力中の欄があるブロックは差し替えない（知らせを残す）→ 入力を終えて押すと差し替わる
//   ⑤ 編集モードを出入りしたあとも、中身の変わらない保存では知らせず、変わったら知らせる
//
// ページは自分で作り（ID の無いブロックだけ——機械が作るページと同じ形）、最後に消します。本物の置き場には書きません。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-page-updates.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, childrenOf } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

(async () => {
  const browser = await chromium.launch();
  const opts = { ignoreHTTPSErrors: true, viewport: { width: 1400, height: 1000 } };
  const ctxW = await browser.newContext(opts);
  const ctxV = await browser.newContext(opts);
  const writer = await ctxW.newPage();
  const viewer = await ctxV.newPage();
  const errs = [];
  viewer.on('pageerror', (e) => errs.push(String(e)));
  let viewLoads = 0;
  viewer.on('request', (r) => { if (/\/api\/load\?.*view=1/.test(r.url())) viewLoads++; });
  let box = '';
  let target = '';
  const save = async (html) => {
    await writer.evaluate(async (arg) => {
      const lr = await fetch('/api/lock?id=' + arg.id, { method: 'POST' });
      const lj = await lr.json().catch(() => ({}));
      await fetch('/api/save', { method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ page_id: arg.id, html: arg.html, token: lj.token || '' }) });
      await fetch('/api/unlock?id=' + arg.id + '&token=' + encodeURIComponent(lj.token || ''), { method: 'POST' });
    }, { id: box, html });
  };
  const toast = () => viewer.locator('#w-toast-host [data-toast-id="page-updated"]');
  const toastText = async () => ((await toast().count()) ? (await toast().first().textContent()) : '');
  const waitToast = async (re, ms = 15000) => {
    const until = Date.now() + ms;
    while (Date.now() < until) {
      const t = await toastText();
      if (re.test(t)) return t;
      await sleep(250);
    }
    return await toastText();
  };
  // quiet は、読み直しが走ったのに知らせが出ないことを確かめる（読み直しが走らなければ空振りなので落とす）。
  const quiet = async (label) => {
    const before = viewLoads;
    const until = Date.now() + 15000;
    while (Date.now() < until && viewLoads === before) await sleep(250);
    await sleep(1000);
    check(label, viewLoads > before && (await toast().count()) === 0, `読み直し ${viewLoads - before} 回・知らせ「${await toastText()}」`);
  };
  const keepOf = () => viewer.evaluate(() => Array.from(document.querySelectorAll('#w-editor-content .editor-block > .block-content > *'))
    .map((el) => (el.__keep || '') + ':' + el.tagName + ':' + el.textContent.trim().slice(0, 12) + (el.closest('.editor-block').classList.contains('w-block-updated') ? ':色' : '')));
  const page = (a, b) => '<h1>【E2E】更新の知らせ</h1><dl data-type="tags"><dt>受信元</dt><dd>' + target + '</dd></dl>' +
    '<h2>見出し</h2><p>' + a + '</p><p>' + b + '</p>';
  try {
    await login(writer, BASE);
    await login(viewer, BASE);
    box = await makePage(writer, '<h1>【E2E】更新の知らせ</h1><p>x</p>');
    target = await makePage(writer, '<h1>【E2E】指す先</h1><p>x</p>', box);
    let body = page('段落A', '段落B');
    await save(body);

    const connected = viewer.waitForRequest((r) => r.url().includes('/api/page-events'), { timeout: 15000 }).catch(() => null);
    await viewer.goto(BASE + '/' + box);
    check('閲覧のタブが知らせを購読する', !!(await connected));
    await sleep(1500);
    // 開いたときの姿を覚えるのに読み直さない（鏡の計算を含む読み直しを、開くたびに1回余計に走らせない）。
    check('開いたときに読み直しを走らせない', viewLoads === 0, `読み直し ${viewLoads} 回`);
    check('開いたときの本文に参照リンク', (await viewer.locator('#w-editor-content dl a.ref-link').count()) === 1);
    await viewer.evaluate(() => {
      const els = Array.from(document.querySelectorAll('#w-editor-content .editor-block > .block-content > *'));
      els.forEach((el, i) => { el.__keep = 'k' + i; });
    });

    // ① 中身の変わらない保存。
    await save(body);
    await quiet('① 中身の変わらない保存では知らせない（開いたときの姿と同じ描き方）');

    // ② 段落A を変えて、段落C を足す。
    body = page('段落A（直した）', '段落B') + '<p>段落C</p>';
    await save(body);
    const t2 = await waitToast(/か所/);
    check('② 書き込むと知らせる（2 か所）', /（2 か所）/.test(t2), t2);
    await viewer.locator('#w-toast-host [data-toast-id="page-updated"] button', { hasText: '変わったところを読み込む' }).click({ timeout: 5000 });
    await sleep(800);
    const k2 = await keepOf();
    check('② 変わったところだけ差し替える（見出し・タグ・段落B は要素ごと残る・A と C は新しく色つき）',
      JSON.stringify(k2) === JSON.stringify(['k0:H1:【E2E】更新の知らせ', 'k1:DL:' + k2[1].split(':')[2], 'k2:H2:見出し', ':P:段落A（直した）:色', 'k4:P:段落B', ':P:段落C:色']),
      JSON.stringify(k2));
    check('② 差し替えたあとも参照リンク', (await viewer.locator('#w-editor-content dl a.ref-link').count()) === 1);
    check('② 知らせは消える', (await toast().count()) === 0, await toastText());

    // ③ 差し替えたあとの、中身の変わらない保存。
    await save(body);
    await quiet('③ 差し替えたあとも、中身の変わらない保存では知らせない');

    // ④ 入力中の欄があるブロックは差し替えない。
    await viewer.evaluate(() => {
      const b = Array.from(document.querySelectorAll('#w-editor-content p')).find((p) => p.textContent.includes('段落B'));
      const input = document.createElement('input');
      input.className = 'e2e-typing';
      b.appendChild(input);
    });
    await viewer.fill('#w-editor-content input.e2e-typing', '書きかけ', { timeout: 5000 });
    body = page('段落A（直した）', '段落B（直した）') + '<p>段落C</p>';
    await save(body);
    const t4 = await waitToast(/か所/);
    check('④ 知らせる（1 か所）', /（1 か所）/.test(t4), t4);
    await viewer.locator('#w-toast-host [data-toast-id="page-updated"] button', { hasText: '変わったところを読み込む' }).click({ timeout: 5000 });
    await sleep(800);
    const t4b = await toastText();
    check('④ 入力中のブロックは差し替えず、そう言う', (await viewer.inputValue('#w-editor-content input.e2e-typing', { timeout: 5000 }).catch(() => '')) === '書きかけ' &&
      /入力中の欄があるところ（1 か所）/.test(t4b), t4b);
    await viewer.fill('#w-editor-content input.e2e-typing', '', { timeout: 5000 }).catch(() => {});
    await viewer.evaluate(() => document.activeElement && document.activeElement.blur());
    await viewer.locator('#w-toast-host [data-toast-id="page-updated"] button', { hasText: '変わったところを読み込む' }).click({ timeout: 5000 });
    await sleep(800);
    const k4 = await keepOf();
    check('④ 入力を終えて押すと差し替わる', k4[4] === ':P:段落B（直した）:色' && k4[3].startsWith('k3') === false && (await toast().count()) === 0,
      JSON.stringify(k4));

    // ⑤ 編集モードを出入りしたあと。
    await viewer.evaluate(() => document.getElementById('w-mode-toggle').click());
    await viewer.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 10000 }).catch(() => {});
    await sleep(500);
    await viewer.evaluate(() => document.getElementById('w-mode-toggle').click());
    await viewer.waitForFunction(() => !document.body.hasAttribute('edit-mode'), null, { timeout: 10000 }).catch(() => {});
    await sleep(1500);
    await save(body);
    await quiet('⑤ 編集を出入りしたあとも、中身の変わらない保存では知らせない');
    body = page('段落A（もう一度）', '段落B（直した）') + '<p>段落C</p>';
    await save(body);
    const t5 = await waitToast(/か所/);
    check('⑤ 変わったら知らせる（1 か所）', /（1 か所）/.test(t5), t5);
  } finally {
    const all = [];
    const walk = async (id) => { for (const k of (await childrenOf(writer, id).catch(() => [])) || []) { await walk(k.ID); } all.push(id); };
    if (box) await walk(box);
    for (const id of all) await deletePage(writer, id);
    const gone = await writer.evaluate(async (ids) => Promise.all(ids.map(async (pid) => (await fetch('/api/load?id=' + pid)).status)), all);
    check('作ったページを消した', gone.length > 0 && gone.every((s) => s === 404), gone.join(','));
  }
  check('ページのエラーが無い', errs.length === 0, errs.join(' / '));
  await browser.close();
  console.log(fails ? `✗ ${fails} 件` : '✓ すべて合格');
  process.exit(fails ? 1 : 0);
})();
