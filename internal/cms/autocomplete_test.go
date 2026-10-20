package cms

import (
	"encoding/json"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// オートコンプリート——何度も入力される定型的な値を、暇なときに DB から拾って候補にする（2026-10-19・autocomplete.go）。
//
// 利用者:「オートコンプリートのコンボボックスは、一回しか入力されたことが無い値は出さないようにしましょう。何度も入力される定型的な値をDBから拾って決定する仕組みを作りましょう。」

func autoValues(vs []TagValueCount) string {
	var out []string
	for _, v := range vs {
		out = append(out, v.Value+":"+strconv.Itoa(v.Count))
	}
	return strings.Join(out, ",")
}

// withAutocomplete は試験の数え方を当てます（min_value_count 2・min_share 0.8・min_rows 4・max_values 3）。
func withAutocomplete(t *testing.T) {
	withSettings(t, func(s *Settings) {
		s.Autocomplete = &AutocompleteSettings{MinValueCount: 2, MinShare: 0.8, MinRows: 4, MaxValues: 3, IdleMinutes: 5, SkipTypes: []string{"date"}}
	})
}

func seedAutocompletePages(t *testing.T) {
	t.Helper()
	newTestFileDB(t)
	newTestTablesDB(t)
	withAutocomplete(t)
	row := func(name, surface, work, date string) string {
		return `<tr><td>` + name + `</td><td>` + surface + `</td><td>` + work + `</td><td>` + date + `</td></tr>`
	}
	// 表面: 黒4・白2・灰1（定型の値が 6/7 = 86%）。加工: 曲げ2・切断2・溶接1・穴1・塗装1（定型 4/7 = 57%——定型の欄でない）。
	newPage(t, "000070", `<h1>部品</h1><dl data-type="tags"><dt>種類</dt><dd>部品</dd></dl>`+
		`<table><caption>加工表</caption><tbody><tr><th>品名</th><th>表面</th><th>加工</th><th>日付</th></tr>`+
		row("板1", "黒", "曲げ", "2026-10-01")+row("板2", "黒", "曲げ", "2026-10-02")+row("板3", "白", "切断", "2026-10-01")+
		row("板4", "黒", "切断", "2026-10-01")+row("板5", "白", "溶接", "2026-10-01")+row("板6", "黒", "穴", "2026-10-01")+
		row("板7", "灰", "塗装", "2026-10-01")+row("板8", "", "", "")+
		`</tbody></table>`, page.PageMeta{Owner: "alice", Mode: "302"})
	newPage(t, "000071", `<h1>秘</h1><table><caption>加工表</caption><tbody><tr><th>品名</th><th>表面</th><th>加工</th><th>日付</th></tr>`+
		row("板9", "白", "曲げ", "2026-10-09")+`</tbody></table>`, page.PageMeta{Owner: "bob", Mode: "300"})
	for i, kind := range []string{"部品", "部品", "装置", "装置", "部品"} {
		id := "00008" + strconv.Itoa(i)
		newPage(t, id, `<h1>`+id+`</h1><dl data-type="tags"><dt>種類</dt><dd>`+kind+`</dd><dt>品名</dt><dd>品`+id+`</dd></dl>`,
			page.PageMeta{Owner: "alice", Mode: "302"})
	}
}

// TestAutocompleteCellsOnlySteadyValues は、表のセルの候補が「定型の値（2回以上）」だけで、定型の欄だけに出ることを固定します。
//
//   - 1回しか入力されていない値（灰）は出さない
//   - 定型の値が行の min_share に届かない欄（加工 57%）・行ごとに違う値の欄（品名）は出さない
//   - 型が skip_types の欄（日付）は数えない
//   - ⚠ 読めないページの行は数えない（bob の白は alice には数えない）
//   - 多い順・最大 max_values 個
func TestAutocompleteCellsOnlySteadyValues(t *testing.T) {
	seedAutocompletePages(t)
	autoMu.Lock()
	autoLast = nil
	autoMu.Unlock()
	if v := CellChoiceCounts(alice, "加工表", "表面"); len(v) != 0 {
		t.Fatalf("まだ数えていないのに候補があります: %v", v)
	}
	if err := SurveyAutocomplete(); err != nil {
		t.Fatal(err)
	}
	if got := CellChoiceCounts(alice, "加工表", "表面"); autoValues(got) != "黒:4,白:2" {
		t.Errorf("表面の候補が違います（灰は1回だけ）: %s", autoValues(got))
	}
	if got := CellChoiceCounts(admin, " 加工表 ", "表面"); autoValues(got) != "黒:4,白:3" {
		t.Errorf("読める人には全部のページの行が数えられるはず: %s", autoValues(got))
	}
	for _, col := range []string{"品名", "加工", "日付"} {
		if got := CellChoiceCounts(admin, "加工表", col); len(got) != 0 {
			t.Errorf("加工表の %s に候補を出しています: %s", col, autoValues(got))
		}
	}
}

// TestAutocompleteTagsCountPages は、タグの値の候補もページの数で数え、1回しか出ない値と、定型でないタグ（品名）を出さないことを固定します。
func TestAutocompleteTagsCountPages(t *testing.T) {
	seedAutocompletePages(t)
	if err := SurveyAutocomplete(); err != nil {
		t.Fatal(err)
	}
	if got := tagValueCounts(alice, "種類"); autoValues(got) != "部品:4,装置:2" {
		t.Errorf("種類の候補が違います: %s", autoValues(got))
	}
	if got := tagValueCounts(alice, "品名"); len(got) != 0 {
		t.Errorf("ページごとに違う品名に候補を出しています: %s", autoValues(got))
	}
}

// TestAutocompleteMaxValues は、候補を多い順に最大 max_values 個に切ることを固定します。
func TestAutocompleteMaxValues(t *testing.T) {
	seedAutocompletePages(t)
	withSettings(t, func(s *Settings) {
		s.Autocomplete = &AutocompleteSettings{MinValueCount: 2, MinShare: 0.8, MinRows: 4, MaxValues: 1, IdleMinutes: 5}
	})
	if err := SurveyAutocomplete(); err != nil {
		t.Fatal(err)
	}
	if got := CellChoiceCounts(alice, "加工表", "表面"); autoValues(got) != "黒:4" {
		t.Errorf("max_values 1 なのに: %s", autoValues(got))
	}
}

// TestAutocompleteNoSettingCountsNothing は、設定 autocomplete が無ければ数えないことを固定します（コードに既定値を持たない）。
func TestAutocompleteNoSettingCountsNothing(t *testing.T) {
	seedAutocompletePages(t)
	withSettings(t, func(s *Settings) { s.Autocomplete = nil })
	autoMu.Lock()
	autoLast = nil
	autoMu.Unlock()
	if err := SurveyAutocomplete(); err != nil {
		t.Fatal(err)
	}
	if got := CellChoiceCounts(admin, "加工表", "表面"); len(got) != 0 {
		t.Errorf("設定が無いのに数えています: %s", autoValues(got))
	}
}

// TestAutocompleteDueOnlyWhenIdle は、数え直すのが「書き換わっていて、そのあと idle だけ何も書かれていない」ときだけなことを固定します。
func TestAutocompleteDueOnlyWhenIdle(t *testing.T) {
	newTestFileDB(t)
	newTestTablesDB(t)
	autoWriteMu.Lock()
	saveW, saveS := autoLastWrite, autoLastSurvey
	autoWriteMu.Unlock()
	t.Cleanup(func() {
		autoWriteMu.Lock()
		autoLastWrite, autoLastSurvey = saveW, saveS
		autoWriteMu.Unlock()
	})
	now := time.Now()
	idle := 5 * time.Minute
	set := func(write, surveyed time.Time) {
		autoWriteMu.Lock()
		autoLastWrite, autoLastSurvey = write, surveyed
		autoWriteMu.Unlock()
	}
	set(now.Add(-time.Minute), now.Add(-time.Hour))
	if autocompleteDue(now, idle) {
		t.Error("書き換わって1分しかたっていないのに数え直します（暇ではない）")
	}
	set(now.Add(-10*time.Minute), now.Add(-time.Hour))
	if !autocompleteDue(now, idle) {
		t.Error("書き換わって10分たったのに数え直しません")
	}
	set(now.Add(-10*time.Minute), now.Add(-5*time.Minute))
	if autocompleteDue(now, idle) {
		t.Error("数えたあとに書き換わっていないのに数え直します")
	}
	newPage(t, "000090", "<h1>x</h1>", page.PageMeta{Owner: "alice", Mode: "302"}) // 中で SyncIndex を通る
	autoWriteMu.Lock()
	fresh := time.Since(autoLastWrite) < time.Second
	autoWriteMu.Unlock()
	if !fresh {
		t.Error("SyncIndex が書き換わった時刻を覚えていません")
	}
}

// TestAutocompleteSavedAsJSON は、数えた結果を data/autocomplete.json に置き、起こし直したあと読み戻せることを固定します（2026-10-19）。
// 利用者:「オートコンプリートの候補一覧はJSON形式で保存しても良いかもしれませんね。それならエディタから読み込むのも容易です」
//   - ファイルには欄ごとの定型の値と、それを持つページ（読めるページだけを数えるため）が入る
//   - 読み戻したあとも、読めるページだけで数える（alice には bob の白が数えられない）
func TestAutocompleteSavedAsJSON(t *testing.T) {
	seedAutocompletePages(t)
	if err := SurveyAutocomplete(); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(database.DataPath(autocompleteFile))
	if err != nil {
		t.Fatalf("data/autocomplete.json がありません: %v", err)
	}
	var file autoSurvey
	if err := json.Unmarshal(data, &file); err != nil {
		t.Fatal(err)
	}
	if got := file.Cells["加工表"]["表面"]; len(got) != 2 || got[0].Value != "黒" || got[0].Pages["000070"] != 4 {
		t.Errorf("ファイルの表面が違います: %+v", got)
	}
	if got := file.Tags["種類"]; len(got) != 2 || got[0].Value != "部品" {
		t.Errorf("ファイルの種類が違います: %+v", got)
	}
	autoMu.Lock()
	autoLast = nil // 起こし直した——メモリは空
	autoMu.Unlock()
	if err := loadAutocompleteFile(); err != nil {
		t.Fatal(err)
	}
	if got := CellChoiceCounts(alice, "加工表", "表面"); autoValues(got) != "黒:4,白:2" {
		t.Errorf("読み戻した候補が違います: %s", autoValues(got))
	}
	list := AutocompleteFor(alice)
	if autoValues(list.Tags["種類"]) != "部品:4,装置:2" || list.SurveyedAt == "" {
		t.Errorf("一覧が違います: %+v", list)
	}
}

// TestAutocompleteReadsBodiesNotTablesCopy は、表の候補を本文から数え、表の写し（data/tables.db）に頼らないことを固定します（2026-10-19）。
// 利用者:「材料の形状や外注加工等の推奨業者に候補が出ないのは何故？」——写しは `移行中` のページを入れないので、ワンノートから
// 移した加工製品ページの値が1つも数えられていなかった。ここでは写しを空にしても候補が出ることを見る。
func TestAutocompleteReadsBodiesNotTablesCopy(t *testing.T) {
	seedAutocompletePages(t)
	if err := ResetTablesDB(); err != nil {
		t.Fatal(err)
	}
	if err := SurveyAutocomplete(); err != nil {
		t.Fatal(err)
	}
	if got := CellChoiceCounts(alice, "加工表", "表面"); autoValues(got) != "黒:4,白:2" {
		t.Errorf("写しを空にしたら候補が消えました（本文から数えていない）: %s", autoValues(got))
	}
}
