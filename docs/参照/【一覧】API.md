# 【一覧】API

w-cms が提供するHTTPエンドポイントの、**実装済みリファレンス**（引くための一覧）です。

エンドポイントは、画面や道具が w-cms に用事を頼むための窓口です。`/api/...` のような道（パス）で呼びます。
この文書では、「口」とも呼びます。

**ルーティングの正本は2種類**あります。本ファイルは、その鏡（写し）です。
ルーティングは、どの道がどの処理につながるか、の対応のことです。

- **コアの口**: [cmd/w-cms/main.go](../../cmd/w-cms/main.go) の `buildHandler()` のルート表が正本です。
- **拡張の口**: 各プラグインの `Routes()` が正本です。
  - `main.go` は、`cms.PluginRoutes()` をループして登録するだけです。だから、**`main.go` を見ても出てきません**。
  - `-tags` で拡張を外すと、ルートごと消えます。
  - 内訳は、次のとおりです（2026-10-14 に、各 `Routes()` の `Pattern:` の行を数えました。拡張の口は計76本です）。

  | 拡張 | `Routes()` の正本 | 口 |
  |---|---|---|
  | `ext/comm`（通信） | [comm_routes.go](../../ext/comm/comm_routes.go) | 8本。`/api/replies`・`/api/thread`・`/api/mails`・`/api/intake/handled`・`/api/intake/memo`・`/api/intake/eml`・`/api/record-makers`・`/api/record-make` |
  | `ext/comm/contacts`（アドレス帳） | [contacts/plugin.go](../../ext/comm/contacts/plugin.go) | 3本。`/api/contacts/register`・`/api/contacts/unfile`・`/api/contacts/addresses` |
  | `ext/comm/mail`（メール） | [mail/plugin.go](../../ext/comm/mail/plugin.go) | 9本。`/api/mail/status`・`/api/mail/signin`・`/api/mail/import`・`/api/mail/fetch-new`・`/api/mail/fetch-new/status`・`/api/mail/send`・`/api/mail/compose`・`/api/mail/draft`・`/api/mail/attach` |
  | `ext/toho`（下請け業務） | [toho/materials.go](../../ext/toho/materials.go) | 57本。解析と整理: `/api/analyze-attachment`・`/api/analyzed`・`/api/compare-drawings`・`/api/filing-proposal`・`/api/filing-target`・`/api/filing-search`・`/api/filing-files-shown`・`/api/filing-attach`・`/api/file-drawings`・`/api/product-folder`・`/api/drawing/supersede`。受注と部材発注: `/api/required-materials`・`/api/order-item`・`/api/material-search`・`/api/order-pdf`・`/api/order-pdf-docs`・`/api/our-order/*`（9本）。見積: `/api/estimate-pdf`・`/api/estimate/*`（10本）。部材見積依頼: `/api/rfq-pdf`・`/api/rfq-pdf-docs`・`/api/rfq/*`（15本）・`/api/rfq-reply/*`（3本）。道具: `/api/table/append-rows` |

  コアの口（`main.go`）は、57本です（`/api/` 配下の要認証が42本、そのほかが15本）。合わせて133本です（2026-10-14: `/api/session` を足し、`/api/admin/reset` を消しました。2026-10-15: WebDAV の `/dav/` を消しました）。

  **起動ログで確かめられます。** `プラグインAPI登録: <パス>` が、1本ずつ出ます。

> **エンドポイントを増減したら、本ファイルも同時に更新すること。**
>
> かつて、この一覧は [アーキテクチャとDBスキーマ.md](../旧文書/アーキテクチャとDBスキーマ.md) の中に埋もれていました。
> その結果、存在しない `/api/new-id` が残り、`/api/children`・`/api/tag-schema`・`/api/lock/force`・
> `/api/admin/*` が載っていない状態になっていました。
> 独立した一覧にしたのは、そのためです。

この文書でよく出る言葉を、先に説明しておきます。

- **ハンドラ**: 口に届いた要求を受けて、処理する関数です。
- **メソッド**: 要求の種類です。GET は「読む」、POST は「変える・送る」に使います。
- **状態行**: 応答に付く、HTTP の状態の番号です（200 は成功、404 は「ありません」など）。
- **匿名**: ログインしていない人のことです。

---

## 1. 認可の区分

認可は、「誰がその口を使ってよいか」の決まりです。

| 区分 | 意味 | 適用 |
|---|---|---|
| **要認証** | 未ログインは API=401 / 画面=`/login` へ302 | `/api/` 配下の既定（`RequireAuth`） |
| **任意認証** | 匿名でも到達でき、各ハンドラが**実効公開**で個別判定する | `OptionalAuth` を明示したルート |
| **認証不要** | 誰でも取得できる（秘密でない情報） | ログイン画面・語彙など |

ページ単位の認可は、Unix風の read（読む）/write（書く）です（[認証認可設計.md](../旧文書/認証認可設計.md) §3）。

**実効公開**とは、自分と全先祖（親をたどった先のすべてのページ）が `public` のときに、匿名へ開くことです（同 §10.2）。

さらに、すべての口にまたがって（横断で）、2つのミドルウェアが最外周（いちばん外側）に掛かります。
ミドルウェアは、どの要求も必ず通る、共通の処理です。

- `CSRFProtect`: Origin/Referer とホストの一致を検証します。
- `CSPProtect`: strict CSP を付与します。

中身と経緯は、[認証認可設計.md](../旧文書/認証認可設計.md) §0.1・§2.3・§6 にあります。

ここは、**黙って壊れる層**です。
次のような退行（後戻り）が起きても、既存のテストは green（合格）のままで、実害だけが出ます。

- ハンドラを `protected` から `root` へ移す
- `OptionalAuth` を付け忘れる
- ミドルウェアの入れ子を外す

そのため、`buildHandler()` を `main` から切り出しました。
ルートごとの保護レベルと、上記2つの配線を、[route_guard_test.go](../../cmd/w-cms/route_guard_test.go) が固定しています。
`csp_test.go` は、ポリシー文字列を見るだけで、配線までは見ていません。

### 1.1. メソッドの関門（状態を変える口は POST 固定）

`CSRFProtect` は、GET/HEAD/OPTIONS を検証しません。
だから、**状態を変える口は、ハンドラ自身がメソッドを絞ります**。

GET で通ってしまうと、本文へ `<img src="/api/…">` を1つ保存するだけで、開いた全ログインユーザーに、同じ操作をさせられます。
同一オリジン（同じサイトの中）なので、SameSite も CSP も止めません。
実例は、`/api/logout` と `/api/new-page` です。理屈は、[セキュリティ設計.md](../旧文書/セキュリティ設計.md) §1 にあります。

これを固定しているのは、[method_guard_test.go](../../internal/cms/method_guard_test.go) です（2026-09-14 新設）。

- **状態を変えるコアのハンドラ12本を直接呼んで、405 を期待**します。
  連絡先の口（`/api/contacts/…`）と通信の口（`/api/intake/…`）は、2026-09-15 に各拡張の試験へ移りました。
- 加えて、JSON で答える読み取り口が、POST を**JSONで**断ることも見ます。

⚠ **`route_guard_test.go` の `TestStateChangingRoutesRejectGET` では足りません。**

- この試験は `buildHandler()` へ流すので、`RequireAuth` が先に 401 を返します。
  **ハンドラ自身のメソッド確認が、一度も走りません。**
- 「401 か 405 なら合格」だったので、確認を丸ごと外しても緑（合格）でした。
- この穴で、`/api/page-chown` のメソッド関門の欠落を、2026-09-14 まで見逃しています。
  GETで状態が変わらなかったのは、`DecodeJSONBody` が空ボディで落ちる、という**偶然**でした。

### 1.2. 失敗応答の形

JSONで答えるAPIの失敗は、`JSONFail`（`handler_save.go`）が `{"success": false, "message": …}` を書きます。
**必ず、意味のある状態行が付きます。**

- 2026-09-14 まで、「HTTP 200 のまま `success:false` を返す」口が**13箇所**ありました。
  `res.ok` を見る受け手からは、失敗が成功に見えていました。
- いまは、`status == 0` の分岐そのものが畳まれています（0 が来たら、500 へ倒します）。

| 状態行 | 意味 | 例 |
|---|---|---|
| **400** | 入力が不正 | ページIDが不正・宛先が空・PDFの読み込みに失敗 |
| **401 / 403** | 認証・認可（`page.Require*` が返す） | 未ログイン／権限なし |
| **404** | 読めない・存在しない（**区別させない**） | JSON読み取り口の共通の関門 `GateJSONPageRead` |
| **405** | メソッドが違う（上の §1.1） | 状態を変える口への GET |
| **409** | システムの状態が整っていない | 通信箱のページが無い・メール未サインイン・編集ロック・**編集中のページへの機械の書き込み**（§3） |
| **413** | JSONボディが 8MiB 超 | `DecodeJSONBody` |
| **423** | ロックが他者保持中 | `POST /api/lock` |
| **501** | プラグインが入っていない | メール送信（**`comm.ErrNoMailer`**。2026-09-15 に `internal/cms` から `ext/comm` へ移った。`comm.RegisterMailer` を通った実装が無い。いまは防御的な枝で、`ext/comm/mail` が載っていれば起きない） |
| **502** | 上流が失敗した | Gemini・SMTP |
| **503** | 機能が構成されていない | `GEMINI_API_KEY` 未設定・メールの設定が無い |

受け取る側（`assets/app.js`）は、`readResult(res)` で**本体を1回だけ**読みます。
JSONにならなければ、本文をそのまま理由にします。

`failMessage(res, d)` が、`message` → `statusText` → 状態行の順で、1行を作ります。**数字は最後の手段**です。

### 1.3. ページIDは入口で6桁へ畳む

対象ページの `id` / `page_id` を受けるハンドラは、最初に `page.NormalizeID` を通します。
ここで、ページIDを6桁の形へそろえます（「畳む」と呼びます）。
不正・空は、400「ページIDが不正です」になります。

2026-09-14 に、**畳んでいなかった5つ**を揃えました。
`/api/page-meta`・`/api/validate-parent`・`/api/load`・`/api/lock-events`・`/api/lock/force` です。

- この5つは、`strconv.Atoi` した数値しか使っていないので、実害はありませんでした。
- しかし、`page.GetPageDir(id)` / `page.AttachmentDir(id)` は、**文字列を取ります**。
  あとで1行足した人が `"1"` を渡すと、`data/1/1.html` を探しに行きます。
- **理由の書いていない例外は、次の人には区別が付きません。**

実測の結果です。

- `id=10272` → `010272` として引けます。
- `id=-1`・`id=abc` → 400 になります（前は、`Atoi` が `-1` を通して、404になっていました）。

⚠ **まだ畳んでいない口が、3種類あります**（2026-09-14 時点・いずれも数値としてしか使わない）。

- `/api/unlock` の `id`（`Locks.Release` に渡すだけ）
- `/api/required-materials` の `page_id`
- **親を指す `parent`**（`/api/new-page`・`/api/set-parent`・`/api/validate-parent`）

## 2. ページ本文・属性

表の読み方です。

- 「認可」の欄の（read）（write）は、そのページを読む権限・書く権限が要ることを表します。
- 「編集ロック」の欄は、その口が編集ロック（§3）を求めるかどうかです。
  「要」は、ハンドラが `editlock.RequireEditLock` か `Locks.Validate` を通すことを表します。
  2026-10-12 から、画面はロックを握りません。だから、「要」の口も、道具がロックを握っているとき以外は通ります。

| メソッド | パス | 認可 | 編集ロック | 概要 |
|---|---|---|---|---|
| GET | `/{id}` | 任意認証 | — | **ページ本体**。本文とタイトルを埋め込んだ完成HTMLを返す（サーバー合成）。**殻は相手で分かれる**（2026-08-26）。認証済みは編集用 `assets/index.html`（`RenderPageShell`・`Cache-Control: no-store`）。匿名は**公開専用** `assets/public.html`（`RenderPublicShell`・スクリプト無し・`description`/OGP/canonical つき・`public, max-age=600` ＋ `Vary: Cookie` ＋ `ETag`）。本文はサニタイズ後に**計算ビューの中身が埋められる**（`RenderComputedViews`。下記の注記）。権限無し=403（体裁つきのHTML）／匿名×非公開=404（トップだけ `/login` へ302）／不存在=404 |
| GET | `/api/load` | 任意認証（read） | — | ページ本文（`text/plain`）。初期表示では使わない。2026-10-12 から、画面が呼ぶのは**閲覧モードの読み直し（`view=1`）だけ**（書ける形の本文は `/api/sync` の `full` で読む）。素の形は、道具と試験のために残る。`id` は入口で6桁へ畳み、**空も不正も400「ページIDが不正です」**（2026-09-14。前は空だけ `Missing id` で分けていたが、呼ぶ側にできることは同じ）。**描画時と同じくサニタイズを通し**、計算ビューの中身を埋めて返す。**ページ内アンカーの合成（`RenderAnchors`）と参照リンクの合成（`RenderReferenceLinks`）は通さない**。合成した id や `<a>` がエディタのDOMへ入ると、本文として保存されるため（下記の注記）。**`view=1` を付けたときだけ**両方を通す（2026-10-07）。閲覧の画面が読み直して、開いたときの姿と比べて変わったブロックだけを差し替えるため（ページの更新の知らせ・`/api/page-events`）。書ける形の本文を読むときは付けない（合成したものが本文として保存されるため） |
| POST | `/api/save` | 要認証（write） | 要 | 本文全体を保存する。2026-10-12 から**画面は使わない**。道具〔ワンノートの製造・テンプレートの取り込み・E2E の `makePage`〕のために残す。画面の保存は `/api/sync`。返すのは、サニタイズ結果と `sanitized`、レジストリ未定義の `data-type` の告知 `unknown_types`、見出しの改名で計算に読まれなくなった項目の告知 `unresolved_fields`、殻の接頭辞を剥がした id の告知 `stripped_ids`（下記の注記）。JSONボディは**8MiB上限**（超過は413。JSONを受けるAPIは共通） |
| POST | `/api/save-block` | 要認証（write） | 要 | `data-id` で指定した**1ブロックだけ**保存する。2026-10-12 から画面は使わない（画面の保存は `/api/sync`）。対象が無い／重複なら **409**（クライアントは全文保存へフォールバック）。応答は `/api/save` と同形（`unknown_types`・`unresolved_fields`・`stripped_ids` は当該ブロック分のみ） |
| POST | `/api/sync` | 要認証（write） | — | **画面の保存はこれ1本**（2026-10-12・`internal/cms/handler_sync.go`）。編集モードをやめてブロック単位の保護にした（【考察】編集モードの再考 §6・§7）。要求は `{page_id, full, base:[{id,h}], order:[id], changes:[{id, base_h, base_html, html}], tab}`。中身は、元の並びと指紋（サーバーが前の応答で渡したもの）・いまの並び・変えた（足した）ブロック。ページごとの書き込みの錠の中で当てる。元の指紋のままなら当てる。相手も変えていれば中身で比べ直し、表は違うセルなら黙って合わせる。合わせられなければ**ぶつかった**（本文は相手の版のまま）。消したブロックは相手が変えていれば残す（`kept`）。応答は `{blocks:[{id, h, html?, why?}], conflicts:[{id, kind:"changed"\|"deleted", html?, h?, by}], kept, kept_by, written, updated_at, unknown_types, unresolved_fields, stripped_ids}`。`html` は画面が持っていない・古いブロックだけ（閲覧と同じ鏡を描いたもの・`why` は other・new・kept・merged・sanitized・full）。`full: true` で全部（書ける形に入るとき）。何も変えていなければ書かない（知らせも出ない） |
| GET | `/api/page-meta` | 任意認証（read） | — | ページ属性（親ページID・親ページ名・更新日時など）。匿名には実効公開のときだけ返す |
| GET | `/api/children` | 任意認証（親のread） | — | 子ページ一覧（ID昇順）。認証済みには read 権限のある子、匿名には**実効公開の子だけ**を絞って返す（`visibleChildren`。計算ビューのサーバー事前描画と共用） |
| POST | `/api/delete-page` | 要認証（write） | 要 | ページを**ゴミ箱（`data/trash`）へ移し**、索引から取り除く。物理削除ではない（取り消せることが要件）。**トップページは400**／**子ページを持つと409**（件数つき）／他者ロック中は409。応答は `{success, page_id, trash_path}` |
| POST | `/api/new-page` | 要認証（親のwrite） | — | 子ページを作成し `/{新ID}?edit=true` へ302（`edit` が付いていると、画面は端末の覚えにかかわらず、書ける形で開く）。**POST限定**（GETは405）。GETで通ると、本文へ `<img src="/api/new-page?parent=…">` を1つ保存するだけで、開いた全ログインユーザーにページを作らせられるため（同一オリジンなので SameSite も CSP も止めない。`method_guard_test.go` が固定）。引数は `r.FormValue` なので、POSTボディでもクエリでも受ける。**親の指定は必須**（親なしにできるのはトップページ `000000` のみ）。`template=<ページID>` を付けると、そのテンプレートの本文を**そのまま写す**（2026-09-25 から純粋なコピー。外すのはブロックID `data-id` だけ。それまでは空欄を列型の既定値で埋めていた）。テンプレートに使えるのは「テンプレート」フォルダ配下の**葉**だけで、分類フォルダとルートは400。テンプレートの**read権限が要る**。検証は**採番より前**に行う（ファイルの無いページ行を残さないため） |
| GET | `/api/templates` | 要認証 | — | テンプレート選択メニューの中身。「テンプレート」フォルダ（トップ直下・同名）配下のツリーを `[{id,title,children}]` で返す。**枝は分類・葉がテンプレート**。ルートが無ければ `[]`（従来どおり空のページだけが作られる）。read 権限で絞られる（`visibleChildren`） |
| GET | `/api/validate-parent` | 要認証（write） | — | 親付け替えの事前検証（循環・自己参照・存在チェック）。`/api/set-parent` と同じ `validateParentChange` を共有する。**現在フロントからは呼ばれていない**（`applyParent()` は `/api/set-parent` の応答だけで判定する） |
| POST | `/api/set-parent` | 要認証（write） | 要 | 親ページの付け替え |
| GET | `/api/versions` | 要認証（read） | — | **版の一覧**を新しい順で返す（`[{id, at, by, size, hash}]`。2026-08-26）。版は本文そのものなので本文と同じ read を要求する |
| GET | `/api/version` | 要認証（read） | — | 指定した版の本文（`?id=&v=`）。`text/plain` ＋ `nosniff` ＋**描画時と同じサニタイズ**（`/api/load` と同じ扱い）。版IDは時刻由来の形しか受け付けない＝ページのフォルダの外は指せない |
| POST | `/api/revert` | 要認証（write） | 要 | 選んだ版を**本文として書き戻す**。戻すのは本文HTMLだけで、サイドカー（親・所有者・権限・公開）は触らない＝**リバートが権限昇格の抜け道にならない**。書き戻す前に「いまの内容」を版として残すので、リバート自体も取り消せる。監査記録は `revert` |
| POST | `/api/version-checkpoint` | 要認証（write） | — | 版の区切り（`?id=`・2026-10-12）。編集権を手放したときの区切りの代わり。画面が閲覧モードへ切り替えたとき・ページを離れるとき（そのタブで書いていれば）に呼ぶ。いまの本文が最新の版と違えば新しい版を切り、同じなら最新の版を閉じる（`sealed`。あとの保存が同じ人の10分の窓でも畳まない） |
| GET | `/data/...` | 任意認証（read） | — | 添付ファイル配信の**旧形（`/data/master/<xx>/<id>/…`・互換）**。新しい添付は下のきれいなURLで配る（`DataFileHandler`。ページフォルダ直下と `files/` の両方を受ける）。ページのread権限を要求する保護ハンドラ。`nosniff` 付き。配り方は3通り。**PDFとラスタ画像（png/jpeg/webp/gif）はインライン**。**SVG は不活性化**（`image/svg+xml` ＋ `Content-Disposition: attachment` ＋ この応答限定の `Content-Security-Policy: sandbox; default-src 'none'`）。**それ以外はダウンロード扱い**（`application/octet-stream`＋`attachment`。応答ヘッダの決め方 `setAttachmentHeaders` はきれいなURLと共有）。本文とサイドカーは配らない |
| GET | `/{6桁ページID}/{ファイル名}` | 任意認証（read） | — | **添付のきれいなURL**（2026-08-31「実際に保存される場所を推測されたくない」）。`RootHandler` がページURLの下の2区画パスを `page.ServeCleanAttachment` へ回し、ページフォルダの `files/` サブフォルダから配る（物理配置はURLに出ない）。認可と応答ヘッダは `/data/...` と同じ（`RequirePageReadOrPublic`・`setAttachmentHeaders`）。無ければ404で、**匿名には「読めない」と「存在しない」を区別させない**。各アップロードAPIが返す `href` はこの形 |

> **計算ビューのサーバー事前描画**
>
> 本文を返す入口は、`/{id}`（`RootHandler`）と `/api/load`（`LoadAPIHandler`）の**2つだけ**です。
> どちらも、`RenderComputedViews` を通します。
> 正本は、[アーキテクチャとDBスキーマ.md](../旧文書/アーキテクチャとDBスキーマ.md) 4.4 です。

**保存応答の3つの告知**

いずれも、**拒否ではなく告知**です（エコーバックの流儀）。

- **`unknown_types`**（`[]string`）
  - 本文中の `table`／`dl`／`section` が持つ `data-type` のうち、語彙レジストリに宣言が無いものです。
  - 未知の形式も**そのまま保存し**、②汎用索引にも載ります。
  - 画面での告知だけは、**他と違う扱い**です。
    赤地・太字の目立つトースト（`.toast.alert`）を出し、**時間では消しません**（閉じるまで残ります。2026-08-25）。
    利用者の要望は、「赤色背景などで告知してください」でした。
  - 目立たせる理由は、控えめな通知に紛れると気づけないためです。
    `data-type="cliet-order"` のような綴り違いがあると、その塊は③計算から静かに外れます。
- **`unresolved_fields`**（`[]string`）
  - レジストリが宣言しているのに、文書の見出しから解決できなかった列です（`"顧客の発注書: 発注元"` の形）。
  - 項目の鍵は、**見出しの表示文字**です。
    見出しを改名すると、③計算プラグインが読めなくなり、型付きテーブルへの同期が**黙って**止まります。
    それを、保存時に気づけるようにするための告知です（2026-08-20 追加。`data-field` 撤去の前提）。
  - 報告するのは、**機械キーを持つ列だけ**です。かつ、**改名の徴候があるとき**に限ります。
    改名の徴候とは、解決されなかった宣言列があり、なおかつ、どの宣言列にも当たらない見出しがあることです。
  - 列を消しただけ・独自の列を足しただけでは、黙ります。
- **`stripped_ids`**（`[]string`）
  - 本文の `id` のうち、**殻が独占する接頭辞**（`w-`）が付いていたものです。
  - サニタイズが、接頭辞を剥がして保存を通します。
    書き手が「意図した名前と違うものになった」ことに気づけるよう、告知します（2026-08-20 追加）。
  - 走査するのは、サニタイズ**前**のHTMLです。後では、接頭辞が消えていて分かりません。

## 3. 編集ロック（同時編集の競合対策）

設計は、[【考察】同時編集の競合対策.md](../考察/【考察】同時編集の競合対策.md) にあります。
2026-10-12 からの形は、[【考察】編集モードの再考.md](../考察/【考察】編集モードの再考.md) §6・§7 にあります。

**2026-10-12 から、画面は編集ロック（ページ単位の編集権）を握りません。**

- 画面の保存は、`/api/sync`（§2）の1本です。守りは、ブロック単位です。
- サーバーは、**ページごとの書き込みの錠**（`cms.LockPageBody`）の中で、本文を読んで・変えて・書きます。
  錠を通るのは、`/api/sync`・`/api/save`・`/api/save-block`・`/api/version-checkpoint`・`RewriteBody`（機械の書き込み）・`RevertToVersion`（`/api/revert` の中身）です。
  だから、人の保存と機械の書き込みが重なっても、片方が消えません。
- 編集ロックの口（`/api/lock`・`/api/lock-events`・`/api/unlock`・`/api/lock/force`）は、**道具のために残っています**。
  道具は、ワンノートの製造（`tools/onenote/build`）・テンプレートの取り込み（`tools/templates.js`）・E2E（`e2e/lib.js`）です。
  `/api/lock` で握り、`/api/save` で書き、`/api/unlock` で放します。
- 下の「同じロックで直列化」と「3本目の関門」は、コードに残っています。
  ただし、効くのは、道具がロックを握っているあいだだけです。ふだんは、ロックが無いので素通りです。

| メソッド | パス | 認可 | 概要 |
|---|---|---|---|
| POST | `/api/lock` | 要認証（write） | 2026-10-12 から**画面は握らない**（ブロック単位の保護。画面の保存は `/api/sync`）。道具が `/api/save` の前に一瞬握るためだけに残す。ロック取得（`{ok, token}`）。取れなければ **423 Locked** ＋ `{ok:false, holder, same_user, grace_remaining_sec}`。**本文は返さない**（本文は `GET /api/load` で読む。2026-08-20 変更） |
| GET | `/api/lock-events` | 要認証（write） | ロック状態の **SSE** 購読（保持者・待機者で共用）。2026-10-12 から画面は使わない |
| GET | `/api/page-events` | 要認証（**read**） | ページの更新の知らせの **SSE** 購読（2026-10-07・`internal/cms/page_events.go`）。送るものは3つ。つないだ直後に `{type:"hello", updated_at, presence}`。本文が書き換わるたびに `{type:"updated", by, updated_at}`。キャレットのあるブロックが変わるたびに `{type:"presence", presence:[{tab, user, block}]}`（2026-10-12 昼。購読ごとに覚える・タブを閉じると消える）。知らせるのは本文を書く口（`/api/sync`・`/api/save`・`/api/save-block`・`RewriteBody`／`SetPageH1`・`RevertToVersion`）。`/api/sync` は、書いたタブ（`tab`）も添える。**どこが変わったかは送らない**。画面が `/api/load?view=1` で読み直して比べる。ロックの流れとは別（あちらは write が要る） |
| POST | `/api/presence` | 要認証（read） | 書ける形のタブの、キャレットのある上の段のブロックを知らせる `{id, tab, block}`（2026-10-12 昼・`page_events.go`）。ほかの人の画面で、そのブロックの背景を薄い黄色にするため。そのタブの購読（`/api/page-events`）に覚え、変わったら同じページのタブへ `presence` を配る。名乗れるのは自分の購読だけ。`block` は空（どこにも置いていない）か英数字。GET は 405 |
| GET | `/api/page-watchers` | 要認証（read） | そのページを開いている人の名前 `{users:[…]}`（2026-10-12・`page_events.go`）。ページを消す前の確かめのダイアログに「○○さんが開いています」と出すため。`tab` のタブ（自分）は除く |
| POST | `/api/unlock` | 要認証 | ロック解放。**write は見ない**（解放できるのはトークンが一致する保持者本人だけ）。2026-10-12 から画面は使わない（それまでは、タブを閉じるときに `navigator.sendBeacon` で送っていた） |
| POST | `/api/lock/force` | **admin のみ** | ロックの強制解放（保持者が落ちてスタックしたときの救済） |

ページの状態を変えるAPIは、**同じロックで直列化**されます（計10本）。
直列化とは、同時に来ても、1つずつ順番に通すことです。
ただし、入口は2種類あります。

- **保存2本**（`/api/save`・`/api/save-block`）は、`editlock.Locks.Validate` を直接呼びます。
  トークン（ロックの持ち主を示す合い札）は、**JSONボディの `token`** で受けます。
- **残り8本**は、共通ゲート `editlock.RequireEditLock` を通ります。
  トークンは、**`X-Lock-Token` ヘッダ**（無ければ `token` クエリ）で受けます。
  - 8本は、`/api/set-parent`・`/api/page-perms`・`/api/page-chown`・`/api/delete-page`・`/api/revert`・
    `/api/upload-pdf`・`/api/upload-image`・`/api/upload-file` です。
  - 2026-10-12 から、フロント（画面の側）は**トークンを付けません**（編集権を取らないので、持っていません）。
    `lockedFetch`（`assets/app.js`）は名前だけ残り、いまは「書きかけを先に保存してから送る」口です。
    7本はこれで送ります。`/api/delete-page` だけは、素の `fetch` で送ります。
  - 例外はありません。`/api/upload-file` が通信箱への取り込みに回る枝は、2026-09-30 に無くなりました（§7 の同APIの行）。

検証規約は、どちらも同じです。

- ロック無し＝許可
- 保持者本人で、トークンが一致するなら許可
- それ以外は409

**機械が既存ページの本文を書き換える口には、3本目の関門があります**（`editlock.RefuseWhileEditing`・2026-09-14）。

この文書では、この関門を「**書き込みの関門**」と呼びます。
判定は `editlock.Locks.EditorOpen` です。誰かがそのページの編集ロックを握って、開いているかを見ます。

2026-09-14 に入れたときは、次の4つが通りました。いずれも、**一覧画面のボタン**です。

- 連絡先の登録（`/api/contacts/register`）
- 未分類へ戻す（`/api/contacts/unfile`）
- 「対応」タグ（`/api/intake/handled`）
- 整理の実行（`/api/file-drawings`）

その後、本文を書き換える口が増えました。いまは、次の口も通ります。

- 東邦の口の大半です（§7 で「書き込みの関門」と書いた行）。
  入口は `requireWritableIdle`・`gateWritablePage`（[ext/toho/handler_gate.go](../../ext/toho/handler_gate.go)）です。
  対象ページの write・テンプレートの外・書き込みの関門を、この順で見ます。
- メールの下書き（`/api/mail/draft`・`/api/mail/attach`）と、タグの値の置き換え（`/api/admin/tag-rename`）・表の列の名前の置き換え（`/api/admin/column-rename`）です。

入れた当時の理由は、次のとおりです。

一覧画面のボタンは、エディタを開いていないので、編集トークンを持てません。
`RequireEditLock` を通すと、必ず409になります。

かといって、素通しにもできませんでした。
`RewriteBody` は**読んで・変えて・書く**ので、オートセーブ（自動保存）と上書きし合ったためです。

2026-10-12 からは、画面がロックを握らないので、人が画面で開いているだけでは断りません。
人の書きかけとぶつかったときは、`/api/sync` が、画面に両方の版を並べます。
関門が断るのは、道具がロックを握っているあいだだけです。

断り方は、次のとおりです。

- **誰かがロックを握って開いていれば断ります。握っているのが自分でも断ります。**
  握っている側は、書き換え前の本文を持っています。そのまま全文で保存すれば、機械の変更が消えるためです。
- **保持者が居なくなったロックは無視します**（判定は、明け渡しと同じ `holderPresent` です）。
- **断り方は、口の流儀に合わせてあります。**
  - 1ページだけ触る口（連絡先の2本）は、**409** を返します。
  - まとめ押し（`/api/intake/handled`）は、**1件ずつ `failed` に数えて飛ばす**形です。
  - 整理は、**行ごとに `outcome: "skipped"`** を返します。
    複数のページへ書くので、1枚が断られても、残りは片付けられるほうがよいためです。

決定の経緯は、[【考察】同時編集の競合対策.md](../考察/【考察】同時編集の競合対策.md) §9（2026-09-14）にあります。

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
| POST | `/api/logout` | 要認証 | ログアウト（セッション行を削除）。**POST限定**（GETは405）。`/api/new-page` と同じ保存型CSRFで、本文の `<img src="/api/logout">` 1つで開いた全員を無音で追い出せたため |
| GET | `/api/me` | 任意認証 | 認証状態。未認証は `{authenticated:false}` |
| GET / POST | `/api/session` | 要認証 | セッションが失効するまでの残り（2026-10-14・`{expires_in, idle_ttl, warn_before}`——秒）。**GET は寿命を延ばさない**（画面 `assets/session.js` が失効の5分前の告知のために見る）。**POST は告知の「続ける」**で、操作として寿命を延ばす（CSRF の検査あり）。失効していれば 401 |

## 6. 管理（admin限定）

管理コンソール（管理者用の画面）は、[assets/admin.html](../../assets/admin.html) です。

| メソッド | パス | 概要 |
|---|---|---|
| GET/POST | `/api/admin/users` | ユーザーの一覧・作成 |
| POST | `/api/admin/users/password` | パスワード再設定 |
| POST | `/api/admin/users/disable` | 有効・無効の切り替え |
| GET/POST | `/api/admin/groups` | グループの一覧・作成 |
| POST | `/api/admin/groups/members` | グループ所属の変更（`action` に `add`／`remove`。既定は `add`）。参照用のGETは無い |
| GET/POST | `/api/admin/pages` | **拡張が要る置き場**（2026-09-16。トップ直下の通信箱・製造台帳・受注・テンプレート置き場）。GET は `{success, pages, templates}` を返す。`pages` は `{title, extension, why, page_id, exists, duplicates?, template?, no_template?}` の並びで、**載っている拡張のぶんだけ**出る（`-tags minimal` ではコアの1件）。`templates` は**機械が作るページのテンプレート**で、中身は `{title, extension, why, page_id?, problem?}`（2026-09-27 夕。`problem` は「無い」「2枚ある」）。`duplicates` は**同じ題の余りのページ**。普通は空で、在るときだけ画面が ⚠ を出す。このとき `page_id` は**いちばん古いもの**で、余りは誰からも使われない。POST は**足りないものだけ**作り、`{success, created, pages}` を返す（**冪等**。何度押しても増えない）。**起動時には作りません**。人が押したことが、そのページを置く意図（通信箱の 2026-09-05 の決定を保つ）。⚠ **置き場は同じ題のテンプレート（テンプレート置き場の下の葉）からだけ作ります**（2026-09-27。それまで本文を持っていた拡張のコードからは消した。利用者:「テンプレートが無ければ作れないまで行きます」）。`template` はそのテンプレートのページID、`no_template` はテンプレートが無いこと（作れない）。POST はテンプレートの無い置き場を作らず、ほかは作り続けて、最後に `{success: false, message, created}` で作れなかったものを返す。テンプレート置き場そのものは見出しだけで作る。監査記録は `required-page.create`。正本は [required_pages.go](../../internal/cms/required_pages.go) |
| GET | `/api/admin/audit` | 監査ログの参照。直近200件。記録対象は認証イベント（`login`/`login.fail`/`logout`）・保存・ページ作成／削除・添付（`attach`/`attach.overwrite`）・親の付け替え・権限変更（公開切替 `publish`/`unpublish` を含む）・ロック強制解除・索引の全再構築・ユーザー／グループ管理・取り込み（`intake.create`/`intake.duplicate`）・PDF判定（`analyze-pdf`）（[認証認可設計.md](../旧文書/認証認可設計.md) §9.4） |
| POST | `/api/rebuild-db` | `data/master` から `cms.db` を再構築（派生インデックスの洗い替え）。先頭で `config/settings.json` を読み直す。2026-09-25 から **`data/tables.db`（表の写し）も**作り直す |
| GET | `/api/admin/tables` | **列の揃っていない表の一覧**（2026-09-25・DBの日本語化 §7 の2段目）。`{success, tables}` で `{name, pages, rows, needs_quote, leftover, suspects, columns[{name, pages, needs_quote, suspect, leftover}]}` の並び。**本文の見出し**を数え直す（値ではない）。`suspect` は2ページ以上ある表で1ページにしか無い見出し、`leftover` は本文にはもう無い表・列（再構築で消える）。正本は [tables_report.go](../../internal/cms/tables_report.go) |
| POST | `/api/admin/tag-rename` | **タグの値を置き換える**（2026-10-01・admin）。要求は `{name, from, to, dry}`。可変タグ `name` の値が `from` のページ（索引 `PagesByTag` で引く）の**「名前：値」の組だけ**を `to` に書き換える（地の文・表・件名は触らない。同じページに `to` の組が既にあれば、古い組を消すだけ）。書くのは `RewriteBody`（版が残る・索引も直る）。**ロックを握られているページ（§3 の書き込みの関門。`editing` に数える）・テンプレートの中は飛ばす**。`dry` なら数えるだけ。応答は `{success, dry, result{found, changed, editing, template, missed}}`。監査記録は `tag.rename`。設定の語彙で選択肢の言葉を変えたときに使う（管理画面のメンテナンス）。正本は [tag_rename.go](../../internal/cms/tag_rename.go) |
| POST | `/api/admin/column-rename` | **表の列の名前を置き換える**（2026-10-15・admin）。要求は `{caption, from, to, dry}`。キャプションが `caption` の表の**見出しのセル（`th`）だけ**を `from` から `to` に書き換える（セルの値・ほかの表・タグは触らない）。探すのは全ページの本文（キャプションで名乗る表は索引に載らないものもあるため）。同じ表に `to` の列が既にあればその表は書き換えない（`conflict`）。書くのは `RewriteBody`（版が残る・索引も直る）。**ロックを握られているページ・テンプレートの中は飛ばす**（テンプレートは人が直す）。`dry` なら数えるだけ。応答は `{success, dry, result{changed, editing, template, conflict}}`。監査記録は `column.rename`。設定の語彙で列の名前を変えたときに使う（管理画面のメンテナンス）。正本は [column_rename.go](../../internal/cms/column_rename.go) |

### 6.1. 表を探す（要認証・⚠ admin 限定ではない）

2026-09-25 に作りました（[【考察】DBの日本語化.md](../考察/【考察】DBの日本語化.md) §7 の3段目）。

- 検索画面（`/assets/tables.html`）と、AI の口です。
- **読めるページの行だけ**返します。
- **自由な SQL は受けません。**
- 正本は、[tables_query.go](../../internal/cms/tables_query.go) です。

| メソッド | パス | 説明 |
|---|---|---|
| GET | `/api/tables` | 探せる表と列。`{success, tables[{name, columns, rows}]}`。返すのは、**読めるページに行のある表**と、**読めるページに値のある列**だけ（名前そのものが読めないページの中身を語ることがあるため） |
| POST | `/api/tables/query` | 表を探す。要求は `{table, columns?, where?[{column, op, value}], limit?}`。`op` は `eq`（等しい）・`contains`（含む）・`gte`（以上）・`lte`（以下）の4つ。表・列の名前は**DB に実際にある名前と照らして**から使い、値は**DB に入れたときと同じ正規化**を掛けて `?` で渡す。`limit` は既定200・上限1000。応答は `{success, result{table, columns, rows[{page_id, title, table_id, row_id, values}], truncated, sql}}`。`sql` は、運用者が DB を直接開いて同じことを引く文（表示用・読めるページの絞りは入らない）。知らない名前・種類は 400 |

> **`/api/intake/memo`・`/api/intake/handled` は、ここではありません**（2026-09-16 に直した）。
>
> - どちらも、**admin 限定ではなく**（要認証＋対象ページの write）、**コアの口でもありません**。
>   `ext/comm` の `Routes()` から生えます。
> - §7 の末尾へ移しました。

> **撤去済みの一括移行API**
>
> - `POST /api/migrate-vocab`: 旧カスタム要素→語彙モデル。2026-08-20 撤去。
> - `POST /api/migrate-headings`・`POST /api/migrate-attachments`:
>   見出し形（D-2）への変換と、添付の `files/` への移動。2026-08-31 導入・両環境適用後 2026-09-02 撤去。
>
> いずれも、管理コンソールにボタンはありません。経緯は、[変更履歴.md](../変更履歴.md) にあります。

## 7. 語彙・PDF・プラグイン

| メソッド | パス | 認可 | 概要 |
|---|---|---|---|
| GET | `/api/tag-schema` | 認証不要 | **本文の語彙**を返す。エディタのシリアライザが従う正本（[本文サニタイズ設計.md](../旧文書/本文サニタイズ設計.md) §7）。キーは9つ（2026-10-13 に `handler_meta.go` で数えた）。`elements` は、構造HTML → 許可属性（`data-*` マーカーもここに属性として現れる。**カスタム要素はゼロ**）。`void` は、終了タグを書かない要素。`block_id` は `data-id`。`vocab` は、語彙レジストリの形式定義を `type` 順で並べたもの。スラッシュメニューと挿入骨格の生成元。2026-09-15 の実測では、既定のビルドで**23形式**（コア5＋`ext/toho` 16＋`ext/comm` 1＋`ext/comm/contacts` 1）。⚠ いまの数は未確認（その後、見積・部材見積依頼などの形式が増えた。形式の一覧は [【一覧】語彙.md](【一覧】語彙.md) §2）。2026-09-15 にコアから出たのは `unhandled-intake`→`ext/comm` と `unknown-contacts`→`ext/comm/contacts` の2つ。`-tags minimal` の実測は5形式）。`vocabulary` は、見出し語の辞書。1語につき `{"type": …, "values": [...]}` の1件で型と選択肢を持つ。エディタの型検証と選択肢の色分けが、サーバーの索引と同じ辞書を使うための配布。**2026-09-14 に `type_inference` と `tag_enums` の2キーを畳んだもの**。`table_vocabulary` は、**表ごとの例外**（設定の `table_vocabulary`・2026-09-26）。エディタは、表の列の型と選択肢を「例外 → `vocabulary` → 文字列」の順で引く。`column_types` は、`th[data-type]` に書ける列型の一覧＝`ColumnTypeNames()`。**2026-09-14 追加**。エディタが手書きで持っていて `datetime`・`ref`・`email` の3つぶん古かったため。`extensions` は、**載っている拡張の名簿**＝`ExtensionIDs()`。ID順。既定のビルドは `["comm","comm/contacts","comm/mail","toho"]`／`-tags nomail` は `["comm","comm/contacts","toho"]`／`-tags minimal` は `[]`（いずれも 2026-09-16 実測）。**空でも `[]`**。画面は拡張のボタンをこれで出し分ける（`app.js` の `hasExtension`。**取得に失敗して `null` のときは出し分けず全部出す**）。ビルドタグで外してもボタンが残り404になっていたため。**2026-09-15 追加**。`sql_names` は、SQL で引くときに気をつける名前の事実（`SQLNames()`・2026-09-28）。エディタは、キャプションのある表の名前と見出しを、これで見て薄赤にする |
| POST | `/api/upload-pdf` | 要認証（対象ページの write） | PDFのアップロード（フォーム欄 `pdf_file`）。ページフォルダの **`files/` サブフォルダ**へ、サーバー生成の名前 `<生成ID>.pdf` で保存する（正本と同居させない。2026-08-31）。**編集ロックが要る**。添付は同名を無条件で上書きし、リビジョンもゴミ箱も無い（＝復元できない）ため、本文編集と同じロックで直列化する。**受け入れは `.pdf` のみ・先頭 `%PDF-` 必須・パス要素と本文/サイドカー同名は拒否**。上限は設定 `max_upload_mib`（`config/settings.json`・既定32MiB・`MaxUploadBytes`）。応答は `{success, file_name, src, id, href}`（`id` は生成ID＝リンクブロックの `data-id`、`href` はきれいなURL）（[アーキテクチャとDBスキーマ.md](../旧文書/アーキテクチャとDBスキーマ.md) §5.1） |
| POST | `/api/upload-image` | 要認証（対象ページの write） | **画像のアップロード**（2026-08-26。フォーム欄 `image_file`。`png`/`jpeg`/`webp`/`gif`/`svg`）。保存先・ロック・サイズ上限・名前の規則は `/api/upload-pdf` と共通（名前の検査は `safeAttachmentName` の1箇所）。加えて**中身のマジックナンバーで種別を判定**し、拡張子と食い違うものは拒否。**EXIF等のメタデータは保存前に除去**する（JPEGは向きを画素へ反映してから再符号化）。SVG は整形式・ルートが `svg`・`<script>`/`<foreignObject>`/`on*=`/`javascript:` 無しを確認。HEIC/AVIF/TIFF/BMP は**形式ごとの理由**を返して拒否。応答は `{success, file_name, kind, src, id}` で、`src` はそのまま `<img src>` に入れる絶対パス（きれいなURL）（要件定義書 §2.6） |
| POST | `/api/upload-file` | 要認証（対象ページの write） | **汎用の添付**（2026-08-31。フォーム欄 `file`）。受ける拡張子は設定 `attachment_extensions`（既定は `settings.go` の14種。`.dxf`・`.xlsx`・`.zip`・`.eml`・`.mp4` 等。`.json` は書けない）。**中身は検査しない**。安全の本体は配信側（未知の種別は `attachment`＋`nosniff` で返す）。画像と `.pdf` はこの口では受けない（専用の口を迂回させないため・400）。保存先・ロック・上限・名前の規則は `/api/upload-pdf` と共通。応答は `{success, file_name, id, href}`。⚠ **通信箱へ上げても添付になるだけ**（2026-09-30。それまでは通信箱宛ての `.eml` を取り込み係へ回して通信記録を作っていた。`.eml` を記録にするのは `/api/intake/eml`） |
| POST | `/api/analyze-attachment` | 要認証（対象ページの write） | **PDFの判定→案件ページ／加工製品ページ生成**（2026-09-01。下請け業務 [ext/toho/analyze_pdf.go](../../ext/toho/analyze_pdf.go)）。**`-tags minimal` で外れる**（2026-09-03。ルートもプラグインの `Routes()` 経由なので一緒に消える）。`{page_id, file}` で添付の `.pdf` を Gemini に **4分類**させる（`doc_type` が `order` / `drawing` / `rfq_reply` / `other`。`rfq_reply` は 2026-10-05）。`.xlsx`（Excel の注文リスト）も受ける（2026-09-30。文字にして判定する）。ZIP の中の PDF は 2026-09-17 から扱わない。取り込みが展開して中身を添付にするので、`entry` 引数は廃止。**枝は4つ**。①**発注書**（`is_client_order:true`）なら案件ページを対象ページの子として生成（`cms.CreateChildPage`。ヘッダ dl＋明細 table＋参照タグ `受信元: <ページID>-<添付ID>`。題は `【顧客】受注 <発注書番号>`・2026-10-16 から頭に【顧客】）。応答 `{success, is_client_order:true, page_id, title}`・監査記録 `analyze-pdf`。②**図面**なら加工製品ページを生成（`<h2>図面</h2>` のヘッダ dl＋参照タグ＋**`<section data-type="file-view" data-ref="<ページID>-<添付ID>">`**＝図面をその場で開くマーカー）。**配線は属性1つ**（2026-09-15。それまでは中に `受信元` のタグを書いていたが、同じ値がすぐ上の図面ブロックにも在り、2つの意味で並んでいた）。同じページのDXF添付と図面番号で突き合わせる（`MatchDXFAttachments`）。応答 `{success, is_client_order:false, doc_type:"drawing", page_id, title, matched_dxf}`・監査記録 `analyze-drawing`。③**部材見積依頼の返事**（業者の返事）なら、返事ページを対象ページの子として作る（題は `【業者】部材見積依頼の返事　<仕入先>`・2026-10-16 から頭に【業者】）。候補の部材見積依頼書が1つなら、結んでその子へ移す。応答 `{success, is_client_order:false, doc_type:"rfq_reply", page_id, title, linked_rfq, candidates, say}`。④**それ以外**は `{success:true, is_client_order:false}` で何も作らない（人が押した問いに「発注書ではない」と答えるのも正常な結果）。**起動は人の操作だけ**（📎の「🤖 解析」ボタン・人間ゲート型）。**ロックは要らない**（対象ページの本文は変えない）。**取り消しはページ削除**（ゴミ箱） |
| GET | `/api/filing-proposal` | 要認証（対象ページの read） | **整理の候補**（2026-09-03。下請け業務 [ext/toho/filing.go](../../ext/toho/filing.go)。`-tags minimal` で外れる）。`?page_id=X` で通信記録ページ X の子のうち、**図面ブロックを持つもの**（加工製品ページ）と**発注書ブロックを持つもの**（案件ページ・2026-09-06）を集めて返す。応答は `{success, rows, orders, kinds, partners, machines, files}`（`files` は記録の添付・2026-10-08。図面以外のファイルも行き先を探して足せるように）。`rows` は部品（`page_id, title, drawing_no, drawing_name, customer, machine_name, kinds`。値は索引から読んだ**推奨値**で、人が直す。`kinds` はそのページにいま付いている区分）。`orders` は受注（`page_id, title, order_no, client_name, ordered_at, destination`。**直す欄は無い**。行き先 `destination` は `記録／年／月／依頼` で、年月は案件ページを作った日で決まる。2026-10-16 までは `受注／年／月` で、年月は発注日だった）。`kinds` は区分の選択肢（設定の語彙 `区分`・2026-09-29 に段の選択肢 `stages` から替えた）。読めないページは黙って落ちる（見せ分けC案） |
| POST | `/api/file-drawings` | 要認証（各ページの write） | **整理の実行**（2026-09-03。同上）。要求は `{rows:[{page_id, customer, machine_name, drawing_name, kinds, merge, confirm_revision, confirm_no_machine, duplicate_of, target, part_no_from_drawing, item_name_from_drawing}], orders:[{page_id, client},…]}`。`merge` は、行き先に同じ題のページがあったときの扱い（空か `new`＝新規・`revision`＝図面改定・`drawing`＝図面追加・`folder`＝装置のページへ・`duplicate`＝重複で取り込まない）。**部品**は `製造台帳／社名／（装置名称）` の下へ移し（2026-09-29 に段をやめた。2026-10-16 に置き場の名前を「取引先」から「製造」へ、夜に「製造台帳」へ改め、社名の下の `加工製品` の箱をなくした）、題を図面名称に揃える（`cms.SetPageParent`＋`SetPageH1`。社名・装置のフォルダは無ければ作り、`ページ種類`〔社名・装置〕を付ける・`ensureProductPath`）。**装置名称は空でもよい**（2026-10-16。空なら社名の直下へ。それまでは装置名称が空の行も動かさなかった）。ただし、その社名の直下に装置名のフォルダ（`ページ種類：装置`、または種類の無いページ・読めるものだけ）があるのに装置名称が空なら、何も書かずに `needs_confirm`（`confirm:"no_machine"`）を返す。画面が確かめのダイアログ（「社名の直下に入れる」「やめる」）で聞き、進めるなら `confirm_no_machine:true` を付けて送り直す（`confirm_revision` とは別の印。使い回すと、図面改定の確かめまで飛ばすため）。`merge:"folder"`（装置のページへ）は、いまも装置名称が要る。`kinds`（区分・試作／見積／旧型）は加工製品ページのタグ `区分` になる。移したページは印のとおりに揃え、合流先へは足すだけ。顧客名か製品名称（`drawing_name`）が空の行と、区分が語彙 `区分` に無い行は動かさない。移した先に同名の加工製品ページが在れば**改定図面**として先頭へ合流（`confirm_revision` が要る場合は `needs_confirm` を返す）。**受注**は `記録／年／月／依頼` の下へ移す（年月は**案件ページを作った日**・2026-10-16。年・月・依頼のフォルダは無ければ作り、`ページ種類` のタグを付ける。それまでは `受注／年／月` で、年月は発注日だった・2026-09-06）。案件ページでないIDは動かさない。応答は `{success, results:[{page_id, outcome, message, target_id?, confirm?}]}`（`confirm` は `needs_confirm` が何の確かめか。`no_machine` か、空〔図面の確かめ〕）（`outcome` は `moved` / `revision` / `added` / `discarded` / `skipped` / `needs_confirm` / `needs_choice`）。**権限も書き込みの関門（§3）も行ごとに見ます**。駄目な行は `skipped` に載るだけで、残りは片付く（**複数のページへ書く**ので、関門はハンドラではなく行ごと・2026-09-14）。監査記録は `file-drawing.move`・`file-drawing.revision`・`file-order.move` |
| GET | `/api/filing-target` | 要認証 | **整理の行き先にページがあるかを聞く**（2026-09-20。[ext/toho/filing.go](../../ext/toho/filing.go) の `FilingTargetAPIHandler`。`-tags minimal` で外れる）。引数は `?customer=&machine=&name=`。2026-09-29 に `stage` を外した（道は `社名／（装置名称）／図面名称`。2026-10-16 に、あいだの `加工製品` の箱をなくし、`machine` が空なら社名の直下で題を探すようにした）。打ち替えのたびに呼ばれ、`{success, exists, ...}` を返す。⚠ **調べるだけで1枚も作らない**。`ensureChildPage` は無ければ作るので使わず、`findFolderChild` だけで辿る（社名・装置名称の段は、同じ題の加工製品ページを外してフォルダだけを見る・2026-10-16。それまでは `findChildByTitle`）（問い合わせのたびに空の顧客ページが増え、次の問い合わせで「在る」と答える最悪の副作用を避ける）。読めないページは「無い」と答える（見せ分けC案）。`customer` か `name` が空なら `exists:false`（`machine` は空でもよい・2026-10-16。それまでは `machine` が空でも `exists:false`） |
| GET | `/api/filing-search` | 要認証 | **整理の行き先を探す**（2026-10-03・[ext/toho/filing_search.go](../../ext/toho/filing_search.go)）。引数は `?q=&customer=&folders=`。加工製品ページを探し、同じ取引先（`customer`）のものを先に、ほかの取引先のものを後に返す。`folders=1` なら装置のページ（社名の直下の、加工製品ページでないページ）も返す（`kind:"folder"`・2026-10-08）。応答は `{success, results:[{page_id, title, machine, customer, drawing_nos, part_nos, kind?}]}`（上限20件）。読めないページは出さない。読むだけ |
| GET | `/api/filing-files-shown` | 要認証（記録の read） | **記録の添付が、どのページに表示されているか**（2026-10-08・[ext/toho/filing_files.go](../../ext/toho/filing_files.go)）。引数は `?page_id=<記録>`。応答は `{success, shown:{<参照>:[{page_id, title}]}}`。読めないページは出さない |
| POST | `/api/filing-attach` | 要認証（記録の read・足す先の write） | **記録の添付を、ほかのページに足す**（2026-10-08・同上。整理の「📎 ほかのファイル」）。要求は `{page_id（記録）, attach（添付ID）, target（足す先）}`。足す先の末尾に、ファイル表示を1つ足す。同じ添付の表示が既にあれば409。足す先のロックを誰かが握っていれば409（§3）。応答は `{success, page_id, title}`。監査記録は `filing.attach` |
| POST | `/api/compare-drawings` | 要認証（両方のページの read） | **図面の中身を比べる**（2026-09-30・[ext/toho/filing_duplicate.go](../../ext/toho/filing_duplicate.go)）。要求は `{page_id（整理を待つ仮のページ）, with（既にある加工製品）}`。2つの図面PDFを Gemini に見比べさせる。中身（sha256）が同じなら、Gemini を呼ばずに `same_file:true` と答える。2つで18MBを超えると413。応答は `{success, same, same_file, differences?, summary}`。**何も書かない** |
| POST | `/api/drawing/supersede` | 要認証（対象ページの write・書き込みの関門） | **あとから改定にする**（2026-10-04・[ext/toho/drawing_supersede.go](../../ext/toho/drawing_supersede.go)）。要求は `{page_id, new:{index, no}, old:{index, no}}`（本文の最上位の図面の何番目か〔0から〕と、その図面番号）。古い図面を `旧版 …` の子ページへブロックごと移し、新しい図面をその場所へ置く。図面番号が合わなければ409（画面を開いたあとに本文が変わった）。応答は `{success, old_page, title}`。監査記録は `drawing.supersede` |
| POST | `/api/product-folder` | 要認証（途中のページの write） | **加工製品の置き場を用意する**（2026-09-29・[ext/toho/product_tree.go](../../ext/toho/product_tree.go)。`-tags minimal` で外れる）。`{customer, machine}` で `製造台帳／社名／装置名称` を辿り、無ければ作って `{success, page_id}` を返す（`machine` が空なら社名のページ。2026-10-16 に、社名の下の「加工製品」の箱をなくした）。作る社名・装置のフォルダには `ページ種類`（社名・装置）を付ける。`customer` が空なら 400。**ワンノートの移植の道具のための口**。木の形を知るのは `ensureProductPath` の1つ（整理と同じ）。置き場「製造台帳」が無く、そのテンプレートも無ければ 409（`cms.ErrNoBoxTemplate`。ほかの「テンプレートが無い」と同じ。2026-10-16 夜に、409 の枝が `cms.ErrNoPageTemplate` だけを見ていて 500 になっていたのを直した。それまでは、テンプレート「製造の加工製品」が無いと 409 だった） |
| POST | `/api/order-item` | 要認証（対象ページの write・書き込みの関門） | **案件明細表のセル1つを書き換える**（2026-09-21。[ext/toho/order_edit.go](../../ext/toho/order_edit.go)）。要求は `{page_id, row, item_no, field, old, value}`。`row` はデータ行の番号（0始まり）。`item_no` はその行の `品番`（**行番号と2つで照合**）。`field` は列の見出しの表示文字。`old` はいまの値（**食い違えば断る**。誰かが先に直したということ）。案件ページの `弊社品番` を結ぶ画面の口 |
| POST | `/api/order-pdf` | 要認証（対象ページの write・書き込みの関門） | **部材発注書のPDFを作って、そのページの添付として残す**（2026-09-21。[ext/toho/order_pdf.go](../../ext/toho/order_pdf.go)・`gopdf`・和文は TTF 埋め込み）。要求は `{page_id}`。応答は `{success, attach_id, file, url, view_note?, overwrote?}`（`url` は開くための URL）。本文にも、PDF を開くマーカーを置く（2026-09-22。だから書き込みの関門を通す）。表示中の PDF がまだ送っていないものなら、新しく作らずにその添付を上書きする（`overwrote: true`・2026-10-14・[order_pdf_sent.go](../../ext/toho/order_pdf_sent.go)）。送ったものなら新しい添付を作って表示を差し替え、送った PDF は残す。上書きの監査記録は `order-pdf.overwrite`。**7年保存がこれで済む**。ファイルがページと一緒に残り、版も監査も付く。差出人は、署名から刷る（`senderLines`・[signature.go](../../ext/toho/signature.go)。読める人の署名だけ）。口の形は、見積書・部材見積依頼書と共通（`servePaper`・[paper_pdf.go](../../ext/toho/paper_pdf.go)）。監査記録は `order-pdf` |
| POST | `/api/order-pdf-docs` | 要認証（対象ページの write） | **部材発注書のうしろに資料を綴じた、FAX・印刷用の1本を作る**（2026-09-28・[ext/toho/order_docs.go](../../ext/toho/order_docs.go)）。要求は `{page_id}`。資料は、明細の行が指す加工製品ページの PDF・画像。そのページの添付として残す。**本文は触らない**（表示中の部材発注書の PDF は差し替えない）。だから書き込みの関門は通さず、write とテンプレートの外だけを見る。綴じられる資料が無ければ400。応答は `{success, attach_id, file, url, skipped}`（`skipped` は綴じられなかった資料と理由）。監査記録は `order-pdf-docs` |
| POST | `/api/material-search` | 要認証 | **材質・形状・寸法で材料を探す**（2026-09-21。[ext/toho/dimension_search.go](../../ext/toho/dimension_search.go)・画面は `material-search` ビュー）。`{material, shape, thickness, diameter, values}`。⚠ **読めないページは混ぜない**。材料表には誰でも行を書けるので、絞らないと、読めない部材発注書の単価と仕入先が引けてしまう |
| POST | `/api/our-order/new`（2026-10-19 から） | — | `draft_page` が製造ページ（`ページ種類：製造`・書ける）なら、部材発注書をその製造ページの子に作ります。⚠ 以下はもとの説明 |
| POST | `/api/our-order/new` | 要認証（置き場の権限は見ない） | **未手配の一覧から部材発注書を1枚作る**（2026-09-21。[ext/toho/our_order_new.go](../../ext/toho/our_order_new.go)）。`{supplier, order_at, due, note, signer, lines:[…], draft_page, draft_index}`。**仕入先が無ければ作らない**。1枚＝1社が部材発注書の単位（相手が決まっていないなら、部材発注書ではなく手配メモ）。置き場は `記録／年／月／製造記録`（年月は作った日。部材発注日ではない・2026-10-16。それまでは `部材発注／年／月`）。発注は納期のグループから出るので、案件ページの下には置けない（2026-09-21 決定）。題は `【弊社】部材発注　<仕入先>`。`draft_page`・`draft_index` は、もとの発注部材表。作り終えたら、その表を消す（2026-09-24）。応答は `{success, page_id, url, draft_note?}`。監査記録は `our-order-new` |
| POST | `/api/our-order/draft` | 要認証（対象ページの write・書き込みの関門） | **発注部材表を1つ足す**（2026-09-22・[ext/toho/order_draft.go](../../ext/toho/order_draft.go)）。要求は `{page_id, lines, into}`。`into` は、足す先の発注部材表（何枚目か・1始まり）。空なら新しく作る。**行が0でも作る**（空の表から始められる）。足す先が見つからなければ409。応答は `{success, page_id, rows, into, temp_note?}`。監査記録は `order-draft.create` |
| POST | `/api/our-order/draft/remove` | 要認証（対象ページの write・書き込みの関門） | **発注部材表から行を1つ外す**（2026-09-22・同上）。要求は `{page_id, table, row}`（何枚目の表の、見出しを除いた何行目か・どちらも1始まり）。必要部材表は毎回計算する鏡なので、外せば戻る。その行が無ければ409。応答は `{success, page_id}`。監査記録は `order-draft.remove` |
| POST | `/api/our-order/line-status` | 要認証（対象ページの write・書き込みの関門） | **部材発注明細表の行の `状態` を書く**（2026-09-22・[ext/toho/order_status.go](../../ext/toho/order_status.go)）。要求は `{page_id, row, value}`。`row` は見出しを除いたデータ行（1始まり）。0 なら、取消でない行すべて。`value` は `未発注`・`発注済`・`納品済`・`取消` のどれか（ほかは400）。応答は `{success, page_id, rows}`。監査記録は `our-order.line-status` |
| POST | `/api/our-order/return-row` | 要認証（対象ページの write・書き込みの関門） | **部材発注明細表の行を、必要部材表へ戻す**（2026-09-24・[ext/toho/order_return.go](../../ext/toho/order_return.go)）。要求は `{page_id, row}`（1始まり）。行を部材発注書から消す。戻す先へは何も書かない（鏡なので、自動で現れる）。取消とは別の操作。応答は `{success, page_id, to}`。監査記録は `our-order.return-row` |
| POST | `/api/our-order/sent` | 要認証（対象ページの write・書き込みの関門） | **送った結果を反映する**（2026-09-22・[ext/toho/order_send.go](../../ext/toho/order_send.go)）。要求は `{page_id, method}`。`method` は `メール`・`FAX`・`手渡し` のどれか（ほかは400）。取消でない行すべてに、`発注済` の印を付ける。表示中の PDF を、本文の「送った部材発注書」の節へ1行で記録する（2026-10-14）。応答は `{success, page_id, method, rows}`。監査記録は `our-order.sent` |
| POST | `/api/our-order/note` | 要認証（対象ページの write・書き込みの関門） | **部材発注書の「備考」の節を書き換える**（2026-10-14・[ext/toho/order_pdf_sent.go](../../ext/toho/order_pdf_sent.go)）。要求は `{page_id, note}`。部材発注書ページ（部材発注明細表を持つ）でなければ400。口の形は `/api/estimate/note` と同じ。応答は `{success, page_id}`。監査記録は `our-order.note` |
| POST | `/api/our-order/skip` | 要認証（対象ページの write・書き込みの関門） | **必要部材表の行を「手配不要」にする**（2026-10-01・[ext/toho/skip.go](../../ext/toho/skip.go)）。要求は `{page_id, lines, reason}`。`page_id` は、必要部材表のあるページ。行を「手配不要表」へ入れる。臨時部材表から来た行は、臨時部材表から消す。行を選ばなければ400。応答は `{success, page_id, rows, temp_note?}`。監査記録は `procure.skip` |
| POST | `/api/our-order/skip/remove` | 要認証（対象ページの write・書き込みの関門） | **「手配不要表」から行を外す**（2026-10-01・同上）。要求は `{page_id, row}`。外すと、必要部材表へ戻る。弊社品番の無い行は、臨時部材表へ戻す。その行が無ければ409。応答は `{success, page_id}`。監査記録は `procure.skip-remove` |
| POST | `/api/estimate/record` | 要認証（対象ページの write・書き込みの関門） | **見積表に単価を記録する**（2026-10-15・[ext/toho/estimate.go](../../ext/toho/estimate.go)。「📌 この単価を記録」「📌 見積表を作って記録」）。要求は `{page_id, block}`。`block` 番目（0から・文書の順）の組の見積計算表の価格を足して原価とし、見積表の利益率（空なら設定の既定）で単価を計算して、見積表の原価・利益率・単価に書く。見積表が無ければ、見積計算表のすぐ後ろに作る。計算できなければ400。応答は `{success}`。監査記録は `estimate.record`（2026-10-15 に `/api/estimate-rate` をやめて置き換えた） |
| POST | `/api/estimate/duplicate` | 要認証（対象ページの write・書き込みの関門） | **見積計算表と見積表の組を写す**（2026-10-15・同上。「⧉ このロットを写す」）。要求は `{page_id, block}`。すぐ前の見出し（H3）・見積計算表・見積表を写して、組のすぐ後ろに置く（見出しに「（写し）」・見積表のロット・原価・単価は空）。応答は `{success}`。監査記録は `estimate.duplicate` |
| GET | `/api/estimate/rows` | 要認証（対象ページの read） | **そのページの見積計算表と、写せる行**（2026-10-05・[ext/toho/estimate_put.go](../../ext/toho/estimate_put.go)）。引数は `?page_id=`。応答は `{success, tables:[{index, lot, rows:[{step, price}], why?}]}`（`lot` は組の見積表のロット）（`why` は、写せない形の表の理由）。見積計算表が無ければ400 |
| POST | `/api/estimate/put-cost` | 要認証（対象ページの write・書き込みの関門） | **見積回答の単価を、見積計算表へ写す**（2026-10-05・同上。「💴 見積回答」の「計算表へ」）。要求は `{page_id, index, step, add, value, note}`。`index` 枚目の表の、工程 `step` の行の `価格` に `value` を書く。`add` なら行を足す。`備考` に `note`（出所）を書く。写せなければ409。応答は `{success, page_id, changed}`。監査記録は `estimate.put-cost` |
| POST | `/api/estimate/add` | 要認証（加工製品ページの read。`into` があれば、その見積書の write・書き込みの関門） | **見積表を、見積書へ1行入れる**（[ext/toho/estimate_doc.go](../../ext/toho/estimate_doc.go)）。要求は `{product, index, into, client, person, signer}`。加工製品ページ `product` の `index` 枚目（0から）の見積表（記録した単価・ロット・表面処理と備考）を、見積書 `into` の見積明細表へ入れる。`into` が空なら、新しい見積書ページを `記録／年／月／依頼` に作る（年月は作った日・題は `【弊社】見積　<見積先>`。2026-10-16 までは `見積／年／月`）。応答は `{success, page_id, new}`。監査記録は `estimate.add`・`estimate.new` |
| GET | `/api/estimate/folder-products` | 要認証（そのページの read） | **装置フォルダの下の加工製品と、その見積表**（2026-10-03・[ext/toho/estimate_folder.go](../../ext/toho/estimate_folder.go)。「見積書を作る」）。引数は `?folder=<ページ番号>`。応答は `{success, folder, title, client, persons, products}`。`products` は、加工製品ごとに `{id, title, machine, part_no, drawing_no, migrating, tables:[{index, quantity, unit, price, note, why}]}`。`client` は見積先（装置フォルダの社名）、`persons` はその会社の担当者の候補。無い・読めない番号は404。読むだけ |
| POST | `/api/estimate/from-folder` | 要認証（各加工製品ページの read） | **選んだ見積表から、見積書ページを1枚作る**（2026-10-03・同上）。要求は `{items:[{product, index}], client, person, signer}`。送られた順に、見積明細表へ入れる。置き場と題は `/api/estimate/add` で新しく作るときと同じ（`newEstimatePage`）。何も選ばなければ400。読めない加工製品は404。単価の無い見積表があれば409。応答は `{success, page_id, rows}`。監査記録は `estimate.new` |
| POST | `/api/estimate/mark` | 要認証（対象ページの write・書き込みの関門） | **見積書に、送った・送らないの印を付ける**（2026-10-03・[ext/toho/estimate_unsent.go](../../ext/toho/estimate_unsent.go)）。要求は `{page_id, mark}`。`mark` は `sent`（FAX・手渡しで送った）か `nosend`（送付不要）。`送付日` か `送付不要` のタグに、今日の日付を書く。「未送付の見積書」の一覧から外れる。見積書ページでなければ400。応答は `{success, page_id, tag, date}`。監査記録は `estimate.sent`・`estimate.nosend` |
| POST | `/api/estimate/note` | 要認証（対象ページの write・書き込みの関門） | **見積書の「備考」の節を書き換える**（[ext/toho/estimate_note.go](../../ext/toho/estimate_note.go)）。要求は `{page_id, note}`。見積書ページでなければ400。応答は `{success, page_id}`。監査記録は `estimate.note` |
| POST | `/api/estimate-pdf` | 要認証（対象ページの write・書き込みの関門） | **見積書のPDFを作って、そのページの添付として残す**（[ext/toho/estimate_pdf.go](../../ext/toho/estimate_pdf.go)）。要求は `{page_id}`。口の形と応答は、`/api/order-pdf` と同じ（`servePaper`）。監査記録は `estimate-pdf` |
| GET | `/api/estimate/rows` の読む先（2026-10-17） | — | `page_id` が加工製品ページで、その品物の見積計算書があれば、**いちばん新しい見積計算書**の見積計算表を返します。応答に `page_id`（写す先）と `title` が加わりました。`/api/estimate/put-cost` はその `page_id` へ送ります（[ext/toho/estimate_put.go](../../ext/toho/estimate_put.go)） |
| POST | `/api/estimate-calc/new` | 要認証（加工製品ページの read） | **新しい見積計算書を作る**（2026-10-16・[ext/toho/estimate_calc_doc.go](../../ext/toho/estimate_calc_doc.go)。加工製品ページの「🧮 見積計算書」の「➕ 新しい見積計算書を作る」）。要求は `{product, mfg}`（加工製品ページの番号・`mfg` は任意の製造ページ）。`ページ種類：加工製品` でなければ400。`mfg` が書ける製造ページでなければ400。中身は、その品物のいちばん新しい見積計算書（`ページ種類：弊社見積計算書` で `弊社品番` がこのページのもの）を写す。無ければ加工製品ページの見出し「見積もり計算」（H2）から次の H2 の手前まで。加工製品ページは変えない。テンプレート「見積計算書」を写し、題は `【弊社】見積計算書　<品物の題>`、タグは `弊社品番`・`見積計算日`（今日）・`ページ種類`。置き場は `記録／年／月／製造記録`（作った日）。`mfg` があれば、その製造ページの子（2026-10-19）。テンプレートが無ければ409。応答は `{success, page_id, from}`（`from` は写した元）。監査記録は `estimate-calc.new` |
| POST | `/api/delivery/new` | 要認証（各案件ページの read。`mfg` があれば、その製造ページの write） | 2026-10-19 から `{mfg}`（製造ページ）を受けます。あれば納品書をその製造ページの子に作り、行の `row` が負なら、案件明細の行を品番（空なら品名・`name`）で探します。⚠ 以下はもとの説明: | **案件残表で選んだ行から、納品書ページを1枚作る**（2026-10-17・[ext/toho/delivery.go](../../ext/toho/delivery.go)。案件残表の「🚚 選んだ行で納品書を作る」）。要求は `{items:[{order, row, item, qty}]}`（`order` は案件ページ・`row` は案件明細表の行〔0始まり〕・`item` は品番・`qty` は今回納める数）。読めない案件ページ（`ページ種類：顧客発注書` でない・見えない）・品番が `item` と違う行（取り違えない）・読めない数があれば409。客先（`発注元`）の違う行が混ざれば400（1枚に1社）。品番・品名・単位・単価・発注書番号・弊社品番は案件明細から写す（単価の空は空のまま）。テンプレート「納品書」を写し、題は `【弊社】納品書　<客先>`、タグは `納品書番号`（ページ番号）・`客先`・`ページ種類`（`弊社納品書`）。`発行日` は空（未発行）。置き場は `記録／年／月／製造記録`（作った日）。テンプレートが無ければ409。応答は `{success, page_id}`。監査記録は `delivery.new` |
| POST | `/api/mfg/estimate` | 要認証（製造ページの write・書き込みの関門） | **製造の見積書を作る**（2026-10-19・[ext/toho/quote_done.go](../../ext/toho/quote_done.go)）。要求は `{page_id, signer}`（`signer` は空なら既定の人）。`ページ種類：製造` でなければ400。製造明細の見積の依頼の案件の行から、見積書を製造ページの子に作る（見積明細表に `案件` を写す・単価は見積表からロットで選ぶ）。見積の依頼の行が無ければ400。応答は `{success, page_id, rows, missing, message}`（`missing` は単価の無い行）。監査記録は `estimate.new` |
| POST | `/api/mfg/rfq` | 要認証（製造ページの write・書き込みの関門） | **製造の部材を、製造ページの見積依頼必要部材表へ入れる**（2026-10-19・[ext/toho/mfg_more.go](../../ext/toho/mfg_more.go)）。要求は `{page_id}`。`ページ種類：製造` でなければ400。製造明細の行ごとに、その加工製品の部材をロット＝数量で集める（案件も写す・手配済みは引かない）。入れるものが無ければ400。応答は `{success, rows, skipped}`。監査記録は `mfg.rfq` |
| POST | `/api/mfg/parts` | 要認証（製造ページの write・書き込みの関門） | **製造の、まだ手配していない部材を、製造ページの発注部材表へ入れる**（2026-10-19・[ext/toho/mfg_view.go](../../ext/toho/mfg_view.go)）。要求は `{page_id}`。`ページ種類：製造` でなければ400。数は必要部材表と同じ残りと「この製造で作る数 × 1個あたりの数」の小さいほう。入れるものが無ければ400。応答は `{success, rows, skipped}`（`skipped` は弊社品番の無い行）。監査記録は `mfg.parts` |
| POST | `/api/mfg/made` | 要認証（製造ページの write・書き込みの関門） | **製造ページの製造明細表の「製造完了」を書く**（2026-10-19・[ext/toho/mfg_view.go](../../ext/toho/mfg_view.go)）。要求は `{page_id, row, item_no, old, value}`（`row` はデータ行の番号〔0始まり〕・`value` は「済」か空・ほかは400）。`ページ種類：製造` でなければ400。行番号・品番・いまの値が合わなければ409。監査記録は `mfg.made` |
| POST | `/api/mfg/new` | 要認証（各案件ページの read） | **案件残表で選んだ行から、製造ページを1枚作る**（2026-10-19・[ext/toho/mfg.go](../../ext/toho/mfg.go)。案件残表の「🏭 選んだ行で製造開始」）。要求は `{items:[{order, row, item, qty}]}`（`qty` は今回作る数）。読めない案件ページ（`ページ種類：顧客発注書` でない・見えない）・品番が `item` と違う行・読めない数・まだ製造に入っていない数より多い数は409。客先（`発注元`）の違う行が混ざれば400（1つの製造は1社）。テンプレート「製造ページ」を写し、題は装置名（無ければ品名）を「、」でつないだもの、タグは `製造番号`（ページ番号）・`客先`・`始めた日`・`ページ種類`（`製造`）。置き場はトップ直下の「製造」（無ければテンプレート「製造」から作る）。応答は `{success, page_id, url}`。監査記録は `mfg.new` |
| POST | `/api/invoice/new` | 要認証（読める納品書の行だけを集める） | **締め日までの、請求書にしていない納品書の行で、請求書ページを1枚作る**（2026-10-17・[ext/toho/invoice.go](../../ext/toho/invoice.go)。ビュー「請求書を作る」の「🧾 請求書を作る」）。要求は `{client, close}`（`close` は締め日 YYYY-MM-DD）。集めるのは `ページ種類：弊社納品書` で発行日のある納品書の、客先の合う行のうち、納品日（無ければ発行日）が締め日以前・単価がある・まだ請求書（`ページ種類：弊社請求書` の請求明細表）に載っていない行。無ければ400。テンプレート「請求書」を写し、題は `【弊社】請求書　<客先>`、タグは `請求書番号`（ページ番号）・`客先`・`請求日`・`請求期間`・`支払条件`（連絡帳の客先の組織ページから）・`請求担当`（作った人の連絡帳のページ）・`ページ種類`。`発行日` は空（未発行）。置き場は `記録／年／月／製造記録`（作った日）。応答は `{success, page_id, url, rows, later, no_price}`（`later` は締めより後の行・`no_price` は単価の無い行）。監査記録は `invoice.new` |
| POST | `/api/invoice-pdf` | 要認証（対象ページの write・書き込みの関門） | **請求書のPDFを作って、そのページの添付とし、明細の下に表示する**（2026-10-17・[ext/toho/invoice_pdf.go](../../ext/toho/invoice_pdf.go)）。要求は `{page_id}`。単価の無い行があれば400。登録番号と振込先は、請求担当の人が属する連絡帳の組織ページ（タグ `登録番号`・節「請求書の振込先」）から。消費税は1枚に1回だけ丸める。`発行日` が空なら今日を書く。監査記録は `invoice-pdf` |
| POST | `/api/delivery-pdf` | 要認証（対象ページの write・書き込みの関門） | **納品書のPDFを作って、そのページの添付とし、明細の下に表示する**（2026-10-17・[ext/toho/delivery_pdf.go](../../ext/toho/delivery_pdf.go)。納品明細表の足元の「📄 PDFを作る」）。要求は `{page_id}`。紙の作りは部材発注書の紙と同じ口（`makePaper`）。`発行日` が空なら今日を書く（発行済になる）。単価の決まっていない行があれば、紙に小計・合計を刷らない。監査記録は `delivery-pdf` |
| POST | `/api/estimate/import` | **admin のみ** | **過去の販売価格を、見積書ページへ移す**（2026-10-04・[ext/toho/estimate_import.go](../../ext/toho/estimate_import.go)。移行の道具のための口）。要求は `{client, date, note, lines:[{product_id, quantity, unit, price, note}]}`。`client` が空なら、1行目の加工製品の取引先。1回で1枚。置き場は `記録／年／月／依頼` で、年月は `date` のもの（過去の書類を移す口なので、作った日ではない・2026-10-16。それまでは `見積／年／月`）。見積日も送付日も `date`。日付か行が無ければ400。応答は `{success, page_id, client, rows}`。監査記録は `estimate.import` |
| POST | `/api/rfq/collect` | 要認証（送り先のページの write・書き込みの関門） | **加工製品の部材を集めて、見積依頼必要部材表へ足す**（2026-10-03・[ext/toho/rfq_api.go](../../ext/toho/rfq_api.go)）。要求は `{page_id, product, lot}` か `{page_id, items:[{product, lot, for_order}]}`。`page_id` が空なら、置き場「部材見積依頼」へ足す（2026-10-04。案件残表から送るとき）。加工製品が1つなら、集められない理由をそのまま断る。何枚かなら、集められないものは飛ばして `skipped` に理由を返す。1つも集められなければ409。応答は `{success, rows, products, skipped, page_id}`。監査記録は `rfq.collect` |
| GET | `/api/rfq/folder-products` | 要認証（そのページの read） | **装置フォルダの下の加工製品**（2026-10-03・同上）。引数は `?folder=<ページ番号>`。応答は `{success, folder, title, products:[{id, title, machine, part_no, drawing_no, migrating, lots}]}`（`lots` は見積計算表のロット）。画面は、これで一時的な表を組む（本文には残さない）。選んだものは `/api/rfq/collect` の `items` で送る。無い・読めない番号は404 |
| POST | `/api/rfq/needs/move` | 要認証（対象ページの write・書き込みの関門） | **見積依頼必要部材表の選んだ行を、見積依頼部材表へ移す**（2026-10-03・同上）。要求は `{page_id, rows, temp_rows, into}`（`temp_rows` は臨時部材表の行。`into` は入れる先の見積依頼部材表が何枚目か。空なら新しい表を作る）。単価は写さない（聞くものなので）。**何も選ばなくても、空の見積依頼部材表を作れる**。入れる先が見つからなければ409。応答は `{success, rows}`。監査記録は `rfq.draft` |
| POST | `/api/rfq/needs/remove` | 要認証（対象ページの write・書き込みの関門） | **選んだ行を消す**（「不要」・2026-10-03・同上）。要求は `{page_id, rows, temp_rows}`。記録は残さない。行を選ばなければ400。応答は `{success, rows}`。監査記録は `rfq.needs.remove` |
| POST | `/api/rfq/draft/back` | 要認証（対象ページの write・書き込みの関門） | **見積依頼部材表の1行を、見積依頼必要部材表へ戻す**（2026-10-03・同上）。要求は `{page_id, table, row}`（何枚目の表の、見出しを除いた何行目か）。最後の1行なら、表ごと消える。その行が無ければ409。応答は `{success}`。監査記録は `rfq.draft.back` |
| POST | `/api/rfq/new` | 要認証（対象ページの write・書き込みの関門） | **見積依頼部材表から、部材見積依頼書ページを1枚作る**（2026-10-03・[ext/toho/rfq_doc.go](../../ext/toho/rfq_doc.go)）。要求は `{page_id, table, supplier, signer, date, note}`（`table` は何枚目の見積依頼部材表か・1始まり）。**仕入先が無ければ作らない**（1枚に1社）。置き場は `記録／年／月／製造記録`（年月は作った日。`date`＝部材見積依頼日ではない・2026-10-16。それまでは `部材見積依頼／年／月`）。`page_id` が書ける製造ページなら、その子（2026-10-19）。題は `【弊社】部材見積依頼　<仕入先>`。作り終えたら、もとの表を消す。表に行が無ければ400。表が見つからなければ409。応答は `{success, page_id, rows, draft_note?}`。監査記録は `rfq.new` |
| POST | `/api/rfq/note` | 要認証（対象ページの write・書き込みの関門） | **部材見積依頼書の「備考」の節を書き換える**（[ext/toho/rfq_pdf.go](../../ext/toho/rfq_pdf.go)）。要求は `{page_id, note}`。部材見積依頼書ページでなければ400。応答は `{success, page_id}`。監査記録は `rfq.note` |
| POST | `/api/rfq-pdf` | 要認証（対象ページの write・書き込みの関門） | **部材見積依頼書のPDFを作って、そのページの添付として残す**（同上）。要求は `{page_id}`。口の形と応答は、`/api/order-pdf` と同じ（`servePaper`）。監査記録は `rfq-pdf` |
| POST | `/api/rfq-pdf-docs` | 要認証（対象ページの write） | **部材見積依頼書のうしろに資料を綴じた、FAX・印刷用の1本を作る**（2026-10-05・同上）。要求は `{page_id}`。作りと応答は、`/api/order-pdf-docs` と同じ（**本文は触らない**ので、書き込みの関門は通さない）。辞退の行の資料は綴じない。部材見積依頼書ページでなければ400。監査記録は `rfq-pdf-docs` |
| POST | `/api/rfq/sent` | 要認証（対象ページの write・書き込みの関門） | **FAX・手渡しで送ったことを書く**（同上）。要求は `{page_id}`。`送付日` に今日を書く。部材見積依頼書ページでなければ400。応答は `{success, page_id, date}`。監査記録は `rfq.sent` |
| POST | `/api/rfq/answered` | 要認証（対象ページの write・書き込みの関門） | **手で書いた単価を、回答として記録する**（2026-10-05・[ext/toho/rfq_reply.go](../../ext/toho/rfq_reply.go)。「✓ 回答を記録」）。要求は `{page_id}`。単価の入った未回答（か空）の行を「回答あり」にし、回答日に今日を書く。部材見積依頼書ページでなければ400。書けなければ409。応答は `{success, page_id, changed}`。監査記録は `rfq.answered` |
| GET | `/api/rfq/reply-sources` | 要認証（対象ページの read） | **返事として読めるものの候補**（2026-10-05・同上）。引数は `?page_id=<部材見積依頼書>`。並びは3つ。①結んだ返事ページの原本。②そのページに置いたファイル（作った部材見積依頼書の PDF は除く）。③題に「№ <ページ番号>」を含む受信メールの本文と添付（新しい順。題の当たったページ20枚まで）。応答は `{success, sources:[{label, page_id, file}]}`。読めないメールは出さない |
| POST | `/api/rfq/read-reply` | 要認証（対象ページの write・書き込みの関門） | **業者の返事を Gemini に読ませる**（2026-10-05・同上。「🤖 返事を読む」）。要求は `{page_id, source_page, source_file}`。**何も書かない**。応答は `{success, rows（いまの行）, guesses（案）, summary}`。案を直して書き込むのは `/api/rfq/apply-reply`。ファイルが18MBを超えると413。`GEMINI_API_KEY` が無ければ503。読めなければ502。監査記録は `rfq.read-reply` |
| POST | `/api/rfq/apply-reply` | 要認証（対象ページの write・書き込みの関門） | **直した案を、部材見積依頼明細表へ書き込む**（2026-10-05・同上）。要求は `{page_id, lines:[{row, what, price, declined, note}]}`。書けなければ409。応答は `{success, page_id, changed}`。監査記録は `rfq.reply` |
| POST | `/api/rfq-reply/link` | 要認証（返事ページの write・書き込みの関門。部材見積依頼書の write） | **部材見積依頼の返事ページを、部材見積依頼書に結ぶ**（2026-10-05・[ext/toho/rfq_reply_page.go](../../ext/toho/rfq_reply_page.go)）。要求は `{page_id（返事ページ）, rfq（部材見積依頼番号）}`。結んで、返事ページを部材見積依頼書の子へ移す。返事ページでなければ400。部材見積依頼書が無ければ404。応答は `{success, rfq}`。監査記録は `rfq-reply.link` |
| GET | `/api/rfq-reply/draft` | 要認証（対象ページの read） | **返事ページから作る部材見積依頼書の案**（2026-10-05・[ext/toho/rfq_reply_make.go](../../ext/toho/rfq_reply_make.go)）。引数は `?page_id=<返事ページ>`。「業者の返事（読んだまま）」の表を行にして返す。返事ページでなければ400。もう部材見積依頼書に結ばれていれば409。応答は `{success, supplier, date, rows}` |
| POST | `/api/rfq-reply/make-rfq` | 要認証（返事ページの write・書き込みの関門） | **返事から部材見積依頼書ページを作って、結ぶ**（2026-10-05・同上。「この返事から部材見積依頼書ページを作る」）。要求は `{page_id（返事ページ）, rows, note}`。単価の入った行が無ければ400。返事ページに仕入先のタグが無ければ400。作る部材見積依頼書の置き場は `記録／年／月／製造記録` で、年月は返事の回答日のもの（`/api/rfq/import` と同じ作り・`createAnsweredRFQPage`・2026-10-16）。応答は `{success, page_id, rows}`。監査記録は `rfq-reply.make-rfq` |
| POST | `/api/rfq/import` | **admin のみ** | **過去の見積もりを、部材見積依頼書ページへ移す**（2026-10-04・[ext/toho/rfq_doc.go](../../ext/toho/rfq_doc.go)。移行の道具のための口）。要求は `{supplier, date, note, lines}`（`lines` の `cost` が単価）。1回で1枚（業者×日付）。単価に値段、状態は「回答あり」、回答日と部材見積依頼日は `date`。置き場は `記録／年／月／製造記録` で、年月は `date` のもの（過去の書類を移す口なので・2026-10-16。それまでは `部材見積依頼／年／月`）。応答は `{success, page_id, rows}`。監査記録は `rfq.import` |
| POST | `/api/rfq/fill-names` | **admin のみ**（対象ページの write・書き込みの関門） | **部材見積依頼明細表の空の品番・品名を、加工製品ページから埋める**（2026-10-04・同上。移したページの後始末）。要求は `{page_id}`。部材見積依頼書ページでなければ400。応答は `{success, page_id, filled}`。監査記録は `rfq.fill-names` |
| POST | `/api/rfq/set-cells` | **admin のみ**（対象ページの write・書き込みの関門） | **部材見積依頼明細表のセルを、いまの値を確かめてから書き換える**（2026-10-04・同上。移したページの後始末）。要求は `{page_id, edits:[{row, expect:{列: いまの値}, set:{列: 新しい値}}]}`（`row` は見出しを除いた何行目か・1始まり）。**1つでも合わなければ、何も書かない**（409。どこが違うかを返す）。応答は `{success, page_id, changed}`。監査記録は `rfq.set-cells` |
| POST | `/api/table/append-rows` | **admin のみ**（対象ページの write・書き込みの関門） | **キャプションで名指した表の末尾に、行を足す**（[ext/toho/table_append.go](../../ext/toho/table_append.go)。移行の道具のための口）。要求は `{page_id, caption, rows:[{見出し: 値}]}`。そのページの、キャプションが `caption` の1枚目の表に足す。キャプションか行が無ければ400。足せなければ409（理由を返す）。応答は `{success, page_id, rows}`。監査記録は `table.append-rows` |
| GET | `/api/suggest` | 要認証 | **入力候補**（2026-09-21。`?source=…&q=…`・コア [internal/cms/suggest.go](../../internal/cms/suggest.go)）。出どころは拡張が `cms.RegisterSuggestSource` で持ち込み、**コアは名前しか知らない**（`推奨業者` が最初の1つ・`ext/toho/suggest_partner.go`）。**知らない出どころは 404 ではなく空で返す**。拡張を外したビルドでは、その出どころが居ないのが正常で、画面は候補が出ないだけであるべき（拡張の出し分けと同じ考え方） |
| GET | `/api/file-ref` | 要認証（指されたページの read） | **ファイルの ID を添付の組へ引く**（2026-10-01・コア [internal/cms/attachment_ref.go](../../internal/cms/attachment_ref.go)）。`?ref=ページID-添付ID`（添付の住所 `/ページID/添付ID.拡張子` も受ける）。応答は `{success, ref, page_id, file（保存名）, name（届いたときの名前）}`。**読めない・無いは同じ 404**、ID の形でなければ 400。使い手はメールの送る欄の「🔗 ID で添付を足す」（`assets/mail-compose.js`） |
| POST | `/api/local-edit/start` | 要認証（指された添付の read） | **添付をローカルのアプリで編集するための鍵を出す**（2026-09-29・コア [internal/cms/local_edit.go](../../internal/cms/local_edit.go)。「📝 ローカル編集」）。要求は `{ref}`（「ページ番号-添付ID」）。応答は `{success, link, name, writable}`。`link` は `w-cms-edit:<w-cms のアドレス>#<鍵>` で、各 PC の常駐ヘルパー（`cmd/w-cms-edit`）が受け取る。鍵は、そのファイル1つに限る。12時間で切れる（使うたびに延びる）。サーバーの記憶だけなので、再起動で切れる。`writable` は、上書きできるか（ページへの書き込み権限と、設定 `attachment_readonly` の範囲で決まる）。添付が無い・読めないは404。監査記録は `local-edit.start` |
| GET/PUT | `/api/local-edit/file` | **ログインの外**（`?token=` の鍵だけを確かめる。`RequireAuth` の外側） | **ヘルパーが、添付の中身を落とす・上げる**（2026-09-29・同上）。GET は、中身を返す（`ETag`・`X-WCMS-Name`・`X-WCMS-Writable` のヘッダつき）。PUT は、中身を上書きする。`If-Match` に、落としたときの ETag が要る（無ければ428）。いまの ETag と違えば409（ほかの人が先に書き換えた）。書けなければ403。前の中身は版に残る。権限は、鍵の持ち主の**いまの**権限で確かめ直す。鍵が切れていれば401。監査記録は `local-edit.write` |
| GET | `/api/record-makers?page_id=` | 要認証（対象ページの read） | **記録から作れるページの種類**（2026-10-01・通信 [ext/comm/record_make.go](../../ext/comm/record_make.go)）。応答は `{success, kinds[{name, hint}], made[{page_id, title, kind}]}`。種類は拡張が `comm.RegisterRecordMaker` で登録する（東邦なら受信のメールに `案件ページ`・`加工製品ページ`）。記録（`チャネル` のタグ）でない・下書きなら空。書けない人には `kinds` を空で返し、`made`（もう作ったページの印）だけ返す |
| POST | `/api/record-make` | 要認証（対象ページの write） | **記録からページを作る**（2026-10-01・同上）。要求は `{page_id, kind}`。その記録で選べない種類は 400。応答は `{success, pages[{page_id, title, kind}], say, open}`。`pages` が空なら作らなかった（`say` に理由）。`open` は人が続けて書くページ（画面が `?edit=true` を付けて、書ける形で開く）。東邦の `案件ページ`（[analyze_mail.go](../../ext/toho/analyze_mail.go)）は、件名・差出人・日付・本文（`comm.RecordBodyText`）を Gemini へ渡し、添付の解析と同じ形で案件ページを記録の子に作る（`受信元` はページ全体・PDF の枠は `DropFileView` で落とす・本文が無ければ 400・テンプレートが無ければ 409）。2026-10-01 の朝は `/api/analyze-mail-order` だった。`加工製品ページ`（[mail_product.go](../../ext/toho/mail_product.go)）は、テンプレートから空の加工製品ページを記録の子に作り（`客先` は差出人の取引先・`受信元` はページ全体・図面の枠は空・改訂明細表の1版目）、`open` で返す。監査記録は `analyze-mail`・`mail-product` |
| GET | `/api/zip-list` | 任意認証（read） | **ZIP添付の目録**（2026-09-01。`?page_id=&file=`。`OptionalAuth` 配下）。標準の `archive/zip` でセントラルディレクトリだけ読み、**この口は展開しない**（ZIP爆弾対策。展開はメール取り込みの時点で `cms.ExpandZip` が上限つきで行う。2026-09-17）。UTF-8フラグの無いエントリ名は Shift_JIS として復号。最大500件（超過は `truncated:true`）。認可は添付配信と同じ閲覧側の関門（`RequirePageReadOrPublic`。読めるページの添付は目録も読める）。応答は `{success, entries:[{name,size}], total, truncated}` |
| GET | `/api/mail/status` | 要認証 | メールの設定とサインイン状態（`{configured, address}`）。**トークンは返しません**。出すのは「誰としてサインインしているか」だけ。**`/api/mail/*` の9本は拡張 [ext/comm/mail](../../ext/comm/mail/) の `Routes()` 提供**で、`-tags nomail` でも `-tags minimal` でも外れます（送信の口は `comm.RegisterMailer` で `ext/comm` へ差し込まれ、使う側は `comm.CurrentMailer()` に尋ねる） |
| POST | `/api/mail/signin` | 要認証 | **デバイスコードのサインインを始める**（`{user_code, verification_uri, expires_in}`）。完了待ちは背後で回り、結果は `/api/mail/status` で確かめる（ここで待つと画面が固まる） |
| POST | `/api/mail/import` | 要認証 | **IMAP で未取り込みのメールを通信箱へ**。要求は `{folder?, max?, since?}`。`folder` は空・`受信`＝受信箱／`送信`＝送信済みの箱〔LIST の `Sent` の印で探す・差出人が自分なので「送信」の記録になる・2026-09-29〕。画面は管理画面の開発メニュー（通信箱の「📥 新しいメールを読み込む」は 2026-10-09 から下の `/api/mail/fetch-new`）。**要求が切れても、始めた1回分は最後まで取り込む**（文脈は `context.WithoutCancel`・2026-10-09）。接続は取り込み1回につき1本。**見る上限（5000）と取り込む上限（50）は別**。混ぜると、押しても古いほうへ進まない。`Message-ID` の見出しだけ先に読み、**本体を落とす前に**重複を弾く。**古い順**に取り込む（スレッドの親が先に着く）。`EXAMINE`＋`BODY.PEEK[]` で**既読にしない**。応答は `{success, summary:{listed, imported, duplicate, failed, titles}}` |
| POST | `/api/mail/fetch-new` | 要認証 | **新しいメールの読み込みを裏で始める**（2026-10-09・[ext/comm/mail/fetch_job.go](../../ext/comm/mail/fetch_job.go)・入力なし）。利用者:「新しいメールの読み込みは、ページを閉じても継続するように」。**要求と切り離した文脈で**、受信と送信の箱を、新しいメールが尽きるまで50通ずつ取り込む（1回分は `/api/mail/import` と同じ `ImportMessages`・全体の上限30分）。**利用者ごとに1つ**。動いていれば始めずに、その様子を返す。サインインしていなければ 409。応答は `{success, started, status}`。画面は通信箱の「📥 新しいメールを読み込む」。⚠ 様子はサーバーの覚えだけ。**サーバーを入れ替えると、動いていた読み込みはそこで止まる**（押し直せば続き・重複は弾く） |
| GET | `/api/mail/fetch-new/status` | 要認証 | **読み込みの様子**（`{success, status}`。一度も始めていなければ `status` は null）。`status` は `{run, running, folder, started_at, finished_at, folders:{受信:{imported, duplicate, failed}, 送信:{…}}, errors, titles}`。`run` は始めるたびに増える番号（画面が自分の始めた回を見分ける）。画面は2秒おきに見て、開いたときに動いていれば続きを見る |
| POST | `/api/mail/send` | 要認証 | **返信・新規送信**（SMTP＋OAuth2）。要求は `{source_page_id?, to, cc, subject, body, attachments?, purpose?, page_id?, draft_id?, skip_generated?}`。`purpose`・`page_id` は、用件とその元のページ（2026-09-30）。用件によって、送る直前の仕事（部材発注書なら PDF を作って添える）と、送れたあとの仕事（発注済みにする）が走る。`draft_id` は、下書きから送るとき。送れたら、下書きはごみ箱へ移る。`skip_generated` は、送るときに作るファイルを外した印。**添付は w-cms の中にあるものを指す**（`{page_id, file, name}`。手元のディスクから選び直さない）。添付できるのは**読めるページのものだけ**（`page.CanView`）。名前は `SafeAttachmentName` の関門を共用。**合計25MBで断る**（途中で切れて「送ったつもりで届いていない」になるより、送る前に断る）。返信元があれば、その `Message-ID` のタグの値を `In-Reply-To` と `References` に載せる（相手のメールソフトで元のスレッドに並ぶ条件。タグの名前は 2026-10-09 に `メッセージID` から改めた）。**送ってから記録する**。記録に失敗しても `{success, sent:true, record_error}` を返す（送ったものは取り消せないので、出た事実を隠さない）。控えは通信箱へ `向き：送信`・`対応：不要` で立つ。控えには `親ページID`（返信元のページ）も書く。監査記録は `mail.send` |
| GET | `/api/mail/compose` | 要認証 | **送る欄の初期値か、保存した下書き**（2026-09-30・[ext/comm/mail/compose.go](../../ext/comm/mail/compose.go)）。引数は `?purpose=<用件>&page_id=<元のページ>` か `?draft=<下書きのページ>`。用件は、拡張が `comm.RegisterSendPurpose` で登録する（返信・新規・部材発注書 など）。応答は `{success, draft, ready}`。`draft` は `{purpose, page_id, draft_id?, to, cc, subject, body, attachments, notes, send_note?, reload?, generated?}`。`ready` は、いま送れるか（サインイン済みか）。知らない用件は400。読めない元のページ・下書きは404 |
| POST | `/api/mail/draft` | 要認証（書き換えは下書きのページの write・書き込みの関門） | **下書きを作る・書き換える**（2026-09-30・同上）。要求は、送る欄の中身に `{draft_id, purpose, page_id}` を足したもの。`draft_id` が空なら、通信箱に新しい下書きのページを作る。**書き換えられるのは、`下書き` のタグのあるページだけ**（この口で、任意のページを上書きさせない）。テンプレートが無ければ409。通信箱が無ければ409。応答は `{success, draft_id}`。監査記録は `mail.draft` |
| POST | `/api/mail/attach` | 要認証（下書きのページの write・書き込みの関門） | **パソコンのファイルを、下書きに添える**（2026-10-04・[ext/comm/mail/compose_attach.go](../../ext/comm/mail/compose_attach.go)。「💻 パソコンから添付」）。multipart で `draft_id`・`file`。置き場は、下書きのページの添付。守りは添付の口と同じ（上限 `max_upload_mib`・送れる種類だけ・名前の検査 `SafeAttachmentName`・中身の検査 `GuardUploadContent`）。下書きでないページは409。応答は `{success, page_id, file, name}`（送る欄の添付の組と同じ形） |
| GET | `/api/replies` | 要認証 | **この記録への返信を逆引きする**（2026-09-03。`?page_id=X`。通信 [ext/comm/handler_replies.go](../../ext/comm/handler_replies.go)。**`-tags minimal` で外れる**）。引くのは、`PagesByTag(comm.ParentPageTag, X)`＝`PagesByTag("親ページID", X)` のうち `向き`＝`送信` のもの。**こちらが送った返信**（w-cms から送った控えと、Outlook から送って取り込んだもの）が出る（2026-10-09。それまでは w-cms の控えだけが持つ `返信元` を引いていた）。応答は `{success, replies:[{page_id, title, sent_at, to}]}`（`to` は `宛先` タグの**畳んだ値＝素のアドレス**。畳めていなければ生の値）。読めないページは黙って落ちる（見せ分けC案）。対象ページを読めなければ**404**（読めないと存在しないを区別させない） |
| GET | `/api/thread` | 要認証 | **やりとりの前後へ移る**（2026-09-14。`?page_id=X`）。鎖は**参照タグ `親ページID`**（2026-10-09。それまではメールヘッダの `In-Reply-To` の鎖）。索引を引くだけ（新しいテーブルも仕掛けも無い）。前（親）＝X の `親ページID` が指すページ、次（子）＝`親ページID` が X のページ。応答は `{success, prev, next}`。`prev` は1件か `null`、`next` は配列（空でも返る）。各件は `{page_id, title, when, direction}`。`when` は `受信日時`・`送信日時`・`発信日時` のうち**そのページに先に現れたもの**1つ（向きに応じて片方しか書かれないので、どれでも構わない）。`direction` は `向き` タグ。**前は高々1件**（`親ページID` は親を1つしか指さない）。**`/api/replies` も、同じ `親ページID` を引く**（2026-10-09 から。それまでは別の鎖だった）。違いは、あちらが `向き`＝`送信` の子だけを返すこと。こちらは向きを問わないので、**受信どうしの返り**（お客様が自分の前のメールに返信した等）も繋がる。送信の記録にも `親ページID` は書かれる（`ext/comm/mail/reply.go`。Message-ID の無い FAX・電話・メモの記録への返信も）ので、両方が同じ鎖に乗る。認可と404の扱いは `/api/replies` と同じ。正本は [ext/comm/handler_thread.go](../../ext/comm/handler_thread.go)（**2026-09-15 に `internal/cms` から移設**。ルートも `ext/comm` の `Routes()` から生えるので `-tags minimal` で外れる） |
| GET | `/api/mails` | 要認証 | **メールの一覧**（2026-09-29・画面は [assets/mails.html](../../assets/mails.html)「✉️ メール一覧」）。**読める**メールの記録（`チャネル`＝`メール`）を全部、新しい順に返す。応答は `{success, mails:[{page_id, title, direction, when, from[], to[], cc[], message_id, in_reply_to, parent, attachments, handled}]}`。`parent` は `親ページID`（2026-10-09）。`when` は向きに応じて `受信日時` か `送信日時`。`handled` は `対応`〔空は未処理〕。**返信の鎖（スレッド）は画面が組む**。`parent` が指すメールの下に字下げする（`/api/thread` が1件ずつ辿るのと同じ鎖を、全部まとめて・2026-10-09 までは `in_reply_to` の鎖）。絞り込み（件名・相手・向き・期間・未処理）も画面。索引は `cms.TagRowsNamed` で**先に読み切ってから** `page.CanView` で絞る（読めない記録は黙って落ちる）。正本は [ext/comm/handler_mail_list.go](../../ext/comm/handler_mail_list.go)・番人は `TestMailListReturnsBothDirectionsAndChain` と `e2e/verify-mail-list.js` |
| GET | `/api/qr` | **認証不要**（`OptionalAuth`。ただし対象ページを読めなければ404） | **そのページのURLのQRを返す**（2026-09-10。`?page_id=X`・**SVG**）。エンコーダは自前で[qr.go](../../internal/cms/qr.go)、**外部依存ゼロ**。URLは要求のホストから組むので、社内（ホスト名）と社外（公開名）で**中身が変わります**。だから `Cache-Control: no-store`。取り違えると「社外の人に社内URLのQRを見せる」が起きます。SVGは中でスクリプトが動けるので、添付と同じく**この応答だけ何も動かせなく**します（`default-src 'none'; sandbox` ＋ `nosniff`）。⚠ **QRは他所のライブラリと同じ絵になりません**（詰め草とマスク選択が実装ごとに違う。3つとも違う絵を出してどれも読める）。**絵の一致を正しさの尺度にしないこと**。見るのは実際に読めるかで、`python tools/qr_verify.py` が OpenCV で復号して確かめます |
| GET | `/api/analyzed` | 要認証（対象ページの read） | **解析済みの印を逆引きする**（2026-09-06。`?page_id=X`・拡張 `ext/toho`）。応答は `{success, analyzed:{<添付ID>:{page_id, title, kind}}, zip_pdfs}`（`zip_pdfs` は「ZIP の添付ID → 中のPDF」。ZIP の中の PDF をまとめて解析するため・2026-09-30）。**印は保存していません**。解析が書いた `受信元` タグの逆引きです。だから**間違った解析をゴミ箱へ入れれば印も消え**、押し直せます（状態を別に持たない）。認可と404の扱いは `/api/replies` と同じ |
| POST | `/api/reorder` | 要認証（**親ページ**の write） | **子ページの並べ替え**（2026-09-03。`?parent=P`・本文 `{"order":["000123","000456",…]}`）。送るのは**その親の子の並び全部**です。「どこへ落としたか」を送ると、サーバーが前後関係を推測することになるためです。並び順キーはサイドカーの `sort_key` が正本で、値は**ゼロ詰め10桁**（`ReorderKey`）。桁を固定するのは、文字列として比べても数の順になるようにするためです（固定しないと 9 と 10 が逆になる）。応答は `{success, changed}`。子ページの並びは「並び順キー → 題 → ID」（`sortChildren`）なので、**キーが空でも題で並びます**。年・月フォルダは何もしなくても正しい |
| GET | `/api/required-materials` | 要認証（対象ページの read） | **プラグイン提供API**。部材手配計算（[ext/toho/materials.go](../../ext/toho/materials.go) の `RouteProvider`）。集計本体は `RequiredMaterials(user, pageID)` で、計算ビューのサーバー事前描画と共用する（応答が呼び出しごとに変わらないよう**部材名順**にソート）。集計対象ページの read だけでは足りず、**部材の定義元ページを読めない相手にはその定義を混ぜない**。品番は本文へ自由に書けるので、自分のページに1行置くだけで、読めない部品定義ページの部材名・仕入先・原価を引けていたため（2026-08-21 修正） |
| POST | `/api/contacts/register` | 要認証（新規は `連絡帳` ページ——無ければトップ——の write／既存へ足すときはその相手ページの write） | **メールから拾った相手をページにする**（アドレス帳・2026-09-05）。**2026-09-15 から拡張 [ext/comm/contacts](../../ext/comm/contacts/) の `Routes()` 提供**（`-tags minimal` で外れる）。要求は `{name?, page_id?, relation, person_name?, addresses:[…], domains?:[…]}`。**行き先の組織は3段で決まる**（2026-09-17）。① `page_id` で名指し。② `name` の**題の完全一致**で既にある組織（画面の組織コンボボックスに無い名前を打っても会社を2枚にしない・`PartnerByTitle`）。③ 無ければ `連絡帳` の下に新しく作る（題は `NormalizeNameForIngest` で畳む。本文は `取引：<relation>`＋`ドメイン`×n、担当者が無ければ `メールアドレス`×n）。`person_name` があれば**組織の直下の人のページ**へアドレスを入れる（無ければ作る）。`relation` は**任意**（2026-09-17。空なら `取引` のタグを書かない。表＝`顧客`／`仕入先`／`自社` に無い値は400）。`relation` も `domains` も、**これを送る画面はもうありません**。どちらも組織の性質なので、必要になったとき組織のページでタグを1行書きます。`domains` は組織へ `ドメイン` タグを足す。**2026-09-17 からこれを送る画面はありません**（未登録の連絡先の行にあったチェックを外した。役に立つのは「新しい組織＋専用ドメイン」の1場面だけなのに、既定で入っていて共有ドメインを組織に結びやすかったため）。口は意図して残してある。`name` が `個人` なら**取引は人に付き、ドメインは付けない**。このとき、担当者が無ければ400。応答 `{success, page_id, title, added, domains_added, merged, created}`（`page_id` はアドレスが入ったページ＝人が居れば人）。**`editlock.RefuseWhileEditing` を通す**（§3 の書き込みの関門。誰かがロックを握って開いていれば409）。監査記録 `contact.register`／`contact.add-addresses` |
| POST | `/api/contacts/unfile` | 要認証（対象ページの write） | **分類を取り消して未分類へ戻す**（2026-09-13 利用者:「間違えてアドレスを分類した場合、どうやって未分類に戻しますか？」。同じく `ext/comm/contacts` 提供）。要求は `{page_id, address}`。やることは**`メールアドレス` タグを1つ外すだけ**。未登録の一覧は「どこにもそのタグが無いアドレス」という索引からの派生なので、外せば自動的に戻る（状態を別に持たない）。対象は `連絡帳` の下のページだけ（よそのページのタグを消せる道を増やさない）。**ページは消さない**。空になったことだけ `{success, page_id, address, remaining, children, empty}` で伝え、消すかは人が決める。そのアドレスが無ければ **404**。`RefuseWhileEditing` を通す。監査記録 `contact.unfile` |
| GET | `/api/contacts/addresses` | 要認証 | **送る欄の宛先・CC の候補**（2026-10-03・[ext/comm/contacts/address_book.go](../../ext/comm/contacts/address_book.go)。同じく `ext/comm/contacts` 提供）。引数なし。連絡帳のアドレスを、組織・名前・アドレスの順に並べて返す。応答は `{success, entries:[{name, org, address}]}`（`name` は人の題。組織のページ自身のアドレスなら空）。空でも `[]`。読むだけ |
| POST | `/api/intake/memo` | 要認証（**通信箱ページの write**） | **手で記録を作る**（2026-09-05。通信 [ext/comm/handler_memo.go](../../ext/comm/handler_memo.go)。**`-tags minimal` で外れる**）。要求は `{channel, direction?, title?, phone?, counterpart?}`。通信箱／年／月の下に記録ページを1枚作る。チャネルは表引きで閉じる（メール／FAX／電話／メモ。自由記入だと `電話` と `TEL` が混ざるため。定数 `comm.ChannelMail`／`ChannelFax`／`ChannelPhone`／`ChannelMemo`）。**向きは電話・FAX・メールが持ち、メモは持たない**（`comm.DirectionTag` と `DirectionIn`／`DirectionOut`）。日時は向きに応じて `受信日時`（`comm.ReceivedAtTag`）か **`発信日時`（`comm.SentOutAtTag`。人が発信した印で、機械が投函した `送信日時`＝`SentAtTag` とは別の欄）**の**片方だけ**。`phone`・`counterpart` は発信（`tel:` の発信ボタン）から来る。監査記録は `intake.memo` |
| POST | `/api/intake/eml` | 要認証（**通信箱ページの write**） | **`.eml` を通信記録にする**（2026-09-30。通信 [ext/comm/intake.go](../../ext/comm/intake.go) の `IntakeEMLAPIHandler`。**`-tags minimal` で外れる**）。フォーム欄 `file` に `.eml`（それ以外は 400）。使い手は道具 `tools/mail/push` と試験。**画面から通信箱へ落としても、もう記録は生まれない**（2026-09-30 利用者:「通信箱ページのファイルをドロップすると子ページが作られる機能はもはや必要ないでしょう」）。芯はメールの取り込みと同じ `IntakeFile`。通信箱の本文は変えないので編集ロックは見ない。応答は `{success, intake:true, page_id, title}`。**重複検知**（2026-09-02）: `.eml` の Message-ID（タグ `Message-ID`。2026-10-09 に `メッセージID` から改めた）を持つページを索引から逆引きし（`ExistingIntakePage`→`PagesByTag`）、あれば**作らずに** `{success, intake:true, duplicate:true, title, page_id?}`（`page_id` は読める相手にだけ。監査記録 `intake.duplicate`）。取り込みでは Gemini を呼ばない（`/api/analyze-attachment`） |
| POST | `/api/intake/handled` | 要認証（**各ページの write**・行ごと） | 通信記録に**`対応` のタグを付ける**（2026-09-05。通信 [ext/comm/handler_handled.go](../../ext/comm/handler_handled.go)。**`-tags minimal` で外れる**。タグ名は `comm.HandledTag`）。要求は `{page_ids:[…], value}`。値は `済`（次の作業へ割り振った。**責任はそちらへ移る**）か `不要`（何も生まれない）。省略時は `済`。**これが「済んだ」を決める唯一の印**。向きでも子ページの有無でも決めない。未処理の一覧から1クリックで片付けるための口。付けるのは**通信の語彙だけ**で、「見積依頼にする」「受注にする」は業種の語彙なので `ext/toho` の仕事。**編集トークンは要らない**（一覧画面のボタンなので持てない）が、**ロックを握られているページは飛ばす**（§3 の書き込みの関門。2026-09-14。判定は `editlock.Locks.EditorOpen`）。2026-10-12 から画面はロックを握らないので、人が画面で開いているだけでは飛ばさない。**1件の権限不足で全体を止めない**（応答は `{success, handled, failed}`。飛ばした分は `failed` に数える）。監査記録は `intake.handled` |

> ⚠ **ページを作る口は、テンプレートを写します**（2026-09-27 夕・テンプレート駆動の D）。
>
> 当てはまる口は、次のとおりです。
>
> - `/api/analyze-attachment`（受注ページ・加工製品）
> - `/api/our-order/new`（部材発注書）
> - `/api/intake/memo`（通信記録（手入力））
> - `/api/contacts/register`（連絡帳の組織・人）
> - `/api/mail/send`（送信の控え）
> - `/api/mail/draft`（メールの下書き）
> - `/api/rfq/new`・`/api/rfq/import`（部材見積依頼書）
> - `/api/estimate/add`・`/api/estimate/from-folder`・`/api/estimate/import`（見積書）
> - 通信箱への `.eml` の取り込み
>
> **テンプレートが無い・値を入れる器（表・見出しの節）が無いときは 409** で断り、ページを作りません。
>
> **確かめるのは作る前**です。
> 解析は **Gemini に聞く前**、部材発注書は仮のページを作る前、**メールは送る前**です（控えを組めないなら、送りません）。
>
> どのテンプレートが要るかは、`GET /api/admin/pages` の `templates` と、管理画面で分かります。
> 正本は、[page_template.go](../../internal/cms/page_template.go)・[【考察】テンプレート駆動.md](../考察/【考察】テンプレート駆動.md) §4a です。

プラグインは、`RouteProvider` を実装すると、ルートを追加できます。
`main.go` は、`cms.PluginRoutes()` をループして登録するだけです。コア側の変更は要りません
（[【ガイド】プラグイン開発.md](【ガイド】プラグイン開発.md) §3）。

**上の表のうち、次の口は、すべてこの経路**で入ります。`main.go` のルート表には現れません（冒頭の内訳表）。

- `/api/mail/*`・`/api/contacts/*`・`/api/replies`・`/api/thread`・`/api/mails`・`/api/intake/*`・
  `/api/record-makers`・`/api/record-make`
- `/api/analyze-attachment`・`/api/analyzed`・`/api/compare-drawings`・`/api/filing-*`・`/api/file-drawings`・
  `/api/product-folder`・`/api/drawing/supersede`・`/api/required-materials`・`/api/order-item`・
  `/api/order-pdf`・`/api/order-pdf-docs`・`/api/material-search`・`/api/our-order/*`・
  `/api/estimate-pdf`・`/api/estimate/*`・`/api/rfq-pdf`・`/api/rfq-pdf-docs`・
  `/api/rfq/*`・`/api/rfq-reply/*`・`/api/table/append-rows`

上の表のうち、コアの口は、`/api/tag-schema`・`/api/upload-*`・`/api/suggest`・`/api/file-ref`・
`/api/local-edit/*`・`/api/zip-list`・`/api/qr`・`/api/reorder` です。
`/api/suggest` は、**候補の出どころ**を、拡張が差し込みます。

## 8. 静的配信

| パス | 認可 | 概要 |
|---|---|---|
| `/assets/...` | 認証不要 | 殻の markup・CSS・JS（`index.html`・`app.css`/`app.js`・`boot.js`・`admin.*`・`login.css`・**`public.html`/`public.css`**）。**ディレクトリ一覧は無効**（`noDirListing`）。`Cache-Control: no-cache`＝毎回再検証（変わっていなければ304）。CSP strict 化（`'unsafe-inline'` 無し）により、スクリプト・スタイルはすべてここに置く |

`/data/...` は、静的配信ではありません。認可付きのハンドラです（§2）。

## 9. クローラ向け（2026-08-26）

クローラは、検索エンジンなどの、サイトを巡回して読むプログラムです。

| メソッド | パス | 認可 | 概要 |
|---|---|---|---|
| GET | `/sitemap.xml` | 認証不要 | **実効公開のページだけ**を載せた sitemap（`<loc>` は絶対URL・`<lastmod>` は W3C Datetime）。判定は認可と同じ `page.EffectivePublic` を通す。独自の判定を書くと、認可とずれた瞬間に非公開ページのアドレスを外へ配ることになるため。`public, max-age=600` |
| GET | `/robots.txt` | 認証不要 | **サイト全体が非公開なら `Disallow: /`**（トップの実効公開で判定。パスゲートにより「サイトが閉じているか」と同義。索引が使えないときも閉じている扱い＝フェイルクローズ）。公開サイトでは `/api/` と `/login` を閉じ、`Sitemap:` 行で sitemap を案内する。添付（`/data/`）は本文の画像がここから配られるので閉じない |

絶対URLの基底（もとになる部分）は、`WCMS_BASE_URL`（[【要求】運用.md](../【要求】運用.md) §3）を最優先します。
無ければ、リクエストから組み立てます。

`X-Forwarded-Proto`/`-Host` は、**前段がプロキシのときだけ**採用します。
無条件に信じると、外から表記を操作できるためです。
監査記録の接続元と同じ規則で、判定は `auth.IsFromTrustedProxy` の1関数が行います。
