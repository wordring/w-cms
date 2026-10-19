package toho

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// TestAutocompleteCountsMigratingPages は、`移行中` のページ（ワンノートから移して、まだ人が確かめていない加工製品ページ）の表も、
// オートコンプリートの候補に数えることを固定します（2026-10-19・internal/cms/autocomplete.go）。
//
// 利用者:「材料の形状や外注加工等の推奨業者に候補が出ないのは何故？」——表の写し（data/tables.db）は `移行中` のページを入れない
// （RegisterTablesExclusion・material_price.go）ので、写しから数えていた間は、職場の形状 202 行が1つも数えられていなかった。
//   - 候補は本文から数える（移行中も）。1回だけの値（パイプ）は出さない
//   - ⚠ 写しには、いままでどおり入らない（集計と検索に、確かめる前の移行のデータを混ぜない）
func TestAutocompleteCountsMigratingPages(t *testing.T) {
	setupExtTest(t, "000010", page.PageMeta{Owner: "alice", Mode: "302"})
	row := func(shape string) string { return `<tr><td>SS400</td><td>` + shape + `</td><td>t3</td><td>1</td></tr>` }
	body := `<h1>ブラケット</h1><dl data-type="tags"><dt>` + MigratingTag + `</dt><dd><br></dd></dl>` +
		`<table><caption>材料表</caption><tbody><tr><th>材質</th><th>形状</th><th>寸法</th><th>個数</th></tr>` +
		row("FB") + row("FB") + row("FB") + row("アングル") + row("アングル") + row("パイプ") + `</tbody></table>`
	addPage(t, 11, -1, "ブラケット", "alice", "302", true)
	writeBodyFile(t, 11, body)

	if err := cms.SurveyAutocomplete(); err != nil {
		t.Fatal(err)
	}
	got := cms.CellChoiceCounts(&auth.User{Username: "root", IsAdmin: true}, "材料表", "形状")
	var vs []string
	for _, v := range got {
		vs = append(vs, v.Value)
	}
	if strings.Join(vs, ",") != "FB,アングル" {
		t.Errorf("移行中のページの形状が候補に出ません（1回だけのパイプは出ないはず）: %v", vs)
	}
	if res, err := cms.QueryTable(&auth.User{Username: "root", IsAdmin: true}, cms.TableQuery{Table: "材料表"}); err == nil && len(res.Rows) > 0 {
		t.Errorf("⚠ 移行中のページの表が写しに入っています: %d 行", len(res.Rows))
	}
}
