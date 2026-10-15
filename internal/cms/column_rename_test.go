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
)

// 表の列の名前を置き換える（2026-10-15・column_rename.go）。利用者:「受注フォルダの表の「出荷済み」を出荷済数量に
// 変更します」→ 列ごと替える——設定の語彙で列の名前を変えたら、既にあるページの表の見出しも揃える。

func TestRenameColumnOnlyTouchesTheCaptionedTableHeader(t *testing.T) {
	// エディタで保存した本文は字下げされる（作業引き継ぎの罠）——見出しのセルの前後に空白・属性があっても当てる。
	body := "<h1>出荷済み</h1><p>出荷済み</p>" +
		"<table><caption> 甲明細 </caption><tbody><tr><th>品名</th><th data-type=\"number\">\n  出荷済み\n</th></tr>" +
		"<tr><td>出荷済み</td><td>3</td></tr></tbody></table>" +
		"<table><caption>乙明細</caption><tbody><tr><th>出荷済み</th></tr></tbody></table>" +
		"<dl data-type=\"tags\"><dt>出荷済み</dt><dd>1</dd></dl>"
	got, conflict := renameColumn(body, "甲明細", "出荷済み", "出荷済数量")
	if conflict {
		t.Errorf("ぶつかっていないのに conflict と答えました")
	}
	if !strings.Contains(got, "<th data-type=\"number\">出荷済数量</th>") {
		t.Errorf("甲明細の見出しが置き換わっていません:\n%s", got)
	}
	// 題・地の文・セルの値・ほかの表・タグには触らない。
	for _, keep := range []string{"<h1>出荷済み</h1>", "<p>出荷済み</p>", "<td>出荷済み</td>",
		"<caption>乙明細</caption><tbody><tr><th>出荷済み</th>", "<dt>出荷済み</dt>"} {
		if !strings.Contains(got, keep) {
			t.Errorf("甲明細の見出しの外まで置き換えました（%s が消えた）:\n%s", keep, got)
		}
	}
	// 新しい名前の列が既にある表は書き換えない（列が2つになる）。
	both := "<table><caption>甲明細</caption><tbody><tr><th>出荷済み</th><th>出荷済数量</th></tr></tbody></table>"
	if got, conflict := renameColumn(both, "甲明細", "出荷済み", "出荷済数量"); got != both || !conflict {
		t.Errorf("新しい名前の列があるのに書き換えたか、conflict と答えません: %v\n%s", conflict, got)
	}
	// 置き換え先の $ は文字のまま。
	if got, _ := renameColumn(body, "甲明細", "出荷済み", "$1"); !strings.Contains(got, ">$1</th>") {
		t.Errorf("置き換え先の $ が展開されました:\n%s", got)
	}
}

func TestColumnRenameAPIRewritesPagesAndSkipsEditing(t *testing.T) {
	newTestFileDB(t)
	meta := page.PageMeta{Owner: "alice", Mode: page.DefaultMode, ParentID: TopPageID}
	table := func(cap string) string {
		return `<table><caption>` + cap + `</caption><tbody><tr><th>品名</th><th>出荷済み</th></tr><tr><td>板</td><td>2</td></tr></tbody></table>`
	}
	newPage(t, "000041", `<h1>甲</h1>`+table("甲明細"), meta)
	newPage(t, "000042", `<h1>乙</h1>`+table("甲明細"), meta)
	newPage(t, "000043", `<h1>丙</h1>`+table("乙明細"), meta)
	// 乙は編集中——飛ばす。
	editlock.Locks.TryAcquire(42, "bob", "tok")
	t.Cleanup(func() { editlock.Locks.ForceRelease(42) })

	post := func(u *auth.User, req map[string]any) (int, map[string]any) {
		b, _ := json.Marshal(req)
		r := httptest.NewRequest("POST", "/api/admin/column-rename", bytes.NewReader(b))
		r.Header.Set("Content-Type", "application/json")
		r = auth.WithUser(r, u)
		rr := httptest.NewRecorder()
		ColumnRenameAPIHandler(rr, r)
		var got map[string]any
		_ = json.Unmarshal(rr.Body.Bytes(), &got)
		return rr.Code, got
	}
	admin := &auth.User{Username: "root", IsAdmin: true}
	req := map[string]any{"caption": "甲明細", "from": "出荷済み", "to": "出荷済数量", "dry": true}

	if code, _ := post(&auth.User{Username: "alice"}, req); code != 403 {
		t.Errorf("管理者でない人が置き換えられます: %d", code)
	}
	ids := func(res map[string]any, k string) string {
		var s []string
		for _, v := range res[k].([]any) {
			s = append(s, v.(string))
		}
		return strings.Join(s, ",")
	}
	// dry は数えるだけ。
	code, got := post(admin, req)
	res, _ := got["result"].(map[string]any)
	if code != 200 || ids(res, "changed") != "000041" || ids(res, "editing") != "000042" {
		t.Fatalf("dry の数が違います: %d %v", code, got)
	}
	if b, _ := os.ReadFile(page.BodyPath("000041")); !strings.Contains(string(b), "<th>出荷済み</th>") {
		t.Errorf("dry なのに書き換えました:\n%s", b)
	}

	req["dry"] = false
	code, got = post(admin, req)
	res, _ = got["result"].(map[string]any)
	if code != 200 || ids(res, "changed") != "000041" {
		t.Fatalf("置き換えの結果が違います: %d %v", code, got)
	}
	if b, _ := os.ReadFile(page.BodyPath("000041")); !strings.Contains(string(b), "<th>出荷済数量</th>") || strings.Contains(string(b), "<th>出荷済み</th>") {
		t.Errorf("甲が置き換わっていません:\n%s", b)
	}
	if b, _ := os.ReadFile(page.BodyPath("000042")); !strings.Contains(string(b), "<th>出荷済み</th>") {
		t.Errorf("編集中の乙を書き換えました:\n%s", b)
	}
	if b, _ := os.ReadFile(page.BodyPath("000043")); !strings.Contains(string(b), "<th>出荷済み</th>") {
		t.Errorf("キャプションの違う丙を書き換えました:\n%s", b)
	}
}
