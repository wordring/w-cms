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

// 通信箱の未処理の表の「☎ 出ていない着信」の行（2026-10-28・ext/comm/missed_call.go）——「記録にする」で電話の記録のページを作って開き、
// 「外す」で一覧から外す（履歴には残る）。行はサーバーが描く。
w.delegateClick([
    ['.unhandled-table .missed-make', async (btn) => {
        btn.disabled = true;
        const r = await w.postJSON('/api/phone/missed/make', { id: btn.dataset.missedId });
        if (!r.ok || !r.data || !r.data.page_id) {
            btn.disabled = false;
            w.notify('記録にできませんでした: ' + ((r.data && (r.data.message || r.data.error)) || ('HTTP ' + r.status)), { type: 'alert', duration: 0, id: 'missed-call' });
            return;
        }
        location.href = '/' + r.data.page_id + '?edit=true'; // 電話のメモを書き始められるように開く
    }],
    ['.unhandled-table .missed-dismiss', async (btn) => {
        btn.disabled = true;
        const r = await w.postJSON('/api/phone/missed/dismiss', { id: btn.dataset.missedId });
        if (!r.ok) {
            btn.disabled = false;
            w.notify('外せませんでした: ' + ((r.data && (r.data.message || r.data.error)) || ('HTTP ' + r.status)), { type: 'alert', duration: 0, id: 'missed-call' });
            return;
        }
        const tr = btn.closest('tr');
        if (tr) tr.remove();
    }],
]);

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
    const { askConfirm, askChoice } = w;
    w.onFileMenu((info, items, f) => {
        if (info.kind !== 'pdf' || !f.pageId || !f.file) return;
        items.push({ icon: '📠', label: 'FAX で送る', title: 'この PDF を、業務サーバーのモデムから FAX で送ります', run: () => sendFax(f.pageId, { file: f.file }, info.name || f.file) });
    });
    // 送る欄の「📠 FAX で送る」（部材発注書・部材見積依頼書・見積書——2026-10-25 夜・comm.FaxSendButtonHTML）。紙はサーバーが
    // 用件の決まりで用意し（メールで添える PDF と同じ）、送れたと分かったときだけ送付日などを書く。
    w.delegateClick([['[data-fax-purpose]', (btn) => {
        const purpose = btn.dataset.faxPurpose, pageId = btn.dataset.faxPage;
        if (purpose && pageId) sendFax(pageId, { purpose }, purpose);
    }]]);
    // askFaxNumber は送り先の番号を決めます。そのページの相手の FAX 番号（連絡帳など・/api/fax/numbers）があれば選ばせ、
    // 無ければ（または「別の番号」なら）打ってもらう。やめたら空。
    async function askFaxNumber(pageId, name) {
        let cands = [];
        try {
            const d = await (await fetch('/api/fax/numbers?page_id=' + encodeURIComponent(pageId))).json();
            if (d && d.success) cands = d.numbers || [];
        } catch (e) { /* 候補が無くても、打てば送れる */ }
        if (cands.length) {
            const labels = cands.map(c => c.number + (c.label ? '（' + c.label + '）' : '')).concat(['別の番号を入れる']);
            const i = await askChoice('「' + name + '」を FAX で送ります。送り先を選んでください。', labels);
            if (i < 0) return '';
            if (i < cands.length) return cands[i].number;
        }
        return (window.prompt('「' + name + '」を FAX で送ります。\n送り先の FAX の番号を入れてください（例 06-1234-5678）', '') || '').trim();
    }
    // sendFax は、ページ pageId から what（{file} か {purpose}）を FAX で送ります。
    async function sendFax(pageId, what, name) {
        const number = await askFaxNumber(pageId, name);
        if (!number) return;
        if (!await askConfirm('「' + name + '」を、FAX の番号 ' + number + ' へ送ります。よろしいですか？', '送る', 'やめる')) return;
        // 書きかけを保存してから頼む（用件の紙は、保存した本文から作る——postJSON が先に保存する）。
        const r = await w.postJSON('/api/fax/send', Object.assign({ page_id: pageId, number }, what));
        const d = r.data;
        if (!r.ok) {
            w.notify('FAX を送れませんでした: ' + ((d && (d.message || d.error)) || ('HTTP ' + r.status)), { type: 'alert', duration: 0, id: 'fax-send' });
            return;
        }
        // 送った FAX は通信箱の記録になる（送れたかは、サーバーがその記録の「FAX送信の様子」に書く——画面を閉じてもよい）。
        const rec = d.record ? '記録 /' + d.record + '。' : (d.record_error ? '記録は作れませんでした（' + d.record_error + '）。' : '');
        w.notify('📠 FAX を送る仕事を受けました（番号 ' + d.number + '・仕事 ' + d.job + '）。' + rec + (what.purpose ? '送れたら、送付日などをサーバーが書きます。' : '') + '送れたかを確かめています…', { type: 'info', duration: 0, id: 'fax-send' });
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

    // ── 通信箱の「☎ 電話をかける」「📠 FAX を送る」（2026-10-28・ext/comm/view_unhandled.go の boxSendChromeHTML）。
    // 利用者:「FAXの送信と電話のダイヤルを通信フォルダから出来るようにしたいです。」
    // 「そのパソコンによってかけ方が違うので、ブラウザにかけ方を記憶させておくのはどうですか？」——かけ方はこの端末に憶える
    // （link＝電話のリンク〔スマホの電話・パソコンのソフトフォン〕・show＝番号を出して会社の電話機で押す・
    // modem＝業務サーバーのモデムが回し、会社の電話機の受話器を上げて話す〔道具 cmd/w-cms-dial が動いているときだけ出す〕）。
    // ⚠ ND6 は番号を聞くだけで、電話をかける命令を持たない（製造元の機能仕様書の命令の一覧）——会社の回線へは、ND6 と分けてつないだモデムで回す。
    const DIAL_HOW_KEY = 'wcms.dialHow';
    const numberCache = {};
    const digitsOf = (s) => String(s || '').replace(/[０-９]/g, (c) => String.fromCharCode(c.charCodeAt(0) - 0xFEE0)).replace(/[^0-9]/g, '');
    // boxNumbers は相手の候補（kind: phone・fax）を読み、datalist に並べます（1回だけ）。
    async function boxNumbers(kind, listId) {
        if (numberCache[kind]) return numberCache[kind];
        let list = [];
        try {
            const d = await (await fetch('/api/comm/numbers?kind=' + kind)).json();
            if (d && d.success) list = d.numbers || [];
        } catch (e) { /* 候補が無くても、番号を打てばかけられる */ }
        numberCache[kind] = list;
        const dl = document.getElementById(listId);
        if (dl && !dl.childElementCount) list.forEach((n) => { const o = document.createElement('option'); o.value = n.label + ' ' + n.number; dl.appendChild(o); });
        return list;
    }
    // pickTarget は欄の文字から相手を決めます（{number, label, page}・決められなければ number が空）。名前が何件にも当たれば選ばせる。
    async function pickTarget(text, list, what) {
        const exact = list.find((n) => n.label + ' ' + n.number === text);
        if (exact) return exact;
        const d = digitsOf(text);
        if (d.length >= 6 && d.length >= text.replace(/[\s\-ー－()（）]/g, '').length) {
            const owner = list.find((n) => digitsOf(n.number) === d);
            return { number: text, label: owner ? owner.label : '', page: owner ? owner.page : '' };
        }
        const hits = list.filter((n) => n.label.includes(text));
        if (hits.length === 1) return hits[0];
        if (hits.length > 1) {
            const i = await askChoice('「' + text + '」の' + what + 'が ' + hits.length + ' 件あります。どれにしますか？', hits.map((n) => n.label + ' ' + n.number));
            return i >= 0 ? hits[i] : { number: '' };
        }
        return { number: '' };
    }
    function savedDialHow() { try { return localStorage.getItem(DIAL_HOW_KEY) || ''; } catch (e) { return ''; } }
    // modemAvailable は、業務サーバーのモデムでかけられるか（cmd/w-cms-dial が動いているか）を1回だけ聞きます。
    let modemAvailableP = null;
    function modemAvailable() {
        if (!modemAvailableP) modemAvailableP = fetch('/api/phone/dial/info').then((r) => r.json()).then((d) => !!(d && d.success && d.available)).catch(() => false);
        return modemAvailableP;
    }
    async function chooseDialHow() {
        const hows = ['link', 'show'];
        const labels = ['📱 この端末の電話のアプリでかける（スマホの電話・パソコンのソフトフォン）', '☎ 会社の電話機で番号を押す（番号を大きく出します）'];
        if (await modemAvailable()) { // 2026-10-28——業務サーバーのモデムが番号を回し、会社の電話機の受話器を上げて話す
            hows.unshift('modem');
            labels.unshift('☎ 業務サーバーのモデムでかける（番号は自動で回し、会社の電話機の受話器を上げて話す）');
        }
        const i = await askChoice('この端末で電話をかけるとき、どの電話を使いますか？（この端末のブラウザに憶えます。あとで「かけ方」で変えられます）', labels);
        if (i < 0) return '';
        const how = hows[i];
        try { localStorage.setItem(DIAL_HOW_KEY, how); } catch (e) { /* 憶えられなくても、今回はかけられる */ }
        return how;
    }
    // dialFromBox は「☎ かける」です。記録（発信）を作ってから、かけ方に合わせてかける。
    async function dialFromBox(btn) {
        const input = document.getElementById('w-dial-to');
        const text = input ? input.value.trim() : '';
        if (!text) { w.notify('電話をかける相手か番号を入れてください', { type: 'warn' }); return; }
        const t = await pickTarget(text, await boxNumbers('phone', 'w-dial-list'), '電話番号');
        if (!t.number || digitsOf(t.number).length < 6) { w.notify('電話番号が分かりません（連絡帳に電話番号のある相手を選ぶか、番号を打ってください）', { type: 'warn' }); return; }
        let how = savedDialHow();
        if (!how) how = await chooseDialHow();
        if (!how) return;
        btn.disabled = true;
        let rec = '';
        // モデムでかけるときは、先に回す（回せなかったら記録を作らない）。
        let held = 0;
        if (how === 'modem') {
            const r = await w.postJSON('/api/phone/dial', { number: t.number });
            if (!r.ok || !r.data || !r.data.success) {
                btn.disabled = false;
                w.notify('かけられませんでした: ' + ((r.data && (r.data.message || r.data.error)) || ('HTTP ' + r.status)), { type: 'alert', duration: 0 });
                return;
            }
            held = r.data.hold_sec || 0;
        }
        try {
            const box = document.getElementById('w-dial-record');
            if (box && box.checked) {
                const now = new Date(), p2 = (n) => String(n).padStart(2, '0');
                const stamp = now.getFullYear() + '-' + p2(now.getMonth() + 1) + '-' + p2(now.getDate()) + ' ' + p2(now.getHours()) + ':' + p2(now.getMinutes());
                const r = await w.postJSON('/api/intake/memo', { channel: '電話', direction: '送信', phone: t.number, counterpart: t.page || '', title: '電話 送信 ' + (t.label || t.number) + ' ' + stamp });
                if (r.ok && r.data && r.data.success) rec = r.data.page_id;
                else w.notify('発信の記録を作れませんでした: ' + ((r.data && (r.data.message || r.data.error)) || ('HTTP ' + r.status)), { type: 'warn' });
            }
        } finally { btn.disabled = false; }
        if (how === 'modem') {
            const i = await askChoice('業務サーバーのモデムが ☎ ' + t.number + (t.label ? '（' + t.label + '）' : '') + ' を回しました。\n\n会社の電話機の受話器を上げて、「受話器を上げた」を押してください。' +
                (held ? '\n（押さなくても ' + held + ' 秒でモデムは線から離れます）' : '') + (rec ? '\n\n発信の記録 /' + rec + ' を作りました。' : ''), ['受話器を上げた']);
            await w.postJSON('/api/phone/dial/release', {}); // やめても離す（受話器を上げていなければ、電話は切れる）
            if (rec && i === 0) location.href = '/' + rec + '?edit=true';
            return;
        }
        if (how === 'link') {
            location.href = 'tel:' + digitsOf(t.number);
            if (rec) setTimeout(() => { location.href = '/' + rec + '?edit=true'; }, 1200); // 電話のアプリが開いたあと、記録を開いて書けるように
            return;
        }
        const i = await askChoice('会社の電話機で、次の番号を押してください。\n\n☎ ' + t.number + (t.label ? '（' + t.label + '）' : '') + (rec ? '\n\n発信の記録 /' + rec + ' を作りました。' : ''), rec ? ['記録を開く'] : ['閉じる']);
        if (rec && i === 0) location.href = '/' + rec + '?edit=true';
    }
    // faxFromBox は「📠 FAX を送る」です。パソコンの PDF を、ページに置かずに送る（/api/fax/send-file・送った FAX は通信箱の記録になる）。
    async function faxFromBox(btn) {
        const fileInput = document.getElementById('w-fax-file');
        const file = fileInput && fileInput.files && fileInput.files[0];
        if (!file) { w.notify('送る PDF を選んでください', { type: 'warn' }); return; }
        if (!/\.pdf$/i.test(file.name)) { w.notify('FAX で送れるのは PDF だけです', { type: 'warn' }); return; }
        const input = document.getElementById('w-fax-to');
        const text = input ? input.value.trim() : '';
        if (!text) { w.notify('FAX の送り先（名前か番号）を入れてください', { type: 'warn' }); return; }
        const t = await pickTarget(text, await boxNumbers('fax', 'w-fax-list'), 'FAX 番号');
        if (!t.number || digitsOf(t.number).length < 6) { w.notify('FAX の番号が分かりません（連絡帳に FAX 番号のある相手を選ぶか、番号を打ってください）', { type: 'warn' }); return; }
        if (!await askConfirm('「' + file.name + '」を、FAX の番号 ' + t.number + (t.label ? '（' + t.label + '）' : '') + ' へ送ります。よろしいですか？', '送る', 'やめる')) return;
        const fd = new FormData();
        fd.append('file', file);
        fd.append('number', t.number);
        btn.disabled = true;
        let d = null, ok = false, status = 0;
        try {
            const res = await fetch('/api/fax/send-file', { method: 'POST', body: fd });
            status = res.status;
            ok = res.ok;
            try { d = await res.json(); } catch (e) { d = null; }
        } catch (e) {
            w.notify('FAX を送れませんでした: ' + e.message, { type: 'alert', duration: 0, id: 'fax-send' });
            return;
        } finally { btn.disabled = false; }
        if (!ok || !d || !d.success) {
            w.notify('FAX を送れませんでした: ' + ((d && (d.message || d.error)) || ('HTTP ' + status)), { type: 'alert', duration: 0, id: 'fax-send' });
            return;
        }
        if (fileInput) fileInput.value = '';
        const rec = d.record ? '記録 /' + d.record + '。' : (d.record_error ? '記録は作れませんでした（' + d.record_error + '）。' : '');
        w.notify('📠 FAX を送る仕事を受けました（番号 ' + d.number + '・仕事 ' + d.job + '）。' + rec + '送れたかを確かめています…', { type: 'info', duration: 0, id: 'fax-send' });
        watchFax(d.job, d.number);
    }
    // wireBoxSend は2行に配線します（描き直されたら、そのたびに・1回だけ）。
    function wireBoxSend() {
        const pairs = [['w-dial-go', dialFromBox], ['w-fax-go', faxFromBox]];
        pairs.forEach(([id, fn]) => {
            const b = document.getElementById(id);
            if (b && !b.dataset.wired) { b.dataset.wired = '1'; b.addEventListener('click', () => fn(b)); }
        });
        const how = document.getElementById('w-dial-how');
        if (how && !how.dataset.wired) {
            how.dataset.wired = '1';
            how.addEventListener('click', async () => {
                const v = await chooseDialHow();
                const said = { modem: 'この端末では、業務サーバーのモデムでかけます（会社の電話機で話す）', link: 'この端末では、電話のアプリでかけます', show: 'この端末では、番号を出して会社の電話機で押します' };
                if (v) w.notify(said[v], { type: 'success' });
            });
        }
        [['w-dial-to', 'phone', 'w-dial-list'], ['w-fax-to', 'fax', 'w-fax-list']].forEach(([id, kind, list]) => {
            const el = document.getElementById(id);
            if (!el || el.dataset.wired) return;
            el.dataset.wired = '1';
            el.addEventListener('focus', () => { boxNumbers(kind, list); }, { once: false });
            el.addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); const b = document.getElementById(kind === 'phone' ? 'w-dial-go' : 'w-fax-go'); if (b) b.click(); } });
        });
    }
    w.onChrome(wireBoxSend);
}
})();
