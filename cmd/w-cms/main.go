package main

import (
	"log"
	"net/http"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/database"
)

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

	// --- ルーティング ---
	// 保護対象のルート（要認証）。RootHandler や各APIをここに登録する。
	protected := http.NewServeMux()
	// /data 配下（PDF原本など）はページのread権限を確認して配信する
	protected.HandleFunc("/data/", cms.DataFileHandler)

	protected.HandleFunc("/api/save", cms.SaveAPIHandler)
	protected.HandleFunc("/api/load", cms.LoadAPIHandler)
	protected.HandleFunc("/api/upload-pdf", cms.UploadPDFHandler)
	protected.HandleFunc("/api/parse-pdf", cms.ParsePDFHandler)
	protected.HandleFunc("/api/new-page", cms.NewPageAPIHandler)
	protected.HandleFunc("/api/children", cms.ChildPagesAPIHandler)
	protected.HandleFunc("/api/rebuild-db", cms.RebuildDBAPIHandler)
	protected.HandleFunc("/api/logout", auth.LogoutAPIHandler)
	protected.HandleFunc("/api/me", auth.MeAPIHandler)
	protected.HandleFunc("/upload", cms.UploadHandler)

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

	// ルート（Wiki型ルーティング）は最後に登録する
	protected.HandleFunc("/", cms.RootHandler)

	// 公開ルート（認証不要）。ログイン関連とフロントの静的アセット。
	root := http.NewServeMux()
	root.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("assets"))))
	root.HandleFunc("/login", auth.LoginPageHandler)
	root.HandleFunc("/api/login", auth.LoginAPIHandler)
	// 上記以外はすべて認証必須の protected へ。
	root.Handle("/", auth.RequireAuth(protected))

	// CSRF対策（状態変更系のオリジン検証）を全体に適用する。
	handler := auth.CSRFProtect(root)

	// サーバーの起動
	log.Println("w-cms 起動: http://localhost:8080")
	if err := http.ListenAndServe(":8080", handler); err != nil {
		log.Fatalf("サーバー終了: %v", err)
	}
}
