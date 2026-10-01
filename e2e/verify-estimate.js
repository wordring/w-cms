// 見積計算表の弊社利益と確定単価（2026-10-01・ext/toho/estimate.go）を画面から確かめる。
//
// 利用者:「加工製品ページの見積もり計算表の下に弊社利益を掛ける項目を作り確定単価としたいです。弊社利益はデフォルトで
// 10％…利益率を変更できるようにしたいです。確定単価は目立つように太字です」「単価*10%=○○円と表示したいです」
// 「見積もり計算表は複数ある場合があります」。
//
//   ① 表の下に「弊社利益: 単価 365円 × 10%（既定）= 37円」と、太字の「確定単価: 402円」
//   ② 表が2枚あれば、それぞれの下に出る
//   ③ 足元の「利益率 [15] % 変える」で、その表に「弊社利益」の行ができて計算し直す（もう1枚は変わらない）
//
// 当て先はトップ直下に自分で作り、最後に消します。東邦の拡張が無い組では飛ばします。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-estimate.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

const table = (rows) => '<table><caption>見積計算表</caption><tbody><tr><th>工程</th><th>数</th><th>単位</th><th>備考</th></tr>' +
  rows.map(r => '<tr><td>' + r[0] + '</td><td>' + r[1] + '</td><td>' + r[2] + '</td><td>' + (r[3] || '') + '</td></tr>').join('') +
  '</tbody></table>';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let id = '';
  try {
    await login(page, BASE);
    const exts = await page.evaluate(async () => (await (await fetch('/api/tag-schema')).json()).extensions || []);
    if (!exts.includes('toho')) {
      console.log('東邦の拡張が無いので飛ばします');
      await browser.close();
      process.exit(0);
    }
    id = await makePage(page, '<h1>【E2E】見積の品</h1>' +
      table([['ロット', '20', '個'], ['材料', '55', '円'], ['板金', '310', '円'], ['単価', '365', '円', '塗装無し']]) +
      table([['ロット', '20', '個'], ['材料', '55', '円'], ['板金', '310', '円'], ['塗装', '200', '円'], ['単価', '', '円', '塗装あり']]));
    check('当て先を作れた', !!id, id);
    await page.goto(BASE + '/' + id);
    await page.waitForSelector('#w-editor-content .estimate-final', { timeout: 8000 }).catch(() => {});
    const foot = async () => page.evaluate(() => [...document.querySelectorAll('#w-editor-content table')].map(t => ({
      profit: (t.querySelector('.estimate-profit') || {}).textContent || '',
      final: (t.querySelector('.estimate-final strong') || {}).textContent || '',
      bold: !!t.querySelector('.estimate-final strong') &&
        Number(getComputedStyle(t.querySelector('.estimate-final strong')).fontWeight) >= 700,
    })));
    let f = await foot();
    check('1枚目: 弊社利益の式', f[0] && f[0].profit === '弊社利益: 単価 365円 × 10%（既定） = 37円', JSON.stringify(f[0]));
    check('1枚目: 確定単価は太字', f[0] && f[0].final === '確定単価: 402円' && f[0].bold, JSON.stringify(f[0]));
    check('2枚目: 単価が空なら円の行を足す', f[1] && f[1].profit === '弊社利益: 単価（円の行の合計） 565円 × 10%（既定） = 57円' &&
      f[1].final === '確定単価: 622円', JSON.stringify(f[1]));

    // ③ 1枚目の率を 15% に
    const form = page.locator('#w-editor-content .estimate-rate-form').first();
    await form.locator('.estimate-rate-input').fill('15');
    await Promise.all([
      page.waitForNavigation({ timeout: 10000 }).catch(() => {}),
      form.locator('.estimate-rate-set').click(),
    ]);
    await page.waitForSelector('#w-editor-content .estimate-final', { timeout: 8000 }).catch(() => {});
    f = await foot();
    check('率を変えると計算し直す', f[0] && f[0].profit === '弊社利益: 単価 365円 × 15% = 55円' && f[0].final === '確定単価: 420円',
      JSON.stringify(f[0]));
    check('もう1枚は変わらない', f[1] && f[1].final === '確定単価: 622円', JSON.stringify(f[1]));
    const saved = await page.evaluate(async (pid) => (await (await fetch('/api/load?id=' + pid)).text()), id);
    check('1枚目の表に「弊社利益」の行が入る（本文）', (saved.match(/<td>弊社利益<\/td>/g) || []).length === 1 &&
      saved.includes('<td>弊社利益</td><td>15</td><td>%</td>'), '');
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
