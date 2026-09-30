// 節の中のファイル表示を1枚だけ外す・節へファイルを足す（2026-09-29）。
//
// 利用者:「加工製品ページで図面のブロックごと消すことは出来ますが、一枚の図面だけ消すことが出来ません」
// 「おそらく追加も難しいのでは？」「エディタを編集モードから閲覧モードに変更しても外すというボタンが消えません」。
//
//   ① 見出しの節（データ）の中の段落にキャレットを置き、書式の帯の 📎 から PDF を2枚足せる（2026-09-30——「＋ ファイル」の札はやめた）
//   ② 1枚だけ「✕ 外す」で外せる——保存した本文（正本のファイル）から消え、余計なものが残らない
//   ③ 閲覧モードに戻すと「✕ 外す」「＋ ファイル」「札」が消える
//   ④ どの節にも「＋ ファイル」の札は出ない（2026-09-30）
//
// 当て先は自分で作って最後に消します（トップ直下に1枚）。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-file-view-remove.js（リポジトリの e2e/ で）
const fs = require('fs');
const path = require('path');
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

function minimalPDF() {
  const stream = '40 40 m 500 400 l S';
  const objs = [
    '<< /Type /Catalog /Pages 2 0 R >>',
    '<< /Type /Pages /Kids [3 0 R] /Count 1 >>',
    '<< /Type /Page /Parent 2 0 R /MediaBox [0 0 595 842] /Contents 4 0 R /Resources << >> >>',
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

// rawBody は正本のファイル（サーバーが鏡を埋める前）を読みます——E2E はサーバーと同じ機械で走る前提。
function rawBody(id) {
  const f = path.join(__dirname, '..', 'data', 'master', id.slice(0, 2), id, id + '.html');
  try { return fs.readFileSync(f, 'utf8'); } catch (e) { return ''; }
}

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  let id = '';
  try {
    await login(page, BASE);
    id = await makePage(page, '<h1>【E2E】ファイル表示を外す</h1>' +
      '<section><h2>データ</h2><p>中身</p></section>' +
      '<section><h2>材料</h2><table><caption>材料</caption><tbody>' +
      '<tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>' +
      '<tr><td>鉄</td><td>FB</td><td>t3.2*50*100</td><td>1</td></tr></tbody></table></section>');
    check('当て先を作れた', !!id, id);

    await page.goto(BASE + '/' + id + '?edit=true');
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    const dataSec = '#w-editor-content section:has(> h2:text-is("データ"))';
    const matSec = '#w-editor-content section:has(> h2:text-is("材料"))';
    // ④ 札はもう出ない（2026-09-30 に書式の帯の 🖼・📎 へ移した）
    check('データの節にも材料の節にも「＋ ファイル」が出ない',
      await page.locator(dataSec + ' > .fold-add-file').count() === 0 && await page.locator(matSec + ' > .fold-add-file').count() === 0);

    // ① PDF を2枚足す
    for (const name of ['図面A.pdf', '図面B.pdf']) {
      const [chooser] = await Promise.all([
        page.waitForEvent('filechooser'),
        (async () => {
          await page.locator(dataSec + ' > p', { hasText: '中身' }).click();
          await page.waitForSelector('#w-context-toolbar.active #w-ctx-file', { timeout: 4000 });
          await page.locator('#w-context-toolbar #w-ctx-file').click();
        })(),
      ]);
      await chooser.setFiles([{ name, mimeType: 'application/pdf', buffer: minimalPDF() }]);
      await page.waitForTimeout(800);
    }
    const views = () => page.locator(dataSec + ' > section[data-type="file-view"][data-ref]').count();
    check('節の中にファイル表示が2つできた', await views() === 2, String(await views()));
    check('それぞれに「✕ 外す」が出る', await page.locator(dataSec + ' .fv-remove').count() === 2);
    await page.waitForTimeout(2500); // 自動保存
    const before = (rawBody(id).match(/data-type="file-view"/g) || []).length;
    check('保存した本文にファイル表示が2つ', before === 2, String(before));

    // ② 1枚だけ外す
    const firstRef = await page.locator(dataSec + ' > section[data-type="file-view"]').first().getAttribute('data-ref');
    await page.locator(dataSec + ' .fv-remove').first().click();
    await page.waitForTimeout(2500); // 自動保存
    check('画面から1枚だけ外れた', await views() === 1, String(await views()));
    const saved = rawBody(id);
    check('保存した本文から外れた（残りは1つ）',
      (saved.match(/data-type="file-view"/g) || []).length === 1 && !saved.includes(firstRef), firstRef);
    check('保存した本文に操作の札や包みが残らない',
      !/fv-remove|fv-wire|fold-add-file|editor-block|<div/.test(saved));
    check('データの節の見出しと中身は残る', saved.includes('<h2>データ</h2>') && saved.includes('中身'));

    // ③ 閲覧モードへ
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForFunction(() => !document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    await page.waitForTimeout(500);
    const left = await page.evaluate(() => document.querySelectorAll(
      '#w-editor-content .fv-remove, #w-editor-content .fv-wire, #w-editor-content .fold-add-file').length);
    check('閲覧モードでは「✕ 外す」「＋ ファイル」「札」が消える', left === 0, String(left));
  } finally {
    await deletePage(page, id);
  }
  check('JavaScript エラーなし', errs.length === 0, errs.join(' / '));
  console.log(fails === 0 ? '\n結果: 合格' : '\n結果: ' + fails + ' 件の不合格');
  await browser.close();
  process.exitCode = fails === 0 ? 0 : 1;
})();
