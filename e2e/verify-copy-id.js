// ファイルの ID を写す道を画面から確かめる（2026-10-01）。
//
// 利用者:「メール送信にファイル添付が無いです。PDFや画像の表示にIDが在ると思いますが、それをクリック程度で簡単に
// クリップボードへコピーできると、ほかの場所にスラッシュメニューから貼りつけたり、メールに添付できるのでは」。
//
//   ① 写真にマウスを載せると 📝 の左に「🔗 ID」が出て、押すと写真の ID（ページ番号-添付ID）が写る
//   ② スラッシュメニューで「ファイル表示」を挿すと参照の欄がすぐ開き、貼るだけで配線される
//   （ファイル表示の頭の「🔗 ID」とメールの送る欄へ貼る道は verify-mail-compose.js）
//
// 当て先はトップ直下に自分で作り、最後に消します。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-copy-id.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

// 1x1 の PNG
const PNG = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==', 'base64');

let fails = 0;
const check = (label, ok, note = '') => { console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : '')); if (!ok) fails++; };

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 }, permissions: ['clipboard-read', 'clipboard-write'] });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let id = '';
  try {
    await login(page, BASE);
    id = await makePage(page, '<h1>【E2E】写真のID</h1><p>x</p>');
    const lock = await page.request.post(BASE + '/api/lock?id=' + id, { headers: { Origin: BASE } });
    const token = (await lock.json()).token;
    const up = await page.request.post(BASE + '/api/upload-image', {
      headers: { Origin: BASE, 'X-Lock-Token': token },
      multipart: { page_id: id, image_file: { name: '写真.png', mimeType: 'image/png', buffer: PNG } },
    });
    const ud = await up.json().catch(() => ({}));
    const src = ud.src || '';
    const m = src.match(/^\/(\d{6})\/([0-9a-z]+)\./i);
    await page.evaluate(async (arg) => {
      await fetch('/api/save', { method: 'POST', headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ page_id: arg.id, html: arg.html, token: arg.token }) });
    }, { id, token, html: '<h1>【E2E】写真のID</h1><p><img src="' + src + '" width="300" height="200"></p><p>y</p>' });
    await page.request.post(BASE + '/api/lock/force?id=' + id, { headers: { Origin: BASE } });
    check('写真を上げた', !!m, JSON.stringify(ud).slice(0, 120));
    await page.goto(BASE + '/' + id);
    const img = page.locator('#w-editor-content img').first();
    await img.waitFor({ timeout: 8000 });
    await img.hover();
    // 2026-10-01 から写真の札は「⋯」1つ——押すと形式に合った操作のメニュー（🔗 ID を写す はその中）。
    const dots = page.locator('#w-img-menu.active');
    await dots.waitFor({ timeout: 5000 }).catch(() => {});
    check('写真に載せると「⋯」が出る', await dots.count() === 1);
    await dots.click();
    const idItem = page.locator('#w-file-menu.active .file-menu-item', { hasText: 'ID を写す' });
    await idItem.waitFor({ timeout: 5000 }).catch(() => {});
    check('「⋯」のメニューに「🔗 ID を写す」', await idItem.count() === 1);
    await idItem.click();
    await page.waitForTimeout(300);
    const clip = await page.evaluate(() => navigator.clipboard.readText()).catch((e) => 'ERR ' + e);
    check('押すと写真の ID が写る', m && clip === m[1] + '-' + m[2], clip);

    // スラッシュメニューで「ファイル表示」を挿すと欄がすぐ開く
    await page.goto(BASE + '/' + id + '?edit=true');
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 }).catch(() => {});
    await page.waitForTimeout(500);
    check('編集モードに入れた', await page.evaluate(() => document.body.hasAttribute('edit-mode')));
    const last = page.locator('#w-editor-content p', { hasText: 'y' }).last();
    await last.click();
    await page.keyboard.press('End');
    await page.keyboard.press('Enter');
    await page.keyboard.type('/ファイル表示');
    await page.waitForTimeout(500);
    await page.keyboard.press('Enter');
    await page.waitForTimeout(800);
    const pop = await page.evaluate(() => {
      const p = document.getElementById('w-fv-popover');
      return { active: p && p.classList.contains('active'), focused: document.activeElement && document.activeElement.id };
    });
    check('挿すと参照の欄が開いてカーソルがある', pop.active && pop.focused === 'w-fv-ref', JSON.stringify(pop));
    await page.keyboard.press('Control+V');
    await page.waitForTimeout(500);
    const wired = await page.evaluate(() => {
      const s = [...document.querySelectorAll('#w-editor-content section[data-type="file-view"]')];
      return s.map(x => x.getAttribute('data-ref'));
    });
    check('貼ると配線される', m && wired.includes(m[1] + '-' + m[2]), JSON.stringify(wired));
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    if (id) {
      await page.request.post(BASE + '/api/lock/force?id=' + id, { headers: { Origin: BASE } }).catch(() => {});
      await deletePage(page, id).catch(() => {});
    }
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
