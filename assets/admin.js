// 管理コンソール（assets/admin.html）の本体スクリプト。
//
// 2026-08-06 に admin.html のインライン <script> から切り出した。外部ファイルにすることで
// Content-Security-Policy から script-src 'unsafe-inline' を外せる（docs/【考察】CSP強化.md §4）。
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
  if (!me) { window.location.href = '/login'; return; }
  document.getElementById('whoami').textContent = 'ログイン中: ' + me.username + (me.is_admin ? '（管理者）' : '');
  if (!me.is_admin) { setHidden(document.getElementById('denied'), false); return; }
  setHidden(document.getElementById('console'), false);
  loadUsers(); loadGroups(); loadRequiredPages(); loadAudit();
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
      `<td>
        <button class="sub" data-action="reset-pw">パスワード再設定</button>
        <button class="sub" data-action="toggle-disable">${u.disabled ? '有効化' : '無効化'}</button>
      </td>`;
    // 操作対象は属性で持たせる（onclick へ値を文字列展開しないので引用符の心配が要らない）。
    tr.querySelectorAll('button[data-action]').forEach(btn => {
      btn.dataset.username = u.username;
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

async function resetPw(username) {
  const pw = prompt(`${username} の新しいパスワード:`);
  if (!pw) return;
  const res = await api('POST', '/api/admin/users/password', { username, password: pw });
  alert(res.ok ? '再設定しました' : '失敗: ' + await res.text());
}

async function toggleDisable(username, disabled) {
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
    });
    tb.appendChild(tr);
  });
  const missing = (d.pages || []).filter(p => !p.exists).length;
  const btn = document.getElementById('reqpages-create');
  if (btn) {
    btn.disabled = missing === 0;
    btn.textContent = missing === 0 ? '足りない置き場はありません' : '足りない置き場を作る（' + missing + '件）';
  }
}

async function createRequiredPages() {
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

// ── データの初期化（2026-09-16）────────────────────────────────────────
//
// ユーザー:「管理ページにデータの初期化ボタンが欲しいくらいです」（開発中の
// 入れ直し用）。正本は internal/cms/reset_data.go。
//
// **歯止めは3つ**——admin・合言葉・確認ダイアログ。⚠ 合言葉は**サーバーでも
// 見ています**（画面だけで守ると口を直に叩けば素通りする）。
async function resetData() {
  const el = document.getElementById('reset-msg');
  const word = val('reset-word');
  if (!word) {
    el.style.color = '#dc2626';
    el.textContent = '合言葉を入力してください';
    return;
  }
  if (!confirm('ページを全部消します（控えは data/_reset-<日時>/ に残ります）。よろしいですか？')) return;
  el.style.color = '#64748b'; el.textContent = '初期化中...';
  const res = await api('POST', '/api/admin/reset', { confirm: word });
  const d = await res.json().catch(() => ({}));
  const s = d.summary || {};
  if (d.success) {
    el.style.color = '#16a34a';
    // **控えの場所を必ず出します**——「消えた」だけを伝えると、間違えて押した人が
    // 戻せると気づけません。
    el.textContent = s.pages + 'ページを控えへ移しました（' + (s.backup_dir || '') + '）。'
      + (s.kept_notice || '');
    document.getElementById('reset-word').value = '';
    loadRequiredPages(); loadAudit();
  } else {
    el.style.color = '#dc2626';
    el.textContent = (d.message || '初期化できませんでした')
      + (s.backup_dir ? '（控え: ' + s.backup_dir + '）' : '');
  }
}

async function rebuildDatabase() {
  if (!confirm('HTMLファイルからデータベースのインデックスを完全に再構築します。よろしいですか？')) return;
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

function val(id) { return document.getElementById(id).value.trim(); }
function esc(s) { return String(s == null ? '' : s).replace(/[&<>"]/g, c => ({'&':'&amp;','<':'&lt;','>':'&gt;','"':'&quot;'}[c])); }
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
  document.getElementById('reset-btn').addEventListener('click', resetData);
  document.getElementById('audit-reload').addEventListener('click', loadAudit);
  document.getElementById('reqpages-create').addEventListener('click', createRequiredPages);
  document.getElementById('reqpages-reload').addEventListener('click', loadRequiredPages);

  document.querySelector('#users-table tbody').addEventListener('click', e => {
    const btn = e.target.closest('button[data-action]');
    if (!btn) return;
    const username = btn.dataset.username;
    if (btn.dataset.action === 'reset-pw') resetPw(username);
    else if (btn.dataset.action === 'toggle-disable') toggleDisable(username, !btn.dataset.disabled);
  });
}

bindActions();
init();
