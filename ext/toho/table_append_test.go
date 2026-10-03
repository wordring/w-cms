package toho

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// TestAppendTableRows は表の末尾へ行を足す口（2026-10-04・table_append.go）を固定します——列は見出しの言葉で合わせ、空の取っ掛かりの
// 行は取り除き、ある行には触らない。表に無い見出しの値・無い表は断って何も書かない。管理者だけ。
func TestAppendTableRows(t *testing.T) {
	const id = "000050"
	setupExtTest(t, id, page.PageMeta{Owner: "root", Mode: "330"})
	seedBody(t, id, `<h1>取付ベース</h1>`+
		`<table><caption>外注加工</caption><tbody><tr><th>番号</th><th>名称</th><th>加工内容</th><th>表面</th><th>個数</th><th>推奨業者</th><th>備考</th></tr>`+
		`<tr><td></td><td></td><td></td><td></td><td></td><td></td><td></td></tr></tbody></table>`+
		`<table><caption>材料</caption><tbody><tr><th>材質</th><th>寸法</th></tr><tr><td>SS400</td><td>t6</td></tr></tbody></table>`)
	root := &auth.User{Username: "root", IsAdmin: true}
	row := map[string]string{"番号": "1", "加工内容": "塗装", "表面": "緑", "推奨業者": "ふじ鍍金", "備考": "見積 2026-08-07"}
	if code, _ := postRFQ(t, &auth.User{Username: "bob"}, AppendTableRowsAPIHandler, map[string]any{"page_id": id, "caption": "外注加工", "rows": []map[string]string{row}}); code != 403 {
		t.Errorf("管理者でない人が足せてしまう: %d", code)
	}
	before := readPageBody(t, id)
	if code, _ := postRFQ(t, root, AppendTableRowsAPIHandler, map[string]any{"page_id": id, "caption": "外注加工", "rows": []map[string]string{{"支給": "x"}}}); code != 409 {
		t.Errorf("表に無い列の値で %d", code)
	}
	if code, _ := postRFQ(t, root, AppendTableRowsAPIHandler, map[string]any{"page_id": id, "caption": "購入部品", "rows": []map[string]string{row}}); code != 409 {
		t.Errorf("無い表で %d", code)
	}
	if readPageBody(t, id) != before {
		t.Fatal("断ったのに本文が変わりました")
	}
	if code, out := postRFQ(t, root, AppendTableRowsAPIHandler, map[string]any{"page_id": id, "caption": "外注加工", "rows": []map[string]string{row}}); code != 200 || out["rows"] != float64(1) {
		t.Fatalf("足せません: %d %v", code, out)
	}
	body := readPageBody(t, id)
	if !strings.Contains(body, "<tr><td>1</td><td></td><td>塗装</td><td>緑</td><td></td><td>ふじ鍍金</td><td>見積 2026-08-07</td></tr></tbody>") {
		t.Errorf("見出しに合わせて足していません:\n%s", body)
	}
	if strings.Count(body, "<tr><td></td><td></td><td></td>") != 0 {
		t.Errorf("空の取っ掛かりの行が残っています:\n%s", body)
	}
	if !strings.Contains(body, "<tr><td>SS400</td><td>t6</td></tr>") {
		t.Errorf("ほかの表が変わりました:\n%s", body)
	}
	// 2回目はある行（1行目）の後ろへ足す——ある行には触らない。
	row2 := map[string]string{"番号": "2", "加工内容": "溶断"}
	postRFQ(t, root, AppendTableRowsAPIHandler, map[string]any{"page_id": id, "caption": "外注加工", "rows": []map[string]string{row2}})
	if b := readPageBody(t, id); !strings.Contains(b, "<td>見積 2026-08-07</td></tr><tr><td>2</td><td></td><td>溶断</td>") {
		t.Errorf("ある行の後ろへ足していません:\n%s", b)
	}
}
