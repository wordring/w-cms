// スラッシュメニューの「基本」の並び（2026-10-01・assets/app.js の slashBasicOrder）を画面から確かめる。
//
// 利用者:「スラッシュメニューは見出し1－3は並んでいて欲しいですね。その下に段落などでしょうか。タグも良く使うのでメニューの
// 上の方に出ていて欲しいです」。
//
//   ① 基本の頭は 見出し1・見出し2・見出し3・段落・可変タグ の順
//   ② 表をたくさん使っても（使った回数が多くても）基本の並びは動かない——ほかの分類だけが使った回数の順
//   ③ 「ファイル（PDF）」はメニューに無い・「ファイル表示」は基本の中
//
// 作ったページは最後に消し、ブラウザに憶えた使った回数（wcms.ui の slashUse）も元に戻します。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-slash-order.js
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
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 1600 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let id = '';
  // menu は編集モードで「/」を打って、メニューを「分類: 項目」の並びで返します。
  const menu = async () => {
    await page.goto(BASE + '/' + id + '?edit=true');
    await page.waitForSelector('#w-editor-content p[contenteditable="true"]', { timeout: 10000 });
    await page.click('#w-editor-content p');
    await page.keyboard.press('End');
    await page.keyboard.press('Enter');
    await page.keyboard.type('/');
    await page.waitForTimeout(600);
    const out = await page.evaluate(() => {
      let cat = '';
      const list = [];
      document.querySelectorAll('#w-slash-menu > *').forEach(e => {
        if (e.classList.contains('slash-menu-group')) cat = e.textContent.trim();
        else list.push(cat + ': ' + e.textContent.replace(/^\S+\s*/, '').trim());
      });
      return list;
    });
    await page.keyboard.press('Escape');
    return out;
  };
  try {
    await login(page, BASE);
    id = await makePage(page, '<h1>【E2E】メニューの並び</h1><p>x</p>');
    // 憶えた使った回数を控えてから試す。
    await page.goto(BASE + '/' + id);
    const saved = await page.evaluate(() => localStorage.getItem('wcms.ui'));

    // ①・③
    const m1 = await menu();
    const head = m1.slice(0, 5).join('・');
    check('① 基本の頭は 見出し1・見出し2・見出し3・段落・可変タグ',
      head === '基本: 見出し1・基本: 見出し2・基本: 見出し3・基本: 段落・基本: 可変タグ', head);
    check('③ 「ファイル（PDF）」は無い・「ファイル表示」は基本', !m1.some(s => s.includes('ファイル（PDF）')) &&
      m1.includes('基本: ファイル表示'), m1.filter(s => s.includes('ファイル')).join('・'));

    // ② 表と（ほかの分類の）子ページ一覧をたくさん使ったことにする。
    await page.evaluate(() => {
      let ui = {};
      try { ui = JSON.parse(localStorage.getItem('wcms.ui') || '{}') || {}; } catch (e) { ui = {}; }
      ui.slashUse = Object.assign({}, ui.slashUse, { table: 99, 'vocab:child-list': 0, 'vocab:material-search': 99 });
      localStorage.setItem('wcms.ui', JSON.stringify(ui));
    });
    const m2 = await menu();
    check('② 表をたくさん使っても基本の並びは動かない', m2.slice(0, 5).join('・') === head, m2.slice(0, 5).join('・'));
    const views = m2.filter(s => s.startsWith('ビュー: '));
    check('② ほかの分類は使った回数の順（材料を探す が先頭）', views[0] === 'ビュー: 材料を探す', views.slice(0, 2).join('・'));

    // 憶えた回数を元に戻す。
    await page.evaluate((s) => { if (s === null) localStorage.removeItem('wcms.ui'); else localStorage.setItem('wcms.ui', s); }, saved);
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
