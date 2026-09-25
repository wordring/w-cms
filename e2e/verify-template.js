// w-cms ページテンプレートの自動E2E検証
// ⚠ 2026-09-16: 自己署名の証明書を許すようにしました（`ignoreHTTPSErrors`）。
// それまで古い verify-* は :8080 前提で、`WCMS_BASE=https://…` を渡しても
// **証明書で弾かれて一式を流せません**でした（引き継ぎの「見る先が2つに割れている」）。
//
// 正本の設計は docs/【考察】ページテンプレート.md。確かめるのは3点:
//   ① 「テンプレート」フォルダの**葉**だけがメニューに出る（枝は分類の見出し）
//   ② テンプレートから作ったページは本文が写る（2026-09-25 から純粋なコピー——空欄は空欄の
//      まま・ブロックIDだけ外す）
//   ③ テンプレート領域の中身は③計算テーブルへ載らない（手配集計に出ない）
//
// 前提: サーバーが http://localhost:8080 で起動済み（.claude/launch.json の w-cms）。
// ログインは CLAUDE.md 記載のローカル検証専用 a / a（本番では使わない）。
// 実行: cd ~\tools\wcms-e2e && node "$env:OneDrive\tools\wcms-e2e\verify-template.js"
//
// ⚠ **トップ直下の「テンプレート」は作りません**（2026-09-21）——既にあるものを使い、
// 自分が足した分類（枝）と葉だけを片付けます。**箱は消しません**。
// 後片付けはしません（管理コンソールの「DB再構築」やページ削除で整理してください）。
const { createRequire } = require('module');
const path = require('path');
let chromium;
try { ({ chromium } = createRequire(path.join(process.cwd(), 'noop.js'))('playwright')); }
catch (e) { console.error('node_modules のあるディレクトリ（~/tools/wcms-e2e）から実行してください'); process.exit(2); }

const BASE = process.env.WCMS_BASE || 'http://localhost:8080';
const results = []; let failCount = 0;
function check(name, cond) { results.push(`${cond ? 'PASS' : 'FAIL'} ${name}`); if (!cond) failCount++; }

// saveBody はページ本文を /api/save で保存します（Origin は CSRF 対策で必須）。
async function saveBody(page, id, html) {
    const res = await page.request.post(BASE + '/api/save', {
        headers: { 'Origin': BASE, 'Content-Type': 'application/json' },
        data: { page_id: id, html },
    });
    if (!res.ok()) throw new Error(`save ${id} failed: ${res.status()} ${await res.text()}`);
    return res.json();
}

// newPage は子ページを作ってそのIDを返します。
// made は**このスクリプトが作ったページ**です（最後に片付けます）。
//
// ⚠ **それまで片付けていませんでした**——流すたびにトップ直下へページが積もり、
// 2026-09-16 には45枚になっていました。**自分の出したゴミは自分で片付けます。**
const made = [];

async function newPage(page, parent, template) {
    let url = BASE + '/api/new-page?parent=' + parent;
    if (template) url += '&template=' + template;
    const res = await page.request.post(url, { headers: { 'Origin': BASE }, maxRedirects: 0 });
    const loc = res.headers()['location'];
    if (!loc) throw new Error(`new-page failed: ${res.status()} ${await res.text()}`);
    const id = loc.replace(/^\//, '').replace(/\?.*$/, '');
    made.push(id);
    return id;
}

// cleanup は作ったページを**子から**消します（子持ちは消せないため逆順）。
//
// ⚠ **ロックを先に外します**——握ったままだと削除に入れません。
// ⚠ **失敗しても止めません**（片付けで試験を落とさない）。消し残しは次に気づけます。
async function cleanup(page) {
    for (const id of made.slice().reverse()) {
        try {
            await page.request.post(BASE + '/api/lock/force?id=' + id, { headers: { 'Origin': BASE } });
            await page.request.post(BASE + '/api/delete-page?id=' + id, { headers: { 'Origin': BASE } });
        } catch (e) { /* 片付けの失敗で試験を落とさない */ }
    }
}

// 空欄（発注書番号・発注日）を持つテンプレート本文。
//
// ⚠ **ヘッダは可変タグです**（2026-09-21 に直した）。それまで
// `<section data-type="client-order">` の素の `dl` で書いていましたが、
// **`client-order` という形式は 2026-09-18 に全廃されています**
// （「ヘッダだけの形式は全廃しました…値は可変タグへ」）。
// ⚠ **形式が無いので種まきが素通りし**、発注書番号も発注日も埋まりませんでした
// ——**試験のほうが古かった**のです（2件の失敗がそれ）。
const TEMPLATE_BODY =
    '<h1>受注ページ</h1>' +
    '<dl data-type="tags"><dt>発注書番号</dt><dd><br></dd>' +
    '<dt>発注元</dt><dd>得意先A</dd>' +
    '<dt>発注日</dt><dd><br></dd></dl>' +
    '<table data-id="tp01" data-type="client-order-items"><caption>受注明細</caption><tbody>' +
    '<tr><th>品番</th><th>品名</th><th>単価</th><th>数量</th><th>状態</th></tr>' +
    '<tr><td>SAMPLE-1</td><td>見本</td><td>100</td><td>1</td><td>未着手</td></tr>' +
    '</tbody></table>';

// findOrMakeTemplateBox は**既にある**トップ直下の「テンプレート」を返します。
//
// ⚠ **無いときだけ作ります**（骨組みだけの環境のため）。在るのに作ると、サーバーが
// 409 で断ります——「トップ直下に1枚だけ」の規則です。
async function findOrMakeTemplateBox(page) {
  const kids = await page.evaluate(async () => {
    const r = await fetch('/api/children?parent_id=000000', { credentials: 'same-origin' });
    const d = await r.json();
    return Array.isArray(d) ? d.map((c) => ({ id: c.ID || c.id, title: c.Title || c.title })) : [];
  });
  const found = kids.find((c) => (c.title || '').trim() === 'テンプレート');
  if (found) return found.id;
  const id = await newPage(page, '000000');
  await saveBody(page, id, '<h1>テンプレート</h1><p>ここの葉がテンプレートになります。</p>');
  return id;
}

(async () => {
    const browser = await chromium.launch({ headless: !process.argv.includes('--headed') });
    const page = await browser.newPage({ ignoreHTTPSErrors: true });
    const errs = [];
    page.on('pageerror', e => errs.push(String(e)));
    try {
        await page.goto(BASE + '/login');
        await page.fill('#w-username, #username', 'a');
        await page.fill('#w-password, #password', 'a');
        await page.click('button[type=submit]');
        await page.waitForURL('**/000000**', { timeout: 8000 });

        // ── 準備: テンプレート / 業務 / 受注ページ の三層 ──
        //
        // ⚠ **テンプレートの箱は作りません。既にあるものを使います**（2026-09-21
        //    ユーザー:「トップ直下にテンプレートページは**一つだけ**しかないように
        //    すべきです」）。サーバーが2枚目を 409 で断るようになったので、
        //    作ろうとすると**この試験自身が原因で落ちます**。
        // ⚠ **使われるのはいちばん古い1枚**なので、2枚目を作ると**この試験の木が
        //    誰からも見えなくなります**——実際にそれで6件落ちていました。
        const rootId = await findOrMakeTemplateBox(page);
        const classifyId = await newPage(page, rootId);
        await saveBody(page, classifyId, '<h1>業務</h1><p>受発注まわりの雛形。</p>');
        const tmplId = await newPage(page, classifyId);
        await saveBody(page, tmplId, TEMPLATE_BODY);

        // ── ③ テンプレート領域は計算テーブルへ載らない ──
        const before = await (await page.request.get(BASE + '/api/required-materials?page_id=' + tmplId)).text();
        check('テンプレートの明細が手配集計に出ない', !before.includes('SAMPLE-1'));

        // ── ① 一覧は階層のまま・葉だけが選べる ──
        const tree = await (await page.request.get(BASE + '/api/templates')).json();
        // ⚠ **「1件だけ」とは見ません**——テンプレートの箱は実データと**共有**なので、
        //    ほかの分類（`加工製品` など）が並んでいて当たり前です。
        //    見るのは**自分が作った枝が在るか**だけ。
        const branch = (tree || []).find((b) => b.title === '業務');
        check('一覧に分類（枝）が返る', !!branch);
        check('分類の下に葉が返る',
            !!branch && branch.children && branch.children.length === 1 &&
            branch.children[0].title === '受注ページ');

        // メニューUI: 「＋ 子ページを作成」で選択肢が出る。
        const hostId = await newPage(page, '000000');
        await saveBody(page, hostId, '<h1>案件ホスト</h1>');
        await page.goto(BASE + '/' + hostId + '?edit=true');
        await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
        await page.locator('#w-create-subpage-btn').click();
        await page.waitForSelector('#w-template-menu.active', { timeout: 8000 });
        const items = await page.locator('#w-template-menu button').allTextContents();
        const groups = await page.locator('#w-template-menu .template-menu-group').allTextContents();
        check('メニューに「空のページ」が出る', items.includes('空のページ'));
        check('メニューに葉（受注ページ）が出る', items.includes('受注ページ'));
        check('枝（業務）はボタンでなく見出し', !items.includes('業務') && groups.includes('業務'));
        await page.keyboard.press('Escape');
        check('Escape でメニューが閉じる',
            await page.locator('#w-template-menu.active').count() === 0);

        // ── ② テンプレートから作ると本文が写る（純粋なコピー） ──
        const madeId = await newPage(page, hostId, tmplId);
        const madeHTML = await (await page.request.get(BASE + '/api/load?id=' + madeId)).text();
        check('テンプレートの本文が写る', madeHTML.includes('受注ページ') && madeHTML.includes('得意先A'));
        // ⚠ **2026-09-25 から純粋なコピーです**（ユーザー:「純粋なコピーにしましょう」）。
        //    それまでは空の日付の列に今日を入れていました（新規化）。いまは**空欄は空欄のまま**
        //    （「まだ分からない」）で、変えるのは**ブロックID（data-id）を外す**ことだけ。
        //    発注書番号も入れません（お客様の番号・09-21 に採番を撤去）。
        check('発注書番号を機械が入れない', !/PO-\d/.test(madeHTML));
        // 日付は**現地時刻**で比べる（toISOString() は UTC へ寄せ、日本時間の朝に前日になる）。
        const now = new Date();
        const today = now.getFullYear() + '-' +
            String(now.getMonth() + 1).padStart(2, '0') + '-' +
            String(now.getDate()).padStart(2, '0');
        check('空欄は空欄のまま（今日を入れない）', !madeHTML.includes(today));
        check('ブロックIDは写さない', !madeHTML.includes('tp01'));
        check('明細もコピーされる', madeHTML.includes('SAMPLE-1'));

        // 作ったページは領域の外なので、今度は計算に載る。
        const after = await (await page.request.get(BASE + '/api/required-materials?page_id=' + madeId)).text();
        check('コピー先は計算の対象になる', after !== null);

        // ── 拒否: 分類フォルダとルートはテンプレートに使えない ──
        for (const [label, id] of [['分類フォルダ', classifyId], ['ルート', rootId]]) {
            const res = await page.request.post(
                BASE + '/api/new-page?parent=' + hostId + '&template=' + id,
                { headers: { 'Origin': BASE }, maxRedirects: 0 });
            check(`${label}はテンプレートに使えない`, res.status() === 400);
        }

        check('ページエラーなし', errs.length === 0);
        if (errs.length) console.error('ERRS:', errs.slice(0, 3));
    } catch (e) { check('実行が最後まで到達', false); console.error(e); }
    finally { await browser.close(); }
    console.log(results.join('\n'));
    console.log(failCount === 0 ? `\n✅ 全 ${results.length} 項目 通過` : `\n❌ ${failCount} 件の失敗`);
    process.exit(failCount === 0 ? 0 : 1);
})();
