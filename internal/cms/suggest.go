package cms

// ─────────────────────────────────────────────────────────────────────────
// 入力の候補（2026-09-21）
//
// ユーザー:「⚠ **`text` だが、候補を出す**」（`外注加工` の `推奨業者` の持ち方）。
//
// **`Enum` との違いは、候補が動くこと**です——選択肢が宣言に書ける値なら `Enum`、
// **データから引いてくる**なら候補（取引先の題など）。
//
// ⚠ **候補は縛りではありません。** 採らずに手で打てます（顧客名のコンボボックスと
// 同じ手）——**揃うのは採ったときだけ**で、手打ちは止められません。
// ⚠ それでも意味があるのは、**打つより選ぶほうが速い**からです。速ければ揃います。
//
// ⚠ **出どころはコアが知りません。** 名前で引くので、業務の候補（取引先・仕入先）は
// **拡張が持ち込みます**——コアのコードから業務語がゼロ、を崩さないため。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"net/http"
	"sort"
	"sync"

	"w-cms/internal/auth"
)

// SuggestFunc は候補を返します。q は打ちかけの文字（空なら全部）。
//
// ⚠ **閲覧者を受け取ります**——候補にも**読めないページの題**が混ざりえます。
// 絞るのは出どころの責任です。
type SuggestFunc func(user *auth.User, q string) []string

var (
	suggestMu      sync.RWMutex
	suggestSources = map[string]SuggestFunc{}
)

// RegisterSuggestSource は候補の出どころを登録します（拡張の `init()` から）。
func RegisterSuggestSource(name string, fn SuggestFunc) {
	suggestMu.Lock()
	defer suggestMu.Unlock()
	if _, dup := suggestSources[name]; dup {
		panic("候補の出どころが重複しています: " + name)
	}
	suggestSources[name] = fn
}

// SuggestAPIHandler は GET /api/suggest?source=…&q=… です。
//
// ⚠ **知らない出どころは 404 ではなく空で返します。** 拡張を外したビルドでは
// その出どころが居ないのが正常で、**画面は候補が出ないだけ**であるべきだからです
// （拡張の出し分けと同じ考え方）。
func SuggestAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	suggestMu.RLock()
	fn := suggestSources[r.URL.Query().Get("source")]
	suggestMu.RUnlock()

	var items []string
	if fn != nil {
		items = fn(auth.CurrentUser(r), r.URL.Query().Get("q"))
	}
	sort.Strings(items)
	// ⚠ **数を絞ります**——「ヤフーのようなドメインでは候補が1万件ということも
	//    あり得ます」（2026-09-16 の教訓）。多すぎる候補は選べません。
	const max = 30
	if len(items) > max {
		items = items[:max]
	}
	if items == nil {
		items = []string{}
	}
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{"success": true, "items": items})
}
