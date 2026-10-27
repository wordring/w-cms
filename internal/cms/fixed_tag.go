package cms

// ─────────────────────────────────────────────────────────────────────────
// 作るときに決めるタグ（2026-10-27）
//
// 利用者:「パージの新規作成時は書類の種類を指定できるように出来ますか？」→（案を出して）
// 「案の1〜3（作るときに種類を選ぶ・作ったあとは管理者だけが替えられる・ボタンから作る書類は人のメニューに出さない）で作ってください」
//
// 設定の語彙で `fixed` を付けた語（タグの名前）は、次の決まりで扱います。
//
//  1. 作るとき: 新規作成（/api/new-page）で値を選べます（`fixed.<名前>` の欄）。選べるのは語の `creatable` の値だけです。
//     テンプレートから作るときは、テンプレートの値のままです（テンプレートの値が creatable に無ければ、管理者でない人は作れない）。
//  2. 作ったあと: 管理者でない人が本文でその値を替えても（足す・消すも）、保存のときに元の値へ戻します（KeepFixedTags）。
//     機械のコード（RewriteBody・CreateChildPage など）が書く値は、そのままです——ボタンが作る書類は、機械が種類を書きます。
//  3. 新規作成のメニュー: creatable に無い値を持つテンプレートは出しません（/api/templates）。
//
// なぜ: 書類の種類のタグで集計を集める拡張（案件残表・出荷済数量・最新の単価など）では、どこかのページに書ける人が自分のページの種類を
// 偽ると、集計に混ぜられました（【考察】コードの矛盾 §7.2）。
//
// ⚠ コアはタグの名前も値も知りません——どの語を固定にするかは設定の語彙が決めます（開発方針 §0）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"sort"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms/htmldoc"
)

// FixedTagNames は、作るときに決める語（設定の語彙の `fixed`）の名前を返します（決まった順）。
func FixedTagNames() []string {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	if settings == nil {
		return nil
	}
	var out []string
	for word, w := range settings.Vocabulary {
		if w.Fixed {
			out = append(out, word)
		}
	}
	sort.Strings(out)
	return out
}

// fixedTagCreatable は、作るときに決める語 name の値 value を、人が選べるかを返します（語が fixed でなければ true）。
func fixedTagCreatable(name, value string) bool {
	settingsMu.RLock()
	defer settingsMu.RUnlock()
	if settings == nil {
		return true
	}
	w, ok := settings.Vocabulary[name]
	if !ok || !w.Fixed {
		return true
	}
	value = strings.TrimSpace(value)
	for _, v := range w.Creatable {
		if v == value {
			return true
		}
	}
	return false
}

// tagValuesInBody は、本文の可変タグ（`<dl data-type="tags">`・節の中も）にある名前 name の値を、出てくる順に返します（空の値も含む）。
func tagValuesInBody(body, name string) []string {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return nil
	}
	var out []string
	for _, n := range nodes {
		WalkElements(n, func(el *html.Node) {
			if el.Data != "dl" || Attr(el, "data-type") != TagsDataType {
				return
			}
			eachDLPair(el, false, func(key string, dd *html.Node) bool {
				if key == name {
					out = append(out, strings.TrimSpace(nodeText(dd)))
				}
				return true
			})
		})
	}
	return out
}

// sameTagValues は、2つの値の並びが（空の値を除いて）同じかを返します。
func sameTagValues(a, b []string) bool {
	a, b = nonEmpty(a), nonEmpty(b)
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func nonEmpty(vs []string) []string {
	var out []string
	for _, v := range vs {
		if v != "" {
			out = append(out, v)
		}
	}
	return out
}

// KeepFixedTags は、管理者でない人の保存で、作るときに決める語の値が変わっていたら、元（before）の値へ戻した本文を返します。
// 戻した語の名前も返します（無ければ空）。管理者（と user が nil のとき——機械の書き手は呼び手が選ぶ）は、そのまま返します。
//
// ⚠ 値を「戻す」ので、本文を書く人の口（/api/sync・/api/save・/api/save-block・版を戻す）だけで呼びます。機械のコードが書く本文
// （RewriteBody・CreateChildPage）では呼びません——ボタンが作る書類は、機械が種類を書くからです。
func KeepFixedTags(user *auth.User, before, after string) (string, []string) {
	if user == nil || user.IsAdmin {
		return after, nil
	}
	var kept []string
	for _, name := range FixedTagNames() {
		old := tagValuesInBody(before, name)
		if sameTagValues(old, tagValuesInBody(after, name)) {
			continue
		}
		d := EditBody(after)
		d.ReplaceTag(name, old...)
		after = d.HTML()
		kept = append(kept, name)
	}
	return after, kept
}

// keepFixedTagsByName は KeepFixedTags を、利用者の名前で呼びます（版を戻す口など、名前しか持たない所のため）。
// 利用者が引けなければ（消された・無効）、管理者でないものとして戻します。
func keepFixedTagsByName(username, before, after string) (string, []string) {
	u, err := auth.LookupUser(username)
	if err != nil || u == nil {
		u = &auth.User{Username: username}
	}
	return KeepFixedTags(u, before, after)
}

// fixedTagsOfNewPage は、新規作成で本文 body（テンプレートを写したもの・空のページの既定）に、作るときに決める語の値を入れます。
// form は欄の値を引く関数です（`fixed.<名前>`）。
//
//   - テンプレートに値があれば、その値のまま（欄は見ない）。
//   - 無ければ、欄の値を入れる（空なら入れない——種類なし）。
//   - 管理者でない人は、creatable に無い値では作れません（ok=false と、断る理由）。
func fixedTagsOfNewPage(user *auth.User, body string, form func(string) string) (string, string, bool) {
	admin := user != nil && user.IsAdmin
	for _, name := range FixedTagNames() {
		vals := nonEmpty(tagValuesInBody(body, name))
		if len(vals) == 0 {
			v := strings.TrimSpace(form("fixed." + name))
			if v == "" {
				continue
			}
			if !admin && !fixedTagCreatable(name, v) {
				return body, "「" + name + "：" + v + "」のページは、ここからは作れません（ボタンから作るか、管理者に頼んでください）", false
			}
			d := EditBody(body)
			d.SetTag(name, v)
			body = d.HTML()
			continue
		}
		if admin {
			continue
		}
		for _, v := range vals {
			if !fixedTagCreatable(name, v) {
				return body, "このテンプレート（" + name + "：" + v + "）からは作れません（ボタンから作るか、管理者に頼んでください）", false
			}
		}
	}
	return body, "", true
}

// templateCreatable は、テンプレートの本文 body から、人が新規作成のメニューで作ってよいかを返します（作るときに決める語の値が、
// どれも creatable にあるか）。
func templateCreatable(body string) bool {
	for _, name := range FixedTagNames() {
		for _, v := range nonEmpty(tagValuesInBody(body, name)) {
			if !fixedTagCreatable(name, v) {
				return false
			}
		}
	}
	return true
}
