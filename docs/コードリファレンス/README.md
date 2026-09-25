# コードリファレンス

**対象コミット: `1b783f8`（2026-09-22 測り直し。コードは `0278690`＝[全体地図.md](全体地図.md) の測定時から不変）**

実装の**現状**を写した文書群です。設計の「なぜ」は各設計書が正本で、ここは
「いま何がどうなっているか」だけを扱います。

この版は **2026-09-16〜22 の6日間（149コミット・Go本体 +7,800行）**を受けて、実際の
コードから測り直したものです。いちばん大きいのは**下請け業務（`ext/subcon`）が
12 → 33 ファイルに育ったこと**——受注残表・未手配の一覧・発注書の作成とPDF発行・
手配状況・材料の参考単価・寸法の分解と検索・検算・弊社品番の自動結び。**拡張が初めて
表を持ち**（`material_dimensions`）、観察係は2人になりました。コアの登録の口は
9 → 11（`RegisterRequiredPage`・`RegisterSuggestSource`）。**表示専用クロームは
サニタイズの冒頭で落とします**（`StripChrome`——`/api/load` の出力を `/api/save` へ
書き戻して鏡が本文に焼き付いた 2026-09-21 の事故への守り）。

## まず `go doc`

説明の本体は**コードの中**にあります。パッケージの冒頭コメントを読むのが最短です。

```bash
go doc ./cmd/w-cms                 # 起動の流れ
go doc ./internal/cms              # 中核（正本・語彙・3層・回覧）＋公開APIの一覧
go doc ./internal/cms/page         # サイドカーと認可・添付の置き場
go doc ./internal/cms/editlock     # 悲観ロック
go doc ./internal/cms/htmldoc      # 本文サニタイズ
go doc ./internal/database         # cms.db（派生）と auth.db（正本）

go doc ./internal/cms Observer                  # 型・関数を1つだけ
go doc ./internal/cms RegisterUploadInterceptor # コアが通信箱を知らないための受け口
go doc ./internal/cms RegisterContactResolver   # アドレス→連絡先ページ
go doc ./internal/cms RegisterSettingsSection   # 拡張の設定の節
go doc ./internal/cms RegisterExtension         # 載っている拡張の名簿
go doc ./internal/cms RegisterRequiredPage      # 置き場（トップ直下の名前で機能が決まるページ）の名簿
go doc ./internal/cms RegisterSuggestSource     # 入力の候補の出どころ

go doc ./ext/comm                  # 通信（通信箱・取り込み係・メールの口）
go doc ./ext/comm IntakeHandler    # 取り込み係の受け口
go doc ./ext/comm Mailer           # メールの口（実装は ext/comm/mail）
go doc ./ext/comm/contacts         # アドレス帳（なぜ独立した拡張か・コアとの境目）
go doc ./ext/comm/mail             # メール送受信（IMAP／SMTP）
go doc ./ext/subcon                # 下請け業務

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
   ├→ ext/subcon        ── ext/comm, ext/comm/contacts を import
   ├→ ext/comm/contacts ── ext/comm を import
   ├→ ext/comm/mail     ── ext/comm を import
   └→ ext/comm          ── **ext/ を1つも import しない**
                ↓（全部が）
           internal/cms（コアは ext/ を知らない。受けるのは11の登録の口だけ）
```

作れる組は3つ——**通常**・**`-tags nomail`**（メールだけ外す）・**`-tags minimal`**
（素の w-cms）。何が載っているかは起動ログ（「拡張セット: …」）と
`/api/tag-schema` の `extensions` に出ます。

## ⚠ この文書群は一度畳まれています（2026-08-21）

手書きの「現状の写像」は実装と同じ速度で陳腐化し、**行数主張30件中16件が誤り・
一部は内容が逆**という状態になりました——読んだ人が正しい実装をバグと誤認しかねない、
という理由で全部消しています。2026-08-27 に作り直し、以後 2026-08-30・09-02・09-04・
09-14・09-15・09-16 に測り直し、**2026-09-16〜22 の6日間を受けて 2026-09-22 に作り直した**
のがこの版です。

同じ轍を踏まないための決め事:

- **説明の本体は `go doc`（コードの中）へ置く。** ここが担うのは横断的な話だけ。
- **数字は対象コミットの実測であって仕様ではない。** 各文書に数え直すコマンドが添えてあります。
- **古くなったら直すのではなく作り直す**（スキル `/code-reference`）。
  直し続けるより安い、というのが 2026-08-21 の判断でした。

**この3本を読むより先に**、[../作業引き継ぎ.md](../作業引き継ぎ.md) の
「覚えておくこと（知らないと必ず踏む罠）」に目を通してください——短くて、
知らないと壊すものだけが並んでいます。
