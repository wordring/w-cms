// 整理の「📎 ほかのファイル」・欄の名前「製品名称」・区分の印を外したこと（2026-10-08——ext/toho/filing_files.go・assets/app.js の
// buildFilingFiles）を画面から確かめる。
//
// 利用者:「図面以外のファイルでも、ここで相手を検索して追加できるとありがたいですね」「『顧客名』『装置名称』『（加工）製品名称』が
// ページ名の意味ではないでしょうか？」「試作や見積もりによるフォルダ分けが無くなったので、整理ブロックで入力する必要はなくなりました」
//
//   ① 図面の無い記録でも、添付があれば「📁 整理（ファイル N）」が出る・欄に添付が並ぶ（受信原本の .eml は並ばない）・実行ボタンは無い
//   ② 表示しているページ「まだありません」→ 行き先を探す（装置名）で装置のページ（📁）と加工製品が出る
//   ③ 装置のページを押すと「✓ 足しました」・装置のページの末尾にファイル表示・「表示しているページ」に出る
//   ④ 同じページへもう一度押すと「⚠ 既にあります」
//   ⑤ 図面の欄の3つ目は「製品名称」・区分の印は無い
//
// 当て先は全部自分で作って最後に消します（取引先の下の【E2E】の会社・通信箱の下の記録）。本物の加工製品には触りません。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-filing-files.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, childrenOf, bodyOf, findMailbox } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};
const CUSTOMER = '【E2E】ファイルの会社';
const MACHINE = '【E2E】ファイルの装置';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', (e) => errs.push(String(e)));
  let record = '', record2 = '', cust = '';
  const wipe = async (id) => {
    for (const c of await childrenOf(page, id)) await wipe(c.ID);
    await deletePage(page, id);
  };
  const cleanup = async () => {
    for (const id of [record, record2, cust]) if (id) await wipe(id);
    record = record2 = cust = '';
  };
  // upload は記録にファイルを1つ添付する（編集ロックを取って・放して）。
  const upload = (pid, name, text) => page.evaluate(async (a) => {
    const lr = await fetch('/api/lock?id=' + a.pid, { method: 'POST' });
    const lj = await lr.json().catch(() => ({}));
    const fd = new FormData();
    fd.append('page_id', a.pid);
    fd.append('file', new File([a.text], a.name, { type: 'application/octet-stream' }));
    const r = await fetch('/api/upload-file', { method: 'POST', body: fd, headers: { 'X-Lock-Token': lj.token || '' } });
    const d = await r.json().catch(() => ({}));
    await fetch('/api/unlock?id=' + a.pid + '&token=' + encodeURIComponent(lj.token || ''), { method: 'POST' });
    return { status: r.status, d };
  }, { pid, name, text });
  try {
    await login(page, BASE);
    const box = await findMailbox(page);
    const top = await childrenOf(page, '000000');
    const partners = (top.find(c => (c.Title || '').trim() === '取引先') || {}).ID || '';
    if (!box || !partners) {
      console.log('通信箱か取引先が無いので飛ばします');
      await browser.close();
      process.exit(0);
    }
    const partnersBefore = (await childrenOf(page, partners)).map(c => c.ID).join(',');

    // 取引先／【E2E】ファイルの会社／加工製品／【E2E】ファイルの装置／【E2E】ファイルの部品
    cust = await makePage(page, '<h1>' + CUSTOMER + '</h1>', partners);
    const prodBox = await makePage(page, '<h1>加工製品</h1>', cust);
    const folder = await makePage(page, '<h1>' + MACHINE + '</h1><p>装置の説明</p>', prodBox);
    // 加工製品ページと見なされるのはタグ（図面番号など）を持つページだけ（productListRows）。
    const part = await makePage(page, '<h1>【E2E】ファイルの部品</h1><dl data-type="tags"><dt>図面番号</dt><dd>E2E-FFL-9</dd></dl><p>x</p>', folder);

    // 図面の無い記録（添付は 3D データと受信原本）。
    record = await makePage(page, '<h1>【E2E】ほかのファイル</h1><dl data-type="tags"><dt>向き</dt><dd>受信</dd>' +
      '<dt>チャネル</dt><dd>メール</dd><dt>差出人</dt><dd>試験 &lt;e2e-ffiles@invalid.example&gt;</dd></dl><p>本文</p>', box);
    const up1 = await upload(record, '取付データ.x_t', 'solid');
    const up2 = await upload(record, 'mail.eml', 'From: x');
    check('当て先を作れた', !!part && up1.status === 200 && up2.status === 200, JSON.stringify([up1, up2]));

    // ①
    await page.goto(BASE + '/' + record);
    const open = page.locator('#w-editor-content .filing-open');
    await open.waitFor({ timeout: 10000 }).catch(() => {});
    const label = (await open.textContent().catch(() => '')) || '';
    check('① 図面が無くても添付があれば「整理（ファイル 1）」が出る', label.includes('ファイル 1'), label);
    await open.click();
    const fileCards = page.locator('#w-editor-content .filing-file');
    await fileCards.first().waitFor({ timeout: 10000 }).catch(() => {});
    const names = await page.locator('#w-editor-content .filing-file .filing-no').allTextContents();
    check('① 添付が並ぶ（受信原本の .eml は並ばない）', names.length === 1 && names[0].includes('取付データ.x_t'), names.join(' | '));
    check('① 実行ボタンは無い（ファイルはそれぞれ足す）', (await page.locator('#w-editor-content .filing-run').count()) === 0);

    // ②
    const shown = fileCards.first().locator('.filing-file-shown');
    await page.waitForFunction(() => { const s = document.querySelector('#w-editor-content .filing-file-shown'); return s && !s.textContent.includes('調べています'); }, null, { timeout: 15000 }).catch(() => {});
    check('② 表示しているページ「まだありません」', ((await shown.textContent()) || '').includes('まだありません'), await shown.textContent());
    await fileCards.first().locator('.filing-search-input').fill('ファイルの装置');
    const pickFolder = fileCards.first().locator('.filing-file-add', { hasText: '📁' });
    await pickFolder.first().waitFor({ timeout: 10000 }).catch(() => {});
    const opts = await fileCards.first().locator('.filing-file-add').allTextContents();
    check('② 行き先を探すと装置のページ（📁）と加工製品が出る', opts.some((t) => t.includes('📁') && t.includes('/' + folder)) && opts.some((t) => t.includes('/' + part)), opts.join(' | '));

    // ③
    await pickFolder.first().click();
    const note = fileCards.first().locator('.filing-file-note');
    await page.waitForFunction(() => { const n = document.querySelector('#w-editor-content .filing-file-note'); return n && !n.hidden && n.textContent; }, null, { timeout: 10000 }).catch(() => {});
    const n3 = (await note.textContent()) || '';
    const fbody = await bodyOf(page, folder);
    check('③ 装置のページを押すと「✓ 足しました」', n3.startsWith('✓') && n3.includes(MACHINE), n3);
    check('③ 装置のページの末尾にファイル表示', /data-type="file-view"[^>]*data-ref="\d{6}-[0-9a-z]+"|data-ref="\d{6}-[0-9a-z]+"[^>]*data-type="file-view"/.test(fbody) &&
      fbody.indexOf('装置の説明') < fbody.indexOf('file-view'), fbody.replace(/\s+/g, ' ').slice(0, 200));
    check('③ 「表示しているページ」に出る', ((await shown.textContent()) || '').includes(MACHINE), await shown.textContent());

    // ④
    await pickFolder.first().click();
    await page.waitForTimeout(800);
    const n4 = (await note.textContent()) || '';
    check('④ 同じページへもう一度押すと「⚠ 既にあります」', n4.startsWith('⚠') && n4.includes('既にあります'), n4);

    // ④b 開き直すと、表示しているページはサーバーが探した答え（本文の data-ref）から出る。
    await page.goto(BASE + '/' + record);
    await page.locator('#w-editor-content .filing-open').click();
    await page.waitForFunction(() => { const s = document.querySelector('#w-editor-content .filing-file-shown'); return s && !s.textContent.includes('調べています'); }, null, { timeout: 15000 }).catch(() => {});
    const shown2 = (await page.locator('#w-editor-content .filing-file-shown').first().textContent()) || '';
    check('④b 開き直しても「表示しているページ」に装置のページ（サーバーが探す）', shown2.includes(MACHINE), shown2);

    // ⑤ 図面の欄（別の記録に解析で作ったような図面のページを置く）。
    record2 = await makePage(page, '<h1>【E2E】図面の欄</h1><dl data-type="tags"><dt>向き</dt><dd>受信</dd><dt>チャネル</dt><dd>メール</dd></dl><p>本文</p>', box);
    await makePage(page, '<h1>E2E-FFL-1 【E2E】部品</h1><section><h2>図面</h2><dl data-type="tags"><dt>図面番号</dt><dd>E2E-FFL-1</dd><dt>図面名称</dt>' +
      '<dd>【E2E】部品</dd><dt>装置名称</dt><dd>' + MACHINE + '</dd><dt>客先</dt><dd>' + CUSTOMER + '</dd></dl></section>', record2);
    await page.goto(BASE + '/' + record2);
    await page.locator('#w-editor-content .filing-open').click();
    await page.locator('#w-editor-content .filing-card').first().waitFor({ timeout: 10000 }).catch(() => {});
    const fieldNames = await page.locator('#w-editor-content .filing-card .filing-field > span').allTextContents();
    const head = (await page.locator('#w-editor-content .filing-head').first().textContent().catch(() => '')) || '';
    check('⑤ 欄は「顧客名・装置名称・製品名称」（図面名称ではない）', fieldNames.slice(0, 3).join('・') === '顧客名・装置名称・製品名称' && head.includes('製品名称'),
      fieldNames.join('・') + ' / ' + head);
    check('⑤ 区分の印は無い', (await page.locator('#w-editor-content .filing-card .filing-kinds:not(.filing-rules)').count()) === 0 && !fieldNames.includes('区分'));

    check('JSエラーなし', errs.length === 0, errs.join(' | '));
    await cleanup();
    const partnersAfter = (await childrenOf(page, partners)).map(c => c.ID).join(',');
    check('取引先の子が元どおり', partnersAfter === partnersBefore);
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    await cleanup().catch(() => {});
    await browser.close();
    console.log(fails === 0 ? '\n結果: すべて通りました' : '\n結果: ' + fails + ' 件落ちました');
    process.exit(fails === 0 ? 0 : 1);
  }
})();
