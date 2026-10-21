// メールの拡張の画面——通信箱の「📥 新しいメールを読み込む」とメールのサインイン（2026-10-20 に assets/app.js から移した・docs/考察/【考察】画面の JS のコアと拡張.md）。
//
// ⚠ **コアの中身には手を出しません**——使うのは app.js が渡す window.wcms だけ。サーバーはこの拡張が載っているときだけ
// この JS を読み込ませる（置き場の決まりで拾う・internal/cms/ext_scripts.go——2026-10-20 夕までは cms.RegisterScript で名乗っていた）。
// ⚠ **`innerHTML` へ文字列を入れません**（createElement＋textContent・保存型XSSの前科）。
(function () {
'use strict';
const w = window.wcms;
// コアが古い（この部分をまだ app.js が持っている版 3 より前）なら何もしない——二重に配線しない。
if (!w || !(w.version >= 3)) return;
const { makeEl, makeButton, notify, jsonPost, postJSON, getJSON, readResult, failMessage, tagValue, hasExtension, reloadContent, loadChildNav } = w;

// ── 通信箱の「📥 新しいメールを読み込む」（2026-09-29） ─────────────────
//
// 利用者:「通信箱に最新のメールを読み込むボタンもお願いします」。「＋ 記録する」（通信箱の未処理の一覧の欄）の
// 隣に置く。押すと**受信箱と送信済みの箱**から、まだ入っていないメールだけを取り込む（重複は Message-ID で弾く）。
// 送信済みのメールは差出人が自分なので「送信」の記録になる。メールの拡張（comm/mail）が載っていなければ出さない。
//
// ⚠ **読み込みはサーバーの裏で続きます**（2026-10-09 利用者:「新しいメールの読み込みは、ページを閉じても継続するように
//    してください」——ext/comm/mail/fetch_job.go）。押すと POST /api/mail/fetch-new で始め、画面は
//    GET /api/mail/fetch-new/status で様子を見に行くだけ。新しいメールが尽きるまで読むので「もう一度押す」は要らない。
//    それまでは画面が箱ごとに /api/mail/import を呼び、ページを閉じると途中で止まっていた。開いたときに読み込みが
//    動いていれば、その続きを見る（ボタンが「読み込み中…」になり、終わったら知らせる）。
function wireMailFetch() {
    const memo = document.getElementById('w-memo-create');
    if (!memo || !hasExtension('comm/mail') || document.getElementById('w-mail-fetch')) return;
    const btn = makeButton('');
    btn.id = 'w-mail-fetch';
    btn.textContent = '📥 新しいメールを読み込む';
    btn.title = '受信箱と送信済みの箱から、まだ通信箱に入っていないメールを取り込みます';
    memo.insertAdjacentElement('afterend', btn);
    btn.addEventListener('click', () => fetchNewMail(btn));
    wireMailSignIn(btn);
    resumeMailFetch(btn);
    showMailAutoFetch(btn);
}

// showMailAutoFetch は、決まった間隔の自動の読み込みの様子を、読み込みのボタンのそばに1行で出します（2026-10-21 夜・
// ext/comm/mail/auto_fetch.go・【考察】通信箱の改善 §7.1）。動いていれば間隔と前回の時刻、休んでいればわけ。
async function showMailAutoFetch(after) {
    let a;
    try {
        a = ((await (await fetch('/api/mail/auto-fetch')).json()) || {}).auto;
    } catch (e) {
        return;
    }
    if (!a || document.getElementById('w-mail-auto')) return;
    const hm = (s) => {
        const d = new Date(s);
        return isNaN(d) ? '' : String(d.getHours()).padStart(2, '0') + ':' + String(d.getMinutes()).padStart(2, '0');
    };
    let text;
    if (a.enabled && a.user) {
        text = '🔄 自動の読み込み: ' + a.address + ' を ' + a.interval_minutes + '分ごと' + (a.last_run ? '（前回 ' + hm(a.last_run) + '）' : '');
    } else {
        text = '🔄 自動の読み込みは休んでいます: ' + (a.reason || '');
    }
    const p = makeEl('span', 'mail-auto-fetch', text);
    p.id = 'w-mail-auto';
    after.insertAdjacentElement('afterend', p);
}

// resumeMailFetch は、開いたときに裏の読み込みが動いていれば、その様子を見続けます（閉じたあいだも続いていた回）。
async function resumeMailFetch(btn) {
    let st = null;
    try {
        st = ((await (await fetch('/api/mail/fetch-new/status')).json()) || {}).status;
    } catch (e) {
        return;
    }
    if (st && st.running) watchMailFetch(btn, st.run);
}

// wireMailSignIn は「✉️ メールにサインイン」を「📥 新しいメールを読み込む」の隣に置きます（2026-10-02）。
//
// 利用者:「メールの画面からもう一度サインインを教えてください」——サインインの口（`POST /api/mail/signin`・
// デバイスコード）はあったが、**押す道が画面のどこにも無かった**（最初のサインインは口を直に叩いていた）。
// 新しい利用者で入るとサインインが要る（保管は利用者ごと）ので、画面から始められるようにする。
//
// ⚠ **サインインしていないときだけ出す**（`/api/mail/status`）。押すと Microsoft の画面へのリンクと番号を出し、
//    そこでサインインし終えるのを待って（status を数秒おきに見る）、済んだら消える。パスワードは w-cms を通らない。
async function wireMailSignIn(after) {
    if (document.getElementById('w-mail-signin')) return;
    let st;
    try {
        st = await (await fetch('/api/mail/status')).json();
    } catch (e) {
        return;
    }
    if (!st || !st.success || !st.configured || st.address) return;
    if (document.getElementById('w-mail-signin')) return;
    const btn = makeButton('');
    btn.id = 'w-mail-signin';
    btn.textContent = '✉️ メールにサインイン';
    btn.title = 'メールにサインインしていません（サインインは利用者ごと）。押すと Microsoft の画面でサインインします';
    const box = document.createElement('span');
    box.id = 'w-mail-signin-box';
    box.className = 'mail-signin-box';
    after.insertAdjacentElement('afterend', btn);
    btn.insertAdjacentElement('afterend', box);
    btn.addEventListener('click', () => startMailSignIn(btn, box));
}

async function startMailSignIn(btn, box) {
    btn.disabled = true;
    box.textContent = 'サインインの番号をもらっています…';
    let d;
    try {
        const res = await fetch('/api/mail/signin', { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: '{}' });
        d = await res.json().catch(() => ({}));
        if (!res.ok || !d.success) throw new Error(d.message || ('HTTP ' + res.status));
    } catch (e) {
        box.textContent = '⚠ サインインを始められません: ' + e.message;
        btn.disabled = false;
        return;
    }
    // 案内: リンクを開いて番号を入れる（別のタブ）。
    box.textContent = '';
    const a = document.createElement('a');
    a.href = d.verification_uri;
    a.target = '_blank';
    a.rel = 'noopener';
    a.textContent = 'Microsoft の画面を開く';
    const code = makeEl('strong', 'mail-signin-code', d.user_code);
    box.append('① ', a, '　② 番号 ', code, ' を入れて、メールのアカウントでサインイン（このページはそのまま待ちます）');
    // 済むのを待つ（番号の期限まで・3秒おき）。
    const until = Date.now() + (Number(d.expires_in) || 900) * 1000;
    while (Date.now() < until) {
        await new Promise((r) => setTimeout(r, 3000));
        let st;
        try {
            st = await (await fetch('/api/mail/status', { headers: w.bgHeaders })).json(); // 待つあいだの見張り——操作ではない
        } catch (e) {
            continue;
        }
        if (st && st.address) {
            btn.remove();
            box.textContent = '✓ ' + st.address + ' でサインインしました';
            notify('メールにサインインしました（' + st.address + '）', { type: 'success', duration: 8000 });
            return;
        }
    }
    box.textContent = '⚠ 番号の期限が切れました。もう一度押してください';
    btn.disabled = false;
}

async function fetchNewMail(btn) {
    btn.disabled = true;
    let r;
    try {
        r = await postJSON('/api/mail/fetch-new', {});
    } catch (e) {
        r = { ok: false, data: { message: '通信に失敗しました: ' + e.message } };
    }
    if (!r.ok || !r.data.status) {
        btn.disabled = false;
        notify((r.data && r.data.message) || 'メールの読み込みを始められませんでした', { type: 'warn', duration: 8000 });
        return;
    }
    watchMailFetch(btn, r.data.status.run);
}

// watchMailFetch は裏の読み込み（回 run）の様子を2秒おきに見て、ボタンに読み込んだ数を出し、終わったら知らせます。
// ⚠ 見ているのは画面だけ——ページを閉じても読み込みは続きます（ボタンの title でもそう言う）。
async function watchMailFetch(btn, run) {
    if (btn.dataset.watching === String(run)) return; // 同じ回を二重に見ない
    btn.dataset.watching = String(run);
    const label = '📥 新しいメールを読み込む';
    const title = btn.title;
    btn.disabled = true;
    btn.title = '読み込みはサーバーで続いています——ページを閉じても止まりません';
    const count = (st, folder) => ((st.folders || {})[folder] || {}).imported || 0;
    let st = null;
    for (;;) {
        try {
            // 2秒おきの見張り——操作ではない（読み込みが何時間続いてもセッションを延ばさない）。
            st = ((await (await fetch('/api/mail/fetch-new/status', { headers: w.bgHeaders })).json()) || {}).status;
        } catch (e) {
            st = null; // 一時の通信の失敗——続けて見る
        }
        if (st && st.run === run && !st.running) break;
        if (st && st.run !== run) break; // 別の回に替わった（ほかの画面から押し直した）——ここでは見るのをやめる
        btn.textContent = '📥 読み込み中…' + (st ? '（' + (count(st, '受信') + count(st, '送信')) + ' 通）' : '');
        await new Promise((res) => setTimeout(res, 2000));
    }
    delete btn.dataset.watching;
    btn.disabled = false;
    btn.textContent = label;
    btn.title = title;
    if (!st || st.run !== run) return;
    const got = { 受信: count(st, '受信'), 送信: count(st, '送信') };
    const failed = ['受信', '送信'].reduce((n, f) => n + (((st.folders || {})[f] || {}).failed || 0), 0);
    const errs = st.errors || [];
    let text = '受信 ' + got['受信'] + ' 通・送信 ' + got['送信'] + ' 通を読み込みました';
    if (failed) text += '（失敗 ' + failed + ' 通）';
    if (errs.length) text += '——' + errs.join(' / ');
    notify(text, { type: (errs.length || failed) ? 'warn' : 'success', duration: 8000 });
    if (got['受信'] + got['送信'] > 0) {
        loadChildNav();
        // 書きかけを消さない（取り込み側は reloadContent を書ける形では呼ばない——引き継ぎの罠）。
        // 2026-10-12 から書ける形の reloadContent は書きかけを先に保存して変わったブロックだけ受け取るので消えないが、
        // 呼ばないままにしてある。
        if (!document.body.hasAttribute('edit-mode')) reloadContent();
    }
}

w.onChrome(wireMailFetch);

// 記録の箱の「✉️ 返信」（2026-10-20 からサーバーが描く・ext/comm/mail/record_section.go）。押すと送る欄を開き、もう一度押すと畳む。
w.delegateClick([['.mail-chrome .mail-reply-open', (btn) => {
    const slot = btn.nextElementSibling;
    const compose = window.wcmsMailCompose;
    if (!slot || !compose) return;
    if (slot.firstChild) { slot.textContent = ''; return; }
    compose.open(slot, { purpose: '返信', pageId: btn.getAttribute('data-page') });
}]]);
// 下書きのページでは、下書きを開いた送る欄を最初から出す（開いたらすぐ直して送れる）。
w.onChrome(() => {
    const compose = window.wcmsMailCompose;
    if (!compose) return;
    document.querySelectorAll('#w-editor-content .mail-compose-host[data-compose-draft]').forEach((host) => {
        if (!host.firstChild) compose.open(host, { draftId: host.getAttribute('data-compose-draft') });
    });
});
})();
