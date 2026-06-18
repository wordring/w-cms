# Notion風ブロックエディタ UI/UX 実装仕様

w-cms のフロントエンドエディタ（`assets/test.html`）は、モダンでインタラクティブなNotionライクなブロックエディタとして実装されています。このドキュメントでは、そのUIとイベント制御の仕組みについて解説します。

## 1. ブロックラッパーのDOM構造

エディタ内のすべての要素（見出し、段落、発注書コンポーネントなど）は、プレーンなHTMLのままではなく、JavaScriptによって動的に `.editor-block` というラッパー要素で包まれます。

```html
<!-- DOM上の実際の構造 -->
<div class="editor-block" draggable="true">
    <!-- ホバー時に表示されるコントロール（＋ と ドラッグハンドル） -->
    <div class="block-controls">
        <button class="add-btn">＋</button>
        <div class="drag-handle">⠿</div>
    </div>
    <!-- 実際のコンテンツ -->
    <div class="block-content" contenteditable="true">
        <p>ここにテキストを入力します...</p>
    </div>
</div>
```

*   **自動ラップ (`wrapInBlock` 関数)**: エディタの初期化時や、新しい要素が挿入された際に、対象の要素を `.block-content` の中に入れ、左側に `.block-controls` を付与します。
*   **シリアライズ (`updateHtmlPreview` 関数)**: データベースに保存（送信）する際は、ラッパー要素を除去し、中身の `<p>` や `<m-file>` だけを抽出してクリーンなHTMLに戻します。

## 2. ドラッグ＆ドロップ (Drag & Drop)

*   `draggable="true"` 属性は `.editor-block` 自体に付与されますが、実際にドラッグを開始できるのは `.drag-handle` (`⠿`) を掴んだ時だけになるようにイベントを制御しています。
*   ドラッグ中は `.dragging` クラスが付与され、ドロップ先の要素には `.drag-over` クラスが付与されて青い境界線（視覚的フィードバック）が表示されます。
*   ドロップされた際、`insertBefore` または `insertAfter` のロジックを用いてブロックのDOMノードを並び替えます。

## 3. スラッシュコマンド (`/`) メニュー

テキストブロックで `/` を入力した際、または `＋` ボタンを押した際に、フローティングの「ブロック種別選択メニュー（`.slash-menu`）」が表示されます。

*   **表示トリガー**: `keyup` イベントを監視し、中身が `/` 1文字だけになったブロックを検知すると `showSlashMenu(targetBlock)` を呼び出します。
*   **キーボードナビゲーション**: メニュー表示中は、`ArrowUp`, `ArrowDown` で選択項目のハイライト（`.selected` クラス）を移動し、`Enter` で確定します。
*   **確定時の処理 (`replaceBlockWithComponent`)**: 選択したブロック種別（`m-file-client` など）の要素を新しく生成し、`/` が入力されていたトリガー元のブロックをその新しい要素で置換（Replace）します。

## 4. コンテキストツールバー (Contextual Toolbar)

対象のブロックを選択・フォーカスした際に、そのブロック専用の「追加アクション」を行うためのツールバーがブロック上部に表示されます。

*   **表示トリガー**: `selectionchange` イベントおよび `click` イベントを監視し、現在カーソルがあるノード（`activeBlock`）を特定します。
*   **動的生成**: 対象のブロックが `<m-file tag="顧客の発注書">` の場合は、ツールバーの中に「＋ 部品を追加」ボタンを動的に生成します。
*   **コンポーネント内への挿入**: ボタンがクリックされると、該当の `m-file` ブロックの中（`.items-list` など）に `<m-item>` コンポーネントを DOM として `appendChild` し、シリアライズ処理（`updateHtmlPreview`）をトリガーします。

## 5. Web Components との親和性・イベント伝播制御

`<m-file>` や `<m-item>` といった Web Components の内部には、ユーザーが文字を入力するための `<input>` タグが含まれています。

ブロックエディタは通常、`Enter` キーを押すと「新しいブロックを下に追加」し、`Backspace` で「ブロックを削除」するグローバルなイベントリスナーを持っています。しかし、コンポーネント内の `<input>` で `Enter` を押した際に勝手に新しいブロックが作られてしまうと致命的なバグになります。

この問題を回避するため、コンポーネント（Shadow DOMや内部要素）のインプット処理においては、キーボードイベントが親の `.editor-block` までバブリングしないよう、適切に `event.stopPropagation()` を呼び出してイベントをせき止める設計としています。
