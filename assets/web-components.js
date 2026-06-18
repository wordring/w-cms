// === <m-tag> (名前：値) の定義 ===
class MTag extends HTMLElement {
    static get observedAttributes() { return ['value']; }
    connectedCallback() { this.render(); }
    attributeChangedCallback() { this.render(); }

    render() {
        const name = this.getAttribute('name') || '';
        const value = this.getAttribute('value') || '';
        const isEdit = document.body.hasAttribute('edit-mode');

        if (isEdit) {
            this.innerHTML = `
                <div class="tag-edit-container" style="display:inline-flex; align-items:center; border:1px solid #007bff; border-radius:4px; padding:2px; margin:4px; font-family:sans-serif; background:#fff; box-shadow: 0 1px 3px rgba(0,123,255,0.1);">
                    <span style="font-weight:bold; padding: 4px 8px; background:#e0f0ff; color:#007bff; border-radius:2px; font-size:13px;">${name}</span>
                    <input type="text" value="${value}" style="border:none; padding:4px 8px; outline:none; font-size:13px; width:150px;" 
                           oninput="this.getRootNode().host.setAttribute('value', this.value); if(window.updateHtmlPreview) window.updateHtmlPreview();">
                </div>
            `;
        } else {
            this.innerHTML = `
                <span class="tag-badge-container" style="display:inline-flex; align-items:center; border:1px solid #e2e8f0; border-radius:16px; padding:4px 12px; margin:4px; background:#f8fafc; font-family:sans-serif; font-size:13px; box-shadow: 0 1px 2px rgba(0,0,0,0.02); transition: all 0.2s;">
                    <strong style="color:#64748b; margin-right:6px;">${name}:</strong>
                    <span style="color:#1e293b; font-weight:500;">${value}</span>
                </span>
            `;
        }
    }
}
customElements.define('m-tag', MTag);

// === <m-item> (部品明細) の定義 ===
class MItem extends HTMLElement {
    static get observedAttributes() { return ['item-id', 'item-name', 'price', 'cost', 'quantity', 'status']; }
    connectedCallback() { this.render(); }
    attributeChangedCallback() { this.render(); }

    render() {
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

        if (isEdit) {
            // 編集モード
            let formFields = '';
            if (parentTag === '顧客の発注書') {
                formFields = `
                    <div style="display:flex; flex-wrap:wrap; gap:8px; align-items:center; background:#f8fafc; border:1px solid #e2e8f0; border-radius:4px; padding:8px; margin:4px 0;">
                        <input type="text" value="${itemId}" placeholder="部品コード" style="width:100px; padding:4px; border:1px solid #cbd5e1; border-radius:4px;"
                               oninput="this.getRootNode().host.setAttribute('item-id', this.value); if(window.updateHtmlPreview) window.updateHtmlPreview();">
                        <input type="text" value="${itemName}" placeholder="部品名" style="width:150px; padding:4px; border:1px solid #cbd5e1; border-radius:4px;"
                               oninput="this.getRootNode().host.setAttribute('item-name', this.value); if(window.updateHtmlPreview) window.updateHtmlPreview();">
                        <input type="number" value="${price}" placeholder="単価" style="width:80px; padding:4px; border:1px solid #cbd5e1; border-radius:4px;"
                               oninput="this.getRootNode().host.setAttribute('price', this.value); if(window.updateHtmlPreview) window.updateHtmlPreview();"> 円
                        <input type="number" value="${quantity}" placeholder="数量" style="width:50px; padding:4px; border:1px solid #cbd5e1; border-radius:4px;"
                               oninput="this.getRootNode().host.setAttribute('quantity', this.value); if(window.updateHtmlPreview) window.updateHtmlPreview();"> 個
                        <select onchange="this.getRootNode().host.setAttribute('status', this.value); if(window.updateHtmlPreview) window.updateHtmlPreview();" style="padding:4px; border:1px solid #cbd5e1; border-radius:4px;">
                            <option value="未着手" ${status === '未着手' || status === '' ? 'selected' : ''}>未着手</option>
                            <option value="加工中" ${status === '加工中' ? 'selected' : ''}>加工中</option>
                            <option value="検査中" ${status === '検査中' ? 'selected' : ''}>検査中</option>
                            <option value="納品済" ${status === '納品済' ? 'selected' : ''}>納品済</option>
                        </select>
                        <button type="button" onclick="const host = this.getRootNode().host; host.parentNode.removeChild(host); if(window.updateHtmlPreview) window.updateHtmlPreview();" style="background:#ef4444; color:#fff; border:none; padding:4px 8px; border-radius:4px; cursor:pointer;">削除</button>
                    </div>
                `;
            } else {
                // 弊社の発注書用（仕入原価など）
                formFields = `
                    <div style="display:flex; flex-wrap:wrap; gap:8px; align-items:center; background:#f8fafc; border:1px solid #e2e8f0; border-radius:4px; padding:8px; margin:4px 0;">
                        <input type="text" value="${itemName}" placeholder="部品名/品名" style="width:200px; padding:4px; border:1px solid #cbd5e1; border-radius:4px;"
                               oninput="this.getRootNode().host.setAttribute('item-name', this.value); if(window.updateHtmlPreview) window.updateHtmlPreview();">
                        <input type="number" value="${cost}" placeholder="単価(原価)" style="width:80px; padding:4px; border:1px solid #cbd5e1; border-radius:4px;"
                               oninput="this.getRootNode().host.setAttribute('cost', this.value); if(window.updateHtmlPreview) window.updateHtmlPreview();"> 円
                        <input type="number" value="${quantity}" placeholder="数量" style="width:50px; padding:4px; border:1px solid #cbd5e1; border-radius:4px;"
                               oninput="this.getRootNode().host.setAttribute('quantity', this.value); if(window.updateHtmlPreview) window.updateHtmlPreview();"> 個
                        <select onchange="this.getRootNode().host.setAttribute('status', this.value); if(window.updateHtmlPreview) window.updateHtmlPreview();" style="padding:4px; border:1px solid #cbd5e1; border-radius:4px;">
                            <option value="未納品" ${status === '未納品' || status === '' ? 'selected' : ''}>未納品</option>
                            <option value="納品済" ${status === '納品済' ? 'selected' : ''}>納品済</option>
                        </select>
                        <button type="button" onclick="const host = this.getRootNode().host; host.parentNode.removeChild(host); if(window.updateHtmlPreview) window.updateHtmlPreview();" style="background:#ef4444; color:#fff; border:none; padding:4px 8px; border-radius:4px; cursor:pointer;">削除</button>
                    </div>
                `;
            }
            this.innerHTML = formFields;
        } else {
            // 閲覧モード
            let detail = '';
            if (parentTag === '顧客の発注書') {
                detail = `
                    <div style="display:flex; align-items:center; justify-content:space-between; padding:6px 12px; border-bottom:1px solid #f1f5f9; font-size:13px; font-family:sans-serif;">
                        <span style="font-family:monospace; color:#475569; font-weight:bold; width:120px;">[${itemId}]</span>
                        <span style="flex-grow:1; color:#1e293b; font-weight:500;">${itemName}</span>
                        <span style="margin:0 12px; color:#475569;">単価: ${Number(price).toLocaleString()} 円 × ${quantity}個</span>
                        <span style="font-weight:bold; color:#0f172a; min-width:80px; text-align:right;">計: ${(price * quantity).toLocaleString()} 円</span>
                        <span style="margin-left:16px; font-size:11px; font-weight:bold; color:${statusColor}; background:${statusBg}; padding:2px 8px; border-radius:12px; border: 1px solid ${statusColor}30;">${status || '未着手'}</span>
                    </div>
                `;
            } else {
                detail = `
                    <div style="display:flex; align-items:center; justify-content:space-between; padding:6px 12px; border-bottom:1px solid #f1f5f9; font-size:13px; font-family:sans-serif;">
                        <span style="flex-grow:1; color:#1e293b; font-weight:500;">${itemName}</span>
                        <span style="margin:0 12px; color:#475569;">単価(原価): ${Number(cost).toLocaleString()} 円 × ${quantity}個</span>
                        <span style="font-weight:bold; color:#0f172a; min-width:80px; text-align:right;">計: ${(cost * quantity).toLocaleString()} 円</span>
                        <span style="margin-left:16px; font-size:11px; font-weight:bold; color:${statusColor}; background:${statusBg}; padding:2px 8px; border-radius:12px; border: 1px solid ${statusColor}30;">${status || '未納品'}</span>
                    </div>
                `;
            }
            this.innerHTML = detail;
        }
    }
}
customElements.define('m-item', MItem);

// === <m-file> (ファイル/PDF) の定義 ===
class MFile extends HTMLElement {
    static get observedAttributes() { return ['src', 'name', 'tag', 'price', 'quantity', 'cost', 'order-no', 'client-name', 'supplier-name', 'ordered-at']; }
    connectedCallback() { this.render(); }
    attributeChangedCallback() { this.render(); }

    render() {
        const src = this.getAttribute('src') || '#';
        const name = this.getAttribute('name') || '添付ファイル';
        const tag = this.getAttribute('tag') || '未分類';
        const orderNo = this.getAttribute('order-no') || '';
        const clientName = this.getAttribute('client-name') || '';
        const supplierName = this.getAttribute('supplier-name') || '';
        const orderedAt = this.getAttribute('ordered-at') || '';
        const isEdit = document.body.hasAttribute('edit-mode');

        // タグの種類に応じたカラースキームの設定
        let tagColor = '#64748b';
        let bgColor = '#f8fafc';
        if (tag === '顧客の発注書') { tagColor = '#10b981'; bgColor = '#ecfdf5'; }
        if (tag === '弊社の発注書') { tagColor = '#ef4444'; bgColor = '#fef2f2'; }
        if (tag === '材料屋・加工業者の見積もり') { tagColor = '#f59e0b'; bgColor = '#fffbeb'; }
        if (tag === '弊社の見積もり') { tagColor = '#3b82f6'; bgColor = '#eff6ff'; }

        // ヘッダー情報（発注番号、発注元・先、発注日）のマークアップ
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

        // 描画ベース
        let innerHTML = `
            <div class="file-block" style="border: 1px solid #e2e8f0; border-left: 5px solid ${tagColor}; border-radius: 6px; padding: 12px 16px; margin: 12px 0; background: #fff; font-family: sans-serif; box-shadow: 0 1px 3px rgba(0,0,0,0.05); transition: all 0.2s;">
                <div style="display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 8px; border-bottom: 1px solid #f1f5f9; padding-bottom: 8px; margin-bottom: 8px;">
                    <div style="display: flex; align-items: center; gap: 8px;">
                        <span style="font-size: 11px; font-weight: bold; color: ${tagColor}; background: ${bgColor}; padding: 3px 8px; border-radius: 4px; border: 1px solid ${tagColor}20;">${tag}</span>
                        <a href="${src}" target="_blank" style="text-decoration: none; color: #1e40af; font-weight: 600; font-size: 14px; hover:text-decoration:underline;">📄 ${name}</a>
                    </div>
                    <div>
                        <a href="${src}" download style="font-size: 12px; text-decoration: none; background: #f1f5f9; padding: 6px 12px; border-radius: 4px; color: #475569; font-weight: 500; border: 1px solid #cbd5e1; transition: background 0.2s;" onmouseover="this.style.background='#e2e8f0'" onmouseout="this.style.background='#f1f5f9'">ダウンロード</a>
                    </div>
                </div>
                ${headerMetaHTML}
        `;

        if (isEdit) {
            innerHTML += `<div style="margin-top: 10px; padding: 10px 0; border-top: 1px dashed #e2e8f0; font-size: 13px; color:#475569;">`;
            if (tag === '顧客の発注書') {
                innerHTML += `
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
                            this.closest('m-file').appendChild(item);
                            if(window.updateHtmlPreview) window.updateHtmlPreview();
                        " style="background:#10b981; color:#fff; border:none; padding:6px 12px; border-radius:4px; cursor:pointer; font-weight:bold;">＋ 部品を追加</button>
                    </div>
                `;
            } else if (tag === '弊社の発注書') {
                innerHTML += `
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
                            this.closest('m-file').appendChild(item);
                            if(window.updateHtmlPreview) window.updateHtmlPreview();
                        " style="background:#10b981; color:#fff; border:none; padding:6px 12px; border-radius:4px; cursor:pointer; font-weight:bold;">＋ 品目を追加</button>
                    </div>
                `;
            } else if (tag === '弊社の見積もり') {
                const price = this.getAttribute('price') || '0';
                const quantity = this.getAttribute('quantity') || '1';
                innerHTML += `
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
                innerHTML += `
                    <div>
                        単価 (仕入原価): 
                        <input type="number" value="${cost}" style="width: 90px; border: 1px solid #cbd5e1; border-radius: 4px; padding: 4px 8px;"
                                   oninput="this.getRootNode().host.setAttribute('cost', this.value); if(window.updateHtmlPreview) window.updateHtmlPreview();"> 円
                    </div>
                `;
            }
            innerHTML += `</div>`;
        }

        // 子要素（m-item）を表示するためのスロット領域を作成
        innerHTML += `<div class="items-list" style="margin-top: 10px; border-top: 1px dashed #f1f5f9; padding-top: 8px;">`;
        innerHTML += `</div>`; // スロット
        innerHTML += `</div>`;

        this.innerHTML = innerHTML;

        // 子要素（m-item）たちを `.items-list` 領域に移動する
        const container = this.querySelector('.items-list');
        const items = Array.from(this.childNodes).filter(node => node.nodeName === 'M-ITEM');
        items.forEach(item => {
            container.appendChild(item);
        });
    }
}
customElements.define('m-file', MFile);
