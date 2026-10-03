// 送る欄の2つの足し（2026-10-03）を画面から確かめる。
//
//   ① 見積書の送る欄の添付に「御見積書 ○.pdf（送るときに作ります）」が**印つき**で並ぶ。外して送ると skip_generated が付く
//      ——利用者:「見積書ページのメール作成欄は見積書PDFも他と同じように表示し、ただし最初から添付に入っているようにすると
//      わかりやすい」（ext/comm/compose.go の Generated・assets/mail-compose.js）
//   ② 宛先・CC の下に「連絡帳」の欄（候補の一覧）。選ぶと宛先（または CC）の末尾にアドレスが足される・同じものは足さない
//      ——利用者:「宛先は連絡帳から候補を取得してコンボボックスで出して欲しい」（ext/comm/contacts/address_book.go）
//
// ⚠ **本物のメールは出しません**——送る口（/api/mail/send）と下書きの保存（/api/mail/draft）は画面の手前で止めて、送ろうとした
//    中身だけを見ます。当て先の見積書は自分で作り（トップ直下・【E2E】）、最後に消します。連絡帳に候補が無ければ②は飛ばします。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-mail-compose-extras.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

const EST = '<h1>見積　【E2E】客先</h1><dl data-type="tags"><dt>見積番号</dt><dd>E2E</dd><dt>見積先</dt><dd>【E2E】客先</dd></dl>' +
  '<table><caption>見積明細</caption><tbody><tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>単位</th><th>単価</th><th>備考</th></tr>' +
  '<tr><td></td><td>E2E-MC-1</td><td>【E2E】部品</td><td>2</td><td>個</td><td>500</td><td></td></tr></tbody></table>' +
  '<section><h2>備考</h2><p><br/></p></section>';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1400, height: 1000 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  // 送る・下書きを保存する口は止める（中身だけ受け取る）。
  let sentBody = null;
  await page.route('**/api/mail/send', (route) => {
    sentBody = route.request().postDataJSON();
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ success: false, message: 'E2E で止めました' }) });
  });
  await page.route('**/api/mail/draft', (route) =>
    route.fulfill({ status: 200, contentType: 'application/json', body: JSON.stringify({ success: false, message: 'E2E で止めました' }) }));
  let est = '';
  try {
    await login(page, BASE);
    est = await makePage(page, EST);
    check('当て先の見積書を作れた', !!est);
    await page.goto(BASE + '/' + est);
    await page.waitForTimeout(800);
    await page.locator('#w-editor-content details.estimate-mail > summary').click();
    await page.waitForSelector('.mail-compose [data-mc="to"]', { timeout: 10000 }).catch(() => {});

    // ①
    const gen = page.locator('.mail-compose .mc-attach-generated input[data-mc-generated]');
    check('① 添付に見積書のPDFが並ぶ', await gen.count() === 1);
    check('① 最初から印が付いている', await gen.isChecked().catch(() => false));
    const genText = (await page.locator('.mail-compose .mc-attach-generated').textContent().catch(() => '')) || '';
    check('① 名前は「御見積書 ページ番号.pdf（送るときに作ります）」', genText.includes('御見積書 ' + est + '.pdf') && genText.includes('送るときに作ります'), genText);

    // ②
    const book = page.locator('.mail-compose [data-mc="book"]');
    await book.waitFor({ timeout: 5000 }).catch(() => {});
    const first = await page.evaluate(() => {
      const inp = document.querySelector('.mail-compose [data-mc="book"]');
      const dl = inp && document.getElementById(inp.getAttribute('list'));
      const o = dl && dl.querySelector('option');
      return o ? o.value : '';
    });
    if (!first) {
      console.log('— 連絡帳にメールアドレスのある連絡先が無いので②は飛ばします');
    } else {
      const addr = (first.match(/<([^>]+)>$/) || [])[1];
      const to = page.locator('.mail-compose [data-mc="to"]');
      await to.fill('');
      await book.fill(first);
      await book.dispatchEvent('input');
      check('② 連絡帳の候補を選ぶと宛先にアドレスが入る', (await to.inputValue()) === addr, await to.inputValue());
      check('② 選んだら候補の欄は空に戻る', (await book.inputValue()) === '');
      await book.fill(first);
      await book.dispatchEvent('input');
      check('② 同じアドレスは二度足さない', (await to.inputValue()) === addr, await to.inputValue());
      await page.locator('.mail-compose .mc-book-target').selectOption('cc');
      await book.fill(first);
      await book.dispatchEvent('input');
      check('② 「CCへ」を選ぶと CC に入る', (await page.locator('.mail-compose [data-mc="cc"]').inputValue()) === addr);
    }

    // ①（送る中身）——外して送ると skip_generated。宛先が空なら仮の宛先を入れる（送る口は止めてある）。
    const toNow = page.locator('.mail-compose [data-mc="to"]');
    if (!(await toNow.inputValue()).trim()) await toNow.fill('e2e@example.invalid');
    const sendBtn = page.locator('.mail-compose [data-mc-send]');
    if (await sendBtn.isDisabled()) {
      console.log('— メールにサインインしていないので送るボタンが押せません（送る中身の確かめは飛ばします）');
    } else {
      await gen.uncheck();
      page.once('dialog', (d) => d.accept());
      await sendBtn.click();
      await page.waitForTimeout(800);
      check('① 印を外して送ると skip_generated が付く（PDF を作らない）', !!sentBody && sentBody.skip_generated === true, JSON.stringify(sentBody || {}).slice(0, 120));
      await gen.check();
      sentBody = null;
      page.once('dialog', (d) => d.accept());
      await sendBtn.click();
      await page.waitForTimeout(800);
      check('① 印のまま送ると skip_generated は付かない', !!sentBody && sentBody.skip_generated === false);
    }
    check('JSエラーなし', errs.length === 0, errs.join(' | '));
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    if (est) {
      await deletePage(page, est).catch(() => {});
      const st = await page.evaluate(async (pid) => (await fetch('/api/load?id=' + pid)).status, est).catch(() => 0);
      check('作ったページを消した /' + est, st === 404, 'status ' + st);
    }
    await browser.close();
    console.log('\n結果: ' + (fails ? fails + ' 件失敗' : 'すべて通りました'));
    process.exit(fails ? 1 : 0);
  }
})();
