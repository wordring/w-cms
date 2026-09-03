package sheetmetal

import (
	"archive/zip"
	"bytes"
	"encoding/json"
	"errors"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// PDF解析ボタン（判定→受注ページ生成・板金部の既定セット）のテスト。
//
// Gemini はネットワークと課金を伴うため呼ばず、判定の入口 judgeOrderPDF を
// 偽物へ差し替える（seam）。固定するのは判定の後ろ側すべて——
// 受注ページの生成（機能見出し形・親子・権限継承）・受信元タグ・ZIP内PDFの
// 取り出し（上限つき）・「発注書ではない」の返答・write 権限の関門。

// stubJudge は judgeOrderPDF を差し替え、テスト後に戻します。
func stubJudge(t *testing.T, f func([]byte) (*orderJudgment, error)) {
	t.Helper()
	orig := judgeOrderPDF
	judgeOrderPDF = f
	t.Cleanup(func() { judgeOrderPDF = orig })
}

// postAnalyze はハンドラを利用者 u で叩きます。
func postAnalyze(t *testing.T, u *auth.User, body map[string]string) *httptest.ResponseRecorder {
	t.Helper()
	b, _ := json.Marshal(body)
	req := httptest.NewRequest("POST", "/api/analyze-attachment", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if u != nil {
		req = auth.WithUser(req, u)
	}
	rr := httptest.NewRecorder()
	AnalyzeAttachmentAPIHandler(rr, req)
	return rr
}

// putAttachment は files/ へ添付を直接置きます。
func putAttachment(t *testing.T, pageID, name string, content []byte) {
	t.Helper()
	dir := page.AttachmentDir(pageID)
	os.MkdirAll(dir, 0755)
	if err := os.WriteFile(filepath.Join(dir, name), content, 0644); err != nil {
		t.Fatalf("添付の設置エラー: %v", err)
	}
}

var sampleJudgment = &orderJudgment{
	IsClientOrder: true,
	OrderNo:       "PO-2026-001",
	Customer:      "トーアスポーツ",
	OrderDate:     "2026-08-25",
	Items: []orderPDFItem{
		{ItemNo: "A-1", ItemName: "ブラケット", Price: "1500", Quantity: "10"},
	},
}

// TestAnalyzePDFCreatesOrderPage は、添付PDFの解析から受注ページ
// （機能見出し形＋受信元タグ・子ページ・権限継承・索引）が生まれることを検証します。
func TestAnalyzePDFCreatesOrderPage(t *testing.T) {
	const id = "000012"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	putAttachment(t, id, "abc123.pdf", []byte("%PDF-1.4 fake"))
	stubJudge(t, func(pdf []byte) (*orderJudgment, error) {
		if !strings.HasPrefix(string(pdf), "%PDF") {
			t.Errorf("判定に渡ったのがPDFの中身ではありません: %q", pdf)
		}
		return sampleJudgment, nil
	})

	rr := postAnalyze(t, &auth.User{Username: "alice"},
		map[string]string{"page_id": id, "file": "abc123.pdf"})
	if rr.Code != 200 {
		t.Fatalf("解析が失敗しました: code=%d body=%s", rr.Code, rr.Body.String())
	}
	var res struct {
		Success       bool   `json:"success"`
		IsClientOrder bool   `json:"is_client_order"`
		PageID        string `json:"page_id"`
		Title         string `json:"title"`
	}
	json.Unmarshal(rr.Body.Bytes(), &res)
	if !res.Success || !res.IsClientOrder || res.Title != "受注 PO-2026-001" {
		t.Fatalf("応答が違います: %+v body=%s", res, rr.Body.String())
	}

	body, err := os.ReadFile(filepath.Join(page.GetPageDir(res.PageID), res.PageID+".html"))
	if err != nil {
		t.Fatalf("受注ページを読めません: %v", err)
	}
	html := string(body)
	for _, want := range []string{
		"<h1>受注 PO-2026-001</h1>",
		"<h2>顧客の発注書</h2>",
		"<dt>発注元</dt><dd>トーアスポーツ</dd>",
		"<td>ブラケット</td>",
		"<td>未着手</td>",
		"<dt>受信元</dt><dd>" + id + "-abc123</dd>",
	} {
		if !strings.Contains(html, want) {
			t.Errorf("受注ページに %q がありません:\n%s", want, html)
		}
	}
	if strings.Contains(html, "元ファイル") {
		t.Error("PDF直接の解析に 元ファイル タグは不要です")
	}

	// 親子と権限。
	meta, ok := page.ReadSidecar(res.PageID)
	if !ok || meta.ParentID != id {
		t.Errorf("親が解析元のページではありません: %+v", meta)
	}
	if meta.Owner != "alice" || meta.Group != "sales" {
		t.Errorf("所有者・グループの継承が違います: %+v", meta)
	}

	// 索引: 機能見出し形が効き、発注書番号で引ける。
	idInt, _ := strconv.Atoi(res.PageID)
	var n int
	if err := database.DB.QueryRow(
		`SELECT COUNT(*) FROM vocab_index
		 WHERE page_id = ? AND data_type = 'client-order' AND field = '発注書番号' AND value = 'PO-2026-001'`,
		idInt).Scan(&n); err != nil || n != 1 {
		t.Errorf("発注書番号が索引にありません (n=%d err=%v)", n, err)
	}
}

// TestAnalyzeZipEntry は、ZIP添付の中のPDFを1件だけ取り出して解析し、
// 元ファイル タグが添えられることを検証します。
func TestAnalyzeZipEntry(t *testing.T) {
	const id = "000012"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Mode: "330"})

	var buf bytes.Buffer
	zw := zip.NewWriter(&buf)
	w, _ := zw.Create("orders/chumon.pdf")
	w.Write([]byte("%PDF-1.4 in zip"))
	w2, _ := zw.Create("readme.txt")
	w2.Write([]byte("x"))
	zw.Close()
	putAttachment(t, id, "zzz9.zip", buf.Bytes())
	stubJudge(t, func(pdf []byte) (*orderJudgment, error) {
		if string(pdf) != "%PDF-1.4 in zip" {
			t.Errorf("ZIPから取り出した中身が違います: %q", pdf)
		}
		return sampleJudgment, nil
	})

	rr := postAnalyze(t, &auth.User{Username: "alice"},
		map[string]string{"page_id": id, "file": "zzz9.zip", "entry": "orders/chumon.pdf"})
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"is_client_order":true`) {
		t.Fatalf("ZIP内PDFの解析が失敗しました: code=%d body=%s", rr.Code, rr.Body.String())
	}
	var res struct {
		PageID string `json:"page_id"`
	}
	json.Unmarshal(rr.Body.Bytes(), &res)
	body, _ := os.ReadFile(filepath.Join(page.GetPageDir(res.PageID), res.PageID+".html"))
	for _, want := range []string{
		"<dt>受信元</dt><dd>" + id + "-zzz9</dd>", // 参照はZIPのリンクブロックへ
		"<dt>元ファイル</dt><dd>orders/chumon.pdf</dd>",
	} {
		if !strings.Contains(string(body), want) {
			t.Errorf("受注ページに %q がありません:\n%s", want, body)
		}
	}

	// ZIP内のPDF以外は拒否。
	rr = postAnalyze(t, &auth.User{Username: "alice"},
		map[string]string{"page_id": id, "file": "zzz9.zip", "entry": "readme.txt"})
	if rr.Code != 400 {
		t.Errorf("ZIP内の非PDFが拒否されません: code=%d", rr.Code)
	}
}

// TestAnalyzeNonOrderCreatesNothing は「発注書ではない」の返答を検証します
// （ページは作らない・エラーでもない）。
func TestAnalyzeNonOrderCreatesNothing(t *testing.T) {
	const id = "000012"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Mode: "330"})
	putAttachment(t, id, "abc123.pdf", []byte("%PDF-1.4 fake"))
	stubJudge(t, func([]byte) (*orderJudgment, error) {
		return &orderJudgment{IsClientOrder: false}, nil
	})

	var before int
	database.DB.QueryRow(`SELECT COUNT(*) FROM pages`).Scan(&before)
	rr := postAnalyze(t, &auth.User{Username: "alice"},
		map[string]string{"page_id": id, "file": "abc123.pdf"})
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), `"is_client_order":false`) {
		t.Fatalf("「発注書ではない」が返りません: code=%d body=%s", rr.Code, rr.Body.String())
	}
	var after int
	database.DB.QueryRow(`SELECT COUNT(*) FROM pages`).Scan(&after)
	if after != before {
		t.Errorf("発注書でないのにページが増えました: %d -> %d", before, after)
	}
}

// TestAnalyzeSurvivesJudgeError は判定失敗（API障害）が生成ゼロの明確な
// エラー応答になることを検証します。
func TestAnalyzeSurvivesJudgeError(t *testing.T) {
	const id = "000012"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Mode: "330"})
	putAttachment(t, id, "abc123.pdf", []byte("%PDF-1.4 fake"))
	stubJudge(t, func([]byte) (*orderJudgment, error) {
		return nil, errors.New("模擬API障害")
	})
	rr := postAnalyze(t, &auth.User{Username: "alice"},
		map[string]string{"page_id": id, "file": "abc123.pdf"})
	if !strings.Contains(rr.Body.String(), `"success":false`) {
		t.Errorf("失敗が伝わりません: %s", rr.Body.String())
	}
}

// TestAnalyzeRequiresWrite は write 権限の関門を検証します
// （子ページを作る操作——読めるだけの人には使わせない）。
func TestAnalyzeRequiresWrite(t *testing.T) {
	const id = "000012"
	// mode 330: owner/group のみ書ける。部外者 bob は read も write も無い。
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	putAttachment(t, id, "abc123.pdf", []byte("%PDF-1.4 fake"))
	stubJudge(t, func([]byte) (*orderJudgment, error) { return sampleJudgment, nil })
	rr := postAnalyze(t, &auth.User{Username: "bob"},
		map[string]string{"page_id": id, "file": "abc123.pdf"})
	if rr.Code != 403 {
		t.Errorf("write 無しで解析できてしまいます: code=%d", rr.Code)
	}
}

// TestBuildOrderPageHTMLEscapes は、抽出値のHTMLエスケープを検証します
// （Gemini の応答は外部入力——サニタイザの後ろ盾はあるが、組み立て側も規律を守る）。
func TestBuildOrderPageHTMLEscapes(t *testing.T) {
	j := &orderJudgment{
		IsClientOrder: true,
		OrderNo:       `<script>alert(1)</script>`,
		Items:         []orderPDFItem{{ItemName: `<img src=x>`}},
	}
	html := buildOrderPageHTML("000090", "abc123", `<b>x</b>.pdf`, j)
	if strings.Contains(html, "<script>") || strings.Contains(html, "<img") || strings.Contains(html, "<b>") {
		t.Errorf("エスケープされていません:\n%s", html)
	}
}

// 図面PDFとDXFの突き合わせのテスト（2026-09-03）。
//
// ユーザー:「PDFにプラスしてDXFがある場合が大部分です。そのため、DXFの図面名称と
// 図面番号をPDFと付き合わせて同じ部品の図面と認識するために解析が必要」。
// 固定するのは、Gemini が返した図面番号で**同じページのDXF添付が選ばれる**ことと、
// **選び過ぎない**こと（番号違い・番号なし）。

// putDXF は表題欄つきのDXF添付を置きます。
func putDXF(t *testing.T, pageID, name, drawingNo, drawingName string) {
	t.Helper()
	body := dxfEntity("TEXT", sjisBytes(t, "図面番号"), 100, 50, 2.5) +
		dxfEntity("TEXT", drawingNo, 100, 45, 2.5) +
		dxfEntity("TEXT", sjisBytes(t, "図面名称"), 100, 40, 2.5) +
		dxfEntity("TEXT", sjisBytes(t, drawingName), 100, 35, 2.5)
	putAttachment(t, pageID, name, []byte(body))
}

var sampleDrawing = &orderJudgment{
	DocType:     "drawing",
	DrawingNo:   "X008-135-4",
	DrawingName: "架台Assy",
}

// TestAnalyzeDrawingMatchesDXF は、図面PDFの解析で部品ページが生まれ、
// 図面番号の一致したDXFだけが参照タグで結ばれることを検証します。
func TestAnalyzeDrawingMatchesDXF(t *testing.T) {
	const id = "000031"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	putAttachment(t, id, "pdf001.pdf", []byte("%PDF-1.4 fake"))
	putDXF(t, id, "dxf001.dxf", "X008-135-4", "架台Assy") // 一致
	putDXF(t, id, "dxf002.dxf", "W240-001", "本体(前）")    // 別の部品——選ばれてはいけない
	stubJudge(t, func([]byte) (*orderJudgment, error) { return sampleDrawing, nil })

	rr := postAnalyze(t, &auth.User{Username: "alice", IsAdmin: true},
		map[string]string{"page_id": id, "file": "pdf001.pdf"})
	if rr.Code != 200 {
		t.Fatalf("解析が失敗しました: %d %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		PageID     string `json:"page_id"`
		Title      string `json:"title"`
		MatchedDXF int    `json:"matched_dxf"`
		DocType    string `json:"doc_type"`
	}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.DocType != "drawing" {
		t.Errorf("図面として扱われていません: %+v", resp)
	}
	if resp.MatchedDXF != 1 {
		t.Errorf("一致したDXFの数が違います: %d（別部品まで拾っていないか）", resp.MatchedDXF)
	}
	if resp.Title != "X008-135-4 架台Assy" {
		t.Errorf("部品ページの題が違います: %q", resp.Title)
	}

	body := readPageBody(t, resp.PageID)
	if !strings.Contains(body, "<dt>対応DXF</dt><dd>"+id+"-dxf001</dd>") {
		t.Errorf("一致したDXFへの参照タグがありません: %s", body)
	}
	if strings.Contains(body, "dxf002") {
		t.Errorf("番号の違うDXFまで結ばれています: %s", body)
	}
	if !strings.Contains(body, "<dt>受信元</dt><dd>"+id+"-pdf001</dd>") {
		t.Errorf("由来（PDF）への参照タグがありません: %s", body)
	}
}

// TestAnalyzeDrawingWithoutNumberMatchesNothing は、図面番号が読めなかったときに
// **何とも結ばない**ことを固定します。
//
// 空文字どうしを一致とみなすと、表題欄が未記入のDXF（構想図など）が無関係な図面へ
// 全部ぶら下がります——実データに未記入の構想図が実在します。
func TestAnalyzeDrawingWithoutNumberMatchesNothing(t *testing.T) {
	const id = "000032"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	putAttachment(t, id, "pdf001.pdf", []byte("%PDF-1.4 fake"))
	putDXF(t, id, "dxf001.dxf", "", "") // 表題欄が未記入
	stubJudge(t, func([]byte) (*orderJudgment, error) {
		return &orderJudgment{DocType: "drawing", DrawingName: "構想図"}, nil
	})

	rr := postAnalyze(t, &auth.User{Username: "alice", IsAdmin: true},
		map[string]string{"page_id": id, "file": "pdf001.pdf"})
	if rr.Code != 200 {
		t.Fatalf("解析が失敗しました: %d %s", rr.Code, rr.Body.String())
	}
	var resp struct {
		MatchedDXF int `json:"matched_dxf"`
	}
	json.Unmarshal(rr.Body.Bytes(), &resp)
	if resp.MatchedDXF != 0 {
		t.Errorf("番号が無いのに結んでいます: %d件", resp.MatchedDXF)
	}
}

// TestNormalizeDrawingNoFoldsOnlyForComparison は、突き合わせのときだけ揺れを畳むこと、
// **表示は畳まない**ことを固定します（タグの値は見た目のままDBへ入る、が原則）。
func TestNormalizeDrawingNoFoldsOnlyForComparison(t *testing.T) {
	same := [][2]string{
		{"X008-135-4", "x008-135-4"},           // 英字の大小
		{"X008-135-4", "X008ー135ー4"},           // 全角ハイフン・長音
		{"X008-135-4", "X008-135-4 "},          // 前後の空白
		{"X008-135-4", "Ｘ００８-135-4"},           // 全角英数
		{"W470-L090-01-09", "W470_L090-01-09"}, // アンダースコア
	}
	for _, p := range same {
		if normalizeDrawingNo(p[0]) != normalizeDrawingNo(p[1]) {
			t.Errorf("同じ番号が一致しません: %q と %q", p[0], p[1])
		}
	}
	if normalizeDrawingNo("X008-135-4") == normalizeDrawingNo("X008-135-5") {
		t.Errorf("違う番号が一致しています")
	}
}

// readPageBody はページの本文HTMLを読みます。
func readPageBody(t *testing.T, pageID string) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(page.GetPageDir(pageID), pageID+".html"))
	if err != nil {
		t.Fatalf("ページを読めません: %v", err)
	}
	return string(b)
}
