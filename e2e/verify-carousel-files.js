// カルーセルにファイル表示を入れる（2026-10-23——internal/cms/file_view.go の fileViewSlideHTML・assets/app.js の「カルーセル」）を
// 画面から確かめる。
//
// 利用者:「カルーセルにファイル参照も入れたい」→（問いへの答え）入れるのは「ファイル表示（添付）」・PDF は「ファイル名の札（押すと開く）」。
//
//   閲覧 ① 画像のファイル表示は、ほかの枚と同じ1枚の画像（頭の行・枠は出ない）・押すと拡大表示に入る
//        ② PDF のファイル表示は、ファイル名の札（新しいタブで開く・埋め込みは出ない）
//   編集 ③ 「⬇ すぐ下を入れる」で、すぐ下のファイル表示も中へ移す
//        ④ 「📄 ファイル表示を足す」で空のファイル表示が中の末尾に足され、参照の欄が開く——貼ると本文に入る
//
// ページは自分で作り、最後に消します。本物の置き場には書きません。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-carousel-files.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, saveNow } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 1000 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let id = '';
  // upload はファイルをこのページの添付にして、添付の ID を返す（画像は upload-image・ほかは upload-file）。
  const upload = (b64, name, type) => page.evaluate(async (a) => {
    const lr = await fetch('/api/lock?id=' + a.id, { method: 'POST' });
    const lj = await lr.json().catch(() => ({}));
    const bin = Uint8Array.from(atob(a.b64), (ch) => ch.charCodeAt(0));
    const fd = new FormData();
    fd.append('page_id', a.id);
    const isImg = a.type.startsWith('image/');
    const isPDF = a.type === 'application/pdf';
    fd.append(isImg ? 'image_file' : isPDF ? 'pdf_file' : 'file', new File([bin], a.name, { type: a.type }));
    const url = isImg ? '/api/upload-image' : isPDF ? '/api/upload-pdf' : '/api/upload-file';
    const r = await fetch(url, { method: 'POST', body: fd, headers: { 'X-Lock-Token': lj.token || '' } });
    const d = await r.json().catch(() => ({}));
    await fetch('/api/unlock?id=' + a.id + '&token=' + encodeURIComponent(lj.token || ''), { method: 'POST' });
    return d.id || ((d.src || '').match(/\/\d{6}\/([a-z0-9]{4})\./) || [])[1] || '';
  }, { id, b64, name, type });
  const save = (html) => page.evaluate(async (a) => {
    const lr = await fetch('/api/lock?id=' + a.id, { method: 'POST' });
    const lj = await lr.json().catch(() => ({}));
    await fetch('/api/save', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ page_id: a.id, html: a.html, token: lj.token || '' }) });
    await fetch('/api/unlock?id=' + a.id + '&token=' + encodeURIComponent(lj.token || ''), { method: 'POST' });
  }, { id, html });
  try {
    await login(page, BASE);
    id = await makePage(page, '<h1>【E2E】カルーセルのファイル表示</h1><p>x</p>');
    // 小さな PNG（1×1 の赤）と、中身の短い PDF。
    const pngB64 = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8DwHwAFBQIAX8jx0gAAAABJRU5ErkJggg==';
    const pdfB64 = Buffer.from('%PDF-1.4\n1 0 obj<<>>endobj\ntrailer<<>>\n%%EOF\n').toString('base64');
    const imgID = await upload(pngB64, 'e2e-slide.png', 'image/png');
    const pdfID = await upload(pdfB64, 'e2e-slide.pdf', 'application/pdf');
    check('添付できた', /^[a-z0-9]{4}$/.test(imgID) && /^[a-z0-9]{4}$/.test(pdfID), imgID + ' ' + pdfID);
    const fv = (aid) => '<section data-type="file-view" data-ref="' + id + '-' + aid + '"></section>';
    await save('<h1>【E2E】カルーセルのファイル表示</h1><section data-type="carousel">' + fv(imgID) + fv(pdfID) + '</section>' + fv(imgID) + '<p>後の段落</p>');

    // ── 閲覧 ──
    await page.goto(BASE + '/' + id);
    await page.waitForSelector('#w-editor-content section[data-type="carousel"] .file-view-card', { timeout: 10000 }).catch(() => {});
    const v = await page.evaluate(() => {
      const car = document.querySelector('#w-editor-content section[data-type="carousel"]');
      const card = car.querySelector('a.file-view-card');
      return {
        slideImg: !!car.querySelector('img.file-view-slide-img'),
        details: car.querySelectorAll('details').length, embed: car.querySelectorAll('embed').length,
        card: card ? { href: card.getAttribute('href'), target: card.getAttribute('target'), name: card.textContent.trim() } : null,
      };
    });
    check('① 画像のファイル表示は1枚の画像（頭の行・枠は出ない）', v.slideImg && v.details === 0, JSON.stringify(v));
    check('② PDF はファイル名の札（新しいタブで開く・埋め込みは出ない）', !!v.card && v.card.href.includes('.pdf') && v.card.target === '_blank' &&
      v.card.name.includes('e2e-slide.pdf') && v.embed === 0, JSON.stringify(v.card));
    await page.click('#w-editor-content img.file-view-slide-img');
    await sleep(300);
    const lb = await page.evaluate(() => { const b = document.getElementById('w-lightbox'); return b && !b.classList.contains('is-hidden') ? b.querySelector('.lb-count').textContent : null; });
    check('① 画像の枚を押すと拡大表示に入る', !!lb, String(lb));
    await page.keyboard.press('Escape');

    // ── 編集 ──
    await page.goto(BASE + '/' + id + '?edit=true');
    await page.waitForSelector('#w-editor-content .car-edit-bar', { timeout: 10000 }).catch(() => {});
    await page.click('#w-editor-content .car-take');
    await sleep(300);
    const n3 = await page.evaluate(() => document.querySelectorAll('#w-editor-content section[data-type="carousel"] > section[data-type="file-view"]').length);
    check('③ 「⬇ すぐ下を入れる」で、すぐ下のファイル表示も中へ移す（3枚）', n3 === 3, String(n3));
    await page.click('#w-editor-content .car-add-file');
    await sleep(300);
    const pop = await page.evaluate(() => document.getElementById('w-fv-popover').classList.contains('active'));
    check('④ 「📄 ファイル表示を足す」で参照の欄が開く', pop);
    await page.fill('#w-fv-ref', id + '-' + pdfID);
    await page.keyboard.press('Enter');
    await sleep(300);
    await saveNow(page);
    // 保存した本文は字下げされるので、HTML として読んでカルーセルの中のファイル表示を数える。
    const refs = await page.evaluate(async (pid) => {
      const html = await (await fetch('/api/load?id=' + pid)).text();
      const doc = new DOMParser().parseFromString(html, 'text/html');
      return Array.from(doc.querySelectorAll('section[data-type="carousel"] > section[data-type="file-view"]'))
        .filter((s) => /^\d{6}-[a-z0-9]{4}$/.test(s.getAttribute('data-ref') || '')).length;
    }, id);
    check('④ 貼った参照が、カルーセルの中のファイル表示として本文に入る（4枚）', refs === 4, String(refs));
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    await page.goto(BASE + '/000000').catch(() => {});
    if (id) await deletePage(page, id).catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
