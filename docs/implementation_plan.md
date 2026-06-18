# 実装計画：発注書への部品ネスト（ぶら下げ）と進捗管理の導入

顧客からの発注書（原本PDF）に付与された発注書番号（Order No）の下に、各部品（図面・製品）を紐づけて（ぶら下げて）管理するデータモデルおよびUI、さらに1ページに複数枚の発注書が配置されるユースケースに対応する実装計画です。

---

## 1. ユーザーレビューが必要な項目

> [!IMPORTANT]
> **マークアップのネスト設計**
> HTML上で「発注書（ファイル）」と「部品」の親子関係を直感的に表すため、`<m-file>` の子要素として `<m-item>` をネストする設計とします。
> ```html
> <m-file tag="顧客の発注書" order-no="PO-2026-001" client-name="トーア" src="..." ...>
>     <m-item item-id="SHAFT-01" item-name="シャフトA" price="8000" quantity="10" status="未着手"></m-item>
>     <m-item item-id="SHAFT-02" item-name="シャフトB" price="12000" quantity="5" status="加工中"></m-item>
> </m-file>
> ```
> 
> **データベース・スキーマのテーブル分割**
> 「顧客の発注書 (`client_orders`)」および「弊社の発注書 (`our_orders`)」は、発注書本体（Header）と部品明細（Items）の親子テーブルに分割し、発注書番号 (`order_no`) でリレーションを張ります。

---

## 2. 変更予定の詳細

### ① ユースケース設計仕様書の作成

#### [NEW] [usecase_cost_and_profit.md](file:///C:/Users/kouic/source/repos/w-cms/docs/usecase_cost_and_profit.md)
*   原価・利益管理におけるタグの使われ方、`item_id` による自動集計ロジックのドキュメント化。

#### [NEW] [usecase_order_and_progress.md](file:///C:/Users/kouic/source/repos/w-cms/docs/usecase_order_and_progress.md)
*   今回実装する、1ページへの複数発注書配置、発注書番号への部品紐づけ、各部品の進捗管理（`status`）に関する仕様書の作成。

---

### ② データベース・スキーマの変更

#### [MODIFY] [sqlite.go](file:///C:/Users/kouic/source/repos/w-cms/internal/database/sqlite.go)
*   `client_orders` と `our_orders` を、HeaderとItemsに分割します。

```sql
-- 顧客の発注書本体（Header）
CREATE TABLE IF NOT EXISTS client_orders (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    order_no TEXT UNIQUE,
    client_name TEXT,
    pdf_path TEXT,
    page_id TEXT,
    ordered_at DATE,
    FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
);

-- 顧客の発注部品明細（Items）
CREATE TABLE IF NOT EXISTS client_order_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    order_no TEXT,
    item_id TEXT,
    item_name TEXT,
    price INTEGER,
    quantity INTEGER,
    status TEXT, -- 未着手, 加工中, 検査中, 納品済 など
    FOREIGN KEY (order_no) REFERENCES client_orders(order_no) ON DELETE CASCADE
);

-- 弊社の発注書本体（Header）
CREATE TABLE IF NOT EXISTS our_orders (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    order_no TEXT UNIQUE,
    supplier_name TEXT,
    pdf_path TEXT,
    page_id TEXT,
    ordered_at DATE,
    FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
);

-- 弊社の発注部品明細（Items）
CREATE TABLE IF NOT EXISTS our_order_items (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    order_no TEXT,
    item_name TEXT,
    cost INTEGER,
    quantity INTEGER,
    status TEXT, -- 未納品, 納品済 など
    FOREIGN KEY (order_no) REFERENCES our_orders(order_no) ON DELETE CASCADE
);
```

---

### ③ HTMLパーサーおよびDB同期処理の改修

#### [MODIFY] [parser.go](file:///C:/Users/kouic/source/repos/w-cms/internal/cms/parser.go)
*   `golang.org/x/net/html` を用いて、`<m-file>` ノードを見つけた際、その内部に存在する `<m-item>` ノード群を子ノードとしてトラバース抽出する処理を追加します。
*   メタデータ抽出構造体（`ParsedPage` 等）を新たに設計し、各見積もり、発注書、発注部品データを完全な構造体として返せるようにします。

#### [MODIFY] [sync.go](file:///C:/Users/kouic/source/repos/w-cms/internal/cms/sync.go)
*   `SyncIndex` において、`pages` テーブルの他、`page_tags`、`client_orders`、`client_order_items`、`our_orders`、`our_order_items`、`our_estimates`、`supplier_estimates` への同期処理を実装します。
*   同期の際は、一旦該当 `page_id` の全インデックスデータを削除（Transaction内）し、最新のHTMLパース結果をインサートすることで「HTMLを唯一の真実（Single source of truth）」とする整合性を維持します。

---

### ④ フロントエンド・コンポーネントの改修

#### [MODIFY] [web-components.js](file:///C:/Users/kouic/source/repos/w-cms/assets/web-components.js)
*   `<m-file>` コンポーネントを更新し、`order-no`、`client-name` / `supplier-name` などの属性の表示・編集をサポートします。
*   `<m-item>` コンポーネントを新規定義し、閲覧モードでは進捗バッジや価格・数量の表示、編集モード（`edit-mode`）では入力フォームを描画するようにします。

#### [MODIFY] [handler.go](file:///C:/Users/kouic/source/repos/w-cms/internal/cms/handler.go)
*   ファイルアップロード時に、`index.html` ではなく `[page_id].html` （例: `00001.html`）に保存されるように修正します。

---

## 3. 検証計画

### 自動テスト
*   `internal/cms/storage_test.go` を更新・拡張し、ネストしたHTMLデータのパースが正しく動作すること、また `SyncIndex` を通して正しくデータベースの親子テーブルにデータが入ることを検証する統合テストコードを追加します。
*   `go test ./...` コマンドでテストを実行し、すべてパスすることを確認します。

### 手動テスト
*   ローカルでサーバーを起動し、ネストされた `<m-file>` と `<m-item>` を含むHTMLファイルをアップロードし、インデックス一覧に正しく階層データ（またはそれに紐づくサマリー）が登録され、SQLiteデータベースの中身が親子関係を保って登録されることを確認します。
