// 保存済み文書の版管理（リビジョン／リバート）のE2E。
// 設計は docs/【考察】アンドゥ・リドゥ.md §4・§5、実装は internal/cms/version.go。
//
// ここで守りたいのは「**履歴が使い物になる**」こと——オートセーブの連打で版が
// 溢れず、右レールから戻せて、戻しても権限が巻き戻らないこと。
const { createRequire } = require('module');
const path = require('path');
let chromium;
try { ({ chromium } = createRequire(path.join(process.cwd(), 'noop.js'))('playwright')); }
catch (e) { console.error('node_modules のあるディレクトリ（e2e/）から実行してください'); process.exit(2); }

const BASE = process.env.WCMS_BASE || 'http://localhost:8080';
const H = { Origin: BASE };

const results = []; let failCount = 0;
function check(name, cond) { results.push(`${cond ? 'PASS' : 'FAIL'} ${name}`); if (!cond) failCount++; }
async function waitSaved(page) { await page.waitForFunction(() => document.getElementById('w-save-status').innerText.includes('保存済'), null, { timeout: 8000 }); }
async function settleSaved(page) { await waitSaved(page); await page.waitForTimeout(2000); await waitSaved(page); }

async function gotoNewPage(page, parent) {
    const res = await page.request.post(BASE + '/api/new-page?parent=' + parent, { headers: H, maxRedirects: 0 });
    const loc = res.headers()['location'];
    if (!loc) throw new Error('new-page failed: ' + res.status());
    await page.goto(BASE + loc);
    return loc.replace(/^\//, '').replace(/\?.*$/, '');
}

// saveVia は API で本文を保存する（ロックを取り直す）。
async function saveVia(page, id, html) {
    const lock = await page.request.post(BASE + '/api/lock?id=' + id, { headers: H });
    const token = (await lock.json()).token;
    const res = await page.request.post(BASE + '/api/save', { headers: H, data: { page_id: id, html, token } });
    if (res.status() !== 200) throw new Error('save failed: ' + res.status());
    await page.request.post(BASE + '/api/unlock?id=' + id + '&token=' + token, { headers: H });
}

async function versionsOf(page, id) {
    const res = await page.request.get(BASE + '/api/versions?id=' + id);
    if (res.status() !== 200) throw new Error('versions failed: ' + res.status());
    return await res.json();
}

(async () => {
    const browser = await chromium.launch({ headless: !process.argv.includes('--headed') });
    const page = await browser.newPage();
    const errs = []; const cspViolations = [];
    page.on('pageerror', e => errs.push(String(e)));
    page.on('console', m => { const t = m.text(); if (/Content.Security.Policy|Refused to/i.test(t)) cspViolations.push(t); });
    page.on('dialog', d => d.accept());
    try {
        await page.goto(BASE + '/login');
        await page.fill('#username', 'a'); await page.fill('#password', 'a');
        await page.click('button[type=submit]');
        await page.waitForURL('**/000000**', { timeout: 8000 });

        const id = await gotoNewPage(page, '000000');
        await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
        const pageURL = BASE + '/' + id;

        // 1. 編集して保存すると版が残る（オートセーブ経由）
        await page.locator('#w-editor-content h1').first().click();
        await page.keyboard.press('Control+A');
        await page.keyboard.type('初版の見出し');
        await settleSaved(page);

        let versions = await versionsOf(page, id);
        check('保存で版が残る', versions.length >= 1);
        check('版に編集者が入る', versions[0] && versions[0].by === 'a');
        check('版に時刻が入る', !!(versions[0] && versions[0].at));

        // 2. オートセーブの連打では版が増えない（コアレッシング）
        const before = versions.length;
        for (const s of ['あ', 'い', 'う', 'え']) {
            await page.keyboard.type(s);
            await page.waitForTimeout(1800);
        }
        await settleSaved(page);
        versions = await versionsOf(page, id);
        check('連続保存で版が溢れない', versions.length === before);

        // 3. 右レールに履歴が出る（開いたときに取り直す作りなので、開いてから見る）
        await page.locator('#w-vh-details > summary').click();
        await page.waitForTimeout(600);
        check('右レールに版が並ぶ', (await page.locator('#w-vh-list .version-item').count()) >= 1);

        // 4. 閲覧モードへ戻ると（ロック解放）チェックポイントが打たれる
        await page.evaluate(() => document.getElementById('w-mode-toggle').click());
        await page.waitForFunction(() => !document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
        await page.waitForTimeout(800);
        versions = await versionsOf(page, id);
        check('ロック解放で最後の状態が残る', versions.length === before + 1);
        const latest = await (await page.request.get(BASE + '/api/version?id=' + id + '&v=' + versions[0].id)).text();
        check('最新の版が最後の本文', latest.includes('あいうえ'));
        check('閲覧モードでは戻すボタンが出ない', (await page.locator('#w-vh-list .version-revert').count()) === 0);

        // 5. 別の内容で保存してから、古い版へ戻す
        const oldVersion = versions[versions.length - 1].id;
        const oldBody = await (await page.request.get(BASE + '/api/version?id=' + id + '&v=' + oldVersion)).text();
        await saveVia(page, id, '<h1>まったく別の本文</h1><p>戻す前</p>');

        await page.goto(pageURL);
        await page.evaluate(() => document.getElementById('w-mode-toggle').click());
        await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
        await page.locator('#w-vh-details > summary').click();
        await page.waitForTimeout(800);
        check('編集モードでは戻すボタンが出る', (await page.locator('#w-vh-list .version-revert').count()) >= 1);

        await page.locator(`#w-vh-list .version-item[data-version="${oldVersion}"] .version-revert`).click();
        await page.waitForTimeout(1500);
        const shown = await page.locator('#w-editor-content').innerText();
        check('本文が古い版に戻る', !shown.includes('まったく別の本文'));
        check('戻した本文が版の中身と一致', oldBody.includes('初版の見出し') && shown.includes('初版の見出し'));

        // 6. 戻す前の内容も履歴に残る（リバートを取り消せる）
        versions = await versionsOf(page, id);
        let foundPrevious = false;
        for (const v of versions) {
            const b = await (await page.request.get(BASE + '/api/version?id=' + id + '&v=' + v.id)).text();
            if (b.includes('まったく別の本文')) foundPrevious = true;
        }
        check('リバート前の内容が履歴に残る', foundPrevious);

        // 7. 版IDの細工は通らない（ページのフォルダの外を読ませない）
        for (const bad of ['../../000000', 'a/b', '..']) {
            const res = await page.request.get(BASE + '/api/version?id=' + id + '&v=' + encodeURIComponent(bad));
            check('不正な版ID（' + bad + '）は拒否', res.status() === 404);
        }

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
