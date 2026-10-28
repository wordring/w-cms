// tls-trust.js — 道具（tools/*.js）が w-cms へ繋ぐときの証明書の確かめです（2026-10-28）。
//
// それまでの道具は、`NODE_TLS_REJECT_UNAUTHORIZED = '0'` で、どの当て先でも証明書を確かめていませんでした。
// 業務サーバー（https://192.168.1.100:8443 など）へ繋ぐと、管理者のパスワードとセッションを途中で盗み見られます
// （【考察】コードの矛盾 §7.2・Go の道具 tools/internal/wcms は 2026-10-22 から同じ形）。
//
//   - 同じ機械（localhost・127.0.0.1・::1）だけは確かめない（手元の開発用の自己署名・SSH の中継の試験用）。
//   - ほかの機械は確かめる。信じるのは、ふだんの CA と Windows の信頼ストアに加えて、WCMS_CA（CA の証明書のファイル）か、
//     無ければデスクトップの `w-cms/サーバー機/` の *.crt（業務サーバーの CA・リポジトリには入れない）。
//
// > 2026-10-28 利用者（道具が証明書の確かめを切っている件に）:「業務サーバーの証明書で確かめる（勧め）」
const fs = require('fs');
const os = require('os');
const path = require('path');
const tls = require('tls');

const LOCAL = new Set(['localhost', '127.0.0.1', '[::1]', '::1']);

// trustFor は、当て先 base に合わせて証明書の確かめを整えます（fetch より前に1回呼ぶ）。
function trustFor(base) {
  let host = '';
  try { host = new URL(base).hostname; } catch { /* 読めなければ確かめる側に倒す */ }
  if (LOCAL.has(host)) {
    process.env.NODE_TLS_REJECT_UNAUTHORIZED = '0';
    return;
  }
  const extra = [];
  for (const f of caFiles()) {
    try { extra.push(fs.readFileSync(f, 'utf8')); } catch { /* 読めないファイルは飛ばす */ }
  }
  let system = [];
  try { system = tls.getCACertificates('system'); } catch { /* 古い node */ }
  if (typeof tls.setDefaultCACertificates === 'function') {
    tls.setDefaultCACertificates([...tls.getCACertificates('default'), ...system, ...extra]);
  } else if (extra.length) {
    throw new Error('この node は信じる証明書を足せません。NODE_EXTRA_CA_CERTS=' + caFiles()[0] + ' を付けて流してください');
  }
}

// caFiles は信じる CA の証明書のファイルです（WCMS_CA・無ければデスクトップの w-cms/サーバー機/*.crt）。
function caFiles() {
  if (process.env.WCMS_CA) return [process.env.WCMS_CA];
  const home = os.homedir();
  for (const d of [path.join(home, 'OneDrive', 'デスクトップ'), path.join(home, 'OneDrive', 'Desktop'), path.join(home, 'Desktop')]) {
    const dir = path.join(d, 'w-cms', 'サーバー機');
    try {
      return fs.readdirSync(dir).filter((n) => n.endsWith('.crt')).map((n) => path.join(dir, n));
    } catch { /* 無ければ次へ */ }
  }
  return [];
}

module.exports = { trustFor };
