package cms

// ─────────────────────────────────────────────────────────────────────────
// タグの値の候補——DB にある値を、多い順に（2026-10-19）
//
// 利用者:「「ページ種類」のタグをDBから検索すると「装置」「社名」などがあります。この値の数を数えて多い方から順に、
// ページ種類のタグを編集するときに値のコンボボックスとして出します。ページ種類は説明のための例で、多くのタグで少数の種類を
// 選択しているだけと思います。品番のように一度しか出てこないものもありますが、コンボボックスなので問題ないのと、一度しか
// 出てこないものは大抵プログラムから入力されるので問題ないと思います。」
//
//   - GET /api/tag-values?name=<タグの名前> は、その名前のタグの値を、ページの数の多い順に返す（同じ数なら値の順・最大 tagValuesMax）。
//   - 数えるのは**読めるページだけ**（読めないページの値は、候補にも出さない——SuggestFunc と同じ考え）。テンプレートの中は数えない。
//   - 候補は縛りではない（suggest.go と同じ）。画面は選ぶボタンを並べるだけで、手で打つこともできる。
//   - コアは値の意味を知らない（どのタグにも同じに効く）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// tagValuesMax は返す候補の数の上限です（多すぎる候補は選べない——suggest.go と同じ）。
const tagValuesMax = 30

// TagValueCount はタグの値1つと、それを持つページの数です。
type TagValueCount struct {
	Value string `json:"value"`
	Count int    `json:"count"`
}

// TagValueCounts は、名前 name のタグの値を、user が読めるページの数の多い順に返します（テンプレートの中は数えない）。
func TagValueCounts(user *auth.User, name string) ([]TagValueCount, error) {
	// ⚠ **先に読み切ってから絞ります**（行を読みながら CanView を呼ばない——:memory: の罠）。
	rows, err := database.DB.Query(`SELECT page_id, value FROM page_tags WHERE name = ?`, name)
	if err != nil {
		return nil, err
	}
	type hit struct {
		page  int
		value string
	}
	var hits []hit
	for rows.Next() {
		var h hit
		if rows.Scan(&h.page, &h.value) == nil {
			if h.value = strings.TrimSpace(h.value); h.value != "" {
				hits = append(hits, h)
			}
		}
	}
	rows.Close()
	ok := map[int]bool{}
	counted := map[string]map[int]bool{} // 同じページに同じ値が2つあっても1と数える
	for _, h := range hits {
		v, seen := ok[h.page]
		if !seen {
			v = page.CanView(user, h.page) && !IsTemplateArea(page.FormatID(h.page))
			ok[h.page] = v
		}
		if !v {
			continue
		}
		if counted[h.value] == nil {
			counted[h.value] = map[int]bool{}
		}
		counted[h.value][h.page] = true
	}
	out := make([]TagValueCount, 0, len(counted))
	for v, pages := range counted {
		out = append(out, TagValueCount{Value: v, Count: len(pages)})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Value < out[j].Value
	})
	return out, nil
}

// TagValuesAPIHandler は GET /api/tag-values?name=… です（応答 {success, name, values:[{value, count}]}・多い順・最大 tagValuesMax）。
func TagValuesAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	name := strings.TrimSpace(r.URL.Query().Get("name"))
	values := []TagValueCount{}
	if name != "" {
		got, err := TagValueCounts(auth.CurrentUser(r), name)
		if err != nil {
			http.Error(w, "候補を数えられません", http.StatusInternalServerError)
			return
		}
		if len(got) > tagValuesMax {
			got = got[:tagValuesMax]
		}
		values = got
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"success": true, "name": name, "values": values})
}
