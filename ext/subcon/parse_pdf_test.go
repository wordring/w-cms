package subcon

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

// TestParsePDFRejectsNonPDFName は、AI解析（外部LLMへの送信）が
// PDF以外のファイルを掴めないことを固定します。
//
// parse-pdf は file_name を filepath.Base で切るだけで拡張子を見ていなかったため、
// ページディレクトリ内の任意のファイル——**本文 <id>.html と権限サイドカー
// <id>.meta.json を含む**——を「PDFとして」外部（Gemini）へ送れた。
// アップロード側は拡張子の許可リストと名指し拒否で守られているのに、
// 送信側だけが素通しという非対称だった。
//
// **2026-09-16 にコアから一緒に移しました**（口が `ext/subcon` へ出たため）。
// ⚠ 守りの本体は**コアの `cms.SafeAttachmentName`** に在ります——移設先で
// 自前の名前検査を書き直すと、この非対称がそのまま復活します。
func TestParsePDFRejectsNonPDFName(t *testing.T) {
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
		body := `{"page_id":"` + id + `","file_name":"` + name + `"}`
		req := httptest.NewRequest("POST", "/api/parse-pdf", strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		req = auth.WithUser(req, &auth.User{Username: "alice"})
		rr := httptest.NewRecorder()
		ParsePDFHandler(rr, req)

		if rr.Code != http.StatusBadRequest {
			t.Errorf("%s の解析が拒否されていません: code=%d body=%s", name, rr.Code, rr.Body.String())
		}
	}
}
