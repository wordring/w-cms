// Claude Code の hook（.claude/settings.json の PostToolUse）から呼ばれ、コアと拡張の境の番人
// （go test ./internal/boundary/）を流します（2026-10-21。docs/開発方針.md §0）。
//
// 2026-10-20 利用者:「コアと拡張を分離することを守らせるために設定に何か書いておくことはできますか？」
// 「両方お願いします。開発方針にも書いたほうが良いと思いうます」
//
// 標準入力に hook の JSON（tool_input.file_path）が来ます。書き換えたのがコアのファイル
// （internal/ の Go・assets/ 直下の JS・CSS・HTML）か、拡張の Go（拡張の class の一覧が変わる）か、
// 決まりの表 internal/boundary/rules.json のときだけ流します。
// 落ちたら、試験の出力を標準エラーへ出して終了コード 2 で終わります（Claude Code が Claude に見せる）。
// 人が手で流すときは: node tools/boundary-hook.js <ファイル>（標準入力なしで、そのファイルを書いたとみなす）
'use strict';
const path = require('path');
const { spawnSync } = require('child_process');

const root = path.resolve(__dirname, '..');

function watched(rel) {
  if (rel === 'internal/boundary/rules.json') return true;
  if (/^(internal|ext)\/.+\.go$/.test(rel) && !rel.endsWith('_test.go')) return true;
  return /^assets\/[^/]+\.(js|css|html)$/.test(rel);
}

function run(file) {
  if (!file) return 0;
  const rel = path.relative(root, path.resolve(root, file)).split(path.sep).join('/');
  if (rel.startsWith('..') || !watched(rel)) return 0;
  const r = spawnSync('go', ['test', './internal/boundary/', '-count=1'], { cwd: root, encoding: 'utf8' });
  if (r.error) {
    process.stderr.write('コアと拡張の境の番人を流せません（go が見つからない？）: ' + r.error.message + '\n');
    return 1; // hook の失敗（Claude を止めない）
  }
  if (r.status === 0) return 0;
  process.stderr.write(
    '⚠ ' + rel + ' を書き換えたら、コアと拡張の境の番人（internal/boundary）が落ちました。\n' +
    'コアは業務の言葉と拡張の class を知りません（docs/開発方針.md §0）。口をコアに作り、言葉は拡張から渡してください。\n' +
    'どうしても要る例外は internal/boundary/rules.json の allow に、理由つきで書きます。\n\n' +
    (r.stdout || '') + (r.stderr || ''));
  return 2;
}

if (process.argv[2]) {
  process.exit(run(process.argv[2]));
} else {
  let input = '';
  process.stdin.setEncoding('utf8');
  process.stdin.on('data', (c) => { input += c; });
  process.stdin.on('end', () => {
    let file = '';
    try {
      const j = JSON.parse(input);
      file = (j.tool_input && j.tool_input.file_path) || (j.tool_response && j.tool_response.filePath) || '';
    } catch (e) {
      process.exit(0); // 読めない入力では何もしない
    }
    process.exit(run(file));
  });
}
