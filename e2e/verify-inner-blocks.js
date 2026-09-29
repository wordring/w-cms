// 節・折りたたみの中の要素にも ⠿・🗑・＋（2026-09-29）。
//
// 利用者:「図面一枚一枚がブロック（Section）という構造は難しいですか？…ブロックを消したり移動したり汎用的な
// 操作なので覚える必要もない」「最終的には節の中のPDFを節の外にも動かせるようにしたいです」。
//
//   ① 節の中の段落を指すと、左の欄に帯（⠿ 🗑 ＋）が出て、上の段のブロックの操作は隠れる
//   ② 節の見出し（入れ物の名前）を指しても帯は出ない
//   ③ 🗑 で中の段落だけが消える
//   ④ ＋ でスラッシュメニューが開き、選んでも**節ごとは置き換わらない**（その段落1つだけ）
//   ⑤ ⠿ で中の段落を節の外（上の段）へ出せる
//   ⑥ 上の段の段落を、折りたたみの題の上へ落とすと中（先頭）へ入る
//   ⑦ 保存した本文（正本のファイル）に包み・印が残らない
//   ⑧ 閲覧モードでは帯が出ない
//
// 当て先は自分で作って最後に消します（トップ直下に1枚）。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-inner-blocks.js（リポジトリの e2e/ で）
const fs = require('fs');
const path = require('path');
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
function rawBody(id) {
  const f = path.join(__dirname, '..', 'data', 'master', id.slice(0, 2), id, id + '.html');
  try { return fs.readFileSync(f, 'utf8'); } catch (e) { return ''; }
}

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  let id = '';
  try {
    await login(page, BASE);
    id = await makePage(page, '<h1>【E2E】中の要素</h1>' +
      '<section><h2>データ</h2><p>段落A</p><p>段落B</p></section>' +
      '<details open><summary>資料 1</summary><p>段落C</p></details>' +
      '<p>段落D</p>');
    check('当て先を作れた', !!id, id);
    await page.goto(BASE + '/' + id + '?edit=true');
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    await page.waitForTimeout(500);

    const sec = '#w-editor-content section:has(> h2:text-is("データ"))';
    const bar = page.locator('#w-inner-controls');
    // ① 節の中の段落を指す
    await page.locator(sec + ' > p:text-is("段落A")').hover();
    await page.waitForTimeout(150);
    check('中の段落を指すと帯が出る', await bar.isVisible());
    const hidden = await page.evaluate(() => {
      const b = document.querySelector('#w-editor-content section').closest('.editor-block');
      return b.classList.contains('inner-pointing') && getComputedStyle(b.querySelector(':scope > .block-controls')).opacity === '0';
    });
    check('そのあいだ上の段のブロックの操作は隠れる', hidden);
    // ② 見出しを指す
    await page.locator(sec + ' > h2').hover();
    await page.waitForTimeout(150);
    check('節の見出しを指しても帯は出ない', !(await bar.isVisible()));

    // ③ 🗑 で段落B を消す
    await page.locator(sec + ' > p:text-is("段落B")').hover();
    await page.waitForTimeout(150);
    await page.locator('#w-inner-controls .delete-btn').click();
    await page.waitForTimeout(300);
    check('🗑 で中の段落だけが消えた', await page.locator(sec + ' > p:text-is("段落B")').count() === 0 &&
      await page.locator(sec + ' > p:text-is("段落A")').count() === 1);

    // ④ ＋ → スラッシュメニュー → 1つ目を選ぶ
    await page.locator(sec + ' > p:text-is("段落A")').hover();
    await page.waitForTimeout(150);
    await page.locator('#w-inner-controls button:has-text("＋")').click();
    await page.waitForTimeout(300);
    check('＋ でスラッシュメニューが開く', await page.locator('#w-slash-menu.active').count() === 1);
    // ⚠ ページの中の querySelector は Playwright の :text-is を知らない——見出しの文字で探す。
    const DATA_SEC = () => [...document.querySelectorAll('#w-editor-content section')].find(x => {
      const h = x.querySelector(':scope > h2'); return h && h.textContent === 'データ'; });
    const before = await page.evaluate(`(${DATA_SEC})().children.length`);
    await page.keyboard.press('Enter');
    await page.waitForTimeout(600);
    const after = await page.evaluate(`(() => { const el = (${DATA_SEC})();
      return el ? { n: el.children.length, head: el.querySelector(':scope > h2') && el.querySelector(':scope > h2').textContent } : null; })()`);
    check('選んでも節ごとは置き換わらない（見出しも中身も残る）', !!after && after.head === 'データ' && after.n >= before,
      JSON.stringify(after));
    check('「/」の段落は残っていない', await page.evaluate(`![...(${DATA_SEC})().querySelectorAll(':scope > p')].some(p => p.textContent.startsWith('/'))`));
    await page.waitForTimeout(2000); // 自動保存

    // ⑤ 段落A を上の段（段落D の後ろ）へ
    await page.locator(sec + ' > p:text-is("段落A")').hover();
    await page.waitForTimeout(150);
    const dBlock = page.locator('#w-editor-content .editor-block:has(> .block-content > p:text-is("段落D"))');
    const db = await dBlock.boundingBox();
    await page.locator('#w-inner-controls .drag-handle').dragTo(dBlock, { targetPosition: { x: 40, y: Math.max(2, db.height - 4) } });
    await page.waitForTimeout(600);
    const topOrder = await page.evaluate(() => [...document.querySelectorAll('#w-editor-content > .editor-block > .block-content > *')]
      .map(el => el.tagName + ':' + (el.tagName === 'P' ? el.textContent : '')));
    check('⠿ で中の段落を節の外（上の段）へ出せた', topOrder.join('|').endsWith('P:段落D|P:段落A') &&
      await page.locator(sec + ' > p:text-is("段落A")').count() === 0, topOrder.join(' | '));

    // ⑥ 上の段の段落D を、折りたたみの題の上へ落とす
    const dBlock2 = page.locator('#w-editor-content .editor-block:has(> .block-content > p:text-is("段落D"))');
    await dBlock2.hover();
    await page.waitForTimeout(150);
    await dBlock2.locator(':scope > .block-controls .drag-handle').dragTo(
      page.locator('#w-editor-content details > summary:text-is("資料 1")'));
    await page.waitForTimeout(600);
    const inFold = await page.evaluate(() => {
      const d = [...document.querySelectorAll('#w-editor-content details')].find(x => x.querySelector(':scope > summary').textContent === '資料 1');
      return [...d.children].filter(c => !c.classList.contains('vocab-chrome')).map(c => c.tagName + ':' + c.textContent);
    });
    check('上の段の段落を折りたたみの中（題の直後）へ入れられた', inFold[1] === 'P:段落D', inFold.join(' | '));
    check('上の段から段落D が消えた', await page.locator('#w-editor-content > .editor-block > .block-content > p:text-is("段落D")').count() === 0);

    // ⑦ 保存した本文
    await page.waitForTimeout(2500);
    const saved = rawBody(id);
    check('保存した本文: 段落B が無い・段落A は上の段・段落D は資料 1 の中',
      !saved.includes('段落B') && /<\/section>[\s\S]*<details[\s\S]*段落D[\s\S]*<\/details>[\s\S]*段落A/.test(saved), saved.replace(/\s+/g, ' ').slice(0, 400));
    check('保存した本文に包み・印が残らない', !/editor-block|block-content|inner-|contenteditable|<div/.test(saved));

    // ⑧ 閲覧モード
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForFunction(() => !document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    await page.waitForTimeout(300);
    await page.locator('#w-editor-content details p').first().hover();
    await page.waitForTimeout(150);
    check('閲覧モードでは帯が出ない', !(await bar.isVisible()));
  } finally {
    await deletePage(page, id);
  }
  check('JavaScript エラーなし', errs.length === 0, errs.join(' / '));
  console.log(fails === 0 ? '\n結果: 合格' : '\n結果: ' + fails + ' 件の不合格');
  await browser.close();
  process.exitCode = fails === 0 ? 0 : 1;
})();
