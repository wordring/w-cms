package toho

import (
	"encoding/json"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// TestAnalyzeSectionListsEveryAttachment は、記録の箱の「🤖 解析」が、本文にどう置いたかによらず、記録に置いた添付を全部並べることを
// 固定します（2026-10-21 夜・【考察】通信箱の改善 §4.1）。
//
// > 2026-10-21 利用者:「どの方法で作られたとしても解析ボタンが必要です。そしてメモのように人が書き込んで作るものは、人がPDFを挿入する場合があります。それにも解析ボタンが必要になります。」
//
//   - 本文にリンクの無い PDF（人がファイル表示の印で入れた）と画像（FAX・写真）にも、ボタンが出る
//   - 解析できない添付（.eml など）は並べない
//   - 下書きには出さない
func TestAnalyzeSectionListsEveryAttachment(t *testing.T) {
	const id = "000012"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	_, pdf, err := cms.SaveAttachmentFrom(id, "alice", "注文書.pdf", "upload", []byte("%PDF-1.4 fake"))
	if err != nil {
		t.Fatal(err)
	}
	_, img, err := cms.SaveAttachmentFrom(id, "alice", "FAX.png", "upload", []byte("\x89PNG\r\n\x1a\nfake"))
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := cms.SaveAttachmentFrom(id, "alice", "原本.eml", "mail:x", []byte("From: a")); err != nil {
		t.Fatal(err)
	}
	// 本文は、メモのように人が書いたもの（添付へのリンクは無い——PDF はファイル表示の印）。
	writeBodyFile(t, 12, `<h1>メモ</h1><section data-type="file-view" data-ref="`+id+`-`+strings.TrimSuffix(pdf, ".pdf")+`"></section>`)

	got := analyzeSectionHTML(&auth.User{Username: "alice"}, 12, false)
	for _, want := range []string{`data-file="` + pdf + `"`, `data-file="` + img + `"`, "注文書.pdf", "FAX.png", "🤖 解析（添付）"} {
		if !strings.Contains(got, want) {
			t.Errorf("⚠ 🤖 解析に %q がありません:\n%s", want, got)
		}
	}
	if strings.Contains(got, "原本.eml") {
		t.Errorf("⚠ 解析できない添付を並べています:\n%s", got)
	}
	if analyzeSectionHTML(&auth.User{Username: "alice"}, 12, true) != "" {
		t.Errorf("⚠ 下書きに 🤖 解析を出しています")
	}
}

// TestAnalyzeImageAttachment は、画像の添付も解析できることを固定します（判定は画像の入口 judgeOrderImage を通る）。
func TestAnalyzeImageAttachment(t *testing.T) {
	const id = "000012"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	putAttachment(t, id, "fax001.png", []byte("\x89PNG\r\n\x1a\nfake"))
	var gotMIME string
	orig := judgeOrderImage
	judgeOrderImage = func(mime string, data []byte) (*orderJudgment, error) {
		gotMIME = mime
		return sampleJudgment, nil
	}
	t.Cleanup(func() { judgeOrderImage = orig })
	stubJudge(t, func([]byte) (*orderJudgment, error) {
		t.Errorf("⚠ 画像を PDF の入口で判定しています")
		return sampleJudgment, nil
	})

	rr := postAnalyze(t, &auth.User{Username: "alice"}, map[string]string{"page_id": id, "file": "fax001.png"})
	if rr.Code != 200 {
		t.Fatalf("画像を解析できません: code=%d body=%s", rr.Code, rr.Body.String())
	}
	var res struct {
		Success       bool `json:"success"`
		IsClientOrder bool `json:"is_client_order"`
	}
	json.Unmarshal(rr.Body.Bytes(), &res)
	if !res.Success || !res.IsClientOrder || gotMIME != "image/png" {
		t.Errorf("⚠ 画像の解析の結果が違います: %+v（MIME %q）body=%s", res, gotMIME, rr.Body.String())
	}
}
