// w-cms 移行第4段の自動E2E検証（計算ビューのSSR・web-components/templates撤去・CSP strict）
// 実行: cd ~\tools\wcms-e2e && node "$env:OneDrive\tools\wcms-e2e\verify-stage4.js"
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
async function waitSaved(page) { await page.waitForFunction(() => document.getElementById('w-save-status').innerText.includes('保存済'), null, { timeout: 8000 }); }
// 直前に飛んでいた保存の応答が「保存済」を上書きするため、1回の waitSaved では直近の
// 変更の保存完了を保証できない。デバウンス（1.5秒）より長く待って2周目を掴む。
async function settleSaved(page) { await waitSaved(page); await page.waitForTimeout(2000); await waitSaved(page); }
async function openSlashMenu(page) {
    await page.evaluate(() => { const b = document.querySelectorAll('.editor-block'); b[b.length - 1].querySelector('.add-btn').click(); });
    await page.waitForSelector('#w-slash-menu.active', { timeout: 4000 });
}

(async () => {
    const browser = await chromium.launch({ headless: !process.argv.includes('--headed') });
    const page = await browser.newPage();
    const errs = []; const cspViolations = [];
    page.on('pageerror', e => errs.push(String(e)));
    page.on('console', m => { const t = m.text(); if (/Content.Security.Policy|Refused to/i.test(t)) cspViolations.push(t); });
    page.on('dialog', d => d.accept());
    try {
        // 1. ログイン画面: CSP strict 下で外部CSS（login.css）が効いている
        await page.goto(BASE + '/login');
        const cardBg = await page.locator('.card').evaluate(el => getComputedStyle(el).backgroundColor);
        check('ログイン画面に外部CSSが効く', cardBg === 'rgb(255, 255, 255)');

        await page.fill('#username', 'a'); await page.fill('#password', 'a');
        await page.click('button[type=submit]');
        await page.waitForURL('**/000000**', { timeout: 8000 });

        // 2. boot.js（FOUC防止の外部化）が実行されている
        check('boot.js がレール状態を確定', await page.evaluate(() => document.documentElement.classList.contains('toc-collapsed') || document.documentElement.classList.contains('left-collapsed')));

        // 3. 撤去したファイルが配信されない
        for (const p of ['/assets/web-components.js', '/assets/components.css', '/assets/templates/m-tag-view.html']) {
            const res = await page.request.get(BASE + p);
            check(`${p} は404`, res.status() === 404);
        }

        // 4. 新規ページで計算ビューを挿す
        await gotoNewPage(page, '000000');
        await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
        const pageURL = page.url().replace(/\?edit.*$/, '');

        await openSlashMenu(page);
        check('旧 m-child-list 項目が無い', await page.locator('#w-slash-menu [data-type="m-child-list"]').count() === 0);
        check('旧 m-required-materials 項目が無い', await page.locator('#w-slash-menu [data-type="m-required-materials"]').count() === 0);
        check('後継: 子ページ一覧がある', await page.locator('#w-slash-menu [data-type="vocab:child-list"]').count() === 1);
        await page.click('#w-slash-menu [data-type="vocab:child-list"]');
        const clMarker = page.locator('#w-editor-content section[data-type="child-list"]');
        await clMarker.waitFor({ timeout: 4000 });
        check('マーカーは contenteditable にならない', await clMarker.getAttribute('contenteditable') === null);
        check('挿入直後の空マーカーに案内（::before）', await clMarker.evaluate(el => getComputedStyle(el, '::before').content.includes('再読み込み')));

        await openSlashMenu(page);
        await page.click('#w-slash-menu [data-type="vocab:required-materials"]');
        await page.locator('#w-editor-content section[data-type="required-materials"]').waitFor({ timeout: 4000 });
        await settleSaved(page);
        const preview1 = await page.locator('#w-html-preview').inputValue();
        check('保存は空のマーカーのみ', preview1.includes('data-type="child-list"') && preview1.includes('data-type="required-materials"') && !preview1.includes('vocab-chrome'));

        // 5. 再読込 → サーバー事前描画が中身を埋める
        await page.goto(pageURL);
        const clFilled = page.locator('#w-editor-content section[data-type="child-list"] .vocab-chrome');
        await clFilled.waitFor({ timeout: 8000 });
        check('子ページ一覧のSSR（空表示）', (await clFilled.innerText()).includes('子ページはありません'));
        const rmFilled = page.locator('#w-editor-content section[data-type="required-materials"] .vocab-chrome');
        check('手配集計のSSR（見出し）', (await rmFilled.innerText()).includes('部材手配・発注進捗状況'));

        // 6. 子ページを作ると一覧に載る
        const pageId = pageURL.split('/').pop();
        await page.request.post(BASE + '/api/new-page?parent=' + pageId, { headers: { 'Origin': BASE } });
        await page.goto(pageURL);
        await page.locator('#w-editor-content section[data-type="child-list"] .vocab-chrome').waitFor({ timeout: 8000 });
        check('作成した子ページがSSRの一覧に出る', await page.locator('#w-editor-content section[data-type="child-list"] .vocab-chrome a').count() >= 1);

        // 7. SSRの中身は保存に漏れない（編集→保存の往復）。再読込後は閲覧モードなので
        // トグルで編集モードへ入る（ロック取得を待つ）。
        await page.evaluate(() => document.getElementById('w-mode-toggle').click());
        await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
        // 編集モードへ入るとロック取得後に本文を載せ替える。/api/lock の生HTMLではなく
        // SSR 済みの /api/load を読むので、ビューの中身が消えないこと（退行の固定）。
        await page.waitForTimeout(500);
        check('編集モードでもSSRの中身が残る',
            (await page.locator('#w-editor-content section[data-type="child-list"] .vocab-chrome').count()) >= 1);
        await page.locator('#w-editor-content h1').first().click();
        await page.keyboard.type('X');
        await settleSaved(page);
        const preview2 = await page.locator('#w-html-preview').inputValue();
        check('SSRの中身が保存に漏れない', !preview2.includes('vocab-chrome') && !preview2.includes('子ページはありません'));
        check('マーカーは保存に残る', preview2.includes('data-type="child-list"'));

        // 8. ページ内アンカー（描画時合成）: 見出しに id が付き、保存には漏れない
        await page.goto(pageURL);
        await page.locator('#w-editor-content h1').first().waitFor({ timeout: 8000 });
        const h1id = await page.locator('#w-editor-content h1').first().getAttribute('id');
        check('見出しに id が合成される', !!h1id && h1id.length > 0);
        check('ブロックにも id が合成される',
            await page.locator('#w-editor-content [data-id][id]').count() >= 1);
        // 生の保存形式（/api/load）には合成した id が入っていないこと
        const raw = await (await page.request.get(BASE + '/api/load?id=' + pageURL.split('/').pop())).text();
        check('保存形式に合成 id は入らない（/api/load）', !raw.includes('id="' + h1id + '"'));
        // 編集モードへ入ると /api/load で載せ替わるので、保存往復でも漏れない
        await page.evaluate(() => document.getElementById('w-mode-toggle').click());
        await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
        await page.waitForTimeout(700);
        await page.locator('#w-editor-content h1').first().click();
        await page.keyboard.type('Z');
        await settleSaved(page);
        const preview3 = await page.locator('#w-html-preview').inputValue();
        check('合成 id が保存に漏れない', !preview3.includes('id="' + h1id + '"'));

        // 9. 計算ビューのクローム見出しは目次に載らない（2026-08-20 の修正）
        const toc = await page.evaluate(() => document.getElementById('w-toc-list').innerText);
        check('目次にクロームの見出しが載らない', !toc.includes('部材手配・発注進捗状況'));
        check('クローム見出しにアンカーを合成しない',
            await page.evaluate(() => {
                const h = document.querySelector('.vocab-chrome h3');
                return !h || !h.getAttribute('id');
            }));

        check('CSP違反なし', cspViolations.length === 0);
        check('ページエラーなし', errs.length === 0);
        if (cspViolations.length) console.error('CSP:', cspViolations.slice(0, 3));
        if (errs.length) console.error('ERRS:', errs.slice(0, 3));
    } catch (e) { check('実行が最後まで到達', false); console.error(e); }
    finally { await browser.close(); }
    console.log(results.join('\n'));
    console.log(failCount === 0 ? `\n✅ 全 ${results.length} 項目 通過` : `\n❌ ${failCount} 件の失敗`);
    process.exit(failCount === 0 ? 0 : 1);
})();
