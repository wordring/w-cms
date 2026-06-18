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

---

## 4. 自動保存とデータロードAPI仕様

エディタ（フロントエンド）とファイルシステム・DBを連携させるため、以下の2つのAPIを提供します。

### 4.1. Save API (`POST /api/save`)
フロントエンドからのオートセーブを受け付け、物理ファイルの上書き保存とDB同期を行います。

*   **リクエスト形式**: JSON
    ```json
    {
      "page_id": "00001",  // 新規の場合は空文字 ""
      "html": "<!DOCTYPE html>..." // シリアライズされた最新のHTML文字列
    }
    ```
*   **バックエンド処理フロー**:
    1.  `page_id` が空の場合、`storage.go` の `GenerateNextID()` で新しいページID（例: `00002`）を発番します。
    2.  指定・発番されたIDに基づく物理ディレクトリ（`data/master/...`）を確保します。
    3.  受け取った `html` を `[page_id].html` に書き込み（上書き保存）します。
    4.  `SyncIndex(page_id, html)` を呼び出し、タグや受発注データをDB（SQLite）に再同期します。
*   **レスポンス形式**: JSON
    ```json
    {
      "success": true,
      "page_id": "00002" // フロントエンドに確定したIDを返す
    }
    ```

### 4.2. Load API (`GET /api/load?id={page_id}`)
既存のページデータをエディタに読み込むために使用します。

*   **リクエストパラメータ**: `id` (例: `00001`)
*   **バックエンド処理フロー**:
    1.  `pages` テーブルから対象IDの `file_path` を取得します。
    2.  物理ストレージ上の該当HTMLファイルを読み込みます。
*   **レスポンス**: HTML文字列をそのまま (`text/html` またはプレーンテキストとして) 返却します。
*   （※フロントエンド側は受け取ったHTMLから `<body>` 内のコンテンツのみを抽出し、エディタ内に展開します）

---

## 5. PDF連携API仕様

発注書のPDFファイルを管理・自動解析するためのAPIです。

### 5.1. Upload PDF API (`POST /api/upload-pdf`)
エディタ上にドラッグ＆ドロップされたPDFを受け取り、ページのディレクトリに保存します。

*   **リクエスト形式**: `multipart/form-data`
    *   `page_id`: 保存先のページID (例: `00002`)。※フロントエンドは必ず一度オートセーブを行ってIDを確定させてから呼び出します。
    *   `pdf_file`: アップロードされるバイナリファイル
*   **バックエンド処理フロー**:
    1.  指定された `page_id` のディレクトリ (`data/master/{id}/`) を作成・確認します。
    2.  受信したPDFファイルをディレクトリ内に保存します。
*   **レスポンス**:
    ```json
    {
      "success": true,
      "file_name": "example.pdf",
      "src": "example.pdf"
    }
    ```

### 3.4. PDF解析API (`POST /api/parse-pdf`)

保存済みのPDFをGoogleのGemini API（`gemini-3.5-flash` モデル）に直接送信し、画像や複雑なレイアウトからでも完璧に明細を抽出して返すエンドポイントです。

**前提条件**:
*   サーバーの環境変数 `GEMINI_API_KEY` にGoogle AI StudioのAPIキーが設定されている必要があります。設定されていない場合はエラーになります。

**リクエスト**:
*   **Method**: `POST`
*   **Content-Type**: `application/json`
*   **Body**:
    ```json
    {
      "page_id": "00A1B",
      "file_name": "sample.pdf"
    }
    ```

**内部処理フロー**:
1.  リクエストの `page_id` と `file_name` から、サーバー上のPDFファイルのパスを特定し、バイナリとして読み込みます。
2.  `github.com/google/generative-ai-go/genai` を使用してGeminiクライアントを初期化します。
3.  PDFバイナリを `application/pdf` のBlobとしてモデルに渡し、以下のプロンプト（指示）とともに送信します。
    *   *「このPDFは発注書または見積書です。記載されているすべての部品明細（品名、単価、数量）を抽出し、以下の形式のJSON配列のみを出力してください...」*
4.  Geminiから返却された文字列から、マークダウン装飾（` ```json `）等を取り除き、純粋なJSONテキストを抽出します。
5.  JSONテキストを `ParsedItem` 構造体の配列にパースします。

**レスポンス例 (成功時)**:
```json
{
  "success": true,
  "items": [
    {
      "item_name": "高耐久ギア",
      "price": "15000",
      "quantity": "2"
    }
  ],
  "raw": "[Geminiからの生レスポンステキスト]"
}
```

**レスポンス例 (エラー時)**:
```json
{
  "success": false,
  "message": "Gemini APIの呼び出しに失敗しました: [エラー詳細]"
}
```
※旧バージョン（正規表現ベース）のテキスト抽出機能は廃止されました。

---

## 6. ページID発番アルゴリズム

フロントエンドで新規ページを作成する際、グローバルで一意の連番IDを安全に生成するための仕組みです。

### 6.1. バックエンドのID生成ロジック (`GenerateNextID`)
バックエンド（`storage.go`）では、以下のアルゴリズムでIDを採番します。
1. `pages` テーブルを対象に、`ORDER BY id DESC LIMIT 1` を実行し、現在保存されている最大の `id`（文字列）を取得します（DBの主キーインデックスを利用するため高速です）。
2. もしレコードが1件も存在しない、または取得したIDが空の場合は、初期値として `"000000"` を返します。
3. 取得した最大IDを10進数の数値（`int64`）としてデコードし、`+1` します。
4. 計算後の数値を、指定された桁数（現在は `6桁`）でゼロ埋めフォーマット（`fmt.Sprintf("%06s", ...)`）して返します。

### 6.2. フロントエンドと `/api/new-id` の連携
フロントエンドで「＋ 子ページを作成」ボタンが押された際のフロー：
1. JavaScriptが `GET /api/new-id` を非同期で呼び出します。
2. バックエンドは上記の `GenerateNextID` を実行し、`{"id": "000001"}` のようなJSONを返します。
3. フロントエンドはこのIDを受け取り、`window.open('/000001?edit=true&parent=000000')` のように新しいタブで子ページを開きます。
4. **フォールバック処理**: 万が一ネットワークエラー等でバックエンドのAPIからIDが取得できなかった場合は、安全対策として `Date.now().toString().slice(-6)` （現在のミリ秒タイムスタンプの下6桁）を擬似乱数として使用し、オフライン状態でもページ作成が止まらないよう設計されています。
