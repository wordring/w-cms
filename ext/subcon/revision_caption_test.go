package subcon

import (
	"strings"
	"testing"
)

// 改訂履歴の表を、**`data-type` でも `<caption>` でも見つける**ことを固定します
// （2026-09-20）。
//
// ⚠ **形式の宣言は `data-type` 属性から見える文字（`<caption>`）へ移る途中**で、
// しばらく併存します（[docs/【考察】発注書から受注明細へ.md] §2.4）。
//
// ⚠ **片方しか見ていないと、caption へ移した日に改定が黙って積まれなくなります**
// ——`InsertRevisionRow` も `linkRevisionRow` も、表が見つからなければ**本文を
// そのまま返す**のでエラーが出ません。気づくのは「版が増えていない」と誰かが
// 思ったときです。だから**移す前に**この番人を置きました。

// revisionBody は改訂履歴を1版だけ持つ本文を、指定の書き方で組みます。
func revisionBody(caption bool) string {
	open := `<table data-type="` + revisionItemsType + `">`
	cap := ""
	if caption {
		open = `<table>`
		cap = `<caption>改訂明細</caption>`
	}
	return `<h1>ブラケット</h1>` +
		`<section data-id="aaaa"><h2>図面</h2>` +
		`<dl data-type="tags"><dt>図面番号</dt><dd>K120-1</dd></dl>` +
		`<section data-type="file-view" data-ref="000001-bbbb"></section>` +
		`</section>` +
		`<section data-id="cccc"><h2>改訂履歴</h2>` + open + cap + `<tbody>` +
		`<tr><th>版</th><th>図面番号</th><th>受領日</th></tr>` +
		`<tr data-id="r001"><td>1</td><td>K120-1</td><td>2026-09-01</td></tr>` +
		`</tbody></table></section>`
}

// TestInsertRevisionRowFindsBothForms は、**どちらの書き方でも版が積まれる**ことを
// 固定します。
func TestInsertRevisionRowFindsBothForms(t *testing.T) {
	for _, c := range []struct {
		name    string
		caption bool
	}{
		{"data-type で名乗る（既存の本文）", false},
		{"caption で名乗る（新しい書き方）", true},
	} {
		t.Run(c.name, func(t *testing.T) {
			body := revisionBody(c.caption)
			got := InsertRevisionRow(body, "K120-1A")

			if got == body {
				t.Fatalf("⚠ 表を見つけられず、本文をそのまま返しています（改定が黙って積まれません）:\n%s", body)
			}
			if !strings.Contains(got, "<td>K120-1A</td>") {
				t.Errorf("新しい版が入っていません:\n%s", got)
			}
			// **版は2になる**（通し番号が進む）。
			if !strings.Contains(got, "<td>2</td>") {
				t.Errorf("版が進んでいません:\n%s", got)
			}
			// ⚠ **見出し行の直後に入る**（新しい版が上）。古い版より前にあること。
			iNew := strings.Index(got, "<td>K120-1A</td>")
			iOld := strings.Index(got, `<tr data-id="r001">`)
			if iNew < 0 || iOld < 0 || iNew > iOld {
				t.Errorf("新しい版が上に来ていません:\n%s", got)
			}
		})
	}
}

// TestLinkRevisionRowFindsBothForms は、**旧版へのリンクもどちらの書き方でも付く**
// ことを固定します。
//
// ⚠ `InsertRevisionRow` と `linkRevisionRow` は**同じ表を別々に探していました**。
// 片方だけ直すと、版は積まれるのにリンクだけ静かに付かなくなります。
func TestLinkRevisionRowFindsBothForms(t *testing.T) {
	for _, caption := range []bool{false, true} {
		body := revisionBody(caption)
		got := linkRevisionRow(body, "K120-1", "000123")
		if got == body {
			t.Errorf("caption=%v でリンクが付いていません:\n%s", caption, body)
		}
		if !strings.Contains(got, `<a href="/000123">K120-1</a>`) {
			t.Errorf("caption=%v のリンクが違います:\n%s", caption, got)
		}
	}
}
