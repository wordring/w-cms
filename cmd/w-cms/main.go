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
	mux.Handle("/assets/", http.StripPrefix("/assets/", http.FileServer(http.Dir("assets"))))
	mux.Handle("/data/", http.StripPrefix("/data/", http.FileServer(http.Dir("data"))))
	
	mux.HandleFunc("/api/required-materials", cms.RequiredMaterialsAPIHandler)
	mux.HandleFunc("/api/save", cms.SaveAPIHandler)
	mux.HandleFunc("/api/load", cms.LoadAPIHandler)
	mux.HandleFunc("/api/upload-pdf", cms.UploadPDFHandler)
	mux.HandleFunc("/api/parse-pdf", cms.ParsePDFHandler)
	
	mux.HandleFunc("/upload", cms.UploadHandler)
	mux.HandleFunc("/", cms.IndexHandler)

	// サーバーの起動
	log.Println("w-cms 起動: http://localhost:8080")
	if err := http.ListenAndServe(":8080", mux); err != nil {
		log.Fatalf("サーバー終了: %v", err)
	}
}
