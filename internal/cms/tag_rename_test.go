package cms

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// タグの値を置き換える（2026-10-01・tag_rename.go）。利用者:「見積もりという見出しや値は見積と短くした方がよいと
// 思います。これは設定ファイルも変える必要があると思います。」——選択肢の言葉を変えたら、既にあるページのタグも揃える。

func TestRenameTagValueOnlyTouchesTheTagPair(t *testing.T) {
	body := "<h1>見積もりの品</h1><dl data-type=\"tags\"><dt>区分</dt>\n    <dd>見積もり</dd><dt>品名</dt><dd>見積もり</dd></dl>" +
		"<p>見積もり</p>"
	got := renameTagValue(body, "区分", "見積もり", "見積")
	if !strings.Contains(got, "<dt>区分</dt>\n    <dd>見積</dd>") {
		t.Errorf("区分の組が置き換わっていません（間の空白は残す）:\n%s", got)
	}
	// 題・別の名前のタグ・地の文には触らない。
	if !strings.Contains(got, "<h1>見積もりの品</h1>") || !strings.Contains(got, "<dt>品名</dt><dd>見積もり</dd>") ||
		!strings.Contains(got, "<p>見積もり</p>") {
		t.Errorf("区分の組の外まで置き換えました:\n%s", got)
	}
	// 新しい値の組が既にあれば、古い組を消すだけ（同じ値を2つにしない）。
	both := `<dl data-type="tags"><dt>区分</dt><dd>試作</dd><dt>区分</dt><dd>見積</dd><dt>区分</dt><dd>見積もり</dd></dl>`
	if got := renameTagValue(both, "区分", "見積もり", "見積"); got != `<dl data-type="tags"><dt>区分</dt><dd>試作</dd><dt>区分</dt><dd>見積</dd></dl>` {
		t.Errorf("新しい値が既にあるのに古い組が残るか、二重になりました:\n%s", got)
	}
	// 置き換え先の $ は文字のまま。
	if got := renameTagValue(body, "区分", "見積もり", "$1"); !strings.Contains(got, "<dd>$1</dd>") {
		t.Errorf("置き換え先の $ が展開されました:\n%s", got)
	}
}

func TestTagRenameAPIRewritesPagesAndSkipsEditing(t *testing.T) {
	newTestFileDB(t)
	meta := page.PageMeta{Owner: "alice", Mode: page.DefaultMode, ParentID: TopPageID}
	tags := func(v string) string { return `<dl data-type="tags"><dt>区分</dt><dd>` + v + `</dd></dl>` }
	newPage(t, "000031", `<h1>甲</h1>`+tags("見積もり"), meta)
	newPage(t, "000032", `<h1>乙</h1>`+tags("見積もり"), meta)
	newPage(t, "000033", `<h1>丙</h1>`+tags("試作"), meta)
	// 乙は編集中——飛ばす。
	editlock.Locks.TryAcquire(32, "bob", "tok")
	t.Cleanup(func() { editlock.Locks.ForceRelease(32) })

	post := func(u *auth.User, req map[string]any) (int, map[string]any) {
		b, _ := json.Marshal(req)
		r := httptest.NewRequest("POST", "/api/admin/tag-rename", bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		r = auth.WithUser(r, u)
		rr := httptest.NewRecorder()
		TagRenameAPIHandler(rr, r)
		var got map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &got)
		return rr.Code, got
	}
	admin := &auth.User{Username: "root", IsAdmin: true}
	req := map[string]any{"name": "区分", "from": "見積もり", "to": "見積", "dry": true}

	if code, _ := post(&auth.User{Username: "alice"}, req); code != 403 {
		t.Errorf("管理者でない人が置き換えられます: %d", code)
	}
	// dry は数えるだけ。
	code, got := post(admin, req)
	res, _ := got["result"].(map[string]any)
	if code != 200 || len(res["changed"].([]any)) != 1 || len(res["editing"].([]any)) != 1 {
		t.Fatalf("dry の数が違います: %d %v", code, got)
	}
	if b, _ := os.ReadFile(page.BodyPath("000031")); !strings.Contains(string(b), "<dd>見積もり</dd>") {
		t.Errorf("dry なのに書き換えました:\n%s", b)
	}

	req["dry"] = false
	code, got = post(admin, req)
	if code != 200 || got["success"] != true {
		t.Fatalf("置き換えられません: %d %v", code, got)
	}
	if b, _ := os.ReadFile(page.BodyPath("000031")); !strings.Contains(string(b), "<dd>見積</dd>") {
		t.Errorf("甲が置き換わっていません:\n%s", b)
	}
	if b, _ := os.ReadFile(page.BodyPath("000032")); !strings.Contains(string(b), "<dd>見積もり</dd>") {
		t.Errorf("編集中の乙を書き換えました:\n%s", b)
	}
	// 索引も直る（新しい値で引ける・古い値では甲が当たらない）。
	if ids, _ := PagesByTag(database.DB, "区分", "見積"); len(ids) != 1 || ids[0] != 31 {
		t.Errorf("新しい値で引けません: %v", ids)
	}
}
