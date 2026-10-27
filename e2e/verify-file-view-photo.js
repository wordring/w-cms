// 写真のファイル表示の枠が、写真の大きさに合うことを画面から確かめる（2026-10-27・assets/app.css）。
//
// 利用者:「写真と動画のファイル表示の枠を、写真の大きさに合わせる形に直してください。ハンドルも外してください。」
// それまでは枠の高さが画面の 70% に決まっていて、写真がその枠に合わせて押しつぶされていた。
//
//   ① 縦長の写真も横長の写真も、縦横の比を保って出る（押しつぶさない）
//   ② 枠の高さは写真に合う（下に大きな空きを作らない）
//   ③ 大きさを変えるハンドル（resize）も案内も無い
//
// 作ったページは最後に消します。
// 使い方: WCMS_BASE=https://localhost:9443 node verify-file-view-photo.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, writeBody } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let id = '';
  // jpegOf はブラウザの canvas で w×h の写真を作ります。
  const jpegOf = async (w, h) => Buffer.from(await page.evaluate(async ([w, h]) => {
    const c = document.createElement('canvas');
    c.width = w; c.height = h;
    const g = c.getContext('2d');
    g.fillStyle = '#3a6'; g.fillRect(0, 0, w, h);
    g.fillStyle = '#fff'; g.fillRect(w / 4, h / 4, w / 2, h / 2);
    const blob = await new Promise((r) => c.toBlob(r, 'image/jpeg', 0.9));
    return Array.from(new Uint8Array(await blob.arrayBuffer()));
  }, [w, h]));
  try {
    await login(page, BASE);
    id = await makePage(page, '<h1>【E2E】写真のファイル表示</h1><p>x</p>');
    const up = async (name, buf) => {
      const lock = await page.request.post(BASE + '/api/lock?id=' + id, { headers: { Origin: BASE } });
      const token = (await lock.json()).token;
      const r = await page.request.post(BASE + '/api/upload-image', {
        headers: { Origin: BASE, 'X-Lock-Token': token },
        multipart: { page_id: id, image_file: { name, mimeType: 'image/jpeg', buffer: buf } },
      });
      await page.request.post(BASE + '/api/lock/force?id=' + id, { headers: { Origin: BASE } });
      return r.json();
    };
    const tall = await up('【E2E】縦長.jpg', await jpegOf(900, 1200));
    const wide = await up('【E2E】横長.jpg', await jpegOf(1200, 900));
    check('写真を上げられた', !!(tall.id && wide.id), JSON.stringify([tall, wide]));
    await writeBody(page, id, '<h1>【E2E】写真のファイル表示</h1>' +
      '<section data-type="file-view" data-ref="' + id + '-' + tall.id + '"></section>' +
      '<section data-type="file-view" data-ref="' + id + '-' + wide.id + '"></section>');
    await page.goto(BASE + '/' + id, { waitUntil: 'networkidle' });
    await page.waitForFunction(() => [...document.querySelectorAll('#w-editor-content img.file-view-body')].length === 2 &&
      [...document.querySelectorAll('#w-editor-content img.file-view-body')].every((i) => i.complete && i.naturalWidth > 0), null, { timeout: 15000 });
    const r = await page.evaluate(() => [...document.querySelectorAll('#w-editor-content .file-view')].map((w) => {
      const i = w.querySelector('img.file-view-body');
      const a = w.getBoundingClientRect(), b = i.getBoundingClientRect();
      return { fw: a.width, fh: a.height, iw: b.width, ih: b.height, nw: i.naturalWidth, nh: i.naturalHeight, resize: getComputedStyle(w).resize, title: w.title || '' };
    }));
    for (const [n, x] of [['縦長', r[0]], ['横長', r[1]]]) {
      const shown = x.ih / x.iw, real = x.nh / x.nw;
      check('① ' + n + 'の写真が縦横の比を保って出る', Math.abs(shown - real) / real < 0.02, Math.round(x.iw) + '×' + Math.round(x.ih) + '（元 ' + x.nw + '×' + x.nh + '）');
      check('② ' + n + 'の写真の枠の高さが写真に合う', x.fh - x.ih < 30 && x.fh >= x.ih - 1, '枠 ' + Math.round(x.fh) + '・写真 ' + Math.round(x.ih));
      check('③ ' + n + 'の写真の枠に大きさを変えるハンドルも案内も無い', x.resize === 'none' && x.title === '', x.resize + ' ' + JSON.stringify(x.title));
    }
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
