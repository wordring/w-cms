package cms

import (
	"bytes"
	"database/sql"
	"mime/multipart"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/editlock"
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
// 添付の追加は本文編集と同じ編集ロックで直列化されるので、その利用者で
// ロックを取り直してから送ります（ロックを持たない場合の挙動は
// TestUploadRequiresEditLock が別に固定する）。
func postUpload(t *testing.T, pageID, fileName string, content []byte, u *auth.User) *httptest.ResponseRecorder {
	t.Helper()
	idInt, _ := strconv.Atoi(pageID)
	editlock.Locks.ForceRelease(idInt)
	t.Cleanup(func() { editlock.Locks.ForceRelease(idInt) })
	a := editlock.Locks.TryAcquire(idInt, u.Username, "")
	if !a.Acquired {
		t.Fatalf("%s のロック取得に失敗", u.Username)
	}
	return postUploadTok(t, pageID, fileName, content, u, a.Token)
}

// postUploadTok は編集ロックトークンを明示して UploadPDFHandler を叩きます。
func postUploadTok(t *testing.T, pageID, fileName string, content []byte, u *auth.User, token string) *httptest.ResponseRecorder {
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
	if token != "" {
		req.Header.Set("X-Lock-Token", token)
	}
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
		if _, err := os.Stat(filepath.Join(page.AttachmentDir(id), name)); err == nil {
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
		if _, err := os.Stat(filepath.Join(page.AttachmentDir(id), "escape.pdf")); err != nil {
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
	if _, err := os.Stat(filepath.Join(page.AttachmentDir(id), "見積書.pdf")); err != nil {
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

// TestUploadRequiresEditLock は、添付の追加が本文編集と同じ編集ロックで
// 直列化されることを固定します。
//
// upload-pdf は同名ファイルを無条件で上書きし、添付にはリビジョンもゴミ箱も
// 無いため、ロックを通さないと「他人が編集中のページの発注書PDFが黙って
// すり替わり、復元できない」という不可逆のデータ破壊になる。
// editlock/handler.go は「将来のリソース操作（画像/PDF等）も同じロックで
// 直列化する」と宣言しているのに、この経路だけ実装が追いついていなかった。
func TestUploadRequiresEditLock(t *testing.T) {
	const id = "000009"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Group: "team", Mode: "330"})
	editlock.Locks.ForceRelease(9)
	t.Cleanup(func() { editlock.Locks.ForceRelease(9) })

	pdf := []byte("%PDF-1.4\n元の発注書")
	alice := &auth.User{Username: "alice"}

	// alice が保持者として正規にアップロードする。
	a := editlock.Locks.TryAcquire(9, "alice", "")
	if !a.Acquired {
		t.Fatal("alice のロック取得に失敗")
	}
	if rr := postUploadTok(t, id, "発注書.pdf", pdf, alice, a.Token); rr.Code != 200 {
		t.Fatalf("保持者のアップロードが失敗: code=%d body=%s", rr.Code, rr.Body.String())
	}

	// bob は同じ group で write を持つが、ロックは alice が保持している。
	bob := &auth.User{Username: "bob", Groups: []string{"team"}}
	rr := postUploadTok(t, id, "発注書.pdf", []byte("%PDF-1.4\nすり替え"), bob, "")
	if rr.Code != http.StatusConflict {
		t.Errorf("他者ロック中のアップロードが 409 になりません: code=%d body=%s", rr.Code, rr.Body.String())
	}

	// 正本が書き換わっていないこと（これが守りたいもの）。
	got, err := os.ReadFile(filepath.Join(page.AttachmentDir(id), "発注書.pdf"))
	if err != nil {
		t.Fatalf("添付を読めません: %v", err)
	}
	if string(got) != string(pdf) {
		t.Errorf("409 なのに添付が上書きされています: %q", string(got))
	}
}

// TestParsePDFRejectsNonPDFName は、AI解析（外部LLMへの送信）が
// PDF以外のファイルを掴めないことを固定します。
//
// parse-pdf は file_name を filepath.Base で切るだけで拡張子を見ていなかったため、
// ページディレクトリ内の任意のファイル——**本文 <id>.html と権限サイドカー
// <id>.meta.json を含む**——を「PDFとして」外部（Gemini）へ送れた。
// アップロード側は allowedAttachmentExts と名指し拒否で守られているのに、
// 送信側だけが素通しという非対称だった。
func TestParsePDFRejectsNonPDFName(t *testing.T) {
	const id = "000012"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "330"})

	// 本文とサイドカーは実在する（setupUploadTest が作る）。ここが狙われる。
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
