// 通信箱の見え方——「作業待ち」と「スレッド」（2026-10-29・docs/考察/【考察】通信箱とメール一覧の統合.md）。
//
// 利用者:「メール一覧は通信箱に統合して、見え方を選択できるようにしたいです。メール一覧のような見え方も欲しいですが、
// 未処理は通知と同じで良いのでは？」——答え: 未処理は通信箱と同じ・スレッドに FAX・電話・メモも入れる・メール一覧のページはなくす。
//
//   - 通信箱の「未処理」のビュー（ext/comm/view_unhandled.go）の頭に、見え方の切り替え（.comm-view-switch）がある。
//     「作業待ち」はサーバーが描いた未処理の表（.comm-queue）、「スレッド」はここが /api/mails から組む（.comm-threads）。
//   - 選んだ見え方は、その端末のブラウザに憶える（wcms.commView）。住所に ?view=threads があれば、それを選ぶ
//     （上のメニューの「✉️ 通信の一覧」と、なくしたメール一覧の古い住所が送ってくる——/api/comm/threads）。
//   - スレッド = 親ページID が指す記録の下に、返信を字下げして並べる（日付の古い順）。スレッドどうしは**いちばん新しい記録の日時**の
//     新しい順。親が読めない・一覧に無い記録は、それ自身が頭。チャネル（メール・FAX・電話・メモ）を問わず同じ鎖。
//     （2026-10-28 までのメール一覧〔ext/comm/assets/pages/mails.js〕から移した。）
//   - **未処理は通信箱と同じ決まり**——人の印 `対応` が `未処理` のもの（向きは見ない・2026-09-05）。それまでのメール一覧は
//     「受信か下書き」だけを未処理にしていた。
//   - 絞り込み（件名・相手・チャネル・向き・期間・未処理）は画面でする。スレッドの中で当たらない記録は薄く残す（前後が分かるように）。
//   - DOM は createElement + textContent だけ（innerHTML へ文字列を入れない）。
(function () {
'use strict';
const w = window.wcms;
if (!w || !w.onChrome) return;

const VIEW_KEY = 'wcms.commView';
const PAGE = 100; // 1度に出すスレッド（日付順なら件）の数

function rememberedView() {
    try { return localStorage.getItem(VIEW_KEY) || ''; } catch (e) { return ''; }
}
function rememberView(v) {
    try { localStorage.setItem(VIEW_KEY, v); } catch (e) { /* 憶えられなくても見え方は切り替わる */ }
}

const el = (tag, cls, text) => {
    const e = document.createElement(tag);
    if (cls) e.className = cls;
    if (text !== undefined && text !== null) e.textContent = text;
    return e;
};

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

const ICON = { 'メール': '✉', 'FAX': '📠', '電話': '☎', 'メモ': '📝' };

// badge は向きとチャネルの札です（文字の色だけに頼らない——受信／送信の字を添える）。
function badge(m) {
    const icon = ICON[m.channel] || m.channel || '';
    if (m.draft) return { cls: 'dir dir-draft', text: '📝 下書き' };
    if (m.direction === '送信') return { cls: 'dir dir-out', text: '📤 ' + icon + ' 送信' };
    if (m.direction === '受信') return { cls: 'dir dir-in', text: '📥 ' + icon + ' 受信' };
    return { cls: 'dir', text: icon + ' ' + (m.channel || '') };
}

// buildThreads は記録を返信の鎖（親ページID）に束ねます。
function buildThreads(mails) {
    const byPage = new Map();
    mails.forEach((m) => byPage.set(m.page_id, m));
    const children = new Map();
    const hasParent = new Set();
    mails.forEach((m) => {
        const p = m.parent ? byPage.get(m.parent) : null;
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
    let threads = [];
    mails.filter((m) => !hasParent.has(m)).forEach((h) => { const out = []; walk(h, 0, out); threads.push(out); });
    // 輪の中にだけ居て頭が無かったもの（普通は起きない）も落とさない。
    mails.forEach((m) => { if (!visited.has(m)) { const out = []; walk(m, 0, out); threads.push(out); } });
    // 下書きは日時を持たない（まだ送っていない）ので、いちばん上に並べる（書きかけを忘れない）。
    const stamp = (m) => (m.draft ? '￿' : String(m.when || ''));
    threads = threads.map((members) => ({
        members,
        latest: members.reduce((x, e) => (stamp(e.m) > x ? stamp(e.m) : x), ''),
    }));
    threads.sort((a, b) => b.latest.localeCompare(a.latest));
    return threads;
}

// threadView はスレッドの見え方1つ（通信箱のビュー1つぶん）です。host は .comm-threads。
function threadView(host) {
    let mails = [];
    let threads = [];
    let shown = PAGE;
    const folded = new Set(); // 返信を畳んだスレッド（頭のページID）

    // 絞り込みの欄。
    const bar = el('div', 'comm-threads-bar');
    const q = el('input', 'comm-threads-q');
    q.type = 'search';
    q.placeholder = '件名・相手（名前・アドレス）';
    const select = (cls, opts) => {
        const s = el('select', cls);
        opts.forEach(([v, label]) => { const o = el('option', '', label); o.value = v; s.appendChild(o); });
        return s;
    };
    const ch = select('comm-threads-channel', [['', 'すべてのチャネル'], ['メール', '✉ メール'], ['FAX', '📠 FAX'], ['電話', '☎ 電話'], ['メモ', '📝 メモ']]);
    const dir = select('comm-threads-dir', [['', 'すべての向き'], ['受信', '受信'], ['送信', '送信']]);
    const from = el('input', 'comm-threads-from'); from.type = 'date'; from.title = 'この日から';
    const to = el('input', 'comm-threads-to'); to.type = 'date'; to.title = 'この日まで';
    const checkbox = (cls, label) => {
        const l = el('label', 'comm-threads-check');
        const c = el('input', cls); c.type = 'checkbox';
        l.appendChild(c);
        l.appendChild(document.createTextNode(' ' + label));
        return [l, c];
    };
    const [unhandledLabel, unhandled] = checkbox('comm-threads-unhandled', '未処理だけ');
    const [flatLabel, flat] = checkbox('comm-threads-flat', 'スレッドに束ねない（日付順）');
    const count = el('span', 'comm-threads-count');
    const say = el('span', 'comm-threads-say');
    [q, ch, dir, from, el('span', '', '〜'), to, unhandledLabel, flatLabel, count, say].forEach((x) => bar.appendChild(x));
    host.appendChild(bar);

    // 新しいメール（送る欄の部品・ext/comm/mail/assets/mail-compose.js——メールの拡張が載っているときだけ）。もう一度押すと畳む。
    const composeHost = el('div', 'comm-threads-compose');
    if (window.wcmsMailCompose) {
        const row = el('p', 'comm-threads-new');
        const b = el('button', 'chip-btn comm-threads-new-mail', '✉️ 新しいメール');
        b.type = 'button';
        b.addEventListener('click', () => {
            if (composeHost.firstChild) { composeHost.textContent = ''; return; }
            window.wcmsMailCompose.open(composeHost, { purpose: '新規' });
        });
        row.appendChild(b);
        host.appendChild(row);
        host.appendChild(composeHost);
        // 送れたら読み直す（送信の控えが並ぶ）。
        composeHost.addEventListener('wcms:mail-sent', () => load());
    }

    const scroll = el('div', 'comm-threads-scroll');
    const table = el('table', 'materials-table comm-threads-table');
    const thead = el('thead');
    const hr = el('tr');
    ['日時', '向き', '相手', '件名', '📎', '対応'].forEach((t) => hr.appendChild(el('th', '', t)));
    thead.appendChild(hr);
    const body = el('tbody');
    table.appendChild(thead);
    table.appendChild(body);
    scroll.appendChild(table);
    host.appendChild(scroll);
    const more = el('button', 'chip-btn comm-threads-more is-hidden');
    more.type = 'button';
    host.appendChild(more);

    function filters() {
        return {
            q: q.value.trim().toLowerCase(),
            channel: ch.value,
            dir: dir.value,
            from: from.value,
            to: to.value,
            unhandled: unhandled.checked,
            flat: flat.checked,
        };
    }

    function matches(m, f) {
        if (f.channel && m.channel !== f.channel) return false;
        if (f.dir && m.direction !== f.dir) return false;
        const day = String(m.when || '').slice(0, 10);
        if (f.from && (!day || day < f.from)) return false;
        if (f.to && (!day || day > f.to)) return false;
        // 未処理は通信箱の作業待ちと同じ——対応が未処理（handled が空）のもの。向きは見ない。
        if (f.unhandled && m.handled) return false;
        if (f.q) {
            const hay = [m.title, ...(m.from || []), ...(m.to || []), ...(m.cc || [])].join(' ').toLowerCase();
            if (!hay.includes(f.q)) return false;
        }
        return true;
    }

    function cell(tr, text, cls) {
        const td = el('td', cls || '', text);
        tr.appendChild(td);
        return td;
    }

    function row(m, opts) {
        const tr = el('tr');
        tr.setAttribute('data-page-id', m.page_id);
        if (opts.start) tr.classList.add('thread-start');
        if (opts.dim) tr.classList.add('dim');
        cell(tr, m.draft ? '（まだ送っていない）' : when(m), 'when');
        const b = badge(m);
        cell(tr, '', 'dir-cell').appendChild(el('span', b.cls, b.text));
        const whoTd = cell(tr, partner(m).join('、'), 'who');
        whoTd.title = (m.direction === '送信' ? m.to : m.from || []).join('\n');
        const subj = cell(tr, '', 'subject');
        if (opts.depth > 0) {
            subj.style.paddingLeft = (8 + opts.depth * 18) + 'px'; // 字下げ（CSSOM・CSP strict のため style 属性は書かない）
            subj.appendChild(el('span', 'reply-mark', '↳'));
        }
        if (opts.foldable) {
            const f = el('button', 'fold', opts.folded ? '▸' : '▾');
            f.type = 'button';
            f.title = opts.folded ? '返信を開く' : '返信を畳む';
            f.addEventListener('click', () => {
                if (folded.has(m.page_id)) folded.delete(m.page_id); else folded.add(m.page_id);
                render();
            });
            subj.appendChild(f);
        }
        const a = el('a', '', m.title || '（件名なし）');
        a.href = '/' + m.page_id;
        subj.appendChild(a);
        if (opts.count > 1) {
            const c = el('span', 'thread-count', '💬 ' + opts.count);
            c.title = 'このスレッドの記録の数';
            subj.appendChild(c);
        }
        cell(tr, m.attachments ? m.attachments : '', 'num');
        const h = cell(tr, '', 'handled');
        if (m.handled) h.textContent = m.handled;
        else h.appendChild(el('span', 'unhandled', '⚠ 未処理'));
        return tr;
    }

    function render() {
        const f = filters();
        body.textContent = '';
        if (f.flat) {
            const hit = mails.filter((m) => matches(m, f));
            hit.slice(0, shown).forEach((m) => body.appendChild(row(m, { depth: 0 })));
            count.textContent = hit.length + ' 件';
            more.classList.toggle('is-hidden', hit.length <= shown);
            more.textContent = 'もっと見る（残り ' + Math.max(0, hit.length - shown) + ' 件）';
            return;
        }
        const hitThreads = threads.filter((t) => t.members.some((e) => matches(e.m, f)));
        let total = 0;
        hitThreads.forEach((t) => { total += t.members.filter((e) => matches(e.m, f)).length; });
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
        count.textContent = total + ' 件・' + hitThreads.length + ' スレッド';
        more.classList.toggle('is-hidden', hitThreads.length <= shown);
        more.textContent = 'もっと見る（残り ' + Math.max(0, hitThreads.length - shown) + ' スレッド）';
    }

    async function load() {
        say.textContent = '読み込んでいます…';
        say.classList.remove('is-warn');
        try {
            const res = await fetch('/api/mails');
            const d = await res.json().catch(() => ({}));
            if (!res.ok || !d.success) throw new Error((d && (d.message || d.error)) || ('HTTP ' + res.status));
            mails = d.mails || [];
            threads = buildThreads(mails);
            say.textContent = '';
            render();
        } catch (e) {
            say.textContent = '⚠ 読めませんでした: ' + e.message;
            say.classList.add('is-warn');
        }
    }

    const again = () => { shown = PAGE; render(); };
    [q, from, to].forEach((x) => x.addEventListener('input', again));
    [ch, dir, unhandled, flat].forEach((x) => x.addEventListener('change', again));
    more.addEventListener('click', () => { shown += PAGE; render(); });
    load();
}

// wireViewSwitch は、通信箱の見え方の切り替えを配線します（描き直されるたびに onChrome から——新しい部品だけ）。
function wireViewSwitch() {
    const content = w.editorContent && w.editorContent();
    if (!content) return;
    content.querySelectorAll('.comm-view-switch').forEach((sw) => {
        if (sw.dataset.wired) return;
        sw.dataset.wired = '1';
        const box = sw.parentElement;
        const queue = box && box.querySelector('.comm-queue');
        const threads = box && box.querySelector('.comm-threads');
        if (!queue || !threads) return;
        const asked = new URLSearchParams(location.search).get('view');
        let view = asked === 'threads' || asked === 'queue' ? asked : rememberedView();
        if (asked === 'threads' || asked === 'queue') rememberView(asked);
        if (view !== 'threads') view = 'queue';
        const show = (v) => {
            sw.querySelectorAll('[data-comm-view]').forEach((b) => b.classList.toggle('is-on', b.getAttribute('data-comm-view') === v));
            queue.classList.toggle('is-hidden', v !== 'queue');
            threads.classList.toggle('is-hidden', v !== 'threads');
            if (v === 'threads' && !threads.dataset.built) {
                threads.dataset.built = '1';
                threadView(threads);
            }
        };
        sw.addEventListener('click', (e) => {
            const b = e.target.closest('[data-comm-view]');
            if (!b) return;
            const v = b.getAttribute('data-comm-view');
            rememberView(v);
            show(v);
        });
        show(view);
    });
}
w.onChrome(wireViewSwitch);
})();
