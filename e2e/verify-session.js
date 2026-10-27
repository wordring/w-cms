// セッションの失効の告知を画面で確かめる（2026-10-14・【要求】権限 §1・assets/session.js）。
//
// 利用者:「操作が8時間無ければログアウトにしては？ログアウト数分前に告知するのも良いと思います。」
//
// 期限の決まり（最後の操作から8時間・自動の口では延びない）は Go の試験が見張ります（internal/auth）。
// ここで見るのは画面の側です:
//   ① 本物の GET /api/session が残り（8時間以内）と告知の時間（5分）を返す
//   ② 残りが告知の時間を切ったら、上に帯「あと N 分でログアウトします」が出る
//   ③ 帯の「続ける」は POST /api/session を送り、延びたら帯が消える
//   ④ 切れたら（401）、書きかけが無ければログインへ移る
//   ⑤ 書きかけがあれば移らず、帯で知らせる（ログインし直す口は別のタブ）
//
// ②〜⑤は残りを待てないので、/api/session の答えを**試験のブラウザの中だけ**で差し替えます（page.route）。
// サーバーのセッションには触りません。当て先は自分で作って最後に消します。
//
// 使い方: WCMS_BASE=https://localhost:8443 node verify-session.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ viewport: { width: 1280, height: 860 }, ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(e.message));
  await login(page, BASE);

  let bad = 0;
  const say = (ok, msg) => { console.log((ok ? '  ✓ ' : '  ✗ ') + msg); if (!ok) bad++; };

  const id = await makePage(page, '<h1>【E2E】セッションの告知</h1><p>本文</p>');
  if (!id) { console.log('✗ 当て先を作れません'); await browser.close(); process.exit(1); }
  console.log('当て先: /' + id + '（作って、最後に消します）');

  try {
    // ① 本物の口
    const real = await page.evaluate(async () => {
      const r = await fetch('/api/session', { cache: 'no-store' });
      return { status: r.status, body: await r.json().catch(() => null) };
    });
    const b = real.body || {};
    say(real.status === 200 && b.expires_in > 0 && b.expires_in <= 8 * 3600 && b.warn_before === 300,
      '本物の /api/session は残り（8時間以内）と告知の5分を返す（' + JSON.stringify(b) + '）');

    // ②〜④: 答えを差し替える。state で返すものを切り替える。
    let state = 'near';
    let posted = 0;
    // ページに埋めた最初の答え（#w-boot-data・2026-10-27・internal/cms/boot_data.go）を外して開く——session.js は最初の1回だけ
    // 埋めた本物の答え（残り8時間）を使うので、外さないと下の差し替えが最初に効かない。
    await page.route((u) => /^\/\d{6}$/.test(u.pathname), async (route) => {
      const res = await route.fetch();
      const body = (await res.text()).replace(/<script type="application\/json" id="w-boot-data">[\s\S]*?<\/script>/, '');
      return route.fulfill({ response: res, body });
    });
    await page.route('**/api/session', async (route) => {
      const req = route.request();
      if (req.method() === 'POST') { posted++; state = 'far'; return route.fulfill({ status: 204, body: '' }); }
      if (state === 'gone') return route.fulfill({ status: 401, body: '' });
      const left = state === 'near' ? 120 : 8 * 3600;
      return route.fulfill({ status: 200, contentType: 'application/json',
        body: JSON.stringify({ expires_in: left, warn_before: 300 }) });
    });
    await page.goto(BASE + '/' + id);
    const bar = page.locator('#w-session-bar');
    await bar.waitFor({ state: 'visible', timeout: 8000 }).catch(() => {});
    const text = (await bar.count()) ? (await bar.textContent()) : '';
    say(/あと 2 分でログアウトします/.test(text), '残りが5分を切ると帯が出る（' + text.trim() + '）');

    await page.click('#w-session-bar button');
    await page.waitForFunction(() => !document.getElementById('w-session-bar'), null, { timeout: 8000 }).catch(() => {});
    say(posted === 1, '「続ける」は POST /api/session を1回送る（' + posted + '回）');
    say((await bar.count()) === 0, '延びたら帯が消える');

    // ④ 切れた（書きかけ無し）→ ログインへ。タブに戻ったときに聞き直すので、visibilitychange で聞かせる。
    state = 'gone';
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')));
    await page.waitForURL(/\/login/, { timeout: 8000 }).catch(() => {});
    say(/\/login\?next=/.test(page.url()), '切れたらログインへ移る（' + page.url().replace(BASE, '') + '）');

    // ⑤ 書きかけがあるとき——ログインへ移らず帯で知らせる。
    state = 'near';
    await page.goto(BASE + '/' + id);
    await bar.waitFor({ state: 'visible', timeout: 8000 }).catch(() => {});
    await page.evaluate(() => { window.wcmsHasUnsaved = () => true; }); // 書きかけのある状態を真似る
    state = 'gone';
    await page.evaluate(() => document.dispatchEvent(new Event('visibilitychange')));
    await page.waitForSelector('#w-session-bar.is-ended', { timeout: 8000 }).catch(() => {});
    const ended = page.locator('#w-session-bar.is-ended');
    const endedText = (await ended.count()) ? (await ended.textContent()) : '';
    say(!/\/login/.test(page.url()) && /書きかけはまだ保存されていません/.test(endedText),
      '書きかけがあれば移らず、帯で知らせる（' + endedText.trim().slice(0, 40) + '…）');
    const target = (await ended.count()) ? await ended.locator('a').getAttribute('target') : '';
    say(target === '_blank', 'ログインし直す口は別のタブ（書きかけのタブを残す）');

    await page.unroute('**/api/session');
  } finally {
    // 書きかけの真似を外してから消す（後片付けの道でログインへ飛ばされないように）。
    await page.evaluate(() => { window.wcmsHasUnsaved = () => false; }).catch(() => {});
    if (!/\/login/.test(page.url())) await page.goto(BASE + '/' + id).catch(() => {});
    await deletePage(page, id);
  }

  if (errs.length) { console.log('✗ JSエラー: ' + errs.join(' / ')); bad++; }
  console.log(bad === 0 ? '結果: すべて通りました' : '結果: ' + bad + ' 件の失敗');
  await browser.close();
  process.exit(bad === 0 ? 0 : 1);
})();
