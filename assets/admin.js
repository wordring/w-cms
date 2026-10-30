// 管理コンソール（assets/admin.html）の本体スクリプト。
//
// 2026-08-06 に admin.html のインライン <script> から切り出した。外部ファイルにすることで
// Content-Security-Policy から script-src 'unsafe-inline' を外せる（docs/旧文書/本文サニタイズ設計.md §6。
// 当時の検討メモ【考察】CSP強化.md は、そこへ畳んで無くなった）。
// 併せて、行ごとに組み立てていた onclick="resetPw('...')" を data 属性＋イベント委譲へ置き換えた
// （文字列としてJSを組み立てないので、ユーザー名に `'` が入っても壊れない）。

const J = { 'Content-Type': 'application/json' };

async function api(method, path, body) {
  const opt = { method };
  if (body) { opt.headers = J; opt.body = JSON.stringify(body); }
  return fetch(path, opt);
}

// setHidden は表示/非表示を class で切り替える（admin.css の .is-hidden）。
function setHidden(el, hidden) {
  if (el) el.classList.toggle('is-hidden', !!hidden);
}

async function init() {
  let me;
  try { const r = await fetch('/api/me'); if (r.ok) me = await r.json(); } catch (e) {}
  // 未ログインでも /api/me は 200（authenticated:false）を返す——それも見る（2026-10-22 の見直し）。
  if (!me || me.authenticated === false) { window.location.href = '/login'; return; }
  window.wcmsAdminMe = me.username;
  document.getElementById('whoami').textContent = 'ログイン中: ' + me.username + (me.is_admin ? '（管理者）' : '');
  if (!me.is_admin) { setHidden(document.getElementById('denied'), false); return; }
  setHidden(document.getElementById('console'), false);
  loadUsers(); loadGroups(); loadRequiredPages(); loadTablesReport(); loadAudit(); loadPhotoShrink();
}

// ── 表の写し（data/tables.db）——「列の揃っていない表」の一覧（2026-09-25）──────
// 同じキャプションの表は列を統合するので、打ち間違えた見出しは黙って新しい列になる。
// ほかのページに無い列に ⚠、本文にはもう無い表・列に「跡」を付ける（数えるのはサーバー
// ——internal/cms/tables_report.go）。DOM は createElement＋textContent で組む（XSS の前科）。
let tablesReport = [];

async function loadTablesReport() {
  const el = document.getElementById('tables-msg');
  const res = await fetch('/api/admin/tables');
  if (!res.ok) { el.textContent = '読めませんでした: ' + res.status; return; }
  const d = await res.json().catch(() => ({}));
  tablesReport = d.tables || [];
  const suspects = tablesReport.filter(t => t.suspects > 0).length;
  el.textContent = '表 ' + tablesReport.length + ' 個' + (suspects ? '（⚠ 揃っていない表 ' + suspects + ' 個）' : '');
  renderTablesReport();
}

function renderTablesReport() {
  const tb = document.querySelector('#tables-table tbody');
  if (!tb) return;
  const only = document.getElementById('tables-only-suspect').checked;
  tb.textContent = '';
  tablesReport
    .filter(t => !only || t.suspects > 0 || t.leftover || (t.columns || []).some(c => c.leftover))
    .forEach(t => {
      const tr = document.createElement('tr');
      const td = (fill) => { const c = document.createElement('td'); fill(c); tr.appendChild(c); return c; };
      td(c => {
        c.textContent = t.name + (t.leftover ? '（跡）' : '');
        if (t.needs_quote) c.appendChild(quoteHint(t.name));
      });
      td(c => { c.textContent = t.leftover ? '—' : String(t.pages); });
      td(c => { c.textContent = String(t.rows); });
      td(c => {
        (t.columns || []).forEach((col, i) => {
          if (i) c.appendChild(document.createTextNode('、'));
          const s = document.createElement('span');
          if (col.leftover) {
            s.textContent = col.name + '（跡）';
            s.className = 'col-leftover';
          } else {
            s.textContent = (col.suspect ? '⚠ ' : '') + col.name + '（' + col.pages + '）';
            if (col.suspect) s.className = 'col-suspect';
          }
          if (col.needs_quote) s.title = 'SQL では "' + col.name.replace(/"/g, '""') + '" と引用符で囲みます';
          c.appendChild(s);
        });
      });
      tb.appendChild(tr);
    });
}

// quoteHint は「SQL では引用符が要る」の小さな注記です。
function quoteHint(name) {
  const s = document.createElement('div');
  s.className = 'hint';
  s.textContent = 'SQL では "' + name.replace(/"/g, '""') + '" と引用符で囲みます';
  return s;
}

async function loadUsers() {
  const res = await fetch('/api/admin/users');
  if (!res.ok) return;
  const users = await res.json() || [];
  const tb = document.querySelector('#users-table tbody');
  tb.innerHTML = '';
  users.forEach(u => {
    const tr = document.createElement('tr');
    tr.innerHTML =
      `<td>${esc(u.username)}</td><td>${u.is_admin ? '✓' : ''}</td><td>${esc(u.primary_group || '')}</td>` +
      `<td>${u.disabled ? '<span class="disabled-mark">無効</span>' : '有効'}</td>` +
      `<td>${esc((u.mail_addresses || []).join(', '))}</td>` +
      `<td>
        <button class="sub" data-action="reset-pw">パスワード再設定</button>
        <button class="sub" data-action="set-mail">アドレスを決める</button>
        <button class="sub" data-action="toggle-disable">${u.disabled ? '有効化' : '無効化'}</button>
      </td>`;
    // 操作対象は属性で持たせる（onclick へ値を文字列展開しないので引用符の心配が要らない）。
    tr.querySelectorAll('button[data-action]').forEach(btn => {
      btn.dataset.username = u.username;
      btn.dataset.addresses = (u.mail_addresses || []).join(', ');
      btn.dataset.disabled = u.disabled ? '1' : '';
    });
    tb.appendChild(tr);
  });
}

async function createUser() {
  const body = {
    username: val('nu-name'), password: val('nu-pass'),
    primary_group: val('nu-group'), is_admin: document.getElementById('nu-admin').checked
  };
  const res = await api('POST', '/api/admin/users', body);
  msg('nu-msg', res); if (res.ok) { document.getElementById('nu-name').value=''; document.getElementById('nu-pass').value=''; loadUsers(); loadGroups(); }
}

// setMailAddresses は、利用者が外の口座でサインインしてよいアドレスを決めます（2026-10-27・/api/admin/users/mail）。
// 利用者:「管理者が対応を決める」——ここに無い口座でサインインしても、鍵を保存しない。「,」で区切って幾つでも。空なら全部外す。
async function setMailAddresses(username, current) {
  const v = prompt(`${username} がサインインしてよいアドレス（「,」で区切る・空なら外す）:`, current);
  if (v === null) return;
  const addresses = v.split(/[,\s、]+/).map(s => s.trim()).filter(Boolean);
  const res = await api('POST', '/api/admin/users/mail', { username, addresses });
  if (!res.ok) { alert('決められませんでした: ' + await res.text()); return; }
  loadUsers(); loadAudit();
}

async function resetPw(username) {
  const pw = prompt(`${username} の新しいパスワード（12文字以上）:`);
  if (!pw) return;
  // 打ち間違いで、だれも知らないパスワードにならないように、もう一度打ってもらう（2026-10-22 の見直し）。
  if (prompt(`確かめのため、もう一度打ってください（${username}）:`) !== pw) { alert('2回の入力が違うので、変えていません'); return; }
  const res = await api('POST', '/api/admin/users/password', { username, password: pw });
  alert(res.ok ? '再設定しました' : '失敗: ' + await res.text());
}

async function toggleDisable(username, disabled) {
  // 無効にする前に確かめる。自分は無効にしない（2026-10-02 に a を無効にして、だれも入れなくなった・2026-10-22 の見直し）。
  if (disabled) {
    if (username === window.wcmsAdminMe) { alert('自分を無効にはできません（ほかの管理者で入って、無効にしてください）'); return; }
    if (!confirm(`${username} を無効にします（ログインできなくなります）。よろしいですか？`)) return;
  }
  const res = await api('POST', '/api/admin/users/disable', { username, disabled });
  if (res.ok) loadUsers(); else alert('失敗: ' + await res.text());
}

async function loadGroups() {
  const res = await fetch('/api/admin/groups');
  if (!res.ok) return;
  const groups = await res.json() || [];
  document.getElementById('groups-list').innerHTML =
    groups.length ? groups.map(g => `<code>${esc(g)}</code>`).join(' ') : '(グループなし)';
}

async function createGroup() {
  const res = await api('POST', '/api/admin/groups', { name: val('ng-name') });
  msg('ng-msg', res); if (res.ok) { document.getElementById('ng-name').value=''; loadGroups(); }
}

async function groupMember(action) {
  const res = await api('POST', '/api/admin/groups/members', { username: val('gm-user'), group: val('gm-group'), action });
  msg('gm-msg', res); if (res.ok) loadGroups();
}

async function loadAudit() {
  const res = await fetch('/api/admin/audit');
  if (!res.ok) return;
  const rows = await res.json() || [];
  const tb = document.querySelector('#audit-table tbody');
  tb.innerHTML = '';
  rows.forEach(e => {
    const tr = document.createElement('tr');
    tr.innerHTML = `<td>${esc(e.ts)}</td><td>${esc(e.username)}</td><td>${esc(e.action)}</td><td>${esc(e.target)}</td>`;
    tb.appendChild(tr);
  });
}

// ── 拡張が要る置き場（2026-09-16）──────────────────────────────────────
//
// ユーザー:「拡張プラグインが必要とするフォルダなどは、管理画面でボタンを押して
// 作成する仕組みにしてはどうでしょう？」——正本は internal/cms/required_pages.go。
//
// **在るものは押せる**（ページIDのリンク）、**無いものは理由が読める**。押す人が
// 「作ってよいか」を自分で判断できるように、`why` をそのまま出します。
async function loadRequiredPages() {
  const tb = document.querySelector('#reqpages-table tbody');
  if (!tb) return;
  const res = await fetch('/api/admin/pages');
  if (!res.ok) return;
  const d = await res.json().catch(() => ({}));
  tb.textContent = '';
  (d.pages || []).forEach(p => {
    const tr = document.createElement('tr');
    const td = (fill) => { const c = document.createElement('td'); fill(c); tr.appendChild(c); return c; };
    td(c => { c.textContent = p.title; });
    // コアの持ち物は拡張IDが空。「—」で埋めて列がずれないようにする。
    td(c => { c.textContent = p.extension || '—'; });
    td(c => { c.textContent = p.why || ''; });
    td(c => {
      if (p.exists) {
        // **開けるようにします**——「在る」と言われても、どこに在るか分からないと確かめられない。
        const a = document.createElement('a');
        a.href = '/' + p.page_id;
        a.textContent = '✓ ' + p.page_id;
        c.appendChild(a);
        // ⚠ **同じ題が2枚以上あると、使われるのは1枚だけ**（いちばん古いもの）。
        // 残りは**誰からも見えないまま**残り、そちらに書いた内容は行方不明になります。
        // 防げない（題は人が自由に付けられる）ので、**ここで知らせます**。
        if (p.duplicates && p.duplicates.length) {
          const warn = document.createElement('div');
          warn.className = 'dup-warn';
          warn.appendChild(document.createTextNode('⚠ 同じ題がほかに' + p.duplicates.length + '枚: '));
          p.duplicates.forEach((d, i) => {
            if (i) warn.appendChild(document.createTextNode('、'));
            const a2 = document.createElement('a');
            a2.href = '/' + d;
            a2.textContent = d;
            warn.appendChild(a2);
          });
          warn.appendChild(document.createTextNode(
            '（使われるのは ' + p.page_id + ' だけです。余りは中身を移してから消してください）'));
          c.appendChild(warn);
        }
      } else {
        c.textContent = '— 未作成';
      }
      // **置き場はテンプレートからだけ作られます**（2026-09-27）。どのテンプレートから作るか、
      // テンプレートが無いか（＝作れない・消えたあと作り直せない）を出します（2026-10-14 に管理画面の
      // 「データの初期化」は外した。下の画面の文は 2026-10-19 に「消したときに」へ直した）。
      const tp = document.createElement('div');
      tp.className = p.no_template ? 'dup-warn' : 'hint';
      if (p.template) {
        tp.appendChild(document.createTextNode('テンプレート: '));
        const a3 = document.createElement('a');
        a3.href = '/' + p.template;
        a3.textContent = p.template;
        tp.appendChild(a3);
      } else if (p.no_template) {
        tp.textContent = '⚠ テンプレートがありません——テンプレート置き場の下に、題が「' + p.title +
          '」のテンプレートを作ってください（無いと' + (p.exists ? '消したときに作り直せません' : '作れません') + '）';
      }
      if (tp.childNodes.length) c.appendChild(tp);
    });
    tb.appendChild(tr);
  });
  renderPageTemplates(d.templates || []);
  const missing = (d.pages || []).filter(p => !p.exists).length;
  const creatable = (d.pages || []).filter(p => !p.exists && !p.no_template).length;
  const btn = document.getElementById('reqpages-create');
  if (btn) {
    btn.disabled = creatable === 0;
    btn.textContent = missing === 0 ? '足りない置き場はありません'
      : creatable === 0 ? 'テンプレートが無いので作れません（' + missing + '件）'
      : '足りない置き場を作る（' + creatable + '件）';
  }
}

// renderPageTemplates は「機械が作るページのテンプレート」の表を描きます（2026-09-27）。
// 解析・取り込み・部材発注書のボタンは、この題のテンプレートを写してページを作ります——
// **無いと作れない**ので、どれが欠けているかをここで見せます。
function renderPageTemplates(list) {
  const tb = document.querySelector('#pagetmpl-table tbody');
  if (!tb) return;
  tb.textContent = '';
  list.forEach(p => {
    const tr = document.createElement('tr');
    const td = (fill) => { const c = document.createElement('td'); fill(c); tr.appendChild(c); };
    td(c => { c.textContent = p.title; });
    td(c => { c.textContent = p.extension || '—'; });
    td(c => { c.textContent = p.why || ''; });
    td(c => {
      if (p.page_id) {
        const a = document.createElement('a');
        a.href = '/' + p.page_id;
        a.textContent = '✓ ' + p.page_id;
        c.appendChild(a);
      } else {
        c.className = 'dup-warn';
        c.textContent = '⚠ ' + (p.problem || 'テンプレートがありません');
      }
    });
    tb.appendChild(tr);
  });
}

let reqPagesBusy = false, rebuildBusy = false; // 二度押しで2回走らせない（2026-10-22 の見直し）
async function createRequiredPages() {
  if (reqPagesBusy) return;
  if (!confirm('足りない置き場を、テンプレートから作ります。よろしいですか？')) return;
  reqPagesBusy = true;
  try { await createRequiredPagesNow(); } finally { reqPagesBusy = false; }
}
async function createRequiredPagesNow() {
  const el = document.getElementById('reqpages-msg');
  el.style.color = '#64748b'; el.textContent = '作成中...';
  const res = await api('POST', '/api/admin/pages');
  const d = await res.json().catch(() => ({}));
  const made = (d.created || []).map(p => p.title + '（' + p.page_id + '）').join('、');
  if (d.success) {
    el.style.color = '#16a34a';
    el.textContent = made ? '作りました: ' + made : '足りないものはありませんでした';
  } else {
    // **途中まで作ったものは残ります。** どこまで進んだかを添えないと、
    // もう一度押してよいのか分かりません（冪等なので押してよい）。
    el.style.color = '#dc2626';
    el.textContent = (d.message || '作れませんでした') + (made ? '（ここまで作成: ' + made + '）' : '');
  }
  loadRequiredPages();
}

async function rebuildDatabase() {
  if (rebuildBusy) return;
  if (!confirm('HTMLファイルからデータベースのインデックスを完全に再構築します。よろしいですか？')) return;
  rebuildBusy = true;
  try { await rebuildDatabaseNow(); } finally { rebuildBusy = false; }
}
async function rebuildDatabaseNow() {
  const el = document.getElementById('rebuild-msg');
  el.style.color = '#64748b'; el.textContent = '再構築中...';
  const res = await api('POST', '/api/rebuild-db');
  if (res.ok) {
    // 件数まで見せる。「完了しました」だけだと、途中で取りこぼしていても気づけない。
    const d = await res.json().catch(() => ({}));
    el.style.color = '#16a34a';
    el.textContent = d.pages != null
      ? `再構築が完了しました（${d.pages}ページ / ${d.duration_ms}ミリ秒）。`
      : '再構築が完了しました。';
    loadAudit();
  }
  else { el.style.color = '#dc2626'; el.textContent = '失敗: ' + await res.text(); }
}

// タグの値を置き換える（2026-10-01・/api/admin/tag-rename）。先に dry で数えて確かめてから書く。
async function renameTagValue() {
  const name = val('tr-name'), from = val('tr-from'), to = val('tr-to');
  const el = document.getElementById('tr-msg');
  const say = (color, text) => { el.style.color = color; el.textContent = text; };
  if (!name || !from || !to) { say('#dc2626', 'タグの名前・今の値・新しい値を書いてください'); return; }
  // skipped は飛ばしたページの説明（編集中・テンプレート・本文に組が無い）。
  await dryThenRename(say, '/api/admin/tag-rename', { name, from, to }, (r) => [
    (r.editing || []).length ? '編集中で飛ばした ' + r.editing.join('・') : '',
    (r.template || []).length ? 'テンプレートの中で飛ばした ' + r.template.join('・') : '',
    (r.missed || []).length ? '本文に組が無かった ' + r.missed.join('・') : '',
  ], `「${name}：${from}」のページはありません`, (n) => `${n} ページの「${name}：${from}」を「${name}：${to}」に置き換えます。よろしいですか？`);
}

// 表の列の名前を置き換える（2026-10-15・/api/admin/column-rename）。先に dry で数えて確かめてから書く。
async function renameColumn() {
  const caption = val('cr-caption'), from = val('cr-from'), to = val('cr-to');
  const el = document.getElementById('cr-msg');
  const say = (color, text) => { el.style.color = color; el.textContent = text; };
  if (!caption || !from || !to) { say('#dc2626', '表のキャプション・今の列の名前・新しい列の名前を書いてください'); return; }
  // skipped は飛ばしたページの説明（編集中・テンプレート・新しい名前の列が既にある）。
  await dryThenRename(say, '/api/admin/column-rename', { caption, from, to }, (r) => [
    (r.editing || []).length ? '編集中で飛ばした ' + r.editing.join('・') : '',
    (r.template || []).length ? 'テンプレートの中で飛ばした ' + r.template.join('・') + '（手で直してください）' : '',
    (r.conflict || []).length ? '新しい名前の列が既にあって飛ばした ' + r.conflict.join('・') : '',
  ], `「${caption}」の表に「${from}」の列があるページはありません`, (n) => `${n} ページの「${caption}」の列「${from}」を「${to}」に置き換えます。よろしいですか？`);
}

// dryThenRename は、置き換えの口（endpoint）を先に dry で叩いて数を見せ、確かめてから書きます（タグの値・表の列の名前）。
// skippedOf は飛ばしたページの説明の並び、none は1枚も当たらないときの文、ask(n) は確かめの文です。
// 2026-10-19 に寄せた（タグの値〔10-01〕と表の列の名前〔10-15〕が同じ流れを写していた）。
async function dryThenRename(say, endpoint, body, skippedOf, none, ask) {
  const post = async (dry) => {
    const res = await api('POST', endpoint, Object.assign({}, body, { dry }));
    const d = await res.json().catch(() => ({}));
    if (!res.ok || !d.success) throw new Error(d.message || ('HTTP ' + res.status));
    return d.result || {};
  };
  const skipped = (r) => skippedOf(r).filter(Boolean).join(' / ');
  try {
    say('#64748b', '数えています…');
    const pre = await post(true);
    const n = (pre.changed || []).length;
    if (n === 0) { say('#dc2626', none + (skipped(pre) ? '（' + skipped(pre) + '）' : '')); return; }
    if (!confirm(ask(n))) { say('#64748b', ''); return; }
    const r = await post(false);
    say('#16a34a', `${(r.changed || []).length} ページを置き換えました` + (skipped(r) ? '（' + skipped(r) + '）' : '') + '。');
    loadAudit();
  } catch (e) {
    say('#dc2626', '置き換えられませんでした: ' + e.message);
  }
}

// 写真をまとめて小さくする（2026-10-27・/api/admin/photo-shrink）。数と大きさを見せて確かめてから始める。
// 裏で動くので、動いているあいだは2秒ごとに進み具合を読み直す。
let photoShrinkTimer = 0;
const mb = (n) => (Number(n || 0) / 1e6).toFixed(1) + ' MB';

async function loadPhotoShrink() {
  const el = document.getElementById('ps-msg');
  const btn = document.getElementById('ps-go');
  const say = (color, text) => { el.style.color = color; el.textContent = text; };
  try {
    const res = await api('GET', '/api/admin/photo-shrink');
    const d = await res.json().catch(() => ({}));
    if (!res.ok || !d.success) { say('#dc2626', d.message || ('HTTP ' + res.status)); return null; }
    const j = d.job || {};
    clearTimeout(photoShrinkTimer);
    if (!d.enabled) { btn.disabled = true; say('#64748b', '設定 photo_shrink がありません（縮めません）'); return d; }
    if (j.running) {
      btn.disabled = true;
      say('#64748b', `縮めています… ${j.done} / ${j.total} 枚（${mb(j.before)} → ${mb(j.after)}）`);
      photoShrinkTimer = setTimeout(loadPhotoShrink, 2000);
      return d;
    }
    btn.disabled = !d.count;
    const last = j.finished ? `前回: ${j.shrunk} 枚を縮めた（${mb(j.before)} → ${mb(j.after)}）` +
      (j.skipped ? `・そのまま ${j.skipped} 枚` : '') + (j.failed ? `・失敗 ${j.failed} 枚（${(j.errors || []).join(' / ')}）` : '') + '。' : '';
    say(j.failed ? '#dc2626' : '#64748b', (d.count ? `縮められる写真: ${d.count} 枚・${mb(d.bytes)}（長い辺 ${d.long_side} px より長いもの）。` : '縮める写真はありません。') + last);
    return d;
  } catch (e) {
    say('#dc2626', '数えられませんでした: ' + e.message);
    return null;
  }
}

async function startPhotoShrink() {
  const d = await loadPhotoShrink();
  if (!d || !d.count) return;
  if (!confirm(`写真 ${d.count} 枚（${mb(d.bytes)}）を、長い辺 ${d.long_side} px に縮めます。\n元の大きさには w-cms からは戻せません（夜の控えと社外の控えには残ります）。よろしいですか？`)) return;
  const res = await api('POST', '/api/admin/photo-shrink', { action: 'start' });
  const r = await res.json().catch(() => ({}));
  if (!res.ok || !r.success) {
    const el = document.getElementById('ps-msg');
    el.style.color = '#dc2626'; el.textContent = r.message || ('始められませんでした（HTTP ' + res.status + '）');
    return;
  }
  loadPhotoShrink();
  loadAudit();
}

function val(id) { return document.getElementById(id).value.trim(); }
function esc(s) { return String(s == null ? '' : s).replace(/[&<>"']/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;',"'":'&#39;'}[c])); }
async function msg(id, res) {
  const el = document.getElementById(id);
  if (res.ok) { el.style.color = '#16a34a'; el.textContent = 'OK'; }
  else { el.style.color = '#dc2626'; el.textContent = '失敗: ' + await res.text(); }
}

// 画面の操作を配線する（インライン on*= を使わない）。
// ユーザー表の行は描画のたびに作り直されるので、tbody へのイベント委譲で受ける。
function bindActions() {
  document.getElementById('nu-create').addEventListener('click', createUser);
  document.getElementById('ng-create').addEventListener('click', createGroup);
  document.getElementById('gm-add').addEventListener('click', () => groupMember('add'));
  document.getElementById('gm-remove').addEventListener('click', () => groupMember('remove'));
  document.getElementById('rebuild-btn').addEventListener('click', rebuildDatabase);
  document.getElementById('tr-go').addEventListener('click', renameTagValue);
  document.getElementById('cr-go').addEventListener('click', renameColumn);
  document.getElementById('ps-go').addEventListener('click', startPhotoShrink);
  document.getElementById('audit-reload').addEventListener('click', loadAudit);
  document.getElementById('reqpages-create').addEventListener('click', createRequiredPages);
  document.getElementById('reqpages-reload').addEventListener('click', loadRequiredPages);
  document.getElementById('tables-reload').addEventListener('click', loadTablesReport);
  document.getElementById('tables-only-suspect').addEventListener('change', renderTablesReport);

  document.querySelector('#users-table tbody').addEventListener('click', e => {
    const btn = e.target.closest('button[data-action]');
    if (!btn) return;
    const username = btn.dataset.username;
    if (btn.dataset.action === 'reset-pw') resetPw(username);
    else if (btn.dataset.action === 'set-mail') setMailAddresses(username, btn.dataset.addresses || '');
    else if (btn.dataset.action === 'toggle-disable') toggleDisable(username, !btn.dataset.disabled);
  });
}

bindActions();
init();
