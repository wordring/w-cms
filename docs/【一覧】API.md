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
- **`CSPProtect`**: 全レスポンスに Content-Security-Policy を付与（[【考察】CSP強化.md](【考察】CSP強化.md)）。

## 2. ページ本文・属性

| メソッド | パス | 認可 | 編集ロック | 概要 |
|---|---|---|---|---|
| GET | `/{id}` | 任意認証 | — | **ページ本体**。`assets/index.html` に本文とタイトルを埋め込んだ完成HTMLを返す（サーバー合成・`RenderPageShell`）。権限無し=403／匿名×非公開=`/login` へ302／不存在=404 |
| GET | `/api/load` | 任意認証（read） | — | ページ本文の**生HTML**（`text/plain`）。初期表示では使わず、**編集ロック起点の載せ替え専用** |
| POST | `/api/save` | 要認証（write） | 要 | 本文全体を保存。サニタイズ結果と `sanitized`、レジストリ未定義の `data-type` の告知 `unknown_types` を返す。JSONボディは**8MiB上限**（超過は413。JSONを受けるAPIは共通） |
| POST | `/api/save-block` | 要認証（write） | 要 | `data-id` で指定した**1ブロックだけ**保存。対象が無い／重複なら **409**（クライアントは全文保存へフォールバック）。応答は `/api/save` と同形（`unknown_types` は当該ブロック分のみ） |
| GET | `/api/page-meta` | 任意認証（read） | — | ページ属性（親ページID・親ページ名・更新日時など）。匿名には実効公開のときだけ返す |
| GET | `/api/children` | 任意認証（親のread） | — | 子ページ一覧。匿名には**実効公開の子だけ**を絞って返す |
| GET/POST | `/api/new-page` | 要認証（親のwrite） | — | 子ページを作成し `/{新ID}?edit=true` へ302。**親の指定は必須**（親なしにできるのはトップページ `000000` のみ） |
| GET | `/api/validate-parent` | 要認証（write） | — | 親付け替えの事前検証（循環・自己参照・存在チェック） |
| POST | `/api/set-parent` | 要認証（write） | 要 | 親ページの付け替え |
| GET | `/data/...` | 任意認証（read） | — | 添付ファイル配信（PDF原本など）。ページのread権限を要求する保護ハンドラ |

## 3. 編集ロック（同時編集の競合対策）

設計は [【考察】同時編集の競合対策.md](【考察】同時編集の競合対策.md)。

| メソッド | パス | 認可 | 概要 |
|---|---|---|---|
| POST | `/api/lock` | 要認証（write） | ロック取得。成功時は**最新の生HTML**を同梱して返す（編集はここから始まる） |
| GET | `/api/lock-events` | 要認証（write） | ロック状態の **SSE** 購読（保持者・待機者で共用） |
| POST | `/api/unlock` | 要認証 | ロック解放。**write は見ない**（解放できるのはトークンが一致する保持者本人だけ）。タブを閉じるときは `navigator.sendBeacon` で送る |
| POST | `/api/lock/force` | **admin のみ** | ロックの強制解放（保持者が落ちてスタックしたときの救済） |

書き込み系API（`/api/save`・`/api/save-block`・`/api/set-parent`・`/api/page-perms`・
`/api/page-chown`）は**同じロックで直列化**される。ただし入口は2種類ある。

- **保存2本**（`/api/save`・`/api/save-block`）は `editlock.Locks.Validate` を直接呼び、
  トークンは**JSONボディの `token`** で受ける。
- **残り3本**（`/api/set-parent`・`/api/page-perms`・`/api/page-chown`）は共通ゲート
  `editlock.RequireEditLock` を通り、トークンは **`X-Lock-Token` ヘッダ**（無ければ `token` クエリ）
  で受ける。フロントはこの3本を `lockedFetch` で送る。

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
| POST | `/api/logout` | 要認証 | ログアウト（セッション行を削除） |
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
| GET | `/api/admin/audit` | 監査ログの参照（書き込み・権限変更を記録） |
| POST | `/api/rebuild-db` | `data/master` から `cms.db` を再構築（派生インデックスの洗い替え） |

## 7. 語彙・PDF・プラグイン

| メソッド | パス | 認可 | 概要 |
|---|---|---|---|
| GET | `/api/tag-schema` | 認証不要 | **本文の語彙**。`elements`（構造HTML ∪ カスタム要素 → 許可属性）・`void`（終了タグを書かない要素）・`block_id`（`data-id`）・`vocab`（語彙レジストリの形式定義。スラッシュメニューと挿入骨格の生成元）・`type_inference`（語→型の推論辞書。エディタの型検証がサーバーの索引と同じ辞書を使うための配布）を返す。エディタのシリアライザが従う正本（[本文サニタイズ設計.md](本文サニタイズ設計.md) §7） |
| POST | `/api/upload-pdf` | 要認証（対象ページの write） | PDFのアップロード（`data/master/<先頭2桁>/<id>/` へ保存）。**受け入れは `.pdf` のみ・先頭 `%PDF-` 必須・32MiB上限・パス要素と本文/サイドカー同名は拒否**（[アーキテクチャとDBスキーマ.md](アーキテクチャとDBスキーマ.md) §5.1） |
| POST | `/api/parse-pdf` | 要認証（対象ページの write） | PDFから明細をAI抽出（Gemini） |
| GET | `/api/required-materials` | 要認証（対象ページの read） | **プラグイン提供API**。部材手配計算（`plugin_materials.go` の `RouteProvider`） |

プラグインは `RouteProvider` を実装するとルートを追加できる。`main.go` は
`cms.PluginRoutes()` をループして登録するだけで、コア側の変更は要らない
（[【ガイド】プラグイン開発.md](【ガイド】プラグイン開発.md) §3）。

## 8. 静的配信

| パス | 認可 | 概要 |
|---|---|---|
| `/assets/...` | 認証不要 | CSS・JS・テンプレート。**ディレクトリ一覧は無効**（`noDirListing`） |

`/data/...` は静的配信ではなく認可付きハンドラ（§2）。
