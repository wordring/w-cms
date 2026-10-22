// verify-leave-checkpoint.js — ページを去るとき、最後の書きかけが保存され、その版が閉じるか（2026-10-22 の見直し）
//
// それまでは版の区切り（/api/version-checkpoint）が最後の保存より先に送られ、最後の書きかけが自分の版に閉じなかった
// （次に書いたものと1つの版に畳まれた）。いまは最後の保存に区切りの印を載せ、サーバーが書いたあとで閉じる。
// 自分で作ったページ（【E2E】）の上だけで動かし、最後に消す。
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');

const BASE = process.env.WCMS_BASE || 'https://localhost:8443';

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  let ok = true;
  const check = (cond, msg) => { console.log((cond ? '✓ ' : '✗ ') + msg); if (!cond) ok = false; };
  let id = '';
  const versions = () => page.evaluate(async (pid) => {
    const r = await fetch('/api/versions?id=' + pid);
    const d = await r.json();
    return (d.versions || d || []);
  }, id);
  try {
    await login(page, BASE);
    id = await makePage(page, '<h1>【E2E】去るときの区切り</h1><p>はじめ</p>');
    check(!!id, 'ページを作った ' + id);
    await page.goto(BASE + '/' + id);
    await page.waitForSelector('#w-editor-content p');
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 5000 });
    await page.waitForTimeout(500);
    // 書いて、ブロックを去らずにページを去る（最後の書きかけは、去るときの保存だけが送る）。
    await page.locator('#w-editor-content p').first().click();
    await page.keyboard.press('End');
    await page.keyboard.type('・最後の書きかけ');
    // ⚠ 去るときの要求（keepalive・ビーコン）は、試験のブラウザの request で見えない——版が閉じたかで確かめる。
    await page.goto(BASE + '/');
    await page.waitForTimeout(1500);
    const list = await versions();
    const newest = Array.isArray(list) ? list[0] : null;
    check(!!newest && newest.sealed === true, 'いちばん新しい版が閉じている: ' + JSON.stringify(newest));
    const body = newest ? await page.evaluate(async (a) => (await fetch('/api/version?id=' + a.id + '&v=' + encodeURIComponent(a.v))).text(),
      { id, v: newest.id }) : '';
    check(body.includes('最後の書きかけ'), '閉じた版に最後の書きかけが入っている');
  } finally {
    await deletePage(page, id);
    await browser.close();
  }
  console.log(ok ? '合格' : '不合格');
  process.exit(ok ? 0 : 1);
})();
