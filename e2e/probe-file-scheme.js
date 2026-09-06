// ブラウザから file:// のファイルを開けるか、実際に確かめる（2026-09-07）。
//
// WebDAV のファイルは `Z:\...` にあるので、もしページ内のリンクから file:// へ
// 飛べるなら「クリック→ローカルのアプリ」が**何も入れずに**成立します。
// 飛べないなら、間に何か（プロトコルハンドラ）が要ります。
//
// 使い方: node probe-file-scheme.js
const { chromium } = require('playwright');

const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
const USER = process.env.WCMS_USER || 'a';
const PASS = process.env.WCMS_PASS || 'a';

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await ctx.newPage();

  const blocked = [];
  page.on('console', (m) => {
    if (m.type() === 'error') blocked.push('console: ' + m.text());
  });

  // ログインして、実際のページ（https オリジン）から試す。
  await page.goto(BASE + '/login');
  await page.fill('#username', USER);
  await page.fill('#password', PASS);
  await page.click('button[type=submit]');
  await page.waitForLoadState('networkidle');
  const origin = new URL(page.url()).origin;
  console.log('起点のオリジン:', origin);

  // ① リンクを本文に足して押す（人がやることに一番近い）
  await page.evaluate(() => {
    const a = document.createElement('a');
    a.id = 'probe-link';
    a.href = 'file:///C:/Windows/win.ini';
    a.textContent = 'file link';
    document.body.appendChild(a);
  });
  const before = page.url();
  await page.click('#probe-link').catch((e) => blocked.push('click: ' + e.message));
  await page.waitForTimeout(700);
  console.log('① リンク押下 → URLが変わったか:', page.url() !== before ? '変わった' : '変わらない');

  // ② スクリプトで直接飛ぶ
  await page.evaluate(() => { window.location.href = 'file:///C:/Windows/win.ini'; })
    .catch((e) => blocked.push('location: ' + e.message));
  await page.waitForTimeout(700);
  console.log('② location 代入 → URL:', page.url());

  // ③ 新しいタブで開く
  const pages0 = ctx.pages().length;
  await page.evaluate(() => { window.open('file:///C:/Windows/win.ini', '_blank'); })
    .catch((e) => blocked.push('open: ' + e.message));
  await page.waitForTimeout(700);
  console.log('③ window.open → タブが増えたか:', ctx.pages().length > pages0 ? '増えた' : '増えない');

  if (blocked.length) {
    console.log('--- ブラウザが出した拒否の記録 ---');
    for (const b of blocked) console.log('  ' + b);
  }
  await browser.close();
})();
