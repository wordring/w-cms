# アーキテクチャとDBスキーマ仕様

w-cms は、フロントエンドのWeb Componentsから生成されるHTMLドキュメントと、バックエンドのGoが管理するSQLiteデータベースを連携させることで動作します。このドキュメントでは、そのバックエンド側の詳細な構造とデータの持ち方について解説します。

## 1. 全体アーキテクチャ

システムの中核は、HTMLという「非構造化（あるいは半構造化）データ」を、データベース上の「構造化データ（インデックス）」に同期させる処理にあります。

### Goバックエンドの構成（`internal/`）
*   **`database/sqlite.go`**: 物理フォルダの確保と、Pure Go実装のSQLiteを用いたデータベース・テーブルの初期化を行います。外部キー制約（PRAGMA foreign_keys = ON）を有効にしています。
*   **`cms/handler.go`**: HTTPリクエストを処理します。APIエンドポイント（例: `/api/required-materials`）の実装もここで行われ、フロントエンドのコンポーネントからの非同期データ要求に応答します。
*   **`cms/parser.go`**: `x/net/html` を用いて、エディタから送られてきたHTML文字列を解析（パース）し、特定のカスタムタグ（`<m-tag>`, `<m-file>`, `<m-material>` など）から属性値を抽出します。子要素としてネストされた `<m-item>` も同時に解析し、ヘッダと明細の関係を構築して返却します。
*   **`cms/sync.go`**: パースされたデータをSQLiteの各種テーブルに `INSERT` または更新します。既存のデータを一度 `DELETE` し、新しいHTMLの構造に基づいて再 `INSERT` する UPSERT 的な同期処理を担います。

---

## 2. データベーススキーマ設計

ドキュメント本文はファイルシステムに保存されますが、検索や集計に必要なメタデータはすべてSQLiteにインデックス化されます。

### ドキュメント管理テーブル
*   **`pages`**: すべてのドキュメントの基本情報。
    *   `id` (TEXT, PK): ページID。
    *   `title` (TEXT): HTMLから抽出したタイトル。
    *   `file_path` (TEXT): 物理ファイルの保存先パス。
*   **`page_tags`**: `<m-tag name="..." value="...">` から抽出された可変タグ。
    *   `page_id` (FK), `name`, `value`

### 受発注トランザクションテーブル（ヘッダ・明細構造）
発注書は `<m-file>` をヘッダとし、その中に複数の `<m-item>`（明細）を持つ 1:N の関係です。

*   **`client_orders`** (顧客の発注書 - ヘッダ)
    *   `id`, `order_no` (UNIQUE), `client_name`, `ordered_at`, `pdf_path`, `page_id`
*   **`client_order_items`** (顧客の発注書 - 明細)
    *   `id`, `order_no` (FK), `item_id` (対象部品ID), `item_name`, `price`, `quantity`, `status`
*   **`our_orders`** (自社の発注書 - ヘッダ)
    *   `id`, `order_no` (UNIQUE), `supplier_name`, `ordered_at`, `pdf_path`, `page_id`
*   **`our_order_items`** (自社の発注書 - 明細)
    *   `id`, `order_no` (FK), `item_name` (品目), `cost`, `quantity`, `status`

### マスタ・構成情報テーブル
*   **`part_materials`** (部品の構成部材 - `<m-material>`タグから抽出)
    *   `id`, `part_id` (対象となる親の部品ID), `material_name` (必要な部材), `cost`, `supplier_name`, `quantity` (1部品あたりの必要数), `page_id`

---

## 3. 部材手配計算APIの仕様

動的に必要な部材数を計算するロジックは、`/api/required-materials` エンドポイントで提供されます。
フロントエンドの `<m-required-materials part-id="...">` コンポーネントがこのAPIを叩きます。

### 計算ロジック概要
1.  **必要部材の特定**: `part_materials` テーブルから、指定された `part-id` を構成する部材一覧（`material_name` と 1個あたりの必要数 `quantity`）を取得します。
2.  **受注総数の算出**: `client_order_items` テーブルを検索し、対象の `item_id`（＝ `part_id`）の合計受注数（受注量 `quantity` の合計）を算出します。
3.  **手配済数の算出**: `our_order_items` テーブルから、対象の `material_name` の手配済数量（合計）を算出します。
4.  **不足分の計算**: `(部材の必要数 × 受注総数) - 手配済数` を計算し、「あといくつ発注しなければならないか（不足数）」を割り出してJSONで返却します。

このように、HTMLに書かれた非構造なデータが、バックエンドでリレーショナルなテーブルにパースされることで、複雑なBOM（部品表）計算と進捗管理を自動化しています。
