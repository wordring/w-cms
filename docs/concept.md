# w-cms ソフトウェアコンセプト・設計仕様書

このドキュメントでは、**w-cms** の基本コンセプトである「自由なファイルベース保存と、Web Components（HTML5カスタムエレメント）を活用した双方向データ連携」の仕様について定義します。

---

## 1. コア・コンセプト：HTMLファイルとWeb Componentsによる自律型双方向連携

w-cms は、ドキュメントの本文データや構造情報をデータベース（SQLite）に格納しません。**「ドキュメントの本体はすべて標準的な『HTMLファイル（index.html）』として物理保存し、さらにHTML5のWeb Components技術（カスタムエレメント）を用いて、HTML自体が表示・編集UIのロジックを自律的に保持する」**という設計を採用します。

```mermaid
graph TD
    subgraph Browser [ブラウザ / アプリ画面]
        UI[自由なテキスト/画像レイアウト]
        FBlock[m-file タグブロック<br>PDFファイル + 意味タグ]
    end

    subgraph Storage [ファイルシステム (物理ストレージ)]
        F1["data/master/xx/xxxxx/index.html (HTML本体)"]
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

### ① HTMLファイルへの集約とWeb ComponentsによるUIの自律描画
すべてのデータやブロック情報は `index.html` に格納されます。
HTML内に記述された定義スクリプト（Web Components）により、ブラウザでHTMLを開いた瞬間、カスタムタグ（`<m-tag>` や `<m-file>`）が自動的に見やすいデザインや編集用フォームへと自律的に描画されます。

### ② OneNote風リアルタイム自動保存（オートセーブ）とDB登録（インプット）
ユーザーがエディタ上で文字入力や値の変更を行うと、**「OneNote」のようにボタンを押すことなく自動で保存されます。**
*   **動作仕様（デバウンス処理）**: タイピング中は保存を保留し、「ユーザーの入力の手が止まって1〜2秒経過した瞬間」または「入力フォームからフォーカスが外れた瞬間」に、バックグラウンドで自動的に `/save` APIへHTMLデータが送信され、物理ファイル（`index.html`）を上書き保存します。
*   上書き保存と同時に、GoのバックエンドがHTMLを再パースし、データベース（SQLite）のタグ情報や取引数値（単価・数量など）をリアルタイムに自動更新（同期）します。

### ③ データベースによる一元集計と動的描画（アウトプット）
データベースにはすべてのドキュメントから抽出されたデータが集約されます。製品詳細ページ等の `「原価・利益算出ブロック」` などの動的ブロックは、ページ表示時に自律的にSQLiteから最新データを取得し、常に最新の粗利益などをリアルタイムに反映します。

### ④ Gemini API によるPDFデータ自動抽出とアシスト入力（OCR機能）
PDF（見積書や発注書）をアップロードすると、バックエンドが裏でGemini API（マルチモーダル機能）を呼び出し、自動でレイアウト解析と文字認識（OCR）を実行します。
Geminiは書類から「品名」「図面番号（製品ID）」「単価」「数量」「取引日付」「書類の種類（タグ）」を構造化データとして自動で抽出し、エディタ上の `<m-file>` タグの属性値（`price`や`quantity`など）に自動で下書き（プリフィル）します。

---

## 2. HTML上のブロック表現とWeb Componentsの仕様

### 2.1. マークアップ例 (`index.html`：自由な配置の例)
固定フォームではなく、通常の文書の中にPDFやタグが自由に配置されている例です。

```html
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>各マシーン用部品の調達と受注記録</title>
    <script src="/assets/web-components.js" defer></script>
</head>
<body>

    <!-- ドキュメント全体の可変メタデータ（どこに配置してもよい） -->
    <m-tag name="自社担当" value="紀平"></m-tag>
    <m-tag name="件名" value="各マシーン用"></m-tag>

    <h1>トーアスポーツマシーン様向けの製品製造に関して</h1>
    <p>本日、潮﨑様より追加発注分の注文書（原本PDF）をいただきました。希望納期は6月10日とのことです。</p>

    <!-- 顧客の発注書PDFを文脈に合わせて配置 -->
    <m-file src="attachments/po_260603.pdf" name="顧客発注書_原本.pdf" 
            tag="顧客の発注書" item-id="W120-P180-05-03A" price="1197" quantity="20"></m-file>

    <p>材料については、以下の材料屋見積書（PDF）を参考に、アイアン素材を確保予定です。</p>

    <!-- 材料見積書PDFを別の場所に配置 -->
    <m-file src="attachments/iron_quote.pdf" name="アイアン素材見積書.pdf" 
            tag="材料屋・加工業者の見積もり" item-name="側板用鋼材" cost="500" supplier-name="東邦金属工業"></m-file>

    <hr>
    <h3>この製品の原価・利益シミュレーション（自動計算）</h3>
    <!-- 動的利益計算ブロック (DBからデータをロードして表示) -->
    <m-profit-calculator item-id="W120-P180-05-03A"></m-profit-calculator>

</body>
</html>
```

### 2.2. Web Componentsの実装例 (`web-components.js` の `<m-file>` 部分)
PDF等のファイルをダウンロードリンクとして綺麗に描画し、編集時には属性（単価や数量）をフォームとして編集できるUIを自動生成します。

```javascript
class MFile extends HTMLElement {
    static get observedAttributes() { return ['src', 'name', 'tag', 'price', 'quantity', 'cost']; }
    connectedCallback() { this.render(); }
    attributeChangedCallback() { this.render(); }

    render() {
        const src = this.getAttribute('src') || '';
        const name = this.getAttribute('name') || '添付ファイル';
        const tag = this.getAttribute('tag') || '未分類';
        const isEdit = document.body.hasAttribute('edit-mode');

        // タグの種類に応じたカラーやアイコンを設定
        let tagColor = '#6c757d';
        if (tag === '顧客の発注書') tagColor = '#28a745';
        if (tag === '弊社の発注書') tagColor = '#dc3545';
        if (tag === '材料屋・加工業者の見積もり') tagColor = '#ffc107';
        if (tag === '弊社の見積もり') tagColor = '#007bff';

        let innerHTML = `
            <div class="file-block" style="border: 1px solid #ddd; border-left: 5px solid ${tagColor}; border-radius: 4px; padding: 10px; margin: 10px 0; background: #fafafa; font-family: sans-serif;">
                <div style="display: flex; align-items: center; justify-content: space-between;">
                    <div>
                        <span style="font-size: 12px; font-weight: bold; color: ${tagColor}; background: ${tagColor}15; padding: 2px 6px; border-radius: 4px; margin-right: 8px;">${tag}</span>
                        <a href="${src}" target="_blank" style="text-decoration: none; color: #0066cc; font-weight: bold;">📄 ${name}</a>
                    </div>
                    <div>
                        <a href="${src}" download style="font-size: 12px; text-decoration: none; background: #eee; padding: 4px 8px; border-radius: 4px; color: #333;">ダウンロード</a>
                    </div>
                </div>
        `;

        if (isEdit) {
            // 編集モード時のフォーム表示
            innerHTML += `<div style="margin-top: 8px; padding-top: 8px; border-top: 1px dashed #eee; font-size: 13px; color:#555;">`;
            if (tag === '顧客の発注書' || tag === '弊社の見積もり') {
                const price = this.getAttribute('price') || '0';
                const quantity = this.getAttribute('quantity') || '1';
                innerHTML += `
                    単価: <input type="number" value="${price}" oninput="this.getRootNode().host.setAttribute('price', this.value)" style="width: 80px; margin-right:10px;">
                    数量: <input type="number" value="${quantity}" oninput="this.getRootNode().host.setAttribute('quantity', this.value)" style="width: 60px;">
                `;
            } else if (tag === '材料屋・加工業者の見積もり' || tag === '弊社の発注書') {
                const cost = this.getAttribute('cost') || '0';
                innerHTML += `
                    単価(原価): <input type="number" value="${cost}" oninput="this.getRootNode().host.setAttribute('cost', this.value)" style="width: 80px;">
                `;
            }
            innerHTML += `</div>`;
        }

        innerHTML += `</div>`;
        this.innerHTML = innerHTML;
    }
}
customElements.define('m-file', MFile);
```

---

## 3. 実装に向けたデータモデル設計（案）

データベーステーブルに、PDFファイルへの参照パスを保持するためのカラム（`pdf_path`）などを拡張します。

### ① `pages` テーブル（ドキュメントのインデックス）
*   `id` (TEXT, PRIMARY KEY): ページID
*   `title` (TEXT): ページタイトル
*   `file_path` (TEXT): 物理HTMLファイルの保存先パス (例: `"data/master/26/260603-103/index.html"`)
*   `updated_at` (DATETIME): 更新日時

### ② `page_tags` テーブル（可変属性インデックス）
*   `page_id` (TEXT, FOREIGN KEY): `pages.id` に紐づく
*   `name` (TEXT): 属性の名前 (例: `"発注元"`, `"希望納入日"`)
*   `value` (TEXT): 属性の値 (例: `"株式会社トーアスポーツマシーン"`, `"2026-06-10"`)

### ③ `our_estimates` テーブル（「弊社の見積もり」インデックス）
*   `id` (INTEGER, PRIMARY KEY AUTOINCREMENT)
*   `item_id` (TEXT): 製品ID（図面番号等）
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
    *   `<m-tag>` および `<m-file>`（タグ付きファイルブロック）のUI表現と、編集時のデータバインディングの実装。
2.  **HTMLパーサー（parser.go）の書き換え**:
    *   HTMLから `<m-tag>` や `<m-file tag="...">` の各種属性値（単価、パス等）をパースする処理の実装。
3.  **データベース（sqlite.go）のインデックス化**:
    *   `pdf_path` カラムを拡張した各取引テーブルの初期化。
4.  **データ同期ロジック（sync.go）の実装**:
    *   HTMLファイル保存時、パーサーが抽出した属性データ（PDFパス含む）をDBへ同期する処理の実装。
5.  **フロントエンド（エディタの自動保存機能）の実装**:
    *   ブラウザ上でHTMLをロードし、`edit-mode`を付与して編集させ、キー入力停止後1〜2秒で自動的にHTMLソースをバックエンドに保存（オートセーブ）するデバウンス処理の実装。
6.  **DB再構築（リビルド）機能の実装**:
    *   DBファイルが紛失・破損した際、物理ストレージ（`data/master`）内のHTMLファイルを再帰的に走査・再パースして、原本PDFへの参照と数値をDBへ100%完全復旧するバッチ・管理処理の実装。
7.  **Gemini API によるPDF自動解析・OCR機能の実装**:
    *   PDFアップロード時にバックエンドでGemini APIを呼び出し、ドキュメントのOCRおよび構造化データ（JSON）を自動抽出してエディタ側に返す連携APIの実装。
