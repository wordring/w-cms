// 画像添付のE2E（要件定義書 §2.6）。
// 入口（スラッシュメニュー・ドロップ・image 列）と出口（配信）の両方を突く。
const { createRequire } = require('module');
const path = require('path');
let chromium;
try { ({ chromium } = createRequire(path.join(process.cwd(), 'noop.js'))('playwright')); }
catch (e) { console.error('node_modules のあるディレクトリ（e2e/）から実行してください'); process.exit(2); }

const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

const results = []; let failCount = 0;
function check(name, cond) { results.push(`${cond ? 'PASS' : 'FAIL'} ${name}`); if (!cond) failCount++; }
async function waitSaved(page) { await page.waitForFunction(() => document.getElementById('w-save-status').innerText.includes('保存済'), null, { timeout: 8000 }); }
async function settleSaved(page) { await waitSaved(page); await page.waitForTimeout(2000); await waitSaved(page); }

async function gotoNewPage(page, parent) {
    const res = await page.request.post(BASE + '/api/new-page?parent=' + parent, { headers: { 'Origin': BASE }, maxRedirects: 0 });
    const loc = res.headers()['location'];
    if (!loc) throw new Error('new-page failed: ' + res.status());
    await page.goto(BASE + loc);
    return loc.replace(/^\//, '').replace(/\?.*$/, '');
}

async function openSlashMenu(page) {
    await page.evaluate(() => { const b = document.querySelectorAll('.editor-block'); b[b.length - 1].querySelector('.add-btn').click(); });
    await page.waitForSelector('#w-slash-menu.active', { timeout: 4000 });
}

// 1x1 の PNG（透明）。素材をリポジトリへ置かずに済むよう、base64 を直に持つ。
const PNG_1x1 = Buffer.from(
    'iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mP8z8BQDwAEhQGAhKmMIQAAAABJRU5ErkJggg==',
    'base64');
// HEIC の先頭だけ（ftyp ボックスのブランドが heic）。中身は判定に使わない。
const HEIC_HEAD = Buffer.concat([
    Buffer.from([0x00, 0x00, 0x00, 0x18]), Buffer.from('ftypheic'), Buffer.alloc(32),
]);

(async () => {
    const browser = await chromium.launch({ headless: !process.argv.includes('--headed') });
    const page = await browser.newPage();
    const errs = []; const cspViolations = [];
    page.on('pageerror', e => errs.push(String(e)));
    page.on('console', m => { const t = m.text(); if (/Content.Security.Policy|Refused to/i.test(t)) cspViolations.push(t); });
    try {
        await page.goto(BASE + '/login');
        await page.fill('#username', 'a'); await page.fill('#password', 'a');
        await page.click('button[type=submit]');
        await page.waitForURL('**/000000**', { timeout: 8000 });

        const pageId = await gotoNewPage(page, '000000');
        await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
        const pageURL = BASE + '/' + pageId;

        // 1. スラッシュメニューに画像がある
        await openSlashMenu(page);
        check('スラッシュメニューに画像がある', await page.locator('#w-slash-menu [data-type="image"]').count() === 1);

        // 2. 選ぶとファイル選択が開き、選んだ画像が本文へ入る
        const [chooser] = await Promise.all([
            page.waitForEvent('filechooser', { timeout: 5000 }),
            page.click('#w-slash-menu [data-type="image"]'),
        ]);
        check('複数枚をまとめて選べる', chooser.isMultiple());
        await chooser.setFiles({ name: 'テスト画像.png', mimeType: 'image/png', buffer: PNG_1x1 });

        const img = page.locator('#w-editor-content img').first();
        await img.waitFor({ timeout: 8000 });
        const src = await img.getAttribute('src');
        check('本文に img が入る', !!src);
        // きれいなURL（/<ページID>/<生成名>）——物理配置はURLに出ない（2026-08-31）
        check('src は添付の配信口を指す', !!src && /^\/[0-9]{6}\/[0-9a-z]+\.png$/.test(src));
        check('alt にファイル名が入る', (await img.getAttribute('alt')) === 'テスト画像.png');

        // 3. 実際に読み込めている（配信が Content-Type を正しく返している）
        await page.waitForTimeout(500);
        check('画像が実際に描画されている', await img.evaluate(el => el.complete && el.naturalWidth > 0));

        // 4. 保存往復で残る（サニタイザが /data/ の絶対パスを落とさない）
        await settleSaved(page);
        const preview = await page.locator('#w-html-preview').inputValue();
        // きれいなURLはサニタイザの許可（/<6桁>/<名前>）に乗る（2026-08-31）
        check('保存されるHTMLに img が乗る', preview.includes('<img') && /src="\/[0-9]{6}\//.test(preview));
        await page.goto(pageURL);
        await page.locator('#w-editor-content img').first().waitFor({ timeout: 8000 });
        check('再読込しても img が残る', await page.locator('#w-editor-content img').count() >= 1);

        // 5. 配信のヘッダ（ラスタ画像はインライン・SVG は不活性化）
        const res = await page.request.get(BASE + src);
        check('PNG は image/png で配信される', res.headers()['content-type'] === 'image/png');
        check('PNG はダウンロード扱いにならない', !res.headers()['content-disposition']);
        check('nosniff が付く', res.headers()['x-content-type-options'] === 'nosniff');

        // 6. HEIC は理由つきで拒否される（iOSのカメラ写真対策）
        const lock = await page.request.post(BASE + '/api/lock?id=' + pageId, { headers: { 'Origin': BASE } });
        const token = (await lock.json()).token;
        const heicRes = await page.request.post(BASE + '/api/upload-image', {
            headers: { 'Origin': BASE, 'X-Lock-Token': token },
            multipart: {
                page_id: pageId,
                image_file: { name: 'IMG_0001.heic', mimeType: 'image/heic', buffer: HEIC_HEAD },
            },
        });
        const heicBody = await heicRes.text();
        check('HEIC は 400 で拒否', heicRes.status() === 400);
        check('HEIC の拒否理由に形式名と次の手がある', heicBody.includes('HEIC') && heicBody.includes('JPEG'));

        // 7. 名乗りと中身の食い違いを拒否（.png という名前のSVG）
        const lieRes = await page.request.post(BASE + '/api/upload-image', {
            headers: { 'Origin': BASE, 'X-Lock-Token': token },
            multipart: {
                page_id: pageId,
                image_file: {
                    name: 'うそ.png', mimeType: 'image/png',
                    buffer: Buffer.from('<svg xmlns="http://www.w3.org/2000/svg"><rect/></svg>'),
                },
            },
        });
        check('拡張子と中身の食い違いは 400', lieRes.status() === 400);

        // 8. スクリプト入りの SVG は入口で拒否
        const svgBad = await page.request.post(BASE + '/api/upload-image', {
            headers: { 'Origin': BASE, 'X-Lock-Token': token },
            multipart: {
                page_id: pageId,
                image_file: {
                    name: 'わな.svg', mimeType: 'image/svg+xml',
                    buffer: Buffer.from('<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>'),
                },
            },
        });
        check('スクリプト入りSVGは 400', svgBad.status() === 400);

        // 9. 正当な SVG は入るが、直接開くと不活性（ダウンロード＋sandbox CSP）
        const svgOK = await page.request.post(BASE + '/api/upload-image', {
            headers: { 'Origin': BASE, 'X-Lock-Token': token },
            multipart: {
                page_id: pageId,
                image_file: {
                    name: '図.svg', mimeType: 'image/svg+xml',
                    buffer: Buffer.from('<svg xmlns="http://www.w3.org/2000/svg" width="8" height="8"><rect width="8" height="8"/></svg>'),
                },
            },
        });
        check('正当なSVGは受け入れられる', svgOK.status() === 200);
        const svgSrc = (await svgOK.json()).src;
        const svgRes = await page.request.get(BASE + svgSrc);
        check('SVG は image/svg+xml', svgRes.headers()['content-type'] === 'image/svg+xml');
        check('SVG は直接開くとダウンロード', (svgRes.headers()['content-disposition'] || '').includes('attachment'));
        check('SVG に sandbox CSP が付く', (svgRes.headers()['content-security-policy'] || '').includes('sandbox'));
        await page.request.post(BASE + '/api/unlock?id=' + pageId + '&token=' + token, { headers: { 'Origin': BASE } });

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
