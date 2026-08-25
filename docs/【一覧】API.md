# 【一覧】API

w-cms が提供するHTTPエンドポイントの**実装済みリファレンス**。ルーティングの正本は
[cmd/w-cms/main.go](../cmd/w-cms/main.go) で、本ファイルはその鏡です。

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

さらに横断で2つのミドルウェアが最外周に掛かる（`main.go`）。

- **`CSRFProtect`**: GET/HEAD/OPTIONS 以外は Origin（無ければ Referer）とホストの一致を要求。
- **`CSPProtect`**: 全レスポンスに Content-Security-Policy を付与。2026-08-19 の移行第4段で
  **strict 版**（`script-src 'self'; style-src 'self'`＝`'unsafe-inline'` なし）へ格上げ済み
  （[本文サニタイズ設計.md §6](本文サニタイズ設計.md)）。

ここは**黙って壊れる層**です——ハンドラを `protected` から `root` へ移す・`OptionalAuth` を
付け忘れる・ミドルウェアの入れ子を外す、といった退行が起きても既存のテストは green のまま
実害だけが出ます。そのため `buildHandler()` を `main` から切り出し、ルートごとの保護レベルと
上記2つの配線を [route_guard_test.go](../cmd/w-cms/route_guard_test.go) が固定しています
（`csp_test.go` はポリシー文字列を見るだけで、配線までは見ていない）。

## 2. ページ本文・属性

| メソッド | パス | 認可 | 編集ロック | 概要 |
|---|---|---|---|---|
| GET | `/{id}` | 任意認証 | — | **ページ本体**。`assets/index.html` に本文とタイトルを埋め込んだ完成HTMLを返す（サーバー合成・`RenderPageShell`）。本文はサニタイズ後に**計算ビューの中身が埋められる**（`RenderComputedViews`。下記の注記）。権限無し=403／匿名×非公開=`/login` へ302／不存在=404 |
| GET | `/api/load` | 任意認証（read） | — | ページ本文（`text/plain`）。初期表示では使わず、**編集ロック起点の載せ替え専用**。**描画時と同じくサニタイズを通し**、計算ビューの中身を埋めて返す。**ページ内アンカーの合成（`RenderAnchors`）は通さない**——合成 id がエディタのDOMへ入ると本文として保存されるため（下記の注記） |
| POST | `/api/save` | 要認証（write） | 要 | 本文全体を保存。サニタイズ結果と `sanitized`、レジストリ未定義の `data-type` の告知 `unknown_types`、見出しの改名で計算に読まれなくなった項目の告知 `unresolved_fields`、殻の接頭辞を剥がした id の告知 `stripped_ids` を返す（下記の注記）。JSONボディは**8MiB上限**（超過は413。JSONを受けるAPIは共通） |
| POST | `/api/save-block` | 要認証（write） | 要 | `data-id` で指定した**1ブロックだけ**保存。対象が無い／重複なら **409**（クライアントは全文保存へフォールバック）。応答は `/api/save` と同形（`unknown_types`・`unresolved_fields`・`stripped_ids` は当該ブロック分のみ） |
| GET | `/api/page-meta` | 任意認証（read） | — | ページ属性（親ページID・親ページ名・更新日時など）。匿名には実効公開のときだけ返す |
| GET | `/api/children` | 任意認証（親のread） | — | 子ページ一覧（ID昇順）。認証済みには read 権限のある子、匿名には**実効公開の子だけ**を絞って返す（`visibleChildren`。計算ビューのサーバー事前描画と共用） |
| POST | `/api/delete-page` | 要認証（write） | 要 | ページを**ゴミ箱（`data/trash`）へ移し**、索引から取り除く。物理削除ではない（取り消せることが要件）。**トップページは400**／**子ページを持つと409**（件数つき）／他者ロック中は409。応答は `{success, page_id, trash_path}` |
| POST | `/api/new-page` | 要認証（親のwrite） | — | 子ページを作成し `/{新ID}?edit=true` へ302。**POST限定**（GETは405）——GETで通ると本文へ `<img src="/api/new-page?parent=…">` を1つ保存するだけで、開いた全ログインユーザーにページを作らせられる（同一オリジンなので SameSite も CSP も止めない。`method_guard_test.go` が固定）。引数は `r.FormValue` なのでPOSTボディでもクエリでも受ける。**親の指定は必須**（親なしにできるのはトップページ `000000` のみ）。`template=<ページID>` を付けるとそのテンプレートの本文を写して**新規化**する（空欄を列型の既定値で埋める。日付は今日・`order-no` は再採番）。テンプレートに使えるのは「テンプレート」フォルダ配下の**葉**だけで、分類フォルダとルートは400。テンプレートの**read権限が要る**。検証は**採番より前**に行う（ファイルの無いページ行を残さないため） |
| GET | `/api/templates` | 要認証 | — | テンプレート選択メニューの中身。「テンプレート」フォルダ（トップ直下・同名）配下のツリーを `[{id,title,children}]` で返す。**枝は分類・葉がテンプレート**。ルートが無ければ `[]`（従来どおり空のページだけが作られる）。read 権限で絞られる（`visibleChildren`） |
| GET | `/api/validate-parent` | 要認証（write） | — | 親付け替えの事前検証（循環・自己参照・存在チェック）。`/api/set-parent` と同じ `validateParentChange` を共有する。**現在フロントからは呼ばれていない**（`applyParent()` は `/api/set-parent` の応答だけで判定する） |
| POST | `/api/set-parent` | 要認証（write） | 要 | 親ページの付け替え |
| GET | `/data/...` | 任意認証（read） | — | 添付ファイル配信（PDF原本など）。ページのread権限を要求する保護ハンドラ |

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

ページの状態を変えるAPIは**同じロックで直列化**される（計7本）。ただし入口は2種類ある。

- **保存2本**（`/api/save`・`/api/save-block`）は `editlock.Locks.Validate` を直接呼び、
  トークンは**JSONボディの `token`** で受ける。
- **残り5本**（`/api/set-parent`・`/api/page-perms`・`/api/page-chown`・`/api/delete-page`・
  `/api/upload-pdf`）は共通ゲート `editlock.RequireEditLock` を通り、トークンは
  **`X-Lock-Token` ヘッダ**（無ければ `token` クエリ）で受ける。フロントはこの5本を
  `lockedFetch`（または同じヘッダを手で付けた `fetch`）で送る。

検証規約はどちらも同じ（ロック無し＝許可／保持者本人でトークン一致なら許可／それ以外は409）。

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
| GET | `/api/admin/audit` | 監査ログの参照（書き込み・権限変更を記録）。直近200件 |
| POST | `/api/rebuild-db` | `data/master` から `cms.db` を再構築（派生インデックスの洗い替え） |

> 移行期にあった `POST /api/migrate-vocab`（旧カスタム要素→語彙モデルの一括変換）は
> **撤去済み**（2026-08-20。両環境の変換完了を確認したうえで `migrate_vocab.go` ごと削除。
> 管理コンソールのボタンも無い）。経緯は [変更履歴.md](変更履歴.md) 2026-08-20 の節。

## 7. 語彙・PDF・プラグイン

| メソッド | パス | 認可 | 概要 |
|---|---|---|---|
| GET | `/api/tag-schema` | 認証不要 | **本文の語彙**。`elements`（構造HTML → 許可属性。`data-*` マーカーもここに属性として現れる。**カスタム要素はゼロ**）・`void`（終了タグを書かない要素）・`block_id`（`data-id`）・`vocab`（語彙レジストリ12形式の定義を `type` 順で。スラッシュメニューと挿入骨格の生成元）・`type_inference`（語→型の推論辞書。エディタの型検証がサーバーの索引と同じ辞書を使うための配布）を返す。エディタのシリアライザが従う正本（[本文サニタイズ設計.md](本文サニタイズ設計.md) §7） |
| POST | `/api/upload-pdf` | 要認証（対象ページの write） | PDFのアップロード（`data/master/<先頭2桁>/<id>/` へ保存）。**編集ロックが要る**——添付は同名を無条件で上書きし、リビジョンもゴミ箱も無い（＝復元できない）ため本文編集と同じロックで直列化する。**受け入れは `.pdf` のみ・先頭 `%PDF-` 必須・32MiB上限・パス要素と本文/サイドカー同名は拒否**（[アーキテクチャとDBスキーマ.md](アーキテクチャとDBスキーマ.md) §5.1） |
| POST | `/api/parse-pdf` | 要認証（対象ページの write） | PDFから明細をAI抽出（Gemini）。**ロックは要らない**——永続状態を変えず、結果はDOMへ足すだけ（保存する `/api/save` 側が検証する）。ファイル名は置く側と**同じ関門**（`attachmentFileName`）を通す——ここが `filepath.Base` だけだったころ、本文 `<id>.html` と権限サイドカー `<id>.meta.json` を「PDFとして」外部へ送れた |
| GET | `/api/required-materials` | 要認証（対象ページの read） | **プラグイン提供API**。部材手配計算（`plugin_materials.go` の `RouteProvider`）。集計本体は `RequiredMaterials(user, pageID)` で、計算ビューのサーバー事前描画と共用する（応答が呼び出しごとに変わらないよう**部材名順**にソート）。集計対象ページの read だけでは足りず、**部材の定義元ページを読めない相手にはその定義を混ぜない**——品番は本文へ自由に書けるので、自分のページに1行置くだけで読めない部品定義ページの部材名・仕入先・原価を引けていた（2026-08-21 修正） |

プラグインは `RouteProvider` を実装するとルートを追加できる。`main.go` は
`cms.PluginRoutes()` をループして登録するだけで、コア側の変更は要らない
（[【ガイド】プラグイン開発.md](【ガイド】プラグイン開発.md) §3）。

## 8. 静的配信

| パス | 認可 | 概要 |
|---|---|---|
| `/assets/...` | 認証不要 | 殻の markup・CSS・JS（`index.html`・`app.css`/`app.js`・`boot.js`・`admin.*`・`login.css`）。**ディレクトリ一覧は無効**（`noDirListing`）。`Cache-Control: no-cache`＝毎回再検証（変わっていなければ304）。CSP strict 化（`'unsafe-inline'` 無し）により、スクリプト・スタイルはすべてここに置く |

`/data/...` は静的配信ではなく認可付きハンドラ（§2）。
