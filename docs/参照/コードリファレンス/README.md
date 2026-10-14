# コードリファレンス

**対象コミット: `2d640c7`（2026-10-14。`36357fe` で作り直したものを、同じ日の変更の分だけ測り直した・[全体地図.md](全体地図.md) と同じ測定）**

実装の**いまの姿**を写した文書群です。設計の「なぜ」は各設計書と【考察】が正本です。ここは「いま何がどうなっているか」だけを扱います。

この版は、2026-10-01 の版（`c60d187`）のあとの変更を受けて、実際のコードから作り直したものです。
本体の Go は **179 → 223 ファイル・42,345 → 53,725 行**になりました。大きいのは次の5つです。

- **編集モードをやめ、ブロック単位で守る**（2026-10-12）。画面は編集権（ページ単位のロック）を取りません。
  保存は `POST /api/sync` の1本で、ブロックごとに合わせ、同じブロックを2人が書けば両方を並べます（`handler_sync.go`・`block_sync.go`）。
  本文を書く道は、1回の書き込みのあいだだけ、ページごとの書き込みの錠（`body_lock.go`）を取ります。
  `/api/save`・`/api/lock` などは、道具（ワンノートの製造・テンプレートの取り込み・E2E）のために残っています。
- **ページの更新の知らせ**（2026-10-07・`page_events.go`）。10-12 からは、開いている人とキャレットのあるブロックも配ります。版の区切りは `/api/version-checkpoint` です。
- **表のセルの式**（2026-10-04・`formula.go`）。表示・索引・表の写しが同じ計算を通ります。
- **東邦の業務に見積と見積依頼**（2026-10-01〜05）。見積書・見積依頼書・返事の取り込み・手配不要・受注ごとの手配の帳簿など。
  10-09 には紙の PDF の段取りを `paper_pdf.go` に寄せました。
- **通信の記録の親子は `親ページID`**（2026-10-09・`ext/comm/parent_link.go`）。新しいメールの読み込みはサーバーの裏で続けます（`ext/comm/mail/fetch_job.go`）。
  記録から作るページの口（`RegisterRecordMaker`）も入りました。道具は `tools/internal/wcms` を共有します。

同じ 10-14 の `36357fe` のあとの変更（セッションを最後の操作から8時間に・`WCMS_DATA_DIR` と `WCMS_ADDR`・送った発注書の PDF・見積依頼のロットの列・
データの初期化と開発メニューを外した）は、作り直さずにその分だけ直しました。一覧は [全体地図.md](全体地図.md) の冒頭にあります。

## まず `go doc`

説明の本体は**コードの中**にあります。パッケージの冒頭コメントを読むのが最短です。

```bash
go doc ./cmd/w-cms                 # 起動の流れ
go doc ./internal/cms              # 中核（正本・語彙・3層・回覧）と公開APIの一覧
go doc ./internal/cms/page         # サイドカーと認可・添付の置き場
go doc ./internal/cms/editlock     # 悲観ロック（10-12 から画面は使わない——道具のために残る）
go doc ./internal/cms/htmldoc      # 本文のサニタイズ
go doc ./internal/database         # cms.db（派生）・tables.db（表の写し・派生）・auth.db（正本）・データの場所（WCMS_DATA_DIR）
go doc ./internal/auth             # セッション（最後の操作から8時間）・パスワード・監査

go doc ./internal/cms SyncAPIHandler            # ブロック単位で合わせる保存の口
go doc ./internal/cms LockPageBody              # ページごとの書き込みの錠
go doc ./internal/cms PageDraft                 # テンプレートを写して印を埋める口
go doc ./internal/cms RegisterPageTemplate      # 機械が写すテンプレートの名簿
go doc ./internal/cms RegisterTablesExclusion   # 表の写しに入れないページを拡張が断る口
go doc ./internal/cms RegisterContactResolver   # アドレス→連絡先ページ
go doc ./internal/cms RegisterSettingsSection   # 拡張の設定の節
go doc ./internal/cms RegisterExtension         # 載っている拡張の名簿
go doc ./internal/cms RegisterRequiredPage      # 置き場（トップ直下の名前で機能が決まるページ）の名簿
go doc ./internal/cms RegisterSuggestSource     # 入力の候補の出どころ

go doc ./ext/comm                  # 通信（通信箱・取り込み係・メールの口・送る欄の用件・記録から作るページ）
go doc ./ext/comm IntakeHandler    # 取り込み係の受け口
go doc ./ext/comm SendPurpose      # 送る欄の用件
go doc ./ext/comm RecordMaker      # 記録から作るページの種類
go doc ./ext/comm Mailer           # メールの口（実装は ext/comm/mail）
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

| 文書 | 何が書いてあるか | 更新の目安 |
|---|---|---|
| [全体地図.md](全体地図.md) | パッケージの構成・依存の向き・**何が正本か**・ファイルの役割・既知のねじれ | 構造が変わったら必ず |
| [【一覧】関数リファレンス.md](【一覧】関数リファレンス.md) | 主な関数と**呼び出し元**の対応表 | 関数を足す・動かす・名前を変えるたび |
| [シナリオ別の呼び出し追跡.md](シナリオ別の呼び出し追跡.md) | 操作ごとの呼び出しの連鎖と分かれ道（403／404／409／423 の条件） | 経路が変わったら |

## いまの形をひとことで

```
cmd/w-cms  ── ext_*.go のブランク import（ビルドタグはここだけ）
   ├→ ext/toho          ── ext/comm, ext/comm/contacts を import
   ├→ ext/comm/mail     ── ext/comm, ext/comm/contacts を import（署名）
   ├→ ext/comm/contacts ── ext/comm を import
   └→ ext/comm          ── ext/ を1つも import しない
                ↓（全部が）
           internal/cms（コアは ext/ を知らない。受けるのは11の登録の口だけ）
```

作れる組は3つです。**通常**・**`-tags nomail`**（メールだけ外す）・**`-tags minimal`**（素の w-cms）。
何が載っているかは、起動ログ（「拡張セット: …」「有効な設定: …」）と `/api/tag-schema` の `extensions` に出ます。

画面の保存は `POST /api/sync` だけです。書ける形と閲覧モードの切り替え・ぶつかったブロックの並べ方・ページの更新の知らせは、
[シナリオ別の呼び出し追跡.md](シナリオ別の呼び出し追跡.md) の §2・§3 にあります。

## この文書群は何度も作り直しています

手書きの「いまの姿の写し」は、実装と同じ速さで古びます。2026-08-21 には、**行数の主張30件のうち16件が誤り、一部は中身が逆**という状態になりました。
読んだ人が正しい実装をバグと思い込みかねないので、一度全部を消しています。2026-08-27 に作り直し、以後 08-30・09-02・09-04・09-14・09-15・09-16・09-22・10-01 に
測り直し・作り直しをしました。**10-01 から 10-12 までの変更を受けて、2026-10-14 に作り直した**のがこの版です。

同じ轍を踏まないための決め事です。

- **説明の本体は `go doc`（コードの中）へ置きます。** ここが受け持つのは、横断的な話だけです。
- **数は対象コミットの実測で、仕様ではありません。** 各文書に、数え直すコマンドを添えてあります。
- **古くなったら、直すのではなく作り直します**（スキル `/code-reference`）。直し続けるより安い、というのが 2026-08-21 の判断でした。

**この3本を読むより先に**、[作業引き継ぎ.md](../../作業引き継ぎ.md) の「覚えておくこと（知らないと必ず踏む罠）」に目を通してください。
短くて、知らないと壊すものだけが並んでいます。
