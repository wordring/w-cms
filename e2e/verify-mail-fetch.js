// 通信箱の「📥 新しいメールを読み込む」を画面から確かめる（2026-10-09・ext/comm/mail/fetch_job.go・assets/app.js の
// fetchNewMail・watchMailFetch・resumeMailFetch）。
//
// 利用者:「新しいメールの読み込みは、ページを閉じても継続するようにしてください」——読み込みはサーバーの裏で続き、画面は
// 様子を見に行くだけになった。
//
//   ① 押すと POST /api/mail/fetch-new で始め、様子を見ているあいだボタンは「読み込み中…（N 通）」で押せない（title で
//      ページを閉じても止まらないと言う）
//   ② 終わると「受信 N 通・送信 M 通を読み込みました」と知らせ、ボタンが戻る
//   ③ 開いたときに読み込みが動いていれば、押さなくてもその続きを見る（閉じたあいだも続いていた回）
//   ④ サインインしていない（409）なら理由を知らせ、ボタンは押せるまま
//
// ⚠ **本物のメールは読みません**——2つの口（/api/mail/fetch-new・/api/mail/fetch-new/status）は試験のブラウザの中で止めて
//    答えを返します。開くのは本物の通信箱ですが、読むだけで何も書きません。メールの拡張が無い組・通信箱が無ければ飛ばします。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-mail-fetch.js
const { chromium } = require('playwright');
const { login, findMailbox } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const status = (run, running, inbox, sent, extra = {}) => ({
  run, running, started_at: '2026-10-09T10:00:00+09:00', finished_at: running ? '' : '2026-10-09T10:01:00+09:00',
  folders: { 受信: { imported: inbox, duplicate: 0, failed: 0 }, 送信: { imported: sent, duplicate: 0, failed: 0 } }, ...extra,
});

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  // 口の答え（試験が順に差し替える）。
  let current = null; // GET status の答え
  let started = 0;
  let startReply = () => ({ status: 200, body: { success: true, started: true, status: status(7, true, 0, 0) } });
  await page.route('**/api/mail/fetch-new/status', (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ success: true, status: current }) }));
  await page.route('**/api/mail/fetch-new', (route) => {
    if (route.request().method() !== 'POST') return route.continue();
    started++;
    const r = startReply();
    if (r.body.status) current = r.body.status;
    return route.fulfill({ status: r.status, contentType: 'application/json', body: JSON.stringify(r.body) });
  });
  const btn = page.locator('#w-mail-fetch');
  const toastText = () => page.locator('#w-toast-host .toast').allTextContents().then((a) => a.join(' | '));
  try {
    await login(page, BASE);
    const box = await findMailbox(page);
    const ext = await page.evaluate(async () => (await (await fetch('/api/tag-schema')).json()).extensions || []).catch(() => []);
    if (!box || !JSON.stringify(ext).includes('comm/mail')) {
      console.log('通信箱かメールの拡張が無いので飛ばします');
      await browser.close();
      process.exit(0);
    }
    // ①②
    await page.goto(BASE + '/' + box);
    await btn.waitFor({ timeout: 10000 });
    check('開いたとき、動いていなければボタンは押せる', await btn.isEnabled() && (await btn.textContent()).includes('新しいメールを読み込む'));
    await btn.click();
    current = status(7, true, 12, 0);
    await page.waitForFunction(() => /読み込み中…（12 通）/.test(document.getElementById('w-mail-fetch').textContent), null, { timeout: 8000 }).catch(() => {});
    check('① 読み込み中はボタンに数が出て、押せない', (await btn.textContent()).includes('読み込み中…（12 通）') && !(await btn.isEnabled()),
      await btn.textContent());
    check('① ページを閉じても止まらないと言う', ((await btn.getAttribute('title')) || '').includes('ページを閉じても止まりません'));
    check('① 始める口を1回だけ呼んだ', started === 1, String(started));
    current = status(7, false, 30, 2);
    await page.waitForFunction(() => /新しいメールを読み込む/.test(document.getElementById('w-mail-fetch').textContent), null, { timeout: 8000 }).catch(() => {});
    check('② 終わるとボタンが戻る', await btn.isEnabled() && (await btn.textContent()).includes('新しいメールを読み込む'));
    check('② 読み込んだ数を知らせる', (await toastText()).includes('受信 30 通・送信 2 通を読み込みました'), await toastText());

    // ③ 開いたときに動いていれば、押さなくても続きを見る。
    current = status(8, true, 5, 0);
    started = 0;
    await page.goto(BASE + '/' + box);
    await btn.waitFor({ timeout: 10000 });
    await page.waitForFunction(() => /読み込み中…（5 通）/.test(document.getElementById('w-mail-fetch').textContent), null, { timeout: 8000 }).catch(() => {});
    check('③ 開いたときに動いていれば、続きを見る（押していない・押せない）', (await btn.textContent()).includes('読み込み中…（5 通）') && started === 0 &&
      !(await btn.isEnabled()),
      await btn.textContent());
    current = status(8, false, 9, 1, { errors: ['送信: 箱を開けません'] });
    await page.waitForFunction(() => /新しいメールを読み込む/.test(document.getElementById('w-mail-fetch').textContent), null, { timeout: 8000 }).catch(() => {});
    const t3 = await toastText();
    check('③ 終わったら知らせる（読めなかった箱の理由も）', t3.includes('受信 9 通・送信 1 通を読み込みました') && t3.includes('送信: 箱を開けません'), t3);

    // ④ サインインしていない。
    current = null;
    startReply = () => ({ status: 409, body: { success: false, message: 'メールアカウントにサインインしていません' } });
    await page.goto(BASE + '/' + box);
    await btn.waitFor({ timeout: 10000 });
    await btn.click();
    await page.waitForFunction(() => /サインインしていません/.test(document.getElementById('w-toast-host').textContent), null, { timeout: 8000 }).catch(() => {});
    check('④ サインインしていなければ理由を知らせ、ボタンは押せるまま', (await toastText()).includes('サインインしていません') && await btn.isEnabled(),
      await toastText());

    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
