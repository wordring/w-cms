package cms

import (
	"archive/zip"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// ZIP添付の目録のテスト。固定するのは:
//   - 目録が返る（ファイルのみ・ディレクトリ行は畳む・展開はしない）
//   - Windows の右クリック圧縮（Shift_JIS 名・NonUTF8）が文字化けしない
//   - 名前の検査が全アップロード口と同じ関門を通る（.zip 以外・本文の名指しは拒否）
//   - ZIPでないファイルはエラー（500 ではなく 422）

// writeTestZip は files/ にテスト用 ZIP を置き、その保存名を返します。
func writeTestZip(t *testing.T, pageID string, build func(*zip.Writer)) string {
	t.Helper()
	dir := page.AttachmentDir(pageID)
	os.MkdirAll(dir, 0755)
	name := "testzip.zip"
	f, err := os.Create(filepath.Join(dir, name))
	if err != nil {
		t.Fatalf("ZIP作成エラー: %v", err)
	}
	zw := zip.NewWriter(f)
	build(zw)
	zw.Close()
	f.Close()
	return name
}

// getZipList はハンドラを閲覧者 u で叩きます。
func getZipList(t *testing.T, pageID, file string, u *auth.User) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/zip-list?page_id="+pageID+"&file="+file, nil)
	if u != nil {
		req = auth.WithUser(req, u)
	}
	rr := httptest.NewRecorder()
	ZipListAPIHandler(rr, req)
	return rr
}

// TestZipListReturnsEntries は目録（サブフォルダ含む・SJIS名の復号）を検証します。
func TestZipListReturnsEntries(t *testing.T) {
	const id = "000012"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "330"})

	sjis, _, err := transform.String(japanese.ShiftJIS.NewEncoder(), "図面一覧.txt")
	if err != nil {
		t.Fatalf("SJIS符号化エラー: %v", err)
	}
	name := writeTestZip(t, id, func(zw *zip.Writer) {
		w, _ := zw.Create("drawings/A-100.dxf")
		w.Write([]byte("dxf data"))
		// Windows の右クリック圧縮の実物形式（SJIS名・UTF-8フラグなし）
		hdr := &zip.FileHeader{Name: sjis, NonUTF8: true, Method: zip.Deflate}
		w2, _ := zw.CreateHeader(hdr)
		w2.Write([]byte("list"))
	})

	rr := getZipList(t, id, name, &auth.User{Username: "alice"})
	if rr.Code != 200 {
		t.Fatalf("目録が返りません: code=%d body=%s", rr.Code, rr.Body.String())
	}
	var res struct {
		Entries   []zipEntry `json:"entries"`
		Total     int        `json:"total"`
		Truncated bool       `json:"truncated"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &res); err != nil {
		t.Fatalf("応答を読めません: %v", err)
	}
	if res.Total != 2 || len(res.Entries) != 2 || res.Truncated {
		t.Fatalf("目録の件数が違います: %+v", res)
	}
	joined := res.Entries[0].Name + "|" + res.Entries[1].Name
	if !strings.Contains(joined, "drawings/A-100.dxf") {
		t.Errorf("サブフォルダのパスがありません: %s", joined)
	}
	if !strings.Contains(joined, "図面一覧.txt") {
		t.Errorf("SJIS名が復号されていません: %s", joined)
	}
	if res.Entries[0].Size == 0 {
		t.Errorf("サイズが入っていません: %+v", res.Entries[0])
	}
}

// TestZipListRejectsBadNames は名前の関門（拡張子・本文の名指し）を検証します。
func TestZipListRejectsBadNames(t *testing.T) {
	const id = "000012"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "330"})
	u := &auth.User{Username: "alice"}
	for _, file := range []string{"a.pdf", id + ".html", id + ".meta.json"} {
		if rr := getZipList(t, id, file, u); rr.Code != 400 {
			t.Errorf("%s が拒否されません: code=%d", file, rr.Code)
		}
	}
	// トラバーサルは拒否ではなく**正規化で無害化**される（SafeAttachmentName の仕様。
	// base 名に畳まれ、そのファイルは無いので 422）。200 にならないことだけ固定する。
	if rr := getZipList(t, id, "..%2F..%2Fx.zip", u); rr.Code == 200 {
		t.Errorf("トラバーサル名が通っています: code=%d", rr.Code)
	}
}

// TestZipListRejectsBrokenZip は ZIP でない中身のエラー（422）を検証します。
func TestZipListRejectsBrokenZip(t *testing.T) {
	const id = "000012"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "330"})
	dir := page.AttachmentDir(id)
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "broken.zip"), []byte("not a zip"), 0644)
	if rr := getZipList(t, id, "broken.zip", &auth.User{Username: "alice"}); rr.Code != 422 {
		t.Errorf("壊れたZIPが 422 になりません: code=%d", rr.Code)
	}
}

// TestZipListRequiresRead は閲覧関門（読めない人・匿名×非公開）を検証します。
func TestZipListRequiresRead(t *testing.T) {
	const id = "000012"
	// mode 330: owner/group のみ。部外者 bob と匿名は読めない。
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	name := writeTestZip(t, id, func(zw *zip.Writer) {
		w, _ := zw.Create("x.txt")
		w.Write([]byte("x"))
	})
	if rr := getZipList(t, id, name, &auth.User{Username: "bob"}); rr.Code != 403 {
		t.Errorf("部外者に目録が漏れます: code=%d", rr.Code)
	}
	if rr := getZipList(t, id, name, nil); rr.Code == 200 {
		t.Errorf("匿名に目録が漏れます: code=%d", rr.Code)
	}
}

// TestZipListHidesUnreferencedZip は、ログインしていない人に、本文が指していない ZIP の目録を見せないことを固定します（2026-10-30）。
// 添付の配信は 10-27 から確かめていて、目録の口だけ抜けていた。
func TestZipListHidesUnreferencedZip(t *testing.T) {
	setupAnonTest(t)
	const id = "000005" // 公開のページ
	name := writeTestZip(t, id, func(zw *zip.Writer) {
		w, _ := zw.Create("秘密の図面.dxf")
		w.Write([]byte("x"))
	})
	if err := page.WriteFileAtomic(page.BodyPath(id), []byte("<h1>公開</h1>"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rr := getZipList(t, id, name, nil); rr.Code != 404 || strings.Contains(rr.Body.String(), "秘密") {
		t.Errorf("⚠ 本文が指していない ZIP の目録が見えます: %d %s", rr.Code, rr.Body.String())
	}
	body := `<h1>公開</h1><p><a href="/` + id + `/` + name + `">zip</a></p>`
	if err := page.WriteFileAtomic(page.BodyPath(id), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	if rr := getZipList(t, id, name, nil); rr.Code != 200 {
		t.Errorf("本文が指す ZIP の目録は見えるはず: %d %s", rr.Code, rr.Body.String())
	}
}

// TestLongAttachmentNameIsRefused は、長すぎるファイル名を断り、ふつうに長い和文の名前は通すことを固定します（2026-10-30）。
func TestLongAttachmentNameIsRefused(t *testing.T) {
	allowed := map[string]bool{".pdf": true}
	if _, err := SafeAttachmentName("000001", strings.Repeat("あ", 200)+".pdf", allowed, "x"); err != nil {
		t.Errorf("200字の和文の名前は通るはず: %v", err)
	}
	if _, err := SafeAttachmentName("000001", strings.Repeat("a", 2000)+".pdf", allowed, "x"); err == nil {
		t.Error("⚠ 長すぎる名前が通っています")
	}
}

// TestTrashRemovesDerivedCaches は、ページをごみ箱へ移すと、表示用の写しと PDF の絵の置き場を消すことを固定します（2026-10-30）。
func TestTrashRemovesDerivedCaches(t *testing.T) {
	const id = "000013"
	setupUploadTest(t, id, page.PageMeta{Owner: "alice", Mode: "330"})
	dirs := []string{database.DataPath("cache", "photo-view", id), database.DataPath("cache", "pdf-pages", id)}
	for _, d := range dirs {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := moveToTrash(id); err != nil {
		t.Fatal(err)
	}
	for _, d := range dirs {
		if _, err := os.Stat(d); err == nil {
			t.Errorf("⚠ 残っています: %s", d)
		}
	}
}
