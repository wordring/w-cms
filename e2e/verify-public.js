// 公開専用ビューのE2E（要件定義書 §4.4・認証認可設計 §10.5）。
//
// 匿名の訪問者に**編集用クロームを含まない体裁**が届くこと、SEO/SNS共有のメタ情報が
// 付くこと、キャッシュの切り分け（公開＝可・認証済み＝不可）が効くこと、
// robots.txt / sitemap.xml が公開ページだけを案内することを見る。
//
// 途中で実データの公開フラグを立てるので、**最後に必ず元へ戻す**（finally）。
const { createRequire } = require('module');
const path = require('path');
let chromium;
try { ({ chromium } = createRequire(path.join(process.cwd(), 'noop.js'))('playwright')); }
catch (e) { console.error('node_modules のあるディレクトリ（e2e/）から実行してください'); process.exit(2); }

const BASE = process.env.WCMS_BASE || 'http://localhost:8080';
const H = { Origin: BASE };

const results = []; let failCount = 0;
function check(name, cond) { results.push(`${cond ? 'PASS' : 'FAIL'} ${name}`); if (!cond) failCount++; }

// setPublic はページの公開フラグを切り替える（編集ロックが要る）。
async function setPublic(page, id, value) {
    const lock = await page.request.post(BASE + '/api/lock?id=' + id, { headers: H });
    const token = (await lock.json()).token;
    const res = await page.request.post(BASE + '/api/page-perms?id=' + id, {
        headers: { ...H, 'X-Lock-Token': token, 'Content-Type': 'application/json' },
        data: { public: value },
    });
    await page.request.post(BASE + '/api/unlock?id=' + id + '&token=' + token, { headers: H });
    if (res.status() !== 200) throw new Error('page-perms failed: ' + res.status() + ' ' + (await res.text()));
}

async function newPageWithBody(page, parent, html) {
    const res = await page.request.post(BASE + '/api/new-page?parent=' + parent, { headers: H, maxRedirects: 0 });
    const loc = res.headers()['location'];
    if (!loc) throw new Error('new-page failed: ' + res.status() + ' ' + (await res.text()));
    const id = loc.replace(/^\//, '').replace(/\?.*$/, '');
    const lock = await page.request.post(BASE + '/api/lock?id=' + id, { headers: H });
    const token = (await lock.json()).token;
    const saved = await page.request.post(BASE + '/api/save', { headers: H, data: { page_id: id, html, token } });
    if (saved.status() !== 200) throw new Error('save failed: ' + saved.status());
    await page.request.post(BASE + '/api/unlock?id=' + id + '&token=' + token, { headers: H });
    return id;
}

(async () => {
    const browser = await chromium.launch({ headless: !process.argv.includes('--headed') });
    const admin = await browser.newPage();
    const errs = []; const cspViolations = [];
    let pageId = null;
    let published = false;
    try {
        await admin.goto(BASE + '/login');
        await admin.fill('#username', 'a'); await admin.fill('#password', 'a');
        await admin.click('button[type=submit]');
        await admin.waitForURL('**/000000**', { timeout: 8000 });

        pageId = await newPageWithBody(admin, '000000',
            '<h1>会社案内</h1>' +
            '<p>板金加工と精密板金の受託を承ります。</p>' +
            '<p>2つめの段落は description に使われない。</p>');

        // ── 公開する前: 匿名には 404（不存在と区別しない） ──
        const anon = await browser.newContext();
        const visitor = await anon.newPage();
        visitor.on('pageerror', e => errs.push(String(e)));
        visitor.on('console', m => { const t = m.text(); if (/Content.Security.Policy|Refused to/i.test(t)) cspViolations.push(t); });

        let res = await visitor.request.get(BASE + '/' + pageId);
        check('公開前は匿名に404', res.status() === 404);
        check('非公開ページの応答はキャッシュ不可', (res.headers()['cache-control'] || '') === 'no-store');

        // ── 公開する（トップも公開しないとパスゲートで閉じたまま） ──
        await setPublic(admin, '000000', true);
        await setPublic(admin, pageId, true);
        published = true;

        // ── 公開後: 匿名には公開専用ビュー ──
        await visitor.goto(BASE + '/' + pageId);
        const html = await visitor.content();
        check('本文が届く', html.includes('板金加工と精密板金の受託を承ります。'));
        check('編集用スクリプトを配信しない', !html.includes('/assets/app.js'));
        check('編集用の殻の要素が無い', !html.includes('w-editor-content') && !html.includes('w-html-preview'));
        check('公開専用の殻が使われる', html.includes('w-public-content'));
        check('公開用CSSが読み込まれる', html.includes('/assets/public.css'));
        check('公開用CSSが実際に効いている',
            await visitor.locator('.public-content').evaluate(el => getComputedStyle(el).maxWidth !== 'none'));

        // ── SEO / SNS共有 ──
        check('description が本文の最初の段落から作られる',
            await visitor.locator('meta[name="description"]').getAttribute('content')
            === '板金加工と精密板金の受託を承ります。');
        check('og:title がページ名', await visitor.locator('meta[property="og:title"]').getAttribute('content') === '会社案内');
        const canonical = await visitor.locator('link[rel="canonical"]').getAttribute('href');
        check('canonical が正規URL', canonical === BASE + '/' + pageId);
        check('og:url は canonical と同じ',
            await visitor.locator('meta[property="og:url"]').getAttribute('content') === canonical);

        // ── キャッシュの切り分け ──
        res = await visitor.request.get(BASE + '/' + pageId);
        const cc = res.headers()['cache-control'] || '';
        check('公開ページはキャッシュ可能', cc.includes('public') && cc.includes('max-age=600'));
        check('Vary: Cookie が付く', (res.headers()['vary'] || '').includes('Cookie'));
        const etag = res.headers()['etag'];
        check('ETag が付く', !!etag);
        const revalidated = await visitor.request.get(BASE + '/' + pageId, { headers: { 'If-None-Match': etag } });
        check('同じ ETag なら 304', revalidated.status() === 304);

        const authed = await admin.request.get(BASE + '/' + pageId);
        check('認証済みの応答はキャッシュ不可', (authed.headers()['cache-control'] || '') === 'no-store');
        check('認証済みには編集用の殻', (await authed.text()).includes('w-editor-content'));
        const meRes = await admin.request.get(BASE + '/api/me');
        check('/api/me はキャッシュ不可', (meRes.headers()['cache-control'] || '') === 'no-store');
        const permsRes = await admin.request.get(BASE + '/api/page-perms?id=' + pageId);
        check('要認証APIはキャッシュ不可', (permsRes.headers()['cache-control'] || '') === 'no-store');

        // ── クローラ向けの2本 ──
        const robots = await visitor.request.get(BASE + '/robots.txt');
        const robotsBody = await robots.text();
        check('robots は sitemap を案内する', robotsBody.includes('Sitemap: ' + BASE + '/sitemap.xml'));
        check('robots は API を閉じる', robotsBody.includes('Disallow: /api/'));

        const sitemap = await visitor.request.get(BASE + '/sitemap.xml');
        const sitemapBody = await sitemap.text();
        check('sitemap は XML で返る', (sitemap.headers()['content-type'] || '').includes('xml'));
        check('sitemap に公開ページが載る', sitemapBody.includes(BASE + '/' + pageId));
        check('sitemap に lastmod が入る', sitemapBody.includes('<lastmod>'));

        // 非公開へ戻すと、その足で匿名からは消える（sitemap からも落ちる）。
        await setPublic(admin, pageId, false);
        const after = await visitor.request.get(BASE + '/' + pageId);
        check('非公開へ戻すと匿名には404', after.status() === 404);
        const sitemap2 = await (await visitor.request.get(BASE + '/sitemap.xml')).text();
        check('非公開へ戻すと sitemap から落ちる', !sitemap2.includes('/' + pageId));
        await setPublic(admin, pageId, true);

        check('CSP違反なし', cspViolations.length === 0);
        check('ページエラーなし', errs.length === 0);
        if (cspViolations.length) console.error('CSP:', cspViolations.slice(0, 3));
        if (errs.length) console.error('ERRS:', errs.slice(0, 3));
    } catch (e) { check('実行が最後まで到達', false); console.error(e); }
    finally {
        // 実データの公開フラグを必ず元へ戻す（検証のために開けたままにしない）。
        if (published) {
            try {
                if (pageId) await setPublic(admin, pageId, false);
                await setPublic(admin, '000000', false);
            } catch (e) { console.error('公開フラグを戻せませんでした:', e.message); }
        }
        await browser.close();
    }
    console.log(results.join('\n'));
    console.log(failCount === 0 ? `\n✅ 全 ${results.length} 項目 通過` : `\n❌ ${failCount} 件の失敗`);
    process.exit(failCount === 0 ? 0 : 1);
})();
