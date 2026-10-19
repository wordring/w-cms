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
    // FOUC 防止のため、<head> で先に読む /assets/boot.js も同じ `wcms.ui` を読む（旧キーfallback付き。
    // もとはインラインの script だったが、CSP strict 化で外部ファイルへ移した）。
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
    // currentSlashNested は、節・折りたたみの中の段落からスラッシュメニューを開いたときの**その段落**
    // （2026-09-29）——選んだものはその段落1つを置き換える（上の段のブロックごと置き換えない）。
    let currentSlashNested = null;
    let slashSelectedIndex = 0;
    let draggedBlock = null;
    let activeBlock = null;
    
    // URLのパスからページIDを取得（/102300 のような形式）
    let pathId = window.location.pathname.replace(/^\/+/, '');
    if (pathId === '' || pathId === 'index.html') {
        pathId = '000000';
    }
    let currentPageId = pathId;
    // unsaved は、まだ保存していない変更がある印（表示だけ——保存は書き出しを元と比べて決める）。editSeq は変えた回数
    // （保存の最中に打った分を見分ける）。2026-10-12 昼に1.5秒止まるたびの保存をやめた（triggerAutoSave）。
    let unsaved = false;
    let editSeq = 0;
    // BG_HEADERS は、利用者の操作ではなく画面が裏で自動に送る要求（知らせを受けての読み直し・様子の見張り）に付ける印。
    // この印の付いた要求ではセッションの寿命が延びない（【要求】権限 §1「最後の操作から8時間」・internal/auth/session.go・2026-10-14）。
    const BG_HEADERS = { 'X-Wcms-Background': '1' };

    // ── 子ページの作成（テンプレート選択つき） ──────────────────────────
    // テンプレートは「テンプレート」フォルダ配下の**葉**（docs/考察/【考察】ページテンプレート.md §3.2）。
    // 使えるテンプレートが1つも無ければ従来どおり即作成する。
    // ページ作成は状態を変える操作なので POST で送る。GET のままだと CSRFProtect が
    // 素通しするうえ、本文へ <img src="/api/new-page?parent=..."> を保存しておくだけで
    // 閲覧者にページを作らせられる（保存型CSRF）。別タブで開く挙動は form の target で保つ
    // （CSP strict なのでインラインの on* は使わず、DOM APIで組み立てて submit する）。
    function openNewPage(templateId) {
        const form = document.createElement('form');
        form.method = 'POST';
        form.action = '/api/new-page';
        form.target = '_blank';
        const addField = (name, value) => {
            const input = document.createElement('input');
            input.type = 'hidden';
            input.name = name;
            input.value = value;
            form.appendChild(input);
        };
        addField('parent', currentPageId);
        if (templateId) addField('template', templateId);
        document.body.appendChild(form);
        form.submit();
        form.remove();
    }

    // hasTemplateLeaf はツリーに葉（＝コピーできるテンプレート）があるかを返す。
    function hasTemplateLeaf(nodes) {
        return (nodes || []).some(n => !n.children || !n.children.length || hasTemplateLeaf(n.children));
    }

    async function createSubpage() {
        let tree = [];
        try {
            const res = await fetch('/api/templates');
            if (res.ok) tree = await res.json();
        } catch (e) {
            // テンプレート一覧が取れなくても新規作成そのものは行えるようにする
        }
        if (!hasTemplateLeaf(tree)) { openNewPage(''); return; }
        showTemplateMenu(tree);
    }

    // appendTemplateNodes はツリーをメニューへ描く。
    // 枝（子を持つ）は分類の見出し、葉はコピーできるテンプレート。
    // 階層は左パディングで表す（CSSクラスではなく段数が可変のため CSSOM で当てる）。
    function appendTemplateNodes(host, nodes, depth) {
        (nodes || []).forEach(n => {
            const isLeaf = !n.children || !n.children.length;
            if (isLeaf) {
                const b = document.createElement('button');
                b.type = 'button';
                b.textContent = n.title;
                b.style.paddingLeft = (10 + depth * 12) + 'px';
                b.addEventListener('click', () => { hideTemplateMenu(); openNewPage(n.id); });
                host.appendChild(b);
            } else {
                const g = document.createElement('div');
                g.className = 'template-menu-group';
                g.textContent = n.title;
                g.style.paddingLeft = (10 + depth * 12) + 'px';
                host.appendChild(g);
                appendTemplateNodes(host, n.children, depth + 1);
            }
        });
    }

    function hideTemplateMenu() {
        const menu = document.getElementById('w-template-menu');
        if (menu) menu.classList.remove('active');
        document.removeEventListener('click', onTemplateMenuOutside, true);
        document.removeEventListener('keydown', onTemplateMenuKey, true);
    }

    function onTemplateMenuOutside(e) {
        if (!e.target.closest('#w-template-menu') && !e.target.closest('#w-create-subpage-btn')) {
            hideTemplateMenu();
        }
    }

    function onTemplateMenuKey(e) {
        if (e.key === 'Escape') hideTemplateMenu();
    }

    // placeFloating は浮くパネル（メニュー・ポップオーバ）を要素の下へ置きます。
    //
    // **画面の右端からはみ出さないよう寄せます**（2026-09-07）。それまでは押した要素の
    // 左端に合わせるだけで、**スマホ幅では 206px はみ出していました**——リンクの
    // プロパティ欄は実寸 246px、画面は 320〜390px なので、右寄りのリンクで開くと
    // 画面外へ出て触れません（Playwright で実測）。
    //
    // **呼ぶ前に表示状態にしておくこと**——寸法を測るので、`display:none` のままだと
    // 0 になって寄せが効きません（測り方を1度間違えた箇所です）。
    function placeFloating(el, rect, gap) {
        const pad = 8;
        el.style.top = (rect.bottom + window.scrollY + (gap || 4)) + 'px';
        const view = document.documentElement.clientWidth;
        const w = el.getBoundingClientRect().width || el.offsetWidth || 0;
        let left = rect.left;
        if (left + w > view - pad) left = view - w - pad; // 右へはみ出す→左へ寄せる
        if (left < pad) left = pad;                        // それでも入らない→左端
        el.style.left = (left + window.scrollX) + 'px';
    }
    // 外の段（表の「⋯」のメニュー——2026-10-15）からも同じ寄せ方を使えるように。
    window.wcmsPlaceFloating = placeFloating;

    // ── 動かせるツールバー（2026-09-27 利用者:「表の編集時に、メニューが邪魔な場所に出がち
    // なので、メニューをドラッグして位置変更ができるようにしてください」） ──
    //
    // 表の行・列のツールバーと定義リストの項目のツールバーは、キャレットのある行の右横
    // （入らなければ行の上）に出ます。表は文書の欄の中で横にはみ出すので、**ほぼ毎回「上」に
    // なって前の行を隠していました**。
    //
    // 左端のつまみ（⠿）をドラッグすると、**その画面位置に留まります**（`position: fixed`——
    // スクロールしても、別の行・別の表へ移っても動かない）。位置は UI ストア `toolbars.<名前>` に
    // 覚えるので、ページを開き直してもそこに出ます（この閲覧者のこのブラウザだけ）。
    // **つまみをダブルクリックすると、元の「行に付いて回る」出し方へ戻ります。**
    // ⚠ つまみを押しただけ（動かさない）では留めません——クリックで勝手に固まらないように。
    // ⚠ 2026-10-19 からは、パソコンでは2つのツールバーとも本文の上の固定の帯に入ります（initFixedBar・つまみは隠れる）。
    //    行に付いて回る出し方とつまみで動かすのは、指で触る端末だけです。
    const TOOLBAR_EDGE = 4;

    // clampToViewport はツールバーが画面（ビューポート）の中に収まる位置を返します。
    function clampToViewport(bar, x, y) {
        const w = bar.offsetWidth || 0, h = bar.offsetHeight || 0;
        const vw = document.documentElement.clientWidth, vh = document.documentElement.clientHeight;
        return {
            x: Math.max(TOOLBAR_EDGE, Math.min(x, vw - w - TOOLBAR_EDGE)),
            y: Math.max(TOOLBAR_EDGE, Math.min(y, vh - h - TOOLBAR_EDGE)),
        };
    }

    // placeToolbar はツールバーを置きます。留めた位置があればそこ（画面座標）、無ければ
    // 呼び手が計算した位置（ページ座標 top・left）。**呼ぶ前に表示状態にしておくこと**（寸法を測る）。
    function placeToolbar(bar, name, top, left) {
        const pin = UI.get('toolbars.' + name, null);
        if (pin && typeof pin.x === 'number' && typeof pin.y === 'number') {
            bar.classList.add('pinned');
            const p = clampToViewport(bar, pin.x, pin.y);
            bar.style.left = p.x + 'px';
            bar.style.top = p.y + 'px';
            return;
        }
        bar.classList.remove('pinned');
        // ⚠ 行に付いて回るときも**画面の左右に寄せます**——スマホ幅では行の左端から出すと
        //    右へはみ出していました（320px の画面で 125px・つまみを足す前から・2026-09-27 に実測）。
        const vw = document.documentElement.clientWidth;
        const w = bar.offsetWidth || 0;
        left = Math.max(window.scrollX + TOOLBAR_EDGE, Math.min(left, window.scrollX + vw - w - TOOLBAR_EDGE));
        bar.style.top = top + 'px';
        bar.style.left = left + 'px';
    }

    // makeToolbarDraggable はツールバーの左端につまみを付け、ドラッグで動かせるようにします。
    // reposition はダブルクリックで元の出し方へ戻したあとに置き直す関数です。
    function makeToolbarDraggable(bar, name, reposition) {
        if (!bar || bar.querySelector('.toolbar-grip')) return;
        const grip = document.createElement('span');
        grip.className = 'toolbar-grip';
        grip.textContent = '⠿';
        grip.title = 'ドラッグで移動（その位置に留まります）／ダブルクリックで元の出し方へ';
        bar.insertBefore(grip, bar.firstChild);

        let drag = null;
        grip.addEventListener('pointerdown', e => {
            if (e.button !== 0) return;
            e.preventDefault(); // キャレット（どの行への操作か）を動かさない
            const r = bar.getBoundingClientRect();
            drag = { sx: e.clientX, sy: e.clientY, dx: e.clientX - r.left, dy: e.clientY - r.top, moved: false };
            grip.setPointerCapture(e.pointerId);
        });
        grip.addEventListener('pointermove', e => {
            if (!drag) return;
            if (!drag.moved) {
                if (Math.abs(e.clientX - drag.sx) + Math.abs(e.clientY - drag.sy) < 4) return;
                drag.moved = true;
                bar.classList.add('pinned'); // ここから画面座標で動かす
            }
            const p = clampToViewport(bar, e.clientX - drag.dx, e.clientY - drag.dy);
            bar.style.left = p.x + 'px';
            bar.style.top = p.y + 'px';
        });
        const end = e => {
            if (!drag) return;
            const moved = drag.moved;
            drag = null;
            try { grip.releasePointerCapture(e.pointerId); } catch (err) { /* 既に外れている */ }
            if (moved) {
                UI.set('toolbars.' + name, { x: parseFloat(bar.style.left) || 0, y: parseFloat(bar.style.top) || 0 });
            }
        };
        grip.addEventListener('pointerup', end);
        grip.addEventListener('pointercancel', end);
        grip.addEventListener('dblclick', e => {
            e.preventDefault();
            UI.set('toolbars.' + name, null);
            bar.classList.remove('pinned');
            reposition();
        });
        // 画面の大きさが変わったら、留めた位置を画面の中へ寄せ直す（覚えた位置はそのまま）。
        window.addEventListener('resize', () => {
            if (bar.classList.contains('active') && bar.classList.contains('pinned')) reposition();
        });
    }

    function showTemplateMenu(tree) {
        const menu = document.getElementById('w-template-menu');
        const btn = document.getElementById('w-create-subpage-btn');
        if (!menu || !btn) { openNewPage(''); return; }

        menu.textContent = ''; // 前回の項目を消す
        const blank = document.createElement('button');
        blank.type = 'button';
        blank.textContent = '空のページ';
        blank.addEventListener('click', () => { hideTemplateMenu(); openNewPage(''); });
        menu.appendChild(blank);

        const sep = document.createElement('div');
        sep.className = 'template-menu-group';
        sep.textContent = 'テンプレートから';
        menu.appendChild(sep);

        appendTemplateNodes(menu, tree, 0);

        menu.classList.add('active');
        const rect = btn.getBoundingClientRect();
        menu.style.minWidth = rect.width + 'px';
        placeFloating(menu, rect);
        // 開いた直後の click がそのまま外側判定に入らないよう capture で次から拾う
        document.addEventListener('click', onTemplateMenuOutside, true);
        document.addEventListener('keydown', onTemplateMenuKey, true);
    }

    // --- サイドパネルの開閉（UI ストア `wcms.ui.rails` に永続化。FOUC防止は boot.js） ---
    // レールは2枚（left=ページ階層・目次／right=ページ情報）。目次は左レールの先頭カード
    // （2026-09-01）。グループは将来「同時に開かない」レールが増えたときのための器で、
    // いまは各レール単独。
    const RAIL_IDS = ['left', 'right'];
    const RAIL_GROUPS = [['left'], ['right']];
    function railGroupOf(side) {
        return RAIL_GROUPS.find(g => g.includes(side)) || [side];
    }

    // 狭幅（ドロワーモード）かどうか。レールごとに異なる閾値を持つ（CSS の @media と
    // 一致させること）。右レールを先に、次に左レールの順にドロワー化する段階式で、
    // 本文（doc-column）の幅が狭くなりすぎないようにする。
    const RAIL_BREAKPOINTS = { left: 760, right: 1100 };
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
            const btn = document.getElementById('w-toggle-' + side);
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
                li.dataset.pageId = p.ID;
                // ドラッグで並べ替えられる（結果は並び順キーとして保存される）。
                li.draggable = true;
                const a = document.createElement('a');
                a.className = 'child-nav-link';
                a.href = '/' + p.ID;
                a.textContent = '📄 ' + (p.Title || p.ID);
                a.title = p.Title || p.ID;
                li.appendChild(a);
                list.appendChild(li);
            });
            wireChildDrag(list);
        } catch (e) { list.innerHTML = '<li class="child-nav-empty">通信エラー</li>'; }
    }

    // ── 左レールの並べ替え（2026-09-03）────────────────────────────────
    //
    // ユーザー:「左レールの子ページ一覧をドラッグで順番を変えることを考えたうえで
    // 判断してください」。落とした結果の**並び全部**をサーバーへ送ります
    // ——「どこへ落としたか」を送ると、サーバーが前後関係を推測することになるため。
    //
    // 失敗したら描き直して元に戻します（画面だけ並び替わったまま、を作らない）。
    let dragSrc = null;

    function wireChildDrag(list) {
        list.querySelectorAll('li[data-page-id]').forEach(li => {
            li.addEventListener('dragstart', e => {
                dragSrc = li;
                li.classList.add('child-nav-dragging');
                e.dataTransfer.effectAllowed = 'move';
                // Firefox はデータを載せないとドラッグが始まらない。
                e.dataTransfer.setData('text/plain', li.dataset.pageId);
            });
            li.addEventListener('dragend', () => {
                li.classList.remove('child-nav-dragging');
                list.querySelectorAll('.child-nav-over')
                    .forEach(el => el.classList.remove('child-nav-over'));
                dragSrc = null;
            });
            li.addEventListener('dragover', e => {
                if (!dragSrc || dragSrc === li) return;
                e.preventDefault();
                e.dataTransfer.dropEffect = 'move';
                li.classList.add('child-nav-over');
            });
            li.addEventListener('dragleave', () => li.classList.remove('child-nav-over'));
            li.addEventListener('drop', e => {
                e.preventDefault();
                li.classList.remove('child-nav-over');
                if (!dragSrc || dragSrc === li) return;
                // 上半分に落としたら手前、下半分なら後ろへ。
                const box = li.getBoundingClientRect();
                const before = (e.clientY - box.top) < box.height / 2;
                list.insertBefore(dragSrc, before ? li : li.nextSibling);
                saveChildOrder(list);
            });
        });
    }

    async function saveChildOrder(list) {
        const order = Array.from(list.querySelectorAll('li[data-page-id]'))
            .map(li => li.dataset.pageId);
        try {
            const res = await fetch('/api/reorder?parent=' + encodeURIComponent(currentPageId), {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ order: order }),
            });
            const d = await res.json();
            if (!d.success) throw new Error(d.message || res.status);
        } catch (e) {
            notify('並び順を保存できませんでした: ' + e, { type: 'alert', duration: 0, id: 'reorder' });
            loadChildNav(); // 画面だけ並び替わったまま、にしない
        }
    }

    // 左レール：目次を本文中の見出し(h1〜h6)から構築する。サーバーには一切依存せず、
    // 表示中の #w-editor-content を走査するだけ（クリックで scrollIntoView）。
    // 本文が変わるたび（初回ロード・編集中）に呼び直して最新化する。
    // markPageTitleHeading は「このページのタイトルになる見出し」へ実行時の印を付ける。
    //
    // タイトルはサーバーが**文書順で最初の h1** から採る（parser.go の ParseCore）。
    // 属性で持たせると並べ替え・挿入でずれるので、**位置から毎回計算し直す**。
    // class はサニタイザもシリアライザも通さないため保存には漏れない
    // （型不一致の .cell-invalid と同じ流儀）。
    function markPageTitleHeading() {
        const container = document.getElementById('w-editor-content');
        if (!container) return;
        container.querySelectorAll('h1.is-page-title')
            .forEach(el => el.classList.remove('is-page-title'));
        // クロームはサーバー／エンハンサが挿す表示専用の飾りで本文ではない（保存もされない）。
        const first = Array.from(container.querySelectorAll('h1'))
            .find(el => !el.closest('.vocab-chrome'));
        if (first) first.classList.add('is-page-title');
    }

    function buildToc() {
        // タイトルの印は本文DOMが変わるたびに付け直す必要があり、契機が目次とまったく同じ
        // （populateEditor・エコーバック・保存後）なのでここに相乗りする。
        markPageTitleHeading();
        // 形式名の札も同じ契機（本文DOMの変化）で付け直す。**変化が無ければDOMを触らない**
        // 作りなので、ここから呼んでも「変える→観測→また変える」の輪にはならない。
        decorateVocabBlocks();
        const list = document.getElementById('w-toc-list');
        if (!list) return;
        const container = document.getElementById('w-editor-content');
        const headings = Array.from(container.querySelectorAll('h1, h2, h3, h4, h5, h6'))
            // サーバーが埋めた計算ビューの中身やエンハンサのクローム（.vocab-chrome）は
            // 本文の見出しではないので目次に載せない（保存もされない表示専用の飾り）。
            .filter(el => !el.closest('.vocab-chrome'))
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
    let currentUsername = ''; // ページの更新の知らせで「あなたの操作で」と言い分けるため（2026-10-07）

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
            currentUsername = me.username || '';
            document.getElementById('w-user-bar').textContent =
                '👤 ' + me.username + (me.is_admin ? '（管理者）' : '');
            if (me.is_admin) setHidden(document.getElementById('w-admin-link'), false);
            // 検索画面（表を探す・2026-09-25）はログインした人なら誰でも使える
            // （サーバーが読めるページの行だけ返す）。匿名には出さない。
            setHidden(document.getElementById('w-tables-link'), false);
            // メールの一覧（2026-09-29）は通信の拡張の持ち物——載っていなければ出さない（語彙の読み込みのあとでも見直す）。
            setHidden(document.getElementById('w-mails-link'), !hasExtension('comm'));
        } catch (e) { /* 通信エラー等は無視（編集UIは権限取得時に再判定） */ }
    }

    async function logout() {
        try { await fetch('/api/logout', { method: 'POST' }); } catch (e) {}
        window.location.href = '/login';
    }

    // 権限カードの編集可否は「書ける形（body[edit-mode]）」かつサーバーが返す権限フラグ（can_chmod/
    // can_chown）の両方で決まる。閲覧モードのあいだは読み取り専用にする（親ページIDの編集と同じ方針）。
    // 2026-10-12 まで、編集モード＝編集ロックを握っているときで、権限変更もそのロックで直列化していた。
    // いまはブラウザは編集権を取らない（送る口 lockedFetch は書きかけを先に保存するだけ）。
    let permCanChmod = false, permCanChown = false, permCanPublish = false;
    function refreshPermsEditable() {
        const editMode = document.body.hasAttribute('edit-mode');
        const canEditMeta = editMode && permCanChmod;
        document.getElementById('w-pp-group').disabled = !canEditMeta;
        document.getElementById('w-pp-mode').disabled = !canEditMeta;
        setHidden(document.getElementById('w-pp-chown'), !(editMode && permCanChown));
        setHidden(document.getElementById('w-pp-save'), !canEditMeta);
        setHidden(document.getElementById('w-pp-mode-hint'), !canEditMeta);
        // 権限はあるが閲覧モードのとき、書ける形へ切り替える誘導（w-pp-view-hint）を出す。
        setHidden(document.getElementById('w-pp-view-hint'),
            !(!editMode && (permCanChmod || permCanChown || permCanPublish)));
        // 匿名公開トグル：owner/admin にのみ行を見せ、書ける形でのみ操作可（chmod と同じ規律）。
        setHidden(document.getElementById('w-pp-public-row'), !permCanPublish);
        const pub = document.getElementById('w-pp-public');
        if (pub) pub.disabled = !(editMode && permCanPublish);
        // 削除は破壊的なので、書ける形のときだけ出す（2026-10-12 までは「編集モード＝ロック保持」のとき）。
        // ⚠ ここで見ているのは書ける形かどうかだけ。write 権限の無い人は書ける形に入れない（/api/sync が 403）ので、結果として
        //    write 権限があるときだけになる。
        // トップページは消せないので、そこでは出さない（サーバーも 400 で拒否する）。
        setHidden(document.getElementById('w-delete-page-btn'),
            !(editMode && currentPageId && currentPageId !== '000000'));
        // 子ページ作成も「書く系」なので書ける形だけに出す（2026-10-12 までは編集モードだけ。2026-08-31 ユーザー:
        // 「閲覧モードで子ページ作成ボタンが出ているのは不思議」——読む画面に作る
        // ボタンが混ざらないよう、削除と同じ規律に揃えた）。
        setHidden(document.getElementById('w-create-subpage-btn'), !editMode);
    }

    // applyEditToggleVisibility は、書く権限（can_write）の無いユーザー（および匿名）から右上の「閲覧」スイッチを隠す。
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
            // chown は admin のみ、chmod/chgrp は owner か admin。さらに書ける形の
            // ときだけ編集可能にする（refreshPermsEditable）。権限フラグは保持して再評価に使う。
            permCanChmod = !!p.can_chmod;
            permCanChown = !!p.can_chown;
            permCanPublish = !!p.can_publish;
            if (permCanChown) document.getElementById('w-pp-owner-input').value = p.owner || '';
            // 匿名公開の現在状態をトグルとヒントに反映する（認証認可設計.md 10章）。
            applyPublicState(!!p.public, !!p.effective_public);
            // 書く権限の無いユーザーからは右上のスイッチを隠す（サーバーも RequirePageWrite で防御——/api/sync は 403）。
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

    // setPublic は匿名公開フラグを切り替える（owner/admin、書ける形のときのみ）。
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
            // 左レールの「↑ 親ページへ」は 2026-09-03 に**パンくず**へ置き換えた
            // （本文の上の帯・サーバーが組む。internal/cms/breadcrumb.go）。
            // 一段しか上がれず、狭い画面ではドロワーを開くまで見えなかったため。
            document.getElementById('w-pi-created-at').textContent = formatDateTime(m.created_at);
            document.getElementById('w-pi-created-by').textContent = m.created_by || '—';
            document.getElementById('w-pi-updated-at').textContent = formatDateTime(m.updated_at);
            loadedUpdatedAt = m.updated_at || ''; // つなぎ直したときに、切れていたあいだの書き込みを見分ける（2026-10-07）
            showPageQR();
        } catch (e) { /* 取得失敗時は既定表示のまま */ }
    }

    // showPageQR はページ情報カードにこのページのURLのQRを出します（2026-09-10
    // ユーザー:「右レールのページ情報にそのページのURLを表すQRコードを入れられますか？」）。
    //
    // **URLはサーバーが組みます**（`/api/qr?page_id=…`）。ここで `location.href` を
    // 送って絵にしてもらう形にすると、任意の文字列をQRにする口になるためです
    // （internal/cms/qr_handler.go の説明）。読めないページなら404が返るので、
    // そのときは**黙って出しません**——「読めない」と「存在しない」を区別させない規律。
    //
    // 下に出す文字は**目で確かめるため**です。QRは中身が見えないので、
    // 「社内のホスト名のまま社外の人に見せていた」に後から気づけません。
    function showPageQR() {
        const img = document.getElementById('w-pi-qr');
        const label = document.getElementById('w-pi-qr-url');
        if (!img || !currentPageId) return;
        img.hidden = true;
        label.textContent = '';
        img.onload = () => {
            img.hidden = false;
            label.textContent = window.location.origin + '/' + currentPageId;
        };
        img.onerror = () => { img.hidden = true; label.textContent = ''; };
        img.src = '/api/qr?page_id=' + encodeURIComponent(currentPageId);
    }

    // ── 版の履歴（リビジョン／リバート。internal/cms/version.go） ────────────
    // 対象は**本文HTMLだけ**なので、戻しても権限・所有者・親は変わらない
    // （設計 docs/考察/【考察】アンドゥ・リドゥ.md §5）。UIもその前提で「本文を戻す」とだけ言う。

    // loadVersions は版の一覧を右レールへ描き直す。
    async function loadVersions() {
        const list = document.getElementById('w-vh-list');
        if (!list || !currentPageId) return;
        const isEdit = document.body.hasAttribute('edit-mode');
        setHidden(document.getElementById('w-vh-view-hint'), isEdit);
        try {
            const res = await fetch('/api/versions?id=' + encodeURIComponent(currentPageId));
            if (!res.ok) return;
            const versions = await res.json();
            list.textContent = '';
            if (!versions.length) {
                const li = document.createElement('li');
                li.className = 'version-empty';
                li.textContent = 'まだ履歴がありません。';
                list.appendChild(li);
                return;
            }
            versions.forEach((v, idx) => {
                const li = document.createElement('li');
                li.className = 'version-item';
                li.dataset.version = v.id;

                const when = document.createElement('span');
                when.className = 'version-when';
                when.textContent = formatDateTime(v.at);
                li.appendChild(when);

                const who = document.createElement('span');
                who.className = 'version-who';
                who.textContent = v.by || '—';
                li.appendChild(who);

                // 先頭（＝いまの内容）に戻すボタンは出さない。押しても何も起きず、
                // 「戻せなかった」ように見えるだけなので。
                if (isEdit && idx > 0) {
                    // ブロックごとに見て戻す（2026-10-12 利用者が選んだ形——ほかの人の書き込みを消さずに、そこだけ戻す・問い8）。
                    const look = document.createElement('button');
                    look.type = 'button';
                    look.className = 'rail-btn version-blocks';
                    look.textContent = 'ブロックごとに見る';
                    look.addEventListener('click', () => openVersionBlocks(v));
                    li.appendChild(look);
                    const btn = document.createElement('button');
                    btn.type = 'button';
                    btn.className = 'rail-btn version-revert';
                    btn.textContent = 'この版に戻す';
                    btn.addEventListener('click', () => revertToVersion(v));
                    li.appendChild(btn);
                }
                list.appendChild(li);
            });
        } catch (e) { /* 取得失敗時は前の表示のまま（履歴が出ないだけ） */ }
    }

    // ── 版から「このブロックだけ戻す」（2026-10-12・【考察】編集モードの再考 §6 問い8）────────────────
    //
    // 利用者が選んだ形:「版を押すと、その版といまで違うブロックに印を付けて見せ、ブロックごとに『このブロックだけ戻す』」。
    // ページ丸ごと戻すと、その後にほかの人が書いた別のブロックまで消える——翌日に誤りに気づいたとき、そこだけ戻せるように。
    // 戻すのは画面のブロックを差し替えるだけ（ふつうの書き込みとして /api/sync で送る——相手がそのブロックを書いていればぶつかる）。
    // 版の本文は ID の無いブロックを持つことがある（機械が書いた昔の版）——ID が無ければ中身で照らす。
    async function openVersionBlocks(v) {
        if (!document.body.hasAttribute('edit-mode')) return;
        let html;
        try {
            const res = await fetch('/api/version?id=' + encodeURIComponent(currentPageId) + '&v=' + encodeURIComponent(v.id));
            if (!res.ok) throw new Error(await res.text());
            html = await res.text();
        } catch (e) {
            notify('版を読めませんでした: ' + e.message, { type: 'warn', duration: 8000 });
            return;
        }
        const old = serverBlocksOf(html).map(el => ({ id: el.getAttribute(BLOCK_ID_ATTR) || '', el, ser: serializeBlock(el) }));
        const live = serializeBlocks();
        const liveById = new Map(live.map(b => [b.id, b]));
        const liveHTML = new Set(live.map(b => b.html));
        const diffs = [];
        old.forEach((b, i) => {
            if (b.id && liveById.has(b.id)) {
                if (liveById.get(b.id).html !== b.ser) diffs.push({ kind: 'changed', b, i });
            } else if (!liveHTML.has(b.ser)) {
                diffs.push({ kind: 'gone', b, i });
            }
        });
        const middle = (done) => {
            const box = document.createElement('div');
            box.className = 'w-version-blocks';
            if (!diffs.length) {
                const p = document.createElement('p');
                p.textContent = 'この版といまで違うブロックはありません。';
                box.appendChild(p);
                return box;
            }
            diffs.forEach(d => {
                const item = document.createElement('div');
                item.className = 'w-version-block';
                const label = document.createElement('p');
                label.className = 'w-version-block-label';
                label.textContent = d.kind === 'changed' ? '⚠ いまと中身が違うブロック（この版のときの中身）' : '⚠ いまは無いブロック（この版のときの中身）';
                const view = document.createElement('div');
                view.className = 'w-version-block-view';
                view.appendChild(document.importNode(d.b.el, true));
                const btn = document.createElement('button');
                btn.type = 'button';
                btn.className = 'w-version-block-revert';
                btn.textContent = 'このブロックだけ戻す';
                btn.addEventListener('click', () => {
                    revertOneBlock(d, old);
                    btn.disabled = true;
                    btn.textContent = '戻しました';
                });
                item.append(label, view, btn);
                box.appendChild(item);
            });
            return box;
        };
        await openDialog(formatDateTime(v.at) + ' の版（' + (v.by || '—') + '）といまで違うブロック: ' + diffs.length + ' か所',
            middle, [['閉じる', null]], null);
    }

    // revertOneBlock は版のブロック1つを画面へ戻す（いまあれば差し替え、無ければ版の並びで前にあるブロックの後ろへ足す）。
    function revertOneBlock(d, old) {
        flushCheckpoint();
        const node = document.importNode(d.b.el, true);
        const cur = d.b.id ? blockElementById(d.b.id) : null;
        if (cur) {
            cur.replaceWith(node);
        } else {
            if (d.b.id && blockElementById(d.b.id)) node.removeAttribute(BLOCK_ID_ATTR);
            let prev = null;
            for (let k = d.i - 1; k >= 0 && !prev; k--) {
                const o = old[k];
                prev = o.id ? blockElementById(o.id) : topLevelBlocks().find(el => serializeBlock(el) === o.ser) || null;
            }
            const editor = document.getElementById('w-editor-content');
            const anchor = prev ? (prev.closest('.editor-block') || prev) : null;
            if (anchor) anchor.after(node); else editor.insertBefore(node, editor.firstChild);
            wrapInBlock(node);
        }
        applyMode();
        buildToc();
        updateHtmlPreview();
        triggerAutoSave();
        notify('ブロックを版の中身に戻しました（保存されます・アンドゥで取り消せます）。', { type: 'success', duration: 5000, id: 'version-block' });
    }

    // revertToVersion は選んだ版を本文へ書き戻す。
    // 取り消せる操作（戻す前の内容も版として残る）だが、本文が丸ごと入れ替わるので確認する。
    async function revertToVersion(v) {
        const msg = document.getElementById('w-vh-msg');
        if (!confirm(formatDateTime(v.at) + ' の版に本文を戻します。よろしいですか？\n'
            + '（いまの内容も履歴に残るので、戻すことができます）')) return;
        msg.textContent = '戻しています…';
        try {
            const res = await lockedFetch('/api/revert', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ page_id: currentPageId, version: v.id }),
            });
            const d = await readResult(res);
            if (!res.ok || !d.success) throw new Error(failMessage(res, d));
            msg.textContent = '';
            // 本文を載せ替える——戻したのも「ほかの書き手の1人」（2026-10-12）。書ける形なら元ごと受け取り直す
            // （アンドゥの履歴は戻した中身を起点に合わせる）。
            if (document.body.hasAttribute('edit-mode')) {
                await loadForEditing();
                applyMode();
                rebaseHistory();
            } else {
                populateEditor(d.html);
            }
            loadVersions();
            loadPageMeta();
            notify('本文を ' + formatDateTime(v.at) + ' の版に戻しました。', { type: 'success', duration: 6000 });
        } catch (e) {
            msg.textContent = '';
            notify('版を戻せませんでした: ' + e.message, { type: 'warn', duration: 8000 });
        }
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
            msg.style.color = '#dc2626'; msg.textContent = '通信エラー';
        }
    }

    // 親ページIDの編集はサイドパネルの「親ページID」入力＋適用ボタンが担い、
    // /api/set-parent（サーバーが検証）でサイドカーへ反映する（applyParent を参照）。

    // ── ブロック単位で合わせる保存の状態（2026-10-12・【考察】編集モードの再考 §6） ──────────────
    //
    // 利用者:「排他のために編集モードを設けたが、今後は編集モードの代わりに、閲覧に邪魔なものを表示しない目的で、
    // 閲覧モードを設けることにします。そして、ブロック単位の保護に変更し、保護の内容は編集中の同ブロックを同時に
    // 編集したものが居たら、警告してマージできるように両方表示します」。
    //
    //   - syncBase は、このタブが**いまの中身の元にしたサーバーの版**——ブロック ID → { h: サーバーの指紋, html: そのとき
    //     この画面が書き出した形 }。h はサーバーが計算したものだけ（画面とサーバーの書き出しは字面が違いうる）。
    //   - syncOrder は元の並び。送るのは「元の並びと指紋」「いまの並び」「元から変えたブロック」（/api/sync）。
    //   - ⚠ **元の指紋は、画面の中身より新しくしてはいけない**——新しくすると、相手の変更を知らないまま「元のまま」と
    //     名乗って上書きする。古いぶんには「ぶつかった」と言われるだけ（サーバーが中身で比べ直す）。
    //   - syncConflicts は並べている（聞いている）ブロック——そのあいだ中身は送らない（相手の版を黙って上書きしない）。
    const TAB_ID = (() => {
        const b = new Uint8Array(8);
        crypto.getRandomValues(b);
        return Array.from(b, x => x.toString(16).padStart(2, '0')).join('');
    })();
    let syncBase = new Map();
    let syncOrder = [];
    let syncReady = false;          // 書ける形に入って元を受け取ったあと（それまでは送らない）
    const syncConflicts = new Map(); // ブロック ID → { kind: 'changed'|'deleted', panel, ... }
    let syncChain = Promise.resolve();
    let syncWrote = false;          // このタブが書いた（閲覧モードへ切り替えたとき・離れるときに版の区切りを打つ——checkpointVersion）

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

    // ── 失敗の理由を、利用者に届ける（2026-09-14）──────────────────────────
    //
    // **サーバーは丁寧に説明しているのに、画面には数字しか出ていませんでした。**
    // 実測: 許可していない拡張子を落とすと、サーバーは 400 で
    // 「この拡張子は添付として受け付けていません（許可リストは…）」と返します。
    // ところが応答は `text/plain` なので `res.json()` が落ち、`catch(()=>({}))` が
    // 空オブジェクトを返し、画面には「400」だけが出ていました。
    //
    // ⚠ **`res.json()` のあとの `res.text()` は永久に空です**——本体は一度しか
    // 読めないので、退避のつもりで書いた `|| await res.text()` は効いていませんでした。
    //
    // だから**本文を1回だけ文字列として読み**、JSONなら解釈します。認可
    // （`RequireAuth` の「認証が必要です」）のように、共通の関門が返す
    // `text/plain` にも同じように効きます。
    async function readResult(res) {
        let text = '';
        try { text = await res.text(); } catch (e) { text = ''; }
        try {
            const d = JSON.parse(text);
            if (d && typeof d === 'object') return d;
        } catch (e) { /* JSONではない——本文をそのまま理由にする */ }
        return { message: String(text || '').trim() };
    }

    // failMessage は「なぜ失敗したか」の1行を作ります。**数字は最後の手段**です。
    function failMessage(res, d) {
        return (d && d.message) || res.statusText || String(res.status);
    }

    function notifySanitized() {
        notify('安全でない記述（スクリプトやイベント属性など）を除去して保存しました。',
            { type: 'warn', duration: 8000, id: 'sanitized' });
    }

    // notifyUnknownTypes はレジストリ未定義の data-type の**告知**（拒否ではない）。
    // 未知の種別も保存され索引にも載る（語彙モデル §9 の決定。エコーバックの流儀）。
    // 告知は**目立つ赤**（type: 'alert'）で、時間では消えない（duration: 0）。
    // 綴り違い（<section data-type="cliet-order"> のような打ち間違い）は保存こそ通るが、
    // その塊は計算から静かに外れる。控えめなトーストで流れると気づけないため
    // （ユーザー要望「赤色背景などで告知してください」・2026-08-25）。
    function notifyUnknownTypes(types) {
        if (!Array.isArray(types) || !types.length) return;
        // サーバーは、廃止した名前の見えない鏡の印（2026-09-27）もここへ入れて返す。
        const retired = types.filter(isRetiredViewMarker);
        const unknown = types.filter(t => !isRetiredViewMarker(t));
        const parts = [];
        if (unknown.length) {
            parts.push('未定義の種別 ' + unknown.map(t => '「' + t + '」').join('・') +
                ' の表・リスト・ブロックがあります。そのまま保存しますが、' +
                '計算には使われません（綴りを確かめてください）。');
        }
        if (retired.length) {
            parts.push('名前の見えない鏡の印 ' + retired.map(t => '「' + t + '」').join('・') +
                ' は廃止しました。描かれないので、スラッシュメニューから鏡を入れ直してください。');
        }
        notify(parts.join(' '), { type: 'alert', duration: 0, id: 'unknown-vocab' });
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

    // deletePage はページをゴミ箱（data/trash）へ移す。物理削除ではないので取り消せるが、
    // 画面からは消えるので確認を挟む。書ける形でのみ押せる（ボタン自体も refreshPermsEditable——applyMode から
    // 呼ばれる——が書ける形でだけ表示する）。2026-10-12 までは編集ロックが要るので編集モードだけだった。いまはロックを
    // 取らない（サーバーの RequireEditLock は、道具が /api/lock で握っているときだけ断る）。
    async function deletePage() {
        if (!currentPageId) return;
        // ほかの人が開いていれば、確かめのダイアログに名前を出す（2026-10-12 利用者が選んだ形——それでも消せる）。
        let others = [];
        try {
            const w = await fetch('/api/page-watchers?id=' + currentPageId + '&tab=' + TAB_ID);
            if (w.ok) others = ((await w.json()).users || []);
        } catch (e) { /* 分からなければ名前を出さずに確かめる */ }
        const opened = others.length ? '「' + others.join('」「') + '」さんがこのページを開いています。\n' : '';
        if (!confirm(opened + 'このページをゴミ箱（data/trash）へ移します。よろしいですか。')) return;
        try {
            await flushPendingSave();
            const res = await fetch('/api/delete-page?id=' + currentPageId, { method: 'POST' });
            if (!res.ok) {
                notify('削除できませんでした: ' + (await res.text()).trim(), { type: 'warn', duration: 10000 });
                return;
            }
            // 消えたページに留まらせない。親があれば親へ、無ければトップへ。
            const parent = document.getElementById('w-pi-parent-input').value.trim();
            location.href = '/' + (parent || '000000');
        } catch (e) {
            notify('削除できませんでした: ' + e.message, { type: 'warn', duration: 10000 });
        }
    }

    // saveToServer は書きかけを送る（約束を返す——書ける形を出る側・業務のボタンが「送り終えるまで待つ」ため）。
    // 送るのは1本ずつ（前の応答で元が変わるので、重ねて送らない——syncChain）。
    function saveToServer() {
        if (!document.body.hasAttribute('edit-mode')) return Promise.resolve(); // 書ける形のときだけ保存
        // 語彙が未取得のまま保存すると、本文が丸ごと欠落したものを書いてしまう。
        // **黙って見送ってはいけない**——打った内容が保存されないまま「保存済」が
        // 出続け、その回の入力がまるごと失われる（2026-08-21 修正）。
        if (!tagSchema) {
            console.warn('本文の語彙が未取得のため保存を見送りました');
            setSaveStatus("⚠ 保存できません", "#ef4444");
            notify('本文の語彙を取得できていないため保存できません。ページを再読み込みしてください。' +
                '（書きかけは失われます——先に内容をコピーしてください）',
                { type: 'warn', duration: 0, id: 'no-vocab' });
            return Promise.resolve();
        }
        return queueSync(false);
    }

    // pullFromServer は、変えていなくても送って、相手の変更を受け取る（ページの更新の知らせを受けたとき・業務のボタンのあと）。
    function pullFromServer() {
        if (!document.body.hasAttribute('edit-mode')) return Promise.resolve();
        return queueSync(true);
    }

    // flushPendingSave は、まだ保存していない書きかけを今すぐ送って終わるまで待つ（業務のボタン・書ける形を出る前・
    // タブを隠す／ページを去るとき）。
    async function flushPendingSave() {
        try { await saveToServer(); } catch (e) { /* 失敗は onSaveFailed が知らせている */ }
    }
    // saveNow は、操作（帯・メニュー・⠿ 🗑 ＋）・アンドゥとリドゥ・ぶつかりを決めたあとにすぐ保存する（ブロックの形を変えるので、
    // ブロックを去るのを待たない——2026-10-12 昼）。
    const saveNow = () => { flushPendingSave(); };
    window.wcmsFlushSave = flushPendingSave; // 鏡のボタン（postJSON）が押す前に呼ぶ
    // 書きかけ（まだ保存していない変更・確かめ待ちのぶつかり）があるか——セッションが切れたとき、session.js が
    // ログインへ移らずにその場に残すかを決める（移ると書きかけが消える・2026-10-14）。
    window.wcmsHasUnsaved = () => unsaved || syncConflicts.size > 0;

    // checkpointVersion は版の区切りを打つ（このタブが書いていれば——2026-10-12）。それまでは編集権を手放したときにサーバーが
    // 打っていた（同じ人の10分以内の保存は1つの版にまとまるので、書き終えたときに区切らないと最後の形が独立した版に残らない）。
    // beacon はページを離れるとき（応答を待たない）。
    function checkpointVersion(beacon) {
        if (!syncWrote || !currentPageId) return Promise.resolve();
        syncWrote = false;
        const url = '/api/version-checkpoint?id=' + currentPageId;
        if (beacon && navigator.sendBeacon) { navigator.sendBeacon(url); return Promise.resolve(); }
        return fetch(url, { method: 'POST' }).catch(() => {});
    }
    window.addEventListener('pagehide', () => checkpointVersion(true));

    function queueSync(pull) {
        const run = syncChain.then(() => runSync(pull));
        syncChain = run.catch(() => {});
        return run;
    }

    // runSync は1回送って、応答を画面へ当てる。
    async function runSync(pull) {
        if (!document.body.hasAttribute('edit-mode') || !syncReady) return;
        // 上の段の ID を揃え、入れ子へは**変えたブロックの中だけ**振る（見ただけのページを書き換えない——ensureBlockIds）。
        captureAll(); // まだ拾っていない自分の変更を先にコマンドにする（このあと振る ID と混ぜない）
        ensureBlockIds(new Set());
        let blocks = serializeBlocks();
        const changedEls = blocks.filter(b => b.id && !syncConflicts.has(b.id) &&
            (!syncBase.has(b.id) || syncBase.get(b.id).html !== b.html)).map(b => b.el);
        if (changedEls.length) {
            // ⚠ 入れ子へ振った ID は利用者の変更ではない——アンドゥの記録へ写す（2026-10-12 利用者:「アンドゥし続けて、アンドゥバッファが
            //    空になると、リドゥバッファも空になっていませんか？」——節の中を直して保存すると、ここで節の中の要素に ID が振られ、
            //    止まったときの拾いがそれを「変更」のコマンドに積んで、やり直しを消していた。直した直後の Ctrl+Z も、まずこれを戻していた）。
            const prior = new Map(changedEls.map(el => [el, serializeBlock(el)]));
            if (ensureBlockIds(new Set(changedEls)) > 0) {
                blocks = serializeBlocks();
                adoptMachineEdits(changedEls.map(el => ({ id: el.getAttribute(BLOCK_ID_ATTR), before: prior.get(el), after: serializeBlock(el) }))
                    .filter(x => x.id && x.before !== x.after));
            }
        }
        document.getElementById('w-html-preview').value = joinBlocks(blocks);
        const sent = new Map();
        const order = [];
        const changes = [];
        // ⚠ サーバーの並び（syncOrder）に無いブロックは、元（syncBase）が残っていても**足したブロックとして中身ごと送る**
        //    （2026-10-12——🗑 で消したブロックをアンドゥで戻すと、元と同じ中身なので「変わっていない」と見なして送らず、
        //    サーバーは並びの中の知らない ID を捨て、画面からも4秒後に消えていた。版の「このブロックだけ戻す」も同じ道）。
        const known = new Set(syncOrder);
        // ⚠ 受け取りに行くとき（pull——相手の保存の知らせ）は、**書いている最中のブロック**（キャレットがあり、まだ保存していない）を
        //    送らない（2026-10-12 昼 利用者:「保存は、ブロックを去る時とページを去る時に限定して、細かい通信を抑制してはどうでしょう？
        //    一文字打っただけで競合を検出しマージしろと言われても困ります」）。並びには入れる。相手がそのブロックを書き換えていても、
        //    いまは受け取らない（applySyncResult の held）——並べるのは、このブロックを去って送ったとき。相手が消したときだけ、
        //    その場で「消す」「残す」を聞く。
        const holdId = pull ? caretBlockId() : '';
        const held = new Set();
        const seqAtSend = editSeq;
        blocks.forEach(b => {
            if (!b.id || sent.has(b.id) || held.has(b.id)) return;
            if (b.id === holdId && !syncConflicts.has(b.id) && known.has(b.id) && syncBase.has(b.id) && syncBase.get(b.id).html !== b.html) {
                order.push(b.id);
                held.add(b.id);
                return;
            }
            sent.set(b.id, b.html);
            const c = syncConflicts.get(b.id);
            if (c && c.kind === 'deleted') return; // 「消す」「残す」を聞いているあいだは並びにも入れない
            order.push(b.id);
            if (c) return; // 並べているあいだは中身を送らない（相手の版を黙って上書きしない）
            const base = syncBase.get(b.id);
            if (!base || !known.has(b.id)) changes.push({ id: b.id, base_h: '', base_html: '', html: b.html });
            else if (base.html !== b.html) changes.push({ id: b.id, base_h: base.h, base_html: base.html, html: b.html });
        });
        const sameOrder = order.length === syncOrder.length && order.every((id, i) => id === syncOrder[i]);
        if (!pull && !changes.length && sameOrder) {
            if (editSeq === seqAtSend) unsaved = false;
            setSyncStatus();
            return;
        }
        if (changes.length || !sameOrder) setSaveStatus("保存中...", "#f59e0b");
        let res, data;
        try {
            const body = JSON.stringify({
                page_id: currentPageId,
                base: syncOrder.map(id => ({ id, h: (syncBase.get(id) || {}).h || '' })),
                order, changes, tab: TAB_ID,
            });
            // タブを隠したとき・ページを去るときの保存は、ページが閉じても届くように keepalive（中身が 64KB を超えると使えない）。
            // ⚠ ページを去るとき（pagehide）は、visibilityState がまだ visible のことがある——印（leavingPage）でも見る。
            const keepalive = (document.visibilityState === 'hidden' || leavingPage) && body.length < 60000;
            // 受け取りに行くだけ（自分の変更を送らない）ときは、知らせを受けて自動に飛ぶ要求——セッションを延ばさない（2026-10-14）。
            const headers = (pull && !changes.length) ? Object.assign({ 'Content-Type': 'application/json' }, BG_HEADERS)
                : { 'Content-Type': 'application/json' };
            res = await fetch('/api/sync', { method: 'POST', headers, body, keepalive });
            data = await readResult(res);
        } catch (e) { onSaveFailed(e); return; }
        if (!res.ok || !data || !data.success) {
            onSaveFailed();
            notify('保存できませんでした: ' + failMessage(res, data), { type: 'warn', duration: 10000, id: 'save-failed' });
            return;
        }
        dismissToast('save-failed');
        unsaved = held.size > 0 || editSeq !== seqAtSend;
        applySyncResult(data, sent, held);
        if (data.written) syncWrote = true;
        notifyUnknownTypes(data.unknown_types);
        notifyUnresolvedFields(data.unresolved_fields);
        notifyStrippedIDs(data.stripped_ids);
        setSyncStatus();
    }

    // setSyncStatus は保存の表示を、いまの状態（並べているブロックがあるか・書きかけが残っているか）に合わせる。
    function setSyncStatus() {
        if (syncConflicts.size) { setSaveStatus("⚠ 確かめ待ち（" + syncConflicts.size + "）", "#ef4444"); return; }
        if (unsaved) { setSaveStatus("未保存", "#94a3b8"); return; }
        setSaveStatus("✅ 保存済", "#10b981");
    }

    // blockElementById は上の段のブロックを ID で探す。
    function blockElementById(id) {
        return topLevelBlocks().find(el => el.getAttribute(BLOCK_ID_ATTR) === id) || null;
    }

    // applySyncResult はサーバーの応答（仕上がりの並び・指紋・描き直すブロック・ぶつかり）を画面へ当てる。
    //   - 描き直すブロック（相手が変えた・足した・合わせた）は、送ったあとにまた書いていれば**その場で並べる**（問い3）。
    //     書いていなければ差し替える（相手の変更はアンドゥの記録へ写す——アンドゥは自分の変更だけを戻す・問い2）。
    //     書いている最中で送らなかったブロック（held）は受け取らない——並べるのは去って送ったとき（2026-10-12 昼）。
    //   - 仕上がりに無いブロック（相手が消した）は、送ったあとに書いていれば「消す」「残す」を聞く（問い6）。
    //   - 並びは仕上がりの並びにする（聞いているブロック・送ったあとに足したブロックはいまの位置の近くに残す）。
    function applySyncResult(data, sent, held) {
        held = held || new Set();
        // 相手の変更を当てる前に、自分のまだ拾っていない変更をコマンドにする（当てたあとは「相手の変更」として記録へ写すので、
        // 混ざると自分の変更がアンドゥできなくなる——2026-10-12）。
        captureAll();
        applySavedMeta(data);
        if (data.updated_at) loadedUpdatedAt = data.updated_at; // つなぎ直したときの見分け（openPageEvents）
        const live = new Map();
        topLevelBlocks().forEach(el => {
            const id = el.getAttribute(BLOCK_ID_ATTR);
            if (id && !live.has(id)) live.set(id, el);
        });
        // 送ったあとにまた書いた・書いている最中で送らなかった（held——ここで効くのは相手が消したときだけ）ブロック。
        const typedSince = id => live.has(id) && (sent.has(id) ? serializeBlock(live.get(id)) !== sent.get(id) : held.has(id));
        const conflictIds = new Set((data.conflicts || []).map(c => c.id));
        const finalIds = (data.blocks || []).map(b => b.id);
        const finalSet = new Set(finalIds);
        const remote = []; // 相手の変更（アンドゥの履歴へ書き写す）: { id, before, after }
        const fresh = new Map(); // 新しく作った要素
        const flashed = [];
        let sanitizedSeen = false;
        const sanitizedList = []; // サニタイズで差し替えたブロック: { id, before, after }（アンドゥの記録の「後」を直す）
        const restoreCaretSoon = []; // 差し替えたブロックにあったキャレット（applyMode のあとで戻す）
        let touched = false;
        const wasQuiet = quietDomChange, wasSuppressed = suppressSnapshot;
        quietDomChange = true;
        suppressSnapshot = true;
        try {
            (data.conflicts || []).forEach(c => openConflict(c.id, c));
            (data.blocks || []).forEach(b => {
                if (conflictIds.has(b.id) || syncConflicts.has(b.id)) return;
                // 書いている最中で送らなかったブロック（held）を相手が書き換えていた——いまは受け取らない。並べるのは、このブロックを
                // 去って送ったとき（表の違うセルならそこで黙って合わさる）。元（syncBase）も進めない——送るときに3者で比べるため
                // （2026-10-12 昼 利用者:「一文字打っただけで競合を検出しマージしろと言われても困ります」——早めの知らせは黄色の背景）。
                // 相手が消したときは、いままでどおりその場で「消す」「残す」を聞く（下）。
                if (held.has(b.id) && b.html) return;
                const el = live.get(b.id);
                if (!b.html) {
                    // 画面の書き出しのまま当たった・変わっていない——元を進める（中身は送ったときの書き出し）。
                    const prev = syncBase.get(b.id);
                    const html = sent.has(b.id) ? sent.get(b.id) : (prev ? prev.html : (el ? serializeBlock(el) : ''));
                    syncBase.set(b.id, { h: b.h, html });
                    return;
                }
                if (el && typedSince(b.id)) {
                    if (b.why === 'sanitized') { syncBase.set(b.id, { h: b.h, html: sent.get(b.id) }); return; }
                    openConflict(b.id, { id: b.id, kind: 'changed', html: b.html, h: b.h, by: data.by || '' });
                    return;
                }
                // キャレットのあるブロックはサニタイズでは差し替えない（それまでの保存と同じ——差し替えるとキャレットと折りたたみの
                // 開閉が飛ぶ）。元は送ったときの書き出しのまま——次に送ればまたサニタイズされる。
                const focused = el && el.contains(document.activeElement);
                if (focused && b.why === 'sanitized') { syncBase.set(b.id, { h: b.h, html: sent.get(b.id) }); return; }
                const node = elementFromHTML(b.html);
                if (!node) return;
                const before = el ? serializeBlock(el) : null;
                const caret = focused ? captureCaret() : null;
                if (el) {
                    if (el.tagName === node.tagName && el.tagName === 'DETAILS') node.open = el.open; // 開閉は見た目——保存されない
                    el.replaceWith(node);
                    if (caret) restoreCaretSoon.push(caret);
                } else {
                    fresh.set(b.id, node);
                }
                const after = serializeBlock(node);
                syncBase.set(b.id, { h: b.h, html: after });
                if (b.why !== 'sanitized' && b.why !== 'full') remote.push({ id: b.id, before, after });
                if (b.why === 'other' || b.why === 'new' || b.why === 'kept' || b.why === 'merged') flashed.push(node);
                if (b.why === 'sanitized') { sanitizedSeen = true; sanitizedList.push({ id: b.id, before, after }); }
                touched = true;
            });
            // 仕上がりに無いブロック——相手が消した。
            live.forEach((el, id) => {
                if (finalSet.has(id) || syncConflicts.has(id) || (!sent.has(id) && !held.has(id))) return;
                if (!syncBase.has(id)) return; // 足したのに入らなかった（サニタイズで空になった）——そのまま残す
                if (typedSince(id)) { openConflict(id, { id, kind: 'deleted', by: '' }); return; }
                remote.push({ id, before: serializeBlock(el), after: null });
                (el.closest('.editor-block') || el).remove();
                syncBase.delete(id);
                touched = true;
            });
            if (placeBlocks(finalIds, fresh)) touched = true;
            // 仕上がりに無いブロックの元は捨てる（聞いているものは残す）——残すと、あとで同じ中身で戻ったときに送られない。
            Array.from(syncBase.keys()).forEach(id => { if (!finalSet.has(id) && !syncConflicts.has(id)) syncBase.delete(id); });
            syncOrder = finalIds.filter(id => !syncConflicts.has(id) || syncConflicts.get(id).kind !== 'deleted');
        } finally {
            quietDomChange = wasQuiet;
            suppressSnapshot = wasSuppressed;
        }
        if (touched) {
            applyMode();
            buildToc();
            updateHtmlPreview();
            // 相手の変更は記録へ写す（コマンドにしない——アンドゥは自分の変更だけを戻す・問い2）。
            if (remote.length) adoptExternal(remote.map(r => r.id));
            // サニタイズで除いたあとの姿を、そのブロックを最後に変えたコマンドの「後」にする（やり直しで除く前の危険な中身へ戻れない）。
            if (sanitizedSeen) { notifySanitized(); adoptMachineEdits(sanitizedList); }
            restoreCaretSoon.forEach(c => restoreCaret(c));
            flashed.forEach(n => {
                const w = n.closest('.editor-block') || n;
                w.classList.add('w-block-updated');
                setTimeout(() => w.classList.remove('w-block-updated'), 4000);
            });
        }
        if ((data.kept || []).length) {
            const by = data.kept_by || {};
            const names = Array.from(new Set(data.kept.map(id => by[id]).filter(Boolean)));
            notify((names.length ? '「' + names.join('」「') + '」さん' : 'ほかの人') +
                'が書き換えていたブロック（' + data.kept.length + ' か所）は消さずに残しました。消すなら、読んでからもう一度消してください。',
                { type: 'warn', duration: 12000, id: 'sync-kept' });
        }
    }

    // placeBlocks は上の段を ids の並びにする（fresh は新しく作った要素）。並びに無いブロック（聞いているもの・送ったあとに
    // 足したもの）は、いまのすぐ前のブロックの後ろに残す。動かしたら true。
    function placeBlocks(ids, fresh) {
        const editor = document.getElementById('w-editor-content');
        const live = topLevelBlocks();
        const byId = new Map();
        live.forEach(el => { const id = el.getAttribute(BLOCK_ID_ATTR); if (id && !byId.has(id)) byId.set(id, el); });
        const seq = [];
        ids.forEach(id => { const el = fresh.get(id) || byId.get(id); if (el && !seq.includes(el)) seq.push(el); });
        let anchor = null;
        live.forEach(el => {
            if (seq.includes(el)) { anchor = el; return; }
            const at = anchor ? seq.indexOf(anchor) + 1 : 0;
            seq.splice(at, 0, el);
            anchor = el;
        });
        let moved = false;
        let prev = null;
        seq.forEach(el => {
            let wrap = el.closest('.editor-block');
            if (!wrap || !editor.contains(wrap)) {
                if (prev) prev.after(el); else editor.insertBefore(el, editor.firstChild);
                wrap = wrapInBlock(el);
                moved = true;
            } else if (prev ? prev.nextElementSibling !== wrap : editor.firstElementChild !== wrap) {
                if (prev) prev.after(wrap); else editor.insertBefore(wrap, editor.firstChild);
                moved = true;
            }
            prev = wrap;
        });
        return moved;
    }

    // ── ぶつかったブロックを並べる（問い4・問い6）──────────────────────────────
    //
    // 相手の版を**上に読むだけで**（色の枠で）出し、下の自分の版はそのまま書ける。「これで決める」「自分の版にする」は
    // 自分の版を相手の版の上に送り直し（元を相手の版にしてある）、「相手の版にする」は相手の版へ差し替える。
    // 相手が消したブロックは「消す（相手に合わせる）」「残す」を聞く。
    // ⚠ 枠は .editor-block の中・.block-content の外へ置く——.block-content の中に置くと上の段のブロックに数えられる。
    function openConflict(id, c) {
        const el = blockElementById(id);
        if (!el) return;
        closeConflict(id);
        const wrap = el.closest('.editor-block');
        const who = c.by ? '「' + c.by + '」さん' : 'ほかの人';
        const panel = document.createElement('div');
        panel.className = 'vocab-chrome w-conflict';
        panel.contentEditable = 'false';
        const head = document.createElement('p');
        head.className = 'w-conflict-head';
        const actions = document.createElement('div');
        actions.className = 'w-conflict-actions';
        const button = (label, cls, fn) => {
            const b = document.createElement('button');
            b.type = 'button';
            b.className = 'w-conflict-btn ' + cls;
            b.textContent = label;
            b.addEventListener('click', fn);
            actions.appendChild(b);
        };
        let theirs = null;
        if (c.kind === 'deleted') {
            head.textContent = '⚠ ' + who + 'がこのブロックを消しました。あなたの書きかけを残しますか？';
            button('消す（相手に合わせる）', 'w-conflict-drop', () => resolveConflict(id, 'drop'));
            button('残す', 'w-conflict-keep', () => resolveConflict(id, 'keep'));
            syncBase.delete(id);
        } else {
            head.textContent = '⚠ ' + who + 'がこのブロックを書き換えました。上が相手の版、下があなたの版です。' +
                '相手の書いたことを取り込んで「これで決める」を押してください。';
            theirs = elementFromHTML(c.html || '');
            const box = document.createElement('div');
            box.className = 'w-conflict-theirs';
            if (theirs) box.appendChild(theirs);
            panel.appendChild(head);
            panel.appendChild(box);
            button('これで決める', 'w-conflict-done', () => resolveConflict(id, 'mine'));
            button('相手の版にする', 'w-conflict-theirs-btn', () => resolveConflict(id, 'theirs'));
            button('自分の版にする', 'w-conflict-mine', () => resolveConflict(id, 'mine'));
            // 元を相手の版にする——「これで決める」で送る自分の版は、相手の版を見たうえでのもの。
            syncBase.set(id, { h: c.h, html: theirs ? serializeBlock(theirs) : '' });
        }
        if (!panel.contains(head)) panel.appendChild(head);
        panel.appendChild(actions);
        if (wrap) {
            const content = wrap.querySelector(':scope > .block-content');
            wrap.insertBefore(panel, content || wrap.firstChild);
            wrap.classList.add('w-conflict-block');
        } else {
            el.before(panel);
        }
        syncConflicts.set(id, { kind: c.kind, panel, theirsHTML: c.html || '', h: c.h || '' });
        notify('⚠ ' + who + 'と同じブロックを書いていました。ページの中の黄色い枠で、どちらにするか決めてください。',
            { type: 'warn', duration: 12000, id: 'sync-conflict' });
        setSyncStatus();
    }

    function closeConflict(id) {
        const c = syncConflicts.get(id);
        if (!c) return;
        if (c.panel) {
            const wrap = c.panel.closest('.editor-block');
            if (wrap) wrap.classList.remove('w-conflict-block');
            c.panel.remove();
        }
        syncConflicts.delete(id);
    }

    // resolveConflict は並べたブロックを決める（mine・theirs・drop・keep）。
    function resolveConflict(id, how) {
        const c = syncConflicts.get(id);
        if (!c) return;
        const el = blockElementById(id);
        flushCheckpoint();
        if (how === 'theirs' && el) {
            const node = elementFromHTML(c.theirsHTML);
            if (node) {
                el.replaceWith(node);
                syncBase.set(id, { h: c.h, html: serializeBlock(node) });
            }
        } else if (how === 'drop' && el) {
            (el.closest('.editor-block') || el).remove();
            syncBase.delete(id);
        } else if (how === 'keep') {
            syncBase.delete(id); // 足したブロックとして送る（前にあるブロックの後ろへ入る）
        }
        closeConflict(id);
        if (!syncConflicts.size) dismissToast('sync-conflict');
        applyMode();
        buildToc();
        triggerAutoSave();
        saveNow(); // 決めた版をすぐ正本へ
        setSyncStatus();
    }

    // ── 編集中のアンドゥ・リドゥ（コマンド——2026-10-12 に作り直した・docs/考察/【考察】アンドゥ・リドゥ.md §3）────────────
    //
    // このエディタは contenteditable に加えプログラム的なDOM操作を大量に行う
    // （ブロックの挿入・並べ替え・置換、明細の追加、カスタム要素の再描画）。
    // ブラウザ標準の undo は execCommand とネイティブ入力しか捕捉しないため、
    // それらを戻せず不整合になる。そこで独自の履歴を持つ。
    //
    // 利用者:「アンドゥが効いていないようです。まずアンドゥ・リドゥを本格化しましょう。コマンドパターンを使った実装はどうでしょう？」
    // 「一文字ずつコマンドを収集し、同じコマンドが来たら結合して圧縮していくのは、ブラウザでは処理が追いつきませんか？」
    // 「日本語入力で壊れやすいのであれば、日本語文字列が確定したときにコマンドとして収集すればよいのでは？」（2026-10-12）。
    //
    // 履歴は**コマンド**の列——1つのコマンドは「変わった上の段のブロックの前と後（changes: [{id, before, after}]——無かった・
    // 消えたは null）・並びの前と後（変わったときだけ）・キャレットの前と後・名前（帯の ↶ の説明に出る）」。戻すときは
    // **そのコマンドが変えたブロックだけ**を前へ戻す（ほかの人が書いたブロックには触らない——アンドゥは自分の変更だけ・
    // 【考察】編集モードの再考 §6 問い2）。それまでの「ページ全体の写しを積み、相手の変更を写し込む」形はやめた。
    // 集め方は3つ:
    //   - **打った文字**（recordInput）——入力のたびに、キャレットのあるブロックの前と後を1つのコマンドにする（ブロックごとに
    //     書ける欄が分かれているので、ブラウザの入力が変えるのはそのブロックだけ——1打鍵に書き出すのは1ブロック）。続けて打った
    //     同じ種類（打つ・消す）は結合する——同じブロック・キャレットが続いている・止まったのが CMD_MERGE_GAP_MS 以内。
    //     **日本語は変換を確定したときに1つ**（変換中はブラウザに任せて結合し、確定〔compositionend〕でコマンドを閉じる）。
    //   - **操作**（帯・メニュー・ブロックの ⠿ 🗑 ＋ など本文の外のボタン）——押した操作が終わったら全体を記録と比べてコマンドに
    //     する（名前は押したボタンの説明か文字・captureAll）。
    //   - **取りこぼさない**——上のどちらでもない変更も、操作の前（Enter・Ctrl の組み合わせ・貼り付け・ドラッグ・本文の外を押す）
    //     と、止まったとき（CAPTURE_IDLE_MS）に「変更」として拾う。
    // 相手の変更（ほかの人・業務のボタン・サーバーのサニタイズ）はコマンドにせず、記録（committed）だけをいまの中身に合わせる
    // （adoptExternal）。戻そうとしたブロックを相手が書き換えていたら（いまの中身がコマンドの後と違う）、黙って戻さずに並べる。
    const HISTORY_LIMIT = 300;
    const CMD_MERGE_GAP_MS = 1000;   // 打つのがこれより長く止まったら、次の打鍵は新しいコマンド
    const CAPTURE_IDLE_MS = 700;     // 本文が変わってこれだけ止まったら、まだ拾っていない変更を拾う
    let undoStack = [];              // コマンドの列（末尾が最新）
    let redoStack = [];
    let committed = new Map();       // ID → いちばん新しいコマンドのあとの書き出し（記録——相手の変更もここへ写す）
    let committedOrder = [];         // 上の段の ID の並び（記録）
    let suppressSnapshot = false;    // 機械が戻している・相手の変更を当てている途中は集めない

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

    // quietDomChange は、機械が要素を差し替えている途中の印（focusout の保存を止める）。
    let quietDomChange = false;

    // ── コマンドを集める（2026-10-12）──────────────────────────────────────────
    // 戻すときは**変わったブロックだけ**差し替える（reconcileBlocks——2026-10-07 から）: 同じ HTML の並びを最長で残し、動いた
    // ブロックは要素ごと動かし、同じ ID と種類のブロックは中身を合わせ、前にあったブロックはそのときの要素を使い回す
    // （サーバーが描いた中身——ファイル表示の枠・鏡——ごと戻る）。
    const KNOWN_ELEMENTS_LIMIT = 400;
    const knownElements = new Map(); // 書き出した HTML → そのときの上の段の要素（戻すときに使い回す・古いものから捨てる）
    let captureTimer = null;
    let composing = false;           // 日本語の変換中（確定でコマンドを閉じる）
    let inputCaretBefore = null;     // 打つ直前のキャレット（beforeinput）
    let lastInputCaret = null;       // 打ったあとのキャレット（次の打鍵の直前と違えば、キャレットが飛んだ——結合しない）
    let opCaretBefore = null;        // 操作の直前のキャレット（本文の外を押す前）

    // rememberElements は、いまの上の段の要素を書き出した HTML で覚える（あとで消されても、戻すときに使い回せる）。
    function rememberElements(live) {
        live.forEach(b => {
            if (knownElements.has(b.html)) knownElements.delete(b.html); // 新しい順へ
            knownElements.set(b.html, b.el);
        });
        while (knownElements.size > KNOWN_ELEMENTS_LIMIT) knownElements.delete(knownElements.keys().next().value);
    }

    // liveState は上の段のブロックの書き出し（ID → HTML）と並び。ID の無いブロックには先に振る（上の段だけ——見ただけのページの
    // 入れ子は書き換えない）。
    function liveState() {
        ensureBlockIds(new Set());
        const blocks = serializeBlocks();
        rememberElements(blocks);
        const map = new Map();
        const order = [];
        blocks.forEach(b => { if (b.id && !map.has(b.id)) { map.set(b.id, b.html); order.push(b.id); } });
        return { map, order };
    }

    const sameOrder = (a, b) => a.length === b.length && a.every((x, i) => x === b[i]);
    const sameCaret = (a, b) => !!a && !!b && a.blockId === b.blockId && a.offset === b.offset;

    // topBlockOf は要素を含む上の段のブロックを返す（本文の外なら null）。
    function topBlockOf(node) {
        const blocks = new Set(topLevelBlocks());
        for (let el = node; el; el = el.parentElement) if (blocks.has(el)) return el;
        return null;
    }

    // closeOpenCommand は、結合を待っているコマンドを閉じる（次の打鍵は新しいコマンド）。
    function closeOpenCommand() {
        const top = undoStack[undoStack.length - 1];
        if (top) top.open = false;
    }

    // pushCommand は新しいコマンドを積む（リドゥは無くなる）。
    function pushCommand(cmd) {
        closeOpenCommand();
        undoStack.push(cmd);
        if (undoStack.length > HISTORY_LIMIT) undoStack.shift();
        redoStack = [];
        refreshUndoButtons();
        saveHistorySoon();
    }

    // commitChanges は記録をコマンドのあとの中身へ進める。
    function commitChanges(changes, order) {
        changes.forEach(c => { if (c.after === null) committed.delete(c.id); else committed.set(c.id, c.after); });
        if (order) committedOrder = order.slice();
    }

    // captureAll は記録といまを比べ、違いがあれば1つのコマンドにする（label は名前——無ければ「変更」）。積んだら true。
    // 変換中は拾わない（変換中の字を記録にすると、確定のコマンドが途中の字から始まる）——止まったときにまた試す。
    function captureAll(label) {
        clearTimeout(captureTimer);
        captureTimer = null;
        if (!document.body.hasAttribute('edit-mode') || suppressSnapshot || !syncReady) return false;
        if (composing) { scheduleCapture(); return false; }
        const st = liveState();
        const changes = [];
        st.order.forEach(id => {
            const before = committed.has(id) ? committed.get(id) : null;
            const after = st.map.get(id);
            if (before !== after) changes.push({ id, before, after });
        });
        committed.forEach((html, id) => { if (!st.map.has(id)) changes.push({ id, before: html, after: null }); });
        const orderChanged = !sameOrder(st.order, committedOrder);
        if (!changes.length && !orderChanged) return false;
        const caret = captureCaret();
        pushCommand({
            label: label || '変更', kind: 'op', changes,
            orderBefore: orderChanged ? committedOrder.slice() : null, orderAfter: orderChanged ? st.order.slice() : null,
            caretBefore: opCaretBefore || caret, caretAfter: caret, t: Date.now(), open: false,
        });
        commitChanges(changes, st.order);
        opCaretBefore = null;
        return true;
    }

    // scheduleCapture は、本文が変わって止まったら拾う（変わるたびに待ち直す）。
    function scheduleCapture() {
        if (!document.body.hasAttribute('edit-mode') || suppressSnapshot) return;
        clearTimeout(captureTimer);
        captureTimer = setTimeout(() => captureAll(), CAPTURE_IDLE_MS);
        refreshUndoButtons();
    }

    // flushCheckpoint は、操作の直前に打った分を拾い、結合を閉じる（打った文字と操作を別のコマンドにする）。
    // 操作の直前のキャレットも覚える（戻したときにそこへ置く）。
    function flushCheckpoint() {
        closeOpenCommand();
        captureAll();
        opCaretBefore = captureCaret();
    }

    // inputKind は入力の種類（'type' 打つ・'delete' 消す・'other' そのほか——結合しない）。
    function inputKind(e) {
        if (e.isComposing || composing) return 'type';
        const t = e.inputType || '';
        if (t === 'insertText' || t === 'insertReplacementText' || t === 'insertCompositionText') return 'type';
        if (/^delete(Content|Word|Soft|Hard)/.test(t)) return 'delete';
        return 'other';
    }
    const KIND_LABEL = { type: '入力', delete: '文字を消す', other: '変更' };

    // recordInput は1回の入力をコマンドにする——前のコマンドが同じ種類・同じブロック・続いているなら結合する。
    function recordInput(e) {
        if (!document.body.hasAttribute('edit-mode') || suppressSnapshot || !syncReady) return;
        const block = e.target instanceof Element ? topBlockOf(e.target) : null;
        if (!block) { scheduleCapture(); return; }
        if (!block.getAttribute(BLOCK_ID_ATTR)) ensureBlockIds(new Set());
        const id = block.getAttribute(BLOCK_ID_ATTR);
        // 記録に無いブロック（足したばかりでまだ拾っていない）——まとめて拾う（足したことごと1つのコマンド）。
        if (!id || !committed.has(id)) { captureAll(); return; }
        const after = serializeBlock(block);
        const before = committed.get(id);
        if (after === before) return;
        const kind = inputKind(e);
        const caret = captureCaret();
        const now = Date.now();
        const top = undoStack[undoStack.length - 1];
        const continued = composing || e.isComposing || sameCaret(inputCaretBefore, lastInputCaret);
        if (top && top.open && kind !== 'other' && top.kind === kind && top.changes.length === 1 && top.changes[0].id === id &&
            !top.orderAfter && continued && (composing || e.isComposing || now - top.t <= CMD_MERGE_GAP_MS)) {
            top.changes[0].after = after;
            top.caretAfter = caret;
            top.t = now;
            redoStack = [];
            refreshUndoButtons();
            saveHistorySoon();
        } else {
            pushCommand({
                label: KIND_LABEL[kind], kind, changes: [{ id, before, after }], orderBefore: null, orderAfter: null,
                caretBefore: inputCaretBefore || caret, caretAfter: caret, t: now, open: kind !== 'other',
            });
        }
        committed.set(id, after);
        lastInputCaret = caret;
    }

    // elementFromHTML は書き出した1ブロックの HTML から要素を作る（populateEditor と同じく DOMParser——スクリプトは走らない）。
    function elementFromHTML(html) {
        const el = new DOMParser().parseFromString(html, 'text/html').body.firstElementChild;
        return el ? document.importNode(el, true) : null;
    }

    // morphInto は画面の要素 live の中身を、記録から作った要素 target に合わせる——**作り直さない**ので、サーバーが描いた中身
    // （.vocab-chrome——表の鏡の列・節の中のファイル表示の枠）は比べずにその場に残る。属性のうち、表示のために画面が付けたもの
    // （class・contenteditable など——書き出しには出ない）は残す（applyMode と飾り付けが付け直す）。
    const MORPH_KEEP_ATTRS = new Set(['class', 'contenteditable', 'style', 'spellcheck', 'draggable', 'tabindex']);
    const isChromeNode = n => n.nodeType === 1 && n.classList.contains('vocab-chrome');
    function morphInto(live, target) {
        Array.from(live.attributes).forEach(a => {
            if (!target.hasAttribute(a.name) && !MORPH_KEEP_ATTRS.has(a.name)) live.removeAttribute(a.name);
        });
        Array.from(target.attributes).forEach(a => { if (live.getAttribute(a.name) !== a.value) live.setAttribute(a.name, a.value); });
        const lk = Array.from(live.childNodes).filter(n => !isChromeNode(n) && (n.nodeType === 1 || n.nodeType === 3));
        const tk = Array.from(target.childNodes).filter(n => n.nodeType === 1 || n.nodeType === 3);
        let i = 0;
        for (; i < tk.length; i++) {
            const t = tk[i], l = lk[i];
            if (l && l.nodeType === t.nodeType && (t.nodeType === 3 || (l.tagName === t.tagName && !isChromeNode(t)))) {
                if (t.nodeType === 3) { if (l.nodeValue !== t.nodeValue) l.nodeValue = t.nodeValue; }
                else morphInto(l, t);
                continue;
            }
            // 種類が違う——記録の側の要素を入れる（後ろのサーバーの中身より前へ）。
            const node = document.importNode(t, true);
            if (l) live.insertBefore(node, l);
            else {
                const tail = Array.from(live.childNodes).reverse().find(n => !isChromeNode(n));
                if (tail) tail.after(node); else live.insertBefore(node, live.firstChild);
            }
            lk.splice(i, 0, node);
        }
        for (; i < lk.length; i++) lk[i].remove(); // 記録に無い子は外す（サーバーの中身はそのまま）
    }

    // reconcileBlocks は上の段のブロックを target（[{html}]）の並びにする——変わったブロックだけ差し替える。
    function reconcileBlocks(target) {
        const editor = document.getElementById('w-editor-content');
        const live = serializeBlocks();
        const want = target.map(b => b.html);
        const n = live.length, m = want.length;
        // 同じ HTML の並びを最長で残す（LCS）——残すブロックは動かさない（PDF の枠を読み直させない）。
        const L = Array.from({ length: n + 1 }, () => new Uint32Array(m + 1));
        for (let i = n - 1; i >= 0; i--) {
            for (let j = m - 1; j >= 0; j--) L[i][j] = live[i].html === want[j] ? L[i + 1][j + 1] + 1 : Math.max(L[i + 1][j], L[i][j + 1]);
        }
        const pick = new Array(m).fill(null);
        const used = new Set();
        for (let i = 0, j = 0; i < n && j < m;) {
            if (live[i].html === want[j]) { pick[j] = live[i].el; used.add(live[i].el); i++; j++; }
            else if (L[i + 1][j] >= L[i][j + 1]) i++;
            else j++;
        }
        // 残さなかったブロック——並びの外で同じ中身のもの（動いたブロック）は要素ごと動かす。
        const spare = new Map();
        live.forEach(b => { if (!used.has(b.el) && !spare.has(b.html)) spare.set(b.html, b.el); });
        // 同じブロック（同じ ID・同じ種類）で中身だけ変わったものは、作り直さずに中身を合わせる（morphInto）。
        const byId = new Map();
        live.forEach(b => { if (!used.has(b.el) && b.id && !byId.has(b.id)) byId.set(b.id, b.el); });
        for (let j = 0; j < m; j++) {
            if (pick[j]) continue;
            let el = spare.get(want[j]);
            if (el && used.has(el)) el = null; // 中身を合わせて使った要素は二度使わない
            if (el) spare.delete(want[j]);
            if (!el && target[j].id && byId.has(target[j].id)) {
                const cand = byId.get(target[j].id);
                const t = elementFromHTML(want[j]);
                if (!used.has(cand) && t && t.tagName === cand.tagName) {
                    morphInto(cand, t);
                    el = cand;
                }
            }
            if (!el) {
                // 前にあった要素（消したブロック）——中身が変わっていなければ、サーバーが描いた中身ごと使い回す。
                const old = knownElements.get(want[j]);
                if (old && !old.isConnected && !used.has(old) && serializeBlock(old) === want[j]) el = old;
            }
            if (!el) el = elementFromHTML(want[j]);
            if (!el) continue;
            pick[j] = el;
            used.add(el);
        }
        // 使わなかったブロックを外す（覚えておく——やり直しで使い回す）。
        live.forEach(b => {
            if (used.has(b.el)) return;
            knownElements.set(b.html, b.el);
            (b.el.closest('.editor-block') || b.el).remove();
        });
        // 並べる——いる場所にあるものは動かさない。
        let prev = null;
        pick.forEach(el => {
            if (!el) return;
            let wrap = el.closest('.editor-block');
            if (!wrap || !editor.contains(wrap)) {
                if (prev) prev.after(el); else editor.insertBefore(el, editor.firstChild);
                wrap = wrapInBlock(el);
            } else if (prev ? prev.nextElementSibling !== wrap : editor.firstElementChild !== wrap) {
                if (prev) prev.after(wrap); else editor.insertBefore(wrap, editor.firstChild);
            }
            prev = wrap;
        });
    }

    // mergeOrder は desired の並びを守り、desired に無い ID（あとから相手が足したブロック）は current でのすぐ前のブロックの後ろに残す。
    function mergeOrder(current, desired) {
        const seq = desired.slice();
        const inDesired = new Set(desired);
        let anchor = null;
        current.forEach(id => {
            if (inDesired.has(id)) { anchor = id; return; }
            const at = anchor ? seq.indexOf(anchor) + 1 : 0;
            seq.splice(at, 0, id);
            anchor = id;
        });
        return seq;
    }

    // applyCommand はコマンドを戻す（forward が偽）・やり直す（真）。触るのは**そのコマンドが変えたブロックだけ**。
    //   - いまの中身がコマンドの側（戻すなら後・やり直すなら前）と同じ——そのまま差し替える。
    //   - 違う（相手が書き換えた）——自分の版に差し替えて、相手の版（いまの中身）を上に並べる（ぶつかったときと同じ枠・
    //     2026-10-12 利用者が選んだ形——相手の書き込みは黙って消さない）。消すはずのブロックを相手が書き換えていたら消さない。
    function applyCommand(cmd, forward) {
        suppressSnapshot = true;
        clearTimeout(captureTimer);
        captureTimer = null;
        const theirs = [];
        let keptOne = false;
        try {
            const st = liveState();
            const want = new Map();
            cmd.changes.forEach(c => {
                const from = forward ? c.before : c.after;
                const to = forward ? c.after : c.before;
                const cur = st.map.has(c.id) ? st.map.get(c.id) : null;
                if (cur === to) return;
                if (cur !== from && cur !== null) {
                    if (to === null) { keptOne = true; return; } // 相手が書き換えたブロックは消さない
                    const base = syncBase.get(c.id);
                    const el = blockElementById(c.id);
                    theirs.push({ id: c.id, html: el ? el.outerHTML : cur, h: base ? base.h : '' });
                }
                want.set(c.id, to);
            });
            let ids = st.order.filter(id => !(want.has(id) && want.get(id) === null));
            const order = forward ? cmd.orderAfter : cmd.orderBefore;
            if (order) {
                ids = mergeOrder(ids, order.filter(id => want.has(id) ? want.get(id) !== null : st.map.has(id)));
            }
            // 並びの記録が無いのに足すブロック（ふつうは無い）——末尾に。
            want.forEach((html, id) => { if (html !== null && !ids.includes(id)) ids.push(id); });
            reconcileBlocks(ids.map(id => ({ id, html: want.has(id) ? want.get(id) : st.map.get(id) })));
            // 新しく作ったブロックには編集用の属性が無い（applyMode が付ける）——付けないと、戻した直後に文字が打てない。
            // キャレット復元より前に呼ぶ必要がある。
            applyMode();
            theirs.forEach(t => openConflict(t.id, { id: t.id, kind: 'changed', html: t.html, h: t.h, by: '' }));
            buildToc();
            restoreCaret(forward ? (cmd.caretAfter || cmd.caretBefore) : (cmd.caretBefore || cmd.caretAfter));
            updateHtmlPreview();
            validateTypedTables(); // 復元でDOMが入れ替わり実行時の印（.cell-invalid）が消えるため
            const now = liveState();
            cmd.changes.forEach(c => { if (now.map.has(c.id)) committed.set(c.id, now.map.get(c.id)); else committed.delete(c.id); });
            committedOrder = now.order.slice();
            lastInputCaret = null;
        } finally {
            suppressSnapshot = false;
        }
        if (keptOne) notify('ほかの人が書き換えていたブロックは消さずに残しました。', { type: 'warn', duration: 8000, id: 'undo-kept' });
        triggerAutoSave();
        saveNow(); // 戻した中身をすぐ正本へ（戻したブロックは変更として送られる）
        refreshUndoButtons();
        saveHistorySoon();
    }

    // adoptExternal は相手の変更（ほかの人・業務のボタン）を記録へ写す——コマンドにはしない（アンドゥで戻さない）。
    // ids は変わったブロック。並びもいまの画面に合わせる（呼ぶ前に自分の変更は拾ってある——applySyncResult の頭）。
    function adoptExternal(ids) {
        const st = liveState();
        ids.forEach(id => { if (st.map.has(id)) committed.set(id, st.map.get(id)); else committed.delete(id); });
        committedOrder = st.order.slice();
        refreshUndoButtons();
    }

    // adoptMachineEdits は、機械がブロックを書き換えた姿（サーバーのサニタイズのエコーバック・保存の前に入れ子へ振った ID）を、
    // そのブロックの「いま」を指すコマンドの側へ写す——アンドゥの列でいちばん新しいコマンドの「後」と、リドゥの列でいちばん新しい
    // （次にやり直す側の）コマンドの「前」。利用者の変更ではないのでコマンドは積まない（積むとやり直しが消える）。サニタイズは、除く前の
    // 危険な中身へやり直しで戻れないように・戻すときに相手の変更と取り違えないように。list は [{id, before, after}]。
    function adoptMachineEdits(list) {
        list.forEach(s => {
            for (let i = undoStack.length - 1; i >= 0; i--) {
                const c = undoStack[i].changes.find(x => x.id === s.id);
                if (!c) continue;
                if (c.after === s.before) c.after = s.after;
                break;
            }
            for (let i = redoStack.length - 1; i >= 0; i--) {
                const c = redoStack[i].changes.find(x => x.id === s.id);
                if (!c) continue;
                if (c.before === s.before) c.before = s.after;
                break;
            }
            if (!committed.has(s.id) || committed.get(s.id) === s.before) {
                if (s.after === null) committed.delete(s.id); else committed.set(s.id, s.after);
            }
        });
        saveHistorySoon();
    }

    // ── アンドゥの履歴を、同じタブなら読み直しても残す（2026-10-12・問い8）─────────────────
    // ブラウザのタブごとの保管（sessionStorage）——タブを閉じると消える。入りきらなければ古いものから減らす。
    // 形の印 v: 2（それまでのページ全体の写し〔v なし〕は読まない）。
    const historyKey = () => 'wcms.undo.' + currentPageId;
    let historySaveTimer = null;
    function saveHistorySoon() {
        clearTimeout(historySaveTimer);
        historySaveTimer = setTimeout(() => {
            const pack = c => ({ label: c.label, kind: c.kind, changes: c.changes, orderBefore: c.orderBefore, orderAfter: c.orderAfter,
                caretBefore: c.caretBefore || null, caretAfter: c.caretAfter || null, t: c.t });
            for (const n of [100, 30, 5]) {
                try {
                    sessionStorage.setItem(historyKey(), JSON.stringify({ v: 2, undo: undoStack.slice(-n).map(pack), redo: redoStack.slice(-n).map(pack) }));
                    return;
                } catch (e) { /* 入りきらない——減らしてもう一度 */ }
            }
            try { sessionStorage.removeItem(historyKey()); } catch (e) { /* 使えない——タブを開いているあいだは手元に残る */ }
        }, 400);
    }
    function loadSavedHistory() {
        try {
            const s = JSON.parse(sessionStorage.getItem(historyKey()) || 'null');
            if (s && s.v === 2 && Array.isArray(s.undo)) return s;
        } catch (e) { /* 読めなければ無いものとする */ }
        return null;
    }

    // rebaseHistory は、いまの画面（書ける形に入った・読み直した・版に戻した直後）を記録の起点にする。手元に履歴が無ければ、
    // このタブの保管から戻す。履歴のコマンドはそのまま——戻すときに、いまの中身がコマンドの後と違えば並べて聞く（applyCommand）。
    function rebaseHistory() {
        if (!undoStack.length && !redoStack.length) {
            const saved = loadSavedHistory();
            if (saved) { undoStack = saved.undo.map(c => ({ ...c, open: false })); redoStack = (saved.redo || []).map(c => ({ ...c, open: false })); }
        }
        closeOpenCommand();
        const st = liveState();
        committed = new Map(st.map);
        committedOrder = st.order.slice();
        lastInputCaret = null;
        refreshUndoButtons();
    }

    // canUndo は ↶ を押せるか——戻せるコマンドがあるとき。打った文字はすぐコマンドになり、操作は押したあとすぐ拾うので、
    // 止まったときの拾いを待つあいだも押せる形にはしない（何も戻せないのに押せていた——2026-10-12）。
    function canUndo() {
        return undoStack.length > 0;
    }

    function undo() {
        if (!document.body.hasAttribute('edit-mode')) return;
        captureAll(); // まだ拾っていない変更も、戻せて・やり直せるように
        const cmd = undoStack.pop();
        if (!cmd) return;
        cmd.open = false;
        redoStack.push(cmd);
        applyCommand(cmd, false);
    }

    function redo() {
        if (!document.body.hasAttribute('edit-mode')) return;
        if (captureAll()) return; // 戻したあとに書いていれば、それが新しい変更（やり直しは無くなる）
        const cmd = redoStack.pop();
        if (!cmd) return;
        undoStack.push(cmd);
        applyCommand(cmd, true);
    }

    // refreshUndoButtons は帯の ↶ ↷ を、押せるときだけ押せるようにする（説明に、戻す・やり直すコマンドの名前）。
    function refreshUndoButtons() {
        const u = document.getElementById('w-ctx-undo');
        const r = document.getElementById('w-ctx-redo');
        const top = undoStack[undoStack.length - 1];
        const next = redoStack[redoStack.length - 1];
        if (u) {
            u.disabled = !canUndo();
            u.title = '元に戻す' + (top ? '：' + top.label : '') + '（Ctrl+Z）';
        }
        if (r) {
            r.disabled = redoStack.length === 0;
            r.title = 'やり直す' + (next ? '：' + next.label : '') + '（Ctrl+Y）';
        }
    }

    // opLabelOf は押したボタンから操作の名前を作る（説明・文字の順——長ければ切る）。
    function opLabelOf(target) {
        const b = target instanceof Element ? target.closest('button, [role="button"], .slash-menu-item, li, a') : null;
        if (!b) return '';
        const s = (b.getAttribute('title') || b.getAttribute('aria-label') || b.textContent || '').replace(/\s+/g, ' ').trim();
        return s.length > 24 ? s.slice(0, 24) + '…' : s;
    }

    // setupHistoryHooks はコマンドを集める引き金を配線する（書ける形のときだけ効く）。
    //   - 打った文字: beforeinput（打つ直前のキャレット・ブラウザの「元に戻す」を横取り）・input（recordInput）・変換の始まりと確定
    //   - Enter・Tab・Ctrl の組み合わせ（太字など——戻す・やり直す・写すは除く）の前に拾って結合を閉じる（キーの処理より先——捕捉の段）。
    //     操作のあと（このタスクのあと）にもう一度拾う——名前を付けて（改行・太字…）
    //   - 帯・メニュー・ブロックの操作・欄など、本文の文字の外を押す前に拾い、押した操作のあとに名前を付けて拾う
    //   - 貼り付け・ドラッグの前に拾う
    //   - Ctrl+Z・Ctrl+Y・Ctrl+Shift+Z は**どこにフォーカスがあっても**（本文の外の入力欄を除く）このアンドゥへ——それまでは本文の
    //     中だけで、帯や余白を押したあとはブラウザ自身のアンドゥが走り、1文字だけ消すような食い違いが起きていた（2026-10-12）。
    //   本文が変わったときの「止まったら拾う」は setupAutoSave の観察から（scheduleCapture）。
    const KEY_OP_LABEL = { b: '太字', i: '斜体', u: '下線', x: '切り取り', v: '貼り付け', k: 'リンク' };
    function setupHistoryHooks() {
        const editor = document.getElementById('w-editor-content');
        if (!editor) return;
        editor.addEventListener('beforeinput', e => {
            if (!document.body.hasAttribute('edit-mode')) return;
            if (e.inputType === 'historyUndo') { e.preventDefault(); undo(); return; }
            if (e.inputType === 'historyRedo') { e.preventDefault(); redo(); return; }
            if (!composing && !e.isComposing) inputCaretBefore = captureCaret();
        }, true);
        editor.addEventListener('input', recordInput);
        editor.addEventListener('compositionstart', () => {
            composing = true;
            inputCaretBefore = captureCaret();
            closeOpenCommand(); // 変換の前に打った字（英数字）とは別のコマンドにする（変換の確定ごとに1つ）
        });
        editor.addEventListener('compositionend', () => {
            composing = false;
            closeOpenCommand(); // 確定したら1つのコマンド（次の変換は新しいコマンド）
        });
        document.addEventListener('keydown', e => {
            if (!document.body.hasAttribute('edit-mode') || e.isComposing) return;
            const k = (e.key || '').toLowerCase();
            if ((e.ctrlKey || e.metaKey) && !e.altKey && (k === 'z' || k === 'y')) {
                // 本文の外の入力欄（検索・送る欄・設定の欄など）では、その欄のアンドゥ。
                const t = e.target;
                if (t instanceof Element && !t.closest('#w-editor-content') && t.closest('input, textarea, select, [contenteditable="true"]')) return;
                e.preventDefault();
                e.stopPropagation();
                if (k === 'z' && !e.shiftKey) undo(); else redo();
                return;
            }
            if (!(e.target instanceof Element) || !e.target.closest('#w-editor-content')) return;
            if (e.key === 'Enter' || e.key === 'Tab' || ((e.ctrlKey || e.metaKey) && !['c', 'control', 'meta', 'shift', 'alt'].includes(k))) {
                flushCheckpoint();
                const label = e.key === 'Enter' ? '改行' : e.key === 'Tab' ? 'Tab' : (KEY_OP_LABEL[k] || '');
                // 改行・Tab はブロックの形を変える——拾ったらすぐ保存（太字などはブロックの中——去るときに）。
                setTimeout(() => { if (captureAll(label) && (e.key === 'Enter' || e.key === 'Tab')) saveNow(); }, 0);
            }
        }, true);
        editor.addEventListener('paste', () => { flushCheckpoint(); setTimeout(() => captureAll('貼り付け'), 0); }, true);
        document.addEventListener('dragstart', () => { if (document.body.hasAttribute('edit-mode')) flushCheckpoint(); }, true);
        document.addEventListener('drop', () => { if (document.body.hasAttribute('edit-mode')) setTimeout(() => { if (captureAll('動かす')) saveNow(); }, 0); }, true);
        let armed = false;
        document.addEventListener('pointerdown', e => {
            if (!document.body.hasAttribute('edit-mode') || !(e.target instanceof Element)) return;
            if (e.target.closest('#w-ctx-undo, #w-ctx-redo')) return; // 戻す・やり直すは自分で拾う
            // 本文の中の文字（段落・セル）を押すのは、打つ場所を変えるだけ——拾わない（結合は閉じる）。
            const inText = e.target.closest('#w-editor-content') && !e.target.closest('.block-controls, .add-btn, .vocab-chrome, button, input, select');
            if (inText) { closeOpenCommand(); return; }
            flushCheckpoint();
            armed = true;
        }, true);
        // 押した操作が終わったら（このタスクのあと）名前を付けて拾う。非同期の操作（サーバーに聞いてから書く）は、止まったときに拾う。
        // ⚠ pointerdown の無いクリック（キーボードの Enter・Space でボタンを押す）でも拾う——押す前の分もここで拾う（2026-10-12 昼）。
        document.addEventListener('click', e => {
            const wasArmed = armed;
            armed = false;
            if (!document.body.hasAttribute('edit-mode') || !(e.target instanceof Element)) return;
            if (e.target.closest('#w-ctx-undo, #w-ctx-redo')) return;
            if (!wasArmed) {
                const inText = e.target.closest('#w-editor-content') && !e.target.closest('.block-controls, .add-btn, .vocab-chrome, button, input, select');
                if (inText) return;
                flushCheckpoint();
            }
            const label = opLabelOf(e.target);
            setTimeout(() => { if (captureAll(label)) saveNow(); }, 0); // 操作のあとはすぐ保存（ブロックを去るのを待たない）
        }, true);
    }

    // ── 矢印キーでブロックの端を越える（2026-10-12 午後）────────────────────────────
    // 利用者:「テーブルのセルを矢印キーで移動していくと、最後のセルの次に移ったあとキャレットが無くなり移動できなくなります。
    // ほかにもそういう要素があります」——ブロックごとに書ける欄（contenteditable）が分かれているので、ブラウザは欄の端を越えて
    // キャレットを動かせない。表の最後のセルの → と最後の行の ↓ は欄の外（.block-content）へ抜けてキャレットが行き場を失い、段落・
    // 箇条書き・タグ・節・折りたたみの端では動かなかった。表の鏡の列（サーバーが描く .vocab-chrome のセル）にも入り込んでいた。
    // 直し方: **まずブラウザに動かしてもらい**、そのあと（このタスクのあと）に見る。欄の外へ抜けた・鏡（.vocab-chrome・
    // contenteditable="false"）に入った・動かなかった（← → は位置が同じ・↑ ↓ は行が変わらない）ときだけ直す——
    //   1. 同じ欄の中で次の場所へ（← → は文書の順で次・前の場所、↑ ↓ は画面で1つ下・上の行のうち、元の横の位置にいちばん近い場所
    //      ——ブラウザの ↓ は、表では同じ行の右のセル・タグでは同じ行の値・折りたたみの見出しでは見出しの終わりへ動くだけだった）
    //   2. 無ければ、次のブロックの頭（→ ↓）・前のブロックの終わり（← ↑）へ
    //   3. それも無ければ、抜けたときは元の位置へ戻す（欄の外にキャレットを残さない）。
    // それまでの「段落と見出しで ↑ ↓ を押すたびに前・次のブロックへフォーカスを移す」は外した（行の途中でも飛び、表や折りたたみへ
    // フォーカスを移すとキャレットが欄の外に置かれていた）。
    const CARET_LEAVES = 'td,th,li,dd,dt,p,h1,h2,h3,h4,h5,h6,summary,caption,blockquote,pre,figcaption';
    // isShown は、その要素が見えているか（閉じた折りたたみの中は、箱があっても見えていない——Chrome は中身を content-visibility で
    // 隠すので getClientRects では分からない。checkVisibility の無いブラウザは箱で見る）。
    const isShown = el => !!el && (el.checkVisibility ? el.checkVisibility() : el.getClientRects().length > 0);
    const isChromeNode2 = n => { const el = n && (n.nodeType === 3 ? n.parentElement : n); return !!(el && el.closest && el.closest('.vocab-chrome, [contenteditable="false"]')); };

    // editableHostOf は、そのノードを含む書ける欄（上の段のブロック）を返す（本文の外・閲覧モードなら null）。
    function editableHostOf(node) {
        const el = node && (node.nodeType === 3 ? node.parentElement : node);
        if (!el || !el.closest) return null;
        const host = el.closest('#w-editor-content .block-content > [contenteditable="true"]');
        return host || null;
    }

    // caretStops は、欄の中でキャレットを置ける場所（文字のあるノード・中身の無い葉の要素）を文書順に返す（鏡の中は除く）。
    function caretStops(host) {
        const out = [];
        const walker = document.createTreeWalker(host, NodeFilter.SHOW_ELEMENT | NodeFilter.SHOW_TEXT, {
            acceptNode: n => (n.nodeType === 1 && (n.classList.contains('vocab-chrome') || n.getAttribute('contenteditable') === 'false'))
                ? NodeFilter.FILTER_REJECT : NodeFilter.FILTER_ACCEPT,
        });
        let n;
        while ((n = walker.nextNode())) {
            const el = n.nodeType === 3 ? n.parentElement : n;
            if (!isShown(el)) continue; // 見えていない（閉じた折りたたみの中など）
            if (n.nodeType === 3) {
                if (n.nodeValue.trim() || (n.parentElement && n.parentElement.matches(CARET_LEAVES))) out.push(n);
            } else if (n.matches(CARET_LEAVES) && !n.textContent.trim()) {
                out.push(n);
            }
        }
        // 文字のあるノードは、その葉の要素より後ろで拾われる——中身のある葉の要素は入れていないので重ならない。
        return out;
    }

    // placeCaretAtStop は、キャレットを置ける場所の頭（atEnd なら終わり）にキャレットを置く（欄へフォーカスしてから）。
    function placeCaretAtStop(host, stop, atEnd) {
        if (host.focus) host.focus({ preventScroll: true });
        const range = document.createRange();
        if (stop.nodeType === 3) range.setStart(stop, atEnd ? stop.nodeValue.length : 0);
        else { range.selectNodeContents(stop); range.collapse(!atEnd); }
        range.collapse(true);
        const sel = window.getSelection();
        sel.removeAllRanges();
        sel.addRange(range);
        const el = stop.nodeType === 3 ? stop.parentElement : stop;
        if (el && el.scrollIntoView) el.scrollIntoView({ block: 'nearest' });
    }

    // moveToNeighborBlock は、次（dir > 0——頭へ）・前（dir < 0——終わりへ）の書けるブロックへキャレットを移す。移したら true。
    function moveToNeighborBlock(host, dir) {
        const blocks = topLevelBlocks();
        let i = blocks.indexOf(host);
        if (i < 0) return false;
        for (i += dir; i >= 0 && i < blocks.length; i += dir) {
            const b = blocks[i];
            if (b.getAttribute('contenteditable') !== 'true') continue;
            const stops = caretStops(b);
            if (!stops.length) continue;
            placeCaretAtStop(b, dir > 0 ? stops[0] : stops[stops.length - 1], dir < 0);
            return true;
        }
        return false;
    }

    // stopRect はキャレットを置ける場所の画面の位置。
    function stopRect(stop) {
        if (stop.nodeType === 3) {
            const r = document.createRange();
            r.selectNodeContents(stop);
            const rs = r.getClientRects();
            if (rs.length) return rs[0];
            return stop.parentElement.getBoundingClientRect();
        }
        return stop.getBoundingClientRect();
    }

    // stopInHost は、同じ欄の中で次に置く場所を返す（無ければ null）。← → は文書の順で before の後ろ・前、↑ ↓ は画面で1つ下・上の行
    // （before の上端より下・上）のうち、元の横の位置にいちばん近い場所。
    function stopInHost(host, before, dir, vertical) {
        const stops = caretStops(host);
        if (!stops.length || !before.node || !before.node.isConnected) return null;
        const at = document.createRange();
        try { at.setStart(before.node, before.offset); at.collapse(true); } catch (e) { return null; }
        if (!vertical) {
            const ordered = stops.filter(s => {
                const end = s.nodeType === 3 ? s.nodeValue.length : s.childNodes.length;
                try { return dir > 0 ? at.comparePoint(s, 0) === 1 : at.comparePoint(s, end) === -1; } catch (e) { return false; }
            });
            if (!ordered.length) return null;
            return dir > 0 ? ordered[0] : ordered[ordered.length - 1];
        }
        if (before.top === null) return null;
        const mid = r => r.top + r.height / 2;
        const cands = stops.map(s => ({ s, r: stopRect(s) })).filter(x => x.r && (dir > 0 ? mid(x.r) > before.top + 4 : mid(x.r) < before.top - 4));
        if (!cands.length) return null;
        const rowMid = dir > 0 ? Math.min(...cands.map(x => mid(x.r))) : Math.max(...cands.map(x => mid(x.r)));
        const row = cands.filter(x => Math.abs(mid(x.r) - rowMid) <= 4);
        const x0 = before.left === null ? 0 : before.left;
        row.sort((a, b) => Math.abs(a.r.left - x0) - Math.abs(b.r.left - x0));
        return row[0].s;
    }

    // alignColumn は、↑ ↓ で表のセル・タグの項目（td・th・dt・dd）へ動いたとき、同じ行のうち元の横の位置にいちばん近い場所へ置き直す。
    const CELLS = 'td,th,dt,dd';
    function alignColumn(host, before, after) {
        const el = after.node && (after.node.nodeType === 3 ? after.node.parentElement : after.node);
        const cell = el && el.closest ? el.closest(CELLS) : null;
        if (!cell || !host.contains(cell) || before.left === null) return;
        const mid = r => r.top + r.height / 2;
        const row = caretStops(host).map(s => ({ s, r: stopRect(s) }))
            .filter(x => x.r && Math.abs(mid(x.r) - after.top) <= 4 && (x.s.nodeType === 3 ? x.s.parentElement : x.s).closest(CELLS));
        if (row.length < 2) return;
        row.sort((a, b) => Math.abs(a.r.left - before.left) - Math.abs(b.r.left - before.left));
        const best = row[0].s;
        const bestCell = (best.nodeType === 3 ? best.parentElement : best).closest(CELLS);
        if (bestCell !== cell) placeCaretAtStop(host, best, false);
    }

    // caretSnapshot は、いまのキャレット（ノード・位置・欄の中の文字の位置・画面の上端）を覚える。
    function caretSnapshot() {
        const sel = window.getSelection();
        if (!sel.rangeCount) return null;
        const range = sel.getRangeAt(0);
        // 行は上端でなく**縦の真ん中**で比べる（キャレットが要素の上にあるときは要素の箱で測る——箱の上端は文字より余白の分だけ上）。
        let top = null, left = null;
        const rects = range.getClientRects();
        if (rects.length) { top = rects[0].top + rects[0].height / 2; left = rects[0].left; }
        else {
            const el = range.startContainer.nodeType === 3 ? range.startContainer.parentElement : range.startContainer;
            if (el && el.getBoundingClientRect) { const b = el.getBoundingClientRect(); top = b.top + b.height / 2; left = b.left; }
        }
        const c = captureCaret();
        return { node: range.startContainer, offset: range.startOffset, key: c ? c.blockId + ':' + c.offset : '', top, left };
    }

    // afterArrow は、ブラウザが矢印キーで動かしたあとのキャレットを見て、必要なら直す。
    function afterArrow(host, dir, vertical, before) {
        const sel = window.getSelection();
        if (!document.body.hasAttribute('edit-mode')) return;
        const now = sel.rangeCount ? sel.anchorNode : null;
        const nowEl = now && (now.nodeType === 3 ? now.parentElement : now);
        // 欄の外・鏡の中・見えないところ（閉じた折りたたみの中——ブラウザの ↓ は見出しからそこへ入っていた）は「抜けた」。
        const escaped = !now || editableHostOf(now) !== host || isChromeNode2(now) || !isShown(nowEl);
        let stuck = false;
        let after = null;
        if (!escaped) {
            after = caretSnapshot();
            // ← → は位置がそのまま（欄の端でブラウザが動かさなかった）。↑ ↓ は行が変わらない（縦の真ん中で比べる）。
            if (vertical) stuck = !!after && before.top !== null && after.top !== null && (dir > 0 ? after.top <= before.top + 4 : after.top >= before.top - 4);
            else stuck = !!after && after.node === before.node && after.offset === before.offset;
        }
        if (!escaped && !stuck) {
            // ↑ ↓ で表のセル・タグの項目へ動いたら、同じ行のうち元の横の位置にいちばん近いセルへ（ブラウザは列を外すことがある）。
            if (vertical && after) alignColumn(host, before, after);
            return;
        }
        const inHost = stopInHost(host, before, dir, vertical);
        if (inHost) { placeCaretAtStop(host, inHost, dir < 0 && !vertical); return; }
        if (moveToNeighborBlock(host, dir)) return;
        if (escaped && before.node && before.node.isConnected) {
            // 次・前に書けるブロックが無い——元の位置へ戻す（欄の外にキャレットを残さない）。
            if (host.focus) host.focus({ preventScroll: true });
            const r = document.createRange();
            try { r.setStart(before.node, before.offset); } catch (e) { r.selectNodeContents(host); r.collapse(dir < 0); }
            r.collapse(true);
            sel.removeAllRanges();
            sel.addRange(r);
        }
    }

    // setupArrowNavigation は、書ける形の本文で矢印キー（Shift・Ctrl なし）を見張る（捕捉の段——スラッシュメニューの操作は除く）。
    function setupArrowNavigation() {
        document.addEventListener('keydown', e => {
            if (!document.body.hasAttribute('edit-mode') || e.isComposing || e.shiftKey || e.ctrlKey || e.metaKey || e.altKey) return;
            const dir = { ArrowRight: 1, ArrowDown: 1, ArrowLeft: -1, ArrowUp: -1 }[e.key];
            if (!dir || slashMenuVisible) return;
            const sel = window.getSelection();
            if (!sel.rangeCount || !sel.isCollapsed) return;
            const host = editableHostOf(sel.anchorNode);
            if (!host) return;
            const before = caretSnapshot();
            if (!before) return;
            const vertical = e.key === 'ArrowDown' || e.key === 'ArrowUp';
            setTimeout(() => afterArrow(host, dir, vertical, before), 0);
        }, true);
    }

    // triggerAutoSave は「まだ保存していない」の印を付けるだけ（2026-10-12 昼——1.5秒止まるたびの保存をやめた。利用者:「保存は、
    // ブロックを去る時とページを去る時に限定して、細かい通信を抑制してはどうでしょう？」【考察】編集モードの再考 §8）。保存するのは、
    // ブロックを去るとき（下の focusout——ブロックごとに書ける欄が分かれているので、別のブロックへ移る・本文の外を押すと出る。表は
    // 表ごと）・タブを隠すとき・ページを去るとき（setupSaveOnLeave）・閲覧モードへ替えるとき・業務のボタンの前・操作とアンドゥの
    // あと（saveNow）。保険の保存（長く止まったら）は置かない（利用者の答え）。
    function triggerAutoSave() {
        if (!document.body.hasAttribute('edit-mode')) return;
        unsaved = true;
        editSeq++;
        setSaveStatus("未保存", "#94a3b8");
    }

    // setupSaveOnLeave は、タブを隠すとき（パソコンが眠る・ほかのアプリへ移る・別のタブへ移る）とページを去るときに保存する。
    let leavingPage = false; // ページを去る途中（pagehide のあと——送る保存は keepalive にする）
    function setupSaveOnLeave() {
        document.addEventListener('visibilitychange', () => {
            if (document.visibilityState === 'hidden' && document.body.hasAttribute('edit-mode')) flushPendingSave();
        });
        window.addEventListener('pagehide', () => {
            leavingPage = true;
            if (document.body.hasAttribute('edit-mode')) flushPendingSave();
        });
        // 戻る・進むでページがそのまま戻ってきたら（bfcache）印を外す。
        window.addEventListener('pageshow', () => { leavingPage = false; });
    }

    function setupAutoSave() {
        const observer = new MutationObserver((mutations) => {
            triggerAutoSave();
            scheduleCapture(); // まだ拾っていない変更を、止まったら拾う（アンドゥ・2026-10-12）
            buildToc(); // 見出しの追加・削除・移動を目次に反映
        });
        observer.observe(document.getElementById('w-editor-content'), {
            childList: true,
            subtree: true,
            characterData: true,
            attributes: true,
            // 本文が持つ属性のうち、編集で変わりうるもの（リンク先・埋め込み先・形式の印）。
            attributeFilter: ['href', 'src', 'download', 'data-type', 'data-src']
        });

        document.getElementById('w-editor-content').addEventListener('input', () => {
            triggerAutoSave();
            scheduleCapture();
            buildToc(); // 見出しテキストの編集を目次に反映
        });

        // ブロックの編集が終わった時点（＝フォーカスがそのブロックから外れた時）で即保存する。
        // サーバーはサニタイズ結果を返すので、除去が起きた場合はその変化がすぐ画面に出る
        // （docs/旧文書/本文サニタイズ設計.md のエコーバック方式）。2026-10-12 昼からは、これがふだんの保存の機会
        // （1.5秒止まるたびの保存はやめた——triggerAutoSave）。
        document.getElementById('w-editor-content').addEventListener('focusout', () => {
            if (!document.body.hasAttribute('edit-mode')) return;
            // 機械が要素を差し替えている途中（見出しにする・戻す）は保存しない——フォーカスのある要素を外した瞬間に、ブラウザが
            // 同期で focusout を出す。そのときの本文は差し替えの途中の姿（中身を移した空の段落）なので、送ると一瞬壊れる（2026-10-07）。
            if (quietDomChange || suppressSnapshot) return;
            saveToServer();
        });
    }

    // ── 書ける形と閲覧モード（2026-10-12 に作り直した）──────────────────────────────
    //
    // 利用者:「排他のために編集モードを設けたが、今後は編集モードの代わりに、閲覧に邪魔なものを表示しない目的で、
    // 閲覧モードを設けることにします」（【考察】編集モードの再考 §6）。
    //   - **編集権は取らない**——切り替えは一瞬。守りはブロック単位（/api/sync——ぶつかったら両方を並べる）。
    //   - **閲覧モードは読むだけ**（打てない）。表の並べ替え・見出しで畳む・カルーセル・画像の拡大・機械向けのタグを畳むは閲覧モード。
    //   - **開いたときの形はこの端末に憶える**（最後に使った形——localStorage `wcms.mode`）。初めてはパソコン＝書ける形・
    //     スマホ＝閲覧モード（読むたびにキーボードと帯が出ないように——問い1）。
    //   - 書ける形の本文は /api/sync の full で読む（閲覧の描き方〔見出しのアンカー・参照リンク〕を入れない——入れると本文として
    //     保存される）。元の並びと指紋も同じ応答で受け取る（読んだ中身と元がずれない）。
    const MODE_KEY = 'wcms.mode';

    // preferredMode は、ページを開いたときの形（'edit' か 'view'）を返す。
    function preferredMode() {
        try {
            const m = localStorage.getItem(MODE_KEY);
            if (m === 'edit' || m === 'view') return m;
        } catch (e) { /* 使えなければ既定 */ }
        const phone = window.matchMedia && window.matchMedia('(max-width: 760px), (pointer: coarse)').matches;
        return phone ? 'view' : 'edit';
    }

    function rememberMode(m) {
        try { localStorage.setItem(MODE_KEY, m); } catch (e) { /* 憶えられなくても切り替えはできる */ }
    }

    // applyMode は閲覧／書ける形の DOM 切り替えのみを行う（元の受け取り・保存は onModeToggle が担う）。
    function applyMode() {
        const toggle = document.getElementById('w-mode-toggle');
        const modeText = document.getElementById('w-mode-text');
        const editor = document.getElementById('w-editor-content');

        // スイッチは「閲覧」1つの意味にそろえる（2026-10-12 利用者:「閲覧モードのスイッチですが、編集できますに変えず「閲覧」の
        // ままで、編集できるときにグレーアウトして意味の統一を図ってはどうでしょう」）——文字はいつも「閲覧」、書けるときは灰色。
        // 中の checkbox は書ける形で checked のまま（見た目だけを逆にする——app.css の .slider）。
        modeText.innerText = "閲覧";
        if (toggle.checked) {
            document.body.setAttribute('edit-mode', '');
            modeText.style.color = "#cbd5e1";
        } else {
            document.body.removeAttribute('edit-mode');
            modeText.style.color = "#334155";
            hideSlashMenu();
            hideContextToolbar();
            hideTableToolbar();
            hideInnerBar();
        }

        // 編集できるのは本文のブロック（見出し・段落・リスト・表・定義リスト・引用…）。
        // 除くのは2つ——計算ビューのマーカー（中身はサーバーが所有）と、貼り付け等で
        // 紛れ込んだカスタム要素（語彙には無いので保存時にアンラップされる）。
        // 見出しや段落を名指ししていたころは、許可されている h4〜h6・ul・table が
        // 表示されるだけで編集できなかった。
        const standardElements = Array.from(editor.querySelectorAll('.block-content > *'))
            .filter(el => !isCustomTag(el.tagName.toLowerCase()));
        standardElements.forEach(el => {
            if (toggle.checked) { el.setAttribute('contenteditable', 'true'); el.oninput = updateHtmlPreview; }
            else { el.removeAttribute('contenteditable'); el.oninput = null; }
        });

        enhanceFileSections(); // ファイル容器のクロームをモードに合わせて作り直す
        refreshAttachmentPreviews(); // 添付のクリック展開（閲覧モード限定）
        refreshAnalyzedMarks();      // 解析済みの印（取得できたら描き直す）
        refreshDrawingPreviews();    // ファイル表示の枠の右下をつまんで高さを変える配線（両方の形）
        restoreViewState();          // 物ごとに憶えた開閉と縦横比（この端末・2026-09-28）
        decorateLocalEdit();         // ファイル表示の「📝 ローカル編集」（2026-09-29）
        openRFQReplyFromHash();      // 返事ページの「部材見積依頼書で単価を写す」から来たら 🤖 返事を読む の欄を開く（2026-10-05）
        // 「書ける形でも」の業務のボタンは、2026-10-12 まで閲覧モード限定だった（いまは押す前に書きかけを保存する）。
        refreshFilingButton();      // 加工製品ページの整理（書ける形でも）
        refreshMailChrome();         // 返信と「この記録への返信」（書ける形でも）
        wireUnhandledActions();      // 未処理一覧の「不要」ボタン（書ける形でも）
        wireNewRecord();             // 「＋ 記録する」（電話・FAX・メール・メモ）
        wireMailFetch();             // 「📥 新しいメールを読み込む」（その隣・2026-09-29）
        refreshPhoneChrome();        // ☎ 発信（電話番号のタグがあるページ・書ける形でも）
        refreshContactUnfile();      // 「未分類へ戻す」（メールアドレスのタグの隣・書ける形でも）
        if (toggle.checked) ensureIdleContextToolbar(); // 固定の帯の文字の帯を、触る前から見せる（2026-10-19）
        markTagVocabulary();         // タグの名前と値が語彙にあるかを色で示す（拒否はしない）
        refreshFormulaCells();       // 表のセルの式——閲覧では結果・編集では式（2026-10-04・並べ替え・印刷より先に）
        refreshHeadingFolds();       // 見出しを押して次の同じ段の見出しまで畳む（閲覧モード限定・表示だけ・2026-10-04）
        refreshTableMenus();         // どの表にも「⋯」——開くと「🖨 この表を印刷」など（閲覧モード限定・2026-10-02・10-15 に「⋯」へ）
        refreshTableSort();          // 列の題を押して並べ替える（閲覧モード限定・表示だけ・2026-10-03）
        wireContactRegister();       // 未登録の連絡先の「組織」「担当者」「登録」（書ける形でも）
        foldMachineTags();           // 機械に向けたタグを「詳細」へ畳む（閲覧モード限定）
        renderPresence();            // ほかの人のキャレットのあるブロック（書ける形だけ・2026-10-12 昼）
        updatePresence(false);
        decorateVocabBlocks(); // 形式名の札もモードに合わせて作り直す
        updateHtmlPreview();
        refreshPermsEditable(); // 権限カードの編集可否を書ける形かどうかに合わせる
        // 版の履歴は「戻す」ボタンの出し分けがモードに依るので描き直す。
        // 閲覧モードへ戻った直後は版の区切り（checkpointVersion——/api/version-checkpoint）が打たれて
        // いるので、一覧そのものも取り直す価値がある（2026-10-12 までは、ロックを手放したときにサーバーが打っていた）。
        loadVersions();
    }

    // onModeToggle は閲覧／書ける形の切り替えの入口（人がトグルを押したとき・ページを開いたとき）。
    // remember が真なら、この端末に憶える（人が押したとき）。
    async function onModeToggle(e) {
        const toggle = document.getElementById('w-mode-toggle');
        if (e && e.type === 'change') rememberMode(toggle.checked ? 'edit' : 'view');
        if (toggle.checked) {
            await enterEditMode();
            return;
        }
        // **書きかけを先に流します**（2026-09-15）。保存はブロックを去るとき・タブを隠すとき・ページを去るときだけ
        // （2026-10-12 昼から。それまでは1.5秒のデバウンス）なので、キャレットを置いたまま閲覧へ戻ると、
        // 最後の書きかけがまだ送られていない（送っている途中の）ことがあります。
        const wasEditing = document.body.hasAttribute('edit-mode');
        if (wasEditing) { await flushPendingSave(); await checkpointVersion(false); }
        syncReady = false;
        // ページの更新の知らせが比べる相手を覚え直す（閲覧の描き方で——2026-10-07）。
        // ⚠ 書ける形から出るときだけ——ページを開いたときもここを通る（閲覧で始まる）が、そのときは開いたときの姿を
        //    覚え済み（window.onload）なので、読み直しを1回余計に走らせない（鏡の計算を含む）。
        if (wasEditing) await adoptViewSourceFromServer();
        // 式のセルの結果はサーバーが計算する（formula.go）——編集で式や値を変えたら、手元の結果は古い。
        // 古い結果を見せないよう外してから閲覧へ戻し、式があれば本文を読み直す（2026-10-04）。
        const formulas = hasFormulaCells();
        if (formulas) clearFormulaResults();
        applyMode();
        if (formulas) await reloadContent();
        if (wasEditing) notify('閲覧モードにしました。', { type: 'info', duration: 2500, id: 'mode' });
    }

    // enterEditMode は書ける形へ入る——編集権は取らない。いまの本文と元（並びと指紋）を /api/sync の full で受け取って
    // 載せ替え、アンドゥの履歴をいまの画面へ合わせる。書けない人（403）は閲覧のままにする。
    async function enterEditMode() {
        const toggle = document.getElementById('w-mode-toggle');
        const ok = await loadForEditing();
        if (!ok) { toggle.checked = false; applyMode(); return false; }
        // 書ける形では「ページの更新の知らせ」の札は出さない（届いた変更はその場で差し替える）。
        clearTimeout(pageCheckTimer); pageCheckTimer = null; pendingPageHTML = null;
        dismissToast('page-updated');
        toggle.checked = true;
        applyMode();
        updateHtmlPreview();
        rebaseHistory(); // アンドゥの履歴は閲覧と行き来しても・同じタブなら読み直しても残す（問い8）
        validateTypedTables(); // 既存本文の型不一致を編集開始時に可視化する
        return true;
    }

    // loadForEditing は /api/sync の full で本文と元を受け取り、エディタへ載せ替える（成功なら true）。
    async function loadForEditing() {
        let res, d;
        try {
            res = await fetch('/api/sync', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ page_id: currentPageId, full: true, tab: TAB_ID }),
            });
            d = await readResult(res);
        } catch (e) {
            notify('書ける形にできませんでした（通信エラー）。', { type: 'error', id: 'mode' });
            return false;
        }
        if (res.status === 403) return false; // 書く権限が無い——閲覧のまま（トグルは loadPerms が隠す）
        if (!res.ok || !d || !d.success) {
            notify('書ける形にできませんでした: ' + failMessage(res, d), { type: 'error', id: 'mode' });
            return false;
        }
        const blocks = d.blocks || [];
        populateEditor(blocks.map(b => b.html || '').join('\n'));
        syncConflicts.forEach((c, id) => closeConflict(id));
        syncBase = new Map();
        topLevelBlocks().forEach(el => {
            const id = el.getAttribute(BLOCK_ID_ATTR);
            const b = blocks.find(x => x.id === id);
            if (b) syncBase.set(id, { h: b.h, html: serializeBlock(el) });
        });
        syncOrder = blocks.map(b => b.id);
        syncReady = true;
        applySavedMeta(d);
        loadedUpdatedAt = d.updated_at || loadedUpdatedAt;
        return true;
    }

    // populateEditor は本文HTMLをエディタへ流し込みブロック化する（ロード時・書ける形に入るときに共用）。
    // view は閲覧の描き方（/api/load?view=1）で読んだ本文か——そうなら、ページの更新の知らせが比べる相手として
    // 覚える（そうでなければ捨てる——編集用の素の本文は閲覧の描き方と比べられない・2026-10-07）。
    function populateEditor(htmlStr, view) {
        const editorContent = document.getElementById('w-editor-content');
        editorContent.innerHTML = '';
        const blocks = serverBlocksOf(htmlStr);
        const htmls = blocks.map(c => c.outerHTML);
        blocks.forEach(child => editorContent.appendChild(child));
        initBlocks();
        adoptViewSource(view ? htmls : []);
        buildToc();
        enhanceFileSections();
        decorateVocabBlocks();
    }

    // lockedFetch は、ページの属性・添付などを変える要求を送る共通の口（2026-10-12 から編集権のしるしは付けない——編集権は
    // 取らないので）。**書きかけを先に保存してから**送る（添付を足す・版を戻すは本文に効くので、書きかけと前後しないように）。
    // 名前は呼び手が多いので残した。
    async function lockedFetch(url, opts = {}) {
        if (document.body.hasAttribute('edit-mode')) await flushPendingSave();
        return fetch(url, opts);
    }

    // reloadContent は現在のページ本文をサーバーから取り直して反映する。
    //   - 閲覧モードでは開いたときと同じ描き方（view=1——見出しのアンカーと参照リンクつき）で読み直す（2026-10-07）。
    //   - 書ける形では、書きかけを消さないよう**変わったブロックだけ**を受け取る（/api/sync——業務のボタンのあとなど）。
    async function reloadContent() {
        try {
            if (document.body.hasAttribute('edit-mode')) {
                await flushPendingSave();
                await pullFromServer();
                loadPageMeta();
                return;
            }
            const res = await fetch('/api/load?id=' + currentPageId + '&view=1');
            if (!res.ok) return;
            populateEditor(await res.text(), true);
            applyMode();
            loadPageMeta(); // 更新日時などの属性表示も最新化
        } catch (e) {}
    }

    // ── ページの更新の知らせ（2026-10-07） ─────────────────────────────────
    //
    // 利用者:「誰かが書き込むと、同じページを見ているほかの人に再読み込み通知が送られて、
    // 編集されたブロックだけ再読み込みできますか？」
    //
    //   - 閲覧しているタブは /api/page-events を購読する（internal/cms/page_events.go）。本文が書き換わると、
    //     書く人の自動保存が続くあいだは待ち（PAGE_CHECK_WAIT_MS・長くても PAGE_CHECK_MAX_MS）、本文を開いたときと
    //     同じ描き方（/api/load?view=1）で読み直して、**前に読んだもの**と上の段のブロックごとに比べる。
    //   - 変わったところがあれば知らせ（「○○がこのページを更新しました（N か所）」＋「変わったところを読み込む」）。
    //     押すと変わったブロックだけを差し替える——変わっていないブロックは要素ごと残すので、畳んだ見出し・開いた
    //     PDF・並べ替え・鏡の絞り込みはそのまま。差し替えたブロックはしばらく色を付ける。
    //   - ⚠ **比べる相手は画面のいまの姿ではなく、前に読んだときのサーバーの描いた HTML**（viewSource）——画面は
    //     飾り付け（式の結果・畳み・並べ替え・札）で変わっている。ID ではなく中身で比べるのは、機械が作るページの
    //     ブロックは data-id を持たないから（2026-10-07 の職場: 1616 ページのうち全部のブロックに ID があるのは 93）。
    //   - ⚠ **入力中の欄があるブロックは差し替えない**（消えるので）——知らせを残して、終えてから押してもらう。
    //   - 書ける形のタブも知らせを受けるが、この比べ方は使わない——少し待って /api/sync で変わったブロックだけ受け取る
    //     （openPageEvents・pullFromServer。2026-10-12 から。それまでの編集モードのタブは、編集権が1人だけだったので知らせを
    //     受けなかった）。書ける形に入ると比べる相手は無くなり（populateEditor）、出るときに閲覧の描き方で読み直して
    //     覚え直す（adoptViewSourceFromServer）。
    let viewSource = new WeakMap(); // 上の段の要素 → 前に読んだときのサーバーの描いた HTML
    let pageES = null;
    let pageCheckTimer = null;
    let pageCheckFirstAt = 0;
    let pageUpdateBy = '';
    let pendingPageHTML = null;
    let editPullTimer = null; // 書ける形で知らせを受けてから受け取りに行くまで（2026-10-12）
    let loadedUpdatedAt = '';
    const PAGE_CHECK_WAIT_MS = 2000;
    const PAGE_CHECK_MAX_MS = 10000;

    // contentBlocks は本文の上の段の要素を文書順で返す（包み .editor-block の中身・包む前なら要素そのもの）。
    // ⚠ 飾り付けが本文の直下へ足した要素（包みの無いもの）は数えない——中身ではないので、差し替えで消さない。
    function contentBlocks(beforeWrap) {
        const editor = document.getElementById('w-editor-content');
        if (!editor) return [];
        return Array.from(editor.children).map(c => {
            if (c.classList.contains('editor-block')) {
                const box = c.querySelector(':scope > .block-content');
                return box ? firstContentChild(box) : null;
            }
            return beforeWrap ? c : null;
        }).filter(el => el && el.tagName !== 'SCRIPT' && el.tagName !== 'STYLE');
    }

    // adoptViewSource は、いまの上の段の要素に「サーバーが描いた HTML」を覚えさせる。htmls を渡せばその順に
    // （読み直したもの）、無ければ要素のいまの姿から（開いた直後・包みと飾り付けの前に呼ぶこと）。
    function adoptViewSource(htmls, beforeWrap) {
        viewSource = new WeakMap();
        const blocks = contentBlocks(beforeWrap);
        if (htmls && htmls.length !== blocks.length) return; // 並びが合わない——覚えない（次の差し替えで全部を取り直す）
        blocks.forEach((el, i) => viewSource.set(el, htmls ? htmls[i] : el.outerHTML));
    }

    // serverBlocksOf は本文の HTML を上の段の要素に分ける（populateEditor と同じ分け方）。
    function serverBlocksOf(htmlStr) {
        const doc = new DOMParser().parseFromString(htmlStr, 'text/html');
        return Array.from(doc.body.children).filter(c => c.tagName.toLowerCase() !== 'script');
    }

    // adoptViewSourceFromServer は、書ける形から閲覧へ戻るとき（書きかけを送ったあと）に、閲覧の描き方で
    // 読み直して覚え直す。画面は編集したときの姿のまま（描き直さない）。
    // ⚠ 2026-10-12 までは編集権を放す前に読んだので、そのあいだほかの人は書けなかった。いまはほかの人がいつでも書けるので、
    //    読んだ直後の書き込みは次の知らせで比べる。
    async function adoptViewSourceFromServer() {
        try {
            const res = await fetch('/api/load?id=' + currentPageId + '&view=1');
            if (res.ok) adoptViewSource(serverBlocksOf(await res.text()).map(c => c.outerHTML));
        } catch (e) { /* 覚えられなければ、次の知らせで全部を取り直す */ }
    }

    // blockIsBusy は、そのブロックの中で人が入力している途中か（差し替えると消える）を返す。
    // 鏡の絞り込み（data-w-remember）は差し替えたあと戻るので数えない。
    function blockIsBusy(el) {
        const a = document.activeElement;
        if (a && a !== document.body && el.contains(a) && (a.matches('input, textarea, select') || a.isContentEditable)) return true;
        return Array.from(el.querySelectorAll('input, textarea, select')).some(f => {
            if (f.closest('[data-w-remember]')) return false;
            if (f.tagName === 'SELECT') return Array.from(f.options).some(o => o.selected !== o.defaultSelected);
            if (f.type === 'checkbox' || f.type === 'radio') return f.checked !== f.defaultChecked;
            if (['button', 'submit', 'reset', 'hidden', 'file'].includes(f.type)) return false;
            return f.value !== f.defaultValue;
        });
    }

    // planPageUpdate は、読み直した本文といまの画面を比べて、残すブロック・消すブロック・足すブロックを決める。
    // 同じ HTML の並びを最長で残し（LCS）、残したもののあいだ（すき間）ごとに差し替える。
    function planPageUpdate(htmlStr) {
        const fresh = serverBlocksOf(htmlStr);
        const freshHTML = fresh.map(c => c.outerHTML);
        const live = contentBlocks();
        const liveHTML = live.map(el => viewSource.get(el));
        const n = live.length, m = fresh.length;
        const same = (i, j) => liveHTML[i] !== undefined && liveHTML[i] === freshHTML[j];
        const L = Array.from({ length: n + 1 }, () => new Uint32Array(m + 1));
        for (let i = n - 1; i >= 0; i--) {
            for (let j = m - 1; j >= 0; j--) L[i][j] = same(i, j) ? L[i + 1][j + 1] + 1 : Math.max(L[i + 1][j], L[i][j + 1]);
        }
        const pairs = [[-1, -1]];
        for (let i = 0, j = 0; i < n && j < m;) {
            if (same(i, j)) { pairs.push([i, j]); i++; j++; }
            else if (L[i + 1][j] >= L[i][j + 1]) i++;
            else j++;
        }
        pairs.push([n, m]);
        const gaps = [];
        for (let k = 1; k < pairs.length; k++) {
            const [ai, aj] = pairs[k - 1], [bi, bj] = pairs[k];
            const removed = live.slice(ai + 1, bi), added = fresh.slice(aj + 1, bj), addedHTML = freshHTML.slice(aj + 1, bj);
            if (!removed.length && !added.length) continue;
            gaps.push({ before: ai >= 0 ? live[ai] : null, after: bi < n ? live[bi] : null, removed, added, addedHTML,
                busy: removed.some(blockIsBusy) });
        }
        return { gaps, places: gaps.reduce((s, g) => s + Math.max(g.removed.length, g.added.length), 0) };
    }

    const wrapperOf = el => el.closest('.editor-block') || el;

    // applyPageUpdate は差し替えを当てる。入力中のブロックがあるすき間は飛ばす（数を返す）。
    function applyPageUpdate(plan) {
        const editor = document.getElementById('w-editor-content');
        let skipped = 0;
        const flashed = [];
        plan.gaps.forEach(g => {
            if (g.busy) { skipped++; return; }
            // 足す位置——消すブロックの先頭の前・無ければ後ろに残すブロックの前・無ければ前に残すブロックの後ろ。
            const ref = g.removed.length ? wrapperOf(g.removed[0]) : (g.after ? wrapperOf(g.after) : null);
            let prev = g.before ? wrapperOf(g.before) : null;
            g.added.forEach((node, k) => {
                if (ref) editor.insertBefore(node, ref);
                else if (prev) prev.after(node);
                else editor.appendChild(node);
                const wrap = wrapInBlock(node);
                viewSource.set(node, g.addedHTML[k]);
                prev = wrap;
                flashed.push(wrap);
            });
            g.removed.forEach(el => wrapperOf(el).remove());
        });
        if (flashed.length || plan.gaps.some(g => !g.busy)) {
            buildToc();
            enhanceFileSections();
            decorateVocabBlocks();
            applyMode();
            loadPageMeta();
        }
        flashed.forEach(w => w.classList.add('w-block-updated'));
        if (flashed.length) setTimeout(() => flashed.forEach(w => w.classList.remove('w-block-updated')), 4000);
        return skipped;
    }

    // schedulePageCheck は知らせを受けて、少し待ってから読み直す（続けて来たら待ち直す・長くても PAGE_CHECK_MAX_MS）。
    function schedulePageCheck(by) {
        if (by) pageUpdateBy = by;
        const now = Date.now();
        if (!pageCheckTimer) pageCheckFirstAt = now;
        clearTimeout(pageCheckTimer);
        const wait = Math.max(0, Math.min(PAGE_CHECK_WAIT_MS, pageCheckFirstAt + PAGE_CHECK_MAX_MS - now));
        pageCheckTimer = setTimeout(checkPageUpdate, wait);
    }

    // checkPageUpdate は読み直して比べ、変わったところがあれば知らせる（まだ差し替えない）。
    async function checkPageUpdate() {
        pageCheckTimer = null;
        if (document.body.hasAttribute('edit-mode')) return;
        let html;
        try {
            // 知らせを受けて自動に読む——操作ではない（セッションを延ばさない）。
            const res = await fetch('/api/load?id=' + currentPageId + '&view=1', { headers: BG_HEADERS });
            if (!res.ok) return;
            html = await res.text();
        } catch (e) { return; }
        if (document.body.hasAttribute('edit-mode')) return; // 読んでいるあいだに編集へ入った
        const plan = planPageUpdate(html);
        if (!plan.places) { pendingPageHTML = null; dismissToast('page-updated'); return; }
        pendingPageHTML = html;
        const who = !pageUpdateBy ? 'このページが更新されました'
            : pageUpdateBy === currentUsername ? 'あなたの操作でこのページが更新されました'
            : '「' + pageUpdateBy + '」がこのページを更新しました';
        notify(who + '（' + plan.places + ' か所）。', {
            type: 'info', duration: 0, id: 'page-updated',
            action: { label: '変わったところを読み込む', onClick: () => applyPendingPageUpdate() },
        });
    }

    // applyPendingPageUpdate は「変わったところを読み込む」——知らせたときに読んだ本文で、いまの画面と比べ直して当てる。
    function applyPendingPageUpdate() {
        if (!pendingPageHTML || document.body.hasAttribute('edit-mode')) return;
        const html = pendingPageHTML;
        pendingPageHTML = null;
        const skipped = applyPageUpdate(planPageUpdate(html));
        pageUpdateBy = '';
        if (skipped) {
            pendingPageHTML = html;
            notify('入力中の欄があるところ（' + skipped + ' か所）は読み込んでいません。入力を終えてからもう一度押してください。', {
                type: 'warn', duration: 0, id: 'page-updated',
                action: { label: '変わったところを読み込む', onClick: () => applyPendingPageUpdate() },
            });
        }
    }

    // ── ほかの人のキャレットのあるブロック（2026-10-12 昼）──────────────────────────
    // 利用者:「他人が同じブロックにキャレットを置いている、あるいはすでに文字を打ち込んでいるような場合は、早期に警告して欲しいです」
    // →「置いただけで背景色を黄色系の彩度の低い色にしては？説明が多すぎるのでシンプルに」「表ごと」（【考察】編集モードの再考 §8）。
    //   - このタブは、キャレットのある上の段のブロックが変わるたびに POST /api/presence で知らせる（表は表ごと——セルを移っても同じ
    //     ブロック）。タブを隠したら「どこにも置いていない」。サーバーはページの更新の知らせの購読ごとに覚え、同じページのタブへ配る
    //     （{type:"presence"}・つないだ直後の hello にも）——タブを閉じれば購読が切れて消える。
    //   - 受けたタブは、ほかのタブのいるブロックの背景を薄い黄色にするだけ（.w-block-others・名前はマウスを載せると出る）。書ける形のときだけ。
    let presenceSent = null;   // 送った（このタブのキャレットのある）ブロック——null はまだ送っていない
    let presenceTimer = null;
    let othersPresence = [];   // [{tab, user, block}]（自分のタブも入っている——描くときに除く）

    // caretBlockId は、このタブのキャレットのある上の段のブロックの ID（書ける形で、本文にフォーカスがあるとき）。
    function caretBlockId() {
        if (!document.body.hasAttribute('edit-mode') || document.visibilityState === 'hidden') return '';
        const a = document.activeElement;
        if (!a || !a.closest || !a.closest('#w-editor-content')) return '';
        const b = topBlockOf(a);
        return b ? (b.getAttribute(BLOCK_ID_ATTR) || '') : '';
    }

    // updatePresence は、キャレットのあるブロックが変わっていれば知らせる（immediate でなければ少し待つ——ブロックを渡り歩くあいだは送らない）。
    function updatePresence(immediate) {
        clearTimeout(presenceTimer);
        const run = () => {
            if (!currentPageId) return;
            const id = caretBlockId();
            if (id === presenceSent || (presenceSent === null && id === '')) { presenceSent = id; return; }
            presenceSent = id;
            fetch('/api/presence', {
                method: 'POST', headers: { 'Content-Type': 'application/json' }, keepalive: true,
                body: JSON.stringify({ id: currentPageId, tab: TAB_ID, block: id }),
            }).catch(() => { /* 届かなくても書くことはできる——次に動いたときにまた送る */ });
        };
        if (immediate) run(); else presenceTimer = setTimeout(run, 250);
    }

    // renderPresence は、ほかのタブのいるブロックの背景を薄い黄色にする（名前はマウスを載せると出る）。
    function renderPresence() {
        document.querySelectorAll('#w-editor-content .w-block-others').forEach(el => {
            el.classList.remove('w-block-others');
            if (el.dataset.othersTitle) { el.removeAttribute('title'); delete el.dataset.othersTitle; }
        });
        if (!document.body.hasAttribute('edit-mode')) return;
        const byBlock = new Map();
        othersPresence.forEach(p => {
            if (!p || p.tab === TAB_ID || !p.block) return;
            if (!byBlock.has(p.block)) byBlock.set(p.block, new Set());
            byBlock.get(p.block).add(p.user || 'ほかの人');
        });
        byBlock.forEach((users, id) => {
            const el = blockElementById(id);
            const wrap = el && (el.closest('.editor-block') || el);
            if (!wrap) return;
            wrap.classList.add('w-block-others');
            if (!wrap.hasAttribute('title')) {
                wrap.title = Array.from(users).join('・');
                wrap.dataset.othersTitle = '1';
            }
        });
    }

    // setupPresence は、キャレットの移り・タブの出入りで知らせる引き金を配線する。
    function setupPresence() {
        document.addEventListener('focusin', () => updatePresence(false), true);
        document.addEventListener('focusout', () => updatePresence(false), true);
        document.addEventListener('visibilitychange', () => updatePresence(true));
        // タブを閉じたら、購読（/api/page-events）が切れてサーバーが印を消す——ここで送らなくてよい。
    }

    // openPageEvents は、このページの更新の知らせを購読する（タブを開いているあいだずっと・EventSource は自分でつなぎ直す）。
    function openPageEvents() {
        if (pageES || !window.EventSource) return;
        // tab はこのタブの印（2026-10-12）——自分の保存の知らせを見分ける・ページを消すときに「開いている人」に数えられる。
        pageES = new EventSource('/api/page-events?id=' + currentPageId + '&tab=' + TAB_ID);
        pageES.onmessage = (e) => {
            let ev; try { ev = JSON.parse(e.data); } catch (_) { return; }
            // ほかの人のキャレットのあるブロック（2026-10-12 昼）——つないだ直後（hello）にも入っている。つなぎ直したら送り直す。
            if (ev.type === 'presence') { othersPresence = ev.presence || []; renderPresence(); return; }
            if (ev.type === 'hello') { othersPresence = ev.presence || []; renderPresence(); presenceSent = null; updatePresence(true); }
            if (ev.type === 'updated' && ev.tab === TAB_ID) return; // このタブ自身の保存
            if (document.body.hasAttribute('edit-mode')) {
                // 書ける形では、ほかの人（業務のボタンを含む）の書き込みをその場で受け取る。少し待つのは相手の保存が続くあいだ。
                // 書いている最中のブロックは送らず、相手の版も受け取らない——並べるのは去って送ったとき（2026-10-12 昼。
                // 未明の形〔問い3〕は受け取ったときに並べていた）。
                if (ev.type === 'updated' || (ev.type === 'hello' && loadedUpdatedAt && ev.updated_at && ev.updated_at !== loadedUpdatedAt)) {
                    clearTimeout(editPullTimer);
                    editPullTimer = setTimeout(() => { pullFromServer(); }, 300);
                }
                return;
            }
            if (ev.type === 'updated') schedulePageCheck(ev.by || '');
            // つなぎ直したとき——切れていたあいだに書かれていたら比べる（開いた直後は属性をまだ読んでいなければ見送る）。
            else if (ev.type === 'hello' && loadedUpdatedAt && ev.updated_at && ev.updated_at !== loadedUpdatedAt) schedulePageCheck('');
        };
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
    // 本文の語彙（要素名 → 保存する属性）は **サーバーの /api/tag-schema** から取得する。
    // サニタイザの許可リストと語彙レジストリが唯一の正本で、サニタイズ許可リストも同じ
    // 宣言から作られるため、「保存する属性」と「許可される属性」が食い違いようがない。
    // ここに手書きの表を持たない。
    let tagSchema = null;              // { "p": ["data-id"], "table": ["data-id", "data-type"], ... }
    let voidTags = new Set();          // 終了タグを書かない要素（br・img 等）
    // 語彙レジストリ（①）の形式定義。スラッシュメニューの項目と挿入骨格
    // （<table data-type> / <dl data-type>）はここから生成する（語彙モデル §7 の原則1:
    // エディタに手書きの形式リストを置かない）。
    let vocabDefs = [];                // [{ type, display_name, icon, element, columns: [...] }, ...]
    // 見出し語の辞書。手書きせず /api/tag-schema から受け取る（サーバーの
    // resolveColumnType と同じ辞書＝検証と索引の型判定が食い違わない）。
    // **1語につき1件で、型と選択肢を一緒に持ちます**（2026-09-14 に
    // type_inference と tag_enums の2本を畳んだ）。
    let vocabWords = {};               // { "数量": {type:"number"}, "在籍": {type:"enum", values:[...]}, ... }
    // **表ごとの例外**（設定の `table_vocabulary`・2026-09-26・DBの日本語化 4-2）。
    // 表の列の型と選択肢は `columnWord(表, 列)` が **例外 → vocabWords → 文字列** の順で
    // 引きます（サーバーの `ColumnWord` と同じ順）。
    let tableWords = {};               // { "部材発注明細表": { "状態": {type:"enum", values:[...]} }, ... }
    // **載っている拡張**（/api/tag-schema の `extensions`・2026-09-15）。拡張の画面はこの
    // ファイルに居るので、サーバーからビルドタグで外しても**ボタンは残り、押すと404が
    // エラーの通知になっていました**（「🤖 解析」「✉️ 返信」「未分類へ戻す」）。
    // **null は「知らされていない」**——取得に失敗したときは出し分けをせず、従来どおり
    // 全部出します（出ないより、押して断られるほうが原因に気づけるため）。
    let loadedExtensions = null;
    // **SQL で引くとき気をつける名前の事実**（/api/tag-schema の `sql_names`・2026-09-28）。
    // null なら薄赤を付けない（知らされていない）。
    let sqlNames = null;

    // hasExtension は拡張が載っているかを返します（知らされていなければ true）。
    function hasExtension(id) {
        return loadedExtensions === null || loadedExtensions.has(id);
    }

    async function loadTagSchema() {
        try {
            const res = await fetch('/api/tag-schema');
            if (!res.ok) throw new Error('tag-schema ' + res.status);
            const d = await res.json();
            tagSchema = (d && d.elements) || {};
            voidTags = new Set((d && d.void) || []);
            vocabDefs = (d && d.vocab) || [];
            vocabWords = (d && d.vocabulary) || {};
            tableWords = (d && d.table_vocabulary) || {};
            sqlNames = (d && d.sql_names) || null;
            loadedExtensions = (d && Array.isArray(d.extensions)) ? new Set(d.extensions) : null;
            // 通信の拡張が無ければメールの一覧の入口を下げる（ログインの確かめより後に届くことがある）。
            const mailsLink = document.getElementById('w-mails-link');
            if (mailsLink && !hasExtension('comm')) setHidden(mailsLink, true);
            // **列型の一覧はサーバーが持ちます**（手書きだと型を足した日に古くなる）。
            // 空で返ってきたら初期値を守ります——「明示した型が全部無視される」より、
            // 一手前の一覧で動いているほうが害が小さいためです。
            if (d && Array.isArray(d.column_types) && d.column_types.length) {
                VALID_COL_TYPES = d.column_types;
            }
            fillColTypeOptions();
        } catch (e) {
            console.error('本文の語彙を取得できませんでした:', e);
            // **null のままにする。** 空オブジェクトを入れるとガード `if (!tagSchema)` が
            // 素通りし、語彙が空＝全要素が保存対象外になって、打った内容が1文字も
            // 保存されないのに「保存済」と出続けた（2026-08-21 修正）。
            tagSchema = null;
            voidTags = new Set();
            vocabDefs = [];
            vocabWords = {};
            tableWords = {};
            loadedExtensions = null;
        }
    }

    // ── タグの値を、表にあるかどうかで色分けする（2026-09-13）────────────
    //
    // ユーザー:「語彙に無いものは背景色で区別すればよいのでは？」。
    //
    // **すでにこの方針が書いてありました**——「型不一致の通知（語彙モデル §5.1:
    // 検証して通知する。**拒否はしない**）」。表のセルには効いていて、タグには
    // 効いていなかっただけです。色の言葉も揃えます:
    //
    //   薄青 … 表にある値（機械が読める・`.cell-known` と同じ）
    //   薄黄 … 表に無い値（書けるが、見慣れない・`th[data-type]` と同じ黄）
    //
    // **拒否しないのは、拒否が見せかけだから**です。本文は人が書くもので、編集モードで
    // 何でも打てる以上、入口で弾いても本文には入ります。**見えるほうが直せます**
    // ——`仕入先` と `仕入れ先` の揺れは、弾かれるのではなく**目に付く**ことで消えます。
    //
    // 印は class なのでサニタイズもシリアライズも通りません（保存されない・実行時だけ）。
    // ── 名前のほう: 辞書が知っている語か（2026-09-13）────────────────────
    //
    // ユーザー:「タグの標準機能として語彙にあれば色付けしてはどうでしょう？
    // 汎用的な機能と思います」。
    //
    // タグの名前は**いつも薄青**でした（「薄青＝機械が読む」——どのタグも索引に
    // 入るので、これ自体は本当です）。足りなかったのは**辞書がその語を知っているか**
    // ——型や選択肢が決まっているかどうかで、そこが同じ見た目でした。
    //
    // **機能見出しの `.vocab-word` と同じ作り**にします: 編集モードでだけ、
    // 辞書に無い名前を静かな灰色にする。書き手は自分の言葉が効いているかを
    // その場で確かめられ、打ち間違い（`受信日時` を `受信時刻`）が目に見えます。
    // 閲覧モードで変えないのは、読む人には「索引に入っている」で十分だからです。
    //
    // **灰色は間違いの印ではありません。** 知らない語も書けて索引にも入ります
    // （語彙モデル §5.1: 検証して通知する。拒否はしない）。
    function markTagVocabulary() {
        const host = document.getElementById('w-editor-content');
        if (!host) return;
        host.querySelectorAll('dl[data-type="tags"] > dd').forEach(dd => {
            dd.classList.remove('tag-known', 'tag-unlisted');
        });
        host.querySelectorAll('dl[data-type="tags"] > dt').forEach(dt => {
            dt.classList.remove('tag-name-known');
            const name = dt.textContent.trim();
            const word = vocabWords[name];
            const type = word && word.type;
            const values = word && word.values;

            // ① 名前——辞書（型 or 選択肢）が知っている語か。
            //
            // **薄青は取り上げません**（どのタグも索引に入るので、あれは全部に付く
            // 「DBに入る」の印）。足すのは別の軸で、点線の下線＝型や選択肢が決まっている語。
            // `受信日時` と `受信時刻` はどちらも索引に入りますが、後者は型が決まらず
            // 参照にも日時にもなりません——下線が出ないことがその場での合図です。
            if (type || values) {
                dt.classList.add('tag-name-known');
                dt.title = values ? ('辞書にある語／選択肢: ' + values.join('・'))
                    : ('辞書にある語／型: ' + type);
            } else {
                dt.title = '辞書にない語です（書けますし索引にも入りますが、'
                    + '型は決まりません。決めるなら config/settings.json へ）';
            }

            // ② 値——選択肢が決まっている名前なら、その中にあるか。
            if (!values || !values.length) return;
            const dd = dt.nextElementSibling;
            if (!dd || dd.tagName.toLowerCase() !== 'dd') return;
            const text = dd.textContent.trim();
            if (text === '') return; // 空欄は「まだ書いていない」で、間違いではない
            dd.classList.toggle('tag-known', values.indexOf(text) >= 0);
            dd.classList.toggle('tag-unlisted', values.indexOf(text) < 0);
        });
    }

    // isCustomTag は名前にハイフンを含む要素（＝HTMLの仕様上かならずカスタム要素）かを返す。
    // 語彙モデルへの移行完了（2026-08-20）で本文の語彙からカスタム要素は無くなったので、
    // 通常は常に false。貼り付け等で紛れ込んだ未知のカスタム要素を編集可能にしない
    // ための防御として残している（保存時はサニタイザ同様アンラップされる）。
    function isCustomTag(name) { return name.indexOf('-') !== -1; }

    // sectionDefOf はセクションの形式を解決する——data-type 属性か、鏡の印（data-mirror）
    // （サーバーの vocabTypeOf と同じ規則。walk.go）。
    // ⚠ **節の見出しでは名乗れません**（2026-09-29 に廃止・DBの日本語化 5段目の4）——見出しは人が読む
    // 言葉で、表は自分の caption で名乗る（利用者:「最終的には節の中のPDFを節の外にも動かせるようにしたい」）。
    // ⚠ **鏡（view）は data-mirror の印だけで名乗る**（サーバーと同じ）——名前の見えない
    // data-type の印は 2026-09-27 朝に廃止、見出しの節（<section><h2>必要部材表</h2>）で
    // 名乗る形も 2026-09-28 に廃止した（両環境を data-mirror へ移し終えた・利用者:「古い形の
    // 鏡を読む道具とコードを消してよいです」）。見出しで解くのは表・節の形式だけ。
    function sectionDefOf(section) {
        const t = section.getAttribute && section.getAttribute('data-type');
        if (t) {
            const def = vocabDefs.find(v => v.type === t) || null;
            if (!def || !def.view) return def;
        }
        // 鏡の印（data-mirror="表示名"・2026-09-27）——サーバーの vocabTypeOf と同じ規則。
        const m = section.getAttribute && section.getAttribute('data-mirror');
        if (m) return mirrorDefOf(m);
        return null;
    }

    // mirrorDefOf は鏡の印の値（表示名）から鏡の形式を引く。鏡でない名前は null。
    function mirrorDefOf(name) {
        const w = String(name || '').trim();
        return vocabDefs.find(v => v.view && v.display_name === w) || null;
    }

    // isRetiredViewMarker は、廃止した**名前の見えない鏡の印**（`<section data-type="child-list">`）
    // かを返す。描かれないので、編集モードで赤く知らせる（decorateVocabBlocks）。
    function isRetiredViewMarker(type) {
        const def = vocabDefs.find(v => v.type === type);
        return !!(def && def.view);
    }

    // tableDefOf は表の列宣言を解決する——自分の data-type が正、次に自分の caption、無ければ**一番近い**
    // 包んでいる section の形式（data-type の節だけ——見出しの節は形式を持たない・2026-09-29）。その形式が
    // 明細（items）を宣言していれば素の表は明細（サーバーの syncVocabSection と同じ規則）。どれにも当たらなければ
    // null＝機械に読まれない素の表（型検証も掛けない——読まれないものに赤印は出さない）。
    function tableDefOf(table) {
        const t = table.getAttribute('data-type');
        if (t) return vocabDefs.find(v => v.type === t) || null;
        // 表は自分の <caption> で名乗れる（サーバーの vocabTypeOf ③と同じ規則）。
        // ⚠ 機械が書く明細（受注明細表・部材発注明細表・発注部材表）はこの形で、`data-type` を
        // 書かない——ここで caption を見ないと、その表だけ列型の検証と折り返しが掛からない。
        const cap = table.querySelector(':scope > caption');
        if (cap) {
            const cdef = vocabDefs.find(v => v.display_name === cap.textContent.trim());
            if (cdef) return cdef;
        }
        const sec = table.closest('section');
        if (!sec) return null;
        const sdef = sectionDefOf(sec);
        if (!sdef) return null;
        if (sdef.items) {
            const idef = vocabDefs.find(v => v.type === sdef.items);
            if (idef) return idef;
        }
        return sdef;
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
    // data-id は本文の上では**任意**の属性（手で書いたHTMLもそのまま扱えるようにするため）。
    // 保存（/api/sync）はブロックを ID で当てるので、送る前に上の段のブロックへ振る（ensureBlockIds）。
    // 2026-10-12 までは、付いていればそのブロックだけを保存（/api/save-block）、無ければ全文保存（/api/save）だった。
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
        // ⚠ 画面が本文の直下へ足した飾り付け（.vocab-chrome——メールの返信の箱・整理のボタン・☎ など）は本文ではない
        //    （2026-10-12——業務のボタンを書ける形でも出すようにしたので、保存の巡りに入ると本文として書き出される）。
        if (el.classList.contains('vocab-chrome')) return false;
        return !!tagSchema[el.tagName.toLowerCase()];
    }

    // ensureBlockIds は未採番のブロックへIDを振り、**重複も修復する**。変更した数を返す。
    //
    // 既存ページは初回の編集時にここで採番され、そのブロックを /api/sync で送ったときに正本へ永続化される。
    // 重複修復が要るのは、ページ間でブロックをコピーするとIDが重複しうるため
    // （重複したままだと、どのブロックかを ID で見分けられない。2026-10-12 までは、サーバーが差し替え対象を
    // 特定できず 409 で全文保存に落ち続けた）。
    //
    // within を渡すと、入れ子へ振るのは**その上の段のブロックの中だけ**（2026-10-12）——書ける形がパソコンの既定になったので、
    // 開いて見ているだけで入れ子の表・行へ ID を振って保存すると、見ただけのページが書き換わる（更新日時が進み、ほかの人へ
    // 知らせが飛ぶ）。上の段の ID だけは常に揃える（どのブロックかを ID で見分けるため）。空の Set なら上の段だけ。
    function ensureBlockIds(within) {
        const used = new Set();
        const seen = new Set();
        let changed = 0;
        const scope = within ? Array.from(within) : null;
        const inScope = el => !scope || scope.some(t => t === el || t.contains(el));
        const assign = el => {
            // ⚠ **同じ要素を2度数えない**（2026-10-07 に直した）——上の段の表・節・定義リストは、上の段の巡りと
            //    下の「入れ子」の巡り（`.block-content table` は上の段の表にも当たる）の両方に来る。2度目は自分の ID を
            //    「重複」とみなして振り直していたので、**編集して保存するたびに上の段の表と節の ID が変わり**、
            //    「ページID-ブロックID」の参照が切れ、ページの更新の知らせが触っていない表まで「変わった」に数えていた。
            if (seen.has(el)) return;
            seen.add(el);
            const id = el.getAttribute(BLOCK_ID_ATTR);
            if (id && !used.has(id)) { used.add(id); return; } // 一意なのでそのまま
            const fresh = newBlockId(used);                     // 未採番 or 重複 → 振り直す
            el.setAttribute(BLOCK_ID_ATTR, fresh);
            used.add(fresh);
            changed++;
        };
        topLevelBlocks().forEach(assign);
        // 入れ子の表・定義リスト・セクションにも振る（2026-08-31・§9.3）。
        // 参照の宛先は「ページID-ブロックID」なので、セクションの中の明細表も
        // 単独で指せる必要がある（「PDFや表を文書に貼り付けると、IDが付く必要が
        // あるのかもしれません」——ユーザー）。クロームの中は保存されないので振らない。
        const editor = document.getElementById('w-editor-content');
        if (editor) {
            editor.querySelectorAll('.block-content table, .block-content dl, .block-content section')
                .forEach(el => {
                    if (el.closest('.vocab-chrome')) return;
                    if (!inScope(el)) { const id = el.getAttribute(BLOCK_ID_ATTR); if (id) used.add(id); return; }
                    assign(el);
                });
            // **語彙の表のデータ行にもIDを振る**（2026-09-03）。
            // 構成部品の行の `data-id` が社内コードの後半になり、外注加工に出す紙に
            // 載ります（ユーザー:「外注加工の時に構成部品の番号が効いてきます」）。
            // 見出し行（th だけの行）は飛び先にならないので振りません。
            editor.querySelectorAll('.block-content table[data-type] tr')
                .forEach(tr => {
                    if (tr.closest('.vocab-chrome')) return;
                    if (!inScope(tr)) { const id = tr.getAttribute(BLOCK_ID_ATTR); if (id) used.add(id); return; }
                    if (!tr.querySelector('td')) return; // 見出し行
                    assign(tr);
                });
        }
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

    // tableSortOriginal は、閲覧モードで並べ替えた `tbody` → 並べ替える前の子ノードの並び（下の serializeChildren と
    // 「表の列の題を押して並べ替える」が使う）。
    const tableSortOriginal = new WeakMap();
    // formulaOriginal は、閲覧モードで式の結果を見せている `td` → 式のときの子ノード（下の「表のセルの式」・書き出しは式で）。
    const formulaOriginal = new WeakMap();

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
    //
    // ⚠ 閲覧モードで列の題を押して並べ替えた表（下の「表の列の題を押して並べ替える」）は、`tbody` の行が DOM の上で
    //    動いています。並べ替えは表示だけなので、書き出しは**並べ替える前の並び**で行います（tableSortOriginal）。
    // ⚠ 閲覧モードで式の結果を見せているセル（下の「表のセルの式」）も、書き出しは**式**で行います（formulaOriginal）。
    function serializeChildren(el, indent) {
        const pretty = WHITESPACE_INSENSITIVE.has(el.tagName.toLowerCase());
        let out = '';
        const kids = tableSortOriginal.has(el)
            ? tableSortOriginal.get(el).filter(n => n.parentNode === el)
            : formulaOriginal.has(el) ? formulaOriginal.get(el) : el.childNodes;
        kids.forEach(node => {
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
    // HTML ソースの表示にも、どのブロックが変わったかの判定（/api/sync で送るもの）にも同じ結果を使う。
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
    // 一意な仮番号（空のまま複数の発注書を作ると見分けが付かないため。
    // 旧 createComponentElement の 'PO-' 採番と同じ理由）。
    function defaultFieldValue(col) {
        if (col.type === 'date') return new Date().toISOString().split('T')[0];
        if (col.field === 'order-no') return 'PO-' + Date.now().toString().slice(-6);
        return '';
    }

    // buildPlainDl は素の dl（ヘッダ）を組む。鍵は dt の表示文字。
    function buildPlainDl(columns) {
        const dl = document.createElement('dl');
        (columns || []).forEach(col => {
            const dt = document.createElement('dt');
            dt.textContent = col.label;
            const dd = document.createElement('dd');
            const v = defaultFieldValue(col);
            if (v) dd.textContent = v;
            else dd.appendChild(document.createElement('br')); // 空 dd のキャレット足場
            dl.appendChild(dt);
            dl.appendChild(dd);
        });
        return dl;
    }

    // buildPlainTable は素の表を組む。最初の tr の th が列を宣言する
    // （「THに表示される文字列が、すなわち列のデータを表します」——2026-08-31 ユーザー）。
    function buildPlainTable(columns) {
        const table = document.createElement('table');
        const tbody = document.createElement('tbody');
        const head = document.createElement('tr');
        const row = document.createElement('tr');
        (columns || []).forEach(col => {
            const th = document.createElement('th');
            th.textContent = col.label;
            head.appendChild(th);
            row.appendChild(document.createElement('td'));
        });
        tbody.appendChild(head);
        tbody.appendChild(row);
        table.appendChild(tbody);
        return table;
    }

    // buildCaptionTable は**キャプションで名乗る表**を組みます（2026-09-26・DBの日本語化 5段目）。
    //
    // 利用者:「全面的にキャプションに置き換える」。表の写し（`data/tables.db`）は**キャプションの
    // ある表だけ**を読むので、それまでの見出し形（`<section><h2>材料</h2><table>`）と
    // `data-type` 形で挿した表は、**書いても表の写しに入りませんでした**。
    // ⚠ `data-type` は書きません（機械が書く表と同じ・09-23）——名前は見える文字だけが持つ。
    function buildCaptionTable(name, columns) {
        const table = buildPlainTable(columns);
        const cap = document.createElement('caption');
        cap.textContent = name;
        table.insertBefore(cap, table.firstChild);
        return table;
    }

    function buildVocabSkeleton(def) {
        // **表の種類は全部キャプションで挿します**（上の buildCaptionTable）。鏡は下の
        // data-mirror の印1つ。それ以外（ファイル表示など属性で配線するもの）は data-type の節。
        // ⚠ 見出しの節（<section><h2>表示名</h2>）で挿す枝は 2026-09-29 に消した——節の見出しでは
        //    名乗れなくなった（DBの日本語化 5段目の4）。そのころ通る種類は既に無かった。
        if (def.element === 'table') {
            return buildCaptionTable(def.display_name || def.type, def.columns);
        }
        // **鏡（view）は印1つ**——<section data-mirror="必要部材表"></section>（2026-09-27 利用者:
        // 「保存するHTMLにも『表と挿げ替えるためのタグ』が記録される…わかりやすいシングルタグに」
        // 「閉じタグがあっても良いです」）。値は表示名で、中身はサーバーが読むたびに描く。
        if (def.view) {
            const sec = document.createElement('section');
            sec.setAttribute('data-mirror', def.display_name || def.type);
            return sec;
        }
        if (def.element === 'section') {
            // 業務文書ブロック（論点A・案1）: section が ヘッダ dl（data-type 無し）と
            // 明細表を包む。項目の鍵は dt の表示文字が運ぶ。file 容器は列を持たない。
            const sec = document.createElement('section');
            sec.setAttribute('data-type', def.type);
            if (def.columns && def.columns.length) {
                sec.appendChild(buildPlainDl(def.columns)); // 空 dd にはキャレットの足場の br（emptyEditable 参照）
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
        // ⚠ **表の種類はここまで来ません**（冒頭でキャプション形に返す）。2026-09-26 まで
        // ここに `<table data-type="…">` を組む枝がありました。
        return null;
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
            // かつて受発注は file 容器で包んで挿していたが、容器は廃止した
            // （PDFの取り付け台は形式自身の File 宣言が担う。2026-08-31）。
        } else if (type === 'h1' || type === 'h2' || type === 'h3') {
            // 見出しは H3 まで（2026-10-01 利用者:「見出しはH3程度まで必要と思います」——それまではメニューに H1 だけ）。
            newEl = document.createElement(type);
            newEl.innerText = '';
        } else if (type === 'p') {
            newEl = document.createElement('p');
            newEl.innerText = '';
        } else if (type === 'table') {
            // 汎用の素の表（D-2 の帰結。2026-08-31 ユーザー:「見出しが型を表すとすると、
            // エディタのメニューは単なる表挿入に変わるのでは？」）——そのとおりで、
            // 型は見える言葉が担う。素の表を挿し、見出しに登録済みの言葉を書けば
            // 定型項目とまったく同じに認識される。定型項目は列名を覚えなくて済む
            // **下書き**として残る。
            newEl = buildPlainTable([{ label: '' }, { label: '' }]);
        } else if (type === 'dl') {
            newEl = buildPlainDl([{ label: '' }]);
        } else if (type === 'section') {
            // **見出しの節**（2026-09-27）——見出しの言葉が節の名前になる。テンプレートでは
            // **機械が中身を入れる器**です（メールの「本文」「添付ファイル」・部材発注書の「備考」・
            // 加工製品の「図面」）。利用者の選択:「見出しの節」。機械語は書かない（見出しが名乗る）。
            // ⚠ 見出しと段落には空の改行を入れる——空の要素は高さが無く、カーソルを置けない
            //    （見出しに打ったつもりの字が段落へ入った・2026-09-27 に E2E で踏んだ）。
            newEl = document.createElement('section');
            for (const tag of ['h2', 'p']) {
                const el = document.createElement(tag);
                el.appendChild(document.createElement('br'));
                newEl.appendChild(el);
            }
        } else if (type === 'details') {
            // **折りたたみ**（2026-09-28）——題（summary）の言葉がブロックの名前になる
            // （例: 加工製品の「資料 1」）。開いた状態で挿し、中に段落を1つ置く。
            // ⚠ 空の要素は高さが無くカーソルを置けないので、どちらにも改行を入れる（見出しの節と同じ）。
            newEl = document.createElement('details');
            newEl.setAttribute('open', '');
            for (const tag of ['summary', 'p']) {
                const el = document.createElement(tag);
                el.appendChild(document.createElement('br'));
                newEl.appendChild(el);
            }
        }
        if (!newEl) return null;

        // 編集モードで挿すなら、構造HTML（見出し・段落・表・定義リスト…）は編集可能にする。
        // カスタム要素は対象外（applyMode と同じ規則）。鏡の節は見出しごと編集でき、
        // 中身の .vocab-chrome だけが contenteditable="false"（サーバーが付ける）。
        if (isEdit && !isCustomTag(newEl.tagName.toLowerCase())) {
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
        
        afterComponentPlaced(newEl);

        if (isEdit && (newEl.tagName === 'H1' || newEl.tagName === 'H2' || newEl.tagName === 'H3' || newEl.tagName === 'P')) {
            newEl.focus();
        } else if (isEdit && openNewFileViewWire(newEl)) {
            // 空のファイル表示は参照の欄を開いた（openNewFileViewWire）
        } else if (isEdit && (newEl.tagName === 'TABLE' || newEl.tagName === 'DL' || newEl.tagName === 'SECTION')) {
            focusFirstCell(newEl); // 挿入した骨格の最初のセルから入力を始められるように
        }

        return newEl;
    }

    // afterComponentPlaced は、本文に置いた新しいブロックを描き、飾り付けとプレビューを整えます（挿す・置き換えるの2つの道で共通・2026-10-20 にまとめた）。
    function afterComponentPlaced(newEl) {
        if (typeof newEl.render === 'function') {
            newEl.render();
        }
        enhanceFileSections();
        decorateVocabBlocks();
        updateHtmlPreview();
    }

    // applySlashChoice はスラッシュメニューの選択を実行します。
    // 大半は「そのブロックを別の形式へ置き換える」ですが、画像だけは
    // **ファイルを選んでアップロードしてから挿す**非同期の操作なので分けます。
    // クリックと Enter の両方から呼ぶので、分岐はここ1箇所に置きます。
    function applySlashChoice(type, block) {
        // 📎 画像・ファイル（2026-09-30・画像は絵・それ以外はファイル表示）。'image'（🖼 画像）は 10-01 にメニューから
        // 外して1つにした——種類は機械が見分ける。古い呼び出しが来ても同じ道へ。
        if (type === 'image' || type === 'attach') {
            pickAnyFiles(files => { if (files.length) insertFilesAfter(files, block); });
            return;
        }
        replaceBlockWithComponent(type, block);
    }

    function replaceBlockWithComponent(type, block) {
        const isEdit = document.body.hasAttribute('edit-mode');
        const newEl = createComponentElement(type, isEdit);
        if (!newEl) return null;

        const contentWrapper = block.querySelector('.block-content');
        contentWrapper.innerHTML = '';
        contentWrapper.appendChild(newEl);

        afterComponentPlaced(newEl);
        
        if (isEdit && (newEl.tagName === 'H1' || newEl.tagName === 'H2' || newEl.tagName === 'H3' || newEl.tagName === 'P')) {
            newEl.focus();
        } else if (isEdit && (type === 'section' || type === 'details')) {
            // 見出しの節・折りたたみは、まず名前（見出し・題）を打てるように。
            const h = newEl.querySelector(type === 'details' ? 'summary' : 'h2');
            newEl.focus();
            const range = document.createRange();
            range.selectNodeContents(h);
            range.collapse(true);
            const sel = window.getSelection();
            sel.removeAllRanges();
            sel.addRange(range);
        } else if (isEdit && openNewFileViewWire(newEl)) {
            // 空のファイル表示は参照の欄を開いた（openNewFileViewWire）
        } else if (isEdit && (newEl.tagName === 'TABLE' || newEl.tagName === 'DL' || newEl.tagName === 'SECTION')) {
            focusFirstCell(newEl);
        }
        return newEl;
    }

    // openNewFileViewWire は、挿したばかりの**空のファイル表示**なら参照の欄をすぐ開いて true を返します
    // （2026-10-01）——ファイル表示・写真の「🔗 ID」で写した ID を貼るだけで済むように（利用者:「…クリップボードへ
    // コピーできると、ほかの場所にスラッシュメニューから貼りつけたり…」）。
    function openNewFileViewWire(newEl) {
        if (newEl.tagName !== 'SECTION' || newEl.getAttribute('data-type') !== FILE_VIEW_TYPE ||
            newEl.getAttribute(FILE_REF_ATTR)) return false;
        decorateFileViews();
        openFileViewPopover(newEl, newEl.querySelector(':scope > .fv-wire'));
        return true;
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

    // deleteBlock はブロックを丸ごと削除する。確認ダイアログは出さない——
    // 行削除（tableRowDelete）と同じ流儀で、編集中のアンドゥ（Ctrl+Z）で戻せるため。
    // 保存は、本文DOMの変化で setupAutoSave の MutationObserver が未保存の印を付け、🗑 を押した操作のあとの拾い
    // （setupHistoryHooks の click——コマンドにしてすぐ保存）が送る（2026-10-12 昼まではオブザーバの自動保存が走った）。
    // 削除で本文が空になったら空の段落を1つ補い、書き続けられる場所を残す。
    function deleteBlock(block) {
        const neighbor = block.previousElementSibling || block.nextElementSibling;
        block.remove();
        if (topLevelBlocks().length === 0) {
            const p = insertComponent('p');
            if (p && p.focus) p.focus();
        } else if (neighbor && neighbor.classList && neighbor.classList.contains('editor-block')) {
            const content = neighbor.querySelector('.block-content > *');
            if (content && content.focus) content.focus({ preventScroll: true });
        }
        updateHtmlPreview();
    }

    function wrapInBlock(element) {
        if (element.classList && element.classList.contains('editor-block')) return element;
        
        const block = document.createElement('div');
        block.className = 'editor-block';
        
        const controls = document.createElement('div');
        controls.className = 'block-controls';
        controls.contentEditable = 'false';
        
        // ＋はブロックの**下端の境界線上**に出す（2026-08-31 ユーザー要望「上ぞろえだと
        // 離れた位置にブロックが出来るように感じる」）。挿入されるのはこのブロックの
        // 直後なので、ボタンの出る場所＝新しいブロックが生まれる場所になる。
        // コントロール帯（⠿ 🗑）はブロック自身への操作なので上ぞろえのまま分ける。
        const addBtn = document.createElement('button');
        addBtn.className = 'add-btn';
        addBtn.contentEditable = 'false';
        addBtn.textContent = '＋';
        addBtn.title = 'ここにブロックを追加';
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

        const delBtn = document.createElement('button');
        delBtn.className = 'delete-btn';
        delBtn.textContent = '🗑';
        delBtn.title = 'ブロックを削除';
        delBtn.onclick = () => deleteBlock(block);

        controls.appendChild(dragHandle);
        controls.appendChild(delBtn);
        block.appendChild(addBtn); // 境界の住人（.block-content の外＝保存対象外）
        
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

    // ── 節・折りたたみの中の要素にも ⠿・🗑・＋（2026-09-29） ─────────────────────────────
    //
    // 利用者:「図面一枚一枚がブロック（Section）という構造は難しいですか？…ブロックを消したり移動したり
    // 汎用的な操作なので覚える必要もない」「Pもブロックなら…普通に段落ブロックと同じ扱いで、埋め込みPDFも
    // 扱えるのでは？」「最終的には節の中のPDFを節の外にも動かせるようにしたいです」。
    // 上の段のブロックだけが包み（.editor-block）で ⠿・🗑・＋ を持ち、節や折りたたみの中の要素には
    // 手が届かなかった。
    //
    // ⚠ **中の要素は包みません**——保存の組み立ては上の段の包みしか外さないので、中を包むと本文に余計な
    //    div が書き出される。操作の帯は**本文の外に1つだけ**置き、指している要素の左の欄へ浮かべる
    //    （指している間は、その上の段のブロックの操作を隠す——左の欄には指している物の操作だけが出る）。
    // ⚠ **入れ物の名前（節の最初の見出し・折りたたみの題）は動かさない・消さない**——節ごとは上の段の ⠿・🗑。
    //    名前の上へ落とすと、その入れ物の先頭（名前の直後）へ入る（空の入れ物へも入れられる）。
    // 節をまたいで動かしてよいのは、表が caption でだけ名乗るようになったから（2026-09-29 に節の見出しで
    // 名乗る形を廃止）——どこへ動かしても読まれ方は変わらない。
    let innerHover = null;   // 帯が指している中の要素
    let draggedInner = null; // ⠿ で掴んでいる中の要素

    function firstContentChild(box) {
        return Array.from(box.children).find(c => !c.classList.contains('vocab-chrome')) || null;
    }

    // innerBoxOf は、el を「中の要素」として扱えるなら、その入れ物（素の節・本文の折りたたみ）を返す。
    function innerBoxOf(el) {
        if (!el || el.nodeType !== 1 || el.classList.contains('vocab-chrome')) return null;
        const box = el.parentElement;
        if (!box || !box.closest('#w-editor-content') || box.closest('.vocab-chrome')) return null;
        const fold = box.tagName === 'DETAILS' && !box.classList.contains('file-view-fold');
        // カルーセルの中の画像も「中の要素」（⠿ で並べ替え・出し入れ・🗑 で外す——2026-10-07）。
        const sec = box.tagName === 'SECTION' && !box.hasAttribute('data-mirror') &&
            (!box.hasAttribute('data-type') || box.getAttribute('data-type') === CAROUSEL_TYPE);
        if (!fold && !sec) return null;
        if (el.tagName === 'SUMMARY') return null;
        if (sec && /^H[1-6]$/.test(el.tagName) && el === firstContentChild(box)) return null;
        return box;
    }

    // innerTargetAt は、指している場所から見て一番内側の「中の要素」を返す（無ければ null）。
    function innerTargetAt(node) {
        const editor = document.getElementById('w-editor-content');
        let n = node && (node.nodeType === 1 ? node : node.parentElement);
        while (n && n !== editor) {
            if (innerBoxOf(n)) return n;
            n = n.parentElement;
        }
        return null;
    }

    // boxHeadAt は、指している場所が入れ物の名前（節の最初の見出し・折りたたみの題）ならそれを返す。
    function boxHeadAt(node) {
        const editor = document.getElementById('w-editor-content');
        let n = node && (node.nodeType === 1 ? node : node.parentElement);
        while (n && n !== editor) {
            const box = n.parentElement;
            if (box && !box.closest('.vocab-chrome')) {
                if (n.tagName === 'SUMMARY' && box.tagName === 'DETAILS' && !box.classList.contains('file-view-fold')) return n;
                if (/^H[1-6]$/.test(n.tagName) && box.tagName === 'SECTION' && !box.hasAttribute('data-type') &&
                    !box.hasAttribute('data-mirror') && n === firstContentChild(box)) return n;
            }
            n = n.parentElement;
        }
        return null;
    }

    function innerBar() {
        let bar = document.getElementById('w-inner-controls');
        if (bar) return bar;
        bar = document.createElement('div');
        bar.id = 'w-inner-controls';
        bar.className = 'inner-controls';
        bar.contentEditable = 'false';
        const drag = document.createElement('div');
        drag.className = 'drag-handle';
        drag.textContent = '⠿';
        drag.title = 'ドラッグして移動（節や折りたたみの外へも・中へも）';
        drag.draggable = true;
        const del = document.createElement('button');
        del.type = 'button';
        del.className = 'delete-btn';
        del.textContent = '🗑';
        del.title = 'この要素を削除（Ctrl+Z で戻せます）';
        const add = document.createElement('button');
        add.type = 'button';
        add.textContent = '＋';
        add.title = 'この下に追加';
        [del, add].forEach(b => b.addEventListener('mousedown', e => e.preventDefault()));
        del.addEventListener('click', () => { const el = innerHover; hideInnerBar(); if (el) deleteInner(el); });
        add.addEventListener('click', () => { const el = innerHover; hideInnerBar(); if (el) addAfterInner(el); });
        drag.addEventListener('dragstart', e => {
            if (!innerHover) { e.preventDefault(); return; }
            draggedInner = innerHover;
            e.dataTransfer.effectAllowed = 'move';
            try { e.dataTransfer.setData('text/plain', ''); } catch (err) { /* 入れなくても動く */ }
            try { e.dataTransfer.setDragImage(draggedInner, 12, 12); } catch (err) { /* 既定の絵 */ }
            setTimeout(() => { if (draggedInner) draggedInner.classList.add('inner-dragging'); }, 0);
        });
        drag.addEventListener('dragend', endInnerDrag);
        bar.append(drag, del, add);
        document.body.appendChild(bar);
        return bar;
    }

    // showInnerBar は帯を el の左の欄（上の段の操作と同じ列）へ出す。CSP のため位置は CSSOM で当てる。
    function showInnerBar(el) {
        const bar = innerBar();
        innerHover = el;
        const block = el.closest('.editor-block');
        document.querySelectorAll('.editor-block.inner-pointing').forEach(b => {
            if (b !== block) b.classList.remove('inner-pointing');
        });
        if (block) block.classList.add('inner-pointing');
        bar.classList.add('active');
        const content = el.closest('.block-content') || el.parentElement;
        const cr = content.getBoundingClientRect();
        const r = el.getBoundingClientRect();
        bar.style.top = (window.scrollY + r.top) + 'px';
        // スマホ幅では左の欄が無いので、画面の左端で止める（要素に少し重なる）。
        bar.style.left = Math.max(window.scrollX + 2, window.scrollX + cr.left - bar.offsetWidth - 8) + 'px';
    }

    function hideInnerBar() {
        innerHover = null;
        const bar = document.getElementById('w-inner-controls');
        if (bar) bar.classList.remove('active');
        document.querySelectorAll('.editor-block.inner-pointing').forEach(b => b.classList.remove('inner-pointing'));
    }

    // deleteInner は中の要素を消す（ファイル表示なら「外す」と同じ知らせ——ファイルは添付に残る）。
    function deleteInner(el) {
        if (el.matches('section[data-type="file-view"][data-ref]')) { removeFileView(el); return; }
        el.remove();
        updateHtmlPreview();
        triggerAutoSave();
    }

    // addAfterInner は中の要素の直後へ段落を足し、スラッシュメニューを開く（上の段の ＋ と同じ流れ）。
    // ⚠ 置き換えるのは**その段落1つ**（chooseSlashItem の currentSlashNested）——上の段の置き換えを
    //    通すと、包んでいる節ごと置き換わる。
    function addAfterInner(el) {
        const p = document.createElement('p');
        p.textContent = '/';
        el.parentElement.insertBefore(p, el.nextSibling);
        const host = p.closest('[contenteditable="true"]');
        if (host) host.focus({ preventScroll: true });
        const range = document.createRange();
        range.selectNodeContents(p);
        range.collapse(false);
        const sel = window.getSelection();
        sel.removeAllRanges();
        sel.addRange(range);
        showSlashMenu(p);
        updateHtmlPreview();
    }

    // applyNestedChoice はスラッシュメニューの選択で、中の段落1つを置き換える（画像は後ろへ足す）。
    function applyNestedChoice(type, p) {
        if (type === 'image' || type === 'attach') { // 'image' は 10-01 にメニューから外した（古い呼び出しの受け）
            pickAnyFiles(async files => {
                if (!files.length) return;
                await insertFilesAfter(files, p);
                if (!p.textContent.trim() && !p.querySelector('img')) p.remove();
                updateHtmlPreview();
                triggerAutoSave();
            });
            return;
        }
        const el = createComponentElement(type, document.body.hasAttribute('edit-mode'));
        if (!el) return;
        el.removeAttribute('contenteditable'); // 入れ物（節・折りたたみ）が編集の単位
        p.replaceWith(el);
        if (typeof el.render === 'function') el.render();
        enhanceFileSections();
        decorateVocabBlocks();
        decorateFileViews();
        updateHtmlPreview();
        triggerAutoSave();
        if (el.tagName === 'TABLE' || el.tagName === 'DL') focusFirstCell(el);
    }

    function clearInnerDropMarks() {
        document.querySelectorAll('.inner-drop-before, .inner-drop-after').forEach(n =>
            n.classList.remove('inner-drop-before', 'inner-drop-after'));
    }

    function endInnerDrag() {
        if (draggedInner) draggedInner.classList.remove('inner-dragging');
        draggedInner = null;
        clearInnerDropMarks();
    }

    // innerDropPlan は落とす先を決める。上の段どうしの移動は従来の処理（setupDragAndDrop）に任せて null。
    //   inner … 中の要素の前後へ／head … 入れ物の名前の直後（先頭）へ／top … 上の段のブロックの前後へ
    function innerDropPlan(e) {
        const src = draggedInner || (draggedBlock && draggedBlock.querySelector('.block-content > *'));
        if (!src) return null;
        const t = innerTargetAt(e.target);
        if (t && t !== src && !src.contains(t)) {
            const r = t.getBoundingClientRect();
            return { mode: 'inner', ref: t, before: e.clientY < r.top + r.height / 2 };
        }
        const head = boxHeadAt(e.target);
        if (head && !src.contains(head)) return { mode: 'head', ref: head };
        // カルーセルの枠（中の画像の外・空のカルーセルも）へ落とすと末尾へ入る（2026-10-07・名前の見出しが無いので）。
        const car = e.target.closest && e.target.closest('section[data-type="' + CAROUSEL_TYPE + '"]');
        if (car && car !== src && !src.contains(car) && car.closest('#w-editor-content')) return { mode: 'into', ref: car };
        if (draggedInner) {
            const block = e.target.closest && e.target.closest('.editor-block');
            if (block) {
                const r = block.getBoundingClientRect();
                return { mode: 'top', ref: block, before: e.clientY < r.top + r.height / 2 };
            }
        }
        return null;
    }

    function setupInnerBlocks() {
        const editor = document.getElementById('w-editor-content');
        if (!editor) return;
        const pick = (target, x, y) => {
            if (!document.body.hasAttribute('edit-mode') || draggedInner || draggedBlock) {
                if (innerHover && !document.body.hasAttribute('edit-mode')) hideInnerBar();
                return;
            }
            const bar = document.getElementById('w-inner-controls');
            if (bar && bar.contains(target)) return; // 帯の上では消さない
            const el = innerTargetAt(target);
            if (el) { showInnerBar(el); return; }
            if (!innerHover) return;
            // 要素から帯へ手を動かすあいだ（左の欄）は消さない。
            const r = innerHover.getBoundingClientRect();
            const br = bar ? bar.getBoundingClientRect() : r;
            if (y >= r.top && y <= r.bottom && x >= br.left - 4 && x <= r.right) return;
            hideInnerBar();
        };
        // ⚠ **ボタンを押しているあいだは帯を動かさない**——⠿ を押したまま動かし始めた瞬間に「段落から外れた」と
        //    帯を隠すと、掴んだ ⠿ が消えてドラッグが始まらない（2026-09-29 に E2E で踏んだ）。
        document.addEventListener('mousemove', e => { if (!e.buttons) pick(e.target, e.clientX, e.clientY); });
        // 触る画面（スマホ）はマウスを乗せられないので、触ったら出す。
        document.addEventListener('pointerdown', e => {
            if (e.pointerType === 'touch') pick(e.target, e.clientX, e.clientY);
        });

        // ⚠ **捕捉の段で先に受けます**——上の段のブロックの受け手（setupDragAndDrop）より前に、
        //    中の要素が絡む落とし方を引き取る。上の段どうしは素通しする（innerDropPlan が null）。
        document.addEventListener('dragover', e => {
            if (!draggedInner && !draggedBlock) return;
            const plan = innerDropPlan(e);
            clearInnerDropMarks();
            if (!plan) return;
            e.preventDefault();
            e.stopPropagation();
            plan.ref.classList.add(plan.mode === 'head' || !plan.before ? 'inner-drop-after' : 'inner-drop-before');
        }, true);
        document.addEventListener('drop', e => {
            if (!draggedInner && !draggedBlock) return;
            const plan = innerDropPlan(e);
            if (!plan) return;
            e.preventDefault();
            e.stopPropagation();
            const fromBlock = draggedInner ? null : draggedBlock;
            const src = draggedInner || fromBlock.querySelector('.block-content > *');
            clearInnerDropMarks();
            if (plan.mode === 'top') {
                plan.ref.parentNode.insertBefore(src, plan.before ? plan.ref : plan.ref.nextSibling);
                src.setAttribute('contenteditable', 'true'); // 上の段のブロックとして編集できるように
                src.oninput = updateHtmlPreview;
                wrapInBlock(src);
            } else if (plan.mode === 'into') {
                plan.ref.appendChild(src);
                src.removeAttribute('contenteditable'); // 入れ物（カルーセル）が編集の単位
                src.oninput = null;
            } else {
                const at = plan.mode === 'head' ? plan.ref.nextSibling : (plan.before ? plan.ref : plan.ref.nextSibling);
                plan.ref.parentElement.insertBefore(src, at);
                src.removeAttribute('contenteditable'); // 入れ物（節・折りたたみ）が編集の単位
                src.oninput = null;
            }
            if (fromBlock) fromBlock.remove();
            endInnerDrag();
            document.querySelectorAll('.editor-block').forEach(b => b.classList.remove('drag-over', 'dragging'));
            draggedBlock = null;
            decorateFileViews();
            decorateVocabBlocks();
            updateHtmlPreview();
            triggerAutoSave();
        }, true);
        document.addEventListener('dragend', () => { if (draggedInner) endInnerDrag(); clearInnerDropMarks(); }, true);
    }

    // ── 汎用表エディタ: 行・列操作ツールバー（docs/考察/【考察】語彙モデル.md §5.2） ──
    // セルの値はブロックの contenteditable がそのまま担い（値＝中身＝表示される文字）、
    // 行・列の追加・削除・並べ替えをこのツールバーで補う。ボタンを表の中へ挿すと
    // シリアライザが中身を本文として拾ってしまうため、クロームは本文DOMの外
    // （#w-table-toolbar・シェル直下）に置き、キャレットのある行に追従させる（2026-10-19 からパソコンでは
    // 本文の上の固定の帯の中——initFixedBar。追従するのは指で触る端末だけ）。
    let currentTableRow = null;
    let currentTableCell = null; // 列操作・列設定の対象列は「キャレットのあるセル」から決める

    // isServerOwned は el が「サーバー（または描画側）が中身を所有する領域」の中に
    // あるかを返す。中身が .vocab-chrome に包まれているもの——計算ビューの集計表・
    // 子ページ一覧・エンハンサのクローム——が該当する。
    //
    // ここが true の要素へ編集の手掛かり（行・列の操作、項目の操作、列型の検証）を
    // 出してはいけない。**シリアライザが .vocab-chrome を保存しない**ので、
    // 入れた内容は保存されず、再読み込みで黙って消えるからです（実測で再現。
    // ユーザー決定 a案「そもそもボタンが出ず、触れない」）。
    //
    // 併せてカスタム要素の中も除外する。語彙モデル移行（2026-08-20）でカスタム要素は
    // 0種になったが、貼り付け等で紛れ込んだものを編集可能にしないための防御として残す
    // （isCustomTag と同じ趣旨）。
    function isServerOwned(el) {
        if (!el) return false;
        if (el.closest && el.closest('.vocab-chrome')) return true;
        for (let p = el; p && p.id !== 'w-editor-content'; p = p.parentElement) {
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
        if (!row || isServerOwned(row)) { hideTableToolbar(); return; }

        currentTableRow = row;
        currentTableCell = el && el.closest ? el.closest('td, th') : null;
        bar.classList.add('active');
        if (fixedBars()) return; // 本文の上の固定の帯の中（2026-10-19・initFixedBar）——置き直さない

        // 行の右横へ。入りきらなければ行の左上へ退避する。
        // ⚠ 人がつまみで動かしていれば、その位置に留める（placeToolbar・2026-09-27）。
        const rect = row.getBoundingClientRect();
        let top = rect.top + window.scrollY + (rect.height - bar.offsetHeight) / 2;
        let left = rect.right + window.scrollX + 8;
        if (rect.right + 8 + bar.offsetWidth > document.documentElement.clientWidth) {
            top = rect.top + window.scrollY - bar.offsetHeight - 4;
            left = rect.left + window.scrollX;
        }
        placeToolbar(bar, 'table', top, left);
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
        placeFloating(pop, rect);
    }

    // refreshColPopoverNote は「明示しない場合に効く型」と、それを**どこが決めたか**を示す。
    // ⚠ **2026-09-26 から語彙だけ**（DBの日本語化 4-2）——登録の列の宣言はもう読みません。
    function refreshColPopoverNote(th, key) {
        const note = document.getElementById('w-cp-note');
        const table = th.closest('table');
        const w = columnWord(table ? tableNameOf(table) : '', key);
        if (w.from === 'table') note.textContent = '未指定なら設定の語彙（この表の例外）: ' + w.type;
        else if (w.from === 'word') note.textContent = '未指定なら設定の語彙: ' + w.type;
        else note.textContent = '未指定なら text 扱い（設定の語彙にない名前）';
    }

    // applyColType は選択された型を th の data-type へ反映する（空＝属性を外し推論に戻す）。
    // data-type は MutationObserver の attributeFilter に載っている（setupAutoSave）が、未保存の印は明示的にも付ける
    // （triggerAutoSave——2026-10-12 昼から印を付けるだけ。送るのはブロックを去るときなど）。
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

    // ── リンクの挿入とプロパティ欄 ───────────────────────────────────────
    // 設計は [【考察】添付ファイルの表示と操作.md] §4。ユーザーの経験則
    // 「編集するのにダイアログが出るのは使いにくかった。プロパティ欄があるものが
    // 使いやすかった」（2026-08-31）——**モーダルを出さず、リンクの近傍に欄を出す**。
    // 列設定ポップオーバとまったく同じ形で、値（表示される文字）は contenteditable、
    // 配線（href・表示されない）はこの欄が担う。
    //
    // 宛先は**制限しない**（サニタイザの既定どおり。2026-08-21 決定——埋め込みの
    // `/data/` 制限はリンクには掛けない）。
    let linkPopoverAnchor = null; // 編集対象の <a>

    function hideLinkPopover() {
        const pop = document.getElementById('w-link-popover');
        if (pop) pop.classList.remove('active');
        linkPopoverAnchor = null;
    }

    // openLinkPopover は対象のリンクの直下へプロパティ欄を出す。
    function openLinkPopover(anchor) {
        const pop = document.getElementById('w-link-popover');
        if (!pop || !anchor) return;
        linkPopoverAnchor = anchor;
        document.getElementById('w-lp-href').value = anchor.getAttribute('href') || '';
        pop.classList.add('active');
        const rect = anchor.getBoundingClientRect();
        placeFloating(pop, rect);
    }

    // anchorAtCaret はキャレット位置の <a>（本文の中のもの）を返す。
    function anchorAtCaret() {
        const sel = window.getSelection();
        if (!sel || sel.rangeCount === 0) return null;
        const node = sel.getRangeAt(0).startContainer;
        const el = node.nodeType === Node.TEXT_NODE ? node.parentElement : node;
        if (!el || !el.closest) return null;
        const a = el.closest('a');
        if (!a || !a.closest('#w-editor-content')) return null;
        // サーバーが埋めた計算ビューやクロームの中のリンクは本文ではない。
        return isServerOwned(a) ? null : a;
    }

    // updateLinkPopover はキャレットがリンク上にあるときだけ欄を出す（selectionchange から）。
    function updateLinkPopover() {
        if (!document.body.hasAttribute('edit-mode')) return hideLinkPopover();
        const a = anchorAtCaret();
        if (!a) return hideLinkPopover();
        if (a !== linkPopoverAnchor) openLinkPopover(a);
    }

    // linkSelection は選択された文字を <a> で包む。既にリンクの中なら包み直さず、
    // その場で編集させる（入れ子の a は HTML として不正）。
    function linkSelection() {
        const existing = anchorAtCaret();
        if (existing) { openLinkPopover(existing); return; }

        const sel = window.getSelection();
        if (!sel || sel.rangeCount === 0 || sel.isCollapsed) return; // 未選択なら何もしない
        const range = sel.getRangeAt(0);
        const host = editableHostOf(range.commonAncestorContainer);
        if (!host) return;

        const a = document.createElement('a');
        a.setAttribute('href', '');
        // surroundContents は範囲が要素境界をまたぐと投げる。またぐ選択は
        // 抜き出して包み直す（装飾ボタンと違い、リンクは1つの a に畳んでよい）。
        try {
            range.surroundContents(a);
        } catch (e) {
            a.appendChild(range.extractContents());
            range.insertNode(a);
        }
        if (!a.textContent) { a.remove(); return; }

        updateHtmlPreview();
        triggerAutoSave();
        openLinkPopover(a);
        document.getElementById('w-lp-href').focus();
    }

    // applyLinkHref は欄の値をリンクへ書き戻す。href は MutationObserver の attributeFilter に
    // 載っているが、未保存の印は明示的にも付ける（applyColType と同じ・triggerAutoSave は印を付けるだけ）。
    function applyLinkHref() {
        const a = linkPopoverAnchor;
        if (!a || !a.isConnected) return;
        a.setAttribute('href', document.getElementById('w-lp-href').value);
        updateHtmlPreview();
        triggerAutoSave();
    }

    // unlinkCurrent はリンクを外して**文字だけ残す**。
    function unlinkCurrent() {
        const a = linkPopoverAnchor;
        if (!a || !a.isConnected) { hideLinkPopover(); return; }
        const parent = a.parentNode;
        while (a.firstChild) parent.insertBefore(a.firstChild, a);
        a.remove();
        if (parent.normalize) parent.normalize();
        hideLinkPopover();
        updateHtmlPreview();
        triggerAutoSave();
    }

    // ── ファイル表示（section[data-type="file-view"]）の配線 ─────────────
    //
    // 本文に残るのは `<section data-type="file-view" data-ref="010272-c3p7">` の1つだけで、
    // **中身はサーバーが描きます**。だから編集モードでは、そのままだと何も掴めません
    // ——空の section はクリックもキャレット移動もできないためです。
    //
    // そこで**配線の札**を1つ出し、押すとプロパティ欄が開くようにします。
    // リンクのプロパティ欄とまったく同じ流儀（ユーザー 2026-08-31:「編集するのに
    // ダイアログが出るのは使いにくかった。プロパティ欄があるものが使いやすかった」）。
    const FILE_VIEW_TYPE = 'file-view';
    const FILE_REF_ATTR = 'data-ref'; // internal/cms/file_view.go の FileRefAttr と対
    let fileViewAnchor = null;        // 編集対象の section

    function hideFileViewPopover() {
        const pop = document.getElementById('w-fv-popover');
        if (pop) pop.classList.remove('active');
        fileViewAnchor = null;
    }

    // openFileViewPopover は**札の直下**へ欄を出す。
    //
    // ⚠ section の矩形に付けてはいけません（2026-09-15 コードレビュー #3）——配線済みだと
    // 中に 70vh の枠があるので、下端は画面の外。そこへ `focus()` すると画面が700px飛びます。
    // リンクの欄が小さな `<a>` に付いているのと同じ理由で、小さいものに付けます。
    function openFileViewPopover(sec, bar) {
        const pop = document.getElementById('w-fv-popover');
        if (!pop || !sec) return;
        fileViewAnchor = sec;
        document.getElementById('w-fv-ref').value = sec.getAttribute(FILE_REF_ATTR) || '';
        pop.classList.add('active');
        placeFloating(pop, (bar || sec).getBoundingClientRect());
        document.getElementById('w-fv-ref').focus();
    }

    // applyFileViewRef は欄の値をブロックへ書き戻す。data-ref は MutationObserver の
    // attributeFilter に載らないので、未保存の印は明示的に付ける（triggerAutoSave——印を付けるだけ。
    // 2026-10-12 昼までは、これで保存が走った）。
    function applyFileViewRef() {
        const sec = fileViewAnchor;
        if (!sec || !sec.isConnected) { hideFileViewPopover(); return; }
        const v = document.getElementById('w-fv-ref').value.trim();
        if (v) sec.setAttribute(FILE_REF_ATTR, v);
        else sec.removeAttribute(FILE_REF_ATTR);
        // **前の参照で描いた枠は消します**（コードレビュー #4）。サーバーが描いたクロームは
        // 保存の往復で作り直されないので、残すと札は新しい値・枠は古いPDF、という
        // 食い違いが閲覧モードへ戻るまで続きます。消せば「配線した。枠は次に開いたとき」と
        // 読めます（札が残るので空白にはならない）。
        sec.querySelectorAll(':scope > .vocab-chrome:not(.fv-wire):not(.fv-remove)').forEach(n => n.remove());
        decorateFileViews();
        updateHtmlPreview();
        triggerAutoSave();
    }

    // decorateFileViews は編集モードで各ファイル表示ブロックへ配線の札を出します。
    //
    // **必要なときだけDOMを変えます**（decorateVocabBlocks と同じ理由——本文DOMの変化は
    // 自動保存へ巡ってくるので、毎回付け直すと保存が回り続けます）。
    function decorateFileViews() {
        const editor = document.getElementById('w-editor-content');
        if (!editor) return;
        const isEdit = document.body.hasAttribute('edit-mode');
        // **欄の相手が居なくなっていたら閉じる**（コードレビュー #6）。閲覧モードへ戻った・
        // 本文やブロックが差し替わった（解析の取り直し・ほかの人の書き込みを受け取った——2026-10-12 までは
        // ロック喪失も）とき、欄だけが浮いたまま残ると、
        // 打ったキーが `!sec.isConnected` で黙って捨てられます。
        if (fileViewAnchor && (!isEdit || !fileViewAnchor.isConnected)) hideFileViewPopover();
        // 形式は sectionDefOf で見ます（サーバーの vocabTypeOf と同じ規則）。⚠ 2026-09-29 から節の見出し
        // （`<section><h2>ファイル表示</h2>`）では名乗れません——ファイル表示は `data-type` の節だけ。
        editor.querySelectorAll('section').forEach(sec => {
            if (sec.closest('.vocab-chrome')) return;
            const def = sectionDefOf(sec);
            if (!def || def.type !== FILE_VIEW_TYPE) return;
            let bar = sec.querySelector(':scope > .fv-wire');
            if (!isEdit) {
                // ⚠ **「✕ 外す」も一緒に消す**（2026-09-29 利用者:「編集モードから閲覧モードに変更しても外すという
                //    ボタンが消えません」——札だけ消して、外すの札を残していた）。
                if (bar) bar.remove();
                sec.querySelectorAll(':scope > .fv-remove').forEach(n => n.remove());
                return;
            }
            if (!bar) {
                bar = document.createElement('button');
                bar.type = 'button';
                bar.className = 'vocab-chrome fv-wire';
                bar.contentEditable = 'false';
                bar.addEventListener('mousedown', e => e.preventDefault());
                bar.addEventListener('click', e => {
                    e.preventDefault();
                    openFileViewPopover(sec, bar);
                });
                // **先頭へ置きます**——サーバーが描く枠は末尾に足されるので、
                // 札が下へ流れて見失われないように。
                sec.insertBefore(bar, sec.firstChild);
            }
            const ref = sec.getAttribute(FILE_REF_ATTR) || '';
            const want = ref ? '📄 ファイル表示：' + ref : '📄 ファイル表示：参照を設定してください';
            if (bar.textContent !== want) bar.textContent = want;
            bar.classList.toggle('fv-wire-empty', !ref);
            // **1枚だけ外す**（2026-09-29 利用者:「図面のブロックごと消すことは出来ますが、一枚の図面だけ
            // 消すことが出来ません」）——ブロックの 🗑 は本文の上の段にしか付かないので、節や折りたたみの
            // 中のファイル表示には外す道がありませんでした。⚠ **外すのは表示の印だけ**で、ファイルは
            // 添付に残ります（別の表示から指せる・版にも残る）。確認は出しません（ブロックの 🗑 と同じ——
            // 編集中のアンドゥで戻せる）。
            if (!sec.querySelector(':scope > .fv-remove')) {
                const rm = document.createElement('button');
                rm.type = 'button';
                rm.className = 'vocab-chrome fv-remove';
                rm.contentEditable = 'false';
                rm.textContent = '✕ 外す';
                rm.title = 'このファイル表示を本文から外します（ファイルは添付に残ります・Ctrl+Z で戻せます）';
                rm.addEventListener('mousedown', e => e.preventDefault());
                rm.addEventListener('click', e => {
                    e.preventDefault();
                    removeFileView(sec);
                });
                bar.insertAdjacentElement('afterend', rm);
            }
        });
    }

    // removeFileView はファイル表示の印を本文から外します（ファイルそのものは消さない）。
    // 空になったブロックは、ブロックの 🗑 と同じく本文の上の段なら取り除きます。
    function removeFileView(sec) {
        if (fileViewAnchor === sec) hideFileViewPopover();
        const name = (sec.getAttribute(FILE_REF_ATTR) || '').trim();
        const block = sec.parentElement && sec.parentElement.classList.contains('block-content')
            ? sec.closest('.editor-block') : null;
        if (block) {
            deleteBlock(block); // 上の段のファイル表示はブロックごと（🗑 と同じ）
        } else {
            sec.remove();
            updateHtmlPreview();
        }
        triggerAutoSave();
        notify('ファイル表示を外しました' + (name ? '（' + name + '）' : '') +
            '。ファイルは添付に残っています——戻すなら Ctrl+Z。', { type: 'info', duration: 6000 });
    }

    // ── カルーセル（2026-10-07） ─────────────────────────────────────────
    //
    // 利用者:「カルーセルを入れることは出来ますか？」→ 形は「『カルーセル』ブロックを挿す」（続いた画像を自動でまとめる案は
    // 採らなかった）・画像を押したときの拡大表示も「する」（下の「画像の拡大表示」）。
    //
    // 本文に残るのは `<section data-type="carousel">` と中の画像の段落（`<p><img></p>`）だけ（internal/cms/vocab.go）。
    // 見せ方はここだけが持つ:
    //   - 閲覧モード: **見せる幅が空いている限り次の画像も並べて見せる**（2026-10-12 利用者:「カルーセルについて、見せる領域が
    //     空いている限り次の画像も見せるように並べて見せてください」——それまでは1枚ずつ重ねて見せていた）。横に巻く帯
    //     （overflow-x・枚の端で止まる scroll-snap）で、入りきらなければすぐ下に「‹ 1–3 / 8 ›」（1画面ぶん巻く・端では押せない）。
    //     触る画面ではそのまま横に払って巻く（ブラウザの横スクロール）。
    //   - 編集モード: 枠と「🎠 カルーセル」の帯——「🖼 画像を足す」と「⬇ すぐ下の画像を入れる」（いまのページに縦に並ぶ
    //     写真をまとめるため）。中の画像は節の中の要素と同じく ⠿ で並べ替え・出し入れ、🗑 で外す（innerBoxOf）。
    //   - 帯は .vocab-chrome（保存されない）。⚠ 必要なときだけ DOM を変える（編集モードでは DOM の変化が自動保存へ巡る）。
    const CAROUSEL_TYPE = 'carousel';

    const carouselSlides = car => Array.from(car.children).filter(c => !c.classList.contains('vocab-chrome'));

    // isImageOnlyBlock は、要素が画像だけの段落か（すぐ下の画像をまとめるときの見分け）。
    function isImageOnlyBlock(el) {
        if (!el || el.tagName !== 'P' || !el.querySelector('img')) return false;
        return Array.from(el.childNodes).every(n => (n.nodeType === 3 && !n.textContent.trim()) ||
            (n.nodeType === 1 && (n.tagName === 'IMG' || n.tagName === 'BR')));
    }

    // nextImageBlocks は、カルーセルのすぐ下に続く「画像だけの段落」を返す（上の段なら包みごと・節の中ならその要素）。
    function nextImageBlocks(car) {
        const top = car.parentElement && car.parentElement.classList.contains('block-content') ? car.closest('.editor-block') : null;
        const out = [];
        for (let n = (top || car).nextElementSibling; n; n = n.nextElementSibling) {
            let el = n;
            if (top) {
                const box = n.classList.contains('editor-block') ? n.querySelector(':scope > .block-content') : null;
                el = box ? firstContentChild(box) : null;
            }
            if (!isImageOnlyBlock(el)) break;
            out.push({ el, wrap: top ? n : null });
        }
        return out;
    }

    function carouselButton(cls, text, title, onClick) {
        const b = document.createElement('button');
        b.type = 'button';
        b.className = cls;
        b.contentEditable = 'false';
        b.textContent = text;
        if (title) b.title = title;
        b.addEventListener('mousedown', e => e.preventDefault()); // キャレットを動かさない
        b.addEventListener('click', e => { e.preventDefault(); e.stopPropagation(); onClick(); });
        return b;
    }

    // takeImagesBelow は「⬇ すぐ下の画像を入れる」——カルーセルのすぐ下に続く画像だけの段落を中の末尾へ移す。
    function takeImagesBelow(car) {
        const below = nextImageBlocks(car);
        if (!below.length) {
            notify('このカルーセルのすぐ下に画像がありません（画像だけの段落が続いているところを入れます）。', { type: 'info', duration: 5000 });
            return;
        }
        below.forEach(({ el, wrap }) => {
            car.appendChild(el);
            el.removeAttribute('contenteditable'); // 入れ物（カルーセル）が編集の単位
            el.oninput = null;
            if (wrap) wrap.remove();
        });
        updateHtmlPreview();
        triggerAutoSave();
        notify('すぐ下の画像 ' + below.length + ' 枚をカルーセルに入れました（戻すなら Ctrl+Z）。', { type: 'success', duration: 4000 });
    }

    // addImagesToCarousel は「🖼 画像を足す」——選んだ画像を添付して中の末尾へ足す。
    function addImagesToCarousel(car) {
        pickImageFiles(async files => {
            if (!files.length) return;
            const slides = carouselSlides(car);
            await insertImagesAfter(files, slides.length ? slides[slides.length - 1] : car.querySelector(':scope > .car-edit-bar'));
        });
    }

    // showCarouselSlide は閲覧モードで、その枚が帯の左端に来るまで横に巻く（拡大表示を閉じたとき・番号から）。
    function showCarouselSlide(car, i) {
        const slides = carouselSlides(car);
        const s = slides[Math.max(0, Math.min(i, slides.length - 1))];
        if (!s) return;
        const pad = parseFloat(getComputedStyle(car).paddingLeft) || 0;
        const left = s.getBoundingClientRect().left - car.getBoundingClientRect().left + car.scrollLeft - pad;
        car.scrollTo({ left, behavior: 'auto' });
        refreshCarouselNav(car);
    }

    // carouselNavOf は、カルーセルのすぐ下の送る帯（閲覧モードだけ・.vocab-chrome——帯は横に巻く中に置けないのですぐ下の兄弟）。
    const carouselNavOf = car => { const n = car.nextElementSibling; return n && n.classList.contains('car-nav') ? n : null; };

    // visibleSlides は、いま帯に見えている枚の番号の範囲（真ん中が見える範囲に入っている枚）。
    function visibleSlides(car) {
        const slides = carouselSlides(car);
        const box = car.getBoundingClientRect();
        let first = -1, last = -1;
        slides.forEach((s, k) => {
            const r = s.getBoundingClientRect();
            const mid = r.left + r.width / 2;
            if (mid >= box.left - 1 && mid <= box.right + 1) { if (first < 0) first = k; last = k; }
        });
        return { first, last, n: slides.length };
    }

    // refreshCarouselNav は送る帯を合わせる——全部が見えていれば帯は出さない、出すときは「1–3 / 8」と端で押せない ‹ ›。
    function refreshCarouselNav(car) {
        const nav = carouselNavOf(car);
        if (!nav) return;
        const overflow = car.scrollWidth > car.clientWidth + 2;
        if (nav.hidden === overflow) nav.hidden = !overflow;
        if (!overflow) return;
        const v = visibleSlides(car);
        const want = v.first < 0 ? '' : (v.first === v.last ? (v.first + 1) + ' / ' + v.n : (v.first + 1) + '–' + (v.last + 1) + ' / ' + v.n);
        const count = nav.querySelector('.car-count');
        if (count && count.textContent !== want) count.textContent = want;
        const prev = nav.querySelector('.car-prev'), next = nav.querySelector('.car-next');
        if (prev) prev.disabled = car.scrollLeft <= 1;
        if (next) next.disabled = car.scrollLeft + car.clientWidth >= car.scrollWidth - 1;
    }

    // pageCarousel は帯を1画面ぶん（見えている幅から少し引いた分）横に巻く——枚の端で止まる（scroll-snap）。
    function pageCarousel(car, dir) {
        car.scrollBy({ left: dir * Math.max(80, car.clientWidth - 48), behavior: 'smooth' });
    }

    // decorateCarousels は帯を付け替える（decorateVocabBlocks の巡りから・モードに合わせて）。
    function decorateCarousels() {
        const editor = document.getElementById('w-editor-content');
        if (!editor) return;
        const isEdit = document.body.hasAttribute('edit-mode');
        editor.querySelectorAll('section[data-type="' + CAROUSEL_TYPE + '"]').forEach(car => {
            if (car.closest('.vocab-chrome')) return;
            const editBar = car.querySelector(':scope > .car-edit-bar');
            const nav = carouselNavOf(car);
            if (isEdit) {
                if (nav) nav.remove();
                if (!editBar) {
                    const bar = document.createElement('div');
                    bar.className = 'vocab-chrome car-edit-bar';
                    bar.contentEditable = 'false';
                    const label = document.createElement('span');
                    label.className = 'car-label';
                    label.textContent = '🎠 カルーセル';
                    bar.append(label,
                        carouselButton('car-add', '🖼 画像を足す', '画像を選んで、このカルーセルの末尾へ足します', () => addImagesToCarousel(car)),
                        carouselButton('car-take', '⬇ すぐ下の画像を入れる', 'このカルーセルのすぐ下に続く画像（画像だけの段落）を中へ移します', () => takeImagesBelow(car)));
                    car.insertBefore(bar, car.firstChild);
                }
                return;
            }
            if (editBar) editBar.remove();
            if (!nav) {
                const bar = document.createElement('div');
                bar.className = 'vocab-chrome car-nav';
                bar.hidden = true;
                const count = document.createElement('span');
                count.className = 'car-count';
                bar.append(
                    carouselButton('car-prev', '‹', '前の画像', () => pageCarousel(car, -1)),
                    count,
                    carouselButton('car-next', '›', '次の画像', () => pageCarousel(car, 1)));
                car.after(bar);
            }
            // 配線の印は WeakSet で持つ（本文の要素に属性を足すと保存される）。
            if (!carouselWired.has(car)) {
                carouselWired.add(car);
                car.addEventListener('scroll', () => refreshCarouselNav(car), { passive: true });
                car.querySelectorAll('img').forEach(img => { if (!img.complete) img.addEventListener('load', () => refreshCarouselNav(car), { once: true }); });
            }
            refreshCarouselNav(car);
        });
    }
    const carouselWired = new WeakSet(); // 横に巻いたときに送る帯を合わせる聞き手を付けたカルーセル
    window.addEventListener('resize', () => {
        if (document.body.hasAttribute('edit-mode')) return;
        document.querySelectorAll('#w-editor-content section[data-type="' + CAROUSEL_TYPE + '"]').forEach(refreshCarouselNav);
    });

    // ── 画像の拡大表示（2026-10-07） ───────────────────────────────────
    //
    // 利用者: 画像を押したときに、大きく開いてページの中の画像を前後に送れるように「する」（カルーセルと一緒に決めた）。
    // 閲覧モードで本文の画像を押すと画面いっぱいに開く。‹ ›・← →・横に払う で同じページの画像を前後に、Esc・✕・外側で閉じる。
    // 数えるのは本文の画像——リンクやボタンの中・鏡やファイル表示（クローム）の中は除く。カルーセルの中は隠れている枚も入る
    // （閉じたとき、最後に見ていた画像がカルーセルの中ならその枚を見せる）。
    let lightboxList = [];
    let lightboxAt = 0;
    let lightboxSwipe = null;

    function lightboxImages() {
        return Array.from(document.querySelectorAll('#w-editor-content img'))
            .filter(img => img.getAttribute('src') && !img.closest('a, button, .vocab-chrome, .file-view'));
    }

    function lightboxEl() {
        let box = document.getElementById('w-lightbox');
        if (box) return box;
        box = document.createElement('div');
        box.id = 'w-lightbox';
        box.className = 'is-hidden';
        box.setAttribute('role', 'dialog');
        box.setAttribute('aria-modal', 'true');
        box.setAttribute('aria-label', '画像の拡大表示');
        const btn = (cls, text, label, fn) => {
            const b = document.createElement('button');
            b.type = 'button';
            b.className = cls;
            b.textContent = text;
            b.title = label;
            b.setAttribute('aria-label', label);
            b.addEventListener('click', e => { e.stopPropagation(); fn(); });
            return b;
        };
        const fig = document.createElement('figure');
        fig.className = 'lb-figure';
        const img = document.createElement('img');
        img.className = 'lb-img';
        const cap = document.createElement('figcaption');
        cap.className = 'lb-caption';
        fig.append(img, cap);
        const count = document.createElement('div');
        count.className = 'lb-count';
        box.append(btn('lb-close', '✕', '閉じる（Esc）', closeLightbox),
            btn('lb-prev', '‹', '前の画像（←）', () => showLightbox(lightboxAt - 1)),
            fig,
            btn('lb-next', '›', '次の画像（→）', () => showLightbox(lightboxAt + 1)),
            count);
        // 外側（画像と道具の外）を押すと閉じる。
        box.addEventListener('click', e => { if (e.target === box || e.target === fig) closeLightbox(); });
        box.addEventListener('pointerdown', e => { lightboxSwipe = e.pointerType === 'mouse' ? null : { x: e.clientX, y: e.clientY }; });
        box.addEventListener('pointerup', e => {
            const s = lightboxSwipe;
            lightboxSwipe = null;
            if (!s) return;
            const dx = e.clientX - s.x, dy = e.clientY - s.y;
            if (Math.abs(dx) >= 40 && Math.abs(dx) >= Math.abs(dy)) showLightbox(lightboxAt + (dx < 0 ? 1 : -1));
        });
        document.body.appendChild(box);
        return box;
    }

    function showLightbox(i) {
        if (!lightboxList.length) return;
        lightboxAt = ((i % lightboxList.length) + lightboxList.length) % lightboxList.length;
        const src = lightboxList[lightboxAt];
        const box = lightboxEl();
        const img = box.querySelector('.lb-img');
        img.src = src.getAttribute('src');
        img.alt = src.getAttribute('alt') || '';
        box.querySelector('.lb-caption').textContent = src.getAttribute('alt') || '';
        box.querySelector('.lb-count').textContent = (lightboxAt + 1) + ' / ' + lightboxList.length;
        const many = lightboxList.length > 1;
        setHidden(box.querySelector('.lb-prev'), !many);
        setHidden(box.querySelector('.lb-next'), !many);
    }

    function openLightbox(img) {
        lightboxList = lightboxImages();
        const i = lightboxList.indexOf(img);
        if (i < 0) return;
        const box = lightboxEl();
        setHidden(box, false);
        document.body.classList.add('w-lightbox-open');
        showLightbox(i);
        box.querySelector('.lb-close').focus({ preventScroll: true });
    }

    function closeLightbox() {
        const box = document.getElementById('w-lightbox');
        if (!box || box.classList.contains('is-hidden')) return;
        setHidden(box, true);
        document.body.classList.remove('w-lightbox-open');
        // 最後に見ていた画像がカルーセルの中なら、その枚が帯の左端に来るまで巻いておく。
        const last = lightboxList[lightboxAt];
        const car = last && last.closest('section[data-type="' + CAROUSEL_TYPE + '"]');
        if (car) {
            const slide = carouselSlides(car).find(s => s.contains(last));
            if (slide) showCarouselSlide(car, carouselSlides(car).indexOf(slide));
        }
        lightboxList = [];
    }

    document.addEventListener('click', e => {
        if (document.body.hasAttribute('edit-mode')) return;
        const img = e.target instanceof Element && e.target.closest('#w-editor-content img');
        if (!img || img.closest('a, button, .vocab-chrome, .file-view')) return;
        e.preventDefault();
        openLightbox(img);
    });
    document.addEventListener('keydown', e => {
        const box = document.getElementById('w-lightbox');
        if (!box || box.classList.contains('is-hidden')) return;
        if (e.key === 'Escape') { e.preventDefault(); closeLightbox(); }
        else if (e.key === 'ArrowLeft') { e.preventDefault(); showLightbox(lightboxAt - 1); }
        else if (e.key === 'ArrowRight') { e.preventDefault(); showLightbox(lightboxAt + 1); }
    });

    // ── 表の名前・見出しの「SQL で引くとき気をつける所」を薄赤に（2026-09-28） ────────────
    //
    // 利用者:「警告文を出すより、問題個所の背景を薄赤にする方が分かりやすいかもしれませんね」——
    // それまでは保存のたびに文で告知していました（TableNameNotes・廃止）。
    // 表の写し（data/tables.db）に入るのは**キャプションのある表だけ**なので、その名前（caption）と
    // 見出し（1行目のセル）だけを見ます。見分けはサーバーの identNeedsQuote・isSystemColumn
    // （internal/cms/tables_db.go）と同じ——**予約語と予約した列はサーバーから**（sql_names・SQLite に
    // 試した結果）、文字の規則は短いのでここに同じものを書きます。
    // ⚠ 名前は表の写しと同じく **NFKC＋空白を詰めてから**見ます（全角の括弧も半角に畳まれて引用符が要る）。
    function sqlNameOf(text) {
        return String(text || '').normalize('NFKC').split(/\s+/).filter((s) => s).join(' ');
    }

    // sqlNameProblem は名前の問題を一文で返します（無ければ空）。見出しなら予約した列も見ます。
    function sqlNameProblem(text, isHeader) {
        if (!sqlNames) return '';
        const name = sqlNameOf(text);
        if (!name) return '';
        // 予約した列と比べるのは SQLite と同じく ASCII の英字の大小を畳んで（asciiFold）。
        const folded = name.replace(/[A-Z]/g, (c) => c.toLowerCase());
        if (isHeader && (sqlNames.system_columns || []).includes(folded)) {
            return '「' + name + '」は DB の予約した列と同じ名前なので、DB では「' + name +
                (sqlNames.system_suffix || '') + '」になります';
        }
        let needs = false;
        for (let i = 0; i < name.length && !needs; i++) {
            const c = name.charCodeAt(i);
            if (c >= 0x80 || c === 0x5f || (c >= 0x61 && c <= 0x7a) || (c >= 0x41 && c <= 0x5a)) continue;
            if ((c >= 0x30 && c <= 0x39) || c === 0x24) { needs = i === 0; continue; }
            needs = true; // 空白・記号（括弧・点・ハイフン…）
        }
        if (!needs) needs = (sqlNames.quote_words || []).includes(name.toUpperCase());
        return needs ? 'SQL で引くとき "' + name.replace(/"/g, '""') + '" と引用符で囲む必要があります' : '';
    }

    // markSqlNames は編集モードで、問題のある表の名前・見出しに薄赤（＋⚠・色だけに頼らない）と説明を付けます。
    // **必要なときだけ DOM を変えます**（decorateVocabBlocks と同じ理由）。印は class と title なので
    // 保存されません（シリアライザは許可した属性しか書かない）。
    function markSqlNames() {
        const editor = document.getElementById('w-editor-content');
        if (!editor) return;
        const want = new Map();
        if (document.body.hasAttribute('edit-mode') && sqlNames) {
            editor.querySelectorAll('table').forEach((tbl) => {
                if (tbl.closest('.vocab-chrome')) return;
                const cap = tbl.querySelector(':scope > caption');
                if (!cap || !sqlNameOf(cap.textContent)) return; // キャプションの無い表は DB に入らない
                const m = sqlNameProblem(cap.textContent, false);
                if (m) want.set(cap, '表の名前は ' + m);
                const first = tbl.querySelector(':scope > thead > tr, :scope > tbody > tr, :scope > tr');
                if (!first) return;
                Array.from(first.children).forEach((cell) => {
                    if (cell.classList.contains('vocab-chrome')) return;
                    const mm = sqlNameProblem(cell.textContent, true);
                    if (mm) want.set(cell, '見出し' + (mm.charAt(0) === '「' ? '' : 'は ') + mm);
                });
            });
        }
        editor.querySelectorAll('.sql-name-warn').forEach((el) => {
            if (want.has(el)) return;
            el.classList.remove('sql-name-warn');
            if (el.title === el._sqlTitle) el.removeAttribute('title');
            el._sqlTitle = undefined;
        });
        want.forEach((msg, el) => {
            if (!el.classList.contains('sql-name-warn')) el.classList.add('sql-name-warn');
            // 他の飾りが付けた説明は上書きしない（自分が付けたものだけ替える）。
            if (!el.title || el.title === el._sqlTitle) {
                if (el.title !== msg) el.title = msg;
                el._sqlTitle = msg;
            }
        });
    }

    // ── 画像・ファイルを足す（2026-09-30） ─────────────────────────────────
    //
    // 利用者:「+ファイルというボタンが編集で消せなくなっています」「ファイルの追加はスラッシュメニューで統一的に
    // 扱ってはどうでしょうか？」「そのブロックの中に画像ファイル等を入れるのに『+ファイル』のボタンが必要というなら、
    // BIUリンクの方に画像マークでもあれば良いのでは？」「イメージと添付ファイルは扱いが違って当然ですね」
    // 「画像を追加するボタンも必要だと思います」「ファイルのドラッグによる追加もあると便利」。
    //
    // それまでは道が4つあり、できる形が3通りでした（スラッシュの「画像」＝絵・ドラッグ＝絵か📎リンク・
    // 節と折りたたみの末尾の「＋ ファイル」＝ファイル表示・スラッシュの「ファイル表示」＝空の印）。いまは:
    //
    //   入口 … 書式の帯の 📎／スラッシュの「📎 画像・ファイル」／ドラッグ／貼り付け（Ctrl+V）——**種類は選ばせない**
    //          （2026-10-01 利用者:「画像とPDFの違いを自動判定して、画像をはる、PDFをはるというわけかたをなくせませんか」
    //          ——09-30 は 🖼〔画像だけ〕と 📎・「🖼 画像」と「📎 ファイル」の2つずつだった）
    //   形   … **画像は本文の中の絵**（`<p><img>`）、**それ以外はファイル表示の印**（開く枠・開く案内）
    //   場所 … キャレット（ドラッグは落とした所）のすぐ後ろ。節・折りたたみの中なら中のまま（名前の上なら先頭）
    //
    // ⚠ 節と折りたたみの末尾に出していた「＋ ファイル」（2026-09-28〜29）は消しました——編集モードで節ごとに
    // 並ぶ飾りで、本文に見えるのに消せませんでした。既にある添付を指すのは、スラッシュの「ファイル表示」の札。

    // pastedFileName は貼り付けた画像に名前を付けます——切り取りの画像は「image.png」で届くので、貼った日時の名前に
    // （`貼り付け 2026-10-01 13.15.42.png`・何枚かなら「-2」…）。名前のあるファイル（エクスプローラーからのコピー）はそのまま。
    function pastedFileName(f, i, n) {
        if (f.name && !/^image\.[a-z0-9]+$/i.test(f.name)) return f;
        const d = new Date();
        const p2 = x => String(x).padStart(2, '0');
        const ext = (f.type.split('/')[1] || 'png').replace('jpeg', 'jpg').replace(/[^a-z0-9]/gi, '');
        const name = '貼り付け ' + d.getFullYear() + '-' + p2(d.getMonth() + 1) + '-' + p2(d.getDate()) + ' ' +
            p2(d.getHours()) + '.' + p2(d.getMinutes()) + '.' + p2(d.getSeconds()) + (n > 1 ? '-' + (i + 1) : '') + '.' + ext;
        return new File([f], name, { type: f.type });
    }

    // pickAnyFiles はファイル選択を開きます（種類は問わない・複数可）。
    function pickAnyFiles(onPick) {
        const input = document.createElement('input');
        input.type = 'file';
        input.multiple = true;
        input.addEventListener('change', () => onPick(Array.from(input.files || [])));
        input.click();
    }

    // fileInsertPointAt は、node（キャレット・落とした所）から見て、足すものを置く「直前の要素」を返します。
    //   - 入れ物の名前（素の節の最初の見出し・折りたたみの題）→ その名前（中の先頭へ入る）
    //   - 節・折りたたみの中の要素 → その要素（入れ物の中のまま）
    //   - それ以外 → 上の段のブロック（その後ろへ新しいブロック）
    function fileInsertPointAt(node) {
        const head = boxHeadAt(node);
        if (head) return head;
        const inner = innerTargetAt(node);
        if (inner) return inner;
        const el = node && (node.nodeType === 1 ? node : node.parentElement);
        return el && el.closest ? el.closest('#w-editor-content .editor-block') : null;
    }

    // insertAfterRef は el を ref の後ろへ置きます（上の段のブロックの後ろなら包む・入れ物の中なら包まない）。
    function insertAfterRef(el, ref) {
        const top = !ref || ref.classList.contains('editor-block');
        const node = top ? wrapInBlock(el) : el;
        if (ref && ref.parentNode) ref.parentNode.insertBefore(node, ref.nextSibling);
        else document.getElementById('w-editor-content').appendChild(node);
        return node;
    }

    // insertFileViewsAfter は画像以外のファイルを順にこのページへ上げ、ref の後ろへファイル表示の印を置きます。
    // 口は種類で振り分けます（PDF は %PDF- 検査つきの専用口・それ以外は汎用口）。最後に置いた要素を返します。
    // ⚠ 通信箱のページでも添付になるだけです（2026-09-30 まで .eml は記録のページとして生まれていた）。
    async function insertFileViewsAfter(files, ref) {
        if (!currentPageId) {
            notify('先にページを保存してください。', { type: 'warn', duration: 5000 });
            return ref;
        }
        let last = ref;
        for (const f of files) {
            const isPDF = f.type === 'application/pdf' || /[.]pdf$/i.test(f.name);
            const fd = new FormData();
            fd.append('page_id', currentPageId);
            fd.append(isPDF ? 'pdf_file' : 'file', f);
            try {
                const res = await lockedFetch(isPDF ? '/api/upload-pdf' : '/api/upload-file', { method: 'POST', body: fd });
                const d = await readResult(res);
                if (!res.ok || !d || !d.success || !d.id) {
                    notify(f.name + ' を上げられませんでした: ' + failMessage(res, d),
                        { type: 'alert', duration: 0, id: 'file-upload' });
                    continue;
                }
                const sec = document.createElement('section');
                sec.setAttribute('data-type', FILE_VIEW_TYPE);
                sec.setAttribute(FILE_REF_ATTR, currentPageId + '-' + d.id);
                sec.textContent = f.name; // 名札（保存のたびにサーバーが書き直す・表示では消える）
                last = insertAfterRef(sec, last);
            } catch (err) {
                notify(f.name + ' を上げられませんでした: ' + err.message, { type: 'warn', duration: 8000 });
            }
        }
        decorateFileViews();
        updateHtmlPreview();
        triggerAutoSave();
        return last;
    }

    // insertFilesAfter は選んだファイルを種類で振り分けて ref の後ろへ置きます（画像は絵・それ以外はファイル表示）。
    async function insertFilesAfter(files, ref) {
        const images = files.filter(f => (f.type || '').indexOf('image/') === 0);
        const others = files.filter(f => (f.type || '').indexOf('image/') !== 0);
        let last = ref;
        if (images.length) last = await insertImagesAfter(images, last);
        if (others.length) last = await insertFileViewsAfter(others, last);
        return last;
    }

    // addFilesAtCaret は書式の帯の 📎 から呼ばれます（キャレットの位置を先に決めてから選ばせる——選んでいる間に
    // キャレットが動いても、押したときの場所へ入る）。種類は選ばせない（画像は絵・それ以外はファイル表示）。
    function addFilesAtCaret() {
        const sel = window.getSelection();
        const ref = sel && sel.rangeCount ? fileInsertPointAt(sel.anchorNode) : null;
        if (!ref || !ref.closest('#w-editor-content')) {
            notify('入れる場所へキャレットを置いてから押してください。', { type: 'warn', duration: 5000 });
            return;
        }
        pickAnyFiles(files => { if (files.length) insertFilesAfter(files, ref); });
    }

    // foldSummaryOf は、編集中の要素が折りたたみの題（summary）の中なら、その summary を返します。
    function foldSummaryOf(node) {
        const el = node && (node.nodeType === Node.ELEMENT_NODE ? node : node.parentElement);
        const s = el && el.closest('summary');
        return s && s.parentElement && s.parentElement.tagName === 'DETAILS' &&
            s.closest('#w-editor-content') ? s : null;
    }

    // ── 定義リスト（dl data-type）の項目操作 ─────────────────────────────
    // 「項目」＝ dt と、次の dt までの dd の組（多値＝複数 dd。語彙モデル §5.3）。
    let currentDlNode = null; // キャレットのある dt / dd

    function hideDlToolbar() {
        const bar = document.getElementById('w-dl-toolbar');
        if (bar) bar.classList.remove('active');
        currentDlNode = null;
    }

    // updateDlToolbar はキャレットが本文中の dl の dt/dd にあるときだけ出す。
    // かつては dl[data-type] に限っていたが、見出し形（D-2）ではヘッダも見積も
    // **素の dl** なので、対の追加・削除はすべての dl で使えるようにした。
    function updateDlToolbar() {
        const bar = document.getElementById('w-dl-toolbar');
        if (!bar) return;
        if (!document.body.hasAttribute('edit-mode')) { hideDlToolbar(); return; }

        const sel = window.getSelection();
        const node = sel && sel.anchorNode;
        const el = node ? (node.nodeType === Node.TEXT_NODE ? node.parentElement : node) : null;
        const item = el && el.closest ? el.closest('#w-editor-content dl > dt, #w-editor-content dl > dd') : null;
        if (!item || isServerOwned(item)) { hideDlToolbar(); return; }

        currentDlNode = item;
        bar.classList.add('active');
        if (fixedBars()) return; // 本文の上の固定の帯の中（2026-10-19・initFixedBar）——置き直さない
        const rect = item.getBoundingClientRect();
        let top, left;
        if (getComputedStyle(item.parentElement).display === 'flex') {
            // 対まで横に流れる dl（可変タグのチップ）では、右横に出すと
            // **隣の対の上に被さって**その対を編集できなくなる。項目の下へ出す。
            // 素の dl はグリッド（対ごとに1行・値の幅は中身なり）なので右横で良い。
            top = rect.bottom + window.scrollY + 4;
            left = rect.left + window.scrollX;
        } else {
            top = rect.top + window.scrollY + (rect.height - bar.offsetHeight) / 2;
            left = rect.right + window.scrollX + 8;
            if (rect.right + 8 + bar.offsetWidth > document.documentElement.clientWidth) {
                top = rect.top + window.scrollY - bar.offsetHeight - 4;
                left = rect.left + window.scrollX;
            }
        }
        placeToolbar(bar, 'dl', top, left); // つまみで動かしていればその位置（2026-09-27）
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
    // th の data-type 明示 > 設定の語彙（表の例外 → 列の名前・`columnWord`・2026-09-26 から） > text。
    // code は「畳んで比較する文字」（図面番号・発注書番号）。見た目も入力補助も
    // text と同じで、違うのはサーバー側の索引の畳み方だけ——だから検証は要らない。
    //
    // **一覧は手書きしません**（語彙モデル §7 の原則1・2026-09-14）。`/api/tag-schema`
    // の `column_types` を受け取ります——**手で持っていたときは3つ古いままでした**
    // （`datetime`・`ref`・`email` を足した日から。`<th data-type="email">` と書いても
    // エディタが黙って無視し、索引だけが `email` として扱う、というずれが出ていた）。
    //
    // 取得前に使われても壊れないよう、空配列ではなく**サーバーと同じ並び**を初期値に
    // 置きます——空だと「明示した型が全部無視される」ほうへ倒れるためです。
    let VALID_COL_TYPES = ['text', 'code', 'number', 'date', 'datetime',
        'enum', 'image', 'ref', 'email'];

    // 列型の日本語の説明。**一覧の正本ではありません**——ここに無い型も
    // 選べます（名前だけが出ます）。言葉は画面の持ち物、型の一覧はサーバーの持ち物。
    const COL_TYPE_LABELS = {
        text: 'text（文字）',
        code: 'code（番号・符牒。揺れを畳んで検索）',
        number: 'number（数値・通貨）',
        date: 'date（日付）',
        datetime: 'datetime（日時）',
        enum: 'enum（選択肢）',
        image: 'image（画像）',
        ref: 'ref（参照。ページID-ブロックID）',
        email: 'email（メールの相手。名前 <アドレス>）',
    };

    // fillColTypeOptions は列設定ポップオーバの選択肢を作り直します。
    // **HTMLに書き並べません**——書くと、型を足した日にそこだけ古くなります。
    function fillColTypeOptions() {
        const sel = document.getElementById('w-cp-type');
        if (!sel) return;
        const keep = sel.value;
        sel.textContent = '';
        const none = document.createElement('option');
        none.value = '';
        none.textContent = '（推論に任せる）';
        sel.appendChild(none);
        VALID_COL_TYPES.forEach(t => {
            const o = document.createElement('option');
            o.value = t;
            o.textContent = COL_TYPE_LABELS[t] || t;
            sel.appendChild(o);
        });
        sel.value = keep; // 開いたまま取得が終わっても、選んでいた型を保つ
    }

    // ── 列の型と選択肢は設定の語彙から（2026-09-26・DBの日本語化 4-2）──────────
    //
    // ⚠ **それまでは登録（`vocabDefs` の列の宣言）から引いていました**——表の写し
    // （`data/tables.db`）はサーバーの `ColumnWord` で**語彙**を読むので、エディタと DB が
    // **別の正本**を見ていました。実際に5か所ずれていて、エディタは受注明細表の `最短納期` を
    // 日付でないとして薄赤にしていました（登録は `date` のまま・語彙は 09-23 から `text`）。
    //
    // wordKey は名前を照らす形に畳みます（NFKC・前後と重なりの空白・ASCII の大小）。
    // サーバーの `asciiFold(TableName(s))` と同じ向きです。⚠ **違うのは1つだけ**——設定の
    // `char_folding`（`Φ`→`φ` など）はここでは掛けません。見出しに直径記号を書く列は
    // 無いので実害はありませんが、書いた日はサーバーとここで引き当てが割れます。
    function wordKey(s) {
        return String(s || '').normalize('NFKC').trim().split(/\s+/).join(' ')
            .replace(/[A-Z]/g, c => c.toLowerCase());
    }

    // pickWord は辞書から、畳んだ名前が同じ語を探します（無ければ null）。
    function pickWord(words, name) {
        const k = wordKey(name);
        if (!k || !words) return null;
        for (const w of Object.keys(words)) {
            if (wordKey(w) === k) return words[w];
        }
        return null;
    }

    // columnWord は表 table の列 column の型と選択肢を返します。
    // 引く順は **表ごとの例外 → 列の名前の既定 → 文字列**（サーバーの `ColumnWord` と同じ）。
    // `from` は出どころ（'table'・'word'・''）——列設定の札が「何で決まったか」を出すため。
    function columnWord(table, column) {
        if (table) {
            const tk = wordKey(table);
            for (const t of Object.keys(tableWords)) {
                if (wordKey(t) !== tk) continue;
                const w = pickWord(tableWords[t], column);
                if (w) return { type: w.type, values: w.values || [], from: 'table' };
            }
        }
        const w = pickWord(vocabWords, column);
        if (w) return { type: w.type, values: w.values || [], from: 'word' };
        return { type: 'text', values: [], from: '' };
    }

    // tableNameOf は表の名前です——**キャプションが正**、無ければ文脈から解決した形式の
    // 表示名（節の見出しの下の素の表など。5段目でキャプションが付くまでの橋渡し）。
    function tableNameOf(table) {
        const cap = table.querySelector(':scope > caption');
        if (cap && cap.textContent.trim()) return cap.textContent.trim();
        const def = tableDefOf(table);
        return (def && def.display_name) || '';
    }

    // resolveCellColumn はセルの属する列の {type, enum} を解決する（データ行の td 用）。
    //
    // 型は **`th[data-type]` の明示 → 語彙（`columnWord`）→ 文字列**。選択肢も語彙から。
    // ⚠ **どの表を検証するか**（下の `def` の関門）はまだ登録を読みます（4-4——引き金をキャプションで）。
    // 2026-10-19 に `suggest`（候補の出どころ）を外しました——候補はオートコンプリート（fillCellChoiceMenu）だけです。
    function resolveCellColumn(cell) {
        const table = cell.closest('table');
        if (!table || !table.rows.length) return null;
        const def = tableDefOf(table);
        if (!def && !table.hasAttribute('data-type')) return null; // 機械に読まれない素の表
        const th = cellAt(table.rows[0], colIndexOf(cell));
        if (!th) return null;
        const key = th.textContent.trim();
        const explicit = th.getAttribute('data-type');
        const word = columnWord(tableNameOf(table), key);
        const type = (explicit && VALID_COL_TYPES.indexOf(explicit) !== -1) ? explicit : word.type;
        // `label` は見出しの表示文字（＝項目の鍵）。折り返しの判定に使う。
        return { label: key, type, enum: word.values };
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

    // WRAP_COLUMNS は**折り返してよい列**の見出しです（ここに無い列は1行に保つ）。
    //
    // 中身が**自由文**の列だけを入れること——番号・名前・数・日付・状態のように
    // 「1つの値」である列を入れると、途中で折れて読みにくくなり、行の高さも揃いません。
    // ⚠ **見出しの表示文字で持ちます**（列の順番ではありません）。列を足しても
    // 並べ替えても、この判定はずれません。
    const WRAP_COLUMNS = ['備考'];

    // validateCell はセルへ2つの印を付け直す。どちらも class なのでシリアライザが
    // 書き出さず、**実行時だけ**の印（保存されない）。
    //
    //   .cell-invalid … number / date 列で値が解釈できない（薄い赤）
    //   .cell-known   … number / date 列で値が**読めた**（薄い青）
    //
    // 後者が D-3 の「表の中で認識できる形式は背景色で示します」（2026-08-30 ユーザー）。
    // 赤が「読めなかった」しか言わないので、書き手には**読めたことを確かめる術が
    // 無かった**——空欄と「読めているが印が無い」が同じ見え方だった。
    //
    // **文字列の列には付けない。** text は「認識できた形式」ではなく、型が決まらな
    // かったときの落ち着き先で、付けると表が丸ごと青くなって何も伝えなくなる。
    // 薄い青は全体で「機械が読む」の色（見出し語の .vocab-word と同じ）。
    function validateCell(cell) {
        // ⚠ **サーバーが足したセル（クローム）には触らない**（2026-09-21）。
        // 本文の列ではないので、型の印（薄い赤／薄い青）も折り返しの方針も当てはまり
        // ません。⚠ 実際に踏みました——材料の参考単価の列は見出しが `備考` でないため
        // `cell-atomic`（1行に保つ）が付き、**断り文「⚠ 買った記録がありません」の
        // 1行幅（実測200px）で列が決まりました**。値より断り文のほうが列を広げる、
        // という逆転です。検算の `<tfoot>` の行も同じ経路を通っていました。
        if (isServerOwned(cell)) return;
        const col = resolveCellColumn(cell);
        const text = cell.textContent.trim();
        // 式のセル（`=個数*単価`）は値ではないので型の印を付けない（式の印は refreshFormulaCells）。
        const typed = !!col && (col.type === 'number' || col.type === 'date') && !isFormulaText(text);
        let bad = false;
        if (typed && text !== '') {
            bad = col.type === 'number' ? !isValidNumberText(text) : !isValidDateText(text);
        }
        cell.classList.toggle('cell-invalid', bad);
        cell.classList.toggle('cell-known', typed && text !== '' && !bad);
        // 折り返し制御（2026-09-21 ユーザー:「備考欄以外は改行しないようにして、
        // テーブルが横に大きくなりますから、テーブルだけスクロールに出来ますか？」）。
        //
        // **折り返してよいのは自由文の列だけ**です。番号・名前・数・日付・状態は
        // 途中で折ると読みにくく、**行の高さも揃いません**。表が横へ伸びるぶんは
        // **表自身が横スクロール**して受けます（CSS側。ページ全体は揺らしません）。
        //
        // ⚠ **2026-09-01 からは「長さ20字」で決めていました**（当時のユーザー:
        // 「単価や品名などは折り返さないほうが見やすい」「テキストでも、図番や品名
        // などは折り返さないほうが良いです」）。ワンノートの実測（品名 p95=19字・
        // 図面番号 p95=18字）から採ったしきい値でしたが、**しきい値は「その値が
        // 自由文かどうか」を当てにいく代理**でしかありません——実データで品名が
        // 20字を超え、**同じ列の中で折れる行と折れない行が混ざりました**。
        // いまは**見出しの表示文字**で決めます（項目の鍵は見える文字。⚠ 列の順番には
        // 依存しません——列を足しても増やしても、この判定はずれません）。
        const label = (col && col.label) || '';
        const wrap = WRAP_COLUMNS.indexOf(label) !== -1;
        cell.classList.toggle('cell-wrap', wrap);
        cell.classList.toggle('cell-atomic', !wrap);
    }

    // validateTypedTables は本文中の**機械に読まれる表**のデータセルを検証し直す。
    // 対象は data-type 付きと、形式が文脈から解決できる素の表（見出し形・D-2）。
    // 編集モード入り・列型の変更・サニタイズ反映のあとに呼ぶ。
    function validateTypedTables() {
        document.querySelectorAll('#w-editor-content table').forEach(table => {
            if (isServerOwned(table)) return;
            if (!table.hasAttribute('data-type') && !tableDefOf(table)) return;
            Array.from(table.rows).slice(1).forEach(row => {
                Array.from(row.children).forEach(c => {
                    if (c.tagName === 'TD') validateCell(c);
                });
            });
        });
    }

    // ── enum 列の入力補助（選択肢メニュー）────────────────────────────────
    // 選択肢は設定の語彙から取る（2026-09-26 からは登録の宣言ではなく `columnWord`・上の「列の型と選択肢は設定の語彙から」。
    // 原則3: 語彙があれば向上・無くても編集可能）。2026-10-19 からはオートコンプリートの候補も同じメニューに出す。
    let enumMenuCell = null;

    function hideEnumMenu() {
        const menu = document.getElementById('w-enum-menu');
        if (menu) menu.classList.remove('active');
        enumMenuCell = null;
    }

    // updateEnumMenu はキャレットが入力補助のある列のデータセルにあるとき、
    // セルの下に補助メニューを出す。対象は2種類:
    //   - enum 列 … 選択肢のボタンを並べる
    //   - image 列 … 画像を選ぶボタン1つ（ファイル選択。モバイルではカメラも選べる）
    // 器（#w-enum-menu）は共通で、中身だけ列型で作り分ける。
    //
    // **タグの値（可変タグの dd）にも enum の選択肢を出す**（2026-10-09 利用者:「タグにはコンボボックスで候補を出す設定が
    // 無かったでしょうか？」→「enum のタグ全部に出す」——【要求】タグと表 §11「タグの値にも、型に応じた入力補助を」）。
    // 選択肢は表のセルと同じ設定の語彙（`vocabWords` の `values`）。タグには enum だけ（image・候補の出どころは表のセルだけ）。
    // ⚠ 2026-10-19 からは、タグの値にはオートコンプリートの候補も出す（下の fillTagValueMenu——enum なら選択肢を数の順に）。
    //    候補の出どころ（suggest）は同じ日に外した。画像を選ぶボタンは、いまも表のセルだけ。
    function updateEnumMenu() {
        const menu = document.getElementById('w-enum-menu');
        if (!menu) return;
        if (!document.body.hasAttribute('edit-mode')) { hideEnumMenu(); return; }

        const sel = window.getSelection();
        const node = sel && sel.anchorNode;
        const el = node ? (node.nodeType === Node.TEXT_NODE ? node.parentElement : node) : null;
        // 対象は data-type 付きに限らない——見出し形（D-2）の素の表も、文脈で列宣言が
        // 解決できれば補助を出す（resolveCellColumn が読まれない表では null を返す）。
        const td = el && el.closest ? el.closest('#w-editor-content table td') : null;
        const tagDD = !td && el && el.closest ? el.closest('#w-editor-content dl[data-type="tags"] > dd') : null;
        const cell = td || tagDD;
        if (!cell || isServerOwned(cell)) { hideEnumMenu(); return; }
        const col = td ? resolveCellColumn(td) : tagValueColumn(tagDD);
        const isEnum = col && col.type === 'enum' && col.enum.length > 0;
        const isImage = !!td && col && col.type === 'image';
        // ⚠ **候補は縛りではありません**（2026-09-21）。採らずに手で打てます
        //    ——揃うのは採ったときだけで、手打ちは止められません。それでも意味が
        //    あるのは**打つより選ぶほうが速い**からです。速ければ揃います。
        // オートコンプリート（2026-10-19・internal/cms/autocomplete.go）——何度も入力された定型的な値を、サーバーが暇なときに
        // DB から拾って置いておく。どの欄に出すか（定型の欄か・数えない型か）はサーバーが決める（画面は聞くだけ）。
        //   - タグの値: その名前のタグの候補（enum なら語彙の選択肢を、その数の順に並べ替える）。
        //   - 表のセル: キャプション＋列の見出しの候補。選択肢（enum）・画像の列は、いままでどおり（そちらが先）。
        // 2026-10-19 利用者:「いま、表の推奨業者に候補が出る仕組みがあると思いますが、これは冗長です。削除してオートコンプリートの
        // コンボボックスで統一しましょう。」——それまでの候補の出どころ（suggest・/api/suggest）は外した。
        const tagLabel = tagDD ? tagLabelOf(tagDD) : '';
        const tagSuggest = !!tagLabel;
        const cellKey = (td && !isEnum && !isImage) ? cellChoiceKey(td) : null;
        if (!isEnum && !isImage && !tagSuggest && !cellKey) { hideEnumMenu(); return; }
        if (cell === enumMenuCell) return; // 同じセル内のキャレット移動では作り直さない

        enumMenuCell = cell;
        menu.textContent = ''; // 前のセルの中身を消す
        if (isImage) {
            const b = document.createElement('button');
            b.type = 'button';
            b.textContent = '🖼 画像を選ぶ';
            b.addEventListener('click', () => {
                hideEnumMenu();
                pickImageFiles(files => { if (files.length) insertImageIntoCell(cell, files[0]); });
            });
            menu.appendChild(b);
        }
        if (cellKey) { fillCellChoiceMenu(menu, td, cellKey); return; } // 候補は届いてから開く
        if (tagSuggest) fillTagValueMenu(menu, cell, tagLabel, isEnum ? col.enum : []);
        else if (isEnum) col.enum.forEach(v => {
            const b = document.createElement('button');
            b.type = 'button';
            b.dataset.choice = '1'; // 打った字で絞る対象（filterEnumMenu）
            b.textContent = v;
            b.addEventListener('click', () => {
                cell.textContent = v;      // childList の変化で MutationObserver が未保存の印・押したあとの拾い（setupHistoryHooks の click）が保存する
                if (td) validateCell(td);
                else markTagVocabulary();  // 選択肢の中にあるかの印（tag-known）を付け直す
                placeCaretAtEnd(cell);
                hideEnumMenu();
                // 選んだあとは、同じセルに居るあいだ開き直さない（2026-10-09）——キャレットを置き直すと selectionchange が
                // この後に届き、閉じたばかりのメニューが同じセルにまた開いていた（タグでは下の行の値を覆った）。
                enumMenuCell = cell;
            });
            menu.appendChild(b);
        });
        if (tagSuggest) return; // タグの候補は届いてから開く（fillTagValueMenu——空の枠を先に出さない）
        menu.classList.add('active');
        const rect = cell.getBoundingClientRect();
        placeFloating(menu, rect, 2);
    }

    // tagLabelOf はタグの値（dd）のすぐ前の dt の名前です（無ければ空）。
    function tagLabelOf(dd) {
        const dt = dd.previousElementSibling;
        return dt && dt.tagName === 'DT' ? dt.textContent.trim() : '';
    }

    // ── オートコンプリートの候補の一覧（2026-10-19・internal/cms/autocomplete.go）──────────────────────
    // サーバーが暇なときに数えて data/autocomplete.json に置いた一覧を、ページを開いたあと1回だけ読む（欄ごとに聞かない）。
    //   2026-10-19 利用者:「オートコンプリートの候補一覧はJSON形式で保存しても良いかもしれませんね。それならエディタから読み込むのも容易です」
    // 答えは読めるページだけを数えたもの。表はキャプション→列の見出し、タグは名前で引く（名前は畳んで比べる——autoFold）。
    let autocompleteList = null;
    function loadAutocomplete() {
        if (!autocompleteList) {
            autocompleteList = fetch('/api/autocomplete').then(r => r.json()).then(d => {
                const cells = new Map(), tags = new Map();
                Object.entries((d && d.cells) || {}).forEach(([t, cols]) => {
                    const m = new Map();
                    Object.entries(cols || {}).forEach(([c, vs]) => m.set(autoFold(c), vs || []));
                    cells.set(autoFold(t), m);
                });
                Object.entries((d && d.tags) || {}).forEach(([n, vs]) => tags.set(n.trim(), vs || []));
                return { cells, tags };
            }).catch(() => { autocompleteList = null; return null; }); // 通信の失敗は覚えない（次にまた聞く）
        }
        return autocompleteList;
    }

    // autoFold は欄の名前を比べる形にします（サーバーの TableName＋ASCII の大小と同じ——全角半角を畳み、続いた空白は1つ）。
    function autoFold(name) {
        return (name || '').normalize('NFKC').trim().replace(/\s+/g, ' ').replace(/[A-Z]/g, c => c.toLowerCase());
    }

    // fillTagValueMenu はタグの値の候補を、DB にある値の多い順にボタンで並べます（2026-10-19）。
    //   ⚠ 同じ日の夜から、候補は「何度も入力された定型的な値」だけ（サーバーが暇なときに数える・internal/cms/autocomplete.go）。
    //   2026-10-19 利用者:「この値の数を数えて多い方から順に、ページ種類のタグを編集するときに値のコンボボックスとして出します。」
    //   - enum のタグ（enumValues がある）は、語彙の選択肢を DB の数の順に並べる（まだ無い選択肢は、語彙の順で後ろに）。
    //   - それ以外のタグは、DB にある定型の値（読めるページだけ・最大は設定の max_values）。0件なら何も出さない。
    //   - ⚠ 候補は縛りではない（手で打てる）。⚠ 遅れて届くので、セルが変わっていたら捨てる。
    async function fillTagValueMenu(menu, cell, label, enumValues) {
        menu.classList.remove('active'); // 前のセルの枠を、届くまで隠す（中身はもう空）
        const all = await loadAutocomplete();
        if (enumMenuCell !== cell) return; // 別のセルへ移っていたら捨てる
        const got = all ? (all.tags.get(label.trim()) || []) : [];
        const counts = new Map((got || []).map(x => [x.value, x.count]));
        let list;
        if (enumValues.length) {
            list = enumValues.map((v, i) => ({ v, n: counts.get(v) || 0, i }))
                .sort((a, b) => (b.n - a.n) || (a.i - b.i)).map(x => ({ v: x.v, n: x.n }));
        } else {
            list = (got || []).map(x => ({ v: x.value, n: x.count }));
        }
        if (!list.length) { hideEnumMenu(); enumMenuCell = cell; return; }
        list.forEach(({ v, n }) => {
            const b = document.createElement('button');
            b.type = 'button';
            b.dataset.choice = '1'; // 打った字で絞る対象（filterEnumMenu）
            b.textContent = v;
            b.title = n ? n + ' ページで使っています' : 'まだ使っていません';
            b.addEventListener('click', () => {
                cell.textContent = v;      // childList の変化で MutationObserver が未保存の印・押したあとの拾い（setupHistoryHooks の click）が保存する
                markTagVocabulary();       // 選択肢の中にあるかの印（tag-known）を付け直す
                placeCaretAtEnd(cell);
                hideEnumMenu();
                enumMenuCell = cell;       // 同じセルに居るあいだ開き直さない（enum のボタンと同じ）
            });
            menu.appendChild(b);
        });
        menu.classList.add('active'); // 開くときは全部を見せる（絞るのは打ったとき——filterEnumMenu）
        placeFloating(menu, cell.getBoundingClientRect(), 2);
    }

    // cellChoiceKey は表のデータセルの {table, column}（キャプションと列の見出し）です。キャプションの無い表・見出しの行は null。
    function cellChoiceKey(td) {
        const table = td.closest('table');
        if (!table || !table.rows.length) return null;
        const row = td.closest('tr');
        if (!row || row === table.rows[0] || row.closest('thead')) return null;
        const cap = table.querySelector(':scope > caption');
        const tableName = cap ? cap.textContent.trim() : '';
        const th = cellAt(table.rows[0], colIndexOf(td));
        const column = th ? th.textContent.trim() : '';
        if (!tableName || !column) return null;
        return { table: tableName, column };
    }

    // fillCellChoiceMenu は表のセルの候補を、使っている行の多い順にボタンで並べます（2026-10-19）。
    //   2026-10-19 利用者:「表のセルの候補は、サーバーが暇なときに DB を数えて置いておく形で作ってください。値が数個に決まっている列だけに出し、何個までかは設定に置いてください。」
    //   2026-10-19 利用者:「オートコンプリートのコンボボックスは、一回しか入力されたことが無い値は出さないようにしましょう。何度も入力される定型的な値をDBから拾って決定する仕組みを作りましょう。」
    //   - 候補はサーバーが暇なときに数えた定型の値（何度も入力された値・読めるページだけ）。定型の欄でない列・まだ数えていない列は何も出さない。
    //   - ⚠ 候補は縛りではない（手で打てる）。⚠ 遅れて届くので、セルが変わっていたら捨てる（fillTagValueMenu と同じ）。
    async function fillCellChoiceMenu(menu, td, key) {
        menu.classList.remove('active');
        const all = await loadAutocomplete();
        if (enumMenuCell !== td) return; // 別のセルへ移っていたら捨てる
        const cols = all && all.cells.get(autoFold(key.table));
        const got = (cols && cols.get(autoFold(key.column))) || [];
        if (!got.length) { hideEnumMenu(); enumMenuCell = td; return; }
        got.forEach(({ value, count }) => {
            const b = document.createElement('button');
            b.type = 'button';
            b.dataset.choice = '1';
            b.textContent = value;
            b.title = count + ' 行で使っています';
            b.addEventListener('click', () => {
                td.textContent = value;    // childList の変化で MutationObserver が未保存の印・押したあとの拾い（setupHistoryHooks の click）が保存する
                validateCell(td);
                placeCaretAtEnd(td);
                hideEnumMenu();
                enumMenuCell = td;         // 同じセルに居るあいだ開き直さない（enum のボタンと同じ）
            });
            menu.appendChild(b);
        });
        menu.classList.add('active'); // 開くときは全部を見せる（絞るのは打ったとき）
        placeFloating(menu, td.getBoundingClientRect(), 2);
    }

    // filterEnumMenu は、候補のボタンを、いまセルに打ってある字で絞ります（オートコンプリート・2026-10-19）。
    //   2026-10-19 利用者:「これをオートコンプリートのようにコンボボックスで選ぶように出来ないでしょうか？」「オートコンプリートはコアの機能です」
    //   - 打った字を含む候補だけを見せる（全角半角・大小は畳む）。空なら全部。合うものが無ければメニューを閉じる。
    //   - 絞るのは候補のボタン（data-choice）だけ。画像を選ぶボタンには触らない（出どころの候補〔suggest〕は同じ日に外した）。
    function filterEnumMenu() {
        const menu = document.getElementById('w-enum-menu');
        const cell = enumMenuCell;
        if (!menu || !cell || !cell.isConnected) return;
        const buttons = Array.from(menu.querySelectorAll('button[data-choice]'));
        if (!buttons.length) return;
        const fold = (t) => (t || '').normalize('NFKC').toLowerCase().trim();
        const q = fold(cell.textContent);
        let shown = 0;
        buttons.forEach(b => {
            const hit = !q || fold(b.textContent).includes(q);
            b.hidden = !hit;
            if (hit) shown++;
        });
        if (!shown) { menu.classList.remove('active'); return; }
        menu.classList.add('active');
        placeFloating(menu, cell.getBoundingClientRect(), 2);
    }

    // tagValueColumn はタグの値（dd）の {label, type, enum} を、すぐ前の dt の名前で設定の語彙から引きます
    // （resolveCellColumn のタグ版・語彙に無い名前は null）。
    function tagValueColumn(dd) {
        const dt = dd.previousElementSibling;
        if (!dt || dt.tagName !== 'DT') return null;
        const label = dt.textContent.trim();
        const word = vocabWords[label];
        if (!word) return null;
        return { label, type: word.type || (word.values ? 'enum' : ''), enum: word.values || [] };
    }

    // ── PDF付きブロックのエンハンサ（語彙モデル §3: マーカー要素へ振る舞いを配線） ──
    // PDF プレビュー（閲覧・編集）と、編集モードのドロップゾーン・明細AI解析ボタンを
    // 付ける。挿すのはすべて .vocab-chrome（シリアライザが保存しない編集クローム）。
    // 呼び出しは applyMode・populateEditor・挿入直後の3点（作り直し方式なので冪等）。
    //
    // 取り付け先は2種類:
    //   - **File 宣言の形式ブロック自身**（受発注。見出し形・属性形とも）——PDFの所在は
    //     本文の可視のファイル名リンクが運ぶ（見える文字がデータの手掛かり）
    //   - **file 容器**（互換・単独のPDF添付ブロック）——所在は data-src の配線
    // かつては file 容器だけが取り付け台で、受発注は必ず容器に包まれていた
    // （「data-typeがfileのsectionは必要ないのでは？」——2026-08-31 に廃止）。
    function pdfSrcOf(sec) {
        if (sec.getAttribute('data-type') === 'file') return sec.getAttribute('data-src') || '';
        const a = sec.querySelector('a[href]');
        if (a && /[.]pdf$/i.test(a.getAttribute('href'))) {
            const parts = a.getAttribute('href').split('/');
            return decodeURIComponent(parts[parts.length - 1]);
        }
        return '';
    }

    function enhanceFileSections() {
        const targets = Array.from(document.querySelectorAll('#w-editor-content section')).filter(sec => {
            if (sec.getAttribute('data-type') === 'file') return true;
            const def = sectionDefOf(sec);
            return !!(def && def.file);
        });
        targets.forEach(sec => {
            sec.querySelectorAll(':scope > .vocab-chrome').forEach(el => el.remove());
            const src = pdfSrcOf(sec);
            const isEdit = document.body.hasAttribute('edit-mode');

            if (src && src.toLowerCase().endsWith('.pdf') && currentPageId) {
                const embed = document.createElement('embed');
                embed.className = 'vocab-chrome file-preview';
                // リンクがあればその href（新旧どちらの置き場でも正しい）。
                // 無ければ旧data-src互換（ページフォルダ直下）。
                const link = sec.querySelector('a[href]');
                embed.src = (link && /[.]pdf$/i.test(link.getAttribute('href')))
                    ? link.getAttribute('href')
                    : '/data/master/' + currentPageId.substring(0, 2) + '/' + currentPageId + '/' + src;
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
            // 「🤖 PDFから明細を解析」（/api/parse-pdf）は 2026-10-01 に口ごと片付けた——押す道が無くなっていた
            // （File を宣言する形式が無い）。PDF を読むのは添付の「🤖 解析」（案件ページ〔2026-10-19 まで受注ページ〕・加工製品ページを作る）。
        });
    }

    // ── 機械に向けたタグを1つの引き出しへ畳む（閲覧モード限定）─────────────
    // 「優先度の低いヘッダをまとめてクリックすると開く場所に入れるのはどうでしょうか」
    // （2026-09-03 ユーザー要望。`差出人`と`差出人アドレス`の重複が発端）。
    //
    // 畳む対象は**機械に向けた値**——人は普段読まないが、機械には要る値:
    //   - `◯◯アドレス`     … 完全一致で検索するために原子化したメールアドレス
    //   - `Message-ID`・`In-Reply-To` … メールのヘッダの写し（取り込みの重複検知の鍵と、返信元のメールの
    //                          Message-ID）。2026-10-09 にヘッダの名前へ改めた（それまでは `メッセージID`・
    //                          `返信元メッセージID`——直す前の記録のために末尾の一致も残す）。人が前後をたどるのは
    //                          参照の `親ページID` なので、そちらは畳まない
    //   - `◯◯ハッシュ`     … 中身から作る重複検知の鍵（FAXの `内容ハッシュ`）
    // 対ごとに開閉ボタンを付ける案より、**開閉が1つで済み・IDの類も一緒に
    // 片付く**。「誰のアドレスか」はタグの名前が持っているので、まとめて開いても
    // 対応は崩れない（宛先が複数なら `宛先アドレス` が本文の順に並ぶ）。
    //
    // **畳むのは見た目だけ**——本文には対がそのまま在り、索引にも入る。開けば
    // すぐ見えるので、利用者は「自分が書いた文字がDBに入っている」ことを確かめられる:
    //
    //	「利用者が、『名前：値』のタグは見た目のままにDBに入れられると信じるためには、
    //	 実際にそうである必要があります」（ユーザー・2026-09-03）
    //
    // だから**編集モードでは畳まない**（編集する対象は本物でなければならない）。
    const MACHINE_TAG_SUFFIX = /(アドレス|メッセージID|ハッシュ)$/;
    const MACHINE_TAG_NAMES = new Set(['Message-ID', 'In-Reply-To']);

    function foldMachineTags() {
        document.querySelectorAll('#w-editor-content .tag-detail-toggle').forEach(el => el.remove());
        document.querySelectorAll('#w-editor-content .tag-folded')
            .forEach(el => el.classList.remove('tag-folded'));
        if (document.body.hasAttribute('edit-mode')) return; // 閲覧モード限定

        document.querySelectorAll('#w-editor-content dl[data-type="tags"]').forEach(dl => {
            // dt/dd の対を文書順で拾う（本文の形は「名前：値」の並び）。
            const pairs = [];
            let dt = null;
            for (const el of dl.children) {
                if (el.tagName === 'DT') dt = el;
                else if (el.tagName === 'DD' && dt) { pairs.push({ dt, dd: el }); dt = null; }
            }
            // **対になる人間向けの名前があるときだけ畳みます**（2026-09-13）。
            //
            // この規則は「`差出人` と `差出人アドレス` の重複」が発端でした。ところが
            // `〜アドレス` で終わる名前をすべて畳むと、**連絡先ページの
            // `メールアドレス` まで隠れます**——そこには対になる名前が無く、
            // アドレスこそがそのページの中身です。実際、担当者ページを開いても
            // アドレスが「詳細」の中で、何も書いていないページに見えていました。
            //
            // `Message-ID`・`In-Reply-To`・`メッセージID`・`ハッシュ` は対の有無に関わらず機械専用なので畳みます。
            const names = new Set(
                Array.from(dl.children)
                    .filter(el => el.tagName === 'DT')
                    .map(el => el.textContent.trim()));
            const folded = pairs.filter(p => {
                const name = p.dt.textContent.trim();
                if (MACHINE_TAG_NAMES.has(name)) return true;
                if (!MACHINE_TAG_SUFFIX.test(name)) return false;
                if (!name.endsWith('アドレス')) return true; // ID・ハッシュは常に畳む
                return names.has(name.slice(0, -'アドレス'.length));
            });
            // 畳む対象が無い、または**全部が対象**なら何もしない
            // （空のタグ欄と「詳細」だけが残るのは、隠しているようで気味が悪い）。
            if (!folded.length || folded.length === pairs.length) return;
            addDetailToggle(dl, folded);
        });
    }

    // addDetailToggle は対象の対を畳み、開閉チップを dl の末尾へ置きます。
    function addDetailToggle(dl, folded) {
        const setFolded = on => folded.forEach(p => {
            p.dt.classList.toggle('tag-folded', on);
            p.dd.classList.toggle('tag-folded', on);
        });
        setFolded(true);

        let open = false;
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'vocab-chrome tag-detail-toggle';
        btn.title = folded.map(p => p.dt.textContent.trim()).join('・');
        const label = () => open ? '▾ 隠す' : '▸ 詳細（' + folded.length + '件）';
        btn.textContent = label();
        btn.setAttribute('aria-expanded', 'false');
        btn.addEventListener('click', () => {
            open = !open;
            setFolded(!open);
            btn.textContent = label();
            btn.setAttribute('aria-expanded', open ? 'true' : 'false');
        });
        dl.appendChild(btn);
    }

    // ── 添付のクリック展開（PDFのインライン表示・ZIPの目録）──────────────────
    // 「clickで展開でお願いします」（2026-09-01 ユーザー決定）。
    // 埋め込みは本文マークアップでは書けない（サニタイザが embed/iframe を落とす
    // ——書き手にデータとしての実行力を渡さない線）ので、**閲覧モードのクローム**
    // としてここが差し込む。宛先はリンクの href から**導出**し、きれいなURL
    // （/<6桁ページID>/<生成ID>.pdf|.zip）の形に一致するものだけを扱う——
    // リンク（a[href]）はサニタイザが宛先を制限していないため、任意の href を
    // そのまま embed.src に流してはならない。
    // PDFの中身が悪意を持つ危険は埋め込み方によらず同じなので、開くかどうかの
    // 判断を人に残す（自動では開かない）。ZIPは目録だけ（/api/zip-list・展開はしない）。
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
        const wrap = document.createElement('p');
        wrap.className = 'record-make';
        wrap.hidden = true;
        const marks = document.createElement('span');
        marks.className = 'record-make-marks';
        const showMarks = (list) => {
            marks.textContent = '';
            (list || []).forEach(r => {
                marks.appendChild(document.createTextNode(' '));
                // ⚠ `attach-expand` を外す——添付の札を描き直す refreshAttachmentPreviews が本文の中の `.attach-expand` を
                // 全部消すので、問い合わせの答えが先に着くと印が消えていた（2026-10-01・E2E で間欠に落ちた）。
                const mark = makeAnalyzedMark(r);
                mark.classList.remove('attach-expand');
                marks.appendChild(mark);
            });
        };
        fetch('/api/record-makers?page_id=' + encodeURIComponent(currentPageId))
            .then(r => r.json()).then(d => {
                if (!d || !d.success) return;
                const kinds = d.kinds || [];
                if (!kinds.length && !(d.made || []).length) return;
                if (kinds.length) {
                    const label = document.createElement('label');
                    label.textContent = '作るページ ';
                    const sel = document.createElement('select');
                    sel.className = 'record-make-kind';
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
                    const btn = document.createElement('button');
                    btn.type = 'button';
                    btn.className = 'record-make-go';
                    btn.textContent = '作成';
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
            const res = await fetch('/api/record-make', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ page_id: currentPageId, kind }),
            });
            const d = await readResult(res);
            if (!res.ok || !d || !d.success) {
                notify(kind + 'を作れませんでした: ' + failMessage(res, d), { type: 'alert', duration: 0, id: 'record-make' });
            } else if (!(d.pages || []).length) {
                notify(d.say || 'ページは作りませんでした。', { type: 'warn', duration: 8000, id: 'record-make' });
            } else {
                notify(kind + 'を作りました: ' + d.pages.map(o => (o.title || o.page_id) + '（/' + o.page_id + '）').join('\n') +
                    (d.say ? '\n' + d.say : ''), { type: 'success', duration: 0, id: 'record-make' });
                born = true;
                open = d.open || '';
            }
        } catch (e) {
            notify(kind + 'を作れませんでした: ' + e.message, { type: 'alert', duration: 0, id: 'record-make' });
        }
        btn.disabled = false;
        btn.textContent = '作成';
        if (open) { location.href = '/' + open + '?edit=true'; return; } // 人が続けて書くページは開く
        if (born) {
            await reloadContent(); // 子ページ一覧の鏡と、この欄（印つき）を描き直す
            loadChildNav();        // 左レールの子ページ一覧
        }
    }

    const ATTACH_PREVIEW_RE = /^\/([0-9]{6})\/([0-9a-z]+)\.(pdf|zip)$/;

    // analyzedMap は「添付ID → 解析で生まれたページ」。印を出すために持ちます。
    //
    // **印そのものは保存していません**——解析が書いた `受信元` の逆引きです。
    // だから、間違った解析をゴミ箱へ入れれば印も消え、もう一度解析できます。
    let analyzedMap = {};
    // zipPDFs は「ZIP の添付ID → 中のPDF（[{id, file, path}]・中のパスの順）」（2026-09-30・まとめて解析）。
    let zipPDFs = {};

    // refreshAnalyzedMarks は解析済みの一覧を取り直し、印を描き直します。
    async function refreshAnalyzedMarks() {
        if (!currentPageId) return;
        if (!hasExtension('toho')) { analyzedMap = {}; zipPDFs = {}; return; } // 下請けが無ければ問わない（404を出さない）
        try {
            const res = await fetch('/api/analyzed?page_id=' + encodeURIComponent(currentPageId));
            const d = await res.json();
            analyzedMap = (d && d.success && d.analyzed) ? d.analyzed : {};
            zipPDFs = (d && d.success && d.zip_pdfs) ? d.zip_pdfs : {};
        } catch (e) {
            analyzedMap = {}; // 印が出ないだけ——解析そのものは押せる
            zipPDFs = {};
        }
        refreshAttachmentPreviews();
    }

    // makeAnalyzedMark は「✓ 図面」の印を作ります（押すと生まれたページへ飛ぶ）。
    //
    // ユーザー:「一度解析したファイルには解析済みの印と『図面』などの解析結果を
    // 付けては？」「添付ファイルのボタンの横あたりで良いのでは？」（2026-09-06）。
    function makeAnalyzedMark(r) {
        const mark = document.createElement('a');
        mark.className = 'vocab-chrome attach-expand attach-analyzed';
        mark.href = '/' + r.page_id;
        mark.textContent = '✓ ' + r.kind;
        mark.title = (r.title || r.page_id) + ' を作りました（押すと開きます）';
        return mark;
    }

    function refreshAttachmentPreviews() {
        document.querySelectorAll('#w-editor-content .attach-expand, #w-editor-content .attach-preview')
            .forEach(el => el.remove());
        if (document.body.hasAttribute('edit-mode')) return; // 閲覧モード限定
        document.querySelectorAll('#w-editor-content a[href]').forEach(a => {
            if (a.closest('.vocab-chrome')) return; // クローム内のリンクは対象外
            const m = ATTACH_PREVIEW_RE.exec(a.getAttribute('href') || '');
            if (!m) return;
            const kind = m[3];
            const btn = document.createElement('button');
            btn.type = 'button';
            btn.className = 'vocab-chrome attach-expand';
            btn.textContent = kind === 'pdf' ? '▶ 表示' : '▶ 中身';
            btn.addEventListener('click', () => toggleAttachPreview(a, btn, m));
            a.insertAdjacentElement('afterend', btn);
            // **参照を写せるようにします**（2026-09-14）。ユーザー:「PDFがほかの部品の
            // 一部で、加工製品ページを作らず、ほかのページに埋め込む場合はどうしますか？」
            //
            // 埋め込み自体は前からできました（`section[data-type="file-view"]` に
            // 参照を1つ書くだけ）。無かったのは**入口**です——`010272-c3p7` という
            // 値は添付IDを手で調べないと書けませんでした。
            const copyRef = makeCopyRefButton(m[1], m[2]);
            btn.insertAdjacentElement('afterend', copyRef);
            // ZIP には「中のPDFをまとめて解析」（2026-09-30）——中身は取り込みで1つずつ添付になっている。
            if (kind === 'zip' && hasExtension('toho') && (zipPDFs[m[2]] || []).length) {
                const zb = makeZipAnalyzeButton(m[1], zipPDFs[m[2]]);
                if (zb) copyRef.insertAdjacentElement('afterend', zb);
            }
            if (kind === 'pdf' && hasExtension('toho')) {
                // 判定→案件ページ（2026-10-19 まで受注ページ）の生成はボタン起動だけ（人間ゲート型・2026-09-01）。
                // **解析は下請けの持ち物**——載っていなければボタンを出さない（2026-09-15）。
                if (a.parentElement) {
                    const ab = makeAnalyzeButton(m[1], m[2] + '.pdf');
                    btn.insertAdjacentElement('afterend', ab);
                    // **解析済みなら、その隣に結果の印**（2026-09-06）。
                    // ボタンは消しません——読み違いはあるので、押し直せる余地を残します。
                    const done = analyzedMap[m[2]];
                    if (done) {
                        ab.textContent = '🤖 再解析';
                        ab.insertAdjacentElement('afterend', makeAnalyzedMark(done));
                    }
                }
            }
        });
        // PDF・ZIP 以外の添付（STEP・DXF・Excel …）にも「🔗 参照」（2026-10-01 利用者:「STEPファイルも加工製品ページで参照して
        // ダウンロードできるようにしたい」）——写した ID を加工製品ページの 📄 ファイル表示へ貼ると、そこから保存できる。
        document.querySelectorAll('#w-editor-content a[href]').forEach(a => {
            if (a.closest('.vocab-chrome')) return;
            const m = /^\/([0-9]{6})\/([0-9a-z]+)\.([0-9a-z_]+)$/.exec(a.getAttribute('href') || '');
            // PDF・ZIP は上で付けた。画像は写真の「🔗 ID」がある（decorateLocalEdit と同じ札を二重にしない）。
            if (!m || /^(pdf|zip|jpe?g|png|gif|webp|svg|bmp)$/.test(m[3])) return;
            a.insertAdjacentElement('afterend', makeCopyRefButton(m[1], m[2]));
        });
        // 表計算（.xlsx）にも「🤖 解析」（2026-09-30 利用者:「ハッキリと受注したとわかる場合は、受注ページにして良い」
        // ——Excel の注文リスト。サーバーが文字にして判定する）。表示のボタンは無い（開くのはリンクで）。
        if (!hasExtension('toho')) return;
        document.querySelectorAll('#w-editor-content a[href]').forEach(a => {
            if (a.closest('.vocab-chrome')) return;
            const m = /^\/([0-9]{6})\/([0-9a-z]+)\.xlsx$/.exec(a.getAttribute('href') || '');
            if (!m) return;
            const ab = makeAnalyzeButton(m[1], m[2] + '.xlsx');
            a.insertAdjacentElement('afterend', ab);
            const done = analyzedMap[m[2]];
            if (done) {
                ab.textContent = '🤖 再解析';
                ab.insertAdjacentElement('afterend', makeAnalyzedMark(done));
            }
        });
    }



    // makeCopyRefButton は添付の参照（`ページID-添付ID`）を写すボタンを作ります。
    //
    // **これが「ほかのページに出す」の入口**です。写した値を、行き先のページで
    // 📄 ファイル表示 を挿し、その札を押して開く**プロパティ欄へ貼れば**、
    // そこにファイルが開きます（2026-09-15 に配線を属性へ移したので、本文へ
    // タグを書く手順ではなくなりました）。
    //
    // 同じ値は `受信元` タグの中身でもあるので、参照を手で書きたい場面すべてに効きます。
    function makeCopyRefButton(pageID, attachID) {
        const ref = pageID + '-' + attachID;
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'vocab-chrome attach-expand attach-copyref';
        btn.textContent = '🔗 参照';
        btn.title = copyRefTitle(ref);
        btn.addEventListener('click', () => copyFileRef(ref));
        return btn;
    }

    // ── ファイルの ID を写す（2026-10-01） ───────────────────────────────
    //
    // 利用者:「メール送信にファイル添付が無いです。PDFや画像の表示にIDが在ると思いますが、それをクリック程度で
    // 簡単にクリップボードへコピーできると、ほかの場所にスラッシュメニューから貼りつけたり、メールに添付できるのでは」。
    // ID（`ページ番号-添付ID`）の貼り先は2つ——📄 ファイル表示の欄（スラッシュメニューで挿して札を押す）と、メールの
    // 送る欄の「🔗 ID で添付を足す」（mail-compose.js・サーバーの GET /api/file-ref が添付の組へ引く）。
    // ⚠ const にしない（本文を描く処理がこの行より上で先に走りうる——関数の宣言は先に立つ）。
    function copyRefTitle(ref) { return ref + ' を写します（メールの「🔗 ID で添付を足す」や、別のページの 📄 ファイル表示 の欄へ貼れます）'; }

    async function copyFileRef(ref) {
        try {
            await navigator.clipboard.writeText(ref);
            notify(ref + ' を写しました。メールの添付や 📄 ファイル表示 の欄へ貼れます。',
                { type: 'success', duration: 6000, id: 'copyref' });
        } catch (e) {
            // **写せない環境でも詰ませません**——値そのものを見せて、手で選べるように。
            notify('写せませんでした。この値を手で控えてください: ' + ref,
                { type: 'warn', duration: 0, id: 'copyref' });
        }
    }

    // ── メールの返信と「この記録への返信」ののぞき見（2026-09-03）──────────
    //
    // ユーザー:「返信の本体は送信箱にあるのはどうでしょう？そして、返信元のメールから
    // ものぞき見できるのが良いかと」——返信の本体は送信箱に立ち、ここは**参照で
    // 見えるだけ**です。返信元の本文は一切変わりません（通信記録は届いたときのまま）。
    //
    // DOM生成は createElement + textContent（innerHTML へ文字列を入れない）。

    // tagValue は本文のタグ（<dt>名前</dt><dd>値</dd>）から値を1つ読みます。
    function tagValue(name) {
        const host = document.getElementById('w-editor-content');
        if (!host) return '';
        const dts = host.querySelectorAll('dl[data-type="tags"] > dt');
        for (const dt of dts) {
            if (dt.textContent.trim() !== name) continue;
            const dd = dt.nextElementSibling;
            if (dd && dd.tagName === 'DD') return dd.textContent.trim();
        }
        return '';
    }

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

    // ── 分類の取り消し（2026-09-13）─────────────────────────────────────
    //
    // ユーザー:「間違えてアドレスを分類した場合、どうやって未分類に戻しますか？」。
    //
    // **仕組みとしては前からできました**——未登録の一覧は「どこにも `メールアドレス`
    // タグが無いアドレス」という索引からの派生なので、タグを外せば戻ります。
    // ただし**手数が非対称**でした: 分類は1クリック、取り消しは編集モードに入って
    // タグの組を消して保存。**押し間違いの取り消しが、いちばん面倒**という形です。
    //
    // そこで、取引先の下のページでは `メールアドレス` の隣に小さな口を出します（2026-09-16 から連絡先の置き場は
    // 「連絡帳」で、サーバーの口〔/api/contacts/unfile〕も連絡帳の下のページだけを受け付けます。⚠ この画面は置き場を
    // 確かめず、`メールアドレス` のタグがあるページならどこでも出します）。
    // 押すとそのタグだけ外れ、未登録の一覧へ戻ります。**ページは消しません**
    // （空になったら、そう伝えるだけ）。
    const EMAIL_TAG = 'メールアドレス';

    function refreshContactUnfile() {
        document.querySelectorAll('#w-editor-content .contact-unfile').forEach(el => el.remove());
        // 書ける形でも出す（2026-10-12——業務のボタンは書ける形でも押せる・【考察】編集モードの再考 §6 問い5）。
        if (!hasExtension('comm/contacts')) return; // アドレス帳の持ち物（2026-09-15）
        const host = document.getElementById('w-editor-content');
        if (!host || !currentPageId) return;

        host.querySelectorAll('dl[data-type="tags"] > dt').forEach(dt => {
            if (dt.textContent.trim() !== EMAIL_TAG) return;
            const dd = dt.nextElementSibling;
            if (!dd || dd.tagName.toLowerCase() !== 'dd') return;
            const addr = dd.textContent.trim();
            if (!addr) return;
            const btn = document.createElement('button');
            btn.type = 'button';
            btn.className = 'vocab-chrome chip-btn contact-unfile';
            btn.contentEditable = 'false'; // 書ける形では dd が書けるので、札の字に打てないように（2026-10-12）
            btn.textContent = '未分類へ戻す';
            btn.title = addr + ' をこのページから外し、「未登録の連絡先」へ戻します'
                + '（ページは消しません）';
            btn.addEventListener('click', () => unfileContact(btn, addr, dt, dd));
            dd.appendChild(btn);
        });
    }

    async function unfileContact(btn, addr, dt, dd) {
        btn.disabled = true;
        try {
            const res = await fetch('/api/contacts/unfile', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ page_id: currentPageId, address: addr }),
            });
            const data = await res.json();
            if (!data.success) {
                notify(data.message || '戻せませんでした', { type: 'warn' });
                return;
            }
            // 見た目だけ先に消します。正本は本文で、次に開けば描き直されます。
            dd.remove();
            dt.remove();
            if (data.empty) {
                // **消しません**——空になったことだけ伝えます（消すかは人が決める）。
                notify(addr + ' を未分類へ戻しました。このページは空になりました'
                    + '（要らなければ削除してください）。', { type: 'info', duration: 8000 });
            } else {
                notify(addr + ' を未分類へ戻しました', { type: 'success', duration: 5000 });
            }
        } catch (e) {
            notify('戻せませんでした: ' + e.message, { type: 'warn' });
        } finally {
            btn.disabled = false;
        }
    }

    function refreshPhoneChrome() {
        document.querySelectorAll('#w-editor-content .phone-chrome').forEach(el => el.remove());
        // 書ける形でも出す（2026-10-12——業務のボタンは書ける形でも押せる・【考察】編集モードの再考 §6 問い5）。
        const host = document.getElementById('w-editor-content');
        if (!host || !currentPageId) return;
        const number = tagValue(PHONE_TAG);
        if (!number) return;

        const box = document.createElement('div');
        box.className = 'vocab-chrome phone-chrome';
        box.setAttribute('contenteditable', 'false');

        // 発信そのものはブラウザ任せ（素の <a href="tel:">）。JSで横取りすると、
        // ソフトフォンへの受け渡しを壊しかねない。
        const call = document.createElement('a');
        call.className = 'phone-call';
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
            const label = document.createElement('label');
            label.className = 'phone-record';
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
            const res = await fetch('/api/intake/memo', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    channel: '電話',
                    direction: '送信',
                    phone: number,
                    counterpart: currentPageId,
                    title: (document.querySelector('#w-editor-content h1') || {}).textContent || '',
                }),
            });
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

    function refreshMailChrome() {
        document.querySelectorAll('#w-editor-content .mail-chrome').forEach(el => el.remove());
        // 書ける形でも出す（2026-10-12——業務のボタンは書ける形でも押せる・【考察】編集モードの再考 §6 問い5）。
        // **箱ごと通信の持ち物**です（2026-09-16）。中の「🧵 やりとりの前後」と
        // 「📨 この記録への返信」は `/api/thread`・`/api/replies` を叩くので、通信が
        // 無いと**通信記録ページを開くたびに 404 を2本**出します（どちらも失敗を
        // 黙って飲むので、画面には何も出ないまま要求だけが飛ぶ）。
        // ⚠ メール（`comm/mail`）だけを外した組では、この箱は**出ます**——読むだけの
        // 2つは通信の口で、外れるのは中の「✉️ 返信」だけ。
        if (!hasExtension('comm')) return;
        const host = document.getElementById('w-editor-content');
        if (!host || !currentPageId) return;
        // 通信記録かどうかは「チャネル」のタグで判る（受信も送信も持つ）。
        if (!tagValue(CHANNEL_TAG)) return;

        const box = document.createElement('div');
        box.className = 'vocab-chrome mail-chrome';
        box.setAttribute('contenteditable', 'false');

        // 対応は、2026-09-30 からここに「対応: 未処理・済・不要」の札を出していました。2026-10-19 に外しました
        // （2026-10-19 利用者:「「対応」の札はなくして、タグだけにしてください。」）——対応は本文のタグ `対応`（設定の語彙の選択肢）だけで、書ける形でタグの値を選んで替える。
        // この記録から作るページ（2026-10-01・選べる種類は拡張が登録する——受信だけ・下書きには出さないもサーバーが決める）。
        if (!tagValue(DRAFT_TAG)) box.appendChild(recordMakeControl());

        // **「✉️ 返信」だけがメール拡張の持ち物**です（2026-09-15）。同じ箱の「🧵 やりとりの
        // 前後」と「📨 この記録への返信」は記録を読むだけなので、メールを外しても出します。
        //
        // ⚠ **書く欄は部品です**（2026-09-30・assets/mail-compose.js）——用件「返信」の初期値
        // （宛先・RE: の件名・署名・引用・添付の候補）はサーバーが組みます。
        // ⚠ **下書きのページ**（`下書き` のタグ）では、返信の代わりに**下書きを開いた送る欄**を出します
        // （開いたらすぐ直して送れる・送ったら下書きはごみ箱へ行き、控えのページへ移る）。
        const compose = window.wcmsMailCompose;
        if (hasExtension('comm/mail') && compose) {
            const slot = document.createElement('div');
            slot.className = 'mail-compose-host';
            if (tagValue(DRAFT_TAG)) {
                box.appendChild(slot);
                compose.open(slot, { draftId: currentPageId });
            } else {
                const btn = document.createElement('button');
                btn.type = 'button';
                btn.className = 'mail-reply-open';
                btn.textContent = '✉️ 返信';
                btn.addEventListener('click', () => {
                    if (slot.firstChild) { slot.textContent = ''; return; } // もう一度押すと畳む
                    compose.open(slot, { purpose: '返信', pageId: currentPageId });
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
            const res = await fetch('/api/thread?page_id=' + encodeURIComponent(currentPageId));
            const d = await res.json();
            if (!d.success) return;
            prev = d.prev || null;
            next = d.next || [];
        } catch (e) { return; }
        if (!prev && !next.length) return;

        const wrap = document.createElement('div');
        wrap.className = 'mail-thread';
        const head = document.createElement('p');
        head.className = 'mail-thread-head';
        head.textContent = '🧵 やりとりの前後';
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
        const tag = document.createElement('span');
        tag.className = 'mail-thread-dir';
        // 向きの記号は通信箱の一覧と同じ流儀（←受信 / →送信）。
        tag.textContent = label + (r.direction === '送信' ? '（送信）' : r.direction === '受信' ? '（受信）' : '');
        li.appendChild(tag);
        const a = document.createElement('a');
        a.href = '/' + r.page_id;
        a.textContent = r.title || r.page_id;
        a.title = r.title || r.page_id;
        li.appendChild(a);
        if (r.when) {
            const when = document.createElement('span');
            when.className = 'mail-thread-when';
            when.textContent = ' ' + String(r.when).slice(0, 16).replace('T', ' ');
            li.appendChild(when);
        }
        return li;
    }

    // loadReplies は「この記録への返信」を逆引きして並べます（送信箱にある本体への案内）。
    async function loadReplies(box) {
        let replies = [];
        try {
            const res = await fetch('/api/replies?page_id=' + encodeURIComponent(currentPageId));
            const d = await res.json();
            if (!d.success) return;
            replies = d.replies || [];
        } catch (e) { return; }
        if (!replies.length) return;

        const wrap = document.createElement('div');
        wrap.className = 'mail-replies';
        const head = document.createElement('p');
        head.className = 'mail-replies-head';
        head.textContent = '📨 この記録への返信（本体は送信箱にあります）';
        wrap.appendChild(head);
        const ul = document.createElement('ul');
        replies.forEach(r => {
            const li = document.createElement('li');
            const when = document.createElement('span');
            when.className = 'mail-replies-when';
            when.textContent = (r.sent_at || '').slice(0, 16) + ' ';
            const a = document.createElement('a');
            a.href = '/' + r.page_id;
            a.textContent = r.title || r.page_id;
            li.appendChild(when);
            li.appendChild(a);
            if (r.to) {
                const to = document.createElement('span');
                to.className = 'mail-replies-to';
                to.textContent = ' → ' + r.to;
                li.appendChild(to);
            }
            ul.appendChild(li);
        });
        wrap.appendChild(ul);
        box.appendChild(wrap);
    }

    // ── 加工製品ページの整理（提案→人が直す→実行）───────────────────────────
    //
    // ユーザー:「各図面の行き場所について、解析から得られた推奨値を提示して、
    // ユーザーがそれを書き直して実行ボタンを押す形はどうですか？」
    // 「顧客名を書く欄に推奨値を入れてユーザーが修正してはどうでしょう」
    //
    // **提案は何も作りません**——顧客名・装置名称のページが生まれるのは実行のときだけ。
    // 「見積もりや試作の場合があるので、フォルダ名はユーザーが確認したほうが良い」ため。
    // DOM生成は createElement + textContent（innerHTML へ文字列を入れない）。

    async function refreshFilingButton() {
        document.querySelectorAll('#w-editor-content .filing-chrome').forEach(el => el.remove());
        // 書ける形でも出す（2026-10-12——業務のボタンは書ける形でも押せる・【考察】編集モードの再考 §6 問い5）。
        if (!currentPageId) return;
        const host = document.getElementById('w-editor-content');
        if (!host) return;
        if (!hasExtension('toho')) return; // 整理は下請けの持ち物（2026-09-15）
        // **整理が意味を持つのは通信記録だけ**——解析で図面ページが生まれた元のページ。
        // 年フォルダや加工製品ページに出しても行き場がない（2026-09-03 ユーザー指摘。
        // それまでは閲覧モードの全ページに出ていた）。
        if (!tagValue(CHANNEL_TAG)) return;

        // **整理するものが実際にあるときだけ出します。** 押してから「ありません」と
        // 言うより、出さないほうが親切です。
        let rows = [];
        let orders = [];
        let kinds = [];
        let partners = [];
        let machines = {};
        let files = [];
        try {
            const res = await fetch('/api/filing-proposal?page_id=' + encodeURIComponent(currentPageId));
            const d = await res.json();
            if (!d.success) return;
            rows = d.rows || [];
            // 案件ページ（2026-10-19 まで受注ページ）も同じ画面で片付けます（行き先は発注日で決まる——記録／年／月／依頼——ので欄は無い）。
            orders = d.orders || [];
            // **区分の選択肢はサーバーが持ちます**（設定の語彙「区分」・2026-09-29 に段から替えた）。
            // 画面に書き写すと、語を足した日に片方だけ古くなります。
            kinds = d.kinds || [];
            // 既にある取引先の名前——顧客名の入力候補に出します。
            partners = d.partners || [];
            // 既にある装置名称（顧客ごと）——装置名称の入力候補に出します。
            machines = d.machines || {};
            // 記録の添付（2026-10-08・ext/toho/filing_files.go——図面以外のファイルも、行き先を探して足す）。
            files = d.files || [];
        } catch (e) { return; }
        if (!rows.length && !orders.length && !files.length) return;

        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'vocab-chrome filing-chrome filing-open';
        const n = rows.length + orders.length;
        btn.textContent = '📁 整理（' + (n ? n + '件' : '') + (n && files.length ? '・' : '') + (files.length ? 'ファイル ' + files.length : '') + '）';
        btn.addEventListener('click', () => toggleFilingPanel(btn, rows, orders, kinds, partners, machines, files));
        host.appendChild(btn);
    }

    // toggleFilingPanel は行き先の表を出し入れします（候補は取得済み）。
    function toggleFilingPanel(btn, rows, orders, kinds, partners, machines, files) {
        const existing = document.querySelector('.filing-panel');
        if (existing) { existing.remove(); return; }
        btn.insertAdjacentElement('afterend', buildFilingPanel(rows, orders, kinds, partners, machines, files));
    }

    // buildFilingPanel は行き先の表を組みます。**全部の欄が編集できます**
    // ——試作の「【試作】…」は機械には決められないので、ここで人が打ちます（2026-09-29 からは試作も区分のタグで、
    // 題に【試作】を付けなくてよい。区分は加工製品ページで付ける——下の kindBoxes）。
    function buildFilingPanel(rows, orders, kinds, partners, machines, files) {
        const panel = document.createElement('div');
        panel.className = 'vocab-chrome filing-chrome filing-panel';
        panel.setAttribute('contenteditable', 'false');

        // ⚠ **言葉は短く・1枚ずつ縦に**（2026-09-30 利用者:「入力欄が見えなくなるくらい、横幅が狭くなります。
        //    横にたくさんの項目がありすぎるのと、チェックボックスの説明が長すぎます」）——それまでは6列の表で、
        //    欄が 50px 台まで押しつぶされ、選択肢の説明が1文字ずつ縦に折れていました。
        const head = document.createElement('p');
        head.className = 'filing-head';
        // 欄の名前はページの名前（2026-10-08 利用者:「『顧客名』『装置名称』『（加工）製品名称』がページ名の意味ではないでしょうか？」
        // ——それまで3つ目は「図面名称」と呼んでいたが、入れるのは加工製品ページの題）。
        // 装置名称は空でもよい（2026-10-16——社名の直下に置く）。顧客名・製品名称が空なら動かさない。
        head.textContent = '行き先: 製造台帳／顧客名／装置名称／製品名称（装置名称は空でもよく、そのときは顧客名の直下）。顧客名か製品名称が空の図面は動かしません。';
        if (rows.length) panel.appendChild(head);

        // **既にある取引先を候補に出します。** 実データの初回で、アドレス帳が作った
        // 「南北スポーツ機械」と、整理で打った「株式会社南北スポーツ機械」が
        // 同じ会社で2枚になりました。題の一致は完全一致のままにして（機械が名寄せすると
        // 別の顧客が1つに潰れる）、**人が既にある名前を選べる**ようにして解きます。
        const listID = 'w-filing-partners';
        if ((partners || []).length) {
            const dl = document.createElement('datalist');
            dl.id = listID;
            partners.forEach(name => {
                const op = document.createElement('option');
                op.value = name;
                dl.appendChild(op);
            });
            panel.appendChild(dl);
        }

        // 図面1枚ごとにカード（見出し＝図面番号・欄は1行に1つで横幅いっぱい）。
        const list = document.createElement('div');
        list.className = 'filing-cards';

        const inputs = [];
        rows.forEach((row, rowIndex) => {
            const card = document.createElement('div');
            card.className = 'filing-card';
            card.dataset.pageId = row.page_id;
            const no = document.createElement('p');
            no.className = 'filing-no';
            no.textContent = '📄 ' + (row.drawing_no || '（図面番号なし）');
            card.appendChild(no);
            const grid = document.createElement('div');
            grid.className = 'filing-grid';
            card.appendChild(grid);

            const fields = {};
            // 顧客名・装置名称・図面名称は自由入力（1行に1つ・見出しは左）、区分は印。
            const addText = (key, labelText, value) => {
                const lb = document.createElement('label');
                lb.className = 'filing-field';
                const span = document.createElement('span');
                span.textContent = labelText;
                const input = document.createElement('input');
                input.type = 'text';
                input.value = value || '';
                input.setAttribute('aria-label', key);
                lb.append(span, input);
                grid.appendChild(lb);
                fields[key] = input;
            };
            addText('customer', '顧客名', row.customer);
            if ((partners || []).length) {
                fields.customer.setAttribute('list', listID);
            }

            addText('machine_name', '装置名称', row.machine_name);
            // **装置名称の候補は、その行の顧客のぶんだけ**（2026-09-11）。
            // 顧客名で解いたのと同じ問題が一段下に残っていました——1通のメールの
            // 5枚が `φ410 2輪` / `2輪シュート改良` / `φ410-2輪` / `2軸シュート改良`
            // （輪→軸の誤読）に割れ、そのまま流すと1台の装置が4フォルダに散ります。
            //
            // **行ごとに作り直します**——顧客名の欄は人がその場で打ち替えるので、
            // パネルを開いた時点の顧客で固定すると、打ち替えたあと候補が嘘になります。
            const machineListID = 'w-filing-machines-' + rowIndex;
            const machineList = document.createElement('datalist');
            machineList.id = machineListID;
            panel.appendChild(machineList);
            fields.machine_name.setAttribute('list', machineListID);
            const fillMachines = () => {
                machineList.replaceChildren();
                const names = (machines || {})[fields.customer.value.trim()] || [];
                names.forEach(name => {
                    const op = document.createElement('option');
                    op.value = name;
                    machineList.appendChild(op);
                });
            };
            fillMachines();
            fields.customer.addEventListener('input', fillMachines);
            fields.customer.addEventListener('change', fillMachines);
            addText('drawing_name', '製品名称', row.drawing_name); // 加工製品ページの題（2026-10-08 に「図面名称」から改めた）

            // **区分は整理では選びません**（2026-10-08 利用者:「試作や見積もりによるフォルダ分けが無くなったので、整理ブロックで入力する
            // 必要はなくなりました」——09-29 から区分は加工製品ページのタグで、行き先の木を分けない）。区分を付けるのは加工製品ページで。
            // 送る値は空（サーバーは区分を書かない——kindsNote は空なら何も言わない）。
            const kindBoxes = [];

            // 「図面番号を品番に」「図面名称を品名に」（2026-10-01 利用者:「図面名称を品名、図面番号を品番にするチェックボックスが
            // あっても良いのかもしれません」）——新規のときだけ効く（出すのも新規のときだけ）。初期値は取引先のページの決まり
            // （`品番の決め方：図面番号`・`品名の決め方：図面名称`・サーバーが行き先と一緒に返す）。人が触ったら、顧客を打ち替えても戻さない。
            // 弊社品番（このページの番号）は印が無くても書く。
            const tdRules = document.createElement('div');
            tdRules.className = 'filing-field filing-kinds filing-rules';
            const rulesHead = document.createElement('span');
            rulesHead.textContent = '品番・品名';
            rulesHead.title = '新しく置く加工製品の題の下のタグ（空いているときだけ書く・弊社品番はいつも書く）';
            tdRules.appendChild(rulesHead);
            const rulesWrap = document.createElement('div');
            rulesWrap.className = 'filing-kinds-boxes';
            tdRules.appendChild(rulesWrap);
            const ruleBox = (text) => {
                const label = document.createElement('label');
                const box = document.createElement('input');
                box.type = 'checkbox';
                box.addEventListener('change', () => { box.dataset.touched = '1'; });
                label.appendChild(box);
                label.appendChild(document.createTextNode(' ' + text));
                rulesWrap.appendChild(label);
                return box;
            };
            const ruleBoxes = { partNo: ruleBox('図面番号を品番に'), itemName: ruleBox('図面名称を品名に') };
            tdRules.hidden = true;
            grid.appendChild(tdRules);

            const tdConfirm = document.createElement('div');
            tdConfirm.className = 'filing-card-foot';

            // ── 新規・図面追加・図面改定（2026-09-30 からいつも出す）──
            //
            // 利用者:「既存の加工製品に図面を追加する場合、既存の加工製品の図面が修正され、新しい図面に変更する場合、
            // 全くの新規図面で加工製品ページを新設する場合があるからです」。それまでは「行き先に同じ題のページが
            // あるときだけ」改定・追加を出していた（09-20）——装置名称が読めていない図面（今回の rev1）では行き先が
            // 引けず、改定の相手を探す手掛かりが画面に無かった。いまは3つをいつも出し、**既にある加工製品の候補**
            // （同じ取引先で図面番号〔版の印は除く〕か題が同じ）を押すと欄に入る。
            //
            //   行き先に同じ題のページが無い … 既定は「新規」。改定・追加を選んだら「相手を決めてください」
            //                                    （サーバーも作らずに断る——黙って新規にしない）
            //   行き先に同じ題のページが在る … 改定か追加か、同じ名前の別の品物なら新規かを**人が選ぶ**（既定を置かない——
            //                                    溶接図が黙って旧版になるのが、それまでの振る舞いだった）。2026-10-01 までは
            //                                    「新規」を選べなかった（利用者:「実際には同じ品名があります」）
            //   同じ題のページが何枚も在る   … 改定・追加の行き先を押して決める（`target`——押すまでサーバーも動かさない）
            //
            // ⚠ 行き先の題は打ち替えるたびに聞き直す（ユーザー:「編集者が微妙に書き換える」2026-09-20）。
            // ⚠ 行き先が別のページへ変わったら、改定・追加の選択は外す（前の相手のつもりで選んだものを持ち越さない）。
            const choiceWrap = document.createElement('div');
            choiceWrap.className = 'filing-choice';
            const choiceNote = document.createElement('p');
            choiceNote.className = 'filing-choice-note';
            // 同じ図面が既にあるときの知らせ（2026-09-30 利用者:「同じものがあるということを提示して欲しいのと、
            // 同じものがあるので取り込まない選択肢が欲しいです」）——サーバーが候補に付けた印（同じ図面番号・同じファイル）から。
            const dupWrap = document.createElement('div');
            dupWrap.className = 'filing-dups';
            dupWrap.hidden = true;
            const candWrap = document.createElement('div');
            candWrap.className = 'filing-candidates';
            // 同じ題の加工製品が何枚もあるときの、図面追加・図面改定の行き先（2026-10-01）。
            const sameWrap = document.createElement('div');
            sameWrap.className = 'filing-candidates filing-same';
            const mergeName = 'w-filing-merge-' + rowIndex;
            const mergeInputs = {};
            const mergeLabels = {};
            // 言葉は利用者の案（2026-09-30:「『図面改定』と『図面追加』をご提案します」）。詳しくはマウスを載せたときに。
            // 「重複（取り込まない）」は同じ図面が既にあるときだけ出す。
            [['new', '新規', '新しい加工製品ページとして置きます'],
             ['drawing', '図面追加', '既にある加工製品に、二つ目の図面として足します（部品図と溶接図など）'],
             ['revision', '図面改定', '既にある加工製品の図面を差し替えます（いまの図面は旧版として子ページへ）'],
             // 装置のページへ（2026-10-08 利用者:「図面をフォルダページに追加したいのですが、出来ません」）——組立図など装置全体の図面。
             ['folder', '装置のページへ', '装置名称のページ（加工製品のフォルダ）に図面を足します——加工製品ページは作りません（組立図など）'],
             ['duplicate', '重複（取り込まない）', '同じ図面が既にあるので取り込みません（解析で作ったページはごみ箱へ・既にある加工製品にこのメールの受信元を書き足します）'],
             // **何もしない**（2026-10-03 利用者:「図面を解析しても何もしない選択肢も必要です」）——実行で送らない。解析で作った
             // ページはこのメールの下にそのまま残る（あとで整理できる・要らなければページを消す）。
             ['skip', '何もしない', '今回は整理しません——解析で作ったページはこのメールの下にそのまま残ります（あとで整理できます）']]
                .forEach(([val, text, hint]) => {
                    const label = document.createElement('label');
                    label.className = 'filing-merge-opt';
                    label.title = hint;
                    const radio = document.createElement('input');
                    radio.type = 'radio';
                    radio.name = mergeName;
                    radio.value = val;
                    label.appendChild(radio);
                    label.appendChild(document.createTextNode(' ' + text));
                    choiceWrap.appendChild(label);
                    mergeInputs[val] = radio;
                    mergeLabels[val] = label;
                });
            mergeLabels.duplicate.hidden = true;
            // **図面追加・図面改定の行き先を探す**（2026-10-03 利用者:「図面を追加してもどこに入れるか入力する欄が無い」
            // 「どの加工製品に追加するか人間が指定する必要があります」）——品番・品名・図面番号・題・ページ番号で既にある
            // 加工製品を探す（`/api/filing-search`・同じ取引先が先）。押すと、上の候補を押したときと同じく欄（取引先・装置名称・
            // 図面名称）に入り、そのページが行き先になる。二つ目の図面（溶接図など）は番号も名前も違うので、機械の候補に出ない。
            const searchWrap = document.createElement('div');
            searchWrap.className = 'filing-search';
            searchWrap.hidden = true;
            const searchLabel = document.createElement('label');
            searchLabel.className = 'filing-search-label';
            searchLabel.textContent = '行き先を探す: ';
            const searchInput = document.createElement('input');
            searchInput.type = 'search';
            searchInput.className = 'filing-search-input';
            searchInput.placeholder = '品番・品名・図面番号・題・ページ番号';
            searchLabel.appendChild(searchInput);
            const searchOut = document.createElement('div');
            searchOut.className = 'filing-candidates filing-search-out';
            searchWrap.append(searchLabel, searchOut);
            choiceWrap.appendChild(choiceNote);
            choiceWrap.appendChild(dupWrap);
            choiceWrap.appendChild(sameWrap);
            choiceWrap.appendChild(candWrap);
            choiceWrap.appendChild(searchWrap);
            tdConfirm.appendChild(choiceWrap);

            let target = { exists: false, page_id: '', same: [] };
            // chosenTarget は人が押した行き先のページ（同じ題が何枚もあるとき・候補を押したとき）。
            let chosenTarget = '';
            // keepMergeOnce は「人が行き先を押した」印です——次の聞き直しで行き先が変わっても、図面追加・図面改定の選択を外さない
            // （2026-10-03——外していたので、図面追加を選んで行き先を探して押すと選択が消え、実行で「選んでください」と断られた）。
            let keepMergeOnce = false;
            const ambiguous = () => target.same.length > 1 && !target.same.some(s => s.page_id === chosenTarget);
            // dup は「重複」の相手（同じ図面番号か同じファイルの加工製品のうち、いちばん強いもの）。
            let dup = null;
            // 人が自分で選んだら、聞き直しのたびの初期値で上書きしない。
            let userPicked = false;
            const picked = () => Object.keys(mergeInputs).find(k => mergeInputs[k].checked) || '';
            const explain = () => {
                const m = picked();
                tdRules.hidden = m !== 'new';
                searchWrap.hidden = m !== 'drawing' && m !== 'revision';
                if (m === 'skip') {
                    choiceNote.textContent = '整理しません——解析で作ったこのページは、このメールの下にそのまま残します';
                } else if (m === 'folder') {
                    // 装置のページへ（2026-10-08・ext/toho/filing_folder.go）——行き先は取引先と装置名称の欄で決まる。
                    const mn = fields.machine_name.value.trim();
                    choiceNote.textContent = mn
                        ? '装置「' + mn + '」のページにこの図面を足します（加工製品ページは作りません・製品名称の欄は使いません）'
                        : '⚠ 装置名称を入れてください（既にある装置のページに足します）';
                } else if (m === 'duplicate' && dup) {
                    choiceNote.textContent = '取り込みません——解析で作ったこのページはごみ箱へ移し、「' +
                        (dup.machine ? dup.machine + '／' : '') + dup.title + '」にこのメールの受信元を書き足します';
                } else if (target.exists && m === 'new') {
                    choiceNote.textContent = '⚠ 「' + target.title + '」は既に' + (target.same.length > 1 ? ' ' + target.same.length + ' 枚' : '') +
                        'あります——同じ名前の別の品物として、隣に新しく置きます';
                } else if (target.exists && (m === 'drawing' || m === 'revision') && ambiguous()) {
                    choiceNote.textContent = '⚠ 「' + target.title + '」が ' + target.same.length + ' 枚あります。下から行き先を押してください';
                } else if (target.exists) {
                    const nos = target.same.length > 1 ? '（' + target.same.length + ' 枚）' : target.nos ? '（図面: ' + target.nos + '）' : '';
                    choiceNote.textContent = '⚠ 行き先の「' + target.title + '」は既にあります' + nos +
                        (m === 'drawing' || m === 'revision' ? '。' : dup ? '。図面追加・図面改定・重複か、同じ名前の別の品物なら新規を選んでください'
                            : '。図面追加・図面改定か、同じ名前の別の品物なら新規を選んでください');
                } else if (m === 'drawing' || m === 'revision') {
                    choiceNote.textContent = '⚠ 行き先に同じ加工製品がありません。下の候補を押すか、装置名称・製品名称を合わせてください';
                } else {
                    choiceNote.textContent = '';
                }
                choiceNote.hidden = !choiceNote.textContent;
            };
            Object.values(mergeInputs).forEach(r => r.addEventListener('change', () => {
                userPicked = true;
                explain();
            }));

            // showDups は「同じ図面が既にある」の知らせです。同じファイルでなく番号だけ同じなら、中身を Gemini に
            // 見比べさせるボタンを添える（押したときだけ・利用者:「PDFの中身も比較してくれるなら」）。
            const showDups = (dups) => {
                dupWrap.replaceChildren();
                dupWrap.hidden = !dups.length;
                dups.forEach(c => {
                    const line = document.createElement('p');
                    line.className = 'filing-dup';
                    const a = document.createElement('a');
                    a.href = '/' + c.page_id;
                    a.target = '_blank';
                    a.textContent = (c.machine ? c.machine + '／' : '') + c.title;
                    const why = c.same_no && c.same_file ? '同じ図面番号・同じファイルです'
                        : c.same_no ? '同じ図面番号ですが、ファイルの中身は違います（図番を変えない改定か、PDFを作り直しただけかもしれません）'
                        : '同じファイルから作られています（1つのPDFに別の図面が入っていることもあります）';
                    line.append('⚠ 同じ図面が既にあります: ', a, '。' + why);
                    if (c.same_no && !c.same_file) {
                        const b = document.createElement('button');
                        b.type = 'button';
                        b.className = 'chip-btn filing-compare';
                        b.textContent = '🤖 中身を比べる';
                        b.title = '2つのPDFを Gemini に見比べさせます（数十秒かかります）';
                        const out = document.createElement('span');
                        out.className = 'filing-compare-out';
                        b.addEventListener('click', async () => {
                            b.disabled = true;
                            out.textContent = ' 見比べています…';
                            try {
                                const res = await fetch('/api/compare-drawings', {
                                    method: 'POST',
                                    headers: { 'Content-Type': 'application/json' },
                                    body: JSON.stringify({ page_id: row.page_id, with: c.page_id }),
                                });
                                const d = await res.json().catch(() => ({}));
                                if (!res.ok || !d.success) throw new Error(d.message || res.status);
                                const diffs = (d.differences || []).join('・');
                                out.textContent = d.same
                                    ? ' ✓ 同じ図面です' + (d.summary ? '（' + d.summary + '）' : '')
                                    : ' ⚠ 違いがあります: ' + (diffs || d.summary || '（詳しくは言えませんでした）');
                            } catch (e) {
                                out.textContent = ' 見比べられませんでした: ' + e.message;
                            }
                            b.disabled = false;
                        });
                        line.append(' ', b, out);
                    }
                    dupWrap.appendChild(line);
                });
            };

            let targetTimer = null;
            const askTarget = async () => {
                const q = new URLSearchParams({
                    customer: fields.customer.value.trim(),
                    machine: fields.machine_name.value.trim(),
                    name: fields.drawing_name.value.trim(),
                    drawing_no: row.drawing_no || '',
                    // 整理を待つこのページ——サーバーが候補に「同じ図面番号」「同じファイル」の印を付ける（2026-09-30）。
                    page_id: row.page_id,
                });
                let d = null;
                try {
                    const res = await fetch('/api/filing-target?' + q.toString());
                    d = await res.json();
                } catch (e) { d = null; }
                const was = target.page_id;
                target = d && d.exists
                    ? { exists: true, page_id: d.page_id || '', title: d.title || '', nos: (d.drawing_nos || []).join('・'),
                        same: d.same || [] }
                    : { exists: false, page_id: '', same: [] };
                // 行き先が変わったら、前の相手のつもりの改定・追加は外す。
                const keepMerge = keepMergeOnce;
                keepMergeOnce = false;
                // ⚠ 行き先が「無い → 在る」に変わっただけなら外さない（前の相手が居ないので、持ち越すものが無い——開いた直後に
                //    図面追加を押すと、最初の聞き直しの答えで選択が消えていた・2026-10-03）。
                if (target.page_id !== was && was !== '' && !keepMerge && (picked() === 'drawing' || picked() === 'revision')) {
                    mergeInputs.drawing.checked = false;
                    mergeInputs.revision.checked = false;
                }
                // 「図面番号を品番に」「図面名称を品名に」の初期値は取引先の決まり（人が触ったものはそのまま）。
                const rule = (d && d.rule) || {};
                if (!ruleBoxes.partNo.dataset.touched) ruleBoxes.partNo.checked = !!rule.part_no;
                if (!ruleBoxes.itemName.dataset.touched) ruleBoxes.itemName.checked = !!rule.item_name;
                // 押した行き先が、もう同じ題の中に無ければ外す（欄を打ち替えた）。
                if (chosenTarget && !target.same.some(s => s.page_id === chosenTarget)) chosenTarget = '';
                // 同じ題のページが在るとき「新規」の既定は外す（同じ名前の別の品物なら人が選ぶ・2026-10-01 から選べる）。
                if (target.exists && !userPicked && mergeInputs.new.checked) mergeInputs.new.checked = false;
                // 同じ題が何枚もあるなら、図面追加・図面改定の行き先を押して決める。
                sameWrap.replaceChildren();
                if (target.same.length > 1) {
                    const head = document.createElement('span');
                    head.className = 'filing-cand-head';
                    head.textContent = '図面追加・図面改定の行き先:';
                    sameWrap.appendChild(head);
                    target.same.forEach(s => {
                        const b = document.createElement('button');
                        b.type = 'button';
                        b.className = 'chip-btn filing-same-pick' + (s.page_id === chosenTarget ? ' is-chosen' : '');
                        b.textContent = (s.page_id === chosenTarget ? '✓ ' : '') + '/' + s.page_id +
                            '（図面: ' + ((s.drawing_nos || []).join('・') || 'なし') + '）';
                        b.title = 'この「' + target.title + '」を行き先にする（押すと開かずに選ぶ・中身は /' + s.page_id + ' で見る）';
                        b.addEventListener('click', () => {
                            chosenTarget = s.page_id;
                            askTarget(); // 印を描き直す
                        });
                        sameWrap.appendChild(b);
                    });
                }
                // 同じ図面が既にあるか（2026-09-30）——「重複（取り込まない）」はそのときだけ出す。相手は同じ番号で
                // 同じファイルのものを第一に、次に同じ番号。同じ番号で同じファイルなら、人が選ぶ前の初期値にする。
                const all = (d && d.candidates) || [];
                const dups = all.filter(c => c.same_no || c.same_file);
                const rank = c => (c.same_no ? 2 : 0) + (c.same_file ? 1 : 0);
                dup = dups.slice().sort((x, y) => rank(y) - rank(x))[0] || null;
                showDups(dups);
                mergeLabels.duplicate.hidden = !dup;
                if (!dup && mergeInputs.duplicate.checked) {
                    mergeInputs.duplicate.checked = false;
                    userPicked = false;
                }
                if (!userPicked && dup && dup.same_no && dup.same_file) {
                    mergeInputs.duplicate.checked = true;
                } else if (!userPicked && mergeInputs.duplicate.checked) {
                    mergeInputs.duplicate.checked = false; // 初期値で選んでいたが、もう言い切れない
                }
                if (!target.exists && !picked()) mergeInputs.new.checked = true;
                // 既にある加工製品の候補（押すと装置名称・図面名称の欄へ入り、行き先を聞き直す）。
                candWrap.replaceChildren();
                const cands = all.filter(c => !target.same.some(s => s.page_id === c.page_id));
                if (cands.length) {
                    const head = document.createElement('span');
                    head.className = 'filing-cand-head';
                    head.textContent = '既にある加工製品:';
                    candWrap.appendChild(head);
                    cands.forEach(c => {
                        const b = document.createElement('button');
                        b.type = 'button';
                        b.className = 'chip-btn filing-cand';
                        b.textContent = (c.machine ? c.machine + '／' : '') + c.title;
                        b.title = '行き先をここにする（図面: ' + (c.drawing_nos || 'なし') + '・/' + c.page_id + '）';
                        b.addEventListener('click', () => {
                            chosenTarget = c.page_id; // 同じ題が何枚もあっても、押したこのページが行き先
                            keepMergeOnce = true;     // 人が押した行き先——図面追加・図面改定の選択は外さない
                            fields.machine_name.value = c.machine || '';
                            fields.drawing_name.value = c.title || '';
                            fields.machine_name.dispatchEvent(new Event('change'));
                            askTarget();
                        });
                        candWrap.appendChild(b);
                    });
                }
                explain();
            };
            const askTargetSoon = () => {
                if (targetTimer) clearTimeout(targetTimer);
                targetTimer = setTimeout(askTarget, 400);
            };
            ['customer', 'machine_name', 'drawing_name'].forEach(key => {
                fields[key].addEventListener('input', askTargetSoon);
                fields[key].addEventListener('change', askTargetSoon);
            });
            askTargetSoon();

            // 行き先を探す（上の searchWrap）——打つたびに少し待って聞く。押した結果が行き先になる。
            let searchTimer = null;
            let searchSeq = 0;
            const runSearch = async () => {
                const qv = searchInput.value.trim();
                const seq = ++searchSeq;
                if (!qv) { searchOut.replaceChildren(); return; }
                let d = null;
                try {
                    const res = await fetch('/api/filing-search?' + new URLSearchParams({
                        customer: fields.customer.value.trim(), q: qv,
                    }).toString());
                    d = await res.json();
                } catch (e) { d = null; }
                if (seq !== searchSeq) return; // 打ち替えた——古い答えは捨てる
                searchOut.replaceChildren();
                const hits = (d && d.results) || [];
                const head = document.createElement('span');
                head.className = 'filing-cand-head';
                head.textContent = hits.length ? '見つかった加工製品（押すと行き先）:' : '見つかりません';
                searchOut.appendChild(head);
                hits.forEach(h => {
                    const b = document.createElement('button');
                    b.type = 'button';
                    b.className = 'chip-btn filing-search-pick' + (h.page_id === chosenTarget ? ' is-chosen' : '');
                    b.textContent = (h.page_id === chosenTarget ? '✓ ' : '') +
                        [h.customer, h.machine, h.title].filter(Boolean).join('／') + '（/' + h.page_id + '）';
                    b.title = '行き先をここにする（図面: ' + (h.drawing_nos || 'なし') + (h.part_nos ? '・品番: ' + h.part_nos : '') + '）';
                    b.addEventListener('click', () => {
                        chosenTarget = h.page_id;
                        keepMergeOnce = true; // 人が押した行き先——図面追加・図面改定の選択は外さない
                        fields.customer.value = h.customer || fields.customer.value;
                        fields.machine_name.value = h.machine || '';
                        fields.drawing_name.value = h.title || '';
                        fields.customer.dispatchEvent(new Event('change'));
                        fields.machine_name.dispatchEvent(new Event('change'));
                        askTarget();
                        runSearch(); // 印（✓）を描き直す
                    });
                    searchOut.appendChild(b);
                });
            };
            searchInput.addEventListener('input', () => {
                if (searchTimer) clearTimeout(searchTimer);
                searchTimer = setTimeout(runSearch, 300);
            });

            // 「改定として合流」の確認——**既定は隠しておき、実行が確認を求めた
            // ときだけ出します**。最初から出すと「押せば通る」と学習されてしまい、
            // 偽の改定を止める意味が薄れます。
            //
            // ⚠ **上の選択とは別の問い**です。あちらは「改定か二つ目の図面か」、
            // こちらは「疑わしい点を承知のうえで進めるか」——⚠ **疑わしさは2種類**
            // あります: 図面番号が既存の版と同じ／**同じ添付から来た同じ図面番号**
            // （本来あってはならないが、先方の誤りで実在する。2026-09-20）。
            // どちらなのかは実行が返す文が言います。
            const confirm = document.createElement('label');
            confirm.className = 'filing-confirm';
            confirm.hidden = true;
            const box = document.createElement('input');
            box.type = 'checkbox';
            confirm.appendChild(box);
            confirm.appendChild(document.createTextNode(' 承知のうえで進める'));
            tdConfirm.appendChild(confirm);
            card.appendChild(tdConfirm);

            inputs.push({
                page_id: row.page_id, fields: fields, confirm: confirm, box: box,
                merge: mergeInputs, choice: choiceWrap, kinds: kindBoxes, dupOf: () => (dup ? dup.page_id : ''),
                target: () => chosenTarget, rules: ruleBoxes,
            });
            list.appendChild(card);
        });
        if (rows.length) panel.appendChild(list);

        // **受注ページは別の表**です。直せるのは発注元だけ（行き先は発注日で決まる）
        // なので、図面と同じ列に並べると空欄ばかりの行になります。
        const pickedOrders = [];
        if (orders.length) {
            const oHead = document.createElement('p');
            oHead.className = 'filing-head';
            oHead.textContent = '案件ページは「記録」の年月へ収めます（外すものは印を外す）。';
            panel.appendChild(oHead);

            const oTable = document.createElement('table');
            oTable.className = 'filing-table filing-orders';
            const oTrh = document.createElement('tr');
            ['', '発注書番号', '発注元', '発注日', '行き先'].forEach(t => {
                const th = document.createElement('th');
                th.textContent = t;
                oTrh.appendChild(th);
            });
            oTable.appendChild(oTrh);

            orders.forEach(o => {
                const tr = document.createElement('tr');
                const tdBox = document.createElement('td');
                const box = document.createElement('input');
                box.type = 'checkbox';
                box.checked = true;
                box.setAttribute('aria-label', '収める');
                tdBox.appendChild(box);
                tr.appendChild(tdBox);
                const tdNo = document.createElement('td');
                tdNo.textContent = o.order_no || '（番号なし）';
                tr.appendChild(tdNo);

                // **発注元だけは直せます**（2026-09-20）。初期値はサーバーが連絡帳へ
                // 寄せた題で、候補は図面の行と同じ `datalist` を共有します
                // ——同じ問い（どの会社か）に2つの候補一覧を出す理由がありません。
                const tdClient = document.createElement('td');
                const client = document.createElement('input');
                client.type = 'text';
                client.value = o.client_name || '';
                client.setAttribute('aria-label', 'client');
                if ((partners || []).length) client.setAttribute('list', listID);
                tdClient.appendChild(client);
                tr.appendChild(tdClient);

                [o.ordered_at || '（日付なし）', o.destination || ''].forEach(v => {
                    const td = document.createElement('td');
                    td.textContent = v;
                    tr.appendChild(td);
                });
                pickedOrders.push({ page_id: o.page_id, client: client, box: box });
                oTable.appendChild(tr);
            });
            panel.appendChild(oTable);
        }

        const run = document.createElement('button');
        run.type = 'button';
        run.className = 'filing-run';
        run.textContent = '実行';
        run.addEventListener('click', () => runFiling(inputs, pickedOrders, run, panel));
        // 「ほかのファイル」の段（2026-10-08）——それぞれ「＋ …」で動くので、実行ボタンは図面か受注があるときだけ。
        if ((files || []).length) panel.appendChild(buildFilingFiles(files));
        if (rows.length || orders.length) panel.appendChild(run);
        return panel;
    }

    // buildFilingFiles は整理の「📎 ほかのファイル」の段です（2026-10-08・ext/toho/filing_files.go）。
    //
    // 利用者:「図面以外のファイルでも、ここで相手を検索して追加できるとありがたいですね」——仕様書・3Dデータなど、🤖 解析で加工製品
    // ページにならない添付。添付ごとに「行き先を探す」（加工製品ページと装置のページ——`folders=1`）→「＋ …」で、そのページの末尾に
    // ファイル表示を足す（添付はこのメールに置いたまま・指すだけ）。どのページがもう表示しているかは、欄を開いたときに1回だけ探す（重い）。
    function buildFilingFiles(files) {
        const wrap = document.createElement('div');
        wrap.className = 'filing-files';
        const head = document.createElement('p');
        head.className = 'filing-head';
        head.textContent = '📎 ほかのファイル——行き先を探して押すと、そのページにファイル表示を足します（添付はこのメールに置いたまま）。';
        wrap.appendChild(head);
        const shownBoxes = {};
        files.forEach(f => {
            const card = document.createElement('div');
            card.className = 'filing-card filing-file';
            card.dataset.attach = f.id;
            const name = document.createElement('p');
            name.className = 'filing-no';
            name.textContent = '📎 ' + f.name;
            const shown = document.createElement('p');
            shown.className = 'filing-file-shown';
            shown.textContent = '表示しているページ: 調べています…';
            shownBoxes[f.ref] = { box: shown, list: [] };
            const label = document.createElement('label');
            label.className = 'filing-search-label';
            label.textContent = '行き先を探す: ';
            const input = document.createElement('input');
            input.type = 'search';
            input.className = 'filing-search-input';
            input.placeholder = '品番・品名・図面番号・題・装置名・ページ番号';
            label.appendChild(input);
            const out = document.createElement('div');
            out.className = 'filing-candidates filing-search-out';
            const note = document.createElement('p');
            note.className = 'filing-file-note';
            note.hidden = true;
            card.append(name, shown, label, out, note);
            let timer = null;
            const search = async () => {
                const q = input.value.trim();
                out.replaceChildren();
                if (!q) return;
                let hits = [];
                try {
                    const res = await fetch('/api/filing-search?' + new URLSearchParams({ q: q, folders: '1' }));
                    hits = ((await res.json()).results) || [];
                } catch (e) { return; }
                if (input.value.trim() !== q) return; // 打ち替えた——古い答えは出さない
                if (!hits.length) {
                    const p = document.createElement('p');
                    p.className = 'filing-search-none';
                    p.textContent = '見つかりません';
                    out.appendChild(p);
                }
                hits.forEach(h => {
                    const b = document.createElement('button');
                    b.type = 'button';
                    b.className = 'chip-btn filing-file-add';
                    b.textContent = '＋ ' + (h.kind === 'folder' ? '📁 ' : '') + (h.customer ? h.customer + '／' : '') +
                        (h.machine ? h.machine + '／' : '') + h.title + '（/' + h.page_id + '）';
                    b.title = h.kind === 'folder' ? '装置のページの末尾にファイル表示を足します' : '加工製品ページの末尾にファイル表示を足します';
                    b.addEventListener('click', () => addFilingFile(f, h, note, shownBoxes[f.ref]));
                    out.appendChild(b);
                });
            };
            input.addEventListener('input', () => { clearTimeout(timer); timer = setTimeout(search, 300); });
            wrap.appendChild(card);
        });
        (async () => {
            let shown = {};
            try {
                const d = await (await fetch('/api/filing-files-shown?page_id=' + encodeURIComponent(currentPageId))).json();
                shown = d.shown || {};
            } catch (e) { /* 分からなければ空のまま */ }
            Object.entries(shownBoxes).forEach(([ref, s]) => { s.list = shown[ref] || []; renderFilingShown(s); });
        })();
        return wrap;
    }

    // renderFilingShown は「表示しているページ: …」を描きます（題へのリンク）。
    function renderFilingShown(s) {
        s.box.replaceChildren();
        s.box.appendChild(document.createTextNode('表示しているページ: '));
        if (!s.list.length) { s.box.appendChild(document.createTextNode('まだありません')); return; }
        s.list.forEach((p, i) => {
            if (i) s.box.appendChild(document.createTextNode('・'));
            const a = document.createElement('a');
            a.href = '/' + p.page_id;
            a.textContent = p.title || ('/' + p.page_id);
            s.box.appendChild(a);
        });
    }

    // addFilingFile は「＋ …」——選んだページの末尾にファイル表示を足します（同じページに同じ添付は足さない——サーバーが断る）。
    async function addFilingFile(f, h, note, s) {
        note.hidden = false;
        let r;
        try {
            r = await postJSON('/api/filing-attach', { page_id: currentPageId, attach: f.id, target: h.page_id });
        } catch (e) {
            note.className = 'filing-file-note is-warn';
            note.textContent = '⚠ 足せませんでした（通信に失敗しました）';
            return;
        }
        if (!r.ok) {
            note.className = 'filing-file-note is-warn';
            note.textContent = '⚠ ' + ((r.data && r.data.message) || ('足せませんでした（' + r.status + '）'));
            return;
        }
        note.className = 'filing-file-note is-ok';
        note.textContent = '✓ 「' + (r.data.title || h.title) + '」に足しました';
        if (s && !s.list.some(p => p.page_id === r.data.page_id)) {
            s.list.push({ page_id: r.data.page_id, title: r.data.title || h.title });
            renderFilingShown(s);
        }
    }

    // askConfirm は確かめのダイアログを出し、進めるなら true を返します（2026-10-03 利用者:「警告ダイアログが出て、追加するか
    // やめるか選んではどうでしょうか」）。⚠ 既定の釦は「やめる」（Enter で黙って進まない）。Esc も「やめる」。
    function askConfirm(message, yesLabel, noLabel) {
        return openDialog(String(message || '').replace(/\*\*/g, ''), null, [[noLabel, false], [yesLabel, true, 'w-confirm-yes']], false);
    }

    // openDialog は確かめ・選ぶダイアログの土台です（askConfirm・askChoice が写していた——2026-10-09 に寄せた）。
    // 文・中身（middle(done) が作る要素・無ければ省く）・下の帯の釦（[文字, 返す値, class]）を並べ、押した釦の値で終わる
    // Promise を返します。⚠ 既定の釦（focus）は帯の最初の釦（「やめる」）——Enter で黙って進まない。Esc は cancelValue。
    function openDialog(message, middle, buttons, cancelValue) {
        return new Promise((resolve) => {
            const dlg = document.createElement('dialog');
            dlg.className = 'w-confirm';
            const p = document.createElement('p');
            p.className = 'w-confirm-msg';
            p.textContent = message;
            const done = (v) => { if (dlg.open) dlg.close(); dlg.remove(); resolve(v); };
            const bar = document.createElement('div');
            bar.className = 'w-confirm-bar';
            const btns = buttons.map(([label, value, cls]) => {
                const b = document.createElement('button');
                b.type = 'button';
                if (cls) b.className = cls;
                b.textContent = label;
                b.addEventListener('click', () => done(value));
                bar.appendChild(b);
                return b;
            });
            const mid = middle ? middle(done) : null;
            if (mid) dlg.append(p, mid, bar); else dlg.append(p, bar);
            document.body.appendChild(dlg);
            dlg.addEventListener('cancel', (e) => { e.preventDefault(); done(cancelValue); });
            dlg.showModal();
            btns[0].focus();
        });
    }

    // keep は「ほかの行がまだ残っている」——確かめて送り直すときに、済んだ行だけを見て表を閉じないため。
    async function runFiling(inputs, pickedOrders, run, panel, keep) {
        // 「何もしない」の行は送らない（2026-10-03）——解析で作ったページはこのメールの下にそのまま残る。
        const mergeOf = (i) => (Object.keys(i.merge || {}).find(k => i.merge[k].checked)) || '';
        const skipped = inputs.filter(i => mergeOf(i) === 'skip');
        inputs = inputs.filter(i => mergeOf(i) !== 'skip');
        const skipNote = skipped.length ? '「何もしない」の ' + skipped.length + ' 枚は、このメールの下にそのまま残しました。' : '';
        if (!inputs.length && !(pickedOrders || []).some(o => o.box.checked)) {
            notify(skipNote || '対象がありませんでした。', { type: 'success', duration: 0, id: 'filing' });
            if (!keep) panel.remove();
            return;
        }
        run.disabled = true;
        run.textContent = '実行中…';
        const payload = inputs.map(i => ({
            page_id: i.page_id,
            customer: i.fields.customer.value,
            // 区分（2026-09-29）——印の付いたものだけ。空なら通常の製品。
            kinds: (i.kinds || []).filter(b => b.checked).map(b => b.value),
            machine_name: i.fields.machine_name.value,
            drawing_name: i.fields.drawing_name.value,
            confirm_revision: i.box.checked,
            // 装置名のフォルダがある会社で、装置名称を空のまま社名の直下に入れてよいか（2026-10-16・下の確かめのダイアログで聞く）。
            // ⚠ 図面の確かめ（confirm_revision）とは別の印——使い回すと、こちらの「はい」が図面改定の確かめまで飛ばす。
            confirm_no_machine: !!i.noMachineOK,
            // 同じ客先に同じ品番の加工製品ページが既にあるが、別の品物として作る（2026-10-19・下の確かめのダイアログで聞く）。
            // ⚠ これも別の印——図面の確かめ・装置名の確かめとは混ぜない。
            confirm_same_code: !!i.sameCodeOK,
            // 行き先に同じ題のページがあったとき、改定か二つ目の図面か（2026-09-20）。
            // ⚠ **選ばれていなければ空**です——サーバーはそのとき動かしません。
            merge: (Object.keys(i.merge || {}).find(k => i.merge[k].checked)) || '',
            // 重複（取り込まない）の相手（2026-09-30）。
            duplicate_of: i.dupOf ? i.dupOf() : '',
            // 図面追加・図面改定の行き先（同じ題が何枚もあるとき・候補を押したとき・2026-10-01）。空なら題で引く。
            target: i.target ? i.target() : '',
            // 「図面番号を品番に」「図面名称を品名に」（新規のときだけ効く・2026-10-01）。
            part_no_from_drawing: !!(i.rules && i.rules.partNo.checked),
            item_name_from_drawing: !!(i.rules && i.rules.itemName.checked),
        }));
        try {
            const res = await fetch('/api/file-drawings', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    rows: payload,
                    // ⚠ **もとはIDの配列でした**（2026-09-20 に変えた）——発注元が
                    // 直せるようになったので値を運びます（サーバーは `orderRequest`）。
                    orders: (pickedOrders || []).filter(o => o.box.checked)
                        .map(o => ({ page_id: o.page_id, client: o.client.value })),
                }),
            });
            const d = await res.json();
            if (!d.success) throw new Error(d.message || res.status);
            // 何が起きたかを必ず見せる（黙って動かさない）。
            const results = d.results || [];
            const lines = results.map(r => r.message).filter(Boolean);
            if (skipNote) lines.push(skipNote);
            // **確認待ちの行は表を閉じない**——チェックを出して、人が判断してから
            // もう一度実行してもらう（偽の改定を黙って作らないための関門）。
            // ⚠ 装置名称が空の確かめ（confirm === 'no_machine'）は、この「承知のうえで進める」の印を出さない——
            //    あの印は図面の確かめ（confirm_revision）のもので、押しても装置名の確かめにはならない。ダイアログだけで聞く。
            //    同じ品番の確かめ（confirm === 'same_code'・2026-10-19）も同じ——ダイアログだけで聞く。
            const ownDialog = (r) => r.confirm === 'no_machine' || r.confirm === 'same_code';
            const pending = new Set(results
                .filter(r => r.outcome === 'needs_confirm' && !ownDialog(r)).map(r => r.page_id));
            const noMachine = results.filter(r => r.outcome === 'needs_confirm' && ownDialog(r)).length;
            inputs.forEach(i => { i.confirm.hidden = !pending.has(i.page_id); });
            // **選択待ちの行も表を閉じません**（2026-09-20）。打ち替えのたびの
            // 問い合わせで既に出ているはずですが、⚠ **聞けなかったとき**（通信が
            // 切れた・打った直後に押した）はここが最後の関門です。
            const choosing = new Set(results
                .filter(r => r.outcome === 'needs_choice').map(r => r.page_id));
            inputs.forEach(i => { if (choosing.has(i.page_id)) i.choice.hidden = false; });
            const stay = pending.size + choosing.size + noMachine;
            notify(lines.join('\n') || '対象がありませんでした。',
                { type: stay ? 'warn' : 'success', duration: 0, id: 'filing' });
            // **確かめが要る行は、1行ずつダイアログで聞く**（2026-10-03）——進めるなら印を付けてその行だけ送り直す。
            // やめた行は表に残る（「承知のうえで進める」のチェックも出ている——あとで選び直せる）。
            const again = [];
            for (const r of results.filter(x => x.outcome === 'needs_confirm')) {
                const i = inputs.find(x => x.page_id === r.page_id);
                if (!i) continue;
                // 装置名称が空の確かめ（2026-10-16 利用者:「装置名のフォルダがあるのに、装置名を空にしている場合は、警告して
                // 保存の可否を問い合わせてください」）——「はい」なら、その印だけを付けて送り直す。
                if (r.confirm === 'no_machine') {
                    if (await askConfirm(r.message, '社名の直下に入れる', 'やめる')) {
                        i.noMachineOK = true;
                        again.push(i);
                    }
                    continue;
                }
                // 同じ客先に同じ品番の加工製品ページが既にある（2026-10-19 利用者:「とすると、二つの加工製品ページが同じ品番を持つのはマズいので、
                // 加工製品ページを作る前にチェックし、編集者に選択させる必要があります」）——「作る」ならその印だけを付けて送り直す。
                // やめたら表に残る（整理の欄で図面追加・図面改定・重複〔取り込まない〕を選び直す）。
                if (r.confirm === 'same_code') {
                    if (await askConfirm(r.message, '別の品物として作る', 'やめる')) {
                        i.sameCodeOK = true;
                        again.push(i);
                    }
                    continue;
                }
                const m = (Object.keys(i.merge || {}).find(k => i.merge[k].checked)) || '';
                const yes = m === 'drawing' ? '追加する' : m === 'revision' ? '改定する' : m === 'folder' ? '足す' : '進める';
                if (await askConfirm(r.message, yes, 'やめる')) {
                    i.box.checked = true;
                    again.push(i);
                }
            }
            if (again.length) {
                run.disabled = false;
                run.textContent = '実行';
                await runFiling(again, [], run, panel, keep || stay > again.length);
                return;
            }
            if (!stay && !keep) panel.remove();
        } catch (e) {
            notify('整理を実行できませんでした: ' + e, { type: 'alert', duration: 0, id: 'filing' });
        }
        run.disabled = false;
        run.textContent = '実行';
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
    // 出し分けが要るのは「JSが自分で作るクローム」だけ——`refreshMailChrome` と
    // 電話の「記録を作る」がそれです。
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
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.id = 'w-mail-fetch';
        btn.textContent = '📥 新しいメールを読み込む';
        btn.title = '受信箱と送信済みの箱から、まだ通信箱に入っていないメールを取り込みます';
        memo.insertAdjacentElement('afterend', btn);
        btn.addEventListener('click', () => fetchNewMail(btn));
        wireMailSignIn(btn);
        resumeMailFetch(btn);
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
        const btn = document.createElement('button');
        btn.type = 'button';
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
        const code = document.createElement('strong');
        code.className = 'mail-signin-code';
        code.textContent = d.user_code;
        box.append('① ', a, '　② 番号 ', code, ' を入れて、メールのアカウントでサインイン（このページはそのまま待ちます）');
        // 済むのを待つ（番号の期限まで・3秒おき）。
        const until = Date.now() + (Number(d.expires_in) || 900) * 1000;
        while (Date.now() < until) {
            await new Promise((r) => setTimeout(r, 3000));
            let st;
            try {
                st = await (await fetch('/api/mail/status', { headers: BG_HEADERS })).json(); // 待つあいだの見張り——操作ではない
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
                st = ((await (await fetch('/api/mail/fetch-new/status', { headers: BG_HEADERS })).json()) || {}).status;
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

    async function createRecord(btn) {
        const sel = document.getElementById('w-memo-channel');
        const dir = document.getElementById('w-memo-direction');
        const title = document.getElementById('w-memo-title');
        if (!sel) return;
        btn.disabled = true;
        try {
            const res = await fetch('/api/intake/memo', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({
                    channel: sel.value,
                    direction: (dir && !dir.disabled) ? dir.value : '',
                    title: title ? title.value : '',
                }),
            });
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

    // ── 未登録の連絡先を相手ページにする（2026-09-05）─────────────────────
    //
    // ユーザー:「アドレス帳のようなものを作って、メールから人物や電話番号、
    // メールアドレスを収集しましょう」。集めるのは取り込みが既にやっているので、
    // ここは**人が名前を直して、どの取引かを押す**だけです。
    //
    // **顧客と仕入先は両方ありえます**（「顧客であり、仕入れ先である場合もあります」）
    // ——押した順に `取引` のタグが増えるので、あとから足せます。
    function wireContactRegister() {
        const host = document.getElementById('w-editor-content');
        if (!host) return;
        const editMode = false; // 書ける形でも押せる（2026-10-12・問い5）
        // **行の道具は2つの欄＋「登録」**（2026-09-17）。組織の欄が候補（datalist）の
        // どれかに一致すれば既にある組織へ、無ければ新しい組織を作ります。
        host.querySelectorAll('.contact-form').forEach(td => {
            td.querySelectorAll('button, input').forEach(el => { el.disabled = editMode; });
            if (td.dataset.wired) return;
            td.dataset.wired = '1';
            const go = td.querySelector('.contact-go');
            if (go) go.addEventListener('click', () => submitContactRow(td));
        });
    }

    // contactOrgCandidate は、組織の欄の文字が候補（datalist）に一致すればその候補を返します。
    // **完全一致だけ**——揺れを画面が吸収すると、別の会社へ足すことになります。
    function contactOrgCandidate(td) {
        const org = td.querySelector('.contact-org');
        const text = org ? org.value.trim() : '';
        const list = org ? org.list : null;
        if (!text || !list) return null;
        for (const opt of list.options) {
            if (opt.value === text) return { id: opt.dataset.id || '', title: opt.value };
        }
        return null;
    }

    // submitContactRow は「登録」——1つの口（/api/contacts/register）へ、組織と担当者を渡します。
    // 組織が候補に一致すれば page_id、無ければ name（サーバーが同じ題の組織を探して寄せる）。
    async function submitContactRow(td) {
        const row = td.closest('tr');
        const orgEl = td.querySelector('.contact-org');
        const personEl = td.querySelector('.contact-person');
        const go = td.querySelector('.contact-go');
        const org = orgEl ? orgEl.value.trim() : '';
        const person = personEl ? personEl.value.trim() : '';
        if (!org) {
            notify('組織を入れてください（社名か「' + (td.dataset.personalTitle || '個人') + '」）', { type: 'warn' });
            if (orgEl) orgEl.focus();
            return;
        }
        const cand = contactOrgCandidate(td);
        const body = {
            addresses: (go.dataset.addresses || '').split(',').filter(Boolean),
            person_name: person,
        };
        if (cand && cand.id) body.page_id = cand.id; else body.name = org;
        go.disabled = true;
        try {
            const res = await fetch('/api/contacts/register', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify(body),
            });
            const data = await res.json();
            if (!data.success) {
                notify(data.message || '登録できませんでした', { type: 'warn' });
                return;
            }
            // 行を消すのは見た目だけ。正本は相手ページのタグで、次に開けば描き直される。
            if (row) row.remove();
            notify(data.created
                ? '「' + data.title + '」を登録しました'
                : '「' + data.title + '」へ' + (data.added || 0) + '件足しました',
                { type: 'success', duration: 6000 });
        } catch (e) {
            notify('登録できませんでした: ' + e.message, { type: 'warn' });
        } finally {
            go.disabled = false;
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
            const res = await fetch('/api/intake/handled', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ page_ids: pageIds, value: value || '済' }),
            });
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

    // analyzeOne は添付1つを解析します（「🤖 解析」と「まとめて解析」が共有する・2026-09-30 に切り出した）。
    // 返すのは {kind: 'drawing'|'order'|'none', made: [{page_id, title}], matchedDxf} か {error}。
    async function analyzeOne(pageId, file) {
        try {
            const res = await fetch('/api/analyze-attachment', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ page_id: pageId, file: file }),
            });
            const d = await res.json().catch(() => ({}));
            if (!res.ok || !d.success) return { error: d.message || String(res.status) };
            if (d.doc_type === 'drawing' && ((d.pages || []).length || d.page_id)) {
                // ⚠ **1つのPDFに図面が複数入っていることがあります**（2026-09-20）——1枚につき1ページ。
                let made = d.pages || [];
                if (!made.length && d.page_id) made = [{ page_id: d.page_id, title: d.title }];
                return { kind: 'drawing', made: made, matchedDxf: d.matched_dxf || 0 };
            }
            // 部材見積依頼の返事（2026-10-05・ext/toho/rfq_reply_page.go）——返事ページを作り、どの部材見積依頼書への返事かを突き合わせた。
            if (d.doc_type === 'rfq_reply' && d.page_id) {
                return { kind: 'rfq_reply', made: [{ page_id: d.page_id, title: d.title }], say: d.say || '', linked: d.linked_rfq || '' };
            }
            if (!d.is_client_order) return { kind: 'none', made: [] };
            // 1つのPDFに発注書が何枚も入っていれば1枚につき1ページ（2026-09-30・`pages`）。
            return { kind: 'order', made: (d.pages || []).length ? d.pages : [{ page_id: d.page_id, title: d.title }] };
        } catch (e) {
            return { error: String(e) };
        }
    }

    // makeZipAnalyzeButton は ZIP の隣の「🤖 中のPDFをまとめて解析」です（2026-09-30 利用者:「添付ファイルのPDFに
    // ついて、ZIPの場合まとめて解析するオプションが欲しいです」）。中のPDFを**1つずつ順に**「🤖 解析」と同じ口で
    // 解析し、最後に1つの知らせにまとめる。⚠ **解析済みのものは飛ばす**（押し直しで同じページを二重に作らない——
    // 読み違いの解析し直しは1件ずつの「🤖 再解析」で）。途中でページを離れると止まるが、押し直せば残りから続く。
    // 解析済みしか無ければ出さない。
    function makeZipAnalyzeButton(pageId, pdfs) {
        const todo = pdfs.filter(p => !analyzedMap[p.id]);
        if (!todo.length) return null;
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'vocab-chrome attach-expand attach-analyze attach-analyze-zip';
        const skipped = pdfs.length - todo.length;
        const label = '🤖 中のPDFをまとめて解析（' + todo.length + '件）';
        btn.textContent = label;
        btn.title = '中のPDFを1つずつ順に解析します（Gemini を ' + todo.length + '回呼びます・1件に数十秒）' +
            (skipped ? '。解析済みの ' + skipped + '件は飛ばします' : '');
        btn.addEventListener('click', async () => {
            if (!confirm('中のPDF ' + todo.length + '件を順に解析します（Gemini を ' + todo.length +
                '回呼びます・1件に数十秒）。' + (skipped ? '解析済みの ' + skipped + '件は飛ばします。' : '') +
                '途中でページを離れると止まります（押し直せば残りから）。')) return;
            btn.disabled = true;
            let drawings = 0, orders = 0, replies = 0;
            const none = [], failed = [];
            for (let i = 0; i < todo.length; i++) {
                btn.textContent = '🤖 解析中… ' + (i + 1) + '/' + todo.length;
                const r = await analyzeOne(pageId, todo[i].file);
                if (r.error) failed.push(todo[i].path + '（' + r.error + '）');
                else if (r.kind === 'drawing') drawings += r.made.length;
                else if (r.kind === 'order') orders += r.made.length;
                else if (r.kind === 'rfq_reply') replies += r.made.length;
                else none.push(todo[i].path);
            }
            const lines = ['まとめて解析しました（' + todo.length + '件）: 加工製品ページ ' + drawings + '枚' +
                (orders ? '・案件ページ ' + orders + '枚' : '') + (replies ? '・部材見積依頼の返事ページ ' + replies + '枚' : '') + '。'];
            if (none.length) lines.push('発注書・図面・部材見積依頼の返事のどれでもない（ページは作っていない）: ' + none.join('・') + '\n' + BY_HAND_REPLY_HINT);
            if (failed.length) lines.push('⚠ 解析できなかった: ' + failed.join('・'));
            if (drawings) lines.push('📁 整理で行き先を決めてください。');
            notify(lines.join('\n'), { type: failed.length ? 'warn' : 'success', duration: 0, id: 'analyze-pdf' });
            btn.disabled = false;
            btn.textContent = label;
            if (drawings || orders || replies) {
                await reloadContent(); // 本文の鏡と、解析済みの印（applyMode 経由）
                loadChildNav();        // 左レールの子ページ一覧
            }
        });
        return btn;
    }

    // makeAnalyzeButton は「🤖 解析」ボタンを作ります（PDF添付）。
    // ⚠ ZIP の中の PDF には出しません（2026-09-17）——取り込みが ZIP を展開して中身を
    // 1つずつ添付にするので、解析はページ直下の PDF だけを見ます（`entry` 引数は廃止）。
    // 押すと /api/analyze-attachment が Gemini で判定し、発注書なら受注ページを
    // このページの子として作る。結果は通知で知らせる（本文は変えない）。
    function makeAnalyzeButton(pageId, file) {
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'vocab-chrome attach-expand attach-analyze';
        btn.textContent = '🤖 解析';
        btn.addEventListener('click', async () => {
            btn.disabled = true;
            btn.textContent = '🤖 解析中…';
            // **ページが生まれたか**。生まれたときだけ画面を取り直します（下）。
            let born = false;
            const d = await analyzeOne(pageId, file);
            if (d.error) {
                notify('解析できませんでした: ' + d.error, { type: 'alert', duration: 0, id: 'analyze-pdf' });
            } else if (d.kind === 'drawing') {
                // 図面と判定された枝。同じページのDXFと図面番号で突き合わせた
                // 結果も知らせる（0件も普通——PDFだけの図面はよくある）。
                //
                // ⚠ **1つのPDFに図面が複数入っていることがあります**（2026-09-20）。
                // 1枚につき1ページ作るので、**できた全部を見せます**——`page_id`
                // （1枚目）だけ見せていると、3枚できても画面には1枚しか出ず、
                // **押した直後の手応えが嘘になります**（子ページ一覧には出るので
                // 消えはしませんが、気づくのは後からです）。
                var made = d.made;
                var msg = made.length > 1
                    ? '加工製品ページを ' + made.length + '枚 作りました（このPDFに図面が'
                      + made.length + '枚入っていました）:\n'
                    : '加工製品ページを作りました: ';
                msg += made.map(function (p) {
                    return (p.title || p.page_id) + '（/' + p.page_id + '）';
                }).join('\n');
                if (made.length > 1) {
                    msg += '\n同じ品物の別図面（部品図と溶接図など）なら、📁 整理で'
                        + '「二つ目の図面として追加」を選ぶと1ページにまとめられます。';
                }
                if (d.matchedDxf > 0) {
                    msg += '\n図面番号の一致したDXF ' + d.matchedDxf + '件と結びました。';
                }
                notify(msg, { type: 'success', duration: 0, id: 'analyze-pdf' });
                born = true;
            } else if (d.kind === 'rfq_reply') {
                const p = d.made[0];
                notify('部材見積依頼の返事ページを作りました: ' + (p.title || p.page_id) + '（/' + p.page_id + '）\n' + d.say,
                    { type: d.linked ? 'success' : 'warn', duration: 0, id: 'analyze-pdf' });
                born = true;
            } else if (d.kind === 'none') {
                notify('発注書・図面・部材見積依頼の返事のどれでもないと判定されました（ページは作っていません）。\n' + BY_HAND_REPLY_HINT, { type: 'warn', duration: 0 });
            } else {
                const list = d.made.map(o => (o.title || o.page_id) + '（/' + o.page_id + '）').join('\n');
                notify((d.made.length > 1
                    ? '案件ページを ' + d.made.length + '枚 作りました（このPDFに発注書が' + d.made.length + '枚入っていました）:\n'
                    : '案件ページを作りました: ') + list, { type: 'success', duration: 0, id: 'analyze-pdf' });
                born = true;
            }
            btn.disabled = false;
            btn.textContent = '🤖 解析';

            // **生まれた子ページを画面へ出す**（2026-09-14 ユーザー:「解析終了後に、
            // 加工製品ページが子ページになりますが、ページに反映されない」）。
            //
            // 古くなるのは**2か所**です——左レールの子ページ一覧と、本文の
            // 「子ページ一覧の鏡」（`<section><h2>子ページ一覧</h2></section>`・サーバーが
            // 中身を埋める計算ビュー）。通知だけでは、押した人は自分で読み込み直す
            // ことになります。
            //
            // **丸ごとの再読み込みにはしません**——`reloadContent()` は本文を
            // 取り直すだけなので、通知・巻き上げ位置・開いているPDFの外側は残ります。
            // 書きかけを失う心配も要りません: 📎 の「🤖 解析」は閲覧モードにしか出ません
            // （`refreshAttachmentPreviews` が書ける形で早々に戻ります）。「⋯」のメニューの解析
            // （runAnalyzeFile）は書ける形でも押せますが、書ける形の `reloadContent()` は書きかけを先に
            // 保存して、変わったブロックだけ受け取ります（2026-10-12 から）。
            if (born) {
                await reloadContent(); // 本文の鏡と、解析済みの印（applyMode 経由）
                loadChildNav();        // 左レールの子ページ一覧
            }
        });
        return btn;
    }

    // ── サーバーが描いた「ファイル表示」の枠に、大きさの記憶を配線する ──────
    //
    // **枠を作るのはサーバーです**（`internal/cms/file_view.go`・
    // `section[data-type="file-view"]` の鏡型）。ここがするのは、右下をつまんで
    // 大きさを変えられるようにすることだけ——高さは**この端末の持ち物**で、
    // 本文にもサーバーにも残さないためです。
    //
    // ⚠ **2026-09-14 にサーバー側へ移しました。** それまではここが参照リンクから
    // `<embed>` を組み立てていましたが、2つ困りごとがありました:
    //
    //  1. **`a.ref-link` はページ描画のときしか作られません**（`RenderReferenceLinks`
    //     は `/api/load` を通らない）。だから編集モードを出入りしたり
    //     `reloadContent()` が走ったりすると、**図面が黙って消えていました**。
    //     計算ビューの鏡は `/api/load` でも走るので、この穴ごと塞がります。
    //  2. **JSが要りました**——公開ページ（匿名の閲覧）では出ませんでした。
    //
    // ユーザー:「PDFを表示するタグが必要に思います」「表示するという意図を伝える
    // 名前が良いと思います」（2026-09-14）。
    function refreshDrawingPreviews() {
        document.querySelectorAll('#w-editor-content .file-view').forEach(wrap => {
            // **同じ枠へ二度配線しません**——`applyMode()` はモードを切り替えるたびに
            // 走りますが、サーバーが描いた枠はDOMに残ったままなので、印を付けないと
            // つまむたびに聞き手が増えます（作り直していた頃は起きませんでした）。
            if (wrap.dataset.wResize === '1') return;
            const body = wrap.querySelector('.file-view-body');
            if (!body) return;
            wrap.dataset.wResize = '1';
            wireDrawingResize(wrap, body);
        });
    }

    // 図面枠の大きさは**見る人が決めます**（2026-09-10 ユーザー:「PDFインライン表示の
    // 大きさを変えられるように出来ますか？」）。枠の右下をつまんで縦に伸ばすと、
    // **次に開いたときも同じ高さ**です。
    //
    // 置き場は**枠ごとの縦横比**（`wcms.view`・この端末のブラウザ・2026-09-28〜）。09-29 までは `wcms.ui` の
    // `drawing.height`（全部の枠で1つの高さ）にも書いていて、1枚を変えるとほかの PDF まで変わった——いまは使わない。
    // **本文にもサーバーにも残しません**——枠はクロームで、A3の図面を大きく見たいか
    // 一覧しやすく小さくしたいかは**見る人と画面の都合**だからです。他の人の画面を
    // 動かしてしまわない、という意味でもあります。
    const DRAWING_H_MIN = 200;   // これ以下だと図面が判別できない
    const DRAWING_H_MAX = 4000;  // 壊れた値でページを埋めない柵
    const DRAWING_GRIP = 24;     // 右下の「つまみ」とみなす範囲（px）

    // ── 「📝 ローカル編集」（2026-09-29） ───────────────────────────────
    //
    // 利用者:「実運用するには、ローカルアプリで添付ファイルを編集して保存できる必要があります」。ファイル表示の頭の
    // 行に置く。押すと w-cms がそのファイル1つに限る鍵を出し（POST /api/local-edit/start）、`w-cms-edit:` のリンクで
    // 各 PC の常駐ヘルパー（cmd/w-cms-edit）がファイルを落として既定のアプリで開き、**保存（Ctrl+S）するたびに**
    // w-cms へ上げる（前の中身は版に残る）。⚠ WebDAV は Ctrl+S をアプリを閉じるまで送らなかった（2026-09-18）
    // ——だからこの形。書けない所（通信箱の下・権限が無い）は読み取り専用で開く（確かめてから）。
    function decorateLocalEdit() {
        if (document.body.classList.contains('anonymous')) return;
        document.querySelectorAll('#w-editor-content section[data-type="file-view"][data-ref]').forEach(sec => {
            const head = sec.querySelector('.file-view-head');
            const data = sec.querySelector('[data-file-url]');
            if (!head || !data || head.querySelector('.file-menu-btn')) return;
            // 「⋯」1つ（2026-10-01）——押すと形式に合った操作のメニュー（🔗 ID・⬇ 保存・📝 ローカル編集・🤖 解析…・openFileMenu）。
            // それまでは「🔗 ID」と「📝 ローカル編集」を全部の形式に並べていた。頭の行（summary）の中なので、押しても畳まない。
            const btn = document.createElement('button');
            btn.type = 'button';
            btn.className = 'vocab-chrome file-menu-btn';
            btn.contentEditable = 'false';
            btn.textContent = '⋯';
            btn.title = 'このファイルの操作（ID を写す・保存・ローカル編集…）';
            btn.addEventListener('mousedown', e => e.preventDefault());
            btn.addEventListener('click', e => {
                e.preventDefault();
                e.stopPropagation();
                const url = data.getAttribute('data-file-url') || '';
                openFileMenu(btn, { ref: sec.getAttribute('data-ref'), url, name: data.getAttribute('data-file-name') || '',
                    kind: fileKindOf(url), host: sec });
            });
            head.appendChild(btn);
        });
    }

    // ── ファイルの「⋯」——形式に合った操作のメニュー（2026-10-01） ──────────────
    //
    // 利用者:「ドロップやファイルダイアログで追加された画像やファイル等は形式を判定して、ファイル形式にあったボタンなり
    // クリックメニューを搭載してはどうでしょう？」→（出し方を聞いて）「⋯」1つにまとめる・🤖 解析はメールのページだけ。
    // ファイル表示の頭と、写真（マウスを載せたとき）に出す。項目は形式で決まる（fileMenuItems）。
    //
    //   どれも     … 🔗 ID を写す・⬇ 保存（届いたときの名前で）
    //   PDF        … ＋📝 ローカル編集・🤖 解析（メールのページだけ——作るページはそのページの子に生まれ、整理で置き場へ）
    //   画像       … ＋🔍 原寸で開く・📝 ローカル編集
    //   Excel      … ＋📝 Excel で開く・🤖 解析（同上）
    //   CAD        … ＋📝 CAD で開く
    //   ZIP        … ＋🗂 中身を見る・🤖 中のPDFをまとめて解析（同上）
    //   動画・音声 … 共通の2つだけ
    //   ほか       … ＋📝 ローカル編集
    const FILE_KIND_BY_EXT = {
        pdf: 'pdf', xlsx: 'xlsx', zip: 'zip',
        jpg: 'image', jpeg: 'image', png: 'image', gif: 'image', webp: 'image', svg: 'image', bmp: 'image',
        dxf: 'cad', step: 'cad', stp: 'cad', rpcd: 'cad', dwg: 'cad', igs: 'cad', iges: 'cad', x_t: 'cad', slddrw: 'cad',
        mp4: 'media', webm: 'media', mov: 'media', mp3: 'media', wav: 'media', m4a: 'media', ogg: 'media',
    };
    function fileKindOf(url) {
        const m = /\.([0-9a-z_]+)$/i.exec(String(url || '').split(/[?#]/)[0]);
        return (m && FILE_KIND_BY_EXT[m[1].toLowerCase()]) || 'other';
    }

    // analyzableHere は、そのファイルを 🤖 解析してよい所か——メールのページで、ファイルがそのページの添付のとき。
    function analyzableHere(pageOfFile) {
        return hasExtension('toho') && !!tagValue(CHANNEL_TAG) && pageOfFile === currentPageId;
    }

    function fileMenuItems(info) {
        const ref = info.ref || '';
        const pid = ref.split('-')[0];
        const aid = ref.split('-')[1] || '';
        const file = String(info.url || '').split('/').pop();
        const items = [];
        items.push({ icon: '🔗', label: 'ID を写す（' + ref + '）', title: copyRefTitle(ref), run: () => copyFileRef(ref) });
        items.push({ icon: '⬇', label: '保存', title: '届いたときの名前で保存します', run: () => downloadFile(info.url, info.name) });
        switch (info.kind) {
        case 'pdf':
            if (analyzableHere(pid)) items.push({ icon: '🤖', label: '解析（発注書・図面を読む）', run: () => runAnalyzeFile(pid, file) });
            items.push({ icon: '📝', label: 'ローカル編集', run: b => startLocalEdit(ref, b) });
            break;
        case 'xlsx':
            if (analyzableHere(pid)) items.push({ icon: '🤖', label: '解析（注文リストを読む）', run: () => runAnalyzeFile(pid, file) });
            items.push({ icon: '📝', label: 'Excel で開く', run: b => startLocalEdit(ref, b) });
            break;
        case 'image':
            items.push({ icon: '🔍', label: '原寸で開く', run: () => window.open(info.url, '_blank', 'noopener') });
            items.push({ icon: '📝', label: 'ローカル編集', run: b => startLocalEdit(ref, b) });
            break;
        case 'cad':
            items.push({ icon: '📝', label: 'CAD で開く', run: b => startLocalEdit(ref, b) });
            break;
        case 'zip':
            items.push({ icon: '🗂', label: '中身を見る', run: () => toggleZipListIn(info.host, pid, file) });
            if (analyzableHere(pid)) items.push({ icon: '🤖', label: '中のPDFをまとめて解析', run: () => runZipAnalyze(pid, aid) });
            break;
        case 'media':
            break;
        default:
            items.push({ icon: '📝', label: 'ローカル編集', run: b => startLocalEdit(ref, b) });
        }
        // 図面が2つ以上並んだページの図面のファイル（2026-10-04）——あとから改定にする（supersedeDrawingFrom）。
        if (canSupersedeFrom(info.host)) {
            items.push({ icon: '📐', label: 'この図面で改定する（前の図面を旧版へ）',
                title: 'この図面を最新の図面にして、選んだ前の図面を旧版の子ページへ移します', run: () => supersedeDrawingFrom(info.host) });
        }
        return items;
    }

    // ── あとから改定にする（2026-10-04） ──────────────────────────────
    //
    // 利用者:「加工製品に図面を追加しました。するともともとあった図面と変更が見つかりました。そこで追加した図面を
    // 改定図面とし、元々あった図面を古い図面として子ページにしたいのです」。整理の「図面追加」で並べた図面を、あとから
    // 整理の「図面改定」と同じ形へ組み替える（POST /api/drawing/supersede・ext/toho/drawing_supersede.go）。
    // 押した図面のファイルの図面が新しい図面。古い図面は選ぶ（2つなら残りの1つ）。両方の番号と名前を出して確かめる。
    // 書ける形でも押せる（2026-10-12 から・canSupersedeFrom。押す前に書きかけを保存する——postJSON）。それまでは閲覧モードだけ
    // だった（編集中はサーバーが断った——自分が開いていても）。いまサーバーは cms.RewriteBody で書き、断るのは道具が
    // /api/lock で握っているときだけ。

    // drawingSections は本文の最上位の図面ブロック（見出し「図面」の h2 を持つ節）を本文の順に返します——サーバーの
    // `drawingSectionsOf` と同じ数え方（何番目かで指すので、数え方が食い違うと別の図面を旧版へ送る——番号の確かめが止める）。
    // ⚠ 閲覧の画面では節がブロックの枠（div.block-content）に包まれている——`#w-editor-content > section` では拾えない。
    function drawingSections() {
        const content = document.getElementById('w-editor-content');
        if (!content) return [];
        return Array.from(content.querySelectorAll('section')).filter(s =>
            !(s.parentElement && s.parentElement.closest('#w-editor-content section')) &&
            Array.from(s.querySelectorAll('h2')).some(h => h.textContent.trim() === '図面'));
    }

    function canSupersedeFrom(host) {
        // 書ける形でも押せる（2026-10-12・問い5）。
        if (!host || !hasExtension('toho')) return false;
        const all = drawingSections();
        return all.length >= 2 && all.some(s => s.contains(host));
    }

    // drawingTag は図面ブロックのタグの値です（dt/dd から）。
    function drawingTag(sec, name) {
        const dt = Array.from(sec.querySelectorAll(':scope > dl[data-type="tags"] > dt')).find(d => d.textContent.trim() === name);
        const dd = dt && dt.nextElementSibling;
        return dd ? dd.textContent.trim() : '';
    }

    // drawingLabel は図面ブロックの「図面番号 名称」です。
    function drawingLabel(sec) {
        return [drawingTag(sec, '図面番号'), drawingTag(sec, '図面名称')].filter(Boolean).join(' ') || '（番号の無い図面）';
    }

    // askChoice は選ぶダイアログを出し、選んだ番号を返します（やめたら -1）。⚠ 既定は「やめる」・Esc も「やめる」。
    function askChoice(message, labels) {
        return openDialog(message, (done) => {
            const list = document.createElement('div');
            list.className = 'w-choice-list';
            labels.forEach((label, i) => {
                const b = document.createElement('button');
                b.type = 'button';
                b.textContent = label;
                b.addEventListener('click', () => done(i));
                list.appendChild(b);
            });
            return list;
        }, [['やめる', -1]], -1);
    }

    async function supersedeDrawingFrom(host) {
        const all = drawingSections();
        const mine = all.find(s => s.contains(host));
        const others = all.filter(s => s !== mine);
        if (!mine || !others.length) return;
        let old = others[0];
        if (others.length > 1) {
            const i = await askChoice('「' + drawingLabel(mine) + '」で改定する前の図面はどれですか？（選んだ図面を旧版の子ページへ移します）',
                others.map(drawingLabel));
            if (i < 0) return;
            old = others[i];
        }
        const ok = await askConfirm('「' + drawingLabel(old) + '」を旧版として子ページへ移し、\n「' + drawingLabel(mine) +
            '」をこのページの最新の図面にします（改訂明細表に1行足します）。', '旧版へ移す', 'やめる');
        if (!ok) return;
        const pick = (s) => ({ index: all.indexOf(s), no: drawingTag(s, '図面番号') });
        const r = await postJSON('/api/drawing/supersede', { page_id: currentPageId, new: pick(mine), old: pick(old) });
        if (!r.ok) {
            notify((r.data && r.data.message) || ('旧版へ移せません（' + r.status + '）'), { type: 'warn', duration: 0 });
            return;
        }
        notify('前の図面を「' + (r.data.title || '旧版') + '」へ移しました。', { type: 'success' });
        await reloadContent();
        loadChildNav();
    }

    // openFileMenu は「⋯」の下にメニューを開きます（もう一度押すか、外を押すか、Esc で閉じる）。
    function openFileMenu(anchor, info) {
        const menu = floatingMenu('w-file-menu', 'file-menu', closeFileMenu);
        if (menu.classList.contains('active') && menu.wcmsAnchor === anchor) { closeFileMenu(); return; }
        menu.textContent = '';
        menu.wcmsAnchor = anchor;
        fillMenuItems(menu, fileMenuItems(info), (it) => { closeFileMenu(); it.run(anchor); });
        menu.classList.add('active');
        placeFloating(menu, anchor.getBoundingClientRect(), 4);
    }

    function closeFileMenu() {
        const menu = document.getElementById('w-file-menu');
        if (menu) { menu.classList.remove('active'); menu.wcmsAnchor = null; }
    }

    // downloadFile は添付を届いたときの名前で保存します（同じ場所のリンクに download を付けて押す）。
    function downloadFile(url, name) {
        if (!url) return;
        const a = document.createElement('a');
        a.href = url;
        a.download = name || '';
        document.body.appendChild(a);
        a.click();
        a.remove();
    }

    // runAnalyzeFile は添付1つを 🤖 解析します（📎 の「🤖 解析」と同じ口と知らせ——makeAnalyzeButton）。
    function runAnalyzeFile(pageId, file) {
        notify('🤖 解析しています…', { type: 'info', duration: 0, id: 'analyze-pdf' });
        makeAnalyzeButton(pageId, file).click();
    }

    // runZipAnalyze は ZIP の中のPDFをまとめて解析します（どの PDF が中にあるかは /api/analyzed が知っている）。
    async function runZipAnalyze(pageId, zipID) {
        try {
            const d = await (await fetch('/api/analyzed?page_id=' + encodeURIComponent(pageId))).json();
            analyzedMap = (d && d.analyzed) || analyzedMap;
            const pdfs = ((d && d.zip_pdfs) || {})[zipID] || [];
            const btn = pdfs.length ? makeZipAnalyzeButton(pageId, pdfs) : null;
            if (!btn) { notify(pdfs.length ? '中のPDFは解析済みです。' : 'この ZIP の中に PDF はありません。', { duration: 6000 }); return; }
            btn.click();
        } catch (e) {
            notify('まとめて解析できません: ' + e.message, { type: 'warn' });
        }
    }

    // toggleZipListIn はファイル表示の下に ZIP の中身の一覧を開け閉めします。
    function toggleZipListIn(host, pageId, file) {
        if (!host) return;
        const open = host.querySelector('.attach-preview');
        if (open) { open.remove(); return; }
        const wrap = document.createElement('div');
        wrap.className = 'vocab-chrome attach-preview';
        wrap.contentEditable = 'false';
        wrap.textContent = '読み込み中…';
        (host.querySelector(':scope > .vocab-chrome') || host).appendChild(wrap);
        loadZipListInto(wrap, pageId, file);
    }

    async function startLocalEdit(ref, btn) {
        btn.disabled = true;
        try {
            const res = await fetch('/api/local-edit/start', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ ref }),
            });
            const d = await res.json().catch(() => ({}));
            if (!res.ok || !d.success) {
                notify(d.message || ('ローカル編集できません（' + res.status + '）'), { type: 'warn' });
                return;
            }
            if (!d.writable && !confirm('「' + d.name + '」は書き換えられません。読み取り専用で開きますか？')) return;
            location.href = d.link; // 各 PC の w-cms-edit が受ける（入っていなければ何も起きない）
            // ⚠ 開かないとき（w-cms-edit が入っていない）の一言だけは残す——黙ると「押しても何も起きない」になる。
            notify(d.writable
                ? '📝 「' + d.name + '」を開きます。保存すると w-cms に入ります（開かないときは w-cms-edit が要ります）'
                : '「' + d.name + '」を読み取り専用で開きます', { duration: 8000 });
        } catch (e) {
            notify('ローカル編集できません: ' + e.message, { type: 'warn' });
        } finally {
            btn.disabled = false;
        }
    }

    // ── 写真も「📝 ローカル編集」（2026-09-30） ────────────────────────────
    //
    // 利用者:「写真などもワンクリックでローカルアプリで編集できてよいと思います」「イメージと添付ファイルは
    // 扱いが違って当然ですね」——写真は本文の中の絵（`<img>`）のままにして、**マウスを載せたときだけ**隅に
    // 📝 を1つ浮かべる（本文の外に置く1つの札・閲覧でも編集でも）。住所 `/<6桁>/<添付ID>.<拡張子>` の添付ID が
    // そのままファイル表示の参照（`ページ番号-添付ID`）なので、同じ口（startLocalEdit）で開く。
    // ⚠ ファイル表示の中の絵（頭の行に 📝 がある）とタッチ端末（載せる操作が無い）には出さない。
    const IMG_ATTACH_SRC = /^\/(\d{6})\/([0-9a-z]+)\.[0-9a-z]+$/i;
    let imgEditTarget = null;
    let imgEditHideTimer = 0;

    // 写真の「⋯」（2026-10-01）——マウスを載せると右上に1つ浮かべ、押すと形式に合った操作のメニュー（🔗 ID・⬇ 保存・
    // 🔍 原寸で開く・📝 ローカル編集——openFileMenu）。それまでは「🔗 ID」と「📝 ローカル編集」の2つを浮かべていた。
    function imgMenuButton() {
        let btn = document.getElementById('w-img-menu');
        if (btn) return btn;
        btn = document.createElement('button');
        btn.type = 'button';
        btn.id = 'w-img-menu';
        btn.className = 'img-edit-btn';
        btn.textContent = '⋯';
        btn.title = 'この写真の操作（ID を写す・保存・原寸・ローカル編集）';
        btn.addEventListener('mousedown', e => e.preventDefault());
        btn.addEventListener('click', e => {
            e.preventDefault();
            const img = imgEditTarget;
            const src = img ? img.getAttribute('src') || '' : '';
            const m = src.match(IMG_ATTACH_SRC);
            if (!m) return;
            openFileMenu(btn, { ref: m[1] + '-' + m[2], url: src, name: (img.getAttribute('alt') || '').trim() || src.split('/').pop(),
                kind: 'image', host: img });
        });
        btn.addEventListener('mouseenter', () => clearTimeout(imgEditHideTimer));
        btn.addEventListener('mouseleave', () => hideImgEditSoon());
        document.body.appendChild(btn);
        return btn;
    }

    function hideImgEditSoon() {
        clearTimeout(imgEditHideTimer);
        imgEditHideTimer = setTimeout(() => {
            const menu = document.getElementById('w-file-menu');
            const btn = document.getElementById('w-img-menu');
            // メニューを開いているあいだは「⋯」を消さない（押した札が消えると、メニューが宙に浮く）。
            if (menu && menu.classList.contains('active') && menu.wcmsAnchor === btn) return;
            if (btn) btn.classList.remove('active');
            imgEditTarget = null;
        }, 250);
    }

    document.addEventListener('mouseover', e => {
        const img = e.target;
        if (!img || img.tagName !== 'IMG' || document.body.classList.contains('anonymous')) return;
        if (!img.closest('#w-editor-content') || img.closest('.vocab-chrome, .file-view')) return;
        if (!IMG_ATTACH_SRC.test(img.getAttribute('src') || '')) return;
        if (window.matchMedia('(pointer: coarse)').matches) return;
        clearTimeout(imgEditHideTimer);
        imgEditTarget = img;
        const btn = imgMenuButton();
        const r = img.getBoundingClientRect();
        btn.classList.add('active');
        btn.style.top = (window.scrollY + r.top + 6) + 'px';
        btn.style.left = (window.scrollX + r.right - btn.offsetWidth - 6) + 'px';
    });
    document.addEventListener('mouseout', e => {
        const to = e.relatedTarget;
        if (e.target && e.target === imgEditTarget && to !== document.getElementById('w-img-menu')) {
            hideImgEditSoon();
        }
    });

    // ── 物ごとの見え方をこの端末に憶える（2026-09-28） ─────────────────────
    //
    // 利用者:「各ページのPDFなどの埋め込みの縦横比や、ブロックの開閉状態をブラウザに記録して再生することは
    // 出来ますか？」「一つ一つについて何らかのブラウザの記録エリアに記録したい」「開いてから長く経ったページの
    // 記録は、古い順に捨ててください」。
    //
    //   - 置き場は localStorage の `wcms.view`（UI設定の `wcms.ui` とは分ける——ページが増えるほど大きくなるので、
    //     レールの開閉などの小さな設定を重くしない）。形は {v:1, pages:{ページ番号:{t:最後に開いた時刻, r:{物:縦横比},
    //     o:{物:開閉}}}}。1つの物が数十バイトなので、localStorage（1サイト約5MB）には十分入る。
    //   - 物の鍵: ファイル表示は `f:`＋添付の参照（data-ref）、本文の折りたたみは `d:`＋題の文字＋同じ題の何番目か。
    //   - **縦横比**（高さ÷幅）で憶える——窓の幅を変えても図面の形が保たれる。つまんで大きさを変えたときに記録する。
    //   - **古いページから捨てる**——憶えるのは最近開いた VIEW_MAX_PAGES ページまで（記録のあるページを開くたびに
    //     時刻を新しくする）。書けない（容量超え・無効な環境）ときは古いほうの半分を捨てて書き直し、それでも駄目なら
    //     憶えないだけ——画面は壊さない。
    //   - 本文にもサーバーにも残さない（見る人と画面の都合・端末ごと）。
    //   - 鏡の中の**絞り込みの欄**も憶える（2026-09-30・欄 `i`・下の rememberKeyOf）——利用者:「チェックボタンの状態を
    //     ブラウザに記憶してはどうでしょうか？汎用的な機構としてです」。
    //   - 表の**並べ替え**も憶える（2026-10-03・欄 `s`・下の「表の列の題を押して並べ替える」）。
    //   - **見出しで畳んだ形**も憶える（2026-10-19・欄 `h`・下の「見出しで畳む」）。
    const VIEW_KEY = 'wcms.view';
    const VIEW_MAX_PAGES = 2000;
    const ViewState = (() => {
        let cache = null;
        function load() {
            if (cache) return cache;
            try { cache = JSON.parse(localStorage.getItem(VIEW_KEY) || 'null'); } catch (e) { cache = null; }
            if (!cache || typeof cache !== 'object' || cache.v !== 1 || !cache.pages || typeof cache.pages !== 'object') {
                cache = { v: 1, pages: {} };
            }
            return cache;
        }
        // dropOldest は古い順に n ページを捨てます（t が小さいものから）。
        function dropOldest(c, n) {
            const ids = Object.keys(c.pages).sort((a, b) => (c.pages[a].t || 0) - (c.pages[b].t || 0));
            for (const id of ids.slice(0, n)) delete c.pages[id];
        }
        function persist() {
            const c = load();
            const over = Object.keys(c.pages).length - VIEW_MAX_PAGES;
            if (over > 0) dropOldest(c, over);
            try { localStorage.setItem(VIEW_KEY, JSON.stringify(c)); return; } catch (e) { /* 容量超えかもしれない */ }
            try {
                dropOldest(c, Math.ceil(Object.keys(c.pages).length / 2));
                localStorage.setItem(VIEW_KEY, JSON.stringify(c));
            } catch (e) { /* 憶えないだけ（画面は既定の見え方） */ }
        }
        function page(id, create) {
            const c = load();
            let p = c.pages[id];
            if ((!p || typeof p !== 'object') && create) p = c.pages[id] = { t: 0, r: {}, o: {}, i: {}, s: {}, h: {} };
            if (p) { p.r = p.r || {}; p.o = p.o || {}; p.i = p.i || {}; p.s = p.s || {}; p.h = p.h || {}; }
            return p;
        }
        return {
            get(id) { return page(id, false); },
            // touch は記録のあるページの時刻を新しくします（開いたページは捨てられにくく）。
            touch(id) { const p = page(id, false); if (p) { p.t = Date.now(); persist(); } },
            setRatio(id, key, ratio) {
                const p = page(id, true);
                p.t = Date.now();
                p.r[key] = Math.round(ratio * 10000) / 10000;
                persist();
            },
            setOpen(id, key, open) {
                const p = page(id, true);
                p.t = Date.now();
                p.o[key] = open ? 1 : 0;
                persist();
            },
            // setInput は鏡の中の絞り込みの欄の値を憶えます（印は 1/0・文字と選択は文字列）。
            setInput(id, key, value) {
                const p = page(id, true);
                p.t = Date.now();
                p.i[key] = value;
                persist();
            },
            // setHeading は見出しで畳んだかを憶えます（畳んだ見出しだけを持つ——開いたら忘れる）。
            setHeading(id, key, folded) {
                const p = page(id, true);
                p.t = Date.now();
                if (folded) p.h[key] = 1; else delete p.h[key];
                persist();
            },
            // setSort は表の並べ替え（列の見出しの文字と向き）を憶えます。null なら忘れる（元の並び）。
            setSort(id, key, value) {
                const p = page(id, true);
                p.t = Date.now();
                if (value) p.s[key] = value; else delete p.s[key];
                persist();
            },
        };
    })();

    // viewKeyOf は物の鍵です（ファイル表示は添付の参照・本文の折りたたみは題の文字＋何番目か）。空なら憶えない。
    function viewKeyOf(el) {
        const fv = el.closest('section[data-type="file-view"][data-ref]');
        if (fv) return 'f:' + fv.getAttribute('data-ref');
        if (el.tagName !== 'DETAILS') return '';
        const label = d => {
            const s = d.querySelector(':scope > summary');
            return s ? s.textContent.trim().slice(0, 60) : '';
        };
        const mine = label(el);
        const same = Array.from(document.querySelectorAll('#w-editor-content details'))
            .filter(d => !d.closest('section[data-type="file-view"]') && !d.closest('.vocab-chrome') && label(d) === mine);
        return 'd:' + mine + '#' + same.indexOf(el);
    }

    // applyViewRatios は憶えた縦横比でファイル表示の枠の高さを決めます（幅は窓で決まる）。閉じている枠は幅が0なので
    // 飛ばし、開いたとき（toggle）にもう一度呼ぶ。
    function applyViewRatios() {
        const st = ViewState.get(currentPageId);
        if (!st) return;
        document.querySelectorAll('#w-editor-content .file-view').forEach(wrap => {
            const r = st.r[viewKeyOf(wrap)];
            if (!r) return;
            const w = wrap.getBoundingClientRect().width;
            if (w <= 0) return;
            wrap.style.height = Math.min(DRAWING_H_MAX, Math.max(DRAWING_H_MIN, Math.round(w * r))) + 'px';
        });
    }

    // viewTrackable は開閉を憶える折りたたみかです——本文の折りたたみと、**ファイル表示（PDF など）の枠**。
    // ⚠ ファイル表示の枠はサーバーが描く飾り（.vocab-chrome）の中にあるので、飾りを一律に除くと外れる
    //    （2026-09-29 利用者:「各ページの各PDFファイルの開閉も個別にブラウザに記録して再生したいです」）。
    //    鍵は縦横比と同じ添付の参照（viewKeyOf の `f:ページID-添付ID`）——開閉と縦横比は別の欄に入る。
    function viewTrackable(d) {
        if (d.classList.contains('file-view-fold')) return !!d.closest('section[data-type="file-view"][data-ref]');
        return !d.closest('.vocab-chrome');
    }

    // restoreViewState は憶えた開閉と縦横比を戻します（描き直しの巡りのたびに呼ぶ）。
    let viewTouchedFor = '';
    function restoreViewState() {
        const st = ViewState.get(currentPageId);
        if (!st) return;
        if (viewTouchedFor !== currentPageId) {
            viewTouchedFor = currentPageId;
            ViewState.touch(currentPageId);
        }
        document.querySelectorAll('#w-editor-content details').forEach(d => {
            if (!viewTrackable(d)) return;
            const k = viewKeyOf(d);
            if (!k || !(k in st.o)) return;
            const want = !!st.o[k];
            if (d.open !== want) d.open = want;
        });
        applyViewRatios();
        restoreRememberedInputs(st);
    }

    // ── 表のセルの式（2026-10-04） ─────────────────────────────────────────
    //
    // 利用者:「汎用的な仕組みとして、列の題名で計算させるということです。=個数*単価みたいにセルに書きます。編集時には
    // 書いたままを表示して編集でき、閲覧時には計算結果を表示できるのはどうですか？数値に変換できない場合、エラーと赤背景
    // 赤太文字を表示してはどうでしょう？」「小数も残す」。
    //
    //   - **計算はサーバー**（internal/cms/formula.go）——本文を返すときに式のセルへ `data-w-value`（結果）か
    //     `data-w-error`（理由）を付ける。DB にも同じ関数で計算した値が入るので、見える値と探せる値が揃う。
    //     ここは**見せるだけ**（計算しない）。属性は保存されない（`td` に許す属性ではない）。
    //   - 閲覧モード: 式のセルの中身を結果（エラーなら「⚠ エラー: 理由」を赤）に差し替える。書き出し（serializeChildren）は
    //     元の式で（formulaOriginal）。マウスを載せると式が出る。
    //   - 編集モード: 式のまま（元の中身へ戻す）。式のセルは `cell-formula`、前回の計算がエラーなら `cell-formula-error`。
    //   - 編集から抜けたら、式があれば本文を読み直して計算し直す（onModeToggle）。
    function isFormulaText(t) {
        return /^\s*[=＝]/.test(t || '');
    }

    function hasFormulaCells() {
        return Array.from(document.querySelectorAll('#w-editor-content td'))
            .some(td => !isServerOwned(td) && isFormulaText(formulaOriginal.has(td) ? '=' : td.textContent));
    }

    function restoreFormulaCell(td) {
        const orig = formulaOriginal.get(td);
        if (!orig) return;
        td.replaceChildren(...orig);
        formulaOriginal.delete(td);
        td.classList.remove('w-formula-error-cell');
    }

    // clearFormulaResults は手元の（古いかもしれない）結果を外します。
    function clearFormulaResults() {
        document.querySelectorAll('#w-editor-content td[data-w-value], #w-editor-content td[data-w-error]').forEach(td => {
            restoreFormulaCell(td);
            td.removeAttribute('data-w-value');
            td.removeAttribute('data-w-error');
        });
    }

    function refreshFormulaCells() {
        const editing = document.body.hasAttribute('edit-mode');
        document.querySelectorAll('#w-editor-content td').forEach(td => {
            if (isServerOwned(td)) return;
            const hasResult = td.hasAttribute('data-w-value') || td.hasAttribute('data-w-error');
            if (editing) {
                restoreFormulaCell(td);
                const formula = isFormulaText(td.textContent);
                td.classList.toggle('cell-formula', formula);
                td.classList.toggle('cell-formula-error', formula && td.hasAttribute('data-w-error'));
                return;
            }
            td.classList.remove('cell-formula', 'cell-formula-error');
            if (!hasResult || formulaOriginal.has(td)) return;
            const err = td.getAttribute('data-w-error');
            formulaOriginal.set(td, Array.from(td.childNodes));
            const shown = document.createElement('span');
            shown.className = 'vocab-chrome w-formula-shown' + (err !== null ? ' w-formula-error' : '');
            shown.contentEditable = 'false';
            shown.title = td.textContent.trim(); // 式
            shown.textContent = err !== null ? '⚠ エラー: ' + err : td.getAttribute('data-w-value');
            td.replaceChildren(shown);
            td.classList.toggle('w-formula-error-cell', err !== null);
        });
    }

    // ── 表へ複数のセルを貼る（2026-10-04） ─────────────────────────────────
    //
    // 利用者:「表のコピペを一つ一つしなくてもキャレットのある位置から右下に向かってペーストできませんか？」「複数セルを
    // コピーしたときの話です」。
    //
    //   - クリップボードが**表**（Excel・ブラウザの表の HTML）か**タブ区切り**の文字なら、キャレットのあるセルを左上にして
    //     右下へ上書きする（Excel と同じ）。**文字だけ**——書式・リンク・画像は持ち込まない。セルの中の改行は空白に。
    //   - 行が足りなければ**行を足す**。列が足りなければ**はみ出した列は捨てて知らせる**（列は見出しが意味を持つので勝手に
    //     足さない）。結合したセル・画像のあるセル・サーバーが足したセル（クローム）には書かない。
    //   - **見出しの行にも貼れる**（利用者:「表のコピペですが、見出しもうまいことコピペできませんか？」→ 問いへの答え「見出しの行にも
    //     貼れる」）——見出しの行から始めると、1行目が見出しを上書きし、2行目からがデータの行へ。見出しが変わると列の型も変わるので、
    //     表の印を付け直す（validateTypedTables）。
    //   - 1つのセルだけなら文字だけを入れる——Excel の1セルのコピーは表の HTML なので、そのままだと表の中に表ができていた。
    //   - 本文の変化は自動保存（MutationObserver）と元に戻す（Ctrl+Z）にそのまま乗る。
    function clipboardGrid(cd) {
        const htmlText = cd.getData('text/html') || '';
        if (/<table[\s>]/i.test(htmlText)) {
            const doc = new DOMParser().parseFromString(htmlText, 'text/html');
            const t = doc.querySelector('table');
            if (t) {
                const rows = [];
                Array.from(t.rows).forEach(tr => {
                    const row = [];
                    Array.from(tr.cells).forEach(c => {
                        if (c.classList.contains('vocab-chrome')) return; // w-cms の表から写した鏡の列（最新単価など）
                        row.push(clipCellText(c));
                        for (let k = 1; k < (c.colSpan || 1); k++) row.push('');
                    });
                    if (row.length) rows.push(row);
                });
                if (rows.length) return rows;
            }
        }
        const plain = cd.getData('text/plain') || '';
        if (plain.indexOf('\t') === -1) return null;
        return parseTSV(plain);
    }

    // clipCellText はコピーしたセルの文字です——鏡の飾り（並べ替えの印など）は除き、式の結果は値のまま、改行は空白に。
    function clipCellText(c) {
        const x = c.cloneNode(true);
        x.querySelectorAll('.vocab-chrome:not(.w-formula-shown), style, script').forEach(e => e.remove());
        x.querySelectorAll('br').forEach(b => b.replaceWith(' '));
        return (x.textContent || '').replace(/\s+/g, ' ').trim();
    }

    // parseTSV はタブ区切りの文字を行と列に分けます（Excel の書き方——"…" で囲んだ値は中にタブ・改行・"" を持てる）。
    function parseTSV(text) {
        const s = text.replace(/\r\n?/g, '\n');
        const rows = [];
        let row = [], cur = '', quoted = false, atStart = true;
        for (let i = 0; i < s.length; i++) {
            const ch = s[i];
            if (quoted) {
                if (ch === '"') {
                    if (s[i + 1] === '"') { cur += '"'; i++; } else { quoted = false; }
                } else {
                    cur += ch;
                }
                continue;
            }
            if (ch === '"' && atStart) { quoted = true; atStart = false; continue; }
            if (ch === '\t') { row.push(cur); cur = ''; atStart = true; continue; }
            if (ch === '\n') { row.push(cur); rows.push(row); row = []; cur = ''; atStart = true; continue; }
            cur += ch;
            atStart = false;
        }
        if (cur !== '' || row.length) { row.push(cur); rows.push(row); }
        return rows.map(r => r.map(v => v.replace(/\s+/g, ' ').trim()));
    }

    // pasteGridIntoTable は grid（行の配列）を start のセルから右下へ書きます。
    function pasteGridIntoTable(start, grid) {
        const startRow = start.parentElement;
        const table = start.closest('table');
        if (!table || !startRow || startRow.tagName !== 'TR') return;
        const headerTouched = startRow === table.rows[0];
        const bodyCells = tr => Array.from(tr.cells).filter(c => !isServerOwned(c));
        const width = bodyCells(startRow).length;
        const col0 = bodyCells(startRow).indexOf(start);
        if (col0 < 0) return;
        let tr = startRow;
        let added = 0, skipped = 0, filled = 0, last = null;
        const maxCols = Math.max(...grid.map(r => r.length));
        const dropped = Math.max(0, col0 + maxCols - width);
        grid.forEach((vals, r) => {
            if (r > 0) {
                let next = tr.nextElementSibling;
                while (next && next.tagName !== 'TR') next = next.nextElementSibling;
                if (!next || isServerOwned(next)) {
                    const nr = document.createElement('tr');
                    for (let k = 0; k < width; k++) nr.appendChild(document.createElement('td'));
                    tr.parentNode.insertBefore(nr, tr.nextSibling);
                    next = nr;
                    added++;
                }
                tr = next;
            }
            const cells = bodyCells(tr);
            vals.forEach((v, c) => {
                const cell = cells[col0 + c];
                if (!cell) return;
                if (cell.colSpan > 1 || cell.rowSpan > 1 || cell.querySelector('img')) { skipped++; return; }
                cell.textContent = v;
                if (cell.tagName === 'TD') validateCell(cell);
                filled++;
                last = cell;
            });
        });
        // 見出しを書き換えたら列の型が変わる——表のセルの印（読めた・読めない）を付け直す。
        if (headerTouched) validateTypedTables();
        if (last && !selectedCells().length) placeCaretAtEnd(last);
        updateHtmlPreview();
        const notes = [];
        if (added) notes.push(`行を ${added} 行足しました`);
        if (dropped) notes.push(`表からはみ出した ${dropped} 列は貼っていません`);
        if (skipped) notes.push(`結合・画像のセル ${skipped} か所には書いていません`);
        notify(`${grid.length} 行 × ${maxCols} 列を貼りました（${filled} セル）` + (notes.length ? '——' + notes.join('・') : '') + '。',
            { type: notes.length ? 'warn' : 'info', id: 'paste-grid', duration: notes.length ? 8000 : 3000 });
    }

    // ── 表のセルを矩形で選ぶ（2026-10-04） ─────────────────────────────────
    //
    // 利用者:「表のセルの選択ですが、ドラッグ矩形の範囲にあるセルを選択するように出来ますか？」。
    //
    //   - 表の中でドラッグして**別のセルへ入ると**、文字の選択をやめて**セルの矩形**を選ぶ（Excel と同じ）。同じセルの中のドラッグは
    //     今までどおり文字の選択。Shift を押しながらセルを押すと、前に押したセルからの矩形。閲覧モード・編集モードの両方
    //     （鏡の表——受注残表など——も選べる）。
    //   - 選んだセルで: **コピー**（Ctrl+C——タブ区切りと表の HTML。Excel にも w-cms の表にも貼れる）。編集モードでは**消す**
    //     （Delete・Backspace）・**切り取り**（Ctrl+X）・**貼り付け**（Ctrl+V——選んだ矩形の左上から。値が1つなら選んだセル全部へ）。
    //   - 写すのは見えている文字（閲覧モードの式は結果・編集モードは式・入力欄は値）。消す・書くのは本文のセルだけ（鏡・結合・画像の
    //     セルは触らない）。
    //   - 選んだ印は class（保存されない）。ほかの所を押すか Esc、ほかのキーで外れる。結合したセルは表の格子（rowspan・colspan）で数える。
    let cellSel = null;   // { table, anchor: [行, 列], focus: [行, 列] }——格子の位置
    let cellDrag = null;  // { table, start: セル, active: 矩形に入ったか }
    let lastCell = null;  // Shift で広げる起点

    // tableGrid は表の格子（grid[行][列] = そこを占めるセル）です。
    function tableGrid(table) {
        const grid = [];
        Array.from(table.rows).forEach((tr, r) => {
            grid[r] = grid[r] || [];
            let c = 0;
            Array.from(tr.cells).forEach(cell => {
                while (grid[r][c]) c++;
                for (let dr = 0; dr < (cell.rowSpan || 1); dr++) {
                    for (let dc = 0; dc < (cell.colSpan || 1); dc++) {
                        grid[r + dr] = grid[r + dr] || [];
                        grid[r + dr][c + dc] = cell;
                    }
                }
                c += cell.colSpan || 1;
            });
        });
        return grid;
    }

    function gridPos(grid, cell) {
        for (let r = 0; r < grid.length; r++) {
            const c = (grid[r] || []).indexOf(cell);
            if (c >= 0) return [r, c];
        }
        return null;
    }

    function cellSelBounds() {
        const [r0, c0] = cellSel.anchor;
        const [r1, c1] = cellSel.focus;
        return { top: Math.min(r0, r1), bottom: Math.max(r0, r1), left: Math.min(c0, c1), right: Math.max(c0, c1) };
    }

    // selectedCells は選んだ矩形のセルです（左上から・同じセルは1つ）。
    function selectedCells() {
        if (!cellSel || !cellSel.table.isConnected) return [];
        const grid = tableGrid(cellSel.table);
        const b = cellSelBounds();
        const out = [];
        for (let r = b.top; r <= b.bottom; r++) {
            for (let c = b.left; c <= b.right; c++) {
                const cell = (grid[r] || [])[c];
                if (cell && out.indexOf(cell) === -1) out.push(cell);
            }
        }
        return out;
    }

    function markCellSel() {
        document.querySelectorAll('#w-editor-content .w-cell-selected').forEach(c => c.classList.remove('w-cell-selected'));
        selectedCells().forEach(c => c.classList.add('w-cell-selected'));
    }

    function clearCellSel() {
        if (!cellSel) return;
        cellSel = null;
        markCellSel();
    }

    function setCellSel(table, from, to) {
        const grid = tableGrid(table);
        const a = gridPos(grid, from);
        const b = gridPos(grid, to);
        if (!a || !b) return;
        cellSel = { table, anchor: a, focus: b };
        markCellSel();
        const s = window.getSelection();
        if (s && s.rangeCount) s.removeAllRanges(); // 文字の選択は外す（セルを選んでいる）
    }

    // writableCell は消す・書くセルか（編集モード・本文・結合も画像も無い）。
    function writableCell(cell) {
        return document.body.hasAttribute('edit-mode') && !isServerOwned(cell) &&
            cell.colSpan === 1 && cell.rowSpan === 1 && !cell.querySelector('img');
    }

    // cellCopyText はセルの見えている文字です（入力欄は値・ボタンと並べ替えの印は除く・改行は空白）。
    function cellCopyText(cell) {
        const field = cell.querySelector('input:not([type=checkbox]):not([type=radio]):not([type=button]):not([type=hidden]), select, textarea');
        if (field) return (field.value || '').trim();
        const x = cell.cloneNode(true);
        x.querySelectorAll('button, .w-sort-mark, script, style').forEach(e => e.remove());
        x.querySelectorAll('br').forEach(e => e.replaceWith(' '));
        return (x.textContent || '').replace(/\s+/g, ' ').trim();
    }

    // cellSelClipboard は選んだ矩形を、タブ区切りの文字と表の HTML にします（結合したセルの続きは空）。
    function cellSelClipboard() {
        const grid = tableGrid(cellSel.table);
        const b = cellSelBounds();
        const quote = v => (/^"|[\t\n]/.test(v) ? '"' + v.replace(/"/g, '""') + '"' : v);
        const lines = [];
        const t = document.createElement('table');
        const tb = document.createElement('tbody');
        t.appendChild(tb);
        for (let r = b.top; r <= b.bottom; r++) {
            const vals = [];
            const tr = document.createElement('tr');
            for (let c = b.left; c <= b.right; c++) {
                const cell = (grid[r] || [])[c];
                const first = cell && gridPos(grid, cell);
                const v = cell && first[0] === r && first[1] === c ? cellCopyText(cell) : '';
                vals.push(quote(v));
                const out = document.createElement(cell && cell.tagName === 'TH' ? 'th' : 'td');
                out.textContent = v;
                tr.appendChild(out);
            }
            lines.push(vals.join('\t'));
            tb.appendChild(tr);
        }
        return { text: lines.join('\n') + '\n', html: t.outerHTML };
    }

    // clearSelectedCells は選んだセルの中身を消します（編集モード）。
    function clearSelectedCells() {
        let n = 0;
        selectedCells().forEach(cell => {
            if (!writableCell(cell)) return;
            cell.textContent = '';
            if (cell.tagName === 'TD') validateCell(cell);
            n++;
        });
        if (n) updateHtmlPreview();
        return n;
    }

    document.addEventListener('mousedown', e => {
        if (e.button !== 0) return;
        const t = e.target instanceof Element ? e.target : null;
        const cell = t && t.closest('#w-editor-content td, #w-editor-content th');
        if (!cell || t.closest('a, button, input, select, textarea, label, summary')) {
            clearCellSel();
            cellDrag = null;
            return;
        }
        const table = cell.closest('table');
        if (e.shiftKey && lastCell && lastCell.isConnected && lastCell.closest('table') === table && lastCell !== cell) {
            e.preventDefault();
            setCellSel(table, lastCell, cell);
            return;
        }
        clearCellSel();
        lastCell = cell;
        cellDrag = { table, start: cell, active: false };
    });

    document.addEventListener('mousemove', e => {
        if (!cellDrag || !(e.buttons & 1)) return;
        const el = document.elementFromPoint(e.clientX, e.clientY);
        const cell = el && el.closest('td, th');
        if (!cell || cell.closest('table') !== cellDrag.table) return;
        if (!cellDrag.active && cell === cellDrag.start) return; // 同じセルの中は文字の選択のまま
        cellDrag.active = true;
        document.body.classList.add('w-cell-dragging');
        e.preventDefault();
        setCellSel(cellDrag.table, cellDrag.start, cell);
    });

    document.addEventListener('mouseup', () => {
        if (!cellDrag) return;
        cellDrag = null;
        document.body.classList.remove('w-cell-dragging');
    });

    document.addEventListener('keydown', e => {
        if (!cellSel) return;
        if (e.key === 'Escape') { clearCellSel(); return; }
        if (['Shift', 'Control', 'Meta', 'Alt'].indexOf(e.key) !== -1 || e.ctrlKey || e.metaKey) return; // Ctrl+C・X・V は下の出来事で
        if ((e.key === 'Delete' || e.key === 'Backspace') && document.body.hasAttribute('edit-mode')) {
            e.preventDefault();
            clearSelectedCells();
            return;
        }
        clearCellSel();
    }, true);

    // putCellsOnClipboard は、選んだセルがあればタブ区切りと表の HTML でクリップボードへ写して true を返します
    // （Ctrl+C・Ctrl+X——コピーと切り取りが写していた・2026-10-09 に寄せた）。
    function putCellsOnClipboard(e) {
        if (!cellSel || !e.clipboardData || !selectedCells().length) return false;
        const c = cellSelClipboard();
        e.clipboardData.setData('text/plain', c.text);
        e.clipboardData.setData('text/html', c.html);
        e.preventDefault();
        return true;
    }
    document.addEventListener('copy', e => { putCellsOnClipboard(e); });
    document.addEventListener('cut', e => { if (putCellsOnClipboard(e)) clearSelectedCells(); });

    // fillSelectedCells は選んだセル全部に同じ値を書きます（1つの値を矩形へ貼ったとき）。
    function fillSelectedCells(value) {
        let n = 0;
        selectedCells().forEach(cell => {
            if (!writableCell(cell)) return;
            cell.textContent = value;
            if (cell.tagName === 'TD') validateCell(cell);
            n++;
        });
        if (n) updateHtmlPreview();
        return n;
    }

    // ── 表の列の題を押して並べ替える（2026-10-03） ─────────────────────────
    //
    // 利用者:「表の列の題をクリックして並び順を変えるように出来ませんか？」→ 問いへの答え:「表示だけ」「この端末で憶える」。
    //
    //   - **閲覧モードだけ・表示だけ**——本文は書き換えない（版も増えない）。題を押すたびに 昇順 → 降順 → 元の並び。
    //   - 並べ替えは端末に憶える（`wcms.view` の欄 `s`）。表の鍵はキャプション（無ければ見出しの文字）＋同じ鍵の何番目か、
    //     列は**見出しの文字**で憶える——列を足しても別の列で並ばない。見出しが消えていたら並べない。
    //   - ⚠ **並べ替えた並びを本文に保存させない**——行は DOM の上で動かすので、書き出し（serializeChildren）は並べ替えた
    //     `tbody` を**元の並び**で書く（tableSortOriginal）。編集モードへ入るときは元の並びへ戻す（編集中に行を足すと
    //     「元の並び」が定まらないため・編集は本文の並びで）。
    //   - 行のボタン（発注・部材見積依頼の ↩ 戻す、受注残表の欄など）は行番号を属性で持つので、並べ替えても押す先はずれない。
    //   - 並べるのは見出しの行（最初の行が全部 th）のある表だけ——縦の表（見積計算表）や、結合したセル（colspan・rowspan）の
    //     ある表は並べない。`tbody` ごとに並べる（臨時部材の行のような別の `tbody` は混ぜない）・足元（tfoot）は動かさない。
    //     選ぶ欄・ボタンだけの見出し（チェックの列など）と空の見出しは押せない。
    //   - 比べ方: 全角は半角に畳み、数（桁区切り・円記号つきも）は数として、ほかは数字の部分を数として比べる（`2026/9/5` <
    //     `2026/10/15`）。**空のセルはどちら向きでも最後**。同じ値は元の並びのまま。
    const tableSortState = new WeakMap(); // table → { col, dir }（dir: 1 昇順・-1 降順）
    const tableSortKeys = new WeakMap();  // table → 憶える鍵
    const tableSortCollator = new Intl.Collator('ja', { numeric: true, sensitivity: 'base' });

    // sortHeaderRow は表の見出しの行（全部が th の最初の行）を返します。並べられない表なら null。
    function sortHeaderRow(table) {
        if (table.parentElement && table.parentElement.closest('table')) return null; // 表の中の表
        const first = table.querySelector(':scope > thead > tr, :scope > tbody > tr, :scope > tr');
        if (!first || !first.children.length) return null;
        if (!Array.from(first.children).every(c => c.tagName === 'TH')) return null;
        const rows = sortBodyRows(table, first);
        if (rows.length < 2) return null;
        const spanned = c => c.colSpan > 1 || c.rowSpan > 1;
        if (Array.from(first.children).some(spanned)) return null;
        if (rows.some(r => Array.from(r.cells).some(spanned))) return null;
        return first;
    }

    // sortBodyRows は並べる行（tbody の行・見出しの行は除く）を返します。
    function sortBodyRows(table, header) {
        const out = [];
        Array.from(table.tBodies).forEach(b => Array.from(b.rows).forEach(r => { if (r !== header) out.push(r); }));
        return out;
    }

    // sortableHeaderCell はその見出しを押して並べられるかです（選ぶ欄・ボタンだけの見出し・空の見出しは押せない）。
    function sortableHeaderCell(th) {
        if (th.querySelector('input, button, select, textarea')) return false;
        return sortHeaderText(th) !== '';
    }

    function sortHeaderText(th) {
        const c = th.cloneNode(true);
        c.querySelectorAll('.w-sort-mark').forEach(e => e.remove());
        return c.textContent.trim();
    }

    // sortCellText はセルの比べる文字です（入力欄なら値・セルの中の飾りとボタンは除く）。
    function sortCellText(cell) {
        if (!cell) return '';
        // 式のセルは計算した値で（見せている値は飾り〔クローム〕なので下では読めない）。エラーの見せ方も飾りなので、
        // 下で空として読まれ、空のセルと同じく最後へ並ぶ。
        if (cell.hasAttribute('data-w-value')) return cell.getAttribute('data-w-value');
        const field = cell.querySelector('input:not([type=checkbox]):not([type=radio]):not([type=button]), select, textarea');
        let s;
        if (field) {
            s = field.value;
        } else {
            const c = cell.cloneNode(true);
            if (!cell.classList.contains('vocab-chrome')) c.querySelectorAll('.vocab-chrome').forEach(e => e.remove());
            c.querySelectorAll('button').forEach(e => e.remove());
            s = c.textContent;
        }
        return (s || '').normalize('NFKC').trim();
    }

    function sortNumberOf(s) {
        const t = s.replace(/[,\s¥円]/g, '');
        return /^[-+]?\d+(\.\d+)?$/.test(t) ? parseFloat(t) : null;
    }

    function compareSortText(a, b) {
        const na = sortNumberOf(a);
        const nb = sortNumberOf(b);
        if (na !== null && nb !== null) return na - nb;
        return tableSortCollator.compare(a, b);
    }

    // unsortTable は表を元の並びへ戻し、印を外します。
    function unsortTable(table) {
        Array.from(table.tBodies).forEach(b => {
            const orig = tableSortOriginal.get(b);
            if (!orig) return;
            orig.forEach(n => { if (n.parentNode === b) b.appendChild(n); });
            tableSortOriginal.delete(b);
        });
        tableSortState.delete(table);
        table.querySelectorAll(':scope .w-sort-mark').forEach(e => e.remove());
        table.querySelectorAll(':scope th.w-sorted').forEach(th => th.classList.remove('w-sorted'));
    }

    // sortTable は表を col 列で並べます（dir: 1 昇順・-1 降順・0 元の並び）。
    function sortTable(table, header, col, dir) {
        unsortTable(table);
        if (!dir || col < 0) return;
        Array.from(table.tBodies).forEach(b => {
            const rows = Array.from(b.rows).filter(r => r !== header);
            if (rows.length < 2) return;
            tableSortOriginal.set(b, Array.from(b.childNodes));
            const keyed = rows.map((r, i) => ({ r, i, t: sortCellText(r.cells[col]) }));
            keyed.sort((x, y) => {
                const ex = x.t === '';
                const ey = y.t === '';
                if (ex !== ey) return ex ? 1 : -1;
                return (ex ? 0 : compareSortText(x.t, y.t) * dir) || x.i - y.i;
            });
            keyed.forEach(k => b.appendChild(k.r)); // 見出しの行は動かさない（行は見出しの後ろへ並ぶ）
        });
        tableSortState.set(table, { col, dir });
        const th = header.children[col];
        if (th) {
            th.classList.add('w-sorted');
            const mark = document.createElement('span');
            mark.className = 'vocab-chrome w-sort-mark';
            mark.contentEditable = 'false';
            mark.textContent = dir > 0 ? '▲' : '▼';
            th.appendChild(mark);
        }
    }

    // refreshTableSort は描き直しの巡りのたびに呼ばれます——いったん元の並びへ戻し、閲覧モードなら押せる見出しに印を付けて、
    // 憶えた並べ替えを戻します。編集モードでは元の並びのまま（印も付けない）。
    function refreshTableSort() {
        const editing = document.body.hasAttribute('edit-mode');
        const st = ViewState.get(currentPageId);
        const seen = {};
        document.querySelectorAll('#w-editor-content table').forEach(table => {
            unsortTable(table);
            table.querySelectorAll(':scope th.w-sortable').forEach(th => th.classList.remove('w-sortable'));
            tableSortKeys.delete(table);
            if (editing) return;
            const header = sortHeaderRow(table);
            if (!header) return;
            const cap = table.querySelector(':scope > caption');
            const name = ((cap && cap.textContent.trim()) ||
                Array.from(header.children).map(sortHeaderText).join('|')).slice(0, 80);
            seen[name] = (seen[name] || 0) + 1;
            const key = 't:' + name + '#' + (seen[name] - 1);
            tableSortKeys.set(table, key);
            Array.from(header.children).forEach(th => { if (sortableHeaderCell(th)) th.classList.add('w-sortable'); });
            const want = st && st.s && st.s[key];
            if (!want) return;
            const col = Array.from(header.children).findIndex(th => th.classList.contains('w-sortable') && sortHeaderText(th) === want.c);
            if (col >= 0) sortTable(table, header, col, want.d === -1 ? -1 : 1);
        });
    }

    document.addEventListener('click', e => {
        if (document.body.hasAttribute('edit-mode')) return;
        if (!(e.target instanceof Element)) return;
        const th = e.target.closest('#w-editor-content th.w-sortable');
        if (!th || e.target.closest('a, button, input, select, textarea, summary')) return;
        const table = th.closest('table');
        const header = th.parentElement;
        if (!table || sortHeaderRow(table) !== header) return;
        const col = Array.from(header.children).indexOf(th);
        const cur = tableSortState.get(table);
        // 別の列なら昇順から・同じ列なら 昇順 → 降順 → 元の並び。
        const dir = (!cur || cur.col !== col) ? 1 : (cur.dir === 1 ? -1 : 0);
        sortTable(table, header, col, dir);
        const key = tableSortKeys.get(table);
        if (key) ViewState.setSort(currentPageId, key, dir ? { c: sortHeaderText(th), d: dir } : null);
    });

    // ── 見出しで畳む（2026-10-04） ─────────────────────────────────────────
    //
    // 利用者:「先ほど、材料や外注加工などを開閉させるようにしたのですが、それはやめて、汎用的な機構にしたいです。すべての
    // 見出しについて、クリックすると、次の同レベル見出し（H2なら次のH2）の前まで閉じれるようにしたいです」
    // （同じ日の午前に作った「部材の節をまとめて畳む」——鏡が印を付けた節だけ——を置き換えた）。
    //
    //   - **閲覧モードだけ・表示だけ**——本文の見出しを押すと、その下を**次の同じ段か上の段の見出しの手前まで**隠す
    //     （H2 なら次の H2 か H1 まで——上の段の見出しを越えて隠すと、別の章が消える）。もう一度押すと開く。本文は書き換えない
    //     （`class` は書き出さない）。編集モードでは全部見える（CSS）。
    //     ⚠ 2026-10-19 から、畳んだ形を**この端末に憶える**（ViewState の欄 `h`——開き直しても畳んだまま）。それまでは憶えなかった。
    //     2026-10-19 利用者:「コアの機能で、見出しから次の見出しまで折り畳むことが出来、状態をブラウザに記憶出来てたはずですが、今は再読み込みすると状態が再現できません」
    //   - 節（`<section>`）の**最初の見出し**なら、節の残りと、節の後ろの続き（次の見出しまで）も隠す——見出しの下に節が来る形
    //     （`<section><h2>材料</h2><table>…</section>`）でも、見出しを並べただけの形でも同じに畳める。節の途中の見出しは、
    //     その節の終わりまで。
    //   - 畳まないもの: ページの題（最初の H1——畳むとページが丸ごと消える）・鏡（サーバーが足したもの）の中の見出し・表や
    //     リストの中の見出し。
    //   - 畳んだ見出しの中にさらに畳んだ見出しがあれば、外を開いても中は畳んだまま（どこを隠すかは畳んだ見出しから毎回求め直す）。
    function headingLevel(el) {
        return el && /^H[1-6]$/.test(el.tagName) ? Number(el.tagName[1]) : 0;
    }

    function isFoldableHeading(h) {
        if (!headingLevel(h) || isServerOwned(h)) return false;
        const editor = document.getElementById('w-editor-content');
        if (!editor || !editor.contains(h) || h.closest('table, ul, ol, dl')) return false;
        return h !== editor.querySelector('h1'); // ページの題
    }

    // stopsFold は el がこの段（level）の畳みを止めるか——同じ段か上の段の本文の見出しであるか、それを中に持つか。
    function stopsFold(el, level) {
        const lv = headingLevel(el);
        if (lv) return lv <= level && !isServerOwned(el);
        return Array.from(el.querySelectorAll('h1, h2, h3, h4, h5, h6'))
            .some(x => headingLevel(x) <= level && !isServerOwned(x));
    }

    // foldRange は見出し h を畳んだときに隠す要素です。
    function foldRange(h) {
        const level = headingLevel(h);
        const editor = document.getElementById('w-editor-content');
        const out = [];
        let node = h;
        while (node && node !== editor) {
            for (let sib = node.nextElementSibling; sib; sib = sib.nextElementSibling) {
                if (stopsFold(sib, level)) return out;
                out.push(sib);
            }
            const parent = node.parentElement;
            if (!parent || parent === editor) break;
            // 入れ物を越えて続けるのは、ブロックの包みか、この見出しで始まる入れ物（節）のとき。
            const wrapper = parent.classList.contains('block-content') || parent.classList.contains('editor-block');
            const first = Array.from(parent.children).find(c => !isServerOwned(c));
            if (!wrapper && !(first && (first === h || first.contains(h)))) break;
            node = parent;
        }
        return out;
    }

    // applyHeadingFolds は、いま畳んでいる見出しから隠す要素を求め直します。
    function applyHeadingFolds() {
        document.querySelectorAll('#w-editor-content .w-fold-hidden').forEach(el => el.classList.remove('w-fold-hidden'));
        document.querySelectorAll('#w-editor-content .w-heading-folded').forEach(h => {
            if (!isFoldableHeading(h)) { h.classList.remove('w-heading-folded'); return; }
            foldRange(h).forEach(el => el.classList.add('w-fold-hidden'));
        });
    }

    // 畳んだ見出しは ViewState（このページの欄 `h`）に憶えます（段・文字・同じ見出しの何番目か）。編集から戻ると、保存の返事
    // （エコーバック）で見出しの要素が作り直されて印が消えるので、描き直しの巡りのたびにここから付け直す（refreshHeadingFolds）。
    // ⚠ 2026-10-19 までは、このページを開いているあいだだけ（Set）で、ブラウザには憶えていなかった——開き直すと開いた形に戻った。
    //   2026-10-19 利用者:「コアの機能で、見出しから次の見出しまで折り畳むことが出来、状態をブラウザに記憶出来てたはずですが、今は再読み込みすると状態が再現できません」
    function headingFolded(key) {
        const st = ViewState.get(currentPageId);
        return !!(st && st.h && st.h[key]);
    }
    const HEADING_SELECTOR = '#w-editor-content h1, #w-editor-content h2, #w-editor-content h3, #w-editor-content h4, #w-editor-content h5, #w-editor-content h6';

    function headingKey(h) {
        const text = h.textContent.trim();
        let nth = 0;
        for (const x of document.querySelectorAll(HEADING_SELECTOR)) {
            if (x === h) break;
            if (x.tagName === h.tagName && x.textContent.trim() === text) nth++;
        }
        return h.tagName + '|' + text + '|' + nth; // ページは ViewState の側で分かれる
    }

    // refreshHeadingFolds は描き直しの巡りのたびに呼ばれます——押せる見出しに印を付け、畳んだ形を求め直す。
    function refreshHeadingFolds() {
        document.querySelectorAll(HEADING_SELECTOR).forEach(h => {
            const ok = isFoldableHeading(h);
            h.classList.toggle('w-foldable', ok);
            h.classList.toggle('w-heading-folded', ok && headingFolded(headingKey(h)));
        });
        applyHeadingFolds();
    }

    document.addEventListener('click', e => {
        if (document.body.hasAttribute('edit-mode')) return;
        if (!(e.target instanceof Element) || e.target.closest('a, button, input, select, textarea, summary')) return;
        const h = e.target.closest(HEADING_SELECTOR);
        if (!h || !isFoldableHeading(h)) return;
        const sel = window.getSelection && window.getSelection();
        if (sel && !sel.isCollapsed && h.contains(sel.anchorNode)) return; // 見出しの文字を選んでいるときは畳まない
        const key = headingKey(h);
        ViewState.setHeading(currentPageId, key, h.classList.toggle('w-heading-folded'));
        applyHeadingFolds();
    });

    // ── 鏡の中の絞り込みの欄を憶える（2026-09-30） ─────────────────────────
    //
    // 利用者:「/001305 を見て思ったのですが、チェックボタンの状態をブラウザに記憶してはどうでしょうか？汎用的な機構と
    // してです」——鏡「加工製品の一覧」の絞り込み（区分の印・移行中だけ・文字・装置名称）が、開くたびに初期値へ戻っていた。
    //
    //   - **憶えるのは鏡を描く側が `data-w-remember` の印を付けた入れ物の中の入力だけ**。⚠ 鏡の中には本文へ書き戻す欄
    //     （受注残表の出荷済数量・材料発注など）もある——それをブラウザの記憶で戻すと、古い値が本文の値を上書きしたように
    //     見え（戻したときの change で書き込みまで走りうる）、データの正本が2つになる。だから一律には憶えず、見る人の
    //     都合の欄（絞り込み）だけを印で名乗らせる。鏡ごとに書くのは印1つで、憶える・戻す仕事はここ1か所。
    //   - 鍵は鏡の名前＋同じ名前の鏡の何番目か＋入力の印（data-* の名前と値・種類）＋同じ印の何番目か。
    //   - **戻したら change を出す**——鏡の側の絞り込み（document への委譲）が掛け直す。印だけ戻って表が絞られて
    //     いない、を作らない。⚠ 自分で出した change は憶えない（isTrusted で見分ける）。
    function rememberKeyOf(el) {
        const box = el.closest('[data-w-remember]');
        const mirror = el.closest('section[data-mirror]');
        if (!box || !mirror || !mirror.closest('#w-editor-content')) return '';
        const name = mirror.getAttribute('data-mirror');
        const mirrors = Array.from(document.querySelectorAll('#w-editor-content section[data-mirror]'))
            .filter(s => s.getAttribute('data-mirror') === name);
        const sig = e => e.tagName.toLowerCase() + ':' + (e.type || '') + ':' + Array.from(e.attributes)
            .filter(a => a.name.startsWith('data-')).map(a => a.name + '=' + a.value).sort().join('&');
        const mine = sig(el);
        const same = Array.from(box.querySelectorAll('input, select, textarea')).filter(e => sig(e) === mine);
        return 'm:' + name + '#' + mirrors.indexOf(mirror) + '|' + mine + '#' + same.indexOf(el);
    }
    const rememberValueOf = el => (el.type === 'checkbox' || el.type === 'radio') ? (el.checked ? 1 : 0) : el.value;

    function restoreRememberedInputs(st) {
        if (!st || !st.i) return;
        document.querySelectorAll('#w-editor-content [data-w-remember]').forEach(box => {
            let changed = null;
            box.querySelectorAll('input, select, textarea').forEach(el => {
                const k = rememberKeyOf(el);
                if (!k || !(k in st.i)) return;
                const want = st.i[k];
                if (el.type === 'checkbox' || el.type === 'radio') {
                    if (el.checked !== !!want) { el.checked = !!want; changed = el; }
                } else if (typeof want === 'string' && el.value !== want) {
                    // 選択肢から消えた値（装置名称が無くなった等）は戻さない——空の選択にすると全部が隠れる。
                    if (el.tagName === 'SELECT' && !Array.from(el.options).some(o => o.value === want)) return;
                    el.value = want;
                    changed = el;
                }
            });
            if (changed) changed.dispatchEvent(new Event('change', { bubbles: true }));
        });
    }

    const recordRememberedInput = e => {
        if (!e.isTrusted) return; // 戻したときに自分で出した change は憶えない
        const el = e.target;
        if (!(el instanceof HTMLInputElement || el instanceof HTMLSelectElement || el instanceof HTMLTextAreaElement)) return;
        const k = rememberKeyOf(el);
        if (k) ViewState.setInput(currentPageId, k, rememberValueOf(el));
    };
    document.addEventListener('input', recordRememberedInput, true);
    document.addEventListener('change', recordRememberedInput, true);

    // 開閉を憶える——**人が題を押したときだけ**（toggle は泡立たないので捕捉の段で受ける）。
    // ⚠ `<details open>` を本文へ入れただけでもブラウザは toggle を出す（人は何もしていない）。それを記録すると、
    //    閉じた記録が読み込みのたびに「開いた」で上書きされる（2026-09-28 に E2E で踏んだ）。だから題への
    //    クリック・Enter・Space で印を付け、印のある toggle だけを記録する（戻したときの toggle も記録しない）。
    const markUserToggle = e => {
        const s = e.target instanceof Element && e.target.closest('summary');
        const d = s && s.parentElement;
        if (d && d.tagName === 'DETAILS' && d.closest('#w-editor-content')) d.dataset.wUserToggle = '1';
    };
    document.addEventListener('click', markUserToggle, true);
    document.addEventListener('keydown', e => { if (e.key === 'Enter' || e.key === ' ') markUserToggle(e); }, true);
    document.addEventListener('toggle', e => {
        const d = e.target;
        if (!(d instanceof HTMLDetailsElement) || !d.closest('#w-editor-content') || !viewTrackable(d)) return;
        if (d.dataset.wUserToggle === '1') {
            delete d.dataset.wUserToggle;
            const k = viewKeyOf(d);
            if (k) ViewState.setOpen(currentPageId, k, d.open);
        }
        if (d.open) applyViewRatios();
    }, true);
    // 本文の欄の幅が変わったら、縦横比から高さを決め直す。
    // ⚠ **その場で追従します**（2026-09-29 利用者:「ブラウザの幅を変えたような場合は、動的に追従できません」）。
    //    それまでは窓の resize を 150ms 待ってから1回だけ決め直していたので、**引いているあいだは高さが古いまま**で、
    //    窓の大きさは変わらずに欄だけが変わるとき（左右のレールの開け閉め・スクロールバーの出入り）は決め直しも
    //    しませんでした。いまは**欄そのものの幅**を見て（ResizeObserver）、描く前の1コマで決め直します。
    //    幅が変わったときだけ動くので、高さを変えたこと自体では回りません（高さ→幅の輪にならない）。
    let viewRatioFrame = 0;
    const applyViewRatiosSoon = () => {
        if (viewRatioFrame) return;
        viewRatioFrame = requestAnimationFrame(() => { viewRatioFrame = 0; applyViewRatios(); });
    };
    window.addEventListener('resize', applyViewRatiosSoon);
    const viewContent = document.getElementById('w-editor-content');
    if (viewContent && typeof ResizeObserver === 'function') {
        let lastWidth = -1;
        new ResizeObserver(entries => {
            const w = Math.round(entries[0].contentRect.width);
            if (w === lastWidth) return;
            lastWidth = w;
            applyViewRatiosSoon();
        }).observe(viewContent);
    }

    function wireDrawingResize(wrap, embed) {
        // ⚠ **記録の無い枠は CSS の既定（70vh）のまま**（2026-09-29）。それまでは「最後につまんだ高さ」（UI設定の
        //    `drawing.height`）を全部の枠に当てていたので、1枚の大きさを変えると、開き直したときに同じページのほかの
        //    PDF まで同じ高さになった（利用者:「一枚のPDFの大きさを変えると、ページを再読み込みすると同じページの
        //    ほかのPDFの大きさも変わってしまいます」）。大きさは物ごとの縦横比だけが持つ（restoreViewState）。

        let dragging = false;
        wrap.addEventListener('pointerdown', (e) => {
            // **つまみの上で押したときだけ**反応します。PDFの中の操作（スクロール・
            // 拡大）を横取りしないためで、隅から離れた押下はそのまま通します。
            const r = wrap.getBoundingClientRect();
            if (e.clientX < r.right - DRAWING_GRIP || e.clientY < r.bottom - DRAWING_GRIP) return;
            dragging = true;
            // **引いているあいだ PDF へマウスを渡しません**——プラグインが受け取ると、
            // ポインタが枠の中へ入った瞬間にドラッグが切れます。
            embed.style.pointerEvents = 'none';
        });
        document.addEventListener('pointerup', () => {
            embed.style.pointerEvents = '';
            if (!dragging) return;
            dragging = false;
            const rect = wrap.getBoundingClientRect();
            const px = Math.round(rect.height);
            if (px >= DRAWING_H_MIN && px <= DRAWING_H_MAX) {
                // この枠の縦横比（物ごと・2026-09-28）。ほかの枠には使わない。
                const k = viewKeyOf(wrap);
                if (k && rect.width > 0) ViewState.setRatio(currentPageId, k, px / rect.width);
            }
        });
    }

    function toggleAttachPreview(anchor, btn, m) {
        const kind = m[3];
        const host = anchor.closest('p, li, td, dd') || anchor;
        const next = host.nextElementSibling;
        if (next && next.classList && next.classList.contains('attach-preview')) {
            next.remove();
            btn.textContent = kind === 'pdf' ? '▶ 表示' : '▶ 中身';
            return;
        }
        const wrap = document.createElement('div');
        wrap.className = 'vocab-chrome attach-preview';
        if (kind === 'pdf') {
            const embed = document.createElement('embed');
            embed.src = m[0]; // 正規表現に一致した href そのもの（導出の規律）
            embed.type = 'application/pdf';
            embed.className = 'attach-preview-pdf';
            wrap.appendChild(embed);
        } else {
            wrap.textContent = '読み込み中…';
            loadZipListInto(wrap, m[1], m[2] + '.zip');
        }
        host.insertAdjacentElement('afterend', wrap);
        btn.textContent = '▼ 閉じる';
    }

    async function loadZipListInto(wrap, pageId, file) {
        try {
            const res = await fetch('/api/zip-list?page_id=' + pageId + '&file=' + encodeURIComponent(file));
            const d = res.ok ? await res.json() : null;
            if (!d || !d.success) throw new Error('zip-list failed');
            wrap.textContent = '';
            if (!d.entries.length) { wrap.textContent = '（空のZIPです）'; return; }
            const ul = document.createElement('ul');
            ul.className = 'attach-zip-list';
            for (const e of d.entries) {
                const li = document.createElement('li');
                const name = document.createElement('span');
                name.textContent = e.name;
                const size = document.createElement('span');
                size.className = 'attach-zip-size';
                size.textContent = fmtBytes(e.size);
                li.appendChild(name);
                // 目録は読むだけ。中のファイルは取り込みが展開して添付にしているので、
                // 解析・表示はその添付のブロックから行う（2026-09-17）。
                li.appendChild(size);
                ul.appendChild(li);
            }
            if (d.truncated) {
                const li = document.createElement('li');
                li.textContent = '…他 ' + (d.total - d.entries.length) + ' 件';
                ul.appendChild(li);
            }
            wrap.appendChild(ul);
        } catch {
            wrap.textContent = 'ZIPの中身を読めませんでした。';
        }
    }

    function fmtBytes(n) {
        if (n < 1024) return n + ' B';
        if (n < 1024 * 1024) return (n / 1024).toFixed(1) + ' KB';
        return (n / (1024 * 1024)).toFixed(1) + ' MB';
    }

    // ── 形式ごとの視覚的区別（語彙モデル §10） ──────────────────────────────
    // 「`data-type` の改名を許す」という決定（§9）は、**見た目で形式を区別できること**を
    // 前提条件にしていた——「区別できるなら、変更者は壊れることを理解できる」（ユーザー）。
    // 一律の破線枠だけでは、改名しても見た目が変わらず、壊れたことに気づけない。
    //
    // そこで各形式ブロックの直前へ**形式名の札**を出す。札はレジストリ（vocabDefs）から
    // 引くので、**改名すると「未定義の形式」の赤い札に変わる**——その場で分かる。
    //
    // 札は `.vocab-chrome`（シリアライザが保存しない編集クローム）で、`contenteditable=false`。
    // 出すのは**編集モードだけ**（閲覧・公開ページには出さない。既存の視覚ヒントと同じ規律）。
    // **DOMを変えるのは必要なときだけ**（作り直し方式にしない）。本文DOMの変化は
    // MutationObserver → 自動保存 → buildToc → ここ、と巡ってくるので、毎回消して
    // 付け直すと変化が止まらず保存が回り続ける。既に正しい札はそのまま残す。
    function decorateVocabBlocks() {
        const editor = document.getElementById('w-editor-content');
        if (!editor) return;
        const isEdit = document.body.hasAttribute('edit-mode');

        // 対象＝本文の形式ブロック。クロームの中と、列型の明示（th/td の data-type）は除く。
        const attrTargets = isEdit ? Array.from(editor.querySelectorAll('[data-type]')).filter(el =>
            !el.closest('.vocab-chrome') && el.tagName !== 'TH' && el.tagName !== 'TD')
            .map(el => ({ el, type: el.getAttribute('data-type') })) : [];

        // **機械が読む言葉へ薄い青の背景**（D-3「認識できた形式は背景色で示す」）——2026-09-29 からは
        // **表の名前（caption）**。それまでは節の見出しの言葉に付けていたが、見出しでは名乗れなくなった
        // （DBの日本語化 5段目の4）。改名すると背景が消える——「機械が読まなくなった」の合図。
        const captionMarked = new Set();
        if (isEdit) {
            editor.querySelectorAll('table > caption').forEach(cap => {
                if (cap.closest('.vocab-chrome')) return;
                const w = cap.textContent.trim();
                if (vocabDefs.find(v => !v.view && v.display_name === w)) {
                    cap.classList.add('vocab-word');
                    captionMarked.add(cap);
                }
            });
        }
        // **札は出さない**（2026-08-31 ユーザー2件:「そんなものを出さなくても
        // 見分けがつく」「薄青にならないものはデータベースに入らないという程度の
        // 認識で良い」）。識別はすべて**背景色**へ一本化した:
        //   薄い青 … 機械が読む（見出し語のハイライト・可変タグのチップの名前側）
        //   薄い赤 … 属性はあるのに未定義（保存はされるが解釈されない。改名・綴り違い）
        //   無印   … ただの文書（機械は解釈しない）
        // 既知の属性形式（可変タグ・添付）は姿そのもの（チップ・PDFクローム）が識別。
        attrTargets.forEach(({ el, type }) => {
            const retired = isRetiredViewMarker(type);
            const unknown = retired || !vocabDefs.find(v => v.type === type);
            el.classList.toggle('is-vocab-unknown', unknown);
            if (retired) {
                const def = vocabDefs.find(v => v.type === type);
                el.title = '名前の見えない鏡の印「' + type + '」は廃止しました——描かれません。' +
                    'スラッシュメニューで「' + def.display_name + '」の鏡を入れ直してください。';
            } else if (unknown) {
                el.title = '未定義の形式「' + type + '」——保存はされますが、計算には使われません。';
            } else if (el.title && (el.title.indexOf('未定義の形式') === 0 ||
                                    el.title.indexOf('名前の見えない鏡の印') === 0)) {
                el.removeAttribute('title');
            }
        });

        // 鏡の印（data-mirror="表示名"）の名前が鏡として登録されていなければ赤
        // （打ち間違い・2026-09-27）。保存の告知（unknown_types）と同じ判定。
        const mirrorTargets = isEdit ? Array.from(editor.querySelectorAll('section[data-mirror]'))
            .filter(el => !el.closest('.vocab-chrome')) : [];
        mirrorTargets.forEach(el => {
            const name = el.getAttribute('data-mirror');
            const unknown = !mirrorDefOf(name);
            el.classList.toggle('is-vocab-unknown', unknown);
            if (unknown) {
                el.title = '鏡「' + name + '」は登録されていません——描かれません。綴りを確かめてください。';
            } else if (el.title && el.title.indexOf('鏡「') === 0) {
                el.removeAttribute('title');
            }
        });

        // 対象でなくなった赤い印を片付ける（形式を外した・宣言を足した）。
        const attrSet = new Set(attrTargets.map(x => x.el).concat(mirrorTargets));
        editor.querySelectorAll('.is-vocab-unknown').forEach(el => {
            if (!attrSet.has(el)) el.classList.remove('is-vocab-unknown');
        });
        // 旧仕様の札が残っていれば片付ける（この関数が唯一の作り手だった）。
        editor.querySelectorAll('.vocab-label').forEach(el => el.remove());

        // 名前を消した・別の言葉へ変えた表（と、見出しに付けていた 2026-09-29 までの印）を片付ける。
        editor.querySelectorAll('.vocab-word').forEach(el => {
            if (!captionMarked.has(el)) el.classList.remove('vocab-word');
        });

        if (!isEdit) {
            editor.querySelectorAll('.is-vocab-unknown').forEach(el => el.classList.remove('is-vocab-unknown'));
        }

        // ファイル表示の配線の札も同じ巡りで面倒を見る（呼び出し口を増やさない）。
        decorateFileViews();
        decorateCarousels(); // カルーセルの帯（編集）と送る道具（閲覧）も同じ巡りで（2026-10-07）
        // 表の名前・見出しの SQL の問題個所の薄赤も同じ巡りで（2026-09-28）。
        markSqlNames();
        // セルの印（折り返し・型の読めた／読めない）も同じ巡りで付け直す。
        //
        // ⚠ **閲覧モードでも要ります。** 2026-09-21 まで `validateTypedTables` は
        // **編集モードに入るときだけ**呼ばれていました——CSS 側には
        // 「編集・閲覧の両モードで効かせる」と書いてあったのに、閲覧で開き直すと
        // 印が付かず、**表が全部折り返して**いました。一度編集へ入ると直るので、
        // 「たまに直っている」という分かりにくい形でした。
        // **変化が無ければ DOM を触らない**（classList.toggle は同じ値なら無変化）ので、
        // ここから呼んでも「変える→観測→また変える」の輪にはなりません。
        validateTypedTables();
    }

    // uploadPDFToSection はPDFを添付し、配線（data-src）と可視のファイル名リンクを更新する。
    async function uploadPDFToSection(sec, file) {
        if (!currentPageId) { notify('先にページを保存してください。', { type: 'warn', duration: 5000 }); return; }
        const fd = new FormData();
        fd.append('page_id', currentPageId);
        fd.append('pdf_file', file);
        try {
            // 送る前に書きかけを保存する（lockedFetch）。2026-10-12 までは本文編集と同じ編集ロックで直列化していた。
            // いまはブラウザはロックを取らない（サーバーが 409 で断るのは、道具が /api/lock で握っているときだけ）。
            const res = await lockedFetch('/api/upload-pdf', { method: 'POST', body: fd });
            const d = await readResult(res);
            if (!res.ok || !d.success) throw new Error(failMessage(res, d));
            // 配線（data-src）を持つのは互換の file 容器だけ。File 宣言の形式ブロックでは
            // 可視のファイル名リンクが唯一の所在（見える文字がデータの手掛かり）。
            if (sec.getAttribute('data-type') === 'file') sec.setAttribute('data-src', d.src);
            // 可視のファイル名リンク（中身＝正本に保存される）を先頭に置き直す
            let p = sec.querySelector(':scope > p');
            if (!p || !p.querySelector('a')) {
                p = document.createElement('p');
                sec.insertBefore(p, sec.firstChild);
            }
            p.textContent = '📎 ';
            const a = document.createElement('a');
            // 置き場の知識はサーバーが持つ（href は /<ページID>/<生成名>）。
            a.href = d.href || ('/data/master/' + currentPageId.substring(0, 2) + '/' + currentPageId + '/' + d.src);
            a.download = file.name; // 保存名の既定は元の名前
            a.textContent = file.name;
            p.appendChild(a);
            enhanceFileSections();
            updateHtmlPreview();
            notify('PDFを添付しました。', { type: 'success', duration: 3000 });
        } catch (e) {
            notify('PDFのアップロードに失敗しました: ' + e.message, { type: 'warn', duration: 8000 });
        }
    }

    // ── 画像の添付と挿入（要件定義書 §2.6） ──────────────────────────────
    // 入口は3つ（スラッシュメニュー／本文へのドロップ／image 列のセル）だが、
    // 送る先も挿す形も同じなので、この2関数に集約する。
    //
    // 検査（中身のマジックナンバー・拡張子との一致・SVGの危険な記述）と EXIF の除去は
    // **すべてサーバー側**（/api/upload-image）が行う。ここで種類を絞り込みすぎると
    // 「なぜ入らないのか」が分からなくなるので、選ばせるのは image/* に留め、
    // 拒否の理由はサーバーの文言をそのまま見せる（HEIC は撮り直し方まで返る）。

    // pickImageFiles は OS のファイル選択を開きます。
    // accept="image/*" にしておくと、モバイルでは OS 標準のカメラUI（「写真を撮る」）が
    // 選べます——独自のカメラ画面は持ちません（要件 §2.6）。
    function pickImageFiles(onPick) {
        const input = document.createElement('input');
        input.type = 'file';
        input.accept = 'image/*';
        input.multiple = true;
        input.addEventListener('change', () => onPick(Array.from(input.files || [])));
        input.click();
    }

    // uploadImageFile は画像を添付し、本文の <img src> へ入れる絶対パスを返します。
    // 失敗したら理由を告知して null を返します。
    async function uploadImageFile(file) {
        if (!currentPageId) {
            notify('先にページを保存してください。', { type: 'warn', duration: 5000 });
            return null;
        }
        const fd = new FormData();
        fd.append('page_id', currentPageId);
        fd.append('image_file', file);
        try {
            // 送る前に書きかけを保存する（lockedFetch）。2026-10-12 までは本文編集と同じ編集ロックで直列化していた
            // （いまサーバーが 409 で断るのは、道具が /api/lock で握っているときだけ）。
            const res = await lockedFetch('/api/upload-image', { method: 'POST', body: fd });
            if (!res.ok) {
                const msg = (await res.text().catch(() => '')).trim();
                notify(msg || ('画像を保存できませんでした（' + res.status + '）'),
                    { type: 'alert', duration: 0, id: 'image-upload' });
                return null;
            }
            const d = await res.json();
            return d.src ? d : null;
        } catch (e) {
            notify('画像のアップロードに失敗しました: ' + e.message, { type: 'warn', duration: 8000 });
            return null;
        }
    }

    // insertImagesAfter は選ばれた画像を順に添付し、refBlock の後ろへ1枚ずつ挿します。
    // alt にはファイル名を入れる（読み上げと、画像が出ないときの手掛かり）。
    async function insertImagesAfter(files, refBlock) {
        // 節・折りたたみの中の要素の後ろへ入れるときは包まない（中の要素は包まない・2026-09-29）。
        const nested = !!(refBlock && refBlock.classList && !refBlock.classList.contains('editor-block'));
        let last = refBlock;
        for (const f of files) {
            const up = await uploadImageFile(f);
            if (!up) continue;
            const p = document.createElement('p');
            if (up.id) p.setAttribute('data-id', up.id); // 添付ID＝ブロックID
            const img = document.createElement('img');
            img.src = up.src;
            img.alt = f.name;
            p.appendChild(img);
            const block = nested ? p : wrapInBlock(p);
            if (last && last.parentNode) {
                last.parentNode.insertBefore(block, last.nextSibling);
            } else {
                document.getElementById('w-editor-content').appendChild(block);
            }
            last = block;
        }
        updateHtmlPreview();
        triggerAutoSave();
        return last; // 続けて足すもの（📎 で画像と一緒に選んだファイル）はこの後ろへ
    }

    // insertImageIntoCell は image 列のセルへ画像を1枚入れます（差し替えは上書き）。
    async function insertImageIntoCell(cell, file) {
        const up = await uploadImageFile(file);
        const src = up && up.src;
        if (!src) return;
        cell.textContent = '';
        const img = document.createElement('img');
        img.src = src;
        img.alt = file.name;
        cell.appendChild(img);
        updateHtmlPreview();
        triggerAutoSave();
    }

    // ── スラッシュメニュー（絞り込み・分類・頻度順。エディタ仕様 §3.1） ──────
    //
    // 語彙は最終的に100種規模になる（要件定義書 §1.2）ので、全項目を1列に並べる形では
    // 選べません。到達性のために3つを効かせます:
    //
    //   絞り込み … `/` に続けて打った文字で候補を絞る（表示名・形式名・分類を見る）
    //   分類     … レジストリの `category` で束ね、見出しを出す
    //   頻度順   … 使った回数の多い項目を**その分類の先頭**へ寄せる
    //
    // 絞り込みの入力欄は**置きません**。`/` を打ったブロックの文字がそのまま検索語に
    // なります（`/部材` のように打つ）——入力欄へフォーカスを移すと contenteditable の
    // キャレットが失われ、選んだあとの挿入先が分からなくなるためです。
    //
    // 分類の並びは固定（宣言順）で、**動くのは分類の中だけ**にしています。分類ごと
    // 動かすと「昨日と場所が違う」状態になり、覚えて選ぶ使い方ができなくなるためです。

    // slashItems はメニューの項目一覧（描画のもとになるデータ）。
    // 殻（index.html の #w-slash-menu）に手書きされている項目は「基本」分類として最初に取り込みます。
    // いま手書きなのは h1〜h3・p・見出しの節・折りたたみ・画像・ファイルの7つ（エディタ仕様 §3.1 のころは h1・p の
    // 2つだけでした）。表・定義リストはここで足し、語彙の形式はレジストリ（vocabDefs）から足します。
    let slashItems = [];
    let slashQuery = '';

    function buildSlashItems() {
        const menu = document.getElementById('w-slash-menu');
        if (!menu) return;
        const items = [];
        menu.querySelectorAll('.slash-menu-item').forEach(el => {
            items.push({
                type: el.getAttribute('data-type'),
                icon: (el.querySelector('b') || {}).textContent || '📄',
                label: el.textContent.replace(/^\s*\S+\s*/, '').trim() || el.textContent.trim(),
                category: '基本',
            });
        });
        // 汎用の表・定義リスト（機械語ゼロの骨組み。型は見出しと th の言葉が担う）
        items.push({ type: 'table', icon: '▦', label: '表', category: '基本', alt: 'table' });
        items.push({ type: 'dl', icon: '≡', label: '定義リスト（名前：値）', category: '基本', alt: 'dl' });
        vocabDefs.forEach(def => {
            if (def.hidden) return; // 明細表など、単独では挿入しない形式
            items.push({
                type: 'vocab:' + def.type,
                icon: def.icon || '📄',
                label: def.display_name || def.type,
                category: def.category || 'その他',
                // 形式名（kebab-case）でも引けるようにする。日本語入力に切り替える前に
                // ローマ字で打ち始める使い方を拾うため。
                alt: def.type,
            });
        });
        // 基本は決まった並び（下の slashBasicOrder）——載っていないものは後ろへ、もとの順のまま。
        const order = slashBasicOrder();
        const rank = it => { const i = order.indexOf(it.type); return i < 0 ? order.length : i; };
        const basic = items.filter(it => it.category === '基本').sort((a, b) => rank(a) - rank(b));
        slashItems = basic.concat(items.filter(it => it.category !== '基本'));
    }

    // slashBasicOrder は「基本」の並びです（2026-10-01 利用者:「スラッシュメニューは見出し1－3は並んでいて欲しいですね。
    // その下に段落などでしょうか。タグも良く使うのでメニューの上の方に出ていて欲しいです」）。**基本は使った回数で並べ替えない**
    // ——並べ替えると見出し1〜3が離れ、覚えた場所が動く。ほかの分類はこれまでどおり、使った回数の多い順。
    // ⚠ 関数にしてある（定数を外に置くと、buildSlashItems が先に呼ばれたときに使えない）。
    function slashBasicOrder() {
        return ['h1', 'h2', 'h3', 'p', 'vocab:tags', 'attach', 'vocab:file-view', 'vocab:carousel', 'table', 'dl', 'section', 'details'];
    }

    // slashUseCount は使った回数（UI ストアに永続化）。
    function slashUseCount(type) {
        const uses = UI.get('slashUse', {});
        return uses[type] || 0;
    }
    function noteSlashUse(type) {
        const uses = UI.get('slashUse', {});
        uses[type] = (uses[type] || 0) + 1;
        UI.set('slashUse', uses);
    }

    // slashMatches は絞り込み語に当たるかを返します。表示名・形式名・分類のどれかに
    // 含まれていれば当たり（部分一致・英字は大小を無視）。
    function slashMatches(item, q) {
        if (!q) return true;
        const hay = (item.label + ' ' + (item.alt || '') + ' ' + item.category + ' ' + item.type).toLowerCase();
        return hay.indexOf(q.toLowerCase()) !== -1;
    }

    // renderSlashMenu はメニューを組み直します（分類の見出し＋項目）。
    function renderSlashMenu() {
        const menu = document.getElementById('w-slash-menu');
        if (!menu) return;
        menu.textContent = '';

        const q = slashQuery.trim();
        // 分類の並びは宣言順（＝安定）。中身だけを頻度順に並べ替える。
        const order = [];
        const groups = {};
        slashItems.forEach(item => {
            if (!slashMatches(item, q)) return;
            if (!groups[item.category]) { groups[item.category] = []; order.push(item.category); }
            groups[item.category].push(item);
        });

        if (order.length === 0) {
            const empty = document.createElement('div');
            empty.className = 'slash-menu-empty';
            empty.textContent = '「' + q + '」に当たる形式はありません';
            menu.appendChild(empty);
            return;
        }

        order.forEach(cat => {
            const head = document.createElement('div');
            head.className = 'slash-menu-group';
            head.textContent = cat;
            menu.appendChild(head);

            groups[cat]
                .slice()
                // 基本は決まった並びのまま（slashBasicOrder）。ほかの分類は使った回数の多い順。
                .sort((a, b) => cat === '基本' ? 0 : slashUseCount(b.type) - slashUseCount(a.type))
                .forEach(item => {
                    const el = document.createElement('div');
                    el.className = 'slash-menu-item';
                    el.setAttribute('data-type', item.type);
                    const icon = document.createElement('b');
                    icon.textContent = item.icon;
                    el.appendChild(icon);
                    el.appendChild(document.createTextNode(' ' + item.label));
                    el.addEventListener('click', () => chooseSlashItem(item.type));
                    el.addEventListener('mouseenter', () => {
                        slashSelectedIndex = visibleSlashItems().indexOf(el);
                        updateSlashMenuSelection();
                    });
                    menu.appendChild(el);
                });
        });
    }

    // visibleSlashItems はいま並んでいる項目（分類見出しを除く）を返します。
    function visibleSlashItems() {
        return Array.from(document.querySelectorAll('#w-slash-menu .slash-menu-item'));
    }

    // chooseSlashItem は項目を選んだときの共通処理です。
    // **絞り込みに打った文字（`/部材` 等）を本文へ残さない**のがここの仕事——
    // 置き換え型の項目では消えますが、画像のように「後ろへ挿す」項目では残ってしまいます。
    function chooseSlashItem(type) {
        const block = currentSlashBlock;
        const nested = currentSlashNested;
        noteSlashUse(type);
        hideSlashMenu();
        // 節・折りたたみの中の段落から開いたときは、その段落1つだけを置き換える（2026-09-29）。
        if (nested && nested.isConnected) {
            if (/^\/.*/.test(nested.textContent || '')) nested.textContent = '';
            applyNestedChoice(type, nested);
            return;
        }
        if (!block) return;
        const content = block.querySelector('.block-content');
        const el = content && content.firstElementChild;
        if (el && /^\/.*/.test(el.textContent || '')) el.textContent = '';
        applySlashChoice(type, block);
    }

    // populateSlashMenuVocab は語彙レジストリから項目を作り、メニューを組み立てます。
    // 名前は従来のまま（呼び出し側の意味は「メニューを用意する」）。
    function populateSlashMenuVocab() {
        buildSlashItems();
        renderSlashMenu();
    }

    // Slash Menu Logic
    function showSlashMenu(targetElement) {
        slashMenuVisible = true;
        const menu = document.getElementById('w-slash-menu');
        currentSlashBlock = targetElement.closest('.editor-block');
        currentSlashNested = innerBoxOf(targetElement) ? targetElement : null;
        // 開くたびに絞り込みは白紙へ戻し、頻度順も取り直す。
        slashQuery = '';
        renderSlashMenu();
        menu.classList.add('active');

        slashAnchor = targetElement;
        placeSlashMenu();

        slashSelectedIndex = 0;
        updateSlashMenuSelection();
    }

    // placeSlashMenu はメニューを、キャレットの段落の下（入らなければ上）の**見えている範囲**に置く（2026-10-07 利用者:
    // 「ブロック挿入のメニューは下層キーボードに隠れるので、これも何とかしたいです」）。見えている範囲は visualViewport
    // （スマホではキーボードの分だけ縮む）で、スマホの帯（画面の上）とも重ねない。高さは入る分に縮めて、中を巻物にする。
    // キーボードが出入りしたら置き直す（下の visualViewport の聞き手）。
    let slashAnchor = null;
    function placeSlashMenu() {
        const menu = document.getElementById('w-slash-menu');
        if (!menu || !slashMenuVisible || !slashAnchor || !slashAnchor.isConnected) return;
        const rect = slashAnchor.getBoundingClientRect();
        const vv = window.visualViewport;
        const vTop = vv ? vv.offsetTop : 0;
        const vBottom = vTop + (vv ? vv.height : window.innerHeight);
        const bar = document.getElementById('w-context-toolbar');
        // スマホの帯（画面の上のドック）の分を空ける——**帯がいま出ていなくても**（「＋」から開いた直後は帯が一度消えてから出直す
        // ことがあり、そのときに置くとメニューが帯に重なっていた——2026-10-12。帯が出直したら置き直す——updateContextToolbar）。
        const docked = !!bar && (bar.classList.contains('is-docked') || window.matchMedia('(pointer: coarse)').matches);
        const reserve = docked ? Math.max(bar.offsetHeight || 0, 48) + 8 : 0;
        const gap = 5, pad = 8, least = 120;
        menu.style.maxHeight = '';
        const natural = Math.min(menu.scrollHeight, 300);
        // メニューは帯の下端（topLimit）より上に出さない——段落が帯の下に隠れているときも（2026-10-12——段落のすぐ下に置いて帯に重なっていた）。
        const topLimit = vTop + reserve;
        const startBelow = Math.max(rect.bottom + gap, topLimit);
        const below = vBottom - startBelow - pad;
        const above = rect.top - topLimit - gap - pad;
        const downward = below >= natural || below >= above;
        const h = Math.max(least, Math.min(natural, downward ? below : above));
        menu.style.maxHeight = h + 'px';
        placeFloating(menu, rect, gap); // 左右の寄せ
        menu.style.top = ((downward ? startBelow : Math.max(rect.top - gap - h, topLimit)) + window.scrollY) + 'px';
    }
    if (window.visualViewport) {
        window.visualViewport.addEventListener('resize', placeSlashMenu);
        window.visualViewport.addEventListener('scroll', placeSlashMenu);
    }
    // ページが巻き戻っても置き直す——「＋」で足した段落へフォーカスするとブラウザがそこまで巻き、ページに置いたメニューだけが
    // 一緒に上がって画面の上の帯に重なっていた（2026-10-12）。
    window.addEventListener('scroll', () => { if (slashMenuVisible) placeSlashMenu(); }, { passive: true });

    // filterSlashMenu は `/` に続けて打たれた文字で候補を絞ります。
    function filterSlashMenu(query) {
        slashQuery = query;
        renderSlashMenu();
        slashSelectedIndex = 0;
        updateSlashMenuSelection();
        placeSlashMenu(); // 絞ると高さが変わる——上に置いていたら段落から離れる
    }

    function hideSlashMenu() {
        slashMenuVisible = false;
        slashQuery = '';
        slashAnchor = null;
        const menu = document.getElementById('w-slash-menu');
        if (menu) menu.classList.remove('active');
        currentSlashBlock = null;
        currentSlashNested = null;
    }

    function updateSlashMenuSelection() {
        visibleSlashItems().forEach((item, idx) => {
            if (idx === slashSelectedIndex) {
                item.classList.add('selected');
                item.scrollIntoView({ block: 'nearest' });
            } else {
                item.classList.remove('selected');
            }
        });
    }

    function handleSlashMenuKey(e) {
        const items = visibleSlashItems();
        if (e.key === 'ArrowDown') {
            e.preventDefault();
            if (!items.length) return;
            slashSelectedIndex = (slashSelectedIndex + 1) % items.length;
            updateSlashMenuSelection();
        } else if (e.key === 'ArrowUp') {
            e.preventDefault();
            if (!items.length) return;
            slashSelectedIndex = (slashSelectedIndex - 1 + items.length) % items.length;
            updateSlashMenuSelection();
        } else if (e.key === 'Enter') {
            e.preventDefault();
            if (!items[slashSelectedIndex]) return;
            chooseSlashItem(items[slashSelectedIndex].getAttribute('data-type'));
        } else if (e.key === 'Escape') {
            hideSlashMenu();
        }
    }

    function setupEditorEvents() {
        const editor = document.getElementById('w-editor-content');

        // レジストリ由来の項目は、下の項目バインド（click / mouseenter）より前に足す。
        populateSlashMenuVocab();
        // 節・折りたたみの中の要素の ⠿・🗑・＋（2026-09-29）。
        setupInnerBlocks();

        // ── 折りたたみの題（summary）を打てるように（2026-09-28） ──
        // ⚠ ブラウザは summary を押すと開閉します——編集モードで題の文字を押すたびに閉じ、
        //    中の「＋ ファイル」の札まで隠れていました（E2E で踏んだ）。**左端の ▸ を押したときだけ**
        //    開閉し、文字の上ではキャレットを置くだけにします。空白は開閉しません（確かめた）。
        // ⚠ Enter は題の中へ改行を入れず、**中身の先頭へ移ります**（行を分ける編集の Enter が
        //    折りたたみごと割らないように・捕捉の段で先に止める）。
        const FOLD_MARKER_PX = 20; // ▸ の幅のめやす
        editor.addEventListener('click', e => {
            if (!document.body.hasAttribute('edit-mode')) return;
            const s = foldSummaryOf(e.target);
            if (!s) return;
            if (e.clientX - s.getBoundingClientRect().left > FOLD_MARKER_PX) e.preventDefault();
        });
        editor.addEventListener('keydown', e => {
            if (e.key !== 'Enter' || e.isComposing || !document.body.hasAttribute('edit-mode')) return;
            const sel = window.getSelection();
            const s = sel && sel.rangeCount ? foldSummaryOf(sel.anchorNode) : null;
            if (!s) return;
            e.preventDefault();
            e.stopPropagation();
            const fold = s.parentElement;
            fold.open = true;
            let target = s.nextElementSibling;
            while (target && target.classList.contains('vocab-chrome')) target = target.nextElementSibling;
            if (!target) {
                target = document.createElement('p');
                target.appendChild(document.createElement('br'));
                fold.appendChild(target);
            }
            const range = document.createRange();
            range.selectNodeContents(target);
            range.collapse(true);
            sel.removeAllRanges();
            sel.addRange(range);
        }, true);

        initFixedBar(); // パソコンでは3つの帯を本文の上に固定する（2026-10-19）

        // 行操作ツールバーのボタン。mousedown を止めないとクリックで選択が崩れ、
        // どの行への操作か（currentTableRow）が失われる。
        const tbar = document.getElementById('w-table-toolbar');
        if (tbar) {
            tbar.addEventListener('mousedown', e => e.preventDefault());
            makeToolbarDraggable(tbar, 'table', updateTableToolbar);
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
            makeToolbarDraggable(dbar, 'dl', updateDlToolbar);
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

        // リンクのプロパティ欄。打つそばから本文のリンクへ反映する（列型と同じ流儀で、
        // 「適用」ボタンは置かない）。
        const lpHref = document.getElementById('w-lp-href');
        if (lpHref) lpHref.addEventListener('input', applyLinkHref);
        const lpUnlink = document.getElementById('w-lp-unlink');
        if (lpUnlink) {
            // 押した瞬間に選択が飛ぶと対象を見失うため、装飾ボタンと同じく mousedown を止める。
            lpUnlink.addEventListener('mousedown', e => e.preventDefault());
            lpUnlink.addEventListener('click', e => { e.preventDefault(); unlinkCurrent(); });
        }

        // ファイル表示の参照欄。打つそばからブロックの配線へ反映する（リンクと同じ流儀）。
        const fvRef = document.getElementById('w-fv-ref');
        if (fvRef) fvRef.addEventListener('input', applyFileViewRef);

        // enum の選択肢メニュー。クリックで選択が飛ぶと対象セルを見失うため mousedown を止める。
        const emenu = document.getElementById('w-enum-menu');
        if (emenu) emenu.addEventListener('mousedown', e => e.preventDefault());
        // 打つたびに、候補をいまの字で絞る（オートコンプリート・2026-10-19——filterEnumMenu）。
        editor.addEventListener('input', () => { if (enumMenuCell) filterEnumMenu(); });

        editor.addEventListener('input', (e) => {
            if (!document.body.hasAttribute('edit-mode')) return;
            const target = e.target;
            // `/` で開き、**続けて打った文字はそのまま絞り込み語**になる
            // （入力欄を置かないのは、フォーカスを移すとキャレットを失うため）。
            // 節・折りたたみの中の段落でも開く（2026-09-29）——そこでは編集の単位が入れ物なので、入力の
            // 相手（target）は入れ物になる。キャレットのある段落を探す。選んだものはその段落1つを置き換える。
            let para = target.tagName === 'P' ? target : null;
            if (!para) {
                const sel0 = window.getSelection();
                const n0 = sel0 && sel0.anchorNode;
                const el0 = n0 && (n0.nodeType === 1 ? n0 : n0.parentElement);
                const p0 = el0 && el0.closest && el0.closest('p');
                if (p0 && innerBoxOf(p0)) para = p0;
            }
            const text = para ? (para.innerText || '') : '';
            // 全角の「／」でも開く（2026-10-07 利用者:「スラッシュを入力するのは、日本語環境ではめんどう」）。
            // ⚠ 「・」（日本語入力の / キー）では開かない——「・材料」のような箇条書きで Enter を押すと、表に化ける。
            //    打たずに開く道は帯の「＋」（showContextToolbarForBlock）。
            if (text.charAt(0) === '/' || text.charAt(0) === '／') {
                if (!slashMenuVisible) showSlashMenu(para);
                filterSlashMenu(text.slice(1));
            } else {
                hideSlashMenu();
            }

            // 型検証（通知のみ）。編集中のセルだけを見る（全表の再検証は入力ごとには重い）。
            // 見出し形（D-2）の素の表も対象——列の解決は resolveCellColumn が文脈から行う。
            const sel = window.getSelection();
            const node = sel && sel.anchorNode;
            const el = node ? (node.nodeType === Node.TEXT_NODE ? node.parentElement : node) : null;
            const cell = el && el.closest ? el.closest('#w-editor-content table td') : null;
            if (cell && !isServerOwned(cell)) validateCell(cell);
        });

        editor.addEventListener('keydown', (e) => {
            if (!document.body.hasAttribute('edit-mode')) return;

            // アンドゥ・リドゥ（Ctrl+Z・Ctrl+Y）は setupHistoryHooks が document で横取りする（本文の外にフォーカスがあっても
            // 効くように・2026-10-12）——ブラウザ標準の undo と二重にならないよう preventDefault もそちら。
            if ((e.ctrlKey || e.metaKey) && !e.altKey) {
                const key = e.key.toLowerCase();

                // Ctrl+B/I/U はブラウザ標準だと execCommand が走り、実装ごとに違うHTML
                // （<b> か style="font-weight:bold" か）を吐く。必ず横取りして自前実装へ回す。
                const fmt = { b: 'bold', i: 'italic', u: 'underline' }[key];
                if (fmt && !e.shiftKey) { e.preventDefault(); toggleInlineFormat(fmt); return; }
            }

            // スラッシュメニューが開いているあいだは、矢印・Enter・Escape はメニューの操作
            // ——節・折りたたみの中の段落から開いたときは入力の相手が入れ物（節）なので、下の段落の
            // 判定より先に受ける（2026-09-29 に E2E で踏んだ——Enter が節の中の改行になっていた）。
            if (slashMenuVisible && ['ArrowUp', 'ArrowDown', 'Enter', 'Escape'].includes(e.key)) {
                handleSlashMenuKey(e);
                return;
            }

            const target = e.target;
            if (target.tagName !== 'P' && target.tagName !== 'H1' && target.tagName !== 'H2' && target.tagName !== 'H3') {
                return; // Ignore inputs inside web components
            }

            const block = target.closest('.editor-block');
            if (!block) return;

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
            }
            // ↑ ↓ ← → でブロックの端を越えるのは setupArrowNavigation（2026-10-12 午後——ここで ↑ ↓ のたびに前・次のブロックへ
            // フォーカスを移していたのはやめた）。
        });

        document.addEventListener('click', (e) => {
            // ⚠ 中の要素の帯（＋）はスラッシュメニューを開く側——閉じない（2026-09-29 に E2E で踏んだ）。
            // 帯（文字の帯）の「＋」も開く側（2026-10-07）。
            if (!e.target.closest('#w-slash-menu') && !e.target.closest('.editor-block') &&
                !e.target.closest('#w-inner-controls') && !e.target.closest('#w-ctx-add')) {
                hideSlashMenu();
            }
        });

        document.addEventListener('selectionchange', () => {
            if (slashMenuVisible) return;
            // 列設定ポップオーバの操作中（select へのフォーカス移動）は、キャレットが
            // 表から出たと誤認してツールバーごと消してしまうため更新しない。
            if (document.activeElement && document.activeElement.closest &&
                document.activeElement.closest('#w-col-popover')) return;
            // リンクのプロパティ欄も同じ理由で、欄へフォーカスがある間は更新しない
            // （href を打っている最中にキャレットが本文から出たと誤認して閉じる）。
            if (document.activeElement && document.activeElement.closest &&
                document.activeElement.closest('#w-link-popover')) return;
            // ファイル表示の参照を打っている最中も同じ（欄はキャレットを本文から奪う）。
            if (document.activeElement && document.activeElement.closest &&
                document.activeElement.closest('#w-fv-popover')) return;
            updateContextToolbar();
            updateTableToolbar();
            updateDlToolbar();
            updateEnumMenu();
            updateLinkPopover();
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
            // リンクのプロパティ欄は「リンク以外を押したら閉じる」。**本文の中を押した
            // ときも閉じる**必要がある——href の欄にフォーカスがある間 selectionchange は
            // 早期 return するので、キャレットが本文へ戻ったことに気づけない。
            if (!e.target.closest('#w-link-popover') && !e.target.closest('#w-context-toolbar') &&
                !e.target.closest('a')) {
                hideLinkPopover();
            }
            // ファイル表示の欄も同じ規律（札か欄の外を押したら閉じる）。
            if (!e.target.closest('#w-fv-popover') && !e.target.closest('.fv-wire')) {
                hideFileViewPopover();
            }
        });

        // 本文へ画像ファイルをドロップしたら添付して挿す（要件 §2.6 の入口の1つ）。
        // ファイル容器のドロップゾーン（PDF）はそちらが処理するので手を出さない。
        editor.addEventListener('dragover', e => {
            if (!document.body.hasAttribute('edit-mode')) return;
            if (e.target.closest && e.target.closest('.pdf-drop-zone')) return;
            if (!Array.from(e.dataTransfer.types || []).includes('Files')) return;
            e.preventDefault();
        });
        editor.addEventListener('drop', e => {
            if (!document.body.hasAttribute('edit-mode')) return;
            if (e.target.closest && e.target.closest('.pdf-drop-zone')) return;
            const all = Array.from((e.dataTransfer && e.dataTransfer.files) || []);
            if (!all.length) return;
            e.preventDefault();
            // 種類で口を分ける——画像は画像口（EXIF除去・中身検査）、PDFはPDF口
            // （%PDF-検査）、それ以外は汎用口（拡張子は設定・中身は配信側が守る）。
            // 「添付はドラッグアンドドロップに耐えられた方が良い」（2026-08-31）。
            // 2026-09-30 から、画像は本文の中の絵・それ以外はファイル表示の印（書式の帯の 📎 と同じ・
            // insertFilesAfter）。**落とした所のすぐ後ろ**——節・折りたたみの中なら中のまま。
            const images = all.filter(f => f.type.indexOf('image/') === 0);
            const others = all.filter(f => f.type.indexOf('image/') !== 0);
            const cell = e.target.closest && e.target.closest('#w-editor-content td');
            const col = cell && resolveCellColumn(cell);
            if (images.length && cell && col && col.type === 'image' && !isServerOwned(cell)) {
                insertImageIntoCell(cell, images[0]); // 表のセルへは1枚だけ
                // ⚠ セルへ入れた画像を本文へ二重に入れない（09-30 まで、ほかのファイルと一緒に落とすと入っていた）。
                if (others.length) insertFileViewsAfter(others, fileInsertPointAt(e.target));
                return;
            }
            insertFilesAfter(all, fileInsertPointAt(e.target));
        });

        // 画像を貼り付ける（2026-10-01 利用者:「画像をペーストできるようにしたい」）——画面の切り取り・「画像をコピー」を
        // Ctrl+V で。ドロップと同じ道（insertFilesAfter）で添付にし、**キャレットのすぐ後ろ**へ置く（節・折りたたみの中なら
        // 中のまま）。画像の列のセルなら、そのセルへ1枚。
        // ⚠ **文字があるときは横取りしない**——Excel のセルや Word の文をコピーすると、文字と一緒に「見た目の画像」も
        //    クリップボードに載るので、画像を優先すると文字の貼り付けが絵に化ける。文字が無いときだけ（画像だけのとき）扱う。
        // ⚠ 切り取りの画像は名前が「image.png」なので、貼った日時の名前に付け替える（添付の目録・alt に出る名前）。
        editor.addEventListener('paste', e => {
            if (!document.body.hasAttribute('edit-mode')) return;
            const cd = e.clipboardData;
            if (!cd) return;
            // **名前：値（タグ・定義リスト）の中は文字だけ**（2026-10-01 利用者:「名前値のタグを編集するとき、HTMLのような
            // 構造がコピペされるとレイアウトが崩れます。そこで、タグの中でコピペするときは、文字列だけにしましょう」）——
            // 書式・表・段落を持ち込まず、改行とタブは空白1つに畳む（値は1行）。ファイルだけのときは下の道へ。
            const selNow = window.getSelection();
            const atNow = selNow && selNow.rangeCount ? selNow.anchorNode : null;
            const elNow = atNow && (atNow.nodeType === 1 ? atNow : atNow.parentElement);
            const inPair = elNow && elNow.closest('#w-editor-content dl dt, #w-editor-content dl dd');
            const plain = cd.getData('text/plain') || '';
            if (inPair && plain.trim()) {
                e.preventDefault();
                document.execCommand('insertText', false, plain.replace(/\s*[\r\n\t]+\s*/g, ' ').trim());
                return;
            }
            // **表のセルへ複数のセルを貼る**（2026-10-04・下の「表へ複数のセルを貼る」）——Excel や表からコピーした
            // セルの塊を、キャレットのあるセルから右下へ。1つのセルだけなら文字だけを入れる（表の中に表を作らない）。
            // セルを矩形で選んでいれば（下の「表のセルを矩形で選ぶ」）、その左上から。値が1つなら選んだセル全部へ。
            const picked = selectedCells().filter(c => !isServerOwned(c));
            const cellNow = picked[0] || (elNow && elNow.closest('#w-editor-content td, #w-editor-content th'));
            if (cellNow && !isServerOwned(cellNow)) {
                let grid = clipboardGrid(cd);
                if (!grid && picked.length && plain.trim()) grid = [[plain.replace(/\s+/g, ' ').trim()]];
                if (grid && grid.length && grid[0].length) {
                    e.preventDefault();
                    if (grid.length === 1 && grid[0].length === 1) {
                        if (picked.length) fillSelectedCells(grid[0][0]);
                        else document.execCommand('insertText', false, grid[0][0]);
                        return;
                    }
                    pasteGridIntoTable(cellNow, grid);
                    return;
                }
            }
            const files = Array.from(cd.files || []);
            if (!files.length || (cd.getData('text/plain') || '').trim()) return;
            const sel = window.getSelection();
            const at = sel && sel.rangeCount ? sel.anchorNode : null;
            const el = at && (at.nodeType === 1 ? at : at.parentElement);
            if (!el || !el.closest('#w-editor-content') || isServerOwned(el)) return;
            e.preventDefault();
            const named = files.map((f, i) => pastedFileName(f, i, files.length));
            const cell = el.closest('#w-editor-content td');
            const col = cell && resolveCellColumn(cell);
            const images = named.filter(f => f.type.indexOf('image/') === 0);
            if (images.length && cell && col && col.type === 'image' && !isServerOwned(cell)) {
                insertImageIntoCell(cell, images[0]); // 表のセルへは1枚だけ（ドロップと同じ）
                return;
            }
            insertFilesAfter(named, fileInsertPointAt(at));
        });

        // 項目の click / mouseenter は renderSlashMenu が項目を作るたびに配線する
        // （絞り込みのたびに作り直すので、ここで一度だけ配線すると外れてしまう）。
        //
        // メニュー自体の mousedown は止める。クリックでキャレットが飛ぶと、
        // どのブロックへ挿すのか（currentSlashBlock）を見失うため。
        const smenu = document.getElementById('w-slash-menu');
        if (smenu) smenu.addEventListener('mousedown', e => e.preventDefault());
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
        } else {
            refreshBlockTypeButtons(); // 同じブロックの中でキャレットが動いた（節の中の段落と見出しなど）
            positionContextToolbar(block); // 帯もキャレットの行へ寄せる（背の高い節の中で動いたとき）
        }
    }

    // ── ブロックの種類を替える（2026-10-07） ───────────────────────────────
    //
    // 利用者:「既存の文字列ブロックを見出しに変えたり段落に変えたりしたい」「スラッシュを入力するのは、日本語環境では
    // めんどう」——帯（showContextToolbarForBlock）の「¶ H1 H2 H3」で、**キャレットのある文字のブロック**（段落・見出し）
    // の種類だけを替える。中身・属性（ブロック ID）・キャレットの位置はそのまま。節や折りたたみの中の段落・見出しにも効く。
    // ⚠ 表・リスト・定義リストの中の文字は対象外（そこは中身の書式なので）。いまの種類のボタンは押した印になる。
    const BLOCK_TYPE_CHOICES = [['P', '¶', '段落'], ['H1', 'H1', '見出し1'], ['H2', 'H2', '見出し2'], ['H3', 'H3', '見出し3']];
    const TEXT_BLOCK_TAGS = ['P', 'H1', 'H2', 'H3', 'H4', 'H5', 'H6'];

    // textBlockAtCaret はキャレットのある文字のブロック（段落・見出し）を返す（無ければ null）。
    function textBlockAtCaret() {
        const sel = window.getSelection();
        const n = sel && sel.rangeCount ? sel.anchorNode : null;
        const editor = document.getElementById('w-editor-content');
        let el = n && (n.nodeType === 1 ? n : n.parentElement);
        if (!el || !editor || !editor.contains(el) || el.closest('.vocab-chrome, .block-controls')) return null;
        for (; el && el !== editor; el = el.parentElement) {
            if (!TEXT_BLOCK_TAGS.includes(el.tagName)) continue;
            return el.closest('td, th, li, dd, dt, caption, summary') ? null : el;
        }
        return null;
    }

    // refreshBlockTypeButtons は、帯の種類のボタンにいまの種類の印を付ける（文字のブロックの外なら押せない）。
    function refreshBlockTypeButtons() {
        const el = textBlockAtCaret();
        document.querySelectorAll('#w-context-toolbar .ctx-type').forEach(b => {
            const on = !!el && el.tagName === b.dataset.tag;
            if (b.classList.contains('is-active') !== on) b.classList.toggle('is-active', on);
            b.setAttribute('aria-pressed', on ? 'true' : 'false');
            b.disabled = !el;
        });
    }

    // caretTextOffset は、キャレットが el の文字の何文字目にあるかを返す（el の外なら null）。
    function caretTextOffset(el) {
        const sel = window.getSelection();
        if (!sel || !sel.rangeCount || !el.contains(sel.anchorNode)) return null;
        const r = document.createRange();
        r.selectNodeContents(el);
        r.setEnd(sel.anchorNode, sel.anchorOffset);
        return r.toString().length;
    }

    // placeCaretAtTextOffset は、el の文字の offset 文字目へキャレットを置く（null なら末尾）。
    function placeCaretAtTextOffset(el, offset) {
        const range = document.createRange();
        let rest = offset == null ? Infinity : offset;
        const walker = document.createTreeWalker(el, NodeFilter.SHOW_TEXT);
        let placed = false;
        for (let t = walker.nextNode(); t; t = walker.nextNode()) {
            if (rest <= t.nodeValue.length) { range.setStart(t, rest); placed = true; break; }
            rest -= t.nodeValue.length;
        }
        if (!placed) range.selectNodeContents(el);
        range.collapse(placed);
        const host = el.closest('[contenteditable="true"]');
        if (host) host.focus({ preventScroll: true });
        const sel = window.getSelection();
        sel.removeAllRanges();
        sel.addRange(range);
    }

    // convertTextBlock は文字のブロックの種類を替える（要素を作り直し、中身と属性を移す）。
    function convertTextBlock(el, tag) {
        if (!el || !document.body.hasAttribute('edit-mode')) return;
        if (el.tagName === tag) { refreshBlockTypeButtons(); return; }
        const offset = caretTextOffset(el);
        const n = document.createElement(tag.toLowerCase());
        Array.from(el.attributes).forEach(a => n.setAttribute(a.name, a.value));
        quietDomChange = true; // 中身を移したあと・外す前の姿を、focusout の保存が拾わないように
        try {
            while (el.firstChild) n.appendChild(el.firstChild);
            n.oninput = el.oninput; // 上の段なら applyMode が付けた配線を引き継ぐ
            el.replaceWith(n);
        } finally {
            quietDomChange = false;
        }
        if (!n.firstChild) n.appendChild(document.createElement('br')); // 空ならキャレットの足場
        placeCaretAtTextOffset(n, offset);
        updateHtmlPreview();
        buildToc();
        triggerAutoSave();
        refreshBlockTypeButtons();
    }

    // addBlockBelowCaret は帯の「＋」——キャレットのある要素の下に段落を足して、スラッシュメニューと同じ一覧を開く
    // （ブロックの下端の ＋・節の中の ＋ と同じ流れ。スラッシュは打たない）。
    function addBlockBelowCaret(block) {
        if (!document.body.hasAttribute('edit-mode')) return;
        const el = textBlockAtCaret();
        if (el && innerBoxOf(el)) { addAfterInner(el); return; }
        const newP = insertComponent('p', block, 'after');
        if (!newP) return;
        newP.focus();
        newP.innerText = '/';
        showSlashMenu(newP);
    }

    // ── インライン装飾（太字・斜体・下線）─────────────────────────────────
    // document.execCommand は仕様上 deprecated で、ブラウザによって生成されるHTMLが
    // 違う（<b> か <strong> か、あるいは style="font-weight:bold" か）。style 属性は
    // サニタイザが落とすので装飾が保存後に消える、という食い違いも起きていた。
    // ここでは Range を直接操作し、常に <strong>/<em>/<u> を出す。
    //
    // アンドゥ・リドゥはこの操作も「太字」などのコマンドとして拾う（Ctrl+B の前に打った分を拾い、操作のあとにもう一度——
    // setupHistoryHooks）。特別扱いは要らない。

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
            // 戻す・やり直す（2026-10-07 利用者:「アンドゥ・リドゥを本格化してください」）——スマホには Ctrl+Z が無いので、帯の頭に。
            [['w-ctx-undo', '↶', '元に戻す（Ctrl+Z）', () => undo()], ['w-ctx-redo', '↷', 'やり直す（Ctrl+Y）', () => redo()]].forEach(([id, label, title, fn]) => {
                const b = document.createElement('button');
                b.id = id;
                b.innerText = label;
                b.title = title;
                b.setAttribute('aria-label', title);
                b.style.minWidth = '24px';
                b.addEventListener('mousedown', e => e.preventDefault()); // キャレットを本文に残す
                b.addEventListener('click', e => { e.preventDefault(); fn(); });
                toolbar.appendChild(b);
            });
            {
                const sep = document.createElement('span');
                sep.className = 'ctx-sep';
                toolbar.appendChild(sep);
            }
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
            // リンクも「選択した文字への操作」なので装飾と同じ並びに置く。
            // スラッシュメニューには載せない——あちらは `/` で始まる段落を
            // **ブロックごと**差し替える口で、文中のリンクは入らない。
            const link = document.createElement('button');
            link.id = 'w-ctx-link';
            link.innerText = '🔗';
            link.title = '選択した文字をリンクにする';
            link.style.minWidth = '24px';
            link.addEventListener('mousedown', e => e.preventDefault());
            link.addEventListener('click', e => { e.preventDefault(); linkSelection(); });
            toolbar.appendChild(link);
            // 画像・ファイルを足す（2026-09-30 利用者:「BIUリンクの方に画像マークでもあれば良いのでは？」）——キャレットの
            // すぐ後ろへ（節・折りたたみの中なら中のまま）。⚠ **入口は 📎 の1つ**（2026-10-01 利用者:「画像とPDFの違いを
            // 自動判定して、画像をはる、PDFをはるというわけかたをなくせませんか」——09-30 は 🖼〔画像だけ〕と 📎 の2つだった）。
            // 種類は機械が見分ける（画像は絵・それ以外はファイル表示——insertFilesAfter）。スマホでもカメラは選べる（種類を
            // 絞らない選択でも、端末がカメラを出す）。
            {
                const b = document.createElement('button');
                b.id = 'w-ctx-file';
                b.innerText = '📎';
                b.title = '画像・ファイルを追加（画像は絵、PDF などはファイル表示で入ります）';
                b.style.minWidth = '24px';
                b.addEventListener('mousedown', e => e.preventDefault()); // キャレットを本文に残す
                b.addEventListener('click', e => { e.preventDefault(); addFilesAtCaret(); });
                toolbar.appendChild(b);
            }
            // 文字のブロックの種類を替える・下にブロックを足す（2026-10-07 利用者:「既存の文字列ブロックを見出しに変えたり
            // 段落に変えたりしたい」「スラッシュを入力するのは、日本語環境ではめんどう」）。下の「ブロックの種類を替える」。
            {
                const sep = document.createElement('span');
                sep.className = 'ctx-sep';
                toolbar.appendChild(sep);
                BLOCK_TYPE_CHOICES.forEach(([tag, label, title]) => {
                    const b = document.createElement('button');
                    b.className = 'ctx-type';
                    b.dataset.tag = tag;
                    b.innerText = label;
                    b.title = title + 'にする';
                    b.style.minWidth = '24px';
                    b.addEventListener('mousedown', e => e.preventDefault()); // キャレットを本文に残す
                    b.addEventListener('click', e => { e.preventDefault(); convertTextBlock(textBlockAtCaret(), tag); });
                    toolbar.appendChild(b);
                });
                const add = document.createElement('button');
                add.id = 'w-ctx-add';
                add.innerText = '＋';
                add.title = '下にブロックを足す（スラッシュを打たずに一覧から選ぶ）';
                add.style.minWidth = '24px';
                add.addEventListener('mousedown', e => e.preventDefault());
                add.addEventListener('click', e => { e.preventDefault(); addBlockBelowCaret(block); });
                toolbar.appendChild(add);
            }
            hasButtons = true;
        }

        if (hasButtons) {
            refreshBlockTypeButtons(); // いまの種類の印（作り直したばかりの帯にはまだ無い）
            refreshUndoButtons();      // ↶ ↷ は押せるときだけ
            toolbar.classList.add('active');
            // タッチ端末では**画面の端のドック**に出す（2026-09-01 スマホ実機確認:
            // 「文字列を選択すると、切り取り、コピーなどのコンテキストメニューが出て、
            // w-cmsのコンテキストメニューが隠れます」）。ネイティブの選択メニューは
            // 選択位置の近くにしか出ないので、端なら場所を取り合わない。⚠ 2026-10-07 から**上の端**
            // （下の端は打っている行のすぐ下を塞いだ——positionDockedToolbar）。
            if (window.matchMedia('(pointer: coarse)').matches) {
                toolbar.classList.add('is-docked');
                toolbar.style.left = '';
                positionDockedToolbar(); // キーボードに合わせて「見えている上端」へ
                if (slashMenuVisible) placeSlashMenu(); // 帯が出直したらメニューを置き直す（帯の下へ）
            } else {
                toolbar.classList.remove('is-docked');
                positionContextToolbar(block);
            }
        } else {
            hideContextToolbar();
        }
    }

    // positionContextToolbar は PC の帯を、**キャレットのある行**（文字の要素）のすぐ上に置く——入らなければ（画面やヘッダーの上へ
    // はみ出すなら）すぐ下へ。⚠ 2026-10-07 まではブロックの上端の上に置いていた（利用者:「帯が出ません」「一番下では出ます」）——
    // 写真を何十枚も持つ節は高さが 7万px にもなり、その下の方を触ると帯は画面のはるか上（実測 -68,624px）に出て見えなかった。
    // キャレットが同じブロックの中で動いたときも置き直す（updateContextToolbar）。
    function positionContextToolbar(block) {
        const toolbar = document.getElementById('w-context-toolbar');
        if (!toolbar || toolbar.classList.contains('is-docked') || !toolbar.classList.contains('active')) return;
        if (fixedBars()) return; // 本文の上の固定の帯の中（2026-10-19・initFixedBar）——置き直さない
        const sel = window.getSelection();
        const n = sel && sel.rangeCount ? sel.anchorNode : null;
        const at = n && (n.nodeType === 1 ? n : n.parentElement);
        // 行の目安: キャレットのある文字の要素（段落・見出し・セル・項目）——無ければブロック。
        const line = (at && block && block.contains(at) && at.closest('p, h1, h2, h3, h4, h5, h6, li, td, th, dt, dd, caption, summary, blockquote, pre')) || block;
        if (!line) return;
        const rect = line.getBoundingClientRect();
        const h = toolbar.offsetHeight || 34;
        const header = document.querySelector('header.header');
        const hr = header ? header.getBoundingClientRect() : null;
        const ceiling = hr && hr.bottom > 0 ? hr.bottom : 0; // 出ているヘッダーの下端（出ていなければ画面の上端）
        let top = rect.top - h - 6;
        if (top < ceiling + 4) top = Math.min(rect.bottom + 6, window.innerHeight - h - 4); // 上に入らなければ下へ
        const blockRect = (block || line).getBoundingClientRect();
        toolbar.style.top = (top + window.scrollY) + 'px';
        toolbar.style.left = (Math.max(blockRect.left, 0) + window.scrollX + 20) + 'px';
    }

    // ── 帯を本文の上に固定する（2026-10-19・パソコンだけ） ─────────────────────────
    //
    // 2026-10-12 利用者:「ブロックエディタのメニューが編集の邪魔をすることがあります。テーブルの編集では、列の見出しを見ながら書くことが多いからです。いっそのこと、普通のアプリケーションのように。メニューを上部に固定してしまってはどうでしょう？」
    //   - パソコン（指で触らない端末）では、文字の帯（#w-context-toolbar）・表の行と列の帯（#w-table-toolbar）・
    //     タグの項目の帯（#w-dl-toolbar）を、本文のすぐ上の1本（#w-fixed-bar）へ入れる。帯はキャレットの近くに浮かず、
    //     本文に重ならない（列の見出しを隠さない）。1本は流れの中で場所を取り、スクロールしても上に留まる（sticky）。
    //   - キャレットの場所に合う組だけが押せる。合わない組は薄くして押せない（隠すと、表に入るたびに1本の高さが変わって
    //     本文がずれるため）。
    //   - 選択肢・スラッシュのメニューは、選ぶものなので、いままでどおりキャレットの近く。
    //   - スマホ・タブレット（指で触る端末）は、いままでどおり画面の上のドック（is-docked）。
    function fixedBars() { return document.body.classList.contains('w-fixed-bars'); }

    function initFixedBar() {
        if (window.matchMedia && window.matchMedia('(pointer: coarse)').matches) return; // 指で触る端末はドックのまま
        const content = document.getElementById('w-editor-content');
        if (!content || document.getElementById('w-fixed-bar')) return;
        const strip = document.createElement('div');
        strip.id = 'w-fixed-bar';
        strip.className = 'fixed-bar';
        strip.addEventListener('mousedown', e => e.preventDefault()); // 帯のすき間を押してもキャレットを本文に残す
        ['w-context-toolbar', 'w-table-toolbar', 'w-dl-toolbar'].forEach(id => {
            const el = document.getElementById(id);
            if (!el) return;
            el.classList.remove('pinned');
            el.style.top = '';
            el.style.left = '';
            strip.appendChild(el);
        });
        content.parentElement.insertBefore(strip, content);
        document.body.classList.add('w-fixed-bars');
        // ヘッダーが出ているときはその下・隠れたら画面の上（ヘッダーはスクロールで出入りする——initHeadroom）。
        const header = document.querySelector('.header');
        const place = () => {
            strip.style.top = (header && !header.classList.contains('header--hidden') ? header.offsetHeight : 0) + 'px';
        };
        place();
        if (header) new MutationObserver(place).observe(header, { attributes: true, attributeFilter: ['class'] });
        window.addEventListener('resize', place);
    }

    // ensureIdleContextToolbar は、固定の帯の文字の帯がまだ空なら、最初のブロックで組んで押せない形にしておきます
    // （触る前から帯の並びが見えて、触ったときに1本の高さが変わらないように）。
    function ensureIdleContextToolbar() {
        if (!fixedBars()) return;
        const toolbar = document.getElementById('w-context-toolbar');
        if (!toolbar || toolbar.childElementCount) return;
        const first = document.querySelector('#w-editor-content .editor-block');
        if (!first) return;
        showContextToolbarForBlock(first);
        hideContextToolbar();
    }

    function hideContextToolbar() {
        const toolbar = document.getElementById('w-context-toolbar');
        if (toolbar) {
            toolbar.classList.remove('active');
            toolbar.style.top = '';
        }
        activeBlock = null;
    }

    // positionDockedToolbar はドック（タッチ端末のコンテキストツールバー）を
    // **見えている領域**（visualViewport）の下端へ置く。固定 bottom はレイアウト
    // ビューポート基準なので、ソフトキーボードが出ると隠れる（2026-09-01 実機確認
    // 「画面下の固定ドックでは、ソフトキーボードに隠されるようです」）。
    // visualViewport はキーボードで縮むため、その下端に合わせれば常に見える。
    // ⚠ **2026-10-07 から画面の上**（利用者:「スマホでは帯が書き込みを邪魔するので上の方に出した方がよいと思います」）
    //    ——それまでは見えている領域の下端（キーボードのすぐ上）で、打っている行のすぐ下を塞いでいた。
    //    見えている領域の上端に置く（iOS でキーボードが出てページがずれても、visualViewport の offsetTop で追う）。
    function positionDockedToolbar() {
        const toolbar = document.getElementById('w-context-toolbar');
        if (!toolbar || !toolbar.classList.contains('is-docked') ||
            !toolbar.classList.contains('active')) return;
        const vv = window.visualViewport;
        if (!vv) return; // 古いブラウザは CSS の top 固定にフォールバック
        toolbar.style.top = (vv.offsetTop + 6) + 'px';
    }
    if (window.visualViewport) {
        window.visualViewport.addEventListener('resize', positionDockedToolbar);
        window.visualViewport.addEventListener('scroll', positionDockedToolbar);
    }

    // 初回読み込み時の初期表示

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
    // を外せるようにする（docs/旧文書/本文サニタイズ設計.md §6）。markup は「何を押したか」だけを
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
        on('#w-delete-page-btn', 'click', deletePage);
        on('#w-pi-parent-apply', 'click', applyParent);
        on('#w-pp-chown-btn', 'click', chownPage);
        on('#w-pp-save', 'click', savePerms);
        on('#w-pp-public', 'change', setPublic);
        // 版の履歴は**開いたときに取り直す**。オートセーブのたびに取りに行くと
        // 1〜2秒ごとの余計な往復になるので、見に来た瞬間だけ最新にする。
        on('#w-vh-details', 'toggle', e => { if (e.target.open) loadVersions(); });
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

        // 開いたときの形——?edit があれば書ける形、無ければこの端末に憶えた形（初めてはパソコン＝書ける形・スマホ＝閲覧——
        // 2026-10-12・問い1）。匿名は書けないので閲覧のまま。書く権限が無ければ loadForEditing が閲覧へ戻す。
        if (!anon && (urlParams.has('edit') || preferredMode() === 'edit')) {
            const toggle = document.getElementById('w-mode-toggle');
            if (toggle) toggle.checked = true;
        }

        // 本文はサーバーが #w-editor-content へ埋め込み済み（合成方式）。fetch はせず、
        // 既にあるDOMをそのまま初期コンテンツとしてブロック化する。認可・404・
        // 匿名のログイン誘導はサーバー側で解決済みなので、ここでは扱わない。
        // ページの更新の知らせが比べる相手を、包みと飾り付けの前に覚える（サーバーが描いたままの姿・2026-10-07）。
        adoptViewSource(null, true);
        initBlocks();
        buildToc();
        setupEditorEvents();
        setupAutoSave();
        setupHistoryHooks(); // アンドゥの区切りの引き金（2026-10-07）
        setupSaveOnLeave();  // タブを隠す・ページを去るときに保存（2026-10-12 昼）
        setupPresence();     // キャレットのあるブロックを知らせる（2026-10-12 昼）
        setupArrowNavigation(); // 矢印キーでブロックの端を越える（2026-10-12 午後）

        if (anon) {
            // 匿名は読み取り専用。書ける形へは入らず、権限カード（loadPerms）は
            // 叩かない。ページ属性は /api/page-meta が実効公開なら匿名にも返すため、
            // loadPageMeta は呼ぶ。もとは左レールの「親ページへ」リンクのためだったが、それは 2026-09-03 に
            // パンくず（サーバーが組む）へ置き換えた。いま loadPageMeta が書くのは右レールのページ情報で、
            // 右レールは body.anonymous の CSS で丸ごと隠れている（見える効き目は無い）。
            // 子ページ一覧も実効公開の子だけをサーバー側で絞って返すため、匿名でも読み込む。
            applyMode();
            loadPageMeta();
            loadChildNav();
            return;
        }
        // 書ける形なら本文と元を受け取って載せ替え、閲覧なら閲覧の形を当てる（2026-10-12——編集権は取らない）。
        onModeToggle();

        // ページ属性（親・作成/更新情報）・権限・子ページナビをサイドパネルへ読み込む。
        loadPageMeta();
        loadPerms();
        loadChildNav();
        loadVersions();
        openPageEvents(); // ほかの人の書き込みを知らせる（2026-10-07）
    };

// ── 鏡のボタンに共通の道具（2026-09-23 に寄せた）─────────────────────────
//
// 09-21〜22 に足した5つの配線（受注残の印刷・受注残からの書き戻し・材料を探す・
// 未手配→発注部材表→部材発注書・部材発注書を送る）が、**同じ数行をそれぞれ写していました**
// ——JSON を POST して `success` を見る（6回）・欄に1文を出す（2回）・入力欄の値を
// 読む（3回）・document へ委譲した click を `closest` で振り分ける（4回）。
// ここへ寄せ、以後はここを通します。
//
// ⚠ **鏡の配線は、サーバーが描いた属性だけで動くように書きます**（`currentPageId`
//    など殻の状態に頼らない）——鏡が別のページに描かれた日に黙って壊れます。
// ⚠ **配線は document へ委譲します**（要素ごとに配線しない）——鏡はサーバーが
//    描き直すので、要素に付けた配線は描き直しのたびに切れます。
// ⚠ **`innerHTML` へ文字列を入れません**（`createElement`＋`textContent`）。

// postJSON は JSON を POST して `{ ok, status, data }` を返します。
// `ok` は **HTTP が 2xx で、かつ本文の `success` が真**のとき。⚠ **通信の失敗は
// 投げます**（呼ぶ側が「通信に失敗しました」と言い分けるため・握り潰さない）。
// BY_HAND_REPLY_HINT は 🤖 解析が「どれでもない」と答えたときに添える、手で作る道です（2026-10-19・ext/toho/rfq_reply_page.go）。
// 利用者:「このようなエクセル形式でも解析できるようにならないでしょうか？ならない場合、手作業で見積もり回答書を作れるとありがたいです」
const BY_HAND_REPLY_HINT = '業者の値段の表なら、メールの「作るページ」の「部材見積依頼の返事ページ（手で書く）」で手で作れます。';

async function postJSON(url, body) {
    // 書ける形で押したときは、書きかけを先に保存してから送る（2026-10-12・問い5——直してすぐ押せる。ボタンが読む本文に
    // 直した中身が入っているように）。
    if (window.wcmsFlushSave) await window.wcmsFlushSave();
    const res = await fetch(url, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        credentials: 'same-origin',
        body: JSON.stringify(body),
    });
    const data = await res.json().catch(() => ({}));
    return { ok: res.ok && !!data.success, status: res.status, data };
}

// sayBoundPDF は、資料を綴じた1本（FAX・印刷用）ができたことを欄に出し、開くリンクを添えます（部材発注書・部材見積依頼書——
// 2026-10-09 に2つの写しを寄せた）。⚠ **綴じなかったものを黙りません**——紙にできない形式・読めないPDF・資料の無い行。
function sayBoundPDF(box, data) {
    const p = sayIn(box, '📠 FAX・印刷用を作りました（このページの添付にも残ります）: ');
    if (!p) return;
    const a = document.createElement('a');
    a.href = data.url || '';
    a.target = '_blank';
    a.rel = 'noopener';
    a.textContent = data.file || '開く';
    p.appendChild(a);
    (data.skipped || []).forEach((s) => {
        const q = document.createElement('p');
        q.className = 'proc-why-ng';
        q.textContent = '⚠ ' + s;
        box.appendChild(q);
    });
}

// sayIn は欄の中身をその1文だけにします（欄が無ければ何もしない）。
function sayIn(box, text, cls) {
    if (!box) return null;
    box.textContent = '';
    const p = document.createElement('p');
    if (cls) p.className = cls;
    p.textContent = text;
    box.appendChild(p);
    return p;
}

// valueIn は root の中の入力欄の値（前後の空白を落とす）です。無ければ空文字。
function valueIn(root, selector) {
    const el = root ? root.querySelector(selector) : null;
    return el ? el.value.trim() : '';
}

// getJSON は GET して JSON を受け取ります（postJSON と同じ形の答え）。
async function getJSON(url) {
    try {
        const res = await fetch(url, { credentials: 'same-origin' });
        const data = await res.json().catch(() => ({}));
        return { ok: res.ok && !!data.success, status: res.status, data };
    } catch (e) {
        return { ok: false, status: 0, data: { message: e.message } };
    }
}

// ── 装置フォルダから選ぶ一時的な表（2026-10-03）────────────────────────────
//
// 再部材見積依頼（wireRFQ）と見積書を作る（wireEstimateFolder）が同じ形で組みます。表は画面だけ（鏡の中のクローム・本文に残さない）。
//   headers: 見出しの文字（先頭の選ぶ欄は除く）
//   rows:    { pick: 選ぶ欄の値（null なら選べない行）, cells: 文字か要素の並び }
//   attr:    選ぶ欄の属性名（値が pick）——見出しの「全部選ぶ」（data-pick-all="attr"）が、その表の attr の欄を全部入れ・外す。
function folderPickTable(headers, rows, attr, className) {
    const el = (tag, text) => {
        const c = document.createElement(tag);
        if (text !== undefined) c.textContent = text;
        return c;
    };
    const table = el('table');
    table.className = className;
    const head = el('tr');
    const allTh = el('th');
    const all = document.createElement('input');
    all.type = 'checkbox';
    all.title = '全部選ぶ';
    all.setAttribute('data-pick-all', attr);
    allTh.appendChild(all);
    head.appendChild(allTh);
    headers.forEach((h) => head.appendChild(el('th', h)));
    const thead = el('thead');
    thead.appendChild(head);
    table.appendChild(thead);
    const tbody = el('tbody');
    rows.forEach((r) => {
        const tr = el('tr');
        const pickTd = el('td');
        if (r.pick !== null && r.pick !== undefined) {
            const c = document.createElement('input');
            c.type = 'checkbox';
            c.setAttribute(attr, r.pick);
            pickTd.appendChild(c);
        }
        tr.appendChild(pickTd);
        r.cells.forEach((v) => {
            const td = el('td');
            if (v instanceof Node) td.appendChild(v);
            else td.textContent = v === null || v === undefined ? '' : String(v);
            tr.appendChild(td);
        });
        tbody.appendChild(tr);
    });
    table.appendChild(tbody);
    return table;
}

// pageLink は加工製品ページへのリンク（別のタブで開く）です。
function pageLink(id) {
    const a = document.createElement('a');
    a.href = '/' + id;
    a.target = '_blank';
    a.textContent = id;
    return a;
}

delegateClick([['[data-pick-all]', (box) => {
    const table = box.closest('table');
    const attr = box.getAttribute('data-pick-all');
    if (table && attr) table.querySelectorAll('input[' + attr + ']').forEach((c) => { c.checked = box.checked; });
}]]);

// delegateClick は document の click を、押された要素から `closest` で引いた
// **最初の当たり**へ振り分けます。⚠ **並び順が優先順位**です（先に書いた方が勝つ）。
function delegateClick(handlers) {
    document.addEventListener('click', (e) => {
        const t = e.target;
        if (!t || !t.closest) return;
        for (const [selector, fn] of handlers) {
            const hit = t.closest(selector);
            if (hit) { fn(hit, e); return; }
        }
    });
}

// ── 見積表に単価を記録する・ロットを写す（2026-10-15・ext/toho/estimate.go）─────────────
//
// 利用者:「結果の単価のみDBに入れるべき」「ロットごとに計算表を分けてはどうでしょう？」「提案通りの構成でお願いします」。
// 見積表の足元（サーバーが描いたクローム）の「📌 この単価を記録」で、すぐ上の見積計算表から計算した原価・利益率・単価を
// 見積表に書く（見積表が無い見積計算表の下では「📌 見積表を作って記録」——作ってから書く）。「⧉ このロットを写す」で
// 見出し・見積計算表・見積表を組ごと写す。どちらも書けたらページを読み直す。利益率は見積表の欄を直接直す
// （2026-10-01〜10-15 の「利益率を変える」欄と /api/estimate-rate はやめた）。
// ── 見積計算書を作る（2026-10-16・ext/toho/estimate_calc_doc.go）──────────────────────────
//
// 利用者:「新しい見積計算書を作るボタンから始めてみましょう」。加工製品ページの「🧮 見積計算書」（サーバーが描いたクローム）の
// 「➕ 新しい見積計算書を作る」で、いちばん新しい見積計算書（無ければこのページの「見積もり計算」）を写した見積計算書を
// 記録／年／月／製造記録 に作り、そのページを開く。
delegateClick([
    ['.estimate-calc-new', async (btn) => {
        const say = btn.parentElement && btn.parentElement.querySelector('.estimate-calc-say');
        btn.disabled = true;
        sayIn(say, '作っています…');
        let r;
        try {
            // 製造ページの「見積計算」の欄から押したときは、その製造ページの子に作る（2026-10-19・ext/toho/mfg_more.go）。
            r = await postJSON('/api/estimate-calc/new', { product: btn.getAttribute('data-ecalc-new') || '', mfg: btn.getAttribute('data-mfg') || '' });
        } catch (e) {
            sayIn(say, '⚠ 通信に失敗しました: ' + e, 'proc-why-ng');
            btn.disabled = false;
            return;
        }
        if (!r.ok) {
            sayIn(say, '⚠ ' + ((r.data && r.data.message) || ('作れませんでした（' + r.status + '）')), 'proc-why-ng');
            btn.disabled = false;
            return;
        }
        location.href = '/' + r.data.page_id;
    }],
]);

delegateClick([
    ['.estimate-record-go', (btn) => estimateBlockPost(btn, '/api/estimate/record', '.estimate-record-say', '記録しています…')],
    ['.estimate-dup-go', (btn) => estimateBlockPost(btn, '/api/estimate/duplicate', '.estimate-dup-say', '写しています…')],
]);

async function estimateBlockPost(btn, url, sayClass, doing) {
    const say = btn.parentElement && btn.parentElement.querySelector(sayClass);
    btn.disabled = true;
    sayIn(say, doing);
    const r = await postJSON(url, {
        page_id: location.pathname.replace(/^\//, '').slice(0, 6),
        block: Number(btn.getAttribute('data-estimate-block')),
    }).catch(e => ({ ok: false, data: { message: String(e) } }));
    btn.disabled = false;
    if (!r.ok) { sayIn(say, '⚠ ' + ((r.data && r.data.message) || '書けませんでした')); return; }
    location.reload();
}

// ── 見積書（2026-10-01・ext/toho/estimate_doc.go・estimate_pdf.go）─────────────
//
// 利用者:「顧客へ見積書」「見積計算表から」「宛名は会社＋担当者」。見積表の足元の「見積書に入れる」で、その見積表の単価・
// ロット・表面処理と備考を見積書の見積明細表へ写す（2026-10-15 から見積表——それまでは見積計算表の確定単価）（新しい見積書か、同じ客先の作りかけへ足す）。見積書の足元の「📄 PDFを作る」で紙を作る。
delegateClick([['.estimate-add-go', async (btn) => {
    const form = btn.closest('.estimate-add-form');
    const say = form && form.querySelector('.estimate-add-say');
    if (!form) return;
    const into = valueIn(form, '.estimate-add-into');
    const client = valueIn(form, '.estimate-add-client');
    if (!into && !client) { sayIn(say, '⚠ 客先を書いてください'); return; }
    btn.disabled = true;
    sayIn(say, '見積書に入れています…');
    const r = await postJSON('/api/estimate/add', {
        product: form.getAttribute('data-estimate-product') || '',
        index: Number(form.getAttribute('data-estimate-index') || 0),
        into, client,
        person: valueIn(form, '.estimate-add-person'),
        signer: valueIn(form, '.estimate-add-signer'),
    }).catch(e => ({ ok: false, data: { message: String(e) } }));
    btn.disabled = false;
    if (!r.ok) { sayIn(say, '⚠ ' + ((r.data && r.data.message) || '入れられませんでした')); return; }
    if (say) {
        say.textContent = (r.data.new ? '新しい見積書を作りました: ' : '見積書に足しました: ');
        const a = document.createElement('a');
        a.href = '/' + r.data.page_id;
        a.textContent = '/' + r.data.page_id;
        say.appendChild(a);
    }
}], ['.estimate-pdf-go', async (btn) => {
    const say = btn.parentElement && btn.parentElement.querySelector('.estimate-pdf-say');
    btn.disabled = true;
    sayIn(say, 'PDFを作っています…');
    const r = await postJSON('/api/estimate-pdf', { page_id: btn.getAttribute('data-estimate-page') || '' })
        .catch(e => ({ ok: false, data: { message: String(e) } }));
    btn.disabled = false;
    if (!r.ok) { sayIn(say, '⚠ ' + ((r.data && r.data.message) || 'PDFを作れませんでした')); return; }
    location.reload();
}], ['.delivery-pdf-go', async (btn) => {
    // 納品書の紙（2026-10-17・ext/toho/delivery_pdf.go）——作ると、ページに表示し、発行日に今日を書く（未発行 → 発行済）。
    const say = btn.parentElement && btn.parentElement.querySelector('.delivery-pdf-say');
    btn.disabled = true;
    sayIn(say, 'PDFを作っています…');
    const r = await postJSON('/api/delivery-pdf', { page_id: btn.getAttribute('data-delivery-page') || '' })
        .catch(e => ({ ok: false, data: { message: String(e) } }));
    btn.disabled = false;
    if (!r.ok) { sayIn(say, '⚠ ' + ((r.data && r.data.message) || 'PDFを作れませんでした')); return; }
    location.reload();
}], ['.invoice-pdf-go', async (btn) => {
    // 請求書の紙（2026-10-17・ext/toho/invoice_pdf.go）——作ると、ページに表示し、発行日に今日を書く（未発行 → 発行済）。
    const say = btn.parentElement && btn.parentElement.querySelector('.invoice-pdf-say');
    btn.disabled = true;
    sayIn(say, 'PDFを作っています…');
    const r = await postJSON('/api/invoice-pdf', { page_id: btn.getAttribute('data-invoice-page') || '' })
        .catch(e => ({ ok: false, data: { message: String(e) } }));
    btn.disabled = false;
    if (!r.ok) { sayIn(say, '⚠ ' + ((r.data && r.data.message) || 'PDFを作れませんでした')); return; }
    location.reload();
}], ['.invoice-make', async (btn) => {
    // 「請求書を作る」（2026-10-17・ext/toho/invoice.go）——その客先の、締めまでの請求書にしていない行で請求書ページを作って開く。
    const row = btn.closest('.invoice-make-row');
    const say = row && row.querySelector('.invoice-make-say');
    const close = row && row.querySelector('.invoice-close');
    if (!row || !close || !close.value) { sayIn(say, '締めの日付を選んでください'); return; }
    btn.disabled = true;
    sayIn(say, '作っています…');
    const r = await postJSON('/api/invoice/new', { client: row.getAttribute('data-client') || '', close: close.value })
        .catch(e => ({ ok: false, data: { message: String(e) } }));
    btn.disabled = false;
    if (!r.ok) { sayIn(say, '⚠ ' + ((r.data && r.data.message) || '請求書を作れませんでした')); return; }
    location.href = r.data.url;
}], ['.estimate-note-save', async (btn) => {
    // 備考を見積明細表の下で書く（2026-10-03・ext/toho/estimate_note.go）——利用者:「見積書ページの見積明細テーブルの下に
    // 備考入力欄を付けると良いと思います」。保存先は見積書ページの「備考」の節（PDF はそこを刷る）。
    const box = btn.closest('.estimate-note-box');
    const input = box && box.querySelector('.estimate-note-input');
    const say = box && box.querySelector('.estimate-note-say');
    if (!input) return;
    btn.disabled = true;
    sayIn(say, '保存しています…');
    // 保存する口は欄が持つ（見積書は /api/estimate/note・部材見積依頼書は /api/rfq/note——2026-10-03）。
    const url = btn.getAttribute('data-note-url') || '/api/estimate/note';
    const r = await postJSON(url, { page_id: btn.getAttribute('data-estimate-page') || '', note: input.value })
        .catch(e => ({ ok: false, data: { message: String(e) } }));
    btn.disabled = false;
    if (!r.ok) { sayIn(say, '⚠ ' + ((r.data && r.data.message) || '備考を保存できませんでした')); return; }
    location.reload();
}], ['.rfq-pdf-go', async (btn) => {
    // 部材見積依頼書の「📄 PDFを作る」（2026-10-03・ext/toho/rfq_pdf.go）——作ったら読み直す（PDF は明細の下に出る）。
    const say = btn.parentElement && btn.parentElement.querySelector('.rfq-pdf-say');
    btn.disabled = true;
    sayIn(say, 'PDFを作っています…');
    const r = await postJSON('/api/rfq-pdf', { page_id: btn.getAttribute('data-rfq-page') || '' })
        .catch(e => ({ ok: false, data: { message: String(e) } }));
    btn.disabled = false;
    if (!r.ok) { sayIn(say, '⚠ ' + ((r.data && r.data.message) || 'PDFを作れませんでした')); return; }
    location.reload();
}], ['.rfq-pdf-docs-go', async (btn) => {
    // 部材見積依頼書の「📠 FAX・印刷用（資料を綴じる）」（2026-10-05・ext/toho/rfq_pdf.go）——部材発注書の makePDFWithDocs と同じ。
    // 部材見積依頼書の PDF（表示中のもの）は差し替えないので読み直さず、できた1本へのリンクを出す（押して印刷・FAX）。
    const say = btn.parentElement && btn.parentElement.querySelector('.rfq-pdf-docs-say');
    btn.disabled = true;
    sayIn(say, '部材見積依頼書に資料を綴じています…');
    const r = await postJSON('/api/rfq-pdf-docs', { page_id: btn.getAttribute('data-rfq-page') || '' })
        .catch(e => ({ ok: false, data: { message: String(e) } }));
    btn.disabled = false;
    if (!r.ok) { sayIn(say, '⚠ ' + ((r.data && r.data.message) || '綴じられませんでした')); return; }
    sayBoundPDF(say, r.data);
}], ['.rfq-sent-go', async (btn) => {
    // 部材見積依頼書を FAX・手渡しで送った（2026-10-03）——送付日に今日。
    if (!window.confirm('FAX・手渡しで送ったことにして、送付日に今日の日付を書きます。よろしいですか？')) return;
    const say = btn.parentElement && btn.parentElement.querySelector('.rfq-sent-say');
    btn.disabled = true;
    const r = await postJSON('/api/rfq/sent', { page_id: btn.getAttribute('data-rfq-page') || '' })
        .catch(e => ({ ok: false, data: { message: String(e) } }));
    btn.disabled = false;
    if (!r.ok) { sayIn(say, '⚠ ' + ((r.data && r.data.message) || '書けませんでした')); return; }
    location.reload();
}]]);

// ── 部材見積依頼の返事（2026-10-05・段4の前半——ext/toho/rfq_reply.go）─────────────────────────
//
// 利用者:「両方」（✓ 回答を記録と 🤖 返事を読む）。返事は「見積依頼書に手書きで FAX」「業者の見積書 PDF をメール」「メールの
// 本文に値段」。部材見積依頼明細表の足元の「返事:」の行に2つのボタン——
//   ✓ 回答を記録 … 手で単価を書いたあと押す。単価の入った未回答の行を回答あり・回答日に今日（/api/rfq/answered）
//   🤖 返事を読む … 返事の候補（このページのファイル・題に「№ このページ」を含む受信メールの本文と添付）を選び、🤖 で読んだ
//                   単価の**案**を表で見せる（/api/rfq/read-reply——何も書かない）。人が直して印の付いた行を書き込む
//                   （/api/rfq/apply-reply——読んだあとに行がずれていたら断られる）。
delegateClick([['.rfq-answered-go', async (btn) => {
    const say = btn.parentElement && btn.parentElement.querySelector('.rfq-reply-say');
    btn.disabled = true;
    sayIn(say, '記録しています…');
    const r = await postJSON('/api/rfq/answered', { page_id: btn.getAttribute('data-rfq-page') || '' })
        .catch(e => ({ ok: false, data: { message: String(e) } }));
    btn.disabled = false;
    if (!r.ok) { sayIn(say, '⚠ ' + ((r.data && r.data.message) || '記録できませんでした')); return; }
    location.reload();
}], ['.rfq-read-go', (btn) => openRFQReplyPanel(btn)]]);

// openRFQReplyFromHash は、返事ページの「部材見積依頼書で単価を写す（🤖 返事を読む）」（`/部材見積依頼書#rfq-reply`）から来たとき、
// 「🤖 返事を読む」の欄を開きます（2026-10-05・ext/toho/rfq_reply_page.go）——候補のいちばん上が結んだ返事の原本。読むのは人が押す。
// 一度開いたら印（#rfq-reply）を消す（描き直しのたびに開き直さない）。
function openRFQReplyFromHash() {
    if (location.hash !== '#rfq-reply') return; // 書ける形でも開く（2026-10-12・問い5）
    const btn = document.querySelector('#w-editor-content .rfq-read-go');
    if (!btn) return;
    history.replaceState(null, '', location.pathname + location.search);
    openRFQReplyPanel(btn);
    btn.scrollIntoView({ block: 'center' });
}

// 返事ページの「この部材見積依頼書への返事」「結ぶ」（2026-10-05・/api/rfq-reply/link）——結ぶと部材見積依頼書の子へ移る。
delegateClick([['.rfq-reply-link', async (btn) => {
    const row = btn.parentElement;
    const input = row && row.querySelector('.rfq-reply-no');
    const rfq = btn.getAttribute('data-rfq') || (input ? input.value.trim() : '');
    const say = row && row.querySelector('.rfq-reply-link-say');
    if (!rfq) { sayIn(say, '⚠ 部材見積依頼番号を入れてください'); return; }
    btn.disabled = true;
    const r = await postJSON('/api/rfq-reply/link', { page_id: btn.getAttribute('data-reply') || '', rfq })
        .catch(e => ({ ok: false, data: { message: String(e) } }));
    btn.disabled = false;
    if (!r.ok) {
        if (say) sayIn(say, '⚠ ' + ((r.data && r.data.message) || '結べませんでした'));
        else notify('結べませんでした: ' + ((r.data && r.data.message) || r.status), { type: 'warn' });
        return;
    }
    location.reload();
}]]);

// 返事ページの「この返事から部材見積依頼書ページを作る」（2026-10-05・移行期——ext/toho/rfq_reply_make.go）。読んだままの表を部材見積依頼明細表の列に
// 読み替えた案（GET /api/rfq-reply/draft——弊社品番は図面番号・品番から当てる）を表で出し、人が直して「部材見積依頼書ページを作る」
// （POST /api/rfq-reply/make-rfq）→ できた部材見積依頼書へ移る（返事はその子へ）。
delegateClick([['.rfq-reply-make', (btn) => openRFQReplyMakePanel(btn)]]);

const RFQ_REPLY_MAKE_COLS = [
    ['product_id', '弊社品番', 'rfq-mk-pid'], ['kind', '種類', 'rfq-mk-kind'], ['item_id', '品番', 'rfq-mk-id'], ['item_name', '品名', 'rfq-mk-name'],
    ['quantity', '数量', 'rfq-mk-qty'], ['unit', '単位', 'rfq-mk-unit'], ['cost', '単価', 'rfq-mk-cost'], ['note', '備考', 'rfq-mk-note'],
];

async function openRFQReplyMakePanel(btn) {
    const panel = btn.closest('section') && btn.closest('section').querySelector('.rfq-reply-make-panel');
    if (!panel) return;
    if (panel.childElementCount) { panel.textContent = ''; return; }
    const replyId = btn.getAttribute('data-reply') || '';
    const head = document.createElement('p');
    head.textContent = '返事の表を読み替えています…';
    panel.appendChild(head);
    const r = await getJSON('/api/rfq-reply/draft?page_id=' + encodeURIComponent(replyId));
    if (!r.ok) { head.textContent = '⚠ ' + ((r.data && r.data.message) || '案を作れません'); return; }
    head.textContent = '仕入先 ' + (r.data.supplier || '（無し）') + '・回答日 ' + (r.data.date || '（無し）') +
        ' の部材見積依頼書ページを作ります。弊社品番（加工製品ページの番号）・種類・品物を直してから「作る」を押してください（入れない行は印を外す）:';
    const table = document.createElement('table');
    table.className = 'rfq-reply-table rfq-reply-make-table';
    const hr = document.createElement('tr');
    ['入れる'].concat(RFQ_REPLY_MAKE_COLS.map((c) => c[1])).forEach((h) => { const th = document.createElement('th'); th.textContent = h; hr.appendChild(th); });
    table.appendChild(hr);
    const lines = [];
    (r.data.rows || []).forEach((row) => {
        const tr = document.createElement('tr');
        const td = (el) => { const c = document.createElement('td'); c.appendChild(el); tr.appendChild(c); return c; };
        const use = document.createElement('input');
        use.type = 'checkbox';
        use.checked = true;
        td(use);
        const inputs = {};
        RFQ_REPLY_MAKE_COLS.forEach(([key, , cls]) => {
            let el;
            if (key === 'kind') {
                el = document.createElement('select');
                ['', '材料', '外注加工', '購入部品', '支給部品'].forEach((k) => { const o = document.createElement('option'); o.value = k; o.textContent = k || '—'; el.appendChild(o); });
                el.value = row.kind || '';
            } else {
                el = document.createElement('input');
                el.type = 'text';
                el.value = row[key] || '';
            }
            el.className = cls;
            inputs[key] = el;
            const c = td(el);
            if (key === 'product_id' && row.product_title) {
                const hint = document.createElement('div');
                hint.className = 'rfq-mk-hint';
                hint.textContent = row.product_title;
                c.appendChild(hint);
            }
        });
        table.appendChild(tr);
        lines.push({ use, inputs, row });
    });
    panel.appendChild(table);
    const go = document.createElement('button');
    go.type = 'button';
    go.className = 'chip-btn rfq-reply-make-go';
    go.textContent = '部材見積依頼書ページを作る';
    const msg = document.createElement('span');
    msg.className = 'rfq-reply-make-say';
    panel.append(go, document.createTextNode(' '), msg);
    go.addEventListener('click', async () => {
        const rows = lines.filter((l) => l.use.checked).map((l) => {
            const o = Object.assign({}, l.row);
            delete o.product_title;
            RFQ_REPLY_MAKE_COLS.forEach(([key]) => { o[key] = l.inputs[key].value.trim(); });
            return o;
        });
        if (!rows.length) { msg.textContent = ' ⚠ 入れる行に印を付けてください'; return; }
        go.disabled = true;
        msg.textContent = ' 作っています…';
        const res = await postJSON('/api/rfq-reply/make-rfq', { page_id: replyId, rows })
            .catch((e) => ({ ok: false, data: { message: String(e) } }));
        go.disabled = false;
        if (!res.ok) { msg.textContent = ' ⚠ ' + ((res.data && res.data.message) || '作れませんでした'); return; }
        location.href = '/' + res.data.page_id;
    });
}

// openRFQReplyPanel は「🤖 返事を読む」の欄を開きます（もう一度押すと閉じる）。
async function openRFQReplyPanel(btn) {
    const cell = btn.parentElement;
    const panel = cell && cell.querySelector('.rfq-reply-panel');
    const say = cell && cell.querySelector('.rfq-reply-say');
    if (!panel) return;
    if (panel.childElementCount) { panel.textContent = ''; return; }
    const pageId = btn.getAttribute('data-rfq-page') || '';
    sayIn(say, '返事の候補を探しています…');
    const r = await getJSON('/api/rfq/reply-sources?page_id=' + encodeURIComponent(pageId));
    if (!r.ok) { sayIn(say, '⚠ ' + ((r.data && r.data.message) || '返事の候補を出せません')); return; }
    if (say) say.textContent = '';
    const sources = r.data.sources || [];
    const head = document.createElement('p');
    head.textContent = '🤖 読む返事を選んでください（FAX の取り込み・写真・業者の見積書はこのページに置くと並びます）:';
    panel.appendChild(head);
    const list = document.createElement('div');
    list.className = 'rfq-reply-sources';
    const radio = (value, checked) => {
        const rb = document.createElement('input');
        rb.type = 'radio';
        rb.name = 'rfq-reply-src-' + pageId;
        rb.value = value;
        rb.checked = checked;
        return rb;
    };
    sources.forEach((s, i) => {
        const lb = document.createElement('label');
        lb.append(radio(String(i), i === 0), document.createTextNode(' ' + s.label));
        list.appendChild(lb);
    });
    const other = document.createElement('label');
    const idIn = document.createElement('input');
    idIn.type = 'text';
    idIn.className = 'rfq-reply-id';
    idIn.placeholder = '🔗 ファイルの ID（000235-ab12）か、メールのページ番号';
    const otherRadio = radio('other', sources.length === 0);
    idIn.addEventListener('focus', () => { otherRadio.checked = true; });
    other.append(otherRadio, document.createTextNode(' ほか: '), idIn);
    list.appendChild(other);
    panel.appendChild(list);
    const go = document.createElement('button');
    go.type = 'button';
    go.className = 'chip-btn rfq-reply-read';
    go.textContent = '🤖 読む';
    panel.appendChild(go);
    const out = document.createElement('div');
    out.className = 'rfq-reply-result';
    panel.appendChild(out);
    go.addEventListener('click', async () => {
        const picked = list.querySelector('input[type=radio]:checked');
        let src = null;
        if (picked && picked.value !== 'other') {
            const s = sources[Number(picked.value)];
            src = { source_page: s.page_id, source_file: s.file || '' };
        } else {
            const v = idIn.value.trim();
            if (/^\d{1,6}-[0-9a-z]+$/i.test(v)) {
                const fr = await getJSON('/api/file-ref?ref=' + encodeURIComponent(v));
                if (!fr.ok) { sayIn(out, '⚠ ' + ((fr.data && fr.data.message) || v + ' を引けません')); return; }
                src = { source_page: fr.data.page_id, source_file: fr.data.file };
            } else if (/^\d{1,6}$/.test(v)) {
                src = { source_page: v.padStart(6, '0'), source_file: '' };
            } else {
                sayIn(out, '⚠ 読む返事を選ぶか、ファイルの ID・メールのページ番号を書いてください');
                return;
            }
        }
        go.disabled = true;
        sayIn(out, '🤖 読んでいます…（数十秒かかることがあります）');
        const rr = await postJSON('/api/rfq/read-reply', Object.assign({ page_id: pageId }, src))
            .catch(e => ({ ok: false, data: { message: String(e) } }));
        go.disabled = false;
        if (!rr.ok) { sayIn(out, '⚠ ' + ((rr.data && rr.data.message) || '読めませんでした')); return; }
        renderRFQReplyProposal(out, pageId, rr.data);
    });
}

// renderRFQReplyProposal は 🤖 の案を表にします——人が直して、印の付いた行を書き込む。
function renderRFQReplyProposal(out, pageId, d) {
    out.textContent = '';
    const note = document.createElement('p');
    note.className = 'proc-why-ng';
    note.textContent = '⚠ 🤖 は読み違えることがあります。返事と見比べて、直してから書き込んでください。';
    out.appendChild(note);
    if (d.summary) {
        const p = document.createElement('p');
        p.textContent = '🤖 ' + d.summary;
        out.appendChild(p);
    }
    const byRow = {};
    (d.guesses || []).forEach((g) => { byRow[g.row] = g; });
    const table = document.createElement('table');
    table.className = 'rfq-reply-table';
    const hr = document.createElement('tr');
    ['書く', '行', '何の値段か', 'いまの単価', '読んだ単価', '辞退', '備考'].forEach((h) => {
        const th = document.createElement('th');
        th.textContent = h;
        hr.appendChild(th);
    });
    table.appendChild(hr);
    const lines = [];
    (d.rows || []).forEach((row) => {
        const g = byRow[row.row] || {};
        const tr = document.createElement('tr');
        const td = (child) => { const c = document.createElement('td'); if (typeof child === 'string') c.textContent = child; else c.appendChild(child); tr.appendChild(c); return c; };
        const use = document.createElement('input');
        use.type = 'checkbox';
        use.checked = !!(g.unit_price || g.declined || g.note);
        const price = document.createElement('input');
        price.type = 'text';
        price.className = 'rfq-reply-price';
        price.value = g.unit_price || '';
        const declined = document.createElement('input');
        declined.type = 'checkbox';
        declined.checked = !!g.declined;
        const memo = document.createElement('input');
        memo.type = 'text';
        memo.className = 'rfq-reply-note';
        memo.value = g.note || '';
        [price, memo].forEach((x) => x.addEventListener('input', () => { use.checked = true; }));
        declined.addEventListener('change', () => { use.checked = true; });
        td(use);
        td(String(row.row));
        td(row.what || '');
        td((row.price || '') + (row.status ? '（' + row.status + '）' : ''));
        td(price);
        td(declined);
        td(memo);
        table.appendChild(tr);
        lines.push({ row, use, price, declined, memo });
    });
    out.appendChild(table);
    const apply = document.createElement('button');
    apply.type = 'button';
    apply.className = 'chip-btn rfq-reply-apply';
    apply.textContent = '✍ 印の付いた行を書き込む';
    const msg = document.createElement('span');
    msg.className = 'rfq-reply-apply-say';
    out.append(apply, document.createTextNode(' '), msg);
    apply.addEventListener('click', async () => {
        const send = lines.filter((l) => l.use.checked).map((l) => ({
            row: l.row.row, what: l.row.what, price: l.price.value.trim(), declined: l.declined.checked, note: l.memo.value.trim(),
        }));
        if (!send.length) { sayIn(msg, '⚠ 書き込む行に印を付けてください'); return; }
        apply.disabled = true;
        sayIn(msg, '書き込んでいます…');
        const r = await postJSON('/api/rfq/apply-reply', { page_id: pageId, lines: send })
            .catch(e => ({ ok: false, data: { message: String(e) } }));
        apply.disabled = false;
        if (!r.ok) { sayIn(msg, '⚠ ' + ((r.data && r.data.message) || '書き込めませんでした')); return; }
        location.reload();
    });
}

// ── 見積回答の単価を見積計算表へ写す（2026-10-05・ext/toho/estimate_put.go）──────────────────
//
// 利用者の答え（【要求】部材見積依頼 §4 の未決5）:「見積計算表へ写すボタン」。加工製品ページの「💴 見積回答」の行の「計算表へ」→
// どの見積計算表（ロット）・どの行（工程——無ければ新しい行）へ写すかを人が選ぶ → `価格` に単価・`備考` に出所（見積 業者 日付）。
// （2026-10-15 から列は `工程｜価格｜備考`・ロットは組の見積表のロット）
// 行は「何の値段か」と同じ名前の工程があれば最初から選んでおく。いまの値を置き換えるときは確かめる。
delegateClick([['.rfq-quote-put', (btn) => openQuotePutPanel(btn)]]);

async function openQuotePutPanel(btn) {
    const box = btn.closest('.rfq-quotes');
    const panel = box && box.querySelector('.rfq-quote-put-panel');
    if (!panel) return;
    const pageId = btn.getAttribute('data-product') || '';
    const price = btn.getAttribute('data-price') || '';
    const what = btn.getAttribute('data-what') || '';
    const note = btn.getAttribute('data-note') || '';
    panel.textContent = '';
    const head = document.createElement('p');
    head.textContent = '見積計算表を読んでいます…';
    panel.appendChild(head);
    const r = await getJSON('/api/estimate/rows?page_id=' + encodeURIComponent(pageId));
    if (!r.ok) { head.textContent = '⚠ ' + ((r.data && r.data.message) || '見積計算表を読めません'); return; }
    const tables = (r.data.tables || []).filter((t) => !t.why);
    if (!tables.length) { head.textContent = '⚠ 写せる見積計算表がありません（「工程」「価格」の列のある表に写せます）'; return; }
    // 写す先——いちばん新しい見積計算書があれば、そのページ（2026-10-17・サーバーが page_id で返す）。
    const target = (r.data && r.data.page_id) || pageId;
    head.textContent = '「' + what + '」 ' + price + '円（' + note + '）を' +
        (target !== pageId ? '見積計算書「' + (r.data.title || target) + '」の' : '') + '見積計算表へ写します:';
    const row = document.createElement('div');
    row.className = 'rfq-quote-put-form';
    const label = (text, el) => { const l = document.createElement('label'); l.append(document.createTextNode(text), el); return l; };
    const tSel = document.createElement('select');
    tSel.className = 'rfq-quote-put-table';
    tables.forEach((t) => {
        const o = document.createElement('option');
        o.value = String(t.index);
        o.textContent = (t.index + 1) + '枚目' + (t.lot ? '（ロット ' + t.lot + '）' : '');
        tSel.appendChild(o);
    });
    const rSel = document.createElement('select');
    rSel.className = 'rfq-quote-put-row';
    const newName = document.createElement('input');
    newName.type = 'text';
    newName.className = 'rfq-quote-put-name';
    newName.value = what;
    const val = document.createElement('input');
    val.type = 'text';
    val.className = 'rfq-quote-put-value';
    val.value = price;
    const fill = () => {
        const t = tables.find((x) => String(x.index) === tSel.value) || tables[0];
        rSel.textContent = '';
        let picked = '';
        (t.rows || []).forEach((rw) => {
            const o = document.createElement('option');
            o.value = rw.step;
            o.textContent = rw.step + (rw.price ? '（いま ' + rw.price + '）' : '');
            rSel.appendChild(o);
            if (!picked && (rw.step === what || (what && (what.includes(rw.step) || rw.step.includes(what))))) picked = rw.step;
        });
        const o = document.createElement('option');
        o.value = '__new';
        o.textContent = '＋ 新しい行を足す';
        rSel.appendChild(o);
        rSel.value = picked || '__new';
        newName.hidden = rSel.value !== '__new';
    };
    tSel.addEventListener('change', fill);
    rSel.addEventListener('change', () => { newName.hidden = rSel.value !== '__new'; });
    fill();
    const go = document.createElement('button');
    go.type = 'button';
    go.className = 'chip-btn rfq-quote-put-go';
    go.textContent = '写す';
    const cancel = document.createElement('button');
    cancel.type = 'button';
    cancel.className = 'chip-btn';
    cancel.textContent = 'やめる';
    cancel.addEventListener('click', () => { panel.textContent = ''; });
    const msg = document.createElement('span');
    msg.className = 'rfq-quote-put-say';
    row.append(label('表: ', tSel), label(' 行: ', rSel), newName, label(' 単価: ', val), document.createTextNode(' 円 '), go, cancel, msg);
    panel.appendChild(row);
    go.addEventListener('click', async () => {
        const t = tables.find((x) => String(x.index) === tSel.value) || tables[0];
        const add = rSel.value === '__new';
        const step = add ? newName.value.trim() : rSel.value;
        if (!step) { msg.textContent = ' ⚠ 新しい行の名前（工程）を書いてください'; return; }
        const cur = add ? null : (t.rows || []).find((rw) => rw.step === step);
        if (cur && cur.price && !window.confirm('「' + step + '」のいまの値 ' + cur.price + ' を ' + val.value.trim() + ' 円に置き換えます。よろしいですか？')) return;
        go.disabled = true;
        msg.textContent = ' 写しています…';
        const res = await postJSON('/api/estimate/put-cost', { page_id: target, index: t.index, step, add, value: val.value.trim(), note })
            .catch((e) => ({ ok: false, data: { message: String(e) } }));
        go.disabled = false;
        if (!res.ok) { msg.textContent = ' ⚠ ' + ((res.data && res.data.message) || '写せませんでした'); return; }
        location.reload();
    });
}

// ── 部材見積依頼（2026-10-03・段1——ext/toho/rfq.go・rfq_api.go）───────────────────────
//
// 再部材見積依頼フォームの「集める」・見積依頼必要部材表の「見積依頼部材表へ入れる」「不要（消す）」・見積依頼部材表の「↩ 戻す」。
// ⚠ **行は行番号で送ります**（サーバーが本文から読む——表示は丸めることがあるので値は送らない）。押したら読み直す。
// ⚠ 欄は表を包む節の中（表の直後）か、包まれていなければ表の足元（tfoot）——どちらからでも表を探す（発注部材表で
//    「欄を含む表」だけを探して黙って何もしなかった前科——2026-10-02）。
(function wireRFQ() {
    const rfqTableOf = (form) => {
        const inTable = form.closest('table');
        if (inTable) return inTable;
        const sec = form.closest('section');
        return sec ? sec.querySelector(':scope > table') : null;
    };
    const resultOf = (form, sel) => {
        const host = form.parentElement;
        return host ? host.querySelector(sel) : null;
    };
    // ── 装置フォルダから選ぶ（2026-10-03）——利用者:「装置フォルダのページ番号を入力する欄を作り、その下にある加工製品を
    //    列挙する一時的な表を作ります。その表でチェックした加工製品から見積依頼必要部材表に追加する」。
    //    表はここで組む（本文には残さない・読むのは /api/rfq/folder-products）。番号の欄はブラウザに憶える
    //    （サーバーの data-w-remember）ので、入れて読み直したあとも、戻した change でもう一度並ぶ。
    const cell = (tag, text) => {
        const c = document.createElement(tag);
        if (text !== undefined) c.textContent = text;
        return c;
    };
    async function listFolder(form) {
        const list = resultOf(form, '[data-rfq-folder-list]');
        if (!list) return;
        const folder = valueIn(form, '[data-rfq="folder"]');
        if (!folder) {
            list.replaceChildren();
            return;
        }
        // 続けて呼ばれたら（欄を離れた change と「一覧を出す」など）、最後の1回の答えだけを描く。
        const seq = (list._rfqSeq = (list._rfqSeq || 0) + 1);
        sayIn(list, '読んでいます…');
        const r = await getJSON('/api/rfq/folder-products?folder=' + encodeURIComponent(folder));
        if (list._rfqSeq !== seq) return;
        if (!r.ok) {
            sayIn(list, '⚠ ' + (r.data.message || '読めませんでした'), 'proc-why-ng');
            return;
        }
        const data = r.data;
        const products = data.products || [];
        list.replaceChildren();
        list.appendChild(cell('p', '/' + data.folder + ' ' + (data.title || '') + ' の下の加工製品: ' + products.length + ' 件'));
        if (!products.length) return;
        const rows = products.map((p) => {
            const lot = document.createElement('input');
            lot.type = 'number';
            lot.min = '1';
            lot.className = 'matsearch-input rfq-folder-lot';
            lot.placeholder = '空なら左';
            lot.setAttribute('data-rfq-folder-lot', '1');
            return { pick: p.id, cells: [
                pageLink(p.id),
                (p.machine && p.machine !== data.title ? p.machine + ' ／ ' : '') + p.title + (p.migrating ? '（移行中）' : ''),
                p.part_no || (p.drawing_no ? '図番 ' + p.drawing_no : ''),
                (p.lots || []).length ? p.lots.join('・') : '—（1個分）',
                lot,
            ] };
        });
        list.appendChild(folderPickTable(['弊社品番', '加工製品', '品番・図番', '見積表のロット', 'ロット'], rows,
            'data-rfq-folder-pick', 'rfq-folder-table'));
        const foot = cell('div');
        foot.className = 'matsearch-form';
        const go = cell('button', 'チェックした加工製品を見積依頼必要部材表へ');
        go.type = 'button';
        go.className = 'matsearch-go';
        go.setAttribute('data-rfq-folder-add', '1');
        foot.appendChild(go);
        list.appendChild(foot);
        const result = cell('div');
        result.className = 'unorder-result';
        result.setAttribute('data-rfq-folder-result', '1');
        list.appendChild(result);
    }
    // 番号の欄: 打って Enter・欄を離れたとき・ブラウザが憶えた値を戻したとき（どれも change）にも並べる。
    document.addEventListener('change', (e) => {
        const input = e.target instanceof Element && e.target.closest('.rfq-folder-form [data-rfq="folder"]');
        if (input) listFolder(input.closest('.rfq-folder-form'));
    });
    const picked = (table) => {
        const rows = [], tempRows = [];
        if (!table) return { rows, tempRows };
        table.querySelectorAll('input.rfq-check:checked').forEach((c) => {
            if (c.dataset.rfqRow) rows.push(Number(c.dataset.rfqRow));
            if (c.dataset.rfqTempRow) tempRows.push(Number(c.dataset.rfqTempRow));
        });
        return { rows, tempRows };
    };
    delegateClick([
        ['[data-rfq-collect]', async (btn) => {
            const form = btn.closest('.rfq-collect-form');
            if (!form) return;
            const box = resultOf(form, '[data-rfq-collect-result]');
            const product = valueIn(form, '[data-rfq="product"]');
            if (!product) {
                sayIn(box, '⚠ 弊社品番（加工製品ページの番号）を書いてください', 'proc-why-ng');
                return;
            }
            btn.disabled = true;
            sayIn(box, '集めています…');
            const r = await postJSON('/api/rfq/collect', {
                page_id: form.getAttribute('data-rfq-page') || '', product, lot: valueIn(form, '[data-rfq="lot"]'),
            });
            btn.disabled = false;
            if (!r.ok) {
                sayIn(box, '⚠ ' + (r.data.message || '集められませんでした'), 'proc-why-ng');
                return;
            }
            sayIn(box, r.data.rows + ' 行を見積依頼必要部材表へ入れました（ロット ' + (r.data.lots || []).join('・') + '）。読み直しています…');
            location.reload();
        }],
        ['[data-rfq-move]', async (btn) => {
            const form = btn.closest('.rfq-needs-form');
            if (!form) return;
            const box = resultOf(form, '[data-rfq-result]');
            const { rows, tempRows } = picked(rfqTableOf(form));
            btn.disabled = true;
            sayIn(box, '見積依頼部材表へ入れています…');
            const r = await postJSON('/api/rfq/needs/move', {
                page_id: form.getAttribute('data-rfq-page') || '', rows, temp_rows: tempRows,
                into: valueIn(form, '[data-rfq="into"]'),
            });
            btn.disabled = false;
            if (!r.ok) {
                sayIn(box, '⚠ ' + (r.data.message || '入れられませんでした'), 'proc-why-ng');
                return;
            }
            sayIn(box, r.data.rows + ' 行を見積依頼部材表へ入れました。読み直しています…');
            location.reload();
        }],
        ['[data-rfq-remove]', async (btn) => {
            const form = btn.closest('.rfq-needs-form');
            if (!form) return;
            const box = resultOf(form, '[data-rfq-result]');
            const { rows, tempRows } = picked(rfqTableOf(form));
            const n = rows.length + tempRows.length;
            if (!n) {
                sayIn(box, '⚠ 消す行を選んでください', 'proc-why-ng');
                return;
            }
            if (!window.confirm('選んだ ' + n + ' 行を消します（記録は残りません）。よろしいですか？')) return;
            btn.disabled = true;
            const r = await postJSON('/api/rfq/needs/remove', {
                page_id: form.getAttribute('data-rfq-page') || '', rows, temp_rows: tempRows,
            });
            btn.disabled = false;
            if (!r.ok) {
                sayIn(box, '⚠ ' + (r.data.message || '消せませんでした'), 'proc-why-ng');
                return;
            }
            sayIn(box, r.data.rows + ' 行を消しました。読み直しています…');
            location.reload();
        }],
        ['[data-rfq-folder]', (btn) => {
            const form = btn.closest('.rfq-folder-form');
            if (form) listFolder(form);
        }],
        ['[data-rfq-folder-add]', async (btn) => {
            const list = btn.closest('[data-rfq-folder-list]');
            const form = list && list.parentElement ? list.parentElement.querySelector('.rfq-folder-form') : null;
            if (!form) return;
            const box = list.querySelector('[data-rfq-folder-result]');
            const items = [];
            list.querySelectorAll('input[data-rfq-folder-pick]:checked').forEach((c) => {
                const tr = c.closest('tr');
                const lot = tr ? tr.querySelector('input[data-rfq-folder-lot]') : null;
                items.push({ product: c.getAttribute('data-rfq-folder-pick'), lot: lot ? lot.value.trim() : '' });
            });
            if (!items.length) {
                sayIn(box, '⚠ 入れる加工製品をチェックしてください', 'proc-why-ng');
                return;
            }
            btn.disabled = true;
            sayIn(box, '集めています…');
            const r = await postJSON('/api/rfq/collect', { page_id: form.getAttribute('data-rfq-page') || '', items });
            btn.disabled = false;
            if (!r.ok) {
                sayIn(box, '⚠ ' + (r.data.message || '集められませんでした'), 'proc-why-ng');
                return;
            }
            const skipped = r.data.skipped || [];
            // 飛ばしたものは読み直すと消えるので、先に知らせる（読み直したあとも一覧は憶えた番号で出る）。
            if (skipped.length) alert((r.data.products || []).length + ' 件・' + r.data.rows + ' 行を入れました。\n入れられなかったもの:\n' + skipped.join('\n'));
            sayIn(box, (r.data.products || []).length + ' 件・' + r.data.rows + ' 行を見積依頼必要部材表へ入れました。読み直しています…');
            location.reload();
        }],
        // 部材見積依頼書ページを作る（2026-10-03・段2——ext/toho/rfq_doc.go）。行はサーバーが本文から読む（表の番号だけ送る）。
        ['[data-rfq-doc-go]', async (btn) => {
            const form = btn.closest('.rfq-doc-form');
            if (!form) return;
            const box = resultOf(form, '[data-rfq-doc-result]');
            const body = {
                page_id: form.getAttribute('data-rfq-page') || '',
                table: Number(form.getAttribute('data-rfq-table') || 0),
                supplier: valueIn(form, '[data-rfqdoc="supplier"]'),
                signer: valueIn(form, '[data-unorder="signer"]'),
                date: valueIn(form, '[data-rfqdoc="date"]'),
                note: valueIn(form, '[data-rfqdoc="note"]'),
            };
            if (!body.supplier) {
                sayIn(box, '⚠ 仕入先を入れてください（部材見積依頼書は1枚に1社です）', 'proc-why-ng');
                return;
            }
            if (!body.signer) {
                sayIn(box, '⚠ 差出人を選んでください（紙に刷る署名です）', 'proc-why-ng');
                return;
            }
            btn.disabled = true;
            sayIn(box, '部材見積依頼書ページを作っています…');
            const r = await postJSON('/api/rfq/new', body);
            btn.disabled = false;
            if (!r.ok) {
                sayIn(box, '⚠ ' + (r.data.message || '部材見積依頼書ページを作れませんでした'), 'proc-why-ng');
                return;
            }
            if (r.data.draft_note) alert(r.data.draft_note);
            sayIn(box, '部材見積依頼書ページ /' + r.data.page_id + ' を作りました（' + r.data.rows + ' 行）。開いています…');
            location.href = '/' + r.data.page_id;
        }],
        ['.rfq-draft-back', async (btn) => {
            btn.disabled = true;
            const r = await postJSON('/api/rfq/draft/back', {
                page_id: btn.getAttribute('data-rfq-page') || '',
                table: Number(btn.getAttribute('data-rfq-table') || 0),
                row: Number(btn.getAttribute('data-rfq-row') || 0),
            });
            if (!r.ok) {
                btn.disabled = false;
                alert('⚠ ' + (r.data.message || '戻せませんでした'));
                return;
            }
            location.reload();
        }],
    ]);
})();

// ── 見積書を作る——装置フォルダから選ぶ（2026-10-03・ext/toho/estimate_folder.go）────────────────
//
// 利用者:「見積依頼フォルダと同じように、装置のフォルダのページIDを入れると加工製品を一時的な表に列挙します。必要な加工製品に
// チェックを入れてボタンを押すと、見積もりページが出来て、対応する加工製品ページから確定単価を収集して見積明細表を作ります。
// ボタンを押すとPDFが作られます」。
//
//   - 行は**見積表1枚につき1行**（ロット・単価・表面処理と備考）——単価を記録していない表は選べない行にして理由を出す
//     （2026-10-15 から見積表——それまでは見積計算表の確定単価）。
//   - 見積先は一覧を出すたびにその装置の社名を入れ直す（担当者の候補もその会社の人）。番号の欄だけブラウザに憶える。
//   - 見積明細表は一覧の順（押した順ではない——並びが予想どおりになるように）。
//   - 作れたらその見積書ページを開く——PDF はそのページの足元の「📄 PDFを作る」。
(function wireEstimateFolder() {
    const partsOf = (el) => {
        const root = el.closest('.vocab-chrome') || el.parentElement;
        return {
            form: root.querySelector('.estimate-folder-form'),
            box: root.querySelector('[data-estf-box]'),
            make: root.querySelector('[data-estf-make]'),
            say: root.querySelector('[data-estf-result]'),
        };
    };
    async function listEstimateFolder(form) {
        const { box, make, say } = partsOf(form);
        if (!box || !make) return;
        const folder = valueIn(form, '[data-estf="folder"]');
        sayIn(say, '');
        if (!folder) {
            box.replaceChildren();
            make.hidden = true;
            return;
        }
        // 続けて呼ばれたら（欄を離れた change と「一覧を出す」など）、最後の1回の答えだけを描く。
        const seq = (box._estfSeq = (box._estfSeq || 0) + 1);
        sayIn(box, '読んでいます…');
        const r = await getJSON('/api/estimate/folder-products?folder=' + encodeURIComponent(folder));
        if (box._estfSeq !== seq) return;
        if (!r.ok) {
            sayIn(box, '⚠ ' + (r.data.message || '読めませんでした'), 'proc-why-ng');
            make.hidden = true;
            return;
        }
        const data = r.data;
        const products = data.products || [];
        const rows = [];
        products.forEach((p) => {
            const name = (p.machine && p.machine !== data.title ? p.machine + ' ／ ' : '') + p.title + (p.migrating ? '（移行中）' : '');
            const part = p.part_no || (p.drawing_no ? '図番 ' + p.drawing_no : '');
            if (!(p.tables || []).length) {
                rows.push({ pick: null, cells: [pageLink(p.id), name, part, '', '', '見積表なし'] });
                return;
            }
            p.tables.forEach((t) => {
                if (t.why) {
                    rows.push({ pick: null, cells: [pageLink(p.id), name, part, '', '', '⚠ 単価がありません: ' + t.why] });
                    return;
                }
                rows.push({ pick: p.id + ':' + t.index, cells: [pageLink(p.id), name, part,
                    (t.quantity || '') + (t.quantity ? ' ' + (t.unit || '') : ''),
                    Number(t.price).toLocaleString('ja-JP') + '円', t.note || ''] });
            });
        });
        box.replaceChildren();
        const head = document.createElement('p');
        head.textContent = '/' + data.folder + ' ' + (data.title || '') + ' の下の加工製品: ' + products.length + ' 件';
        box.appendChild(head);
        if (rows.length) {
            box.appendChild(folderPickTable(['弊社品番', '加工製品', '品番・図番', '数量（ロット）', '単価', '備考'], rows,
                'data-estf-pick', 'estimate-folder-table'));
        }
        const pickable = rows.some((x) => x.pick !== null);
        make.hidden = !pickable;
        if (!pickable) return;
        const client = make.querySelector('[data-estf="client"]');
        if (client) client.value = data.client || '';
        const person = make.querySelector('[data-estf="person"]');
        if (person) person.value = '';
        const persons = make.querySelector('datalist[data-estf="persons"]');
        if (persons) {
            persons.replaceChildren();
            (data.persons || []).forEach((n) => {
                const o = document.createElement('option');
                o.value = n;
                persons.appendChild(o);
            });
        }
    }
    delegateClick([
        ['[data-estf-list]', (btn) => {
            const form = btn.closest('.estimate-folder-form');
            if (form) listEstimateFolder(form);
        }],
        ['[data-estf-make-go]', async (btn) => {
            const { box, make, say } = partsOf(btn);
            const items = [];
            box.querySelectorAll('input[data-estf-pick]:checked').forEach((c) => {
                const v = c.getAttribute('data-estf-pick');
                const at = v.lastIndexOf(':');
                items.push({ product: v.slice(0, at), index: Number(v.slice(at + 1)) });
            });
            if (!items.length) {
                sayIn(say, '⚠ 見積書に入れる見積表をチェックしてください', 'proc-why-ng');
                return;
            }
            btn.disabled = true;
            sayIn(say, '見積書を作っています…');
            const r = await postJSON('/api/estimate/from-folder', {
                items,
                client: valueIn(make, '[data-estf="client"]'),
                person: valueIn(make, '[data-estf="person"]'),
                signer: valueIn(make, 'select.estimate-add-signer'),
            });
            btn.disabled = false;
            if (!r.ok) {
                sayIn(say, '⚠ ' + (r.data.message || '見積書を作れませんでした'), 'proc-why-ng');
                return;
            }
            sayIn(say, '見積書 /' + r.data.page_id + ' を作りました（' + r.data.rows + ' 行）。開いています…');
            location.href = '/' + r.data.page_id;
        }],
        // 未送付の見積書（2026-10-03・ext/toho/estimate_unsent.go）——「送った（FAX・手渡し）」「送付不要」。
        // 利用者:「見積書を創ったら送付するか、必要ないと記すまで、未送付としてフォルダにいて欲しい」。
        ['[data-est-mark]', async (btn) => {
            const mark = btn.getAttribute('data-est-mark');
            const id = btn.getAttribute('data-est-page') || '';
            const msg = mark === 'nosend'
                ? '見積書 /' + id + ' を「送付不要」にします（未送付の一覧から外れます）。よろしいですか？'
                : '見積書 /' + id + ' を FAX・手渡しで送ったことにして、送付日に今日の日付を書きます。よろしいですか？';
            if (!window.confirm(msg)) return;
            const say = btn.closest('table') ? btn.closest('table').parentElement.querySelector('[data-est-mark-result]') : null;
            btn.disabled = true;
            const r = await postJSON('/api/estimate/mark', { page_id: id, mark });
            if (!r.ok) {
                btn.disabled = false;
                if (say) sayIn(say, '⚠ ' + (r.data.message || '書けませんでした'), 'proc-why-ng');
                else alert('⚠ ' + (r.data.message || '書けませんでした'));
                return;
            }
            location.reload();
        }],
    ]);
    // 番号の欄: 打って Enter・欄を離れたとき・ブラウザが憶えた値を戻したとき（どれも change）にも並べる。
    document.addEventListener('change', (e) => {
        const input = e.target instanceof Element && e.target.closest('.estimate-folder-form [data-estf="folder"]');
        if (input) listEstimateFolder(input.closest('.estimate-folder-form'));
    });
})();

// ── 受注残表の印刷（2026-09-21）────────────────────────────────────────
//
// ユーザー:「顧客、納期ごとに別の表として分けて、**ワンタッチで印刷**もできると
// ありがたい」「**表ごとに印刷ボタン**」。
//
// ⚠ **最初の実装は「隠すものを名指しする」形で、失敗しました**（同日・実機で
// 「表ではなくページ全体が印刷されました」）。レールやフッタは消していましたが、
// **本文の見出しや子ページ一覧が残っていた**ためです。
//
// いまは逆向きです——**刷る1枚を `#w-print-area` へ写し、body の他の子をまとめて
// 消します**（app.css の `@media print`）。殻に何が増えても効きます。
//
// ⚠ **写し（クローン）を使い、画面の要素は動かしません。** 動かすと、印刷から
// 戻ったときに元の場所へ返す仕事が要り、失敗すると画面が壊れます。
//
// ⚠ **ボタンはサーバーが描いたクロームです**（ext/toho/backlog.go）。本文は
// 読み込み直しで作り替わるので、**要素ごとに配線せず document へ委譲します**。
// ⚠ **インラインの `onclick` は書けません**（CSP strict）。
delegateClick([['.backlog-print', (btn) => {
    const sheet = btn.closest('.backlog-sheet');
    if (!sheet) return;
    const copy = sheet.cloneNode(true);
    copy.querySelectorAll('.backlog-print').forEach((b) => b.remove());
    printOnly(copy);
}]]);

// openPickQtyPanel は、案件残表で選んだ行（`.<cls>-pick:checked`）と、今回の数の欄（初めは data-<qty> の数・直せる）を
// 欄 `.<cls>-panel` に開きます。押すボタンは `.<cls>-make`、数の欄は `.<cls>-qty`、知らせは `.<cls>-msg` です。
// ⚠ 1回に1社（客先の違う行は画面でも止める）。納品書〔10-17〕と製造〔10-19〕が同じ組み立てを写していたので寄せた（2026-10-19）。
// 表の class はどちらも `backlog-dlv-table`（見た目は納品書の欄と同じ）。
function openPickQtyPanel(btn, cfg) {
    const bar = btn.parentElement;
    const say = bar.querySelector('.' + cfg.cls + '-say');
    const panel = bar.querySelector('.' + cfg.cls + '-panel');
    const picks = Array.from(document.querySelectorAll('#w-editor-content .' + cfg.cls + '-pick:checked'));
    if (!picks.length) {
        sayIn(say, cfg.none, 'warn');
        return;
    }
    const clients = Array.from(new Set(picks.map((c) => c.dataset.client || '')));
    if (clients.length > 1) {
        sayIn(say, cfg.mixed(clients.join('・')), 'warn');
        return;
    }
    sayIn(say, '');
    panel.textContent = '';
    const head = document.createElement('p');
    head.textContent = cfg.head(clients[0] || '（発注元なし）');
    panel.appendChild(head);
    const table = document.createElement('table');
    table.className = 'materials-table backlog-dlv-table';
    const tr0 = document.createElement('tr');
    cfg.cols.forEach((h) => { const th = document.createElement('th'); th.textContent = h; tr0.appendChild(th); });
    table.appendChild(tr0);
    picks.forEach((c) => {
        const tr = document.createElement('tr');
        [c.dataset.item, c.dataset.name, c.dataset[cfg.qty]].forEach((v) => { const td = document.createElement('td'); td.textContent = v || ''; tr.appendChild(td); });
        const td = document.createElement('td');
        const inp = document.createElement('input');
        inp.type = 'number';
        inp.min = '1';
        inp.value = c.dataset[cfg.qty] || '';
        inp.className = cfg.cls + '-qty';
        inp.dataset.order = c.dataset.order;
        inp.dataset.row = c.dataset.row;
        inp.dataset.item = c.dataset.item || '';
        td.appendChild(inp);
        tr.appendChild(td);
        table.appendChild(tr);
    });
    panel.appendChild(table);
    const make = document.createElement('button');
    make.type = 'button';
    make.className = cfg.cls + '-make';
    make.textContent = cfg.make;
    panel.appendChild(make);
    const msg = document.createElement('span');
    msg.className = cfg.cls + '-msg';
    panel.appendChild(msg);
    panel.hidden = false;
}

// ── 受注残表から納品書を作る（2026-10-17・ext/toho/delivery.go） ──────────────────
//
// 利用者:「分納はよくあります。」「納品書をまとめることはあります。まとめない場合もあります。」。受注残表の「納品」の欄で選んだ
// 行（受注ページ・行・品番・残——サーバーが描いた data-*）について、今回納める数（残りが初めから入っていて直せる）の欄を開き、
// 「納品書を作る」で /api/delivery/new へ送って、できた納品書ページを開く。⚠ 1枚に1社（客先の違う行は画面でも止める）。
delegateClick([
    ['.backlog-dlv-go', (btn) => openPickQtyPanel(btn, {
        cls: 'backlog-dlv',
        none: '案件残表の右端の「納品」の欄で、納める行を選んでください。',
        mixed: (names) => '納品書は1枚に1社です（' + names + ' が混ざっています）。',
        head: (client) => '「' + client + '」への納品書——今回納める数を確かめてください:',
        cols: ['品番', '品名', '残', '今回納める数'],
        qty: 'remaining',
        make: '🚚 納品書を作る',
    })],
    // 受注残表から製造を始める（2026-10-19・ext/toho/mfg.go）——利用者:「受注残表でチェックして製造開始ボタンを押すと、
    // 製造フォルダに入るのはどうですか？」。選んだ行（「製造」の欄）と今回作る数（まだ製造に入っていない数が初めから入っていて直せる）。
    // ⚠ 1つの製造は1社（客先の違う行は画面でも止める）。
    ['.backlog-mfg-go', (btn) => openPickQtyPanel(btn, {
        cls: 'backlog-mfg',
        none: '案件残表の「製造」の欄で、製造を始める行を選んでください。',
        mixed: (names) => '1つの製造は1社です（' + names + ' が混ざっています）。',
        head: (client) => '「' + client + '」の製造——今回作る数を確かめてください:',
        cols: ['品番', '品名', 'まだ製造に入っていない数', '今回作る数'],
        qty: 'free',
        make: '🏭 製造を始める',
    })],
    ['.backlog-mfg-make', async (btn) => {
        const panel = btn.closest('.backlog-mfg-panel');
        const msg = panel.querySelector('.backlog-mfg-msg');
        const items = Array.from(panel.querySelectorAll('.backlog-mfg-qty')).map((i) => ({
            order: i.dataset.order, row: Number(i.dataset.row), item: i.dataset.item, qty: i.value,
        }));
        btn.disabled = true;
        sayIn(msg, '製造ページを作っています…');
        const r = await postJSON('/api/mfg/new', { items }).catch((e) => ({ ok: false, data: { message: String(e) } }));
        btn.disabled = false;
        if (!r.ok) { sayIn(msg, '⚠ ' + ((r.data && r.data.message) || '製造ページを作れませんでした')); return; }
        location.href = r.data.url;
    }],
    ['.backlog-dlv-make', async (btn) => {
        const panel = btn.closest('.backlog-dlv-panel');
        const msg = panel.querySelector('.backlog-dlv-msg');
        const items = Array.from(panel.querySelectorAll('.backlog-dlv-qty')).map((i) => ({
            order: i.dataset.order, row: Number(i.dataset.row), item: i.dataset.item, qty: i.value.trim(),
        }));
        btn.disabled = true;
        msg.textContent = ' 作っています…';
        let r;
        try {
            r = await postJSON('/api/delivery/new', { items });
        } catch (e) {
            msg.textContent = ' ⚠ 通信に失敗しました: ' + e;
            btn.disabled = false;
            return;
        }
        if (!r.ok) {
            msg.textContent = ' ⚠ ' + ((r.data && r.data.message) || ('作れませんでした（' + r.status + '）'));
            btn.disabled = false;
            return;
        }
        location.href = '/' + r.data.page_id;
    }],
]);

// （受注残表から部材見積依頼へ送る「☑ 選んだ行を部材見積依頼へ」は 2026-10-19 に外した——部材見積依頼は製造ページから・ext/toho/mfg_more.go。）

// printOnly は node（写し）だけを紙に刷ります。受注残表の印刷と、どの表にも付く「⋯」の「🖨 この表を印刷」が使います。
//
// **見えない別の枠（iframe `#w-print-frame`）に写しを置き、その枠だけを刷ります**（2026-10-03）。枠の中は app.css を読み、
// `body.w-printing-sheet` と `#w-print-area` の形で受注残表と同じ紙の決まり（`@media print`）が効きます。
//
// ⚠ **ページそのものを刷ってはいけません**——ページに図面の PDF の埋め込みがあると、Chrome は print() を「まだ読み込み中」
//    として後回しにし、印刷の画面を開きません（エラーも出ない）。2026-10-02 夜に利用者:「この表を印刷を押しても何も起きません」
//    （加工製品ページの材料表）。その夜は「刷るあいだだけ埋め込みを外す」で直したが、職場では「二回押さないと開きません」
//    「一回で開くページもありました」（10-03）——外してから Chrome がそれを受け取るまでの間合いしだいで、1回目の print() が
//    後回しのまま消えていた（外した埋め込みは戻らないまま残り、2回目は外すものが無いので開く）。別の枠は自分の読み込みが
//    済んでから刷るので、ページの PDF の読み込みに左右されず、埋め込みを外す必要もありません。
// ⚠ 枠は大きさ0で画面の外（`#w-print-frame`・app.css）。`display:none` にすると刷れないブラウザがある。
// ⚠ 枠は次に刷るときまで残します（刷り終わりの合図はブラウザで当てにならない——0の大きさなので邪魔にならない）。
//    試験は枠の中身と、print() を呼んだ印（`data-printed`）を見ます。
function printOnly(node) {
    const old = document.getElementById('w-print-frame');
    if (old) old.remove();
    const frame = document.createElement('iframe');
    frame.id = 'w-print-frame';
    frame.setAttribute('aria-hidden', 'true');
    frame.tabIndex = -1;
    document.body.appendChild(frame);

    const doc = frame.contentDocument;
    const link = doc.createElement('link');
    link.rel = 'stylesheet';
    link.href = new URL('/assets/app.css', location.href).href;
    doc.head.appendChild(link);
    doc.title = document.title;
    doc.body.className = 'w-printing-sheet';
    const area = doc.createElement('div');
    area.id = 'w-print-area';
    area.appendChild(doc.importNode(node, true));
    doc.body.appendChild(area);

    // 紙の決まり（app.css）を読み終えてから刷る。読めなくても刷る（罫線が無いだけ）。
    let started = false;
    const go = () => {
        if (started) return;
        started = true;
        frame.setAttribute('data-printed', '1');
        frame.contentWindow.focus();
        frame.contentWindow.print();
    };
    link.addEventListener('load', go, { once: true });
    link.addEventListener('error', go, { once: true });
    setTimeout(go, 2000);
}

// ── どの表にも「⋯」——表の操作のメニュー（2026-10-02 の「🖨 この表を印刷」を 10-15 にメニューへ）──────────
//
// 2026-10-02 利用者:「こういった表を印刷できるようになりませんか？」（Excel から貼った表）→「どの表にも『🖨 この表を印刷』」。
// 2026-10-15 利用者:「表に付く「この表を印刷」というボタンをよくある点3つのボタンに変えて開くメニューの中に納めてください。この点3つのボタンは
// 表のキャプションと同じ高さの右側に配置できると良いと思います。表に対するコマンドは印刷以外にも増えると思うからです」。
//
//   - 表の直前に高さ0の入れ物（.w-table-menu）を置き、「⋯」をキャプションの行の右端に重ねます（表は欄の幅いっぱいの
//     ブロックなので、キャプションの行も欄の幅いっぱい）。キャプションの無い表では、表の上の右端に1行で置きます。
//   - 押すとメニュー（#w-table-menu-list・placeFloating で寄せる）。項目は TABLE_MENU_ITEMS に足します（when で出す表を選ぶ）。
//   - 印刷は**その表の写しだけ**を刷ります（受注残表の印刷と同じ printOnly）。紙の頭にはページの題を置きます。
//
// ⚠ **閲覧モードだけ**——書ける形では出しません（`applyMode` が呼ぶたびに作り直す）。入れ物はクローム
//    （`.vocab-chrome`）なので保存されません。
// ⚠ キャプションの中には入れません——キャプションの文字は表の名前で、読み手が文字で比べるため。
// ⚠ **案件残表（2026-10-19 までの名前は受注残表）は自分のボタンを持つ**ので付けません。表の中の表にも付けません。
// ⚠ 紙には操作の部品を出しません——ボタン・入力欄・選ぶ欄を外し、外して空になったサーバーのセル（「↩ 戻す」の列など）も
//    外します。値の入ったサーバーのセル（最新単価など）は画面と同じに残します。
const TABLE_MENU_ITEMS = [
    { icon: '🖨', label: 'この表を印刷', run: (table) => printTable(table) },
];

function refreshTableMenus() {
    closeTableMenu();
    document.querySelectorAll('#w-editor-content .w-table-menu').forEach((b) => b.remove());
    if (document.body.hasAttribute('edit-mode')) return;
    document.querySelectorAll('#w-editor-content table').forEach((t) => {
        if (t.parentElement && t.parentElement.closest('table')) return;
        if (t.closest('.backlog-sheet')) return;
        const box = document.createElement('div');
        box.className = 'vocab-chrome w-table-menu' + (t.querySelector(':scope > caption') ? ' on-caption' : '');
        box.contentEditable = 'false';
        const btn = document.createElement('button');
        btn.type = 'button';
        btn.className = 'w-table-menu-btn';
        btn.textContent = '⋯';
        btn.title = 'この表の操作（印刷…）';
        btn.setAttribute('aria-haspopup', 'menu');
        box.appendChild(btn);
        t.insertAdjacentElement('beforebegin', box);
    });
}

// floatingMenu は「⋯」から開く浮いたメニューの入れ物です（無ければ作る）。外を押すか Esc で close を呼びます。
// fillMenuItems は項目（icon・label・title）のボタンを並べ、押すと pick(it) を呼びます。
// ⚠ ボタンの mousedown は止めます——キャレット（書きかけの場所）を動かさないため。
// 2026-10-19 に寄せた（ファイルの「⋯」〔10-01〕と表の「⋯」〔10-15〕が同じ組み立てを写していた）。
function floatingMenu(id, className, close) {
    let menu = document.getElementById(id);
    if (menu) return menu;
    menu = document.createElement('div');
    menu.id = id;
    menu.className = className;
    menu.setAttribute('role', 'menu');
    document.body.appendChild(menu);
    document.addEventListener('mousedown', (e) => {
        const m = document.getElementById(id);
        if (m && m.classList.contains('active') && !m.contains(e.target) && e.target !== m.wcmsAnchor) close();
    });
    document.addEventListener('keydown', (e) => { if (e.key === 'Escape') close(); });
    return menu;
}

function fillMenuItems(menu, items, pick) {
    items.forEach((it) => {
        const b = document.createElement('button');
        b.type = 'button';
        b.className = 'file-menu-item';
        b.setAttribute('role', 'menuitem');
        b.textContent = it.icon + ' ' + it.label;
        if (it.title) b.title = it.title;
        b.addEventListener('mousedown', (e) => e.preventDefault());
        b.addEventListener('click', (e) => {
            e.preventDefault();
            e.stopPropagation();
            pick(it);
        });
        menu.appendChild(b);
    });
}

// tableOfMenu は「⋯」の入れ物のすぐ後ろの表です。
function tableOfMenu(btn) {
    const box = btn.closest('.w-table-menu');
    const t = box && box.nextElementSibling;
    return t && t.tagName === 'TABLE' ? t : null;
}

// openTableMenu は「⋯」の下にメニューを開きます（もう一度押すか、外を押すか、Esc で閉じる）。
function openTableMenu(btn) {
    const table = tableOfMenu(btn);
    if (!table) return;
    const menu = floatingMenu('w-table-menu-list', 'file-menu w-table-menu-list', closeTableMenu);
    if (menu.classList.contains('active') && menu.wcmsAnchor === btn) { closeTableMenu(); return; }
    menu.textContent = '';
    menu.wcmsAnchor = btn;
    fillMenuItems(menu, TABLE_MENU_ITEMS.filter((it) => !it.when || it.when(table)), (it) => { closeTableMenu(); it.run(table); });
    menu.classList.add('active');
    // 右端の「⋯」から左へ開く（右へはみ出さないように placeFloating が寄せる）。
    const r = btn.getBoundingClientRect();
    const w = menu.getBoundingClientRect().width || 0;
    if (window.wcmsPlaceFloating) window.wcmsPlaceFloating(menu, { left: r.right - w, bottom: r.bottom }, 4);
}

function closeTableMenu() {
    const menu = document.getElementById('w-table-menu-list');
    if (menu) { menu.classList.remove('active'); menu.wcmsAnchor = null; }
}

// printableTableCopy は表の写しから、紙に要らない操作の部品を外したものを返します。
function printableTableCopy(table) {
    const copy = table.cloneNode(true);
    copy.querySelectorAll('button, input, select, textarea, .w-table-menu, .draft-form-box, .w-sort-mark')
        .forEach((el) => el.remove());
    copy.querySelectorAll('th.vocab-chrome, td.vocab-chrome').forEach((c) => {
        if (!c.textContent.trim()) c.remove();
    });
    copy.querySelectorAll('tr').forEach((tr) => {
        if (!tr.children.length) tr.remove();
    });
    return copy;
}

// printTable はその表の写しだけを紙に刷ります（紙の頭にページの題）。
function printTable(table) {
    const sheet = document.createElement('div');
    sheet.className = 'w-print-sheet';
    const h1 = document.querySelector('#w-editor-content h1');
    if (h1 && h1.textContent.trim()) {
        const title = document.createElement('p');
        title.className = 'w-print-title';
        title.textContent = h1.textContent.trim();
        sheet.appendChild(title);
    }
    sheet.appendChild(printableTableCopy(table));
    printOnly(sheet);
}

delegateClick([['.w-table-menu-btn', (btn) => openTableMenu(btn)]]);

// ── 受注残表から受注明細表を書き換える（2026-09-21）────────────────────────
//
// ユーザー:「**受注トップページの受注残集計表を鏡にして受注ページの受注明細表が
// 編集される**ようにしましょう」。
//
// ⚠ **押した先は別のページの本文です。** サーバーが**行番号・品番・いまの値の3つ**で
// 照合します（ext/toho/order_edit.go）。ここの仕事は**送ることと、失敗したら
// 画面を元へ戻すこと**です。
//
// ⚠ **押せたのに保存できていない、がいちばん困る壊れ方**なので、
// 失敗したら必ず値を戻して、理由を出します。
//
// ⚠ **`data-old` を成功のたびに更新します。** 忘れると、2回目の変更で
// 「いまの値が違う」と断られます（自分の1回目の変更と食い違うため）。
(function wireBacklogEditing() {
    const valueOf = (el) => (el.type === 'checkbox' ? (el.checked ? '済' : '') : el.value);
    const setValue = (el, v) => {
        if (el.type === 'checkbox') el.checked = (v === '済');
        else el.value = v;
    };

    // showWhyHere は理由をそのセルの中に出します（空なら消す）。
    //
    // ⚠ **`title` だけでは伝わりません**（指で触る画面では出ません）。⚠ そして
    // トーストは使えません——`showToast` はこのファイルの IIFE の中にあり、
    // **外からは呼べません**（`typeof` で守ると、黙って何も出ない形になります）。
    function showWhyHere(el, why) {
        const cell = el.closest('td');
        if (!cell) return;
        let msg = cell.querySelector('.order-edit-msg');
        if (!why) { if (msg) msg.remove(); return; }
        if (!msg) {
            msg = document.createElement('span');
            msg.className = 'order-edit-msg';
            cell.appendChild(msg);
        }
        msg.textContent = why;
    }

    async function send(el) {
        const tr = el.closest('tr');
        if (!tr) return;
        const body = {
            page_id: tr.getAttribute('data-order-page'),
            row: parseInt(tr.getAttribute('data-order-row'), 10),
            item_no: tr.getAttribute('data-order-item'),
            field: el.getAttribute('data-field'),
            old: el.getAttribute('data-old') || '',
            value: valueOf(el),
        };
        if (body.old === body.value) return; // 変わっていないなら送らない
        el.classList.remove('order-edit-ng', 'order-edit-ok');
        el.disabled = true;
        try {
            const r = await postJSON('/api/order-item', body);
            if (!r.ok) {
                // ⚠ **画面を元へ戻します。** 戻さないと「押したのに保存されていない」
                // 状態が残り、次に見た人が正しいと思い込みます。
                setValue(el, body.old);
                el.classList.add('order-edit-ng');
                showWhyHere(el, r.data.message || ('保存できません（' + r.status + '）'));
                return;
            }
            // ⚠ **次の変更のために、いまの値を更新します**（忘れると2回目が断られます）。
            el.setAttribute('data-old', body.value);
            showWhyHere(el, '');
            el.classList.add('order-edit-ok');
            setTimeout(() => el.classList.remove('order-edit-ok'), 1200);
        } catch (e) {
            setValue(el, body.old);
            el.classList.add('order-edit-ng');
            showWhyHere(el, '保存できません（通信に失敗しました）');
        } finally {
            el.disabled = false;
        }
    }

    // ⚠ **document へ委譲します**——受注残表はサーバーが描き直すので、要素ごとに
    // 配線すると描き直しのたびに切れます。
    document.addEventListener('change', (e) => {
        const el = e.target;
        if (el && el.classList && el.classList.contains('order-edit')) send(el);
    });
    // 文字の欄は「離れたとき」にも送る（`change` は値が変わったときだけ出る）。
    document.addEventListener('blur', (e) => {
        const el = e.target;
        if (el && el.classList && el.classList.contains('order-edit') && el.type === 'text') send(el);
    }, true);
})();

// ── 材料を探す（2026-09-21）──────────────────────────────────────────────
//
// ユーザー:「あとで**材質形状寸法で検索**したいときがあります。問題は寸法ですが、
// 検索できるように**分解してDBに入れる**ことは出来ますか？」。
//
// ⚠ **欄はサーバーが描きます**（ext/toho/view_material_search.go）。ここの仕事は
// **押したときに送って、返ってきた行を並べること**だけです。⚠ 画面が欄を自分で
// 組むと、**拡張を外したビルドで空の欄だけが残ります**（3d-4 の一般則）。
//
// ⚠ **`innerHTML` へ文字列を入れません**（`createElement`＋`textContent`）。
// 結果には本文から来た文字（材質・形状・寸法・仕入先）が入るので、
// 組み立てを誤ると保存型XSSになります。
(function wireMaterialSearch() {
    const FIELDS = ['material', 'shape', 'thickness', 'diameter', 'values'];

    function cell(row, text, cls) {
        const td = document.createElement('td');
        if (cls) td.className = cls;
        td.textContent = text;
        row.appendChild(td);
        return td;
    }

    // renderHits は結果の表を組みます。⚠ **0件も黙りません**——「まだ押していない」と
    // 「押したが無かった」は別物です。
    function renderHits(box, hits) {
        box.textContent = '';
        if (!hits || hits.length === 0) {
            const p = document.createElement('p');
            p.className = 'matsearch-empty';
            p.textContent = '見つかりませんでした（条件を緩めてみてください）';
            box.appendChild(p);
            return;
        }
        const table = document.createElement('table');
        table.className = 'matsearch-table';
        const head = document.createElement('tr');
        ['材質', '形状', '寸法', '最新単価', '出所', 'ページ'].forEach((h) => {
            const th = document.createElement('th');
            th.textContent = h;
            head.appendChild(th);
        });
        table.appendChild(head);
        hits.forEach((h) => {
            const tr = document.createElement('tr');
            cell(tr, h.material || '—');
            cell(tr, h.shape || '—');
            cell(tr, h.size || '—');
            // ⚠ **引けなかったことを黙りません**（材料表の鏡と同じ言い方に揃える）。
            cell(tr, h.cost ? h.cost.toLocaleString('ja-JP') + '円' : '⚠ 記録なし',
                h.cost ? 'matsearch-cost' : 'matsearch-none');
            cell(tr, [h.at, h.supplier].filter(Boolean).join(' ') || '—', 'matsearch-src');
            const td = document.createElement('td');
            const a = document.createElement('a');
            a.href = '/' + String(h.page_id).padStart(6, '0');
            a.textContent = h.page_title || String(h.page_id);
            td.appendChild(a);
            if (h.migrating) {
                // ⚠ **移行の確認前だと分かるようにします**（表引きは止めてあるが、
                //    探して見つかること自体は本当なので、隠さずに印を付ける）。
                const mark = document.createElement('span');
                mark.className = 'matsearch-migrating';
                mark.textContent = ' ⚠ 移行の確認前';
                td.appendChild(mark);
            }
            tr.appendChild(td);
            table.appendChild(tr);
        });
        box.appendChild(table);
    }

    async function search(go) {
        const form = go.closest('.matsearch-form');
        const box = form && form.parentElement
            ? form.parentElement.querySelector('[data-matsearch-result]') : null;
        if (!form || !box) return;
        const body = {};
        FIELDS.forEach((f) => { body[f] = valueIn(form, '[data-matsearch="' + f + '"]'); });
        go.disabled = true;
        box.textContent = '探しています…';
        try {
            const r = await postJSON('/api/material-search', body);
            if (!r.ok) {
                box.textContent = r.data.message || ('探せません（' + r.status + '）');
                return;
            }
            renderHits(box, r.data.hits);
        } catch (e) {
            box.textContent = '探せません（通信に失敗しました）';
        } finally {
            go.disabled = false;
        }
    }

    // ⚠ **document へ委譲します**——欄はサーバーが描き直すので、要素ごとに配線すると
    //    描き直しのたびに切れます（受注残表の編集と同じ理由）。
    delegateClick([['[data-matsearch-go]', search]]);
    // Enter でも探せるようにする（欄に指を置いたまま押せる）。
    document.addEventListener('keydown', (e) => {
        if (e.key !== 'Enter') return;
        const el = e.target;
        if (!el || !el.classList || !el.classList.contains('matsearch-input')) return;
        const form = el.closest('.matsearch-form');
        const go = form ? form.querySelector('[data-matsearch-go]') : null;
        if (go) { e.preventDefault(); search(go); }
    });
})();

// ── 未手配の一覧から部材発注書を作る（2026-09-21）────────────────────────────
//
// ユーザー:「弊社の発注書は、**受注明細の単位とは無関係に、納期のグループなどから
// 発行**されます」。⚠ **だから起点は受注ページではなく、受注横断の未手配の一覧**です。
//
// ⚠ **仕入先を決めるのは人**（相見積もりを見て決めるので機械には決められない）。
// ここの仕事は**選ばれた行を送ることと、結果を出すこと**だけです。
//
// ⚠ **選ばれていない行は何もしません**——既定は未選択で、押した行だけが入ります。
(function wireUnorderedOrder() {
    // ⚠ **送る値は属性から採ります**（表示は丸めることがあるので本文から読まない）。
    function lineOf(tr) {
        const d = tr.dataset;
        // ⚠ **臨時部材表から来た行**（2026-09-25）は、品番・表面・単位・備考と
        //    「何行目から来たか」も運びます——発注部材表へ入れたら、サーバーが
        //    臨時部材表からその行を消します（移す・引き算しない）。
        return {
            product_id: d.product || '',
            // どの受注のための部材か（2026-10-01・受注ごとに数える——サーバーの procure_ledger.go）。
            for_order: d.forOrder || '',
            item_id: d.itemid || '',
            material: d.material || '',
            shape: d.shape || '',
            size: d.size || '',
            color: d.color || '',
            item_name: d.itemname || '',
            // 種類・番号・加工内容・仕様・支給（2026-09-28・部材の種類ごとの運び方——サーバーの
            // orderLineAttrs が属性に載せる）。
            kind: d.kind || '', no: d.no || '', work: d.work || '',
            spec: d.spec || '', supplied: d.supplied || '',
            quantity: d.qty || '',
            unit: d.unit || '',
            cost: d.cost && d.cost !== '0' ? d.cost : '',
            note: d.note || '',
            temp_page: d.tempPage || '',
            temp_row: Number(d.tempRow || 0),
        };
    }

    // 発注部材表の足元から**部材発注書ページ**を作る（2026-09-22・ユーザーの流れ）。
    //
    // ⚠ **ボタンは発注部材表の下に居ます**——「発注部材表から発注書を作るので
    //    『発注書を作る』ボタンは発注部材表の下にあるはずです」（ユーザー訂正）。
    //    だから**行は、その表から読みます**（未発注の表ではありません）。
    async function create(go) {
        const form = go.closest('.draft-form');
        const box = form && form.parentElement
            ? form.parentElement.querySelector('[data-draft-result]') : null;
        // ⚠ **欄は表の外に居ます**（2026-09-28 から——発注部材表を節で包み、欄を表の直後へ置いた）。
        //    それまでの `form.closest('table')` は節の形で空になり、**押しても黙って何もしませんでした**
        //    （2026-10-02 利用者:「発注部材表から発注書が作れません」——09-28 の夕方から1枚も作れていなかった）。
        //    節で包まない古い形（欄が表の中）も読めるよう、両方を探します。
        const table = form ? (form.closest('table') || draftTableOf(form)) : null;
        if (!form || !box) return;
        if (!table) {
            sayIn(box, '⚠ 発注部材表が見つかりません（ページを読み直してから押してください）', 'proc-why-ng');
            return;
        }

        const lines = draftLinesOf(table);
        if (lines.length === 0) {
            sayIn(box,'⚠ 発注部材表に行がありません（表へ書いてから押してください）', 'proc-why-ng');
            return;
        }
        const val = (k) => valueIn(form, '[data-draft="' + k + '"], [data-unorder="' + k + '"]');
        const body = {
            supplier: val('supplier'), order_at: val('order_at'),
            due: val('due'), note: val('note'), signer: val('signer'), lines,
            // ⚠ **どの表から作ったか**——作り終えたら、その表はこの部材発注書ページへの
            //    リンクに化けます（ユーザー決定）。
            draft_page: form.getAttribute('data-draft-page') || '',
            draft_index: form.getAttribute('data-draft-index') || '',
        };
        if (!body.supplier) {
            sayIn(box,'⚠ 仕入先を入れてください（部材発注書は1枚に1社です）', 'proc-why-ng');
            return;
        }
        if (!body.signer) {
            sayIn(box,'⚠ 差出人を選んでください（紙に刷る署名です）', 'proc-why-ng');
            return;
        }
        go.disabled = true;
        sayIn(box,'部材発注書を作っています…');
        try {
            const r = await postJSON('/api/our-order/new', body);
            if (!r.ok) {
                sayIn(box,'⚠ ' + (r.data.message || '部材発注書を作れませんでした'), 'proc-why-ng');
                return;
            }
            sayIn(box,'部材発注書ができました（' + r.data.page_id + '）。読み直しています…');
            location.reload();
        } catch (err) {
            sayIn(box,'⚠ 通信に失敗しました: ' + err, 'proc-why-ng');
        } finally {
            go.disabled = false;
        }
    }

    // draftTableOf は「部材発注書を作る」の欄が属する発注部材表を返します（無ければ null）。
    //
    // 欄は表を包む節（`<section>`）の中、表の直後に居ます（サーバーの `draftBoxOf`）。
    function draftTableOf(form) {
        const sec = form.closest('section');
        return sec ? sec.querySelector(':scope > table') : null;
    }

    // draftLinesOf は発注部材表の中身を、部材発注書へ送る形で読みます。
    //
    // ⚠ **人が書き換えた値を読みます**——だから属性（`data-*`）ではなく**セルの文字**
    //    です。未発注の表とは逆で、あちらは機械が出した値なので属性から読みます。
    function draftLinesOf(table) {
        const rows = [...table.querySelectorAll('tr')];
        if (rows.length < 2) return [];
        const head = [...rows[0].children].map((c) => c.textContent.trim());
        const at = (cells, label) => {
            const i = head.indexOf(label);
            return i >= 0 && cells[i] ? cells[i].textContent.trim() : '';
        };
        const out = [];
        for (const tr of rows.slice(1)) {
            // ⚠ **鏡が足した行（足元のフォーム）は飛ばします。**
            if (tr.closest('tfoot')) continue;
            const cells = [...tr.children];
            // ⚠ **品番・表面も運びます**（2026-09-25）——それまで送っておらず、
            //    部材発注書で**品番と表面（塗装の色・鍍金）が落ちていました**。
            const ln = {
                product_id: at(cells, '弊社品番'), for_order: at(cells, '案件'), item_id: at(cells, '品番'),
                item_name: at(cells, '品名'),
                material: at(cells, '材質'), shape: at(cells, '形状'), size: at(cells, '寸法'),
                color: at(cells, '表面'),
                // 種類・番号・加工内容・仕様・支給も運ぶ（2026-09-28）——運ばないと、部材発注書で落ちる。
                kind: at(cells, '種類'), no: at(cells, '番号'), work: at(cells, '加工内容'),
                spec: at(cells, '仕様'), supplied: at(cells, '支給'),
                quantity: at(cells, '数量'), unit: at(cells, '単位'),
                cost: at(cells, '単価'), note: at(cells, '備考'),
            };
            // 空の行（書き足す取っ掛かりとして置いた1行）は送りません。
            if (Object.values(ln).every((v) => !v)) continue;
            out.push(ln);
        }
        return out;
    }

    // pickedOf は押したボタンの欄（.unorder-form）・答えを出す欄・未発注の表でチェックした行を返します（欄が無ければ null）。
    // 「発注部材表を作る」と「不要にする」が写していた（2026-10-09 に寄せた）。
    function pickedOf(btn) {
        const form = btn.closest('.unorder-form');
        const root = form && form.parentElement;
        const box = root ? root.querySelector('[data-unorder-result]') : null;
        const table = root ? root.querySelector('.unorder-table') : null;
        if (!form || !box) return null;
        const lines = table
            ? [...table.querySelectorAll('.unorder-check')].filter((c) => c.checked).map((c) => lineOf(c.closest('tr')))
            : [];
        return { form, box, lines };
    }

    // 未発注の表から**発注部材表**を1つ作る（2026-09-22・ユーザーの流れ）。
    //
    // ⚠ **何も選ばなくても押せます**——空の表から始める道（加工製品ページに無い
    //    部材だけを買うとき）。だから `create` と違って「0件なら断る」をしません。
    async function draft(btn) {
        const at = pickedOf(btn);
        if (!at) return;
        const { form, box, lines: picked } = at;
        btn.disabled = true;
        sayIn(box,'発注部材表を作っています…');
        try {
            const r = await postJSON('/api/our-order/draft', {
                // ⚠ **ページIDはサーバーが描いた属性から**——この配線は別の
                //    スコープに居るので `currentPageId` が見えません。
                page_id: form.getAttribute('data-unorder-page') || '',
                lines: picked,
                // ⚠ **入れる先**（空なら新しく作る）。2026-09-22 に**これを
                //    送り忘れていて**、「1枚目へ足す」を選んでも**別の表が
                //    できていました**（ユーザー報告）——サーバーは正しく、
                //    **画面が値を送っていなかった**のです。
                into: valueIn(form, '[data-unorder="into"]'),
            });
            if (!r.ok) {
                sayIn(box,'⚠ ' + (r.data.message || '発注部材表を作れませんでした'), 'proc-why-ng');
                return;
            }
            const d = r.data;
            // ⚠ **本文が変わったのでページを読み直します**——表は本文の一部なので、
            //    画面で組み立てず、サーバーが描いたものを受け取ります。
            sayIn(box,(d.into ? d.into + '枚目へ ' : '新しい表に ') + d.rows + '行入れました。読み直しています…');
            location.reload();
        } catch (err) {
            sayIn(box,'⚠ 通信に失敗しました: ' + err, 'proc-why-ng');
        } finally {
            btn.disabled = false;
        }
    }

    // ⚠ **document へ委譲します**——ビューはサーバーが描き直すので、要素ごとに
    //    配線すると描き直しのたびに切れます。
    // 必要部材表から「不要にする」（2026-10-01・サーバーの skip.go）——選んだ行を手配不要表へ。理由は任意。
    async function skip(btn) {
        const at = pickedOf(btn);
        if (!at) return;
        const { form, box, lines: picked } = at;
        if (picked.length === 0) {
            sayIn(box, '⚠ 不要にする行を選んでください', 'proc-why-ng');
            return;
        }
        btn.disabled = true;
        sayIn(box, '手配不要表へ移しています…');
        try {
            const r = await postJSON('/api/our-order/skip', {
                page_id: form.getAttribute('data-unorder-page') || '',
                lines: picked,
                reason: valueIn(form, '[data-unorder="reason"]'),
            });
            if (!r.ok) {
                sayIn(box, '⚠ ' + (r.data.message || '不要にできませんでした'), 'proc-why-ng');
                return;
            }
            sayIn(box, r.data.rows + '行を手配不要表へ移しました。読み直しています…');
            location.reload();
        } catch (err) {
            sayIn(box, '⚠ 通信に失敗しました: ' + err, 'proc-why-ng');
        } finally {
            btn.disabled = false;
        }
    }

    // 手配不要表から1行を外す＝必要部材表へ戻す（2026-10-01）。
    async function removeSkipRow(btn) {
        btn.disabled = true;
        try {
            const r = await postJSON('/api/our-order/skip/remove', {
                page_id: btn.getAttribute('data-skip-page') || '',
                row: Number(btn.getAttribute('data-skip-row') || 0),
            });
            if (!r.ok) {
                alert('⚠ ' + (r.data.message || '戻せませんでした'));
                btn.disabled = false;
                return;
            }
            location.reload();
        } catch (err) {
            alert('⚠ 通信に失敗しました: ' + err);
            btn.disabled = false;
        }
    }

    delegateClick([
        ['[data-draft-go]', create],
        ['[data-unorder-draft]', draft],
        ['[data-unorder-skip]', skip],
        ['.draft-row-back', removeDraftRow],
        ['.skip-row-back', removeSkipRow],
    ]);

    // 発注部材表から1行を外す＝**未手配の一覧へ戻す**（2026-09-22）。
    //
    // ユーザー:「**発注部材表から未手配の一覧へ戻す方法がありません**」
    //
    // ⚠ **戻す先へ何かを書く必要はありません**——一覧は**毎回計算される鏡**なので、
    //    ここから消せば**自動的に戻ってきます**。
    async function removeDraftRow(btn) {
        btn.disabled = true;
        try {
            const r = await postJSON('/api/our-order/draft/remove', {
                page_id: btn.getAttribute('data-draft-page') || '',
                table: Number(btn.getAttribute('data-draft-table') || 0),
                row: Number(btn.getAttribute('data-draft-row') || 0),
            });
            if (!r.ok) {
                alert('⚠ ' + (r.data.message || '戻せませんでした'));
                btn.disabled = false;
                return;
            }
            location.reload();
        } catch (err) {
            alert('⚠ 通信に失敗しました: ' + err);
            btn.disabled = false;
        }
    }
})();

// ── 部材発注書を送る・発注済みの印を付ける（2026-09-22）──────────────────────
//
// ユーザー:「発注書ページで**送信方法を選んで**、メールの場合は**メールを書く
// ボックス**が出て送信します。FAXの場合は、FAXのページを揃えて送信します。
// （**FAXサーバーはまだつくっていないので、人間が送ったボタンを押すかもしれません**）」
// 「**発注書の表の一品ずつに発注済みの印**をつけます」「大事なことは、**発注の
// 取り消しもある**ということです」。
//
// ⚠ **送信の口は作っていません**——メールは `/api/mail/send` が既に在り、
//    添付つきで送れて**通信箱に控えが残ります**。
// ⚠ **メールを書く欄は部品です**（2026-09-30・assets/mail-compose.js）——ここに残るのは行ごとの印・
//    PDFを作る・FAX・手渡しのボタンだけ。
(function wireOurOrderSend() {
    // 行1つの印を変える（発注済・納品済・取消・戻す）。
    //
    // ⚠ **紙が外へ出たあとの取消は、押しただけでは終わりません**（ユーザー:「電話などで
    //    材料屋に取り消しを依頼して、**OKが出たら**取り消しボタンを押します」）。
    //    サーバーが `data-order-confirm` を付けた手は、**押す前に確かめます**——
    //    確かめずに押せると「押したから片付いた」と読まれ、**材料屋には注文が
    //    残ったまま**になります。⚠ **発注前は確認を出しません**（「単純に取り消します」）。
    async function setRowStatus(btn) {
        const ask = btn.getAttribute('data-order-confirm') || '';
        if (ask && !confirm(ask)) return;
        btn.disabled = true;
        const r = await postJSON('/api/our-order/line-status', {
            page_id: btn.getAttribute('data-order-page') || '',
            row: Number(btn.getAttribute('data-order-row') || 0),
            value: btn.getAttribute('data-order-status') || '',
        }).catch((err) => ({ ok: false, data: { message: '通信に失敗しました: ' + err } }));
        if (!r.ok) {
            alert('⚠ ' + (r.data.message || '印を変えられませんでした'));
            btn.disabled = false;
            return;
        }
        // ⚠ **本文が変わったので読み直します**——印は本文の列なので、画面で
        //    組み立てず、サーバーが描いたものを受け取ります。
        location.reload();
    }

    // 行を部材発注書から外して、必要部材表へ戻す（2026-09-24）。
    //
    // ⚠ **いつも確かめます**（行が本文から消えるため）。確認の文はサーバーが書きます
    //    ——送付後の行には「再送してください」が付きます。
    async function returnRow(btn) {
        const ask = btn.getAttribute('data-order-confirm') || '';
        if (ask && !confirm(ask)) return;
        btn.disabled = true;
        const r = await postJSON('/api/our-order/return-row', {
            page_id: btn.getAttribute('data-order-page') || '',
            row: Number(btn.getAttribute('data-order-row') || 0),
        }).catch((err) => ({ ok: false, data: { message: '通信に失敗しました: ' + err } }));
        if (!r.ok) {
            alert('⚠ ' + (r.data.message || '戻せませんでした'));
            btn.disabled = false;
            return;
        }
        location.reload();
    }

    // 部材発注書のPDFを作って、ページに表示する（2026-09-22）。
    //
    // ⚠ **押す道がありませんでした**——口は 09-21 から在ったのに、呼ぶボタンが
    //    どこにも無く、API を直に叩いて確かめただけでした。
    async function makePDF(btn) {
        const root = btn.closest('.order-send');
        const box = root ? root.querySelector('[data-order-result]') : null;
        btn.disabled = true;
        // 備考の欄を書きかけのまま押したら、先に保存してから作る（2026-10-14・欄の下の「PDF作成」——紙は本文の
        // 「備考」の節を刷るので、保存していない備考は紙に載らない）。
        const note = root ? root.querySelector('.estimate-note-input') : null;
        const noteBtn = root ? root.querySelector('.estimate-note-save') : null;
        if (note && noteBtn && note.value !== note.defaultValue) {
            sayIn(box, '備考を保存しています…');
            const s = await postJSON(noteBtn.getAttribute('data-note-url') || '/api/our-order/note', {
                page_id: noteBtn.getAttribute('data-estimate-page') || '', note: note.value,
            }).catch((err) => ({ ok: false, data: { message: '通信に失敗しました: ' + err } }));
            if (!s.ok) {
                sayIn(box, '⚠ ' + ((s.data && s.data.message) || '備考を保存できませんでした') + '（PDFは作っていません）', 'proc-why-ng');
                btn.disabled = false;
                return;
            }
            note.defaultValue = note.value;
        }
        sayIn(box,'部材発注書のPDFを作っています…');
        const r = await postJSON('/api/order-pdf', {
            page_id: root ? root.getAttribute('data-order-page') || '' : '',
        }).catch((err) => ({ ok: false, data: { message: '通信に失敗しました: ' + err } }));
        if (!r.ok) {
            sayIn(box,'⚠ ' + (r.data.message || 'PDFを作れませんでした'), 'proc-why-ng');
            btn.disabled = false;
            return;
        }
        // ⚠ **表示できなかったことを黙りません**（PDFは出来ているので、失敗では
        //    ありませんが、画面に出ないことは伝える必要があります）。
        if (r.data.view_note) {
            sayIn(box,r.data.view_note, 'proc-why-ng');
            btn.disabled = false;
            return;
        }
        location.reload();
    }

    // 資料を綴じた FAX・印刷用の1本（2026-09-28・order_docs.go）。部材発注書のPDF（表示中の正本）は
    // 差し替えないので、ページは開き直さず、できた1本へのリンクを欄に出します（押して印刷・FAX）。
    async function makePDFWithDocs(btn) {
        const root = btn.closest('.order-send');
        const box = root ? root.querySelector('[data-order-result]') : null;
        btn.disabled = true;
        sayIn(box, '部材発注書に資料を綴じています…');
        const r = await postJSON('/api/order-pdf-docs', {
            page_id: root ? root.getAttribute('data-order-page') || '' : '',
        }).catch((err) => ({ ok: false, data: { message: '通信に失敗しました: ' + err } }));
        btn.disabled = false;
        if (!r.ok) {
            sayIn(box, '⚠ ' + (r.data.message || '綴じられませんでした'), 'proc-why-ng');
            return;
        }
        sayBoundPDF(box, r.data);
    }

    // FAX・手渡しの「送った」。⚠ **人が押したことがその事実**です。
    async function markSent(btn) {
        const root = btn.closest('.order-send');
        const box = root ? root.querySelector('[data-order-result]') : null;
        const method = btn.getAttribute('data-order-sent') || '';
        if (!confirm(method + 'で部材発注書を送りましたか？（取消でない行すべてを発注済みにします）')) return;
        btn.disabled = true;
        sayIn(box,'印を付けています…');
        const r = await postJSON('/api/our-order/sent', {
            page_id: root ? root.getAttribute('data-order-page') || '' : '',
            method,
        }).catch((err) => ({ ok: false, data: { message: '通信に失敗しました: ' + err } }));
        if (!r.ok) {
            sayIn(box,'⚠ ' + (r.data.message || '印を付けられませんでした'), 'proc-why-ng');
            btn.disabled = false;
            return;
        }
        location.reload();
    }

    // ⚠ **document へ委譲します**——鏡はサーバーが描き直すので、要素ごとに
    //    配線すると描き直しのたびに切れます。
    delegateClick([
        ['.order-row-set', setRowStatus],
        ['.order-row-return', returnRow],
        ['[data-order-pdf]', makePDF],
        ['[data-order-pdf-docs]', makePDFWithDocs],
        ['[data-order-sent]', markSent],
        // メールで送る欄は部品です（2026-09-30・assets/mail-compose.js——「PDFを作る → 送る → 発注済みにする」は
        // サーバーの用件「部材発注書」が受け持つ・ext/toho/order_mail.go）。
    ]);
})();

// ── 加工製品の一覧を絞る（2026-09-29）──────────────────────────────────────
//
// 利用者:「加工製品のトップページにでも、フィルターして表示する項目があれば良いと思います」。
// 段のフォルダ（現行・旧型・試作）をやめて区分のタグにしたので、試作だけ・見積だけを
// **見る**場所がここです。
//
// ⚠ **欄も表もサーバーが描きます**（ext/toho/product_list.go）。ここは**隠すだけ**——1社の
// 加工製品は数百枚なので、全部を描いて隠すほうが、打つたびにサーバーへ聞くより速い。
// 文字は NFKC＋小文字で比べます（全角の英数字・大小の打ち分けで外れないように）。
(function wireProductList() {
    const norm = (s) => String(s || '').normalize('NFKC').toLowerCase();

    function apply(form) {
        const wrap = form.parentElement;
        const table = wrap ? wrap.querySelector('table.plist-table') : null;
        if (!table) return;
        const terms = norm(valueIn(form, '[data-plist-text]')).split(/\s+/).filter(Boolean);
        // 社名（2026-10-16）——一覧はどこに置いても全社の分なので、会社ごとに見るのはこの欄。「-」は社名の分からない行。
        const company = valueIn(form, '[data-plist-company]');
        const machine = valueIn(form, '[data-plist-machine]');
        const boxes = Array.from(form.querySelectorAll('[data-plist-kind]'));
        const known = new Set(boxes.map(b => b.dataset.plistKind).filter(Boolean));
        const picked = new Set(boxes.filter(b => b.checked).map(b => b.dataset.plistKind));
        const migBox = form.querySelector('[data-plist-migrating]');
        const migOnly = !!(migBox && migBox.checked);
        let shown = 0;
        let total = 0;
        table.querySelectorAll('tr[data-plist-row]').forEach(tr => {
            total++;
            // 区分の付いていない行は「通常」（値が空の印）。⚠ 選択肢に無い区分だけの行も
            // 「通常」に数えます——黙って一覧から消えるほうが悪いので。
            const kinds = (tr.dataset.kinds || '').split('\t').filter(k => known.has(k));
            let ok = kinds.length ? kinds.some(k => picked.has(k)) : picked.has('');
            if (ok && company && tr.dataset.company !== company) ok = false;
            if (ok && machine && tr.dataset.machine !== machine) ok = false;
            if (ok && migOnly && tr.dataset.migrating !== '1') ok = false;
            if (ok && terms.length) {
                if (tr.wPlistText === undefined) tr.wPlistText = norm(tr.dataset.text);
                ok = terms.every(t => tr.wPlistText.includes(t));
            }
            tr.hidden = !ok;
            if (ok) shown++;
        });
        const count = form.querySelector('[data-plist-count]');
        if (count) count.textContent = shown === total ? total + ' 件' : shown + ' 件（全 ' + total + ' 件）';
        const none = wrap.querySelector('[data-plist-none]');
        if (none) none.hidden = shown > 0;
    }

    // ⚠ **document へ委譲します**——鏡はサーバーが描き直すので、要素ごとに配線すると
    //    描き直しのたびに切れます（材料を探す欄と同じ理由）。
    const onEvent = (e) => {
        const t = e.target;
        const form = t && t.closest ? t.closest('[data-plist-form]') : null;
        if (form) apply(form);
    };
    document.addEventListener('input', onEvent);
    document.addEventListener('change', onEvent);
})();

// ── 製造ページの作業の帯（2026-10-19・ext/toho/mfg_view.go）────────────────────────────────
//
// 利用者:「一つの製造ページに案件の見積もり依頼、部材発注、製造記録（工数など）、納品書、請求書を並べるのはどうですか？」。
// 製造明細表の「製造完了」のチェックは /api/mfg/made へ（行番号・品番・いまの値の3つで照合）。「納品」の欄で選んだ行は、
// 今回納める数（まだ納めていない数が初めから入っていて直せる）を確かめて、/api/delivery/new へ mfg（この製造ページ）を添えて送る
// ——納品書はこの製造ページの子にできる。
delegateClick([
    ['.mfg-made', async (box) => {
        const page = (location.pathname.match(/\/(\d{6})/) || [])[1] || '';
        const want = box.checked ? '済' : '';
        box.disabled = true;
        const r = await postJSON('/api/mfg/made', {
            page_id: page, row: Number(box.dataset.row), item_no: box.dataset.item || '', old: box.dataset.old || '', value: want,
        }).catch((e) => ({ ok: false, data: { message: String(e) } }));
        box.disabled = false;
        if (!r.ok) {
            box.checked = !box.checked;
            alert('⚠ ' + ((r.data && r.data.message) || '製造完了を書けませんでした'));
            return;
        }
        box.dataset.old = want;
    }],
    ['.mfg-dlv-go', (btn) => {
        const cell = btn.parentElement;
        const say = cell.querySelector('.mfg-dlv-say');
        const panel = cell.querySelector('.mfg-dlv-panel');
        const table = btn.closest('table');
        const picks = Array.from((table || document).querySelectorAll('.mfg-dlv-pick:checked'));
        if (!picks.length) {
            sayIn(say, '製造明細表の右の「納品」の欄で、納める行を選んでください。', 'warn');
            return;
        }
        sayIn(say, '');
        panel.textContent = '';
        const t = document.createElement('table');
        t.className = 'materials-table backlog-dlv-table';
        const tr0 = document.createElement('tr');
        ['品番', '品名', 'まだ納めていない数', '今回納める数'].forEach((h) => { const th = document.createElement('th'); th.textContent = h; tr0.appendChild(th); });
        t.appendChild(tr0);
        picks.forEach((c) => {
            const tr = document.createElement('tr');
            [c.dataset.item, c.dataset.name, c.dataset.left].forEach((v) => { const td = document.createElement('td'); td.textContent = v || ''; tr.appendChild(td); });
            const td = document.createElement('td');
            const inp = document.createElement('input');
            inp.type = 'number';
            inp.min = '1';
            inp.value = c.dataset.left || '';
            inp.className = 'mfg-dlv-qty';
            inp.dataset.order = c.dataset.order;
            inp.dataset.item = c.dataset.item || '';
            inp.dataset.name = c.dataset.name || '';
            td.appendChild(inp);
            tr.appendChild(td);
            t.appendChild(tr);
        });
        panel.appendChild(t);
        const make = document.createElement('button');
        make.type = 'button';
        make.className = 'mfg-dlv-make';
        make.dataset.mfg = btn.dataset.mfg || '';
        make.textContent = '🚚 納品書を作る';
        panel.appendChild(make);
        const msg = document.createElement('span');
        msg.className = 'mfg-dlv-msg';
        panel.appendChild(msg);
        panel.hidden = false;
    }],
    ['.mfg-dlv-make', async (btn) => {
        const panel = btn.closest('.mfg-dlv-panel');
        const msg = panel.querySelector('.mfg-dlv-msg');
        const items = Array.from(panel.querySelectorAll('.mfg-dlv-qty')).map((i) => ({
            order: i.dataset.order, row: -1, item: i.dataset.item, name: i.dataset.name, qty: i.value.trim(),
        }));
        btn.disabled = true;
        sayIn(msg, '納品書を作っています…');
        const r = await postJSON('/api/delivery/new', { items, mfg: btn.dataset.mfg || '' }).catch((e) => ({ ok: false, data: { message: String(e) } }));
        btn.disabled = false;
        if (!r.ok) { sayIn(msg, '⚠ ' + ((r.data && r.data.message) || '納品書を作れませんでした')); return; }
        location.href = '/' + r.data.page_id;
    }],
]);

// 製造ページの「📦 この製造の部材を発注部材表へ」（2026-10-19・ext/toho/mfg_view.go の MfgPartsAPIHandler）——この製造の、まだ手配して
// いない部材（この製造で作る数ぶんまで）を、この製造ページに新しい発注部材表として入れる。部材発注書はその表の「部材発注書を作る」から
// （製造ページの子にできる）。
delegateClick([['.mfg-parts-go', async (btn) => {
    const say = btn.parentElement && btn.parentElement.querySelector('.mfg-parts-say');
    btn.disabled = true;
    sayIn(say, '部材を数えています…');
    const r = await postJSON('/api/mfg/parts', { page_id: btn.dataset.mfg || '' }).catch((e) => ({ ok: false, data: { message: String(e) } }));
    btn.disabled = false;
    if (!r.ok) { sayIn(say, '⚠ ' + ((r.data && r.data.message) || '発注部材表へ入れられませんでした')); return; }
    location.reload();
}]]);

// 製造ページの「📁 この製造を閉じる」（2026-10-19・ext/toho/mfg_close.go）——確かめてから、記録／年／月／製造記録へ移して読み直す。
delegateClick([['.mfg-close-go', async (btn) => {
    const say = btn.parentElement && btn.parentElement.querySelector('.mfg-close-say');
    if (!confirm('この製造ページを「記録／年／月／製造記録」へ移します（中身と下の書類はそのままです）。よろしいですか？\n' +
        '納め終えて請求書を発行した製造は、請求書の PDF を作ったときに自動で移ります。')) return;
    btn.disabled = true;
    sayIn(say, '移しています…');
    const r = await postJSON('/api/mfg/close', { page_id: btn.dataset.mfg || '' }).catch((e) => ({ ok: false, data: { message: String(e) } }));
    btn.disabled = false;
    if (!r.ok) { sayIn(say, '⚠ ' + ((r.data && r.data.message) || '移せませんでした')); return; }
    location.reload();
}]]);

// 製造ページの「📨 この製造の部材を見積依頼必要部材表へ」（2026-10-19・ext/toho/mfg_more.go）——入れたら読み直す（表はこの製造ページの中）。
delegateClick([['.mfg-rfq-go', async (btn) => {
    const say = btn.parentElement && btn.parentElement.querySelector('.mfg-rfq-say');
    btn.disabled = true;
    sayIn(say, '部材を集めています…');
    const r = await postJSON('/api/mfg/rfq', { page_id: btn.dataset.mfg || '' }).catch((e) => ({ ok: false, data: { message: String(e) } }));
    btn.disabled = false;
    if (!r.ok) { sayIn(say, '⚠ ' + ((r.data && r.data.message) || '見積依頼必要部材表へ入れられませんでした')); return; }
    location.reload();
}]]);

// 製造ページの「📄 この製造の見積書を作る」（2026-10-19・ext/toho/quote_done.go）——見積の依頼の行から見積書をこの製造ページの子に作って開く。
delegateClick([['.mfg-estimate-go', async (btn) => {
    const say = btn.parentElement && btn.parentElement.querySelector('.mfg-estimate-say');
    btn.disabled = true;
    sayIn(say, '見積書を作っています…');
    const r = await postJSON('/api/mfg/estimate', { page_id: btn.dataset.mfg || '' }).catch((e) => ({ ok: false, data: { message: String(e) } }));
    btn.disabled = false;
    if (!r.ok || !r.data || !r.data.page_id) { sayIn(say, '⚠ ' + ((r.data && r.data.message) || '見積書を作れませんでした')); return; }
    if (r.data.missing > 0) {
        // 単価の無い行があれば、そう言ってから開く（押すと開く）。
        sayIn(say, (r.data.message || '') + ' ');
        const link = document.createElement('a');
        link.href = '/' + r.data.page_id;
        link.textContent = '見積書を開く';
        if (say) say.appendChild(link);
        return;
    }
    location.href = '/' + r.data.page_id;
}]]);
