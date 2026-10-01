package toho

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// TestAnalyzeAttachmentRejectsNonPDFName は、🤖解析（外部LLMへの送信）が PDF・Excel 以外のファイルを掴めないことを
// 固定します。
//
// ⚠ もとは `/api/parse-pdf` の番人でした（2026-10-01 に口ごと片付けたので、こちらへ移した）——そちらは file_name を
// filepath.Base で切るだけで拡張子を見ていなかったため、ページディレクトリ内の任意のファイル——**本文 <id>.html と
// 権限サイドカー <id>.meta.json を含む**——を「PDFとして」外部（Gemini）へ送れた。アップロード側は拡張子の許可リストと
// 名指し拒否で守られているのに、送信側だけが素通しという非対称だった。
// ⚠ 守りの本体は**コアの `cms.SafeAttachmentName`** に在ります——自前の名前検査を書き直すと、この非対称がそのまま復活します。
func TestAnalyzeAttachmentRejectsNonPDFName(t *testing.T) {
	const id = "000012"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Mode: "330"})

	// 本文とサイドカーは実在する（setupExtTest が作る）。ここが狙われる。
	dir := page.GetPageDir(id)
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, id+".html"), []byte("<h1>秘密</h1>"), 0644)

	for _, name := range []string{
		id + ".meta.json", // 権限サイドカー
		id + ".html",      // 本文
		"../../../etc/passwd",
		"notes.txt",
	} {
		body := `{"page_id":"` + id + `","file":"` + name + `"}`
		req := httptest.NewRequest("POST", "/api/analyze-attachment", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req = auth.WithUser(req, &auth.User{Username: "alice"})
		rr := httptest.NewRecorder()
		AnalyzeAttachmentAPIHandler(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s の解析が拒否されていません: code=%d body=%s", name, rr.Code, rr.Body.String())
		}
	}
}
