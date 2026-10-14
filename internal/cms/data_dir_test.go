package cms

// データの場所（WCMS_DATA_DIR・2026-10-14・【要求】運用 §3・§4）。
//
// 1台に2つの w-cms を同居させるとき、データ置き場を分けます。場所を変えたら、本文・版・添付・ゴミ箱が
// **全部そこへ行き**、作業ディレクトリの `data/` には何も書かないことを固定します
// （初期化の控えは、データの初期化と一緒に 2026-10-14 に無くなりました）
// （1か所でも `"data"` を直に書くと、そこだけ隣のインスタンスのデータへ書きます）。

import (
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// useOtherDataDir は、作業ディレクトリの外（別の一時ディレクトリ）をデータの場所にします（試験の終わりに既定へ戻す）。
func useOtherDataDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "インスタンス2", "data")
	database.SetDataDir(dir)
	t.Cleanup(func() { database.SetDataDir("") })
	return dir
}

// noLocalData は、作業ディレクトリの `data/` が作られていないことを確かめます。
func noLocalData(t *testing.T) {
	t.Helper()
	if _, err := os.Stat("data"); !os.IsNotExist(err) {
		t.Errorf("⚠ データの場所を変えたのに、作業ディレクトリの data/ に書いています（%v）", err)
	}
}

// TestDataDirMovesWhatIsWritten は、データの場所を変えると、保存・版・添付の配信・本文の読み出し・
// 削除（ゴミ箱）がその場所で行われることを固定します。
func TestDataDirMovesWhatIsWritten(t *testing.T) {
	setupSaveTest(t)
	writeTestShell(t)
	dir := useOtherDataDir(t)
	user := &auth.User{Username: "tester"}

	// ① 保存——本文と版が新しい場所に。
	postSave(t, "000041", "<h1>置き場の試験</h1><p>一回目</p>")
	postSave(t, "000041", "<h1>置き場の試験</h1><p>二回目</p>")
	body := filepath.Join(dir, "master", "00", "000041", "000041.html")
	if b, err := os.ReadFile(body); err != nil || !strings.Contains(string(b), "二回目") {
		t.Fatalf("本文が新しい場所 %s にありません: %v", body, err)
	}
	if ents, err := os.ReadDir(filepath.Join(dir, "master", "00", "000041", "versions")); err != nil || len(ents) == 0 {
		t.Errorf("版が新しい場所にありません: %v", err)
	}

	// ② 読み出し——索引が古い場所（作業ディレクトリの data/）を覚えていても、いまの場所から読む
	// （data/ を動かして WCMS_DATA_DIR を渡したときの形）。
	if _, err := database.DB.Exec(`UPDATE pages SET file_path = ? WHERE id = 41`,
		filepath.Join("data", "master", "00", "000041", "000041.html")); err != nil {
		t.Fatal(err)
	}
	req := auth.WithUser(httptest.NewRequest("GET", "/api/load?id=000041", nil), user)
	rr := httptest.NewRecorder()
	LoadAPIHandler(rr, req)
	if rr.Code != 200 || !strings.Contains(rr.Body.String(), "二回目") {
		t.Errorf("/api/load が新しい場所の本文を返しません: %d %s", rr.Code, rr.Body.String())
	}
	if rr := getPage(t, "/000041", user); rr.Code != 200 || !strings.Contains(rr.Body.String(), "二回目") {
		t.Errorf("ページの画面が新しい場所の本文を返しません: %d", rr.Code)
	}

	// ③ 添付の配信——URL は `/data/master/...` のまま、中身は新しい場所から。
	if err := os.WriteFile(filepath.Join(page.GetPageDir("000041"), "doc.pdf"), []byte("%PDF-1.4 新しい場所\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if rr := getData(t, "/data/master/00/000041/doc.pdf", user); rr.Code != 200 || !strings.Contains(rr.Body.String(), "新しい場所") {
		t.Errorf("/data/master/… が新しい場所の添付を配りません: %d %q", rr.Code, rr.Body.String())
	}

	// ④ 削除——ゴミ箱も新しい場所に。
	token := lockAs(t, 41, "tester")
	if rr := postDelete(t, "000041", token, user); rr.Code != 200 {
		t.Fatalf("削除できません: %d %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(filepath.Join(dir, "trash", "00", "000041")); err != nil {
		t.Errorf("ゴミ箱が新しい場所にありません: %v", err)
	}

	noLocalData(t)
}

// TestDataDirDefaultIsData は、⚠ **渡さなければ、いままでと同じ `data/`** であることを固定します。
func TestDataDirDefaultIsData(t *testing.T) {
	if got := page.MasterDir(); got != filepath.Join("data", "master") {
		t.Errorf("既定の正本の置き場が %q です（data/master のはず）", got)
	}
	if got := page.TrashDir(); got != filepath.Join("data", "trash") {
		t.Errorf("既定のゴミ箱が %q です（data/trash のはず）", got)
	}
	if got := page.BodyPath("000041"); got != filepath.Join("data", "master", "00", "000041", "000041.html") {
		t.Errorf("既定の本文の場所が %q です", got)
	}
}
