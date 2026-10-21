package toho

import (
	"net/http"
	"strings"
	"testing"

	"github.com/google/generative-ai-go/genai"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// TestRFQReplyOCRFromReplyPage は、返事ページから Gemini に読ませる口を固定します（2026-10-21 夜）。
//
// > 2026-10-21 利用者:「部材見積依頼の返事ページからGeminiにOCRを依頼できるようにしてください」
//
//   - 返事ページの欄に「🤖 読む」と候補（このページに置いた PDF）が出る
//   - 読むと「業者の返事（読んだまま）」の表が読んだもので置き換わる（ほかの本文とブロックIDは残る）
//   - 空の仕入先・読んだ部材見積依頼番号は埋める
//   - 候補に無いファイルは読ませない
//   - 返事ページでないページでは断る
func TestRFQReplyOCRFromReplyPage(t *testing.T) {
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 50, 0, "受信メール", "root", "302", true)
	addPage(t, 51, 50, "返事", "root", "302", true)
	mail := `<h1>受信メール</h1><dl data-type="tags"><dt>向き</dt><dd>受信</dd></dl><p>見積を送ります</p>`
	writeBodyFile(t, 50, mail)
	syncBody(t, 50, mail)
	reply := `<h1 data-id="aaaa">【業者】部材見積依頼の返事</h1><dl data-type="tags" data-id="bbbb"><dt>` + SupplierTag + `</dt><dd><br/></dd><dt>` + RFQAnsweredTag +
		`</dt><dd>2026-10-21</dd><dt>` + RFQReplyReadNoTag + `</dt><dd><br/></dd><dt>` + SourceRefTag + `</dt><dd>000050</dd></dl>` +
		`<p data-id="cccc">人のメモ</p><details open data-id="dddd"><summary>` + rfqReplySourceCaption + `</summary><table><tbody><tr><th>品番</th><th>単価</th></tr><tr><td></td><td></td></tr></tbody></table></details>`
	writeBodyFile(t, 51, reply)
	syncBody(t, 51, reply)
	_, stored, err := cms.SaveAttachmentFrom("000051", "root", "返事.pdf", "upload", []byte("%PDF-1.4 fake"))
	if err != nil {
		t.Fatalf("添付を置けません: %v", err)
	}

	u := adminUser()
	view := rfqReplyMatchViewHTML(u, 51)
	if !strings.Contains(view, `class="chip-btn rfq-reply-ocr"`) || !strings.Contains(view, "このページ: 返事.pdf") || !strings.Contains(view, "原本: メールの本文") {
		t.Fatalf("⚠ 返事ページに「🤖 読む」と候補が出ていません:\n%s", view)
	}

	var gotBlobs int
	old := rfqReplyOCR
	rfqReplyOCR = func(prompt string, blobs ...genai.Blob) (string, error) {
		gotBlobs = len(blobs)
		return `{"rfq_no":"001864","supplier":"ふじ鍍金","date":"2026-10-20","source_table":{"headers":["品番","数量","単価"],"rows":[["A100-B01-01","50","2,900"]]}}`, nil
	}
	t.Cleanup(func() { rfqReplyOCR = old })

	if code, out := postRFQ(t, u, RFQReplyOCRAPIHandler, map[string]any{"page_id": "000051", "source_page": "000051", "source_file": stored}); code != http.StatusOK {
		t.Fatalf("読めません: %d %v", code, out)
	}
	if gotBlobs != 1 {
		t.Errorf("⚠ ファイルを Gemini に渡していません（%d）", gotBlobs)
	}
	body := readPageBody(t, "000051")
	for _, want := range []string{"<td>A100-B01-01</td>", "<td>2,900</td>", `data-id="cccc">人のメモ`, "<dd>ふじ鍍金</dd>", "<dd>001864</dd>"} {
		if !strings.Contains(body, want) {
			t.Errorf("⚠ 読んだあとの返事ページに %q がありません:\n%s", want, body)
		}
	}
	if strings.Count(body, rfqReplySourceCaption) != 1 {
		t.Errorf("⚠ 「読んだまま」の枠が2つになっています:\n%s", body)
	}

	if code, _ := postRFQ(t, u, RFQReplyOCRAPIHandler, map[string]any{"page_id": "000051", "source_page": "000050", "source_file": "zz99.pdf"}); code != http.StatusNotFound {
		t.Errorf("⚠ 候補に無いファイルを読ませています（%d）", code)
	}
	if code, _ := postRFQ(t, u, RFQReplyOCRAPIHandler, map[string]any{"page_id": "000050", "source_page": "000050", "source_file": ""}); code != http.StatusBadRequest {
		t.Errorf("⚠ 返事ページでないページで読んでいます（%d）", code)
	}
	// 読む人が書けなければ、欄を出さない。
	if v := rfqReplyOCRPanelHTML(&auth.User{Username: "bob"}, "000051", nil); v != "" {
		t.Errorf("⚠ 書けない人に「🤖 読む」を出しています: %s", v)
	}
}
