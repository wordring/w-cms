package toho

import (
	"strconv"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
)

// 整理の「装置のページへ」（filing_folder.go・2026-10-08）の番人です。
// 利用者:「メールの整理について、図面をフォルダページに追加したいのですが、出来ません」（組立図）。

// folderSetup は取引先・加工製品・装置のページを、ふつうの整理（新規）で作って装置のページの ID を返します。
func folderSetup(t *testing.T, inbox string) string {
	t.Helper()
	setupFilingTest(t, inbox)
	part := makeDrawingPageFrom(t, inbox, "pdf001", "A100-B01-001", "取付ベース", "テスト装置", "みらい産業")
	res := postFiling(t, &auth.User{Username: "alice"}, []filingRequest{{PageID: part, Customer: "みらい産業",
		MachineName: "テスト装置", DrawingName: "取付ベース", Merge: "new"}})
	if len(res) != 1 || res[0].Outcome != "moved" {
		t.Fatalf("下ごしらえの整理: %+v", res)
	}
	folder, ok := findMachineFolder("みらい産業", "テスト装置")
	if !ok {
		t.Fatal("装置のページがありません")
	}
	return folder
}

// TestFileToMachineFolderAddsDrawing は、装置のページへ図面を足し、加工製品ページは作らず、解析で作ったページはごみ箱へ
// 移すことを確かめます。図面名称の欄が空でも動く。
func TestFileToMachineFolderAddsDrawing(t *testing.T) {
	const inbox = "000013"
	folder := folderSetup(t, inbox)
	asm := makeDrawingPageFrom(t, inbox, "pdf002", "A100-00A", "組立図", "テスト装置", "みらい産業")
	res := postFiling(t, &auth.User{Username: "alice"}, []filingRequest{{PageID: asm, Customer: "みらい産業",
		MachineName: "テスト装置", DrawingName: "", Merge: "folder"}})
	if len(res) != 1 || res[0].Outcome != "added" || res[0].TargetID != folder {
		t.Fatalf("装置のページへ足していません: %+v（装置のページ %s）", res, folder)
	}
	body, _ := cms.ReadPageBody(folder)
	if !strings.Contains(body, "A100-00A") || !strings.Contains(body, `data-ref="`+inbox+`-pdf002"`) {
		t.Errorf("装置のページに図面のまとまりがありません: %s", body)
	}
	if _, ok := findChildByTitle(folder, "組立図"); ok {
		t.Error("加工製品ページが作られています（作らないはず）")
	}
	if _, ok := page.ReadSidecar(asm); ok {
		t.Error("解析で作ったページが残っています（ごみ箱へ移すはず）")
	}
	if !strings.Contains(res[0].Message, "みらい産業／加工製品／テスト装置") {
		t.Errorf("知らせに行き先がありません: %s", res[0].Message)
	}
}

// TestFileToMachineFolderRefusesMissingFolder は、装置のページが無ければ作らずに断ることを確かめます。
func TestFileToMachineFolderRefusesMissingFolder(t *testing.T) {
	const inbox = "000014"
	folderSetup(t, inbox)
	asm := makeDrawingPageFrom(t, inbox, "pdf002", "A100-00A", "組立図", "無い装置", "みらい産業")
	res := postFiling(t, &auth.User{Username: "alice"}, []filingRequest{{PageID: asm, Customer: "みらい産業",
		MachineName: "無い装置", Merge: "folder"}})
	if len(res) != 1 || res[0].Outcome != "skipped" || !strings.Contains(res[0].Message, "ありません") {
		t.Fatalf("断っていません: %+v", res)
	}
	if _, ok := findMachineFolder("みらい産業", "無い装置"); ok {
		t.Error("装置のページを作っています（作らないはず）")
	}
	if _, ok := page.ReadSidecar(asm); !ok {
		t.Error("解析で作ったページが消えています（断ったので残すはず）")
	}
}

// TestFileToMachineFolderAsksWhenSameFileShown は、装置のページに同じファイルの表示が既にあれば確かめ、
// 承知のうえなら足すことを確かめます（手で貼った表示があると2つになるため）。
func TestFileToMachineFolderAsksWhenSameFileShown(t *testing.T) {
	const inbox = "000015"
	folder := folderSetup(t, inbox)
	if err := cms.RewriteBody(folder, "alice", func(cur string) string {
		return cur + `<section data-type="file-view" data-ref="` + inbox + `-pdf003"></section>`
	}); err != nil {
		t.Fatal(err)
	}
	asm := makeDrawingPageFrom(t, inbox, "pdf003", "A100-00A", "組立図", "テスト装置", "みらい産業")
	row := filingRequest{PageID: asm, Customer: "みらい産業", MachineName: "テスト装置", Merge: "folder"}
	res := postFiling(t, &auth.User{Username: "alice"}, []filingRequest{row})
	if len(res) != 1 || res[0].Outcome != "needs_confirm" || !strings.Contains(res[0].Message, "既にあります") {
		t.Fatalf("確かめていません: %+v", res)
	}
	row.ConfirmRevision = true
	res = postFiling(t, &auth.User{Username: "alice"}, []filingRequest{row})
	if len(res) != 1 || res[0].Outcome != "added" {
		t.Fatalf("承知のうえで足していません: %+v", res)
	}
	body, _ := cms.ReadPageBody(folder)
	if n := strings.Count(body, `data-ref="`+inbox+`-pdf003"`); n != 2 {
		t.Errorf("同じファイルの表示が %d 個（前からの1つ＋足した1つ のはず）: %s", n, body)
	}
}

// TestFilingSkipsUnwritableOrOpen は、整理が、動かすページを書けない人の行と、誰かが開いているページの行を飛ばすことを
// 固定します（通常の整理と「装置のページへ」が共有する関門 filingSourceGate の番人・2026-10-09）。
func TestFilingSkipsUnwritableOrOpen(t *testing.T) {
	const inbox = "000019"
	folderSetup(t, inbox)
	part := makeDrawingPageFrom(t, inbox, "pdf011", "A100-00Y", "仮の部品", "テスト装置", "みらい産業")
	row := filingRequest{PageID: part, Customer: "みらい産業", MachineName: "テスト装置", DrawingName: "仮の部品", Merge: "new"}
	if res := postFiling(t, &auth.User{Username: "bob"}, []filingRequest{row}); len(res) != 1 || res[0].Outcome != "skipped" ||
		res[0].Message != "このページを動かす権限がありません" {
		t.Errorf("書けない人の行を飛ばしていません: %+v", res)
	}
	n, _ := strconv.Atoi(part)
	if r := editlock.Locks.TryAcquire(n, "carol", ""); !r.Acquired {
		t.Fatal("ロックを取れません")
	}
	t.Cleanup(func() { editlock.Locks.ForceRelease(n) })
	for _, m := range []string{"new", "folder"} {
		row.Merge = m
		if res := postFiling(t, &auth.User{Username: "alice"}, []filingRequest{row}); len(res) != 1 || res[0].Outcome != "skipped" ||
			!strings.Contains(res[0].Message, "編集中") {
			t.Errorf("%s: 開いているページを動かしています: %+v", m, res)
		}
	}
}
