// セッションの失効の告知（2026-10-14・【要求】権限 §1）。
//
// 利用者:「操作が8時間無ければログアウトにしては？ログアウト数分前に告知するのも良いと思います。」
//   - セッションは「最後の操作から8時間」で切れる（サーバー——internal/auth/session.go）。
//   - 切れる5分前（サーバーが warn_before で教える）に、画面の上に帯を出す。「続ける」を押すと、それが操作になって延びる。
//   - 切れたらログインへ。ただし書きかけ（まだ保存していない変更）があれば、その場に残して帯で知らせる
//     （移るとその書きかけが消えるため——写してからログインし直せる）。
//
// 残り時間は GET /api/session で見る。この口は寿命を延ばさない（見ているだけで延び続けないように）。
// 匿名の閲覧（公開ページ）では 401 が返るので、何もしない。
//
// ⚠ CSP strict——スタイルは /assets/session.css、DOM は createElement＋textContent で作る。
(function () {
    'use strict';

    const API = '/api/session';
    let authed = false;      // 一度でもログインしていると分かったか（はじめから 401 なら匿名——何もしない）
    let timer = null;        // 次にサーバーへ残りを聞く時刻
    let tick = null;         // 帯の「あと N 分」を書き直す時計
    let expiresAt = 0;       // 失効の時刻（このタブの時計で）
    let warnMs = 5 * 60 * 1000;
    let bar = null;
    let ended = false;

    // firstAnswer は、サーバーがページに埋めた最初の答え（#w-boot-data の /api/session・2026-10-27・internal/cms/boot_data.go）を
    // 1回だけ返します（無ければ null——いつもどおり問い合わせる）。埋めたのはページを返したときの残りなので、開いた直後に使う。
    let bootUsed = false;
    function firstAnswer() {
        if (bootUsed) return null;
        bootUsed = true;
        try {
            const el = document.getElementById('w-boot-data');
            const d = el ? JSON.parse(el.textContent) : null;
            if (!d || !Object.prototype.hasOwnProperty.call(d, API)) return null;
            return new Response(JSON.stringify(d[API]), { status: 200, headers: { 'Content-Type': 'application/json' } });
        } catch (e) { return null; }
    }

    function schedule(ms) {
        clearTimeout(timer);
        timer = setTimeout(check, Math.max(1000, ms));
    }

    // check はサーバーに残りを聞き、告知の帯を出すか・次にいつ聞くかを決める。
    async function check() {
        if (ended) return;
        let res;
        try {
            res = firstAnswer() || await fetch(API, { cache: 'no-store', credentials: 'same-origin' });
        } catch (e) {
            schedule(60 * 1000); // 通信の失敗——少し待って聞き直す
            return;
        }
        if (res.status === 401) {
            if (authed) expired();
            return;
        }
        if (!res.ok) { schedule(60 * 1000); return; }
        let d;
        try { d = await res.json(); } catch (e) { schedule(60 * 1000); return; }
        authed = true;
        warnMs = (Number(d.warn_before) || 300) * 1000;
        const left = (Number(d.expires_in) || 0) * 1000;
        expiresAt = Date.now() + left;
        if (left <= warnMs) {
            showBar();
            // 帯を出しているあいだも聞き直す（ほかのタブで操作すれば延びる——そのときは帯を消す）。
            schedule(Math.min(30 * 1000, left + 1000));
        } else {
            hideBar();
            schedule(left - warnMs);
        }
    }

    function minutesLeft() {
        return Math.max(1, Math.ceil((expiresAt - Date.now()) / 60000));
    }

    function makeBar() {
        if (bar) return bar;
        bar = document.createElement('div');
        bar.id = 'w-session-bar';
        bar.className = 'w-session-bar';
        bar.setAttribute('role', 'alert');
        document.body.appendChild(bar);
        return bar;
    }

    function showBar() {
        const el = makeBar();
        el.textContent = '';
        el.classList.remove('is-ended');
        const msg = document.createElement('span');
        msg.className = 'w-session-msg';
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.textContent = '続ける';
        btn.addEventListener('click', extend);
        el.append(msg, btn);
        const write = () => {
            msg.textContent = '操作が無いので、あと ' + minutesLeft() + ' 分でログアウトします。';
        };
        write();
        clearInterval(tick);
        tick = setInterval(write, 15 * 1000);
    }

    function hideBar() {
        clearInterval(tick);
        tick = null;
        if (bar) { bar.remove(); bar = null; }
    }

    // extend は「続ける」——POST は操作なので、サーバーが寿命を延ばす。
    async function extend() {
        let res;
        try {
            res = await fetch(API, { method: 'POST', cache: 'no-store', credentials: 'same-origin' });
        } catch (e) {
            return; // 通信の失敗——帯は残る（もう一度押せる）
        }
        if (res.status === 401) { expired(); return; }
        check();
    }

    function hasUnsaved() {
        try { return typeof window.wcmsHasUnsaved === 'function' && !!window.wcmsHasUnsaved(); } catch (e) { return false; }
    }

    function loginURL() {
        return '/login?next=' + encodeURIComponent(location.pathname + location.search + location.hash);
    }

    // clearPerUserState は、この端末に残した、その人の操作の写しを捨てます（2026-10-27 の見直し）。共用の PC で、次にこの
    // タブ・この端末で入った人に、前の人のものが見えないように——
    //   - このタブのアンドゥの記録（sessionStorage の wcms.undo.<ページ>・ブロックの変更前と変更後の HTML）
    //   - 物ごとに憶えた開閉と絞り込みの欄（localStorage の wcms.view——社名などの値が入る）・表を探す画面の最後の表
    // ログアウトの押しボタン（app.js）・切れてログインへ移るとき・別の人が入ったと分かったとき（app.js の loadMe）に呼ぶ。
    function clearPerUserState() {
        try {
            for (let i = sessionStorage.length - 1; i >= 0; i--) {
                const k = sessionStorage.key(i);
                if (k && k.indexOf('wcms.undo.') === 0) sessionStorage.removeItem(k);
            }
        } catch (e) {}
        try {
            localStorage.removeItem('wcms.view');
            localStorage.removeItem('w-tables-last');
        } catch (e) {}
    }
    window.wcmsClearPerUserState = clearPerUserState;

    // expired は切れたとき——ログインへ。書きかけがあればその場に残して知らせる。
    function expired() {
        if (ended) return;
        ended = true;
        clearTimeout(timer);
        hideBar();
        if (!hasUnsaved()) {
            clearPerUserState(); // 切れてログインへ移るときも、前の人の写しを残さない（2026-10-27 の見直し）
            location.href = loginURL();
            return;
        }
        const el = makeBar();
        el.classList.add('is-ended');
        const msg = document.createElement('span');
        msg.className = 'w-session-msg';
        msg.textContent = 'ログアウトしました（最後の操作から8時間）。書きかけはまだ保存されていません——写してからログインし直してください。';
        const a = document.createElement('a');
        a.href = loginURL();
        a.target = '_blank';
        a.rel = 'noopener';
        a.textContent = 'ログインし直す（別のタブ）';
        el.append(msg, a);
    }

    // タブに戻ったとき・機械が眠りから覚めたとき（時計が止まっていた）に聞き直す。
    // 切れたあとに別のタブでログインし直していたら、帯を消して見張りに戻る（書きかけを保存できるようになる）。
    document.addEventListener('visibilitychange', async () => {
        if (document.visibilityState !== 'visible' || !authed) return;
        if (!ended) { check(); return; }
        try {
            const res = await fetch(API, { cache: 'no-store', credentials: 'same-origin' });
            if (!res.ok) return;
        } catch (e) { return; }
        ended = false;
        hideBar();
        check();
    });

    check();
})();
