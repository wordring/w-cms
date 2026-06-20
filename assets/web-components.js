// === テンプレートキャッシュと非同期ローダー ===
const templateCache = {};

async function fetchTemplate(name) {
    if (templateCache[name]) {
        return templateCache[name];
    }
    const response = await fetch(`/assets/templates/${name}.html`);
    const text = await response.text();
    templateCache[name] = text;
    return text;
}

// === <m-tag> (名前：値) の定義 ===
class MTag extends HTMLElement {
    static get observedAttributes() { return ['value']; }
    async connectedCallback() { await this.render(); }
    async attributeChangedCallback() { await this.render(); }

    async render() {
        if (!this.isConnected) return;
        // <m-page-info> に内包される親ページID等のタグは、ページ情報パネルが表示を
        // 担当するため、本文中にはインライン表示しない（値はシリアライズ時に属性から読む）。
        if (this.closest('m-page-info')) { this.innerHTML = ''; return; }
        const name = this.getAttribute('name') || '';
        const value = this.getAttribute('value') || '';
        const isEdit = document.body.hasAttribute('edit-mode');

        const templateName = isEdit ? 'm-tag-edit' : 'm-tag-view';
        let html = await fetchTemplate(templateName);

        // 変数置換
        html = html.replace(/\${name}/g, name).replace(/\${value}/g, value);
        this.innerHTML = html;
    }
}
customElements.define('m-tag', MTag);

// === <m-item> (部品明細) の定義 ===
class MItem extends HTMLElement {
    static get observedAttributes() { return ['item-id', 'item-name', 'price', 'cost', 'quantity', 'status']; }
    async connectedCallback() { await this.render(); }
    async attributeChangedCallback() { await this.render(); }

    async render() {
        if (!this.isConnected) return;
        const itemId = this.getAttribute('item-id') || '';
        const itemName = this.getAttribute('item-name') || '';
        const price = this.getAttribute('price') || '0';
        const cost = this.getAttribute('cost') || '0';
        const quantity = this.getAttribute('quantity') || '1';
        const status = this.getAttribute('status') || '';
        const isEdit = document.body.hasAttribute('edit-mode');

        // 親の m-file が「顧客の発注書」か「弊社の発注書」かで表示を変える
        const parentFile = this.closest('m-file');
        const parentTag = parentFile ? parentFile.getAttribute('tag') : '';

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
        if (parentTag === '顧客の発注書') {
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
            .replace(/\${itemId}/g, itemId)
            .replace(/\${itemName}/g, itemName)
            .replace(/\${priceDisplay}/g, priceNum.toLocaleString())
            .replace(/\${costDisplay}/g, costNum.toLocaleString())
            .replace(/\${totalDisplay}/g, (parentTag === '顧客の発注書' ? priceNum * qtyNum : costNum * qtyNum).toLocaleString())
            .replace(/\${price}/g, price)
            .replace(/\${cost}/g, cost)
            .replace(/\${quantity}/g, quantity)
            .replace(/\${statusColor}/g, statusColor)
            .replace(/\${statusBg}/g, statusBg)
            .replace(/\${status}/g, status || (parentTag === '顧客の発注書' ? '未着手' : '未納品'));

        // selectの選択状態の置換
        html = html
            .replace(/\${selectUnstarted}/g, (status === '未着手' || status === '') ? 'selected' : '')
            .replace(/\${selectProcessing}/g, status === '加工中' ? 'selected' : '')
            .replace(/\${selectInspecting}/g, status === '検査中' ? 'selected' : '')
            .replace(/\${selectFinished}/g, status === '納品済' ? 'selected' : '')
            .replace(/\${selectUndelivered}/g, (status === '未納品' || status === '') ? 'selected' : '')
            .replace(/\${selectDelivered}/g, status === '納品済' ? 'selected' : '');

        this.innerHTML = html;
    }
}
customElements.define('m-item', MItem);

// === <m-file> (ファイル/PDF) の定義 ===
class MFile extends HTMLElement {
    static get observedAttributes() { return ['src', 'name', 'tag', 'price', 'quantity', 'cost', 'order-no', 'client-name', 'supplier-name', 'ordered-at']; }
    async connectedCallback() { await this.render(); }
    async attributeChangedCallback() { await this.render(); }

    async render() {
        if (!this.isConnected) return;
        // 先に子ノード（m-item）を退避（孫要素のコンテナ内に移動しているものも含む）
        const items = Array.from(this.querySelectorAll('m-item')).filter(item => item.closest('m-file') === this);

        const src = this.getAttribute('src') || '#';
        const name = this.getAttribute('name') || '添付ファイル';
        const tag = this.getAttribute('tag') || '未分類';
        const orderNo = this.getAttribute('order-no') || '';
        const clientName = this.getAttribute('client-name') || '';
        const supplierName = this.getAttribute('supplier-name') || '';
        const orderedAt = this.getAttribute('ordered-at') || '';
        const isEdit = document.body.hasAttribute('edit-mode');

        // カラースキームの設定
        let tagColor = '#64748b';
        let bgColor = '#f8fafc';
        if (tag === '顧客の発注書') { tagColor = '#10b981'; bgColor = '#ecfdf5'; }
        if (tag === '弊社の発注書') { tagColor = '#ef4444'; bgColor = '#fef2f2'; }
        if (tag === '材料屋・加工業者の見積もり') { tagColor = '#f59e0b'; bgColor = '#fffbeb'; }
        if (tag === '弊社の見積もり') { tagColor = '#3b82f6'; bgColor = '#eff6ff'; }

        // ヘッダー情報の組み立て
        let headerMetaHTML = '';
        if (tag === '顧客の発注書') {
            headerMetaHTML = `
                <div style="font-size: 13px; color: #475569; margin: 8px 0; display: flex; flex-wrap: wrap; gap: 16px;">
                    <div><strong>発注書番号:</strong> ${orderNo || '未指定'}</div>
                    <div><strong>発注元:</strong> ${clientName || '未指定'}</div>
                    <div><strong>発注日:</strong> ${orderedAt || '未指定'}</div>
                </div>
            `;
        } else if (tag === '弊社の発注書') {
            headerMetaHTML = `
                <div style="font-size: 13px; color: #475569; margin: 8px 0; display: flex; flex-wrap: wrap; gap: 16px;">
                    <div><strong>発注番号:</strong> ${orderNo || '未指定'}</div>
                    <div><strong>発注先:</strong> ${supplierName || '未指定'}</div>
                    <div><strong>発注日:</strong> ${orderedAt || '未指定'}</div>
                </div>
            `;
        }

        // 編集フォーム内レイアウトの組み立て
        let editFieldsHTML = '';
        if (isEdit) {
            if (tag === '顧客の発注書') {
                editFieldsHTML = `
                    <div style="display:flex; flex-wrap:wrap; gap:12px; align-items:center; margin-bottom:10px;">
                        <div>
                            発注書番号: 
                            <input type="text" value="${orderNo}" style="width: 120px; border: 1px solid #cbd5e1; border-radius: 4px; padding: 4px;"
                                   oninput="this.getRootNode().host.setAttribute('order-no', this.value); if(window.updateHtmlPreview) window.updateHtmlPreview();">
                        </div>
                        <div>
                            発注元顧客: 
                            <input type="text" value="${clientName}" style="width: 120px; border: 1px solid #cbd5e1; border-radius: 4px; padding: 4px;"
                                   oninput="this.getRootNode().host.setAttribute('client-name', this.value); if(window.updateHtmlPreview) window.updateHtmlPreview();">
                        </div>
                        <div>
                            発注日付: 
                            <input type="date" value="${orderedAt}" style="border: 1px solid #cbd5e1; border-radius: 4px; padding: 4px;"
                                   onchange="this.getRootNode().host.setAttribute('ordered-at', this.value); if(window.updateHtmlPreview) window.updateHtmlPreview();">
                        </div>
                    </div>
                `;
            } else if (tag === '弊社の発注書') {
                editFieldsHTML = `
                    <div style="display:flex; flex-wrap:wrap; gap:12px; align-items:center; margin-bottom:10px;">
                        <div>
                            発注書番号: 
                            <input type="text" value="${orderNo}" style="width: 120px; border: 1px solid #cbd5e1; border-radius: 4px; padding: 4px;"
                                   oninput="this.getRootNode().host.setAttribute('order-no', this.value); if(window.updateHtmlPreview) window.updateHtmlPreview();">
                        </div>
                        <div>
                            発注先仕入先: 
                            <input type="text" value="${supplierName}" style="width: 120px; border: 1px solid #cbd5e1; border-radius: 4px; padding: 4px;"
                                   oninput="this.getRootNode().host.setAttribute('supplier-name', this.value); if(window.updateHtmlPreview) window.updateHtmlPreview();">
                        </div>
                        <div>
                            発注日付: 
                            <input type="date" value="${orderedAt}" style="border: 1px solid #cbd5e1; border-radius: 4px; padding: 4px;"
                                   onchange="this.getRootNode().host.setAttribute('ordered-at', this.value); if(window.updateHtmlPreview) window.updateHtmlPreview();">
                        </div>
                    </div>
                `;
            } else if (tag === '弊社の見積もり') {
                const price = this.getAttribute('price') || '0';
                const quantity = this.getAttribute('quantity') || '1';
                editFieldsHTML = `
                    <div style="display:flex; gap:16px; align-items:center;">
                        <div>
                            単価 (売上): 
                            <input type="number" value="${price}" style="width: 90px; border: 1px solid #cbd5e1; border-radius: 4px; padding: 4px 8px;"
                                   oninput="this.getRootNode().host.setAttribute('price', this.value); if(window.updateHtmlPreview) window.updateHtmlPreview();"> 円
                        </div>
                        <div>
                            数量: 
                            <input type="number" value="${quantity}" style="width: 60px; border: 1px solid #cbd5e1; border-radius: 4px; padding: 4px 8px;"
                                   oninput="this.getRootNode().host.setAttribute('quantity', this.value); if(window.updateHtmlPreview) window.updateHtmlPreview();">
                        </div>
                    </div>
                `;
            } else if (tag === '材料屋・加工業者の見積もり') {
                const cost = this.getAttribute('cost') || '0';
                editFieldsHTML = `
                    <div>
                        単価 (仕入原価): 
                        <input type="number" value="${cost}" style="width: 90px; border: 1px solid #cbd5e1; border-radius: 4px; padding: 4px 8px;"
                                   oninput="this.getRootNode().host.setAttribute('cost', this.value); if(window.updateHtmlPreview) window.updateHtmlPreview();"> 円
                    </div>
                `;
            }
        }

        let pdfEmbedHTML = '';
        if (src && src !== '#' && src.toLowerCase().endsWith('.pdf')) {
            const pageId = window.currentPageId || new URLSearchParams(window.location.search).get('id');
            if (pageId) {
                const prefix = pageId.length >= 2 ? pageId.substring(0, 2) : "00";
                pdfEmbedHTML = `<embed src="/data/master/${prefix}/${pageId}/${src}" type="application/pdf" width="100%" height="400px" style="border:1px solid #cbd5e1; border-radius:4px; margin: 10px 0;">`;
            } else {
                pdfEmbedHTML = `<div style="padding:10px; background:#fffbeb; color:#b45309; border-radius:4px; font-size:12px;">（保存後にプレビューが表示されます）</div>`;
            }
        }

        const templateName = isEdit ? 'm-file-edit' : 'm-file-view';
        let html = await fetchTemplate(templateName);

        // 変数置換
        html = html
            .replace(/\${tagColor}/g, tagColor)
            .replace(/\${bgColor}/g, bgColor)
            .replace(/\${tag}/g, tag)
            .replace(/\${src}/g, src)
            .replace(/\${name}/g, name)
            .replace(/\${headerMetaHTML}/g, headerMetaHTML)
            .replace(/\${editFieldsHTML}/g, editFieldsHTML)
            .replace(/\${pdfEmbedHTML}/g, pdfEmbedHTML);

        this.innerHTML = html;

        // 退避させていた m-item 子要素たちを `.items-list` 領域に再配置
        const container = this.querySelector('.items-list');
        if (container) {
            items.forEach(item => container.appendChild(item));
        }

        // PDFのD&D設定
        const dropZone = this.querySelector('.pdf-drop-zone');
        if (dropZone) {
            dropZone.addEventListener('dragover', (e) => {
                e.preventDefault();
                dropZone.style.background = '#e2e8f0';
            });
            dropZone.addEventListener('dragleave', (e) => {
                e.preventDefault();
                dropZone.style.background = '#f8fafc';
            });
            dropZone.addEventListener('drop', (e) => {
                e.preventDefault();
                dropZone.style.background = '#f8fafc';
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

class MChildList extends HTMLElement {
    constructor() {
        super();
        this.attachShadow({ mode: 'open' });
    }

    connectedCallback() {
        this.render();
    }

    async render() {
        // currentPageId is defined globally in index.html
        const pageId = window.currentPageId || "000000";
        const res = await fetch(`/api/children?parent_id=${pageId}`);
        let pages = [];
        if (res.ok) {
            pages = await res.json() || [];
        }

        let itemsHtml = pages.length === 0 
            ? `<li style="color: #94a3b8; font-style: italic;">子ページはありません</li>`
            : pages.map(p => `<li><a href="/${p.ID}" style="color: #38bdf8; text-decoration: none;">📄 ${p.Title}</a></li>`).join('');

        const html = `
        <style>
            .child-list {
                background: #f8fafc;
                border: 1px dashed #cbd5e1;
                border-radius: 8px;
                padding: 16px;
                margin: 10px 0;
                list-style: none;
            }
            .child-list li {
                margin-bottom: 8px;
            }
            .child-list a:hover {
                text-decoration: underline !important;
            }
        </style>
        <ul class="child-list">
            ${itemsHtml}
        </ul>
        `;
        this.shadowRoot.innerHTML = html;
    }
}
customElements.define('m-child-list', MChildList);

// ============================================
// Register all components
// ============================================

// === <m-material> (必要部材定義) の定義 ===
class MMaterial extends HTMLElement {
    static get observedAttributes() { return ['item-name', 'cost', 'supplier-name', 'quantity']; }
    async connectedCallback() { await this.render(); }
    async attributeChangedCallback() { await this.render(); }

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
            .replace(/\${itemName}/g, itemName)
            .replace(/\${costDisplay}/g, costNum.toLocaleString())
            .replace(/\${cost}/g, cost)
            .replace(/\${supplierName}/g, supplierName)
            .replace(/\${quantity}/g, quantity);

        this.innerHTML = html;
    }
}
customElements.define('m-material', MMaterial);

// === <m-required-materials> (手配進捗状況) の定義 ===
class MRequiredMaterials extends HTMLElement {
    static get observedAttributes() { return ['page-id']; }
    async connectedCallback() { await this.render(); }
    async attributeChangedCallback() { await this.render(); }

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

                tr.innerHTML = `
                    <td style="padding: 10px; font-weight: 500; color: #1e293b;">${item.material_name}</td>
                    <td style="padding: 10px; color: #475569;">${item.supplier_name || '-'}</td>
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

// === <m-page-info> (ページ情報パネル) の定義 ===
// 文書先頭に1つだけ置き、ページの属性（ページID・親ページID・作成日時・作成者・
// 更新日時）を集約表示する。編集できるのは親ページIDのみで、内包する
// <m-tag name="親ページID"> の value を裏で更新する（既存の同期機構をそのまま活用）。
// 作成日時・作成者・更新日時はサーバーが管理する読み取り専用フィールド。
class MPageInfo extends HTMLElement {
    static get observedAttributes() { return ['created-at', 'created-by', 'updated-at']; }

    constructor() {
        super();
        this.attachShadow({ mode: 'open' });
    }

    async connectedCallback() { await this.render(); }
    async attributeChangedCallback() { await this.render(); }

    // ISO8601（RFC3339）の日時を日本語ロケールで読みやすく整形する。空・不正値は「—」。
    formatDateTime(value) {
        if (!value) return '—';
        const d = new Date(value);
        if (isNaN(d.getTime())) return value;
        return d.toLocaleString('ja-JP', {
            year: 'numeric', month: '2-digit', day: '2-digit',
            hour: '2-digit', minute: '2-digit'
        });
    }

    // 内包する親ページIDタグの現在値（無ければ空文字）。
    parentValue() {
        const tagEl = this.querySelector('m-tag[name="親ページID"]');
        return tagEl ? (tagEl.getAttribute('value') || '') : '';
    }

    async render() {
        if (!this.isConnected) return;
        const isEdit = document.body.hasAttribute('edit-mode');
        const pageId = window.currentPageId || '（未保存）';
        const parentId = this.parentValue();

        const templateName = isEdit ? 'm-page-info-edit' : 'm-page-info-view';
        let html = await fetchTemplate(templateName);
        html = html
            .replace(/\$\{pageId\}/g, pageId)
            .replace(/\$\{parentId\}/g, parentId || (isEdit ? '' : '—'))
            .replace(/\$\{createdAt\}/g, this.formatDateTime(this.getAttribute('created-at')))
            .replace(/\$\{createdBy\}/g, this.getAttribute('created-by') || '—')
            .replace(/\$\{updatedAt\}/g, this.formatDateTime(this.getAttribute('updated-at')));
        this.shadowRoot.innerHTML = html;

        // 編集モードでは親ページID入力を内包タグへ反映する。
        const input = this.shadowRoot.querySelector('.pi-parent-input');
        if (input) {
            input.addEventListener('change', () => this.applyParentID(input.value));
        }
    }

    // 親ページID入力を内包する <m-tag name="親ページID"> へ反映し、保存をトリガーする。
    applyParentID(rawValue) {
        const parentId = (rawValue || '').trim();
        let tagEl = this.querySelector('m-tag[name="親ページID"]');
        if (parentId === '') {
            if (tagEl) tagEl.remove();
        } else {
            if (!tagEl) {
                tagEl = document.createElement('m-tag');
                tagEl.setAttribute('name', '親ページID');
                this.appendChild(tagEl);
            }
            tagEl.setAttribute('value', parentId);
        }
        if (window.updateHtmlPreview) window.updateHtmlPreview();
        if (window.triggerAutoSave) window.triggerAutoSave();
    }
}
customElements.define('m-page-info', MPageInfo);

