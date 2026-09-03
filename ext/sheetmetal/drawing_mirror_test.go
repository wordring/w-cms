package sheetmetal

import (
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// TestSupersededDrawingGetsMarked は、2つ目以降の図面ブロックに表示のときだけ
// 印が付くことを固定します。
//
// ユーザー:「既存の図面は古いとわかるように赤枠で囲み、ユーザーの判断で消します」。
// **先頭以外が古い**——改定図面は先頭へ差し込まれるので、並びそのものが最新を表します。
// 印は保存されません（class はサニタイズで落ちる）。
func TestSupersededDrawingGetsMarked(t *testing.T) {
	const id = "000012"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Mode: "330"})

	body := "<h1>脚取付台</h1>" +
		`<section><h2>図面</h2><dl><dt>図面番号</dt><dd>Y050-1A</dd></dl></section>` +
		`<section><h2>図面</h2><dl><dt>図面番号</dt><dd>Y050-1</dd></dl></section>`

	idInt, _ := strconv.Atoi(id)
	req := auth.WithUser(httptest.NewRequest("GET", "/"+id, nil), &auth.User{Username: "alice"})
	out := cms.RenderComputedViews(req, idInt, cms.Sanitize(body))

	if strings.Count(out, "drawing-superseded") != 1 {
		t.Fatalf("古い図面の印が1つではありません:\n%s", out)
	}
	// 印が付くのは**後ろのほう**（先頭が最新）。
	mark := strings.Index(out, "drawing-superseded")
	newest := strings.Index(out, "Y050-1A")
	if mark < newest {
		t.Errorf("先頭（最新）に印が付いています:\n%s", out)
	}

	// **保存はされない**——サニタイズを通せば印は落ちる。
	if strings.Contains(cms.Sanitize(out), "drawing-superseded") {
		t.Errorf("印が保存経路を通り抜けています（class はサニタイズで落ちるはず）")
	}
}
