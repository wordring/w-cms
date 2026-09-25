// 検索画面（assets/tables.html）——「表を探す」（2026-09-25・docs/【考察】DBの日本語化.md §7 の3段目）。
//
// 口はサーバーの2つだけ: GET /api/tables（探せる表と列）・POST /api/tables/query（探す）。
// 自由な SQL は送らない。表・列・条件の種類は選ぶだけで、値だけを文字で送る
// （サーバーが DB にある名前と照らし、値は `?` で渡す——tables_query.go）。
//
// ⚠ DOM は createElement＋textContent で組む（innerHTML に文字を入れない・保存型XSSの前科）。
// ⚠ インラインの script／style／on*= は書かない（CSP strict）。

let tables = [];

// 条件の種類（サーバーの tableQueryOps と同じ4つ）。
const OPS = [
  ['eq', '等しい'],
  ['contains', '含む'],
  ['gte', '以上'],
  ['lte', '以下'],
];

function el(tag, attrs, text) {
  const e = document.createElement(tag);
  if (attrs) for (const [k, v] of Object.entries(attrs)) e.setAttribute(k, v);
  if (text !== undefined) e.textContent = text;
  return e;
}

function currentTable() {
  const name = document.getElementById('tq-table').value;
  return tables.find(t => t.name === name);
}

async function init() {
  let me;
  try { const r = await fetch('/api/me'); if (r.ok) me = await r.json(); } catch (e) {}
  if (!me || !me.authenticated) { window.location.href = '/login?next=' + encodeURIComponent('/assets/tables.html'); return; }
  document.getElementById('whoami').textContent = 'ログイン中: ' + me.username;

  const res = await fetch('/api/tables');
  if (!res.ok) { say('表の一覧を読めませんでした: ' + res.status, true); return; }
  const d = await res.json().catch(() => ({}));
  tables = d.tables || [];
  const sel = document.getElementById('tq-table');
  sel.textContent = '';
  if (!tables.length) {
    say('探せる表がありません（キャプションのある表を書くと、ここに出ます）');
    return;
  }
  tables.forEach(t => sel.appendChild(el('option', { value: t.name }, t.name)));
  // 前に選んだ表を覚えておく（この人の画面だけの便利機能・消えても困らない）。
  try {
    const last = localStorage.getItem('w-tables-last');
    if (last && tables.some(t => t.name === last)) sel.value = last;
  } catch (e) {}
  onTableChange();
}

function onTableChange() {
  const t = currentTable();
  if (!t) return;
  try { localStorage.setItem('w-tables-last', t.name); } catch (e) {}
  document.getElementById('tq-rows').textContent = '（読めるページに ' + t.rows + ' 行）';
  const box = document.getElementById('tq-columns');
  box.textContent = '';
  t.columns.forEach(c => {
    const lab = el('label');
    const cb = el('input', { type: 'checkbox', value: c });
    cb.checked = true;
    lab.appendChild(cb);
    lab.appendChild(document.createTextNode(' ' + c));
    box.appendChild(lab);
  });
  document.getElementById('tq-where').textContent = '';
  addCondition();
  clearResult();
}

function addCondition() {
  const t = currentTable();
  if (!t) return;
  const row = el('div', { class: 'where-row' });
  const col = el('select', { class: 'w-col' });
  t.columns.forEach(c => col.appendChild(el('option', { value: c }, c)));
  const op = el('select', { class: 'w-op' });
  OPS.forEach(([v, label]) => op.appendChild(el('option', { value: v }, label)));
  const val = el('input', { class: 'w-val', placeholder: '値（空なら使わない）' });
  val.addEventListener('keydown', e => { if (e.key === 'Enter') run(); });
  const rm = el('button', { class: 'remove', title: 'この条件を外す' }, '×');
  rm.addEventListener('click', () => row.remove());
  [col, op, val, rm].forEach(x => row.appendChild(x));
  document.getElementById('tq-where').appendChild(row);
}

function say(text, error) {
  const m = document.getElementById('tq-msg');
  m.textContent = text || '';
  m.classList.toggle('error', !!error);
}

function clearResult() {
  document.querySelector('#tq-result thead').textContent = '';
  document.querySelector('#tq-result tbody').textContent = '';
  document.getElementById('tq-summary').textContent = '';
  document.getElementById('tq-sql-box').classList.add('is-hidden');
}

async function run() {
  const t = currentTable();
  if (!t) return;
  const columns = [...document.querySelectorAll('#tq-columns input:checked')].map(i => i.value);
  const where = [...document.querySelectorAll('#tq-where .where-row')].map(r => ({
    column: r.querySelector('.w-col').value,
    op: r.querySelector('.w-op').value,
    value: r.querySelector('.w-val').value,
  }));
  say('探しています…');
  let res;
  try {
    res = await fetch('/api/tables/query', {
      method: 'POST',
      headers: { 'Content-Type': 'application/json' },
      body: JSON.stringify({ table: t.name, columns, where }),
    });
  } catch (e) { say('通信できませんでした', true); return; }
  const d = await res.json().catch(() => ({}));
  if (!res.ok || !d.success) { say(d.message || ('探せませんでした: ' + res.status), true); return; }
  say('');
  render(d.result);
}

function render(r) {
  clearResult();
  const head = document.querySelector('#tq-result thead');
  const body = document.querySelector('#tq-result tbody');
  const hr = el('tr');
  hr.appendChild(el('th', null, 'ページ'));
  r.columns.forEach(c => hr.appendChild(el('th', null, c)));
  head.appendChild(hr);
  r.rows.forEach(row => {
    const tr = el('tr');
    const td = el('td');
    const a = el('a', { href: '/' + row.page_id }, row.page_id + ' ' + (row.title || ''));
    td.appendChild(a);
    if (row.table_id > 1) td.appendChild(document.createTextNode('（表' + row.table_id + '）'));
    tr.appendChild(td);
    row.values.forEach(v => {
      const c = el('td');
      if (typeof v === 'number') c.className = 'num';
      const s = v === null || v === undefined ? '' : String(v);
      if (s.includes('\n')) c.classList.add('multiline');
      c.textContent = s;
      tr.appendChild(c);
    });
    body.appendChild(tr);
  });
  document.getElementById('tq-summary').textContent =
    r.rows.length + ' 行' + (r.truncated ? '（多いので途中まで。条件を足して絞ってください）' : '');
  document.getElementById('tq-sql').textContent = r.sql || '';
  document.getElementById('tq-sql-box').classList.toggle('is-hidden', !r.sql);
}

document.getElementById('tq-table').addEventListener('change', onTableChange);
document.getElementById('tq-add').addEventListener('click', addCondition);
document.getElementById('tq-run').addEventListener('click', run);
init();
