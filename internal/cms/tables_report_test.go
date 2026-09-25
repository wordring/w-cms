package cms

import (
	"os"
	"path/filepath"
	"testing"

	"w-cms/internal/cms/page"
)

// 「列の揃っていない表」の一覧（2026-09-25・DBの日本語化 §7 の2段目）。

// writeAndSync は本文をファイルへ書き、同期します（一覧は本文のファイルを読み直すため）。
func writeAndSync(t *testing.T, id, body string) {
	t.Helper()
	p := page.BodyPath(id)
	if err := os.MkdirAll(filepath.Dir(p), 0755); err != nil {
		t.Fatalf("MkdirAll: %v", err)
	}
	if err := os.WriteFile(p, []byte(body), 0644); err != nil {
		t.Fatalf("WriteFile: %v", err)
	}
	if err := SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndex: %v", err)
	}
}

func orderTable(heads ...string) string {
	b := `<h1>p</h1><table><caption>受注明細</caption><tbody><tr>`
	for _, h := range heads {
		b += `<th>` + h + `</th>`
	}
	b += `</tr><tr>`
	for range heads {
		b += `<td>x</td>`
	}
	return b + `</tr></tbody></table>`
}

func reportOf(t *testing.T, name string) TableReport {
	t.Helper()
	list, err := TablesReport()
	if err != nil {
		t.Fatalf("TablesReport: %v", err)
	}
	for _, r := range list {
		if r.Name == name {
			return r
		}
	}
	t.Fatalf("表 %q が一覧にありません: %+v", name, list)
	return TableReport{}
}

func columnOf(r TableReport, name string) (ColumnReport, bool) {
	for _, c := range r.Columns {
		if c.Name == name {
			return c, true
		}
	}
	return ColumnReport{}, false
}

// TestTablesReportFindsTypoColumn は、⚠ **ほかのページに無い見出し（打ち間違いの疑い）**を
// 見つけ、直したあとは**跡**として言うことを固定します。
func TestTablesReportFindsTypoColumn(t *testing.T) {
	newTestFileDB(t)
	newTestTablesDB(t)
	writeAndSync(t, "000060", orderTable("品番", "品名", "備考"))
	writeAndSync(t, "000061", orderTable("品番", "品名", "備考"))
	writeAndSync(t, "000062", orderTable("品 番", "品名", "備考")) // 打ち間違い
	writeAndSync(t, "000063", `<h1>q</h1><table><caption>旧表</caption><tbody><tr><th>a</th></tr><tr><td>1</td></tr></tbody></table>`)

	r := reportOf(t, "受注明細")
	if r.Pages != 3 || r.Rows != 3 {
		t.Errorf("ページ %d・行 %d（3・3 のはず）", r.Pages, r.Rows)
	}
	typo, ok := columnOf(r, "品 番")
	if !ok || !typo.Suspect || typo.Pages != 1 || !typo.NeedsQuote {
		t.Errorf("⚠ 打ち間違いの列を疑っていません: %+v", typo)
	}
	// ⚠ **全員が持つ列は疑わない**（値の数ではなく見出しで数える）。
	if c, _ := columnOf(r, "品名"); c.Suspect || c.Pages != 3 {
		t.Errorf("全ページにある列を疑っています: %+v", c)
	}
	if r.Suspects != 1 {
		t.Errorf("疑いの数 %d（1 のはず）", r.Suspects)
	}

	// 打ち間違いを直し、旧表を消す——DB には列と表が残る（跡）。
	writeAndSync(t, "000062", orderTable("品番", "品名", "備考"))
	writeAndSync(t, "000063", `<h1>q</h1><p>表を消した</p>`)
	r = reportOf(t, "受注明細")
	if r.Suspects != 0 {
		t.Errorf("直したのにまだ疑っています: %+v", r)
	}
	if c, ok := columnOf(r, "品 番"); !ok || !c.Leftover {
		t.Errorf("直した見出しの跡を言っていません: %+v", r.Columns)
	}
	if old := reportOf(t, "旧表"); !old.Leftover || old.Rows != 0 {
		t.Errorf("本文にはもう無い表を跡と言っていません: %+v", old)
	}
}
