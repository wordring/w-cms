package main

import (
	"log"
	"net/http"
	"os"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
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
	// データベースを初期化（コアテーブル: pages / page_tags）
	if err := database.InitDB(); err != nil {
		log.Fatalf("DB初期化エラー: %v", err)
	}
	defer database.DB.Close()

	// プラグインのテーブルを作成する（各ユースケース固有のテーブル）
	if err := cms.ApplySchema(database.DB); err != nil {
		log.Fatalf("プラグインスキーマ作成エラー: %v", err)
	}

	// DBが空でファイル（data/master）が存在する場合は自動再構築する。
	// バックアップからファイルだけ復元して起動した場合の復旧フック。
	if err := cms.RebuildIfEmpty(); err != nil {
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
	cms.StartLockReaper()

	// --- ルーティング ---
	// 保護対象のAPI（要認証）。/api/ 配下にまとめ、RequireAuth でまとめて包む。
	// 匿名でも閲覧しうるルート（ページ本文・添付配信・/api/me・ページの殻）は別扱い（後述の OptionalAuth）。
	protected := http.NewServeMux()

	protected.HandleFunc("/api/save", cms.SaveAPIHandler)
	protected.HandleFunc("/api/upload-pdf", cms.UploadPDFHandler)
	protected.HandleFunc("/api/parse-pdf", cms.ParsePDFHandler)
	protected.HandleFunc("/api/new-page", cms.NewPageAPIHandler)
	protected.HandleFunc("/api/validate-parent", cms.ValidateParentAPIHandler)
	protected.HandleFunc("/api/set-parent", cms.SetParentAPIHandler)

	// 同時編集の悲観ロック（ページ単位・競合トリガー方式・SSEプッシュ）
	protected.HandleFunc("/api/lock", cms.LockAPIHandler)
	protected.HandleFunc("/api/lock-events", cms.LockEventsAPIHandler)
	protected.HandleFunc("/api/unlock", cms.UnlockAPIHandler)
	protected.HandleFunc("/api/lock/force", cms.LockForceAPIHandler)

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
	root.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(noDirListing{http.Dir("assets")})))
	root.HandleFunc("/login", auth.LoginPageHandler)
	root.HandleFunc("/api/login", auth.LoginAPIHandler)

	// カスタム要素の語彙（各プラグインの Tags() の集約）。エディタが本文の
	// シリアライズに使う。語彙は秘密ではないので認証不要。
	root.HandleFunc("/api/tag-schema", cms.TagSchemaAPIHandler)

	// 匿名でも閲覧しうるルート（OptionalAuth）。認可は各ハンドラが実効公開で個別判定する。
	//   - /api/load     : ページ本文（匿名でも実効公開なら200、非公開は401）
	//   - /data/        : 添付（PDF原本など。同上）
	//   - /api/me       : 認証状態（未認証は {authenticated:false}）
	//   - /api/children : 子ページ一覧（匿名には実効公開の子だけを絞って返す）
	//   - /api/page-meta: ページ属性（親ページIDなど。匿名には実効公開のときだけ返す）
	root.Handle("/api/load", auth.OptionalAuth(http.HandlerFunc(cms.LoadAPIHandler)))
	root.Handle("/data/", auth.OptionalAuth(http.HandlerFunc(cms.DataFileHandler)))
	root.Handle("/api/me", auth.OptionalAuth(http.HandlerFunc(auth.MeAPIHandler)))
	root.Handle("/api/children", auth.OptionalAuth(http.HandlerFunc(cms.ChildPagesAPIHandler)))
	root.Handle("/api/page-meta", auth.OptionalAuth(http.HandlerFunc(cms.PageMetaAPIHandler)))

	// 要認証のAPI群（/api/ 配下のうち上記の例外を除く全て）。
	root.Handle("/api/", auth.RequireAuth(protected))

	// ページの殻（assets/index.html を返すだけの静的シェル）は匿名にも返す。
	// 実際の本文・属性は JS が /api/load 等を叩いて取得し、そこで認可される。
	root.Handle("/", auth.OptionalAuth(http.HandlerFunc(cms.RootHandler)))

	// CSRF対策（状態変更系のオリジン検証）と CSP（Content-Security-Policy）を
	// 全体に適用する。CSP は最外周に置き、全レスポンスへヘッダを付与する。
	handler := auth.CSPProtect(auth.CSRFProtect(root))

	// サーバーの起動
	log.Println("w-cms 起動: http://localhost:8080")
	if err := http.ListenAndServe(":8080", handler); err != nil {
		log.Fatalf("サーバー終了: %v", err)
	}
}
