// 端末の登録の画面（2026-10-31・docs/考察/【考察】端末の登録.md・API は internal/auth/device_handlers.go）。
//
// ⚠ CSP strict——DOM は createElement＋textContent で作る。
(function () {
    'use strict';

    const $ = (id) => document.getElementById(id);
    const setHidden = (el, hidden) => el.classList.toggle('is-hidden', hidden);

    function say(el, text, isError) {
        el.textContent = text;
        el.classList.toggle('error', !!isError);
    }

    async function post(url, body) {
        const res = await fetch(url, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify(body || {}),
        });
        if (res.status === 401) {
            location.href = '/login?next=' + encodeURIComponent(location.pathname);
            throw new Error('ログインが切れました');
        }
        if (!res.ok) throw new Error((await res.text()).trim() || '失敗しました');
        return res.json();
    }

    const day = (s) => {
        const d = new Date(s);
        return isNaN(d) ? '' : d.toLocaleDateString('ja-JP');
    };

    function stateText(st) {
        if (st.this_device) {
            const me = st.devices.find((d) => d.id === st.this_device);
            return 'この端末は「' + (me ? me.name : '') + '」として登録されています。';
        }
        if (st.host_is_address) {
            return 'この端末は登録されていません。いまは数字の住所で開いているので、登録できません。社外から使う名前で開き直してください（登録は開いた名前ごとに別です）。';
        }
        if (st.outside) {
            return 'この端末は登録されていません。登録済みの端末か社内で番号を出して、下に入れてください。';
        }
        return 'この端末は登録されていません。社内から開いているので、番号なしで登録できます。';
    }

    function row(d, st, showUser) {
        const tr = document.createElement('tr');
        const cell = (text) => {
            const td = document.createElement('td');
            td.textContent = text;
            tr.appendChild(td);
        };
        cell(d.name + (d.id === st.this_device ? '（この端末）' : ''));
        if (showUser) cell(d.username);
        cell(day(d.created_at));
        cell(day(d.last_seen));
        cell(d.last_ip);
        const td = document.createElement('td');
        if (st.can_issue) {
            const btn = document.createElement('button');
            btn.type = 'button';
            btn.className = 'sub';
            btn.textContent = '取り消す';
            btn.addEventListener('click', () => revoke(d, st));
            td.appendChild(btn);
        }
        tr.appendChild(td);
        return tr;
    }

    async function revoke(d, st) {
        const warn = d.id === st.this_device ? '\nこの端末なので、取り消すとログアウトします。' : '';
        if (!window.confirm('「' + d.name + '」を取り消しますか？その端末のログインもすぐに切れます。' + warn)) return;
        try {
            await post('/api/devices/revoke', { id: d.id });
            await load();
        } catch (e) {
            say($('dev-list-msg'), e.message, true);
        }
    }

    async function load() {
        const all = $('dev-all').checked;
        const res = await fetch('/api/devices' + (all ? '?all=1' : ''));
        if (res.status === 401) {
            location.href = '/login?next=' + encodeURIComponent(location.pathname);
            return;
        }
        if (!res.ok) {
            say($('dev-list-msg'), '端末の一覧を読めませんでした。', true);
            return;
        }
        const st = await res.json();
        $('dev-state').textContent = stateText(st);
        setHidden($('dev-register'), !!st.this_device || st.host_is_address);
        setHidden($('dev-code-label'), !st.outside);
        setHidden($('dev-issue'), !st.can_issue);
        setHidden($('dev-all-label'), !st.is_admin);
        setHidden($('dev-user-head'), !all);
        const body = $('dev-table').querySelector('tbody');
        body.replaceChildren(...st.devices.map((d) => row(d, st, all)));
        setHidden($('dev-table'), st.devices.length === 0);
        setHidden($('dev-empty'), st.devices.length !== 0);
    }

    $('dev-register-btn').addEventListener('click', async () => {
        const msg = $('dev-register-msg');
        say(msg, '');
        try {
            await post('/api/devices/register', { name: $('dev-name').value, code: $('dev-code').value });
            $('dev-name').value = '';
            $('dev-code').value = '';
            say(msg, '登録しました。');
            await load();
        } catch (e) {
            say(msg, e.message, true);
        }
    });

    $('dev-issue-btn').addEventListener('click', async () => {
        const msg = $('dev-issue-msg');
        say(msg, '');
        try {
            const r = await post('/api/devices/code');
            $('dev-issued').textContent = r.code;
            say(msg, Math.round(r.expires_in / 60) + ' 分で切れます。出し直すと、前の番号は使えなくなります。');
        } catch (e) {
            say(msg, e.message, true);
        }
    });

    $('dev-all').addEventListener('change', load);
    load();
})();
