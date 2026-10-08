package toho

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// 整理の「ほかのファイル」（filing_files.go・2026-10-08）と、整理の新規で区分を消さないこと（filing.go）の番人です。
// 利用者:「図面以外のファイルでも、ここで相手を検索して追加できるとありがたいですね」
// 「試作や見積もりによるフォルダ分けが無くなったので、整理ブロックで入力する必要はなくなりました」

// putNamedAttachment は記録に添付を1つ置きます（中身と名前の記録）。
func putNamedAttachment(t *testing.T, pageID, stored, name string) {
	t.Helper()
	dir := page.AttachmentDir(pageID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, stored), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := cms.RecordAttachmentMeta(pageID, stored, cms.NewAttachmentMeta(name, "alice", "mail:mail.eml", []byte("x"))); err != nil {
		t.Fatal(err)
	}
}

func postAttach(t *testing.T, u *auth.User, record, attach, target string) (int, map[string]any) {
	t.Helper()
	b, _ := json.Marshal(map[string]string{"page_id": record, "attach": attach, "target": target})
	req := httptest.NewRequest("POST", "/api/filing-attach", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Origin", "http://example.com")
	req = auth.WithUser(req, u)
	rr := httptest.NewRecorder()
	FilingAttachAPIHandler(rr, req)
	var d map[string]any
	json.Unmarshal(rr.Body.Bytes(), &d)
	return rr.Code, d
}

// TestFilingFilesListAndAttach は、記録の添付（.eml を除く）が並び、選んだページの末尾にファイル表示を足し、同じページに
// 二度は足さず、表示しているページとして見つかることを確かめます。
func TestFilingFilesListAndAttach(t *testing.T) {
	const inbox = "000016"
	folder := folderSetup(t, inbox)
	record := makeDrawingPageFrom(t, inbox, "pdf009", "A100-00Z", "記録の代わり", "テスト装置", "みらい産業")
	putNamedAttachment(t, record, "ab12.x_t", "取付データ.x_t")
	putNamedAttachment(t, record, "cd34.eml", "mail.eml")
	files := mailFilesOf(record)
	if len(files) != 1 || files[0].ID != "ab12" || files[0].Name != "取付データ.x_t" || files[0].Ref != record+"-ab12" {
		t.Fatalf("添付の一覧 = %+v（.eml を除いた1つのはず）", files)
	}
	u := &auth.User{Username: "alice"}
	code, d := postAttach(t, u, record, "ab12", folder)
	if code != 200 || d["success"] != true {
		t.Fatalf("足せません: %d %v", code, d)
	}
	body, _ := cms.ReadPageBody(folder)
	if !strings.Contains(body, `data-ref="`+record+`-ab12"`) || !strings.HasSuffix(strings.TrimSpace(body), "</section>") {
		t.Errorf("装置のページの末尾にファイル表示がありません: %s", body)
	}
	if code, d := postAttach(t, u, record, "ab12", folder); code == 200 || !strings.Contains(d["message"].(string), "既にあります") {
		t.Errorf("同じページに二度足しています: %d %v", code, d)
	}
	if code, _ := postAttach(t, u, record, "zz99", folder); code == 200 {
		t.Error("記録に無い添付を足しています")
	}
	shown := pagesShowing(u, []string{record + "-ab12"})
	if got := shown[record+"-ab12"]; len(got) != 1 || got[0].PageID != folder {
		t.Errorf("表示しているページ = %+v（装置のページ %s のはず）", got, folder)
	}
}

// TestFilingSearchFindsFolders は、行き先を探すで folders を付けたときだけ装置のページも出ることを確かめます。
func TestFilingSearchFindsFolders(t *testing.T) {
	const inbox = "000017"
	folder := folderSetup(t, inbox)
	u := &auth.User{Username: "alice"}
	kinds := func(hits []productHit) (folders, products int) {
		for _, h := range hits {
			if h.Kind == "folder" {
				folders++
				if h.PageID != folder {
					t.Errorf("装置のページの ID = %s（%s のはず）", h.PageID, folder)
				}
			} else {
				products++
			}
		}
		return
	}
	if f, _ := kinds(searchProducts(u, "", "テスト装置", 20, false)); f != 0 {
		t.Errorf("folders を付けないのに装置のページが出ています")
	}
	if f, p := kinds(searchProducts(u, "", "テスト装置", 20, true)); f != 1 || p != 1 {
		t.Errorf("装置のページ %d・加工製品 %d（1と1のはず——装置名で加工製品も当たる）", f, p)
	}
}

// TestFilingNewKeepsKinds は、整理の新規で区分を送らなくても、運んできたページに付いていた区分を消さないことを確かめます
// （整理の欄から区分の印を外したので、画面は区分を送らない——それまでは空で置き換えて消していた）。
func TestFilingNewKeepsKinds(t *testing.T) {
	const inbox = "000018"
	setupFilingTest(t, inbox)
	part := makeDrawingPageFrom(t, inbox, "pdf001", "A100-B01-002", "支え", "テスト装置", "みらい産業")
	if err := setProductKinds(&auth.User{Username: "alice"}, part, []string{"試作"}, false); err != nil {
		t.Fatal(err)
	}
	res := postFiling(t, &auth.User{Username: "alice"}, []filingRequest{{PageID: part, Customer: "みらい産業",
		MachineName: "テスト装置", DrawingName: "支え", Merge: "new"}})
	if len(res) != 1 || res[0].Outcome != "moved" {
		t.Fatalf("整理: %+v", res)
	}
	body, _ := cms.ReadPageBody(part)
	if !strings.Contains(body, "試作") {
		t.Errorf("区分「試作」が消えています: %s", body)
	}
}
