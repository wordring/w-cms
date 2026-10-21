# コードリファレンス

**対象コミット: `8fff53c`（2026-10-21。この時点のコードから作り直しました・[全体地図.md](全体地図.md) と同じ測定）**

実装の**いまの姿**を写した文書群です。設計の「なぜ」は、各設計書と【考察】が正本です。ここは「いま何がどうなっているか」だけを扱います。

前の版（`2d640c7`・2026-10-14）のあと、コードが大きく変わったので、作り直しました。
本体の Go は **224 → 257 ファイル・54,417 → 61,045 行**になりました（どちらも `cmd`・`internal`・`ext` の、試験を除く数）。大きく変わったのは、次の6つです。

- **拡張は `ext/` の下のフォルダ1つで完結します**（2026-10-20）。
  Go のパッケージと、画面のファイル（`assets/` の JS・CSS・HTML）を同じフォルダに置きます。
  実行ファイルへの取り付け口（`cmd/w-cms/ext_*_gen.go`）は、`go generate ./cmd/w-cms` が作ります（`internal/extgen`・`tools/extgen`）。
- **画面の JS をコアと拡張に分けました**（2026-10-20）。コアの `assets/app.js` は拡張を名指ししません。
  拡張の JS（`ext/<拡張>/assets/*.js`）は、コアが渡す `window.wcms` だけを使います。
  サーバーは、載っている拡張の画面のファイルだけを `/assets/ext/…` で配ります（`internal/cms/ext_scripts.go`）。
- **拡張の機能を、本文の印として描きます**（2026-10-20）。
  通信記録の操作は、本文の印 `<section data-mirror="記録の箱">` の中にサーバーが描きます（`ext/comm/record_box.go`）。
  ☎ と「未分類へ戻す」は、タグの値に足す飾りです（`cms.RegisterTagDecorator`・`internal/cms/tag_decor.go`）。
- **本文を書き換える道具が増えました**（2026-10-19〜20）。
  道具がページを握っていれば書かない `cms.RewriteBodyUnlessHeld`、ページのタグを足す `cms.AddPageTags`、
  既にあるページをテンプレートと同じ方法で書き換える `cms.EditBody`（`DraftBlock.ReplaceTag` など）です。
- **東邦の業務が、案件・製造・記録の形になりました**（2026-10-16〜19）。
  「受注」は「案件」に改名し、トップ直下の「製造」に製造ページを置きます。書類は `記録／年／月／依頼・製造記録` に作ります。
  加工製品は `製造台帳／社名／（装置名称）` の下です。納品書・請求書・見積計算書・ページ種類のタグも入りました。
- **コアと拡張の境を、試験で見張ります**（2026-10-20〜21）。見張りの試験は `internal/boundary`、決まりの表は `internal/boundary/rules.json` です。
  Claude Code の hook（`.claude/settings.json`）も、この試験を流します（`tools/boundary-hook.js`）。

## まず `go doc`

説明の本体は**コードの中**にあります。パッケージの冒頭コメントを読むのが近道です。

```bash
go doc ./cmd/w-cms                 # 起動の流れ
go doc ./internal/cms              # 中核（正本・語彙・3層・回覧）と公開APIの一覧
go doc ./internal/cms/page         # サイドカーと認可・添付の置き場
go doc ./internal/cms/editlock     # 悲観ロック（2026-10-12 から画面は使わない。道具のために残る）
go doc ./internal/cms/htmldoc      # 本文のサニタイズ
go doc ./internal/database         # cms.db（派生）・tables.db（表の写し・派生）・auth.db（正本）・データの場所
go doc ./internal/auth             # セッション（最後の操作から8時間）・パスワード・監査
go doc ./internal/extgen           # 拡張の取り付け口を作る決まり（2026-10-20）

go doc ./internal/cms SyncAPIHandler            # ブロック単位で合わせる保存の API
go doc ./internal/cms LockPageBody              # ページごとの書き込みのロック
go doc ./internal/cms RewriteBodyUnlessHeld     # 道具が握っていれば書かない書き換え（2026-10-20）
go doc ./internal/cms PageDraft                 # テンプレートを写して印を埋める道具
go doc ./internal/cms EditBody                  # 既にある本文を、同じ道具で書き換える（2026-10-20）
go doc ./internal/cms RegisterTagDecorator      # タグの値に飾りを足す登録の仕組み（2026-10-20）
go doc ./internal/cms RegisterPageTemplate      # 機械が写すテンプレートの名簿
go doc ./internal/cms RegisterTablesExclusion   # 表の写しに入れないページを、拡張が断る仕組み
go doc ./internal/cms RegisterContactResolver   # アドレス → 連絡先ページ
go doc ./internal/cms RegisterSettingsSection   # 拡張の設定の節
go doc ./internal/cms RegisterExtension         # 載っている拡張の名簿
go doc ./internal/cms RegisterRequiredPage      # 置き場（トップ直下の、名前で機能が決まるページ）の名簿

go doc ./ext/comm                  # 通信（通信箱・取り込み係・メールの API・送る欄の用件・記録から作るページ・記録の箱）
go doc ./ext/comm IntakeHandler    # 取り込み係のインターフェース
go doc ./ext/comm SendPurpose      # 送る欄の用件
go doc ./ext/comm RecordMaker      # 記録から作るページの種類
go doc ./ext/comm RecordSection    # 記録の箱へ節を足す（2026-10-20）
go doc ./ext/comm Mailer           # メールの API（中身は ext/comm/mail）
go doc ./ext/comm/contacts         # アドレス帳（なぜ独立した拡張か・コアとの境目）
go doc ./ext/comm/mail             # メール送受信（IMAP／SMTP）
go doc ./ext/toho                  # 東邦の業務

go doc -all ./internal/cms/page      # そのパッケージの全公開APIをコメントごと
go doc -src ./internal/cms Sanitize  # 実装も見る
```

ブラウザで読みたいときは、`pkgsite` を手元で動かせます（外へは公開されません）。

```bash
go run golang.org/x/pkgsite/cmd/pkgsite@latest -open .
```

> `internal/` の下なので、pkg.go.dev には出ません（出す必要もありません）。

## 3本の文書

| 文書 | 何が書いてあるか | 直す目安 |
|---|---|---|
| [全体地図.md](全体地図.md) | パッケージの構成・依存の向き・**何が正本か**・ファイルの役割・知っておくべき食い違い | 構造が変わったら必ず |
| [【一覧】関数リファレンス.md](【一覧】関数リファレンス.md) | 主な関数と**呼び出し元**の対応表 | 関数を足す・動かす・名前を変えるたび |
| [シナリオ別の呼び出し追跡.md](シナリオ別の呼び出し追跡.md) | 操作ごとの呼び出しの連鎖と分かれ道（403／404／409／423 の条件） | 経路が変わったら |

## いまの形をひとことで

```
cmd/w-cms  ── ext_*_gen.go のブランク import（go generate が作る。ビルドタグはここだけ）
   ├→ ext/toho          ── ext/comm, ext/comm/contacts を import
   ├→ ext/comm/mail     ── ext/comm, ext/comm/contacts を import（署名）
   ├→ ext/comm/contacts ── ext/comm を import
   └→ ext/comm          ── ext/ を1つも import しない
                ↓（全部が）
           internal/cms（コアは ext/ を知らない。受けるのは11の登録の仕組みだけ）
```

組み立てられる組は3つです。**通常**・**`-tags nomail`**（メールだけ外す）・**`-tags minimal`**（素の w-cms）。
何が載っているかは、起動ログ（「拡張セット: …」「有効な設定: …」）と `/api/tag-schema` の `extensions` に出ます。
拡張のフォルダを足し・外したら、`go generate ./cmd/w-cms` を流してから組み立てます。

画面の保存は `POST /api/sync` だけです。書ける形と閲覧モードの切り替え・ぶつかったブロックの並べ方・ページの更新の知らせは、
[シナリオ別の呼び出し追跡.md](シナリオ別の呼び出し追跡.md) の §2・§3 にあります。

## この文書群は何度も作り直しています

手書きの「いまの姿の写し」は、実装と同じ速さで古びます。2026-08-21 には、**行数の主張30件のうち16件が誤り、一部は中身が逆**という状態になりました。
読んだ人が正しい実装をバグと思い込みかねないので、一度全部を消しています。
2026-08-27 に作り直し、以後 08-30・09-02・09-04・09-14・09-15・09-16・09-22・10-01・10-14 に測り直し・作り直しをしました。
**10-14 から 10-21 までの変更を受けて、2026-10-21 に作り直した**のがこの版です。

同じ失敗をしないための決め事です。

- **説明の本体は `go doc`（コードの中）に置きます。** ここが受け持つのは、パッケージをまたぐ話だけです。
- **数は対象コミットで数えたもので、仕様ではありません。** 各文書に、数え直すコマンドを添えてあります。
- **古くなったら、直すのではなく作り直します**（スキル `/code-reference`）。直し続けるより安い、というのが 2026-08-21 の判断でした。

**この3本を読むより先に**、[作業引き継ぎ.md](../../作業引き継ぎ.md) の「覚えておくこと（知らないと必ず踏む罠）」に目を通してください。
短くて、知らないと壊すものだけが並んでいます。
