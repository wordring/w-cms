// メールの一覧（2026-09-29）——GET /api/mails を読み、In-Reply-To で返信の鎖（スレッド）に束ねて並べる。
//
// 利用者:「過去のメールを一覧で見る方法を作れませんか？受信送信両方、そしてIn reply toによる返信の連鎖も
// 一覧性があるように」。
//
//   - スレッド = 返信元メッセージID が指すメールの下に、返信を字下げして並べる（日付の古い順）。スレッドどうしは
//     **いちばん新しいメールの日時**の新しい順（やりとりが動いたものが上）。親が取り込まれていない返信は、それ自身が頭。
//   - 絞り込み（件名・相手・向き・期間・未処理）は画面でする。スレッドの中で当たらないメールは薄く残す（前後が分かるように）。
//   - DOM は createElement + textContent だけ（innerHTML へ文字列を入れない）。
(() => {
    'use strict';
    const $ = (id) => document.getElementById(id);
    const PAGE = 100; // 1度に出すスレッド（日付順なら通）の数
    let mails = [];
    let threads = []; // [{ members: [{ m, depth }], latest }]
    let shown = PAGE;
    const folded = new Set(); // 返信を畳んだスレッド（頭のページID）

    function msg(text, isError) {
        const el = $('ml-msg');
        el.textContent = text || '';
        el.classList.toggle('error', !!isError);
    }

    // who はアドレス欄の値（`名前 <アドレス>`）から見せる名前を取ります（名前が無ければアドレス）。
    function who(v) {
        const s = String(v || '').trim();
        const i = s.indexOf('<');
        if (i > 0) return s.slice(0, i).trim().replace(/^"|"$/g, '');
        return s.replace(/^<|>$/g, '');
    }

    // partner は相手（受信なら差出人・送信なら宛先）です。
    function partner(m) {
        const list = m.direction === '送信' ? m.to : m.from;
        return (list || []).map(who).filter(Boolean);
    }

    function when(m) {
        const s = String(m.when || '');
        return s.length >= 16 ? s.slice(0, 10) + ' ' + s.slice(11, 16) : s;
    }

    function buildThreads() {
        const byMsg = new Map();
        mails.forEach((m) => { if (m.message_id && !byMsg.has(m.message_id)) byMsg.set(m.message_id, m); });
        const children = new Map();
        const hasParent = new Set();
        mails.forEach((m) => {
            const p = m.in_reply_to ? byMsg.get(m.in_reply_to) : null;
            if (p && p !== m) {
                if (!children.has(p)) children.set(p, []);
                children.get(p).push(m);
                hasParent.add(m);
            }
        });
        const byDate = (a, b) => String(a.when || '').localeCompare(String(b.when || ''));
        const visited = new Set();
        const walk = (m, depth, out) => {
            if (visited.has(m)) return; // 鎖が輪になっていても止まる
            visited.add(m);
            out.push({ m, depth });
            (children.get(m) || []).slice().sort(byDate).forEach((c) => walk(c, depth + 1, out));
        };
        threads = [];
        const heads = mails.filter((m) => !hasParent.has(m));
        heads.forEach((h) => { const out = []; walk(h, 0, out); threads.push(out); });
        // 輪の中にだけ居て頭が無かったもの（普通は起きない）も落とさない。
        mails.forEach((m) => { if (!visited.has(m)) { const out = []; walk(m, 0, out); threads.push(out); } });
        // 下書きは日時を持たない（まだ送っていない）ので、いちばん上に並べる（書きかけを忘れない）。
        const stamp = (m) => (m.draft ? '￿' : String(m.when || ''));
        threads = threads.map((members) => ({
            members,
            latest: members.reduce((x, e) => (stamp(e.m) > x ? stamp(e.m) : x), ''),
        }));
        threads.sort((a, b) => b.latest.localeCompare(a.latest));
    }

    function filters() {
        return {
            q: $('ml-q').value.trim().toLowerCase(),
            dir: $('ml-dir').value,
            from: $('ml-from').value,
            to: $('ml-to').value,
            unhandled: $('ml-unhandled').checked,
            flat: $('ml-flat').checked,
        };
    }

    function matches(m, f) {
        if (f.dir && m.direction !== f.dir) return false;
        const day = String(m.when || '').slice(0, 10);
        if (f.from && (!day || day < f.from)) return false;
        if (f.to && (!day || day > f.to)) return false;
        // 下書きも未処理（書きかけ・送っていない）。
        if (f.unhandled && !((m.direction === '受信' || m.draft) && !m.handled)) return false;
        if (f.q) {
            const hay = [m.title, ...(m.from || []), ...(m.to || []), ...(m.cc || [])].join(' ').toLowerCase();
            if (!hay.includes(f.q)) return false;
        }
        return true;
    }

    function cell(tr, text, cls) {
        const td = document.createElement('td');
        if (cls) td.className = cls;
        if (text !== undefined && text !== null) td.textContent = text;
        tr.appendChild(td);
        return td;
    }

    function row(m, opts) {
        const tr = document.createElement('tr');
        if (opts.start) tr.classList.add('thread-start');
        if (opts.dim) tr.classList.add('dim');
        cell(tr, m.draft ? '（まだ送っていない）' : when(m));
        const dirTd = cell(tr, '');
        const badge = document.createElement('span');
        badge.className = 'dir ' + (m.draft ? 'dir-draft' : m.direction === '送信' ? 'dir-out' : 'dir-in');
        // 下書き（2026-09-30）——送る欄で「下書きに保存」したもの。開くと直して送れる。
        badge.textContent = m.draft ? '📝 下書き' : m.direction === '送信' ? '📤 送信' : '📥 受信';
        dirTd.appendChild(badge);
        const names = partner(m);
        const whoTd = cell(tr, names.join('、'), 'who');
        whoTd.title = (m.direction === '送信' ? m.to : m.from || []).join('\n');
        const subj = cell(tr, '', 'subject');
        if (opts.depth > 0) {
            subj.style.paddingLeft = (8 + opts.depth * 18) + 'px'; // 字下げ（CSSOM）
            const mark = document.createElement('span');
            mark.className = 'reply-mark';
            mark.textContent = '↳';
            subj.appendChild(mark);
        }
        if (opts.foldable) {
            const b = document.createElement('button');
            b.type = 'button';
            b.className = 'fold';
            b.textContent = opts.folded ? '▸' : '▾';
            b.title = opts.folded ? '返信を開く' : '返信を畳む';
            b.addEventListener('click', () => {
                if (folded.has(m.page_id)) folded.delete(m.page_id); else folded.add(m.page_id);
                render();
            });
            subj.appendChild(b);
        }
        const a = document.createElement('a');
        a.href = '/' + m.page_id;
        a.textContent = m.title || '（件名なし）';
        subj.appendChild(a);
        if (opts.count > 1) {
            const c = document.createElement('span');
            c.className = 'thread-count';
            c.textContent = '💬 ' + opts.count;
            c.title = 'このスレッドのメールの数';
            subj.appendChild(c);
        }
        cell(tr, m.attachments ? m.attachments : '', 'num');
        const h = cell(tr, '');
        if (m.handled) h.textContent = m.handled;
        else if (m.direction === '受信' || m.draft) {
            const u = document.createElement('span');
            u.className = 'unhandled';
            u.textContent = '⚠ 未処理';
            h.appendChild(u);
        }
        return tr;
    }

    function render() {
        const f = filters();
        const body = $('ml-body');
        body.textContent = '';
        let total = 0;
        let groups = 0;
        const more = $('ml-more');
        if (f.flat) {
            const hit = mails.filter((m) => matches(m, f));
            total = hit.length;
            hit.slice(0, shown).forEach((m) => body.appendChild(row(m, { depth: 0 })));
            $('ml-count').textContent = total + ' 通';
            more.classList.toggle('is-hidden', hit.length <= shown);
            more.textContent = 'もっと見る（残り ' + Math.max(0, hit.length - shown) + ' 通）';
            return;
        }
        const hitThreads = threads.filter((t) => t.members.some((e) => matches(e.m, f)));
        hitThreads.forEach((t) => { total += t.members.filter((e) => matches(e.m, f)).length; });
        groups = hitThreads.length;
        hitThreads.slice(0, shown).forEach((t) => {
            const head = t.members[0].m;
            const isFolded = folded.has(head.page_id);
            t.members.forEach((e, i) => {
                if (i > 0 && isFolded) return;
                body.appendChild(row(e.m, {
                    depth: e.depth,
                    start: i === 0,
                    dim: !matches(e.m, f),
                    foldable: i === 0 && t.members.length > 1,
                    folded: isFolded,
                    count: i === 0 ? t.members.length : 0,
                }));
            });
        });
        $('ml-count').textContent = total + ' 通・' + groups + ' スレッド';
        more.classList.toggle('is-hidden', hitThreads.length <= shown);
        more.textContent = 'もっと見る（残り ' + Math.max(0, hitThreads.length - shown) + ' スレッド）';
    }

    async function load() {
        msg('読み込んでいます…');
        try {
            const res = await fetch('/api/mails');
            if (res.status === 401 || res.status === 403) { location.href = '/login'; return; }
            if (res.status === 404) { msg('この w-cms には通信の拡張が入っていません。', true); return; }
            const d = await res.json();
            if (!res.ok || !d.success) throw new Error((d && d.error) || ('HTTP ' + res.status));
            mails = d.mails || [];
            buildThreads();
            msg('');
            render();
        } catch (e) {
            msg('読めませんでした: ' + e.message, true);
        }
    }

    const again = () => { shown = PAGE; render(); };
    ['ml-q', 'ml-from', 'ml-to'].forEach((id) => $(id).addEventListener('input', again));
    ['ml-dir', 'ml-unhandled', 'ml-flat'].forEach((id) => $(id).addEventListener('change', again));
    $('ml-more').addEventListener('click', () => { shown += PAGE; render(); });

    // 新しいメール（送る欄の部品・2026-09-30・assets/mail-compose.js）。もう一度押すと畳む。
    const composeHost = $('ml-compose');
    $('ml-new').addEventListener('click', () => {
        if (composeHost.firstChild) { composeHost.textContent = ''; return; }
        if (window.wcmsMailCompose) window.wcmsMailCompose.open(composeHost, { purpose: '新規' });
    });
    // 送れたら一覧を読み直す（送信の控えが並ぶ）。
    composeHost.addEventListener('wcms:mail-sent', () => load());
    load();
})();
