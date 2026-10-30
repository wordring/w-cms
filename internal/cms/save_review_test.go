package cms

import (
	"database/sql"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 2026-10-30 の見直し（【考察】コードの矛盾 §11）で直したものの見張りの試験です。

func titleInIndex(id int) string {
	var title sql.NullString
	database.DB.QueryRow(`SELECT title FROM pages WHERE id = ?`, id).Scan(&title)
	return title.String
}

// TestIndexFailureIsRetried は、索引づけに失敗した保存のあと、索引が直し直されることを固定します。
// それまでは本文だけが書けて、次に中身が変わるまで索引が古いまま残った（画面が送り直しても、本文が同じなので書き直されない）。
func TestIndexFailureIsRetried(t *testing.T) {
	setupSaveTest(t)
	newPage(t, "000050", "<h1>前の題</h1>", page.PageMeta{Owner: "alice", Mode: "330"})
	if got := titleInIndex(50); got != "前の題" {
		t.Fatalf("下ごしらえの索引: %q", got)
	}
	oldDelay := indexRetryDelay
	indexRetryDelay = 50 * time.Millisecond
	defer func() { indexRetryDelay = oldDelay }()

	// 索引の DB が読めない状態で保存する。
	good := database.DB
	bad, err := sql.Open("sqlite", filepath.Join(t.TempDir(), "closed.db"))
	if err != nil {
		t.Fatal(err)
	}
	bad.Close()
	database.DB = bad
	body := "<h1>新しい題</h1>"
	if err := page.WriteFileAtomic(page.BodyPath("000050"), []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
	rr := httptest.NewRecorder()
	if syncBody(rr, "000050", body) {
		database.DB = good
		t.Fatal("閉じた DB で索引づけが通っています")
	}
	database.DB = good

	for deadline := time.Now().Add(5 * time.Second); time.Now().Before(deadline); time.Sleep(20 * time.Millisecond) {
		if titleInIndex(50) == "新しい題" {
			return
		}
	}
	t.Errorf("⚠ 索引が直し直されていません: %q", titleInIndex(50))
}

// TestTopLevelTextSurvivesSplit は、本文の上の段の素の文字が、分けても消えないことを固定します。
func TestTopLevelTextSurvivesSplit(t *testing.T) {
	blocks, err := splitStoredBlocks("<h1>題</h1>素の文字<p>段落</p>\n  \n<!-- 注 -->")
	if err != nil {
		t.Fatal(err)
	}
	var all []string
	for _, b := range blocks {
		all = append(all, b.HTML)
	}
	joined := strings.Join(all, "|")
	if joined != "<h1>題</h1>|<p>素の文字</p>|<p>段落</p>" {
		t.Errorf("分け方: %s", joined)
	}
}

// TestRevertRefusesDuplicateBoxTitle は、古い版へ戻しても、トップ直下の置き場の題が2枚にならないことを固定します。
func TestRevertRefusesDuplicateBoxTitle(t *testing.T) {
	setupSaveTest(t)
	setupAuditDB(t)
	const box = "置き場の試験"
	RegisterRequiredPage(RequiredPage{Title: box, Extension: "test", Why: "試験用"})
	defer delete(requiredPageRegistry, box)
	newPage(t, "000060", "<h1>"+box+"</h1>", page.PageMeta{Owner: "alice", Mode: "330", ParentID: TopPageID})
	newPage(t, "000061", "<h1>別の題</h1>", page.PageMeta{Owner: "alice", Mode: "330", ParentID: TopPageID})
	if err := RecordVersion("000061", "alice", "<h1>"+box+"</h1>", true); err != nil {
		t.Fatal(err)
	}
	list, err := ListVersions("000061")
	if err != nil || len(list) == 0 {
		t.Fatalf("版: %v %v", list, err)
	}
	err = RevertToVersion("000061", list[0].ID, "alice")
	if err == nil || !strings.Contains(err.Error(), "1枚だけ") {
		t.Fatalf("断るはず: %v", err)
	}
	if b, _ := os.ReadFile(page.BodyPath("000061")); !strings.Contains(string(b), "別の題") {
		t.Errorf("⚠ 本文が戻っています: %s", b)
	}
}

// TestForcedVersionIsNotReplaced は、区切りで作った版を、同じ人の次の保存が置き換えないことを固定します。
func TestForcedVersionIsNotReplaced(t *testing.T) {
	setupSaveTest(t)
	newPage(t, "000062", "<h1>題</h1>", page.PageMeta{Owner: "alice", Mode: "330"})
	if err := RecordVersion("000062", "alice", "<p>戻した</p>", true); err != nil {
		t.Fatal(err)
	}
	if err := RecordVersion("000062", "alice", "<p>そのあと書いた</p>", false); err != nil {
		t.Fatal(err)
	}
	list, _ := ListVersions("000062")
	found := false
	for _, v := range list {
		if b, err := ReadVersion("000062", v.ID); err == nil && string(b) == "<p>戻した</p>" {
			found = true
		}
	}
	if !found {
		t.Errorf("⚠ 区切りで作った版が置き換えられました（版 %d 個）", len(list))
	}
}

// TestVersionBodySurvivesWhenInfoCannotBeWritten は、置き換えで情報のファイルを書けなくても、本文を消さないことを固定します。
func TestVersionBodySurvivesWhenInfoCannotBeWritten(t *testing.T) {
	dir := t.TempDir()
	const id = "20261030T000000Z"
	if err := writeVersionFiles(dir, id, VersionInfo{By: "alice"}, []byte("<p>一</p>")); err != nil {
		t.Fatal(err)
	}
	// 情報のファイルをフォルダに替えて、名前の置き換えを失敗させる。
	jsonPath := filepath.Join(dir, id+".json")
	if err := os.Remove(jsonPath); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(jsonPath, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := writeVersionFiles(dir, id, VersionInfo{By: "alice"}, []byte("<p>二</p>")); err == nil {
		t.Fatal("書けないはず")
	}
	if _, err := os.Stat(filepath.Join(dir, id+".html.gz")); err != nil {
		t.Errorf("⚠ 置き換えで本文が消えました: %v", err)
	}
}

// TestDeleteChecksParentFromSidecar は、索引に無いページでも、親に書けない人は消せないことを固定します。
func TestDeleteChecksParentFromSidecar(t *testing.T) {
	setupSaveTest(t)
	newPage(t, "000070", "<h1>親</h1>", page.PageMeta{Owner: "alice", Mode: "300"})
	newPage(t, "000071", "<h1>子</h1>", page.PageMeta{Owner: "bob", Mode: "300", ParentID: "000070"})
	if _, err := database.DB.Exec(`DELETE FROM pages WHERE id = 71`); err != nil {
		t.Fatal(err)
	}
	req := auth.WithUser(httptest.NewRequest("POST", "/api/delete-page?id=000071", nil), &auth.User{Username: "bob"})
	rr := httptest.NewRecorder()
	DeletePageAPIHandler(rr, req)
	if rr.Code != http.StatusForbidden {
		t.Errorf("⚠ 親に書けない人が消せています: %d %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(page.BodyPath("000071")); err != nil {
		t.Errorf("⚠ 消えています: %v", err)
	}
}

// TestRevertAnswersWrittenBody は、リバートの答えが実際に書いた本文であることを固定します。
func TestRevertAnswersWrittenBody(t *testing.T) {
	setupSaveTest(t)
	setupAuditDB(t)
	newPage(t, "000072", "<h1>いま</h1>", page.PageMeta{Owner: "alice", Mode: "330"})
	if err := RecordVersion("000072", "alice", "<h1>前</h1>", true); err != nil {
		t.Fatal(err)
	}
	list, _ := ListVersions("000072")
	body, _ := json.Marshal(map[string]string{"page_id": "000072", "version": list[0].ID})
	req := auth.WithUser(httptest.NewRequest("POST", "/api/revert", strings.NewReader(string(body))), &auth.User{Username: "alice"})
	rr := httptest.NewRecorder()
	RevertAPIHandler(rr, req)
	if rr.Code != 200 {
		t.Fatalf("戻せません: %d %s", rr.Code, rr.Body.String())
	}
	var got struct {
		HTML string `json:"html"`
	}
	json.Unmarshal(rr.Body.Bytes(), &got)
	onDisk, _ := os.ReadFile(page.BodyPath("000072"))
	if !strings.Contains(got.HTML, "前") || !strings.Contains(string(onDisk), "前") {
		t.Errorf("答え %q・本文 %q", got.HTML, onDisk)
	}
}
