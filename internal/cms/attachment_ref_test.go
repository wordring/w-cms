package cms

import (
	"encoding/json"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

// TestFileRefsInCollectsThreeForms は、本文がファイルを指す**3つの形**を拾い、
// 出てきた順に・重複を1つにして返すことを固定します（2026-09-28）。
//
// ⚠ 添付でないリンク（外のサイト・ページへのリンク）と、ページ全体への参照は拾いません。
func TestFileRefsInCollectsThreeForms(t *testing.T) {
	body := `<details open><summary>資料 1</summary>` +
		`<section data-type="file-view" data-ref="000223-fhea"></section>` +
		`<p>📎 <a href="/000235/ab12.pdf" download="図面.pdf">図面.pdf</a></p>` +
		`<p><img src="/000235/cd34.jpg"></p>` +
		`<p><a href="/000223/fhea.pdf">同じ図面をもう一度</a></p>` +
		`<p><a href="https://example.com/x.pdf">外</a> <a href="/000235">ページ</a></p>` +
		`<section data-type="file-view" data-ref="000235"></section>` +
		`</details>`
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		t.Fatal(err)
	}
	got := FileRefsIn(nodes[0])
	want := []FileRef{{"000223", "fhea"}, {"000235", "ab12"}, {"000235", "cd34"}}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("拾った参照が違います:\n got  %v\n want %v", got, want)
	}
}

// TestAttachmentOfRefChecksTheReferredPage は、**指されたページが読めるか**で決めることを
// 固定します——資料のブロックは別のページ（通信記録）の図面を指すので、ブロックのある
// ページが読めても、図面のページが読めなければ渡しません。
func TestAttachmentOfRefChecksTheReferredPage(t *testing.T) {
	// ⚠ **ファイルDB**で——`page.CanView` を通るので `:memory:` では誰も読めない（引き継ぎの罠）。
	newTestFileDB(t)
	if err := page.WriteSidecar("000001", page.PageMeta{Owner: "alice", Mode: "300"}); err != nil {
		t.Fatal(err)
	}
	if err := SyncIndex("000001", "<h1>通信記録</h1>"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(page.GetPageDir("000001"), "files")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "c3p7.pdf"), []byte("%PDF-1.4"), 0o644); err != nil {
		t.Fatal(err)
	}
	ref := FileRef{PageID: "000001", ID: "c3p7"}

	stored, display, ok := AttachmentOfRef(&auth.User{Username: "alice"}, ref)
	if !ok || stored != "c3p7.pdf" || display == "" {
		t.Errorf("読める人に添付が引けません: %q %q %v", stored, display, ok)
	}
	if _, _, ok := AttachmentOfRef(&auth.User{Username: "bob"}, ref); ok {
		t.Error("⚠ 読めない人に添付を渡しています")
	}
	if _, _, ok := AttachmentOfRef(&auth.User{Username: "alice"}, FileRef{PageID: "000001", ID: "zzzz"}); ok {
		t.Error("無い添付を在ると言っています")
	}
}

// TestParseFileRefTakesIDAndAddress は、人が貼る2つの形——ID（`ページ番号-添付ID`）と添付の住所——を
// 同じ参照へ畳み、ページ全体の参照や文法外は断ることを固定します（2026-10-01）。
func TestParseFileRefTakesIDAndAddress(t *testing.T) {
	want := FileRef{PageID: "000235", ID: "ab12"}
	for _, s := range []string{
		"000235-ab12", " 000235-ab12\n", "000235－ab12",
		"/000235/ab12.pdf", "https://localhost:8443/000235/ab12.pdf", "https://localhost:8443/000235/ab12.pdf#page=2",
	} {
		got, ok := ParseFileRef(s)
		if !ok || got != want {
			t.Errorf("%q を畳めません: %v %v", s, got, ok)
		}
	}
	for _, s := range []string{"", "000235", "abc", "/000235", "https://example.com/x.pdf", "00235-ab12"} {
		if got, ok := ParseFileRef(s); ok {
			t.Errorf("%q を参照と読んでいます: %v", s, got)
		}
	}
}

// TestFileRefAPIHandlerResolvesForReaders は、`/api/file-ref` が読める人にだけ添付の組を返し、
// 読めない・無いは同じ 404 にすることを固定します（2026-10-01）。
func TestFileRefAPIHandlerResolvesForReaders(t *testing.T) {
	newTestFileDB(t)
	if err := page.WriteSidecar("000001", page.PageMeta{Owner: "alice", Mode: "300"}); err != nil {
		t.Fatal(err)
	}
	if err := SyncIndex("000001", "<h1>通信記録</h1>"); err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(page.GetPageDir("000001"), "files")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "c3p7.pdf"), []byte("%PDF-1.4"), 0o644); err != nil {
		t.Fatal(err)
	}
	call := func(user, ref string) (int, map[string]any) {
		req := httptest.NewRequest("GET", "/api/file-ref?ref="+url.QueryEscape(ref), nil)
		req = auth.WithUser(req, &auth.User{Username: user})
		rr := httptest.NewRecorder()
		FileRefAPIHandler(rr, req)
		var got map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &got)
		return rr.Code, got
	}

	code, got := call("alice", "000001-c3p7")
	if code != 200 || got["page_id"] != "000001" || got["file"] != "c3p7.pdf" || got["ref"] != "000001-c3p7" || got["name"] == "" {
		t.Errorf("読める人に添付の組を返しません: %d %v", code, got)
	}
	if code, _ := call("alice", "/000001/c3p7.pdf"); code != 200 {
		t.Errorf("添付の住所を受けません: %d", code)
	}
	codeNoRead, _ := call("bob", "000001-c3p7")
	codeMissing, _ := call("alice", "000001-zzzz")
	if codeNoRead != 404 || codeMissing != 404 {
		t.Errorf("読めない・無いは同じ 404 のはず: 読めない=%d 無い=%d", codeNoRead, codeMissing)
	}
	if code, _ := call("alice", "000001"); code != 400 {
		t.Errorf("ページ全体の参照は 400 のはず: %d", code)
	}
}
