# コードリファレンス

**対象コミット: `c60d187`（2026-10-01 作り直し・[全体地図.md](全体地図.md) と同じ測定）＋同日の「受注の行は受注フォルダを開いたときに結ぶ」を反映**

実装の**現状**を写した文書群です。設計の「なぜ」は各設計書と【考察】が正本で、ここは
「いま何がどうなっているか」だけを扱います。

この版は **2026-09-22〜30 の9日間（Go本体が 141 → 179 ファイル・31,444 → 42,345 行）**を受けて、実際の
コードから作り直したものです。大きいのは次の5つです:

- **テンプレート駆動**（09-25〜27）——テンプレートは純粋なコピーになり（種まきの段を撤去）、置き場も機械が作る
  ページも**テンプレートを写して印を埋める**形に（`RegisterPageTemplate`・`PageDraft`）。
- **表の写し `data/tables.db`**（09-25）——キャプションのある表を「キャプションの名前の表」として写す別の DB と、
  🔎 表を探す・AI の口。表は `<caption>` で、鏡は `<section data-mirror>` で名乗り、**節の見出しで名乗る読み方は廃止**（09-29）。
- **拡張 `ext/subcon` → `ext/toho`（東邦の業務）**（09-27）——発注の後半（発注部材表・臨時部材表・行ごとの状態・
  送る・戻す・資料を綴じる）・加工製品の木と一覧・同じ図面の重複・受注残高・結び直しの口。
- **通信の送る欄の部品**（09-30）——返信・新しいメール・発注書が同じ欄を使い、中身は用件（`RegisterSendPurpose`）が持つ。
  下書き・メール一覧。`.eml` を記録にする口は専用の `POST /api/intake/eml` になり、**アップロードの横取りは撤去**。
- **添付をローカルのアプリで編集する常駐ヘルパー**（`cmd/w-cms-edit`・09-29）と、移行の道具（`tools/`）。

## まず `go doc`

説明の本体は**コードの中**にあります。パッケージの冒頭コメントを読むのが最短です。

```bash
go doc ./cmd/w-cms                 # 起動の流れ
go doc ./internal/cms              # 中核（正本・語彙・3層・回覧）＋公開APIの一覧
go doc ./internal/cms/page         # サイドカーと認可・添付の置き場
go doc ./internal/cms/editlock     # 悲観ロック
go doc ./internal/cms/htmldoc      # 本文サニタイズ
go doc ./internal/database         # cms.db（派生）・tables.db（表の写し・派生）・auth.db（正本）

go doc ./internal/cms Observer                  # 型・関数を1つだけ
go doc ./internal/cms PageDraft                 # テンプレートを写して印を埋める口
go doc ./internal/cms RegisterPageTemplate      # 機械が写すテンプレートの名簿
go doc ./internal/cms RegisterTablesExclusion   # 表の写しに入れないページを拡張が断る口
go doc ./internal/cms RegisterContactResolver   # アドレス→連絡先ページ
go doc ./internal/cms RegisterSettingsSection   # 拡張の設定の節
go doc ./internal/cms RegisterExtension         # 載っている拡張の名簿
go doc ./internal/cms RegisterRequiredPage      # 置き場（トップ直下の名前で機能が決まるページ）の名簿
go doc ./internal/cms RegisterSuggestSource     # 入力の候補の出どころ

go doc ./ext/comm                  # 通信（通信箱・取り込み係・メールの口・送る欄の用件）
go doc ./ext/comm IntakeHandler    # 取り込み係の受け口
go doc ./ext/comm SendPurpose      # 送る欄の用件
go doc ./ext/comm Mailer           # メールの口（実装は ext/comm/mail）
go doc ./ext/comm/contacts         # アドレス帳（なぜ独立した拡張か・コアとの境目）
go doc ./ext/comm/mail             # メール送受信（IMAP／SMTP）
go doc ./ext/toho                  # 東邦の業務

go doc -all ./internal/cms/page      # そのパッケージの全公開APIをコメントごと
go doc -src ./internal/cms Sanitize  # 実装も見る
```

ブラウザで読みたいときは `pkgsite` をローカルで動かせます（外部公開はされません）:

```bash
go run golang.org/x/pkgsite/cmd/pkgsite@latest -open .
```

> `internal/` 配下なので pkg.go.dev には出ません（そもそも出す必要もありません）。

## 3本の文書

| 文書 | 何が書いてあるか | 更新の目安 |
|---|---|---|
| [全体地図.md](全体地図.md) | パッケージ構成・依存の向き・**何が正本か**・ファイルの役割・既知のねじれ | 構造が変わったら必ず |
| [【一覧】関数リファレンス.md](【一覧】関数リファレンス.md) | 主要関数と**呼び出し元**の対応表 | 関数の追加・移動・改名のたび |
| [シナリオ別の呼び出し追跡.md](シナリオ別の呼び出し追跡.md) | 操作ごとの呼び出しの連鎖と分岐（403／404／409／423 の条件） | 経路が変わったら |

## いまの形をひとことで

```
cmd/w-cms  ── ext_*.go のブランク import（ビルドタグはここだけ）
   ├→ ext/toho          ── ext/comm, ext/comm/contacts を import
   ├→ ext/comm/mail     ── ext/comm, ext/comm/contacts を import（署名）
   ├→ ext/comm/contacts ── ext/comm を import
   └→ ext/comm          ── **ext/ を1つも import しない**
                ↓（全部が）
           internal/cms（コアは ext/ を知らない。受けるのは11の登録の口だけ）
```

作れる組は3つ——**通常**・**`-tags nomail`**（メールだけ外す）・**`-tags minimal`**
（素の w-cms）。何が載っているかは起動ログ（「拡張セット: …」）と
`/api/tag-schema` の `extensions` に出ます。

## ⚠ この文書群は何度も作り直しています

手書きの「現状の写像」は実装と同じ速度で陳腐化し、2026-08-21 には**行数主張30件中16件が誤り・
一部は内容が逆**という状態になりました——読んだ人が正しい実装をバグと誤認しかねない、
という理由で一度全部消しています。2026-08-27 に作り直し、以後 08-30・09-02・09-04・09-14・09-15・09-16・
09-22 に測り直し・作り直し、**2026-09-22〜30 の9日間を受けて 2026-10-01 に作り直した**のがこの版です。

同じ轍を踏まないための決め事:

- **説明の本体は `go doc`（コードの中）へ置く。** ここが担うのは横断的な話だけ。
- **数字は対象コミットの実測であって仕様ではない。** 各文書に数え直すコマンドが添えてあります。
- **古くなったら直すのではなく作り直す**（スキル `/code-reference`）。
  直し続けるより安い、というのが 2026-08-21 の判断でした。

**この3本を読むより先に**、[作業引き継ぎ.md](../../作業引き継ぎ.md) の
「覚えておくこと（知らないと必ず踏む罠）」に目を通してください——短くて、
知らないと壊すものだけが並んでいます。
