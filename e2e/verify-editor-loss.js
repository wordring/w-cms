// w-cms 「黙って入力が消える」2件の自動E2E検証
//
// docs/判断待ち.md E-1／E-2。どちらも画面には「編集モード」「✅保存済」が出たまま、
// 実際には入力が保存されない（＝利用者が気づけない）種類の欠陥。
//
//   E-1 サーバーが安全化した本文を返した瞬間に、ページの全ブロックが編集不能になる
//       原因: applySanitizedHtml が outerHTML を直接比較していた。編集モードの
//       contenteditable とサーバー事前描画のクローム（.vocab-chrome）は現在のDOMに
//       だけ在るので**必ず不一致**になり、全ブロックが毎回差し替えられていた。
//       差し替え後に applyMode を呼ばないので contenteditable も失われる。
//
//   E-2 本文の語彙（/api/tag-schema）を取り損ねると、以後の入力が一切保存されないのに
//       「保存済」と出続ける。原因: 取得失敗時に tagSchema = {} を代入していたため
//       ガード `if (!tagSchema)` が素通りし、語彙が空＝全要素が保存対象外になっていた。
//
// 前提: サーバーが http://localhost:8080 で起動済み。ログインは a / a（ローカル検証専用）。
// 実行: cd e2e && node verify-editor-loss.js
const { chromium } = require('playwright');

const BASE = process.env.WCMS_BASE || 'http://localhost:8080';
const results = []; let failCount = 0;
function check(name, cond) { results.push(`${cond ? 'PASS' : 'FAIL'} ${name}`); if (!cond) failCount++; }

async function login(page) {
    await page.goto(BASE + '/login');
    await page.fill('#w-username, #username', 'a');
    await page.fill('#w-password, #password', 'a');
    await page.click('button[type=submit]');
    await page.waitForURL('**/000000**', { timeout: 8000 });
}

// newPage は子ページを作ってIDを返す（/api/new-page は POST 限定）。
async function newPage(page, parent) {
    const res = await page.request.post(BASE + '/api/new-page?parent=' + parent, {
        headers: { 'Origin': BASE }, maxRedirects: 0,
    });
    const loc = res.headers()['location'];
    if (!loc) throw new Error(`new-page failed: ${res.status()} ${await res.text()}`);
    return loc.replace(/^\//, '').replace(/\?.*$/, '');
}

async function saveBody(page, id, html) {
    const res = await page.request.post(BASE + '/api/save', {
        headers: { 'Origin': BASE, 'Content-Type': 'application/json' },
        data: { page_id: id, html },
    });
    if (!res.ok()) throw new Error(`save ${id} failed: ${res.status()}`);
    return res.json();
}

async function enterEditMode(page) {
    await page.evaluate(() => document.getElementById('w-mode-toggle').click());
    await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
    await page.waitForTimeout(600); // ロック取得と本文の読み直しを待つ
}

// countEditable は編集できるブロックの数を返す。
const countEditable = page => page.evaluate(() =>
    document.querySelectorAll('#w-editor-content .block-content > [contenteditable="true"]').length);

(async () => {
    const browser = await chromium.launch({ headless: !process.argv.includes('--headed') });
    const page = await browser.newPage();
    const errs = [];
    page.on('pageerror', e => errs.push(String(e)));
    try {
        await login(page);

        // ── E-1: 安全化された応答で編集不能にならないこと ─────────────────
        const id1 = await newPage(page, '000000');
        // 計算ビュー（子ページ一覧）を含めておく。中身はサーバーが毎回描くクロームで、
        // 差し替えが起きるとこれも消える——「消えない」ことまで確かめたいので入れる。
        await saveBody(page, id1,
            '<h1>安全化の検証</h1><p>ひとつめの段落</p><p>ふたつめの段落</p>' +
            '<section data-type="child-list"></section>');

        await page.goto(BASE + '/' + id1 + '?edit=true');
        await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
        await page.waitForTimeout(800);

        const before = await countEditable(page);
        check('前提: 編集できるブロックがある', before > 0);
        const chromeBefore = await page.evaluate(() =>
            document.querySelectorAll('#w-editor-content .vocab-chrome').length);
        check('前提: 計算ビューの中身が描かれている', chromeBefore > 0);

        // **サーバーだけが落とすもの**を入れて保存させる。`class` はクライアントの
        // シリアライザが送る前に落としてしまうので誘発できない。危険なURLの `href` は
        // 属性自体が語彙にあるためクライアントは送り、サーバーの safeLinkURL が落とす
        // ——外部からリンクつきの内容を貼った状況と同じ。応答は sanitized=true になる。
        await page.evaluate(() => {
            const p = document.querySelector('#w-editor-content .block-content > p');
            p.textContent = '貼り付けた内容: ';
            const a = document.createElement('a');
            a.setAttribute('href', 'javascript:alert(1)');
            a.textContent = 'あぶないリンク';
            p.appendChild(a);
        });
        // 自動保存（1.5秒）＋応答の反映を待つ
        await page.waitForTimeout(4000);
        const sanitizedFired = await page.evaluate(() =>
            !document.querySelector('#w-editor-content a[href^="javascript"]'));
        check('前提: サーバーの安全化が実際に走った', sanitizedFired);

        const after = await countEditable(page);
        check(`E-1: 安全化のあとも編集できる（${before} → ${after}）`, after >= before);
        const chromeAfter = await page.evaluate(() =>
            document.querySelectorAll('#w-editor-content .vocab-chrome').length);
        check(`E-1: 安全化のあとも計算ビューの中身が残る（${chromeBefore} → ${chromeAfter}）`,
            chromeAfter >= chromeBefore);

        // 実際に打てることまで確かめる（contenteditable が付いていても壊れていたら分かる）
        await page.evaluate(() => {
            const p = document.querySelector('#w-editor-content .block-content > p');
            p.focus();
        });
        await page.keyboard.type('あいう');
        const typed = await page.evaluate(() =>
            document.querySelector('#w-editor-content .block-content > p').textContent);
        check('E-1: 安全化のあとも実際に文字が入る', typed.includes('あいう'));

        // ── E-2: 語彙を取り損ねたら「保存済」と出さず、はっきり知らせること ──
        const id2 = await newPage(page, '000000');
        await saveBody(page, id2, '<h1>語彙の取得失敗</h1><p>もとの本文</p>');

        const page2 = await browser.newPage();
        const errs2 = [];
        page2.on('pageerror', e => errs2.push(String(e)));
        await login(page2);
        // 語彙の取得だけを落とす
        await page2.route('**/api/tag-schema', route => route.abort());
        await page2.goto(BASE + '/' + id2 + '?edit=true');
        await page2.waitForTimeout(2000); // 語彙の取得失敗と編集モード投入を待つ

        await page2.evaluate(() => {
            const p = document.querySelector('#w-editor-content .block-content > p')
                || document.querySelector('#w-editor-content p');
            if (p) { p.textContent = '保存されないはずの入力'; }
        });
        await page2.waitForTimeout(4000);

        const status = await page2.evaluate(() => document.getElementById('w-save-status').innerText);
        check(`E-2: 語彙が無いとき「保存済」と偽らない（実際の表示: ${JSON.stringify(status)}）`,
            !status.includes('保存済'));

        const toldUser = await page2.evaluate(() =>
            document.getElementById('w-toast-host').innerText.length > 0);
        check('E-2: 語彙が無いことを画面で知らせる', toldUser);

        // 正本が空で上書きされていないこと（これが最悪の壊れ方）
        const body2 = await (await page.request.get(BASE + '/api/load?id=' + id2)).text();
        check('E-2: 正本が空で上書きされていない', body2.includes('もとの本文'));

        check('E-1側: ページエラーなし', errs.length === 0);
        if (errs.length) console.error('ERRS:', errs.slice(0, 3));
        check('E-2側: ページエラーなし', errs2.length === 0);
        if (errs2.length) console.error('ERRS2:', errs2.slice(0, 3));
    } catch (e) { check('実行が最後まで到達', false); console.error(e); }
    finally { await browser.close(); }
    console.log(results.join('\n'));
    console.log(failCount === 0 ? `\n✅ 全 ${results.length} 項目 通過` : `\n❌ ${failCount} 件の失敗`);
    process.exit(failCount === 0 ? 0 : 1);
})();
