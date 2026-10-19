package cms

// ─────────────────────────────────────────────────────────────────────────
// 表のセルの候補——値が数個に決まっている列を、暇なときに数えて置いておく（2026-10-19）
//
// 利用者:「表のセルに入る値が数個に決まっている場合があります。タグも同じです。これは列やタグの名前に対してどんな値が
// 何個あるか調べればわかります。これをオートコンプリートのようにコンボボックスで選ぶように出来ないでしょうか？この調査は
// 暇なときにDBに対して自動で行うようにできますか？」
// 「オートコンプリートはコアの機能です」
// 「表のセルの候補は、サーバーが暇なときに DB を数えて置いておく形で作ってください。値が数個に決まっている列だけに出し、
// 何個までかは設定に置いてください。」
//
//   - 数えるのは data/tables.db（キャプションのある表の写し・tables_db.go）。表はキャプション、列は見出しで名乗る。
//     テンプレートの中の表は、写しに入らないので数えない。
//   - **暇なとき**——最後に表が書き換わってから、設定 `cell_choices.idle_minutes` 分だけ何も書かれなかったら数え直す
//     （書き換わっていなければ数え直さない）。結果はサーバーのメモリに置く（起動のあとも、暇になったら数える）。
//   - **値が数個に決まっている列だけ**——違う値が `max_values` 個以下で、値のある行が `min_rows` 行以上の列。
//     どの値も1回しか出ない列（行ごとに違う値を書く列）は除く。
//     型が `skip_types` の列（参照・日付など）は数えない。
//     ⚠ 式のセルは、写しに計算した結果が入っている（`=1+1` なら 2）ので、結果の値として数える。
//   - 設定 `cell_choices` が無ければ、何もしない（コードに既定値を持たない）。
//   - 返すのは**読めるページの行だけ**を数えた値（読めないページの値は、候補にも出さない——tag_values.go と同じ）。
//   - コアは値の意味を知らない（どの表のどの列にも同じに効く）。候補は縛りではない（手で打てる）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// CellChoicesSettings は設定 `cell_choices` です。
type CellChoicesSettings struct {
	// MaxValues は「数個に決まっている」とみなす、違う値の数の上限です。
	MaxValues int `json:"max_values"`
	// MinRows は数える列に要る、値のある行の数です（少なすぎる列は「決まっている」と言えない）。
	MinRows int `json:"min_rows"`
	// IdleMinutes は、表が最後に書き換わってから数え直すまで待つ分です（暇なとき）。
	IdleMinutes int `json:"idle_minutes"`
	// SkipTypes は数えない列の型です（ref・date など——選んでも役に立たない）。
	SkipTypes []string `json:"skip_types,omitempty"`
}

func validateCellChoices(path string, c *CellChoicesSettings) error {
	if c == nil {
		return nil
	}
	if c.MaxValues <= 0 || c.MinRows <= 0 || c.IdleMinutes <= 0 {
		return fmt.Errorf("%s: cell_choices の max_values・min_rows・idle_minutes は 1 以上で書いてください", path)
	}
	return nil
}

// CellChoicesConfig は設定 `cell_choices` を返します（無ければ nil＝候補を数えない）。
func CellChoicesConfig() *CellChoicesSettings {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	if settings == nil {
		return nil
	}
	return settings.CellChoices
}

// cellChoice は列の値1つと、それを持つ行の数（ページごと）です。
type cellChoice struct {
	value string
	pages map[int]int // page_id → 行の数
}

// cellChoiceSurvey は数えた結果です（表の名前 → 列の名前 → 値・多い順）。名前は asciiFold(TableName(…)) の鍵。
type cellChoiceSurvey struct {
	at     time.Time
	tables map[string]map[string][]cellChoice
}

var (
	cellChoicesMu    sync.RWMutex
	cellChoicesLast  *cellChoiceSurvey
	cellWriteMu      sync.Mutex
	cellLastWrite    = time.Now() // 起動した時を「書き換わった」とみなす（起動のあと、暇になったら1度数える）
	cellLastSurveyed time.Time
)

// markTablesWritten は表が書き換わったことを覚えます（syncCaptionTables が呼ぶ・暇かどうかの目安）。
func markTablesWritten() {
	cellWriteMu.Lock()
	cellLastWrite = time.Now()
	cellWriteMu.Unlock()
}

// cellChoicesDue は、いま数え直す時か（書き換わっていて、そのあと idle だけ何も書かれていない）を返します。
func cellChoicesDue(now time.Time, idle time.Duration) bool {
	cellWriteMu.Lock()
	defer cellWriteMu.Unlock()
	return cellLastWrite.After(cellLastSurveyed) && now.Sub(cellLastWrite) >= idle
}

// StartCellChoicesSurvey は、暇なときに表のセルの候補を数え直す見張りを始めます（main が1度呼ぶ）。
// 1分ごとに「暇か」を見ます。設定が無ければ何もしません（設定を足して起こし直せば始まる）。
func StartCellChoicesSurvey() {
	go func() {
		t := time.NewTicker(time.Minute)
		defer t.Stop()
		for range t.C {
			cfg := CellChoicesConfig()
			if cfg == nil || !cellChoicesDue(time.Now(), time.Duration(cfg.IdleMinutes)*time.Minute) {
				continue
			}
			if err := SurveyCellChoices(); err != nil {
				log.Printf("表のセルの候補を数えられません: %v", err)
			}
		}
	}()
}

// SurveyCellChoices は data/tables.db を数えて、値が数個に決まっている列の候補を置き直します。
func SurveyCellChoices() error {
	cfg := CellChoicesConfig()
	db := database.TablesDB
	if cfg == nil || db == nil {
		return nil
	}
	started := time.Now()
	skip := map[string]bool{}
	for _, t := range cfg.SkipTypes {
		skip[strings.TrimSpace(t)] = true
	}
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	existing, err := existingTables(tx)
	if err != nil {
		return err
	}
	out := map[string]map[string][]cellChoice{}
	for _, t := range existing {
		if !t.Ours {
			continue
		}
		cols, err := tableColumns(tx, t.Name)
		if err != nil {
			return err
		}
		for _, col := range cols {
			if isSystemColumn(col) || skip[string(ColumnWord(t.Name, col).Type)] {
				continue
			}
			choices, err := surveyColumn(tx, t.Name, col, cfg)
			if err != nil {
				return err
			}
			if choices == nil {
				continue
			}
			tk := asciiFold(TableName(t.Name))
			if out[tk] == nil {
				out[tk] = map[string][]cellChoice{}
			}
			out[tk][asciiFold(TableName(col))] = choices
		}
	}
	cellChoicesMu.Lock()
	cellChoicesLast = &cellChoiceSurvey{at: started, tables: out}
	cellChoicesMu.Unlock()
	cellWriteMu.Lock()
	cellLastSurveyed = started
	cellWriteMu.Unlock()
	return nil
}

// surveyColumn は1列を数えます。値が数個に決まっていなければ nil。
func surveyColumn(tx *sql.Tx, table, col string, cfg *CellChoicesSettings) ([]cellChoice, error) {
	// ⚠ 名前は quoteIdent を通す（tables_db.go の冒頭）。
	rows, err := tx.Query(`SELECT ` + colPageID + `, ` + quoteIdent(col) + ` FROM ` + quoteIdent(table) +
		` WHERE ` + quoteIdent(col) + ` IS NOT NULL`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	byValue := map[string]map[int]int{}
	total := 0
	for rows.Next() {
		var pid int
		var raw any
		if err := rows.Scan(&pid, &raw); err != nil {
			return nil, err
		}
		v := strings.TrimSpace(cellChoiceText(raw))
		if v == "" {
			continue // 空のセルは数えない
		}
		if byValue[v] == nil {
			byValue[v] = map[int]int{}
			if len(byValue) > cfg.MaxValues {
				return nil, rows.Err() // 違う値が多すぎる——数個に決まっていない
			}
		}
		byValue[v][pid]++
		total++
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	// どの値も1回しか出ない列は、数個に決まっているとは言えない（品名・番号のように、行ごとに違う値を書く列）。
	if total < cfg.MinRows || len(byValue) == 0 || total == len(byValue) {
		return nil, nil
	}
	out := make([]cellChoice, 0, len(byValue))
	for v, pages := range byValue {
		out = append(out, cellChoice{value: v, pages: pages})
	}
	return out, nil
}

// cellChoiceText は写しの値を文字にします（数は書いたままの形に近く——1.0 ではなく 1）。
func cellChoiceText(raw any) string {
	switch v := raw.(type) {
	case nil:
		return ""
	case string:
		return v
	case []byte:
		return string(v)
	case float64:
		return strconv.FormatFloat(v, 'f', -1, 64)
	case int64:
		return strconv.FormatInt(v, 10)
	default:
		return fmt.Sprint(v)
	}
}

// CellChoiceCounts は、表 table の列 column の候補を、user が読めるページの行の数の多い順に返します
// （数えていない・数個に決まっていない列は空）。surveyed は数えた時刻（まだ数えていなければ zero）。
func CellChoiceCounts(user *auth.User, table, column string) (values []TagValueCount, surveyed time.Time) {
	cellChoicesMu.RLock()
	last := cellChoicesLast
	cellChoicesMu.RUnlock()
	values = []TagValueCount{}
	if last == nil {
		return values, time.Time{}
	}
	choices := last.tables[asciiFold(TableName(table))][asciiFold(TableName(column))]
	canView := viewableFunc(user)
	for _, c := range choices {
		n := 0
		for pid, rows := range c.pages {
			if canView(pid) {
				n += rows
			}
		}
		if n > 0 {
			values = append(values, TagValueCount{Value: c.value, Count: n})
		}
	}
	sort.Slice(values, func(i, j int) bool {
		if values[i].Count != values[j].Count {
			return values[i].Count > values[j].Count
		}
		return values[i].Value < values[j].Value
	})
	return values, last.at
}

// CellChoicesAPIHandler は GET /api/cell-values?table=<キャプション>&column=<見出し> です
// （応答 {success, values:[{value, count}], surveyed_at}・多い順）。
func CellChoicesAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	table := strings.TrimSpace(r.URL.Query().Get("table"))
	column := strings.TrimSpace(r.URL.Query().Get("column"))
	values, at := CellChoiceCounts(auth.CurrentUser(r), table, column)
	surveyed := ""
	if !at.IsZero() {
		surveyed = at.UTC().Format(time.RFC3339)
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"success": true, "values": values, "surveyed_at": surveyed})
}

// CellChoicesSurveyAPIHandler は POST /api/admin/cell-values/survey です（admin 限定・暇を待たずにいま数える）。
func CellChoicesSurveyAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if !page.RequireAdmin(w, r) {
		return
	}
	if _, ok := GateJSONPost(w, r); !ok {
		return
	}
	if CellChoicesConfig() == nil {
		WriteJSON(w, map[string]any{"success": false, "message": "設定 cell_choices がありません（候補は数えません）"})
		return
	}
	if err := SurveyCellChoices(); err != nil {
		WriteJSON(w, map[string]any{"success": false, "message": "数えられません: " + err.Error()})
		return
	}
	n := 0
	cellChoicesMu.RLock()
	for _, cols := range cellChoicesLast.tables {
		n += len(cols)
	}
	cellChoicesMu.RUnlock()
	WriteJSON(w, map[string]any{"success": true, "columns": n})
}
