package cms

// ─────────────────────────────────────────────────────────────────────────
// オートコンプリート——何度も入力される定型的な値を、暇なときに DB から拾って候補にする（2026-10-19）
//
// 利用者:「表のセルに入る値が数個に決まっている場合があります。タグも同じです。これは列やタグの名前に対してどんな値が
// 何個あるか調べればわかります。これをオートコンプリートのようにコンボボックスで選ぶように出来ないでしょうか？この調査は
// 暇なときにDBに対して自動で行うようにできますか？」「オートコンプリートはコアの機能です」
// 「いま、表の推奨業者に候補が出る仕組みがあると思いますが、これは冗長です。削除してオートコンプリートのコンボボックスで
// 統一しましょう。オートコンプリートのコンボボックスは、一回しか入力されたことが無い値は出さないようにしましょう。何度も
// 入力される定型的な値をDBから拾って決定する仕組みを作りましょう。」
// 「オートコンプリートの候補一覧はJSON形式で保存しても良いかもしれませんね。それならエディタから読み込むのも容易です」
//
//   - 数えるのは、表のセル（各ページの本文のキャプションのある表）と、タグの値（cms.db の page_tags）。
//     表はキャプション＋列の見出し、タグは名前で、1つの「欄」として数える。テンプレートの中は数えない。
//     ⚠ 表は data/tables.db（表の写し）からではなく、本文から数える（2026-10-19）——写しは `移行中` のページを入れない
//     （確かめる前の移行のデータを集計に混ぜない・RegisterTablesExclusion）ので、ワンノートから移した加工製品ページの
//     材料表の形状・外注加工表の推奨業者が、1つも数えられていなかった（利用者:「材料の形状や外注加工等の推奨業者に候補が出ないのは何故？」）。
//     人が打った値は、確かめる前でも入力の候補としては役に立つ。集計と検索は、いままでどおり写しを読む（移行中を混ぜない）。
//   - **暇なとき**——索引が最後に書き換わってから、設定 `autocomplete.idle_minutes` 分だけ何も書かれなかったら数え直す
//     （書き換わっていなければ数え直さない）。
//   - **定型の値**——`min_value_count` 回以上入力された値（表は行の数・タグはページの数）。1回しか入力されたことが無い値は出さない。
//   - **定型の欄**——定型の値が、値のある行（ページ）の `min_share` 以上を占め、値のある行が `min_rows` 以上の欄だけに出す。
//     品名・品番・図面名称のように、行ごとに違う値を書く欄は、同じ値がたまたま何度か出ても、割合が低いので出さない
//     （2026-10-19 に職場で測った: ページ種類・単位 100%・工程 98%・装置名称 97%／品名 61%・品番 41%・図面名称 28%）。
//   - 型が `skip_types` の欄（参照・日付など）は数えない。型は設定の語彙で決まる（表は ColumnWord・タグは InferColumnType）。
//   - **数えた結果は data/autocomplete.json に置く**。起動したらまずこれを読むので、起こし直しても候補はすぐ出る。
//     中身は欄ごとの定型の値と、それを持つページ（読めるページだけを数えるため）。派生のデータなので、消しても数え直せば戻る。
//   - エディタは GET /api/autocomplete で、読めるページだけを数えた候補の一覧を、ページを開いたあと1回だけ読む（欄ごとに聞かない）。
//     多い順に最大 `max_values` 個。読めないページの値は、候補にも出さない。
//   - 設定 `autocomplete` が無ければ、何もしない（コードに既定値を持たない）。
//   - コアは値の意味を知らない（どの表のどの列にも、どのタグにも同じに効く）。候補は縛りではない（手で打てる）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"errors"
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// autocompleteFile は数えた結果を置くファイルの名前です（データの場所の下）。
const autocompleteFile = "autocomplete.json"

// AutocompleteSettings は設定 `autocomplete` です。
type AutocompleteSettings struct {
	// MinValueCount は定型の値とみなす、入力された回数です（表は行の数・タグはページの数）。
	MinValueCount int `json:"min_value_count"`
	// MinShare は定型の欄とみなす、定型の値が値のある行（ページ）に占める割合です（0〜1）。
	MinShare float64 `json:"min_share"`
	// MinRows は数える欄に要る、値のある行（ページ）の数です。
	MinRows int `json:"min_rows"`
	// MaxValues は返す候補の数の上限です（多い順）。
	MaxValues int `json:"max_values"`
	// IdleMinutes は、索引が最後に書き換わってから数え直すまで待つ分です（暇なとき）。
	IdleMinutes int `json:"idle_minutes"`
	// SkipTypes は数えない欄の型です（ref・date など——選んでも役に立たない）。
	SkipTypes []string `json:"skip_types,omitempty"`
}

func validateAutocomplete(path string, c *AutocompleteSettings) error {
	if c == nil {
		return nil
	}
	if c.MinValueCount < 1 || c.MinRows < 1 || c.MaxValues < 1 || c.IdleMinutes < 1 {
		return fmt.Errorf("%s: autocomplete の min_value_count・min_rows・max_values・idle_minutes は 1 以上で書いてください", path)
	}
	if c.MinShare <= 0 || c.MinShare > 1 {
		return fmt.Errorf("%s: autocomplete の min_share は 0 より大きく 1 以下で書いてください", path)
	}
	return nil
}

// AutocompleteConfig は設定 `autocomplete` を返します（無ければ nil＝候補を数えない）。
func AutocompleteConfig() *AutocompleteSettings { return settingsField(func(s *Settings) *AutocompleteSettings { return s.Autocomplete }) }

// autoChoice は定型の値1つと、それを持つ行の数（ページごと）です（data/autocomplete.json の1件）。
type autoChoice struct {
	Value string         `json:"value"`
	Pages map[string]int `json:"pages"` // ページ番号（6桁）→ 行の数（タグは1）
}

// autoSurvey は数えた結果です（data/autocomplete.json の形）。値は多い順。
//
//	{"surveyed_at": "…", "cells": {"<キャプション>": {"<列の見出し>": [{"value": "黒", "pages": {"000070": 4}}]}},
//	 "tags": {"<タグの名前>": [{"value": "部品", "pages": {"000080": 1}}]}}
type autoSurvey struct {
	SurveyedAt string                             `json:"surveyed_at"`
	Cells      map[string]map[string][]autoChoice `json:"cells"`
	Tags       map[string][]autoChoice            `json:"tags"`
}

var (
	autoMu         sync.RWMutex
	autoLast       *autoSurvey
	autoWriteMu    sync.Mutex
	autoLastWrite  = time.Now() // 起動した時を「書き換わった」とみなす（起動のあと、暇になったら1度数える）
	autoLastSurvey time.Time
)

// autoFold は欄の名前を引く形にします（表・列の名前の比べ方と同じ——TableName と ASCII の大小）。
func autoFold(name string) string { return asciiFold(TableName(name)) }

// markIndexWritten は索引（表・タグ）が書き換わったことを覚えます（SyncIndex が呼ぶ・暇かどうかの目安）。
func markIndexWritten() {
	autoWriteMu.Lock()
	autoLastWrite = time.Now()
	autoWriteMu.Unlock()
}

// autocompleteDue は、いま数え直す時か（書き換わっていて、そのあと idle だけ何も書かれていない）を返します。
func autocompleteDue(now time.Time, idle time.Duration) bool {
	autoWriteMu.Lock()
	defer autoWriteMu.Unlock()
	return autoLastWrite.After(autoLastSurvey) && now.Sub(autoLastWrite) >= idle
}

// StartAutocompleteSurvey は、前に数えた結果（data/autocomplete.json）を読み、暇なときに数え直す見張りを始めます（main が1度呼ぶ）。
// 1分ごとに「暇か」を見ます。設定が無ければ何もしません（設定を足して起こし直せば始まる）。
func StartAutocompleteSurvey() {
	if AutocompleteConfig() != nil {
		if err := loadAutocompleteFile(); err != nil {
			log.Printf("オートコンプリートの候補（%s）を読めません（暇になったら数え直します）: %v", autocompleteFile, err)
		}
	}
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for range t.C {
			cfg := AutocompleteConfig()
			if cfg == nil || !autocompleteDue(time.Now(), time.Duration(cfg.IdleMinutes)*time.Minute) {
				continue
			}
			func() {
				defer RecoverBackground("オートコンプリートの数え直し")
				if err := SurveyAutocomplete(); err != nil {
					log.Printf("オートコンプリートの候補を数えられません: %v", err)
				}
			}()
		}
	}()
}

var autoSurveyMu sync.Mutex

// SurveyAutocomplete は表のセルとタグの値を数えて、定型の欄の候補を置き直します（data/autocomplete.json にも書く）。
func SurveyAutocomplete() error {
	// 1本ずつ通す（2026-10-30。見張りと管理画面の口が重なると、結果の書き出しが前後した）。
	autoSurveyMu.Lock()
	defer autoSurveyMu.Unlock()
	cfg := AutocompleteConfig()
	if cfg == nil {
		return nil
	}
	started := time.Now()
	skip := map[string]bool{}
	for _, t := range cfg.SkipTypes {
		skip[strings.TrimSpace(t)] = true
	}
	result := &autoSurvey{SurveyedAt: started.UTC().Format(time.RFC3339),
		Cells: map[string]map[string][]autoChoice{}, Tags: map[string][]autoChoice{}}
	if err := surveyTableCells(cfg, skip, result.Cells); err != nil {
		return err
	}
	if err := surveyTags(cfg, skip, result.Tags); err != nil {
		return err
	}
	data, err := json.MarshalIndent(result, "", " ")
	if err != nil {
		return err
	}
	if err := page.WriteFileAtomic(database.DataPath(autocompleteFile), data, 0o644); err != nil {
		return err
	}
	autoMu.Lock()
	autoLast = result
	autoMu.Unlock()
	autoWriteMu.Lock()
	autoLastSurvey = started
	autoWriteMu.Unlock()
	return nil
}

// loadAutocompleteFile は前に数えた結果（data/autocomplete.json）を読みます（無ければ何もしない）。
func loadAutocompleteFile() error {
	data, err := os.ReadFile(database.DataPath(autocompleteFile))
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	var got autoSurvey
	if err := json.Unmarshal(data, &got); err != nil {
		return err
	}
	autoMu.Lock()
	autoLast = &got
	autoMu.Unlock()
	return nil
}

// surveyTableCells は、各ページの本文のキャプションのある表を、列ごとに数えます（テンプレートの中は数えない・移行中も数える）。
func surveyTableCells(cfg *AutocompleteSettings, skip map[string]bool, out map[string]map[string][]autoChoice) error {
	// ⚠ **先に読み切ってから本文を読みます**（行を読みながら別の問い合わせを投げない——:memory: の罠）。
	rows, err := database.DB.Query(`SELECT id FROM pages`)
	if err != nil {
		return err
	}
	var ids []int
	for rows.Next() {
		var id int
		if rows.Scan(&id) == nil {
			ids = append(ids, id)
		}
	}
	rows.Close()
	// 表の名前 → 列の名前 → 値 → ページ → 行の数（名前は本文に書いたまま——captionTablesOf が正規化した形）。
	counts := map[string]map[string]map[string]map[int]int{}
	for _, id := range ids {
		pid := page.FormatID(id)
		if IsTemplateArea(pid) {
			continue
		}
		body, err := ReadPageBody(pid)
		if err != nil || !strings.Contains(body, "<caption") {
			continue
		}
		root, err := html.Parse(strings.NewReader(body))
		if err != nil {
			continue
		}
		for _, ct := range captionTablesOf(root) {
			for ci, col := range ct.Headers {
				if col == "" || isSystemColumn(col) || skip[string(ColumnWord(ct.Name, col).Type)] {
					continue
				}
				for _, r := range ct.Rows {
					if ci >= len(r.Cells) {
						continue
					}
					v := strings.TrimSpace(r.Cells[ci])
					if v == "" {
						continue
					}
					if counts[ct.Name] == nil {
						counts[ct.Name] = map[string]map[string]map[int]int{}
					}
					if counts[ct.Name][col] == nil {
						counts[ct.Name][col] = map[string]map[int]int{}
					}
					if counts[ct.Name][col][v] == nil {
						counts[ct.Name][col][v] = map[int]int{}
					}
					counts[ct.Name][col][v][id]++
				}
			}
		}
	}
	for table, cols := range counts {
		for col, byValue := range cols {
			if choices := steadyChoices(byValue, cfg); choices != nil {
				if out[table] == nil {
					out[table] = map[string][]autoChoice{}
				}
				out[table][col] = choices
			}
		}
	}
	return nil
}

// surveyTags は page_tags のタグの値を、名前ごとに数えます（ページの数で数える・テンプレートの中は数えない）。
func surveyTags(cfg *AutocompleteSettings, skip map[string]bool, out map[string][]autoChoice) error {
	// ⚠ **先に読み切ってから絞ります**（行を読みながら別の問い合わせを投げない——:memory: の罠）。
	rows, err := database.DB.Query(`SELECT page_id, name, value FROM page_tags`)
	if err != nil {
		return err
	}
	type hit struct {
		page        int
		name, value string
	}
	var hits []hit
	for rows.Next() {
		var h hit
		if rows.Scan(&h.page, &h.name, &h.value) == nil {
			h.name, h.value = strings.TrimSpace(h.name), strings.TrimSpace(h.value)
			if h.name != "" && h.value != "" {
				hits = append(hits, h)
			}
		}
	}
	rows.Close()
	inTemplate := map[int]bool{}
	byName := map[string]map[string]map[int]int{}
	for _, h := range hits {
		tmpl, seen := inTemplate[h.page]
		if !seen {
			tmpl = IsTemplateArea(page.FormatID(h.page))
			inTemplate[h.page] = tmpl
		}
		if tmpl || skip[string(InferColumnType(h.name))] {
			continue
		}
		if byName[h.name] == nil {
			byName[h.name] = map[string]map[int]int{}
		}
		if byName[h.name][h.value] == nil {
			byName[h.name][h.value] = map[int]int{}
		}
		byName[h.name][h.value][h.page] = 1 // 同じページに同じ値が2つあっても1と数える
	}
	for name, byValue := range byName {
		if choices := steadyChoices(byValue, cfg); choices != nil {
			out[name] = choices
		}
	}
	return nil
}

// steadyChoices は、欄の値の数え（値 → ページ → 行の数）から定型の値を多い順に返します。定型の欄でなければ nil。
func steadyChoices(byValue map[string]map[int]int, cfg *AutocompleteSettings) []autoChoice {
	total, steady := 0, 0
	var out []autoChoice
	for v, pages := range byValue {
		n := 0
		for _, c := range pages {
			n += c
		}
		total += n
		if n >= cfg.MinValueCount {
			steady += n
			out = append(out, autoChoice{Value: v, Pages: pageKeys(pages)})
		}
	}
	if total < cfg.MinRows || len(out) == 0 || float64(steady) < cfg.MinShare*float64(total) {
		return nil
	}
	sort.Slice(out, func(i, j int) bool {
		ni, nj := sumRows(out[i].Pages), sumRows(out[j].Pages)
		if ni != nj {
			return ni > nj
		}
		return out[i].Value < out[j].Value
	})
	return out
}

func sumRows(pages map[string]int) int {
	n := 0
	for _, c := range pages {
		n += c
	}
	return n
}

// pageKeys はページ番号を JSON の鍵（6桁の文字）にします。
func pageKeys(pages map[int]int) map[string]int {
	out := make(map[string]int, len(pages))
	for id, n := range pages {
		out[page.FormatID(id)] = n
	}
	return out
}

// TagValueCount は候補の値1つと、それを使っている行（タグはページ）の数です。
type TagValueCount struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// readableChoices は候補を、読めるページの行の数の多い順に、最大 max 個返します（読めない行は数えない）。
func readableChoices(choices []autoChoice, canView func(int) bool, max int) []TagValueCount {
	values := []TagValueCount{}
	for _, c := range choices {
		n := 0
		for pid, rows := range c.Pages {
			if id, err := strconv.Atoi(pid); err == nil && canView(id) {
				n += rows
			}
		}
		if n > 0 {
			values = append(values, TagValueCount{Value: c.Value, Count: n})
		}
	}
	sort.SliceStable(values, func(i, j int) bool {
		if values[i].Count != values[j].Count {
			return values[i].Count > values[j].Count
		}
		return values[i].Value < values[j].Value
	})
	if len(values) > max {
		values = values[:max]
	}
	return values
}

// AutocompleteList は、user が読めるページだけを数えた候補の一覧です（GET /api/autocomplete の答え）。
// 表とタグの名前は、数えたときの名前（表は正規化したキャプション・列の見出し）。
type AutocompleteList struct {
	SurveyedAt string                                `json:"surveyed_at"`
	Cells      map[string]map[string][]TagValueCount `json:"cells"`
	Tags       map[string][]TagValueCount            `json:"tags"`
}

// AutocompleteFor は user の候補の一覧を返します（まだ数えていなければ空）。
func AutocompleteFor(user *auth.User) AutocompleteList {
	out := AutocompleteList{Cells: map[string]map[string][]TagValueCount{}, Tags: map[string][]TagValueCount{}}
	autoMu.RLock()
	last := autoLast
	autoMu.RUnlock()
	cfg := AutocompleteConfig()
	if last == nil || cfg == nil {
		return out
	}
	out.SurveyedAt = last.SurveyedAt
	canView := viewableFunc(user)
	for table, cols := range last.Cells {
		for col, choices := range cols {
			if vs := readableChoices(choices, canView, cfg.MaxValues); len(vs) > 0 {
				if out.Cells[table] == nil {
					out.Cells[table] = map[string][]TagValueCount{}
				}
				out.Cells[table][col] = vs
			}
		}
	}
	for name, choices := range last.Tags {
		if vs := readableChoices(choices, canView, cfg.MaxValues); len(vs) > 0 {
			out.Tags[name] = vs
		}
	}
	return out
}

// CellChoiceCounts は表 table の列 column の候補です（数えていない・定型でない列は空）。名前は正規化して比べる。
func CellChoiceCounts(user *auth.User, table, column string) []TagValueCount {
	for t, cols := range AutocompleteFor(user).Cells {
		if autoFold(t) != autoFold(table) {
			continue
		}
		for c, vs := range cols {
			if autoFold(c) == autoFold(column) {
				return vs
			}
		}
	}
	return []TagValueCount{}
}

// tagValueCounts は名前 name のタグの候補です（数えていない・定型でないタグは空）。
func tagValueCounts(user *auth.User, name string) []TagValueCount {
	if vs, ok := AutocompleteFor(user).Tags[strings.TrimSpace(name)]; ok {
		return vs
	}
	return []TagValueCount{}
}

// AutocompleteAPIHandler は GET /api/autocomplete です（応答 {success, surveyed_at, cells:{表:{列:[{value,count}]}}, tags:{名前:[…]}}）。
// エディタはページを開いたあと1回だけ読み、欄に入るたびにこの一覧から引く。
func AutocompleteAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	list := AutocompleteFor(auth.CurrentUser(r))
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"success": true, "surveyed_at": list.SurveyedAt, "cells": list.Cells, "tags": list.Tags})
}

// AutocompleteSurveyAPIHandler は POST /api/admin/autocomplete/survey です（admin 限定・暇を待たずにいま数える）。
func AutocompleteSurveyAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !page.RequireAdmin(w, r) {
		return
	}
	if _, ok := GateJSONPost(w, r); !ok {
		return
	}
	if AutocompleteConfig() == nil {
		WriteJSON(w, map[string]any{"success": false, "message": "設定 autocomplete がありません（候補は数えません）"})
		return
	}
	if err := SurveyAutocomplete(); err != nil {
		WriteJSON(w, map[string]any{"success": false, "message": "数えられません: " + err.Error()})
		return
	}
	cells, tags := 0, 0
	autoMu.RLock()
	for _, cols := range autoLast.Cells {
		cells += len(cols)
	}
	tags = len(autoLast.Tags)
	autoMu.RUnlock()
	WriteJSON(w, map[string]any{"success": true, "columns": cells, "tags": tags})
}
