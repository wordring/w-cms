// 発注部材表の「発注書を作る」が、押したら本当に送るかを**ブラウザから**確かめる（2026-10-02）。
//
// ⚠ **これは実機の失敗から生まれた番人です。** 利用者:「発注部材表から発注書が作れません」——2026-09-28 に
// 「発注書を作る」の欄を表の外（包む節の中）へ移したとき、画面は「欄を含む表」（`form.closest('table')`）を
// 探したままで、**押しても黙って何もしていませんでした**（それから1枚も発注書ができていなかった）。
// 既にあった `verify-order-draft.js` は欄の**置き場所**は見ていましたが、**押してはいませんでした**。
//
// ⚠ **本物の発注書は作りません**——`/api/our-order/new` をブラウザの中で止め（`page.route`）、送られた
// 中身だけを見ます。発注部材表は**自分で作った別のページ**に置くので、本物の「発注」ページには触りません。
// 最後にそのページを消します。
//
// 使い方: WCMS_BASE=https://localhost:8443 node verify-order-draft-create.js
const { chromium } = require('playwright');
const { login, makePage, deletePage } = require('./lib');
const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

// 発注部材表は機械が節で包んで作る形（`draftBoxOf`）と同じにします。
const BODY = '<h1>【E2E】発注部材表から発注書を作る</h1>' +
  '<section><table><caption>発注部材表</caption><tbody>' +
  '<tr><th>弊社品番</th><th>受注</th><th>種類</th><th>材質</th><th>形状</th><th>寸法</th><th>数量</th><th>単位</th><th>単価</th><th>備考</th><th>状態</th></tr>' +
  '<tr><td></td><td></td><td>材料</td><td>E2E-CREATE-A</td><td>板</td><td>t3.2</td><td>4</td><td>枚</td><td></td><td></td><td></td></tr>' +
  '</tbody></table></section>';

(async () => {
  const browser = await chromium.launch();
  const ctx = await browser.newContext({ ignoreHTTPSErrors: true });
  const page = await ctx.newPage();
  const errs = [];
  page.on('pageerror', (e) => errs.push(e.message));
  await login(page, BASE);

  let bad = 0;
  const id = await makePage(page, BODY);
  const run = async () => {
    if (!id) {
      console.log('✗ 当て先のページを作れません');
      bad++;
      return;
    }
    await page.goto(BASE + '/' + id);
    await page.waitForTimeout(800);
    const form = page.locator('.draft-form[data-draft-page="' + id + '"]');
    if (await form.count() !== 1) {
      console.log('✗ 「発注書を作る」の欄が出ていません（' + await form.count() + ' 個）');
      bad++;
      return;
    }
    // 送らずに止める——送られた中身を控え、失敗を返す（ページは作られない）。
    let sent = null;
    await page.route('**/api/our-order/new', (route) => {
      sent = route.request().postDataJSON();
      route.fulfill({
        status: 409, contentType: 'application/json',
        body: JSON.stringify({ success: false, message: 'E2E: 送らずに止めました' }),
      });
    });
    await form.locator('[data-draft="supplier"]').fill('みなと商店');
    // 差出人は候補があれば最初の人を選ぶ（候補が無ければ「選んでください」と言われるのが正しい）。
    const values = await form.locator('[data-unorder="signer"] option').evaluateAll(
      (os) => os.map((o) => o.value).filter((v) => v));
    if (values.length) await form.locator('[data-unorder="signer"]').selectOption(values[0]);
    await form.locator('[data-draft-go]').click();
    await page.waitForTimeout(800);
    const said = (await page.locator('[data-draft-result]').first().textContent() || '').trim();

    if (!values.length) {
      // 候補が無い環境: 表を見つけたうえで、差出人を求める文が出ること（黙って終わらない）。
      if (said.includes('差出人を選んでください')) {
        console.log('✓ 差出人の候補が無い環境——押すと「差出人を選んでください」と言う（黙って終わらない）');
      } else {
        console.log('✗ 押しても何も言いません（差出人の候補が無い環境・欄の文: ' + JSON.stringify(said) + '）');
        bad++;
      }
      return;
    }
    if (!sent) {
      console.log('✗ 「発注書を作る」を押しても送られていません（欄の文: ' + JSON.stringify(said) + '）');
      bad++;
      return;
    }
    const lines = sent.lines || [];
    if (lines.length !== 1 || lines[0].material !== 'E2E-CREATE-A' || lines[0].quantity !== '4') {
      console.log('✗ 送られた行が表と合いません: ' + JSON.stringify(lines));
      bad++;
    } else console.log('✓ 押すと発注部材表の行が送られる（' + lines.length + ' 行）');
    if (sent.draft_page !== id || sent.draft_index !== '1' || sent.supplier !== 'みなと商店' || !sent.signer) {
      console.log('✗ どの表から・仕入先・差出人が送られていません: ' + JSON.stringify(
        { draft_page: sent.draft_page, draft_index: sent.draft_index, supplier: sent.supplier, signer: sent.signer }));
      bad++;
    } else console.log('✓ どの表から（/' + id + ' の1枚目）・仕入先・差出人も送られる');
    if (!said.includes('E2E: 送らずに止めました')) {
      console.log('✗ 断られたことが欄に出ていません: ' + JSON.stringify(said));
      bad++;
    } else console.log('✓ 断られたら欄にその理由が出る');
  };
  try {
    await run();
  } finally {
    await page.unroute('**/api/our-order/new').catch(() => {});
    if (id) await deletePage(page, id);
  }

  if (errs.length) {
    console.log('✗ JSエラー: ' + errs.join(' / '));
    bad++;
  } else console.log('✓ JSエラーなし');

  console.log(bad === 0 ? '\n結果: 合格' : '\n結果: ' + bad + '件の不合格');
  await browser.close();
  process.exitCode = bad === 0 ? 0 : 1;
})();
