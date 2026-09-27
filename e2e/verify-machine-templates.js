// 機械が作るページのテンプレート（2026-09-27・テンプレート駆動の D）を画面から確かめる。
//
// 利用者:「テンプレートにはスラッシュメニューから表などの印を置き、コードはそれを埋めては
// どうでしょう？」——**テンプレートに印を置けること**と、**要るテンプレートが揃っているかが
// 管理画面で見えること**を見ます。
//
//   ① スラッシュメニューに「見出しの節」がある。挿すと <section><h2>…</h2> が本文に保存される
//      （メールの「本文」「添付ファイル」・発注書の「備考」・加工製品の「図面」の器）
//   ② 機械が行を入れる表（受注明細・発注明細）がスラッシュメニューに出る（2026-09-27 まで隠していた）
//   ③ 管理画面に「機械が作るページのテンプレート」の表があり、行ごとに在る／無いが出る
//
// 当て先は自分で作って最後に消します（トップ直下に1枚）。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-machine-templates.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, bodyOf } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

async function openSlashMenu(page) {
  const p = page.locator('#w-editor-content p').last();
  await p.click();
  await p.evaluate(el => {
    const r = document.createRange();
    r.selectNodeContents(el);
    const s = window.getSelection();
    s.removeAllRanges();
    s.addRange(r);
  });
  await page.keyboard.type('/');
  await page.waitForSelector('#w-slash-menu.active', { timeout: 4000 });
}

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true });
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  let id = '';
  try {
    await login(page, BASE);
    id = await makePage(page, '<h1>【E2E】テンプレートの印</h1><p><br></p>');
    check('当て先を作れた', !!id, id);
    await page.goto(BASE + '/' + id + '?edit=true');
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });

    // ② 機械が行を入れる表がメニューに出る。
    await openSlashMenu(page);
    const has = async t => (await page.locator(`#w-slash-menu .slash-menu-item[data-type="${t}"]`).count()) === 1;
    check('メニューに「見出しの節」がある', await has('section'));
    check('メニューに受注明細がある', await has('vocab:client-order-items'));
    check('メニューに発注明細がある', await has('vocab:our-order-items'));

    // ① 見出しの節を挿して、見出しを打つ。
    await page.locator('#w-slash-menu .slash-menu-item[data-type="section"]').click();
    await page.keyboard.type('本文');
    await page.waitForTimeout(2500); // 自動保存（1.5秒のデバウンス）を待つ
    const body = await bodyOf(page, id);
    check('見出しの節が本文に保存される', /<section[^>]*>\s*<h2>本文<\/h2>/.test(body), body.slice(0, 160));

    // ③ 管理画面。
    await page.goto(BASE + '/assets/admin.html');
    await page.waitForSelector('#pagetmpl-table tbody tr', { timeout: 8000 });
    const rows = await page.locator('#pagetmpl-table tbody tr').allTextContents();
    check('テンプレートの表に行がある', rows.length >= 3, rows.length + '行');
    const missing = rows.filter(r => r.includes('⚠'));
    check('どの行も「在る」か「無い」のどちらかを出す',
      rows.every(r => r.includes('✓') || r.includes('⚠')));
    console.log('  （無いテンプレート: ' + (missing.length ? missing.map(r => r.slice(0, 20)).join('／') : 'なし') + '）');
    check('JSエラーなし', errs.length === 0, errs.slice(0, 2).join(' | '));
  } catch (e) {
    check('最後まで到達', false, String(e));
  } finally {
    await deletePage(page, id);
    await browser.close();
  }
  console.log(fails ? `\n✗ ${fails} 件の失敗` : '\n全項目OK');
  process.exit(fails ? 1 : 0);
})();
