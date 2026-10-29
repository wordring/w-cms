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
// 2026-10-12 からブラウザは編集権を取らないので、この関門が断るのは道具（`/api/lock`）が握っているときだけです。
// 人の保存とは、`cms.RewriteBody` の書き込みの錠（`cms.LockPageBody`）で順に並びます。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"sync"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// RegisterContactAPIHandler は POST /api/contacts/register です。
// 入力: {"name":"株式会社緑川製作所", "addresses":["…"]}
func RegisterContactAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		Name      string   `json:"name"`
		Addresses []string `json:"addresses"`
		// PageID があれば**既存の相手ページへ足します**（2026-09-06）。同じ会社が
		// 2つ目のドメインから送ってくると、アドレス帳には新しい行として現れます
		// ——そこで新しいページを作ると、会社ページが2枚になります。
		PageID string `json:"page_id"`
		// PersonName があれば、その会社の**担当者ページ**へ入れます
		// （いまは `連絡帳／組織／人`・2026-09-16。2026-09-13 は `取引先／社名／担当者／氏名`）。連絡先は1人にタグが何個も
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

	// 登録は1つずつ通す——2つ同時に（二度押し・2人）登録すると、どちらも「まだ無い」と見て、同じ名前の組織・人の
	// ページが2枚できた（2026-10-22 の見直し）。登録はまれなので、全部で1つのロックにする。
	registerMu.Lock()
	defer registerMu.Unlock()
	target, orgTitle, ok := resolveContactOrg(w, user, req.PageID, req.Name)
	if !ok {
		return
	}
	// 「個人」は人の器です。ドメイン（共有のもの）は付けません。
	personal := orgTitle == PersonalOrgTitle
	if personal && person == "" {
		cms.JSONFail(w, http.StatusBadRequest, "「"+PersonalOrgTitle+"」のときは担当者の名前を入れてください")
		return
	}
	if personal {
		doms = nil
	}

	if !requireContactTemplates(w, target, person) {
		return
	}

	created := false
	if target == "" {
		if target, ok = createContactOrg(w, r, user, orgTitle, person, addrs, doms); !ok {
			return
		}
		created = true
	} else if !page.RequirePageWrite(w, r, target) {
		return
	}
	// **開いている人が居たら断ります**（2026-09-14）。本文を読んで・変えて・書くので、
	// エディタが開いているとオートセーブと上書きし合います。
	// （2026-10-12 から、断るのは道具〔`/api/lock`〕が握っているときだけ——ファイルの冒頭。）
	// テンプレートの中へは書かない（2026-10-20 にほかの口とそろえた——コアの RefuseTemplateArea）。
	if !cms.RefuseTemplateArea(w, target) || !editlock.RefuseWhileEditing(w, target) {
		return
	}

	// ── 人の器へ（2026-09-13）──
	// 社名ページにアドレスを平らに積むと、6つ並んだ `メールアドレス` が誰のものか
	// 分からなくなり、電話番号を足す先もありません。**組織の直下に人**（2026-09-16）。
	dest, destTitle := target, orgTitle
	added := 0
	var err error
	if person != "" {
		// ⚠ `:=` で err を作り直さないこと（2026-09-21 に staticcheck が見つけた）。
		// 内側の err に代入すると、下の `if err != nil` は外側の nil を見て
		// **足せなかった事実が黙って落ちます**。
		pid, perr := EnsureContactPerson(user, target, person)
		if perr != nil {
			cms.JSONFail(w, http.StatusInternalServerError, "担当者ページを作れません: "+perr.Error())
			return
		}
		dest, destTitle = pid, orgTitle+"／"+person
		// 既にある人のページへ書くときも、そのページに書けるかを見る（組織に書ければ、読めない人のページへも書き込めた・
		// 2026-10-22 の見直し）。
		if !page.RequirePageWrite(w, r, dest) || !cms.RefuseTemplateArea(w, dest) || !editlock.RefuseWhileEditing(w, dest) {
			return
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
	// `example-works.co.jp` と `exampleworks.onmicrosoft.com` の2つを持つ）。
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
	cms.WriteJSON(w, map[string]any{
		"success": true, "page_id": dest, "title": destTitle,
		"added": added, "domains_added": domainsAdded,
		"merged": !created, "created": created,
	})
}

// resolveContactOrg は登録の行き先の組織を決めます（2026-10-20 に RegisterContactAPIHandler から分けた）。
// target が空なら新しく作る組織（題は orgTitle）。通らなければ断りを書いて false。
func resolveContactOrg(w http.ResponseWriter, user *auth.User, pageID, name string) (target, orgTitle string, ok bool) {
	// ── 行き先の組織を決める（2026-09-17 に3つの道を1本にした）──
	//
	// ① `page_id` で名指し ② 題の完全一致で既にある組織 ③ 無ければ新しく作る。
	// **②が要るのは、組織がコンボボックスになったから**——候補に無い名前を打っても、
	// 同じ題の組織が既にあればそこへ寄せます（会社ページを2枚にしない）。
	// 揺れは吸収しません（`PartnerByTitle` は完全一致・2枚あれば引かない）。
	target = strings.TrimSpace(pageID)
	if target != "" {
		norm, ok := page.NormalizeID(target)
		if !ok {
			cms.JSONFail(w, http.StatusBadRequest, "相手ページのIDが不正です")
			return "", "", false
		}
		idInt, err := strconv.Atoi(norm)
		if err != nil || !isPartnerPage(idInt) {
			// **箱の外へは足しません**（ドメインの逆引きが別物を拾うため）。
			cms.JSONFail(w, http.StatusBadRequest, "「"+ContactsBoxTitle+"」の下のページを選んでください")
			return "", "", false
		}
		target, orgTitle = norm, cms.PageTitleByID(idInt)
	} else {
		// **相手ページの題も早期に正規化します**（2026-09-06）。加工製品の階層の顧客名と
		// 題で結ぶので、こちらだけ畳まないと食い違います。
		orgTitle = cms.NormalizeNameForIngest(name)
		if orgTitle == "" {
			cms.JSONFail(w, http.StatusBadRequest, "組織の名前を入れてください（社名か「"+PersonalOrgTitle+"」）")
			return "", "", false
		}
		if id, ok := PartnerByTitle(user, orgTitle); ok {
			target = id
		} else if box, found := contactsBoxInt(); found {
			// 同じ名前の組織が既にある（見えない・2枚あって決められない）のに作ると、同じ名前のページが増え続けた
			// （2026-10-22 の見直し）。作らずに、一覧から選んでもらう。
			if _, exists := cms.FindChildByTitle(page.FormatID(box), orgTitle); exists {
				cms.JSONFail(w, http.StatusConflict, "「"+orgTitle+"」という組織のページが既にあります（見えないか、同じ名前が2つあります）。候補から選ぶか、管理者に聞いてください")
				return "", "", false
			}
		}
	}
	return target, orgTitle, true
}

// registerMu は連絡先の登録（探して無ければ作る）を1つずつ通すロックです。
var registerMu sync.Mutex

// requireContactTemplates は、作る前に要るテンプレートがあるかを確かめます（無ければ断りを書いて false）。
func requireContactTemplates(w http.ResponseWriter, target, person string) bool {
	// ⚠ **作り始める前に、要るテンプレートを確かめます**（2026-09-27・テンプレート駆動の D）
	// ——組織を作ったあとで人のテンプレートが無いと分かると、組織だけが残ります。
	var need []string
	if target == "" {
		need = append(need, OrgTemplate)
	}
	if person != "" {
		if _, found := cms.FindChildByTitle(target, cms.NormalizeNameForIngest(person)); target == "" || !found {
			need = append(need, PersonTemplate)
		}
	}
	for _, tt := range need {
		if _, err := cms.PageTemplateBody(tt); err != nil {
			cms.JSONFail(w, http.StatusConflict, "連絡先を登録できません: "+err.Error())
			return false
		}
	}
	return true
}

// createContactOrg は新しい組織ページを「連絡帳」の下に作ります（2026-10-20 に RegisterContactAPIHandler から分けた）。
// 作れなければ断りを書いて false。
func createContactOrg(w http.ResponseWriter, r *http.Request, user *auth.User, orgTitle, person string, addrs, doms []string) (string, bool) {
	// ── 新しい組織ページを作る ──
	// **相手ページは「連絡帳」の下**。箱がまだ無いときはトップへ1枚足すので、
	// **トップへの書き込み**が要ります。
	needParent := cms.TopPageID
	if id, ok := ContactsBoxPageID(); ok {
		needParent = id
	}
	if !page.RequirePageWrite(w, r, needParent) {
		return "", false
	}
	boxID, err := EnsureContactsBox(user)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "「"+ContactsBoxTitle+"」ページを作れません: "+err.Error())
		return "", false
	}
	// **本文はテンプレート「連絡帳の組織」を写して組みます**（2026-09-27）——電話番号など
	// **人が書く欄**はテンプレートが持ちます（それまでは空の段落1つ:「書く場所が見えて
	// いれば、人は書きます」）。機械が入れるのは題と、ここで分かったアドレス・ドメインだけ。
	d, err := cms.DraftFromTemplate(OrgTemplate)
	if err != nil {
		cms.JSONFail(w, http.StatusConflict, "相手ページを作れません: "+err.Error())
		return "", false
	}
	d.SetTitle(orgTitle)
	// 担当者が居なければ、アドレスは組織の口として組織のページへ（人が居れば下で人へ）。
	if person == "" {
		d.SetTag(EmailTag, addrs...)
	}
	// **組織の連絡先**（2026-09-16）。これがあると、同じドメインの**新しい人**からの
	// 初メールも、この組織に結びつきます。⚠ 共有ドメインに付けると、そのドメインの
	// 他人まで引き寄せます——だから**人が組織のページで書きます**（2026-09-17 に画面の
	// チェックを外した。口はここに残っている）。
	d.SetTag(DomainTag, doms...)
	target, err := cms.CreateChildPage(boxID, user.Username, d.HTML())
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "相手ページを作れません: "+err.Error())
		return "", false
	}
	auth.Audit(user.Username, "contact.register",
		target+" "+strings.Join(addrs, ",")+domainsForAudit(doms))
	return target, true
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
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID  string `json:"page_id"`
		Address string `json:"address"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, ok := cms.PageIDOrFail(w, strings.TrimSpace(req.PageID))
	if !ok {
		return
	}
	addr := normalizeEmail(req.Address)
	if addr == "" {
		cms.JSONFail(w, http.StatusBadRequest, "メールアドレスがありません")
		return
	}
	idInt, _ := strconv.Atoi(pageID) // NormalizeID を通った値は必ず数
	// **連絡帳の下だけ**（2026-09-16 までは取引先。ここは連絡先の分類を取り消す口で、本文の一般的な編集口では
	// ありません。よそのページのタグを消せる道を増やさない）。
	if _, _, inPartner := PartnerOfPage(idInt); !inPartner {
		cms.JSONFail(w, http.StatusBadRequest, "「"+ContactsBoxTitle+"」の下のページではありません")
		return
	}
	if !page.RequirePageWrite(w, r, pageID) {
		return
	}
	// テンプレートの中へは書かない（2026-10-20 にほかの口とそろえた——コアの RefuseTemplateArea）。
	if !cms.RefuseTemplateArea(w, pageID) || !editlock.RefuseWhileEditing(w, pageID) {
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

	cms.WriteJSON(w, map[string]any{
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
// 字下げのある形（エディタで一度保存したページ——`</dt>` と `<dd>` のあいだに改行と空白）も探します（tagPairRe・2026-10-22 から。
// それまでは詰めた形しか探さず、「そのアドレスはこのページにありません」と断っていた）。
func removeEmailTag(body, addr string, removed *int) string {
	// 字下げのある形も拾う（エディタで保存した本文で「そのアドレスはこのページにありません」になっていた・2026-10-22 の見直し）。
	re := tagPairRe(EmailTag)
	out := re.ReplaceAllStringFunc(body, func(m string) string {
		sub := re.FindStringSubmatch(m)
		if sub != nil && normalizeEmail(tagText(sub[1])) == addr {
			*removed++
			return ""
		}
		return m
	})
	// `<dl data-type="tags"></dl>` が残ったら外す。
	return regexp.MustCompile(`<dl data-type="tags">\s*</dl>`).ReplaceAllString(out, "")
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

// tagPairRe は、本文の名前 name のタグの組（dt と dd）です——字下げ・属性があっても拾う（エディタで保存した本文は字下げされる）。
// 中の文字は1つ目の組（タグを足した印・<br> などを含むことがある）。
func tagPairRe(name string) *regexp.Regexp {
	return regexp.MustCompile(`(?s)<dt[^>]*>\s*` + regexp.QuoteMeta(stdhtml.EscapeString(name)) + `\s*</dt>\s*<dd[^>]*>(.*?)</dd>`)
}

// tagText は dd の中身を文字にします（タグを落として、実体参照を戻す）。
func tagText(inner string) string {
	return strings.TrimSpace(stdhtml.UnescapeString(regexp.MustCompile(`<[^>]*>`).ReplaceAllString(inner, "")))
}
