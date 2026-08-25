package main

import (
	"log"
	"net/http"
	"os"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// noDirListing はディレクトリ一覧の出力を抑止する http.FileSystem です。
// http.FileServer は index.html を持たないディレクトリにアクセスされると中身を
// 一覧表示してしまい、匿名ユーザーに配置ファイルが見えてしまう（例: /assets/templates/）。
// ディレクトリを開こうとしたら「存在しない」として扱い、404 を返させる。
type noDirListing struct{ fs http.FileSystem }

func (n noDirListing) Open(name string) (http.File, error) {
	f, err := n.fs.Open(name)
	if err != nil {
		return nil, err
	}
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return nil, err
	}
	if info.IsDir() {
		f.Close()
		return nil, os.ErrNotExist
	}
	return f, nil
}

// main はアプリケーションの起動とルーティングの設定を行います。
func main() {
	// データベースを初期化（コアテーブル: pages / page_perms）
	// page_tags は plugin_page_tags.go 側のテーブルでコアではない。
	if err := database.InitDB(); err != nil {
		log.Fatalf("DB初期化エラー: %v", err)
	}
	defer database.DB.Close()

	// 既存DBの定義が現在の宣言とずれていないか先に見る。ApplySchema は
	// CREATE TABLE IF NOT EXISTS を流すだけで、**既に在るテーブルの定義変更は
	// 反映されない**ため、放っておくと起動は成功して保存だけが 500 になる。
	// cms.db は data/master から再生成できる派生索引なので、作り直すのが正しい。
	drifted := cms.DriftedSchemaTables(database.DB)

	// プラグインのテーブルを作成する（各ユースケース固有のテーブル）
	if err := cms.ApplySchema(database.DB); err != nil {
		log.Fatalf("プラグインスキーマ作成エラー: %v", err)
	}

	if len(drifted) > 0 {
		log.Printf("テーブル定義の変更を検出しました（%v）。派生索引を再構築します。", drifted)
		if err := cms.RebuildDatabase(); err != nil {
			log.Fatalf("スキーマ変更に伴う再構築でエラー: %v", err)
		}
	}

	// DBが空でファイル（data/master）が存在する場合は自動再構築する。
	// バックアップからファイルだけ復元して起動した場合の復旧フック。
	if err := cms.RebuildIfNeeded(); err != nil {
		log.Printf("起動時の自動再構築でエラー: %v", err)
	}

	// 認証用DB（data/auth.db）を初期化し、初期管理者をブートストラップする。
	if err := database.InitAuthDB(); err != nil {
		log.Fatalf("認証DB初期化エラー: %v", err)
	}
	defer database.AuthDB.Close()
	if err := auth.BootstrapAdmin(); err != nil {
		log.Fatalf("初期管理者の作成エラー: %v", err)
	}

	// 編集ロックの猶予満了などを定期評価するバックグラウンド処理を起動する。
	editlock.StartLockReaper()

	handler := buildHandler()

	// サーバーの起動
	log.Println("w-cms 起動: http://localhost:8080")
	if err := http.ListenAndServe(":8080", handler); err != nil {
		log.Fatalf("サーバー終了: %v", err)
	}
}

// buildHandler はルート表を組み立て、CSRF と CSP のミドルウェアで包んだ最終的な
// ハンドラを返します。
//
// main から切り出してあるのは、**ルートごとの保護レベルをテストで固定するため**です
// （route_guard_test.go）。ここは「黙って壊れる」層で、ハンドラを protected から root へ
// 移す・OptionalAuth を付け忘れる・ミドルウェアの入れ子を外す、といった退行が起きても
// 既存のテストは全部 green のまま実害だけが出ます。CSP はポリシー文字列を csp_test.go が
// 固定していますが、それは**配線されていること**までは見ていません。
func buildHandler() http.Handler {
	// --- ルーティング ---
	// 保護対象のAPI（要認証）。/api/ 配下にまとめ、RequireAuth でまとめて包む。
	// 匿名でも閲覧しうるルート（ページ本文・添付配信・/api/me・ページの殻）は別扱い（後述の OptionalAuth）。
	protected := http.NewServeMux()

	protected.HandleFunc("/api/save", cms.SaveAPIHandler)
	// 1ブロックだけの保存。data-id が無い本文や構造変更では使えないため、
	// クライアントは 409 等で /api/save（全文保存）へフォールバックする。
	protected.HandleFunc("/api/save-block", cms.SaveBlockAPIHandler)
	protected.HandleFunc("/api/upload-pdf", cms.UploadPDFHandler)
	// 画像の添付（png/jpeg/webp/gif/svg。中身の検証とEXIF除去はハンドラ内。要件 §2.6）
	protected.HandleFunc("/api/upload-image", cms.UploadImageHandler)
	protected.HandleFunc("/api/parse-pdf", cms.ParsePDFHandler)
	protected.HandleFunc("/api/new-page", cms.NewPageAPIHandler)
	// テンプレート選択メニューの中身（「テンプレート」フォルダ配下のツリー）
	protected.HandleFunc("/api/templates", cms.TemplatesAPIHandler)
	protected.HandleFunc("/api/validate-parent", cms.ValidateParentAPIHandler)
	protected.HandleFunc("/api/set-parent", cms.SetParentAPIHandler)
	// 削除は物理削除でなく data/trash への移動（取り消せることが要件。handler_delete.go）
	protected.HandleFunc("/api/delete-page", cms.DeletePageAPIHandler)

	// 同時編集の悲観ロック（ページ単位・競合トリガー方式・SSEプッシュ）
	protected.HandleFunc("/api/lock", editlock.LockAPIHandler)
	protected.HandleFunc("/api/lock-events", editlock.LockEventsAPIHandler)
	protected.HandleFunc("/api/unlock", editlock.UnlockAPIHandler)
	protected.HandleFunc("/api/lock/force", editlock.LockForceAPIHandler)

	// 保存済み文書の版管理（リビジョン／リバート。version.go）。
	// 版は本文そのものなので、一覧・取得は本文と同じ read、書き戻しは write ＋編集ロック。
	protected.HandleFunc("/api/versions", cms.VersionsAPIHandler)
	protected.HandleFunc("/api/version", cms.VersionAPIHandler)
	protected.HandleFunc("/api/revert", cms.RevertAPIHandler)

	protected.HandleFunc("/api/rebuild-db", cms.RebuildDBAPIHandler)
	protected.HandleFunc("/api/logout", auth.LogoutAPIHandler)

	// 権限管理（owner/admin）
	protected.HandleFunc("/api/page-perms", cms.PagePermsHandler)
	protected.HandleFunc("/api/page-chown", cms.PageChownHandler)

	// 管理API（admin限定）
	protected.HandleFunc("/api/admin/users", auth.UsersAPIHandler)
	protected.HandleFunc("/api/admin/users/password", auth.UserPasswordAPIHandler)
	protected.HandleFunc("/api/admin/users/disable", auth.UserDisableAPIHandler)
	protected.HandleFunc("/api/admin/groups", auth.GroupsAPIHandler)
	protected.HandleFunc("/api/admin/groups/members", auth.GroupMembersAPIHandler)
	protected.HandleFunc("/api/admin/audit", auth.AuditAPIHandler)

	// プラグインが提供するAPI（例: /api/required-materials）を登録する
	for _, route := range cms.PluginRoutes() {
		protected.HandleFunc(route.Pattern, route.Handler)
		log.Printf("プラグインAPI登録: %s", route.Pattern)
	}

	// 公開ルート（認証不要）とトップレベルのルーティング。
	root := http.NewServeMux()
	// /assets/ は Cache-Control 無指定だとブラウザのヒューリスティックキャッシュが効き、
	// デプロイ後も古いフロント（app.js 等）が残りうる（2026-08-05 監査の指摘）。
	// no-cache は「使う前に毎回再検証」——FileServer が Last-Modified を返すので、
	// 変わっていなければ 304 で済み、変わっていれば即座に新しい版が届く。
	assets := http.StripPrefix("/assets/", http.FileServer(noDirListing{http.Dir("assets")}))
	root.Handle("/assets/", http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Cache-Control", "no-cache")
		assets.ServeHTTP(w, r)
	}))
	root.HandleFunc("/login", auth.LoginPageHandler)
	root.HandleFunc("/api/login", auth.LoginAPIHandler)

	// クローラ向けの2本（要件定義書 §4.4）。載せるのは実効公開のページだけで、
	// サイト全体が非公開なら robots.txt が全面拒否を返す（業務インスタンスは
	// 匿名に何も見せない運用なので、クローラにも入らせない）。
	// 認証は要らない——中身は公開情報しか含まないため。
	root.HandleFunc("/sitemap.xml", cms.SitemapHandler)
	root.HandleFunc("/robots.txt", cms.RobotsHandler)

	// 本文で扱えるHTMLの語彙（構造HTML＋data-* マーカー＋レジストリの形式宣言）。エディタが本文の
	// シリアライズに使う。語彙は秘密ではないので認証不要。
	root.HandleFunc("/api/tag-schema", cms.TagSchemaAPIHandler)

	// 匿名でも閲覧しうるルート（OptionalAuth）。認可は各ハンドラが実効公開で個別判定する。
	//   - /api/load     : ページ本文（匿名でも実効公開なら200、非公開は401）
	//   - /data/        : 添付（PDF原本など。同上）
	//   - /api/me       : 認証状態（未認証は {authenticated:false}）
	//   - /api/children : 子ページ一覧（匿名には実効公開の子だけを絞って返す）
	//   - /api/page-meta: ページ属性（親ページIDなど。匿名には実効公開のときだけ返す）
	root.Handle("/api/load", auth.OptionalAuth(http.HandlerFunc(cms.LoadAPIHandler)))
	root.Handle("/data/", auth.OptionalAuth(http.HandlerFunc(page.DataFileHandler)))
	root.Handle("/api/me", auth.OptionalAuth(http.HandlerFunc(auth.MeAPIHandler)))
	root.Handle("/api/children", auth.OptionalAuth(http.HandlerFunc(cms.ChildPagesAPIHandler)))
	root.Handle("/api/page-meta", auth.OptionalAuth(http.HandlerFunc(cms.PageMetaAPIHandler)))

	// 要認証のAPI群（/api/ 配下のうち上記の例外を除く全て）。
	root.Handle("/api/", auth.RequireAuth(protected))

	// ページ本体。RootHandler が assets/index.html へ本文とタイトルを埋め込んだ
	// 完成HTMLを返す（サーバー合成。初期表示で /api/load は叩かない）。
	// 認可はハンドラ内で行い、権限無し=403・匿名×非公開=/login へ302・不存在=404 を返す。
	root.Handle("/", auth.OptionalAuth(http.HandlerFunc(cms.RootHandler)))

	// CSRF対策（状態変更系のオリジン検証）と CSP（Content-Security-Policy）を
	// 全体に適用する。CSP は最外周に置き、全レスポンスへヘッダを付与する。
	handler := auth.CSPProtect(auth.CSRFProtect(root))

	return handler
}
