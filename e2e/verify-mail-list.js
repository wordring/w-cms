// メールの一覧（assets/mails.html・2026-09-29）を画面から確かめる。
//
// 利用者:「過去のメールを一覧で見る方法を作れませんか？受信送信両方、そしてIn reply toによる返信の連鎖も一覧性があるように」。
//
//   ① 受信も送信も並ぶ・返信は元のメールの下に字下げして並ぶ（返信の返信はさらに下）
//   ② 絞り込み（向き）で当たらない行は、同じスレッドなら薄く残る
//   ③ 「スレッドに束ねない」で日付の新しい順・字下げなし
//   ④ スレッドの頭の ▾ で返信を畳める
//
// 当て先はメールの記録（チャネル＝メール）を3枚自分で作り、最後に消します（件名に一意の印を入れて本物と混ぜない）。
// 使い方: WCMS_BASE=https://localhost:8443 node verify-mail-list.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
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
    ids.push(await makePage(page, rec(MARK + ' 見積のお願い', [['チャネル', 'メール'], ['向き', '受信'],
      ['受信日時', '2020-01-01T10:00:00+09:00'], ['差出人', 'みなと商店 &lt;minato@example.jp&gt;'], ['メッセージID', '&lt;' + MARK + '-a@x&gt;']])));
    ids.push(await makePage(page, rec('Re: ' + MARK + ' 見積のお願い', [['チャネル', 'メール'], ['向き', '送信'],
      ['送信日時', '2020-01-02T09:00:00+09:00'], ['宛先', 'みなと商店 &lt;minato@example.jp&gt;'], ['メッセージID', '&lt;' + MARK + '-b@x&gt;'],
      ['返信元メッセージID', '&lt;' + MARK + '-a@x&gt;'], ['対応', '不要']])));
    ids.push(await makePage(page, rec('Re: Re: ' + MARK + ' 見積のお願い', [['チャネル', 'メール'], ['向き', '受信'],
      ['受信日時', '2020-01-03T09:00:00+09:00'], ['差出人', 'みなと商店 &lt;minato@example.jp&gt;'], ['メッセージID', '&lt;' + MARK + '-c@x&gt;'],
      ['返信元メッセージID', '&lt;' + MARK + '-b@x&gt;']])));
    check('当て先を3枚作れた', ids.every(Boolean), ids.join(' '));

    // 入口——上のメニューに「✉️ メール一覧」が出る（通信の拡張が載っているとき）
    await page.goto(BASE + '/');
    await page.waitForTimeout(800);
    check('上のメニューに「✉️ メール一覧」が出る', await page.locator('#w-mails-link').isVisible());

    await page.goto(BASE + '/assets/mails.html');
    await page.waitForFunction(() => document.querySelectorAll('#ml-body tr').length > 0, null, { timeout: 20000 });
    await page.fill('#ml-q', MARK);
    await page.waitForTimeout(300);
    const rows = () => page.evaluate(() => Array.from(document.querySelectorAll('#ml-body tr')).map(tr => ({
      subject: tr.querySelector('td.subject a').textContent,
      dir: tr.children[1].textContent.trim(),
      indent: parseInt(tr.querySelector('td.subject').style.paddingLeft || '0', 10),
      dim: tr.classList.contains('dim'),
    })));

    // ① スレッド——頭（受信）→ 返信（送信）→ 返信の返信（受信）、字下げが深くなる
    let r = await rows();
    check('3通が1つのスレッドに並ぶ', r.length === 3 && (await page.textContent('#ml-count')).includes('1 スレッド'), JSON.stringify(r));
    check('受信も送信も並ぶ', r.some(x => x.dir.includes('受信')) && r.some(x => x.dir.includes('送信')));
    check('返信は元のメールの下に字下げ（返信の返信はさらに下）',
      r.length === 3 && r[0].subject.startsWith(MARK) && r[1].subject.startsWith('Re: ' + MARK) &&
      r[2].subject.startsWith('Re: Re: ') && r[0].indent < r[1].indent && r[1].indent < r[2].indent,
      JSON.stringify(r.map(x => x.indent)));

    // ② 向きで絞る——当たらない受信は同じスレッドなので薄く残る
    await page.selectOption('#ml-dir', '送信');
    await page.waitForTimeout(300);
    r = await rows();
    check('向きで絞ると、当たらない行は薄く残る', r.length === 3 && r.filter(x => !x.dim).length === 1 && !r[1].dim,
      JSON.stringify(r.map(x => x.dim)));
    await page.selectOption('#ml-dir', '');

    // ④ 畳む
    await page.click('#ml-body tr:first-child button.fold');
    await page.waitForTimeout(200);
    check('スレッドの頭の ▾ で返信を畳める', (await rows()).length === 1);
    await page.click('#ml-body tr:first-child button.fold');

    // ③ 束ねない——日付の新しい順・字下げなし
    await page.check('#ml-flat');
    await page.waitForTimeout(300);
    r = await rows();
    check('「スレッドに束ねない」で新しい順・字下げなし',
      r.length === 3 && r[0].subject.startsWith('Re: Re: ') && r.every(x => x.indent === 0), JSON.stringify(r));
  } finally {
    for (const id of ids) await deletePage(page, id);
  }
  check('JavaScript エラーなし', errs.length === 0, errs.join(' / '));
  console.log(fails === 0 ? '\n結果: 合格' : '\n結果: ' + fails + ' 件の不合格');
  await browser.close();
  process.exitCode = fails === 0 ? 0 : 1;
})();
