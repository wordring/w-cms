// w-cms ページテンプレートの自動E2E検証
//
// 正本の設計は docs/【考察】ページテンプレート.md。確かめるのは3点:
//   ① 「テンプレート」フォルダの**葉**だけがメニューに出る（枝は分類の見出し）
//   ② テンプレートから作ったページは本文が写り、空欄が型の既定値で埋まる
//   ③ テンプレート領域の中身は③計算テーブルへ載らない（手配集計に出ない）
//
// 前提: サーバーが http://localhost:8080 で起動済み（.claude/launch.json の w-cms）。
// ログインは CLAUDE.md 記載のローカル検証専用 a / a（本番では使わない）。
// 実行: cd ~\tools\wcms-e2e && node "$env:OneDrive\tools\wcms-e2e\verify-template.js"
//
// 注意: このスクリプトはトップ直下に「テンプレート」ページを作ります。
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
async function newPage(page, parent, template) {
    let url = BASE + '/api/new-page?parent=' + parent;
    if (template) url += '&template=' + template;
    const res = await page.request.post(url, { headers: { 'Origin': BASE }, maxRedirects: 0 });
    const loc = res.headers()['location'];
    if (!loc) throw new Error(`new-page failed: ${res.status()} ${await res.text()}`);
    return loc.replace(/^\//, '').replace(/\?.*$/, '');
}

// 空欄（発注書番号・発注日）を持つテンプレート本文。
const TEMPLATE_BODY =
    '<h1>受注ページ</h1>' +
    '<section data-type="client-order">' +
    '<dl><dt>発注書番号</dt><dd><br></dd>' +
    '<dt>発注元</dt><dd>得意先A</dd>' +
    '<dt>発注日</dt><dd><br></dd></dl>' +
    '<table data-type="client-order-items"><tbody>' +
    '<tr><th>品番</th><th>品名</th><th>単価</th><th>数量</th><th>状態</th></tr>' +
    '<tr><td>SAMPLE-1</td><td>見本</td><td>100</td><td>1</td><td>未着手</td></tr>' +
    '</tbody></table></section>';

(async () => {
    const browser = await chromium.launch({ headless: !process.argv.includes('--headed') });
    const page = await browser.newPage();
    const errs = [];
    page.on('pageerror', e => errs.push(String(e)));
    try {
        await page.goto(BASE + '/login');
        await page.fill('#w-username, #username', 'a');
        await page.fill('#w-password, #password', 'a');
        await page.click('button[type=submit]');
        await page.waitForURL('**/000000**', { timeout: 8000 });

        // ── 準備: テンプレート / 業務 / 受注ページ の三層を作る ──
        const rootId = await newPage(page, '000000');
        await saveBody(page, rootId, '<h1>テンプレート</h1><p>ここの葉がテンプレートになります。</p>');
        const classifyId = await newPage(page, rootId);
        await saveBody(page, classifyId, '<h1>業務</h1><p>受発注まわりの雛形。</p>');
        const tmplId = await newPage(page, classifyId);
        await saveBody(page, tmplId, TEMPLATE_BODY);

        // ── ③ テンプレート領域は計算テーブルへ載らない ──
        const before = await (await page.request.get(BASE + '/api/required-materials?page_id=' + tmplId)).text();
        check('テンプレートの明細が手配集計に出ない', !before.includes('SAMPLE-1'));

        // ── ① 一覧は階層のまま・葉だけが選べる ──
        const tree = await (await page.request.get(BASE + '/api/templates')).json();
        check('一覧に分類（枝）が1件返る', tree.length === 1 && tree[0].title === '業務');
        check('分類の下に葉が返る',
            tree[0].children && tree[0].children.length === 1 && tree[0].children[0].title === '受注ページ');

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

        // ── ② テンプレートから作ると本文が写り、空欄が埋まる ──
        const madeId = await newPage(page, hostId, tmplId);
        const madeHTML = await (await page.request.get(BASE + '/api/load?id=' + madeId)).text();
        check('テンプレートの本文が写る', madeHTML.includes('受注ページ') && madeHTML.includes('得意先A'));
        check('発注書番号が新ページIDで採番される', madeHTML.includes('PO-' + madeId));
        const today = new Date().toISOString().split('T')[0];
        check('発注日が今日で埋まる', madeHTML.includes(today));
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
