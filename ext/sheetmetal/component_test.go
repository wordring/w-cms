package sheetmetal

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 構成部品（材料・外注加工・購入部品）のテスト。
//
// ユーザー:「構成部品は私たちが図面から抽出します。材料も抽出しますし、外注加工、
// 購入部品も抽出します。**外注加工の時に構成部品の番号が効いてきます**」
// 「構成部品は図面の改定に伴って**廃版**になる場合があります」
//
// 固定するのは2つ:
//   - 行が索引に載る（社内コードで引ける・種別ごとに分かれている）
//   - **廃版は状態であって削除ではない**（行は消えない・表示だけ薄くなる）

const componentsBody = `<h1>脚取付台</h1>` +
	`<table data-type="part-outsourcing" data-id="t001"><tbody>` +
	`<tr><th>加工内容</th><th>支給品</th><th>数量</th><th>加工先</th><th>単価</th><th>状態</th></tr>` +
	`<tr data-id="k3x9"><td>曲げ加工</td><td>有</td><td>1</td><td>加工屋B</td><td>3000</td><td>有効</td></tr>` +
	`<tr data-id="p7m1"><td>旧・溶接</td><td>有</td><td>1</td><td>加工屋C</td><td>5000</td><td>廃版</td></tr>` +
	`</tbody></table>`

// TestComponentRowsAreIndexed は、構成部品の行が索引に載り、**行のIDが社内コードの
// 後半として引ける**ことを固定します。外注加工に出す紙に載る番号なので、
// ここが効かないと相手からの問い合わせに答えられません。
func TestComponentRowsAreIndexed(t *testing.T) {
	const id = "000012"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Mode: "330"})
	if err := cms.SyncIndex(id, componentsBody); err != nil {
		t.Fatalf("索引エラー: %v", err)
	}

	idInt, _ := strconv.Atoi(id)
	rows, err := cms.VocabTableRowsOf(database.DB, idInt, "part-outsourcing")
	if err != nil {
		t.Fatalf("索引を読めません: %v", err)
	}
	if len(rows) != 2 {
		t.Fatalf("外注加工の行が2つありません: %+v", rows)
	}
	if rows[0].Values["work"] != "曲げ加工" || rows[0].Values["supplier-name"] != "加工屋B" {
		t.Errorf("列が読めていません: %+v", rows[0].Values)
	}
	if rows[1].Values["status"] != "廃版" {
		t.Errorf("廃版の状態が入っていません: %+v", rows[1].Values)
	}
}

// TestObsoleteRowIsMarkedNotRemoved は、廃版の行が**消えず**に印だけ付くことを
// 固定します。行を消すと、外注加工に出した紙の社内コードが指す先が無くなります。
func TestObsoleteRowIsMarkedNotRemoved(t *testing.T) {
	const id = "000012"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Mode: "330"})

	idInt, _ := strconv.Atoi(id)
	req := auth.WithUser(httptest.NewRequest("GET", "/"+id, nil), &auth.User{Username: "alice"})
	out := cms.RenderComputedViews(req, idInt, cms.Sanitize(componentsBody))

	if strings.Count(out, "row-obsolete") != 1 {
		t.Fatalf("廃版の印が1つではありません:\n%s", out)
	}
	// **行は残っている**（印が付くだけ）。
	if !strings.Contains(out, "旧・溶接") {
		t.Errorf("廃版の行が消えています:\n%s", out)
	}
	if !strings.Contains(out, `data-id="p7m1"`) {
		t.Errorf("廃版の行のIDが消えています（社内コードの指し先が失われる）:\n%s", out)
	}
	// 有効な行には印が付かない。
	obs := strings.Index(out, "row-obsolete")
	if live := strings.Index(out, "曲げ加工"); obs < live {
		t.Errorf("有効な行に印が付いています:\n%s", out)
	}
}
