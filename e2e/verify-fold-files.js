// 本文の折りたたみ（<details>）とその中のファイルを画面から確かめる（2026-09-28）。
//
// 利用者:「加工製品のページに、外注加工ごとに資料のブロック（開いたり閉じたりできる）を用意して、
// そこに保存したファイルをメールやFAX、印刷等に追加できるようにしてはどうでしょう？」。
//
//   ① 編集モードで折りたたみの中に「＋ ファイル」の札が出る（閲覧モードでは出ない）
//   ② 札からファイルを上げると、中にファイル表示の印（data-ref＝このページ-添付ID）が入り、保存される
//   ③ 題（summary）の文字を打てる——題を押しても閉じない・空白が打てる
//   ④ スラッシュメニュー「折りたたみ」で挿せて、題から打ち始められる
//
// 当て先は自分で作って最後に消します（トップ直下に1枚）。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-fold-files.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, bodyOf } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

const BODY = '<h1>【E2E】折りたたみ</h1>' +
  '<details open><summary>資料 1</summary><p>メモ</p></details><p>あとがき</p>';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  let id = '';
  const openEditor = async () => {
    await page.goto(BASE + '/' + id + '?edit=true');
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
  };
  const saved = async () => {
    // 自動保存（1.5秒のデバウンス）を待ってから正本を読む。
    await page.waitForTimeout(2500);
    return bodyOf(page, id);
  };
  try {
    await login(page, BASE);
    id = await makePage(page, BODY);
    check('当て先を作れた', !!id, id);
    await openEditor();

    // ① 札が出る
    const fold = page.locator('#w-editor-content details').first();
    check('編集モードで折りたたみに「＋ ファイル」の札が出る',
      await fold.locator(':scope > .fold-add-file').count() === 1);

    // ③ 題を押しても閉じない・空白が打てる
    const sum = fold.locator(':scope > summary');
    const sb = await sum.boundingBox();
    await page.mouse.click(sb.x + sb.width - 10, sb.y + sb.height / 2); // 題の文字の右端あたり
    await page.waitForTimeout(200);
    check('題を押しても閉じない', await fold.evaluate(el => el.open));
    await page.keyboard.press('End');
    await page.keyboard.type(' 追記');
    await page.waitForTimeout(200);
    const sumText = await sum.evaluate(el => el.textContent);
    check('題に空白ごと打てる', sumText.includes('資料 1 追記'), JSON.stringify(sumText));
    check('空白を打っても閉じない', await fold.evaluate(el => el.open));
    // Enter は折りたたみを割らず、中身の先頭へ移る。
    await page.keyboard.press('Enter');
    await page.keyboard.type('先頭');
    await page.waitForTimeout(200);
    check('題で Enter を押しても折りたたみは1つのまま',
      await page.locator('#w-editor-content details').count() === 1);
    check('題で Enter を押すと中身の先頭へ移る',
      (await sum.evaluate(el => el.textContent)) === sumText &&
      (await fold.locator(':scope > p').first().textContent()).startsWith('先頭'));
    // 左端の ▸ を押したときは開閉する（閉じて・開く）。
    await page.mouse.click(sb.x + 6, sb.y + sb.height / 2);
    await page.waitForTimeout(150);
    const closed = !(await fold.evaluate(el => el.open));
    await page.mouse.click(sb.x + 6, sb.y + sb.height / 2);
    await page.waitForTimeout(150);
    check('左端の ▸ では開閉できる', closed && await fold.evaluate(el => el.open));

    // ② 札からファイルを上げる（DXF＝汎用口・PNG＝画像口）
    const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==', 'base64');
    const [chooser] = await Promise.all([
      page.waitForEvent('filechooser'),
      fold.locator(':scope > .fold-add-file').click(),
    ]);
    await chooser.setFiles([
      { name: '展開.dxf', mimeType: 'application/octet-stream', buffer: Buffer.from('0\nSECTION\n0\nEOF\n') },
      { name: '写真.png', mimeType: 'image/png', buffer: png },
    ]);
    await page.waitForFunction(() => document.querySelectorAll(
      '#w-editor-content details section[data-type="file-view"]').length >= 2, null, { timeout: 8000 })
      .catch(() => {});
    const refs = await fold.evaluate(el => Array.from(el.querySelectorAll('section[data-type="file-view"]'))
      .map(s => s.getAttribute('data-ref')));
    check('上げた2つがファイル表示の印として中に入る', refs.length === 2 &&
      refs.every(r => new RegExp('^' + id + '-[0-9a-z]+$').test(r)), JSON.stringify(refs));

    // ④ スラッシュメニュー「折りたたみ」
    await page.locator('#w-editor-content p', { hasText: 'あとがき' }).click();
    await page.keyboard.press('End');
    await page.keyboard.press('Enter');
    await page.keyboard.type('/');
    await page.waitForSelector('#w-slash-menu.active', { timeout: 4000 });
    await page.locator('#w-slash-menu .slash-menu-item[data-type="details"]').click();
    await page.waitForTimeout(200);
    await page.keyboard.type('資料 2');
    await page.waitForTimeout(200);
    const second = page.locator('#w-editor-content details').nth(1);
    check('スラッシュメニューで折りたたみが入る', await second.count() === 1);
    check('挿したら題から打ち始められる',
      (await second.locator(':scope > summary').textContent()).includes('資料 2'));

    const body = await saved();
    check('保存: 題の文字', body.includes('<summary>資料 1 追記</summary>'), '');
    check('保存: 2つ目の折りたたみ', /<details open="?"?>\s*<summary>資料 2<\/summary>/.test(body) ||
      body.includes('<summary>資料 2</summary>'));
    check('保存: 中のファイル表示の印', refs.every(r => body.includes('data-ref="' + r + '"')));
    check('保存: 札は本文に残らない', !body.includes('fold-add-file') && !body.includes('＋ ファイル'));

    // ① 閲覧モードでは札が出ない
    await page.goto(BASE + '/' + id);
    await page.waitForSelector('#w-editor-content details', { timeout: 8000 });
    check('閲覧モードでは札が出ない', await page.locator('.fold-add-file').count() === 0);
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    if (id) await deletePage(page, id).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
