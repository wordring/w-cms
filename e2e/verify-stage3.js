// w-cms 移行第3段の自動E2E検証（受発注→section・見積→dl・file容器＋エンハンサ・一括変換）
// 実行: cd ~\tools\wcms-e2e && node "$env:OneDrive\tools\wcms-e2e\verify-stage3.js"
const { createRequire } = require('module');
const path = require('path');
let chromium;
try { ({ chromium } = createRequire(path.join(process.cwd(), 'noop.js'))('playwright')); }
catch (e) { console.error('node_modules のあるディレクトリ（~/tools/wcms-e2e）から実行してください'); process.exit(2); }

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
    return loc.replace(/^\//, "").replace(/\?.*$/, "");
}

const results = []; let failCount = 0;
function check(name, cond) { results.push(`${cond ? 'PASS' : 'FAIL'} ${name}`); if (!cond) failCount++; }
async function caretInto(page, h) { await h.click(); await h.evaluate(el => { const r = document.createRange(); r.selectNodeContents(el); r.collapse(false); const s = getSelection(); s.removeAllRanges(); s.addRange(r); }); }
async function waitSaved(page) { await page.waitForFunction(() => document.getElementById('w-save-status').innerText.includes('保存済'), null, { timeout: 8000 }); }
async function openSlashMenu(page) {
    await page.evaluate(() => { const b = document.querySelectorAll('.editor-block'); b[b.length - 1].querySelector('.add-btn').click(); });
    await page.waitForSelector('#w-slash-menu.active', { timeout: 4000 });
}

(async () => {
    const browser = await chromium.launch({ headless: !process.argv.includes('--headed') });
    const page = await browser.newPage();
    const errs = [];
    page.on('pageerror', e => errs.push(String(e)));
    page.on('dialog', d => d.accept());
    try {
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
        check('明細表（hidden）はメニューに出ない', await page.locator('#w-slash-menu [data-type="vocab:client-order-items"]').count() === 0);
        check('後継: 顧客の発注書がある', await page.locator('#w-slash-menu [data-type="vocab:client-order"]').count() === 1);

        // 2. 顧客の発注書の挿入 = **見出し形のsection骨格だけ**（D-2・2026-08-31）。
        //    file 容器は廃止（PDFの取り付け台は形式自身の File 宣言が担い、
        //    所在は可視のファイル名リンクが運ぶ）。本文は
        //    <section><h2>顧客の発注書</h2>＋素のヘッダdl＋素の明細表。
        await page.click('#w-slash-menu [data-type="vocab:client-order"]');
        const order = page.locator('#w-editor-content section').filter({ hasText: '顧客の発注書' }).first();
        await order.waitFor({ timeout: 4000 });
        check('file容器なしで受注ブロックが挿さる', await page.locator('#w-editor-content section[data-type="file"]').count() === 0);
        check('受注ブロックは見出しが機能を宣言', (await order.locator('h2').first().innerText()).trim() === '顧客の発注書');
        check('受注ブロックに data-type は無い', await order.getAttribute('data-type') === null);
        // 鍵は dt の表示文字が運ぶ（機械キーの属性は 2026-08-20 に撤去）。
        const hdr = order.locator('dl').first();
        check('ヘッダdlの見出しが鍵', (await hdr.locator('dt').allInnerTexts()).map(t => t.trim())
            .join('/') === '発注書番号/発注元/発注日');
        check('機械キーの属性は書き出さない', await order.locator('[data-field]').count() === 0);
        check('発注日に今日が入る', /^\d{4}-\d{2}-\d{2}$/.test((await hdr.locator('dd').nth(2).innerText()).trim()));
        const items = order.locator('table');
        check('明細表の骨格（5列・素の表）', await items.locator('tr').first().locator('th').count() === 5);
        check('明細表にも data-type は無い', await items.getAttribute('data-type') === null);
        check('ドロップゾーンは受注ブロック自身に付く', await order.locator('.pdf-drop-zone').count() === 1);
        // 認識の印は**一致した言葉そのものの薄青の背景**（枠や札ではなく）
        check('見出し語に認識の背景が付く',
            await order.locator('h2').first().evaluate(el =>
                el.classList.contains('vocab-word') &&
                getComputedStyle(el).backgroundColor === 'rgb(219, 234, 254)'));
        check('形式名の札は出ない（見出し自身が宣言）',
            await order.evaluate(el => {
                const prev = el.closest('.editor-block');
                return !prev || !prev.parentElement.querySelector(':scope > .vocab-label') ||
                    prev.previousElementSibling === null || !prev.previousElementSibling.classList ||
                    !prev.previousElementSibling.classList.contains('vocab-label');
            }));

        // 3. 編集と保存往復（クロームが漏れないこと）
        await caretInto(page, hdr.locator('dd').nth(1)); // 発注元
        await page.keyboard.type('トーア');
        await caretInto(page, items.locator('tr').nth(1).locator('td').first());
        await page.keyboard.type('SHAFT-01');
        await page.locator('#w-editor-content h1').first().click();
        await waitSaved(page);
        check('sanitized 警告なし', await page.locator('[data-toast-id="sanitized"]').count() === 0);
        const preview = await page.locator('#w-html-preview').inputValue();
        check('保存に見出し形が乗る', preview.includes('<h2>顧客の発注書</h2>'));
        check('本文から機械語が消えた（file容器も無い）', !preview.includes('data-type="client-order') && !preview.includes('data-type="file"'));
        check('編集クロームが保存に漏れない', !preview.includes('vocab-chrome') && !preview.includes('pdf-drop-zone'));

        // 4. 見積の挿入（見出し形: section + h2 + 素の dl）
        await openSlashMenu(page);
        await page.click('#w-slash-menu [data-type="vocab:our-estimate"]');
        const estSec = page.locator('#w-editor-content section').filter({ hasText: '弊社の見積もり' }).first();
        await estSec.waitFor({ timeout: 4000 });
        const est = estSec.locator('dl');
        check('見積dlの骨格（4項目）', await est.locator('dt').count() === 4);

        // 5. 見出しの改名は「計算に読まれなくなった項目」として告知される（拒否はしない）。
        //    鍵は見出しの表示文字が運ぶので、改名すると③計算プラグインが読めなくなる。
        await page.evaluate(() => {
            const h = Array.from(document.querySelectorAll('#w-editor-content section > h2'))
                .find(x => x.textContent.trim() === '顧客の発注書');
            const dt = h.parentElement.querySelectorAll('dl dt')[1];
            dt.textContent = '得意先';
            triggerAutoSave();
        });
        await page.waitForFunction(
            () => document.getElementById('w-toast-host').innerText.includes('計算に読まれません'),
            null, { timeout: 8000 });
        check('見出しの改名を保存時に告知する', true);

        check('ページエラーなし', errs.length === 0);
    } catch (e) { check('実行が最後まで到達', false); console.error(e); }
    finally { await browser.close(); }
    console.log(results.join('\n'));
    console.log(failCount === 0 ? `\n✅ 全 ${results.length} 項目 通過` : `\n❌ ${failCount} 件の失敗`);
    process.exit(failCount === 0 ? 0 : 1);
})();
