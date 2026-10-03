// 見積依頼書ページを作る（2026-10-03・段2——ext/toho/rfq_doc.go・assets/app.js の wireRFQ）を画面から確かめる。
//
// 利用者:「見積依頼部材表の下で諸々入力してボタンを押すと「見積依頼ページ」が出来ます」（10-02 の答えでは名前は見積依頼書ページ）。
//
//   ① 見積依頼部材表の下に「見積依頼書ページを作る」の欄（差出人・仕入先・見積依頼日・備考）がある
//   ② 仕入先が空なら断る
//   ③ 入れて押すと見積依頼書ページが開く——タグ（見積依頼番号・仕入先・見積依頼日）・見積依頼明細（未回答・単価は空）・備考
//   ④ 元の見積依頼部材表は消える
//
// 当て先（【E2E】の置き場と見積依頼部材表）は自分で作り、できた見積依頼書（本物の 見積依頼／年／月 の下）と、試験が作った
// 年月のフォルダは最後に消します。差出人（署名を持つ人）が居なければ飛ばします。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-rfq-doc.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, childrenOf } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

const HEAD = ['弊社品番', '種類', '品名', '材質', '形状', '寸法', '数量', '単位', '単価', '備考', '状態'];
const tr = (cells, tag = 'td') => '<tr>' + cells.map((c) => '<' + tag + '>' + c + '</' + tag + '>').join('') + '</tr>';
const BOX = '<h1>【E2E】見積依頼書の置き場</h1>' +
  '<section><table><caption>見積依頼部材表</caption><tbody>' + tr(HEAD, 'th') +
  tr(['', '材料', '', 'E2E-RFQD-SS400', '板', 't6*80*120', '6', '枚', '999', '', '']) +
  tr(['', '購入部品', 'E2E-RFQD-ボルト', '', '', '', '10', '本', '', '', '']) +
  '</tbody></table></section>';

const parentOf = (page, id) => page.evaluate(async (x) => (await (await fetch('/api/page-meta?id=' + x)).json()).parent_id, id);

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1400, height: 1000 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  page.on('dialog', (d) => d.accept());
  let box = '', doc = '';
  try {
    await login(page, BASE);
    box = await makePage(page, BOX);
    check('当て先を作れた', !!box);
    await page.goto(BASE + '/' + box);
    await page.waitForTimeout(800);

    // ①
    const form = page.locator('.rfq-doc-form');
    check('① 見積依頼部材表の下に「見積依頼書ページを作る」の欄がある', await form.count() === 1);
    const signers = await form.locator('[data-unorder="signer"] option').evaluateAll((os) => os.map((o) => o.value).filter(Boolean));
    if (!signers.length) {
      console.log('— 差出人（署名を持つ人）が居ないので、作るところは飛ばします');
      return;
    }
    // ②
    await form.locator('[data-rfqdoc="supplier"]').fill('');
    await form.locator('[data-rfq-doc-go]').click();
    await page.waitForTimeout(400);
    check('② 仕入先が空なら断る', ((await page.locator('[data-rfq-doc-result]').textContent()) || '').includes('仕入先を入れてください'));

    // ③
    await form.locator('[data-unorder="signer"]').selectOption(signers[0]);
    await form.locator('[data-rfqdoc="supplier"]').fill('【E2E】仕入先');
    await form.locator('[data-rfqdoc="date"]').fill('2026-10-05');
    await form.locator('[data-rfqdoc="note"]').fill('E2E用');
    await Promise.all([
      page.waitForURL((u) => !u.pathname.endsWith('/' + box), { timeout: 15000 }).catch(() => {}),
      form.locator('[data-rfq-doc-go]').click(),
    ]);
    await page.waitForTimeout(1000);
    doc = (new URL(page.url())).pathname.replace(/^\//, '');
    check('③ 見積依頼書ページが開く', /^\d{6}$/.test(doc) && doc !== box, page.url());
    const raw = await page.evaluate(async (id) => (await fetch('/api/load?id=' + id)).text(), doc);
    check('③ 題は「見積依頼　仕入先」', raw.includes('<h1>見積依頼　【E2E】仕入先</h1>'), raw.slice(0, 80));
    check('③ 見積依頼番号はページ番号・仕入先・見積依頼日のタグ', raw.includes('<dt>見積依頼番号</dt><dd>' + doc + '</dd>') &&
      raw.includes('<dt>仕入先</dt><dd>【E2E】仕入先</dd>') && raw.includes('<dt>見積依頼日</dt><dd>2026-10-05</dd>'));
    check('③ 見積依頼担当に差出人', raw.includes('<dt>見積依頼担当</dt><dd>' + signers[0] + '</dd>'));
    check('③ 見積依頼明細に2行・どちらも未回答', raw.includes('E2E-RFQD-SS400') && raw.includes('E2E-RFQD-ボルト') &&
      (raw.match(/>未回答</g) || []).length === 2);
    check('③ 単価は空（業者に聞く前の値を写さない）', !raw.includes('999'));
    check('③ 備考の節に「E2E用」', raw.includes('<p>E2E用</p>'));

    // ④
    const after = await page.evaluate(async (id) => (await fetch('/api/load?id=' + id)).text(), box);
    check('④ 元の見積依頼部材表は消える', !after.includes('<caption>見積依頼部材表</caption>'));

    // ⑤ 見積依頼明細の下の備考の欄・PDF を作る・PDF の下の送る欄・送った（FAX・手渡し）（2026-10-03・段2の後半）。
    // ⚠ 送る口は画面の手前で止める（本物のメールは出さない）。
    await page.route('**/api/mail/send', (route) => route.fulfill({ status: 200, contentType: 'application/json',
      body: JSON.stringify({ success: false, message: 'E2E で止めました' }) }));
    await page.goto(BASE + '/' + doc);
    await page.waitForTimeout(800);
    const noteBox = page.locator('#w-editor-content .estimate-note-save[data-note-url="/api/rfq/note"]');
    check('⑤ 見積依頼明細の下に備考の欄', await noteBox.count() === 1);
    await page.locator('#w-editor-content .estimate-note-input').fill('E2E 標準2輪用');
    await Promise.all([page.waitForEvent('load', { timeout: 15000 }).catch(() => {}), noteBox.click()]);
    await page.waitForTimeout(1000);
    let raw5 = await page.evaluate(async (id) => (await fetch('/api/load?id=' + id)).text(), doc);
    check('⑤ 備考を保存すると備考の節に入る', raw5.includes('<h2>備考</h2><p>E2E 標準2輪用</p>'));
    await Promise.all([page.waitForEvent('load', { timeout: 20000 }).catch(() => {}), page.locator('#w-editor-content .rfq-pdf-go').click()]);
    await page.waitForTimeout(1500);
    const order = await page.evaluate(() => {
      const root = document.getElementById('w-editor-content');
      const html = root.innerHTML;
      return { table: html.indexOf('<caption>見積依頼明細</caption>'), pdf: html.search(/data-type="file-view"/), send: html.indexOf('見積依頼を送る') };
    });
    check('⑤ PDF を作ると明細の下に出る', order.pdf > order.table && order.table >= 0, JSON.stringify(order));
    check('⑤ 送る欄は PDF の下', order.send > order.pdf, JSON.stringify(order));
    await page.waitForSelector('.mail-compose [data-mc="to"]', { timeout: 10000 }).catch(() => {});
    const genText = (await page.locator('.mail-compose .mc-attach-generated').textContent().catch(() => '')) || '';
    check('⑤ 送る欄が開いていて、見積依頼書のPDFが添付に印つきで並ぶ', genText.includes('見積依頼書 ' + doc + '.pdf') &&
      await page.locator('.mail-compose input[data-mc-generated]').isChecked().catch(() => false), genText);
    check('⑤ 件名は「見積依頼（№ ページ番号）」', (await page.locator('.mail-compose [data-mc="subject"]').inputValue().catch(() => '')) === '見積依頼（№ ' + doc + '）');
    await Promise.all([page.waitForEvent('load', { timeout: 15000 }).catch(() => {}), page.locator('#w-editor-content .rfq-sent-go').click()]);
    await page.waitForTimeout(1000);
    raw5 = await page.evaluate(async (id) => (await fetch('/api/load?id=' + id)).text(), doc);
    check('⑤ 「送った（FAX・手渡し）」で送付日に今日', /<dt>送付日<\/dt><dd>\d{4}-\d{2}-\d{2}<\/dd>/.test(raw5));
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    // 見積依頼書と、試験が作った年月のフォルダ（作った当て先より新しい番号で、空のもの）を消す。
    const base = Number(box || 0);
    if (doc && doc !== box) {
      const up = await parentOf(page, doc).catch(() => '');
      await deletePage(page, doc).catch(() => {});
      let f = up;
      for (let i = 0; i < 2 && f && Number(f) > base; i++) {
        const kids = await childrenOf(page, f).catch(() => [1]);
        if (kids.length !== 0) break;
        const next = await parentOf(page, f).catch(() => '');
        await deletePage(page, f).catch(() => {});
        f = next;
      }
    }
    for (const id of [doc, box]) {
      if (!id) continue;
      if (id !== doc) await deletePage(page, id).catch(() => {});
      const st = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).status, id).catch(() => 0);
      check('作ったページを消した /' + id, st === 404, 'status ' + st);
    }
    await browser.close();
    console.log('\n結果: ' + (fails ? fails + ' 件失敗' : 'すべて通りました'));
    process.exit(fails ? 1 : 0);
  }
})();
