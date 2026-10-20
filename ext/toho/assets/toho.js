// 東邦の拡張の画面（2026-10-20 に assets/app.js の後ろから移した・docs/考察/【考察】画面の JS のコアと拡張.md）。
//
// 見積（見積表・見積計算書・見積書）・部材見積依頼（必要部材表・部材見積依頼書・返事）・案件残表（印刷・納品書・明細の書き換え）・
// 材料を探す・必要部材表から部材発注書・部材発注書を送る・加工製品の一覧・製造ページ の、鏡（サーバーが描いたクローム）の配線です。
//
// ⚠ **コアの中身には手を出しません**——使うのは app.js が渡す window.wcms だけ。サーバーは東邦の拡張が載っているときだけ
// この JS を読み込ませる（ext/toho/plugin.go の cms.RegisterScript）。
// ⚠ **鏡の配線は、サーバーが描いた属性だけで動くように書きます**・**配線は document へ委譲します**（コアの「鏡のボタンに共通の道具」と同じ決まり）。
// ⚠ **`innerHTML` へ文字列を入れません**（createElement＋textContent・保存型XSSの前科）。
(function () {
'use strict';
const w = window.wcms;
// コアが古い（読み込む順が崩れた・東邦の配線をまだ app.js が持っている版 1）なら何もしない——二重に配線すると、押すたびに2度送る。
if (!w || !(w.version >= 2)) return;
const { makeEl, makeButton, notify, postJSON, getJSON, sayIn, valueIn, delegateClick, printOnly } = w;

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
        const q = makeEl('p', 'proc-why-ng', '⚠ ' + s);
        box.appendChild(q);
    });
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
    const head = makeEl('p', '', '返事の表を読み替えています…');
    panel.appendChild(head);
    const r = await getJSON('/api/rfq-reply/draft?page_id=' + encodeURIComponent(replyId));
    if (!r.ok) { head.textContent = '⚠ ' + ((r.data && r.data.message) || '案を作れません'); return; }
    head.textContent = '仕入先 ' + (r.data.supplier || '（無し）') + '・回答日 ' + (r.data.date || '（無し）') +
        ' の部材見積依頼書ページを作ります。弊社品番（加工製品ページの番号）・種類・品物を直してから「作る」を押してください（入れない行は印を外す）:';
    const table = makeEl('table', 'rfq-reply-table rfq-reply-make-table');
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
                const hint = makeEl('div', 'rfq-mk-hint', row.product_title);
                c.appendChild(hint);
            }
        });
        table.appendChild(tr);
        lines.push({ use, inputs, row });
    });
    panel.appendChild(table);
    const go = makeButton('chip-btn rfq-reply-make-go', '部材見積依頼書ページを作る');
    const msg = makeEl('span', 'rfq-reply-make-say');
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
    const head = makeEl('p', '', '🤖 読む返事を選んでください（FAX の取り込み・写真・業者の見積書はこのページに置くと並びます）:');
    panel.appendChild(head);
    const list = makeEl('div', 'rfq-reply-sources');
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
    const go = makeButton('chip-btn rfq-reply-read', '🤖 読む');
    panel.appendChild(go);
    const out = makeEl('div', 'rfq-reply-result');
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
    const note = makeEl('p', 'proc-why-ng', '⚠ 🤖 は読み違えることがあります。返事と見比べて、直してから書き込んでください。');
    out.appendChild(note);
    if (d.summary) {
        const p = makeEl('p', '', '🤖 ' + d.summary);
        out.appendChild(p);
    }
    const byRow = {};
    (d.guesses || []).forEach((g) => { byRow[g.row] = g; });
    const table = makeEl('table', 'rfq-reply-table');
    const hr = document.createElement('tr');
    ['書く', '行', '何の値段か', 'いまの単価', '読んだ単価', '辞退', '備考'].forEach((h) => {
        const th = makeEl('th', '', h);
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
    const apply = makeButton('chip-btn rfq-reply-apply', '✍ 印の付いた行を書き込む');
    const msg = makeEl('span', 'rfq-reply-apply-say');
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
    const head = makeEl('p', '', '見積計算表を読んでいます…');
    panel.appendChild(head);
    const r = await getJSON('/api/estimate/rows?page_id=' + encodeURIComponent(pageId));
    if (!r.ok) { head.textContent = '⚠ ' + ((r.data && r.data.message) || '見積計算表を読めません'); return; }
    const tables = (r.data.tables || []).filter((t) => !t.why);
    if (!tables.length) { head.textContent = '⚠ 写せる見積計算表がありません（「工程」「価格」の列のある表に写せます）'; return; }
    // 写す先——いちばん新しい見積計算書があれば、そのページ（2026-10-17・サーバーが page_id で返す）。
    const target = (r.data && r.data.page_id) || pageId;
    head.textContent = '「' + what + '」 ' + price + '円（' + note + '）を' +
        (target !== pageId ? '見積計算書「' + (r.data.title || target) + '」の' : '') + '見積計算表へ写します:';
    const row = makeEl('div', 'rfq-quote-put-form');
    const label = (text, el) => { const l = document.createElement('label'); l.append(document.createTextNode(text), el); return l; };
    const tSel = makeEl('select', 'rfq-quote-put-table');
    tables.forEach((t) => {
        const o = document.createElement('option');
        o.value = String(t.index);
        o.textContent = (t.index + 1) + '枚目' + (t.lot ? '（ロット ' + t.lot + '）' : '');
        tSel.appendChild(o);
    });
    const rSel = makeEl('select', 'rfq-quote-put-row');
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
    const go = makeButton('chip-btn rfq-quote-put-go', '写す');
    const cancel = makeButton('chip-btn', 'やめる');
    cancel.addEventListener('click', () => { panel.textContent = ''; });
    const msg = makeEl('span', 'rfq-quote-put-say');
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
        const head = makeEl('p', '', '/' + data.folder + ' ' + (data.title || '') + ' の下の加工製品: ' + products.length + ' 件');
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
    const head = makeEl('p', '', cfg.head(clients[0] || '（発注元なし）'));
    panel.appendChild(head);
    const table = makeEl('table', 'materials-table backlog-dlv-table');
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
    const make = makeButton('');
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
            const p = makeEl('p', 'matsearch-empty', '見つかりませんでした（条件を緩めてみてください）');
            box.appendChild(p);
            return;
        }
        const table = makeEl('table', 'matsearch-table');
        const head = document.createElement('tr');
        ['材質', '形状', '寸法', '最新単価', '出所', 'ページ'].forEach((h) => {
            const th = makeEl('th', '', h);
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
                const mark = makeEl('span', 'matsearch-migrating', ' ⚠ 移行の確認前');
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
        const t = makeEl('table', 'materials-table backlog-dlv-table');
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
        const make = makeButton('mfg-dlv-make');
        make.dataset.mfg = btn.dataset.mfg || '';
        make.textContent = '🚚 納品書を作る';
        panel.appendChild(make);
        const msg = makeEl('span', 'mfg-dlv-msg');
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

// 返事ページの「部材見積依頼書で単価を写す」から来たら、🤖 返事を読む の欄を開く（モードを切り替えるたび——それまでは applyMode が名指しで呼んでいた）。
w.onChrome(openRFQReplyFromHash);
})();
