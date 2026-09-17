package cms

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

// 添付の目録（files/meta.json）のテスト。固定するのは:
//   - 保存すると目録に事実（名前・時刻・利用者・大きさ・ハッシュ・由来）が残る
//   - 名前はファイル名だけ（パスを渡されてもフォルダは落とす）
//   - 目録が無い添付は保存名で振る舞う（読み手を止めない）
//   - 目録は配信されない（/<ページ>/meta.json は 404）
//   - 壊れた目録は上書きしない

func TestSaveAttachmentRecordsMeta(t *testing.T) {
	newTestFileDB(t)
	newPage(t, "000201", "<h1>目録</h1>", page.PageMeta{Owner: "alice", Mode: "330"})

	id, stored, err := SaveAttachmentFrom("000201", "alice", `Q055-図面\R310-002_本体.pdf`, "zip:abcd.zip/Q055-図面/R310-002_本体.pdf", []byte("%PDF-1.4 x"))
	if err != nil {
		t.Fatal(err)
	}
	m, ok := AttachmentMetaOf("000201", stored)
	if !ok {
		t.Fatalf("目録に載っていません: %s", stored)
	}
	if m.Name != "R310-002_本体.pdf" {
		t.Errorf("名前はファイル名だけのはず: %q", m.Name)
	}
	if m.By != "alice" || m.Size != 10 || len(m.SHA256) != 64 || m.SavedAt == "" ||
		m.Source != "zip:abcd.zip/Q055-図面/R310-002_本体.pdf" {
		t.Errorf("事実が欠けています: %+v", m)
	}
	if AttachmentDisplayName("000201", stored) != "R310-002_本体.pdf" {
		t.Errorf("表示名が目録から来ていません")
	}
	// 目録が無い添付は保存名のまま。
	if got := AttachmentDisplayName("000201", "zzzz.dxf"); got != "zzzz.dxf" {
		t.Errorf("目録の無い添付は保存名で振る舞うはず: %q", got)
	}
	// 2つ目を足しても1つ目は残る（読み・足し・書きが1つの目録で回る）。
	_, stored2, _ := SaveAttachment("000201", "bob", "second.dxf", []byte("0\n"))
	metas := ReadAttachmentMetas("000201")
	if len(metas) != 2 || metas[stored2].Source != AttachmentSourceUpload || metas[stored].Name != "R310-002_本体.pdf" {
		t.Errorf("目録の追記が壊れています: %+v", metas)
	}
	_ = id
}

func TestUpdateAttachmentMetaKeepsName(t *testing.T) {
	newTestFileDB(t)
	newPage(t, "000202", "<h1>目録</h1>", page.PageMeta{Owner: "alice", Mode: "330"})
	_, stored, _ := SaveAttachment("000202", "alice", "図面.dxf", []byte("0\n"))
	before, _ := AttachmentMetaOf("000202", stored)
	fresh := NewAttachmentMeta(stored, "bob", AttachmentSourceDav, []byte("0 NEW\n"))
	if err := UpdateAttachmentMeta("000202", stored, func(m *AttachmentMeta) {
		m.By, m.Size, m.SHA256, m.Source = fresh.By, fresh.Size, fresh.SHA256, fresh.Source
	}); err != nil {
		t.Fatal(err)
	}
	after, _ := AttachmentMetaOf("000202", stored)
	if after.Name != "図面.dxf" || after.By != "bob" || after.Source != AttachmentSourceDav || after.SHA256 == before.SHA256 {
		t.Errorf("上書きの反映が違います: before=%+v after=%+v", before, after)
	}
}

func TestBrokenMetaIsNotOverwritten(t *testing.T) {
	newTestFileDB(t)
	newPage(t, "000203", "<h1>目録</h1>", page.PageMeta{Owner: "alice", Mode: "330"})
	dir := page.AttachmentDir("000203")
	os.MkdirAll(dir, 0o755)
	os.WriteFile(filepath.Join(dir, AttachmentMetaFile), []byte("{ broken"), 0o644)
	// 保存そのものは成功する（目録が書けなくても添付は失敗にしない）。
	if _, _, err := SaveAttachment("000203", "alice", "a.dxf", []byte("0\n")); err != nil {
		t.Fatalf("目録が壊れているだけで保存が失敗しました: %v", err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, AttachmentMetaFile))
	if string(b) != "{ broken" {
		t.Errorf("壊れた目録を上書きしました: %q", b)
	}
	if len(ReadAttachmentMetas("000203")) != 0 {
		t.Error("壊れた目録は空として読むはず")
	}
}

func TestMetaFileIsNotServed(t *testing.T) {
	newTestFileDB(t)
	newPage(t, "000204", "<h1>目録</h1>", page.PageMeta{Owner: "alice", Mode: "330"})
	SaveAttachment("000204", "alice", "a.dxf", []byte("0\n"))
	req := httptest.NewRequest("GET", "/000204/"+AttachmentMetaFile, nil)
	req = auth.WithUser(req, &auth.User{Username: "alice"})
	rr := httptest.NewRecorder()
	page.ServeCleanAttachment(rr, req, "000204", AttachmentMetaFile, http.NotFound)
	if rr.Code != 404 {
		t.Errorf("目録が配信されています: %d %s", rr.Code, rr.Body.String())
	}
	// 添付そのものは同じ口から配られる（目録だけを素通ししている）。
	stored := ""
	for k := range ReadAttachmentMetas("000204") {
		stored = k
	}
	req = auth.WithUser(httptest.NewRequest("GET", "/000204/"+stored, nil), &auth.User{Username: "alice"})
	rr = httptest.NewRecorder()
	page.ServeCleanAttachment(rr, req, "000204", stored, http.NotFound)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "0") {
		t.Errorf("添付が配られません: %d", rr.Code)
	}
	if !IsAttachmentMetaFile("meta.json") || IsAttachmentMetaFile("kokl.pdf") {
		t.Error("IsAttachmentMetaFile の判定が違います")
	}
}
