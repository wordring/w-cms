package cms

import (
	"strconv"
	"strings"
	"testing"
	"time"

	"w-cms/internal/cms/page"
)

// 表のセルの候補——値が数個に決まっている列を、暇なときに数えて置いておく（2026-10-19・cell_choices.go）。
//
// 利用者:「表のセルの候補は、サーバーが暇なときに DB を数えて置いておく形で作ってください。値が数個に決まっている列だけに出し、何個までかは設定に置いてください。」

func seedCellChoicePages(t *testing.T) {
	t.Helper()
	newTestFileDB(t)
	newTestTablesDB(t)
	withSettings(t, func(s *Settings) {
		s.CellChoices = &CellChoicesSettings{MaxValues: 3, MinRows: 4, IdleMinutes: 5, SkipTypes: []string{"date"}}
	})
	row := func(name, surface, date string) string {
		return `<tr><td>` + name + `</td><td>` + surface + `</td><td>` + date + `</td></tr>`
	}
	newPage(t, "000070", `<h1>部品</h1><table><caption>加工表</caption><tbody>`+
		`<tr><th>品名</th><th>表面</th><th>日付</th></tr>`+
		row("板1", "黒", "2026-10-01")+row("板2", "黒", "2026-10-02")+row("板3", "白", "2026-10-03")+
		row("板4", "黒", "2026-10-04")+row("板5", "", "2026-10-05")+row("板6", "", "2026-10-06")+
		`</tbody></table>`+
		`<table><caption>少ない表</caption><tbody><tr><th>区分</th></tr><tr><td>A</td></tr><tr><td>A</td></tr></tbody></table>`,
		page.PageMeta{Owner: "alice", Mode: "302"})
	newPage(t, "000071", `<h1>秘</h1><table><caption>加工表</caption><tbody>`+
		`<tr><th>品名</th><th>表面</th><th>日付</th></tr>`+row("板9", "金", "2026-10-09")+`</tbody></table>`,
		page.PageMeta{Owner: "bob", Mode: "300"})
}

func cellValues(vs []TagValueCount) string {
	var out []string
	for _, v := range vs {
		out = append(out, v.Value+":"+strconv.Itoa(v.Count))
	}
	return strings.Join(out, ",")
}


// TestCellChoicesCountFewValuedColumns は、値が数個に決まっている列だけに候補を置き、多い順に返すことを固定します。
//
//   - 違う値が max_values 個以下・値のある行が min_rows 行以上の列だけ（品名は値が多すぎる・少ない表は行が足りない）
//   - 空のセルは数えない。型が skip_types の列（日付）は数えない
//   - ⚠ 読めないページの値は出さない（bob のページの「金」は alice に見えない）
//   - 表と列の名前は正規化して引く（全角の英数字・前後の空白）
func TestCellChoicesCountFewValuedColumns(t *testing.T) {
	seedCellChoicePages(t)
	if v, at := CellChoiceCounts(alice, "加工表", "表面"); len(v) != 0 || !at.IsZero() {
		t.Fatalf("まだ数えていないのに候補があります: %v", v)
	}
	if err := SurveyCellChoices(); err != nil {
		t.Fatal(err)
	}
	if got, at := CellChoiceCounts(alice, "加工表", "表面"); cellValues(got) != "黒:3,白:1" || at.IsZero() {
		t.Errorf("表面の候補が違います: %s", cellValues(got))
	}
	if got, _ := CellChoiceCounts(admin, " 加工表 ", "表面"); cellValues(got) != "黒:3,白:1,金:1" {
		t.Errorf("読める人には全部のページの値が出るはず: %s", cellValues(got))
	}
	for _, c := range [][2]string{{"加工表", "品名"}, {"加工表", "日付"}, {"少ない表", "区分"}} {
		if got, _ := CellChoiceCounts(admin, c[0], c[1]); len(got) != 0 {
			t.Errorf("%s の %s に候補を出しています: %s", c[0], c[1], cellValues(got))
		}
	}
}

// TestCellChoicesNoSettingCountsNothing は、設定 cell_choices が無ければ数えないことを固定します（コードに既定値を持たない）。
func TestCellChoicesNoSettingCountsNothing(t *testing.T) {
	seedCellChoicePages(t)
	withSettings(t, func(s *Settings) { s.CellChoices = nil })
	cellChoicesMu.Lock()
	cellChoicesLast = nil
	cellChoicesMu.Unlock()
	if err := SurveyCellChoices(); err != nil {
		t.Fatal(err)
	}
	if got, _ := CellChoiceCounts(admin, "加工表", "表面"); len(got) != 0 {
		t.Errorf("設定が無いのに数えています: %s", cellValues(got))
	}
}

// TestCellChoicesDueOnlyWhenIdle は、数え直すのが「書き換わっていて、そのあと idle だけ何も書かれていない」ときだけなことを固定します。
func TestCellChoicesDueOnlyWhenIdle(t *testing.T) {
	cellWriteMu.Lock()
	saveW, saveS := cellLastWrite, cellLastSurveyed
	cellWriteMu.Unlock()
	t.Cleanup(func() {
		cellWriteMu.Lock()
		cellLastWrite, cellLastSurveyed = saveW, saveS
		cellWriteMu.Unlock()
	})
	now := time.Now()
	idle := 5 * time.Minute
	set := func(write, surveyed time.Time) {
		cellWriteMu.Lock()
		cellLastWrite, cellLastSurveyed = write, surveyed
		cellWriteMu.Unlock()
	}
	set(now.Add(-time.Minute), now.Add(-time.Hour))
	if cellChoicesDue(now, idle) {
		t.Error("書き換わって1分しかたっていないのに数え直します（暇ではない）")
	}
	set(now.Add(-10*time.Minute), now.Add(-time.Hour))
	if !cellChoicesDue(now, idle) {
		t.Error("書き換わって10分たったのに数え直しません")
	}
	set(now.Add(-10*time.Minute), now.Add(-5*time.Minute))
	if cellChoicesDue(now, idle) {
		t.Error("数えたあとに書き換わっていないのに数え直します")
	}
	markTablesWritten()
	cellWriteMu.Lock()
	fresh := time.Since(cellLastWrite) < time.Second
	cellWriteMu.Unlock()
	if !fresh {
		t.Error("markTablesWritten が書き換わった時刻を覚えていません")
	}
}

// TestCellChoicesSkipAllUniqueColumns は、どの値も1回しか出ない列（行ごとに違う値を書く列）に候補を出さないことを固定します
// （違う値の数が上限より少なくても——品名は6行・6つの値）。
func TestCellChoicesSkipAllUniqueColumns(t *testing.T) {
	seedCellChoicePages(t)
	withSettings(t, func(s *Settings) {
		s.CellChoices = &CellChoicesSettings{MaxValues: 10, MinRows: 4, IdleMinutes: 5, SkipTypes: []string{"date"}}
	})
	if err := SurveyCellChoices(); err != nil {
		t.Fatal(err)
	}
	if got, _ := CellChoiceCounts(alice, "加工表", "品名"); len(got) != 0 {
		t.Errorf("どの値も1回しか出ない品名に候補を出しています: %s", cellValues(got))
	}
	if got, _ := CellChoiceCounts(alice, "加工表", "表面"); cellValues(got) != "黒:3,白:1" {
		t.Errorf("表面の候補が違います: %s", cellValues(got))
	}
}
