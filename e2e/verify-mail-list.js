// 通信箱の見え方「スレッド」（2026-10-29・ext/comm/assets/comm-threads.js・view_unhandled.go）を画面から確かめる。
// それまでのメール一覧（ext/comm/assets/pages/mails.html・2026-09-29〜10-28）を通信箱へ入れたもの。
//
// 利用者:「メール一覧は通信箱に統合して、見え方を選択できるようにしたいです。メール一覧のような見え方も欲しいですが、
// 未処理は通知と同じで良いのでは？」——答え: 未処理は通信箱と同じ・スレッドに FAX・電話・メモも入れる・メール一覧のページはなくす。
//
//   ① 上のメニューの「✉️ 通信の一覧」と、メール一覧の古い住所は、通信箱の「スレッド」へ送る
//   ② 受信も送信も、FAX も1つのスレッドに並ぶ・返信は元の記録の下に字下げ（返信の返信はさらに下）——木は 親ページID
//   ③ チャネルで絞ると、当たらない行は同じスレッドなら薄く残る
//   ④ 「未処理だけ」は通信箱と同じ決まり（対応が未処理）——対応が「不要」の送信は外れる、未処理の受信・FAX は残る
//   ⑤ スレッドの頭の ▾ で畳める・「スレッドに束ねない」で日付の新しい順・字下げなし
//   ⑥ 選んだ見え方は端末に憶える（読み直してもスレッド）・「作業待ち」に戻せる
//
// 当て先は通信記録を4枚自分で作り、最後に消します（件名に一意の印を入れて本物と混ぜない）。
// 使い方: WCMS_BASE=https://localhost:9443 node verify-mail-list.js
const { chromium } = require('playwright');
const { login, makePage, deletePage, findMailbox } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

let fails = 0;
const check = (label, ok, note = '') => {
  console.log((ok ? '✓ ' : '✗ ') + label + (note ? '  ' + note : ''));
  if (!ok) fails++;
};

const MARK = 'E2E' + Date.now().toString(36).toUpperCase();
const rec = (title, pairs) => '<h1>' + title + '</h1><dl data-type="tags">' +
  pairs.map(([k, v]) => '<dt>' + k + '</dt><dd>' + v + '</dd>').join('') + '</dl>';

(async () => {
  const browser = await chromium.launch();
  const page = await browser.newPage({ ignoreHTTPSErrors: true, viewport: { width: 1280, height: 900 } });
  const errs = [];
  page.on('pageerror', e => errs.push(String(e)));
  const ids = [];
  try {
    await login(page, BASE);
    const box = await findMailbox(page);
    if (!box) {
      console.log('通信箱が無いので飛ばします');
      await browser.close();
      process.exit(0);
    }
    ids.push(await makePage(page, rec(MARK + ' 見積のお願い', [['チャネル', 'メール'], ['向き', '受信'],
      ['受信日時', '2020-01-01T10:00:00+09:00'], ['差出人', 'みなと商店 &lt;minato@example.jp&gt;'], ['対応', '未処理']])));
    ids.push(await makePage(page, rec('Re: ' + MARK + ' 見積のお願い', [['チャネル', 'メール'], ['向き', '送信'],
      ['送信日時', '2020-01-02T09:00:00+09:00'], ['宛先', 'みなと商店 &lt;minato@example.jp&gt;'], ['親ページID', ids[0]], ['対応', '不要']])));
    ids.push(await makePage(page, rec('Re: Re: ' + MARK + ' 見積のお願い', [['チャネル', 'メール'], ['向き', '受信'],
      ['受信日時', '2020-01-03T09:00:00+09:00'], ['差出人', 'みなと商店 &lt;minato@example.jp&gt;'], ['親ページID', ids[1]], ['対応', '済']])));
    // メールへの返事が FAX で来た——同じスレッドに並ぶ（親ページID の鎖はチャネルを問わない）。
    ids.push(await makePage(page, rec(MARK + ' FAX の返事', [['チャネル', 'FAX'], ['向き', '受信'],
      ['受信日時', '2020-01-04T09:00:00+09:00'], ['親ページID', ids[2]], ['対応', '未処理']])));
    check('当て先を4枚作れた', ids.every(Boolean), ids.join(' '));

    // ① 入口
    await page.goto(BASE + '/');
    await page.waitForTimeout(800);
    const link = page.locator('#w-mails-link');
    check('上のメニューに「✉️ 通信の一覧」が出る', (await link.isVisible()) && ((await link.textContent()) || '').includes('通信の一覧'));
    await page.goto(BASE + '/assets/ext/comm/pages/mails.html');
    await page.waitForURL(new RegExp('/' + box + '\\?view=threads'), { timeout: 10000 }).catch(() => {});
    check('メール一覧の古い住所は、通信箱の「スレッド」へ送る', page.url().includes('/' + box + '?view=threads'), page.url());

    const threads = page.locator('#w-editor-content .comm-threads');
    await page.waitForFunction(() => document.querySelectorAll('#w-editor-content .comm-threads tbody tr').length > 0, null, { timeout: 20000 }).catch(() => {});
    check('スレッドの見え方が開いている（作業待ちは隠れる）', (await threads.isVisible()) &&
      !(await page.locator('#w-editor-content .comm-queue').isVisible()));
    await threads.locator('.comm-threads-q').fill(MARK);
    await page.waitForTimeout(300);
    const rows = () => page.evaluate(() => Array.from(document.querySelectorAll('#w-editor-content .comm-threads tbody tr')).map(tr => ({
      subject: tr.querySelector('td.subject a').textContent,
      dir: tr.querySelector('td.dir-cell').textContent.trim(),
      indent: parseInt(tr.querySelector('td.subject').style.paddingLeft || '0', 10),
      dim: tr.classList.contains('dim'),
      handled: tr.querySelector('td.handled').textContent.trim(),
    })));

    // ②
    let r = await rows();
    check('4つが1つのスレッドに並ぶ', r.length === 4 && ((await threads.locator('.comm-threads-count').textContent()) || '').includes('1 スレッド'), JSON.stringify(r));
    check('受信も送信も FAX も並ぶ', r.some(x => x.dir.includes('受信')) && r.some(x => x.dir.includes('送信')) && r.some(x => x.dir.includes('📠')));
    check('返信は元の記録の下に字下げ（返信の返信はさらに下）',
      r.length === 4 && r[0].subject.startsWith(MARK) && r[1].subject.startsWith('Re: ' + MARK) &&
      r[2].subject.startsWith('Re: Re: ') && r[3].subject.includes('FAX') &&
      r[0].indent < r[1].indent && r[1].indent < r[2].indent && r[2].indent < r[3].indent,
      JSON.stringify(r.map(x => x.indent)));

    // ③ チャネルで絞る
    await threads.locator('.comm-threads-channel').selectOption('FAX');
    await page.waitForTimeout(300);
    r = await rows();
    check('チャネルで絞ると、当たらない行は薄く残る', r.length === 4 && r.filter(x => !x.dim).length === 1 && !r[3].dim,
      JSON.stringify(r.map(x => x.dim)));
    await threads.locator('.comm-threads-channel').selectOption('');

    // ④ 未処理は通信箱と同じ決まり
    await threads.locator('.comm-threads-flat').check();
    await threads.locator('.comm-threads-unhandled').check();
    await page.waitForTimeout(300);
    r = await rows();
    check('「未処理だけ」は対応が未処理のもの（受信のメールと FAX・送信の「不要」と「済」は外れる）',
      r.length === 2 && r.every(x => x.handled.includes('未処理')) && r.some(x => x.subject.includes('FAX')), JSON.stringify(r));
    await threads.locator('.comm-threads-unhandled').uncheck();

    // ⑤ 束ねない・畳む
    await page.waitForTimeout(300);
    r = await rows();
    check('「スレッドに束ねない」で新しい順・字下げなし', r.length === 4 && r[0].subject.includes('FAX') && r.every(x => x.indent === 0), JSON.stringify(r));
    await threads.locator('.comm-threads-flat').uncheck();
    await page.waitForTimeout(300);
    await threads.locator('tbody tr:first-child button.fold').click();
    await page.waitForTimeout(200);
    check('スレッドの頭の ▾ で返信を畳める', (await rows()).length === 1);

    // ⑥ 見え方を憶える
    await page.goto(BASE + '/' + box);
    await page.waitForTimeout(1200);
    check('読み直してもスレッドのまま（端末に憶える）', await threads.isVisible());
    await page.locator('#w-editor-content .comm-view-switch [data-comm-view="queue"]').click();
    await page.waitForTimeout(300);
    check('「作業待ち」に戻せる', (await page.locator('#w-editor-content .comm-queue').isVisible()) && !(await threads.isVisible()));
    await page.goto(BASE + '/' + box);
    await page.waitForTimeout(1200);
    check('読み直すと作業待ち', await page.locator('#w-editor-content .comm-queue').isVisible());
  } catch (e) {
    check('例外なく流れた', false, String(e));
  } finally {
    for (const id of ids.reverse()) await deletePage(page, id);
  }
  check('JavaScript エラーなし', errs.length === 0, errs.join(' / '));
  console.log(fails === 0 ? '\n結果: 合格' : '\n結果: ' + fails + ' 件の不合格');
  await browser.close();
  process.exitCode = fails === 0 ? 0 : 1;
})();
