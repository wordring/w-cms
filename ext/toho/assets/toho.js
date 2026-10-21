// 東邦の拡張の画面（2026-10-20 に assets/app.js の後ろから移した・docs/考察/【考察】画面の JS のコアと拡張.md）。
//
// 見積（見積表・見積計算書・見積書）・部材見積依頼（必要部材表・部材見積依頼書・返事）・案件残表（印刷・納品書・明細の書き換え）・
// 材料を探す・必要部材表から部材発注書・部材発注書を送る・加工製品の一覧・製造ページ の、鏡（サーバーが描いたクローム）の配線です。
//
// ⚠ **コアの中身には手を出しません**——使うのは app.js が渡す window.wcms だけ。サーバーは東邦の拡張が載っているときだけ
// この JS を読み込ませる（置き場の決まりで拾う・internal/cms/ext_scripts.go——2026-10-20 夕までは cms.RegisterScript で名乗っていた）。
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
            // 製造ページの「見積計算」の欄から押したときは、mfg を送る——サーバーがタグ「製造」にその製造ページを書く（2026-10-21。それまでは子に作っていた）。
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
    // （受注残表の「🏭 選んだ行で製造開始」〔2026-10-19〕は 2026-10-21 に外した——表の先頭のチェックと「🏭 製造」で製造待ちへ・下の配線。）
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

// ── 案件残表から製造待ちへ・製造待ちから開始・取り消し（2026-10-21・ext/toho/mfg_wait.go）─────────────────
//
// 利用者:「チェックしてボタンを押すと「製造」フォルダへ入ります」「ここで開始した案件は製造のひとまとまりとなります。
// ただし、開始した案件はいつでも単体で取り消せるようにします。」「製造フォルダの表に入った案件もキャンセルして案件フォルダに戻すことが出来ます。」
// 押した表（1つの表が1つの客先・納期）の中で、先頭にチェックした行だけを送る。サーバーが行番号と品番で照合する。
function checkedRowsIn(btn, sel) {
    const sheet = btn.closest('.backlog-sheet');
    return Array.from((sheet || document).querySelectorAll(sel + ':checked')).map((c) => ({
        order: c.dataset.order || '', row: Number(c.dataset.row), item: c.dataset.item || '',
    }));
}

async function sendCheckedRows(btn, cfg) {
    const say = btn.parentElement && btn.parentElement.querySelector(cfg.say);
    const items = checkedRowsIn(btn, cfg.pick);
    if (!items.length) { sayIn(say, cfg.none, 'warn'); return; }
    if (cfg.confirm && !confirm(cfg.confirm(items.length))) return;
    btn.disabled = true;
    sayIn(say, cfg.busy);
    const r = await postJSON(cfg.url, { items }).catch((e) => ({ ok: false, data: { message: String(e) } }));
    btn.disabled = false;
    if (!r.ok) { sayIn(say, '⚠ ' + ((r.data && r.data.message) || cfg.fail)); return; }
    cfg.done(r.data || {});
}

delegateClick([
    // 案件残表の表の下の「🏭 製造」——先頭にチェックした行を、「製造」の製造待ちへ入れる。
    ['.backlog-wait-go', (btn) => sendCheckedRows(btn, {
        pick: '.backlog-wait-pick', say: '.backlog-wait-say', url: '/api/mfg/wait',
        none: 'この表の先頭のチェックで、製造へ入れる行を選んでください。',
        busy: '製造へ入れています…', fail: '製造へ入れられませんでした',
        done: () => location.reload(),
    })],
    // 「製造」の製造待ちの表の「▶ 選んだ行で開始」——製造ページ（1社）を作って開く。
    ['.mfgwait-start-go', (btn) => sendCheckedRows(btn, {
        pick: '.mfgwait-pick', say: '.mfgwait-say', url: '/api/mfg/start',
        none: 'この表の先頭のチェックで、開始する行を選んでください。',
        busy: '製造ページを作っています…', fail: '開始できませんでした',
        done: (d) => { if (d.warning) alert(d.warning); location.href = d.url || ('/' + d.page_id); },
    })],
    // 「製造」の製造待ちの表の「↩ 選んだ行を案件へ戻す」。
    ['.mfgwait-back-go', (btn) => sendCheckedRows(btn, {
        pick: '.mfgwait-pick', say: '.mfgwait-say', url: '/api/mfg/unwait',
        none: 'この表の先頭のチェックで、案件へ戻す行を選んでください。',
        confirm: (n) => '選んだ ' + n + ' 行を製造待ちから外して、案件残表へ戻します。よろしいですか？',
        busy: '戻しています…', fail: '戻せませんでした',
        done: () => location.reload(),
    })],
    // 製造ページの製造明細表の「↩ 製造待ちへ」——その行だけを取り消す（作った書類は残る）。
    ['.mfg-back-go', async (btn) => {
        if (!confirm('この行を製造明細から外して、「製造」の製造待ちへ戻します（この製造で作った書類はそのまま残ります）。よろしいですか？')) return;
        btn.disabled = true;
        const r = await postJSON('/api/mfg/back', {
            page_id: btn.dataset.mfg || '', row: Number(btn.dataset.row), item: btn.dataset.item || '',
        }).catch((e) => ({ ok: false, data: { message: String(e) } }));
        btn.disabled = false;
        if (!r.ok) { alert('⚠ ' + ((r.data && r.data.message) || '戻せませんでした')); return; }
        location.reload();
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

    // （必要部材表の「発注部材表を作る」「不要にする」と、手配不要表の「↩ 戻す」は、2026-10-21 に表ごと外した——
    //  利用者:「臨時部材表と手配不要表もなくし、必要部材表もなくしてください。」「一度無くして、適切な形で作り直します」）

    delegateClick([
        ['[data-draft-go]', create],
        ['.draft-row-back', removeDraftRow],
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
// ⚠ **メールを書く欄は部品です**（2026-09-30・ext/comm/mail/assets/mail-compose.js）——ここに残るのは行ごとの印・
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
        // メールで送る欄は部品です（2026-09-30・ext/comm/mail/assets/mail-compose.js——「PDFを作る → 送る → 発注済みにする」は
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
// ——納品書は記録にでき、明細の「製造」の列にこの製造ページが入る（2026-10-21）。
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
// （記録にでき、明細の「製造」の列にこの製造ページが入る・2026-10-21）。
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

// 製造ページの「📄 この製造の見積書を作る」（2026-10-19・ext/toho/quote_done.go）——見積の依頼の行から見積書を作って開く（置き場は記録・2026-10-21）。
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

// ── 版 4 から東邦の JS が持つ部分（2026-10-20 に app.js の前半から移した）——加工製品ページの整理の欄・🤖 解析と解析済みの印・
// あとから改定にする。ファイルの「⋯」には onFileMenu の口で足す（🤖 解析のボタンは記録の箱の中・2026-10-20 夕から）。
if (w.version >= 4) {
const { askConfirm, askChoice, jsonPost, hasExtension, reloadContent, loadChildNav } = w;
// BY_HAND_REPLY_HINT は 🤖 解析が「どれでもない」と答えたときに添える、手で作る道です（2026-10-19・ext/toho/rfq_reply_page.go）。
// 利用者:「このようなエクセル形式でも解析できるようにならないでしょうか？ならない場合、手作業で見積もり回答書を作れるとありがたいです」
const BY_HAND_REPLY_HINT = '業者の値段の表なら、メールの「作るページ」の「部材見積依頼の返事ページ（手で書く）」で手で作れます。';

// 「📁 整理（N件）」を押したとき（2026-10-20 夕から、ボタンと件数はサーバーが描く——ext/toho/record_sections.go。それまでは
// ページを開くたびにこの JS が候補を全部引いて、ボタンを組んでいた）。押すと候補を引いて、行き先の表を出し入れします。
// 書ける形でも押せる（2026-10-12——業務のボタンは書ける形でも押せる・【考察】編集モードの再考 §6 問い5）。
// **整理が意味を持つのは通信記録だけ**——解析で図面ページが生まれた元のページ（2026-09-03 ユーザー指摘。それまでは
// 閲覧モードの全ページに出ていた）。だからボタンは記録の箱の中にだけ出る。
w.delegateClick([['.mail-chrome .filing-open', async (btn) => {
    const existing = document.querySelector('.filing-panel');
    if (existing) { existing.remove(); return; }
    btn.disabled = true;
    try {
        const res = await fetch('/api/filing-proposal?page_id=' + encodeURIComponent(btn.getAttribute('data-page') || w.pageId()));
        const d = await res.json();
        if (!d.success) { notify('整理の候補を読めません: ' + (d.message || res.status), { type: 'warn' }); return; }
        btn.insertAdjacentElement('afterend', buildFilingPanel(
            d.rows || [],
            // 案件ページ（2026-10-19 まで受注ページ）も同じ画面で片付けます（行き先は発注日で決まる——記録／年／月／依頼——ので欄は無い）。
            d.orders || [],
            // **区分の選択肢はサーバーが持ちます**（設定の語彙「区分」・2026-09-29 に段から替えた）。
            // 画面に書き写すと、語を足した日に片方だけ古くなります。
            d.kinds || [],
            d.partners || [],  // 既にある取引先の名前——顧客名の入力候補に出します。
            d.machines || {},  // 既にある装置名称（顧客ごと）——装置名称の入力候補に出します。
            // 記録の添付（2026-10-08・ext/toho/filing_files.go——図面以外のファイルも、行き先を探して足す）。
            d.files || []));
    } catch (e) {
        notify('整理の候補を読めません: ' + e.message, { type: 'warn' });
    } finally {
        btn.disabled = false;
    }
}]]);

// buildFilingPanel は行き先の表を組みます。**全部の欄が編集できます**
// ——試作の「【試作】…」は機械には決められないので、ここで人が打ちます（2026-09-29 からは試作も区分のタグで、
// 題に【試作】を付けなくてよい。区分は加工製品ページで付ける——下の kindBoxes）。
function buildFilingPanel(rows, orders, kinds, partners, machines, files) {
    const panel = makeEl('div', 'vocab-chrome filing-chrome filing-panel');
    panel.setAttribute('contenteditable', 'false');

    // ⚠ **言葉は短く・1枚ずつ縦に**（2026-09-30 利用者:「入力欄が見えなくなるくらい、横幅が狭くなります。
    //    横にたくさんの項目がありすぎるのと、チェックボックスの説明が長すぎます」）——それまでは6列の表で、
    //    欄が 50px 台まで押しつぶされ、選択肢の説明が1文字ずつ縦に折れていました。
    const head = makeEl('p', 'filing-head');
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
    const list = makeEl('div', 'filing-cards');

    const inputs = [];
    rows.forEach((row, rowIndex) => buildFilingCard(row, rowIndex, { panel, list, inputs, partners, machines, listID }));
    if (rows.length) panel.appendChild(list);

    // **受注ページは別の表**です。直せるのは発注元だけ（行き先は発注日で決まる）
    // なので、図面と同じ列に並べると空欄ばかりの行になります。
    const pickedOrders = [];
    if (orders.length) {
        const oHead = makeEl('p', 'filing-head', '案件ページは「記録」の年月へ収めます（外すものは印を外す）。');
        panel.appendChild(oHead);

        const oTable = makeEl('table', 'filing-table filing-orders');
        const oTrh = document.createElement('tr');
        ['', '発注書番号', '発注元', '発注日', '行き先'].forEach(t => {
            const th = makeEl('th', '', t);
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
            const tdNo = makeEl('td', '', o.order_no || '（番号なし）');
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
                const td = makeEl('td', '', v);
                tr.appendChild(td);
            });
            pickedOrders.push({ page_id: o.page_id, client: client, box: box });
            oTable.appendChild(tr);
        });
        panel.appendChild(oTable);
    }

    const run = makeButton('filing-run', '実行');
    run.addEventListener('click', () => runFiling(inputs, pickedOrders, run, panel));
    // 「ほかのファイル」の段（2026-10-08）——それぞれ「＋ …」で動くので、実行ボタンは図面か受注があるときだけ。
    if ((files || []).length) panel.appendChild(buildFilingFiles(files));
    if (rows.length || orders.length) panel.appendChild(run);
    return panel;
}

// buildFilingCard は整理の表の、図面1枚ぶんのカードを組んで list に足し、実行のときに読む欄を inputs へ積みます
// （2026-10-20 に buildFilingPanel から分けた）。ctx は表全体で共有するもの（panel・list・inputs・取引先と装置の候補）。
function buildFilingCard(row, rowIndex, ctx) {
    const { panel, list, inputs, partners, machines, listID } = ctx;
    const card = makeEl('div', 'filing-card');
    card.dataset.pageId = row.page_id;
    const no = makeEl('p', 'filing-no', '📄 ' + (row.drawing_no || '（図面番号なし）'));
    card.appendChild(no);
    const grid = makeEl('div', 'filing-grid');
    card.appendChild(grid);

    const fields = {};
    // 顧客名・装置名称・図面名称は自由入力（1行に1つ・見出しは左）、区分は印。
    const addText = (key, labelText, value) => {
        const lb = makeEl('label', 'filing-field');
        const span = makeEl('span', '', labelText);
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
    const tdRules = makeEl('div', 'filing-field filing-kinds filing-rules');
    const rulesHead = makeEl('span', '', '品番・品名');
    rulesHead.title = '新しく置く加工製品の題の下のタグ（空いているときだけ書く・弊社品番はいつも書く）';
    tdRules.appendChild(rulesHead);
    const rulesWrap = makeEl('div', 'filing-kinds-boxes');
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

    const tdConfirm = makeEl('div', 'filing-card-foot');

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
    const choiceWrap = makeEl('div', 'filing-choice');
    const choiceNote = makeEl('p', 'filing-choice-note');
    // 同じ図面が既にあるときの知らせ（2026-09-30 利用者:「同じものがあるということを提示して欲しいのと、
    // 同じものがあるので取り込まない選択肢が欲しいです」）——サーバーが候補に付けた印（同じ図面番号・同じファイル）から。
    const dupWrap = makeEl('div', 'filing-dups');
    dupWrap.hidden = true;
    const candWrap = makeEl('div', 'filing-candidates');
    // 同じ題の加工製品が何枚もあるときの、図面追加・図面改定の行き先（2026-10-01）。
    const sameWrap = makeEl('div', 'filing-candidates filing-same');
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
            const label = makeEl('label', 'filing-merge-opt');
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
    const searchWrap = makeEl('div', 'filing-search');
    searchWrap.hidden = true;
    const searchLabel = makeEl('label', 'filing-search-label', '行き先を探す: ');
    const searchInput = document.createElement('input');
    searchInput.type = 'search';
    searchInput.className = 'filing-search-input';
    searchInput.placeholder = '品番・品名・図面番号・題・ページ番号';
    searchLabel.appendChild(searchInput);
    const searchOut = makeEl('div', 'filing-candidates filing-search-out');
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
            const line = makeEl('p', 'filing-dup');
            const a = document.createElement('a');
            a.href = '/' + c.page_id;
            a.target = '_blank';
            a.textContent = (c.machine ? c.machine + '／' : '') + c.title;
            const why = c.same_no && c.same_file ? '同じ図面番号・同じファイルです'
                : c.same_no ? '同じ図面番号ですが、ファイルの中身は違います（図番を変えない改定か、PDFを作り直しただけかもしれません）'
                : '同じファイルから作られています（1つのPDFに別の図面が入っていることもあります）';
            line.append('⚠ 同じ図面が既にあります: ', a, '。' + why);
            if (c.same_no && !c.same_file) {
                const b = makeButton('chip-btn filing-compare', '🤖 中身を比べる');
                b.title = '2つのPDFを Gemini に見比べさせます（数十秒かかります）';
                const out = makeEl('span', 'filing-compare-out');
                b.addEventListener('click', async () => {
                    b.disabled = true;
                    out.textContent = ' 見比べています…';
                    try {
                        const res = await fetch('/api/compare-drawings', jsonPost({ page_id: row.page_id, with: c.page_id }));
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
            const head = makeEl('span', 'filing-cand-head', '図面追加・図面改定の行き先:');
            sameWrap.appendChild(head);
            target.same.forEach(s => {
                const b = makeButton('');
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
            const head = makeEl('span', 'filing-cand-head', '既にある加工製品:');
            candWrap.appendChild(head);
            cands.forEach(c => {
                const b = makeButton('chip-btn filing-cand', (c.machine ? c.machine + '／' : '') + c.title);
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
        const head = makeEl('span', 'filing-cand-head', hits.length ? '見つかった加工製品（押すと行き先）:' : '見つかりません');
        searchOut.appendChild(head);
        hits.forEach(h => {
            const b = makeButton('');
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
    const confirm = makeEl('label', 'filing-confirm');
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
}

// buildFilingFiles は整理の「📎 ほかのファイル」の段です（2026-10-08・ext/toho/filing_files.go）。
//
// 利用者:「図面以外のファイルでも、ここで相手を検索して追加できるとありがたいですね」——仕様書・3Dデータなど、🤖 解析で加工製品
// ページにならない添付。添付ごとに「行き先を探す」（加工製品ページと装置のページ——`folders=1`）→「＋ …」で、そのページの末尾に
// ファイル表示を足す（添付はこのメールに置いたまま・指すだけ）。どのページがもう表示しているかは、欄を開いたときに1回だけ探す（重い）。
function buildFilingFiles(files) {
    const wrap = makeEl('div', 'filing-files');
    const head = makeEl('p', 'filing-head', '📎 ほかのファイル——行き先を探して押すと、そのページにファイル表示を足します（添付はこのメールに置いたまま）。');
    wrap.appendChild(head);
    const shownBoxes = {};
    files.forEach(f => {
        const card = makeEl('div', 'filing-card filing-file');
        card.dataset.attach = f.id;
        const name = makeEl('p', 'filing-no', '📎 ' + f.name);
        const shown = makeEl('p', 'filing-file-shown', '表示しているページ: 調べています…');
        shownBoxes[f.ref] = { box: shown, list: [] };
        const label = makeEl('label', 'filing-search-label', '行き先を探す: ');
        const input = document.createElement('input');
        input.type = 'search';
        input.className = 'filing-search-input';
        input.placeholder = '品番・品名・図面番号・題・装置名・ページ番号';
        label.appendChild(input);
        const out = makeEl('div', 'filing-candidates filing-search-out');
        const note = makeEl('p', 'filing-file-note');
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
                const p = makeEl('p', 'filing-search-none', '見つかりません');
                out.appendChild(p);
            }
            hits.forEach(h => {
                const b = makeButton('chip-btn filing-file-add');
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
            const d = await (await fetch('/api/filing-files-shown?page_id=' + encodeURIComponent(w.pageId()))).json();
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
        r = await postJSON('/api/filing-attach', { page_id: w.pageId(), attach: f.id, target: h.page_id });
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
        const res = await fetch('/api/file-drawings', jsonPost({
                rows: payload,
                // ⚠ **もとはIDの配列でした**（2026-09-20 に変えた）——発注元が
                // 直せるようになったので値を運びます（サーバーは `orderRequest`）。
                orders: (pickedOrders || []).filter(o => o.box.checked)
                    .map(o => ({ page_id: o.page_id, client: o.client.value })),
            }));
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

// analyzeOne は添付1つを解析します（「🤖 解析」と「まとめて解析」が共有する・2026-09-30 に切り出した）。
// 返すのは {kind: 'drawing'|'order'|'none', made: [{page_id, title}], matchedDxf} か {error}。
async function analyzeOne(pageId, file) {
    try {
        const res = await fetch('/api/analyze-attachment', jsonPost({ page_id: pageId, file: file }));
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

// runZipAnalyze は ZIP の「🤖 中のPDFをまとめて解析」の働きです（2026-09-30 利用者:「添付ファイルのPDFに
// ついて、ZIPの場合まとめて解析するオプションが欲しいです」）。中のPDFを**1つずつ順に**「🤖 解析」と同じ口で
// 解析し、最後に1つの知らせにまとめる。⚠ **解析済みのものは飛ばす**（押し直しで同じページを二重に作らない——
// 読み違いの解析し直しは1件ずつの「🤖 再解析」で）。途中でページを離れると止まるが、押し直せば残りから続く。
// ボタン（解析済みを除いた件数つき）はサーバーが描く（record_sections.go・2026-10-20 夕から）。押すたびに /api/analyzed を
// 引き直す。btn は無くてもよい（「⋯」のメニューから——進み具合は知らせに出す）。
async function runZipAnalyze(pageId, zipID, btn) {
    let pdfs = [], analyzed = {};
    try {
        const d = await (await fetch('/api/analyzed?page_id=' + encodeURIComponent(pageId))).json();
        analyzed = (d && d.analyzed) || {};
        pdfs = ((d && d.zip_pdfs) || {})[zipID] || [];
    } catch (e) {
        notify('まとめて解析できません: ' + e.message, { type: 'warn' });
        return;
    }
    const todo = pdfs.filter(p => !analyzed[p.id]);
    if (!todo.length) { notify(pdfs.length ? '中のPDFは解析済みです。' : 'この ZIP の中に PDF はありません。', { duration: 6000 }); return; }
    const skipped = pdfs.length - todo.length;
    const label = btn ? btn.textContent : '';
    const say = (t) => { if (btn) btn.textContent = t; else notify(t, { type: 'info', duration: 0, id: 'analyze-pdf' }); };
    if (!confirm('中のPDF ' + todo.length + '件を順に解析します（Gemini を ' + todo.length +
        '回呼びます・1件に数十秒）。' + (skipped ? '解析済みの ' + skipped + '件は飛ばします。' : '') +
        '途中でページを離れると止まります（押し直せば残りから）。')) return;
    if (btn) btn.disabled = true;
    let drawings = 0, orders = 0, replies = 0;
    const none = [], failed = [];
    for (let i = 0; i < todo.length; i++) {
        say('🤖 解析中… ' + (i + 1) + '/' + todo.length);
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
    if (btn) { btn.disabled = false; btn.textContent = label; }
    if (drawings || orders || replies) {
        await reloadContent(); // 本文の鏡と、解析済みの印（applyMode 経由）
        loadChildNav();        // 左レールの子ページ一覧
    }
}

// runAnalyze は添付1つ（PDF・表計算）を 🤖 解析します（記録の箱の「🤖 解析」「🤖 再解析」と「⋯」のメニューが使う。
// ボタンはサーバーが描く——record_sections.go・2026-10-20 夕から。btn は無くてもよい）。
// ⚠ ZIP の中の PDF には出しません（2026-09-17）——取り込みが ZIP を展開して中身を
// 1つずつ添付にするので、解析はページ直下の PDF だけを見ます（`entry` 引数は廃止）。
// 押すと /api/analyze-attachment が Gemini で判定し、発注書なら受注ページを
// このページの子として作る。結果は通知で知らせる（本文は変えない）。
async function runAnalyze(pageId, file, btn) {
    const label = btn ? btn.textContent : '';
    if (btn) { btn.disabled = true; btn.textContent = '🤖 解析中…'; }
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
    if (btn) { btn.disabled = false; btn.textContent = label; }

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
    // 書きかけを失う心配も要りません: 記録の箱の「🤖 解析」も「⋯」のメニューの解析（runAnalyzeFile）も
    // 書ける形で押せますが、書ける形の `reloadContent()` は書きかけを先に保存して、変わったブロックだけ
    // 受け取ります（2026-10-12 から）。
    if (born) {
        await reloadContent(); // 本文の鏡と、解析済みの印（applyMode 経由）
        loadChildNav();        // 左レールの子ページ一覧
    }
}

// analyzableHere は、そのファイルを 🤖 解析してよい所か——通信記録（記録の箱がある）で、ファイルがそのページの添付のとき。
// （2026-10-20 夕までは `チャネル` のタグを読んでいた——いまは記録の機能はどれも記録の箱の印で決まる。）
function analyzableHere(pageOfFile) {
    return hasExtension('toho') && !!document.querySelector('#w-editor-content .mail-chrome') && pageOfFile === w.pageId();
}

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
    const r = await postJSON('/api/drawing/supersede', { page_id: w.pageId(), new: pick(mine), old: pick(old) });
    if (!r.ok) {
        notify((r.data && r.data.message) || ('旧版へ移せません（' + r.status + '）'), { type: 'warn', duration: 0 });
        return;
    }
    notify('前の図面を「' + (r.data.title || '旧版') + '」へ移しました。', { type: 'success' });
    await reloadContent();
    loadChildNav();
}

// runAnalyzeFile は添付1つを 🤖 解析します（記録の箱の「🤖 解析」と同じ口と知らせ——runAnalyze）。
function runAnalyzeFile(pageId, file) {
    notify('🤖 解析しています…', { type: 'info', duration: 0, id: 'analyze-pdf' });
    runAnalyze(pageId, file, null);
}

// 記録の箱の「🤖 解析」「🤖 再解析」「まとめて解析」の配線（ボタンと解析済みの印はサーバーが描く・ext/toho/record_sections.go——
// 2026-10-20 夕から。それまではこの JS が /api/analyzed を引いて、置き場にボタンを組んでいた）。
w.delegateClick([['.mail-chrome .attach-analyze', (btn) => {
    const pid = btn.getAttribute('data-page');
    const zip = btn.getAttribute('data-zip');
    if (zip) runZipAnalyze(pid, zip, btn);
    else runAnalyze(pid, btn.getAttribute('data-file'), btn);
}]]);

// ファイルの「⋯」: メールのページの添付に「🤖 解析」、図面が2つ以上並んだページの図面に「📐 この図面で改定する」。
w.onFileMenu((info, items, f) => {
    if (analyzableHere(f.pageId)) {
        if (info.kind === 'pdf') items.splice(2, 0, { icon: '🤖', label: '解析（発注書・図面を読む）', run: () => runAnalyzeFile(f.pageId, f.file) });
        if (info.kind === 'xlsx') items.splice(2, 0, { icon: '🤖', label: '解析（注文リストを読む）', run: () => runAnalyzeFile(f.pageId, f.file) });
        if (info.kind === 'zip') items.push({ icon: '🤖', label: '中のPDFをまとめて解析', run: () => runZipAnalyze(f.pageId, f.attachId) });
    }
    if (canSupersedeFrom(info.host)) {
        items.push({ icon: '📐', label: 'この図面で改定する（前の図面を旧版へ）',
            title: 'この図面を最新の図面にして、選んだ前の図面を旧版の子ページへ移します', run: () => supersedeDrawingFrom(info.host) });
    }
});

}
})();
