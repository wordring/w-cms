// 文書を読みやすく書き直したときに、元の文書にあったものを落としていないかを比べる（2026-10-13・開発方針 §2a）。
//
// 比べる相手は git の版（既定は HEAD——まだコミットしていない書き直しと、コミット済みの元を比べる）。比べるもの:
// リンクの先・バッククォートの識別子・「」の引用（6字以上）・日付（YYYY-MM-DD）・節の見出しの番号。
// ⚠ 「落ちた」と出たものは、戻すか、意図して言い換えた・古くなって外したと説明できること（機械は意味を読まない）。
//
// 使い方（リポジトリの根で）: node tools/docs-check-rewrite.js "docs/【要求】w-cmsとは.md" [比べる相手のコミット]
const { execFileSync } = require('child_process');
const fs = require('fs');
const [file, rev = 'HEAD'] = process.argv.slice(2);
const oldText = execFileSync('git', ['-c', 'core.quotepath=false', 'show', rev + ':' + file], { encoding: 'utf8', maxBuffer: 1 << 28 });
const newText = fs.readFileSync(file, 'utf8');

const pick = (text, re, group = 1) => {
  const out = new Set();
  for (const m of text.matchAll(re)) out.add(m[group].trim());
  return out;
};
const norm = (s) => s.replace(/\s+/g, '');
const newNorm = norm(newText);
const cats = [
  ['リンクの先', /\]\(([^)\s]+)\)/g],
  ['識別子（`…`）', /`([^`\n]+)`/g],
  ['「」の引用', /「([^」\n]{6,})」/g],
  ['日付', /\b(20\d\d-\d\d-\d\d)\b/g],
  ['節の見出しの番号', /^#{2,4}\s+((?:§?\d+[a-z]?(?:\.\d+[a-z]?)*)\.?)\s/gm],
];
let total = 0;
for (const [name, re] of cats) {
  const olds = pick(oldText, re);
  const missing = [...olds].filter((x) => !newNorm.includes(norm(x)));
  total += missing.length;
  console.log(`${name}: 元 ${olds.size} ・落ちた ${missing.length}`);
  for (const m of missing) console.log('   - ' + m.slice(0, 110));
}
const lines = (t) => t.split('\n').length;
console.log(`行数: ${lines(oldText)} → ${lines(newText)}・字数: ${oldText.length} → ${newText.length}`);
process.exitCode = 0;
