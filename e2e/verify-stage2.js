// w-cms 汎用表エディタの自動E2E検証（第2段: 列操作・列設定・dl項目操作・型検証・enum補助）
//
// 会社側の verify-stage1.js（31項目・第1段の範囲）と同じ流儀の独立スクリプト。
// 第1段の主要動作（骨格挿入・セル編集・行操作・保存往復・未知種別トースト・閲覧モード・
// JSエラーなし）も回帰として含む。
//
// 前提: サーバーが http://localhost:8080 で起動済み（.claude/launch.json の w-cms）。
// ログインは CLAUDE.md 記載のローカル検証専用 a / a（本番では使わない）。
// 実行: cd ~\tools\wcms-e2e && node "$env:OneDrive\tools\wcms-e2e\verify-stage2.js"
//       （--headed でブラウザ表示）
// 注意: 実行のたびにテストページが1枚できる（管理コンソールの「DB再構築」で整理可）。
//
// 本体は OneDrive（%OneDrive%\tools\wcms-e2e\）が正本で、両マシンへ自動同期される。
// node_modules は各マシンの ~\tools\wcms-e2e\ に置く（数千ファイルの同期を避ける）ため、
// playwright は**スクリプトの場所ではなくカレントディレクトリ**から解決する。

const { createRequire } = require('module');
const path = require('path');
let chromium;
try {
    ({ chromium } = createRequire(path.join(process.cwd(), 'noop.js'))('playwright'));
} catch (e) {
    console.error('playwright が見つかりません。node_modules のあるディレクトリから実行してください:');
    console.error('  cd %USERPROFILE%\\tools\\wcms-e2e');
    console.error('  node "%OneDrive%\\tools\\wcms-e2e\\verify-stage2.js"');
    console.error('（初回セットアップ: npm install playwright && npx playwright install chromium）');
    process.exit(2);
}

const BASE = process.env.WCMS_BASE || 'http://localhost:8080';
const HEADED = process.argv.includes('--headed');


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

const results = [];
let failCount = 0;
function check(name, cond) {
    results.push(`${cond ? 'PASS' : 'FAIL'} ${name}`);
    if (!cond) failCount++;
}

// セルの中身を選択してから打鍵すると置換になる（contenteditable の Ctrl+A は
// ブロック全体に及んで表を消しかねないため使わない）。
async function selectContents(page, handle) {
    await handle.evaluate(el => {
        const r = document.createRange();
        r.selectNodeContents(el);
        const s = window.getSelection();
        s.removeAllRanges();
        s.addRange(r);
    });
}

async function caretInto(page, handle) {
    await handle.click();
    await handle.evaluate(el => {
        const r = document.createRange();
        r.selectNodeContents(el);
        r.collapse(false);
        const s = window.getSelection();
        s.removeAllRanges();
        s.addRange(r);
    });
}

async function waitSaved(page) {
    await page.waitForFunction(
        () => document.getElementById('w-save-status').innerText.includes('保存済'),
        null, { timeout: 8000 });
}

(async () => {
    const browser = await chromium.launch({ headless: !HEADED });
    const page = await browser.newPage();
    const pageErrors = [];
    page.on('pageerror', e => pageErrors.push(String(e)));

    try {
        // ── ログイン（ローカル検証専用の a / a） ──
        await page.goto(BASE + '/login');
        await page.fill('#username', 'a');
        await page.fill('#password', 'a');
        await page.click('button[type=submit]');
        await page.waitForURL('**/000000**', { timeout: 8000 });
        check('ログインしてトップページへ', true);

        // ── 新規ページ（?edit=true で自動的に編集モード） ──
        await gotoNewPage(page, '000000');
        await page.waitForFunction(() => document.body.hasAttribute('edit-mode'), null, { timeout: 8000 });
        const pageURL = page.url();
        check('新規ページが編集モードで開く', /\?edit=true/.test(pageURL));

        // ── スラッシュメニュー → 検査記録の骨格挿入（第1段の回帰） ──
        const p = page.locator('#w-editor-content p').first();
        await p.click();
        await selectContents(page, p);
        await page.keyboard.type('/');
        await page.waitForSelector('#w-slash-menu.active', { timeout: 4000 });
        check('スラッシュメニューが開く', true);
        const vocabItem = page.locator('#w-slash-menu .slash-menu-item[data-type="vocab:inspection-record"]');
        check('レジストリ由来の項目がある', await vocabItem.count() === 1);
        await vocabItem.click();
        const table = page.locator('#w-editor-content table[data-type="inspection-record"]');
        await table.waitFor({ timeout: 4000 });
        const headers = await table.locator('tr').first().locator('th').allInnerTexts();
        check('骨格: 見出しがレジストリの列定義どおり', headers.join(',') === '品番,判定,検査写真,検査日');
        check('骨格: データ行が1行ある', await table.locator('tr').count() === 2);

        // ── セル編集と保存往復（第1段の回帰） ──
        const cell = (r, c) => table.locator('tr').nth(r).locator('th, td').nth(c);
        await caretInto(page, cell(1, 0));
        await page.keyboard.type('SHAFT-01');
        await waitSaved(page);
        check('セル編集が保存される', true);

        // ── 行操作（第1段の回帰） ──
        await caretInto(page, cell(1, 0));
        await page.click('#w-tt-add');
        check('行追加: データ行が2行に', await table.locator('tr').count() === 3);
        await caretInto(page, cell(2, 0));
        await page.keyboard.type('SHAFT-02');
        await page.click('#w-tt-up');
        check('行移動: 上の行と入れ替わる', (await cell(1, 0).innerText()) === 'SHAFT-02');
        await page.click('#w-tt-del');
        check('行削除: 1行へ戻る', await table.locator('tr').count() === 2);

        // ── 列操作（第2段） ──
        await caretInto(page, cell(0, 1)); // 「判定」の見出し
        await page.click('#w-tt-col-add');
        check('列追加: 見出しが5列', await table.locator('tr').first().locator('th').count() === 5);
        check('列追加: データ行も5列', await table.locator('tr').nth(1).locator('td').count() === 5);
        await caretInto(page, cell(0, 2)); // 追加された空見出し
        await page.keyboard.type('数量');
        // ── 列設定ポップオーバ（第2段） ──
        await page.click('#w-tt-col-cfg');
        await page.waitForSelector('#w-col-popover.active', { timeout: 4000 });
        check('列設定: 鍵は見出しテキスト', (await page.locator('#w-cp-key').innerText()) === '数量');
        check('列設定: 推論の注記（number）', (await page.locator('#w-cp-note').innerText()).includes('number'));
        await page.selectOption('#w-cp-type', 'date');
        check('列設定: 型の明示が th に付く',
            (await cell(0, 2).getAttribute('data-type')) === 'date');
        // ポップオーバは列見出しの直下＝データセルに重なるため、実利用と同じく
        // 外をクリックして閉じてから下のセルを操作する。
        await page.locator('#w-editor-content h1').click();
        check('列設定: 外クリックで閉じる',
            !(await page.locator('#w-col-popover').evaluate(el => el.classList.contains('active'))));
        // ── 型検証（明示が推論に優先） ──
        await caretInto(page, cell(1, 2));
        await page.keyboard.type('123');
        check('型検証: date 明示の列で 123 は赤印',
            await cell(1, 2).evaluate(el => el.classList.contains('cell-invalid')));
        await caretInto(page, cell(0, 2));
        await page.click('#w-tt-col-cfg');
        await page.waitForSelector('#w-col-popover.active', { timeout: 4000 });
        await page.selectOption('#w-cp-type', '');
        check('列設定: 空で属性が外れる', (await cell(0, 2).getAttribute('data-type')) === null);
        await page.locator('#w-editor-content h1').click();
        await selectContents(page, cell(1, 2));
        await page.keyboard.type('1,234円');
        check('型検証: 推論（数量→number）で通貨表記は無印',
            !(await cell(1, 2).evaluate(el => el.classList.contains('cell-invalid'))));
        // 日付列の検証
        await caretInto(page, cell(1, 4));
        await page.keyboard.type('あした');
        check('型検証: 日付列の解釈できない値は赤印',
            await cell(1, 4).evaluate(el => el.classList.contains('cell-invalid')));
        await selectContents(page, cell(1, 4));
        await page.keyboard.type('2026年8月19日');
        check('型検証: 和暦風の表記でも実在日付なら無印',
            !(await cell(1, 4).evaluate(el => el.classList.contains('cell-invalid'))));

        // ── 列移動・列削除（第2段） ──
        await caretInto(page, cell(0, 2)); // 「数量」
        await page.click('#w-tt-col-left');
        const headers2 = await table.locator('tr').first().locator('th').allInnerTexts();
        check('列移動: 見出しの順序が入れ替わる', headers2.join(',') === '品番,数量,判定,検査写真,検査日');
        check('列移動: データ行も同期', (await cell(1, 1).innerText()) === '1,234円');
        await caretInto(page, cell(0, 1));
        await page.click('#w-tt-col-del');
        const headers3 = await table.locator('tr').first().locator('th').allInnerTexts();
        check('列削除: 元の4列へ戻る', headers3.join(',') === '品番,判定,検査写真,検査日');

        // ── enum の入力補助（第2段） ──
        await caretInto(page, cell(1, 1)); // 「判定」のデータセル
        await page.waitForSelector('#w-enum-menu.active', { timeout: 4000 });
        const choices = await page.locator('#w-enum-menu button').allInnerTexts();
        check('enum: 選択肢はレジストリの2件', choices.join(',') === '合格,不合格');
        await page.locator('#w-enum-menu button').first().click();
        check('enum: クリックで値が入る', (await cell(1, 1).innerText()) === '合格');
        check('enum: 選択後メニューは閉じる',
            !(await page.locator('#w-enum-menu').evaluate(el => el.classList.contains('active'))));

        // ── dl の項目操作（第2段。レジストリに dl 形式が無いため DOM で用意し UI を検証） ──
        await page.evaluate(() => {
            const dl = document.createElement('dl');
            dl.setAttribute('data-type', 'tags');
            const dt1 = document.createElement('dt'); dt1.textContent = '希望納期';
            const dd1 = document.createElement('dd'); dd1.textContent = '2026-07-10';
            const dt2 = document.createElement('dt'); dt2.textContent = '納入場所';
            const dd2 = document.createElement('dd'); dd2.textContent = '本社工場';
            dl.append(dt1, dd1, dt2, dd2);
            document.getElementById('w-editor-content').appendChild(dl);
            wrapInBlock(dl);
            applyMode(); // wrapInBlock は editable 化しない（既存経路では applyMode が担う）
        });
        const dl = page.locator('#w-editor-content dl[data-type="tags"]');
        await dl.locator('dt').first().click();
        await page.waitForSelector('#w-dl-toolbar.active', { timeout: 4000 });
        check('dl: ツールバーが出る', true);
        await page.click('#w-dt-add');
        check('dl: 項目追加で dt が3つ', await dl.locator('dt').count() === 3);
        await page.keyboard.type('担当');
        await page.click('#w-dt-val');
        await page.keyboard.type('山田');
        await page.click('#w-dt-val');
        await page.keyboard.type('佐藤');
        const names = await dl.locator('dt').allInnerTexts();
        check('dl: 追加位置は現在項目の直後', names.join(',') === '希望納期,担当,納入場所');
        await dl.locator('dt').nth(1).click(); // 「担当」
        await page.click('#w-dt-down');
        check('dl: 項目移動（多値の dd ごと）',
            (await dl.locator('dt').allInnerTexts()).join(',') === '希望納期,納入場所,担当');
        await dl.locator('dt').nth(2).click();
        await page.click('#w-dt-del');
        check('dl: 項目削除（dd も一緒に消える）',
            await dl.locator('dt').count() === 2 && await dl.locator('dd').count() === 2);

        // ── 未知種別の告知トースト（第1段の回帰） ──
        await page.evaluate(() => {
            const t = document.createElement('table');
            t.setAttribute('data-type', 'mystery-form');
            const tr1 = document.createElement('tr');
            const th = document.createElement('th'); th.textContent = '項目'; tr1.appendChild(th);
            const tr2 = document.createElement('tr');
            const td = document.createElement('td'); td.textContent = '値'; tr2.appendChild(td);
            t.append(tr1, tr2);
            document.getElementById('w-editor-content').appendChild(t);
            wrapInBlock(t);
            triggerAutoSave();
        });
        await page.waitForFunction(
            () => document.getElementById('w-toast-host').innerText.includes('未定義の種別'),
            null, { timeout: 8000 });
        check('未知種別: 保存時にトーストで告知（拒否しない）', true);
        // 告知は**目立つ**こと（ユーザー要望「赤色背景などで告知してください」・2026-08-25）。
        // 綴り違いは黙って計算から外れるので、他の控えめなトーストに紛れてはいけない。
        const alertToast = page.locator('[data-toast-id="unknown-vocab"]');
        check('未知種別: 告知は目立つ赤（alert）',
            (await alertToast.getAttribute('class') || '').includes('alert'));
        check('未知種別: 背景が赤い',
            /^rgb\(1[0-9]{2}, [0-9]{1,2}, [0-9]{1,2}\)$/.test(
                await alertToast.evaluate(el => getComputedStyle(el).backgroundColor)));
        // 見落とすと綴り違いに気づけないので、勝手に消えないこと。
        await page.waitForTimeout(9000);
        check('未知種別: 時間で勝手に消えない', await alertToast.count() === 1);

        // ── ページタイトルの印（位置で決まる＝属性にしない） ──
        check('最初の h1 に印が付く', await page.evaluate(() => {
            const hs = document.querySelectorAll('#w-editor-content h1');
            return hs.length > 0 && hs[0].classList.contains('is-page-title');
        }));
        await page.evaluate(() => {
            const c = document.getElementById('w-editor-content');
            const h = document.createElement('h1');
            h.textContent = '後から足した見出し';
            c.insertBefore(h, c.firstChild);
            wrapInBlock(h);
            buildToc();
        });
        check('上に見出しを足すと印が移る（属性ならずれる箇所）', await page.evaluate(() => {
            const hs = document.querySelectorAll('#w-editor-content h1');
            return hs[0].textContent === '後から足した見出し' &&
                hs[0].classList.contains('is-page-title') &&
                !hs[1].classList.contains('is-page-title');
        }));
        await page.evaluate(() => triggerAutoSave());
        await waitSaved(page);
        check('印は保存に漏れない',
            !(await page.locator('#w-html-preview').inputValue()).includes('is-page-title'));

        // ── 本文の id（2026-08-20: 殻が接頭辞 w- を独占し、本文の id は自由） ──
        await page.evaluate(() => {
            const ok = document.createElement('p');
            ok.setAttribute('id', 'anchor-1');
            ok.textContent = 'ページ内リンクの目印';
            const bad = document.createElement('p');
            bad.setAttribute('id', 'w-w-html-preview'); // 殻の接頭辞（重ねがけ）
            bad.textContent = '乗っ取りを狙う本文';
            const c = document.getElementById('w-editor-content');
            c.appendChild(ok); c.appendChild(bad);
            wrapInBlock(ok); wrapInBlock(bad);
            triggerAutoSave();
        });
        await page.waitForFunction(
            () => document.getElementById('w-toast-host').innerText.includes('接頭辞は画面側の予約'),
            null, { timeout: 8000 });
        check('id: 殻の接頭辞つきは告知される', true);
        await waitSaved(page);
        const idHTML = await page.locator('#w-html-preview').inputValue();
        check('id: 接頭辞の無い id は保存される', idHTML.includes('id="anchor-1"'));
        check('id: 殻の接頭辞は繰り返し剥がされる',
            idHTML.includes('id="html-preview"') && !idHTML.includes('id="w-'));
        check('id: 殻の要素は奪われていない',
            await page.evaluate(() => document.getElementById('w-html-preview').tagName) === 'TEXTAREA');

        // ── 保存往復（リロードして残っているか・実行時の印が残っていないか） ──
        await waitSaved(page);
        await page.goto(pageURL.replace('?edit=true', ''));
        await page.waitForSelector('#w-editor-content table[data-type="inspection-record"]', { timeout: 8000 });
        const savedHTML = await page.locator('#w-editor-content').innerHTML();
        check('保存往復: 表が残る', savedHTML.includes('data-type="inspection-record"'));
        check('保存往復: dl が残る', savedHTML.includes('data-type="tags"'));
        check('保存往復: 未知種別も保存される', savedHTML.includes('data-type="mystery-form"'));
        check('保存往復: enum で入れた値が残る', savedHTML.includes('合格'));
        check('保存往復: 実行時の印（cell-invalid）が漏れていない', !savedHTML.includes('cell-invalid'));
        check('保存往復: 編集クロームが漏れていない',
            !savedHTML.includes('table-toolbar') && !savedHTML.includes('enum-menu'));

        // ── 閲覧モード（編集クロームが出ない・第1段の回帰） ──
        const barsHidden = await page.evaluate(() =>
            !document.getElementById('w-table-toolbar').classList.contains('active') &&
            !document.getElementById('w-dl-toolbar').classList.contains('active') &&
            !document.getElementById('w-col-popover').classList.contains('active'));
        check('閲覧モード: 編集クロームは非表示', barsHidden);

        check('JSエラーなし（pageerror ゼロ）', pageErrors.length === 0);
        if (pageErrors.length) console.error('pageerrors:', pageErrors);
    } catch (e) {
        check('例外なく完走', false);
        console.error(e);
    } finally {
        await browser.close();
    }

    console.log(results.join('\n'));
    console.log(`\n結果: ${results.length - failCount} pass / ${failCount} fail`);
    process.exit(failCount === 0 ? 0 : 1);
})();
