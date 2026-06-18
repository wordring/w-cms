# w-cms ソフトウェアコンセプト・設計仕様書

このドキュメントでは、**w-cms** の基本コンセプトである「自由なファイルベース保存と、タグ（名前：値）による意味付けをベースとした双方向データ連携」の仕様について定義します。

---

## 1. コア・コンセプト：自由な記述と「タグ」によるデータの意味付け

w-cms は、入力項目が固定された硬直的な入力フォーム（帳票システム）を排除します。**「OneNoteのように、テキストや画像を自由なレイアウトでフラットな数字IDのページに記述し、PDF等の原本ファイルを好きな場所に配置し、タグを付与することでその情報の意味（親ページ、発注書、見積書など）をデータベースに認識させる」**という自律型データ連携を採用します。

```mermaid
graph TD
    subgraph Browser [ブラウザ / アプリ画面]
        UI[自由なテキスト/画像レイアウト]
        FBlock[m-file タグブロック<br>PDFファイル + 意味タグ]
    end

    subgraph Storage [ファイルシステム (物理ストレージ)]
        F1["data/master/xx/xxxxx/xxxxx.html (ID名のHTML本体)"]
        F2["data/master/xx/xxxxx/attachments/ (PDF等)"]
    end

    subgraph DB [SQLite データベース]
        T1["page_tags テーブル (可変属性名:値)"]
        T2["各種取引インデックス (見積/発注/PDFファイルパス)"]
    end

    UI -- "1. 自動保存 (Auto-Save: デバウンス)" --> F1
    FBlock -- 2. PDF保存とHTML記述 --> F2
    F1 -- 3. HTML解析・タグに基づくDB登録 (Sync) --> DB
    DB -- 4. 最新の原価・利益をロード --> UI
```

### ① すべてがフラットな「ただのメモ（ページ）」とファイル名
システム上には「顧客ページ」「製品ページ」といった特有のページ種別や専用フォルダ階層は存在しません。すべてのページはフラットな数字IDのフォルダに格納された、同一の構造を持つHTMLファイルです。
*   **自動採番仕様**: ページID（ファイル名）は、**新規ページ作成時に連番の10進数（例: `00001`, `00002`...）で自動的に付与されます。**
*   **ファイル名仕様**: これがそのまま物理ファイル名（例: `00001.html`）およびページIDとして使用されます。これにより、ファイルシステム上での検索や特定が非常に容易になります。
*   *保存先パスの例*: `data/master/00/00001/00001.html`

### ② 記述内容によって「役割」が動的に決まる
白紙のメモに、ユーザーが何を書くかによってページの役割が決まります。
*   `<m-tag name="顧客名" value="〇〇">` を埋め込めば、そのページは「顧客のページ」として機能します。
*   `<m-file tag="顧客の発注書">` を配置すれば、「受注記録」としての役割を持ちます。

### ③ タグによる「仮想的な親子関係（階層）」の表現
「顧客 ＞ 完成品機種 ＞ 部品」のような階層構造も、データベースの固定設計ではなく、ページ内に記述する「親ページを示すタグ」によって表現します。
*   **ルール**: 親ページを指定するタグには、**親ページのページID（数字）をそのまま記入します。**
    ```html
    <!-- ページID "00002" (製品) の中に記述する親 (顧客 "00001") の指定例 -->
    <m-tag name="親ページ" value="00001"></m-tag>
    ```
*   これにより、システムは複雑な階層構造を意識することなく、単に「『親ページ』タグに記載されたIDを順番にたどる」だけで、動的にパンくずリストや階層ツリーを生成できます。

### ④ データベースによる一元集計と時系列に依存しない「非同期集計」
データベースにはすべてのドキュメントから抽出されたデータがフラットに集約されます。
同一の `item_id`（製品コード/プロジェクトID）で紐付いていれば、いつどの書類（発注書や見積書）が登録されても、データベースが自動で名寄せして原価と売上を集計します。

### ⑤ Gemini API によるPDFデータ自動抽出とアシスト入力（OCR機能）
PDF（見積書や発注書）をアップロードすると、バックエンドが裏でGemini API（マルチモーダル機能）を呼び出し、自動でレイアウト解析と文字認識（OCR）を実行します。
Geminiは書類から「品名」「図面番号（製品ID）」「単価」「数量」「取引日付」「書類の種類（タグ）」を構造化データとして自動で抽出し、エディタ上の `<m-file>` タグの属性値に自動で下書き（プリフィル）します。

---

## 2. HTML上のブロック表現とWeb Componentsの仕様

### 2.1. マークアップ例 (`data/master/26/260603-103/260603-103.html` の例)
固定フォームではなく、通常の文書の中にPDFやタグが自由に配置されている例です。

```html
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>開発案件：新型シャフトの試作記録</title>
    <!-- Web Components定義へのパス（階層に合わせた相対パス） -->
    <script src="../../../../assets/web-components.js" defer></script>
</head>
<body>

    <!-- パンくずリスト（親ページタグに書かれたID "260603-100" をたどって階層リンクを自動生成） -->
    <m-breadcrumbs></m-breadcrumbs>

    <!-- 親ページのIDを指定するタグ -->
    <m-tag name="親ページ" value="260603-100"></m-tag>
    <m-tag name="案件区分" value="受託開発"></m-tag>
    <m-tag name="自社担当" value="佐藤"></m-tag>

    <h1>新型シャフト（試作コード: DEV-SHAFT-99）の開発と調達記録</h1>
    <p>メーカー側からの正式発注は試作評価後となりますが、先行して部材調達および試作加工を開始します。</p>

    <p>先行して手配した鋼材の材料見積書（PDF）です。</p>
    <m-file src="attachments/material_shaft.pdf" name="シャフト用鋼材見積.pdf" 
            tag="材料屋・加工業者の見積もり" item-name="特殊鋼材" cost="2500" supplier-name="東邦金属工業"></m-file>

    <p>【更新履歴 2026-07-01】評価合格に伴い、メーカーより正式な発注書を受領しました。以下に原本を添付します。</p>
    <m-file src="attachments/po_shaft.pdf" name="正式発注書_トーア.pdf" 
            tag="顧客の発注書" item-id="DEV-SHAFT-99" price="8000" quantity="10"></m-file>

    <hr>
    <h3>このプロジェクトの原価・利益推移</h3>
    <m-profit-calculator item-id="DEV-SHAFT-99"></m-profit-calculator>

</body>
</html>
```

---

## 3. 実装に向けたデータモデル設計（案）

データベーステーブルは、HTML上のカスタムタグから抽出した各種インデックス情報を保持します。

### ① `pages` テーブル（ドキュメントのインデックス：完全にフラット）
*   `id` (TEXT, PRIMARY KEY): ページID（数字IDなど）
*   `title` (TEXT): ページタイトル
*   `file_path` (TEXT): 物理HTMLファイルの保存先パス (例: `"data/master/26/260603-103/260603-103.html"`)
*   `updated_at` (DATETIME): 更新日時

### ② `page_tags` テーブル（可変属性・階層インデックス）
*   `page_id` (TEXT, FOREIGN KEY): `pages.id` に紐づく
*   `name` (TEXT): 属性の名前 (例: `"親ページ"`, `"案件区分"`, `"自社担当"`)
*   `value` (TEXT): 属性の値 (例: `"260603-100"`, `"受託開発"`, `"佐藤"`) -- ※"親ページ"の場合、値は親のページID（数字）になる

### ③ `our_estimates` テーブル（「弊社の見積もり」インデックス）
*   `id` (INTEGER, PRIMARY KEY AUTOINCREMENT)
*   `item_id` (TEXT): 製品ID（図面番号/プロジェクトID等）
*   `client_name` (TEXT): 見積提示先の顧客名
*   `price` (INTEGER): 見積単価
*   `pdf_path` (TEXT)
*   `page_id` (TEXT, FOREIGN KEY)
*   `estimated_at` (DATE)

### ④ `client_orders` テーブル（「顧客の発注書」インデックス）
*   `id` (INTEGER, PRIMARY KEY AUTOINCREMENT)
*   `item_id` (TEXT)
*   `client_name` (TEXT)
*   `price` (INTEGER)
*   `quantity` (INTEGER)
*   `pdf_path` (TEXT)
*   `page_id` (TEXT, FOREIGN KEY)
*   `ordered_at` (DATE)

### ⑤ `supplier_estimates` テーブル（「材料屋・加工業者の見積もり」インデックス）
*   `id` (INTEGER, PRIMARY KEY AUTOINCREMENT)
*   `item_name` (TEXT)
*   `supplier_name` (TEXT)
*   `cost` (INTEGER)
*   `pdf_path` (TEXT)
*   `page_id` (TEXT, FOREIGN KEY)
*   `estimated_at` (DATE)

### ⑥ `our_orders` テーブル（「弊社の発注書」インデックス）
*   `id` (INTEGER, PRIMARY KEY AUTOINCREMENT)
*   `item_name` (TEXT)
*   `supplier_name` (TEXT)
*   `cost` (INTEGER)
*   `quantity` (INTEGER)
*   `pdf_path` (TEXT)
*   `page_id` (TEXT, FOREIGN KEY)
*   `ordered_at` (DATE)

---

## 4. 今後の開発ロードマップ

1.  **Web Components（web-components.js）の基本定義**:
    *   `<m-tag>`、`<m-file>`、および親のページID（数字）をたどって階層リンクを動的に生成する `<m-breadcrumbs>` の実装。
2.  **HTMLパーサー（parser.go）の書き換え**:
    *   HTMLファイル（`[ID].html`）から `<m-tag>` や `<m-file tag="...">` の各種属性値（単価、パス等）をパースする処理の実装。
3.  **データベース（sqlite.go）のインデックス化**:
    *   `pdf_path` カラムを拡張した各取引テーブルの初期化。
4.  **データ同期ロジック（sync.go）の実装**:
    *   HTMLファイル保存時、パーサーが抽出した属性データ（PDFパス含む）をDBへ同期する処理の実装。
5.  **フロントエンド（エディタの自動保存機能）の実装**:
    *   ブラウザ上でHTMLをロードし、`edit-mode`を付与して編集させ、キー入力停止後1〜2秒で自動的にHTMLソースをバックエンドに保存（オートセーブ）するデバウンス処理の実装。
6.  **DB再構築（リビルド）機能の実装**:
    *   DBファイルが紛失・破損した際、物理ストレージ（`data/master`）内の各HTMLファイル（`[ID].html`）を再帰的に走査・再パースして、原本PDFへの参照と数値をDBへ100%完全復旧するバッチ・管理処理の実装。
7.  **Gemini API によるPDF自動解析・OCR機能の実装**:
    *   PDFアップロード時にバックエンドでGemini APIを呼び出し、ドキュメントのOCRおよび構造化データ（JSON）を自動抽出してエディタ側に返す連携APIの実装。
