// 通信の拡張の画面（2026-10-20 に assets/app.js から移した・docs/考察/【考察】画面の JS のコアと拡張.md）。
//
// 通信の記録ページの「記録の箱」（本文の印 <section data-mirror="記録の箱">・中身はサーバーが描く——ext/comm/record_box.go）の
// 「作成」と、☎ かける（`電話番号` のタグの飾り・ext/comm/phone_decor.go）・通信箱の未処理と「＋ 記録する」の配線です。
//
// ⚠ **コアの中身には手を出しません**——使うのは app.js が渡す window.wcms だけ。サーバーは通信の拡張が載っているときだけ
// この JS を読み込ませる（置き場の決まりで拾う・internal/cms/ext_scripts.go——2026-10-20 夕までは cms.RegisterScript で名乗っていた）。
// ⚠ **`innerHTML` へ文字列を入れません**（createElement＋textContent・保存型XSSの前科）。
(function () {
'use strict';
const w = window.wcms;
if (!w) return; // コアが古い（読み込む順が崩れた）——何もしない


// makeFromRecord は選んだ種類のページを作ります（POST /api/record-make）。案件ページ（2026-10-19 まで受注ページ）は Gemini が本文を読むので時間がかかる。
async function makeFromRecord(kind, btn) {
    btn.disabled = true;
    btn.textContent = '作っています…';
    let born = false, open = '';
    try {
        const res = await fetch('/api/record-make', w.jsonPost({ page_id: w.pageId(), kind }));
        const d = await w.readResult(res);
        if (!res.ok || !d || !d.success) {
            w.notify(kind + 'を作れませんでした: ' + w.failMessage(res, d), { type: 'alert', duration: 0, id: 'record-make' });
        } else if (!(d.pages || []).length) {
            w.notify(d.say || 'ページは作りませんでした。', { type: 'warn', duration: 8000, id: 'record-make' });
        } else {
            w.notify(kind + 'を作りました: ' + d.pages.map(o => (o.title || o.page_id) + '（/' + o.page_id + '）').join('\n') +
                (d.say ? '\n' + d.say : ''), { type: 'success', duration: 0, id: 'record-make' });
            born = true;
            open = d.open || '';
        }
    } catch (e) {
        w.notify(kind + 'を作れませんでした: ' + e.message, { type: 'alert', duration: 0, id: 'record-make' });
    }
    btn.disabled = false;
    btn.textContent = '作成';
    if (open) { location.href = '/' + open + '?edit=true'; return; } // 人が続けて書くページは開く
    if (born) {
        await w.reloadContent(); // 子ページ一覧の鏡と、この欄（印つき）を描き直す
        w.loadChildNav();        // 左レールの子ページ一覧
    }
}






// 記録の箱（作るページ・✉️ 返信・🧵 前後・📨 返信の一覧）は、2026-10-20 から本文の印 <section data-mirror="記録の箱"> を
// サーバーが描く（ext/comm/record_box.go）。ここは「作成」を押したときの配線だけ。
w.delegateClick([['.mail-chrome .record-make-go', (btn) => {
    const sel = btn.parentElement && btn.parentElement.querySelector('.record-make-kind');
    if (sel) makeFromRecord(sel.value, btn);
}]]);

// ── 版 3 から通信の JS が持つ部分（2026-10-20 に app.js から移した）——☎ 発信・未処理の一覧の「済」「不要」・「＋ 記録する」。
if (w.version >= 3) {
const { makeEl, makeButton, notify, jsonPost, postJSON, getJSON, readResult, failMessage, tagValue, hasExtension, reloadContent, loadChildNav } = w;

// ── 電話をかける（tel: リンク・2026-09-05）───────────────────────────
//
// ユーザー:「CMSから電話をかける時にページを作るかチェックボックスがあっても
// 良いと思います」。**発信はもともと人が意図してする操作**なので、そこに
// チェック1つを足すのがいちばん安い歯止めになります（着信のように歯止めを
// あとから足す必要がない）。
//
// 発信の記録は `向き：送信` なので、**作業待ちには並びません**——かけた電話は
// 済んだ出来事です。特別扱いのコードは1行も要りません。
//
// `tel:` はサニタイザが元から許すスキームなので、**ソフトフォンがあれば今日から
// 押せます**。CTI の発信APIへ変えるときも、記録の形は変わりません。


// createCallRecord は発信の記録を作り、書けるように開きます。
async function createCallRecord(number) {
    try {
        const res = await fetch('/api/intake/memo', jsonPost({
                channel: '電話',
                direction: '送信',
                phone: number,
                counterpart: w.pageId(),
                title: (document.querySelector('#w-editor-content h1') || {}).textContent || '',
            }));
        const data = await res.json();
        if (!data.success) {
            notify(data.message || '発信の記録を作れませんでした', { type: 'warn' });
            return;
        }
        notify('発信の記録を作りました。開いて内容を書けます。',
            { type: 'success', duration: 8000 });
        location.href = '/' + data.page_id + '?edit=true';
    } catch (e) {
        notify('発信の記録を作れませんでした: ' + e.message, { type: 'warn' });
    }
}

// ── 未処理の一覧の操作（2026-09-05）────────────────────────────────
//
// サーバーが描いた表（view_unhandled.go）に、click を配線するだけの役です。
// **CSP strict なのでインラインの `onclick` は書けません**——サーバーが出せるのは
// 印（`.vocab-chrome` と `data-page-id`）までで、動きはここが与えます。
//
// ボタンは書ける形でも押せます（2026-10-12 から・【考察】編集モードの再考 §6 問い5）。
// それまでは**閲覧モードだけ**でした（編集モードで本文をいじっている最中に行が消えると、
// 何が起きたのか分からないため）。
// ⚠ **ここに `hasExtension('comm')` は要りません**（2026-09-16 に確かめた）。
// 押す相手（`.unhandled-mark`・まとめて片付け）は**サーバーが描いたもの**で、
// 通信が無ければ `unhandled-intake` は語彙レジストリに載らず、鏡の引き金自体が
// 立ちません——本文に `<section><h2>未処理の受信</h2></section>` が書いてあっても
// 空のまま出るので、この関数は何も見つけずに帰ります。**同じ理由で `wireNewRecord`
// にも要りません**（「＋ 記録する」も同じビューが描いています）。
// 出し分けが要るのは「JSが自分で作るクローム」だけ——通信の記録の箱（2026-10-20 から ext/comm/assets/comm.js・それまでは
// `refreshMailChrome`）と電話の「記録を作る」がそれです。
function wireUnhandledActions() {
    const host = document.getElementById('w-editor-content');
    if (!host) return;
    const editMode = false; // 書ける形でも押せる（2026-10-12・問い5。書き換えるのは一覧の行の先の記録ページで、このページの本文ではない——markHandled は書きかけを先に保存しない）
    host.querySelectorAll('.unhandled-mark').forEach(btn => {
        btn.disabled = editMode;
        if (btn.dataset.wired) return;
        btn.dataset.wired = '1';
        btn.addEventListener('click',
            () => markHandled([btn.dataset.pageId], btn, btn.dataset.value));
    });
    host.querySelectorAll('#w-unhandled-bulk-done, #w-unhandled-bulk-skip').forEach(bulk => {
        bulk.disabled = editMode;
        if (bulk.dataset.wired) return;
        bulk.dataset.wired = '1';
        bulk.addEventListener('click', () => {
            const picked = Array.from(
                host.querySelectorAll('.unhandled-check:checked')
            ).map(c => c.dataset.pageId);
            if (!picked.length) {
                notify('片付けるものを選んでください', { type: 'warn' });
                return;
            }
            markHandled(picked, bulk, bulk.dataset.value);
        });
    });
}

// wireNewRecord は「＋ 記録する」を配線します（電話・FAX・メール・メモ）。
//
// ユーザー:「いずれFAXサーバーを接続したら、自動的にチャネルがFAXになります」
// ——手で作る道と機械が作る道が**同じ形のページ**に落ちるので、ここは
// 「チャネルを選んで作る」以上のことをしません。作ったら開いて書き始められる
// ように、そのページへ移ります。
function wireNewRecord() {
    const btn = document.getElementById('w-memo-create');
    if (!btn || btn.dataset.wired) return;
    btn.dataset.wired = '1';
    const title = document.getElementById('w-memo-title');
    const dir = document.getElementById('w-memo-direction');
    const sel = document.getElementById('w-memo-channel');
    // **メモには向きがありません。** 選べてしまうと「どちらでもない」を
    // どちらかで書くことになるので、伏せます。
    const syncDirection = () => {
        if (!dir || !sel) return;
        const off = sel.value === 'メモ';
        dir.disabled = off;
        dir.style.opacity = off ? '0.4' : '';
    };
    if (sel) sel.addEventListener('change', syncDirection);
    syncDirection();
    btn.addEventListener('click', () => createRecord(btn));
    // 用件を打って Enter でも作れる（電話中に押す操作なので、手数を減らす）。
    if (title) {
        title.addEventListener('keydown', e => {
            if (e.key === 'Enter') { e.preventDefault(); createRecord(btn); }
        });
    }
}

async function createRecord(btn) {
    const sel = document.getElementById('w-memo-channel');
    const dir = document.getElementById('w-memo-direction');
    const title = document.getElementById('w-memo-title');
    if (!sel) return;
    btn.disabled = true;
    try {
        const res = await fetch('/api/intake/memo', jsonPost({
                channel: sel.value,
                direction: (dir && !dir.disabled) ? dir.value : '',
                title: title ? title.value : '',
            }));
        const data = await res.json();
        if (!data.success) {
            notify(data.message || '記録を作れませんでした', { type: 'warn' });
            return;
        }
        // **作ったら開く。** 記録は書くために作るので、一覧に戻す意味がない。
        location.href = '/' + data.page_id + '?edit=true';
    } catch (e) {
        notify('記録を作れませんでした: ' + e.message, { type: 'warn' });
    } finally {
        btn.disabled = false;
    }
}

// markHandled は `対応` の印を付け、片付いた行をその場で消します。
//
// 値は「済」（次の作業へ割り振った——**責任はそちらへ移る**）か「不要」
// （何も生まれない）。**行を消すのは見た目だけ**で、正本は本文のタグです。
// 次にページを開けばサーバーが描き直すので、ここでの見せ方がずれても壊れません。
async function markHandled(pageIds, btn, value) {
    // **空のIDは送りません。** 配列に undefined が混じると、サーバーは
    // 「権限がありません」と数えるしかなく、押した人には理由が分かりません
    // （実際にボタンのクラス取り違えでこれが出ました・2026-09-05）。
    pageIds = (pageIds || []).filter(id => id);
    if (!pageIds.length) return;
    btn.disabled = true;
    try {
        const res = await fetch('/api/intake/handled', jsonPost({ page_ids: pageIds, value: value || '済' }));
        const data = await res.json();
        if (!data.success) {
            notify(data.message || '片付けられませんでした', { type: 'warn' });
            return;
        }
        // 消すのは、印を付けられた行だけ（done_ids）。付けられなかった行まで消すと、付いたと思い込む（2026-10-22 の見直し）。
        const host = document.getElementById('w-editor-content');
        (Array.isArray(data.done_ids) ? data.done_ids : (data.failed ? [] : pageIds)).forEach(id => {
            const row = host && host.querySelector('tr[data-page-id="' + id + '"]');
            if (row) row.remove();
        });
        if (data.failed) {
            notify(data.handled + '件を「' + (value || '済') + '」にしました（' +
                data.failed + '件は付けられませんでした——権限が無いか、通信箱の記録でないか、道具が開いています）', { type: 'warn' });
        } else {
            notify(data.handled + '件を「' + (value || '済') + '」にしました',
                { type: 'success', duration: 4000 });
        }
    } catch (e) {
        notify('片付けられませんでした: ' + e.message, { type: 'warn' });
    } finally {
        btn.disabled = false;
    }
}

// ☎ 発信は、2026-10-20 から `電話番号` のタグの飾りをサーバーが描く（ext/comm/phone_decor.go）。発信そのものはブラウザ任せ
// （素の <a href="tel:">・既定の動きを止めない）。「記録を作る」に印があれば、発信の記録を作る。
w.delegateClick([['.phone-chrome .phone-call', (a) => {
    const check = a.parentElement && a.parentElement.querySelector('.phone-record input');
    if (check && check.checked) createCallRecord(a.getAttribute('data-number') || '');
}]]);
w.onChrome(wireUnhandledActions);
w.onChrome(wireNewRecord);
}

// ── 版 4 から: 上のメニューの「✉️ メール一覧」（2026-09-29 の入口）。それまでは殻（index.html）に置き、コアが通信の拡張の有無で出し分けていた。
// この JS は通信の拡張が載っているときだけ読み込まれるので、ここで足せば出し分けは要らない。⚠ 殻の id は接頭辞 w- を独占する決まり。
if (w.version >= 4) {
    const tables = document.getElementById('w-tables-link');
    const old = document.getElementById('w-mails-link'); // 入れ替えの途中で、殻にまだ古い入口が残っていれば、それを見せる
    if (old) old.classList.remove('is-hidden');
    else if (tables) {
        const link = w.makeEl('a', 'admin-link tables-link', '✉️ メール一覧');
        link.id = 'w-mails-link';
        link.href = '/assets/ext/comm/pages/mails.html'; // メールの一覧の画面も通信の持ち物（2026-10-20 から ext/comm/assets/pages/）
        tables.insertAdjacentElement('afterend', link);
    }
}

// ── 版 4 から: PDF の「⋯」の「📠 FAX で送る」（2026-10-25 夜・ext/comm/fax_send.go）。
// 利用者:「とりあえず実装してみて、間に合わなかったらまた明日の夜やりましょう」——複合機を FAX の線から外したので、w-cms から送る。
// 番号を聞き、確かめてから HylaFAX へ頼む。送れたかは、しばらく様子を見て知らせる（画面を閉じても、送ること自体は続く）。
if (w.version >= 4) {
    const { askConfirm } = w;
    w.onFileMenu((info, items, f) => {
        if (info.kind !== 'pdf' || !f.pageId || !f.file) return;
        items.push({ icon: '📠', label: 'FAX で送る', title: 'この PDF を、業務サーバーのモデムから FAX で送ります', run: () => sendFax(f.pageId, f.file, info.name || f.file) });
    });
    async function sendFax(pageId, file, name) {
        const number = (window.prompt('「' + name + '」を FAX で送ります。\n送り先の FAX の番号を入れてください（例 06-1234-5678）', '') || '').trim();
        if (!number) return;
        if (!await askConfirm('「' + name + '」を、FAX の番号 ' + number + ' へ送ります。よろしいですか？', '送る', 'やめる')) return;
        const res = await fetch('/api/fax/send', w.jsonPost({ page_id: pageId, file, number }));
        const d = await w.readResult(res);
        if (!res.ok || !d || !d.success) {
            w.notify('FAX を送れませんでした: ' + w.failMessage(res, d), { type: 'alert', duration: 0, id: 'fax-send' });
            return;
        }
        // 送った FAX は通信箱の記録になる（送れたかは、サーバーがその記録の「FAX送信の様子」に書く——画面を閉じてもよい）。
        const rec = d.record ? '記録 /' + d.record + '。' : (d.record_error ? '記録は作れませんでした（' + d.record_error + '）。' : '');
        w.notify('📠 FAX を送る仕事を受けました（番号 ' + d.number + '・仕事 ' + d.job + '）。' + rec + '送れたかを確かめています…', { type: 'info', duration: 0, id: 'fax-send' });
        watchFax(d.job, d.number);
    }
    // watchFax は仕事の様子を15秒おきに見て、送れた・送れなかったを知らせます（10分まで）。
    async function watchFax(job, number) {
        for (let i = 0; i < 40; i++) {
            await new Promise(r => setTimeout(r, 15000));
            let d;
            try { d = await (await fetch('/api/fax/status?job=' + encodeURIComponent(job))).json(); } catch (e) { continue; }
            if (!d || !d.success) continue;
            if (d.state === 'done') { w.notify('📠 FAX を送れました（' + number + '）', { type: 'success', duration: 0, id: 'fax-send' }); return; }
            if (d.state === 'failed') { w.notify('📠 FAX を送れませんでした（' + number + '）: ' + d.line, { type: 'alert', duration: 0, id: 'fax-send' }); return; }
        }
        w.notify('📠 FAX はまだ送っている途中か、待っています（' + number + '・仕事 ' + job + '）。話し中なら、少しあとで自動でかけ直します。', { type: 'warn', duration: 0, id: 'fax-send' });
    }
}
})();
