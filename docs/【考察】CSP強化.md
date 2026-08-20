# 【考察】CSP強化

w-cms へ Content-Security-Policy（CSP）を全レスポンスに付与し、保存済みXSS等への
多層防御、クリックジャッキング（`frame-ancestors`）防止、外部オリジンへのデータ送信の
遮断を得るための設計・実装計画の**正本**です。着手前に必ず本ファイルを読むこと。

> 状態: **strict 版（`'unsafe-inline'` なし）へ格上げ完了（2026-08-19・移行第4段）**。
> インラインの script/style はリポジトリ全体でゼロ——FOUC 防止は `/assets/boot.js` へ外部化
> （§4 の per-request nonce 案から変更: head の同期外部スクリプトで同じ順序が保て、nonce 機構が
> 不要で静的配信とも両立するため）、templates/*.html と web-components.js は第4段でファイルごと
> 撤去、ログイン画面の `<style>` は `/assets/login.css` へ外部化。後戻りは
> [csp_test.go](../internal/auth/csp_test.go) が検出する。
> **`【考察】` 接頭辞は維持する**（理由は §5。開発方針 §2「実装済みになっても考察メモは
> 記録として接頭辞を維持します」に従う）。
> 最終更新: 2026-08-20

---

## 1. 目的と脅威モデル

- **保存済みXSSへの多層防御**: 本文（マーカー付き標準HTML）はユーザー入力由来のHTMLを含みうる。
  万一スクリプトが混入しても、CSP で `script-src` を絞れば実行を抑止できる。
- **クリックジャッキング防止**: `frame-ancestors 'self'` で他サイトからの iframe 埋め込みを拒否。
- **外部送信の遮断**: `default-src 'self'` で、注入されたコードが外部オリジンへ
  データを送る・外部リソースを読み込む経路を塞ぐ。

### 1.1 サニタイズとの関係（2026-08-02 更新）

当初この節には「Go側に本文サニタイザは存在しない」と記録していたが、**その後実装された**。
サーバー合成（`RootHandler` が本文を `assets/index.html` へ埋め込んで返す方式）の導入に伴い、
本文中の `<script>`・`on*=` が実行されうる状態になったため、許可リスト方式のサニタイザを
**保存時と描画時の二層**で通すようにしている。正本は [本文サニタイズ設計.md](本文サニタイズ設計.md)、
実装は [internal/cms/htmldoc/sanitize.go](../internal/cms/htmldoc/sanitize.go)（2026-08-17 に切り出し）。

現時点の役割分担は次のとおり:

- **サニタイズ**: 本文という「データ」から危険な構造を除く。入口の防御。
- **CSP**: 漏れや将来の別経路に対する網。**strict 化（`'unsafe-inline'` 除去）が完了した
  2026-08-19 以降は、サニタイズをすり抜けたインライン script/style/`on*=` もCSPで止まる**
  （中間版の間はサニタイズが実質の単独防御だった）。

なお `/api/load` は **`text/plain; charset=utf-8` ＋ `X-Content-Type-Options: nosniff`** を返す
（text/plain 化は実施済み。[handler_view.go](../internal/cms/handler_view.go) の `LoadAPIHandler`）。
エディタは `fetch().text()` で受けて自前で `DOMParser` にかけるため text/html である必要がなく、
このURLを直接ブラウザで開いてもHTMLとして実行されない。これは初期表示には使われず、編集ロック
起点の載せ替え専用。保存時サニタイズにより正本自体が清書済みのため、返る内容もサニタイズ済みと一致する。

サニタイザが `on*` と `style` を常に落とす方針は、**strict 化の前提（インラインを増やさない）と
一致している**。

---

## 2. ポリシー

### 2.1 現行（strict・2026-08-19 到達）

実際に付与している値（[middleware.go](../internal/auth/middleware.go) の `cspPolicy`）:

```
Content-Security-Policy: default-src 'self'; script-src 'self'; style-src 'self'; object-src 'self'; base-uri 'self'; frame-ancestors 'self'
```

`'unsafe-inline'` を一切含まない。インラインの script/style/`on*=` は全てブロックされる。

> 当初の目標記述は `script-src`/`style-src` を省いて `default-src 'self'` の継承に任せる形
> （`default-src 'self'; object-src 'self'; base-uri 'self'; frame-ancestors 'self'`）だったが、
> 実装では**2つを明示**した。効果は同値で、明示のほうが「インラインを許していない」ことが
> ヘッダを見ただけで分かり、[csp_test.go](../internal/auth/csp_test.go) の固定も直接的になるため。

### 2.2 中間版（2026-08-02〜2026-08-19 に使っていた暫定値・撤去済み）

```
Content-Security-Policy: default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; object-src 'self'; base-uri 'self'; frame-ancestors 'self'
```

- `default-src` / `object-src` / `base-uri` / `frame-ancestors` は**目標strictと同値**だった。
  外部送信遮断・クリックジャッキング防止の実利を、中間段階でも確保していた。
- `script-src` / `style-src` のみ暫定で `'unsafe-inline'` を許可し、当時のフロントの
  多数のインライン script/style/`on*=` を生かしていた。**2026-08-19 にこの2つの
  `'unsafe-inline'` を外して strict へ到達**（§4 のチェックリスト参照）。

実装: `internal/auth/middleware.go` の `CSPProtect`（`cspPolicy` 定数）。
[cmd/w-cms/main.go](../cmd/w-cms/main.go) のチェーン最外周
（`auth.CSPProtect(auth.CSRFProtect(root))`）で全レスポンスへ付与。

### 2.3 なぜ `default-src 'self'` で壊れないか（実測）

`assets/` 配下に**外部オリジンへの参照**・`data:`/`blob:` URI・web fonts・`@import` は
**皆無**（実測。`https?://` のヒットもゼロ）。外部化で増えた `<link rel="stylesheet">`
（`index.html` に1本＝`app.css`／`admin.html` に1本＝`admin.css`／ログイン画面に1本＝`login.css`）と
`<script src>`（`index.html` に2本＝head の `boot.js` と末尾の `app.js`／`admin.html` に1本）は
すべて `/assets/` 配下＝同一オリジンなので `'self'` で通る
（`components.css` は第4段で撤去されたため `index.html` の link は2本→1本になった）。
PDFは `<embed src="/data/master/...">` の同一オリジン配信で
`object-src 'self'` を通る。よって `default-src 'self'` はインライン以外を一切壊さない。

---

## 3. インライン依存の棚卸し（strict格上げの対象・実測。**すべて解消済み**）

strict化には、以下の全インラインの除去／外部化が前提だった。素でstrictを入れると編集機能が全停止する、
という当時の見立ての記録として残す。

**2026-08-19 時点**: 下表はすべて解消済み。`on*=`・インライン `<script>`・`<style>`・`style=` は
**リポジトリ全体でゼロ**（後戻りは [csp_test.go](../internal/auth/csp_test.go) が検出する）。
どう片付けたかは §4 のチェックリストを参照。

| 箇所 | 種別 | 実測（2026-08-06 の外部化より前） |
| --- | --- | --- |
| `assets/index.html` | インライン `<script>` | 2つ（FOUC用 head 13–26／本体 922–2363） |
| `assets/index.html` | `<style>` ブロック | 1つ（27–724） |
| `assets/index.html` | `on*=` ハンドラ | 11個 |
| `assets/index.html` | `style=` 属性 | 12個 |
| `assets/admin.html` | インライン `<script>`／`<style>`／`on*=`／`style=` | 各1つ／1つ／8個／10個 |
| `assets/web-components.js` | `innerHTML` 描画＋動的 `style=` 生成 | `style=`/`innerHTML` 系 43箇所 |
| `assets/templates/*.html`（12ファイル） | `on*=`／`style=`（**保存本文としてレンダされる**） | 各ファイルに多数 |

`assets/templates/*.html` は、`web-components.js` が `fetch`→変数置換→`innerHTML` で
描画していたカスタム要素（`m-item`・`m-file`・`m-material` 等）のテンプレートだった
（**2026-08-19 に3ファイル群ごと撤去済み**。現在は存在しない）。
strict CSP は **`innerHTML` 経由で挿入した `on*=` 属性ハンドラも実行しない**点に注意
（DOM生成方法に関わらず、インラインイベントハンドラは `script-src 'unsafe-inline'` が無いとブロックされる）。
同様に `style=` 属性は `style-src 'unsafe-inline'` が無いとブロックされる。
**この性質は現在も効いている**——将来 `innerHTML` でUIを組む書き方を持ち込んでも、
そこに `on*=`／`style=` を混ぜた時点でブロックされる。

---

## 4. strict格上げの前提リファクタ・チェックリスト（**全項目完了・2026-08-19**）

- [x] **(1) 本体スクリプトの外部化**（2026-08-06 完了）: `index.html` 本体 `<script>` を
      [assets/app.js](../assets/app.js) へ、`admin.html` の `<script>` を
      [assets/admin.js](../assets/admin.js) へ移設。`index.html` は 3015行 → 215行、
      `admin.html` は 195行 → 72行（どちらも markup だけになった）。
      **`defer` は付けない**——当時は `web-components.js`（head・defer）より後に実行されると
      インライン時代の順序（本文スクリプトが先）が変わるため、という理由だった。
      `web-components.js` 撤去後も `defer` は付けていない（`</body>` 直前なので解析済みDOMを
      前提にでき、付ける利点が無い）。
- [x] **(2) FOUC head スクリプトの外部化**（2026-08-19 完了。**per-request nonce 案から方針変更**）:
      head の `<script>`（サイドパネル開閉状態を描画前に確定）は、当初「チラつき防止のため
      外部化せず `<script nonce=...>` で残す」計画だった。**実装では `/assets/boot.js` への
      外部化を選んだ**（`index.html` 冒頭の `<script src="/assets/boot.js"></script>`。
      `defer` なしの同期読み込み）。
      **変更理由**: ①head の同期外部スクリプトは解析をブロックして本文描画前に走るので、
      インライン時代と**同じ順序が保て、FOUC 防止の目的を等しく満たす** ②nonce 配線
      （`CSPProtect` で乱数生成 → `context` 経由で `RootHandler` がプレースホルダへ注入）が
      まるごと不要になる ③ページごとにヘッダが変わらないので**静的配信・将来のキャッシュとも
      両立する**（公開ビューの要件。[要件定義書.md](要件定義書.md) §4.4）。
      これにより `cspPolicy` は定数のままでよく、nonce 機構は**一度も実装せずに済んだ**。
      （なお nonce 方式は**属性ハンドラ `on*=` を救えない**ため、(3) はどちらの案でも必須だった。）
- [x] **(3) `on*=` の除去**（2026-08-06 完了・**リポジトリ全体でゼロ**）:
      `index.html` の11個は `data-rail` 等＋`bindChromeActions()` の addEventListener へ、
      `admin.html` の8個は id＋`bindActions()` へ置換した。ユーザー表の行ボタンは
      **tbody へのイベント委譲＋`data-action`/`data-username`** にした（`onclick="resetPw('…')"`
      と文字列でJSを組み立てていたため、ユーザー名に `'` が入ると壊れる作りでもあった）。
      テンプレート側は 2026-08-05 の Light DOM 統一で `data-attr`／`data-remove` へ移行済みで、
      最後に残っていた `m-required-materials-edit.html` の3個も今回置換した
      （[エディタ仕様.md](エディタ仕様.md) §0.1 の作法に合流）。
- [x] **(4) `style=` / `<style>` の除去**（2026-08-06 に殻ぶん、2026-08-19 に残り＝完了）:
      `index.html` の `<style>` は [assets/app.css](../assets/app.css)、`admin.html` の分は
      [assets/admin.css](../assets/admin.css) へ移設し、両ファイルの `style=` 属性も
      CSSクラスへ移した。
      **残っていた** `templates/*.html` の `style=`（計75個）と `web-components.js` が
      文字列で組み立てる `style=`（手配状況テーブルの8行）は、**Web Components の
      全廃決定（2026-08-17・[【考察】語彙モデル.md](【考察】語彙モデル.md) §9）どおり
      個別除去を一切行わず、移行第4段で `templates/`（12ファイル）・`web-components.js`・
      `components.css` を**ファイルごと撤去**して発生源を消した**（2026-08-19）。
      手配状況テーブルはサーバー事前描画（[view_render.go](../internal/cms/view_render.go)）
      へ移り、外観はCSSクラスで当てている。
      ログイン画面に1つ残っていた `<style>` も [assets/login.css](../assets/login.css) へ外部化した。
      > **落とし穴**: markup の `style="display:none"` を CSS へ移すときは、JS 側の
      > `el.style.display = ''` も同時に潰すこと。CSS に規則が残ると `''` では**二度と表示できない**。
      > 今回は `.is-hidden` クラス＋`setHidden()` に統一して入口を1つにした
      > （対象: `#admin-link`・`#pp-chown`・`#pp-view-hint`・`#pp-public-row`・`#denied`・`#console`）。
      > 動的な値（色・座標）は従来どおり `element.style.x = y`（CSSOM経由）でよい。
- ~~**(5) web-components.js の描画方式変更**（最大の工数）~~ → **不要になった（2026-08-17 決定・
      2026-08-19 実施）**: Web Components の全廃決定により、描画方式の変更ではなく
      **ファイルの撤去**になった（語彙モデル §8.4 の第4段）。
      後継は2系統で、どちらもインライン `style=` を生成しない——**計算ビューはサーバー事前描画**
      （Goが `.vocab-chrome` の中身を組み立てる）、**編集中の付帯UIはエンハンサ**
      （`createElement` ＋ `textContent` でDOMを組む。実物は `enhanceFileSections`）。
- [x] **(6) strict適用と確認**（2026-08-19 完了）: `cspPolicy` から `script-src`/`style-src` の
      `'unsafe-inline'` を外した（nonce は (2) の方針変更で不要になったため使っていない）。
      `<embed>` PDF・`/data/` 配信・`/assets/` が self で通ることを確認済み。
      後戻り防止として [csp_test.go](../internal/auth/csp_test.go) が
      「`'unsafe-inline'` を含まないこと」と各ディレクティブの存在を固定する。
      E2E（verify-stage4.js）でも JS エラーゼロ・CSP違反ゼロを確認した。

---

## 5. 完了時（strict到達時）の後処理

- [x] **本ファイルの `【考察】` 接頭辞は「外さず維持」と決めた（2026-08-20）。**
      当初は「strict 到達時に `docs/CSP強化.md` へリネームする」と書いていたが、
      [開発方針.md](開発方針.md) §2 を読み直して**維持**に改めた。理由は3つ:
      1. 開発方針 §2 は「`【考察】` は**状態（検討中・未実装）マーカー**として、
         **型接頭辞の前に重ねられる**」（例: `【考察】【ユースケース】○○` → 実装後に
         `【ユースケース】○○`）としたうえで、「**実装済みになっても考察メモは記録として
         接頭辞を維持します**」と明記している。本ファイルは型接頭辞を持たない**純粋な考察メモ**
         なので、後者に当たる。
      2. 本文の大半（§3 の棚卸し、§4 の nonce 案を採らなかった判断、`style=` を個別除去せず
         機構撤去に相乗りさせた判断）は**設計判断の経緯**であり、恒久的な仕様書ではない。
         考察メモとしての性格は strict 到達後も変わらない。
      3. 実務上、リネームは本ファイルへの相対リンク（2026-08-20 実測で**他の13ファイル・16箇所**。
         `docs/` 各書・`README.md`・`internal/auth/middleware.go` のコメントを含む）の
         一斉張り替えを伴う。得るものが接頭辞1つの整合に対して代償が大きい。
      → **現行仕様として CSP の値を知りたい読者への入口**は §2.1 と
      [認証認可設計.md](認証認可設計.md)・[本文サニタイズ設計.md](本文サニタイズ設計.md) §6 とし、
      本ファイルは「なぜそのCSPになったか」の記録に徹する。
- [ ] `docs/作業引き継ぎ.md` の該当予約タスク（「1.5 CSP を strict へ格上げ」）を
      strict 完了として畳む。**本ファイルの担当範囲外のため未実施**。
- [x] [本文サニタイズ設計.md](本文サニタイズ設計.md) §6（CSPとの関係）を strict 到達後の記述へ更新
      （2026-08-20 完了）。同 §6 は「strict 化は完了済み」を明記し、**本文に `style=` を通す例外を
      作ると `style-src 'self'` を緩めることになるのでセットで維持する**という含意まで書かれている。
      本ファイル §1.1 も同様に更新済み。
- [x] `docs/開発方針.md` §4 のインライン禁止ルールが新規コードで守られているか確認
      （2026-08-20 に機械走査で再確認: `assets/*.html`・ログイン画面テンプレートともに
      `on*=`・`style=`・インライン `<script>`/`<style>` は**すべて0件**）。

---

## 6. 参考

- 実装: [internal/auth/middleware.go](../internal/auth/middleware.go)（`CSPProtect`・`cspPolicy`）、
  [cmd/w-cms/main.go](../cmd/w-cms/main.go)（チェーン組み込み）。
- 関連ルール: [開発方針.md](開発方針.md) §4（新規コードのインライン禁止）。
- 認可・CSRF等のミドルウェア設計: [認証認可設計.md](認証認可設計.md)。
