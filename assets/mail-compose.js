// ── メールの送る欄（部品）──────────────────────────────────────────────────
//
// 利用者（2026-09-30）:「メール表示、メール送信、メール編集などを部品化したら良いと思います。FAXなども
// いずれ部品化したいです」「メール編集は下書きのことです」。
//
// それまでメールを書く欄は2つあり、別々に作られていました（返信の欄＝app.js・発注書の送信の欄＝東邦の
// 拡張）。いまはこの1つで、違うのは**用件**（返信・新規・発注書）だけです。用件ごとの初期値（宛先・件名・
// 署名入りの本文・添付の候補）と、送る前後の仕事（発注書なら PDF を作って添える・発注済みにする）は
// サーバーの用件が持ちます（`ext/comm/compose.go`・`ext/comm/mail/compose.go`）。
//
//   GET  /api/mail/compose?purpose=…&page_id=… … 初期値
//   GET  /api/mail/compose?draft=…              … 保存した下書き
//   POST /api/mail/draft                         … 下書きを保存（通信箱のページ・未処理の一覧に並ぶ）
//   POST /api/mail/send                          … 送る（下書きから送ったら下書きはごみ箱へ）
//
// 置き方は2通り:
//   - `<div data-mail-compose="用件" data-mail-page="元のページ"></div>` を `<details>` の中に置くと、
//     開いたときに描く（発注書の送信欄）。
//   - `window.wcmsMailCompose.open(host, { purpose, pageId, draftId })` で直に描く（返信・下書き・新規）。
//
// ⚠ **app.js に頼りません**（メール一覧 mails.html からも読み込むため・道具は中に持つ）。
// ⚠ **`innerHTML` へ文字列を入れません**（`createElement`＋`textContent`・保存型XSSの前科）。
// ⚠ **インラインの script/style・on*= は書けません**（CSP strict）——配線は addEventListener。
(function () {
    'use strict';

    function el(tag, cls, text) {
        const e = document.createElement(tag);
        if (cls) e.className = cls;
        if (text != null) e.textContent = text;
        return e;
    }

    async function getJSON(url) {
        const res = await fetch(url, { credentials: 'same-origin' });
        const data = await res.json().catch(() => ({}));
        return { ok: res.ok && !!data.success, status: res.status, data };
    }

    async function postJSON(url, body) {
        const res = await fetch(url, {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            credentials: 'same-origin',
            body: JSON.stringify(body),
        });
        const data = await res.json().catch(() => ({}));
        return { ok: res.ok && !!data.success, status: res.status, data };
    }

    // 宛先・CC は「,」「;」・空白で区切って並べる（`名前 <アドレス>` の名前の空白で割らないよう、
    // 山括弧の外の区切りだけで割る）。
    function splitAddrs(s) {
        const out = [];
        let cur = '', depth = 0;
        for (const ch of String(s || '')) {
            if (ch === '<') depth++;
            if (ch === '>') depth = Math.max(0, depth - 1);
            if (depth === 0 && (ch === ',' || ch === ';' || ch === '、' || ch === '\n')) {
                if (cur.trim()) out.push(cur.trim());
                cur = '';
                continue;
            }
            cur += ch;
        }
        if (cur.trim()) out.push(cur.trim());
        return out;
    }

    // say は結果の欄に1文を出します（cls: 'mc-ng' で注意の色）。link を渡すとページへのリンクを添える。
    function say(box, text, cls, link) {
        box.textContent = '';
        const p = el('p', cls || '', text);
        if (link) {
            const a = el('a', '', '/' + link);
            a.href = '/' + link;
            p.appendChild(document.createTextNode(' '));
            p.appendChild(a);
        }
        box.appendChild(p);
        return p;
    }

    function add(box, text, cls) {
        box.appendChild(el('p', cls || '', text));
    }

    // open は host の中に送る欄を描きます。opts: { purpose, pageId, draftId }。
    async function open(host, opts) {
        opts = opts || {};
        host.textContent = '';
        const wait = el('p', 'mc-wait', '読み込んでいます…');
        host.appendChild(wait);
        const q = opts.draftId
            ? '?draft=' + encodeURIComponent(opts.draftId)
            : '?purpose=' + encodeURIComponent(opts.purpose || '') +
              (opts.pageId ? '&page_id=' + encodeURIComponent(opts.pageId) : '');
        let r;
        try {
            r = await getJSON('/api/mail/compose' + q);
        } catch (e) {
            wait.textContent = '⚠ 通信に失敗しました: ' + e;
            wait.className = 'mc-ng';
            return;
        }
        if (!r.ok) {
            wait.textContent = '⚠ ' + (r.data.message || '送る欄を開けません（' + r.status + '）');
            wait.className = 'mc-ng';
            return;
        }
        render(host, r.data.draft || {}, !!r.data.ready);
    }

    function render(host, d, ready) {
        host.textContent = '';
        const state = { draftId: d.draft_id || '' };
        const form = el('div', 'mail-compose');
        form.setAttribute('data-mc-purpose', d.purpose || '');

        const head = el('p', 'mc-head', state.draftId ? '📝 下書き（' + (d.purpose || '') + '）' : '✉️ ' + (d.purpose || 'メール'));
        form.appendChild(head);
        if (!ready) {
            add(form, '⚠ メールにサインインしていないので、いまは送れません（下書きには保存できます）。', 'mc-ng');
        }
        (d.notes || []).forEach((n) => add(form, n, 'mc-note'));

        const field = (label, key, value) => {
            const row = el('label', 'mc-row');
            row.appendChild(el('span', 'mc-label', label));
            const input = el('input', 'mc-input');
            input.type = 'text';
            input.value = value || '';
            input.setAttribute('data-mc', key);
            row.appendChild(input);
            form.appendChild(row);
            return input;
        };
        const to = field('宛先', 'to', (d.to || []).join(', '));
        const cc = field('CC', 'cc', (d.cc || []).join(', '));
        const subject = field('件名', 'subject', d.subject || '');
        const body = el('textarea', 'mc-body');
        body.rows = 12;
        body.value = d.body || '';
        body.setAttribute('data-mc', 'body');
        form.appendChild(body);

        // 添付の候補——印の付いたものだけ送る（回すかどうかは人が決める）。
        const picks = [];
        form.appendChild(el('p', 'mc-attach-head', '📎 添付（印を付けたものを送ります）'));
        const list = el('div', 'mc-attach');
        form.appendChild(list);
        // addPick は候補を1つ足します（同じファイルが既にあれば印を付けるだけ）。足したら true。
        const addPick = (a, checked) => {
            const key = a.page_id + '/' + a.file;
            const had = picks.find((p) => p.key === key);
            if (had) {
                had.cb.checked = true;
                return false;
            }
            const lb = el('label', 'mc-attach-item');
            const cb = el('input');
            cb.type = 'checkbox';
            cb.checked = !!checked;
            cb.setAttribute('data-mc-file', key);
            lb.appendChild(cb);
            lb.appendChild(document.createTextNode(' ' + (a.name || a.file)));
            list.appendChild(lb);
            picks.push({ key, cb, ref: { page_id: a.page_id, file: a.file, name: a.name || a.file } });
            return true;
        };
        (d.attachments || []).forEach((a) => addPick(a, !!a.checked));

        // 「🔗 ID で添付を足す」（2026-10-01 利用者:「メール送信にファイル添付が無いです。PDFや画像の表示にIDが
        // 在ると思いますが、それをクリック程度で簡単にクリップボードへコピーできると、…メールに添付できるのでは」）
        // ——ファイル表示・写真の「🔗」で写した ID（`ページ番号-添付ID`）を貼ると、サーバーが添付の組へ引いて
        // （GET /api/file-ref・読めるファイルだけ）印を付けて並べる。貼った時点で足す（Enter・ボタンでも）。
        // いくつでも（空白・「,」区切り）。添付の住所（`/000235/ab12.pdf`）も受ける。
        const addRow = el('div', 'mc-attach-add');
        const idInput = el('input', 'mc-input mc-attach-id');
        idInput.type = 'text';
        idInput.placeholder = '🔗 ファイルの ID を貼る（例 000235-ab12）';
        idInput.setAttribute('data-mc-attach-id', '1');
        const addBtn = el('button', 'mc-attach-add-btn', '添付に足す');
        addBtn.type = 'button';
        addRow.appendChild(idInput);
        addRow.appendChild(addBtn);
        form.appendChild(addRow);
        const addMsg = el('div', 'mc-attach-msg');
        addMsg.setAttribute('data-mc-attach-msg', '1');
        form.appendChild(addMsg);
        let adding = false;
        const addByIDs = async () => {
            const ids = idInput.value.split(/[\s,、;；]+/).map((x) => x.trim()).filter(Boolean);
            if (!ids.length || adding) return;
            adding = true;
            addBtn.disabled = true;
            addMsg.textContent = '';
            const left = [];
            for (const id of ids) {
                let r;
                try {
                    r = await getJSON('/api/file-ref?ref=' + encodeURIComponent(id));
                } catch (e) {
                    add(addMsg, '⚠ ' + id + ': 通信に失敗しました', 'mc-ng');
                    left.push(id);
                    continue;
                }
                if (!r.ok) {
                    add(addMsg, '⚠ ' + (r.data.message || id + ' を引けません（' + r.status + '）'), 'mc-ng');
                    left.push(id);
                    continue;
                }
                const a = { page_id: r.data.page_id, file: r.data.file, name: r.data.name };
                add(addMsg, (addPick(a, true) ? '✓ 足しました: ' : '✓ 既に並んでいます（印を付けました）: ') + a.name, 'mc-ok');
            }
            idInput.value = left.join(' '); // 引けなかったものだけ残す（直して押し直せる）
            addBtn.disabled = false;
            adding = false;
        };
        addBtn.addEventListener('click', addByIDs);
        idInput.addEventListener('keydown', (e) => {
            if (e.key === 'Enter' && !e.isComposing) {
                e.preventDefault();
                addByIDs();
            }
        });
        // 貼った時点で足す（値が入るのは paste の後なので、1拍おく）。
        idInput.addEventListener('paste', () => setTimeout(addByIDs, 0));
        if (d.send_note) add(form, d.send_note, 'mc-note');

        const bar = el('div', 'mc-bar');
        const sendBtn = el('button', 'mc-send', '送信');
        sendBtn.type = 'button';
        sendBtn.setAttribute('data-mc-send', '1');
        if (!ready) sendBtn.disabled = true;
        const saveBtn = el('button', 'mc-save', '📝 下書きに保存');
        saveBtn.type = 'button';
        saveBtn.setAttribute('data-mc-save', '1');
        bar.appendChild(sendBtn);
        bar.appendChild(saveBtn);
        form.appendChild(bar);
        const result = el('div', 'mc-result');
        result.setAttribute('data-mc-result', '1');
        form.appendChild(result);
        host.appendChild(form);

        const collect = () => ({
            purpose: d.purpose || '',
            page_id: d.page_id || '',
            draft_id: state.draftId,
            to: splitAddrs(to.value),
            cc: splitAddrs(cc.value),
            subject: subject.value,
            body: body.value,
            attachments: picks.filter((p) => p.cb.checked).map((p) => p.ref),
        });

        saveBtn.addEventListener('click', async () => {
            saveBtn.disabled = true;
            say(result, '下書きを保存しています…');
            try {
                const r = await postJSON('/api/mail/draft', collect());
                if (!r.ok) {
                    say(result, '⚠ 下書きを保存できません: ' + (r.data.message || r.status), 'mc-ng');
                    return;
                }
                state.draftId = r.data.draft_id || state.draftId;
                head.textContent = '📝 下書き（' + (d.purpose || '') + '）';
                say(result, '📝 下書きに保存しました（通信箱の未処理に並びます）:', 'mc-ok', state.draftId);
            } catch (e) {
                say(result, '⚠ 通信に失敗しました: ' + e, 'mc-ng');
            } finally {
                saveBtn.disabled = false;
            }
        });

        sendBtn.addEventListener('click', async () => {
            const req = collect();
            if (req.to.length === 0) {
                say(result, '⚠ 宛先を入れてください', 'mc-ng');
                to.focus();
                return;
            }
            if (!req.body.trim()) {
                say(result, '⚠ 本文が空です', 'mc-ng');
                body.focus();
                return;
            }
            sendBtn.disabled = true;
            saveBtn.disabled = true;
            say(result, 'メールを送っています…');
            let r;
            try {
                r = await postJSON('/api/mail/send', req);
            } catch (e) {
                say(result, '⚠ 通信に失敗しました（送れたかどうか分かりません——通信箱の送信の控えを確かめてください）: ' + e, 'mc-ng');
                sendBtn.disabled = false;
                saveBtn.disabled = false;
                return;
            }
            if (!r.ok) {
                say(result, '⚠ 送れませんでした: ' + (r.data.message || r.status), 'mc-ng');
                sendBtn.disabled = false;
                saveBtn.disabled = false;
                return;
            }
            // ⚠ **送れたことは必ず言います**——控えを作れなかった・後の仕事ができなかった・下書きを
            //    片付けられなかったときも、「送れていない」とは言わない（黙るともう一度送ってしまう）。
            const record = r.data.page_id || '';
            say(result, '✓ 送信しました。控えは通信箱にあります:', 'mc-ok', record);
            const warns = [r.data.record_error, r.data.after_error, r.data.draft_error].filter(Boolean);
            warns.forEach((w) => add(result, '⚠ ' + w, 'mc-ng'));
            form.querySelectorAll('input, textarea, button').forEach((x) => { x.disabled = true; });
            host.dispatchEvent(new CustomEvent('wcms:mail-sent', { bubbles: true, detail: { record, warns } }));
            if (warns.length) return;
            // 下書きのページから送ったら、下書きはごみ箱へ行ったので控えへ移る。
            if (state.draftId && location.pathname.replace(/^\//, '') === state.draftId && record) {
                setTimeout(() => { location.href = '/' + record; }, 1200);
            } else if (d.reload) {
                // 送った結果が本文に入る用件（発注書の発注済み）は、ページを開き直して見せる。
                setTimeout(() => location.reload(), 1200);
            }
        });
    }

    // `<details>` の中の置き場は、開いたときに描く（⚠ toggle は泡立たないので捕捉で拾う）。
    document.addEventListener('toggle', (e) => {
        const det = e.target;
        if (!det || det.tagName !== 'DETAILS' || !det.open) return;
        det.querySelectorAll('[data-mail-compose]').forEach((host) => {
            if (host.getAttribute('data-mc-loaded')) return;
            host.setAttribute('data-mc-loaded', '1');
            open(host, {
                purpose: host.getAttribute('data-mail-compose') || '',
                pageId: host.getAttribute('data-mail-page') || '',
                draftId: host.getAttribute('data-mail-draft') || '',
            });
        });
    }, true);

    window.wcmsMailCompose = { open };
})();
