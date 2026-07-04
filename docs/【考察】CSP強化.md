# 【考察】CSP強化

w-cms へ Content-Security-Policy（CSP）を全レスポンスに付与し、保存済みXSS等への
多層防御、クリックジャッキング（`frame-ancestors`）防止、外部オリジンへのデータ送信の
遮断を得るための設計・実装計画の**正本**です。着手前に必ず本ファイルを読むこと。

> 状態: **段階移行の第1段（中間版CSP）を実装・反映済み**。strict 版（`'unsafe-inline'` なし）への
> 格上げは前提リファクタが必要で未着手（`【考察】` 接頭辞は strict 到達まで維持）。
> 最終更新: 2026-07-04

---

## 1. 目的と脅威モデル

- **保存済みXSSへの多層防御**: 本文・カスタム要素はユーザー入力由来のHTMLを含みうる。
  万一スクリプトが混入しても、CSP で `script-src` を絞れば実行を抑止できる。
- **クリックジャッキング防止**: `frame-ancestors 'self'` で他サイトからの iframe 埋め込みを拒否。
- **外部送信の遮断**: `default-src 'self'` で、注入されたコードが外部オリジンへ
  データを送る・外部リソースを読み込む経路を塞ぐ。

### 1.1 前提の補正（実地調査で判明した事実・重要）

過去メモには「主経路はサニタイズ＋`/api/load` の text/plain 化で既に封鎖済み」との記述が
あったが、**現ブランチの実態は異なる**:

- `/api/load` は `text/html; charset=utf-8` を返している（[handler.go](../internal/cms/handler.go)
  `LoadAPIHandler`）。text/plain 化は**未実施**。
- Go側に本文サニタイザは**存在しない**。`docs/本文サニタイズ設計.md` も**未作成**。

したがって本CSPは「サニタイズ済みの上での belt-and-suspenders」ではなく、現状では
**実効的なハードニングの主力**である。将来サニタイズ／text/plain 化を別途入れる場合は
本ファイルからの相互参照を張ること（それらは本CSPとは独立のトピック）。

---

## 2. ポリシー

### 2.1 目標（strict）

```
Content-Security-Policy: default-src 'self'; object-src 'self'; base-uri 'self'; frame-ancestors 'self'
```

`'unsafe-inline'` を一切含まない。`script-src`/`style-src` は `default-src 'self'` を継承し、
インラインの script/style/`on*=` は全てブロックされる。

### 2.2 中間版（今回実装・反映済み）

```
Content-Security-Policy: default-src 'self'; script-src 'self' 'unsafe-inline'; style-src 'self' 'unsafe-inline'; object-src 'self'; base-uri 'self'; frame-ancestors 'self'
```

- `default-src` / `object-src` / `base-uri` / `frame-ancestors` は**目標strictと同値**。
  外部送信遮断・クリックジャッキング防止の実利を、中間段階でも確保する。
- `script-src` / `style-src` のみ暫定で `'unsafe-inline'` を許可し、現行フロントの
  多数のインライン script/style/`on*=` を生かす。strict化でこの2つの `'unsafe-inline'` を外す。

実装: `internal/auth/middleware.go` の `CSPProtect`（`cspPolicy` 定数）。
[cmd/w-cms/main.go](../cmd/w-cms/main.go) のチェーン最外周
（`auth.CSPProtect(auth.CSRFProtect(root))`）で全レスポンスへ付与。

### 2.3 なぜ `default-src 'self'` で壊れないか（実測）

`assets/` 配下に外部リソース参照・`data:`/`blob:` URI・web fonts・`<link>`・`@import` は
**皆無**（実測）。PDFは `<embed src="/data/master/...">` の同一オリジン配信で
`object-src 'self'` を通る。よって `default-src 'self'` はインライン以外を一切壊さない。

---

## 3. インライン依存の棚卸し（strict格上げの対象・実測）

strict化には、以下の全インラインの除去／外部化が前提。素でstrictを入れると編集機能が全停止する。

| 箇所 | 種別 | 実測 |
| --- | --- | --- |
| `assets/index.html` | インライン `<script>` | 2つ（FOUC用 head 13–26／本体 922–2363） |
| `assets/index.html` | `<style>` ブロック | 1つ（27–724） |
| `assets/index.html` | `on*=` ハンドラ | 11個 |
| `assets/index.html` | `style=` 属性 | 12個 |
| `assets/web-components.js` | `innerHTML` 描画＋動的 `style=` 生成 | `style=`/`innerHTML` 系 43箇所 |
| `assets/templates/*.html`（12ファイル） | `on*=`／`style=`（**保存本文としてレンダされる**） | 各ファイルに多数 |

`assets/templates/*.html` は `web-components.js` が `fetch`→変数置換→`innerHTML` で
描画するカスタム要素（`m-item`・`m-file`・`m-material` 等）のテンプレート。
strict CSP は **`innerHTML` 経由で挿入した `on*=` 属性ハンドラも実行しない**点に注意
（DOM生成方法に関わらず、インラインイベントハンドラは `script-src 'unsafe-inline'` が無いとブロックされる）。
同様に `style=` 属性は `style-src 'unsafe-inline'` が無いとブロックされる。

---

## 4. strict格上げの前提リファクタ・チェックリスト（次回作業）

- [ ] **(1) 本体スクリプトの外部化**: `index.html` 本体 `<script>`（922–2363）を
      `/assets/` 配下の外部 `.js`（＝self）へ移設。グローバル関数参照（`on*=` から呼ばれる
      関数）は (3) の addEventListener 化とセットで解消。
- [ ] **(2) FOUC head スクリプトを per-request nonce で残す**: head の
      `<script>`（13–26、サイドパネル開閉状態を描画前に確定）はチラつき防止のため
      外部化せず `<script nonce=...>` で残す。
      **nonce配線**: `CSPProtect` が乱数nonceを生成 → `context` へ格納し、ヘッダの
      `script-src` に `'nonce-…'` を追加 → `RootHandler`（[handler.go](../internal/cms/handler.go)）が
      `context` からnonceを読み、`index.html` のプレースホルダへ注入。
      （nonce方式は**属性ハンドラ `on*=` を救えない**ので、(3) は別途必須。）
- [ ] **(3) `on*=` の除去**: `index.html` 11個＋テンプレート12ファイルの `on*=`
      （`oninput`/`onchange`/`onclick`/`onmouseover`/`onfocus`/`onblur` 等）を
      addEventListener／イベント委譲へ置換。テンプレートは `innerHTML` 後に
      `querySelector` で配線する方式へ寄せる。
- [ ] **(4) `style=` / `<style>` の除去**: `<style>` ブロック（27–724）→外部 `.css`。
      `style=` 属性（index.html 12＋web-components/テンプレート多数）を除去。
      静的装飾はCSSクラス、動的値は描画後に `element.style.x = y`（CSSOM経由なら
      strict CSPでもブロックされない）。
- [ ] **(5) web-components.js の描画方式変更**（最大の工数）: 「HTML文字列＋`innerHTML`」を
      「クラス付与＋CSSOMワイヤリング」へ。ステータス色分け等は `data-status` 属性＋CSSで表現。
- [ ] **(6) strict適用と確認**: `cspPolicy` から `script-src`/`style-src` の
      `'unsafe-inline'` を外す（nonce方式なら `script-src 'self' 'nonce-…'`）。
      `<embed>` PDF・`/data/` 配信・`/assets/` が self で通ることを確認。

---

## 5. 完了時（strict到達時）の後処理

- [ ] 本ファイルの `【考察】` 接頭辞を外す（`docs/CSP強化.md` へリネーム。`docs/開発方針.md` §2 準拠）。
- [ ] `docs/作業引き継ぎ.md` の該当予約タスクを更新（strict完了として畳む）。
- [ ] `docs/本文サニタイズ設計.md` が存在すれば §6 を更新（本CSPとの関係を追記）。
- [ ] `docs/開発方針.md` §4 のインライン禁止ルール（今回追加）が新規コードで守られているか確認。

---

## 6. 参考

- 実装: [internal/auth/middleware.go](../internal/auth/middleware.go)（`CSPProtect`・`cspPolicy`）、
  [cmd/w-cms/main.go](../cmd/w-cms/main.go)（チェーン組み込み）。
- 関連ルール: [開発方針.md](開発方針.md) §4（新規コードのインライン禁止）。
- 認可・CSRF等のミドルウェア設計: [認証認可設計.md](認証認可設計.md)。
