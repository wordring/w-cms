// 本文の折りたたみ（<details>）とその中のファイルを画面から確かめる（2026-09-28）。
//
// 利用者:「加工製品のページに、外注加工ごとに資料のブロック（開いたり閉じたりできる）を用意して、
// そこに保存したファイルをメールやFAX、印刷等に追加できるようにしてはどうでしょう？」。
//
//   ① 折りたたみの末尾に「＋ ファイル」の札は**もう出ない**（2026-09-30 に書式の帯の 🖼・📎 へ移した）
//   ② 中の段落にキャレットを置いて帯の 📎 からファイルを上げると、**画像は絵・それ以外はファイル表示の印**
//      （data-ref＝このページ-添付ID）で**折りたたみの中**に入り、保存される
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

    // ① 札はもう出ない（書式の帯の 🖼・📎 へ移した・2026-09-30）
    const fold = page.locator('#w-editor-content details').first();
    check('編集モードでも折りたたみに「＋ ファイル」の札が出ない',
      await page.locator('#w-editor-content .fold-add-file').count() === 0);

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

    // ② 中の段落にキャレットを置き、書式の帯の 📎 から上げる（DXF＝ファイル表示・PNG＝絵）
    const png = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==', 'base64');
    await fold.locator(':scope > p').first().click();
    await page.waitForSelector('#w-context-toolbar.active #w-ctx-file', { timeout: 4000 });
    check('書式の帯に 🖼 と 📎 が出る',
      await page.locator('#w-context-toolbar #w-ctx-image').count() === 1 &&
      await page.locator('#w-context-toolbar #w-ctx-file').count() === 1);
    const [chooser] = await Promise.all([
      page.waitForEvent('filechooser'),
      page.locator('#w-context-toolbar #w-ctx-file').click(),
    ]);
    await chooser.setFiles([
      { name: '展開.dxf', mimeType: 'application/octet-stream', buffer: Buffer.from('0\nSECTION\n0\nEOF\n') },
      { name: '写真.png', mimeType: 'image/png', buffer: png },
    ]);
    await page.waitForFunction(() => {
      const d = document.querySelector('#w-editor-content details');
      return d && d.querySelector('section[data-type="file-view"]') && d.querySelector('img');
    }, null, { timeout: 8000 }).catch(() => {});
    const refs = await fold.evaluate(el => Array.from(el.querySelectorAll('section[data-type="file-view"]'))
      .map(s => s.getAttribute('data-ref')));
    check('DXF はファイル表示の印として折りたたみの中に入る', refs.length === 1 &&
      refs.every(r => new RegExp('^' + id + '-[0-9a-z]+$').test(r)), JSON.stringify(refs));
    const imgSrc = await fold.evaluate(el => { const i = el.querySelector('img'); return i ? i.getAttribute('src') : ''; });
    check('写真は絵（img）として折りたたみの中に入る', new RegExp('^/' + id + '/[0-9a-z]+\\.png$').test(imgSrc), imgSrc);
    check('折りたたみは割れず1つのまま', await page.locator('#w-editor-content details').count() === 1);

    // ②' ドラッグ——折りたたみの中の段落へ落とすと、中のまま入る（2026-09-30・帯の 📎 と同じ処理）
    await page.evaluate(() => {
      const p = document.querySelector('#w-editor-content details > p');
      const dt = new DataTransfer();
      dt.items.add(new File(['0\nSECTION\n0\nEOF\n'], '落とした.dxf', { type: 'application/octet-stream' }));
      p.dispatchEvent(new DragEvent('drop', { bubbles: true, cancelable: true, dataTransfer: dt }));
    });
    await page.waitForFunction(() => document.querySelectorAll(
      '#w-editor-content details section[data-type="file-view"]').length >= 2, null, { timeout: 8000 }).catch(() => {});
    check('落としたファイルも折りたたみの中にファイル表示で入る',
      await fold.locator('section[data-type="file-view"]').count() === 2);
    check('スラッシュメニューに「📎 ファイル」がある', await page.locator('#w-slash-menu .slash-menu-item[data-type="attach"]').count() === 1);

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
    check('保存: 写真', !!imgSrc && body.includes('src="' + imgSrc + '"'));
    check('保存: 帯の札は本文に残らない', !body.includes('fold-add-file') && !body.includes('＋ ファイル') && !body.includes('w-ctx-'));

    // 閲覧モードでは写真にマウスを載せると 📝（ローカルで編集）が出る
    await page.goto(BASE + '/' + id);
    await page.waitForSelector('#w-editor-content details img', { timeout: 8000 });
    await page.locator('#w-editor-content details img').first().hover({ force: true });
    await page.waitForTimeout(200);
    check('写真に載せると 📝 が出る', await page.locator('#w-img-edit.active').count() === 1);
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
