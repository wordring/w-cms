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
        // 先に子ノード（m-item）を退避
        const items = Array.from(this.childNodes).filter(node => node.nodeName === 'M-ITEM');

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
                    <div style="margin-top:8px;">
                        <button type="button" onclick="
                            const item = document.createElement('m-item');
                            item.setAttribute('item-id', 'NEW-ITEM');
                            item.setAttribute('item-name', '新規部品');
                            item.setAttribute('price', '0');
                            item.setAttribute('quantity', '1');
                            item.setAttribute('status', '未着手');
                            const file = this.closest('m-file');
                            const container = file.querySelector('.items-list') || file;
                            container.appendChild(item);
                            if(window.updateHtmlPreview) window.updateHtmlPreview();
                        " style="background:#10b981; color:#fff; border:none; padding:6px 12px; border-radius:4px; cursor:pointer; font-weight:bold;">＋ 部品を追加</button>
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
                    <div style="margin-top:8px;">
                        <button type="button" onclick="
                            const item = document.createElement('m-item');
                            item.setAttribute('item-name', '新規品目');
                            item.setAttribute('cost', '0');
                            item.setAttribute('quantity', '1');
                            item.setAttribute('status', '未納品');
                            const file = this.closest('m-file');
                            const container = file.querySelector('.items-list') || file;
                            container.appendChild(item);
                            if(window.updateHtmlPreview) window.updateHtmlPreview();
                        " style="background:#10b981; color:#fff; border:none; padding:6px 12px; border-radius:4px; cursor:pointer; font-weight:bold;">＋ 品目を追加</button>
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
            .replace(/\${editFieldsHTML}/g, editFieldsHTML);

        this.innerHTML = html;

        // 退避させていた m-item 子要素たちを `.items-list` 領域に再配置
        const container = this.querySelector('.items-list');
        if (container) {
            items.forEach(item => {
                container.appendChild(item);
            });
        }
    }
}
customElements.define('m-file', MFile);
