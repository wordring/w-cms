package cms

// ─────────────────────────────────────────────────────────────────────────
// Gemini 呼び出しのコア側インフラ（2026-09-01）
//
// 「Gemini はコア側インフラ、プロンプト＝解釈はプラグインの持ち物」
// （docs/【考察】通信記録処理.md §3.2）。
//
// 呼び出しの型（キーの取得・クライアント生成・応答テキストの合成・フェンス剥がし）を
// ここに1本化し、プロンプトは呼ぶ側（ParsePDFHandler・analyze_pdf.go）が持ちます。
//
// ⚠ 運用条件: **有料枠のキーで運用すること**。無料枠では入力データが製品改善に
// 使われるため、「学習に使用されない」という前提は課金が有効なキーであることが条件
// （docs/【ガイド】デプロイ・運用.md）。コードからは枠を判定できません。
// ─────────────────────────────────────────────────────────────────────────

import (
	"context"
	"errors"
	"os"
	"strings"
	"time"

	"github.com/google/generative-ai-go/genai"
	"google.golang.org/api/option"
)

// ErrNoGeminiKey は GEMINI_API_KEY 未設定の印です。呼ぶ側が API の失敗と区別して
// 「設定を促す」応答を返せるよう、独立したエラー値にしてあります。
var ErrNoGeminiKey = errors.New("GEMINI_API_KEY が設定されていません")

// geminiModelName は全呼び出しで共有するモデル名です。
const geminiModelName = "gemini-3.5-flash"

// GeminiGenerate はプロンプトと添付データ（PDF等）を Gemini へ渡し、
// 応答テキストを返します。
func GeminiGenerate(prompt string, blob genai.Blob) (string, error) {
	apiKey := os.Getenv("GEMINI_API_KEY")
	if apiKey == "" {
		return "", ErrNoGeminiKey
	}
	// アップロード応答の中から同期で呼ぶ経路があるため、無限に待たない。
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	client, err := genai.NewClient(ctx, option.WithAPIKey(apiKey))
	if err != nil {
		return "", err
	}
	defer client.Close()

	resp, err := client.GenerativeModel(geminiModelName).GenerateContent(ctx, blob, genai.Text(prompt))
	if err != nil {
		return "", err
	}
	var out strings.Builder
	if len(resp.Candidates) > 0 {
		for _, part := range resp.Candidates[0].Content.Parts {
			if t, ok := part.(genai.Text); ok {
				out.WriteString(string(t))
			}
		}
	}
	return out.String(), nil
}

// StripJSONFence は「JSONのみを出力」と指示しても付いてくることがある
// マークダウンのコードブロック修飾（```json … ```）を剥がします。
func StripJSONFence(s string) string {
	s = strings.TrimSpace(s)
	if strings.HasPrefix(s, "```") {
		s = strings.TrimPrefix(s, "```json")
		s = strings.TrimPrefix(s, "```")
		s = strings.TrimSuffix(s, "```")
		s = strings.TrimSpace(s)
	}
	return s
}
