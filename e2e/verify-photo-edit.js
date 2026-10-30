// 画像の拡大表示の部分の拡大・簡易編集と、画像の下のコメント（2026-10-30——assets/app.js の「画像の拡大表示」「画像の下のコメント」・
// internal/cms/photo_edit.go）を画面から確かめる。
//
// 利用者:「拡大表示はクリックやタッチした部分を中心に押すたびに少しずつ拡大するようなイメージです。」
// 「簡易編集機能は、90度ずつの回転、切り抜き、画像を明るくするが欲しいです」
// 「拡大表示は保存が必要なく、編集機能は無条件に保存が必要です。したがって編集時のアンドゥも必要かと思われます。」
// 「画像の下にコメントを書く場所は確保できないでしょうか？これは、カルーセルを使う場合も入ってほしいです。」
//
//   パソコンの幅
//     ① 押すたびに少しずつ拡大し、押した所が真ん中へ寄る・保存はしない・なぞると動く（なぞったあとは拡大しない）・元の大きさへ戻す
//     ② ⟳ で回して保存（本文の画像も替わる・開き直しても回っている）・↶ で戻す（戻せないときは押せない）
//     ③ ☀ で明るくなる・↶ で戻す
//     ④ ✂ でなぞった範囲を切り抜いて保存（拡大していても範囲が合う）・↶ で戻す
//     ⑤ 書ける形で「コメントを書く」を押して書くと、<p><img>コメント</p> で保存され、閲覧で画像の下に出る・拡大表示にも出る
//     ⑥ カルーセルの中も同じ（コメントの幅は画像の幅まで）
//   スマホの幅
//     ⑦ タッチで拡大・道具が画面に収まる・横にはみ出さない・回して戻せる
//
// ページは自分で作り、最後に消します。本物の置き場には書きません。
// 使い方: WCMS_BASE=https://localhost:9443 NODE_TLS_REJECT_UNAUTHORIZED=0 node verify-photo-edit.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, saveNow } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

// makeJpeg は左上が赤・右上が緑・左下が青・右下が暗い灰色の JPEG を、ページの中の canvas で作って添付にし、src を返す。
const uploadQuad = (page, id, w, h, name) => page.evaluate(async (a) => {
  const c = document.createElement('canvas');
  c.width = a.w; c.height = a.h;
  const g = c.getContext('2d');
  [['#f00', 0, 0], ['#0f0', 1, 0], ['#00f', 0, 1], ['#404040', 1, 1]].forEach(([col, x, y]) => {
    g.fillStyle = col; g.fillRect(x * a.w / 2, y * a.h / 2, a.w / 2, a.h / 2);
  });
  const blob = await new Promise((r) => c.toBlob(r, 'image/jpeg', 0.95));
  const fd = new FormData();
  fd.append('page_id', a.id);
  fd.append('image_file', new File([blob], a.name, { type: 'image/jpeg' }));
  const r = await fetch('/api/upload-image', { method: 'POST', body: fd });
  const d = await r.json().catch(() => ({}));
  return d.src || '';
}, { id, w, h, name });

const writeBody = (page, id, body) => page.evaluate(async (a) => {
  const lr = await fetch('/api/lock?id=' + a.id, { method: 'POST' });
  const lj = await lr.json();
  await fetch('/api/save?id=' + a.id, { method: 'POST', headers: { 'Content-Type': 'text/html', 'X-Lock-Token': lj.token }, body: a.body });
  await fetch('/api/unlock?id=' + a.id + '&token=' + encodeURIComponent(lj.token), { method: 'POST' });
}, { id, body });

// lbState は拡大表示の絵の様子（画素の大きさ・拡大・絵の場所）。
const lbState = (page) => page.evaluate(() => {
  const img = document.querySelector('#w-lightbox .lb-img');
  const r = img.getBoundingClientRect();
  return { nw: img.naturalWidth, nh: img.naturalHeight, tf: img.style.transform, l: r.left, t: r.top, w: r.width, h: r.height, src: img.getAttribute('src') };
});
// pixelAt は拡大表示の絵の、割合 (fx, fy) の色を読む。
const pixelAt = (page, fx, fy) => page.evaluate(async (a) => {
  const img = document.querySelector('#w-lightbox .lb-img');
  if (!img.complete) await new Promise((r) => { img.onload = r; });
  const c = document.createElement('canvas');
  c.width = img.naturalWidth; c.height = img.naturalHeight;
  const g = c.getContext('2d');
  g.drawImage(img, 0, 0);
  return Array.from(g.getImageData(Math.floor(a.fx * c.width), Math.floor(a.fy * c.height), 1, 1).data).slice(0, 3);
}, { fx, fy });
const isRed = ([r, g, b]) => r > 180 && g < 80 && b < 80;
const waitSaid = (page, text) => page.waitForFunction((t) => (document.querySelector('#w-lightbox .lb-msg').textContent || '').includes(t), text, { timeout: 15000 });
const waitLoaded = (page) => page.waitForFunction(() => { const i = document.querySelector('#w-lightbox .lb-img'); return i.complete && i.naturalWidth > 0; }, null, { timeout: 8000 });
const openBox = async (page, sel) => {
  await page.locator(sel).first().click();
  await page.waitForSelector('#w-lightbox:not(.is-hidden)');
  await waitLoaded(page);
  await page.waitForTimeout(400);
};
const opAndWait = async (page, cls, said) => {
  await page.locator('#w-lightbox ' + cls).click();
  await waitSaid(page, said);
  await page.waitForTimeout(100);
  await waitLoaded(page);
};
const setMode = async (page, edit) => {
  await page.evaluate((v) => { const t = document.getElementById('w-mode-toggle'); if (t.checked !== v) { t.checked = v; t.dispatchEvent(new Event('change', { bubbles: true })); } }, edit);
  await page.waitForFunction((v) => document.body.hasAttribute('edit-mode') === v, edit, { timeout: 8000 });
  await page.waitForTimeout(600);
};

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let id = '';
  try {
    await login(page, BASE);
    id = await makePage(page, '<h1>【E2E】画像の編集</h1><p>本文</p>');
    const src1 = await uploadQuad(page, id, 800, 600, 'quad1.jpg');
    const src2 = await uploadQuad(page, id, 640, 480, 'quad2.jpg');
    const src3 = await uploadQuad(page, id, 500, 400, 'quad3.jpg');
    check('画像を上げた', !!(src1 && src2 && src3), src1);
    await writeBody(page, id, `<h1>【E2E】画像の編集</h1><p><img src="${src1}" alt="quad1"></p>` +
      `<section data-type="carousel"><p><img src="${src2}" alt="quad2"></p><p><img src="${src3}" alt="quad3"></p></section>`);
    await page.goto(BASE + '/' + id);
    await page.waitForTimeout(800);

    const posts = [];
    page.on('request', (r) => { if (r.url().includes('/api/photo-edit') && r.method() === 'POST') posts.push(r.url()); });

    // ① 部分の拡大
    await openBox(page, '#w-editor-content > .editor-block p img, #w-editor-content p img');
    let s = await lbState(page);
    check('初めは拡大していない・元の大きさの道具は出ない', s.tf === '' && !(await page.locator('#w-lightbox .lb-zoom-reset').isVisible()));
    check('✏ 編集が出る', await page.locator('#w-lightbox .lb-edit-toggle').isVisible());
    const atX = s.l + s.w * 0.75, atY = s.t + s.h * 0.75;
    await page.mouse.click(atX, atY);
    await page.waitForTimeout(300);
    const s1 = await lbState(page);
    check('押すと少し拡大する（1.4倍）', /scale\(1\.4\)/.test(s1.tf) && Math.abs(s1.w - s.w * 1.4) < 2, s1.tf);
    // 右下の点は、押すと画面の真ん中へ寄る。
    const ptX = s1.l + s1.w * 0.75, ptY = s1.t + s1.h * 0.75;
    check('押した所が真ん中へ寄る', Math.abs(ptX - 640) < Math.abs(atX - 640) && Math.abs(ptY - 450) < Math.abs(atY - 450), ptX + ',' + ptY);
    await page.mouse.click(640, 450);
    await page.waitForTimeout(300);
    const s2 = await lbState(page);
    check('もう一度押すと、さらに拡大する', /scale\(1\.96\)/.test(s2.tf), s2.tf);
    await page.mouse.move(640, 450);
    await page.mouse.down();
    await page.mouse.move(600, 420, { steps: 5 });
    await page.mouse.up();
    await page.waitForTimeout(200);
    const s3 = await lbState(page);
    check('なぞると動く・なぞったあとは拡大しない', /scale\(1\.96\)/.test(s3.tf) && Math.abs(s3.l - s2.l) > 10, s3.tf);
    await page.locator('#w-lightbox .lb-zoom-out').click();
    await page.waitForTimeout(250);
    check('－で1段小さくなる', /scale\(1\.4\)/.test((await lbState(page)).tf), (await lbState(page)).tf);
    await page.locator('#w-lightbox .lb-zoom-reset').click();
    await page.waitForTimeout(250);
    const s4 = await lbState(page);
    check('元の大きさへ戻す', s4.tf === '' && Math.abs(s4.w - s.w) < 1);
    check('部分の拡大は保存しない', posts.length === 0, String(posts.length));

    // ② 回転と元に戻す
    await page.locator('#w-lightbox .lb-edit-toggle').click();
    check('編集の道具が出る・初めは戻せない', await page.locator('#w-lightbox .lb-rotate-right').isVisible() && await page.locator('#w-lightbox .lb-undo').isDisabled());
    await opAndWait(page, '.lb-rotate-right', '保存しました');
    const r1 = await lbState(page);
    check('右へ回すと縦横が入れ替わる', r1.nw === 600 && r1.nh === 800, r1.nw + '×' + r1.nh);
    check('右へ回すと左上の赤が右上へ行く', isRed(await pixelAt(page, 0.75, 0.25)));
    check('本文の画像も新しい版の印になる', await page.evaluate((u) => {
      const i = Array.from(document.querySelectorAll('#w-editor-content img')).find((x) => x.getAttribute('src').startsWith(u));
      return i && i.getAttribute('src') !== document.querySelector('#w-lightbox .lb-img').getAttribute('src').replace('?v=', '?view=1&v=') ? 'ちがう' : 'おなじ';
    }, src1.split('?')[0]) === 'おなじ');
    check('戻せるようになる', await page.locator('#w-lightbox .lb-undo').isEnabled());
    await page.goto(BASE + '/' + id);
    await page.waitForTimeout(800);
    await openBox(page, '#w-editor-content p img');
    const r2 = await lbState(page);
    check('開き直しても回っている（保存された）', r2.nw === 600 && r2.nh === 800, r2.nw + '×' + r2.nh);
    await page.locator('#w-lightbox .lb-edit-toggle').click();
    await page.waitForFunction(() => !document.querySelector('#w-lightbox .lb-undo').disabled, null, { timeout: 5000 });
    await opAndWait(page, '.lb-undo', '戻しました');
    const r3 = await lbState(page);
    check('↶ で元に戻る', r3.nw === 800 && r3.nh === 600 && isRed(await pixelAt(page, 0.25, 0.25)), r3.nw + '×' + r3.nh);
    check('もう戻せない', await page.locator('#w-lightbox .lb-undo').isDisabled());

    // ③ 明るくする
    const dark0 = await pixelAt(page, 0.75, 0.75);
    await opAndWait(page, '.lb-brighten', '保存しました');
    const dark1 = await pixelAt(page, 0.75, 0.75);
    check('☀ で暗い所が明るくなる', dark1[0] > dark0[0] + 5, dark0 + ' → ' + dark1);
    await opAndWait(page, '.lb-undo', '戻しました');
    const dark2 = await pixelAt(page, 0.75, 0.75);
    check('↶ で元の明るさへ', Math.abs(dark2[0] - dark0[0]) <= 1, String(dark2));

    // ④ 切り抜き（右下の4分の1。拡大したまま）
    await page.mouse.click(640, 450);
    await page.waitForTimeout(300);
    await page.locator('#w-lightbox .lb-crop-start').click();
    check('範囲を選ぶまで切り抜けない', await page.locator('#w-lightbox .lb-crop-do').isDisabled());
    const c0 = await lbState(page);
    const fx = (f) => c0.l + c0.w * f, fy = (f) => c0.t + c0.h * f;
    await page.mouse.move(fx(0.55), fy(0.55));
    await page.mouse.down();
    await page.mouse.move(fx(0.75), fy(0.75), { steps: 6 });
    await page.mouse.up();
    check('なぞると切り抜ける', await page.locator('#w-lightbox .lb-crop-do').isEnabled());
    const c1 = await lbState(page);
    check('切り抜きでなぞっても拡大は変わらない', c1.tf === c0.tf, c1.tf);
    await opAndWait(page, '.lb-crop-do', '保存しました');
    const c2 = await lbState(page);
    check('なぞった範囲の大きさになる（拡大していても合う）', Math.abs(c2.nw - 160) <= 3 && Math.abs(c2.nh - 120) <= 3, c2.nw + '×' + c2.nh);
    const col = await pixelAt(page, 0.5, 0.5);
    check('切り抜いた絵は右下の色', Math.abs(col[0] - 64) < 20 && Math.abs(col[1] - 64) < 20, String(col));
    await opAndWait(page, '.lb-undo', '戻しました');
    check('↶ で切り抜きも戻る', (await lbState(page)).nw === 800);
    await page.locator('#w-lightbox .lb-close').click();

    // ⑤ 画像の下のコメント
    await setMode(page, true);
    const hint = await page.evaluate(() => {
      const p = document.querySelector('#w-editor-content p.w-img-nocap');
      return p ? getComputedStyle(p, '::after').content : '';
    });
    check('書ける形で「コメントを書く」の場所が出る', hint.includes('コメントを書く'), hint);
    const boxOf = await page.evaluate(() => {
      const p = document.querySelector('#w-editor-content p.w-img-nocap');
      const i = p.querySelector('img').getBoundingClientRect(), r = p.getBoundingClientRect();
      return { x: r.left + 40, y: i.bottom + (r.bottom - i.bottom) / 2 };
    });
    await page.mouse.click(boxOf.x, boxOf.y);
    await page.keyboard.type('溶接の跡');
    await page.waitForTimeout(200);
    check('書くと案内が消える', await page.evaluate(() => !document.querySelector('#w-editor-content p.w-img-cap:not(.w-img-nocap)') ? false : true));
    // カルーセルの1枚目にも書く。
    const carBox = await page.evaluate(() => {
      const p = document.querySelector('#w-editor-content section[data-type="carousel"] p.w-img-nocap');
      const i = p.querySelector('img').getBoundingClientRect(), r = p.getBoundingClientRect();
      return { x: r.left + 20, y: i.bottom + (r.bottom - i.bottom) / 2 };
    });
    await page.mouse.click(carBox.x, carBox.y);
    await page.keyboard.type('カルーセルの写真についての少し長めのコメントです。画像の幅で折り返します');
    await page.waitForTimeout(200);
    await saveNow(page);
    const saved = await page.evaluate(async (i) => (await (await fetch('/api/load?id=' + i)).text()), id);
    check('本文は <p><img>コメント</p> の形', /<img[^>]*>\s*溶接の跡\s*(<br\s*\/?>)?\s*<\/p>/.test(saved), saved.slice(0, 400));
    check('カルーセルの中も同じ形', /<img[^>]*>\s*カルーセルの写真/.test(saved));
    check('印の class は保存されない', !saved.includes('w-img-'));

    await page.goto(BASE + '/' + id);
    await page.waitForTimeout(800);
    const viewCap = await page.evaluate(() => {
      const ps = Array.from(document.querySelectorAll('#w-editor-content p.w-img-cap'));
      const p = ps.find((x) => x.textContent.includes('溶接'));
      const car = ps.find((x) => x.textContent.includes('カルーセル'));
      const rng = (el) => { const r = document.createRange(); r.selectNodeContents(el); const t = Array.from(el.childNodes).find((n) => n.nodeType === 3 && n.textContent.trim()); r.selectNode(t); return r.getBoundingClientRect(); };
      const iBottom = p.querySelector('img').getBoundingClientRect().bottom;
      const c = rng(p);
      const ci = car.querySelector('img').getBoundingClientRect();
      const cc = rng(car);
      return { under: c.top >= iBottom - 1, carUnder: cc.top >= ci.bottom - 1, carW: Math.round(cc.width), imgW: Math.round(ci.width), hint: getComputedStyle(p, '::after').content };
    });
    check('閲覧で画像の下に出る', viewCap.under, JSON.stringify(viewCap));
    check('カルーセルでも画像の下に出て、画像の幅で折り返す', viewCap.carUnder && viewCap.carW <= viewCap.imgW + 1, JSON.stringify(viewCap));
    await openBox(page, '#w-editor-content p.w-img-cap img');
    check('拡大表示にコメントが出る', (await page.locator('#w-lightbox .lb-caption').innerText()) === '溶接の跡');
    await page.keyboard.press('ArrowRight');
    await page.waitForTimeout(300);
    check('カルーセルの枚のコメントも拡大表示に出る', (await page.locator('#w-lightbox .lb-caption').innerText()).startsWith('カルーセルの写真'));
    await page.keyboard.press('Escape');

    // ⑦ スマホの幅
    const phone = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 375, height: 812 }, hasTouch: true, isMobile: true });
    const pp = await phone.newPage();
    pp.on('pageerror', (e) => errs.push(String(e)));
    await login(pp, BASE);
    await pp.goto(BASE + '/' + id);
    await pp.waitForTimeout(1000);
    await pp.locator('#w-editor-content p.w-img-cap img').first().tap();
    await pp.waitForSelector('#w-lightbox:not(.is-hidden)');
    await waitLoaded(pp);
    await pp.waitForTimeout(500);
    const ps = await lbState(pp);
    await pp.touchscreen.tap(ps.l + ps.w * 0.25, ps.t + ps.h * 0.25);
    await pp.waitForTimeout(400);
    const ps1 = await lbState(pp);
    check('スマホ: タッチで拡大する', /scale\(1\.4\)/.test(ps1.tf), ps1.tf);
    check('スマホ: 押した所が真ん中へ寄る', Math.abs(ps1.l + ps1.w * 0.25 - 187) < Math.abs(ps.l + ps.w * 0.25 - 187));
    await pp.locator('#w-lightbox .lb-zoom-reset').tap();
    await pp.waitForTimeout(300);
    await pp.locator('#w-lightbox .lb-edit-toggle').tap();
    await pp.waitForTimeout(300);
    const layout = await pp.evaluate(() => {
      const vis = Array.from(document.querySelectorAll('#w-lightbox .lb-tools button')).filter((b) => b.offsetParent);
      const rects = vis.map((b) => b.getBoundingClientRect());
      const img = document.querySelector('#w-lightbox .lb-img').getBoundingClientRect();
      return {
        n: vis.length,
        inView: rects.every((r) => r.left >= 0 && r.right <= innerWidth && r.top >= 0 && r.bottom <= innerHeight),
        over: rects.some((r) => r.bottom > img.top + 1),
        scroll: document.documentElement.scrollWidth <= innerWidth + 1,
      };
    });
    check('スマホ: 編集の道具が画面に収まり、絵に重ならない', layout.n >= 6 && layout.inView && !layout.over, JSON.stringify(layout));
    check('スマホ: 横にはみ出さない', layout.scroll);
    await pp.locator('#w-lightbox .lb-rotate-left').tap();
    await waitSaid(pp, '保存しました');
    await pp.waitForTimeout(100);
    await waitLoaded(pp);
    const ps2 = await lbState(pp);
    check('スマホ: 左へ回せる', ps2.nw === 600 && ps2.nh === 800 && isRed(await pixelAt(pp, 0.25, 0.75)), ps2.nw + '×' + ps2.nh);
    await pp.locator('#w-lightbox .lb-undo').tap();
    await waitSaid(pp, '戻しました');
    await pp.waitForTimeout(100);
    await waitLoaded(pp);
    check('スマホ: ↶ で戻せる', (await lbState(pp)).nw === 800);
    await phone.close();

    check('ページのエラーが無い', errs.length === 0, errs.join(' / '));
  } catch (e) {
    check('例外なし: ' + e.message, false);
  } finally {
    if (id) await deletePage(page, id).catch(() => {});
    await browser.close();
  }
  console.log(fails ? '✗ ' + fails + ' 件' : '✓ 全部とおりました');
  process.exit(fails ? 1 : 0);
})();
