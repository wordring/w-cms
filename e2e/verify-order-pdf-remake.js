// 部材発注書の「📄 PDF作成」を、明細を直してからもう一度押すと、画面に新しい PDF が出ることを確かめます（2026-10-23）。
// 利用者:「PDFを一度作ると修正できないのは辛いので、再度作れるようにしてください」
// 送る前の「PDF作成」は同じ添付を上書きするので、埋め込む住所が同じだとブラウザが前の中身を出し続けていました。
// いまは住所に書いた時刻（?v=）が付き、上書きすると変わります（internal/cms/file_view.go）。
// 自分で作ったページの上だけで動き、最後に消します。WCMS_BASE=https://localhost:8443 node verify-order-pdf-remake.js
const fs = require('fs');
const path = require('path');
const { chromium } = require(path.join(process.cwd(), 'node_modules', 'playwright'));
const { login, makePage, deletePage, writeBody } = require('./lib');

const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
let fails = 0;
function check(label, ok, note) {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
}
const orderBody = (qty) => '<h1>【E2E】部材発注　作り直し</h1><dl data-type="tags"><dt>仕入先</dt><dd>【E2E】仕入先</dd>' +
  '<dt>ページ種類</dt><dd>弊社部材発注書</dd></dl>' +
  '<table><caption>部材発注明細表</caption><tbody><tr><th>種類</th><th>品名</th><th>数量</th><th>単位</th><th>状態</th></tr>' +
  '<tr><td>購入部品</td><td>【E2E】ねじ</td><td>' + qty + '</td><td>個</td><td>未発注</td></tr></tbody></table>';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let id = '';
  const embedSrc = async () => {
    await page.goto(BASE + '/' + id);
    await page.waitForSelector('#w-editor-content embed.file-view-body', { timeout: 10000 }).catch(() => {});
    return page.evaluate(() => { const e = document.querySelector('#w-editor-content embed.file-view-body'); return e ? e.getAttribute('src') : ''; });
  };
  const pressPDF = async () => {
    await page.goto(BASE + '/' + id);
    await page.waitForSelector('#w-editor-content [data-order-pdf]', { timeout: 10000 });
    await Promise.all([page.waitForNavigation({ timeout: 15000 }).catch(() => {}), page.locator('#w-editor-content [data-order-pdf]').first().click()]);
  };
  const pdfBytes = async (src) => page.evaluate(async (u) => { const r = await fetch(u.split('#')[0]); return (await r.arrayBuffer()).byteLength; }, src);
  try {
    await login(page, BASE);
    id = await makePage(page, orderBody('10'));
    check('当て先を作れた', !!id);
    await pressPDF();
    const first = await embedSrc();
    check('① PDF作成で PDF が出て、住所に版（?v=）が付く', /\?v=/.test(first), first);
    const firstSize = await pdfBytes(first);
    // 明細の数量だけを直して、もう一度（本文の正本を読んで直す——PDF の表示の印は残す。/api/load は鏡を含むので書き戻さない）。
    const file = path.join(__dirname, '..', process.env.WCMS_DATA_DIR || 'data', 'master', id.slice(0, 2), id, id + '.html');
    const cur = fs.readFileSync(file, 'utf8');
    check('① 本文に PDF の表示の印がある', cur.includes('data-type="file-view"'));
    await writeBody(page, id, cur.replace(/<td>\s*10\s*<\/td>/, '<td>12345</td>'));
    await pressPDF();
    const second = await embedSrc();
    check('② 直してもう一度押すと、住所が変わる（ブラウザが前の写しを出さない）', !!second && second !== first, first + ' → ' + second);
    check('② 同じ添付を上書きした（添付の名前は同じ）', second.split('?')[0] === first.split('?')[0], second.split('?')[0]);
    const secondSize = await pdfBytes(second);
    check('② 新しい住所の PDF は中身が変わっている', secondSize > 0 && secondSize !== firstSize, firstSize + ' → ' + secondSize + ' バイト');
    check('画面のエラーが無い', errs.length === 0, errs.join(' / '));
  } catch (e) {
    check('途中で止まらない', false, String(e));
  } finally {
    await deletePage(page, id);
    await browser.close();
  }
  console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
  process.exit(fails ? 1 : 0);
})();
