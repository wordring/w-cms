// === テンプレートキャッシュと非同期ローダー ===
const templateCache = {};

// escapeText / escapeAttr はテンプレートへ差し込む値をエスケープする。
//
// **属性値をテンプレートへ差すときは必ず escapeAttr を通すこと。** テンプレートは
// 文字列置換＋innerHTML で描画するため、素通しすると値がHTMLとして解釈される。
// これは理屈上の話ではなく、実際に発火する保存型XSSだった（2026-08-04 修正）:
// `<m-tag value='" onmouseover="…'>` で属性を注入でき、`value` に `<img onerror=…>`
// を入れればスクリプトが走った。**サーバーのサニタイザでは止まらない**——`value` は
// URL属性ではないので中身を検査せず、`&lt;img…&gt;` として保存された値を
// getAttribute() が生の文字列へ戻すため。中間版CSPの `script-src 'unsafe-inline'` も
// インラインイベントハンドラを許すので防げない。
//
// escapeAttr は `& < > "` を落とすので、**二重引用符の属性値と本文テキストの両方で安全**。
// 同じプレースホルダが両方の文脈で使われるテンプレートがある（`${itemName}` 等）ため、
// 迷ったら escapeAttr を使う。escapeText は本文にしか現れないと確実な場合だけ。
//
// 値を生のHTMLとして差したい箇所（`${pdfEmbedHTML}`）と、コードが生成する固定値
// （`${statusColor}`・`${selectUnstarted}` 等）はユーザー入力ではないので対象外。
function escapeText(s) {
    return String(s == null ? '' : s)
        .replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}
function escapeAttr(s) {
    return escapeText(s).replace(/"/g, '&quot;');
}

// closestCustom は el 自身から祖先方向へ、最初のカスタム要素（タグ名に "-" を含む）を返す。
function closestCustom(el) {
    for (let p = el; p; p = p.parentElement) {
        if (p.tagName.includes('-')) return p;
    }
    return null;
}

// takeCustomChildren は「host に属するカスタム要素の子」を集める。
//
// 直下の children では**取りこぼす**。コンポーネントは描画時に自前のクローム（<div>）を
// 挟むため、2回目以降の描画では子がその中に入っているためである。
// （ここを children で書いていたために、再描画で明細 <m-item> が消える不具合があった）
// そこで子孫を走査し、「直近のカスタム要素の祖先が host であるもの」だけを拾う。
function takeCustomChildren(host) {
    return Array.from(host.querySelectorAll('*')).filter(
        el => el.tagName.includes('-') && closestCustom(el.parentElement) === host
    );
}

async function fetchTemplate(name) {
    if (templateCache[name]) {
        return templateCache[name];
    }
    const response = await fetch(`/assets/templates/${name}.html`);
    const text = await response.text();
    templateCache[name] = text;
    return text;
}

// === 全カスタム要素の基底 MElement ===
//
// **描画先は Light DOM（this.innerHTML）に統一する。Shadow DOM は使わない。**
// 理由は本文（正本）の扱いにある:
//   - 保存はエディタが**DOMを辿って**行う（index.html の serializeCustomElement）。Shadow に
//     隠すと入れ子の <m-item> が走査から外れ、保存される本文とサーバーの語彙がずれる。
//   - 配色は assets/components.css（外部スタイルシート）に置ける。Shadow だと要素ごとに
//     <style> を複製することになり、CSPのstrict化（docs/【考察】CSP強化.md）とも噛み合わない。
//   - contenteditable のエディタと同じツリーに乗るので、選択・キャレットの扱いが素直。
//
// かつて <m-child-list> だけが Shadow DOM で描画しており、その名残の `getRootNode().host` が
// Light DOM のテンプレートに残っていた。Light DOM では getRootNode() は document を返し
// `.host` は undefined なので、m-tag の値編集・m-item の項目編集と削除は**例外で動いて
// いなかった**（2026-08-05 に解消）。ホストへの書き戻しは data-attr に一本化する。
class MElement extends HTMLElement {
    async connectedCallback() { await this.render(); }

    async attributeChangedCallback() {
        if (this._selfUpdate) return; // 自分のUI操作による変更では描き直さない（updateAttr 参照）
        await this.render();
    }

    // updateAttr は「このコンポーネント自身のUI操作による属性変更」を表す。
    //
    // 素の setAttribute だと attributeChangedCallback → render() で入力欄が作り直され、
    // **1文字打つたびにフォーカスが飛ぶ**（＝実質1文字しか入力できない）。自己更新の間だけ
    // 再描画を止める。入力欄には既に打った値が入っているので、描き直さなくても画面は正しい。
    // 外から属性が変わったとき（読み込み・PDF解析の結果反映など）は従来どおり再描画する。
    updateAttr(name, value) {
        this._selfUpdate = true;
        try {
            this.setAttribute(name, value);
        } finally {
            this._selfUpdate = false;
        }
        if (window.updateHtmlPreview) window.updateHtmlPreview();
    }

    // bindEditFields は描画したテンプレートへ編集操作を配線する。
    // インラインの on*= は使わない（docs/開発方針.md §4。CSPのstrict化の前提でもある）。
    //   data-attr="item-name" … 入力値をその属性へ書き戻す（select は change、他は input）
    //   data-remove           … 押すとこの要素自身を本文から取り除く
    //
    // 対象は**自分が描いたUIだけ**に限る。入れ子のカスタム要素（<m-client-order> の中の
    // <m-item> など）も同じ data-attr を使うため、素の querySelectorAll では明細の入力欄まで
    // 拾ってしまい、明細を打つと親ブロックの属性まで書き換わる。takeCustomChildren と同じく
    // 「直近のカスタム要素の祖先が自分か」で線を引く。
    bindEditFields() {
        const isMine = el => closestCustom(el.parentElement) === this;

        this.querySelectorAll('[data-attr]').forEach(field => {
            if (!isMine(field)) return;
            const eventName = field.tagName === 'SELECT' ? 'change' : 'input';
            field.addEventListener(eventName, () => this.updateAttr(field.dataset.attr, field.value));
        });
        this.querySelectorAll('[data-remove]').forEach(button => {
            if (!isMine(button)) return;
            button.addEventListener('click', () => {
                this.remove();
                if (window.updateHtmlPreview) window.updateHtmlPreview();
            });
        });
    }
}

// === <m-tag> (名前：値) の定義 ===
class MTag extends MElement {
    static get observedAttributes() { return ['value']; }

    async render() {
        if (!this.isConnected) return;
        const name = this.getAttribute('name') || '';
        const value = this.getAttribute('value') || '';
        const isEdit = document.body.hasAttribute('edit-mode');

        const templateName = isEdit ? 'm-tag-edit' : 'm-tag-view';
        let html = await fetchTemplate(templateName);

        // 変数置換
        html = html.replace(/\${name}/g, escapeAttr(name)).replace(/\${value}/g, escapeAttr(value));
        this.innerHTML = html;
        this.bindEditFields();
    }
}
customElements.define('m-tag', MTag);

// === <m-item> (部品明細) の定義 ===
class MItem extends MElement {
    static get observedAttributes() { return ['item-id', 'item-name', 'price', 'cost', 'quantity', 'status']; }

    async render() {
        if (!this.isConnected) return;
        const itemId = this.getAttribute('item-id') || '';
        const itemName = this.getAttribute('item-name') || '';
        const price = this.getAttribute('price') || '0';
        const cost = this.getAttribute('cost') || '0';
        const quantity = this.getAttribute('quantity') || '1';
        const status = this.getAttribute('status') || '';
        const isEdit = document.body.hasAttribute('edit-mode');

        // 客先向け（売価）か仕入向け（原価）かは、**親の業務要素**で決まる。
        // 旧来は親 m-file の tag 文字列を見ていたが、意味は要素そのものが持つようになった。
        const isClient = !!this.closest('m-client-order');

        // ステータスの色分け
        let statusColor = '#64748b';
        let statusBg = '#f1f5f9';
        if (status === '未着手' || status === '未納品' || status === '') { 
            statusColor = '#ef4444'; 
            statusBg = '#fef2f2'; 
        } else if (status === '加工中') { 
            statusColor = '#3b82f6'; 
            statusBg = '#eff6ff'; 
        } else if (status === '検査中') { 
            statusColor = '#f59e0b'; 
            statusBg = '#fffbeb'; 
        } else if (status === '納品済') { 
            statusColor = '#10b981'; 
            statusBg = '#ecfdf5'; 
        }

        let templateName = '';
        if (isClient) {
            templateName = isEdit ? 'm-item-edit-client' : 'm-item-view-client';
        } else {
            templateName = isEdit ? 'm-item-edit-our' : 'm-item-view-our';
        }

        let html = await fetchTemplate(templateName);

        const priceNum = Number(price);
        const costNum = Number(cost);
        const qtyNum = Number(quantity);

        // 変数置換
        html = html
            .replace(/\${itemId}/g, escapeAttr(itemId))
            .replace(/\${itemName}/g, escapeAttr(itemName))
            .replace(/\${priceDisplay}/g, priceNum.toLocaleString())
            .replace(/\${costDisplay}/g, costNum.toLocaleString())
            .replace(/\${totalDisplay}/g, (isClient ? priceNum * qtyNum : costNum * qtyNum).toLocaleString())
            .replace(/\${price}/g, escapeAttr(price))
            .replace(/\${cost}/g, escapeAttr(cost))
            .replace(/\${quantity}/g, escapeAttr(quantity))
            // statusColor / statusBg は上の分岐が返す固定の色コードなのでエスケープ不要
            .replace(/\${statusColor}/g, statusColor)
            .replace(/\${statusBg}/g, statusBg)
            .replace(/\${status}/g, escapeAttr(status || (isClient ? '未着手' : '未納品')));

        // selectの選択状態の置換
        html = html
            .replace(/\${selectUnstarted}/g, (status === '未着手' || status === '') ? 'selected' : '')
            .replace(/\${selectProcessing}/g, status === '加工中' ? 'selected' : '')
            .replace(/\${selectInspecting}/g, status === '検査中' ? 'selected' : '')
            .replace(/\${selectFinished}/g, status === '納品済' ? 'selected' : '')
            .replace(/\${selectUndelivered}/g, (status === '未納品' || status === '') ? 'selected' : '')
            .replace(/\${selectDelivered}/g, status === '納品済' ? 'selected' : '');

        this.innerHTML = html;
        this.bindEditFields();
    }
}
customElements.define('m-item', MItem);

// === <m-file> (ファイル容器) の定義 ===
//
// 「ここにファイルがある」ことだけを表す**純粋な容器**。業務上の意味は中身の要素
// （<m-client-order> 等）が持つ（docs/【考察】通信記録処理.md §4.5）。
//
// かつては tag 属性の文字列で意味を切り替え、業務属性（order-no・client-name・
// price…）も直に抱えていたため、この render は長い tag 分岐の塊だった。
// 責務を分離したことでファイル表示だけの短い実装になっている。
class MFile extends MElement {
    static get observedAttributes() { return ['src', 'name', 'ext']; }

    async render() {
        if (!this.isConnected) return;

        // 中身（業務要素など）を退避してから描画し、あとで戻す。
        // 「子を退避 → クローム描画 → 戻す」は既存コンポーネント共通のパターン。
        const contents = takeCustomChildren(this);

        const src = this.getAttribute('src') || '';
        const name = this.getAttribute('name') || 'ファイル';
        const isEdit = document.body.hasAttribute('edit-mode');

        let pdfEmbedHTML = '';
        if (src && src.toLowerCase().endsWith('.pdf')) {
            const pageId = window.currentPageId || new URLSearchParams(window.location.search).get('id');
            if (pageId) {
                const prefix = pageId.length >= 2 ? pageId.substring(0, 2) : "00";
                pdfEmbedHTML = `<embed class="m-file-embed" src="/data/master/${prefix}/${pageId}/${src}" type="application/pdf">`;
            } else {
                pdfEmbedHTML = `<div class="m-file-hint">（保存後にプレビューが表示されます）</div>`;
            }
        }

        const templateName = isEdit ? 'm-file-edit' : 'm-file-view';
        let html = await fetchTemplate(templateName);
        html = html
            .replace(/\$\{src\}/g, escapeAttr(src))
            .replace(/\$\{name\}/g, escapeText(name))
            .replace(/\$\{pdfEmbedHTML\}/g, pdfEmbedHTML);

        this.innerHTML = html;

        // 退避した中身を戻す
        const slot = this.querySelector('.m-file-contents');
        if (slot) contents.forEach(c => slot.appendChild(c));

        // PDFのドラッグ＆ドロップ（編集時のみテンプレートに存在）
        const dropZone = this.querySelector('.pdf-drop-zone');
        if (dropZone) {
            dropZone.addEventListener('dragover', (e) => {
                e.preventDefault();
                dropZone.classList.add('is-dragover');
            });
            dropZone.addEventListener('dragleave', (e) => {
                e.preventDefault();
                dropZone.classList.remove('is-dragover');
            });
            dropZone.addEventListener('drop', (e) => {
                e.preventDefault();
                dropZone.classList.remove('is-dragover');
                const file = e.dataTransfer.files[0];
                const isPDF = file && (file.type === 'application/pdf' || file.name.toLowerCase().endsWith('.pdf'));
                if (!isPDF) {
                    alert("PDFファイルをアップロードしてください (認識された名前: " + (file ? file.name : 'なし') + ")");
                    return;
                }
                this.uploadPDF(file);
            });
        }
    }

    async uploadPDF(file) {
        let pageId = window.currentPageId || new URLSearchParams(window.location.search).get('id');
        if (!pageId) {
            alert("先に一度内容を入力し、オートセーブ（URLにidが付く）されるのをお待ちください。");
            return;
        }

        const formData = new FormData();
        formData.append('page_id', pageId);
        formData.append('pdf_file', file);

        try {
            const dropZone = this.querySelector('.pdf-drop-zone');
            if (dropZone) dropZone.innerText = "アップロード中...";

            const res = await fetch('/api/upload-pdf', { method: 'POST', body: formData });
            if (!res.ok) {
                const text = await res.text();
                throw new Error(`Server returned ${res.status}: ${text}`);
            }
            const data = await res.json();
            if (data.success) {
                this.setAttribute('src', data.src);
                this.setAttribute('name', data.file_name);
                if(window.updateHtmlPreview) window.updateHtmlPreview();
                if(window.triggerAutoSave) window.triggerAutoSave();
                this.render(); 
                
                // 続けて自動解析
                this.parsePDF(pageId, data.file_name);
            } else {
                throw new Error(data.message || "Upload failed with success:false");
            }
        } catch (e) {
            console.error("PDF upload failed", e);
            const dropZone = this.querySelector('.pdf-drop-zone');
            if (dropZone) dropZone.innerText = "アップロード失敗: " + e.message;
            alert("アップロードに失敗しました。サーバーが再起動されているか確認してください。\n詳細: " + e.message);
        }
    }

    async parsePDF(pageId, fileName) {
        try {
            const dropZone = this.querySelector('.pdf-drop-zone');
            if (dropZone) dropZone.innerText = "PDF内容をAI解析中...";

            const res = await fetch('/api/parse-pdf', {
                method: 'POST',
                headers: { 'Content-Type': 'application/json' },
                body: JSON.stringify({ page_id: pageId, file_name: fileName })
            });
            const data = await res.json();
            console.log("PDF解析 レスポンス:", data);
            
            if (data.success === false) {
                alert("バックエンドエラー: " + (data.message || "不明なエラー"));
                return;
            }

            if (data.success && data.items && data.items.length > 0) {
                const container = this.querySelector('.items-list') || this;
                data.items.forEach(it => {
                    const item = document.createElement('m-item');
                    item.setAttribute('item-id', 'PARSED-' + Math.floor(Math.random()*10000));
                    item.setAttribute('item-name', it.item_name);
                    item.setAttribute('price', it.price);
                    item.setAttribute('quantity', it.quantity);
                    item.setAttribute('status', '未着手');
                    container.appendChild(item);
                });
                if(window.updateHtmlPreview) window.updateHtmlPreview();
                if(window.triggerAutoSave) window.triggerAutoSave();
                alert(`PDFの解析に成功し、${data.items.length}件の明細を自動追加しました！`);
            } else {
                console.log("抽出アイテムなし", data);
                if (!data.raw || data.raw.trim() === '') {
                    alert("PDFからテキストを抽出できませんでした。（画像のみのPDFの可能性があります）");
                } else {
                    alert("PDFからテキストは抽出できましたが、明細（金額や数量など）のパターンを自動検知できませんでした。\nF12開発者ツールのConsoleにて生テキストを確認できます。");
                }
                
                // フォールバックとして、ファイル名から1件ダミー追加する？（UX向上ため）
                const container = this.querySelector('.items-list') || this;
                const item = document.createElement('m-item');
                item.setAttribute('item-id', 'FALLBACK-' + Math.floor(Math.random()*10000));
                item.setAttribute('item-name', fileName.replace('.pdf', '') + ' 一式');
                item.setAttribute('price', '0');
                item.setAttribute('quantity', '1');
                item.setAttribute('status', '未着手');
                container.appendChild(item);
                if(window.updateHtmlPreview) window.updateHtmlPreview();
                if(window.triggerAutoSave) window.triggerAutoSave();
            }
        } catch (e) {
            console.error("PDF parse failed", e);
            alert("PDFの解析に失敗しました。");
        }
    }
}
customElements.define('m-file', MFile);

// === <m-child-list> (子ページ一覧) の定義 ===
//
// 唯一 Shadow DOM で描画していた要素だったが、他要素と同じ Light DOM へ揃えた
// （理由は MElement の説明を参照）。中身は毎回この要素が組み立てる動的な一覧であり、
// 保存対象ではない。エディタのシリアライザはカスタム要素の**属性と入れ子のカスタム要素**
// しか書き出さないので、ここで作った <ul> が本文に混ざることはない。
class MChildList extends MElement {
    async render() {
        // currentPageId is defined globally in index.html
        const pageId = window.currentPageId || "000000";
        const res = await fetch(`/api/children?parent_id=${pageId}`);
        let pages = [];
        if (res.ok) {
            pages = await res.json() || [];
        }

        // ページ名は利用者が書いた文字列（本文の見出し由来）なので、必ずエスケープしてから差す。
        const itemsHtml = pages.length === 0
            ? `<li class="m-child-list-empty">子ページはありません</li>`
            : pages.map(p => `<li><a class="m-child-list-link" href="/${escapeAttr(p.ID)}">📄 ${escapeText(p.Title)}</a></li>`).join('');

        this.innerHTML = `<ul class="m-child-list">${itemsHtml}</ul>`;
    }
}
customElements.define('m-child-list', MChildList);

// ============================================
// Register all components
// ============================================

// === <m-material> (必要部材定義) の定義 ===
class MMaterial extends MElement {
    static get observedAttributes() { return ['item-name', 'cost', 'supplier-name', 'quantity']; }

    async render() {
        if (!this.isConnected) return;
        const itemName = this.getAttribute('item-name') || '';
        const cost = this.getAttribute('cost') || '0';
        const supplierName = this.getAttribute('supplier-name') || '';
        const quantity = this.getAttribute('quantity') || '1';
        const isEdit = document.body.hasAttribute('edit-mode');

        const templateName = isEdit ? 'm-material-edit' : 'm-material-view';
        let html = await fetchTemplate(templateName);

        const costNum = Number(cost);

        // 変数置換
        html = html
            .replace(/\${itemName}/g, escapeAttr(itemName))
            .replace(/\${costDisplay}/g, costNum.toLocaleString())
            .replace(/\${cost}/g, escapeAttr(cost))
            .replace(/\${supplierName}/g, escapeAttr(supplierName))
            .replace(/\${quantity}/g, escapeAttr(quantity));

        this.innerHTML = html;
        this.bindEditFields();
    }
}
customElements.define('m-material', MMaterial);

// === <m-required-materials> (手配進捗状況) の定義 ===
class MRequiredMaterials extends MElement {
    static get observedAttributes() { return ['page-id']; }

    async render() {
        if (!this.isConnected) return;
        const isEdit = document.body.hasAttribute('edit-mode');
        const templateName = isEdit ? 'm-required-materials-edit' : 'm-required-materials-view';
        let html = await fetchTemplate(templateName);
        this.innerHTML = html;

        if (isEdit) return; // 編集モード時はプレースホルダー表示のみ

        // 閲覧モードの場合はAPIからデータをフェッチ
        const pageId = this.getAttribute('page-id') || new URLSearchParams(window.location.search).get('page_id') || '';
        if (!pageId) {
            const loadingEl = this.querySelector('.materials-loading');
            if (loadingEl) loadingEl.innerText = 'ページIDが指定されていません。';
            return;
        }

        try {
            const response = await fetch(`/api/required-materials?page_id=${pageId}`);
            if (!response.ok) throw new Error('API error');
            const data = await response.json();

            const loadingEl = this.querySelector('.materials-loading');
            if (loadingEl) loadingEl.style.display = 'none';

            const tableEl = this.querySelector('.materials-table');
            const tbodyEl = this.querySelector('.materials-tbody');

            if (!tbodyEl) return;

            if (data.length === 0) {
                if (loadingEl) {
                    loadingEl.style.display = 'block';
                    loadingEl.innerText = '必要部材として登録されているアイテムはありません。';
                }
                return;
            }

            tableEl.style.display = 'table';
            tbodyEl.innerHTML = '';

            data.forEach(item => {
                const tr = document.createElement('tr');
                tr.style.borderBottom = '1px solid #f1f5f9';
                
                let statusBadge = '';
                if (item.remaining === 0) {
                    statusBadge = `<span style="background-color: #ecfdf5; color: #10b981; padding: 4px 8px; border-radius: 4px; font-weight: bold; font-size: 11px;">手配完了</span>`;
                } else {
                    statusBadge = `<span style="background-color: #fef2f2; color: #ef4444; padding: 4px 8px; border-radius: 4px; font-weight: bold; font-size: 11px;">要手配 (${item.remaining})</span>`;
                }

                // 部材名・仕入先名は <m-material> の属性（＝利用者の入力）がDB経由で返ってきた値。
                // 数値項目は toLocaleString() を通した数なのでエスケープ不要。
                tr.innerHTML = `
                    <td style="padding: 10px; font-weight: 500; color: #1e293b;">${escapeText(item.material_name)}</td>
                    <td style="padding: 10px; color: #475569;">${escapeText(item.supplier_name || '-')}</td>
                    <td style="padding: 10px; text-align: right; color: #1e293b;">${item.total_required.toLocaleString()}</td>
                    <td style="padding: 10px; text-align: right; color: #475569;">${item.ordered.toLocaleString()}</td>
                    <td style="padding: 10px; text-align: right; font-weight: bold; color: ${item.remaining > 0 ? '#ef4444' : '#1e293b'};">${item.remaining.toLocaleString()}</td>
                    <td style="padding: 10px; text-align: center;">${statusBadge}</td>
                `;
                tbodyEl.appendChild(tr);
            });
        } catch (e) {
            const loadingEl = this.querySelector('.materials-loading');
            if (loadingEl) loadingEl.style.display = 'none';
            const errorEl = this.querySelector('.materials-error');
            if (errorEl) errorEl.style.display = 'block';
            console.error(e);
        }
    }
}
customElements.define('m-required-materials', MRequiredMaterials);

// ページの属性（ページID・親ページID・作成/更新情報）はHTML本文ではなくサイドカーが
// 正本になり、サイドパネル（assets/index.html）が /api/page-meta 経由で表示・編集する。
// このため、かつて文書先頭に置いていた <m-page-info> コンポーネントは廃止した。


// ─────────────────────────────────────────────────────────────────────────
// 業務要素（発注書・見積もり）
//
// <m-file> から責務を分離した「業務上の意味」を持つ要素群（docs/【考察】通信記録処理.md §4.5）。
// 意味は tag 属性の文字列ではなく**要素そのもの**が持つので、プラグインは自分の要素だけを
// 拾えばよく、分岐が要らない。<m-file> の中に置いても、単独で置いてもよい。
//
// 4種はヘッダの項目が違うだけなので、共通の基底クラスにフィールド定義を与えて実装する。
// 新規コードなので docs/開発方針.md §4 に従い、インライン on*= / style= は使わない
// （クラス付与＋addEventListener。配色は assets/components.css）。
// ─────────────────────────────────────────────────────────────────────────
class MBusinessBlock extends MElement {
    // サブクラスが定義するもの:
    //   blockLabel  … 見出しに出す名前
    //   blockKind   … CSSの配色クラス（m-block--<kind>）
    //   blockFields … [{ attr, label, type }]（type は 'text' | 'number' | 'date'）
    //   hasItems    … 明細（<m-item>）を持つか

    async render() {
        if (!this.isConnected) return;

        // 子（明細など）を退避してから描画し、あとで戻す
        const contents = takeCustomChildren(this);
        const isEdit = document.body.hasAttribute('edit-mode');

        const rows = this.blockFields.map(f => {
            const v = this.getAttribute(f.attr) || '';
            if (isEdit) {
                return `<label class="m-block-field">
                    <span class="m-block-field-label">${escapeText(f.label)}</span>
                    <input class="m-block-input" type="${f.type === 'text' ? 'text' : f.type}"
                           data-attr="${escapeAttr(f.attr)}" value="${escapeAttr(v)}">
                </label>`;
            }
            return `<div class="m-block-field">
                <span class="m-block-field-label">${escapeText(f.label)}</span>
                <span class="m-block-field-value">${escapeText(v || '未指定')}</span>
            </div>`;
        }).join('');

        this.innerHTML = `<div class="m-block m-block--${escapeAttr(this.blockKind)}">
            <div class="m-block-title">${escapeText(this.blockLabel)}</div>
            <div class="m-block-fields">${rows}</div>
            ${this.hasItems ? '<div class="m-block-items"></div>' : ''}
        </div>`;

        // 退避した子（明細）を戻す
        const slot = this.querySelector('.m-block-items') || this.querySelector('.m-block');
        if (slot) contents.forEach(c => slot.appendChild(c));

        // 編集フィールドを属性へ書き戻す（data-attr の共通配線。インライン on* は使わない）
        this.bindEditFields();
    }
}

// 顧客の発注書（売り側）。明細の単価は売価。
class MClientOrder extends MBusinessBlock {
    static get observedAttributes() { return ['order-no', 'client-name', 'ordered-at']; }
    get blockLabel() { return '顧客の発注書'; }
    get blockKind() { return 'client-order'; }
    get hasItems() { return true; }
    get blockFields() {
        return [
            { attr: 'order-no', label: '発注書番号', type: 'text' },
            { attr: 'client-name', label: '発注元', type: 'text' },
            { attr: 'ordered-at', label: '発注日', type: 'date' },
        ];
    }
}
customElements.define('m-client-order', MClientOrder);

// 弊社の発注書（買い側）。明細の単価は原価。
class MSupplierOrder extends MBusinessBlock {
    static get observedAttributes() { return ['order-no', 'supplier-name', 'ordered-at']; }
    get blockLabel() { return '弊社の発注書'; }
    get blockKind() { return 'supplier-order'; }
    get hasItems() { return true; }
    get blockFields() {
        return [
            { attr: 'order-no', label: '発注番号', type: 'text' },
            { attr: 'supplier-name', label: '発注先', type: 'text' },
            { attr: 'ordered-at', label: '発注日', type: 'date' },
        ];
    }
}
customElements.define('m-supplier-order', MSupplierOrder);

// 弊社の見積もり（客先へ出す見積）。
class MOurEstimate extends MBusinessBlock {
    static get observedAttributes() { return ['item-id', 'client-name', 'price', 'estimated-at']; }
    get blockLabel() { return '弊社の見積もり'; }
    get blockKind() { return 'our-estimate'; }
    get hasItems() { return false; }
    get blockFields() {
        return [
            { attr: 'item-id', label: '品番', type: 'text' },
            { attr: 'client-name', label: '客先', type: 'text' },
            { attr: 'price', label: '見積単価', type: 'number' },
            { attr: 'estimated-at', label: '見積日', type: 'date' },
        ];
    }
}
customElements.define('m-our-estimate', MOurEstimate);

// 材料屋・加工業者の見積もり（仕入側の見積）。
class MSupplierEstimate extends MBusinessBlock {
    static get observedAttributes() { return ['item-name', 'supplier-name', 'cost', 'estimated-at']; }
    get blockLabel() { return '材料屋・加工業者の見積もり'; }
    get blockKind() { return 'supplier-estimate'; }
    get hasItems() { return false; }
    get blockFields() {
        return [
            { attr: 'item-name', label: '品名', type: 'text' },
            { attr: 'supplier-name', label: '仕入先', type: 'text' },
            { attr: 'cost', label: '仕入単価', type: 'number' },
            { attr: 'estimated-at', label: '見積日', type: 'date' },
        ];
    }
}
customElements.define('m-supplier-estimate', MSupplierEstimate);
