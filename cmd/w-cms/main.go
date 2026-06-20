package main

import (
	"log"
	"net/http"

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

	// ルーティングの設定
	mux := http.NewServeMux()
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("assets"))))
	mux.Handle("/data/", http.StripPrefix("/data/", http.FileServer(http.Dir("data"))))

	// コアAPI
	mux.HandleFunc("/api/save", cms.SaveAPIHandler)
	mux.HandleFunc("/api/load", cms.LoadAPIHandler)
	mux.HandleFunc("/api/upload-pdf", cms.UploadPDFHandler)
	mux.HandleFunc("/api/parse-pdf", cms.ParsePDFHandler)

	mux.HandleFunc("/api/new-page", cms.NewPageAPIHandler)
	mux.HandleFunc("/api/children", cms.ChildPagesAPIHandler)
	mux.HandleFunc("/api/rebuild-db", cms.RebuildDBAPIHandler)
	mux.HandleFunc("/upload", cms.UploadHandler)

	// プラグインが提供するAPI（例: /api/required-materials）を登録する
	for _, route := range cms.PluginRoutes() {
		mux.HandleFunc(route.Pattern, route.Handler)
		log.Printf("プラグインAPI登録: %s", route.Pattern)
	}

	// ルート（Wiki型ルーティング）は最後に登録する
	mux.HandleFunc("/", cms.RootHandler)

	// サーバーの起動
	log.Println("w-cms 起動: http://localhost:8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatalf("サーバー終了: %v", err)
	}
}
