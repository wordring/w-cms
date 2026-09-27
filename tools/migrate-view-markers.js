// 名前の見えない鏡の印を、見出しの印へ書き換える（2026-09-27・一度きりの移し替え）。
//
//   <section data-type="unordered-items"></section>  →  <section><h2>必要部材表</h2></section>
//
// 利用者:「名前の見えない印 <section data-type="unordered-items"></section> は廃止して削除して
// 欲しい」。コードは 2026-09-27 から属性の印を**読みません**（鏡が出なくなる）ので、各環境の
// データをこれで一度だけ書き換えます。⚠ **両方の環境（職場・自宅）で流し終えたら、この道具は消す**。
//
// どれが鏡で、何という名前かは**動いているサーバーの `/api/tag-schema` から引きます**
// （形式名も表示名もここに焼かない）。本文は**正本のファイル**（data/master）を読み、
// `/api/save` で書きます（`/api/load` の出力は鏡が焼き込まれるので書き戻さない）。
//
// 使い方（リポジトリの根で・サーバーを動かしたまま）:
//   DRY=1 node tools/migrate-view-markers.js   … 書き換える箇所を見せるだけ
//   node tools/migrate-view-markers.js         … 書き換える
// 当て先は WCMS_BASE（既定 https://localhost:8443）。ログインは a / a（ローカル検証専用）。
process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0'; // 自己署名の :8443 に繋ぐため（ローカル専用）
const fs = require('fs');
const path = require('path');

const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
const DRY = process.env.DRY === '1';
const MASTER = path.join(process.cwd(), 'data', 'master');
let cookie = '';

async function req(method, p, { body, headers = {} } = {}) {
  const res = await fetch(BASE + p, {
    method, body, redirect: 'manual', headers: { Origin: BASE, Cookie: cookie, ...headers },
  });
  for (const c of (res.headers.getSetCookie ? res.headers.getSetCookie() : [])) {
    const kv = c.split(';')[0];
    const name = kv.split('=')[0];
    cookie = cookie.split('; ').filter(x => x && !x.startsWith(name + '=')).concat(kv).join('; ');
  }
  return res;
}

function escapeHTML(s) {
  return s.replace(/&/g, '&amp;').replace(/</g, '&lt;').replace(/>/g, '&gt;');
}

// 本文の中の属性の印を見出しの印へ。中身のある印・入れ子の印は触らずに報告する。
function rewrite(html, views) {
  const done = [];
  const skipped = [];
  const re = /<section\b([^>]*?)\s+data-type="([^"]+)"([^>]*)>([\s\S]*?)<\/section>/g;
  const out = html.replace(re, (all, before, type, after, inner) => {
    const name = views.get(type);
    if (!name) return all; // 鏡でない（ファイル表示・タグなど）はそのまま
    if (/<section\b/.test(inner) || inner.trim() !== '') {
      skipped.push(type);
      return all;
    }
    done.push(type);
    return `<section${before}${after}><h2>${escapeHTML(name)}</h2></section>`;
  });
  return { out, done, skipped };
}

async function save(id, html) {
  let r = await req('POST', `/api/lock/force?id=${id}`);
  if (![200, 204].includes(r.status)) throw new Error(`lock/force ${id}: ${r.status}`);
  r = await req('POST', `/api/lock?id=${id}`);
  if (r.status !== 200) throw new Error(`lock ${id}: ${r.status} ${await r.text()}`);
  const { token } = await r.json();
  try {
    r = await req('POST', '/api/save', {
      body: JSON.stringify({ page_id: id, html, token }),
      headers: { 'Content-Type': 'application/json', 'X-Lock-Token': token },
    });
    if (r.status !== 200) throw new Error(`save ${id}: ${r.status} ${await r.text()}`);
  } finally {
    await req('POST', `/api/unlock?id=${id}&token=${encodeURIComponent(token)}`);
  }
}

(async () => {
  const lr = await req('POST', '/api/login', {
    body: 'username=a&password=a',
    headers: { 'Content-Type': 'application/x-www-form-urlencoded' },
  });
  if ((lr.headers.get('location') || '').includes('error')) throw new Error('ログインできません');

  const schema = await (await req('GET', '/api/tag-schema')).json();
  const views = new Map((schema.vocab || []).filter(d => d.view).map(d => [d.type, d.display_name]));
  if (views.size === 0) throw new Error('鏡の形式が1つも引けません（/api/tag-schema）');

  let pages = 0, markers = 0;
  for (const bucket of fs.readdirSync(MASTER)) {
    const dir = path.join(MASTER, bucket);
    if (!fs.statSync(dir).isDirectory()) continue;
    for (const id of fs.readdirSync(dir)) {
      const file = path.join(dir, id, id + '.html');
      if (!fs.existsSync(file)) continue;
      const html = fs.readFileSync(file, 'utf8');
      const { out, done, skipped } = rewrite(html, views);
      if (skipped.length) console.log(`⚠ ${id}: 中身のある印は触りません: ${skipped.join(', ')}`);
      if (done.length === 0) continue;
      pages++;
      markers += done.length;
      console.log(`${DRY ? '（試し）' : ''}${id}: ${done.map(t => views.get(t)).join('・')}`);
      if (!DRY) await save(id, out);
    }
  }
  console.log(`${DRY ? '書き換える予定' : '書き換えた'}: ${pages} ページ・${markers} 個`);
})().catch(e => { console.error('失敗:', e.message); process.exit(1); });
