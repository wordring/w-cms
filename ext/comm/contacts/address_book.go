package contacts

// ─────────────────────────────────────────────────────────────────────────
// 宛先の候補——連絡帳のアドレス（2026-10-03）
//
// 利用者:「宛先は連絡帳から候補を取得してコンボボックスで出して欲しい」。
//
// 送る欄（assets/mail-compose.js）の宛先・CC の横に並べる候補です。並ぶのは連絡帳の箱の下（組織と、その下の人）の
// ページで `メールアドレス` を持つもの（読めるものだけ）。名前は人の題、会社はその親（組織）の題——組織のページ自身の
// アドレスなら会社名だけ。連絡帳の外のページ（通信記録など）に書いたアドレスは出しません（宛先は連絡帳が正本）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"net/http"
	"sort"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// AddressEntry は宛先の候補1つです。
type AddressEntry struct {
	Name    string `json:"name"`    // 人の題（組織のページ自身のアドレスなら空）
	Org     string `json:"org"`     // 組織の題
	Address string `json:"address"` // 素のアドレス
}

// AddressBook は、連絡帳の下の読めるページが持つメールアドレスを、会社・名前・アドレスの順で返します。
func AddressBook(user *auth.User) []AddressEntry {
	boxInt, ok := contactsBoxInt()
	if !ok {
		return nil
	}
	// **先に読み切ってから絞ります**（`cms.TagRowsNamed`——行を読みながら別のクエリを投げない）。
	rows, err := cms.TagRowsNamed(database.DB, EmailTag)
	if err != nil {
		return nil
	}
	type node struct {
		parent int
		title  string
	}
	memo := map[int]node{}
	info := func(id int) node {
		if n, ok := memo[id]; ok {
			return n
		}
		var n node
		database.DB.QueryRow(`SELECT COALESCE(parent_id, 0), COALESCE(title, '') FROM pages WHERE id = ?`, id).Scan(&n.parent, &n.title)
		memo[id] = n
		return n
	}
	seen := map[string]bool{}
	var out []AddressEntry
	for _, r := range rows {
		v := r.Value
		if i := strings.LastIndex(v, "<"); i >= 0 { // `名前 <アドレス>` の形でも素のアドレスを
			if j := strings.Index(v[i:], ">"); j > 0 {
				v = v[i+1 : i+j]
			}
		}
		addr := normalizeEmail(v)
		if addr == "" || !page.CanView(user, r.PageID) {
			continue
		}
		me := info(r.PageID)
		var e AddressEntry
		switch {
		case me.parent == boxInt: // 組織のページ自身のアドレス
			e = AddressEntry{Org: me.title, Address: addr}
		case me.parent != 0 && info(me.parent).parent == boxInt: // 組織の下の人
			e = AddressEntry{Name: me.title, Org: info(me.parent).title, Address: addr}
		default:
			continue // 連絡帳の外
		}
		key := e.Org + "\x1f" + e.Name + "\x1f" + addr
		if seen[key] {
			continue
		}
		seen[key] = true
		out = append(out, e)
	}
	sort.SliceStable(out, func(i, j int) bool {
		a, b := out[i], out[j]
		if a.Org != b.Org {
			return a.Org < b.Org
		}
		if a.Name != b.Name {
			return a.Name < b.Name // 組織のアドレス（名前が空）が先
		}
		return a.Address < b.Address
	})
	return out
}

// AddressBookAPIHandler は GET /api/contacts/addresses です（送る欄の宛先・CC の候補・読むだけ）。
func AddressBookAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		cms.JSONFail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	user := auth.CurrentUser(r)
	if user == nil {
		cms.JSONFail(w, http.StatusForbidden, "ログインが必要です")
		return
	}
	entries := AddressBook(user)
	if entries == nil {
		entries = []AddressEntry{}
	}
	_ = json.NewEncoder(w).Encode(map[string]any{"success": true, "entries": entries})
}
