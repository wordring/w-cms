# w-cms ソフトウェアコンセプト・設計仕様書

このドキュメントでは、**w-cms** の基本コンセプトである「自由なファイルベース保存と、タグ（名前：値）による意味付けをベースとした双方向データ連携」の仕様について定義します。

---

## 1. コア・コンセプト：自由な記述と「タグ」によるデータの意味付け

w-cms は、入力項目が固定された硬直的な入力フォーム（帳票システム）を排除します。**「Notionのように、テキストや画像を自由なレイアウトで記述し、PDF等の原本ファイルを好きな場所に配置し、タグを付与することでその情報の意味（発注書なのか見積書なのか）をデータベースに認識させる」**という自律型データ連携を採用します。

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

### ① 自由な記述（コンテキストの保存）
ユーザーは、取引の経緯、注意点、打ち合わせメモなどの文章（HTML）を自由に書き込みます。その文書の任意の場所に、原本データであるPDFファイル等をインラインで配置します。

### ② `<m-file>` タグによる意味付け（セマンティクス）
配置したファイルには、**`<m-file>`（ファイルブロック）** を用い、`tag` 属性（例: `tag="顧客の発注書"`）を指定することでそのファイルの意味を示します。
Go言語のバックエンドは、このHTMLファイルをスキャンし、`tag` の値に基づいて適切なSQLiteのテーブル（受注、仕入見積など）に、ファイルパスと取引数値（単価・数量など）を自動で振り分けてインデックス登録します。

### ③ データベースによる一元集計と時系列に依存しない「非同期集計」
データベースにはすべてのドキュメントから抽出されたデータが集約されます。
w-cms は**「データの登録順序」を強制しません。**
同一の `item_id`（製品コード/プロジェクトID）で紐付いていれば、いつどの書類（発注書や見積書）が登録されても、データベースが自動で名寄せして原価と売上を集計します。

*   **開発案件（先行製造）**: 先に材料仕入や加工ログ（原価）が登録され、製造完了後に「顧客の発注書」が登録された時点で、自動的に粗利益が確定・反映されます。
*   **自社開発案件**: 「顧客の発注書」が登録されないため、売上0円として扱い、かかった「総開発原価（コストの累計）」のみをプロジェクトコストとして集計します。

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
    <title>開発案件：新型シャフトの試作記録</title>
    <script src="/assets/web-components.js" defer></script>
</head>
<body>

    <!-- 案件区分タグでプロジェクトの種類を分類（一般受注 / 受託開発 / 自社開発） -->
    <m-tag name="案件区分" value="受託開発"></m-tag>
    <m-tag name="自社担当" value="佐藤"></m-tag>
    <m-tag name="プロジェクト名" value="新型シャフト先行開発"></m-tag>

    <h1>新型シャフト（試作コード: DEV-SHAFT-99）の開発と調達記録</h1>
    <p>メーカー側からの正式発注は試作評価後となりますが、先行して部材調達および試作加工を開始します。</p>

    <p>先行して手配した鋼材の材料見積書（PDF）です。</p>
    <!-- 先にコスト（見積）を登録 -->
    <m-file src="attachments/material_shaft.pdf" name="シャフト用鋼材見積.pdf" 
            tag="材料屋・加工業者の見積もり" item-name="特殊鋼材" cost="2500" supplier-name="東邦金属工業"></m-file>

    <p>【更新履歴 2026-07-01】評価合格に伴い、メーカーより正式な発注書を受領しました。以下に原本を添付します。</p>
    <!-- 後から顧客発注（売上）を同じページ内に配置 -->
    <m-file src="attachments/po_shaft.pdf" name="正式発注書_トーア.pdf" 
            tag="顧客の発注書" item-id="DEV-SHAFT-99" price="8000" quantity="10"></m-file>

    <hr>
    <h3>このプロジェクトの原価・利益推移</h3>
    <!-- 動的利益計算ブロック (DBから 'DEV-SHAFT-99' の売上・原価を自動集計) -->
    <m-profit-calculator item-id="DEV-SHAFT-99"></m-profit-calculator>

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

        // タグの種類に応じたカラーの設定
        let tagColor = '#64748b';
        let bgColor = '#f8fafc';
        if (tag === '顧客の発注書') { tagColor = '#10b981'; bgColor = '#ecfdf5'; }
        if (tag === '弊社の発注書') { tagColor = '#ef4444'; bgColor = '#fef2f2'; }
        if (tag === '材料屋・加工業者の見積もり') { tagColor = '#f59e0b'; bgColor = '#fffbeb'; }
        if (tag === '弊社の見積もり') { tagColor = '#3b82f6'; bgColor = '#eff6ff'; }

        let innerHTML = `
            <div class="file-block" style="border: 1px solid #e2e8f0; border-left: 5px solid ${tagColor}; border-radius: 6px; padding: 12px 16px; margin: 12px 0; background: #fff; font-family: sans-serif; box-shadow: 0 1px 3px rgba(0,0,0,0.05); transition: all 0.2s;">
                <div style="display: flex; align-items: center; justify-content: space-between; flex-wrap: wrap; gap: 8px;">
                    <div style="display: flex; align-items: center; gap: 8px;">
                        <span style="font-size: 11px; font-weight: bold; color: ${tagColor}; background: ${bgColor}; padding: 3px 8px; border-radius: 4px; border: 1px solid ${tagColor}20;">${tag}</span>
                        <a href="${src}" target="_blank" style="text-decoration: none; color: #1e40af; font-weight: 600; font-size: 14px;">📄 ${name}</a>
                    </div>
                    <div>
                        <a href="${src}" download style="font-size: 12px; text-decoration: none; background: #f1f5f9; padding: 6px 12px; border-radius: 4px; color: #475569; font-weight: 500; border: 1px solid #cbd5e1; transition: background 0.2s;">ダウンロード</a>
                    </div>
                </div>
        `;

        if (isEdit) {
            innerHTML += `<div style="margin-top: 10px; padding-top: 10px; border-top: 1px dashed #e2e8f0; font-size: 13px; color:#475569; display: flex; gap: 16px; align-items: center;">`;
            if (tag === '顧客の発注書' || tag === '弊社の見積もり') {
                const price = this.getAttribute('price') || '0';
                const quantity = this.getAttribute('quantity') || '1';
                innerHTML += `
                    <div>
                        単価 (売上): 
                        <input type="number" value="${price}" style="width: 90px; border: 1px solid #cbd5e1; border-radius: 4px; padding: 4px 8px;"
                               oninput="this.getRootNode().host.setAttribute('price', this.value);"> 円
                    </div>
                    <div>
                        数量: 
                        <input type="number" value="${quantity}" style="width: 60px; border: 1px solid #cbd5e1; border-radius: 4px; padding: 4px 8px;"
                               oninput="this.getRootNode().host.setAttribute('quantity', this.value);">
                    </div>
                `;
            } else if (tag === '材料屋・加工業者の見積もり' || tag === '弊社の発注書') {
                const cost = this.getAttribute('cost') || '0';
                innerHTML += `
                    <div>
                        単価 (仕入原価): 
                        <input type="number" value="${cost}" style="width: 90px; border: 1px solid #cbd5e1; border-radius: 4px; padding: 4px 8px;"
                               oninput="this.getRootNode().host.setAttribute('cost', this.value);"> 円
                    </div>
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

データベーステーブルは、HTML上のカスタムタグ（`<m-tag>` や `<m-file>` など）から抽出した各種インデックス情報を保持します。

### ① `pages` テーブル（ドキュメントのインデックス）
*   `id` (TEXT, PRIMARY KEY): ページID
*   `title` (TEXT): ページタイトル
*   `file_path` (TEXT): 物理HTMLファイルの保存先パス (例: `"data/master/26/260603-103/index.html"`)
*   `updated_at` (DATETIME): 更新日時

### ② `page_tags` テーブル（可変属性インデックス）
*   `page_id` (TEXT, FOREIGN KEY): `pages.id` に紐づく
*   `name` (TEXT): 属性の名前 (例: `"案件区分"`, `"自社担当"`, `"支払条件"`)
*   `value` (TEXT): 属性の値 (例: `"受託開発"`, `"佐藤"`, `"通常"`)

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
*   `item_id` (TEXT): 製品ID（図面番号/プロジェクトID等）
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
