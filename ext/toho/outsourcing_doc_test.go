package toho

import (
	"strings"
	"testing"

	"w-cms/internal/auth"
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
//
// ⚠ **2026-09-28 に `資料` の列を宣言から外しました**——資料は加工製品ページの折りたたみ
// 「資料 <番号>」へ（order_docs.go・利用者:「外す」）。**それまでの本文の `資料` の列は、設定の語彙
// （`資料` は `ref`）で型が決まるので、押して飛べるまま**です（下の TestOutsourcingDocCellLinks）。

// TestOutsourcingHasDocColumn は、外注加工の宣言に `資料` の列が**参照（ref）で在る**ことを固定します。
//
// ⚠ 2026-09-28 に外し、2026-10-19 に戻しました。利用者:「資料の置き場をページ内に一つにして、資料一つ一つにいつものIDを付けましょう。ページID-いつものIDで資料を特定し、その連結IDを空白文字や「、」で区切って表の該当セルに羅列しておけば良いのでは？」
// 部材発注書はこの列を読みます（order_docs.go の docsCellOfLine）。参照でないと、並べた ID がリンクになりません。
func TestOutsourcingHasDocColumn(t *testing.T) {
	def, ok := cms.VocabDefByType("part-outsourcing")
	if !ok {
		t.Fatal("外注加工表の宣言がありません")
	}
	for _, c := range def.Columns {
		if c.Label == docsColumnLabel {
			if c.Type != cms.ColRef {
				t.Fatalf("⚠ `資料` の列が参照ではありません: %v", c.Type)
			}
			return
		}
	}
	t.Fatal("⚠ 外注加工表の宣言に `資料` の列がありません（部材発注書が資料を読めない・order_docs.go）")
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

	got := cms.RenderReferenceLinks(&auth.User{Username: "alice"}, body)
	if !strings.Contains(got, `href="/000037`) {
		t.Errorf("⚠ 資料が押せません（加工業者へ渡す図面へ飛べません）:\n%s", got)
	}
	// ⚠ **加工内容は触らない。** 自由文なので、参照の形に見える値が来ても
	// リンクにしてはいけません（2026-09-04 の事故の形）。
	if strings.Contains(got, `>塗装(黒半ツヤ)</a>`) {
		t.Errorf("参照でない列をリンクにしています:\n%s", got)
	}
}
