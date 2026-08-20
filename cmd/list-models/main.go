// list-models は Gemini で使えるモデル名を一覧するだけの診断ツールです。
// 発注書PDFの明細抽出（internal/cms/pdf_handler.go）でモデル名を選ぶときに使います。
//
//	GEMINI_API_KEY=... go run ./cmd/list-models
//
// もとはリポジトリ直下に package main で置いてあり、ルートに main が2つある状態を
// 作っていました（そのせいで「一時パッケージはサブdirで実行」という回避策が
// 引き継ぎへ恒久手順として書かれていた）。2026-08-21 にここへ移設。
package main

import (
	"context"
	"fmt"
	"log"
	"os"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/iterator"
	"google.golang.org/api/option"
)

func main() {
	ctx := context.Background()
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		log.Fatal("No API key")
	}

	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		log.Fatal(err)
	}
	defer client.Close()

	iter := client.ListModels(ctx)
	for {
		m, err := iter.Next()
		if err == iterator.Done {
			break
		}
		if err != nil {
			log.Fatal(err)
		}
		fmt.Println(m.Name)
	}
}
