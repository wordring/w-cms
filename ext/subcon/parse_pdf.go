package subcon

// ─────────────────────────────────────────────────────────────────────────
// PDFから明細を読む——ブロックの中へ差し込む口（2026-09-16 にコアから移設）
//
// ユーザー:「**PDF解析は業務に密着せざるを得ないので、ext/subcon ではないでしょうか？
// さらに立ち入った解析をするようになると、弊社独自の拡張になるやもしれません**」。
//
// **コアに残っていた最後の業務の口**でした。プロンプトが「このPDFは発注書または
// 見積書です」「部品明細（品名、単価、数量）」と業務そのものを語っているのに、
// `internal/cms` に居たので——**素の w-cms（`-tags minimal`）では、口は生きているのに
// 押す手段が無い**という状態でした（ボタンは `File: true` を宣言する形式、つまり
// 下請けの語彙が在るときだけ出るため）。移設で口とボタンの寿命が揃います。
//
// ── `/api/analyze-attachment` との違い（役割が似ているので、混ぜないこと）──
//
//	この口   … **人が開いているブロックの中へ明細を差し込む**。読むだけで、
//	           ページも作らない（差し込んだ結果を保存するかは人が決める）
//	あちら   … **添付が発注書かを判定し、受注ページを1枚作る**（analyze_pdf.go）
//
// どちらも Gemini を人の操作の直後にだけ呼びます（人間ゲート型・§3）。
// 将来「さらに立ち入った解析」へ進むなら、プロンプトが分かれていくのはこちらです
// ——だから**業務の側に置くのが正しい**。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"errors"
	"log"
	"net/http"
	"os"

	"github.com/google/generative-ai-go/genai"

	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// ParsedItem はPDFから抽出された品目データです。
type ParsedItem struct {
	ItemName string `json:"item_name"`
	Price    string `json:"price"`
	Quantity string `json:"quantity"`
}

// ParsePDFHandler は保存されたPDFを Gemini に渡し、JSONとして明細を抽出します。
func ParsePDFHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := cms.GateJSONPost(w, r); !ok {
		return
	}
	var req struct {
		PageID   string `json:"page_id"`
		FileName string `json:"file_name"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	// パスに使う前にゼロ詰め6桁へ正規化する（page.NormalizeID 参照）。
	normID, ok := cms.PageIDOrFail(w, req.PageID)
	if !ok {
		return
	}
	req.PageID = normID
	// PDF解析→明細挿入はページ内容の変更につながるため write 権限を要求する
	if !page.RequirePageWrite(w, r, req.PageID) {
		return
	}

	// 送る前に名前を検証する。ここが filepath.Base だけだったころ、ページ
	// ディレクトリ内の任意のファイル——**本文 <id>.html と権限サイドカー
	// <id>.meta.json を含む**——を「PDFとして」外部（Gemini）へ送れた。
	// 置く側（cms.UploadPDFHandler）と同じ関門（cms.SafeAttachmentName）を通し、
	// 拡張子の許可リストと本文・サイドカーの名指し拒否をそのまま効かせる。
	// ⚠ **移設しても必ず通すこと**——守りはコアの1箇所に在り、口が増えるたびに
	// 「サイドカーを上書きできる穴」が復活しないようにしてあります。
	fileName, err := cms.SafeAttachmentName(req.PageID, req.FileName,
		map[string]bool{".pdf": true}, "PDFファイル（.pdf）のみ解析できます")
	if err != nil {
		cms.JSONFail(w, http.StatusBadRequest, err.Error())
		return
	}

	pdfPath, found := page.AttachmentPath(req.PageID, fileName)
	if !found {
		cms.JSONFail(w, http.StatusNotFound, "PDF file not found on server")
		return
	}
	pdfBytes, err := os.ReadFile(pdfPath)
	if err != nil {
		cms.JSONFail(w, http.StatusBadRequest, "PDFファイルの読み込みに失敗しました")
		return
	}

	prompt := `このPDFは発注書または見積書です。
記載されているすべての部品明細（品名、単価、数量）を抽出し、以下の形式のJSON配列のみを出力してください。
キーは必ず "item_name", "price", "quantity" にしてください。
単価はカンマを除いた数値文字列にしてください。
マークダウンのコードブロック修飾 (例: ` + "```json" + ` ) は付けず、純粋なJSON配列から出力してください。

[
  {"item_name": "部品A", "price": "1000", "quantity": "2"}
]`

	respText, err := cms.GeminiGenerate(prompt, genai.Blob{MIMEType: "application/pdf", Data: pdfBytes})
	if err != nil {
		if errors.Is(err, cms.ErrNoGeminiKey) {
			// APIキーがない場合はフロント側に分かりやすいエラーメッセージを返す
			cms.JSONFail(w, http.StatusServiceUnavailable, "サーバーに GEMINI_API_KEY 環境変数が設定されていません。\nターミナルで設定してから起動してください。\n\n例(Windows): \nset GEMINI_API_KEY=AIzaSy...\ngo run ./cmd/w-cms/")
			return
		}
		log.Printf("[Gemini API Error] %v", err)
		cms.JSONFail(w, http.StatusBadGateway, "Gemini APIの呼び出しに失敗しました: "+err.Error())
		return
	}

	var items []ParsedItem
	err = json.Unmarshal([]byte(cms.StripJSONFence(respText)), &items)
	if err != nil {
		// パース失敗時はエラーではなく空配列を返す（フロント側でダミー追加ロジックが走るため）
		json.NewEncoder(w).Encode(map[string]interface{}{
			"success": true,
			"items":   []ParsedItem{},
			"raw":     respText,
		})
		return
	}

	json.NewEncoder(w).Encode(map[string]interface{}{
		"success": true,
		"items":   items,
		"raw":     respText,
	})
}
