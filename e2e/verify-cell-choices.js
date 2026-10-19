// 表のセルの候補（2026-10-19・internal/cms/cell_choices.go・assets/app.js の fillCellChoiceMenu・filterEnumMenu）を画面から確かめる。
//
// 利用者:「表のセルに入る値が数個に決まっている場合があります。…これをオートコンプリートのようにコンボボックスで選ぶように出来ないでしょうか？」
// 「表のセルの候補は、サーバーが暇なときに DB を数えて置いておく形で作ってください。値が数個に決まっている列だけに出し、何個までかは設定に置いてください。」
//
//   ① 値が数個に決まっている列（表面）の空のセルに入ると、候補が使っている行の多い順に出る
//   ② 打った字で絞れる（オートコンプリート）
//   ③ 候補を押すとセルに入る
//   ④ 値がばらばらの列（品名）には出ない
//
// 暇を待たずに、管理者の口（/api/admin/autocomplete/survey）で数えさせます（読むだけ・本文は書かない）。
// ⚠ 2026-10-19 夜から、候補は「何度も入力された定型的な値」だけ（表面は黒3・白2で、どちらも2回以上）。
// 当て先は自分で作って最後に消します（トップ直下に1枚）。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-cell-choices.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const sleep = (ms) => new Promise((r) => setTimeout(r, ms));
const CAP = '【E2E】候補の表';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let id = '';
  try {
    await login(page, BASE, 'a', 'a', { keepMode: true });
    const row = (n, s) => '<tr><td>' + n + '</td><td>' + s + '</td></tr>';
    id = await makePage(page, '<h1>【E2E】セルの候補</h1><table><caption>' + CAP + '</caption><tbody>' +
      '<tr><th>品名</th><th>表面</th></tr>' +
      row('板1', '黒') + row('板2', '黒') + row('板3', '白') + row('板4', '黒') + row('板5', '白') + row('板6', '') + row('板7', '') +
      '</tbody></table>');
    check('当て先を作れた', !!id, id);
    const surveyed = await page.evaluate(async () => {
      const r = await fetch('/api/admin/autocomplete/survey', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' });
      return r.status + ' ' + (await r.text()).slice(0, 200);
    });
    check('管理者の口で数えた', /"success":true/.test(surveyed), surveyed);

    await page.evaluate(() => localStorage.setItem('wcms.mode', 'edit'));
    await page.goto(BASE + '/' + id);
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    await sleep(600);
    const menu = page.locator('#w-enum-menu');
    const shown = () => page.$$eval('#w-enum-menu.active button[data-choice]', (bs) => bs.filter((b) => !b.hidden).map((b) => b.textContent));

    // ① 表面の空のセル（7行目のデータ＝8行目）
    const cell = '#w-editor-content table tr:nth-of-type(7) > td:nth-of-type(2)';
    await page.click(cell);
    await menu.waitFor({ state: 'visible', timeout: 5000 }).catch(() => {});
    await sleep(300);
    const first = await shown();
    check('① 表面の空のセルに、候補が多い順に出る（黒・白）', first.join(',') === '黒,白', JSON.stringify(first));

    // ②
    await page.keyboard.type('白');
    await sleep(300);
    const filtered = await shown();
    check('② 打った字で絞れる（白だけ）', filtered.join(',') === '白', JSON.stringify(filtered));
    await page.keyboard.press('Backspace');
    await sleep(300);
    check('② 字を消すと全部に戻る', (await shown()).join(',') === '黒,白', JSON.stringify(await shown()));

    // ③
    await page.locator('#w-enum-menu button[data-choice]', { hasText: '黒' }).click();
    await sleep(300);
    check('③ 候補を押すとセルに入る', (await page.textContent(cell)).trim() === '黒');

    // ④ 品名のセル
    await page.click('#w-editor-content table tr:nth-of-type(8) > td:nth-of-type(1)');
    await sleep(800);
    check('④ 行ごとに違う値を書く列（品名・どの値も1回）には出ない', (await shown()).length === 0, JSON.stringify(await shown()));
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    await page.goto(BASE + '/000000').catch(() => {});
    if (id) {
      await deletePage(page, id).catch(() => {});
      if (await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).status, id).catch(() => 0) === 200) {
        await sleep(800);
        await deletePage(page, id).catch(() => {});
      }
      const st = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).status, id).catch(() => 0);
      check('作ったページを消した', st === 404, String(st));
    }
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
