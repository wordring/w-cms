# w-cms ソフトウェアコンセプト・設計仕様書

このドキュメントでは、**w-cms** の基本コンセプトである「HTMLファイルベースのデータ管理と、Web Components（HTML5カスタムエレメント）を活用した双方向データ連携」の仕様について定義します。

---

## 1. コア・コンセプト：HTMLファイルとWeb Componentsによる自律型双方向連携

w-cms は、ドキュメントの本文データや構造情報をデータベース（SQLite）に格納しません。**「ドキュメントの本体はすべて標準的な『HTMLファイル（index.html）』として物理保存し、さらにHTML5のWeb Components技術（カスタムエレメント）を用いて、HTML自体が表示・編集UIのロジックを自律的に保持する」**という設計を採用します。

```mermaid
graph TD
    subgraph Browser [ブラウザ / アプリ画面]
        UI[表示・編集UI<br>Web Componentsが描画]
    end

    subgraph Storage [ファイルシステム (物理ストレージ)]
        F1["data/master/xx/xxxxx/index.html (HTML本体)"]
        F2["data/master/xx/xxxxx/images/ (写真・添付ファイル)"]
    end

    subgraph DB [SQLite データベース]
        T1["page_tags テーブル (可変属性名:値)"]
        T2["各種取引テーブル (見積/発注/製造ログ等)"]
    end

    UI -- 1. 属性の書き換えとHTML保存 (Save) --> F1
    F1 -- 2. HTMLの解析・同期 (Sync) --> DB
    DB -- 3. 動的データを反映 (Load) --> UI
    F1 -- 4. 読み込み時にカスタムエレメントがUI生成 --> UI
```

### ① HTMLファイルへの集約とWeb ComponentsによるUIの自律描画
すべてのデータやブロック情報は `index.html` に格納されます。
このHTMLの中身は人間が読みやすいタグと、`<m-tag>` などのシステム用カスタムタグが混在しています。
HTML内に記述された定義スクリプト（Web Components）により、ブラウザでHTMLを開いた瞬間、これらのカスタムタグが自動的に「見やすいバッジデザイン」や「入力用のテキストボックス（編集モード時）」へと自身の見た目を変形（レンダリング）させます。

### ② 編集の連動とDBインデックス登録（インプット）
画面上でユーザーが入力ボックスの値を変更すると、カスタムエレメントの内部ロジックがHTMLタグの属性値（例: `value`）をリアルタイムに更新します。保存時はその変更されたHTMLをそのままファイル保存します。
Go言語のバックエンドは、保存されたHTMLファイルをパースして属性値を取り出し、データベース（SQLite）に即座に同期（インデックス登録）します。

### ③ DBからの動的ロード（アウトプット）
「予定・実績利益計算」などの動的ブロックは、自身のカスタムエレメントのライフサイクル（接続時）にバックエンドAPIを叩き、SQLiteから最新データを取得して自律的にグラフや集計表を描画します。

---

## 2. HTML上のブロック表現とWeb Componentsの仕様

### 2.1. マークアップ例 (`index.html`)
以下のように、共通のWebコンポーネント定義JS（`web-components.js`）を読み込ませることで、ブラウザ表示時にタグが自律的にUI化します。

```html
<!DOCTYPE html>
<html>
<head>
    <meta charset="UTF-8">
    <title>トーアスポーツマシーン様発注書</title>
    <!-- Web Componentsの定義をロード -->
    <script src="/assets/web-components.js" defer></script>
</head>
<body edit-mode> <!-- edit-mode属性の有無で表示/編集モードを切り替える -->

    <!-- 可変タグブロック -->
    <m-tag name="発注元" value="株式会社トーアスポーツマシーン"></m-tag>
    <m-tag name="希望納入日" value="2026-06-10"></m-tag>
    <m-tag name="支払条件" value="通常"></m-tag>
    <m-tag name="自社担当" value="紀平"></m-tag>

    <h1>トーアスポーツマシーン様発注書（PO-260603-103）</h1>

    <!-- 顧客の発注書データブロック -->
    <m-client-order item-id="W120-P180-05-03A" item-name="側板" price="1197" quantity="20"></m-client-order>
    <m-client-order item-id="X008-123-2" item-name="ﾓｰﾀｰｶｯﾌﾟﾘﾝｸﾞ" price="3500" quantity="20"></m-client-order>

    <!-- 動的利益計算ブロック (DBからデータをロードして表示) -->
    <m-profit-calculator item-id="W120-P180-05-03A"></m-profit-calculator>

</body>
</html>
```

### 2.2. Web Componentsの実装例 (`web-components.js`)
ブラウザ上でカスタムタグがUIとして振る舞うためのJavaScript定義です。

```javascript
// <m-tag> (名前：値) の定義
class MTag extends HTMLElement {
    static get observedAttributes() { return ['value']; }
    connectedCallback() { this.render(); }
    attributeChangedCallback() { this.render(); }

    render() {
        const name = this.getAttribute('name') || '';
        const value = this.getAttribute('value') || '';
        // 親（body等）に edit-mode があれば編集フォーム、無ければバッジ表示
        const isEdit = document.body.hasAttribute('edit-mode');

        if (isEdit) {
            this.innerHTML = `
                <div class="tag-edit-container" style="display:inline-flex; align-items:center; border:1px solid #007bff; border-radius:4px; padding:2px; margin:4px; font-family:sans-serif;">
                    <span style="font-weight:bold; padding: 2px 6px; background:#e0f0ff; color:#007bff; border-radius:2px;">${name}</span>
                    <input type="text" value="${value}" style="border:none; padding:4px; outline:none;" 
                           oninput="this.getRootNode().host.setAttribute('value', this.value)">
                </div>
            `;
        } else {
            this.innerHTML = `
                <span class="tag-badge-container" style="display:inline-flex; align-items:center; border:1px solid #ccc; border-radius:12px; padding:2px 10px; margin:4px; background:#f8f9fa; font-family:sans-serif; font-size:14px;">
                    <strong style="color:#555; margin-right:6px;">${name}:</strong>
                    <span style="color:#333;">${value}</span>
                </span>
            `;
        }
    }
}
customElements.define('m-tag', MTag);
```

---

## 3. 実装に向けたデータモデル設計（案）

データベースは、HTML上のカスタムタグ（`<m-tag>` や `<m-client-order>` など）から抽出した各種インデックス情報を保持します。

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
*   `page_id` (TEXT, FOREIGN KEY): 抽出元HTMLのページID
*   `estimated_at` (DATE): 見積日

### ④ `client_orders` テーブル（「顧客の発注書」インデックス）
*   `id` (INTEGER, PRIMARY KEY AUTOINCREMENT)
*   `item_id` (TEXT): 製品ID
*   `client_name` (TEXT): 発注元顧客名
*   `price` (INTEGER): 受注単価
*   `quantity` (INTEGER): 数量
*   `page_id` (TEXT, FOREIGN KEY): 抽出元HTMLのページID
*   `ordered_at` (DATE): 受注日

### ⑤ `supplier_estimates` テーブル（「材料屋・加工業者の見積もり」インデックス）
*   `id` (INTEGER, PRIMARY KEY AUTOINCREMENT)
*   `item_name` (TEXT): 材料名、または外注加工内容
*   `supplier_name` (TEXT): 仕入・加工先の名前
*   `cost` (INTEGER): 予定原価
*   `page_id` (TEXT, FOREIGN KEY): 抽出元HTMLのページID
*   `estimated_at` (DATE): 見積日

### ⑥ `our_orders` テーブル（「弊社の発注書」インデックス）
*   `id` (INTEGER, PRIMARY KEY AUTOINCREMENT)
*   `item_name` (TEXT): 材料名、または外注加工内容
*   `supplier_name` (TEXT): 発注先の名前
*   `cost` (INTEGER): 実績原価
*   `quantity` (INTEGER): 数量
*   `page_id` (TEXT, FOREIGN KEY): 抽出元HTML의 ページID
*   `ordered_at` (DATE): 発注日

---

## 4. 今後の開発ロードマップ

1.  **Web Components（web-components.js）の基本定義**:
    *   `<m-tag>` や `<m-client-order>` がブラウザ上で表示・編集フォームとして機能するためのJavaScriptの実装。
2.  **HTMLパーサー（parser.go）の書き換え**:
    *   HTMLファイルから `<m-tag name="..." value="...">` や各種取引カスタムタグの属性値をパースする処理の実装。
3.  **データベース（sqlite.go）のインデックス化**:
    *   「4つの取引書類」および可変タグ用テーブルの初期化。
4.  **データ同期ロジック（sync.go）の実装**:
    *   HTMLファイル保存時に、パーサーが抽出した属性データをDBへUPSERTする処理の実装。
5.  **フロントエンド（エディタ連携）の実装**:
    *   ブラウザ上でHTMLをロードし、`edit-mode`を付与して編集させ、変更後のHTMLソースコードを取得して保存APIにポストする仕組みの構築。
6.  **DB再構築（リビルド）機能の実装**:
    *   DBファイルが紛失・破損した際、物理ストレージ（`data/master`）内のHTMLファイルを再帰的に走査・再パースして、DBインデックスを100%完全復旧するバッチ・管理処理の実装。
