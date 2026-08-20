// エディタの殻（assets/index.html）の本体スクリプト。
//
// 2026-08-06 に index.html のインライン <script> から切り出した。外部ファイルにすることで
// Content-Security-Policy から script-src 'unsafe-inline' を外せる（2026-08-19 の
// 移行第4段で strict 化を完了）。<head> の FOUC 防止スクリプトは /assets/boot.js。
//
// 読み込み位置は </body> の直前で defer なし（本文DOMが揃ってから走る従来の順序）。

    // ── 表示/非表示の切り替え ────────────────────────────────────────────────
    // 初期状態を markup の class（.is-hidden）で持ち、切り替えも同じ class で行う。
    // JS から style.display を書くと、CSS 側の規則と綱引きになる——markup の
    // style="display:none" を app.css へ移した時点で、style.display='' では
    // 二度と表示できなくなる（規則が残るため）。入口を1つにして食い違いを無くす。
    function setHidden(el, hidden) {
        if (el) el.classList.toggle('is-hidden', !!hidden);
    }

    // ── UI 設定ストア（クライアント側の表示状態・設定を一元管理） ─────────────────
    // サーバー非依存のUI設定（レール開閉・将来の配色やカード開閉など）を、localStorage の
    // 単一ルートキー `wcms.ui` に階層化した JSON で保存する。キーを散らかさず、設定が増えても
    // セクションを足すだけで済む。`_v` でバージョン管理し、スキーマ変更時は migrate を1本足す。
    //   UI.get('rails.left', true)   // ドットパスで取得（無ければ既定値）
    //   UI.set('rails.left', false)  // 階層を掘って保存＋永続化
    // FOUC 防止のため、<head> のインラインscriptも同じ `wcms.ui` を読む（旧キーfallback付き）。
    const UI = (() => {
        const KEY = 'wcms.ui';
        const VERSION = 1;
        function migrate(o) {
            // 初版: 旧 `wcms.rails`（単発キー）を `wcms.ui.rails` へ取り込み、旧キーは掃除する。
            if (o._v === undefined) {
                try {
                    const old = JSON.parse(localStorage.getItem('wcms.rails') || 'null');
                    if (old && typeof old === 'object') o.rails = Object.assign({}, old, o.rails);
                } catch (e) { /* ignore */ }
                try { localStorage.removeItem('wcms.rails'); } catch (e) { /* ignore */ }
                o._v = VERSION;
            }
            return o;
        }
        let cache;
        try { cache = JSON.parse(localStorage.getItem(KEY) || 'null'); } catch (e) { cache = null; }
        if (!cache || typeof cache !== 'object') cache = {};
        cache = migrate(cache);
        function persist() { try { localStorage.setItem(KEY, JSON.stringify(cache)); } catch (e) { /* 無効環境は既定動作 */ } }
        persist(); // 移行結果を確定
        function get(path, dflt) {
            let cur = cache;
            for (const p of path.split('.')) {
                if (cur == null || typeof cur !== 'object') return dflt;
                cur = cur[p];
            }
            return cur === undefined ? dflt : cur;
        }
        function set(path, value) {
            const parts = path.split('.');
            let cur = cache;
            for (let i = 0; i < parts.length - 1; i++) {
                const p = parts[i];
                if (cur[p] == null || typeof cur[p] !== 'object') cur[p] = {};
                cur = cur[p];
            }
            cur[parts[parts.length - 1]] = value;
            persist();
        }
        return { get, set };
    })();

    let slashMenuVisible = false;
    let currentSlashBlock = null;
    let slashSelectedIndex = 0;
    let draggedBlock = null;
    let activeBlock = null;
    
    // URLのパスからページIDを取得（/102300 のような形式）
    let pathId = window.location.pathname.replace(/^\/+/, '');
    if (pathId === '' || pathId === 'index.html') {
        pathId = '000000';
    }
    let currentPageId = pathId;
    let saveTimeout = null;

    function createSubpage() {
        // 表示中のページの下に子ページを作成する
        window.open(`/api/new-page?parent=${currentPageId}`, '_blank');
    }

    // --- サイドパネルの開閉（UI ストア `wcms.ui.rails` に永続化。FOUC防止は <head> のインラインscript） ---
    // レールは3枚（left=ページ階層／toc=目次／right=ページ情報）。
    // 左側の2枚（left・toc）は同じグループとして扱い、同時には開かない
    // （片方を開くともう片方は自動で畳む）。right は独立。
    const RAIL_IDS = ['left', 'toc', 'right'];
    const RAIL_GROUPS = [['left', 'toc'], ['right']];
    function railGroupOf(side) {
        return RAIL_GROUPS.find(g => g.includes(side)) || [side];
    }

    function railState() {
        return UI.get('rails', {});
    }

    // 狭幅（ドロワーモード）かどうか。レールごとに異なる閾値を持つ（CSS の @media と
    // 一致させること）。右レールを先に、次に左側2レール（left・toc）の順にドロワー化する
    // 段階式で、本文（doc-column）の幅が狭くなりすぎないようにする。
    const RAIL_BREAKPOINTS = { left: 760, toc: 760, right: 1100 };
    const railNarrow = (side) => window.matchMedia(`(max-width: ${RAIL_BREAKPOINTS[side]}px)`).matches;

    function toggleRail(side) {
        // 狭幅ではオフキャンバスのドロワーを開閉する（永続化しない・既定は閉）。
        if (railNarrow(side)) { toggleDrawer(side); return; }
        // 広い画面では従来どおりインライン折り畳み（UI ストアに永続化）。
        const cls = side + '-collapsed';
        const collapsed = document.documentElement.classList.toggle(cls);
        UI.set('rails.' + side, !collapsed); // true = 開
        if (!collapsed) {
            // 開いた側なので、同じグループの他のレールは畳んで同時に開かないようにする
            railGroupOf(side).forEach(id => {
                if (id === side) return;
                document.documentElement.classList.add(id + '-collapsed');
                UI.set('rails.' + id, false);
            });
        }
        syncRailToggleButtons();
    }

    // toggleDrawer は狭幅時のドロワー開閉。同時に1つだけ開く（開くと他は自動で閉じる）。
    function toggleDrawer(side) {
        const html = document.documentElement;
        const cls = side + '-drawer-open';
        const willOpen = !html.classList.contains(cls);
        RAIL_IDS.forEach(id => { if (id !== side) html.classList.remove(id + '-drawer-open'); });
        html.classList.toggle(cls, willOpen);
        syncRailToggleButtons();
    }

    // closeDrawers は開いているドロワーを全て閉じる（バックドロップclick・Escape・デスクトップ復帰で呼ぶ）。
    function closeDrawers() {
        RAIL_IDS.forEach(id => document.documentElement.classList.remove(id + '-drawer-open'));
        syncRailToggleButtons();
    }

    // トグルボタンの見た目（押下状態）を現在の開閉状態に合わせる。
    // レールごとに、狭幅ならドロワーの開閉、広い画面では折り畳み状態を反映する。
    function syncRailToggleButtons() {
        RAIL_IDS.forEach(side => {
            const btn = document.getElementById('toggle-' + side);
            if (!btn) return;
            const open = railNarrow(side)
                ? document.documentElement.classList.contains(side + '-drawer-open')
                : !document.documentElement.classList.contains(side + '-collapsed');
            btn.classList.toggle('is-on', open);
            btn.setAttribute('aria-pressed', open ? 'true' : 'false');
        });
    }

    // Escape でドロワーを閉じる。
    document.addEventListener('keydown', (e) => {
        if (e.key === 'Escape') closeDrawers();
    });
    // 広い画面へ戻ったらドロワー状態を解除し、ボタン表示を折り畳み基準に戻す
    // （レールごとに閾値が異なるため、使われている閾値ごとにリスナーを張る）。
    new Set(Object.values(RAIL_BREAKPOINTS)).forEach(bp => {
        window.matchMedia(`(max-width: ${bp}px)`).addEventListener('change', (e) => {
            if (!e.matches) {
                RAIL_IDS.forEach(id => {
                    if (RAIL_BREAKPOINTS[id] === bp) document.documentElement.classList.remove(id + '-drawer-open');
                });
            }
            syncRailToggleButtons();
        });
    });

    // 左レール：子ページナビを /api/children から描画する。
    async function loadChildNav() {
        const list = document.getElementById('w-child-nav-list');
        if (!list) return;
        list.innerHTML = '';
        try {
            const res = await fetch('/api/children?parent_id=' + currentPageId);
            if (!res.ok) { list.innerHTML = '<li class="child-nav-empty">読み込めません</li>'; return; }
            const items = await res.json() || [];
            if (items.length === 0) { list.innerHTML = '<li class="child-nav-empty">子ページはありません</li>'; return; }
            items.forEach(p => {
                const li = document.createElement('li');
                const a = document.createElement('a');
                a.className = 'child-nav-link';
                a.href = '/' + p.ID;
                a.textContent = '📄 ' + (p.Title || p.ID);
                a.title = p.Title || p.ID;
                li.appendChild(a);
                list.appendChild(li);
            });
        } catch (e) { list.innerHTML = '<li class="child-nav-empty">通信エラー</li>'; }
    }

    // 左レール：目次を本文中の見出し(h1〜h6)から構築する。サーバーには一切依存せず、
    // 表示中の #w-editor-content を走査するだけ（クリックで scrollIntoView）。
    // 本文が変わるたび（初回ロード・編集中）に呼び直して最新化する。
    function buildToc() {
        const list = document.getElementById('w-toc-list');
        if (!list) return;
        const container = document.getElementById('w-editor-content');
        const headings = Array.from(container.querySelectorAll('h1, h2, h3, h4, h5, h6'))
            .map(el => ({ el, text: el.textContent.trim() }))
            .filter(h => h.text);
        list.innerHTML = '';
        if (headings.length === 0) {
            list.innerHTML = '<li class="toc-nav-empty">見出しがありません</li>';
            return;
        }
        headings.forEach(({ el, text }) => {
            const li = document.createElement('li');
            const btn = document.createElement('button');
            btn.type = 'button';
            btn.className = 'toc-nav-link';
            btn.dataset.level = el.tagName.slice(1); // "H2" -> "2"
            btn.textContent = text;
            btn.title = text;
            btn.addEventListener('click', () => el.scrollIntoView({ behavior: 'smooth', block: 'start' }));
            li.appendChild(btn);
            list.appendChild(li);
        });
    }

    // --- 認証・権限まわり ---
    let currentUserIsAdmin = false;

    // loadMe は認証状態を取得し、未認証（匿名）なら読み取り専用モードへ切り替える。
    // /api/me は OptionalAuth 配下で、未認証時も 200 で {authenticated:false} を返す。
    async function loadMe() {
        try {
            const res = await fetch('/api/me');
            const me = res.ok ? await res.json() : { authenticated: false };
            if (!me.authenticated) {
                // 匿名: 編集UI・権限カード・レール操作を隠し（CSS body.anonymous）、ログイン導線を出す。
                document.body.classList.add('anonymous');
                const ub = document.getElementById('w-user-bar');
                if (ub) ub.innerHTML = '<a href="/login" class="login-link">ログイン</a>';
                return;
            }
            currentUserIsAdmin = !!me.is_admin;
            document.getElementById('w-user-bar').textContent =
                '👤 ' + me.username + (me.is_admin ? '（管理者）' : '');
            if (me.is_admin) setHidden(document.getElementById('w-admin-link'), false);
        } catch (e) { /* 通信エラー等は無視（編集UIは権限取得時に再判定） */ }
    }

    async function logout() {
        try { await fetch('/api/logout', { method: 'POST' }); } catch (e) {}
        window.location.href = '/login';
    }

    // 権限カードの編集可否は「編集モード（＝ロック保持）」かつサーバーが返す権限フラグ（can_chmod/
    // can_chown）の両方で決まる。編集モードでないあいだは読み取り専用にして、権限変更もロックで
    // 直列化し、他者との競合を避ける（親ページIDの編集と同じ方針）。
    let permCanChmod = false, permCanChown = false, permCanPublish = false;
    function refreshPermsEditable() {
        const editMode = document.body.hasAttribute('edit-mode');
        const canEditMeta = editMode && permCanChmod;
        document.getElementById('w-pp-group').disabled = !canEditMeta;
        document.getElementById('w-pp-mode').disabled = !canEditMeta;
        setHidden(document.getElementById('w-pp-chown'), !(editMode && permCanChown));
        setHidden(document.getElementById('w-pp-save'), !canEditMeta);
        setHidden(document.getElementById('w-pp-mode-hint'), !canEditMeta);
        // 権限はあるが閲覧モードのとき、「編集モードへ」の誘導を出す。
        setHidden(document.getElementById('w-pp-view-hint'),
            !(!editMode && (permCanChmod || permCanChown || permCanPublish)));
        // 匿名公開トグル：owner/admin にのみ行を見せ、編集モードでのみ操作可（chmod と同じ規律）。
        setHidden(document.getElementById('w-pp-public-row'), !permCanPublish);
        const pub = document.getElementById('w-pp-public');
        if (pub) pub.disabled = !(editMode && permCanPublish);
    }

    // applyEditToggleVisibility は、編集権が無いユーザー（および匿名）から右上の編集スイッチを隠す。
    // 匿名は body.anonymous の CSS で隠れるため、ここでは認証済みユーザーの can_write を反映する。
    function applyEditToggleVisibility(canWrite) {
        setHidden(document.querySelector('.switch-container .switch'), !canWrite);
        setHidden(document.getElementById('w-mode-text'), !canWrite);
    }

    // 権限はサイドパネルの「🔒 権限」カードに常設。読み込み時に取得し、カードを開けば見える。
    async function loadPerms() {
        const msg = document.getElementById('w-pp-msg');
        msg.textContent = '';
        try {
            const res = await fetch('/api/page-perms?id=' + currentPageId);
            if (!res.ok) { msg.style.color = '#dc2626'; msg.textContent = '権限を取得できません'; return; }
            const p = await res.json();
            document.getElementById('w-pp-owner').textContent = p.owner || '(なし)';
            document.getElementById('w-pp-group').value = p.group || '';
            document.getElementById('w-pp-mode').value = p.mode || '';
            // chown は admin のみ、chmod/chgrp は owner か admin。さらに編集モード（＝ロック保持）の
            // ときだけ編集可能にする（refreshPermsEditable）。権限フラグは保持して再評価に使う。
            permCanChmod = !!p.can_chmod;
            permCanChown = !!p.can_chown;
            permCanPublish = !!p.can_publish;
            if (permCanChown) document.getElementById('w-pp-owner-input').value = p.owner || '';
            // 匿名公開の現在状態をトグルとヒントに反映する（認証認可設計.md 10章）。
            applyPublicState(!!p.public, !!p.effective_public);
            // 編集権の無いユーザーからは右上の編集スイッチを隠す（サーバーも RequireEditLock で防御）。
            applyEditToggleVisibility(!!p.can_write);
            refreshPermsEditable();
        } catch (e) { msg.style.color = '#dc2626'; msg.textContent = '権限の取得に失敗しました'; }
    }

    // applyPublicState は匿名公開トグルの状態と説明文を更新する。
    // pub=このページ自身の公開フラグ、eff=親チェーンとのANDによる実際の公開状態。
    function applyPublicState(pub, eff) {
        const cb = document.getElementById('w-pp-public');
        if (cb) cb.checked = pub;
        const hint = document.getElementById('w-pp-public-hint');
        if (!hint) return;
        if (pub && eff) {
            hint.textContent = '現在インターネットに公開されています（ログイン不要で閲覧可）。';
        } else if (pub && !eff) {
            hint.textContent = '公開に設定されていますが、親ページが非公開のため実際には公開されていません。';
        } else {
            hint.textContent = '非公開（閲覧にはログインと閲覧権限が必要）。';
        }
    }

    // setPublic は匿名公開フラグを切り替える（owner/admin、編集モード中のみ）。
    // 親が非公開のまま公開しようとするとサーバーが 403 を返すので、その旨を表示して元へ戻す。
    async function setPublic() {
        const cb = document.getElementById('w-pp-public');
        const msg = document.getElementById('w-pp-msg');
        const want = cb.checked;
        try {
            const res = await lockedFetch('/api/page-perms?id=' + currentPageId, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ public: want }),
            });
            if (!res.ok) {
                cb.checked = !want; // 失敗したのでUIを元に戻す
                const t = await res.text();
                msg.style.color = '#dc2626';
                msg.textContent = t || '公開設定の変更に失敗しました';
                return;
            }
            const d = await res.json();
            msg.style.color = '#16a34a';
            msg.textContent = want ? '公開しました' : '非公開にしました';
            applyPublicState(!!d.public, !!d.effective_public);
        } catch (e) {
            cb.checked = !want;
            msg.style.color = '#dc2626';
            msg.textContent = '公開設定の変更に失敗しました（通信エラー）';
        }
    }

    // --- ページ情報パネル（属性はサイドカーが正本。/api/page-meta で取得） ---

    // ISO8601（RFC3339）の日時を日本語ロケールで読みやすく整形する。空・不正値は「—」。
    function formatDateTime(value) {
        if (!value) return '—';
        const d = new Date(value);
        if (isNaN(d.getTime())) return value;
        return d.toLocaleString('ja-JP', {
            year: 'numeric', month: '2-digit', day: '2-digit',
            hour: '2-digit', minute: '2-digit'
        });
    }

    // ページ属性をサーバーから取得してサイドパネルに反映する。
    async function loadPageMeta() {
        document.getElementById('w-pi-id').textContent = currentPageId;
        try {
            const res = await fetch('/api/page-meta?id=' + currentPageId);
            if (!res.ok) return;
            const m = await res.json();
            const parent = m.parent_id || '';
            document.getElementById('w-pi-parent').textContent = parent || '（トップレベル）';
            document.getElementById('w-pi-parent-input').value = parent;
            // 左ナビの「親ページへ」リンク
            // 左ナビの「↑ 親ページへ」リンク：矢印＋親ページの見出し（h1）を表示する。
            // 見出しはユーザー入力なので、テンプレート文字列＋innerHTML ではなく
            // textContent で組み立てる（見出しに含まれる記号でHTMLが壊れないように）。
            const parentBox = document.getElementById('w-child-nav-parent');
            if (parentBox) {
                parentBox.innerHTML = '';
                if (parent) {
                    const a = document.createElement('a');
                    a.href = '/' + parent;
                    a.textContent = '↑ ' + (m.parent_title || parent);
                    parentBox.appendChild(a);
                }
            }
            document.getElementById('w-pi-created-at').textContent = formatDateTime(m.created_at);
            document.getElementById('w-pi-created-by').textContent = m.created_by || '—';
            document.getElementById('w-pi-updated-at').textContent = formatDateTime(m.updated_at);
        } catch (e) { /* 取得失敗時は既定表示のまま */ }
    }

    // 親ページIDの付け替え。検証はサーバー（/api/set-parent）が権威。
    async function applyParent() {
        const input = document.getElementById('w-pi-parent-input');
        const errEl = document.getElementById('w-pi-parent-err');
        const parent = input.value.trim();
        errEl.textContent = '';
        input.classList.remove('invalid');
        try {
            const res = await lockedFetch(`/api/set-parent?id=${encodeURIComponent(currentPageId)}&parent=${encodeURIComponent(parent)}`, { method: 'POST' });
            if (!res.ok) {
                input.classList.add('invalid');
                errEl.textContent = (await res.text()) || '指定できない親ページです';
                return;
            }
            const data = await res.json();
            document.getElementById('w-pi-parent').textContent = data.parent_id || '（トップレベル）';
            document.getElementById('w-pi-parent-input').value = data.parent_id || '';
            if (data.updated_at) document.getElementById('w-pi-updated-at').textContent = formatDateTime(data.updated_at);
        } catch (e) {
            if (e && e.message === 'lock-lost') return; // 編集権喪失は handleLockLost が処理済み
            input.classList.add('invalid');
            errEl.textContent = '親ページの変更に失敗しました（通信エラー）';
        }
    }

    async function savePerms() {
        const mode = document.getElementById('w-pp-mode').value.trim();
        const group = document.getElementById('w-pp-group').value;
        const msg = document.getElementById('w-pp-msg');
        try {
            const res = await lockedFetch('/api/page-perms?id=' + currentPageId, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ mode: mode, group: group })
            });
            if (res.ok) { msg.style.color = '#16a34a'; msg.textContent = '保存しました。'; }
            else { msg.style.color = '#dc2626'; msg.textContent = '失敗: ' + (await res.text()); }
        } catch (e) {
            if (e && e.message === 'lock-lost') return; // 編集権喪失は handleLockLost が処理済み
            msg.style.color = '#dc2626'; msg.textContent = '通信エラー';
        }
    }

    async function chownPage() {
        const owner = document.getElementById('w-pp-owner-input').value.trim();
        const msg = document.getElementById('w-pp-msg');
        if (!owner) { msg.style.color = '#dc2626'; msg.textContent = '所有者を入力してください'; return; }
        try {
            const res = await lockedFetch('/api/page-chown?id=' + currentPageId, {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ owner: owner })
            });
            if (res.ok) {
                document.getElementById('w-pp-owner').textContent = owner;
                msg.style.color = '#16a34a'; msg.textContent = '所有者を変更しました。';
            } else { msg.style.color = '#dc2626'; msg.textContent = '失敗: ' + (await res.text()); }
        } catch (e) {
            if (e && e.message === 'lock-lost') return; // 編集権喪失は handleLockLost が処理済み
            msg.style.color = '#dc2626'; msg.textContent = '通信エラー';
        }
    }

    // 親ページIDの編集はサイドパネルの「親ページID」入力＋適用ボタンが担い、
    // /api/set-parent（サーバーが検証）でサイドカーへ反映する（applyParent を参照）。

    // lastSavedBlocks は直近に保存した状態（[{id, html}]）。変更の有無と、
    // 「どのブロックが変わったか」の判定に使う。null なら次回は全文保存。
    let lastSavedBlocks = null;

    // decideSaveStrategy は今回の保存方法を決める。
    //   none  … 変更なし。送らない（ブロック間を移動しただけ等）
    //   block … 1ブロックの内容だけが変わった。そのブロックだけ送る
    //   full  … 追加・削除・並べ替え・ID未採番・複数ブロック変更。従来どおり全文
    function decideSaveStrategy(blocks) {
        if (!lastSavedBlocks) return { kind: 'full' };

        // 構造（ブロックの並びとID）が変わっていたら全文で送る
        const ids = blocks.map(b => b.id).join('\n');
        const prevIds = lastSavedBlocks.map(b => b.id).join('\n');
        if (ids !== prevIds) return { kind: 'full' };

        const changed = blocks.filter((b, i) => b.html !== lastSavedBlocks[i].html);
        if (changed.length === 0) return { kind: 'none' };
        if (changed.length === 1 && changed[0].id) return { kind: 'block', block: changed[0] };
        return { kind: 'full' };
    }

    function setSaveStatus(text, color) {
        const el = document.getElementById('w-save-status');
        el.innerText = text;
        el.style.color = color;
    }

    function onSaveFailed(err) {
        if (err) console.error(err);
        setSaveStatus("❌ 保存エラー", "#ef4444");
    }

    // applySavedMeta は保存応答の共通処理（ID確定・更新日時の反映）。
    function applySavedMeta(data) {
        if (data.page_id && currentPageId !== data.page_id) {
            currentPageId = data.page_id;
            window.history.pushState({}, '', '?id=' + currentPageId); // ページリロードなし
            document.getElementById('w-pi-id').textContent = currentPageId;
        }
        if (data.updated_at) {
            document.getElementById('w-pi-updated-at').textContent = formatDateTime(data.updated_at);
        }
    }

    function notifySanitized() {
        notify('安全でない記述（スクリプトやイベント属性など）を除去して保存しました。',
            { type: 'warn', duration: 8000, id: 'sanitized' });
    }

    // notifyUnknownTypes はレジストリ未定義の data-type の**告知**（拒否ではない）。
    // 未知の種別も保存され索引にも載る（語彙モデル §9 の決定。エコーバックの流儀）。
    function notifyUnknownTypes(types) {
        if (!Array.isArray(types) || !types.length) return;
        notify('未定義の種別 ' + types.map(t => '「' + t + '」').join('・') +
            ' の表・リストがあります（そのまま保存し、検索の索引にも載ります）。',
            { type: 'info', duration: 8000, id: 'unknown-vocab' });
    }

    // notifyUnresolvedFields は「見出しの改名で計算に読まれなくなった項目」の告知。
    // 項目の鍵は見出しの表示文字なので、改名すると集計・型付きテーブルへの同期が
    // 黙って止まる。保存は通す（拒否ではない）が、気づけるように警告を出す。
    function notifyUnresolvedFields(fields) {
        if (!Array.isArray(fields) || !fields.length) return;
        notify('見出しが合わないため ' + fields.map(f => '「' + f + '」').join('・') +
            ' が計算に読まれません（見出しの文字が項目名になります）。',
            { type: 'warn', duration: 10000, id: 'unresolved-fields' });
    }

    // notifyStrippedIDs は「殻が独占する接頭辞つきの id を剥がした」ことの告知。
    // 本文の id は自由だが、この接頭辞だけは画面側（シェル）の名前空間なので侵させない。
    function notifyStrippedIDs(ids) {
        if (!Array.isArray(ids) || !ids.length) return;
        notify('id の接頭辞は画面側の予約です。' + ids.map(v => '「' + v + '」').join('・') +
            ' から接頭辞を外して保存しました。',
            { type: 'warn', duration: 10000, id: 'stripped-ids' });
    }

    function saveToServer() {
        if (!document.body.hasAttribute('edit-mode')) return; // 編集モード時のみ保存
        // 語彙が未取得のまま保存すると、カスタム要素が丸ごと欠落した本文を書いてしまう。
        if (!tagSchema) { console.warn('カスタム要素の語彙が未取得のため保存を見送りました'); return; }

        ensureBlockIds(); // 新しく作られたブロックにIDを振る（構造変更なので全文保存になる）
        const blocks = serializeBlocks();
        document.getElementById('w-html-preview').value = joinBlocks(blocks);

        const plan = decideSaveStrategy(blocks);
        if (plan.kind === 'none') return; // 変更なし。無駄な保存・DB同期・書き込みをしない

        // 変更が確定したこの時点をアンドゥ履歴へ積む（保存と同じ粒度）
        pushSnapshot(document.getElementById('w-html-preview').value);

        if (plan.kind === 'block') { saveBlockToServer(plan.block, blocks); return; }
        saveFullToServer(blocks);
    }

    // saveFullToServer は本文全体を保存する（従来の方式）。
    function saveFullToServer(blocks) {
        setSaveStatus("保存中...", "#f59e0b");
        fetch('/api/save', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({ page_id: currentPageId, html: joinBlocks(blocks), token: lockToken })
        })
        .then(res => {
            // 409 = 編集権の喪失（他者へ明け渡し済み）。退避して閲覧モードへ。
            if (res.status === 409) { handleLockLost(); return null; }
            return res.json();
        })
        .then(data => {
            if (!data) return;
            if (!data.success) { onSaveFailed(); return; }
            applySavedMeta(data);
            if (data.sanitized && typeof data.html === 'string') {
                if (applySanitizedHtml(data.html)) { updateHtmlPreview(); buildToc(); }
                notifySanitized();
                // 除去後の状態を積み直す。積まないと「除去された危険な内容」へアンドゥで戻れてしまう。
                pushSnapshot(document.getElementById('w-html-preview').value);
            }
            notifyUnknownTypes(data.unknown_types);
            notifyUnresolvedFields(data.unresolved_fields);
            notifyStrippedIDs(data.stripped_ids);
            // 保存できた状態を記録する（次回の差分判定の基準）
            lastSavedBlocks = data.sanitized ? serializeBlocks() : blocks;
            setSaveStatus("✅ 保存済", "#10b981");
        })
        .catch(onSaveFailed);
    }

    // saveBlockToServer は1ブロックだけを保存する。
    // サーバー側でブロックが見つからない／重複する場合は 409 が返るので、全文保存へ切り替える。
    function saveBlockToServer(block, blocks) {
        setSaveStatus("保存中...", "#f59e0b");
        fetch('/api/save-block', {
            method: 'POST',
            headers: { 'Content-Type': 'application/json' },
            body: JSON.stringify({
                page_id: currentPageId, block_id: block.id, html: block.html, token: lockToken
            })
        })
        .then(res => {
            if (res.status === 409) {
                // 編集権の喪失かブロック不整合か、本文を見て切り分ける
                return res.text().then(text => {
                    if (text.indexOf('編集権') >= 0) { handleLockLost(); return null; }
                    lastSavedBlocks = null; // 次は全文で送り直す
                    saveFullToServer(blocks);
                    return null;
                });
            }
            return res.json();
        })
        .then(data => {
            if (!data) return;
            if (!data.success) { onSaveFailed(); return; }
            applySavedMeta(data);
            if (data.sanitized && typeof data.html === 'string') {
                applySanitizedBlock(block, data.html);
                updateHtmlPreview();
                buildToc();
                notifySanitized();
                // 除去後の状態を積み直す（アンドゥで危険な内容へ戻らないように）
                pushSnapshot(document.getElementById('w-html-preview').value);
                lastSavedBlocks = serializeBlocks();
            } else {
                // 送ったブロックだけ記録を更新する
                lastSavedBlocks = blocks.map(b => ({ id: b.id, html: b.html }));
            }
            notifyUnknownTypes(data.unknown_types);
            notifyUnresolvedFields(data.unresolved_fields);
            notifyStrippedIDs(data.stripped_ids);
            setSaveStatus("✅ 保存済", "#10b981");
        })
        .catch(onSaveFailed);
    }

    // applySanitizedBlock はサニタイズ結果を当該ブロックへ反映する。
    // 編集中のブロックはキャレットが飛ぶので触らない（離脱時の保存で反映される）。
    function applySanitizedBlock(block, htmlStr) {
        const el = block.el;
        if (!el || !el.parentNode) return false;
        if (el.contains(document.activeElement)) return false;
        const doc = new DOMParser().parseFromString(htmlStr, 'text/html');
        const incoming = doc.body.firstElementChild;
        if (!incoming) { el.parentNode.removeChild(el); return true; }
        el.parentNode.replaceChild(incoming, el);
        return true;
    }

    // ── 編集中のアンドゥ・リドゥ（docs/【考察】アンドゥ・リドゥ.md §3）────────────
    //
    // このエディタは contenteditable に加えプログラム的なDOM操作を大量に行う
    // （ブロックの挿入・並べ替え・置換、明細の追加、カスタム要素の再描画）。
    // ブラウザ標準の undo は execCommand とネイティブ入力しか捕捉しないため、
    // それらを戻せず不整合になる。そこで本文HTMLのスナップショットを積む独自スタックを持つ。
    //
    // 粒度は「変更が確定した単位」＝保存が走る単位（既存の1.5秒デバウンス）。
    // 1キーストロークごとではないので粗いが、カスタム要素も属性ごとHTMLに乗るため
    // すべてのブロック操作を一様に扱える。
    const UNDO_LIMIT = 100;
    let undoStack = [];        // [{html, caret}] 末尾が現在の状態
    let redoStack = [];
    let suppressSnapshot = false; // 復元中は積まない（復元が新しい変更として記録されるのを防ぐ）

    // captureCaret は現在のキャレット位置を {blockId, offset} として取り出す。
    // ブロックの識別にはブロック単位保存で導入した data-id を使う。
    function captureCaret() {
        const sel = window.getSelection();
        if (!sel || sel.rangeCount === 0) return null;
        const range = sel.getRangeAt(0);
        const host = range.startContainer.nodeType === Node.ELEMENT_NODE
            ? range.startContainer
            : range.startContainer.parentElement;
        if (!host) return null;

        // 祖先をたどって「トップレベルのブロック」を探す。属性だけで探すと、
        // 貼り付けた内容が入れ子に data-id を持っていた場合に取り違える。
        const blocks = new Set(topLevelBlocks());
        let block = host;
        while (block && !blocks.has(block)) block = block.parentElement;
        if (!block || !block.getAttribute(BLOCK_ID_ATTR)) return null;

        // ブロック先頭からの文字数を数える（テキストブロックが対象）
        let offset = 0;
        const walker = document.createTreeWalker(block, NodeFilter.SHOW_TEXT);
        let node;
        while ((node = walker.nextNode())) {
            if (node === range.startContainer) return { blockId: block.getAttribute(BLOCK_ID_ATTR), offset: offset + range.startOffset };
            offset += node.nodeValue.length;
        }
        // カスタム要素の中など、テキストノードを特定できない場合はブロックだけ覚えておく
        return { blockId: block.getAttribute(BLOCK_ID_ATTR), offset: 0 };
    }

    // restoreCaret は captureCaret の記録からキャレットを復元する。
    // 対象ブロックが見つからなければ何もしない（安全側）。
    function restoreCaret(caret) {
        if (!caret || !caret.blockId) return;
        // 探すのはトップレベルのブロックだけ（入れ子の同名 data-id を拾わないため）
        const block = topLevelBlocks().find(el => el.getAttribute(BLOCK_ID_ATTR) === caret.blockId);
        if (!block) return;

        let remaining = caret.offset;
        const walker = document.createTreeWalker(block, NodeFilter.SHOW_TEXT);
        let node, target = null, targetOffset = 0;
        while ((node = walker.nextNode())) {
            if (remaining <= node.nodeValue.length) { target = node; targetOffset = remaining; break; }
            remaining -= node.nodeValue.length;
        }
        try {
            // focus() は contenteditable のキャレットを先頭へ動かすので、**範囲を張る前**に呼ぶ。
            // 逆にすると、せっかく計算した位置が focus() に上書きされてしまう。
            if (block.focus) block.focus({ preventScroll: true });

            const range = document.createRange();
            if (target) {
                range.setStart(target, targetOffset);
            } else {
                range.selectNodeContents(block);
            }
            range.collapse(true);
            const sel = window.getSelection();
            sel.removeAllRanges();
            sel.addRange(range);
        } catch (e) { /* 位置が復元できなくても編集は続行できる */ }
    }

    // pushSnapshot は現在の状態を履歴へ積む。直前と同じ内容なら積まない。
    function pushSnapshot(html) {
        if (suppressSnapshot) return;
        const top = undoStack[undoStack.length - 1];
        if (top && top.html === html) return;
        undoStack.push({ html: html, caret: captureCaret() });
        if (undoStack.length > UNDO_LIMIT) undoStack.shift();
        redoStack = []; // 新しい変更が入ったらリドゥは無効
    }

    // resetHistory はセッションの区切りで履歴を捨てる。
    // 編集権を失ったときは本文がサーバーの最新版に置き換わり手元の編集も失われているため、
    // 戻せない状態へアンドゥできないようにここで必ず捨てる。
    function resetHistory(initialHtml) {
        undoStack = [];
        redoStack = [];
        if (typeof initialHtml === 'string') undoStack.push({ html: initialHtml, caret: null });
    }

    // restoreSnapshot は履歴の状態を画面へ戻し、その結果を保存する。
    function restoreSnapshot(snap) {
        suppressSnapshot = true;
        populateEditor(snap.html);
        // populateEditor は本文を流し込むだけで編集用の属性は付けない（既存の呼び出し元も
        // 直後に applyMode を呼んでいる）。これを忘れるとブロックが contenteditable でなくなり、
        // アンドゥ直後に文字が打てなくなる。キャレット復元より前に呼ぶ必要がある。
        applyMode();
        restoreCaret(snap.caret);
        updateHtmlPreview();
        validateTypedTables(); // 復元でDOMが入れ替わり実行時の印（.cell-invalid）が消えるため
        suppressSnapshot = false;
        triggerAutoSave(); // 戻した内容を正本へ反映する
    }

    function undo() {
        if (undoStack.length < 2) return; // 先頭は編集開始時の状態なので残す
        const current = undoStack.pop();
        redoStack.push(current);
        // 内容は1つ前の状態へ戻すが、キャレットは**取り消した編集があった位置**へ置く。
        // 「戻した先の状態が作られたときのキャレット」は今回の操作と無関係な場所なので、
        // 一般的なエディタと同じく、取り消した編集の位置に寄せるほうが自然に感じられる。
        restoreSnapshot({ html: undoStack[undoStack.length - 1].html, caret: current.caret });
    }

    function redo() {
        if (redoStack.length === 0) return;
        const next = redoStack.pop();
        undoStack.push(next);
        restoreSnapshot(next);
    }

    function triggerAutoSave() {
        if (!document.body.hasAttribute('edit-mode')) return;
        const statusEl = document.getElementById('w-save-status');
        statusEl.innerText = "未保存";
        statusEl.style.color = "#94a3b8";

        if (saveTimeout) clearTimeout(saveTimeout);
        saveTimeout = setTimeout(() => {
            saveToServer();
        }, 1500); // 1.5秒デバウンス
    }

    function setupAutoSave() {
        const observer = new MutationObserver((mutations) => {
            triggerAutoSave();
            buildToc(); // 見出しの追加・削除・移動を目次に反映
        });
        observer.observe(document.getElementById('w-editor-content'), {
            childList: true,
            subtree: true,
            characterData: true,
            attributes: true,
            attributeFilter: ['value', 'order-no', 'client-name', 'ordered-at', 'supplier-name', 'item-id', 'item-name', 'price', 'quantity', 'cost', 'status', 'tag']
        });

        document.getElementById('w-editor-content').addEventListener('input', () => {
            triggerAutoSave();
            buildToc(); // 見出しテキストの編集を目次に反映
        });

        // ブロックの編集が終わった時点（＝フォーカスがそのブロックから外れた時）で即保存する。
        // サーバーはサニタイズ結果を返すので、除去が起きた場合はその変化がすぐ画面に出る
        // （docs/本文サニタイズ設計.md のエコーバック方式）。デバウンス保存は、ひとつの
        // ブロックを編集し続けている間の保険として引き続き効く。
        document.getElementById('w-editor-content').addEventListener('focusout', () => {
            if (!document.body.hasAttribute('edit-mode')) return;
            if (saveTimeout) clearTimeout(saveTimeout);
            saveToServer();
        });
    }

    // applySanitizedHtml はサーバーがサニタイズした本文をエディタへ反映する。
    // 差分のあったトップレベルのブロックだけを差し替え、フォーカス中のブロックは
    // キャレットが飛ばないよう据え置く（そのブロックは離脱時の保存で反映される）。
    function applySanitizedHtml(htmlStr) {
        const container = document.getElementById('w-editor-content');
        const doc = new DOMParser().parseFromString(htmlStr, 'text/html');
        const incoming = Array.from(doc.body.children);
        // 現在のトップレベル要素（wrapInBlock により .editor-block > .block-content > 実体）
        const holders = Array.from(container.children).map(block => {
            const content = block.classList.contains('editor-block')
                ? block.querySelector('.block-content')
                : null;
            return { block, content, el: content ? content.firstElementChild : block };
        });

        // 個数が変わった（ブロックごと除去された等）場合は全体を作り直す。
        if (holders.length !== incoming.length) {
            populateEditor(htmlStr);
            return true;
        }

        let replaced = false;
        holders.forEach((h, i) => {
            if (!h.el || !h.content) return;
            if (h.el.outerHTML === incoming[i].outerHTML) return;
            // 編集中のブロックは触らない（離脱時の保存で反映される）
            if (h.block.contains(document.activeElement)) return;
            h.content.replaceChild(incoming[i], h.el);
            replaced = true;
        });
        return replaced;
    }

    // ── 編集モードと編集ロック ──────────────────────────────────────────────
    // 編集は悲観ロック（ページ単位）で保護する。編集モードに入るときにロックを取得し、
    // 抜けるとき／タブを閉じるときに解放する。保持中は status を10秒ポーリングして
    // 「待機者あり」や「明け渡し（lost）」を検知する。設計は
    // docs/【考察】同時編集の競合対策.md を参照。
    let lockToken = null;       // 保持中のみ非null
    let lockES = null;          // ロック状態のSSE購読（保持者/待機者で共用。閲覧時はnull）

    // applyMode は表示/編集の DOM 切り替えのみを行う（ロックの取得・解放は onModeToggle が担う）。
    function applyMode() {
        const toggle = document.getElementById('w-mode-toggle');
        const modeText = document.getElementById('w-mode-text');
        const editor = document.getElementById('w-editor-content');

        if (toggle.checked) {
            document.body.setAttribute('edit-mode', '');
            modeText.innerText = "編集モード";
            modeText.style.color = "#10b981";
        } else {
            document.body.removeAttribute('edit-mode');
            modeText.innerText = "閲覧モード";
            modeText.style.color = "#475569";
            hideSlashMenu();
            hideContextToolbar();
            hideTableToolbar();
        }

        // 編集できるのは本文のブロック（見出し・段落・リスト・表・定義リスト・引用…）。
        // 除くのは2つ——計算ビューのマーカー（中身はサーバーが所有）と、貼り付け等で
        // 紛れ込んだカスタム要素（語彙には無いので保存時にアンラップされる）。
        // 見出しや段落を名指ししていたころは、許可されている h4〜h6・ul・table が
        // 表示されるだけで編集できなかった。
        const standardElements = Array.from(editor.querySelectorAll('.block-content > *'))
            .filter(el => !isCustomTag(el.tagName.toLowerCase()) && !isViewSection(el));
        standardElements.forEach(el => {
            if (toggle.checked) { el.setAttribute('contenteditable', 'true'); el.oninput = updateHtmlPreview; }
            else { el.removeAttribute('contenteditable'); el.oninput = null; }
        });

        enhanceFileSections(); // ファイル容器のクロームをモードに合わせて作り直す
        updateHtmlPreview();
        refreshPermsEditable(); // 権限カードの編集可否を編集モードに合わせる
    }

    // onModeToggle は編集モード切替の入口。編集に入るときはロックを取得してから、
    // 抜けるときはロックを解放してから DOM を切り替える。
    async function onModeToggle() {
        const toggle = document.getElementById('w-mode-toggle');
        if (toggle.checked) {
            await enterEditMode();
        } else {
            closeLockEvents();
            dismissToast('waiter'); // 自分が編集をやめたら「待機者あり」トーストも消す
            await releaseLock();
            applyMode();
            // 自分の操作での退出もモードを明示（ヘッダーが隠れるため）。id:'mode' で取得トーストと置換。
            notify('閲覧モードに戻りました。', { type: 'info', duration: 2500, id: 'mode' });
        }
    }

    // enterEditMode は編集モードへ入る。ロックを取得できたら編集UIへ切り替え、保持者として SSE を購読する。
    // busy なら閲覧モードに留め、待機者として購読しつつ「待機をやめる」トーストを表示する
    // （空き次第サーバーからの available で自動的に編集へ入る。available 経路からも呼ばれる）。
    async function enterEditMode() {
        const toggle = document.getElementById('w-mode-toggle');
        const ok = await acquireLock();
        if (!ok) { toggle.checked = false; applyMode(); return false; }
        toggle.checked = true;
        applyMode();
        openHolderEvents();

        // ブロック単位保存の基準を作る。未採番のブロックがあれば採番し、その回は
        // 全文保存でIDを正本へ永続化させる（lastSavedBlocks を null のままにする）。
        // すべて採番済みなら、いまの状態を基準にして以降は差分だけ送れる。
        lastSavedBlocks = ensureBlockIds() > 0 ? null : serializeBlocks();

        // アンドゥ履歴はこの編集セッションのもの。開始時の状態を起点として積む。
        updateHtmlPreview();
        resetHistory(document.getElementById('w-html-preview').value);
        validateTypedTables(); // 既存本文の型不一致を編集開始時に可視化する
        // ヘッダー（モードトグル）はスクロールで隠れるため、モード移行は緑トーストで明示する。
        // 待機後に available で自動入室した場合もここを通るので「権限が回ってきた」気付きにもなる。
        notify('編集権を取得しました。編集モードです。', { type: 'success', duration: 3000, id: 'mode' });
        return true;
    }

    // populateEditor は本文HTMLをエディタへ流し込みブロック化する（ロード時・ロック取得時に共用）。
    // 本文を丸ごと入れ替えるので、ブロック単位保存の基準は破棄する（次回は全文保存）。
    function populateEditor(htmlStr) {
        lastSavedBlocks = null;
        const doc = new DOMParser().parseFromString(htmlStr, 'text/html');
        const editorContent = document.getElementById('w-editor-content');
        editorContent.innerHTML = '';
        Array.from(doc.body.children).forEach(child => {
            if (child.tagName.toLowerCase() === 'script') return;
            editorContent.appendChild(child);
        });
        initBlocks();
        buildToc();
        enhanceFileSections();
    }

    // acquireLock は編集ロックを取得する。成功なら最新HTMLで再ロードして true、
    // busy なら「待機をやめる」ボタン付きトーストを出し、待機者として SSE を購読して false を返す
    // （空き次第でサーバーから push され、自動的に編集へ入る）。
    async function acquireLock() {
        try {
            const res = await fetch('/api/lock?id=' + currentPageId, { method: 'POST' });
            if (res.status === 423) {
                const d = await res.json().catch(() => ({}));
                showBusyToast(d);
                openWaiterEvents(); // 空き通知を待つ（プッシュ）
                return false;
            }
            if (!res.ok) { notify('編集ロックを取得できませんでした。', { type: 'error' }); return false; }
            const d = await res.json();
            lockToken = d.token;
            // ロックを取ったら最新の本文へ載せ替える。ロック応答ではなく /api/load を読むのは、
            // そちらが計算ビューのサーバー事前描画を通るため（生のHTMLだとビューの中身が消える）。
            // ロック保持中は他者が保存できないので、この2手の間に内容は変わらない。
            try {
                const body = await fetch('/api/load?id=' + currentPageId);
                if (body.ok) {
                    const html = await body.text();
                    if (html.length) populateEditor(html);
                }
            } catch (e) { /* 読めなければ画面の内容のまま編集を続ける */ }
            dismissToast('lock');
            return true;
        } catch (e) {
            notify('編集ロックの取得に失敗しました（通信エラー）。', { type: 'error' });
            return false;
        }
    }

    // showBusyToast は busy(423)応答／busyイベントから「○○が編集中・残り時間」トーストを出す。
    // すでに待機者として購読済みで、空き次第サーバーからの available で自動的に編集へ入るため、
    // ボタンは取得（再試行）ではなく「待機をやめる（キャンセル）」を提供する。
    function showBusyToast(d) {
        const who = d.same_user ? 'このページは別のタブ／ウィンドウ' : `このページは「${d.holder || '他のユーザー'}」さん`;
        const sec = d.grace_remaining_sec || 0;
        const tail = sec > 0 ? `あと約${Math.max(1, Math.ceil(sec / 60))}分で空きます。` : '';
        notify(`${who}で編集中です。${tail}空き次第このまま自動で編集を開始します。閲覧のみ可能です。`, {
            type: 'warn', duration: 0, id: 'lock',
            action: { label: '待機をやめる', onClick: () => cancelWaiting() }
        });
    }

    // cancelWaiting は空き待ち（待機者としての SSE 購読）をやめ、busyトーストを消す。
    // 購読を閉じるとサーバーは待機者の離脱として猶予をキャンセルする（保持者は保持を続ける）。
    function cancelWaiting() {
        closeLockEvents();
        dismissToast('lock');
    }

    // releaseLock は保持中のロックを明示解放する。
    async function releaseLock() {
        if (!lockToken) return;
        const t = lockToken; lockToken = null;
        try { await fetch(`/api/unlock?id=${currentPageId}&token=${encodeURIComponent(t)}`, { method: 'POST' }); } catch (e) {}
    }

    // lockedFetch はエディタ内の変更操作を、現在の編集ロックトークン付き（X-Lock-Token ヘッダ）で送る
    // 共通ラッパ。サーバー（RequireEditLock）が他者保持/失効を 409 で返したら handleLockLost に集約する
    // （クリップボード退避＋閲覧復帰＋再読込）。権限変更・親付け替え・将来の画像/PDF操作などは必ず
    // これを通すことで、本文編集と同じロックを共有して直列化する。409時は Error('lock-lost') を投げる。
    async function lockedFetch(url, opts = {}) {
        const headers = Object.assign({}, opts.headers, { 'X-Lock-Token': lockToken || '' });
        const res = await fetch(url, Object.assign({}, opts, { headers }));
        if (res.status === 409) { handleLockLost(); throw new Error('lock-lost'); }
        return res;
    }

    // ── ロック状態のプッシュ通知（SSE） ─────────────────────────────────────
    // ポーリングの代わりに /api/lock-events を購読し、サーバーからの状態変化を受け取る。
    // 接続中＝presence なので、タブを閉じれば即座に保持者離脱／待機者離脱が検知される。

    function closeLockEvents() { if (lockES) { lockES.close(); lockES = null; } }

    // openHolderEvents は保持者として購読し、「待機者あり」「明け渡し（lost）」を受け取る。
    function openHolderEvents() {
        closeLockEvents();
        lockES = new EventSource(`/api/lock-events?id=${currentPageId}&role=holder&token=${encodeURIComponent(lockToken)}`);
        lockES.onmessage = (e) => {
            let st; try { st = JSON.parse(e.data); } catch (_) { return; }
            if (st.type === 'lost') { handleLockLost(); return; }
            if (st.type === 'waiter') {
                const mins = Math.max(1, Math.ceil((st.grace_remaining_sec || 0) / 60));
                notify(`このページの編集を待っている人がいます。あと約${mins}分で編集権が移ります。`, {
                    type: 'warn', duration: 0, id: 'waiter',
                    // 待たずに今すぐ譲る。直前の内容は保存してから解放するので失われない。
                    action: { label: '今すぐ編集権を渡す', onClick: () => { saveToServer(); leaveEditMode(); } }
                });
            } else { // holding（待機者なし）
                dismissToast('waiter');
            }
        };
    }

    // openWaiterEvents は待機者として購読し、空き次第（available）で自動的に編集へ入る。
    function openWaiterEvents() {
        closeLockEvents();
        lockES = new EventSource(`/api/lock-events?id=${currentPageId}&role=waiter`);
        lockES.onmessage = (e) => {
            let st; try { st = JSON.parse(e.data); } catch (_) { return; }
            if (st.type === 'available') {
                closeLockEvents();
                enterEditMode(); // 取得して編集へ（競合に負ければ acquireLock が再び待機購読する）
            } else if (st.type === 'busy') {
                showBusyToast(st); // 残り時間などを更新
            }
        };
    }

    // handleLockLost は明け渡し/失効で編集権を失ったときの処理。
    // 自分の変更をクリップボードへ退避し、閲覧モードへ戻したうえで最新の内容を再読込する
    // （他の人の編集を反映する）。
    async function handleLockLost() {
        closeLockEvents();
        dismissToast('waiter'); // 「待機者あり」トーストは編集権を失った時点で無意味なので消す
        lockToken = null;
        let saved = false;
        try {
            const html = document.getElementById('w-html-preview').value;
            if (navigator.clipboard && html) { await navigator.clipboard.writeText(html); saved = true; }
        } catch (e) { saved = false; }
        const toggle = document.getElementById('w-mode-toggle');
        toggle.checked = false;
        applyMode();
        // 本文がサーバーの最新版に置き換わり、手元の編集は失われている（上で退避済み）。
        // 戻せない状態へアンドゥできてしまわないよう、履歴はここで捨てる。
        resetHistory();
        await reloadContent(); // 最新（他の人の編集）を反映
        const tail = saved ? 'あなたの変更はクリップボードへ退避しました。' : 'あなたの未保存の変更は反映されていない場合があります。';
        notify('編集権が他の人に移りました。最新の内容を再読込しました。' + tail,
            { type: 'warn', duration: 10000, id: 'lost' }); // 情報通知なので一定時間で自動消去
    }

    // reloadContent は現在のページ本文をサーバーから取り直してエディタへ反映する（閲覧モード想定）。
    async function reloadContent() {
        try {
            const res = await fetch('/api/load?id=' + currentPageId);
            if (!res.ok) return;
            populateEditor(await res.text());
            applyMode();
            loadPageMeta(); // 更新日時などの属性表示も最新化
        } catch (e) {}
    }

    // leaveEditMode は編集モードを抜ける（トグルOFF相当）。
    async function leaveEditMode() {
        const toggle = document.getElementById('w-mode-toggle');
        if (!toggle.checked) return;
        toggle.checked = false;
        resetHistory(); // 編集セッションの終わり。アンドゥ履歴は持ち越さない
        await onModeToggle();
    }


    // ── アプリ内通知（トースト） ────────────────────────────────────────────
    // notify(message, {type, duration, id, action}) でトーストを出す。
    //   type: info|warn|error|success / duration: ms（0で消えない）/ id: 同一idは置換 /
    //   action: {label, onClick}
    function notify(message, opts) {
        opts = opts || {};
        const host = document.getElementById('w-toast-host');
        if (!host) return;
        if (opts.id) dismissToast(opts.id);
        const el = document.createElement('div');
        el.className = 'toast ' + (opts.type || 'info');
        if (opts.id) el.dataset.toastId = opts.id;
        const span = document.createElement('span');
        span.style.flex = '1';
        span.textContent = message;
        el.appendChild(span);
        if (opts.action) {
            const b = document.createElement('button');
            b.textContent = opts.action.label;
            b.onclick = () => { dismissEl(el); opts.action.onClick(); };
            el.appendChild(b);
        }
        const close = document.createElement('button');
        close.className = 'toast-close';
        close.textContent = '✕';
        close.onclick = () => dismissEl(el);
        el.appendChild(close);
        host.appendChild(el);
        const dur = opts.duration === undefined ? 5000 : opts.duration;
        if (dur > 0) setTimeout(() => dismissEl(el), dur);
        return el;
    }
    function dismissEl(el) { if (el && el.parentNode) el.parentNode.removeChild(el); }
    function dismissToast(id) {
        const host = document.getElementById('w-toast-host');
        if (host) host.querySelectorAll(`[data-toast-id="${id}"]`).forEach(dismissEl);
    }

    // HTMLソースコードのリアルタイムプレビューを生成する処理
    //
    // カスタム要素の語彙（要素名 → 保存する属性）は **サーバーの /api/tag-schema** から取得する。
    // 各プラグインの Tags() が唯一の正本で、サニタイズ許可リストも同じ宣言から作られるため、
    // 「保存する属性」と「許可される属性」が食い違いようがない。ここに手書きの表を持たないので、
    // プラグインを追加すれば新しい要素もそのまま保存されるようになる。
    let tagSchema = null;              // { "p": ["data-id"], "table": ["data-id", "data-type"], ... }
    let voidTags = new Set();          // 終了タグを書かない要素（br・img 等）
    // 語彙レジストリ（①）の形式定義。スラッシュメニューの項目と挿入骨格
    // （<table data-type> / <dl data-type>）はここから生成する（語彙モデル §7 の原則1:
    // エディタに手書きの形式リストを置かない）。
    let vocabDefs = [];                // [{ type, display_name, icon, element, columns: [...] }, ...]
    // 語→型の推論辞書。手書きせず /api/tag-schema から受け取る（サーバーの
    // resolveColumnType と同じ辞書＝検証と索引の型判定が食い違わない）。
    let typeInferenceDict = {};        // { "数量": "number", "検査日": "date", ... }

    async function loadTagSchema() {
        try {
            const res = await fetch('/api/tag-schema');
            if (!res.ok) throw new Error('tag-schema ' + res.status);
            const d = await res.json();
            tagSchema = (d && d.elements) || {};
            voidTags = new Set((d && d.void) || []);
            vocabDefs = (d && d.vocab) || [];
            typeInferenceDict = (d && d.type_inference) || {};
        } catch (e) {
            console.error('本文の語彙を取得できませんでした:', e);
            tagSchema = {};
            voidTags = new Set();
            vocabDefs = [];
            typeInferenceDict = {};
        }
    }

    // isCustomTag は名前にハイフンを含む要素（＝HTMLの仕様上かならずカスタム要素）かを返す。
    // 語彙モデルへの移行完了（2026-08-20）で本文の語彙からカスタム要素は無くなったので、
    // 通常は常に false。貼り付け等で紛れ込んだ未知のカスタム要素を編集可能にしない
    // ための防御として残している（保存時はサニタイザ同様アンラップされる）。
    function isCustomTag(name) { return name.indexOf('-') !== -1; }

    // isViewSection は計算ビューのマーカー（子ページ一覧・手配集計）かを返す。
    // 中身はサーバー事前描画（vocab-chrome）が所有するので、編集モードでも
    // contenteditable にしない（view_render.go 参照）。
    function isViewSection(el) {
        if (!el.tagName || el.tagName !== 'SECTION') return false;
        const t = el.getAttribute('data-type');
        if (!t) return false;
        const def = vocabDefs.find(v => v.type === t);
        return !!(def && def.view);
    }

    // esc は属性値・テキストをHTMLとして安全な形にエスケープする。
    // これが無いと、タグの値に " や < を入力しただけで生成HTMLが壊れ、値が欠けたり
    // 意図しない属性が生まれたりする（サーバーのサニタイザがそれを除去してしまう）。
    function esc(s) {
        return String(s == null ? '' : s)
            .replace(/&/g, '&amp;')
            .replace(/</g, '&lt;')
            .replace(/>/g, '&gt;')
            .replace(/"/g, '&quot;');
    }

    // ── ブロック単位保存のための識別子 ──────────────────────────────────
    // data-id は**任意**の属性。付いていればそのブロックだけを保存でき、
    // 無ければ従来どおり全文保存になる（手で書いたHTMLもそのまま扱えるようにするため）。
    //
    // `id` ではなく data-* にしているのは、本文がシェルと同じDOMへ合成されるため。
    // `id` を許すと本文から html-preview 等シェル側の要素を乗っ取れてしまい
    // （getElementById は文書順で最初の要素を返す）、保存や権限UIが壊れる経路が開く。
    //
    // 値は4桁の base36。正本HTMLのdiffを読みやすく保つため短くし、
    // 短さで衝突しうる分は**採番時の重複チェック**で潰す（assignBlockId）。
    const BLOCK_ID_ATTR = 'data-id';
    const BLOCK_ID_LEN = 4;

    // newBlockId は used に無い識別子を返す。短いので衝突しうるが、
    // 使用済み集合と突き合わせて振り直すため文書内で一意になる。
    function newBlockId(used) {
        for (let attempt = 0; attempt < 50; attempt++) {
            const b = new Uint8Array(BLOCK_ID_LEN);
            crypto.getRandomValues(b);
            const id = Array.from(b, x => (x % 36).toString(36)).join('');
            if (!used || !used.has(id)) return id;
        }
        // 万一ここまで衝突が続いたら桁を増やして確実に抜ける
        return Date.now().toString(36).slice(-6);
    }

    // topLevelBlocks は保存対象になるトップレベル要素を文書順で返す。
    function topLevelBlocks() {
        const container = document.getElementById('w-editor-content');
        return Array.from(container.querySelectorAll(
            '.editor-block > .block-content > *, #w-editor-content > :not(.editor-block)'
        )).filter(el => el && isSerializableBlock(el));
    }

    // isSerializableBlock は、その要素が保存対象のトップレベルブロックかを返す。
    // 判定は語彙（/api/tag-schema ＝ サニタイザの許可リスト）だけに任せる。
    // エディタ自身が挿す枠（.editor-block / .block-content / .block-controls）は
    // 本文ではないので除く（div が語彙に入っているため明示的に弾く必要がある）。
    const EDITOR_CHROME = ['editor-block', 'block-content', 'block-controls'];

    function isSerializableBlock(el) {
        if (!tagSchema) return false;
        if (EDITOR_CHROME.some(c => el.classList.contains(c))) return false;
        return !!tagSchema[el.tagName.toLowerCase()];
    }

    // ensureBlockIds は未採番のブロックへIDを振り、**重複も修復する**。変更した数を返す。
    //
    // 既存ページは初回の編集時にここで採番され、その回の全文保存で正本へ永続化される。
    // 重複修復が要るのは、ページ間でブロックをコピーするとIDが重複しうるため
    // （重複したままだとサーバーが差し替え対象を特定できず 409 で全文保存に落ち続ける）。
    function ensureBlockIds() {
        const used = new Set();
        let changed = 0;
        topLevelBlocks().forEach(el => {
            const id = el.getAttribute(BLOCK_ID_ATTR);
            if (id && !used.has(id)) { used.add(id); return; } // 一意なのでそのまま
            const fresh = newBlockId(used);                     // 未採番 or 重複 → 振り直す
            el.setAttribute(BLOCK_ID_ATTR, fresh);
            used.add(fresh);
            changed++;
        });
        return changed;
    }

    // ── 本文のシリアライズ ──────────────────────────────────────────────────
    // 本文（p・ul・table・dl・section・strong …）は**中身そのものが値**なので、DOMを
    // そのまま辿って「語彙にある要素と属性だけを残す」形で書き出す。判定基準はサニタイザと
    // 同じ語彙（/api/tag-schema）なので、ここを通ったものはサーバーでも落ちない。
    // かつては属性へ値を持つカスタム要素だけ別経路（serializeCustomElement）で書き出して
    // いたが、移行完了（2026-08-20）で経路は1本になった。
    //
    // el.innerHTML をそのまま使わないのは、ブラウザが編集中に挿す一時的な属性
    // （contenteditable・spellcheck 等）や、貼り付けで紛れ込んだ未許可の要素・style を
    // 落とすため。落としておかないと保存のたびにサーバーが除去し、エコーバックで
    // ブロックが差し替わってキャレットが飛ぶ。

    // WHITESPACE_INSENSITIVE は、子を1行ずつ字下げして書いても表示が変わらない要素。
    // 逆に p や li の中で改行を入れると空白として描画されてしまうため、対象を絞る。
    const WHITESPACE_INSENSITIVE = new Set([
        'ul', 'ol', 'dl', 'table', 'thead', 'tbody', 'tfoot', 'tr', 'colgroup',
        'figure', 'details', 'section', 'article', 'header', 'footer', 'aside', 'nav',
    ]);

    function serializeAttrs(el, allowed, extraAttrs) {
        let attrs = extraAttrs || '';
        allowed.forEach(a => {
            if (!el.hasAttribute(a)) return;
            attrs += ` ${a}="${esc(el.getAttribute(a))}"`;
        });
        return attrs;
    }

    // serializeChildren は子ノード列を書き出す。未知の要素は**アンラップ**して中身だけ残す
    // （サーバーのサニタイザと同じ扱い。文字が消えるより形が崩れる方が損失が小さい）。
    function serializeChildren(el, indent) {
        const pretty = WHITESPACE_INSENSITIVE.has(el.tagName.toLowerCase());
        let out = '';
        el.childNodes.forEach(node => {
            // エンハンサが挿す編集クローム（PDFドロップゾーン・プレビュー等）は本文ではない
            if (node.nodeType === Node.ELEMENT_NODE && node.classList.contains('vocab-chrome')) return;
            if (node.nodeType === Node.TEXT_NODE) {
                // 字下げする要素の中の空白だけのテキストは整形由来なので捨てる
                if (pretty && !node.nodeValue.trim()) return;
                out += esc(node.nodeValue);
                return;
            }
            if (node.nodeType !== Node.ELEMENT_NODE) return; // コメント等は残さない
            const child = serializeNode(node, pretty ? indent + '    ' : '');
            out += pretty && child ? '\n' + child : child;
        });
        return pretty && out ? out + '\n' + indent : out;
    }

    function serializeNode(el, indent) {
        const name = el.tagName.toLowerCase();
        const allowed = tagSchema && tagSchema[name];
        if (!allowed) return serializeChildren(el, indent); // 未知の要素はアンラップ
        const open = `${indent}<${name}${serializeAttrs(el, allowed)}>`;
        if (voidTags.has(name)) return open;
        return `${open}${serializeChildren(el, indent)}</${name}>`;
    }

    // serializeBlock は1つのトップレベルブロックを保存用HTMLへ変換する。
    function serializeBlock(el) {
        const name = el.tagName.toLowerCase();
        const allowed = tagSchema && tagSchema[name];
        if (!allowed) return '';
        // data-id は全要素共通の属性として語彙（/api/tag-schema）に含まれるので、
        // serializeAttrs が書き出す。ここで足すと二重出力になる。
        const open = `<${name}${serializeAttrs(el, allowed)}>`;
        if (voidTags.has(name)) return open + '\n';
        return `${open}${serializeChildren(el, '')}</${name}>\n`;
    }

    // serializeBlocks は本文を「ブロックの配列」として返す。
    // 全文保存にも、どのブロックが変わったかの判定にも同じ結果を使う。
    function serializeBlocks() {
        return topLevelBlocks().map(el => ({
            el,
            id: el.getAttribute(BLOCK_ID_ATTR) || '',
            html: serializeBlock(el),
        })).filter(b => b.html !== '');
    }

    function joinBlocks(blocks) {
        return blocks.map(b => b.html).join('').trim();
    }

    function updateHtmlPreview() {
        document.getElementById('w-html-preview').value = joinBlocks(serializeBlocks());
    }

    // ── 語彙レジストリ駆動の挿入骨格（汎用表エディタv1） ──────────────────
    // レジストリの列定義から <table data-type>（見出し行＋空のデータ行1行）または
    // <dl data-type>（dt/dd の組）を機械的に生成する。DOM生成は createElement ＋
    // textContent 代入（DOMが自動エスケープする）で行い、文字列置換＋innerHTML の
    // テンプレート機構は持ち込まない（Web Components 廃止決定・語彙モデル §7）。
    // defaultFieldValue は骨格生成時に埋める既定値。日付は今日、発注書番号は
    // 一意な仮番号（order_no は UNIQUE 制約があり、空のまま複数保存すると衝突するため。
    // 旧 createComponentElement の 'PO-' 採番と同じ理由）。
    function defaultFieldValue(col) {
        if (col.type === 'date') return new Date().toISOString().split('T')[0];
        if (col.field === 'order-no') return 'PO-' + Date.now().toString().slice(-6);
        return '';
    }

    function buildVocabSkeleton(def) {
        if (def.element === 'section') {
            // 業務文書ブロック（論点A・案1）: section が ヘッダ dl（data-type 無し）と
            // 明細表を包む。項目の鍵は dt の表示文字が運ぶ。file 容器は列を持たない。
            const sec = document.createElement('section');
            sec.setAttribute('data-type', def.type);
            if (def.columns && def.columns.length) {
                const dl = document.createElement('dl');
                def.columns.forEach(col => {
                    const dt = document.createElement('dt');
                    dt.textContent = col.label;
                    const dd = document.createElement('dd');
                    const v = defaultFieldValue(col);
                    if (v) dd.textContent = v;
                    else dd.appendChild(document.createElement('br')); // 空 dd のキャレット足場（emptyEditable 参照）
                    dl.appendChild(dt);
                    dl.appendChild(dd);
                });
                sec.appendChild(dl);
            }
            if (def.items) {
                const itemsDef = vocabDefs.find(v => v.type === def.items);
                if (itemsDef) sec.appendChild(buildVocabSkeleton(itemsDef));
            }
            return sec;
        }
        if (def.element === 'dl') {
            const dl = document.createElement('dl');
            dl.setAttribute('data-type', def.type);
            (def.columns || []).forEach(col => {
                const dt = document.createElement('dt');
                dt.textContent = col.label;
                // 空の dd には <br> の足場が要る（emptyEditable のコメント参照）
                const dd = emptyEditable('dd');
                dl.appendChild(dt);
                dl.appendChild(dd);
            });
            return dl;
        }
        // table: 最初の tr が見出し行（列の鍵と型を運ぶ。文書自身がスキーマを携帯する）
        const table = document.createElement('table');
        table.setAttribute('data-type', def.type);
        const tbody = document.createElement('tbody');
        const head = document.createElement('tr');
        const row = document.createElement('tr');
        (def.columns || []).forEach(col => {
            const th = document.createElement('th');
            // 見出しの表示文字が列の鍵になる（機械キーはレジストリの Label 経由で解決され、
            // 本文には書き出さない。語彙モデル §5.1「見える文字がすべて」）
            th.textContent = col.label;
            head.appendChild(th);
            row.appendChild(document.createElement('td'));
        });
        tbody.appendChild(head);
        tbody.appendChild(row);
        table.appendChild(tbody);
        return table;
    }

    // focusFirstCell は挿入直後の骨格の最初の入力先（td / dd）へキャレットを置く。
    function focusFirstCell(el) {
        const cell = el.querySelector('td, dd');
        if (!cell) { if (el.focus) el.focus(); return; }
        const range = document.createRange();
        range.selectNodeContents(cell);
        range.collapse(true);
        const sel = window.getSelection();
        sel.removeAllRanges();
        sel.addRange(range);
    }

    function createComponentElement(type, isEdit, ...args) {
        let newEl;
        if (type && type.indexOf('vocab:') === 0) {
            const def = vocabDefs.find(v => v.type === type.slice(6));
            if (!def) return null;
            newEl = buildVocabSkeleton(def);
            if (def.container) { // 容器つきの形式（受発注は file 容器の中に生まれる）
                const cdef = vocabDefs.find(v => v.type === def.container);
                if (cdef) {
                    const wrap = buildVocabSkeleton(cdef);
                    wrap.appendChild(newEl);
                    newEl = wrap;
                }
            }
        } else if (type === 'h1') {
            newEl = document.createElement('h1');
            newEl.innerText = '';
        } else if (type === 'p') {
            newEl = document.createElement('p');
            newEl.innerText = '';
        }
        if (!newEl) return null;

        // 編集モードで挿すなら、構造HTML（見出し・段落・表・定義リスト…）は編集可能にする。
        // カスタム要素と計算ビューのマーカーは対象外（applyMode と同じ規則）。
        if (isEdit && !isCustomTag(newEl.tagName.toLowerCase()) && !isViewSection(newEl)) {
            newEl.setAttribute('contenteditable', 'true');
            newEl.oninput = updateHtmlPreview;
        }

        return newEl;
    }

    function insertComponent(type, refBlock = null, position = 'after', ...args) {
        const editor = document.getElementById('w-editor-content');
        const isEdit = document.body.hasAttribute('edit-mode');
        
        const newEl = createComponentElement(type, isEdit, ...args);
        if (!newEl) return null;

        const block = wrapInBlock(newEl);

        if (refBlock && refBlock.classList.contains('editor-block')) {
            if (position === 'after') {
                refBlock.parentNode.insertBefore(block, refBlock.nextSibling);
            } else {
                refBlock.parentNode.insertBefore(block, refBlock);
            }
        } else {
            const selection = window.getSelection();
            if (selection.rangeCount > 0 && editor.contains(selection.anchorNode)) {
                let targetNode = selection.anchorNode;
                while (targetNode && targetNode.parentNode !== editor) {
                    if (targetNode.classList && targetNode.classList.contains('editor-block')) break;
                    targetNode = targetNode.parentNode;
                }
                if (targetNode && targetNode.classList && targetNode.classList.contains('editor-block')) {
                    targetNode.parentNode.insertBefore(block, targetNode.nextSibling);
                } else {
                    editor.appendChild(block);
                }
            } else {
                editor.appendChild(block);
            }
        }
        
        if (typeof newEl.render === 'function') {
            newEl.render();
        }
        enhanceFileSections();
        updateHtmlPreview();

        if (isEdit && (newEl.tagName === 'H1' || newEl.tagName === 'H2' || newEl.tagName === 'H3' || newEl.tagName === 'P')) {
            newEl.focus();
        } else if (isEdit && (newEl.tagName === 'TABLE' || newEl.tagName === 'DL' || newEl.tagName === 'SECTION')) {
            focusFirstCell(newEl); // 挿入した骨格の最初のセルから入力を始められるように
        }

        return newEl;
    }

    function replaceBlockWithComponent(type, block) {
        const isEdit = document.body.hasAttribute('edit-mode');
        const newEl = createComponentElement(type, isEdit);
        if (!newEl) return null;

        const contentWrapper = block.querySelector('.block-content');
        contentWrapper.innerHTML = '';
        contentWrapper.appendChild(newEl);

        if (typeof newEl.render === 'function') {
            newEl.render();
        }
        enhanceFileSections();
        updateHtmlPreview();
        
        if (isEdit && (newEl.tagName === 'H1' || newEl.tagName === 'P')) {
            newEl.focus();
        } else if (isEdit && (newEl.tagName === 'TABLE' || newEl.tagName === 'DL' || newEl.tagName === 'SECTION')) {
            focusFirstCell(newEl);
        }
        return newEl;
    }

    function setupDragAndDrop(block) {
        const dragHandle = block.querySelector('.drag-handle');
        if (!dragHandle) return;

        dragHandle.addEventListener('dragstart', (e) => {
            draggedBlock = block;
            setTimeout(() => block.classList.add('dragging'), 0);
        });

        block.addEventListener('dragend', () => {
            draggedBlock = null;
            block.classList.remove('dragging');
            document.querySelectorAll('.editor-block').forEach(b => b.classList.remove('drag-over'));
        });

        block.addEventListener('dragover', (e) => {
            e.preventDefault();
            if (block !== draggedBlock) {
                block.classList.add('drag-over');
            }
        });

        block.addEventListener('dragleave', () => {
            block.classList.remove('drag-over');
        });

        block.addEventListener('drop', (e) => {
            e.preventDefault();
            block.classList.remove('drag-over');
            if (draggedBlock && draggedBlock !== block) {
                const bounding = block.getBoundingClientRect();
                const offset = e.clientY - bounding.top;
                if (offset > bounding.height / 2) {
                    block.parentNode.insertBefore(draggedBlock, block.nextSibling);
                } else {
                    block.parentNode.insertBefore(draggedBlock, block);
                }
                updateHtmlPreview();
            }
        });
    }

    function wrapInBlock(element) {
        if (element.classList && element.classList.contains('editor-block')) return element;
        
        const block = document.createElement('div');
        block.className = 'editor-block';
        
        const controls = document.createElement('div');
        controls.className = 'block-controls';
        controls.contentEditable = 'false';
        
        const addBtn = document.createElement('button');
        addBtn.className = 'add-btn';
        addBtn.innerHTML = '＋';
        addBtn.title = 'ブロックを下に追加';
        addBtn.onclick = () => {
            const newP = insertComponent('p', block, 'after');
            if (newP) {
                // Focus the newly inserted empty block
                newP.focus();
                // Treat it as if the user typed '/' to open the menu for replacement
                newP.innerText = '/';
                showSlashMenu(newP);
            }
        };
        
        const dragHandle = document.createElement('div');
        dragHandle.className = 'drag-handle';
        dragHandle.innerHTML = '⠿';
        dragHandle.draggable = true;
        dragHandle.title = 'ドラッグして移動';
        
        controls.appendChild(addBtn);
        controls.appendChild(dragHandle);
        
        const contentWrapper = document.createElement('div');
        contentWrapper.className = 'block-content';
        
        if (element.parentNode) {
            element.parentNode.insertBefore(block, element);
        }
        contentWrapper.appendChild(element);
        block.appendChild(controls);
        block.appendChild(contentWrapper);

        setupDragAndDrop(block);
        
        return block;
    }

    function initBlocks() {
        const container = document.getElementById('w-editor-content');
        const children = Array.from(container.children);
        children.forEach(child => {
            if (!child.classList.contains('editor-block') && child.tagName !== 'SCRIPT' && child.tagName !== 'STYLE') {
                wrapInBlock(child);
            }
        });
    }

    // ── 汎用表エディタ: 行・列操作ツールバー（docs/【考察】語彙モデル.md §5.2） ──
    // セルの値はブロックの contenteditable がそのまま担い（値＝中身＝表示される文字）、
    // 行・列の追加・削除・並べ替えをこのツールバーで補う。ボタンを表の中へ挿すと
    // シリアライザが中身を本文として拾ってしまうため、クロームは本文DOMの外
    // （#w-table-toolbar・シェル直下）に置き、キャレットのある行に追従させる。
    let currentTableRow = null;
    let currentTableCell = null; // 列操作・列設定の対象列は「キャレットのあるセル」から決める

    // insideCustomElement は el がカスタム要素の描画内（m-required-materials の
    // 集計表など）にあるかを返す。カスタム要素内の表は自前UIの領分なので触らない。
    function insideCustomElement(el) {
        for (let p = el; p && p.id !== 'editor-content'; p = p.parentElement) {
            if (p.tagName && p.tagName.indexOf('-') !== -1) return true;
        }
        return false;
    }

    function hideTableToolbar() {
        const bar = document.getElementById('w-table-toolbar');
        if (bar) bar.classList.remove('active');
        currentTableRow = null;
        currentTableCell = null;
        hideColPopover();
    }

    // updateTableToolbar はキャレットが本文中の表の行にあるときだけツールバーを出す。
    function updateTableToolbar() {
        const bar = document.getElementById('w-table-toolbar');
        if (!bar) return;
        if (!document.body.hasAttribute('edit-mode')) { hideTableToolbar(); return; }

        const sel = window.getSelection();
        const node = sel && sel.anchorNode;
        const el = node ? (node.nodeType === Node.TEXT_NODE ? node.parentElement : node) : null;
        const row = el && el.closest ? el.closest('#w-editor-content tr') : null;
        if (!row || insideCustomElement(row)) { hideTableToolbar(); return; }

        currentTableRow = row;
        currentTableCell = el && el.closest ? el.closest('td, th') : null;
        bar.classList.add('active');

        // 行の右横へ。入りきらなければ行の左上へ退避する。
        const rect = row.getBoundingClientRect();
        let top = rect.top + window.scrollY + (rect.height - bar.offsetHeight) / 2;
        let left = rect.right + window.scrollX + 8;
        if (rect.right + 8 + bar.offsetWidth > document.documentElement.clientWidth) {
            top = rect.top + window.scrollY - bar.offsetHeight - 4;
            left = rect.left + window.scrollX;
        }
        bar.style.top = top + 'px';
        bar.style.left = left + 'px';
    }

    // rowCellCount は行の直接の子セル（th / td）の数を返す。
    function rowCellCount(row) {
        let n = 0;
        Array.from(row.children).forEach(c => {
            if (c.tagName === 'TH' || c.tagName === 'TD') n++;
        });
        return n;
    }

    // tableRowAdd は現在行の下へ空のデータ行（td）を追加する。
    // 見出し行（th）の上から使えば最初のデータ行になる。
    function tableRowAdd() {
        const row = currentTableRow;
        if (!row) return;
        const cells = rowCellCount(row) || 1;
        const tr = document.createElement('tr');
        for (let i = 0; i < cells; i++) tr.appendChild(document.createElement('td'));
        row.parentNode.insertBefore(tr, row.nextSibling);
        focusFirstCell(tr);
        updateHtmlPreview();
    }

    // tableRowDelete は現在行を削除する。最初の行（見出し行＝列の鍵と型を運ぶ）は
    // 消さない——形式のスキーマは文書自身が携帯するため（語彙モデル §5.1）。
    function tableRowDelete() {
        const row = currentTableRow;
        if (!row) return;
        const table = row.closest('table');
        if (!table || row === table.querySelector('tr')) return;
        const neighbor = row.previousElementSibling || row.nextElementSibling;
        row.remove();
        if (neighbor && neighbor.tagName === 'TR') focusFirstCell(neighbor);
        else hideTableToolbar();
        updateHtmlPreview();
    }

    // placeCaretAtEnd はセル（td / th）の末尾へキャレットを置く。
    function placeCaretAtEnd(cell) {
        if (!cell) return;
        const range = document.createRange();
        range.selectNodeContents(cell);
        range.collapse(false);
        const sel = window.getSelection();
        sel.removeAllRanges();
        sel.addRange(range);
    }

    // tableRowMove は現在行を上下の行と入れ替える。見出し行は動かさず、
    // データ行が見出しより上へ出ることも許さない。
    function tableRowMove(dir) {
        const row = currentTableRow;
        if (!row) return;
        const table = row.closest('table');
        if (!table) return;
        const first = table.querySelector('tr');
        if (row === first) return; // 見出し行は動かさない

        // insertBefore でノードを動かすとブラウザがキャレットを失い、直後の
        // selectionchange でツールバーまで消えてしまう。移動前にキャレットのある
        // セルを覚えておき、移動後に同じセルへ戻す。
        const sel = window.getSelection();
        const anchorEl = sel && sel.anchorNode
            ? (sel.anchorNode.nodeType === Node.TEXT_NODE ? sel.anchorNode.parentElement : sel.anchorNode)
            : null;
        const cell = anchorEl && anchorEl.closest ? anchorEl.closest('td, th') : null;

        if (dir < 0) {
            const prev = row.previousElementSibling;
            if (!prev || prev.tagName !== 'TR' || prev === first) return;
            row.parentNode.insertBefore(row, prev);
        } else {
            const next = row.nextElementSibling;
            if (!next || next.tagName !== 'TR') return;
            row.parentNode.insertBefore(next, row);
        }
        placeCaretAtEnd(cell && row.contains(cell) ? cell : row.querySelector('td, th'));
        updateTableToolbar(); // 移動後の位置へ追従させる
        updateHtmlPreview();
    }

    // ── 列操作（追加・削除・左右移動）─────────────────────────────────
    // 列は全行（見出し行＋データ行）に同時に効く。対象列はキャレットのあるセルの位置。
    // colspan は骨格生成が作らないため考慮しない（セル数が足りない行はその行だけスキップ）。

    // colIndexOf はセルが行の何番目（th/td だけを数えた位置）かを返す。
    function colIndexOf(cell) {
        if (!cell) return -1;
        let i = 0;
        for (let c = cell.previousElementSibling; c; c = c.previousElementSibling) {
            if (c.tagName === 'TH' || c.tagName === 'TD') i++;
        }
        return i;
    }

    // cellAt は行の idx 番目のセル（th/td）を返す。
    function cellAt(row, idx) {
        let i = 0;
        for (const c of row.children) {
            if (c.tagName !== 'TH' && c.tagName !== 'TD') continue;
            if (i === idx) return c;
            i++;
        }
        return null;
    }

    // tableColAdd は現在列の右へ空の列を追加する（見出し行は th、データ行は td）。
    function tableColAdd() {
        const cell = currentTableCell;
        const table = cell && cell.closest('table');
        if (!table) return;
        const idx = colIndexOf(cell);
        const headerRow = table.rows[0];
        Array.from(table.rows).forEach(row => {
            const at = cellAt(row, idx);
            if (!at) return;
            const el = document.createElement(row === headerRow ? 'th' : 'td');
            at.after(el);
        });
        placeCaretAtEnd(cellAt(headerRow, idx + 1)); // 追加した列の見出しから入力させる
        updateTableToolbar();
        updateHtmlPreview();
    }

    // tableColDelete は現在列を全行から削除する。最後の1列は消さない
    // （表の骨格を保つ。行側の「見出し行は消さない」と同じ理由）。
    function tableColDelete() {
        const cell = currentTableCell;
        const table = cell && cell.closest('table');
        if (!table) return;
        if (rowCellCount(table.rows[0]) <= 1) return;
        const idx = colIndexOf(cell);
        Array.from(table.rows).forEach(row => {
            const at = cellAt(row, idx);
            if (at) at.remove();
        });
        const fallback = cellAt(currentTableRow || table.rows[0], Math.max(0, idx - 1));
        placeCaretAtEnd(fallback || table.querySelector('td, th'));
        updateTableToolbar();
        updateHtmlPreview();
    }

    // tableColMove は現在列を左右の列と入れ替える（全行で）。
    function tableColMove(dir) {
        const cell = currentTableCell;
        const table = cell && cell.closest('table');
        if (!table) return;
        const idx = colIndexOf(cell);
        const dst = idx + dir;
        if (dst < 0 || dst >= rowCellCount(table.rows[0])) return;
        Array.from(table.rows).forEach(row => {
            const a = cellAt(row, idx);
            const b = cellAt(row, dst);
            if (!a || !b) return;
            if (dir < 0) b.before(a);
            else b.after(a);
        });
        placeCaretAtEnd(cell); // 動かした列のセルへキャレットを戻す（行移動と同じ理由）
        updateTableToolbar();
        updateHtmlPreview();
    }

    // ── 列設定ポップオーバ（属性側の編集口。語彙モデル §5.2「編集の2面」）──────
    // 値（中身・表示される）はセルの contenteditable で、役割・型（属性・表示されない）は
    // ここで編集する。contenteditable はテキストしか触れないため、属性の編集口は別に要る。
    // v1 で編集できるのは列型の明示（th の data-type）だけ。列の鍵は見出しの表示文字
    // そのものなので、鍵の編集口は要らない（見出しを書き換えることが鍵の変更にあたる）。
    let colPopoverTh = null; // 設定対象の見出しセル

    function hideColPopover() {
        const pop = document.getElementById('w-col-popover');
        if (pop) pop.classList.remove('active');
        colPopoverTh = null;
    }

    // openColPopover は現在列の見出し（th）に対する設定を開く。
    function openColPopover() {
        const pop = document.getElementById('w-col-popover');
        const cell = currentTableCell;
        const table = cell && cell.closest('table');
        if (!pop || !table) return;
        const th = cellAt(table.rows[0], colIndexOf(cell));
        if (!th) return;
        colPopoverTh = th;

        // 鍵（＝見出しの表示文字）と現在の型を表示する
        const key = th.textContent.trim() || '（見出し未入力）';
        document.getElementById('w-cp-key').textContent = key;
        const sel = document.getElementById('w-cp-type');
        sel.value = th.getAttribute('data-type') || '';
        refreshColPopoverNote(th, key);

        pop.classList.add('active');
        const rect = th.getBoundingClientRect();
        pop.style.top = (rect.bottom + window.scrollY + 4) + 'px';
        pop.style.left = (rect.left + window.scrollX) + 'px';
    }

    // refreshColPopoverNote は「明示しない場合に効く型」（レジストリ宣言 or 推論辞書）を示す。
    function refreshColPopoverNote(th, key) {
        const note = document.getElementById('w-cp-note');
        const table = th.closest('table');
        const def = vocabDefs.find(v => v.type === (table && table.getAttribute('data-type')));
        const col = def && (def.columns || []).find(c => (c.field && c.field === key) || c.label === key);
        if (col) note.textContent = '未指定ならレジストリ宣言: ' + col.type;
        else if (typeInferenceDict[key]) note.textContent = '未指定なら推論: ' + typeInferenceDict[key];
        else note.textContent = '未指定なら text 扱い';
    }

    // applyColType は選択された型を th の data-type へ反映する（空＝属性を外し推論に戻す）。
    // 属性の変更は MutationObserver の attributeFilter に載っていないため、保存は明示的に蹴る。
    function applyColType() {
        const th = colPopoverTh;
        if (!th) return;
        const v = document.getElementById('w-cp-type').value;
        if (v) th.setAttribute('data-type', v);
        else th.removeAttribute('data-type');
        validateTypedTables();
        updateHtmlPreview();
        triggerAutoSave();
    }

    // ── 定義リスト（dl data-type）の項目操作 ─────────────────────────────
    // 「項目」＝ dt と、次の dt までの dd の組（多値＝複数 dd。語彙モデル §5.3）。
    let currentDlNode = null; // キャレットのある dt / dd

    function hideDlToolbar() {
        const bar = document.getElementById('w-dl-toolbar');
        if (bar) bar.classList.remove('active');
        currentDlNode = null;
    }

    // updateDlToolbar はキャレットが本文中の <dl data-type> の dt/dd にあるときだけ出す。
    function updateDlToolbar() {
        const bar = document.getElementById('w-dl-toolbar');
        if (!bar) return;
        if (!document.body.hasAttribute('edit-mode')) { hideDlToolbar(); return; }

        const sel = window.getSelection();
        const node = sel && sel.anchorNode;
        const el = node ? (node.nodeType === Node.TEXT_NODE ? node.parentElement : node) : null;
        const item = el && el.closest ? el.closest('#w-editor-content dl[data-type] > dt, #w-editor-content dl[data-type] > dd') : null;
        if (!item || insideCustomElement(item)) { hideDlToolbar(); return; }

        currentDlNode = item;
        bar.classList.add('active');
        const rect = item.getBoundingClientRect();
        let top = rect.top + window.scrollY + (rect.height - bar.offsetHeight) / 2;
        let left = rect.right + window.scrollX + 8;
        if (rect.right + 8 + bar.offsetWidth > document.documentElement.clientWidth) {
            top = rect.top + window.scrollY - bar.offsetHeight - 4;
            left = rect.left + window.scrollX;
        }
        bar.style.top = top + 'px';
        bar.style.left = left + 'px';
    }

    // dlGroupOf は dt/dd が属する項目（dt ＋ 後続の dd 列）を返す。
    function dlGroupOf(node) {
        let dt = node.tagName === 'DT' ? node : null;
        if (!dt) {
            for (let p = node.previousElementSibling; p; p = p.previousElementSibling) {
                if (p.tagName === 'DT') { dt = p; break; }
            }
        }
        if (!dt) return null;
        const dds = [];
        for (let n = dt.nextElementSibling; n && n.tagName === 'DD'; n = n.nextElementSibling) {
            dds.push(n);
        }
        return { dt, dds, nodes: [dt].concat(dds) };
    }

    // emptyEditable は空の dt/dd を作る。<br> はキャレットの足場（contenteditable の定石）——
    // 空の dt/dd にはキャレットが実際には入れず、Chrome は**次の要素の先頭**へ文字を挿入して
    // しまう（「＋項目」直後に打った名前が隣の項目へ混入する実バグをE2Eで検出した）。
    // 表のセル（td/th）はブラウザがキャレット容器として特別扱いするため足場は不要。
    // <br> は入力を始めるとブラウザが取り除き、未入力のまま保存されても無害（許可要素）。
    function emptyEditable(tag) {
        const el = document.createElement(tag);
        el.appendChild(document.createElement('br'));
        return el;
    }

    // dlItemAdd は現在の項目の下へ新しい項目（dt＋dd）を追加する。
    function dlItemAdd() {
        const group = currentDlNode && dlGroupOf(currentDlNode);
        if (!group) return;
        const dt = emptyEditable('dt');
        const dd = emptyEditable('dd');
        const last = group.nodes[group.nodes.length - 1];
        last.after(dt, dd);
        placeCaretAtEnd(dt); // 名前から入力させる
        updateDlToolbar();
        updateHtmlPreview();
    }

    // dlValueAdd は現在の項目へ値（dd）をもう1つ足す（多値。語彙モデル §5.3）。
    function dlValueAdd() {
        const group = currentDlNode && dlGroupOf(currentDlNode);
        if (!group) return;
        const dd = emptyEditable('dd');
        group.nodes[group.nodes.length - 1].after(dd);
        placeCaretAtEnd(dd);
        updateDlToolbar();
        updateHtmlPreview();
    }

    // dlItemMove は現在の項目を上下の項目と入れ替える。
    function dlItemMove(dir) {
        const group = currentDlNode && dlGroupOf(currentDlNode);
        if (!group) return;
        const caretNode = currentDlNode;
        if (dir < 0) {
            // 直前の兄弟（前の項目の dd か dt）から、その項目のグループを引く
            const prevSibling = group.dt.previousElementSibling;
            if (!prevSibling) return;
            const prev = dlGroupOf(prevSibling);
            if (!prev || prev.dt === group.dt) return;
            prev.dt.before(...group.nodes);
        } else {
            const nextDt = group.nodes[group.nodes.length - 1].nextElementSibling;
            if (!nextDt || nextDt.tagName !== 'DT') return;
            const next = dlGroupOf(nextDt);
            next.nodes[next.nodes.length - 1].after(...group.nodes);
        }
        placeCaretAtEnd(caretNode); // 行移動と同じ理由（キャレット喪失→ツールバー消失）
        updateDlToolbar();
        updateHtmlPreview();
    }

    // dlItemDelete は現在の項目（dt＋dd）を削除する。最後の1項目は消さない
    // （空の dl はキャレットの置き場が無く項目を足せなくなる＝骨格を保つ）。
    function dlItemDelete() {
        const group = currentDlNode && dlGroupOf(currentDlNode);
        if (!group) return;
        const dl = group.dt.parentElement;
        if (dl.querySelectorAll(':scope > dt').length <= 1) return;
        const neighbor = group.dt.previousElementSibling || group.nodes[group.nodes.length - 1].nextElementSibling;
        group.nodes.forEach(n => n.remove());
        if (neighbor && (neighbor.tagName === 'DT' || neighbor.tagName === 'DD')) placeCaretAtEnd(neighbor);
        else hideDlToolbar();
        updateHtmlPreview();
    }

    // ── 型検証（通知のみ・拒否しない）と enum の入力補助（語彙モデル §5.1・§5.2）──
    // 列型の決定順序はサーバー（vocab_index.go の resolveColumnType）と同じ:
    // findVocabColumn は形式定義から鍵に対応する列を探す。サーバー側の columnFor
    // （internal/cms/vocab.go）と**同じ規則**で、Label でも Field でも引ける
    // （本文が運ぶ鍵は見出しの表示文字＝Label 側だが、レジストリ由来の機械キーでも
    // 引けるようにしておくと、AI応答など機械キー起点の照合が同じ関数で済む）。
    function findVocabColumn(def, key) {
        if (!def || !key) return null;
        return (def.columns || []).find(c => (c.field && c.field === key) || c.label === key) || null;
    }

    // th の data-type 明示 > レジストリ宣言 > 推論辞書（/api/tag-schema） > text。
    const VALID_COL_TYPES = ['text', 'number', 'date', 'enum', 'image'];

    // resolveCellColumn はセルの属する列の {type, enum} を解決する（データ行の td 用）。
    function resolveCellColumn(cell) {
        const table = cell.closest('table[data-type]');
        if (!table || !table.rows.length) return null;
        const th = cellAt(table.rows[0], colIndexOf(cell));
        if (!th) return null;
        const key = th.textContent.trim();
        const explicit = th.getAttribute('data-type');
        const def = vocabDefs.find(v => v.type === table.getAttribute('data-type'));
        const col = findVocabColumn(def, key);
        let type = 'text';
        if (explicit && VALID_COL_TYPES.indexOf(explicit) !== -1) type = explicit;
        else if (col) type = col.type;
        else if (typeInferenceDict[key]) type = typeInferenceDict[key];
        return { type, enum: (col && col.enum) || [] };
    }

    // 値の解釈可否。サーバーの NormalizeValue と同じ規則（全角→半角・通貨記号と
    // 桁区切りの除去・YYYY-M-D／YYYY/M/D／YYYY年M月D日）で判定する。
    function toHalfWidthJS(s) {
        return s.replace(/[０-９]/g, c => String.fromCharCode(c.charCodeAt(0) - '０'.charCodeAt(0) + '0'.charCodeAt(0)))
            .replace(/．/g, '.').replace(/／/g, '/').replace(/[－ー−]/g, '-');
    }

    function isValidNumberText(raw) {
        let s = toHalfWidthJS(raw.trim()).replace(/[¥￥$,，\s　]/g, '');
        s = s.replace(/円$/, '');
        return /^-?[0-9]+(\.[0-9]+)?$/.test(s);
    }

    function isValidDateText(raw) {
        const m = toHalfWidthJS(raw.trim()).match(/^([0-9]{4})[-/年]([0-9]{1,2})[-/月]([0-9]{1,2})日?$/);
        if (!m) return false;
        const y = +m[1], mo = +m[2], d = +m[3];
        const dt = new Date(y, mo - 1, d);
        return dt.getFullYear() === y && dt.getMonth() === mo - 1 && dt.getDate() === d;
    }

    // validateCell は number / date 列のセルが解釈できない値のとき .cell-invalid を付ける。
    // class はシリアライザが書き出さないので、印は実行時だけ（保存されない）。
    function validateCell(cell) {
        const col = resolveCellColumn(cell);
        const text = cell.textContent.trim();
        let bad = false;
        if (col && text !== '') {
            if (col.type === 'number') bad = !isValidNumberText(text);
            else if (col.type === 'date') bad = !isValidDateText(text);
        }
        cell.classList.toggle('cell-invalid', bad);
    }

    // validateTypedTables は本文中の全 <table data-type> のデータセルを検証し直す。
    // 編集モード入り・列型の変更・サニタイズ反映のあとに呼ぶ。
    function validateTypedTables() {
        document.querySelectorAll('#w-editor-content table[data-type]').forEach(table => {
            if (insideCustomElement(table)) return;
            Array.from(table.rows).slice(1).forEach(row => {
                Array.from(row.children).forEach(c => {
                    if (c.tagName === 'TD') validateCell(c);
                });
            });
        });
    }

    // ── enum 列の入力補助（選択肢メニュー）────────────────────────────────
    // 選択肢はレジストリ宣言から取る（原則3: レジストリがあれば向上・無くても編集可能）。
    let enumMenuCell = null;

    function hideEnumMenu() {
        const menu = document.getElementById('w-enum-menu');
        if (menu) menu.classList.remove('active');
        enumMenuCell = null;
    }

    // updateEnumMenu はキャレットが enum 列のデータセルにあるとき選択肢を出す。
    function updateEnumMenu() {
        const menu = document.getElementById('w-enum-menu');
        if (!menu) return;
        if (!document.body.hasAttribute('edit-mode')) { hideEnumMenu(); return; }

        const sel = window.getSelection();
        const node = sel && sel.anchorNode;
        const el = node ? (node.nodeType === Node.TEXT_NODE ? node.parentElement : node) : null;
        const cell = el && el.closest ? el.closest('#w-editor-content table[data-type] td') : null;
        if (!cell || insideCustomElement(cell)) { hideEnumMenu(); return; }
        const col = resolveCellColumn(cell);
        if (!col || col.type !== 'enum' || !col.enum.length) { hideEnumMenu(); return; }
        if (cell === enumMenuCell) return; // 同じセル内のキャレット移動では作り直さない

        enumMenuCell = cell;
        menu.textContent = ''; // 前のセルの選択肢を消す
        col.enum.forEach(v => {
            const b = document.createElement('button');
            b.type = 'button';
            b.textContent = v;
            b.addEventListener('click', () => {
                cell.textContent = v;      // childList の変化で MutationObserver → 自動保存が走る
                validateCell(cell);
                placeCaretAtEnd(cell);
                hideEnumMenu();
            });
            menu.appendChild(b);
        });
        menu.classList.add('active');
        const rect = cell.getBoundingClientRect();
        menu.style.top = (rect.bottom + window.scrollY + 2) + 'px';
        menu.style.left = (rect.left + window.scrollX) + 'px';
    }

    // ── ファイル容器のエンハンサ（語彙モデル §3: マーカー要素へ振る舞いを配線） ──
    // <section data-type="file"> に PDF プレビュー（閲覧・編集）と、編集モードの
    // ドロップゾーン・明細AI解析ボタンを付ける。挿すのはすべて .vocab-chrome
    // （シリアライザが保存しない編集クローム）。呼び出しは applyMode・populateEditor・
    // 挿入直後の3点（エンハンスパスの規律。作り直し方式なので冪等）。
    function enhanceFileSections() {
        document.querySelectorAll('#w-editor-content section[data-type="file"]').forEach(sec => {
            sec.querySelectorAll(':scope > .vocab-chrome').forEach(el => el.remove());
            const src = sec.getAttribute('data-src') || '';
            const isEdit = document.body.hasAttribute('edit-mode');

            if (src && src.toLowerCase().endsWith('.pdf') && currentPageId) {
                const embed = document.createElement('embed');
                embed.className = 'vocab-chrome file-preview';
                embed.src = '/data/master/' + currentPageId.substring(0, 2) + '/' + currentPageId + '/' + src;
                embed.type = 'application/pdf';
                sec.appendChild(embed);
            }
            if (!isEdit) return;

            const zone = document.createElement('div');
            zone.className = 'vocab-chrome pdf-drop-zone';
            zone.contentEditable = 'false';
            zone.textContent = src ? '📎 PDFを差し替えるにはここへドロップ' : '📎 PDFをここへドロップして添付';
            zone.addEventListener('dragover', e => { e.preventDefault(); zone.classList.add('is-dragover'); });
            zone.addEventListener('dragleave', () => zone.classList.remove('is-dragover'));
            zone.addEventListener('drop', e => {
                e.preventDefault();
                zone.classList.remove('is-dragover');
                const f = e.dataTransfer.files[0];
                const isPDF = f && (f.type === 'application/pdf' || f.name.toLowerCase().endsWith('.pdf'));
                if (!isPDF) { notify('PDFファイルをドロップしてください。', { type: 'warn', duration: 5000 }); return; }
                uploadPDFToSection(sec, f);
            });
            sec.appendChild(zone);

            if (src && sec.querySelector('section[data-type="client-order"], section[data-type="our-order"]')) {
                const btn = document.createElement('button');
                btn.type = 'button';
                btn.className = 'vocab-chrome parse-pdf-btn';
                btn.textContent = '🤖 PDFから明細を解析';
                btn.addEventListener('click', () => parsePDFIntoSection(sec, src));
                sec.appendChild(btn);
            }
        });
    }

    // uploadPDFToSection はPDFを添付し、配線（data-src）と可視のファイル名リンクを更新する。
    async function uploadPDFToSection(sec, file) {
        if (!currentPageId) { notify('先にページを保存してください。', { type: 'warn', duration: 5000 }); return; }
        const fd = new FormData();
        fd.append('page_id', currentPageId);
        fd.append('pdf_file', file);
        try {
            const res = await fetch('/api/upload-pdf', { method: 'POST', body: fd });
            const d = await res.json().catch(() => ({}));
            if (!res.ok || !d.success) throw new Error(d.message || res.status);
            sec.setAttribute('data-src', d.src);
            // 可視のファイル名リンク（中身＝正本に保存される）を先頭に置き直す
            let p = sec.querySelector(':scope > p');
            if (!p || !p.querySelector('a')) {
                p = document.createElement('p');
                sec.insertBefore(p, sec.firstChild);
            }
            p.textContent = '📎 ';
            const a = document.createElement('a');
            a.href = '/data/master/' + currentPageId.substring(0, 2) + '/' + currentPageId + '/' + d.src;
            a.textContent = file.name;
            p.appendChild(a);
            enhanceFileSections();
            updateHtmlPreview();
            notify('PDFを添付しました。', { type: 'success', duration: 3000 });
        } catch (e) {
            notify('PDFのアップロードに失敗しました: ' + e.message, { type: 'warn', duration: 8000 });
        }
    }

    // parsePDFIntoSection はPDFをAI解析し、容器内の受発注ブロックの明細表へ行を足す。
    async function parsePDFIntoSection(sec, src) {
        notify('PDFを解析しています…', { type: 'info', duration: 0, id: 'parse-pdf' });
        try {
            const res = await fetch('/api/parse-pdf', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ page_id: currentPageId, file_name: src })
            });
            const d = await res.json().catch(() => ({}));
            dismissToast('parse-pdf');
            if (!d.success || !Array.isArray(d.items)) {
                notify('解析に失敗しました: ' + (d.message || res.status), { type: 'warn', duration: 10000 });
                return;
            }
            const order = sec.querySelector('section[data-type="client-order"], section[data-type="our-order"]');
            const table = order && order.querySelector('table[data-type]');
            if (!table) return;
            // 見出しの表示文字をレジストリ経由で機械キーへ解決する（AI応答の鍵は機械キー）。
            const itemsDef = vocabDefs.find(v => v.type === table.getAttribute('data-type'));
            const fields = Array.from(table.querySelectorAll('tr:first-child th'))
                .map(th => (findVocabColumn(itemsDef, th.textContent.trim()) || {}).field || '');
            const tbody = table.querySelector('tbody') || table;
            d.items.forEach(it => {
                const tr = document.createElement('tr');
                fields.forEach(f => {
                    const td = document.createElement('td');
                    if (f === 'item-name') td.textContent = it.item_name || '';
                    else if (f === 'price' || f === 'cost') td.textContent = it.price || '';
                    else if (f === 'quantity') td.textContent = it.quantity || '';
                    tr.appendChild(td);
                });
                tbody.appendChild(tr);
            });
            updateHtmlPreview();
            notify(d.items.length + ' 件の明細を追加しました。内容を確認してください。', { type: 'success', duration: 5000 });
        } catch (e) {
            dismissToast('parse-pdf');
            notify('解析に失敗しました: ' + e.message, { type: 'warn', duration: 10000 });
        }
    }

    // ── スラッシュメニューのレジストリ駆動項目 ──────────────────────────
    // 語彙レジストリ（①）の形式定義から項目を生成して静的項目の後ろへ足す。
    // メニュー全体の再設計（絞り込み・分類・頻度順）は別課題（エディタ仕様 §3）で、
    // ここは「殻の markup を手で足さずに語彙を増やせる」ことだけを実現する。
    function populateSlashMenuVocab() {
        const menu = document.getElementById('w-slash-menu');
        if (!menu) return;
        vocabDefs.forEach(def => {
            if (def.hidden) return; // 明細表など、単独では挿入しない形式
            const item = document.createElement('div');
            item.className = 'slash-menu-item';
            item.setAttribute('data-type', 'vocab:' + def.type);
            const icon = document.createElement('b');
            icon.textContent = def.icon || '📄';
            item.appendChild(icon);
            item.appendChild(document.createTextNode(' ' + (def.display_name || def.type)));
            menu.appendChild(item);
        });
    }

    // Slash Menu Logic
    function showSlashMenu(targetElement) {
        slashMenuVisible = true;
        const menu = document.getElementById('w-slash-menu');
        currentSlashBlock = targetElement.closest('.editor-block');
        menu.classList.add('active');
        
        const rect = targetElement.getBoundingClientRect();
        menu.style.top = (rect.bottom + window.scrollY + 5) + 'px';
        menu.style.left = (rect.left + window.scrollX) + 'px';

        slashSelectedIndex = 0;
        updateSlashMenuSelection();
    }

    function hideSlashMenu() {
        slashMenuVisible = false;
        const menu = document.getElementById('w-slash-menu');
        if (menu) menu.classList.remove('active');
        currentSlashBlock = null;
    }

    function updateSlashMenuSelection() {
        const items = document.querySelectorAll('#w-slash-menu .slash-menu-item');
        items.forEach((item, idx) => {
            if (idx === slashSelectedIndex) {
                item.classList.add('selected');
                item.scrollIntoView({ block: 'nearest' });
            } else {
                item.classList.remove('selected');
            }
        });
    }

    function handleSlashMenuKey(e) {
        const items = document.querySelectorAll('#w-slash-menu .slash-menu-item');
        if (e.key === 'ArrowDown') {
            e.preventDefault();
            slashSelectedIndex = (slashSelectedIndex + 1) % items.length;
            updateSlashMenuSelection();
        } else if (e.key === 'ArrowUp') {
            e.preventDefault();
            slashSelectedIndex = (slashSelectedIndex - 1 + items.length) % items.length;
            updateSlashMenuSelection();
        } else if (e.key === 'Enter') {
            e.preventDefault();
            const type = items[slashSelectedIndex].getAttribute('data-type');
            if (currentSlashBlock) {
                replaceBlockWithComponent(type, currentSlashBlock);
            }
            hideSlashMenu();
        } else if (e.key === 'Escape') {
            hideSlashMenu();
        }
    }

    function setupEditorEvents() {
        const editor = document.getElementById('w-editor-content');

        // レジストリ由来の項目は、下の項目バインド（click / mouseenter）より前に足す。
        populateSlashMenuVocab();

        // 行操作ツールバーのボタン。mousedown を止めないとクリックで選択が崩れ、
        // どの行への操作か（currentTableRow）が失われる。
        const tbar = document.getElementById('w-table-toolbar');
        if (tbar) {
            tbar.addEventListener('mousedown', e => e.preventDefault());
            document.getElementById('w-tt-add').addEventListener('click', tableRowAdd);
            document.getElementById('w-tt-del').addEventListener('click', tableRowDelete);
            document.getElementById('w-tt-up').addEventListener('click', () => tableRowMove(-1));
            document.getElementById('w-tt-down').addEventListener('click', () => tableRowMove(1));
            document.getElementById('w-tt-col-add').addEventListener('click', tableColAdd);
            document.getElementById('w-tt-col-del').addEventListener('click', tableColDelete);
            document.getElementById('w-tt-col-left').addEventListener('click', () => tableColMove(-1));
            document.getElementById('w-tt-col-right').addEventListener('click', () => tableColMove(1));
            document.getElementById('w-tt-col-cfg').addEventListener('click', openColPopover);
        }

        // dl の項目操作ツールバー（表と同じく mousedown を止めて選択を守る）
        const dbar = document.getElementById('w-dl-toolbar');
        if (dbar) {
            dbar.addEventListener('mousedown', e => e.preventDefault());
            document.getElementById('w-dt-add').addEventListener('click', dlItemAdd);
            document.getElementById('w-dt-val').addEventListener('click', dlValueAdd);
            document.getElementById('w-dt-up').addEventListener('click', () => dlItemMove(-1));
            document.getElementById('w-dt-down').addEventListener('click', () => dlItemMove(1));
            document.getElementById('w-dt-del').addEventListener('click', dlItemDelete);
        }

        // 列設定ポップオーバ。select の操作にはフォーカスが要るため mousedown は止めない
        // （かわりに selectionchange 側でポップオーバ操作中の消灯を抑止する）。
        const cpType = document.getElementById('w-cp-type');
        if (cpType) cpType.addEventListener('change', applyColType);

        // enum の選択肢メニュー。クリックで選択が飛ぶと対象セルを見失うため mousedown を止める。
        const emenu = document.getElementById('w-enum-menu');
        if (emenu) emenu.addEventListener('mousedown', e => e.preventDefault());

        editor.addEventListener('input', (e) => {
            if (!document.body.hasAttribute('edit-mode')) return;
            const target = e.target;
            if (target.tagName === 'P' && target.innerText === '/') {
                showSlashMenu(target);
            } else {
                hideSlashMenu();
            }

            // 型検証（通知のみ）。編集中のセルだけを見る（全表の再検証は入力ごとには重い）。
            const sel = window.getSelection();
            const node = sel && sel.anchorNode;
            const el = node ? (node.nodeType === Node.TEXT_NODE ? node.parentElement : node) : null;
            const cell = el && el.closest ? el.closest('#w-editor-content table[data-type] td') : null;
            if (cell && !insideCustomElement(cell)) validateCell(cell);
        });

        editor.addEventListener('keydown', (e) => {
            if (!document.body.hasAttribute('edit-mode')) return;

            // アンドゥ・リドゥは独自スタックに一本化する。preventDefault しないと
            // ブラウザ標準の undo と二重スタックになり、DOM操作を戻せずに壊れる
            // （docs/【考察】アンドゥ・リドゥ.md §3「必須」）。
            // カスタム要素内の入力欄からも効かせたいので、下の絞り込みより前に処理する。
            if ((e.ctrlKey || e.metaKey) && !e.altKey) {
                const key = e.key.toLowerCase();
                if (key === 'z' && !e.shiftKey) { e.preventDefault(); undo(); return; }
                if (key === 'y' || (key === 'z' && e.shiftKey)) { e.preventDefault(); redo(); return; }

                // Ctrl+B/I/U はブラウザ標準だと execCommand が走り、実装ごとに違うHTML
                // （<b> か style="font-weight:bold" か）を吐く。必ず横取りして自前実装へ回す。
                const fmt = { b: 'bold', i: 'italic', u: 'underline' }[key];
                if (fmt && !e.shiftKey) { e.preventDefault(); toggleInlineFormat(fmt); return; }
            }

            const target = e.target;
            if (target.tagName !== 'P' && target.tagName !== 'H1' && target.tagName !== 'H2' && target.tagName !== 'H3') {
                return; // Ignore inputs inside web components
            }

            const block = target.closest('.editor-block');
            if (!block) return;

            if (slashMenuVisible) {
                if (['ArrowUp', 'ArrowDown', 'Enter', 'Escape'].includes(e.key)) {
                    handleSlashMenuKey(e);
                    return;
                }
            }

            if (e.key === 'Enter' && !e.shiftKey) {
                e.preventDefault();
                insertComponent('p', block, 'after');
            } else if (e.key === 'Backspace' && target.innerText === '') {
                e.preventDefault();
                const prevBlock = block.previousElementSibling;
                if (prevBlock && prevBlock.classList.contains('editor-block')) {
                    const content = prevBlock.querySelector('.block-content > *');
                    if (content && content.focus) {
                        content.focus();
                        const selection = window.getSelection();
                        const range = document.createRange();
                        range.selectNodeContents(content);
                        range.collapse(false); // End of content
                        selection.removeAllRanges();
                        selection.addRange(range);
                    }
                }
                block.remove();
                updateHtmlPreview();
            } else if (e.key === 'ArrowUp') {
                const prevBlock = block.previousElementSibling;
                if (prevBlock && prevBlock.classList.contains('editor-block')) {
                    const content = prevBlock.querySelector('.block-content > *');
                    if (content && content.focus) content.focus();
                }
            } else if (e.key === 'ArrowDown') {
                const nextBlock = block.nextElementSibling;
                if (nextBlock && nextBlock.classList.contains('editor-block')) {
                    const content = nextBlock.querySelector('.block-content > *');
                    if (content && content.focus) content.focus();
                }
            }
        });

        document.addEventListener('click', (e) => {
            if (!e.target.closest('#w-slash-menu') && !e.target.closest('.editor-block')) {
                hideSlashMenu();
            }
        });

        document.addEventListener('selectionchange', () => {
            if (slashMenuVisible) return;
            // 列設定ポップオーバの操作中（select へのフォーカス移動）は、キャレットが
            // 表から出たと誤認してツールバーごと消してしまうため更新しない。
            if (document.activeElement && document.activeElement.closest &&
                document.activeElement.closest('#w-col-popover')) return;
            updateContextToolbar();
            updateTableToolbar();
            updateDlToolbar();
            updateEnumMenu();
        });

        // Hide toolbar when clicking outside editor
        document.addEventListener('mousedown', (e) => {
            if (!e.target.closest('#w-editor-content') && !e.target.closest('#w-context-toolbar')) {
                hideContextToolbar();
            }
            if (!e.target.closest('#w-editor-content') && !e.target.closest('#w-table-toolbar') &&
                !e.target.closest('#w-col-popover')) {
                hideTableToolbar();
            }
            if (!e.target.closest('#w-table-toolbar') && !e.target.closest('#w-col-popover')) {
                hideColPopover();
            }
            if (!e.target.closest('#w-editor-content') && !e.target.closest('#w-dl-toolbar')) {
                hideDlToolbar();
            }
            if (!e.target.closest('#w-editor-content') && !e.target.closest('#w-enum-menu')) {
                hideEnumMenu();
            }
        });

        const items = document.querySelectorAll('#w-slash-menu .slash-menu-item');
        items.forEach((item, idx) => {
            item.addEventListener('click', () => {
                const type = item.getAttribute('data-type');
                if (currentSlashBlock) {
                    replaceBlockWithComponent(type, currentSlashBlock);
                }
                hideSlashMenu();
            });
            item.addEventListener('mouseenter', () => {
                slashSelectedIndex = idx;
                updateSlashMenuSelection();
            });
        });
    }

    function updateContextToolbar() {
        if (!document.body.hasAttribute('edit-mode')) {
            hideContextToolbar();
            return;
        }

        const selection = window.getSelection();
        if (!selection.rangeCount) return;

        let targetNode = selection.anchorNode;
        if (!targetNode) return;
        
        const block = targetNode.nodeType === 3 ? targetNode.parentNode.closest('.editor-block') : (targetNode.closest ? targetNode.closest('.editor-block') : null);
        
        if (!block) {
            hideContextToolbar();
            return;
        }

        if (activeBlock !== block) {
            activeBlock = block;
            showContextToolbarForBlock(block);
        }
    }

    // ── インライン装飾（太字・斜体・下線）─────────────────────────────────
    // document.execCommand は仕様上 deprecated で、ブラウザによって生成されるHTMLが
    // 違う（<b> か <strong> か、あるいは style="font-weight:bold" か）。style 属性は
    // サニタイザが落とすので装飾が保存後に消える、という食い違いも起きていた。
    // ここでは Range を直接操作し、常に <strong>/<em>/<u> を出す。
    //
    // アンドゥ・リドゥは本文HTMLのスナップショット方式なので、この操作も
    // updateHtmlPreview() → pushHistory 経由でそのまま履歴に載る（特別扱い不要）。

    const INLINE_FORMATS = { bold: 'strong', italic: 'em', underline: 'u' };
    // 統合・除去の対象。装飾要素は入れ子や隣接が増えても表示は同じなので整理する。
    const INLINE_FORMAT_SELECTOR = 'strong,em,u,b,i,s,code,mark,small,sub,sup';

    // editableHostOf は、その節点を含む編集可能なブロックを返す（無ければ null）。
    function editableHostOf(node) {
        const el = node.nodeType === Node.TEXT_NODE ? node.parentElement : node;
        if (!el || !el.closest) return null;
        return el.closest('[contenteditable="true"]');
    }

    // splitAround は parent を child の前後で分割し、child を parent の兄弟へ引き上げる。
    // 「選択範囲だけ装飾を外す」ために必要な操作（<strong>ABC</strong> の B だけ外すなら
    // <strong>A</strong>B<strong>C</strong> にする）。
    function splitAround(parent, child) {
        const gp = parent.parentNode;
        if (!gp) return;
        let after = null;
        if (child.nextSibling) {
            after = parent.cloneNode(false);
            while (child.nextSibling) after.appendChild(child.nextSibling);
        }
        gp.insertBefore(child, parent.nextSibling);
        if (after) gp.insertBefore(after, child.nextSibling);
        if (!parent.firstChild) gp.removeChild(parent);
    }

    // liftOutOf は node を装飾要素 fmt の外へ出す（fmt は必要なだけ分割される）。
    function liftOutOf(node, fmt) {
        let cur = node;
        while (cur.parentNode && cur.parentNode !== fmt) {
            splitAround(cur.parentNode, cur);
        }
        if (cur.parentNode === fmt) splitAround(fmt, cur);
    }

    // selectedTextNodes は選択範囲に含まれるテキスト節点を返す。
    // 端が途中で切れている節点は splitText で割り、「範囲＝節点の集合」に揃える。
    function selectedTextNodes(range, host) {
        if (range.startContainer.nodeType === Node.TEXT_NODE && range.startOffset > 0) {
            const tail = range.startContainer.splitText(range.startOffset);
            range.setStart(tail, 0);
        }
        if (range.endContainer.nodeType === Node.TEXT_NODE && range.endOffset < range.endContainer.nodeValue.length) {
            range.endContainer.splitText(range.endOffset);
            range.setEnd(range.endContainer, range.endContainer.nodeValue.length);
        }
        const nodes = [];
        const walker = document.createTreeWalker(host, NodeFilter.SHOW_TEXT);
        while (walker.nextNode()) {
            const n = walker.currentNode;
            if (n.nodeValue !== '' && range.intersectsNode(n)) nodes.push(n);
        }
        // intersectsNode は境界に接するだけの節点も拾うので、実際に範囲内のものへ絞る
        return nodes.filter(n =>
            range.comparePoint(n, 0) >= 0 && range.comparePoint(n, n.nodeValue.length) <= 0);
    }

    // FORMAT_ORDER は装飾が入れ子になるときの外側→内側の順序。
    // 装飾は掛ける順番で <em><strong>x</strong></em> にも <strong><em>x</em></strong> にも
    // なるが表示は同じ。順序を一つに決めておくと、隣り合う装飾が同じ形になって統合でき、
    // 保存HTMLが操作履歴に依存して散らからない。
    const FORMAT_ORDER = ['strong', 'b', 'em', 'i', 'u', 's', 'mark', 'small', 'sub', 'sup', 'code'];

    // tidyInline は装飾要素の空・入れ子・隣接を整理する。
    // これをしないと、太字を付けては外すたびに <strong></strong> や
    // <strong>A</strong><strong>B</strong> が積もり、保存HTMLが無意味に膨らむ。
    function tidyInline(host) {
        // 何段か整理すると新しい統合機会が生まれるので、変化が止まるまで回す。
        for (let pass = 0; pass < 10; pass++) {
            let changed = false;
            host.querySelectorAll(INLINE_FORMAT_SELECTOR).forEach(el => {
                if (!el.isConnected) return;

                // 中身が無くなった装飾は捨てる
                if (!el.textContent) { el.remove(); changed = true; return; }

                const parent = el.parentElement;

                // 同じ装飾の入れ子（<strong><strong>x</strong></strong>）は内側を外す
                if (parent && parent.tagName === el.tagName) {
                    while (el.firstChild) parent.insertBefore(el.firstChild, el);
                    el.remove();
                    changed = true;
                    return;
                }

                // 入れ子の順序を FORMAT_ORDER に揃える（内側が上位なら内外を入れ替える）
                const inner = el.children.length === 1 && el.childNodes.length === 1
                    ? el.firstElementChild : null;
                if (inner && inner.matches(INLINE_FORMAT_SELECTOR)) {
                    const oi = FORMAT_ORDER.indexOf(el.tagName.toLowerCase());
                    const ii = FORMAT_ORDER.indexOf(inner.tagName.toLowerCase());
                    if (oi >= 0 && ii >= 0 && ii < oi && parent) {
                        const lifted = document.createElement(inner.tagName.toLowerCase());
                        parent.insertBefore(lifted, el);
                        lifted.appendChild(el);
                        while (inner.firstChild) el.insertBefore(inner.firstChild, inner);
                        inner.remove();
                        changed = true;
                        return;
                    }
                }

                // 隣り合う同じ装飾は1つにまとめる
                let next = el.nextSibling;
                while (next && next.nodeType === Node.ELEMENT_NODE && next.tagName === el.tagName) {
                    while (next.firstChild) el.appendChild(next.firstChild);
                    next.remove();
                    next = el.nextSibling;
                    changed = true;
                }
            });
            if (!changed) break;
        }
        host.normalize(); // 分割で細切れになったテキスト節点を戻す
    }

    // toggleInlineFormat は選択範囲の装飾を付け外しする。
    // 範囲全体が既にその装飾なら外し、そうでなければ（一部だけの場合も含め）付ける。
    // ワープロ等と同じ挙動で、「まだ掛かっていない所に揃える」ほうが直感に合う。
    function toggleInlineFormat(kind) {
        const tag = INLINE_FORMATS[kind];
        if (!tag) return;
        const sel = window.getSelection();
        if (!sel || sel.rangeCount === 0 || sel.isCollapsed) return; // 未選択なら何もしない

        const range = sel.getRangeAt(0);
        const host = editableHostOf(range.commonAncestorContainer);
        if (!host) return;

        const nodes = selectedTextNodes(range, host);
        const targets = nodes.filter(n => n.nodeValue.trim() !== '');
        if (targets.length === 0) return;

        const upper = tag.toUpperCase();
        const wrapperOf = n => {
            for (let p = n.parentElement; p && p !== host; p = p.parentElement) {
                if (p.tagName === upper) return p;
            }
            return null;
        };
        const alreadyAll = targets.every(n => wrapperOf(n));

        targets.forEach(n => {
            const fmt = wrapperOf(n);
            if (alreadyAll) {
                if (fmt) liftOutOf(n, fmt);
            } else if (!fmt) {
                const w = document.createElement(tag);
                n.parentNode.insertBefore(w, n);
                w.appendChild(n);
            }
        });

        // 選択は操作前と同じ文字範囲に戻す（tidyInline の normalize より前に端を控える）
        const first = targets[0], last = targets[targets.length - 1];
        const restored = document.createRange();
        restored.setStart(first, 0);
        restored.setEnd(last, last.nodeValue.length);
        sel.removeAllRanges();
        sel.addRange(restored);

        tidyInline(host);
        updateHtmlPreview();
    }

    function showContextToolbarForBlock(block) {
        const toolbar = document.getElementById('w-context-toolbar');
        toolbar.innerHTML = '';
        let hasButtons = false;

        const content = block.querySelector('.block-content > *');
        if (!content) return hideContextToolbar();
        const tagName = content.tagName.toLowerCase();

        // 装飾ボタンは構造HTMLのブロックにだけ出す。
        // （明細行の追加は、表そのものを編集する #w-table-toolbar が担う。かつては
        //  発注書のカスタム要素に「＋ 部品を追加」を出していたが、移行完了で不要になった）
        if (!isCustomTag(tagName)) {
            // 見出し・段落だけでなく、リストや引用の中の文字も装飾できる。
            [['B', 'bold'], ['I', 'italic'], ['U', 'underline']].forEach(([label, kind]) => {
                const btn = document.createElement('button');
                btn.innerText = label;
                btn.style.minWidth = '24px';
                // mousedown で preventDefault しないと、押した瞬間に選択が解除されて
                // 対象が消えてしまう（execCommand と違い自前実装は選択範囲に依存する）。
                btn.addEventListener('mousedown', e => e.preventDefault());
                btn.addEventListener('click', e => {
                    e.preventDefault();
                    toggleInlineFormat(kind);
                });
                toolbar.appendChild(btn);
            });
            hasButtons = true;
        }

        if (hasButtons) {
            toolbar.classList.add('active');
            const rect = block.getBoundingClientRect();
            toolbar.style.top = (rect.top + window.scrollY - 35) + 'px';
            toolbar.style.left = (rect.left + window.scrollX + 20) + 'px';
        } else {
            hideContextToolbar();
        }
    }

    function hideContextToolbar() {
        const toolbar = document.getElementById('w-context-toolbar');
        if(toolbar) toolbar.classList.remove('active');
        activeBlock = null;
    }

    // 初回読み込み時の初期表示
    // タブを閉じる／離れるときに、保持中のロックをベストエフォートで解放する。
    window.addEventListener('pagehide', () => {
        if (lockToken) {
            navigator.sendBeacon(`/api/unlock?id=${currentPageId}&token=${encodeURIComponent(lockToken)}`);
        }
    });

    // initHeadroom はヘッダーをスクロール方向で出し入れする（headroom 型 / B案）。
    // 下スクロールで隠し、上スクロール／上端付近（REVEAL_AT_TOP）で再表示する。
    // rAF でスロットルし passive リスナーにするのでスクロール性能への影響は軽微。
    function initHeadroom() {
        const header = document.querySelector('.header');
        if (!header) return;
        let lastY = window.scrollY, ticking = false;
        const REVEAL_AT_TOP = 80, DELTA = 6;
        function update() {
            const y = window.scrollY;
            if (y <= REVEAL_AT_TOP) header.classList.remove('header--hidden');
            else if (Math.abs(y - lastY) > DELTA) header.classList.toggle('header--hidden', y > lastY);
            lastY = y;
            ticking = false;
        }
        window.addEventListener('scroll', () => {
            if (!ticking) { requestAnimationFrame(update); ticking = true; }
        }, { passive: true });
    }

    // bindChromeActions は、殻（ヘッダー・レール・パネル）の操作を配線する。
    //
    // かつては markup のインライン on*= から関数を直接呼んでいた。外部ファイルへ移して
    // addEventListener で結ぶことで、Content-Security-Policy から script-src 'unsafe-inline'
    // を外せるようにする（docs/【考察】CSP強化.md §4）。カスタム要素側の data-attr /
    // data-remove（エディタ仕様.md §0.1）と同じ考え方で、markup は「何を押したか」だけを
    // data 属性で示し、振る舞いはこちらが持つ。
    //
    // このスクリプトは </body> の直前で読まれるので、ここに出てくる要素はすべて存在する。
    function bindChromeActions() {
        const on = (sel, event, handler) => {
            const el = document.querySelector(sel);
            if (el) el.addEventListener(event, handler);
        };

        on('#w-drawer-backdrop', 'click', closeDrawers);
        document.querySelectorAll('.rail-toggle[data-rail]').forEach(btn => {
            btn.addEventListener('click', () => toggleRail(btn.dataset.rail));
        });

        on('#w-logout-btn', 'click', logout);
        on('#w-mode-toggle', 'change', onModeToggle);
        on('#w-create-subpage-btn', 'click', createSubpage);
        on('#w-pi-parent-apply', 'click', applyParent);
        on('#w-pp-chown-btn', 'click', chownPage);
        on('#w-pp-save', 'click', savePerms);
        on('#w-pp-public', 'change', setPublic);
    }

    bindChromeActions();

    window.onload = async function() {
        // 認証状態と本文の語彙を先に確定させる。
        // 語彙（/api/tag-schema）はシリアライズに必須なので、保存が走りうる状態になる前に読む。
        await Promise.all([loadMe(), loadTagSchema()]);
        const anon = document.body.classList.contains('anonymous');
        syncRailToggleButtons(); // レール開閉トグルの見た目を保存状態に合わせる
        initHeadroom(); // ヘッダーをスクロール方向で出し入れ（B案）

        const urlParams = new URLSearchParams(window.location.search);

        // 編集モード自動判定（?editパラメータがあるか）。匿名は編集できないので無視する。
        // 実際の編集モード適用とロック取得はロード完了後の onModeToggle() が行う。
        if (!anon && urlParams.has('edit')) {
            const toggle = document.getElementById('w-mode-toggle');
            if (toggle) toggle.checked = true;
        }

        // 本文はサーバーが #w-editor-content へ埋め込み済み（合成方式）。fetch はせず、
        // 既にあるDOMをそのまま初期コンテンツとしてブロック化する。認可・404・
        // 匿名のログイン誘導はサーバー側で解決済みなので、ここでは扱わない。
        initBlocks();
        buildToc();
        setupEditorEvents();
        setupAutoSave();

        if (anon) {
            // 匿名は読み取り専用。編集モードへは入らず、権限カード（loadPerms）は
            // 叩かない。ページ属性は /api/page-meta が実効公開なら匿名にも返すため、
            // 左レールの「親ページへ」リンク表示のために loadPageMeta は呼ぶ
            // （右レールの権限系表示は body.anonymous の CSS で非表示のまま）。
            // 子ページ一覧も実効公開の子だけをサーバー側で絞って返すため、匿名でも読み込む。
            applyMode();
            loadPageMeta();
            loadChildNav();
            return;
        }
        // ?edit ならロックを取得して編集、なければ閲覧モードを適用する。
        onModeToggle();

        // ページ属性（親・作成/更新情報）・権限・子ページナビをサイドパネルへ読み込む。
        loadPageMeta();
        loadPerms();
        loadChildNav();
    };
