# 実装計画：部品加工情報（材料構成）の定義と受注ページでの手配状況（要注文リスト）の自動表示

各完成部品の「加工情報（部品ページ）」に記述された必要な材料・外注情報に基づき、受注があった際に「何をいくつ、どこに注文（手配）すべきか」「すでに発注済みか」を受注記録ページ上でリアルタイムに可視化する機能の実装計画です。

---

## 1. ユーザーレビューが必要な項目

> [!IMPORTANT]
> **業務データフローの設計**
> 1.  **部品マスタ（部品ページ）**: 新しいカスタムタグ `<m-material>` を使って、その部品（例: `SHAFT-01`）を作るのに必要な材料（仕入品や外注加工）を定義します。
> 2.  **受注ページ**: 顧客から発注書を受け取ったページ（例: 完成品 `SHAFT-01` を10個受注）の下部に、新コンポーネント `<m-required-materials>` を配置します。
> 3.  **自動計算API**: サーバー側で「(受注部品の必要部材数 × 受注数) − すでに仕入先に発注した発注済数量」を自動計算し、**「未手配の要注文リスト」**を算出します。

---

## 2. 変更予定の詳細

### ① データベース・スキーマの追加

#### [MODIFY] [sqlite.go](file:///C:/Users/kouic/source/repos/w-cms/internal/database/sqlite.go)
*   部品ごとに必要な部材（材料構成マスタ情報）をインデックス化する `part_materials` テーブルを追加します。

```sql
-- 部品構成・材料テーブル
CREATE TABLE IF NOT EXISTS part_materials (
    id INTEGER PRIMARY KEY AUTOINCREMENT,
    part_id TEXT, -- 対象の完成部品番号（例: SHAFT-01）
    material_name TEXT, -- 材料・外注加工名（例: シャフト用鋼材）
    cost INTEGER, -- 予定コスト
    supplier_name TEXT, -- 仕入先・外注先名
    quantity INTEGER, -- 基準必要数量（1点製作あたり）
    page_id TEXT, -- 記述されている部品ページのID
    FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
);
```

---

### ② HTMLマークアップ仕様

#### 部品ページ（例: ページ `00003` - シャフトAの加工情報）
```html
<m-tag name="部品番号" value="SHAFT-01"></m-tag>
<!-- この部品1点あたりに必要な手配部材 -->
<m-material item-name="シャフト用鋼材 (S45C)" cost="2500" supplier-name="東邦金属工業" quantity="1"></m-material>
<m-material item-name="外注高周波焼入れ" cost="1500" supplier-name="山下熱処理" quantity="1"></m-material>
```

#### 受注ページ（例: ページ `00002` - 試作受注の記録）
```html
<m-file tag="顧客の発注書" order-no="PO-A100" client-name="トーア" ordered-at="2026-06-18">
    <m-item item-id="SHAFT-01" item-name="シャフトA" price="8000" quantity="10" status="加工中"></m-item>
</m-file>

<!-- すでに一部発注した記録（自社発注書）があるとする -->
<m-file tag="弊社の発注書" order-no="PO-OUR-001" supplier-name="東邦金属工業" ordered-at="2026-06-18">
    <m-item item-name="シャフト用鋼材 (S45C)" cost="2500" quantity="10" status="未納品"></m-item>
</m-file>

<h3>部材手配・発注進捗状況</h3>
<m-required-materials></m-required-materials>
```

---

### ③ HTMLパーサーおよびDB同期処理の改修

#### [MODIFY] [parser.go](file:///C:/Users/kouic/source/repos/w-cms/internal/cms/parser.go)
*   `<m-material>` ノードを検出し、`PartMaterial` 構造体として抽出するパーサーロジックを追加。
*   `ParsedPage` 構造体に `Materials []PartMaterial` を追加。

#### [MODIFY] [sync.go](file:///C:/Users/kouic/source/repos/w-cms/internal/cms/sync.go)
*   `SyncIndex` 内で、一旦 `part_materials` から該当 `page_id` のレコードを全削除し、最新のパース結果をインサートする処理をトランザクション内に追加。

---

### ④ バックエンド手配計算APIの実装

#### [MODIFY] [handler.go](file:///C:/Users/kouic/source/repos/w-cms/internal/cms/handler.go) または [main.go](file:///C:/Users/kouic/source/repos/w-cms/cmd/w-cms/main.go)
*   API エンドポイント `/api/required-materials` の追加。
*   **集計アルゴリズム**:
    1.  指定された `page_id` (受注ページ) に含まれる `client_order_items` の `item_id` (部品番号) と `quantity` (受注数量) を取得。
    2.  `part_materials` から、その `item_id` (部品番号) に紐づく必要部材名、基準数量、予定コスト、仕入先を取得。
    3.  `総必要数 = 基準数量 × 受注数量` を算出。
    4.  同じ `page_id` に紐づく自社発注実績 `our_order_items` の発注済数量を集計し、部材名で突き合わせ。
    5.  `要注文数 = 総必要数 − 発注済数量` を算出し、仕入先・材料ごとに集計したJSONを返却。

---

### ⑤ フロントエンド Web Components の追加

#### [MODIFY] [web-components.js](file:///C:/Users/kouic/source/repos/w-cms/assets/web-components.js)
*   `<m-material>`: 部品ページでの材料マスタ登録用のUI（閲覧・編集モード）。
*   `<m-required-materials>`: 受注ページ下部に手配状況の進捗テーブルを非同期で描画するUI。
    *   APIからデータを取得し、「材料名」「必要数」「発注済数」「残要注文数」「仕入先」を表形式で描画。
    *   手配が完了している行は緑色、未手配（要注文）が残っている行は赤色のバッジで強調。

---

## 3. 検証計画

### 自動テスト
*   `internal/cms/storage_test.go` にテストコードを追加。部品ページ（材料定義）と受注ページ（受注明細＋自社発注実績）のHTML文字列をそれぞれパース・同期し、API相当の関数を実行した際に正しい「要注文数量」が計算されることを検証します。

### 手動テスト
*   ローカルサーバーでデモHTMLを構築し、部品ページと受注ページを作成。自社発注書を追加した際に、手配リストの「発注済数」が増え「要注文数」がリアルタイムに差し引かれていくダイナミックな連動を確認します。
