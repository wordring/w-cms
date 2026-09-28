package cms

import (
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
