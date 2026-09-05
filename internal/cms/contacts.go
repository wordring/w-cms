package cms

// ─────────────────────────────────────────────────────────────────────────
// アドレス帳——メールから相手を拾い、ページにする（2026-09-05）
//
// ユーザー:「アドレス帳のようなものを作って、メールから人物や電話番号、
// メールアドレスを収集しましょう」。
//
// **材料はもう索引に入っています。** 取り込みが `差出人アドレス`・`宛先アドレス`・
// `CCアドレス`・`返信先アドレス` を書いているので、新しく集める仕掛けは要りません
// ——足りないのは「**まだページになっていないアドレス**」を並べて、人が確定する口だけです。
// 未処理の受信とまったく同じ形（見つかっているものを並べ、決めるのは人）。
//
// ── 実データで分かった3つのこと（60通・11アドレス）──
//
//  1. **アドレスは揺れない。** 11アドレス中、表示名が2通りになったものはゼロ。
//     突き合わせの鍵はアドレスで足り、名寄せは要らない（機械が発行した文字列だから）。
//  2. **表示名は「人」と「会社」が混ざる**（`潮崎 光俊` / `株式会社高瀬製作所` /
//     `レーザマックス大阪支店`）。メールは「誰か」を1つしか教えないので、
//     人と会社を分けるのは人の仕事。
//  3. **会社はドメインでまとまる——が、例外がある。** `toa-sports-machine.co.jp` に
//     4人。しかし自社の工場長だけ別プロバイダのアドレスを使っていた。
//     **ドメインは手掛かりであって決定ではない**（今日から何度も出てきた形）。
//
// ── 決めたこと ──
//
// **取引の相手は1種類。役割はタグで表します**（`取引：顧客` / `仕入先` / `自社`）。
// ユーザー:「顧客であり、仕入れ先である場合もあります」——置き場所で分けると、
// そのとき1枚に保てません。同じページに両方書けます（タグは同じ名前を何度でも置ける）。
//
// **相手ページは「取引先」の下**です（2026-09-05 ユーザー:「トップページの直接の子が
// 個人名や社名はちょっと具合が悪い」）。もとは 2026-09-03 の決定でトップ直下でしたが、
// 相手が増えるほどトップが名簿になってしまうため、**箱を1枚かませました**。
//
// **箱は1枚だけ**です。`顧客` `仕入先` `自社` で箱を分けないのは、ユーザーの
// 「顧客であり、仕入れ先である場合もあります」がそのまま効くため——置き場所で
// 分けると、その相手を**どちらか一方にしか置けません**。役割はタグの仕事です。
//
// **顧客名／装置名称／図面名称の「顧客名」も同じページ**です（同日ユーザー決定）。
// 会社ページを別に作ると「株式会社トーアスポーツマシーン」が2枚になり、
// 連絡先を見るページと部品を見るページが分かれてしまいます。
//
// **個人のお客様なら、そのページが本人**です。人物ページを別に作りません
// ——本人の名前のページを2枚重ねる意味がないためです。
//
// **窓口の人は `社名／担当者／名前`**（2026-09-05 ユーザー決定）。社名ページの子には
// 既に**装置名称**が並ぶので、人を直接ぶら下げると**人と装置が兄弟になり**、装置が
// 増えるほど人が埋もれます。`担当者` を1枚かませて分けます:
//
//	取引先／株式会社トーアスポーツマシーン／担当者／潮崎 光俊
//	取引先／株式会社トーアスポーツマシーン／オールラウンド2輪／脚取付台
//
// **人のページは要るときだけ**です。連絡先そのもの（メールアドレス・電話番号）は
// 社名ページにタグとして何個でも置けるので、**その人に添付や記録を紐づけたく
// なったとき**に初めてページにします。**作る操作は未実装**。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"fmt"
	stdhtml "html"
	"net/http"
	"sort"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// ContactPersonBoxTitle は社名ページの下の、窓口の人を集める箱の名前です
// （`取引先／社名／担当者／名前`）。**装置名称と人を兄弟にしない**ための1枚。
// 語を1箇所に閉じておくのは「担当」と「担当者」が混ざるのを防ぐためです。
const ContactPersonBoxTitle = "担当者"

// PartnerBoxTitle は相手ページの置き場（トップ直下）の名前です。通信箱
// （MailBoxTitle）・テンプレート置き場と同じく **h1（ページ名）が正**。
const PartnerBoxTitle = "取引先"

// PartnerBoxPageID はトップ直下の取引先ページを返します（無ければ ok=false）。
func PartnerBoxPageID() (string, bool) { return topLevelPageByTitle(PartnerBoxTitle) }

// EnsurePartnerBox は取引先ページを返し、**無ければ作ります**。
//
// **通信箱と違って自動で作ります。** 通信箱は「そこへ落とすと取り込みが走る」という
// 機能の入口なので、人が意図して置くものです。取引先はただの置き場——無いからと
// 登録ボタンを行き止まりにする理由がありません。名前を変えられたら次の登録で
// また作られますが、**取り込みのように静かに壊れることはありません**。
//
// 権限は呼ぶ側が見ます（作るときはトップへの書き込み、あるときは箱への書き込み）。
func EnsurePartnerBox(user *auth.User) (string, error) {
	if id, ok := PartnerBoxPageID(); ok {
		return id, nil
	}
	return CreateChildPage(TopPageID, user.Username,
		"<h1>"+stdhtml.EscapeString(PartnerBoxTitle)+"</h1><p><br/></p>")
}

// EmailTag は連絡先のメールアドレスです。**1ページに何個でも置けます**
// （会社の窓口が複数、同じ人が複数のアドレスを持つ、どちらも起きる）。
const EmailTag = "メールアドレス"

// RelationTag は取引の役割です（値は RelationCustomer / RelationSupplier / RelationSelf）。
// **同じページに複数書けます**——顧客でも仕入先でもある相手が実際にいるためです。
const RelationTag = "取引"

// 取引の値。**表引きで閉じます**——`仕入先` と `仕入れ先` が混ざると絞り込みが
// 静かに取りこぼします。
const (
	RelationCustomer = "顧客"
	RelationSupplier = "仕入先"
	RelationSelf     = "自社"
)

// Relations は選択肢を並び順つきで返します（画面が使います）。
func Relations() []string { return []string{RelationCustomer, RelationSupplier, RelationSelf} }

// addressFields は索引からアドレスを拾う項目です。取り込みが書いている名前
// （intake_eml.go の writeAddressTags）とそろえること。
var addressFields = []string{"差出人アドレス", "宛先アドレス", "CCアドレス", "返信先アドレス"}

// nameOfAddressField は、そのアドレス項目と対になる表示名の項目です。
var nameOfAddressField = map[string]string{
	"差出人アドレス": "差出人",
	"宛先アドレス":  "宛先",
	"CCアドレス":  "CC",
	"返信先アドレス": "返信先",
}

// UnknownContact は「まだページになっていない相手」1件（ドメイン単位）です。
//
// **ドメインでまとめるのは、会社が1枚に収まるようにするため**です。4人のアドレスを
// 別々に登録すると相手ページが4枚になり、あとから1枚へまとめ直すことになります。
type UnknownContact struct {
	Domain    string   // toa-sports-machine.co.jp
	Name      string   // いちばんよく出た表示名（登録の初期値）
	Addresses []string // そのドメインのアドレス（昇順）
	Count     int      // 索引に出てきた延べ回数（多い順に並べるため）
}

// UnknownContacts は、索引にあってページになっていない相手をドメインごとに返します。
func UnknownContacts(user *auth.User) ([]UnknownContact, error) {
	known, err := knownEmails()
	if err != nil {
		return nil, err
	}

	type acc struct {
		addrs map[string]int
		names map[string]int
		count int
	}
	byDomain := map[string]*acc{}

	for _, field := range addressFields {
		rows, err := database.DB.Query(`
			SELECT page_id, value FROM vocab_index WHERE field = ?
		`, field)
		if err != nil {
			return nil, err
		}
		// **先に読み切ってから解釈します**（行を読みながら別のクエリを投げない）。
		type hit struct {
			pageID int
			value  string
		}
		var hits []hit
		for rows.Next() {
			var h hit
			if err := rows.Scan(&h.pageID, &h.value); err != nil {
				rows.Close()
				return nil, err
			}
			hits = append(hits, h)
		}
		rows.Close()
		if err := rows.Err(); err != nil {
			return nil, err
		}

		for _, h := range hits {
			addr := normalizeEmail(h.value)
			if addr == "" || known[addr] {
				continue
			}
			// **読めないページの相手は数えません**（見せ分けC案——黙って落ちる）。
			if !page.CanView(user, h.pageID) {
				continue
			}
			d := domainOf(addr)
			a := byDomain[d]
			if a == nil {
				a = &acc{addrs: map[string]int{}, names: map[string]int{}}
				byDomain[d] = a
			}
			a.addrs[addr]++
			a.count++
			if n := displayNameFor(h.pageID, nameOfAddressField[field]); n != "" {
				a.names[n]++
			}
		}
	}

	out := make([]UnknownContact, 0, len(byDomain))
	for d, a := range byDomain {
		c := UnknownContact{Domain: d, Count: a.count, Name: mostCommon(a.names)}
		for addr := range a.addrs {
			c.Addresses = append(c.Addresses, addr)
		}
		sort.Strings(c.Addresses)
		if c.Name == "" {
			c.Name = c.Addresses[0] // 表示名が無ければアドレスを初期値にする
		}
		out = append(out, c)
	}
	// 多い順（よく来る相手から登録できるように）。同数はドメイン順で安定させる。
	sort.Slice(out, func(i, j int) bool {
		if out[i].Count != out[j].Count {
			return out[i].Count > out[j].Count
		}
		return out[i].Domain < out[j].Domain
	})
	return out, nil
}

// knownEmails は既にページに登録済みのアドレスを集めます。
func knownEmails() (map[string]bool, error) {
	rows, err := database.DB.Query(`SELECT value FROM vocab_index WHERE field = ?`, EmailTag)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	known := map[string]bool{}
	for rows.Next() {
		var v string
		if err := rows.Scan(&v); err != nil {
			return nil, err
		}
		if a := normalizeEmail(v); a != "" {
			known[a] = true
		}
	}
	return known, rows.Err()
}

// displayNameFor は同じページの表示名タグを1つ読みます（無ければ空）。
func displayNameFor(pageID int, field string) string {
	if field == "" {
		return ""
	}
	var v string
	err := database.DB.QueryRow(
		`SELECT value FROM vocab_index WHERE page_id = ? AND field = ? LIMIT 1`,
		pageID, field).Scan(&v)
	if err != nil {
		return ""
	}
	// 取り込みが引用符ごと拾うことがある（`'南 公一'`）。表示のためだけなので落とす。
	return strings.Trim(strings.TrimSpace(v), `'"`)
}

// normalizeEmail はアドレスを突き合わせ用に畳みます。
//
// **大小の違いだけ**を畳みます。アドレスのローカル部は理屈のうえでは大小を
// 区別しますが、実際に区別する事業者はまずおらず、**同じ人を2人に見せる**ほうが害が大きい。
func normalizeEmail(v string) string {
	s := strings.ToLower(strings.Trim(strings.TrimSpace(v), `<>'"`))
	if !strings.Contains(s, "@") || strings.HasPrefix(s, "@") || strings.HasSuffix(s, "@") {
		return ""
	}
	return s
}

// domainOf はアドレスのドメイン部を返します。
func domainOf(addr string) string {
	if i := strings.LastIndex(addr, "@"); i >= 0 {
		return addr[i+1:]
	}
	return addr
}

// mostCommon はいちばん多く出た文字列を返します（同数なら辞書順で安定させる）。
func mostCommon(counts map[string]int) string {
	best, bestN := "", 0
	for s, n := range counts {
		if n > bestN || (n == bestN && s < best) {
			best, bestN = s, n
		}
	}
	return best
}

// contactsViewHTML は「未登録の連絡先」の作業面です。
func contactsViewHTML(user *auth.User, pageIDInt int) string {
	list, err := UnknownContacts(user)
	if err != nil {
		return `<p class="view-error">連絡先の一覧を作れませんでした。</p>`
	}

	var sb strings.Builder
	sb.WriteString(`<h3 class="materials-title">📇 未登録の連絡先（` +
		fmt.Sprint(len(list)) + `件）</h3>`)
	if len(list) == 0 {
		sb.WriteString(`<p class="child-list-empty">未登録の連絡先はありません</p>`)
		return sb.String()
	}
	sb.WriteString(`<p class="unhandled-note">` +
		`同じドメインはまとめてあります——会社を1枚に保つためです。` +
		`名前を直してから、どの取引かを押してください（顧客と仕入先はあとから足せます）。` +
		`ページは「` + PartnerBoxTitle + `」の下にできます。</p>`)

	sb.WriteString(`<table class="materials-table unhandled-table"><tbody>`)
	for _, c := range list {
		sb.WriteString(`<tr data-domain="` + stdhtml.EscapeString(c.Domain) + `">`)
		sb.WriteString(`<td class="vocab-chrome contact-name">` +
			`<input type="text" class="contact-name-input" maxlength="120" value="` +
			stdhtml.EscapeString(c.Name) + `"></td>`)
		sb.WriteString(`<td class="contact-addrs">`)
		for i, a := range c.Addresses {
			if i > 0 {
				sb.WriteString(`<br>`)
			}
			sb.WriteString(stdhtml.EscapeString(a))
		}
		sb.WriteString(`</td>`)
		sb.WriteString(`<td class="unhandled-clip">` + fmt.Sprint(c.Count) + `件</td>`)
		sb.WriteString(`<td class="vocab-chrome unhandled-act">`)
		for _, rel := range Relations() {
			sb.WriteString(`<button type="button" class="chip-btn contact-register"` +
				` data-relation="` + stdhtml.EscapeString(rel) + `"` +
				` data-addresses="` + stdhtml.EscapeString(strings.Join(c.Addresses, ",")) + `"` +
				` title="この相手を「` + PartnerBoxTitle + `」の下のページにします（取引：` +
				stdhtml.EscapeString(rel) + `）">` + stdhtml.EscapeString(rel) + `</button>`)
		}
		sb.WriteString(`</td></tr>`)
	}
	sb.WriteString(`</tbody></table>`)
	return sb.String()
}

// RegisterContactAPIHandler は POST /api/contacts/register です。
// 入力: {"name":"株式会社高瀬製作所", "relation":"仕入先", "addresses":["…"]}
func RegisterContactAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		JSONFail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	user := auth.CurrentUser(r)
	if user == nil {
		JSONFail(w, http.StatusForbidden, "ログインが必要です")
		return
	}
	var req struct {
		Name      string   `json:"name"`
		Relation  string   `json:"relation"`
		Addresses []string `json:"addresses"`
	}
	if !DecodeJSONBody(w, r, &req) {
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		JSONFail(w, http.StatusBadRequest, "名前を入れてください")
		return
	}
	if !validRelation(req.Relation) {
		JSONFail(w, http.StatusBadRequest, "取引の種類が不正です")
		return
	}
	var addrs []string
	for _, a := range req.Addresses {
		if v := normalizeEmail(a); v != "" {
			addrs = append(addrs, v)
		}
	}
	if len(addrs) == 0 {
		JSONFail(w, http.StatusBadRequest, "メールアドレスがありません")
		return
	}

	// **相手ページは「取引先」の下**（顧客名ページと同じ場所——会社を2枚にしない）。
	// 箱がまだ無いときはトップへ1枚足すので、**トップへの書き込み**が要ります。
	needParent := TopPageID
	if id, ok := PartnerBoxPageID(); ok {
		needParent = id
	}
	if !page.RequirePageWrite(w, r, needParent) {
		return
	}
	boxID, err := EnsurePartnerBox(user)
	if err != nil {
		JSONFail(w, http.StatusInternalServerError, "「"+PartnerBoxTitle+"」ページを作れません: "+err.Error())
		return
	}

	var b strings.Builder
	b.WriteString("<h1>" + stdhtml.EscapeString(name) + "</h1>")
	b.WriteString(`<dl data-type="tags">`)
	WriteTag(&b, RelationTag, req.Relation)
	for _, a := range addrs {
		WriteTag(&b, EmailTag, a)
	}
	b.WriteString("</dl>")
	// 電話番号は空で置きます——**書く場所が見えていれば、人は書きます**
	// （タグがあれば ☎ 発信のボタンも出ます・app.js）。
	b.WriteString(`<p><br/></p>`)

	pageID, err := CreateChildPage(boxID, user.Username, b.String())
	if err != nil {
		JSONFail(w, http.StatusInternalServerError, "相手ページを作れません: "+err.Error())
		return
	}
	auth.Audit(user.Username, "contact.register", pageID+" ("+req.Relation+") "+strings.Join(addrs, ","))
	json.NewEncoder(w).Encode(map[string]any{
		"success": true, "page_id": pageID, "title": name,
	})
}

// validRelation は取引の値を表引きで確かめます。
func validRelation(v string) bool {
	for _, r := range Relations() {
		if r == v {
			return true
		}
	}
	return false
}
