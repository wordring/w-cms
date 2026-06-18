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
                           oninput="this.getRootNode().host.setAttribute('value', this.value); updateHtmlPreview();">
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

// === <m-file> (ファイル/PDF) の定義 ===
class MFile extends HTMLElement {
    static get observedAttributes() { return ['src', 'name', 'tag', 'price', 'quantity', 'cost']; }
    connectedCallback() { this.render(); }
    attributeChangedCallback() { this.render(); }

    render() {
        const src = this.getAttribute('src') || '#';
        const name = this.getAttribute('name') || '添付ファイル';
        const tag = this.getAttribute('tag') || '未分類';
        const isEdit = document.body.hasAttribute('edit-mode');

        // タグの種類に応じたカラースキームの設定
        let tagColor = '#64748b';
        let bgColor = '#f8fafc';
        if (tag === '顧客の発注書') { tagColor = '#10b981'; bgColor = '#ecfdf5'; }
        if (tag === '弊社の発注書') { tagColor = '#ef4444'; bgColor = '#fef2f2'; }
        if (tag === '材料屋・加工業者の見積もり') { tagColor = '#f59e0b'; bgColor = '#fffbeb'; }
        if (tag === '弊社の見積もり') { tagColor = '#3b82f6'; bgColor = '#eff6ff'; }

        let innerHTML = `
            <div class="file-block" style="border: 1px solid #e2e8f0; border-left: 5px solid ${tagColor}; border-radius: 6px; padding: 12px 16px; margin: 12px 0; background: #fff; font-family: sans-serif; box-shadow: 0 1px 3px rgba(0,0,0,0.05); transition: all 0.2s;">
                <div style="display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 8px;">
                    <div style="display: flex; align-items: center; gap: 8px;">
                        <span style="font-size: 11px; font-weight: bold; color: ${tagColor}; background: ${bgColor}; padding: 3px 8px; border-radius: 4px; border: 1px solid ${tagColor}20;">${tag}</span>
                        <a href="${src}" target="_blank" style="text-decoration: none; color: #1e40af; font-weight: 600; font-size: 14px; hover:text-decoration:underline;">📄 ${name}</a>
                    </div>
                    <div>
                        <a href="${src}" download style="font-size: 12px; text-decoration: none; background: #f1f5f9; padding: 6px 12px; border-radius: 4px; color: #475569; font-weight: 500; border: 1px solid #cbd5e1; transition: background 0.2s;" onmouseover="this.style.background='#e2e8f0'" onmouseout="this.style.background='#f1f5f9'">ダウンロード</a>
                    </div>
                </div>
        `;

        if (isEdit) {
            innerHTML += `<div style="margin-top: 10px; padding-top: 10px; border-top: 1px dashed #e2e8f0; font-size: 13px; color:#475569; display: flex; gap: 16px; align-items: center;">`;
            if (tag === '顧客の発注書' || tag === '弊社の見積もり') {
                const price = this.getAttribute('price') || '0';
                const quantity = this.getAttribute('quantity') || '1';
                innerHTML += `
                    <div>
                        単価 (売上): 
                        <input type="number" value="${price}" style="width: 90px; border: 1px solid #cbd5e1; border-radius: 4px; padding: 4px 8px;"
                               oninput="this.getRootNode().host.setAttribute('price', this.value); updateHtmlPreview();"> 円
                    </div>
                    <div>
                        数量: 
                        <input type="number" value="${quantity}" style="width: 60px; border: 1px solid #cbd5e1; border-radius: 4px; padding: 4px 8px;"
                               oninput="this.getRootNode().host.setAttribute('quantity', this.value); updateHtmlPreview();">
                    </div>
                `;
            } else if (tag === '材料屋・加工業者の見積もり' || tag === '弊社の発注書') {
                const cost = this.getAttribute('cost') || '0';
                innerHTML += `
                    <div>
                        単価 (仕入原価): 
                        <input type="number" value="${cost}" style="width: 90px; border: 1px solid #cbd5e1; border-radius: 4px; padding: 4px 8px;"
                               oninput="this.getRootNode().host.setAttribute('cost', this.value); updateHtmlPreview();"> 円
                    </div>
                `;
            }
            innerHTML += `</div>`;
        }

        innerHTML += `</div>`;
        this.innerHTML = innerHTML;
    }
}
customElements.define('m-file', MFile);
