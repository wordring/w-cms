# E2E（Playwright）

w-cms のブラウザE2E。**このディレクトリが正本**です（2026-08-21 に OneDrive
`%OneDrive%\tools\wcms-e2e\` から移設）。

git 管理外に置いていたころは、レビューも引き継ぎもできず、OneDrive が失われれば
安全網ごと消える状態でした。とりわけ **`serializeChildren` の `.vocab-chrome` 除外
（保存側）には Go テストが無く、E2E だけが守っています**——派生表示が本文の正本へ
焼き付く不可逆の破損に対する唯一の自動防壁なので、リポジトリの中にあるべきものでした。

| スクリプト | 守っているもの | `check()` |
|---|---|---|
| `verify-stage2.js` | 第1段の回帰＋列操作・列設定・型検証・enum・dl項目操作 | 49 |
| `verify-stage3.js` | 受発注 section・見積 dl・ファイル容器とエンハンサ | 17 |
| `verify-stage4.js` | 計算ビューのSSR・撤去ファイルの404・CSP strict | 23 |
| `verify-delete.js` | ページ削除（ゴミ箱への移動）と拒否条件 | 9 |
| `verify-template.js` | ページテンプレートの選択と新規化パス | 14 |

`assets/` を変更したら `node --check` に続けて**一式を回帰として流してください**。
`verify-stage1.js`（第1段）と `verify-migration2.js`（一括変換）は役目を終えて
廃棄済みです——前者は stage2 が回帰を含み、後者は移行完了（2026-08-20）で変換ツール
ごと撤去したため。

## 実行方法

```bash
# 1. 依存を入れる（初回のみ。node_modules は .gitignore 対象）
cd e2e
npm install
npx playwright install chromium

# 2. 別のターミナルで w-cms を起動しておく（ローカル検証の管理者は a / a）
WCMS_SECURE_COOKIES=0 go run cmd/w-cms/main.go

# 3. e2e をカレントにして流す（--headed でブラウザを表示）
node verify-stage2.js
node verify-stage3.js
node verify-stage4.js
node verify-delete.js
node verify-template.js
```

スクリプトは playwright を**カレントディレクトリから**解決します（`createRequire`）。
場所を間違えると手順つきのエラーが出ます。

`node` が PATH に無いことがあります（`C:\Program Files\nodejs` を足す）。

## 落とし穴（実際に踏んだもの）

- セレクタ `h1` は殻のヘッダーを拾う。エディタ内は `#w-editor-content h1`
  （殻の `id` は接頭辞 `w-` を独占する。`shell_id_test.go` 参照）
- 保存待ちは `waitSaved` 1回では足りない（直前の保存の応答が「保存済」を上書きする）。
  デバウンス1.5秒を跨いで2周目を掴む `settleSaved` を使う。**新しい検査を書くときも
  このパターンに倣うこと**
- ページ再読込後の本文検査は、`populateEditor` の再構築が終わるまで待つ
  （目的の要素を `waitFor` してから検査する）

## 注意

- 実行のたびに w-cms にテストページができる（管理コンソールの「DB再構築」で整理可）
- CI はまだありません。回すのは人の規律に委ねられています
