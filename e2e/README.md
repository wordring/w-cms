# E2E（Playwright）

w-cms のブラウザE2E。**このディレクトリが正本**です（2026-08-21 に OneDrive
`%OneDrive%\tools\wcms-e2e\` から移設）。

git 管理外に置いていたころは、レビューも引き継ぎもできず、OneDrive が失われれば
安全網ごと消える状態でした。とりわけ **`serializeChildren` の `.vocab-chrome` 除外
（保存側）には Go テストが無く、E2E だけが守っています**——派生表示が本文の正本へ
焼き付く不可逆の破損に対する唯一の自動防壁なので、リポジトリの中にあるべきものでした。

| スクリプト | 守っているもの | `check()` |
|---|---|---|
| `verify-stage2.js` | 第1段の回帰＋列操作・列設定・型検証・enum・dl項目操作・未知種別の赤い告知・形式名の札・**スラッシュメニューの到達性** | 65 |
| `verify-stage3.js` | 受発注 section・見積 dl・ファイル容器とエンハンサ | 20 |
| `verify-stage4.js` | 計算ビューのSSR・**自動集計の表が触れないこと**・撤去ファイルの404・CSP strict | 29 |
| `verify-delete.js` | ページ削除（ゴミ箱への移動）と拒否条件 | 9 |
| `verify-template.js` | ページテンプレートの選択と新規化パス | 15 |
| `verify-editor-loss.js` | 編集画面で入力が消える経路（貼り付け・保存済表示）の回帰 | 11 |
| `verify-image.js` | 画像の添付（挿入・EXIF除去の入口・HEIC拒否・SVGの不活性配信） | 21 |
| `verify-public.js` | 公開専用ビュー（クローム無し・SEO/OGP・キャッシュの切り分け・sitemap/robots） | 29 |
| `verify-version.js` | 版の履歴（コアレッシング・ロック解放時の記録・リバート・版IDの検証） | 17 |

（件数は 2026-08-26 の実測。合計216項目）

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
node verify-editor-loss.js
node verify-image.js
node verify-public.js
node verify-version.js
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
- **日付の比較に `toISOString()` を使わない**。UTC へ寄るので、日本時間の 00:00〜09:00 に
  走らせるとサーバー（現地時刻）と1日ずれて落ちる（`verify-template.js` で実際に踏んだ）
- **`verify-public.js` は実データの公開フラグを立てる**。必ず `finally` で戻す作りにしてある
  ——途中で中断すると**ローカルのトップページが公開のまま残る**ので、`curl localhost:8080/robots.txt`
  が `Disallow: /` を返すか確認すること
- **編集ロックは前のスクリプトから残ることがある**。一式を続けて流すと、トップページの
  権限変更（`verify-public.js` の `setPublic`）が 409 になって落ちた。ロックを要するAPIを
  叩くヘルパーは**409 なら少し待って取り直す**作りにしておくこと

## 注意

- 実行のたびに w-cms にテストページができる（管理コンソールの「DB再構築」で整理可）
- CI はまだありません。回すのは人の規律に委ねられています
