# 【考察】CSP強化

> 状態: **完了の記録**。strict 版（`'unsafe-inline'` なし）へ **2026-08-19**（語彙モデル移行の第4段）に到達。
> 後戻りは [csp_test.go](../internal/auth/csp_test.go)（ポリシー文字列）と
> [route_guard_test.go](../cmd/w-cms/route_guard_test.go)（`buildHandler()` への配線）の2本が検出する。
> **現行の値だけ知りたい読者の入口は §1**。本文は「なぜそのCSPになったか」の記録に徹する。
> `【考察】` 接頭辞は維持する（理由は §4）。
> 最終更新: 2026-08-21（完了に伴い圧縮。インライン依存の棚卸し表・チェックリストの進捗など
> 当時の作業記録は [変更履歴.md](変更履歴.md) 2026-08-06／2026-08-19 と git 履歴へ譲った）

---

## 1. 現行ポリシーと、そう決めた理由

実装は [middleware.go](../internal/auth/middleware.go) の `cspPolicy`。
[main.go](../cmd/w-cms/main.go) の最外周（`auth.CSPProtect(auth.CSRFProtect(root))`）で全応答へ付く。

```
default-src 'self'; script-src 'self'; style-src 'self'; object-src 'self'; base-uri 'self'; frame-ancestors 'self'
```

- **なぜCSPを入れるか（脅威モデル）**: ①**保存済みXSSへの多層防御**——本文はユーザー入力由来のHTMLで、
  サーバー合成で初期HTMLの一部としてパースされる。万一スクリプトが混入しても実行を抑止できる。
  ②**クリックジャッキング防止**（`frame-ancestors 'self'`）。③**外部送信の遮断**——注入されたコードが
  外部オリジンへデータを送る・外部リソースを読み込む経路を `default-src 'self'` で塞ぐ。
- **なぜ `script-src`/`style-src` を明示するか**: `default-src 'self'` の継承に任せても効果は同値だが、
  明示すれば「インラインを許していない」ことがヘッダを見ただけで分かり、テストの固定も直接的になる。
- **なぜ `'self'` で何も壊れないか（実測）**: `assets/` 配下に外部オリジン参照・`data:`/`blob:` URI・
  web fonts・`@import` は**皆無**。`<link rel="stylesheet">` と `<script src>` はすべて `/assets/` 配下＝
  同一オリジン、PDFは `<embed src="/data/master/...">` の同一オリジン配信なので `object-src 'self'` を通る。
- **中間版（2026-08-02〜08-19）の位置づけ**: `script-src`/`style-src` にだけ `'unsafe-inline'` を許した暫定値。
  他の4ディレクティブは最初から目標と同値だったので、**外部送信遮断とクリックジャッキング防止の実利は
  中間段階でも確保していた**。素で strict を入れると当時のフロント（インライン script/style/`on*=` が多数）は
  全停止した——だから段階を切った。

## 2. なぜ per-request nonce ではなく外部ファイル化を選んだか

FOUC（サイドパネル開閉状態を描画前に確定する）用の `<head>` スクリプトだけは、当初
「チラつき防止のため外部化せず `<script nonce=...>` で残す」計画だった。**実装では
[assets/boot.js](../assets/boot.js) への外部化を選んだ**（`index.html` 冒頭の
`<script src="/assets/boot.js"></script>`。`defer` なしの同期読み込み）。理由は3つ:

1. head の同期外部スクリプトは解析をブロックして本文描画前に走るので、**インライン時代と同じ順序が保て、
   FOUC 防止の目的を等しく満たす**。
2. nonce 配線（`CSPProtect` で乱数生成 → `context` 経由で `RootHandler` がプレースホルダへ注入）が
   **まるごと不要**になる。`cspPolicy` は定数のままでよく、nonce 機構は一度も実装せずに済んだ。
3. ページごとにヘッダが変わらないので**静的配信・将来のキャッシュと両立**する
   （公開ビューの要件。[要件定義書.md](要件定義書.md) §4.4）。

なお nonce 方式は**属性ハンドラ `on*=` を救えない**ので、`on*=` の除去はどちらの案でも必須だった。

## 3. strict へ至る過程で下した判断（記録）

- **本体スクリプト／スタイルの外部化**（2026-08-06）: `index.html` の本体 `<script>`→[app.js](../assets/app.js)、
  `<style>`→[app.css](../assets/app.css)、`admin.html` も同様。`index.html` は 3015行→215行になった。
  `app.js` に `defer` は付けない（`</body>` 直前なので解析済みDOMを前提にでき、付ける利点が無い）。
- **`on*=` は「イベント委譲＋`data-*`」へ**: `onclick="resetPw('…')"` のように**文字列でJSを組み立てて**いたため、
  ユーザー名に `'` が入ると壊れる作りでもあった。CSPと関係なく直す価値があった。
- **`style=` は個別除去せず、発生源ごと撤去した**（最大の判断）: `templates/*.html` の `style=`（計75個）と
  `web-components.js` が文字列で組み立てる `style=`（8行）は、**Web Components 全廃の決定**（2026-08-17・
  [【考察】語彙モデル.md](【考察】語彙モデル.md) §9）に相乗りさせ、第4段で `templates/`（12ファイル）・
  `web-components.js`・`components.css` を**ファイルごと撤去**して消した。後継はサーバー事前描画
  （[view_render.go](../internal/cms/view_render.go)）とエンハンサ（`createElement`＋`textContent`）で、
  どちらもインライン `style=` を生成しない。ログイン画面の `<style>` は
  [login.css](../assets/login.css) へ外部化。
  > **落とし穴（再発しうる）**: markup の `style="display:none"` をCSSへ移すときは、JS側の
  > `el.style.display = ''` も同時に潰すこと。CSSに規則が残ると `''` では**二度と表示できない**。
  > `.is-hidden` クラス＋`setHidden()` に統一して入口を1つにした。動的な値（色・座標）は
  > 従来どおり `element.style.x = y`（CSSOM経由）でよい。
- **strict CSP は `innerHTML` 経由で挿入した `on*=`/`style=` も止める**（DOM生成の方法に関わらない）。
  将来 `innerHTML` でUIを組む書き方を持ち込んでも、そこに `on*=`／`style=` を混ぜた時点でブロックされる。

## 4. なぜ `【考察】` 接頭辞を外さないか（2026-08-20 決定）

当初は「strict 到達時に `docs/CSP強化.md` へリネームする」と書いていたが、**維持**に改めた。理由は3つ。
① [開発方針.md](開発方針.md) §2 は「`【考察】` は状態マーカーとして**型接頭辞の前に重ねられる**」
（`【考察】【ユースケース】○○` → 実装後に `【ユースケース】○○`）としたうえで「**実装済みになっても
考察メモは記録として接頭辞を維持します**」と明記しており、本ファイルは型接頭辞を持たない**純粋な考察メモ**
なので後者に当たる。②本文の大半は恒久仕様ではなく**設計判断の経緯**で、その性格は strict 到達後も変わらない。
③リネームは本ファイルへの相対リンク（2026-08-20 実測で**他13ファイル・16箇所**。`docs/` 各書・`README.md`・
`internal/auth/middleware.go` のコメントを含む）の一斉張り替えを伴い、代償が大きい。

## 5. サニタイズとの関係

役割が違う多層防御で、どちらか一方では足りない（**正本は
[本文サニタイズ設計.md](本文サニタイズ設計.md) §6**）。CSPの側から言えるのは1点——サニタイザが `on*` と
`style` を**どの要素でも常に落とす**方針は strict 化の前提と一致しており、本文に `style=` を通す例外を作ると
`style-src 'self'` を緩めることになるので、**この2つはセットで維持する**。

## 6. 参考

実装は [internal/auth/middleware.go](../internal/auth/middleware.go)（`CSPProtect`・`cspPolicy`）と
[cmd/w-cms/main.go](../cmd/w-cms/main.go)（チェーン組み込み）。新規コードのインライン禁止ルールは
[開発方針.md](開発方針.md) §4、ミドルウェア全体の設計は [認証認可設計.md](認証認可設計.md)。
