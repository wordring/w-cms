package sheetmetal

// ─────────────────────────────────────────────────────────────────────────
// PDFの判定→受注ページ生成——ボタン起動（板金部の既定セット・2026-09-01）
//
// 添付PDF（またはZIP添付の中のPDF）を Gemini で判定し、**顧客が発行した発注書**
// なら受注ページをそのページの子として生成します。
//
//	通信記録ページ（親）── 📎 発注書.pdf ／ 📎 図面一式.zip
//	└─ 受注ページ（本APIが生成）
//	     <h1>受注 PO-xxx</h1>
//	     <section><h2>顧客の発注書</h2> ヘッダ dl ＋ 明細 table（機能見出し形・D-2）
//	     <dl data-type="tags"> 受信元: <ページID>-<添付ID>（押すと該当ブロックへ飛ぶ）
//	                            元ファイル: <ZIP内のパス>（ZIP経由のときだけ）
//
// **起動は人の指先だけ**——「自動ではなくボタンのclickなどで解析が始まると良い」
// （2026-09-01 ユーザー決定）。当初は .eml 到着時の自動判定（取り込み観察係）として
// 作ったが同日ボタン起動へ一本化した。§3 の全体方針「人間ゲート型の取り込み」への
// 回帰であり、Gemini の呼び出し（コスト・誤判定）は常に人の操作の直後にだけ起きる。
// 誤生成の取り消しはページ削除（§2.7④ 可逆性——通信記録は不変で残っている）。
//
// このファイルは他社デプロイでは外す・差し替える前提の既定セットです
// （docs/【考察】通信記録処理.md §3.2。Gemini はコア側インフラ gemini.go、
// プロンプト＝解釈はこのセットの持ち物）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"archive/zip"
	"encoding/json"
	"errors"
	"fmt"
	"html"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/generative-ai-go/genai"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// orderJudgment は Gemini の判定＋抽出結果です（プロンプトと同じ形）。
type orderJudgment struct {
	IsClientOrder bool `json:"is_client_order"`
	// **図面PDFの枝**（2026-09-03）——添付DXFとの突き合わせに使う。
	// PDFが図面なら DocType が "drawing" になり、図面番号・図面名称が入る。
	DocType     string         `json:"doc_type"`
	DrawingNo   string         `json:"drawing_no"`
	DrawingName string         `json:"drawing_name"`
	OrderNo     string         `json:"order_no"`
	Customer    string         `json:"customer"`
	OrderDate   string         `json:"order_date"`
	Items       []orderPDFItem `json:"items"`
}

type orderPDFItem struct {
	ItemNo   string `json:"item_no"`
	ItemName string `json:"item_name"`
	Price    string `json:"price"`
	Quantity string `json:"quantity"`
}

// judgeOrderPDF は判定の入口です。テストが偽物へ差し替えられるよう変数にしてあります
// （Gemini はネットワークと課金を伴うため、自動テストでは呼ばない）。
var judgeOrderPDF = judgeOrderPDFWithGemini

// AnalyzeAttachmentAPIHandler は POST /api/analyze-attachment です。
// 入力: {page_id, file, entry?}——file は添付の保存名（.pdf か .zip）、
// entry は ZIP の中のPDFのパス（目録の表示名）。
func AnalyzeAttachmentAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		cms.JSONFail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		File   string `json:"file"`
		Entry  string `json:"entry"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, ok := page.NormalizeID(req.PageID)
	if !ok {
		cms.JSONFail(w, http.StatusBadRequest, "ページIDが不正です")
		return
	}
	// 子ページを作る操作なので write 権限を要求する（本文は変えないので編集ロックは不要
	// ——取り込みと同じ理屈）。
	if !page.RequirePageWrite(w, r, pageID) {
		return
	}
	fileName, err := cms.SafeAttachmentName(pageID, req.File,
		map[string]bool{".pdf": true, ".zip": true}, "解析できるのは .pdf と .zip の中のPDFだけです")
	if err != nil {
		cms.JSONFail(w, http.StatusBadRequest, err.Error())
		return
	}

	pdf, srcEntry, err := loadPDFForAnalysis(pageID, fileName, req.Entry)
	if err != nil {
		cms.JSONFail(w, http.StatusBadRequest, err.Error())
		return
	}

	j, err := judgeOrderPDF(pdf)
	if err != nil {
		if errors.Is(err, cms.ErrNoGeminiKey) {
			cms.JSONFail(w, 0, "サーバーに GEMINI_API_KEY 環境変数が設定されていません。設定してから起動し直してください。")
			return
		}
		cms.JSONFail(w, 0, "解析に失敗しました: "+err.Error())
		return
	}
	attachIDOf := func() string { return strings.TrimSuffix(fileName, filepath.Ext(fileName)) }

	// **図面PDFの枝**——同じページに付いているDXFと図面番号で突き合わせ、
	// 同じ部品の図面として1枚の部品ページにまとめる（drawing_match.go）。
	if !j.IsClientOrder && j.DocType == "drawing" {
		matches := MatchDXFAttachments(pageID, j.DrawingNo)
		newID, err := cms.CreateChildPage(pageID, auth.CurrentUser(r).Username,
			buildPartPageHTML(pageID, attachIDOf(), srcEntry, j, matches))
		if err != nil {
			cms.JSONFail(w, http.StatusInternalServerError, "部品ページを作れません: "+err.Error())
			return
		}
		auth.Audit(auth.CurrentUser(r).Username, "analyze-drawing",
			newID+" from "+pageID+"/"+fileName+srcEntrySuffix(srcEntry))
		json.NewEncoder(w).Encode(map[string]any{
			"success": true, "is_client_order": false, "doc_type": "drawing",
			"page_id": newID, "title": pageTitleOf(newID),
			"matched_dxf": len(matches),
		})
		return
	}
	if !j.IsClientOrder {
		// 生成しないのも正常な結果——人が押した問いに「発注書ではない」と答える。
		json.NewEncoder(w).Encode(map[string]any{"success": true, "is_client_order": false})
		return
	}

	attachID := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	newID, err := cms.CreateChildPage(pageID, auth.CurrentUser(r).Username,
		buildOrderPageHTML(pageID, attachID, srcEntry, j))
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "受注ページを作れません: "+err.Error())
		return
	}
	auth.Audit(auth.CurrentUser(r).Username, "analyze-pdf", newID+" from "+pageID+"/"+fileName+srcEntrySuffix(srcEntry))

	json.NewEncoder(w).Encode(map[string]any{
		"success": true, "is_client_order": true,
		"page_id": newID, "title": pageTitleOf(newID),
	})
}

// pageTitleOf は索引から題を引きます（引けなければIDをそのまま返す）。
func pageTitleOf(pageID string) string {
	idInt, err := strconv.Atoi(pageID)
	if err != nil {
		return pageID
	}
	var t string
	if err := database.DB.QueryRow(`SELECT title FROM pages WHERE id = ?`, idInt).Scan(&t); err == nil && t != "" {
		return t
	}
	return pageID
}

// srcEntrySuffix は監査の対象表記にZIP内パスを添えます。
func srcEntrySuffix(entry string) string {
	if entry == "" {
		return ""
	}
	return "!" + entry
}

// loadPDFForAnalysis は解析対象のPDFの中身を読みます。
// file が .pdf ならそのまま、.zip なら entry（目録の表示名）で1件だけ取り出す。
// 取り出しには上限を掛ける——ZIPの申告サイズは自己申告なので、実読みも打ち切る
// （小さな入力が巨大に膨らむ細工＝ZIP爆弾への備え）。
func loadPDFForAnalysis(pageID, fileName, entry string) (pdf []byte, srcEntry string, err error) {
	path, found := page.AttachmentPath(pageID, fileName)
	if !found {
		return nil, "", errors.New("添付が見つかりません")
	}

	if strings.ToLower(filepath.Ext(fileName)) == ".pdf" {
		b, err := os.ReadFile(path)
		if err != nil {
			return nil, "", errors.New("PDFを読めません")
		}
		return b, "", nil
	}

	// ZIP の中の1件。
	if strings.ToLower(filepath.Ext(entry)) != ".pdf" {
		return nil, "", errors.New("ZIPの中で解析できるのはPDFだけです")
	}
	zr, err := zip.OpenReader(path)
	if err != nil {
		return nil, "", errors.New("ZIPとして読めません")
	}
	defer zr.Close()
	limit := cms.MaxUploadBytes()
	for _, f := range zr.File {
		if f.FileInfo().IsDir() || cms.DecodeZipName(f.Name, f.NonUTF8) != entry {
			continue
		}
		if f.UncompressedSize64 > uint64(limit) {
			return nil, "", fmt.Errorf("ZIP内のファイルが大きすぎます（上限 %dMiB）", limit>>20)
		}
		rc, err := f.Open()
		if err != nil {
			return nil, "", errors.New("ZIPの中身を開けません")
		}
		defer rc.Close()
		b, err := io.ReadAll(io.LimitReader(rc, limit+1))
		if err != nil {
			return nil, "", errors.New("ZIPの中身を読めません")
		}
		if int64(len(b)) > limit {
			return nil, "", fmt.Errorf("ZIP内のファイルが大きすぎます（上限 %dMiB）", limit>>20)
		}
		return b, entry, nil
	}
	return nil, "", errors.New("ZIPの中に " + entry + " が見つかりません")
}

// judgeOrderPDFWithGemini は判定＋抽出を1コールで行います。
// プロンプト（＝解釈）は本セットの持ち物、呼び出しの型（キー・クライアント・
// フェンス剥がし）はコア（gemini.go）。
func judgeOrderPDFWithGemini(pdf []byte) (*orderJudgment, error) {
	prompt := `このPDFが何の文書かを判定し、種類に応じた項目を抽出してください。
判定する種類は次の3つです:
  - "order"   : 顧客（取引先）が当社宛てに発行した発注書（注文書）
  - "drawing" : 部品や製品の図面（表題欄に図面番号・図面名称があるもの）
  - "other"   : 上記以外（見積書・請求書・納品書・カタログ・案内など）
次の形式のJSONオブジェクトのみを出力してください（マークダウンのコードブロック修飾は付けない）:
{
  "doc_type": "order" または "drawing" または "other",
  "is_client_order": doc_type が "order" のとき true、それ以外は false,
  "order_no": "発注書番号（発注書のとき。記載が無ければ空文字）",
  "customer": "発行元（顧客）の会社名（記載が無ければ空文字）",
  "order_date": "発注日を YYYY-MM-DD 形式で（記載が無ければ空文字）",
  "items": [{"item_no": "品番", "item_name": "品名", "price": "単価（カンマを除いた数値文字列）", "quantity": "数量（数値文字列）"}],
  "drawing_no": "図面番号（図面のとき。表題欄に記載された文字列をそのまま。記載が無ければ空文字）",
  "drawing_name": "図面名称（図面のとき。記載が無ければ空文字）"
}
図面番号は突き合わせに使うので、**表題欄に書かれている文字列をそのまま**返してください
（ハイフンや記号を補ったり省いたりしない）。該当しない項目は空でかまいません。`

	respText, err := cms.GeminiGenerate(prompt, genai.Blob{MIMEType: "application/pdf", Data: pdf})
	if err != nil {
		return nil, err
	}
	var j orderJudgment
	if err := json.Unmarshal([]byte(cms.StripJSONFence(respText)), &j); err != nil {
		return nil, errors.New("応答をJSONとして読めません: " + err.Error())
	}
	return &j, nil
}

// buildOrderPageHTML は受注ページの本文を組みます（機能見出し形・D-2）。
// 形はページテンプレートの受注ページと同じ: ヘッダ dl（発注書番号・発注元・発注日）＋
// 明細 table（品番・品名・単価・数量・状態）。状態は「未着手」で始まる（進捗の起点）。
func buildOrderPageHTML(hostPageID, attachID, srcEntry string, j *orderJudgment) string {
	title := "受注 " + strings.TrimSpace(j.OrderNo)
	if strings.TrimSpace(j.OrderNo) == "" {
		if strings.TrimSpace(j.Customer) != "" {
			title = "受注（" + strings.TrimSpace(j.Customer) + "）"
		} else {
			title = "受注（番号不明）"
		}
	}

	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	b.WriteString("<section><h2>顧客の発注書</h2><dl>")
	writeHeaderPair(&b, "発注書番号", j.OrderNo)
	writeHeaderPair(&b, "発注元", j.Customer)
	writeHeaderPair(&b, "発注日", j.OrderDate)
	b.WriteString("</dl><table><tbody>")
	b.WriteString("<tr><th>品番</th><th>品名</th><th>単価</th><th>数量</th><th>状態</th></tr>")
	for _, it := range j.Items {
		b.WriteString("<tr><td>" + html.EscapeString(it.ItemNo) + "</td>" +
			"<td>" + html.EscapeString(it.ItemName) + "</td>" +
			"<td>" + html.EscapeString(it.Price) + "</td>" +
			"<td>" + html.EscapeString(it.Quantity) + "</td>" +
			"<td>未着手</td></tr>")
	}
	b.WriteString("</tbody></table></section>")
	// 由来参照（§9.1）——値は「元ページID-添付ID」。参照タグの文法（ref_render.go）に
	// 一致するのでリンクとして描画され、押すと元ページの該当ブロックへ飛ぶ。
	// ZIP経由なら中のパスも添える（こちらはただのタグ——参照文法には乗らない）。
	b.WriteString(`<dl data-type="tags"><dt>受信元</dt><dd>` +
		html.EscapeString(hostPageID+"-"+attachID) + "</dd>")
	if srcEntry != "" {
		b.WriteString("<dt>元ファイル</dt><dd>" + html.EscapeString(srcEntry) + "</dd>")
	}
	b.WriteString("</dl>")
	return b.String()
}

// writeHeaderPair はヘッダ dl の1対を書きます（空値は空欄＝あとから人が埋める）。
func writeHeaderPair(b *strings.Builder, name, value string) {
	b.WriteString("<dt>" + html.EscapeString(name) + "</dt>")
	if strings.TrimSpace(value) == "" {
		b.WriteString("<dd><br/></dd>")
	} else {
		b.WriteString("<dd>" + html.EscapeString(value) + "</dd>")
	}
}

// buildPartPageHTML は部品ページの本文を組みます（機能見出し形・D-2）。
//
//	<h1>X008-135-4 架台Assy</h1>
//	<section><h2>図面</h2><dl> 図面番号・図面名称 </dl></section>
//	<dl data-type="tags"> 受信元: <ページID>-<PDFの添付ID>
//	                      対応DXF: <ページID>-<DXFの添付ID>（一致した数だけ繰り返す）
//
// **1通のメールの中でPDFとDXFを対応づけた結果**がこのページです。過去のページを
// 図面番号で探して束ねることはしません——番号は別製品で衝突しうるので、
// 同一性を担うのは常にページID（drawing_match.go 冒頭）。
func buildPartPageHTML(hostPageID, attachID, srcEntry string, j *orderJudgment, matches []matchedDXF) string {
	no, name := strings.TrimSpace(j.DrawingNo), strings.TrimSpace(j.DrawingName)
	// 題は「図面番号 図面名称」——**図面名称は重複しうる**ので番号を先に置く。
	title := strings.TrimSpace(no + " " + name)
	if title == "" {
		title = "図面（番号不明）"
	}

	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	b.WriteString("<section><h2>図面</h2><dl>")
	writeHeaderPair(&b, "図面番号", no)
	writeHeaderPair(&b, "図面名称", name)
	b.WriteString("</dl></section>")

	b.WriteString(`<dl data-type="tags"><dt>受信元</dt><dd>` +
		html.EscapeString(hostPageID+"-"+attachID) + "</dd>")
	if srcEntry != "" {
		b.WriteString("<dt>元ファイル</dt><dd>" + html.EscapeString(srcEntry) + "</dd>")
	}
	// 一致したDXFを参照タグで指す（押すと元の通信記録ページの該当添付へ飛ぶ）。
	// 一致が無ければ何も書かない——**DXFが無いのも普通**（PDFだけの図面）。
	for _, m := range matches {
		b.WriteString("<dt>対応DXF</dt><dd>" +
			html.EscapeString(hostPageID+"-"+m.AttachID) + "</dd>")
	}
	b.WriteString("</dl>")
	return b.String()
}
