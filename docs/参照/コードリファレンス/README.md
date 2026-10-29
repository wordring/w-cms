# コードリファレンス

**対象コミット: `609bb6ef`（2026-10-30。`c20d76b6` のコードから作り直し、`609bb6ef` までの変更を写しました・[全体地図.md](全体地図.md) と同じ測定）**

実装の**いまの姿**を写した文書群です。設計の「なぜ」は、各設計書と【考察】が正本です。ここは「いま何がどうなっているか」だけを扱います。

前の版（`8fff53c`・2026-10-21）のあと、コードが大きく変わったので、作り直しました。
本体の Go は **257 → 324 ファイル・61,045 → 76,478 行**になりました（どちらも `cmd`・`internal`・`ext` の、試験を除く数）。大きく変わったのは、次の6つです。

- **機械の入口と、業務サーバーの中だけの入口ができました**（2026-10-21〜26）。
  電話の道具と FAX の受信は、前置き `/api/machine/` の API を鍵で呼びます（`internal/auth/machine.go`）。
  業務サーバーの中のファイル（Unix ソケット）から来た要求は、決めた1人の利用者として扱います（`internal/auth/local_socket.go`）。
- **電話と FAX を w-cms が扱います**（2026-10-21〜28）。
  常駐の道具 `cmd/w-cms-phone`（着信の記録）と `cmd/w-cms-dial`（モデムで電話をかける）が増えました。
  `ext/comm` に、着信・出ていない着信・FAX の受信と送信・電話をかける API と、ブラウザへの通知（Web Push）が入りました。
- **消す前の確かめがコアに入りました**（2026-10-23〜28）。
  ほかのページから指されているページ・索引に入った表を持つページは、ふつうの削除では断ります（`delete_impact.go`・`referrers.go`・`page_links.go`）。
- **画面と添付をブラウザの手元に残し、写真と PDF を軽く見せます**（2026-10-27）。
  画面のファイルと写真の住所に版の印を添えます。写真の縮小・表示用の写し・PDF の絵・立ち上がりの答えの埋め込みも入りました。
- **東邦の業務が、製造ページの作業・在庫・相乗り・業者の納品書などへ広がりました**（2026-10-21〜29）。東邦のルートは 76本です。
- **振る舞いを変えないリファクタリングを5段で入れました**（2026-10-29〜30）。名前ごとの錠 `internal/keylock` と、写していた手順を寄せた道具の関数が増えています。

`c20d76b6` のあと、2026-10-30 に、画面から呼ばれていない API 5本（`/api/replies`・`/api/thread`・`/api/record-makers`・`/api/mail/import`・`/api/validate-parent`）と、呼び手の無い関数3つを外しました。手配の対象は、案件残表と同じページ種類（`caseKinds`）で選びます。

## まず `go doc`

説明の本体は**コードの中**にあります。パッケージの冒頭コメントを読むのが近道です。

```bash
go doc ./cmd/w-cms                 # 起動の流れ
go doc ./internal/cms              # 中核（正本・語彙・3層・回覧）と公開APIの一覧
go doc ./internal/cms/page         # サイドカーと認可・添付の置き場
go doc ./internal/cms/editlock     # 悲観ロック（画面は使わない。道具のために残る）
go doc ./internal/cms/htmldoc      # 本文のサニタイズ
go doc ./internal/database         # cms.db（派生）・tables.db（表の写し・派生）・auth.db（正本）・データの場所
go doc ./internal/auth             # セッション（最後の操作から8時間）・パスワード・監査・機械の入口・業務サーバーの中の入口
go doc ./internal/keylock          # 名前ごとの錠（2026-10-29）
go doc ./internal/extgen           # 拡張の取り付け口を作る決まり

go doc ./internal/cms SyncAPIHandler            # ブロック単位で合わせる保存の API
go doc ./internal/cms LockPageBody              # ページごとの書き込みのロック
go doc ./internal/cms RewriteBodyUnlessHeld     # 道具が握っていれば書かない書き換え
go doc ./internal/cms PageDraft                 # テンプレートを写して印を埋める道具
go doc ./internal/cms EditBody                  # 既にある本文を、同じ道具で書き換える
go doc ./internal/cms DeleteImpactOf            # 消すと何が壊れるか（2026-10-23）
go doc ./internal/cms RegisterRemovalGuard      # 拡張が「消させない」を決める登録の仕組み（2026-10-28）
go doc ./internal/cms RegisterBackgroundTask    # 裏で動き続ける仕事の登録（2026-10-21 夜）
go doc ./internal/cms RegisterPublicMirror      # ログインしていない人にも描く鏡（2026-10-26）
go doc ./internal/cms RegisterTagDecorator      # タグの値に飾りを足す登録の仕組み
go doc ./internal/cms RegisterPageTemplate      # 機械が写すテンプレートの名簿
go doc ./internal/cms RegisterRequiredPage      # 置き場（トップ直下の、名前で機能が決まるページ）の名簿
go doc ./internal/cms SendPush                  # ブラウザの通知（Web Push）

go doc ./ext/comm                  # 通信（通信箱・取り込み係・機械の入口・FAX・電話・メールの API・記録の箱）
go doc ./ext/comm IntakeHandler    # 取り込み係のインターフェース
go doc ./ext/comm SendPurpose      # 送る欄の用件（FAX の紙と送ったあとも持つ）
go doc ./ext/comm RecordMaker      # 記録から作るページの種類
go doc ./ext/comm RecordSection    # 記録の箱へ節を足す
go doc ./ext/comm/contacts         # アドレス帳（なぜ独立した拡張か・コアとの境目）
go doc ./ext/comm/mail             # メール送受信（IMAP／SMTP）
go doc ./ext/toho                  # 東邦の業務

go doc -all ./internal/cms/page      # そのパッケージの全公開APIをコメントごと
go doc -src ./internal/cms Sanitize  # 実装も見る
```

> `internal/` の下なので、pkg.go.dev には出ません（出す必要もありません）。

## 3本の文書

| 文書 | 何が書いてあるか | 直す目安 |
|---|---|---|
| [全体地図.md](全体地図.md) | パッケージの構成・依存の向き・入口・**何が正本か**・ファイルの役割・知っておくべき食い違い | 構造が変わったら必ず |
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
           internal/cms（コアは ext/ を知らない。受けるのは14の登録の仕組みだけ）

cmd/w-cms-phone・cmd/w-cms-dial・cmd/w-cms-edit … w-cms の Go を import しない道具（HTTP か Unix ソケットだけでつながる）
```

組み立てられる組は3つです。**通常**・**`-tags nomail`**（メールだけ外す）・**`-tags minimal`**（素の w-cms）。
何が載っているかは、起動ログ（「拡張セット: …」「有効な設定: …」）と `/api/tag-schema` の `extensions` に出ます。
拡張のフォルダを足し・外したら、`go generate ./cmd/w-cms` を流してから組み立てます。

画面の保存は `POST /api/sync` だけです。入口は、ふつうの待ち受け・機械の入口（`/api/machine/`）・業務サーバーの中のファイルの3つです（[全体地図 §2f](全体地図.md)）。

## この文書群は何度も作り直しています

手書きの「いまの姿の写し」は、実装と同じ速さで古びます。2026-08-21 には、**行数の主張30件のうち16件が誤り、一部は中身が逆**という状態になりました。
読んだ人が正しい実装をバグと思い込みかねないので、一度全部を消しています。
2026-08-27 に作り直し、以後 08-30・09-02・09-04・09-14・09-15・09-16・09-22・10-01・10-14・10-21 に測り直し・作り直しをしました。
**10-21 から 10-30 までの変更を受けて、2026-10-30 に作り直した**のがこの版です。

同じ失敗をしないための決め事です。

- **説明の本体は `go doc`（コードの中）に置きます。** ここが受け持つのは、パッケージをまたぐ話だけです。
- **数は対象コミットで数えたもので、仕様ではありません。** 各文書に、数え直すコマンドを添えてあります。
- **古くなったら、直すのではなく作り直します**（スキル `/code-reference`）。直し続けるより安い、というのが 2026-08-21 の判断でした。

**この3本を読むより先に**、[作業引き継ぎ.md](../../作業引き継ぎ.md) の「覚えておくこと（知らないと必ず踏む罠）」に目を通してください。
短くて、知らないと壊すものだけが並んでいます。
