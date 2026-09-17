package contacts

// ─────────────────────────────────────────────────────────────────────────
// アドレス帳の口（contacts.go から分離）
//
//	POST /api/contacts/register … 相手をページにする（新規・既存へ足す・担当者にする）
//	POST /api/contacts/unfile   … 未分類へ戻す（本文から `メールアドレス` を外す）
//
// **戻せることが要ります**（2026-09-13 ユーザー:「間違えてアドレスを分類した場合、
// どうやって未分類に戻しますか？」）。タグを外せば作業面へ戻ります——登録の印は
// タグそのものなので、別に状態を持ちません。
//
// ⚠ **どちらも本文を読んで・変えて・書きます。** エディタが開いていると上書きし
// 合うので、`editlock.RefuseWhileEditing` を通します（2026-09-14）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	stdhtml "html"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// RegisterContactAPIHandler は POST /api/contacts/register です。
// 入力: {"name":"株式会社緑川製作所", "relation":"仕入先", "addresses":["…"]}
func RegisterContactAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		cms.JSONFail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	user := auth.CurrentUser(r)
	if user == nil {
		cms.JSONFail(w, http.StatusForbidden, "ログインが必要です")
		return
	}
	var req struct {
		Name      string   `json:"name"`
		Relation  string   `json:"relation"`
		Addresses []string `json:"addresses"`
		// PageID があれば**既存の相手ページへ足します**（2026-09-06）。同じ会社が
		// 2つ目のドメインから送ってくると、アドレス帳には新しい行として現れます
		// ——そこで新しいページを作ると、会社ページが2枚になります。
		PageID string `json:"page_id"`
		// PersonName があれば、その会社の**担当者ページ**へ入れます
		// （`取引先／社名／担当者／氏名`・2026-09-13）。連絡先は1人にタグが何個も
		// ぶら下がるので（メール・電話・役職）、**人ごとの器**が要ります。
		// 空なら会社の口として社名ページへ（`order@…` のような人でないもの）。
		PersonName string `json:"person_name"`
		// Domains は**この組織のドメイン**です（`ドメイン` タグ・2026-09-16）。
		//
		// ⚠ **いまこれを送る画面はありません**（2026-09-17）。未登録の連絡先の行に
		// あったチェックは外しました——役に立つのは「新しい組織＋専用のドメイン」の
		// 1場面だけなのに、既定で入っていたので**共有のドメインを組織に結ぶ**ほうが
		// 起きやすかったためです（`contacts_view.go` に経緯）。
		// **口は残します**——ドメインは組織の性質で、宣言する道がプログラムからも
		// 要るからです（`deleteToken` と同じ、意図して残した土台）。
		Domains []string `json:"domains"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	var addrs []string
	for _, a := range req.Addresses {
		if v := normalizeEmail(a); v != "" {
			addrs = append(addrs, v)
		}
	}
	if len(addrs) == 0 {
		cms.JSONFail(w, http.StatusBadRequest, "メールアドレスがありません")
		return
	}
	person := cms.NormalizeNameForIngest(req.PersonName)
	doms := normalizeDomains(req.Domains)

	// ── 行き先の組織を決める（2026-09-17 に3つの道を1本にした）──
	//
	// ① `page_id` で名指し ② 題の完全一致で既にある組織 ③ 無ければ新しく作る。
	// **②が要るのは、組織がコンボボックスになったから**——候補に無い名前を打っても、
	// 同じ題の組織が既にあればそこへ寄せます（会社ページを2枚にしない）。
	// 揺れは吸収しません（`PartnerByTitle` は完全一致・2枚あれば引かない）。
	target := strings.TrimSpace(req.PageID)
	orgTitle := ""
	if target != "" {
		norm, ok := page.NormalizeID(target)
		if !ok {
			cms.JSONFail(w, http.StatusBadRequest, "相手ページのIDが不正です")
			return
		}
		idInt, err := strconv.Atoi(norm)
		if err != nil || !isPartnerPage(idInt) {
			// **箱の外へは足しません**（ドメインの逆引きが別物を拾うため）。
			cms.JSONFail(w, http.StatusBadRequest, "「"+ContactsBoxTitle+"」の下のページを選んでください")
			return
		}
		target, orgTitle = norm, cms.PageTitleByID(idInt)
	} else {
		// **相手ページの題も早期に正規化します**（2026-09-06）。部品階層の顧客名と
		// 題で結ぶので、こちらだけ畳まないと食い違います。
		orgTitle = cms.NormalizeNameForIngest(req.Name)
		if orgTitle == "" {
			cms.JSONFail(w, http.StatusBadRequest, "組織の名前を入れてください（社名か「"+PersonalOrgTitle+"」）")
			return
		}
		if id, ok := PartnerByTitle(user, orgTitle); ok {
			target = id
		}
	}
	// 「個人」は人の器です——取引は人に付き、ドメイン（共有のもの）は付けません。
	personal := orgTitle == PersonalOrgTitle
	if personal && person == "" {
		cms.JSONFail(w, http.StatusBadRequest, "「"+PersonalOrgTitle+"」のときは担当者の名前を入れてください")
		return
	}
	if personal {
		doms = nil
	}

	created := false
	if target == "" {
		// ── 新しい組織ページを作る ──
		//
		// ⚠ **取引（`relation`）は任意です**（2026-09-17）。送る画面が無くなったので、
		// 空なら `取引` のタグを書きません——必要になったとき組織のページで足します。
		// 値が付いていれば書きますが、表に無い値は断ります（黙って捨てると、書いたつもりの
		// 値が消えます）。
		if req.Relation != "" && !validRelation(req.Relation) {
			cms.JSONFail(w, http.StatusBadRequest, "取引の種類が不正です")
			return
		}
		// **相手ページは「連絡帳」の下**。箱がまだ無いときはトップへ1枚足すので、
		// **トップへの書き込み**が要ります。
		needParent := cms.TopPageID
		if id, ok := ContactsBoxPageID(); ok {
			needParent = id
		}
		if !page.RequirePageWrite(w, r, needParent) {
			return
		}
		boxID, err := EnsureContactsBox(user)
		if err != nil {
			cms.JSONFail(w, http.StatusInternalServerError, "「"+ContactsBoxTitle+"」ページを作れません: "+err.Error())
			return
		}
		// **タグが1つも無ければ `dl` ごと書きません**（空の形式ブロックを置かない）。
		var tags strings.Builder
		if !personal {
			cms.WriteTag(&tags, RelationTag, req.Relation)
		}
		// 担当者が居なければ、アドレスは組織の口として組織のページへ（人が居れば下で人へ）。
		if person == "" {
			for _, a := range addrs {
				cms.WriteTag(&tags, EmailTag, a)
			}
		}
		// **組織の連絡先**（2026-09-16）。これがあると、同じドメインの**新しい人**からの
		// 初メールも、この組織に結びつきます。⚠ 共有ドメインに付けると、そのドメインの
		// 他人まで引き寄せます——だから**人が組織のページで書きます**（2026-09-17 に画面の
		// チェックを外した。口はここに残っている）。
		for _, d := range doms {
			cms.WriteTag(&tags, DomainTag, d)
		}
		var b strings.Builder
		b.WriteString("<h1>" + stdhtml.EscapeString(orgTitle) + "</h1>")
		if tags.Len() > 0 {
			b.WriteString(`<dl data-type="tags">` + tags.String() + "</dl>")
		}
		// 電話番号は空で置きます——**書く場所が見えていれば、人は書きます**。
		b.WriteString(`<p><br/></p>`)
		target, err = cms.CreateChildPage(boxID, user.Username, b.String())
		if err != nil {
			cms.JSONFail(w, http.StatusInternalServerError, "相手ページを作れません: "+err.Error())
			return
		}
		created = true
		auth.Audit(user.Username, "contact.register",
			target+" ("+req.Relation+") "+strings.Join(addrs, ",")+domainsForAudit(doms))
	} else if !page.RequirePageWrite(w, r, target) {
		return
	}
	// **開いている人が居たら断ります**（2026-09-14）。本文を読んで・変えて・書くので、
	// エディタが開いているとオートセーブと上書きし合います。
	if !editlock.RefuseWhileEditing(w, target) {
		return
	}

	// ── 人の器へ（2026-09-13）──
	// 社名ページにアドレスを平らに積むと、6つ並んだ `メールアドレス` が誰のものか
	// 分からなくなり、電話番号を足す先もありません。**組織の直下に人**（2026-09-16）。
	dest, destTitle := target, orgTitle
	added := 0
	var err error
	if person != "" {
		pid, err := EnsureContactPerson(user, target, person)
		if err != nil {
			cms.JSONFail(w, http.StatusInternalServerError, "担当者ページを作れません: "+err.Error())
			return
		}
		dest, destTitle = pid, orgTitle+"／"+person
		if !editlock.RefuseWhileEditing(w, dest) {
			return
		}
		// 個人のお客様は**取引が人に付きます**（ユーザー決定）。
		if personal && validRelation(req.Relation) {
			if _, err := addContactTags(dest, user.Username, RelationTag, []string{req.Relation}); err != nil {
				cms.JSONFail(w, http.StatusInternalServerError, "取引を書けません: "+err.Error())
				return
			}
		}
		added, err = AddContactAddresses(dest, user.Username, addrs)
	} else if created {
		added = len(addrs) // 作るときに本文へ書き込み済み
	} else {
		added, err = AddContactAddresses(dest, user.Username, addrs)
	}
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "相手ページへ足せません: "+err.Error())
		return
	}
	// **ドメインは組織のページへ**（2026-09-16）。人のページには付けません——組織の
	// 連絡先だからです。**2つ目のドメインはここで足ります**（実データの自社が
	// `example-works.co.jp` と `itohocorp.onmicrosoft.com` の2つを持つ）。
	domainsAdded := 0
	if !created && len(doms) > 0 {
		domainsAdded, err = AddContactDomains(target, user.Username, doms)
		if err != nil {
			cms.JSONFail(w, http.StatusInternalServerError, "ドメインを足せません: "+err.Error())
			return
		}
	}
	if !created {
		auth.Audit(user.Username, "contact.add-addresses",
			dest+" +"+strconv.Itoa(added)+" "+strings.Join(addrs, ",")+domainsForAudit(doms))
	}
	json.NewEncoder(w).Encode(map[string]any{
		"success": true, "page_id": dest, "title": destTitle,
		"added": added, "domains_added": domainsAdded,
		"merged": !created, "created": created,
	})
}

// UnfileContactAPIHandler は POST /api/contacts/unfile です。
// 入力: {"page_id":"010274", "address":"ms-noreply@microsoft.com"}
//
// **分類を取り消して未分類へ戻します**（2026-09-13 ユーザー:「間違えてアドレスを
// 分類した場合、どうやって未分類に戻しますか？」）。
//
// やることは**タグを1つ外すだけ**です。未登録の一覧は「どこにも
// `メールアドレス` タグが無いアドレス」という**索引からの派生**なので、
// 外せば自動的に戻ります（解析済みの印と同じ形——状態を別に持たない）。
//
// **ページは消しません。** 間違えて作った相手ページが空になることはありますが、
// 消すかどうかは人が決めます——押し間違いの取り消しが、**別の押し間違いで
// ページを消す**ことになっては割に合いません。空になったことは返り値で伝えます。
func UnfileContactAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		cms.JSONFail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	user := auth.CurrentUser(r)
	if user == nil {
		cms.JSONFail(w, http.StatusForbidden, "ログインが必要です")
		return
	}
	var req struct {
		PageID  string `json:"page_id"`
		Address string `json:"address"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, ok := page.NormalizeID(strings.TrimSpace(req.PageID))
	if !ok {
		cms.JSONFail(w, http.StatusBadRequest, "ページIDが不正です")
		return
	}
	addr := normalizeEmail(req.Address)
	if addr == "" {
		cms.JSONFail(w, http.StatusBadRequest, "メールアドレスがありません")
		return
	}
	idInt, err := strconv.Atoi(pageID)
	if err != nil {
		cms.JSONFail(w, http.StatusBadRequest, "ページIDが不正です")
		return
	}
	// **取引先の下だけ**（ここは連絡先の分類を取り消す口で、本文の一般的な編集口では
	// ありません。よそのページのタグを消せる道を増やさない）。
	if _, _, inPartner := PartnerOfPage(idInt); !inPartner {
		cms.JSONFail(w, http.StatusBadRequest, "「"+ContactsBoxTitle+"」の下のページではありません")
		return
	}
	if !page.RequirePageWrite(w, r, pageID) {
		return
	}
	if !editlock.RefuseWhileEditing(w, pageID) {
		return
	}

	removed := 0
	if err := cms.RewriteBody(pageID, user.Username, func(current string) string {
		removed = 0
		return removeEmailTag(current, addr, &removed)
	}); err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "外せません: "+err.Error())
		return
	}
	if removed == 0 {
		cms.JSONFail(w, http.StatusNotFound, "そのアドレスはこのページにありません")
		return
	}
	auth.Audit(user.Username, "contact.unfile", pageID+" -"+addr)

	// 残りを数えて、**空になったことだけ伝えます**（消すのは人の判断）。
	var left int
	database.DB.QueryRow(
		`SELECT COUNT(*) FROM page_tags WHERE page_id = ? AND name = ?`,
		idInt, EmailTag).Scan(&left)
	var children int
	database.DB.QueryRow(`SELECT COUNT(*) FROM pages WHERE parent_id = ?`, idInt).Scan(&children)

	json.NewEncoder(w).Encode(map[string]any{
		"success": true, "page_id": pageID, "address": addr,
		"remaining": left, "children": children,
		"empty": left == 0 && children == 0,
	})
}

// removeEmailTag は本文から `<dt>メールアドレス</dt><dd>そのアドレス</dd>` を外します。
//
// **畳んだ一致で探します**——本文には大文字混じりで書かれていることがあるためです
// （`normalizeEmail` は小文字化と前後の空白落としだけ）。
// 空になった可変タグの `dl` も畳みます（空の箱を残さない）。
func removeEmailTag(body, addr string, removed *int) string {
	re := regexp.MustCompile(`<dt>` + regexp.QuoteMeta(EmailTag) + `</dt><dd>([^<]*)</dd>`)
	out := re.ReplaceAllStringFunc(body, func(m string) string {
		sub := re.FindStringSubmatch(m)
		if sub != nil && normalizeEmail(sub[1]) == addr {
			*removed++
			return ""
		}
		return m
	})
	// `<dl data-type="tags"></dl>` が残ったら外す。
	return regexp.MustCompile(`<dl data-type="tags">\s*</dl>`).ReplaceAllString(out, "")
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

// normalizeDomains は受け取ったドメインを畳み、重複と空を落とします。
func normalizeDomains(in []string) []string {
	seen := map[string]bool{}
	out := make([]string, 0, len(in))
	for _, d := range in {
		n := normalizeDomain(d)
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		out = append(out, n)
	}
	return out
}

// domainsForAudit は監査記録へ添えるドメインの並びです（無ければ空文字）。
//
// **記録に残すのは、あとから「なぜこのドメインが付いたのか」を辿れるように**です
// ——共有ドメインを付けてしまうと、そのドメインの他人まで引き寄せます。
func domainsForAudit(domains []string) string {
	if len(domains) == 0 {
		return ""
	}
	return " domains=" + strings.Join(domains, ",")
}
