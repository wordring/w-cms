// テンプレートを別の環境へ運ぶ（書き出し・取り込み）——2026-09-27。
//
// 置き場（通信箱・連絡帳・取引先・受注・発注など）は**同じ題のテンプレートからだけ**作られます
// （利用者:「テンプレートが無ければ作れないまで行きます。ハードコーディングを無くしたい」）。
// テンプレートは**環境ごとのデータ**（Git に入らない）なので、ほかの環境へはこの道具で運びます。
//
//   node tools/templates.js export <ファイル>   … テンプレートの木を JSON に書き出す（本文は正本のファイルから）
//   node tools/templates.js import <ファイル>   … 無いものだけを作る（同じ題が既にあれば触らない）
//
// 当て先は WCMS_BASE（既定 https://localhost:8443）。サーバーを動かしたまま、リポジトリの根で流します。
// ログインは a / a（ローカル検証専用）。運ぶファイルは OneDrive の
// `デスクトップ/w-cms/テンプレート/` に置く（開発方針 §2 の置き場・リポジトリには入れない）。
process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0'; // 自己署名の :8443 に繋ぐため（ローカル専用）
const fs = require('fs');
const path = require('path');

const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
const MASTER = path.join(process.cwd(), 'data', 'master');
const ROOT_TITLE = 'テンプレート';
let cookie = '';

async function req(method, p, { body, headers = {} } = {}) {
  const res = await fetch(BASE + p, { method, body, redirect: 'manual', headers: { Origin: BASE, Cookie: cookie, ...headers } });
  for (const c of (res.headers.getSetCookie ? res.headers.getSetCookie() : [])) {
    const kv = c.split(';')[0], name = kv.split('=')[0];
    cookie = cookie.split('; ').filter(x => x && !x.startsWith(name + '=')).concat(kv).join('; ');
  }
  return res;
}
const login = () => req('POST', '/api/login', { body: 'username=a&password=a',
  headers: { 'Content-Type': 'application/x-www-form-urlencoded' } });
const bodyOf = id => fs.readFileSync(path.join(MASTER, id.slice(0, 2), id, id + '.html'), 'utf8');
async function children(id) {
  const r = await req('GET', '/api/children?parent_id=' + id);
  const d = await r.json();
  return (Array.isArray(d) ? d : (d.children || [])).map(c => ({ id: c.id || c.ID, title: c.title || c.Title }));
}

async function exportTo(file) {
  const tree = await (await req('GET', '/api/templates')).json(); // テンプレート置き場の下の木
  const walk = nodes => nodes.map(n => ({ title: n.title, body: bodyOf(n.id), children: walk(n.children || []) }));
  const out = walk(tree);
  fs.mkdirSync(path.dirname(file), { recursive: true });
  fs.writeFileSync(file, JSON.stringify(out, null, 2), 'utf8');
  const count = ns => ns.reduce((s, n) => s + 1 + count(n.children), 0);
  console.log(`書き出した: ${count(out)} ページ → ${file}`);
}

async function lock(id) { await req('POST', `/api/lock/force?id=${id}`); return (await (await req('POST', `/api/lock?id=${id}`)).json()).token; }
async function save(id, html) {
  const token = await lock(id);
  const r = await req('POST', '/api/save', { body: JSON.stringify({ page_id: id, html, token }),
    headers: { 'Content-Type': 'application/json', 'X-Lock-Token': token } });
  await req('POST', `/api/unlock?id=${id}&token=${encodeURIComponent(token)}`);
  if (r.status !== 200) throw new Error(`save ${id}: ${r.status} ${await r.text()}`);
}
async function newPage(parent, html) {
  const r = await req('POST', '/api/new-page', { body: 'parent=' + parent,
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' } });
  const id = ((r.headers.get('location') || '').match(/(\d{6})/) || [])[1];
  if (!id) throw new Error('ページを作れません: ' + r.status);
  await save(id, html);
  return id;
}

async function importFrom(file) {
  const nodes = JSON.parse(fs.readFileSync(file, 'utf8'));
  let root = (await children('000000')).find(c => c.title === ROOT_TITLE);
  let made = 0, kept = 0;
  if (!root) {
    root = { id: await newPage('000000', `<h1>${ROOT_TITLE}</h1>`) };
    made++;
  }
  const put = async (parent, list) => {
    const have = await children(parent);
    for (const n of list) {
      let hit = have.find(c => c.title === n.title);
      if (hit) { kept++; } else { hit = { id: await newPage(parent, n.body) }; made++; }
      await put(hit.id, n.children || []);
    }
  };
  await put(root.id, nodes);
  console.log(`取り込んだ: 作った ${made} ページ・既にあったので触らなかった ${kept} ページ`);
}

(async () => {
  const [cmd, file] = process.argv.slice(2);
  if (!['export', 'import'].includes(cmd) || !file) {
    console.error('使い方: node tools/templates.js export|import <ファイル>');
    process.exit(2);
  }
  const lr = await login();
  if ((lr.headers.get('location') || '').includes('error')) throw new Error('ログインできません');
  if (cmd === 'export') await exportTo(file); else await importFrom(file);
})().catch(e => { console.error('失敗:', e.message); process.exit(1); });
