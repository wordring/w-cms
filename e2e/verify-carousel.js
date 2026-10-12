// カルーセルと画像の拡大表示（2026-10-07——internal/cms/vocab.go の "carousel"・assets/app.js の「カルーセル」「画像の拡大表示」）を
// 画面から確かめる。
//
// 利用者:「カルーセルを入れることは出来ますか？」→ 形は「『カルーセル』ブロックを挿す」・画像を押したときの拡大表示も「する」。
//
//   閲覧 ① 幅が空いている限り並べて見せる（2026-10-12 利用者:「見せる領域が空いている限り次の画像も見せるように並べて見せて
//           ください」——それまでは1枚ずつ）・全部見えていれば送る帯は出さない
//        ② 狭い画面では入りきらない分を横に巻く——「‹ 1 / 3 ›」で送る・端では押せない・横に巻くと数も変わる
//        ③ 画像を押すと拡大表示——ページの画像を数える（リンクの中の画像は数えない）・→ ← で前後・Esc で閉じると、
//           最後に見ていた枚がカルーセルに見える・外側を押しても閉じる
//   編集 ④ 帯（🎠 カルーセル）が出て送る道具は消える・本文の写しに帯や印が混ざらない・画像を押しても拡大しない
//        ⑤ 「⬇ すぐ下の画像を入れる」——画像だけの段落が続くところまで中へ移す
//        ⑥ 「🖼 画像を足す」——選んだ画像を末尾へ
//        ⑦ スラッシュメニューの「基本」でファイル表示のすぐ下に「カルーセル」——押すと空のカルーセル（帯つき）
//        ⑧ 保存した本文はカルーセルの節と中の画像だけ（帯・印なし）→ 閲覧へ戻ると並べて見せる（入りきらなければ送る帯）
//
// ページは自分で作り、最後に消します。本物の置き場には書きません。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-carousel.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 1100 } });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let id = '';
  // png は w×h の単色の PNG を、ページの中の canvas で作って base64 で返す。
  const png = (w, h, color) => page.evaluate(async (a) => {
    const c = document.createElement('canvas');
    c.width = a.w; c.height = a.h;
    const g = c.getContext('2d');
    g.fillStyle = a.color; g.fillRect(0, 0, a.w, a.h);
    const blob = await new Promise((r) => c.toBlob(r, 'image/png'));
    const b = new Uint8Array(await blob.arrayBuffer());
    let s = ''; b.forEach((x) => { s += String.fromCharCode(x); });
    return btoa(s);
  }, { w, h, color });
  // upload は画像をこのページの添付にして src を返す（編集ロックを取って・放して）。
  const upload = (b64, name) => page.evaluate(async (a) => {
    const lr = await fetch('/api/lock?id=' + a.id, { method: 'POST' });
    const lj = await lr.json().catch(() => ({}));
    const bin = Uint8Array.from(atob(a.b64), (ch) => ch.charCodeAt(0));
    const fd = new FormData();
    fd.append('page_id', a.id);
    fd.append('image_file', new File([bin], a.name, { type: 'image/png' }));
    const r = await fetch('/api/upload-image', { method: 'POST', body: fd, headers: { 'X-Lock-Token': lj.token || '' } });
    const d = await r.json().catch(() => ({}));
    await fetch('/api/unlock?id=' + a.id + '&token=' + encodeURIComponent(lj.token || ''), { method: 'POST' });
    return d.src || '';
  }, { id, b64, name });
  // carState は閲覧のカルーセル——見えている枚（帯の中に真ん中がある）・1列か・送る帯（出ているか・数・端で押せないか）。
  const carState = () => page.evaluate(() => {
    const car = document.querySelector('#w-editor-content section[data-type="carousel"]');
    const slides = Array.from(car.children).filter((c) => !c.classList.contains('vocab-chrome'));
    const box = car.getBoundingClientRect();
    const shown = slides.filter((s) => { const r = s.getBoundingClientRect(); const m = r.left + r.width / 2; return m >= box.left - 1 && m <= box.right + 1; })
      .map((s) => (s.querySelector('img') || {}).alt);
    const tops = slides.map((s) => Math.round(s.getBoundingClientRect().top + s.getBoundingClientRect().height / 2));
    const nav = car.nextElementSibling && car.nextElementSibling.classList.contains('car-nav') ? car.nextElementSibling : null;
    const shownNav = !!nav && !nav.hidden && getComputedStyle(nav).display !== 'none';
    return { shown, oneRow: Math.max(...tops) - Math.min(...tops) <= 2, nav: shownNav, count: nav ? nav.querySelector('.car-count').textContent : '',
      prevDisabled: !!nav && nav.querySelector('.car-prev').disabled, nextDisabled: !!nav && nav.querySelector('.car-next').disabled, n: slides.length };
  });
  const lb = () => page.evaluate(() => {
    const box = document.getElementById('w-lightbox');
    if (!box || box.classList.contains('is-hidden')) return null;
    return { count: box.querySelector('.lb-count').textContent, alt: box.querySelector('.lb-img').alt };
  });
  try {
    await login(page, BASE);
    id = await makePage(page, '<h1>【E2E】カルーセル</h1><p>x</p>');
    const src = {};
    for (const [k, w, h, c] of [['a', 160, 80, '#ef4444'], ['b', 160, 240, '#22c55e'], ['c', 160, 120, '#3b82f6'], ['d', 100, 100, '#eab308'], ['e', 100, 100, '#a855f7']]) {
      src[k] = await upload(await png(w, h, c), k + '.png');
    }
    check('画像を添付できた', Object.values(src).every((s) => /^\/\d{6}\//.test(s)), JSON.stringify(src));
    const im = (k, alt) => '<p><img src="' + src[k] + '" alt="' + (alt || k + '.png') + '"/></p>';
    const body = '<h1>【E2E】カルーセル</h1><p>前の段落</p><section data-type="carousel">' + im('a') + im('b') + im('c') + '</section>' +
      im('d') + im('e') + '<p>後の段落</p><p><a href="/000000"><img src="' + src.a + '" alt="リンクの画像"/></a></p>';
    await page.evaluate(async (a) => {
      const lr = await fetch('/api/lock?id=' + a.id, { method: 'POST' });
      const lj = await lr.json().catch(() => ({}));
      await fetch('/api/save', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ page_id: a.id, html: a.html, token: lj.token || '' }) });
      await fetch('/api/unlock?id=' + a.id + '&token=' + encodeURIComponent(lj.token || ''), { method: 'POST' });
    }, { id, html: body });

    // ── 閲覧 ──（2026-10-12 から並べて見せる——利用者:「見せる領域が空いている限り次の画像も見せるように並べて見せてください」）
    await page.goto(BASE + '/' + id);
    await page.waitForSelector('#w-editor-content .car-nav', { state: 'attached', timeout: 10000 }).catch(() => {});
    await page.waitForFunction(() => Array.from(document.querySelectorAll('#w-editor-content img')).every((i) => i.complete), null, { timeout: 10000 }).catch(() => {});
    await sleep(300);
    const s1 = await carState();
    check('① 幅が空いている限り並べて見せる（3枚とも1列に見える・送る帯は出さない）', JSON.stringify(s1.shown) === '["a.png","b.png","c.png"]' &&
      s1.oneRow && !s1.nav, JSON.stringify(s1));

    // ② 狭い画面では入りきらない分を横に巻く——「‹ › 」で送り、数は見えている枚。
    await page.setViewportSize({ width: 375, height: 900 });
    await sleep(500);
    const n1 = await carState();
    check('② 入りきらないときは送る帯が出る（1 から・始めは ‹ が押せない）', n1.nav && /^1(–2)? \/ 3$/.test(n1.count) && n1.prevDisabled && !n1.nextDisabled, JSON.stringify(n1));
    await page.click('#w-editor-content .car-next');
    await sleep(700);
    const n2 = await carState();
    check('② › で次の枚へ巻く', n2.count !== n1.count && !n2.count.startsWith('1') && !n2.prevDisabled, JSON.stringify(n2));
    for (let k = 0; k < 3; k++) { if (!(await carState()).nextDisabled) { await page.click('#w-editor-content .car-next'); await sleep(700); } }
    const n3 = await carState();
    check('② 端まで巻くと › は押せない（最後の枚が見える）', n3.nextDisabled && /3 \/ 3$/.test(n3.count) && n3.shown.includes('c.png'), JSON.stringify(n3));
    await page.click('#w-editor-content .car-prev');
    await sleep(700);
    const n4 = await carState();
    check('② ‹ で戻る', !n4.nextDisabled && n4.count !== n3.count, JSON.stringify(n4));
    await page.evaluate(() => { const car = document.querySelector('#w-editor-content section[data-type="carousel"]'); car.scrollLeft = 0; });
    await sleep(400);
    check('② 横に払う（帯を横に巻く）と数も変わる', (await carState()).count === n1.count);

    // ③ 拡大表示。
    await page.click('#w-editor-content section[data-type="carousel"] img[alt="a.png"]');
    const l1 = await lb();
    check('③ 画像を押すと拡大（ページの画像5枚・リンクの中は数えない）', l1 && l1.count === '1 / 5' && l1.alt === 'a.png', JSON.stringify(l1));
    await page.keyboard.press('ArrowRight');
    const l2 = await lb();
    await page.keyboard.press('ArrowRight');
    await page.keyboard.press('ArrowRight');
    const l4 = await lb();
    await page.keyboard.press('ArrowLeft');
    const l3 = await lb();
    check('③ → ← で前後', l2 && l2.alt === 'b.png' && l4 && l4.alt === 'd.png' && l3 && l3.count === '3 / 5' && l3.alt === 'c.png',
      JSON.stringify([l2, l4, l3]));
    await page.keyboard.press('Escape');
    await sleep(300);
    const s5 = await carState();
    check('③ Esc で閉じると、最後に見ていた枚がカルーセルに見える', (await lb()) === null && s5.shown.includes('c.png') && /3 \/ 3$/.test(s5.count), JSON.stringify(s5));
    await page.setViewportSize({ width: 1280, height: 1100 });
    await sleep(400);
    await page.click('#w-editor-content img[alt="d.png"]');
    const l5 = await lb();
    await page.mouse.click(8, 500); // 外側
    check('③ カルーセルの外の画像からも開く・外側を押すと閉じる', l5 && l5.count === '4 / 5' && (await lb()) === null, JSON.stringify(l5));

    // ── 編集 ──
    await page.goto(BASE + '/' + id + '?edit=true');
    await page.waitForSelector('#w-editor-content .car-edit-bar', { timeout: 10000 }).catch(() => {});
    const e1 = await page.evaluate(() => ({
      bar: !!document.querySelector('#w-editor-content .car-edit-bar'),
      nav: !!document.querySelector('#w-editor-content .car-nav'),
      preview: document.getElementById('w-html-preview').value,
    }));
    check('④ 編集では帯が出て、送る道具は消える', e1.bar && !e1.nav, JSON.stringify({ bar: e1.bar, nav: e1.nav }));
    // 編集では節にブロック ID（data-id）が振られ、写しは属性を名前順に並べる（data-id が先）。
    check('④ 本文の写しに帯や印が混ざらない', /<section [^>]*data-type="carousel"[^>]*>/.test(e1.preview) && !/car-|vocab-chrome|🎠/.test(e1.preview),
      (e1.preview.match(/<section [^>]*data-type="carousel"[\s\S]{0,300}/) || [''])[0]);
    await page.click('#w-editor-content section[data-type="carousel"] img[alt="a.png"]');
    check('④ 編集では画像を押しても拡大しない', (await lb()) === null);

    await page.click('#w-editor-content .car-take');
    await sleep(300);
    const e2 = await page.evaluate(() => {
      const car = document.querySelector('#w-editor-content section[data-type="carousel"]');
      const alts = Array.from(car.querySelectorAll('img')).map((i) => i.alt);
      const top = Array.from(document.querySelectorAll('#w-editor-content .editor-block > .block-content > *')).map((el) => el.textContent.trim() || (el.querySelector('img') || {}).alt || el.tagName);
      return { alts, top };
    });
    check('⑤ すぐ下の画像だけを中へ移す（後の段落で止まる）', JSON.stringify(e2.alts) === '["a.png","b.png","c.png","d.png","e.png"]' &&
      !e2.top.includes('d.png') && e2.top.includes('後の段落'), JSON.stringify(e2));

    const fpng = Buffer.from(await png(120, 60, '#14b8a6'), 'base64');
    const [chooser] = await Promise.all([page.waitForEvent('filechooser', { timeout: 5000 }), page.click('#w-editor-content .car-add')]);
    await chooser.setFiles({ name: 'f.png', mimeType: 'image/png', buffer: fpng });
    await page.waitForFunction(() => document.querySelectorAll('#w-editor-content section[data-type="carousel"] img').length === 6, null, { timeout: 10000 }).catch(() => {});
    const e3 = await page.evaluate(() => Array.from(document.querySelectorAll('#w-editor-content section[data-type="carousel"] img')).map((i) => i.alt));
    check('⑥ 「画像を足す」で末尾へ', JSON.stringify(e3) === '["a.png","b.png","c.png","d.png","e.png","f.png"]', JSON.stringify(e3));

    // ⑦ スラッシュメニュー。
    await page.click('#w-editor-content p:not(:has(img))', { timeout: 5000 }).catch(() => {});
    const para = page.locator('#w-editor-content .editor-block > .block-content > p', { hasText: '後の段落' });
    await para.click();
    await page.keyboard.press('End');
    await page.keyboard.press('Enter');
    await page.keyboard.type('/');
    await sleep(600);
    const menu = await page.evaluate(() => {
      let cat = '';
      const list = [];
      document.querySelectorAll('#w-slash-menu > *').forEach((e) => {
        if (e.classList.contains('slash-menu-group')) cat = e.textContent.trim();
        else list.push(cat + ': ' + e.textContent.replace(/^\S+\s*/, '').trim());
      });
      return list;
    });
    const fi = menu.indexOf('基本: ファイル表示');
    check('⑦ 基本のファイル表示のすぐ下に「カルーセル」', fi >= 0 && menu[fi + 1] === '基本: カルーセル', menu.slice(Math.max(0, fi - 1), fi + 3).join('・'));
    await page.locator('#w-slash-menu .slash-menu-item', { hasText: 'カルーセル' }).first().click({ timeout: 5000 });
    await sleep(500);
    const e4 = await page.evaluate(() => Array.from(document.querySelectorAll('#w-editor-content section[data-type="carousel"]')).map((c) =>
      ({ imgs: c.querySelectorAll('img').length, bar: !!c.querySelector(':scope > .car-edit-bar') })));
    check('⑦ 押すと空のカルーセル（帯つき）', e4.length === 2 && e4[1].imgs === 0 && e4[1].bar, JSON.stringify(e4));

    // ⑧ 保存した本文。
    await sleep(3000);
    const saved = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).text(), id);
    const cars = saved.match(/<section [^>]*data-type="carousel"[^>]*>[\s\S]*?<\/section>/g) || [];
    check('⑧ 保存した本文はカルーセルの節と中の画像だけ', cars.length === 2 && (cars[0].match(/<img /g) || []).length === 6 &&
      !/car-|vocab-chrome|🎠|class=/.test(cars.join('')), cars.map((c) => c.slice(0, 120)).join(' | '));
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForFunction(() => !document.body.hasAttribute('edit-mode'), null, { timeout: 10000 }).catch(() => {});
    await sleep(500);
    const s6 = await carState();
    const bar = await page.evaluate(() => !!document.querySelector('#w-editor-content .car-edit-bar'));
    check('⑧ 閲覧へ戻ると並べて見せる（入りきらない分は送る帯・1 から）・編集の帯は消える', s6.oneRow && s6.shown[0] === 'a.png' &&
      (s6.nav ? /^1(–\d+)? \/ 6$/.test(s6.count) : s6.shown.length === 6) && !bar, JSON.stringify(s6));
  } finally {
    // 編集モードのまま消すと断られることがある（エディタが編集権を持ったまま）——ページから離れてから消す。
    await page.goto(BASE + '/000000').catch(() => {});
    await deletePage(page, id);
    const st = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).status, id);
    check('作ったページを消した', st === 404, String(st));
  }
  check('ページのエラーが無い', errs.length === 0, errs.join(' / '));
  await browser.close();
  console.log(fails ? `✗ ${fails} 件` : '✓ すべて合格');
  process.exit(fails ? 1 : 0);
})();
