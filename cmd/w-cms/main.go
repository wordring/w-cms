package main

import (
	"log"
	"net/http"

	"w-cms/internal/cms"
	"w-cms/internal/database"
)

// main はアプリケーションの起動とルーティングの設定を行います。
func main() {
	// データベースを初期化
	if err := database.InitDB(); err != nil {
		log.Fatalf("DB初期化エラー: %v", err)
	}
	defer database.DB.Close()

	// ルーティングの設定
	mux := http.NewServeMux()
	mux.HandleFunc("/upload", cms.UploadHandler)
	mux.HandleFunc("/", cms.IndexHandler)

	// サーバーの起動
	log.Println("w-cms 起動: http://localhost:8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatalf("サーバー終了: %v", err)
	}
}
