# w-cms ソフトウェアコンセプト・設計仕様書

このドキュメントでは、**w-cms** の基本コンセプトである「HTMLファイルベースのデータ管理と、データベースによる可変タグ・原価情報の双方向連携」の仕様について定義します。

---

## 1. コア・コンセプト：HTMLファイル（ドキュメント）とDBの双方向連携

w-cms は、ドキュメントの本文データや構造情報をデータベース（SQLite）に格納しません。また、独自のJSONファイルも作成しません。**「ドキュメントの本体はすべて標準的な『HTMLファイル（index.html）』として物理保存し、検索・集計に必要な可変タグや取引データのみをデータベースにインデックス登録する」**という設計を採用します。

```mermaid
graph TD
    subgraph Editor [Notion風ブロックエディタ]
        E1[ドキュメント編集/HTML読み書き]
    end

    subgraph Storage [ファイルシステム (物理ストレージ)]
        F1["data/master/xx/xxxxx/index.html (HTML本体)"]
        F2["data/master/xx/xxxxx/images/ (写真・添付ファイル)"]
    end

    subgraph DB [SQLite データベース]
        T1["page_tags テーブル (可変属性名:値)"]
        T2["各種取引テーブル (見積/発注/製造ログ等)"]
    end

    E1 -- 1. HTML保存 (Save) --> F1
    F1 -- 2. HTMLの解析・同期 (Sync) --> DB
    DB -- 3. 動的データを反映 (Load) --> E1
    F1 -- 4. 編集時にHTMLをロード --> E1
```

### ① HTMLファイルへの統一（人間と機械のインターフェース）
ドキュメントはすべて単一の `index.html` として保存されます。
*   **人間のメリット**: 特別なアプリがなくても、Webブラウザやスマートフォンでそのまま中身を閲覧・印刷できます。また、標準的なHTMLエディタやWYSIWYGエディタで直接手動編集することも可能です。
*   **機械（システム）のメリット**: HTMLは構造化されており、Go言語の標準的なHTMLパーサー（`golang.org/x/net/html`）を使用して、機械的に容易にデータ抽出（パース）が可能です。
*   *保存先*: `data/master/26/260603-103/index.html`

### ② HTML内のブロック表現とDB登録（インプット）
Notion風ブロックエディタで編集した内容は、セマンティックなHTMLタグおよびカスタムタグとして `index.html` にシリアライズ（出力）されます。
*   **標準ブロック**: 見出しは `<h1>`〜`<h6>`、本文は `<p>`、画像は `<img>` として保存。
*   **4つの取引書類ブロック**: 専用のカスタムHTMLタグ（例: `<m-our-estimate>` や `<m-client-order>`）として属性付きで保存。
*   **可変タグブロック**: メタデータ属性を記述するカスタムタグ（例: `<m-tag name="支払条件" value="通常">`）として保存。

ファイル保存時、GoのバックエンドがこのHTMLファイルをスキャンし、カスタムタグの内容を抽出してデータベース（SQLite）の各種テーブルに即座に同期（インデックス登録）します。

### ③ DBからHTML画面への動的反映（アウトプット）
詳細ページを表示する際、静的なHTML（`index.html`）をロードした上で、データベースから動的ブロック（利益計算やグラフなど）の最新データを引き出して、ページ内にリアルタイムで埋め込んで表示します。

---

## 2. HTML上のブロック表現仕様（マークアップ例）

このシステムで保存される `index.html` は、以下のように標準的なHTMLタグとカスタムタグが融合した形になります。

```html
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>トーアスポーツマシーン様発注書</title>
</head>
<body>
    <!-- 1. 可変タグブロック (Name: Value) -->
    <m-tag name="発注元" value="株式会社トーアスポーツマシーン"></m-tag>
    <m-tag name="希望納入日" value="2026-06-10"></m-tag>
    <m-tag name="支払条件" value="通常"></m-tag>
    <m-tag name="自社担当" value="紀平"></m-tag>

    <h1>トーアスポーツマシーン様発注書（PO-260603-103）</h1>
    <p>以下の通り、各マシーン用の部材を発注いたします。</p>

    <!-- 2. 取引データブロック（例: 顧客の発注書データ） -->
    <!-- 明細項目をカスタムタグで記述し、人間用には通常の<table>で表示します -->
    <m-client-order item-id="W120-P180-05-03A" item-name="側板" price="1197" quantity="20"></m-client-order>
    <m-client-order item-id="W120-P180-06-03A" item-name="側板" price="947" quantity="20"></m-client-order>
    <m-client-order item-id="X008-123-2" item-name="ﾓｰﾀｰｶｯﾌﾟﾘﾝｸﾞ" price="3500" quantity="20"></m-client-order>

    <!-- 3. 写真（画像）ブロック -->
    <h3>側板（SW側）の加工写真</h3>
    <img src="images/setup_plate.jpg" alt="側板セット状態">
</body>
</html>
```

---

## 3. 実装に向けたデータモデル設計（案）

データベースは、HTMLファイルから抽出した各種インデックスのみを保持します。

### ① `pages` テーブル（ドキュメントのインデックス）
*   `id` (TEXT, PRIMARY KEY): ページID (例: `"260603-103"`)
*   `title` (TEXT): ページタイトル
*   `file_path` (TEXT): 物理HTMLファイルの保存先パス (例: `"data/master/26/260603-103/index.html"`)
*   `updated_at` (DATETIME): 更新日時

### ② `page_tags` テーブル（可変属性インデックス）【可変タグ式】
*   `page_id` (TEXT, FOREIGN KEY): `pages.id` に紐づく
*   `name` (TEXT): 属性の名前（キー） (例: `"発注元"`, `"希望納入日"`, `"支払条件"`)
*   `value` (TEXT): 属性の値 (例: `"株式会社トーアスポーツマシーン"`, `"2026-06-10"`, `"通常"`)

### ③ `our_estimates` テーブル（「弊社の見積もり」インデックス）
*   `id` (INTEGER, PRIMARY KEY AUTOINCREMENT)
*   `item_id` (TEXT): 製品ID（図面番号など）
*   `client_name` (TEXT): 見積提示先の顧客名
*   `price` (INTEGER): 見積単価
*   `source_page_id` (TEXT, FOREIGN KEY): 抽出元HTMLのページID
*   `estimated_at` (DATE): 見積日

### ④ `client_orders` テーブル（「顧客の発注書」インデックス）
*   `id` (INTEGER, PRIMARY KEY AUTOINCREMENT)
*   `item_id` (TEXT): 製品ID
*   `client_name` (TEXT): 発注元顧客名
*   `price` (INTEGER): 受注単価（売上）
*   `quantity` (INTEGER): 数量
*   `source_page_id` (TEXT, FOREIGN KEY): 抽出元HTMLのページID
*   `ordered_at` (DATE): 受注日

### ⑤ `supplier_estimates` テーブル（「材料屋・加工業者の見積もり」インデックス）
*   `id` (INTEGER, PRIMARY KEY AUTOINCREMENT)
*   `item_name` (TEXT): 材料名、または外注加工内容
*   `supplier_name` (TEXT): 仕入・加工先の名前
*   `cost` (INTEGER): 予定原価
*   `source_page_id` (TEXT, FOREIGN KEY): 抽出元HTMLのページID
*   `estimated_at` (DATE): 見積日

### ⑥ `our_orders` テーブル（「弊社の発注書」インデックス）
*   `id` (INTEGER, PRIMARY KEY AUTOINCREMENT)
*   `item_name` (TEXT): 材料名、または外注加工内容
*   `supplier_name` (TEXT): 発注先の名前
*   `cost` (INTEGER): 実績原価
*   `quantity` (INTEGER): 数量
*   `source_page_id` (TEXT, FOREIGN KEY): 抽出元HTMLのページID
*   `ordered_at` (DATE): 発注日

---

## 4. 今後の開発ロードマップ

1.  **HTMLマークアップの仕様確定**:
    *   カスタムタグ（`<m-tag>`, `<m-our-estimate>`等）の正確な属性設計。
2.  **HTMLパーサー（parser.go）の書き換え**:
    *   HTMLファイルから上記カスタムタグを検出し、Go構造体（メタデータ）にパースする処理の実装。
3.  **データベース（sqlite.go）のインデックス化**:
    *   上記の「4つの取引書類」および可変タグ用テーブルの初期化。
4.  **データ同期ロジック（sync.go）の拡張**:
    *   HTMLアップロード時に、パーサーが抽出したデータをDBの各テーブルへ同期する処理の実装。
5.  **フロントエンド（エディタ）の実装**:
    *   ブラウザ上でHTMLを直接ロードし、Notion風ブロックエディタ形式で編集してHTMLとして書き戻すフロントエンドUIの構築。
