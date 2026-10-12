// 右上のスイッチを「閲覧」1つの意味で見せる（2026-10-12）を画面から確かめる。
//
// 利用者:「閲覧モードのスイッチですが、編集できますに変えず「閲覧」のままで、編集できるときにグレーアウトして意味の統一を図っては
// どうでしょう」——文字はいつも「閲覧」。閲覧モードでスイッチがオン（緑・つまみが右）、書けるときはオフ（灰色・つまみが左）で文字も灰色。
// 中の checkbox は書ける形で checked のまま（見た目だけ逆）。
//
// ページは自分で作り、最後に消します。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-mode-switch.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let id = '';
  // look はスイッチの見た目（文字・文字の色・地の色・つまみの位置）。
  const look = () => page.evaluate(() => {
    const t = document.getElementById('w-mode-text');
    const sl = document.querySelector('.switch .slider');
    const knob = getComputedStyle(sl, '::before').transform;
    return { text: t.innerText, color: getComputedStyle(t).color, bg: getComputedStyle(sl).backgroundColor,
      knobRight: knob !== 'none' && !/matrix\(1, 0, 0, 1, 0, 0\)/.test(knob), edit: document.body.hasAttribute('edit-mode') };
  });
  const toggle = async (on) => {
    await page.evaluate((v) => { const t = document.getElementById('w-mode-toggle'); if (t.checked !== v) { t.checked = v; t.dispatchEvent(new Event('change', { bubbles: true })); } }, on);
    await page.waitForFunction((v) => document.body.hasAttribute('edit-mode') === v, on, { timeout: 8000 });
    await page.waitForTimeout(600); // つまみの動き（0.4秒）
  };
  try {
    await login(page, BASE);
    id = await makePage(page, '<h1>【E2E】スイッチ</h1><p>本文</p>');
    await page.goto(BASE + '/' + id);
    await page.waitForTimeout(800);

    await toggle(false);
    const v = await look();
    check('閲覧モード: 文字は「閲覧」・濃い色', v.text === '閲覧' && v.color === 'rgb(51, 65, 85)', JSON.stringify(v));
    check('閲覧モード: スイッチはオン（緑・つまみが右）', v.bg === 'rgb(16, 185, 129)' && v.knobRight, JSON.stringify(v));

    await toggle(true);
    const e = await look();
    check('書けるとき: 文字は「閲覧」のまま・灰色', e.text === '閲覧' && e.color === 'rgb(203, 213, 225)', JSON.stringify(e));
    check('書けるとき: スイッチはオフ（灰色・つまみが左）', e.bg === 'rgb(203, 213, 225)' && !e.knobRight, JSON.stringify(e));

    // スイッチを押すと閲覧モードに（押して替える向きもそろっている）。
    await page.locator('.switch .slider').click();
    await page.waitForFunction(() => !document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    check('オフのスイッチを押すと閲覧モード（オン）になる', (await look()).text === '閲覧' && !(await look()).edit);
    check('ページのエラーが無い', errs.length === 0, errs.join(' / '));
  } catch (e) {
    check('例外なし: ' + e.message, false);
  } finally {
    await deletePage(page, id);
    await browser.close();
  }
  console.log(fails ? '\n' + fails + ' 件の失敗' : '\nすべて合格');
  process.exit(fails ? 1 : 0);
})();
