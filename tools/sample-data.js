// 家（開発用）のサンプルデータを、移行用の元データから作るレシピ（2026-10-15）。
//
// 利用者:「家には移行用の元データがあります。これを使ってレシピによってサンプルデータを作れますか」。
// 元データは OneDrive で家にも届いているもの——デスクトップの w-cms\ の下の、テンプレート（テンプレート.json）・
// メール（tools/mail/fetch が職場で落としたもの）・ワンノート（tools/onenote/extract.ps1 が吸い出したもの）。
// 職場の data（実運用）は使わない。手順の説明は docs/参照/【ガイド】家のサンプルデータ.md。
//
// 使い方（リポジトリの根で・**サンプル用のデータの場所**で動かしているサーバーに向けて）:
//
//   WCMS_DATA_DIR=data-sample node tools/sample-data.js          … 全部（何度流してもよい——入っているものは飛ばす）
//   WCMS_DATA_DIR=data-sample node tools/sample-data.js --only mail,onenote
//
// 段（--only で選ぶ。既定は全部、この順）:
//   templates … テンプレートを取り込む（無いものだけ・tools/templates.js import）
//   boxes     … 足りない置き場を作る（管理画面の「足りない置き場を作る」と同じ口）
//   mail      … メールを通信箱へ（go run ./tools/mail/push・2026-05-01 より前の受信は「対応：不要」——職場と同じ）
//   onenote   … ワンノートから加工製品ページを作る（go run ./tools/onenote/build -gemini=false）。製造の記録は
//               -state の場所（既定 %LOCALAPPDATA%\w-cms\サンプルの製造）——職場の製造の記録は書かない
//   rebuild   … DB 再構築
//
// ⚠ 職場の実データ（data）に向けて流さないこと——WCMS_DATA_DIR が空か data なら止まる（サーバーの側も同じ
//    WCMS_DATA_DIR で動かす。サーバーと道具の data の場所が違うと、同じ図面番号の見張りが外れる）。
// ⚠ Gemini は呼ばない（図面の表題欄は、職場で読んだ控えのあるものだけ使う）。
const path = require('path');
const os = require('os');
const { spawnSync } = require('child_process');

const BASE = process.env.WCMS_BASE || 'https://localhost:8443';
// 証明書は、同じ機械のときだけ確かめない（業務サーバーへは確かめる・tls-trust.js・2026-10-28）。
require('./tls-trust').trustFor(BASE);
const DATA = process.env.WCMS_DATA_DIR || '';
const DESKTOP = path.join(os.homedir(), 'OneDrive', 'デスクトップ', 'w-cms');
const STATE = process.env.WCMS_SAMPLE_STATE ||
  path.join(process.env.LOCALAPPDATA || path.join(os.homedir(), 'AppData', 'Local'), 'w-cms', 'サンプルの製造');
const STEPS = ['templates', 'boxes', 'mail', 'onenote', 'rebuild'];

const onlyArg = process.argv.indexOf('--only');
const only = onlyArg > 0 ? process.argv[onlyArg + 1].split(',').map((s) => s.trim()) : STEPS;
for (const s of only) {
  if (!STEPS.includes(s)) {
    console.error('知らない段です: ' + s + '（' + STEPS.join('・') + '）');
    process.exit(2);
  }
}
if (!DATA || path.resolve(DATA) === path.resolve('data')) {
  console.error('WCMS_DATA_DIR にサンプル用のデータの場所を渡してください（例: data-sample）——職場の data には流しません');
  process.exit(2);
}

let cookie = '';
async function req(method, p, { body, headers = {} } = {}) {
  const res = await fetch(BASE + p, { method, body, redirect: 'manual', headers: { Origin: BASE, Cookie: cookie, ...headers } });
  for (const c of (res.headers.getSetCookie ? res.headers.getSetCookie() : [])) {
    const kv = c.split(';')[0], name = kv.split('=')[0];
    cookie = cookie.split('; ').filter((x) => x && !x.startsWith(name + '=')).concat(kv).join('; ');
  }
  return res;
}

function run(cmd, args) {
  console.log('\n$ ' + cmd + ' ' + args.join(' '));
  const r = spawnSync(cmd, args, { stdio: 'inherit', env: process.env });
  if (r.status !== 0) throw new Error(cmd + ' ' + args[0] + ' が失敗しました（' + r.status + '）');
}

(async () => {
  const lr = await req('POST', '/api/login', { body: 'username=a&password=a', headers: { 'Content-Type': 'application/x-www-form-urlencoded' } });
  if (!cookie) throw new Error('ログインできません（' + BASE + '・' + lr.status + '）——サーバーを動かしてから流してください');
  // サーバーが同じデータの場所で動いているかを、起動ログの代わりに確かめる口は無いので、人に見せて確かめる。
  console.log('当て先: ' + BASE + '・データ: ' + DATA + '・製造の記録: ' + STATE);

  if (only.includes('templates')) {
    run('node', ['tools/templates.js', 'import', path.join(DESKTOP, 'テンプレート', 'テンプレート.json')]);
  }
  if (only.includes('boxes')) {
    const r = await req('POST', '/api/admin/pages');
    const d = await r.json().catch(() => ({}));
    const made = (d.created || []).map((x) => x.title); // この回に作ったもの（在ったものは入らない）
    console.log('\n置き場: ' + (r.status === 200 ? (made.length ? '作った ' + made.join('・') : 'みな在る') : '失敗 ' + r.status));
  }
  if (only.includes('mail')) {
    run('go', ['run', './tools/mail/push', '-handled-before', '2026-05-01']);
  }
  if (only.includes('onenote')) {
    run('go', ['run', './tools/onenote/build', '-gemini=false', '-state', STATE]);
  }
  if (only.includes('rebuild')) {
    const r = await req('POST', '/api/rebuild-db');
    console.log('\nDB 再構築: ' + r.status + ' ' + (await r.text()).slice(0, 120));
  }
  console.log('\n済み。');
})().catch((e) => { console.error('失敗:', e.message); process.exit(1); });
