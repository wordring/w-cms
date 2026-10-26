// 写真を小さくする（2026-10-27・internal/cms/photo_shrink.go）を画面から確かめる。
//
// 利用者:「では、添付の中で容量を取るファイル形式を調べ、ワンクリックで容量を小さくする方法を考えましょう」
// →（置き場を聞いて）管理画面でまとめて・写真ごとの「⋯」・これから上げる写真は自動で・長い辺 2000 px。
//
//   ① 長い写真を上げると、保存するときに長い辺が設定の大きさまで縮む
//   ② 「📝 ローカル編集」で大きな写真に上書きする（縮めない口——これで大きな写真を作る）
//   ③ 写真の「⋯」に「🗜 写真を小さくする」が出て、確かめて押すと縮み、ページを開き直さずに絵が描き直る
//   ④ もう一度押すと「もう小さい」と断る
//   ⑤ 管理画面に「🗜 写真を小さくする」と、縮められる写真の数が出る（押さない——全ページの写真を縮めてしまう）
//   ⑥ 閲覧の写真の住所に版の印（?v=）が付き、その住所はブラウザの手元に残せる・印の無い住所は毎回確かめる
//
// 作ったページは最後に消します。設定 photo_shrink が無いサーバーでは飛ばします。
// 使い方: WCMS_BASE=https://localhost:9443 node verify-photo-shrink.js
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
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 1000 } });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  const made = [];
  // noisyJPEG はブラウザの canvas で w×h のざらついた写真を作り、中身を返します。
  const noisyJPEG = async (w, h) => Buffer.from(await page.evaluate(async ([w, h]) => {
    const c = document.createElement('canvas');
    c.width = w; c.height = h;
    const g = c.getContext('2d');
    const im = g.createImageData(w, h);
    for (let i = 0; i < im.data.length; i += 4) {
      im.data[i] = (i / 4 % w) * 255 / w ^ (Math.random() * 64);
      im.data[i + 1] = Math.floor(i / 4 / w) * 255 / h;
      im.data[i + 2] = Math.random() * 255;
      im.data[i + 3] = 255;
    }
    g.putImageData(im, 0, 0);
    const blob = await new Promise((r) => c.toBlob(r, 'image/jpeg', 0.92));
    return Array.from(new Uint8Array(await blob.arrayBuffer()));
  }, [w, h]));
  // dimsOf は住所の写真の大きさ（画素）を読みます（手元の写しを使わない）。
  const dimsOf = (url) => page.evaluate(async (url) => {
    const b = await (await fetch(url, { cache: 'no-store' })).blob();
    const bm = await createImageBitmap(b);
    return { w: bm.width, h: bm.height, size: b.size };
  }, url);
  try {
    await login(page, BASE);
    const schema = await page.request.get(BASE + '/api/admin/photo-shrink');
    const conf = await schema.json().catch(() => ({}));
    if (!conf.enabled) {
      console.log('設定 photo_shrink が無いので飛ばします');
      process.exitCode = 0;
      return;
    }
    const long = conf.long_side;
    const id = await makePage(page, '<h1>【E2E】写真を小さくする</h1><p>x</p>');
    made.push(id);
    const big = await noisyJPEG(Math.round(long * 1.5), Math.round(long * 0.9));

    // ①
    const lock = await page.request.post(BASE + '/api/lock?id=' + id, { headers: { Origin: BASE } });
    const token = (await lock.json()).token;
    const up = await page.request.post(BASE + '/api/upload-image', {
      headers: { Origin: BASE, 'X-Lock-Token': token },
      multipart: { page_id: id, image_file: { name: '【E2E】現場.jpg', mimeType: 'image/jpeg', buffer: big } },
    });
    await page.request.post(BASE + '/api/lock/force?id=' + id, { headers: { Origin: BASE } });
    const u = await up.json().catch(() => ({}));
    check('写真を上げられた', up.ok() && !!u.src, JSON.stringify(u));
    await page.goto(BASE + '/' + id);
    const d1 = await dimsOf(u.src);
    check('① 上げた写真は長い辺が ' + long + ' px に縮む', Math.max(d1.w, d1.h) === long, JSON.stringify(d1));

    // ②
    const ref = id + '-' + u.id;
    const st = await page.request.post(BASE + '/api/local-edit/start', { headers: { Origin: BASE }, data: { ref } });
    const link = (await st.json()).link || '';
    const leToken = link.split('#')[1] || '';
    const get = await page.request.get(BASE + '/api/local-edit/file?token=' + leToken);
    const put = await page.request.put(BASE + '/api/local-edit/file?token=' + leToken, {
      headers: { Origin: BASE, 'If-Match': get.headers()['etag'] || '' }, data: big,
    });
    const d2 = await dimsOf(u.src);
    check('② ローカル編集で大きな写真に上書きできた', put.status() === 204 && Math.max(d2.w, d2.h) > long, put.status() + ' ' + (await put.text()).slice(0, 200) + ' ' + JSON.stringify(d2) + ' ' + JSON.stringify(get.headers()['x-wcms-writable']));

    // ③
    await writeBody(page, id, '<h1>【E2E】写真を小さくする</h1><p><img src="' + u.src + '" alt="【E2E】現場.jpg"></p>');
    await page.goto(BASE + '/' + id);
    const img = page.locator('#w-editor-content img[src^="' + u.src + '"]');
    await img.waitFor({ timeout: 10000 });
    await page.waitForFunction((src) => { const i = document.querySelector('#w-editor-content img[src^="' + src + '"]'); return i && i.complete && i.naturalWidth > 0; }, u.src);
    const before = await img.evaluate((i) => i.naturalWidth);
    const openMenu = async () => {
      await img.hover();
      await page.locator('#w-img-menu.active').click({ timeout: 5000 });
      await page.locator('#w-file-menu.active').waitFor({ timeout: 5000 });
    };
    await openMenu();
    const items = await page.locator('#w-file-menu.active .file-menu-item').allTextContents();
    check('③ 写真の「⋯」に「🗜 写真を小さくする」', items.some((t) => t.includes('写真を小さくする')), items.join('｜'));
    await page.locator('#w-file-menu.active .file-menu-item', { hasText: '写真を小さくする' }).click();
    await page.locator('dialog.w-confirm .w-confirm-yes').click({ timeout: 5000 });
    const note = await page.waitForFunction(() => /写真を小さくしました/.test(document.body.innerText), null, { timeout: 15000 }).then(() => true).catch(() => false);
    check('③ 「写真を小さくしました」と出る', note);
    const d3 = await dimsOf(u.src);
    check('③ 写真が縮んだ', Math.max(d3.w, d3.h) === long && d3.size < d2.size, JSON.stringify(d3));
    const redrawn = await page.waitForFunction(([src, long]) => {
      const i = document.querySelector('#w-editor-content img[src^="' + src + '"]');
      return i && i.complete && Math.max(i.naturalWidth, i.naturalHeight) === long;
    }, [u.src, long], { timeout: 10000 }).then(() => true).catch(() => false);
    check('③ 開き直さずに絵が描き直る', redrawn, '前の幅 ' + before);

    // ④
    await openMenu();
    await page.locator('#w-file-menu.active .file-menu-item', { hasText: '写真を小さくする' }).click();
    await page.locator('dialog.w-confirm .w-confirm-yes').click({ timeout: 5000 });
    const small = await page.waitForFunction(() => /もう小さい写真です/.test(document.body.innerText), null, { timeout: 15000 }).then(() => true).catch(() => false);
    check('④ もう一度押すと「もう小さい」', small);

    // ⑤
    await page.goto(BASE + '/assets/admin.html');
    await page.waitForFunction(() => /写真|設定 photo_shrink/.test((document.getElementById('ps-msg') || {}).textContent || ''), null, { timeout: 20000 }).catch(() => {});
    const ps = await page.evaluate(() => ({ btn: (document.getElementById('ps-go') || {}).textContent, msg: (document.getElementById('ps-msg') || {}).textContent }));
    check('⑤ 管理画面に「🗜 写真を小さくする」と数', /写真を小さくする/.test(ps.btn || '') && /(縮められる写真: \d+ 枚|縮める写真はありません)/.test(ps.msg || ''), JSON.stringify(ps));

    // ⑥ 閲覧の描き方の写真には版の印（?v=）が付き、その住所はブラウザの手元に残せる・印の無い住所は毎回確かめる（2026-10-27）。
    await page.goto(BASE + '/' + id);
    const shown = await page.locator('#w-editor-content img[src^="' + u.src + '"]').first().getAttribute('src').catch(() => '');
    const ccOf = async (url) => (await page.request.get(BASE + url)).headers()['cache-control'] || '';
    const ccV = shown && shown.includes('?v=') ? await ccOf(shown) : '';
    const ccPlain = await ccOf(u.src);
    check('⑥ 閲覧の写真に版の印が付き、手元に残せる', /\?v=/.test(shown || '') && /private/.test(ccV) && /immutable/.test(ccV), shown + ' ' + ccV);
    check('⑥ 印の無い住所は毎回確かめる', /private/.test(ccPlain) && /no-cache/.test(ccPlain), ccPlain);

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
