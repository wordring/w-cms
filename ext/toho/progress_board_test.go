package toho

import (
	"strings"
	"testing"

	"w-cms/internal/cms/page"
)

// TestProgressBoard は「進捗ボード」を固定します（2026-10-21 夜・【要求】製造 §5 の下書き）。
//
//   - 案件残表と同じ行を、段（未着手・製造待ち・製造中・製造完了（納品待ち））ごとに並べる
//   - 行に、この案件×加工製品に当たる部材発注書と、その行の状態が出る
func TestProgressBoard(t *testing.T) {
	setupExtTest(t, "000500", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	addPage(t, 501, -1, "受注", "alice", "302", true)
	row := func(our, item, name, qty string) string {
		return `<tr><td>` + our + `</td><td>` + item + `</td><td>` + name + `</td><td>` + qty + `</td><td>個</td><td>100</td><td></td><td></td><td></td><td>未着手</td></tr>`
	}
	seedOrder(t, 502, 501, "あけぼの精工", "2026-10-30", row("000031", "A-1", "ブラケット", "5")+row("", "A-2", "まだ何も", "3"))
	addPage(t, 503, 501, "部材発注", "alice", "302", true)
	po := `<h1>部材発注</h1><table><caption>部材発注明細表</caption><tbody><tr><th>弊社品番</th><th>案件</th><th>材質</th><th>数量</th><th>状態</th></tr>` +
		`<tr><td>000031</td><td>000502</td><td>鉄</td><td>2</td><td>発注済</td></tr>` +
		`<tr><td>000031</td><td>000502</td><td>SUS</td><td>1</td><td>未発注</td></tr></tbody></table>`
	seedBody(t, "000503", po)

	out := progressBoardHTML(adminUser(), 500)
	for _, want := range []string{"📊 進捗ボード", "未着手 2", "<summary>未着手（2）</summary>", "ブラケット", "まだ何も", `href="/000503">/000503</a> 発注済・未発注`, "2026-10-30"} {
		if !strings.Contains(out, want) {
			t.Errorf("⚠ 進捗ボードに %q がありません:\n%s", want, out)
		}
	}
	// 段の決め方。
	for _, tc := range []struct {
		r    backlogRow
		want string
	}{
		{backlogRow{}, "未着手"},
		{backlogRow{Waiting: 2}, MfgWaitLabel},
		{backlogRow{Waiting: 1, MfgPages: []string{"000600"}}, "製造中"},
		{backlogRow{MfgPages: []string{"000600"}, Made: MarkDone}, "製造完了（納品待ち）"},
	} {
		if got := progressStageOf(tc.r); got != tc.want {
			t.Errorf("段が %q です（%q を期待）: %+v", got, tc.want, tc.r)
		}
	}
}
