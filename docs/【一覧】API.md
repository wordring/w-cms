# 【一覧】API

w-cms が提供するHTTPエンドポイントの**実装済みリファレンス**。**ルーティングの正本は2種類**で、
本ファイルはその鏡です。

- **コアの口** … [cmd/w-cms/main.go](../cmd/w-cms/main.go) の `buildHandler()` のルート表。
- **拡張の口** … 各プラグインの `Routes()`。`main.go` は `cms.PluginRoutes()` をループして
  登録するだけなので、**`main.go` を見ても出てきません**。`-tags` で拡張を外すとルートごと
  消えます。内訳（2026-09-16 の起動ログ実測・登録順）:

  | 拡張 | `Routes()` の正本 | 口 |
  |---|---|---|
  | `ext/comm`（通信） | [comm_routes.go](../ext/comm/comm_routes.go) | `/api/replies`・`/api/thread`・`/api/intake/handled`・`/api/intake/memo` |
  | `ext/comm/contacts`（アドレス帳） | [contacts/plugin.go](../ext/comm/contacts/plugin.go) | `/api/contacts/register`・`/api/contacts/unfile` |
  | `ext/comm/mail`（メール） | [mail/plugin.go](../ext/comm/mail/plugin.go) | `/api/mail/status`・`/api/mail/signin`・`/api/mail/import`・`/api/mail/send` |
  | `ext/subcon`（下請け業務） | [subcon/materials.go](../ext/subcon/materials.go) | `/api/required-materials`・`/api/analyze-attachment`・**`/api/parse-pdf`**（2026-09-16 にコアから移設）・`/api/filing-proposal`・`/api/file-drawings`・`/api/analyzed` |

  **起動ログで確かめられます**——`プラグインAPI登録: <パス>` が1本ずつ出ます。

> エンドポイントを増減したら本ファイルも同時に更新すること。かつてこの一覧が
> [アーキテクチャとDBスキーマ.md](アーキテクチャとDBスキーマ.md) の中に埋もれていた結果、
> 存在しない `/api/new-id` が残り、`/api/children`・`/api/tag-schema`・`/api/lock/force`・
> `/api/admin/*` が載っていない状態になっていた。独立した一覧にしたのはそのため。

---

## 1. 認可の区分

| 区分 | 意味 | 適用 |
|---|---|---|
| **要認証** | 未ログインは API=401 / 画面=`/login` へ302 | `/api/` 配下の既定（`RequireAuth`） |
| **任意認証** | 匿名でも到達でき、各ハンドラが**実効公開**で個別判定する | `OptionalAuth` を明示したルート |
| **認証不要** | 誰でも取得できる（秘密でない情報） | ログイン画面・語彙など |

ページ単位の認可は Unix風の read/write（[認証認可設計.md](認証認可設計.md) §3）。
**実効公開**＝自分と全先祖が `public` のとき匿名に開く（同 §10.2）。

さらに横断で2つのミドルウェア `CSRFProtect`（Origin/Referer とホストの一致検証）と `CSPProtect`
（strict CSP の付与）が最外周に掛かる——中身と経緯は [認証認可設計.md](認証認可設計.md) §0.1・§2.3・§6。

ここは**黙って壊れる層**です——ハンドラを `protected` から `root` へ移す・`OptionalAuth` を
付け忘れる・ミドルウェアの入れ子を外す、といった退行が起きても既存のテストは green のまま
実害だけが出ます。そのため `buildHandler()` を `main` から切り出し、ルートごとの保護レベルと
上記2つの配線を [route_guard_test.go](../cmd/w-cms/route_guard_test.go) が固定しています
（`csp_test.go` はポリシー文字列を見るだけで、配線までは見ていない）。

### 1.1. メソッドの関門（状態を変える口は POST 固定）

`CSRFProtect` は GET/HEAD/OPTIONS を検証しません。**状態を変える口は、ハンドラ自身が
メソッドを絞ります**——GET で通ると本文へ `<img src="/api/…">` を1つ保存するだけで、
開いた全ログインユーザーに同じ操作をさせられます（同一オリジンなので SameSite も CSP も
止めない。実例は `/api/logout` と `/api/new-page`——理屈は
[セキュリティ設計.md](セキュリティ設計.md) §1）。

固定しているのは [method_guard_test.go](../internal/cms/method_guard_test.go)（2026-09-14 新設）で、
**状態を変えるハンドラ16本を直接呼んで 405 を期待**します。加えて JSON で答える読み取り口が
POST を**JSONで**断ることも見ます。

⚠ **`route_guard_test.go` の `TestStateChangingRoutesRejectGET` では足りません**——
`buildHandler()` へ流すので `RequireAuth` が先に 401 を返し、**ハンドラ自身のメソッド確認が
一度も走りません**（「401 か 405 なら合格」だったので、確認を丸ごと外しても緑でした）。
この穴で `/api/page-chown` のメソッド関門の欠落を 2026-09-14 まで見逃しています
——GETで状態が変わらなかったのは `DecodeJSONBody` が空ボディで落ちるという**偶然**でした。

### 1.2. 失敗応答の形

JSONで答えるAPIの失敗は `JSONFail`（`handler_save.go`）が
`{"success": false, "message": …}` を書きます。**必ず意味のある状態行が付きます**
——2026-09-14 まで「HTTP 200 のまま `success:false` を返す」口が**13箇所**あり、
`res.ok` を見る受け手からは失敗が成功に見えていました。いまは `status == 0` の分岐そのものが
畳まれています（0 が来たら 500 へ倒す）。

| 状態行 | 意味 | 例 |
|---|---|---|
| **400** | 入力が不正 | ページIDが不正・宛先が空・PDFの読み込みに失敗 |
| **401 / 403** | 認証・認可（`page.Require*` が返す） | 未ログイン／権限なし |
| **404** | 読めない・存在しない（**区別させない**） | JSON読み取り口の共通の関門 `GateJSONPageRead` |
| **405** | メソッドが違う（上の §1.1） | 状態を変える口への GET |
| **409** | システムの状態が整っていない | 通信箱のページが無い・メール未サインイン・編集ロック・**編集中のページへの機械の書き込み**（§3） |
| **413** | JSONボディが 8MiB 超 | `DecodeJSONBody` |
| **423** | ロックが他者保持中 | `POST /api/lock` |
| **501** | プラグインが入っていない | メール送信（**`comm.ErrNoMailer`**。2026-09-15 に `internal/cms` から `ext/comm` へ移った。`comm.RegisterMailer` を通った実装が無い——いまは防御的な枝で、`ext/comm/mail` が載っていれば起きない） |
| **502** | 上流が失敗した | Gemini・SMTP |
| **503** | 機能が構成されていない | `GEMINI_API_KEY` 未設定・メールの設定が無い |

受け取る側（`assets/app.js`）は `readResult(res)` で**本体を1回だけ**読み、JSONにならなければ
本文をそのまま理由にします。`failMessage(res, d)` が `message` → `statusText` → 状態行の順で
1行を作ります——**数字は最後の手段**です。

### 1.3. ページIDは入口で6桁へ畳む

対象ページの `id` / `page_id` を受けるハンドラは、最初に `page.NormalizeID` を通します
（不正・空は 400「ページIDが不正です」）。2026-09-14 に**畳んでいなかった5つ**を揃えました
——`/api/page-meta`・`/api/validate-parent`・`/api/load`・`/api/lock-events`・`/api/lock/force`。
`strconv.Atoi` した数値しか使っていないので実害はありませんでしたが、
`page.GetPageDir(id)` / `page.AttachmentDir(id)` は**文字列を取る**ので、あとで1行足した人が
`"1"` を渡すと `data/1/1.html` を探しに行きます。**理由の書いていない例外は、次の人には
区別が付きません。**

実測: `id=10272` → `010272` として引ける／`id=-1`・`id=abc` → 400（前は `Atoi` が
`-1` を通して404になっていた）。

⚠ **まだ畳んでいない口が3種類あります**（2026-09-14 時点・いずれも数値としてしか使わない）:
`/api/unlock` の `id`（`Locks.Release` に渡すだけ）、`/api/required-materials` の `page_id`、
そして**親を指す `parent`**（`/api/new-page`・`/api/set-parent`・`/api/validate-parent`）。

## 2. ページ本文・属性

| メソッド | パス | 認可 | 編集ロック | 概要 |
|---|---|---|---|---|
| GET | `/{id}` | 任意認証 | — | **ページ本体**。本文とタイトルを埋め込んだ完成HTMLを返す（サーバー合成）。**殻は相手で分かれる**（2026-08-26）——認証済みは編集用 `assets/index.html`（`RenderPageShell`・`Cache-Control: no-store`）、匿名は**公開専用** `assets/public.html`（`RenderPublicShell`・スクリプト無し・`description`/OGP/canonical つき・`public, max-age=600` ＋ `Vary: Cookie` ＋ `ETag`）。本文はサニタイズ後に**計算ビューの中身が埋められる**（`RenderComputedViews`。下記の注記）。権限無し=403（体裁つきのHTML）／匿名×非公開=404（トップだけ `/login` へ302）／不存在=404 |
| GET | `/api/load` | 任意認証（read） | — | ページ本文（`text/plain`）。初期表示では使わず、**編集ロック起点の載せ替え専用**。`id` は入口で6桁へ畳み、**空も不正も400「ページIDが不正です」**（2026-09-14。前は空だけ `Missing id` で分けていたが、呼ぶ側にできることは同じ）。**描画時と同じくサニタイズを通し**、計算ビューの中身を埋めて返す。**ページ内アンカーの合成（`RenderAnchors`）と参照リンクの合成（`RenderReferenceLinks`）は通さない**——合成した id や `<a>` がエディタのDOMへ入ると本文として保存されるため（下記の注記） |
| POST | `/api/save` | 要認証（write） | 要 | 本文全体を保存。サニタイズ結果と `sanitized`、レジストリ未定義の `data-type` の告知 `unknown_types`、見出しの改名で計算に読まれなくなった項目の告知 `unresolved_fields`、殻の接頭辞を剥がした id の告知 `stripped_ids` を返す（下記の注記）。JSONボディは**8MiB上限**（超過は413。JSONを受けるAPIは共通） |
| POST | `/api/save-block` | 要認証（write） | 要 | `data-id` で指定した**1ブロックだけ**保存。対象が無い／重複なら **409**（クライアントは全文保存へフォールバック）。応答は `/api/save` と同形（`unknown_types`・`unresolved_fields`・`stripped_ids` は当該ブロック分のみ） |
| GET | `/api/page-meta` | 任意認証（read） | — | ページ属性（親ページID・親ページ名・更新日時など）。匿名には実効公開のときだけ返す |
| GET | `/api/children` | 任意認証（親のread） | — | 子ページ一覧（ID昇順）。認証済みには read 権限のある子、匿名には**実効公開の子だけ**を絞って返す（`visibleChildren`。計算ビューのサーバー事前描画と共用） |
| POST | `/api/delete-page` | 要認証（write） | 要 | ページを**ゴミ箱（`data/trash`）へ移し**、索引から取り除く。物理削除ではない（取り消せることが要件）。**トップページは400**／**子ページを持つと409**（件数つき）／他者ロック中は409。応答は `{success, page_id, trash_path}` |
| POST | `/api/new-page` | 要認証（親のwrite） | — | 子ページを作成し `/{新ID}?edit=true` へ302。**POST限定**（GETは405）——GETで通ると本文へ `<img src="/api/new-page?parent=…">` を1つ保存するだけで、開いた全ログインユーザーにページを作らせられる（同一オリジンなので SameSite も CSP も止めない。`method_guard_test.go` が固定）。引数は `r.FormValue` なのでPOSTボディでもクエリでも受ける。**親の指定は必須**（親なしにできるのはトップページ `000000` のみ）。`template=<ページID>` を付けるとそのテンプレートの本文を写して**新規化**する（空欄を列型の既定値で埋める。日付は今日・`order-no` は再採番）。テンプレートに使えるのは「テンプレート」フォルダ配下の**葉**だけで、分類フォルダとルートは400。テンプレートの**read権限が要る**。検証は**採番より前**に行う（ファイルの無いページ行を残さないため） |
| GET | `/api/templates` | 要認証 | — | テンプレート選択メニューの中身。「テンプレート」フォルダ（トップ直下・同名）配下のツリーを `[{id,title,children}]` で返す。**枝は分類・葉がテンプレート**。ルートが無ければ `[]`（従来どおり空のページだけが作られる）。read 権限で絞られる（`visibleChildren`） |
| GET | `/api/validate-parent` | 要認証（write） | — | 親付け替えの事前検証（循環・自己参照・存在チェック）。`/api/set-parent` と同じ `validateParentChange` を共有する。**現在フロントからは呼ばれていない**（`applyParent()` は `/api/set-parent` の応答だけで判定する） |
| POST | `/api/set-parent` | 要認証（write） | 要 | 親ページの付け替え |
| GET | `/api/versions` | 要認証（read） | — | **版の一覧**を新しい順で返す（`[{id, at, by, size, hash}]`。2026-08-26）。版は本文そのものなので本文と同じ read を要求する |
| GET | `/api/version` | 要認証（read） | — | 指定した版の本文（`?id=&v=`）。`text/plain` ＋ `nosniff` ＋**描画時と同じサニタイズ**（`/api/load` と同じ扱い）。版IDは時刻由来の形しか受け付けない＝ページのフォルダの外は指せない |
| POST | `/api/revert` | 要認証（write） | 要 | 選んだ版を**本文として書き戻す**。戻すのは本文HTMLだけで、サイドカー（親・所有者・権限・公開）は触らない＝**リバートが権限昇格の抜け道にならない**。書き戻す前に「いまの内容」を版として残すので、リバート自体も取り消せる。監査記録は `revert` |
| GET | `/data/...` | 任意認証（read） | — | 添付ファイル配信の**旧形（`/data/master/<xx>/<id>/…`・互換）**。新しい添付は下のきれいなURLで配る（`DataFileHandler`。ページフォルダ直下と `files/` の両方を受ける）。ページのread権限を要求する保護ハンドラ。`nosniff` 付き。**PDFとラスタ画像（png/jpeg/webp/gif）はインライン**、**SVG は不活性化**（`image/svg+xml` ＋ `Content-Disposition: attachment` ＋ この応答限定の `Content-Security-Policy: sandbox; default-src 'none'`）、**それ以外はダウンロード扱い**（`application/octet-stream`＋`attachment`。応答ヘッダの決め方 `setAttachmentHeaders` はきれいなURLと共有）。本文とサイドカーは配らない |
| GET | `/{6桁ページID}/{ファイル名}` | 任意認証（read） | — | **添付のきれいなURL**（2026-08-31「実際に保存される場所を推測されたくない」）。`RootHandler` がページURLの下の2区画パスを `page.ServeCleanAttachment` へ回し、ページフォルダの `files/` サブフォルダから配る（物理配置はURLに出ない）。認可と応答ヘッダは `/data/...` と同じ（`RequirePageReadOrPublic`・`setAttachmentHeaders`）。無ければ404で、**匿名には「読めない」と「存在しない」を区別させない**。各アップロードAPIが返す `href` はこの形 |

> **計算ビューのサーバー事前描画**: 本文を返す入口は `/{id}`（`RootHandler`）と `/api/load`
> （`LoadAPIHandler`）の**2つだけ**で、どちらも `RenderComputedViews` を通します。
> 正本は [アーキテクチャとDBスキーマ.md](アーキテクチャとDBスキーマ.md) 4.4。

> **保存応答の3つの告知**（いずれも**拒否ではなく告知**——エコーバックの流儀）
>
> - **`unknown_types`**（`[]string`）: 本文中の `table`／`dl`／`section` が持つ `data-type` のうち、
>   語彙レジストリに宣言が無いもの。未知の形式も**そのまま保存し**、②汎用索引にも載る。
>   画面での告知だけは**他と違う扱い**で、赤地・太字の目立つトースト（`.toast.alert`）を出し、
>   **時間では消さない**（閉じるまで残る。2026-08-25。ユーザー要望「赤色背景などで告知して
>   ください」）。`data-type="cliet-order"` のような綴り違いはその塊が③計算から静かに外れる
>   ので、控えめな通知に紛れると気づけないため。
> - **`unresolved_fields`**（`[]string`）: レジストリが宣言しているのに、文書の見出しから
>   解決できなかった列（`"顧客の発注書: 発注元"` の形）。項目の鍵は**見出しの表示文字**なので、
>   見出しを改名すると③計算プラグインが読めなくなり、型付きテーブルへの同期が**黙って**止まる。
>   それを保存時に気づけるようにするための告知（2026-08-20 追加。`data-field` 撤去の前提）。
>   報告するのは**機械キーを持つ列だけ**で、かつ**改名の徴候があるとき**（解決されなかった
>   宣言列があり、なおかつどの宣言列にも当たらない見出しがある）に限る——列を消しただけ・
>   独自の列を足しただけでは黙る。
> - **`stripped_ids`**（`[]string`）: 本文の `id` のうち、**殻が独占する接頭辞**
>   （`w-`）が付いていたもの。サニタイズが接頭辞を剥がして保存を通すので、書き手が
>   「意図した名前と違うものになった」ことに気づけるよう告知する（2026-08-20 追加）。
>   走査するのはサニタイズ**前**のHTML——後では接頭辞が消えていて分からない。

## 3. 編集ロック（同時編集の競合対策）

設計は [【考察】同時編集の競合対策.md](【考察】同時編集の競合対策.md)。

| メソッド | パス | 認可 | 概要 |
|---|---|---|---|
| POST | `/api/lock` | 要認証（write） | ロック取得（`{ok, token}`）。取れなければ **423 Locked** ＋ `{ok:false, holder, same_user, grace_remaining_sec}`。**本文は返さない**——取得後にフロントが `GET /api/load` を読む（そちらは計算ビューのSSRを通るため。2026-08-20 変更） |
| GET | `/api/lock-events` | 要認証（write） | ロック状態の **SSE** 購読（保持者・待機者で共用） |
| POST | `/api/unlock` | 要認証 | ロック解放。**write は見ない**（解放できるのはトークンが一致する保持者本人だけ）。タブを閉じるときは `navigator.sendBeacon` で送る |
| POST | `/api/lock/force` | **admin のみ** | ロックの強制解放（保持者が落ちてスタックしたときの救済） |

ページの状態を変えるAPIは**同じロックで直列化**される（計10本）。ただし入口は2種類ある。

- **保存2本**（`/api/save`・`/api/save-block`）は `editlock.Locks.Validate` を直接呼び、
  トークンは**JSONボディの `token`** で受ける。
- **残り8本**（`/api/set-parent`・`/api/page-perms`・`/api/page-chown`・`/api/delete-page`・`/api/revert`・
  `/api/upload-pdf`・`/api/upload-image`・`/api/upload-file`）は共通ゲート `editlock.RequireEditLock` を通り、トークンは
  **`X-Lock-Token` ヘッダ**（無ければ `token` クエリ）で受ける。フロントはこの8本を
  `lockedFetch`（または同じヘッダを手で付けた `fetch`）で送る。
  例外は `/api/upload-file` が**通信箱への取り込み**に回る場合で、このときはロックを見ない
  （通信箱の本文は変わらず子ページが生まれるだけ。§7 の同APIの行）。

検証規約はどちらも同じ（ロック無し＝許可／保持者本人でトークン一致なら許可／それ以外は409）。

**機械が既存ページの本文を書き換える口には、3本目の関門があります**（`editlock.RefuseWhileEditing`・
2026-09-14）。連絡先の登録（`/api/contacts/register`）・未分類へ戻す（`/api/contacts/unfile`）・
「対応」タグ（`/api/intake/handled`）・整理の実行（`/api/file-drawings`）がそれで、いずれも
**一覧画面のボタン**です——エディタを開いていないので編集トークンを持てず、`RequireEditLock` を
通すと必ず409になります。かといって素通しにすると、`RewriteBody` は**読んで・変えて・書く**ので
オートセーブと上書きし合います。

- **誰かが開いていれば断ります。自分が開いていても断ります**（手元のエディタは書き換え前の
  本文を持っているので、保存すれば機械の変更が消える）。
- **保持者が居なくなったロックは無視します**（判定は明け渡しと同じ `holderPresent`）。
- **断り方は口の流儀に合わせてあります**——1ページだけ触る口（連絡先の2本）は**409**、
  まとめ押し（`/api/intake/handled`）は**1件ずつ `failed` に数えて飛ばす**、
  整理は**行ごとに `outcome: "skipped"`**（複数のページへ書くので、1枚が編集中でも残りは
  片付けられるほうがよい）。

決定の経緯は [【考察】同時編集の競合対策.md](【考察】同時編集の競合対策.md) §9（2026-09-14）。

## 4. 権限・所有者

| メソッド | パス | 認可 | 編集ロック | 概要 |
|---|---|---|---|---|
| GET | `/api/page-perms` | 要認証（read） | — | 現在の権限と `can_write`・`can_publish` を返す |
| POST | `/api/page-perms` | 要認証（owner/admin） | 要 | mode（owner/group/other の rw）と `public` フラグの変更 |
| POST | `/api/page-chown` | **admin のみ** | 要 | 所有者（`owner`）の変更。所有グループの変更は `/api/page-perms` の `group` 側 |

## 5. 認証

| メソッド | パス | 認可 | 概要 |
|---|---|---|---|
| GET | `/login` | 認証不要 | ログイン画面 |
| POST | `/api/login` | 認証不要 | ログイン。argon2id 検証（同時実行はセマフォで4件まで）。失敗は `login_attempts` に記録し、**5回連続で15分ロックアウト** |
| POST | `/api/logout` | 要認証 | ログアウト（セッション行を削除）。**POST限定**（GETは405）——`/api/new-page` と同じ保存型CSRFで、本文の `<img src="/api/logout">` 1つで開いた全員を無音で追い出せた |
| GET | `/api/me` | 任意認証 | 認証状態。未認証は `{authenticated:false}` |

## 6. 管理（admin限定）

管理コンソールは [assets/admin.html](../assets/admin.html)。

| メソッド | パス | 概要 |
|---|---|---|
| GET/POST | `/api/admin/users` | ユーザーの一覧・作成 |
| POST | `/api/admin/users/password` | パスワード再設定 |
| POST | `/api/admin/users/disable` | 有効・無効の切り替え |
| GET/POST | `/api/admin/groups` | グループの一覧・作成 |
| POST | `/api/admin/groups/members` | グループ所属の変更（`action` に `add`／`remove`。既定は `add`）。参照用のGETは無い |
| GET | `/api/admin/audit` | 監査ログの参照。直近200件。記録対象は認証イベント（`login`/`login.fail`/`logout`）・保存・ページ作成／削除・添付（`attach`/`attach.overwrite`）・親の付け替え・権限変更（公開切替 `publish`/`unpublish` を含む）・ロック強制解除・索引の全再構築・ユーザー／グループ管理・取り込み（`intake.create`/`intake.duplicate`）・PDF判定（`analyze-pdf`）（[認証認可設計.md](認証認可設計.md) §9.4） |
| POST | `/api/rebuild-db` | `data/master` から `cms.db` を再構築（派生インデックスの洗い替え）。先頭で `config/settings.json` を読み直す |

> ⚠ **`/api/intake/memo`・`/api/intake/handled` はここではありません**（2026-09-16 に直した）。
> どちらも **admin 限定ではなく**（要認証＋対象ページの write）、**コアの口でもありません**
> ——`ext/comm` の `Routes()` から生えます。§7 の末尾へ移しました。

> **撤去済みの一括移行API**: `POST /api/migrate-vocab`（旧カスタム要素→語彙モデル。2026-08-20 撤去）、
> `POST /api/migrate-headings`・`POST /api/migrate-attachments`（見出し形（D-2）への変換と添付の
> `files/` への移動。2026-08-31 導入・両環境適用後 2026-09-02 撤去）。いずれも管理コンソールに
> ボタンは無く、経緯は [変更履歴.md](変更履歴.md)。

## 7. 語彙・PDF・プラグイン

| メソッド | パス | 認可 | 概要 |
|---|---|---|---|
| GET | `/api/tag-schema` | 認証不要 | **本文の語彙**。`elements`（構造HTML → 許可属性。`data-*` マーカーもここに属性として現れる。**カスタム要素はゼロ**）・`void`（終了タグを書かない要素）・`block_id`（`data-id`）・`vocab`（語彙レジストリの形式定義を `type` 順で。既定のビルドで**21形式**——コア5＋`ext/subcon` 14＋`ext/comm` 1＋`ext/comm/contacts` 1（2026-09-15 にコアから出たのは `unhandled-intake`→`ext/comm` と `unknown-contacts`→`ext/comm/contacts` の2つ。`-tags minimal` の実測は5形式）。スラッシュメニューと挿入骨格の生成元）・`vocabulary`（見出し語の辞書。1語につき `{"type": …, "values": [...]}` の1件で型と選択肢を持つ。エディタの型検証と選択肢の色分けがサーバーの索引と同じ辞書を使うための配布。**2026-09-14 に `type_inference` と `tag_enums` の2キーを畳んだもの**）・`column_types`（`th[data-type]` に書ける列型の一覧＝`ColumnTypeNames()`。**2026-09-14 追加**——エディタが手書きで持っていて `datetime`・`ref`・`email` の3つぶん古かったため）・`extensions`（**載っている拡張の名簿**＝`ExtensionIDs()`。ID順。既定のビルドは `["comm","comm/contacts","comm/mail","subcon"]`／`-tags nomail` は `["comm","comm/contacts","subcon"]`／`-tags minimal` は `[]`（いずれも 2026-09-16 実測）。**空でも `[]`**。画面は拡張のボタンをこれで出し分ける（`app.js` の `hasExtension`。**取得に失敗して `null` のときは出し分けず全部出す**）——ビルドタグで外してもボタンが残り404になっていたため。**2026-09-15 追加**）を返す。エディタのシリアライザが従う正本（[本文サニタイズ設計.md](本文サニタイズ設計.md) §7） |
| POST | `/api/upload-pdf` | 要認証（対象ページの write） | PDFのアップロード（フォーム欄 `pdf_file`。ページフォルダの **`files/` サブフォルダ**へ、サーバー生成の名前 `<生成ID>.pdf` で保存——正本と同居させない。2026-08-31）。**編集ロックが要る**——添付は同名を無条件で上書きし、リビジョンもゴミ箱も無い（＝復元できない）ため本文編集と同じロックで直列化する。**受け入れは `.pdf` のみ・先頭 `%PDF-` 必須・パス要素と本文/サイドカー同名は拒否**。上限は設定 `max_upload_mib`（`config/settings.json`・既定32MiB・`MaxUploadBytes`）。応答は `{success, file_name, src, id, href}`（`id` は生成ID＝リンクブロックの `data-id`、`href` はきれいなURL）（[アーキテクチャとDBスキーマ.md](アーキテクチャとDBスキーマ.md) §5.1） |
| POST | `/api/upload-image` | 要認証（対象ページの write） | **画像のアップロード**（2026-08-26。フォーム欄 `image_file`。`png`/`jpeg`/`webp`/`gif`/`svg`）。保存先・ロック・サイズ上限・名前の規則は `/api/upload-pdf` と共通（名前の検査は `safeAttachmentName` の1箇所）。加えて**中身のマジックナンバーで種別を判定**し、拡張子と食い違うものは拒否。**EXIF等のメタデータは保存前に除去**する（JPEGは向きを画素へ反映してから再符号化）。SVG は整形式・ルートが `svg`・`<script>`/`<foreignObject>`/`on*=`/`javascript:` 無しを確認。HEIC/AVIF/TIFF/BMP は**形式ごとの理由**を返して拒否。応答は `{success, file_name, kind, src, id}` で、`src` はそのまま `<img src>` に入れる絶対パス（きれいなURL）（要件定義書 §2.6） |
| POST | `/api/upload-file` | 要認証（対象ページの write） | **汎用の添付**（2026-08-31。フォーム欄 `file`）。受ける拡張子は設定 `attachment_extensions`（既定は `settings.go` の14種——`.dxf`・`.xlsx`・`.zip`・`.eml`・`.mp4` 等。`.json` は書けない）。**中身は検査しない**——安全の本体は配信側（未知の種別は `attachment`＋`nosniff` で返す）。画像と `.pdf` はこの口では受けない（専用の口を迂回させないため・400）。保存先・ロック・上限・名前の規則は `/api/upload-pdf` と共通。応答は `{success, file_name, id, href}`。**対象が通信箱（トップ直下の「通信箱」ページ）なら取り込み係へ回す**——**コアは通信箱を名指ししません**（2026-09-15）。write を確かめたあと、**編集ロックを確かめる前**に `interceptUpload`（[upload_intercept.go](../internal/cms/upload_intercept.go)）が登録済みの受け口へ順に尋ね、`ext/comm` の受け口（`serveIntake`・[ext/comm/intake.go](../ext/comm/intake.go)）が「自分の担当だ」と判断したら引き受ける。拡張子の担当（現在は `.eml` の1人）が居れば、添付ではなく通信箱の子ページ（通信記録）を作って `{success, intake:true, page_id, title}` を返す。このときは編集ロックを見ない（通信箱の本文は変わらない）。**`-tags minimal` では受け口が1つも積まれず、通信箱へ落としても普通の添付になる**。**重複検知**（2026-09-02）: 取り込み係が鍵を出せれば（`.eml` は `メッセージID`＝Message-ID）、同じ鍵を持つページを索引から逆引きし（`ExistingIntakePage`→`PagesByTag`）、あれば**作らずに** `{success, intake:true, duplicate:true, title, page_id?}` を返す（`page_id` は読める相手にだけ。監査記録 `intake.duplicate`）。担当が居ない拡張子は通常の添付になる。取り込みでは Gemini を呼ばない（下の `/api/analyze-attachment`） |
| POST | `/api/parse-pdf` | 要認証（対象ページの write） | **PDFから明細をAI抽出**し、開いているブロックへ差し込む（Gemini。呼び出しの型は `gemini.go` の `GeminiGenerate` に1本化・プロンプトは呼ぶ側の持ち物）。**下請け業務 [ext/subcon/parse_pdf.go](../ext/subcon/parse_pdf.go)——`-tags minimal` で外れる**（2026-09-16 にコアから移設。ユーザー:「PDF解析は業務に密着せざるを得ないので、ext/subcon ではないでしょうか？」——プロンプトが「発注書または見積書」と業務を語る口が、コアに残っていた最後の1本だった。移設前は**口は生きているのに押す手段が無い**状態で、ボタンは `File: true` を宣言する形式＝下請けの語彙が在るときだけ出る）。⚠ **`/api/analyze-attachment` とは別の仕事**——あちらは受注ページを1枚作り、こちらは**人が開いているブロックの中へ明細を入れるだけ**。**ロックは要らない**——永続状態を変えず、結果はDOMへ足すだけ（保存する `/api/save` 側が検証する）。ファイル名は置く側と**同じ関門**（コアの `cms.SafeAttachmentName` に `.pdf` だけを渡す）を通す——ここが `filepath.Base` だけだったころ、本文 `<id>.html` と権限サイドカー `<id>.meta.json` を「PDFとして」外部へ送れた。ファイルは `files/`→旧置き場（直下）の順で探す |
| POST | `/api/analyze-attachment` | 要認証（対象ページの write） | **PDFの判定→受注ページ／部品ページ生成**（2026-09-01。下請け業務 [ext/subcon/analyze_pdf.go](../ext/subcon/analyze_pdf.go)——**`-tags minimal` で外れる**（2026-09-03。ルートもプラグインの `Routes()` 経由なので一緒に消える））。`{page_id, file, entry?}` で添付の `.pdf`（または `.zip` の中の `entry`＝目録の表示名）を Gemini に **3分類**させる（`doc_type` が `order` / `drawing` / `other`）。**枝は3つ**——①**発注書**（`is_client_order:true`）なら受注ページを対象ページの子として生成（`cms.CreateChildPage`。ヘッダ dl＋明細 table＋参照タグ `受信元: <ページID>-<添付ID>`、ZIP経由なら `元ファイル` も）。応答 `{success, is_client_order:true, page_id, title}`・監査記録 `analyze-pdf` ②**図面**なら部品ページを生成（`<h2>図面</h2>` のヘッダ dl＋参照タグ＋**`<section data-type="file-view" data-ref="<ページID>-<添付ID>">`**＝図面をその場で開くマーカー（**配線は属性1つ**・2026-09-15。それまでは中に `受信元` のタグを書いていたが、同じ値がすぐ上の図面ブロックにも在り、2つの意味で並んでいた）。同じページのDXF添付と図面番号で突き合わせる `MatchDXFAttachments`）。応答 `{success, is_client_order:false, doc_type:"drawing", page_id, title, matched_dxf}`・監査記録 `analyze-drawing` ③**それ以外**は `{success:true, is_client_order:false}` で何も作らない（人が押した問いに「発注書ではない」と答えるのも正常な結果）。**起動は人の操作だけ**（📎の「🤖 解析」ボタン・人間ゲート型）。**ロックは要らない**（対象ページの本文は変えない）。ZIP内の取り出しは `max_upload_mib` を上限に打ち切る（ZIP爆弾対策）。**取り消しはページ削除**（ゴミ箱） |
| GET | `/api/filing-proposal` | 要認証（対象ページの read） | **整理の候補**（2026-09-03。下請け業務 [ext/subcon/filing.go](../ext/subcon/filing.go)——`-tags minimal` で外れる）。`?page_id=X` で通信記録ページ X の子のうち、**図面ブロックを持つもの**（部品ページ）と**発注書ブロックを持つもの**（受注ページ・2026-09-06）を集めて返す。応答は `{success, rows, orders, stages}`——`rows` は部品（`page_id, title, drawing_no, drawing_name, customer, machine_name, stage`。値は索引から読んだ**推奨値**で、人が直す）、`orders` は受注（`page_id, title, order_no, client_name, ordered_at, destination`。**直す欄は無い**——行き先は発注日で決まる）、`stages` は段の選択肢（設定 `extensions.subcon.machine_stages`）。読めないページは黙って落ちる（見せ分けC案） |
| POST | `/api/file-drawings` | 要認証（各ページの write） | **整理の実行**（2026-09-03。同上）。`{rows:[{page_id, customer, stage, machine_name, drawing_name, confirm_revision}], orders:[page_id,…]}`。**部品**は `取引先／社名／段／装置名称` の下へ移し、題を図面名称に揃える（`cms.SetPageParent`＋`SetPageH1`。途中のページは無ければ作る）。空欄の行と、段が `machine_stages` に無い行は動かさない。移した先に同名の部品ページが在れば**改定図面**として先頭へ合流（`confirm_revision` が要る場合は `needs_confirm` を返す）。**受注**は `受注／年／月` の下へ移す（年月は**発注日**・2026-09-06）——受注ページでないIDは動かさない。応答は `{success, results:[{page_id, outcome, message, target_id?}]}`（`outcome` は `moved` / `revision` / `skipped` / `needs_confirm`）。**権限も「編集中か」も行ごとに見ます**——駄目な行は `skipped` に載るだけで、残りは片付く（**複数のページへ書く**ので、関門はハンドラではなく行ごと・2026-09-14）。監査記録は `file-drawing.move`・`file-drawing.revision`・`file-order.move` |
| GET | `/api/zip-list` | 任意認証（read） | **ZIP添付の目録**（2026-09-01。`?page_id=&file=`。`OptionalAuth` 配下）。標準の `archive/zip` でセントラルディレクトリだけ読み、**展開はしない**（ZIP爆弾対策）。UTF-8フラグの無いエントリ名は Shift_JIS として復号。最大500件（超過は `truncated:true`）。認可は添付配信と同じ閲覧側の関門（`RequirePageReadOrPublic`——読めるページの添付は目録も読める）。応答は `{success, entries:[{name,size}], total, truncated}` |
| GET | `/api/mail/status` | 要認証 | メールの設定とサインイン状態（`{configured, address}`）。**トークンは返しません**——出すのは「誰としてサインインしているか」だけ。**`/api/mail/*` の4本は拡張 [ext/comm/mail](../ext/comm/mail/) の `Routes()` 提供**で、`-tags nomail` でも `-tags minimal` でも外れます（送信の口は `comm.RegisterMailer` で `ext/comm` へ差し込まれ、使う側は `comm.CurrentMailer()` に尋ねる） |
| POST | `/api/mail/signin` | 要認証 | **デバイスコードのサインインを始める**（`{user_code, verification_uri, expires_in}`）。完了待ちは背後で回り、結果は `/api/mail/status` で確かめる（ここで待つと画面が固まる） |
| POST | `/api/mail/import` | 要認証 | **IMAP で未取り込みのメールを通信箱へ**（`{folder?, max?, since?}`）。接続は取り込み1回につき1本。**見る上限（5000）と取り込む上限（50）は別**——混ぜると押しても古いほうへ進まない。`Message-ID` の見出しだけ先に読み、**本体を落とす前に**重複を弾く。**古い順**に取り込む（スレッドの親が先に着く）。`EXAMINE`＋`BODY.PEEK[]` で**既読にしない**。応答は `{success, summary:{listed, imported, duplicate, failed, titles}}` |
| POST | `/api/mail/send` | 要認証 | **返信・新規送信**（SMTP＋OAuth2）。`{source_page_id?, to, cc, subject, body, attachments?}`。**添付は w-cms の中にあるものを指す**（`{page_id, file, name}`。手元のディスクから選び直さない）——**読めるページのものだけ**（`page.CanView`）・名前は `SafeAttachmentName` の関門を共用・**合計25MBで断る**（途中で切れて「送ったつもりで届いていない」になるより、送る前に断る）。返信元があればその `メッセージID` を `In-Reply-To` と `References` に載せる（相手のメールソフトで元のスレッドに並ぶ条件）。**送ってから記録する**——記録に失敗しても `{success, sent:true, record_error}` を返す（送ったものは取り消せないので、出た事実を隠さない）。控えは通信箱へ `向き：送信`・`対応：不要` で立つ |
| GET | `/api/replies` | 要認証 | **この記録への返信を逆引きする**（2026-09-03。`?page_id=X`。通信 [ext/comm/handler_replies.go](../ext/comm/handler_replies.go)——**`-tags minimal` で外れる**）。`PagesByTag(comm.ReplySourceTag, X)`＝`PagesByTag("返信元", X)`——送信記録に書かれた**参照タグ**を引くので、**w-cms 自身が送った返信**だけが出る。応答は `{success, replies:[{page_id, title, sent_at, to}]}`（`to` は `宛先` タグの**畳んだ値＝素のアドレス**。畳めていなければ生の値）。読めないページは黙って落ちる（見せ分けC案）。対象ページを読めなければ**404**（読めないと存在しないを区別させない） |
| GET | `/api/thread` | 要認証 | **やりとりの前後へ移る**（2026-09-14。`?page_id=X`）。鎖は**メールヘッダの `In-Reply-To`** で、索引の逆引き2回だけ（新しいテーブルも仕掛けも無い）——前（親）＝`メッセージID` が X の `返信元メッセージID` と一致するページ、次（子）＝`返信元メッセージID` が X の `メッセージID` と一致するページ。応答は `{success, prev, next}`——`prev` は1件か `null`、`next` は配列（空でも返る）。各件は `{page_id, title, when, direction}`（`when` は `受信日時`・`送信日時`・`発信日時` のうち**そのページに先に現れたもの**1つ——向きに応じて片方しか書かれないので、どれでも構わない。`direction` は `向き` タグ）。**前は高々1件**（`In-Reply-To` は親を1つしか指さない）。**`/api/replies` とは別の鎖**——あちらは w-cms を通った返信だけだが、こちらはヘッダの鎖なので**受信どうしの返り**（お客様が自分の前のメールに返信した等）も繋がる。送信の記録にも `返信元メッセージID` は書かれる（`ext/comm/mail/reply.go`）ので両方が同じ鎖に乗る。認可と404の扱いは `/api/replies` と同じ。正本は [ext/comm/handler_thread.go](../ext/comm/handler_thread.go)（**2026-09-15 に `internal/cms` から移設**。ルートも `ext/comm` の `Routes()` から生えるので `-tags minimal` で外れる） |
| GET | `/api/qr` | **認証不要**（`OptionalAuth`。ただし対象ページを読めなければ404） | **そのページのURLのQRを返す**（2026-09-10。`?page_id=X`・**SVG**）。エンコーダは自前で[qr.go](../internal/cms/qr.go)、**外部依存ゼロ**。URLは要求のホストから組むので、社内（ホスト名）と社外（公開名）で**中身が変わります**——だから `Cache-Control: no-store`。取り違えると「社外の人に社内URLのQRを見せる」が起きます。SVGは中でスクリプトが動けるので、添付と同じく**この応答だけ何も動かせなく**します（`default-src 'none'; sandbox` ＋ `nosniff`）。⚠ **QRは他所のライブラリと同じ絵になりません**（詰め草とマスク選択が実装ごとに違う。3つとも違う絵を出してどれも読める）。**絵の一致を正しさの尺度にしないこと**——見るのは実際に読めるかで、`python tools/qr_verify.py` が OpenCV で復号して確かめます |
| GET | `/api/analyzed` | 要認証（対象ページの read） | **解析済みの印を逆引きする**（2026-09-06。`?page_id=X`・拡張 `ext/subcon`）。応答は `{success, analyzed:{<添付ID>:{page_id, title, kind}}}`。**印は保存していません**——解析が書いた `受信元` タグの逆引きです。だから**間違った解析をゴミ箱へ入れれば印も消え**、押し直せます（状態を別に持たない）。認可と404の扱いは `/api/replies` と同じ |
| POST | `/api/reorder` | 要認証（**親ページ**の write） | **子ページの並べ替え**（2026-09-03。`?parent=P`・本文 `{"order":["000123","000456",…]}`）。送るのは**その親の子の並び全部**です——「どこへ落としたか」を送ると、サーバーが前後関係を推測することになるため。並び順キーはサイドカーの `sort_key` が正本で、値は**ゼロ詰め10桁**（`ReorderKey`）。桁を固定するのは、文字列として比べても数の順になるようにするためです（固定しないと 9 と 10 が逆になる）。応答は `{success, changed}`。⚠ 子ページの並びは「並び順キー → 題 → ID」（`sortChildren`）なので、**キーが空でも題で並びます**——年・月フォルダは何もしなくても正しい |
| GET | `/api/required-materials` | 要認証（対象ページの read） | **プラグイン提供API**。部材手配計算（[ext/subcon/materials.go](../ext/subcon/materials.go) の `RouteProvider`）。集計本体は `RequiredMaterials(user, pageID)` で、計算ビューのサーバー事前描画と共用する（応答が呼び出しごとに変わらないよう**部材名順**にソート）。集計対象ページの read だけでは足りず、**部材の定義元ページを読めない相手にはその定義を混ぜない**——品番は本文へ自由に書けるので、自分のページに1行置くだけで読めない部品定義ページの部材名・仕入先・原価を引けていた（2026-08-21 修正） |
| POST | `/api/contacts/register` | 要認証（新規は `取引先` ページ——無ければトップ——の write／既存へ足すときはその相手ページの write） | **メールから拾った相手をページにする**（アドレス帳・2026-09-05。**2026-09-15 から拡張 [ext/comm/contacts](../ext/comm/contacts/) の `Routes()` 提供**——`-tags minimal` で外れる）。`{name, relation, addresses:[…], page_id?, person_name?}`。`page_id` が無ければ**新しい相手ページ**を `取引先` の下に作る（題は `NormalizeNameForIngest` で畳む——部品階層の顧客名と**同じページ**なので、畳まないと2枚に割れる。本文は `取引：<relation>`＋`メールアドレス`×n の可変タグ。`relation` は `顧客`／`仕入先`／`自社` の表引き）。`page_id` があれば**既存の相手へ足す**（2つ目のドメインのため。会社ページを2枚にしない）——`取引先` の下のページだけ。さらに `person_name` があれば `取引先／社名／担当者／氏名` の**担当者ページ**へ入れる（無ければ作る）。応答は新規 `{success, page_id, title}`／追加 `{success, page_id, title, added, merged:true}`。**`editlock.RefuseWhileEditing` を通す**（本文を読んで・変えて・書くので、誰かが開いていれば409）。監査記録 `contact.register`／`contact.add-addresses` |
| POST | `/api/contacts/unfile` | 要認証（対象ページの write） | **分類を取り消して未分類へ戻す**（2026-09-13 ユーザー:「間違えてアドレスを分類した場合、どうやって未分類に戻しますか？」。同じく `ext/comm/contacts` 提供）。`{page_id, address}`。やることは**`メールアドレス` タグを1つ外すだけ**——未登録の一覧は「どこにもそのタグが無いアドレス」という索引からの派生なので、外せば自動的に戻る（状態を別に持たない）。`取引先` の下のページだけ（よそのページのタグを消せる道を増やさない）。**ページは消さない**——空になったことだけ `{success, page_id, address, remaining, children, empty}` で伝え、消すかは人が決める。そのアドレスが無ければ **404**。`RefuseWhileEditing` を通す。監査記録 `contact.unfile` |
| POST | `/api/intake/memo` | 要認証（**通信箱ページの write**） | **手で記録を作る**（2026-09-05。通信 [ext/comm/handler_memo.go](../ext/comm/handler_memo.go)——**`-tags minimal` で外れる**）。`{channel, direction?, title?, phone?, counterpart?}`。通信箱／年／月の下に記録ページを1枚作る。チャネルは表引きで閉じる（メール／FAX／電話／メモ——自由記入だと `電話` と `TEL` が混ざる。定数 `comm.ChannelMail`／`ChannelFax`／`ChannelPhone`／`ChannelMemo`）。**向きは電話・FAX・メールが持ち、メモは持たない**（`comm.DirectionTag` と `DirectionIn`／`DirectionOut`）。日時は向きに応じて `受信日時`（`comm.ReceivedAtTag`）か **`発信日時`（`comm.SentOutAtTag`。人が発信した印で、機械が投函した `送信日時`＝`SentAtTag` とは別の欄）**の**片方だけ**。`phone`・`counterpart` は発信（`tel:` の発信ボタン）から来る。監査記録は `intake.memo` |
| POST | `/api/intake/handled` | 要認証（**各ページの write**・行ごと） | 通信記録に**`対応` のタグを付ける**（2026-09-05。通信 [ext/comm/handler_handled.go](../ext/comm/handler_handled.go)——**`-tags minimal` で外れる**。タグ名は `comm.HandledTag`）。`{page_ids:[…], value}`。値は `済`（次の作業へ割り振った——**責任はそちらへ移る**）か `不要`（何も生まれない）。省略時は `済`。**これが「済んだ」を決める唯一の印**——向きでも子ページの有無でも決めない。未処理の一覧から1クリックで片付けるための口——付けるのは**通信の語彙だけ**で、「見積依頼にする」「受注にする」は業種の語彙なので `ext/subcon` の仕事。**編集トークンは要らない**（一覧画面のボタンなので持てない）が、**編集中のページは飛ばす**——`MarkHandled` は本文を読んで・変えて・書くので、エディタが開いているとオートセーブと上書きし合う（2026-09-14。判定は `editlock.Locks.EditorOpen`）。**1件の権限不足で全体を止めない**（応答は `{success, handled, failed}`。飛ばした分は `failed` に数える）。監査記録は `intake.handled` |

プラグインは `RouteProvider` を実装するとルートを追加できる。`main.go` は
`cms.PluginRoutes()` をループして登録するだけで、コア側の変更は要らない
（[【ガイド】プラグイン開発.md](【ガイド】プラグイン開発.md) §3）。**上の表のうち
`/api/mail/*`・`/api/contacts/*`・`/api/replies`・`/api/thread`・`/api/intake/*` と
`/api/analyze-attachment`・`/api/filing-proposal`・`/api/file-drawings`・`/api/analyzed`・
`/api/required-materials` はすべてこの経路**で、`main.go` のルート表には現れません（冒頭の内訳表）。

## 8. 静的配信

| パス | 認可 | 概要 |
|---|---|---|
| `/assets/...` | 認証不要 | 殻の markup・CSS・JS（`index.html`・`app.css`/`app.js`・`boot.js`・`admin.*`・`login.css`・**`public.html`/`public.css`**）。**ディレクトリ一覧は無効**（`noDirListing`）。`Cache-Control: no-cache`＝毎回再検証（変わっていなければ304）。CSP strict 化（`'unsafe-inline'` 無し）により、スクリプト・スタイルはすべてここに置く |

`/data/...` は静的配信ではなく認可付きハンドラ（§2）。

## 9. クローラ向け（2026-08-26）

| メソッド | パス | 認可 | 概要 |
|---|---|---|---|
| GET | `/sitemap.xml` | 認証不要 | **実効公開のページだけ**を載せた sitemap（`<loc>` は絶対URL・`<lastmod>` は W3C Datetime）。判定は認可と同じ `page.EffectivePublic` を通す——独自の判定を書くと、認可とずれた瞬間に非公開ページのアドレスを外へ配ることになる。`public, max-age=600` |
| GET | `/robots.txt` | 認証不要 | **サイト全体が非公開なら `Disallow: /`**（トップの実効公開で判定。パスゲートにより「サイトが閉じているか」と同義。索引が使えないときも閉じている扱い＝フェイルクローズ）。公開サイトでは `/api/` と `/login` を閉じ、`Sitemap:` 行で sitemap を案内する。添付（`/data/`）は本文の画像がここから配られるので閉じない |

絶対URLの基底は `WCMS_BASE_URL`（[要件定義書.md](要件定義書.md) §4.3）を最優先し、無ければ
リクエストから組み立てる（`X-Forwarded-Proto`/`-Host` は**前段がプロキシのときだけ**採用——
無条件に信じると外から表記を操作できる。監査記録の接続元と同じ規則で、判定は
`auth.IsFromTrustedProxy` の1関数）。
