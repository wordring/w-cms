// STEP ファイルを加工製品ページで参照して保存する（2026-10-01）を画面から確かめる。
//
// 利用者:「STEPファイルも加工製品ページで参照してダウンロードできるようにしたい」。
//
//   ① .step と .stp を添付に上げられる（設定 attachment_extensions）
//   ② 添付のリンク（📎）の横に「🔗 参照」——ID を写せる（PDF・ZIP 以外にも）
//   ③ 別のページ（加工製品ページの代わり）の 📄 ファイル表示にその ID を書くと、届いたときの名前で保存するリンク
//      （download 属性）が出て、押すと保存（Content-Disposition: attachment）になる
//
// 作ったページは最後に消します。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-step-file.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

// 中身は STEP の頭だけ（形を見るのはブラウザではなく人の CAD）。
const STEP = Buffer.from('ISO-10303-21;\nHEADER;\nFILE_DESCRIPTION((\'E2E\'),\'2;1\');\nENDSEC;\nDATA;\nENDSEC;\nEND-ISO-10303-21;\n');

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  const made = [];
  try {
    await login(page, BASE);
    const host = await makePage(page, '<h1>【E2E】STEP の置き場</h1><p>x</p>');
    made.push(host);
    const lock = await page.request.post(BASE + '/api/lock?id=' + host, { headers: { Origin: BASE } });
    const token = (await lock.json()).token;
    const up = async (name) => {
      const r = await page.request.post(BASE + '/api/upload-file', {
        headers: { Origin: BASE, 'X-Lock-Token': token },
        multipart: { page_id: host, file: { name, mimeType: 'application/octet-stream', buffer: STEP } },
      });
      return r.json().catch(() => ({}));
    };
    const a = await up('【E2E】カバー.step');
    const b = await up('【E2E】カバー.stp');
    check('① .step と .stp を上げられる', a.success && b.success, JSON.stringify([a.href, b.href, a.message, b.message]));
    const html = '<h1>【E2E】STEP の置き場</h1><p><a href="' + a.href + '" download="【E2E】カバー.step">【E2E】カバー.step</a></p>';
    await page.evaluate(async (arg) => {
      await fetch('/api/save', { method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ page_id: arg.id, html: arg.html, token: arg.token }) });
    }, { id: host, html, token });
    await page.request.post(BASE + '/api/lock/force?id=' + host, { headers: { Origin: BASE } });

    // ②
    await page.goto(BASE + '/' + host);
    const copy = page.locator('#w-editor-content .attach-copyref');
    await copy.first().waitFor({ timeout: 8000 }).catch(() => {});
    const ref = host + '-' + a.id;
    check('② 📎 の横に「🔗 参照」', await copy.count() === 1 && ((await copy.first().getAttribute('title')) || '').startsWith(ref), ref);

    // ③
    const product = await makePage(page, '<h1>【E2E】STEP を参照する品</h1><section data-type="file-view" data-ref="' + ref + '"></section>');
    made.unshift(product);
    await page.goto(BASE + '/' + product);
    const link = page.locator('#w-editor-content section[data-type="file-view"] .file-view-head a[download]').first();
    await link.waitFor({ timeout: 8000 }).catch(() => {});
    const dl = await link.getAttribute('download').catch(() => '');
    const href = await link.getAttribute('href').catch(() => '');
    check('③ ファイル表示に届いたときの名前で保存するリンク', dl === '【E2E】カバー.step' && href === a.href, dl + ' ' + href);
    const res = await page.request.get(BASE + href);
    check('押すと保存（attachment）', res.status() === 200 && (res.headers()['content-disposition'] || '').startsWith('attachment'),
      res.status() + ' ' + res.headers()['content-disposition']);
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    for (const id of made) await deletePage(page, id).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
