package subcon

import (
	"strings"
	"testing"

	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// **外注加工の `資料` は押して飛べる**（2026-09-21）。
//
// ユーザー:「加工の発注には**私が書いた加工図面**も入れる場合があります。
// これまでは、**加工製品のページに貼っていました**」——貼る場所はそのままで、
// **発注の側はここから指します**（写しを作らない）。
//
// ⚠ **2026-09-21 まで `text` 型でした。** 「表のセルの参照はリンクにならない」という
// 残件が同日午前に片付いた（`linkRefCells`）のに、**型が `text` のままでは恩恵を
// 受けません**——宣言が `ref` であることが、リンクにする唯一の条件です
// （値の形だけでは参照と番号を見分けない・2026-09-04 の事故の教訓）。
//
// ⚠ **この番人が無いと、型を戻しても「エラーにならず、ただ押せなくなる」だけ**です。
//
// ⚠ 置き場が拡張側なのは、**`part-outsourcing` が下請けの語彙**だからです
// ——コアの試験からは見えません（最初コア側に書いて落ちました）。

// TestOutsourcingDocIsRef は、宣言が `ref` であることを固定します。
func TestOutsourcingDocIsRef(t *testing.T) {
	def, ok := cms.VocabDefByType("part-outsourcing")
	if !ok {
		t.Fatal("外注加工の宣言がありません")
	}
	for _, c := range def.Columns {
		if c.Label != "資料" {
			continue
		}
		if c.Type != cms.ColRef {
			t.Fatalf("⚠ `資料` の型が %q です（%q を期待——`text` だと押せません）",
				c.Type, cms.ColRef)
		}
		return
	}
	t.Fatal("`資料` の列がありません（加工業者へ渡す図面の置き場です）")
}

// TestOutsourcingDocCellLinks は、**実際にリンクになる**ことを固定します。
//
// ⚠ 宣言を見るだけでは足りません——描画の側が列の宣言を見ていなければ、
// 型を直しても押せないままです（**宣言と振る舞いは別**）。
func TestOutsourcingDocCellLinks(t *testing.T) {
	setupExtTest(t, "000036", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	addPage(t, 37, -1, "加工製品", "alice", "302", true)

	body := `<h1>加工製品</h1><table data-type="part-outsourcing"><tbody>` +
		`<tr><th>加工内容</th><th>支給</th><th>個数</th><th>資料</th><th>備考</th><th>区分</th></tr>` +
		`<tr><td>塗装(黒半ツヤ)</td><td>支給</td><td>1</td><td>000037-a1b2</td><td></td><td>現行</td></tr>` +
		`</tbody></table>`

	got := cms.RenderReferenceLinks(body)
	if !strings.Contains(got, `href="/000037`) {
		t.Errorf("⚠ 資料が押せません（加工業者へ渡す図面へ飛べません）:\n%s", got)
	}
	// ⚠ **加工内容は触らない。** 自由文なので、参照の形に見える値が来ても
	// リンクにしてはいけません（2026-09-04 の事故の形）。
	if strings.Contains(got, `>塗装(黒半ツヤ)</a>`) {
		t.Errorf("参照でない列をリンクにしています:\n%s", got)
	}
}
