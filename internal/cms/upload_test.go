package cms

import (
	"bytes"
	"database/sql"
	"mime/multipart"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"

	_ "modernc.org/sqlite"
)

// 添付アップロードと添付配信の安全性テスト。
//
// ページのディレクトリ（data/master/xx/<id>/）には本文 <id>.html と属性サイドカー
// <id>.meta.json が同居している。任意の名前・種類のファイルを置けると
//   - サイドカーを上書きして owner / mode / public を書き換える（権限昇格）
//   - .html を置いて /data/... から同一オリジンのHTMLとして配信させる（保存型XSS）
// が成立するため、その両方が塞がっていることを固定する。

// setupUploadTest は data/master を一時ディレクトリへ切り替え、対象ページの
// サイドカーと page_perms を用意します。
func setupUploadTest(t *testing.T, id string, p page.PageMeta) {
	t.Helper()

	origWd, _ := os.Getwd()
	if err := os.Chdir(t.TempDir()); err != nil {
		t.Fatalf("Chdirエラー: %v", err)
	}
	t.Cleanup(func() { os.Chdir(origWd) })

	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatalf("DB接続エラー: %v", err)
	}
	t.Cleanup(func() { db.Close() })
	database.DB = db
	if err := database.CreateCoreTables(db); err != nil {
		t.Fatalf("コアテーブル作成エラー: %v", err)
	}
	if err := ApplySchema(db); err != nil {
		t.Fatalf("プラグインスキーマ作成エラー: %v", err)
	}
	if err := page.WriteSidecar(id, p); err != nil {
		t.Fatalf("page.WriteSidecarエラー: %v", err)
	}
	if err := SyncIndex(id, "<h1>添付テスト</h1>"); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}
}

// postUpload は multipart で UploadPDFHandler を叩きます。
func postUpload(t *testing.T, pageID, fileName string, content []byte, u *auth.User) *httptest.ResponseRecorder {
	t.Helper()

	var body bytes.Buffer
	mw := multipart.NewWriter(&body)
	mw.WriteField("page_id", pageID)
	part, err := mw.CreateFormFile("pdf_file", fileName)
	if err != nil {
		t.Fatalf("CreateFormFileエラー: %v", err)
	}
	part.Write(content)
	mw.Close()

	req := httptest.NewRequest("POST", "/api/upload-pdf", &body)
	req.Header.Set("Content-Type", mw.FormDataContentType())
	req = auth.WithUser(req, u)

	rr := httptest.NewRecorder()
	UploadPDFHandler(rr, req)
	return rr
}

// TestUploadRejectsSidecarOverwrite は、write権限しか持たない利用者が属性サイドカーを
// 上書きして所有者・権限・公開フラグを書き換えられないことを検証します（権限昇格の遮断）。
func TestUploadRejectsSidecarOverwrite(t *testing.T) {
	const id = "000012"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "333"})

	attacker := &auth.User{Username: "mallory"}
	evil := []byte(`{"owner":"mallory","mode":"333","public":true}`)

	rr := postUpload(t, id, id+".meta.json", evil, attacker)
	if rr.Code != 400 {
		t.Fatalf("サイドカーの上書きが拒否されていません: status=%d body=%s", rr.Code, rr.Body.String())
	}

	meta, ok := page.ReadSidecar(id)
	if !ok {
		t.Fatal("サイドカーが読めません")
	}
	if meta.Owner != "alice" || meta.Public {
		t.Fatalf("サイドカーが書き換えられました: %+v", meta)
	}
}

// TestUploadRejectsNonPDFName は、HTML等の実行されうる種類を置けないことを検証します
// （/data/ 経由の同一オリジン配信による保存型XSSの遮断）。
func TestUploadRejectsNonPDFName(t *testing.T) {
	const id = "000012"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "333"})

	user := &auth.User{Username: "alice"}
	for _, name := range []string{"evil.html", "evil.svg", "evil.pdf.html", "evil.js"} {
		rr := postUpload(t, id, name, []byte("<script>alert(1)</script>"), user)
		if rr.Code != 400 {
			t.Errorf("%s のアップロードが拒否されていません: status=%d", name, rr.Code)
		}
		if _, err := os.Stat(filepath.Join(page.GetPageDir(id), name)); err == nil {
			t.Errorf("%s がページディレクトリに保存されました", name)
		}
	}
}

// TestUploadRejectsFakePDF は、拡張子だけPDFを名乗る中身を弾くことを検証します。
func TestUploadRejectsFakePDF(t *testing.T) {
	const id = "000012"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "333"})

	rr := postUpload(t, id, "innocent.pdf", []byte("<html><script>alert(1)</script>"), &auth.User{Username: "alice"})
	if rr.Code != 400 {
		t.Fatalf("中身がPDFでないファイルが受理されました: status=%d body=%s", rr.Code, rr.Body.String())
	}
}

// TestUploadStripsPathComponents は、ファイル名のパス要素が落ちてページの
// ディレクトリ内へ収まることを検証します。
func TestUploadStripsPathComponents(t *testing.T) {
	const id = "000012"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "333"})

	pdf := []byte("%PDF-1.7\n本文")
	for _, name := range []string{`../../escape.pdf`, `..\..\escape.pdf`, `/etc/escape.pdf`} {
		rr := postUpload(t, id, name, pdf, &auth.User{Username: "alice"})
		if rr.Code != 200 {
			t.Fatalf("%s のアップロードが失敗しました: status=%d body=%s", name, rr.Code, rr.Body.String())
		}
		if _, err := os.Stat(filepath.Join(page.GetPageDir(id), "escape.pdf")); err != nil {
			t.Errorf("%s がページディレクトリに保存されていません: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join("data", "escape.pdf")); err == nil {
			t.Errorf("%s がページディレクトリの外へ保存されました", name)
		}
	}
}

// TestUploadAcceptsPDF は正常系（本物のPDF）が従来どおり保存されることを検証します。
func TestUploadAcceptsPDF(t *testing.T) {
	const id = "000012"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "333"})

	rr := postUpload(t, id, "見積書.pdf", []byte("%PDF-1.4\n..."), &auth.User{Username: "alice"})
	if rr.Code != 200 {
		t.Fatalf("PDFのアップロードが失敗しました: status=%d body=%s", rr.Code, rr.Body.String())
	}
	if !strings.Contains(rr.Body.String(), "見積書.pdf") {
		t.Errorf("レスポンスにファイル名が含まれていません: %s", rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(page.GetPageDir(id), "見積書.pdf")); err != nil {
		t.Errorf("PDFが保存されていません: %v", err)
	}
}

// getData は page.DataFileHandler へ GET します。
func getData(t *testing.T, urlPath string, u *auth.User) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", urlPath, nil)
	if u != nil {
		req = auth.WithUser(req, u)
	}
	rr := httptest.NewRecorder()
	page.DataFileHandler(rr, req)
	return rr
}

// TestDataFileHandlerDoesNotServeExecutableTypes は、過去に置かれた（あるいは復元された）
// HTMLがブラウザに解釈されない形で配信されること、本文とサイドカーは配信されないことを
// 検証します（アップロード側の許可リストに対する多層防御）。
func TestDataFileHandlerDoesNotServeExecutableTypes(t *testing.T) {
	const id = "000012"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "333"})

	dir := page.GetPageDir(id)
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "legacy.html"), []byte("<script>alert(1)</script>"), 0644)
	os.WriteFile(filepath.Join(dir, "doc.pdf"), []byte("%PDF-1.4\n"), 0644)

	user := &auth.User{Username: "alice"}
	base := "/data/master/00/" + id + "/"

	// 1. HTML は text/html では返さない（同一オリジンでの実行を防ぐ）
	rr := getData(t, base+"legacy.html", user)
	if ct := rr.Header().Get("Content-Type"); strings.Contains(ct, "text/html") {
		t.Errorf("HTMLが text/html で配信されました: %s", ct)
	}
	if rr.Header().Get("Content-Disposition") == "" {
		t.Error("HTMLがダウンロード扱いになっていません")
	}
	if rr.Header().Get("X-Content-Type-Options") != "nosniff" {
		t.Error("nosniff が付いていません")
	}

	// 2. PDF は従来どおりインライン表示できる
	rr = getData(t, base+"doc.pdf", user)
	if rr.Code != 200 {
		t.Fatalf("PDFの配信に失敗しました: status=%d", rr.Code)
	}
	if ct := rr.Header().Get("Content-Type"); ct != "application/pdf" {
		t.Errorf("PDFの Content-Type が想定と違います: %s", ct)
	}

	// 3. 本文と属性サイドカーは添付として配らない
	for _, name := range []string{id + ".html", id + ".meta.json"} {
		if rr := getData(t, base+name, user); rr.Code != 404 {
			t.Errorf("%s が配信されました: status=%d", name, rr.Code)
		}
	}
}
