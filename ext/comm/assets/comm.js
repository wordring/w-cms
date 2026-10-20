// 通信の拡張の画面（2026-10-20 に assets/app.js から移した・docs/考察/【考察】画面の JS のコアと拡張.md）。
//
// 通信の記録ページ（`チャネル` のタグがあるページ）の下に出す箱です——「作るページ」（記録から作るページ・ext/comm/record_make.go）・
// 「✉️ 返信」（メールの拡張・assets/mail-compose.js の送る欄）・「🧵 やりとりの前後」（/api/thread）・
// 「📨 この記録への返信」（/api/replies）。
//
// ⚠ **コアの中身には手を出しません**——使うのは app.js が渡す window.wcms だけ。サーバーは通信の拡張が載っているときだけ
// この JS を読み込ませる（ext/comm/comm_routes.go の cms.RegisterScript）。
// ⚠ **`innerHTML` へ文字列を入れません**（createElement＋textContent・保存型XSSの前科）。
(function () {
'use strict';
const w = window.wcms;
if (!w) return; // コアが古い（読み込む順が崩れた）——何もしない

const CHANNEL_TAG = 'チャネル'; // 通信記録の目印（受信も送信も持つ）
// DRAFT_TAG は下書きの印のタグです（ext/comm/compose.go の comm.DraftTag と同じ言葉・2026-09-30）。
const DRAFT_TAG = '下書き';

// recordMakeControl はメールの記録の「作るページ [選ぶ] [作成]」です（2026-10-01・ext/comm/record_make.go）。
// 利用者:「メールから作るボタンは「受注ページ作成」「加工製品ページ作成」「返信」ページのタイプは増える可能性があるので
// 「受注ページ」「加工製品ページ」をコンボボックスで選択して「作成」ボタンを押せばいいかも」。
// 選べる種類は**拡張が登録する**（`GET /api/record-makers`——東邦の拡張なら 案件ページ・見積依頼ページ・加工製品ページ・
// 部材見積依頼の返事ページ・同じく手で書くもの〔2026-10-19〕）。この画面は種類の名前も作り方も知らない。
// もう作ったページは横に「✓ 案件」「✓ 加工製品」などの印で並べる（作り直しはできる）。
// 種類が1つも無く、印も無ければ何も出さない（書けない人には選ぶ欄を出さず、印だけ）。
function recordMakeControl() {
    const wrap = w.makeEl('p', 'record-make');
    wrap.hidden = true;
    const marks = w.makeEl('span', 'record-make-marks');
    const showMarks = (list) => {
        marks.textContent = '';
        (list || []).forEach(r => {
            marks.appendChild(document.createTextNode(' '));
            // ⚠ `attach-expand` を外す——添付の札を描き直す refreshAttachmentPreviews が本文の中の `.attach-expand` を
            // 全部消すので、問い合わせの答えが先に着くと印が消えていた（2026-10-01・E2E で間欠に落ちた）。
            const mark = w.analyzedMark(r);
            mark.classList.remove('attach-expand');
            marks.appendChild(mark);
        });
    };
    fetch('/api/record-makers?page_id=' + encodeURIComponent(w.pageId()))
        .then(r => r.json()).then(d => {
            if (!d || !d.success) return;
            const kinds = d.kinds || [];
            if (!kinds.length && !(d.made || []).length) return;
            if (kinds.length) {
                const label = w.makeEl('label', '', '作るページ ');
                const sel = w.makeEl('select', 'record-make-kind');
                kinds.forEach(k => {
                    const o = document.createElement('option');
                    o.value = k.name;
                    o.textContent = k.name;
                    o.title = k.hint || '';
                    sel.appendChild(o);
                });
                const hint = () => { sel.title = (kinds.find(k => k.name === sel.value) || {}).hint || ''; };
                sel.addEventListener('change', hint);
                hint();
                label.appendChild(sel);
                const btn = w.makeButton('record-make-go', '作成');
                btn.addEventListener('click', () => makeFromRecord(sel.value, btn));
                wrap.appendChild(label);
                wrap.appendChild(document.createTextNode(' '));
                wrap.appendChild(btn);
            }
            wrap.appendChild(marks);
            showMarks(d.made);
            wrap.hidden = false;
        }).catch(() => {});
    return wrap;
}

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


function refreshMailChrome() {
    document.querySelectorAll('#w-editor-content .mail-chrome').forEach(el => el.remove());
    // 書ける形でも出す（2026-10-12——業務のボタンは書ける形でも押せる・【考察】編集モードの再考 §6 問い5）。
    // **箱ごと通信の持ち物**です（2026-09-16）。中の「🧵 やりとりの前後」と
    // 「📨 この記録への返信」は `/api/thread`・`/api/replies` を叩くので、通信が
    // 無いと**通信記録ページを開くたびに 404 を2本**出します（どちらも失敗を
    // 黙って飲むので、画面には何も出ないまま要求だけが飛ぶ）。
    // ⚠ メール（`comm/mail`）だけを外した組では、この箱は**出ます**——読むだけの
    // 2つは通信の口で、外れるのは中の「✉️ 返信」だけ。
    if (!w.hasExtension('comm')) return;
    const host = w.editorContent();
    if (!host || !w.pageId()) return;
    // 通信記録かどうかは「チャネル」のタグで判る（受信も送信も持つ）。
    if (!w.tagValue(CHANNEL_TAG)) return;

    const box = w.makeEl('div', 'vocab-chrome mail-chrome');
    box.setAttribute('contenteditable', 'false');

    // 対応は、2026-09-30 からここに「対応: 未処理・済・不要」の札を出していました。2026-10-19 に外しました
    // （2026-10-19 利用者:「「対応」の札はなくして、タグだけにしてください。」）——対応は本文のタグ `対応`（設定の語彙の選択肢）だけで、書ける形でタグの値を選んで替える。
    // この記録から作るページ（2026-10-01・選べる種類は拡張が登録する——受信だけ・下書きには出さないもサーバーが決める）。
    if (!w.tagValue(DRAFT_TAG)) box.appendChild(recordMakeControl());

    // **「✉️ 返信」だけがメール拡張の持ち物**です（2026-09-15）。同じ箱の「🧵 やりとりの
    // 前後」と「📨 この記録への返信」は記録を読むだけなので、メールを外しても出します。
    //
    // ⚠ **書く欄は部品です**（2026-09-30・assets/mail-compose.js）——用件「返信」の初期値
    // （宛先・RE: の件名・署名・引用・添付の候補）はサーバーが組みます。
    // ⚠ **下書きのページ**（`下書き` のタグ）では、返信の代わりに**下書きを開いた送る欄**を出します
    // （開いたらすぐ直して送れる・送ったら下書きはごみ箱へ行き、控えのページへ移る）。
    const compose = window.wcmsMailCompose;
    if (w.hasExtension('comm/mail') && compose) {
        const slot = w.makeEl('div', 'mail-compose-host');
        if (w.tagValue(DRAFT_TAG)) {
            box.appendChild(slot);
            compose.open(slot, { draftId: w.pageId() });
        } else {
            const btn = w.makeButton('mail-reply-open', '✉️ 返信');
            btn.addEventListener('click', () => {
                if (slot.firstChild) { slot.textContent = ''; return; } // もう一度押すと畳む
                compose.open(slot, { purpose: '返信', pageId: w.pageId() });
            });
            box.appendChild(btn);
            box.appendChild(slot);
        }
    }
    host.appendChild(box);

    loadThread(box);
    loadReplies(box);
}

// ── スレッドの前後へ移る（2026-09-14）──────────────────────────────
//
// ユーザー:「メールですが、In-reply-to をみて前後のメールにも移動できると便利です」。
//
// 鎖は `親ページID` のタグで、辿るのはサーバー（`/api/thread`・ext/comm/handler_thread.go）。
// 2026-10-09 まではメールヘッダの `In-Reply-To`（当時のタグの名前は `返信元メッセージID`）の鎖でした。
// `📨 この記録への返信`（`/api/replies`）も、いまは同じ `親ページID` を引きます——違いは、あちらが子のうち
// こちらから送った返信（向き＝送信）だけを出すことです。こちらは前（親）と次（子）の全部なので、
// **受信どうしの返り**も繋がります。
//
// **前後が無ければ何も出しません**（1通で終わる記録のほうが多い）。
async function loadThread(box) {
    let prev = null, next = [];
    try {
        const res = await fetch('/api/thread?page_id=' + encodeURIComponent(w.pageId()));
        const d = await res.json();
        if (!d.success) return;
        prev = d.prev || null;
        next = d.next || [];
    } catch (e) { return; }
    if (!prev && !next.length) return;

    const wrap = w.makeEl('div', 'mail-thread');
    const head = w.makeEl('p', 'mail-thread-head', '🧵 やりとりの前後');
    wrap.appendChild(head);

    const ul = document.createElement('ul');
    if (prev) ul.appendChild(threadItem('↑ 返信元', prev));
    // 次は複数ありえます（同じメールに2人が別々に返信した、など）。
    next.forEach(n => ul.appendChild(threadItem('↓ この記録への返り', n)));
    wrap.appendChild(ul);
    box.appendChild(wrap);
}

// threadItem はスレッド1件ぶんの行を作ります（矢印つきの札＋題＋日時）。
function threadItem(label, r) {
    const li = document.createElement('li');
    const tag = w.makeEl('span', 'mail-thread-dir');
    // 向きの記号は通信箱の一覧と同じ流儀（←受信 / →送信）。
    tag.textContent = label + (r.direction === '送信' ? '（送信）' : r.direction === '受信' ? '（受信）' : '');
    li.appendChild(tag);
    const a = document.createElement('a');
    a.href = '/' + r.page_id;
    a.textContent = r.title || r.page_id;
    a.title = r.title || r.page_id;
    li.appendChild(a);
    if (r.when) {
        const when = w.makeEl('span', 'mail-thread-when', ' ' + String(r.when).slice(0, 16).replace('T', ' '));
        li.appendChild(when);
    }
    return li;
}

// loadReplies は「この記録への返信」を逆引きして並べます（送信箱にある本体への案内）。
async function loadReplies(box) {
    let replies = [];
    try {
        const res = await fetch('/api/replies?page_id=' + encodeURIComponent(w.pageId()));
        const d = await res.json();
        if (!d.success) return;
        replies = d.replies || [];
    } catch (e) { return; }
    if (!replies.length) return;

    const wrap = w.makeEl('div', 'mail-replies');
    const head = w.makeEl('p', 'mail-replies-head', '📨 この記録への返信（本体は送信箱にあります）');
    wrap.appendChild(head);
    const ul = document.createElement('ul');
    replies.forEach(r => {
        const li = document.createElement('li');
        const when = w.makeEl('span', 'mail-replies-when', (r.sent_at || '').slice(0, 16) + ' ');
        const a = document.createElement('a');
        a.href = '/' + r.page_id;
        a.textContent = r.title || r.page_id;
        li.appendChild(when);
        li.appendChild(a);
        if (r.to) {
            const to = w.makeEl('span', 'mail-replies-to', ' → ' + r.to);
            li.appendChild(to);
        }
        ul.appendChild(li);
    });
    wrap.appendChild(ul);
    box.appendChild(wrap);
}

w.onChrome(refreshMailChrome);

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
const PHONE_TAG = '電話番号';

function refreshPhoneChrome() {
    document.querySelectorAll('#w-editor-content .phone-chrome').forEach(el => el.remove());
    // 書ける形でも出す（2026-10-12——業務のボタンは書ける形でも押せる・【考察】編集モードの再考 §6 問い5）。
    const host = document.getElementById('w-editor-content');
    if (!host || !w.pageId()) return;
    const number = tagValue(PHONE_TAG);
    if (!number) return;

    const box = makeEl('div', 'vocab-chrome phone-chrome');
    box.setAttribute('contenteditable', 'false');

    // 発信そのものはブラウザ任せ（素の <a href="tel:">）。JSで横取りすると、
    // ソフトフォンへの受け渡しを壊しかねない。
    const call = makeEl('a', 'phone-call');
    call.href = 'tel:' + number.replace(/[^0-9+]/g, '');
    call.textContent = '☎ ' + number + ' へかける';
    box.appendChild(call);

    // **かけること自体は通信拡張の持ち物ではありません**（2026-09-16）。
    // `tel:` はただのリンクなので、通信を外した組でも掛けられます。外れるのは
    // 「記録を作る」だけ——こちらは `/api/intake/memo` を叩くので、通信が無いと
    // **押した瞬間に「発信の記録を作れませんでした」の通知**が出ます
    // （チェックは既定でオンなので、掛けるたびに必ず出る）。
    let check = null;
    if (hasExtension('comm')) {
        const label = makeEl('label', 'phone-record');
        check = document.createElement('input');
        check.type = 'checkbox';
        check.checked = true; // **既定はオン**——「かけたのに記録が無い」ほうが後で困る
        label.appendChild(check);
        label.appendChild(document.createTextNode(' 記録を作る'));
        box.appendChild(label);
    }

    call.addEventListener('click', () => {
        if (!check || !check.checked) return;
        createCallRecord(number);
    });
    host.appendChild(box);
}

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
        const host = document.getElementById('w-editor-content');
        pageIds.forEach(id => {
            const row = host && host.querySelector('tr[data-page-id="' + id + '"]');
            if (row) row.remove();
        });
        if (data.failed) {
            notify(data.handled + '件を「' + (value || '済') + '」にしました（' +
                data.failed + '件は権限がありません）', { type: 'warn' });
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

w.onChrome(refreshPhoneChrome);
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
        link.href = '/assets/mails.html';
        tables.insertAdjacentElement('afterend', link);
    }
}
})();
