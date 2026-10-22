// w-cms 移行第3段の自動E2E検証（受発注→section・見積→dl・file容器＋エンハンサ・一括変換）
// ⚠ 2026-09-16: 自己署名の証明書を許すようにしました（`ignoreHTTPSErrors`）。
// それまで古い verify-* は :8080 前提で、`WCMS_BASE=https://…` を渡しても
// **証明書で弾かれて一式を流せません**でした（引き継ぎの「見る先が2つに割れている」）。
// 実行: cd ~\tools\wcms-e2e && node "$env:OneDrive\tools\wcms-e2e\verify-stage3.js"
//
// ⚠ 2026-10-14: 今の形に合わせて書き直しました。それまでは第3段の形（「顧客の発注書」の節＝見出し＋素のヘッダ dl＋
// 素の明細表・「弊社の見積もり」の dl）を挿していましたが、その形式は 2026-09-18 に消えています（ヘッダはタグへ・
// DB に入るのはタグと表だけ）。いまの後継は「受注明細表」の表（2026-09-26 からキャプションで名乗る・09-27 から
// スラッシュメニューに出す）です。見ていること（旧4項目が無い・file 容器が無い・機械語が本文に出ない・編集の
// クロームが保存に漏れない・見出しの改名を告知する・パンくず）は同じです。作ったページは最後に消します。
const { createRequire } = require('module');
const path = require('path');
let chromium;
try { ({ chromium } = createRequire(path.join(process.cwd(), 'noop.js'))('playwright')); }
catch (e) { console.error('node_modules のあるディレクトリ（~/tools/wcms-e2e）から実行してください'); process.exit(2); }

const { saveNow } = require(path.join(__dirname, 'lib'));

const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

// gotoNewPage は子ページを作ってそのページへ遷移する。
// /api/new-page は保存型CSRF対策で **POST 限定**（2026-08-21・beb98a1）なので、
// ブラウザの GET 遷移では作れない。POST してから Location へ遷移する。
async function gotoNewPage(page, parent, template) {
    let url = BASE + "/api/new-page?parent=" + parent;
    if (template) url += "&template=" + encodeURIComponent(template);
    const res = await page.request.post(url, { headers: { "Origin": BASE }, maxRedirects: 0 });
    const loc = res.headers()["location"];
    if (!loc) throw new Error("new-page failed: " + res.status() + " " + (await res.text()));
    await page.goto(BASE + loc);
    const id = loc.replace(/^\//, "").replace(/\?.*$/, "");
    createdPages.push(id);
    return id;
}

// createdPages は作ったページ（最後に消す・2026-10-14——それまで消しておらず、流すたびにトップ直下へ残っていた）。
// 子を先に消す（親を先に消すと子の削除が断られる）ので、作った順の逆に消す。
const createdPages = [];
// 消す前に開いているタブの保存を済ませてページを去る（書ける形のタブの /api/sync と削除が行き違わないように——verify-stage2 の注）。
async function deleteCreatedPages(page) {
    await saveNow(page).catch(() => {});
    await page.goto('about:blank').catch(() => {});
    await page.waitForTimeout(500);
    for (const id of createdPages.slice().reverse()) {
        const opts = { headers: { Origin: BASE } };
        await page.request.post(BASE + '/api/lock/force?id=' + id, opts).catch(() => {});
        await page.request.post(BASE + '/api/delete-page?force=1&id=' + id, opts).catch(() => {});
    }
}

const results = []; let failCount = 0;
function check(name, cond) { results.push(`${cond ? 'PASS' : 'FAIL'} ${name}`); if (!cond) failCount++; }
// caretInto は押す前に、本文の外の mousedown とキャレットを外すことで書式の帯を閉じる（2026-10-14——10-07 から PC の帯は
// キャレットの行のすぐ上に出るので、押す先に重なることがある。verify-stage2 の caretInto と同じ）。
// 保存の応答で帯が出直したときは、閉じ直して押し直す。
async function caretInto(page, h) {
    for (let i = 0; ; i++) {
        await page.dispatchEvent('body', 'mousedown');
        await page.evaluate(() => window.getSelection().removeAllRanges());
        try { await h.click({ timeout: 3000 }); break; } catch (e) { if (i >= 3) throw e; }
    }
    await h.evaluate(el => { const r = document.createRange(); r.selectNodeContents(el); r.collapse(false); const s = getSelection(); s.removeAllRanges(); s.addRange(r); }); }
// waitSaved は先に「いま保存」する（2026-10-14——10-12 昼から、画面はブロックを去るときなどにだけ保存する。lib.js の saveNow）。
async function waitSaved(page) { await saveNow(page); await page.waitForFunction(() => document.getElementById('w-save-status').innerText.includes('保存済'), null, { timeout: 8000 }); }
async function openSlashMenu(page) {
    await page.evaluate(() => { const b = document.querySelectorAll('.editor-block'); b[b.length - 1].querySelector('.add-btn').click(); });
    await page.waitForSelector('#w-slash-menu.active', { timeout: 4000 });
}

(async () => {
    const browser = await chromium.launch({ headless: !process.argv.includes('--headed') });
    const page = await browser.newPage({ ignoreHTTPSErrors: true });
    const errs = [];
    page.on('pageerror', e => errs.push(String(e)));
    page.on('dialog', d => d.accept());
    try {
        // 開いたときの形は閲覧（2026-10-12——パソコンの既定が書ける形になった・lib.js の login と同じ）。
        await page.context().addInitScript(() => { try { localStorage.setItem('wcms.mode', 'view'); } catch (e) {} });
        await page.goto(BASE + '/login');
        await page.fill('#username', 'a'); await page.fill('#password', 'a');
        await page.click('button[type=submit]');
        await page.waitForURL('**/000000**', { timeout: 8000 });
        await gotoNewPage(page, '000000');
        await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });

        // 1. メニュー: 旧4項目が消え、後継がある
        await openSlashMenu(page);
        for (const t of ['m-file-client', 'm-file-our', 'm-our-estimate', 'm-supplier-estimate'])
            check(`旧 ${t} 項目が無い`, await page.locator(`#w-slash-menu [data-type="${t}"]`).count() === 0);
        // ⚠ 2026-10-14 に期待を改めた——「顧客の発注書」（ヘッダの節）は 2026-09-18 に消え（ヘッダはタグへ）、
        //    明細の「受注明細表」は 2026-09-27 からメニューに出す（それまでは hidden。テンプレートに人が置く印になった——
        //    ext/toho/vocab.go の client-order-items の注）。
        check('ヘッダだけの「顧客の発注書」はメニューに無い（2026-09-18 にタグへ）',
            await page.locator('#w-slash-menu [data-type="vocab:client-order"]').count() === 0);
        check('後継: 案件明細表がメニューにある（2026-09-27 から）',
            await page.locator('#w-slash-menu [data-type="vocab:client-order-items"]').count() === 1);

        // 2. 受注明細表の挿入 = **キャプションで名乗る素の表だけ**（2026-09-26・DBの日本語化 5段目）。
        //    file 容器は廃止（PDF は添付のファイル表示が担う）。本文は <table><caption>受注明細表</caption>…</table>。
        await page.click('#w-slash-menu [data-type="vocab:client-order-items"]');
        const items = page.locator('#w-editor-content table')
            .filter({ has: page.locator('caption', { hasText: '案件明細表' }) }).first();
        await items.waitFor({ timeout: 4000 });
        check('file容器なしで案件明細表が挿さる', await page.locator('#w-editor-content section[data-type="file"]').count() === 0);
        check('案件明細表はキャプションが機能を宣言', (await items.locator('caption').first().innerText()).trim() === '案件明細表');
        check('案件明細表に data-type は無い', await items.getAttribute('data-type') === null);
        check('案件明細表を節で包まない',
            await items.evaluate(t => !(t.parentElement && t.parentElement.tagName === 'SECTION')));
        // 鍵は th の表示文字が運ぶ（機械キーの属性は 2026-08-20 に撤去）。
        const heads = (await items.locator('tr').first().locator('th').allInnerTexts()).map(t => t.trim());
        check('明細表の見出しが鍵（弊社品番・品番・品名・数量・単価…）',
            ['弊社品番', '品番', '品名', '数量', '単価'].every(h => heads.includes(h)));
        check('機械キーの属性は書き出さない', await items.locator('[data-field]').count() === 0);
        check('明細表の骨格（データ行が1行）', await items.locator('tr').count() === 2);
        // 認識の印は**一致した言葉そのものの薄青の背景**（枠や札ではなく）——2026-09-29 からは表の名前（caption）に付く。
        check('表の名前に認識の背景が付く',
            await items.locator('caption').first().evaluate(el =>
                el.classList.contains('vocab-word') &&
                getComputedStyle(el).backgroundColor === 'rgb(219, 234, 254)'));
        check('形式名の札は出ない（キャプション自身が宣言）',
            await page.locator('#w-editor-content .vocab-label').count() === 0);

        // 3. 編集と保存往復（クロームが漏れないこと）
        const col = (name) => heads.indexOf(name);
        await caretInto(page, items.locator('tr').nth(1).locator('td').nth(col('品番')));
        await page.keyboard.type('SHAFT-01');
        await caretInto(page, items.locator('tr').nth(1).locator('td').nth(col('品名')));
        await page.keyboard.type('取付ベース');
        await page.locator('#w-editor-content h1').first().click();
        await waitSaved(page);
        check('sanitized 警告なし', await page.locator('[data-toast-id="sanitized"]').count() === 0);
        const preview = await page.locator('#w-html-preview').inputValue();
        check('保存にキャプションが乗る', /<table(?![^>]*data-type)[^>]*>\s*<caption>案件明細表<\/caption>/.test(preview));
        check('保存に打った値が乗る', preview.includes('SHAFT-01') && preview.includes('取付ベース'));
        check('本文から機械語が消えた（file容器も無い）', !preview.includes('data-type="client-order') && !preview.includes('data-type="file"'));
        check('編集クロームが保存に漏れない', !preview.includes('vocab-chrome') && !preview.includes('pdf-drop-zone') &&
            !preview.includes('vocab-word'));

        // 4. 見積の挿入——「弊社の見積もり」（dl）は 2026-09-18 に消えた。後継は加工製品の「見積計算表」（表）。
        await openSlashMenu(page);
        check('旧: 弊社の見積もりはメニューに無い', await page.locator('#w-slash-menu [data-type="vocab:our-estimate"]').count() === 0);
        await page.click('#w-slash-menu [data-type="vocab:part-estimate"]');
        const est = page.locator('#w-editor-content table')
            .filter({ has: page.locator('caption', { hasText: '見積計算表' }) }).first();
        await est.waitFor({ timeout: 4000 });
        check('見積計算表の骨格（3列・工程｜価格｜備考）', await est.locator('tr').first().locator('th').count() === 3);

        // 5. 見出しの改名は「計算に読まれなくなった項目」として告知される（拒否はしない）。
        //    鍵は見出しの表示文字が運ぶので、改名すると③計算プラグインが読めなくなる。
        //    ⚠ 2026-10-14: 改名するのは受注明細表の列の見出し（ヘッダの dl はタグになり、タグは告知しない——2026-09-18）。
        await page.evaluate(() => {
            const cap = Array.from(document.querySelectorAll('#w-editor-content table > caption'))
                .find(x => x.textContent.trim() === '案件明細表');
            const th = Array.from(cap.parentElement.querySelectorAll('tr:first-child th'))
                .find(x => x.textContent.trim() === '単価');
            th.textContent = '単価（税抜）';
            triggerAutoSave();
        });
        await saveNow(page); // triggerAutoSave は「未保存」の印を付けるだけ——告知は保存したときに出る（2026-10-14）
        await page.waitForFunction(
            () => document.getElementById('w-toast-host').innerText.includes('計算に読まれません'),
            null, { timeout: 8000 });
        check('見出しの改名を保存時に告知する', true);

        // ── パンくず（本文の上の帯・2026-09-03）──────────────────────────
        // 左レールの「↑ 親ページへ」を置き換えた。サーバーが組むので、
        // ページを開いた時点で既にHTMLに入っている（JSの往復は無い）。
        // 親を作る。gotoNewPage は作成後に ?edit=true へ遷移して編集ロックを取るため、
        // その直後に次の POST を撃つと稀に 401 が返る（既知の揺れ——前の操作の
        // ロック残り）。ここでは作成と遷移を分け、閲覧モードで開いてから次へ進む。
        const mkRes = await page.request.post(BASE + '/api/new-page?parent=000000',
            { headers: { Origin: BASE }, maxRedirects: 0 });
        const parentId = (mkRes.headers()['location'] || '').replace(/^\//, '').replace(/\?.*$/, '');
        if (parentId) createdPages.push(parentId);
        await page.goto(BASE + '/' + parentId);
        const childId = await gotoNewPage(page, parentId);    // その子
        const crumbs = () => page.evaluate(() => Array.from(
            document.querySelectorAll('.breadcrumb .crumb')).map(a => a.getAttribute('href')));
        check('パンくずに先祖の道が出る',
            JSON.stringify(await crumbs()) === JSON.stringify(['/000000', '/' + parentId]));
        check('現在のページは入らない', !(await crumbs()).includes('/' + childId));
        check('左レールの親リンクは無くなった',
            await page.locator('#w-child-nav-parent').count() === 0);

        await page.goto(BASE + '/000000');
        // 帯は :empty で display:none になるので、既定の「可視待ち」では止まる。
        await page.waitForSelector('.breadcrumb', { state: 'attached', timeout: 8000 });
        check('トップでは帯が畳まれる',
            await page.locator('.breadcrumb').evaluate(el => getComputedStyle(el).display) === 'none');

        check('ページエラーなし', errs.length === 0);
    } catch (e) { check('実行が最後まで到達', false); console.error(e); }
    finally { await deleteCreatedPages(page).catch(() => {}); await browser.close(); }
    console.log(results.join('\n'));
    console.log(failCount === 0 ? `\n✅ 全 ${results.length} 項目 通過` : `\n❌ ${failCount} 件の失敗`);
    process.exit(failCount === 0 ? 0 : 1);
})();
