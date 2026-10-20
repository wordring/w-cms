// 連絡帳の拡張の画面——「未分類へ戻す」と、未登録の連絡先の「組織」「担当者」「登録」（2026-10-20 に assets/app.js から移した・docs/考察/【考察】画面の JS のコアと拡張.md）。
//
// ⚠ **コアの中身には手を出しません**——使うのは app.js が渡す window.wcms だけ。サーバーはこの拡張が載っているときだけ
// この JS を読み込ませる（置き場の決まりで拾う・internal/cms/ext_scripts.go——2026-10-20 夕までは cms.RegisterScript で名乗っていた）。
// ⚠ **`innerHTML` へ文字列を入れません**（createElement＋textContent・保存型XSSの前科）。
(function () {
'use strict';
const w = window.wcms;
// コアが古い（この部分をまだ app.js が持っている版 3 より前）なら何もしない——二重に配線しない。
if (!w || !(w.version >= 3)) return;
const { makeEl, makeButton, notify, jsonPost, postJSON, getJSON, readResult, failMessage, tagValue, hasExtension, reloadContent, loadChildNav } = w;

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


async function unfileContact(btn, addr, dt, dd) {
    btn.disabled = true;
    try {
        const res = await fetch('/api/contacts/unfile', jsonPost({ page_id: w.pageId(), address: addr }));
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
        const res = await fetch('/api/contacts/register', jsonPost(body));
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

// 「未分類へ戻す」は、2026-10-20 から `メールアドレス` のタグの飾りをサーバーが描く（ext/comm/contacts/unfile_decor.go）。
w.delegateClick([['.contact-unfile', (btn) => {
    const dd = btn.closest('dd');
    const dt = dd && dd.previousElementSibling;
    if (dd && dt) unfileContact(btn, btn.getAttribute('data-addr') || '', dt, dd);
}]]);
w.onChrome(wireContactRegister);
})();
