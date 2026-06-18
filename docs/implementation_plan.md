# 実装計画：Web Components テンプレートの外部ファイル化 (パターン1)

JavaScriptコード内にハードコードされている Web Components（`<m-tag>`, `<m-file>`, `<m-item>`）のHTMLデザイン部分を、個別のHTMLテンプレートファイルへ分離し、動的にロード・キャッシュして適用する設計・実装計画です。

---

## 1. ユーザーレビューが必要な項目

> [!IMPORTANT]
> **外部テンプレートの読み込みとキャッシュ機構**
> 各要素がレンダリングされるたびにサーバーにHTTPリクエストが発生するのを防ぐため、`web-components.js` 内に非同期テンプレートローダーとメモリキャッシュを実装します。1度ロードされたテンプレートはメモリに保持され、2回目以降は瞬時に描画されます。
> 
> **テンプレート内の変数置換**
> 別ファイルのHTML内に記述されたプレースホルダー（例: `${name}`, `${value}`）を、JavaScript側で属性値と動的に置換（補間）して描画します。

---

## 2. 変更予定の詳細

### ① テンプレート用HTMLファイルの新規作成

HTMLデザイン部分を、閲覧用（`view`）と編集用（`edit`）に分けて、`assets/templates/` ディレクトリ配下に作成します。

#### [NEW] [m-tag-view.html](file:///C:/Users/kouic/source/repos/w-cms/assets/templates/m-tag-view.html) / [m-tag-edit.html](file:///C:/Users/kouic/source/repos/w-cms/assets/templates/m-tag-edit.html)
*   `<m-tag>` 用の閲覧/編集モードのHTMLレイアウト。

#### [NEW] [m-file-view.html](file:///C:/Users/kouic/source/repos/w-cms/assets/templates/m-file-view.html) / [m-file-edit.html](file:///C:/Users/kouic/source/repos/w-cms/assets/templates/m-file-edit.html)
*   `<m-file>` 用の閲覧/編集モードのHTMLレイアウト。

#### [NEW] [m-item-view-client.html](file:///C:/Users/kouic/source/repos/w-cms/assets/templates/m-item-view-client.html) / [m-item-view-our.html](file:///C:/Users/kouic/source/repos/w-cms/assets/templates/m-item-view-our.html)
*   `<m-item>` 用の、顧客発注・弊社発注それぞれに応じた閲覧モードのHTMLレイアウト。

#### [NEW] [m-item-edit-client.html](file:///C:/Users/kouic/source/repos/w-cms/assets/templates/m-item-edit-client.html) / [m-item-edit-our.html](file:///C:/Users/kouic/source/repos/w-cms/assets/templates/m-item-edit-our.html)
*   `<m-item>` 用の、顧客発注・弊社発注それぞれに応じた編集モードのHTMLレイアウト。

---

### ② フロントエンド・スクリプトの改修

#### [MODIFY] [web-components.js](file:///C:/Users/kouic/source/repos/w-cms/assets/web-components.js)
*   共通の非同期テンプレートローダー `fetchTemplate(name)` を定義。
*   各コンポーネントの `render()` メソッドを非同期 (`async/await`) 化し、外部テンプレートを取得して属性値を置換した上で `this.innerHTML` に流し込む処理に書き換えます。

---

## 3. 検証計画

### 自動テスト
*   既存 of `go test ./...` を実行し、バックエンドのテストが壊れていないことを確認します。

### 手動テスト
*   ローカルサーバーを起動し、ブラウザで [test.html](http://localhost:8080/assets/test.html) を開きます。
*   閲覧モードと編集モードの切り替え、および入力変更に追従したリアルタイムHTMLシリアライズが従来と全く変わらずに動作し、かつコンポーネントデザインが外部HTMLテンプレートの内容に沿って正常に描画されることを確認します。
