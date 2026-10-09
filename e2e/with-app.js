// with-app.js は E2E を、試験のブラウザにだけ別の app.js（環境変数 APP_JS のファイル）を渡して走らせます（2026-10-09）。
//
// ⚠ **ディスクの assets/app.js を変えずに、直した app.js を確かめるため**です——サーバーは assets/ をディスクから配るので、
//    書き換えた瞬間に実運用の利用者へ届きます（職場は 2026-10-03 から実運用）。写しを直し、この道具で E2E を通してから
//    書き戻します。Playwright の chromium.launch を包み、作る文脈（newContext・newPage）すべてで /assets/app.js を差し替えます。
//    差し替えた app.js が一度も配られなければ「!!」の行を出します（道具が効いていない）。
//
// 使い方: APP_JS=<直した app.js> WCMS_BASE=https://localhost:8443 node with-app.js verify-cell-select.js
// ⚠ 効いているかは、わざと壊した写しで E2E が落ちることを1度見て確かめる。
const fs = require('fs');
const path = require('path');
const pw = require('playwright');

const body = fs.readFileSync(process.env.APP_JS, 'utf8');
let served = 0;
const hook = async (ctx) => {
  await ctx.route('**/assets/app.js*', (route) => {
    served++;
    route.fulfill({ status: 200, contentType: 'application/javascript; charset=utf-8', body });
  });
  return ctx;
};
const origLaunch = pw.chromium.launch.bind(pw.chromium);
pw.chromium.launch = async (...args) => {
  const b = await origLaunch(...args);
  const newContext = b.newContext.bind(b);
  b.newContext = async (...o) => hook(await newContext(...o));
  b.newPage = async (...o) => (await b.newContext(...o)).newPage();
  return b;
};
process.on('exit', () => { if (!served) console.log('!! 差し替えた app.js が一度も配られていません'); });
require(path.resolve(__dirname, process.argv[2]));
