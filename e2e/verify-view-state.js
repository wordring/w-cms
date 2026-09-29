// 物ごとの見え方（折りたたみの開閉・ファイル表示の縦横比）をこの端末に憶えて戻すことを確かめる（2026-09-28）。
//
// 利用者:「各ページのPDFなどの埋め込みの縦横比や、ブロックの開閉状態をブラウザに記録して再生することは出来ますか？」
// 「開いてから長く経ったページの記録は、古い順に捨ててください」。
//
//   ① 閲覧モードで閉じた折りたたみは、開き直しても閉じたまま
//   ② ファイル表示の枠の縦横比を憶えていれば、その比で高さが決まる（窓の幅を変えても比が保たれる）
//   ③ 記録のページが上限（2000）を超えると、古い順に捨てる（開いているページは残る）
//
// 当て先は自分で作って最後に消します（トップ直下に1枚）。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-view-state.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

function minimalPDF() {
  const stream = '40 40 m 1100 800 l S';
  const objs = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 1190.55 841.89] /Contents 4 0 R /Resources << >> >>',
    '<< /Length ' + stream.length + ' >>\nstream\n' + stream + '\nendstream',
  ];
  let out = '%PDF-1.4\n';
  const offs = [];
  objs.forEach((o, i) => { offs.push(out.length); out += (i + 1) + ' 0 obj\n' + o + '\nendobj\n'; });
  const xref = out.length;
  out += 'xref\n0 ' + (objs.length + 1) + '\n0000000000 65535 f \n' +
    offs.map((o) => String(o).padStart(10, '0') + ' 00000 n \n').join('');
  out += 'trailer\n<< /Size ' + (objs.length + 1) + ' /Root 1 0 R >>\nstartxref\n' + xref + '\n%%EOF\n';
  return Buffer.from(out, 'latin1');
}

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  let id = '';
  try {
    await login(page, BASE);
    id = await makePage(page, '<h1>【E2E】見え方の記録</h1>' +
      '<details open><summary>資料 1</summary><p>図面</p></details>' +
      '<details open><summary>メモ</summary><p>中身</p></details>' +
      '<details open><summary>資料 2</summary><p>図面2</p></details>');
    check('当て先を作れた', !!id, id);

    // 図面（PDF）を「資料 1」へ上げる（編集モードの「＋ ファイル」）。
    await page.goto(BASE + '/' + id + '?edit=true');
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    const [chooser] = await Promise.all([
      page.waitForEvent('filechooser'),
      page.locator('#w-editor-content details .fold-add-file').first().click(),
    ]);
    await chooser.setFiles([{ name: '図面A3.pdf', mimeType: 'application/pdf', buffer: minimalPDF() }]);
    await page.waitForFunction(() => document.querySelector(
      '#w-editor-content details section[data-type="file-view"][data-ref]'), null, { timeout: 8000 });
    // 2枚目の PDF を「資料 2」へ（1枚目の大きさを変えても、2枚目は変わらないことを見るため）。
    const [chooser2] = await Promise.all([
      page.waitForEvent('filechooser'),
      page.locator('#w-editor-content details:has(> summary:text-is("資料 2")) .fold-add-file').click(),
    ]);
    await chooser2.setFiles([{ name: '図面A3-2.pdf', mimeType: 'application/pdf', buffer: minimalPDF() }]);
    await page.waitForFunction(() => document.querySelectorAll(
      '#w-editor-content details section[data-type="file-view"][data-ref]').length >= 2, null, { timeout: 8000 });
    await page.waitForTimeout(2500); // 自動保存

    // ① 閲覧モードで「メモ」を閉じる → 開き直しても閉じたまま
    await page.goto(BASE + '/' + id);
    await page.waitForTimeout(800);
    await page.locator('#w-editor-content details:has(> summary:text-is("メモ")) > summary').click();
    await page.waitForTimeout(300);
    await page.reload();
    await page.waitForTimeout(800);
    const memoOpen = await page.locator('#w-editor-content details:has(> summary:text-is("メモ"))').evaluate(d => d.open);
    check('閉じた折りたたみは、開き直しても閉じたまま', memoOpen === false);
    const docOpen = await page.locator('#w-editor-content details:has(> summary:text-is("資料 1"))').evaluate(d => d.open);
    check('触っていない折りたたみは開いたまま', docOpen === true);

    // ② 枠の右下をつまんで縮める → 開き直すと同じ縦横比
    const wrap = page.locator('#w-editor-content .file-view').first();
    const second = () => page.locator('#w-editor-content details:has(> summary:text-is("資料 2")) .file-view').first().boundingBox();
    const s0 = await second();
    await wrap.scrollIntoViewIfNeeded();
    const b0 = await wrap.boundingBox();
    await page.mouse.move(b0.x + b0.width - 4, b0.y + b0.height - 4);
    await page.mouse.down();
    await page.mouse.move(b0.x + b0.width - 4, b0.y + b0.height - 204, { steps: 8 });
    await page.mouse.up();
    await page.waitForTimeout(300);
    const b1 = await wrap.boundingBox();
    const saved = await page.evaluate(() => JSON.parse(localStorage.getItem('wcms.view') || 'null'));
    const rec = saved && saved.pages && saved.pages[id];
    const ratioKey = rec && Object.keys(rec.r || {}).find(k => k.startsWith('f:'));
    check('つまんで変えた枠の縦横比を憶えた', !!ratioKey, JSON.stringify(rec && rec.r));
    await page.reload();
    await page.waitForTimeout(1000);
    const b2 = await page.locator('#w-editor-content .file-view').first().boundingBox();
    check('開き直しても同じ高さ（縦横比）', Math.abs(b2.height - b1.height) <= 3, b1.height + ' → ' + b2.height);
    // ⚠ ほかの枠は変わらない（2026-09-29 利用者:「一枚のPDFの大きさを変えると、ページを再読み込みすると同じページの
    //    ほかのPDFの大きさも変わってしまいます」——最後につまんだ高さを全部の枠に当てていた）。
    const s2 = await second();
    check('1枚目を変えても、2枚目の高さは変わらない', s0 && s2 && Math.abs(s2.height - s0.height) <= 3,
      (s0 && s0.height) + ' → ' + (s2 && s2.height));
    await page.setViewportSize({ width: 1000, height: 900 });
    await page.waitForTimeout(600);
    const b3 = await page.locator('#w-editor-content .file-view').first().boundingBox();
    const r2 = b2.height / b2.width, r3 = b3.height / b3.width;
    check('窓の幅を変えても縦横比が保たれる', Math.abs(r3 - r2) < 0.02 || b3.height <= 201,
      r2.toFixed(3) + ' → ' + r3.toFixed(3));
    await page.setViewportSize({ width: 1280, height: 900 });

    // ③ 上限を超えたら古い順に捨てる（2005ページの古い記録を入れてから1つ書く）
    await page.evaluate(() => {
      const c = JSON.parse(localStorage.getItem('wcms.view'));
      for (let i = 1; i <= 2005; i++) c.pages['9' + String(i).padStart(5, '0')] = { t: i, r: {}, o: { 'd:x#0': 1 } };
      localStorage.setItem('wcms.view', JSON.stringify(c));
    });
    await page.reload();
    await page.waitForTimeout(800);
    await page.locator('#w-editor-content details:has(> summary:text-is("メモ")) > summary').click(); // 開く＝書く
    await page.waitForTimeout(300);
    const after = await page.evaluate(() => {
      const c = JSON.parse(localStorage.getItem('wcms.view'));
      return { n: Object.keys(c.pages).length, oldest: !!c.pages['900001'], newest: !!c.pages['902005'],
        mine: !!c.pages[location.pathname.slice(1)] };
    });
    check('記録は上限（2000ページ）まで', after.n <= 2000, String(after.n));
    check('古い記録から捨てた・開いているページと新しい記録は残る', !after.oldest && after.newest && after.mine,
      JSON.stringify(after));
    // 片付け（この端末の記録から E2E の分を消す）
    await page.evaluate(() => {
      const c = JSON.parse(localStorage.getItem('wcms.view'));
      for (const k of Object.keys(c.pages)) if (k.startsWith('9') || k === location.pathname.slice(1)) delete c.pages[k];
      localStorage.setItem('wcms.view', JSON.stringify(c));
    });
  } finally {
    await deletePage(page, id);
  }
  check('JavaScript エラーなし', errs.length === 0, errs.join(' / '));
  console.log(fails === 0 ? '\n結果: 合格' : '\n結果: ' + fails + ' 件の不合格');
  await browser.close();
  process.exitCode = fails === 0 ? 0 : 1;
})();
