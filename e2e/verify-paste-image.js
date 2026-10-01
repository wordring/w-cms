// 画像を貼り付ける（Ctrl+V）を画面から確かめる（2026-10-01・assets/app.js の paste）。
//
// 利用者:「画像をペーストできるようにしたい」。
//
//   ① 編集モードで、画像だけのクリップボードを貼ると、キャレットの段落のすぐ後ろへ絵が入る（添付になる）
//   ② 切り取りの画像の名前「image.png」は、貼った日時の名前（貼り付け …）に付け替わる
//   ③ 保存した本文に絵が残る
//   ④ 文字と画像が一緒に載っているとき（Excel のセルのコピーなど）は横取りしない——絵を足さない
//   ⑤ 閲覧モードでは何もしない
//
// 当て先はトップ直下に自分で作り、最後に消します。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-paste-image.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

// 1x1 の PNG
const PNG_B64 = 'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

// paste は、キャレットを置いた要素へ貼り付けの出来事を送ります（text を渡すと文字も一緒に載せる）。
async function paste(page, selector, text) {
  await page.evaluate(({ selector, b64, text }) => {
    const target = document.querySelector(selector);
    const range = document.createRange();
    range.selectNodeContents(target);
    range.collapse(false);
    const sel = window.getSelection();
    sel.removeAllRanges();
    sel.addRange(range);
    const bin = atob(b64);
    const bytes = new Uint8Array(bin.length);
    for (let i = 0; i < bin.length; i++) bytes[i] = bin.charCodeAt(i);
    const dt = new DataTransfer();
    dt.items.add(new File([bytes], 'image.png', { type: 'image/png' }));
    if (text) dt.setData('text/plain', text);
    target.dispatchEvent(new ClipboardEvent('paste', { clipboardData: dt, bubbles: true, cancelable: true }));
  }, { selector, b64: PNG_B64, text });
}

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let id = '';
  try {
    await login(page, BASE);
    id = await makePage(page, '<h1>【E2E】画像の貼り付け</h1><p>段落A</p><p>段落B</p>');
    check('当て先を作れた', !!id, id);

    // ⑤ 閲覧モードでは何もしない
    await page.goto(BASE + '/' + id);
    await page.waitForSelector('#w-editor-content p');
    await paste(page, '#w-editor-content p');
    await page.waitForTimeout(800);
    check('閲覧モードでは絵を足さない', await page.locator('#w-editor-content img').count() === 0);

    // ① 編集モードで貼る
    await page.goto(BASE + '/' + id + '?edit=true');
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    await page.waitForTimeout(500);
    const paraA = '#w-editor-content .editor-block:nth-of-type(2) p';
    const first = await page.evaluate(() => {
      const ps = [...document.querySelectorAll('#w-editor-content p')];
      return ps.map((p) => p.textContent);
    });
    check('段落が2つある', first.includes('段落A') && first.includes('段落B'), JSON.stringify(first));
    await page.evaluate(() => {
      const p = [...document.querySelectorAll('#w-editor-content p')].find((x) => x.textContent === '段落A');
      p.setAttribute('data-e2e', 'a');
    });
    await paste(page, '#w-editor-content p[data-e2e="a"]');
    const img = page.locator('#w-editor-content img');
    await img.first().waitFor({ timeout: 10000 }).catch(() => {});
    check('貼ると絵が1枚入る', await img.count() === 1);
    const src = (await img.first().getAttribute('src').catch(() => '')) || '';
    check('絵はこのページの添付', new RegExp('^/' + id + '/[0-9a-z]+\\.png$').test(src), src);
    const alt = (await img.first().getAttribute('alt').catch(() => '')) || '';
    check('名前は貼った日時（image.png ではない）', /^貼り付け \d{4}-\d{2}-\d{2} \d{2}\.\d{2}\.\d{2}\.png$/.test(alt), alt);
    const order = await page.evaluate(() => {
      const nodes = [...document.querySelectorAll('#w-editor-content p, #w-editor-content img')];
      return nodes.map((n) => (n.tagName === 'IMG' ? 'IMG' : n.textContent)).filter((x) => x === 'IMG' || x.startsWith('段落'));
    });
    check('段落Aのすぐ後ろに入る', JSON.stringify(order) === JSON.stringify(['段落A', 'IMG', '段落B']), JSON.stringify(order));

    // ④ 文字と一緒なら横取りしない
    await paste(page, '#w-editor-content p[data-e2e="a"]', 'セルの文字');
    await page.waitForTimeout(1500);
    check('文字が一緒に載っているときは絵を足さない', await img.count() === 1, String(await img.count()));

    // ③ 保存した本文に残る
    await page.waitForTimeout(2500); // 自動保存（1.5秒のデバウンス）
    const saved = await page.evaluate(async (pid) => (await (await fetch('/api/load?id=' + pid)).text()), id);
    check('保存した本文に絵が残る', saved.includes('src="' + src + '"'), saved.slice(0, 300));
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
