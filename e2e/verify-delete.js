// w-cms ページ削除（ゴミ箱への移動）の自動E2E検証
const { createRequire } = require('module');
const path = require('path');
let chromium;
try { ({ chromium } = createRequire(path.join(process.cwd(), 'noop.js'))('playwright')); }
catch (e) { console.error('node_modules のあるディレクトリ（~/tools/wcms-e2e）から実行してください'); process.exit(2); }

const BASE = process.env.WCMS_BASE || 'http://localhost:8080';
const results = []; let failCount = 0;
function check(name, cond) { results.push(`${cond ? 'PASS' : 'FAIL'} ${name}`); if (!cond) failCount++; }

(async () => {
    const browser = await chromium.launch({ headless: !process.argv.includes('--headed') });
    const page = await browser.newPage();
    const errs = [];
    page.on('pageerror', e => errs.push(String(e)));
    page.on('dialog', d => d.accept()); // 削除の confirm を承諾
    try {
        await page.goto(BASE + '/login');
        await page.fill('#w-username, #username', 'a');
        await page.fill('#w-password, #password', 'a');
        await page.click('button[type=submit]');
        await page.waitForURL('**/000000**', { timeout: 8000 });

        // 1. トップページでは削除ボタンを出さない
        await page.evaluate(() => document.getElementById('w-mode-toggle').click());
        await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
        check('トップページに削除ボタンを出さない',
            await page.locator('#w-delete-page-btn:visible').count() === 0);
        await page.evaluate(() => document.getElementById('w-mode-toggle').click());

        // 2. 親と子を作る → 親は子がいるので削除できない
        await page.goto(BASE + '/api/new-page?parent=000000');
        await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
        const parentURL = page.url().replace(/\?edit.*$/, '');
        const parentId = parentURL.split('/').pop();
        check('編集モードで削除ボタンが出る', await page.locator('#w-delete-page-btn:visible').count() === 1);

        await page.request.get(BASE + '/api/new-page?parent=' + parentId);
        await page.goto(parentURL + '?edit=true');
        await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
        await page.waitForTimeout(500);
        await page.locator('#w-delete-page-btn').click();
        await page.waitForFunction(
            () => document.getElementById('w-toast-host').innerText.includes('子ページが'),
            null, { timeout: 8000 });
        check('子ページがあると削除できず告知する', true);
        check('親ページはまだ生きている', (await page.request.get(parentURL)).status() === 200);

        // 3. 子ページ（末端）は削除できる → 親へ戻る
        const childURL = await page.evaluate(() =>
            document.querySelector('#w-child-nav-list a').getAttribute('href'));
        const childId = childURL.split('/').pop().replace(/\?.*$/, '');
        await page.goto(BASE + '/' + childId + '?edit=true');
        await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
        await page.waitForTimeout(500);
        await page.locator('#w-delete-page-btn').click();
        await page.waitForURL('**/' + parentId + '**', { timeout: 8000 });
        check('削除すると親ページへ戻る', true);
        check('削除したページは404', (await page.request.get(BASE + '/' + childId)).status() === 404);

        // 4. 親が末端になったので削除できる
        await page.goto(parentURL + '?edit=true');
        await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
        await page.waitForTimeout(500);
        await page.locator('#w-delete-page-btn').click();
        await page.waitForURL('**/000000**', { timeout: 8000 });
        check('子を消したあとは親も削除できる', true);
        check('親も404になる', (await page.request.get(parentURL)).status() === 404);

        check('ページエラーなし', errs.length === 0);
        if (errs.length) console.error('ERRS:', errs.slice(0, 3));
    } catch (e) { check('実行が最後まで到達', false); console.error(e); }
    finally { await browser.close(); }
    console.log(results.join('\n'));
    console.log(failCount === 0 ? `\n✅ 全 ${results.length} 項目 通過` : `\n❌ ${failCount} 件の失敗`);
    process.exit(failCount === 0 ? 0 : 1);
})();
