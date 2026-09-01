// 添付のクリック展開のE2E（2026-09-01 ユーザー決定「clickで展開でお願いします」）。
// PDFの ▶表示（embed 差し込み・閉じる）、ZIPの ▶中身（/api/zip-list の目録）、
// そして**正本を汚さないこと**（クロームは保存されない）を突く。
const { createRequire } = require('module');
const path = require('path');
let chromium;
try { ({ chromium } = createRequire(path.join(process.cwd(), 'noop.js'))('playwright')); }
catch (e) { console.error('node_modules のあるディレクトリ（e2e/）から実行してください'); process.exit(2); }

const BASE = process.env.WCMS_BASE || 'http://localhost:8080';

const results = []; let failCount = 0;
function check(name, cond) { results.push(`${cond ? 'PASS' : 'FAIL'} ${name}`); if (!cond) failCount++; }

async function gotoNewPage(page, parent) {
    const res = await page.request.post(BASE + '/api/new-page?parent=' + parent, { headers: { 'Origin': BASE }, maxRedirects: 0 });
    const loc = res.headers()['location'];
    if (!loc) throw new Error('new-page failed: ' + res.status());
    await page.goto(BASE + loc);
    return loc.replace(/^\//, '').replace(/\?.*$/, '');
}

// チェックボックス自体は視覚的に隠れているので、値を替えて change を発火する
// （onModeToggle 経由でロックの取得・解放と applyMode が走る、UIと同じ経路）。
async function setEditMode(page, on) {
    await page.evaluate(v => {
        const t = document.getElementById('w-mode-toggle');
        if (t.checked !== v) { t.checked = v; t.dispatchEvent(new Event('change', { bubbles: true })); }
    }, on);
    await page.waitForFunction(v => document.body.hasAttribute('edit-mode') === v, on, { timeout: 8000 });
}

// ── 無圧縮ZIPを手組みする（素材をリポジトリへ置かないため） ──────────────
const CRC_TABLE = (() => {
    const t = new Uint32Array(256);
    for (let n = 0; n < 256; n++) {
        let c = n;
        for (let k = 0; k < 8; k++) c = (c & 1) ? (0xEDB88320 ^ (c >>> 1)) : (c >>> 1);
        t[n] = c >>> 0;
    }
    return t;
})();
function crc32(buf) {
    let c = 0xFFFFFFFF;
    for (const b of buf) c = CRC_TABLE[(c ^ b) & 0xFF] ^ (c >>> 8);
    return (c ^ 0xFFFFFFFF) >>> 0;
}
function u16(n) { const b = Buffer.alloc(2); b.writeUInt16LE(n); return b; }
function u32(n) { const b = Buffer.alloc(4); b.writeUInt32LE(n >>> 0); return b; }
function buildZip(files) { // files: [{name, data}]
    const locals = [], centrals = [];
    let offset = 0;
    for (const f of files) {
        const name = Buffer.from(f.name), data = Buffer.from(f.data), crc = crc32(data);
        const local = Buffer.concat([
            Buffer.from('PK\x03\x04', 'binary'), u16(20), u16(0), u16(0), u16(0), u16(0x21),
            u32(crc), u32(data.length), u32(data.length), u16(name.length), u16(0), name, data]);
        centrals.push(Buffer.concat([
            Buffer.from('PK\x01\x02', 'binary'), u16(20), u16(20), u16(0), u16(0), u16(0), u16(0x21),
            u32(crc), u32(data.length), u32(data.length), u16(name.length),
            u16(0), u16(0), u16(0), u16(0), u32(0), u32(offset), name]));
        locals.push(local);
        offset += local.length;
    }
    const cd = Buffer.concat(centrals);
    const eocd = Buffer.concat([
        Buffer.from('PK\x05\x06', 'binary'), u16(0), u16(0), u16(files.length), u16(files.length),
        u32(cd.length), u32(offset), u16(0)]);
    return Buffer.concat([...locals, cd, eocd]);
}

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

        // 閲覧モードへ戻してブラウザのロックを手放し、APIで添付を置く。
        await setEditMode(page, false);
        await page.waitForTimeout(500); // unlock の往復を待つ

        const lockRes = await page.request.post(BASE + '/api/lock?id=' + pageId, { headers: { 'Origin': BASE } });
        const token = (await lockRes.json()).token;
        const up1 = await page.request.post(BASE + '/api/upload-pdf', {
            headers: { 'Origin': BASE, 'X-Lock-Token': token },
            multipart: {
                page_id: pageId,
                pdf_file: { name: 'order.pdf', mimeType: 'application/pdf', buffer: Buffer.from('%PDF-1.4 fake e2e') },
            },
        });
        const zipBuf = buildZip([
            { name: 'drawings/A-100.dxf', data: 'dxf data here' },
            { name: 'orders/chumon.pdf', data: '%PDF-1.4 in zip' },
            { name: 'list.txt', data: 'hello' },
        ]);
        const up2 = await page.request.post(BASE + '/api/upload-file', {
            headers: { 'Origin': BASE, 'X-Lock-Token': token },
            multipart: {
                page_id: pageId,
                file: { name: 'parts.zip', mimeType: 'application/zip', buffer: zipBuf },
            },
        });
        check('PDFとZIPをアップロードできる', up1.ok() && up2.ok());
        const pdfName = (await up1.json()).file_name;
        const zipName = (await up2.json()).file_name;
        await page.request.post(BASE + '/api/unlock?id=' + pageId + '&token=' + token, { headers: { 'Origin': BASE } });

        // リンクブロックを本文へ足す（編集モードで書いて保存）。
        await setEditMode(page, true);
        await page.evaluate(({ pid, pdf, zip }) => {
            const target = document.querySelector('#w-editor-content .block-content > *');
            const mk = (file, label) => {
                const p = document.createElement('p');
                const a = document.createElement('a');
                a.href = '/' + pid + '/' + file;
                a.setAttribute('download', label);
                a.textContent = '📎 ' + label;
                p.appendChild(a);
                return p;
            };
            target.after(mk(pdf, 'order.pdf'), mk(zip, 'parts.zip'));
        }, { pid: pageId, pdf: pdfName, zip: zipName });
        await page.evaluate(() => document.querySelector('#w-editor-content [contenteditable]')
            .dispatchEvent(new InputEvent('input', { bubbles: true })));
        await page.waitForFunction(() => document.getElementById('w-save-status').innerText.includes('保存済'), null, { timeout: 8000 });

        // 編集モードでは展開ボタンが出ない。
        check('編集モードに展開ボタンは無い', await page.locator('.attach-expand').count() === 0);

        // 閲覧モードで ▶ が2つ出る。
        await setEditMode(page, false);
        check('閲覧モードで展開・解析ボタンが3つ出る', await page.locator('.attach-expand').count() === 3);

        // PDF: ▶表示 → embed が差し込まれ、▼閉じる → 消える。
        // クリックでボタン文字が「閉じる」へ変わるので、文字ではなく位置で掴む
        // （PDFリンクを先・ZIPを後に挿しているのでDOM順が固定）。
        const pdfBtn = page.locator('.attach-expand:not(.attach-analyze)').nth(0);
        await pdfBtn.click();
        await page.waitForSelector('embed.attach-preview-pdf', { timeout: 4000 });
        const embedSrc = await page.locator('embed.attach-preview-pdf').getAttribute('src');
        check('embed の宛先がきれいなURL', embedSrc === '/' + pageId + '/' + pdfName);
        check('ボタンが「閉じる」になる', (await pdfBtn.innerText()).includes('閉じる'));
        await pdfBtn.click();
        check('閉じると embed が消える', await page.locator('embed.attach-preview-pdf').count() === 0);

        // ZIP: ▶中身 → 目録が出る（サブフォルダのパスとサイズ）。
        await page.locator('.attach-expand:not(.attach-analyze)').nth(1).click();
        await page.waitForSelector('.attach-zip-list', { timeout: 4000 });
        const listText = await page.locator('.attach-zip-list').innerText();
        check('ZIPの目録にサブフォルダのパスが出る', listText.includes('drawings/A-100.dxf'));
        check('ZIPの目録にサイズが出る', /\d+ B/.test(listText));
        check('ZIPの目録に3件出る', await page.locator('.attach-zip-list li').count() === 3);

        // 解析ボタン（人間ゲート型）——PDFリンクの横と、ZIP目録のPDF行にだけ出る。
        // このサーバーは GEMINI_API_KEY 無しで動いているので、押すと設定を促す
        // 通知が出るところまでが配線の検証（判定はGoテストが偽物判定で固定）。
        check('PDFリンクの横に解析ボタンが出る', await page.locator('p .attach-analyze').count() === 1);
        check('ZIP目録のPDF行にだけ解析ボタンが出る', await page.locator('.attach-zip-list .attach-analyze').count() === 1);
        await page.locator('p .attach-analyze').click();
        await page.waitForFunction(() => /GEMINI_API_KEY/.test(document.getElementById('w-toast-host').innerText), null, { timeout: 8000 });
        check('キー未設定の通知が出る', true);

        // 正本は汚れていない（クロームは保存されない）。
        const loadedHTML = await (await page.request.get(BASE + '/api/load?id=' + pageId)).text();
        check('本文に embed が保存されていない', !/attach-preview|<embed/.test(loadedHTML));
        check('本文にリンクは保存されている', loadedHTML.includes(pdfName));

        check('CSP違反なし', cspViolations.length === 0);
        check('ページエラーなし', errs.length === 0);
    } catch (e) {
        check('例外なし: ' + e.message, false);
    } finally {
        await browser.close();
    }
    console.log(results.join('\n'));
    console.log(`\n結果: ${results.length - failCount} pass / ${failCount} fail`);
    process.exit(failCount ? 1 : 0);
})();
