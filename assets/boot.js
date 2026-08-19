// boot.js — body 描画前に走る起動スクリプト（head から同期読み込み）。
//
// サイドパネルの開閉状態を描画前に確定してチラつき（FOUC）を防ぐ。
// 状態は UI 設定ストア（localStorage の単一キー `wcms.ui`）に保存する（クッキー不要）。
// 本体スクリプト（app.js）読込前なので、UI ヘルパに依らず localStorage を直接読む
// （旧 `wcms.rails` キーは fallback。本体側 UI.migrate が後で正式に取り込む）。
//
// かつては index.html のインライン <script> だったが、CSP strict 化
// （'unsafe-inline' 除去）に伴い外部ファイル（＝CSPの self）へ移した。
try {
    var ui = JSON.parse(localStorage.getItem('wcms.ui') || 'null');
    var rails = (ui && ui.rails) || JSON.parse(localStorage.getItem('wcms.rails') || '{}');
    // 左側の2レール（ページ階層／目次）は同時に開かない。どちらも明示的に
    // 畳まれていなければページ階層を優先し、目次を畳んだ状態で描画する。
    var leftCollapsed = rails.left === false;
    var tocCollapsed = rails.toc === false;
    if (!leftCollapsed && !tocCollapsed) tocCollapsed = true;
    if (leftCollapsed) document.documentElement.classList.add('left-collapsed');
    if (tocCollapsed) document.documentElement.classList.add('toc-collapsed');
    if (rails.right === false) document.documentElement.classList.add('right-collapsed');
} catch (e) { /* localStorage 不可時は既定（ページ階層のみ開く） */ }
