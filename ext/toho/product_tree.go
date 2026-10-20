package toho

// ─────────────────────────────────────────────────────────────────────────
// 加工製品の木——`製造台帳／社名／（装置名称）／品目`（2026-10-16）
//
// 利用者:「製造台帳の下に社名フォルダ、その下にオプションで装置名フォルダ、その下に加工製品ページとしたいです
// （装置名が分からないメーカーもあるので）」「トップ直下を製造台帳…としてはどうでしょう？」（【考察】全体の設計 §4.4）。
//
//   - 根は「製造台帳」（2026-10-16 の夕方までは「取引先」、夜までは「製造」）。
//   - **社名の下の「加工製品」の箱はなくしました。** 装置名称のフォルダも加工製品ページも、社名の直下に並びます。
//     装置名称は無くてもかまいません（そのときは社名の直下）。
//   - 社名・装置名称のフォルダは `ページ種類`（社名・装置）を持って作ります。⚠ **装置名称のフォルダを探すときは、
//     加工製品ページを外します**（社名の直下に、同じ題の加工製品ページが並びうるため——findFolderChild）。
//   - 一覧は、スラッシュメニューから「加工製品の一覧」を挿した所に出ます（どこに挿しても全社の分・product_list.go）。
//     利用者:「加工製品の一覧は、スラッシュメニューから一覧のタグを書き込んだ場所に出ると良いと思います」。
//
// 以下は、それまでの形（`製造／社名／加工製品／装置名称／品目`・2026-09-29）の記録です。
//
// 利用者:「試作フォルダについてですが、**フォルダで分けるのをやめて、試作のタグを
// つけるように**変更したいと思います」「〈取引先〉／加工製品／装置名称／品目にしたい
// です。というのも、**ワンノートでは出来なかった検索などが出来るから**です」
// 「見積についても、図面があるなら加工製品に入れて、見積もりのタグを付けたいです。
// 加工製品のトップページにでも、**フィルターして表示する項目**があれば良いと思います」。
//
// それまでは装置名称の上に**段**（現行・旧型・試作——設定の `machine_stages`）の
// フォルダを挟んでいました（2026-09-05）。段をやめて:
//
//   - **社名の下に「加工製品」の箱を1枚**置きます。社名のページには、ほかに
//     担当者の箱なども並ぶので、加工製品をまとめる箱が要ります。箱は**テンプレート
//     「製造の加工製品」から作ります**（2026-10-16 までの題は「取引先の加工製品」）（テンプレート駆動・「加工製品の一覧」の鏡を
//     テンプレートが持つ）。
//   - 試作・見積・旧型は**加工製品ページのタグ `区分`** です。⚠ **1枚に2つ付くことが
//     あります**（利用者:「試作かつ見積もりという場合がある」）——フォルダでは1つしか
//     選べませんでした。**何も付いていなければ通常の製品**です。選択肢は設定の語彙
//     `区分` が持ちます（コードに書き写さない）。
//
// ⚠ **`区分` という語は、構成部品の表の列（現行／廃版）にもあります。** タグの型は
// 名前で決まるので、語彙の既定（`vocabulary`）を加工製品の区分にして、4つの表
// （材料・外注加工・購入部品・支給部品）の列は表ごとの例外（`table_vocabulary`）で
// 現行／廃版に保ちます。
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"
	"fmt"
	stdhtml "html"
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

const (
	// ProductKindTag は加工製品の区分（試作・見積・旧型…）のタグ名です。
	ProductKindTag = "区分"
)

// ProductKinds は加工製品の区分の選択肢を、設定の語彙 `区分` の並びで返します。
//
// ⚠ **表の名前を渡さずに引きます**——同じ語の表ごとの例外（構成部品の表の現行／廃版）を
// 拾わないためです。
func ProductKinds() []string { return cms.ColumnWord("", ProductKindTag).Values }

// cleanProductKinds は人が選んだ区分を、選択肢の並びに揃えて重複と空を落とします。
// 選択肢に無い値があれば、それを bad に返します（黙って捨てない——打ち間違いか、
// 設定の語彙が古いかのどちらかなので）。
func cleanProductKinds(in []string) (out []string, bad string) {
	want := map[string]bool{}
	for _, k := range in {
		if k = strings.TrimSpace(k); k != "" {
			want[k] = true
		}
	}
	for _, k := range ProductKinds() {
		if want[k] {
			out = append(out, k)
			delete(want, k)
		}
	}
	for k := range want {
		return nil, k
	}
	return out, ""
}

// isProductPage は、ページが加工製品ページ（旧版を含む）を名乗っているかを返します（`ページ種類` で）。
func isProductPage(id int) bool {
	tags, err := cms.TagsOfPage(database.DB, id)
	if err != nil {
		return false
	}
	k := strings.TrimSpace(cms.FirstTag(tags, PageKindTag))
	return k == PageKindProduct || k == PageKindProductOld
}

// isMachineFolder は、ページが製造台帳の装置名称のフォルダかを返します。
//
// `ページ種類：装置` なら装置。種類の無いページは、親が社名（`ページ種類：社名`、または製造台帳の直下）で、
// 加工製品ページでもないものを装置とみなします（人が作ったフォルダ）。⚠ フォルダの種類には頼りすぎない（利用者）。
func isMachineFolder(id string) bool {
	idInt, err := strconv.Atoi(id)
	if err != nil {
		return false
	}
	tags, err := cms.TagsOfPage(database.DB, idInt)
	if err != nil {
		return false
	}
	switch strings.TrimSpace(cms.FirstTag(tags, PageKindTag)) {
	case PageKindMachine:
		return true
	case "":
	default:
		return false
	}
	meta, ok := page.ReadSidecar(id)
	if !ok || meta.ParentID == "" {
		return false
	}
	return isCompanyFolder(meta.ParentID)
}

// isCompanyFolder は、ページが製造台帳の社名のフォルダか（`ページ種類：社名`、または製造台帳の直下）を返します。
func isCompanyFolder(id string) bool {
	idInt, err := strconv.Atoi(id)
	if err != nil {
		return false
	}
	if tags, err := cms.TagsOfPage(database.DB, idInt); err == nil {
		switch strings.TrimSpace(cms.FirstTag(tags, PageKindTag)) {
		case PageKindCompany:
			return true
		case "":
		default:
			return false
		}
	}
	boxID, ok := CustomerBoxPageID()
	meta, found := page.ReadSidecar(id)
	return ok && found && meta.ParentID == boxID
}

// findFolderChild は親の子から、題が完全一致する**フォルダ**（加工製品ページではないもの）を1つ探します。
//
// 社名の直下には、装置名称のフォルダと加工製品ページが並びます（2026-10-16）。同じ題の加工製品ページを
// 装置名称のフォルダと取り違えないよう、`ページ種類` が加工製品のページを外します。
func findFolderChild(parentID, title string) (string, bool) {
	parentInt, err := strconv.Atoi(parentID)
	if err != nil {
		return "", false
	}
	kids, err := cms.ChildPages(database.DB, parentInt)
	if err != nil {
		return "", false
	}
	for _, k := range kids {
		if k.Title == title && !isProductPage(k.ID) {
			return page.FormatID(k.ID), true
		}
	}
	return "", false
}

// ensureKindFolder は親の下に題 title のフォルダ（`ページ種類：kind`）を探し、無ければ作ります。
func ensureKindFolder(user *auth.User, parentID, title, kind string) (string, error) {
	if id, ok := findFolderChild(parentID, title); ok {
		return id, nil
	}
	parentInt, err := strconv.Atoi(parentID)
	if err != nil {
		return "", err
	}
	if !canWritePage(user, parentInt) {
		return "", errors.New("親ページへ書き込む権限がありません")
	}
	// ⚠ **`cms.EnsureFolder` は使いません**——あれは題だけで探し直すので、社名の直下に同じ題の加工製品ページが
	// あると、それを「もうある」と返してしまいます（2026-10-16 に試験で見つけた）。探すのは上の findFolderChild だけ。
	newID, err := cms.CreateChildPage(parentID, user.Username, kindFolderBody(title, kind))
	if err != nil {
		return "", err
	}
	auth.Audit(user.Username, "product.folder", newID+" under "+parentID+" ("+title+")")
	return newID, nil
}

// ensureProductPath は `製造台帳／社名／装置名称` を辿り、無ければ作って、
// 装置名称のページを返します（machine が空なら社名のページ——装置名の分からないメーカーもある）。
//
// **木の形を知っているのはここだけ**です——整理（filing.go）も、ワンノートの移植の道具
// （`/api/product-folder` 経由）もこれを通ります。形を2か所に持つと、片方だけ古い形で
// ページを作ります（段をやめた日に、道具のほうが段を作り続ける）。
//
// 社名のページは連絡帳の組織と参照タグで結びます（`linkPartner`・結べなくても続ける）。
func ensureProductPath(user *auth.User, customer, machine string) (string, error) {
	boxID, err := EnsureCustomerBox(user)
	if err != nil {
		return "", fmt.Errorf("「%s」ページを用意できません: %w", CustomerBoxTitle, err)
	}
	customerID, err := ensureKindFolder(user, boxID, customer, PageKindCompany)
	if err != nil {
		return "", fmt.Errorf("顧客名ページを用意できません: %w", err)
	}
	linkPartner(user, customerID, customer)
	if machine == "" {
		return customerID, nil
	}
	machineID, err := ensureKindFolder(user, customerID, machine, PageKindMachine)
	if err != nil {
		return "", fmt.Errorf("装置名称ページを用意できません: %w", err)
	}
	return machineID, nil
}

// machineFoldersOf は、社名 customer の直下にある装置名のフォルダの題を返します（読めるものだけ・作らない）。
func machineFoldersOf(user *auth.User, customer string) []string {
	boxID, ok := CustomerBoxPageID()
	if !ok {
		return nil
	}
	customerID, ok := findFolderChild(boxID, customer)
	if !ok {
		return nil
	}
	kids, err := cms.ChildPages(database.DB, pageNum(customerID))
	if err != nil {
		return nil
	}
	var out []string
	for _, k := range kids {
		if page.CanView(user, k.ID) && isMachineFolder(page.FormatID(k.ID)) {
			out = append(out, k.Title)
		}
	}
	return out
}

// firstN は並びの頭 n 個を返します。
func firstN(vals []string, n int) []string {
	if len(vals) > n {
		return vals[:n]
	}
	return vals
}

// findMachineFolder は `製造台帳／社名／装置名称` の装置のページを、作らずに探します。
func findMachineFolder(customer, machine string) (string, bool) {
	boxID, ok := CustomerBoxPageID()
	if !ok {
		return "", false
	}
	customerID, ok := findFolderChild(boxID, customer)
	if !ok {
		return "", false
	}
	return findFolderChild(customerID, machine)
}

// setProductKinds は加工製品ページの `区分` のタグを kinds に揃えます。
//
// replace が偽なら**足すだけ**です（既にある区分は消さない）。整理で既にあるページへ
// 合流するときがこれで、画面の欄の初期値は**運んでくる側**のページの区分なので、
// 合流先の区分を消してよい根拠がありません。
//
// ⚠ **編集中のページには書きません**（オートセーブと上書きし合う・競合対策 §9）。
// （2026-10-12 から、断るのは道具〔`/api/lock`〕が握っているときだけ。人の保存とは `cms.RewriteBody` の錠で順に並ぶ。）
func setProductKinds(user *auth.User, pageID string, kinds []string, replace bool) error {
	if _, err := strconv.Atoi(pageID); err != nil {
		return err
	}
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		return err
	}
	if next := withTagValues(body, ProductKindTag, kinds, replace); next == body {
		return nil // 変わらない——版を増やさない
	}
	return cms.RewriteBodyUnlessHeld(pageID, user.Username, func(current string) string {
		return withTagValues(current, ProductKindTag, kinds, replace)
	})
}

// withTagValues は本文の可変タグ `name` に values を足します（replace なら values に無い値を消す）。
//
// **文字列で見ます**（`addContactTags`・`MarkHandled` と同じ流儀）——ページ全体を
// 組み直すと、関係の無い字下げまで書き換わって版の差分が読めなくなります。
//   - 同じ組（`<dt>区分</dt><dd>試作</dd>`）が在れば足さない（間の空白は問わない）。
//   - テンプレートが置いた**空の欄**（`<dd><br/></dd>`）があれば、そこへ入れる。
//   - 無ければ、ページの可変タグ（h1 の下の並び）の最後へ。並びが無ければ h1 の下に作る。
func withTagValues(body, name string, values []string, replace bool) string {
	dt := `<dt>` + regexp.QuoteMeta(stdhtml.EscapeString(name)) + `</dt>\s*`
	if replace {
		keep := map[string]bool{}
		for _, v := range values {
			keep[v] = true
		}
		set := regexp.MustCompile(dt + `<dd>([^<]*)</dd>`)
		body = set.ReplaceAllStringFunc(body, func(m string) string {
			v := stdhtml.UnescapeString(strings.TrimSpace(set.FindStringSubmatch(m)[1]))
			if v == "" || keep[v] {
				return m
			}
			return ""
		})
	}
	empty := regexp.MustCompile(dt + `<dd>(?:\s|<br\s*/?>)*</dd>`)
	for _, v := range values {
		pair := `<dt>` + stdhtml.EscapeString(name) + `</dt><dd>` + stdhtml.EscapeString(v) + `</dd>`
		if regexp.MustCompile(dt + `<dd>\s*` + regexp.QuoteMeta(stdhtml.EscapeString(v)) + `\s*</dd>`).MatchString(body) {
			continue
		}
		if loc := empty.FindStringIndex(body); loc != nil {
			body = body[:loc[0]] + pair + body[loc[1]:]
			continue
		}
		body = cms.AddPageTags(body, pair) // 図面ブロックの中の並びへ、ページの区分を混ぜない（cms.PageTagListEnd）
	}
	return body
}

// ProductFolderAPIHandler は POST /api/product-folder です（2026-09-29）。
// 入力: {customer, machine}。`製造台帳／社名／装置名称` を用意して、そのページIDを返します
// （machine が空なら社名のページ）。
//
// **ワンノートの移植の道具のための口**です（tools/onenote/build）。道具は外から HTTP で
// ページを作るので、それまで木の道を自分で辿っていました——**段をやめた日に、道具だけが
// 段を作り続ける**形です。木の形を知るのは `ensureProductPath` の1つにします。
func ProductFolderAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		Customer string `json:"customer"`
		Machine  string `json:"machine"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	// 題になる値は整理と同じく早期に正規化します（題の完全一致が木の同一性）。
	customer := cms.NormalizeNameForIngest(req.Customer)
	machine := cms.NormalizeNameForIngest(req.Machine)
	if customer == "" {
		cms.JSONFail(w, http.StatusBadRequest, "社名が空です")
		return
	}
	id, err := ensureProductPath(user, customer, machine)
	if err != nil {
		status := http.StatusInternalServerError
		if errors.Is(err, cms.ErrNoPageTemplate) || errors.Is(err, cms.ErrNoBoxTemplate) {
			status = http.StatusConflict // テンプレートを置けば通る（ほかの「テンプレートが無い」と同じ 409。置き場「製造台帳」のテンプレートも）
		}
		cms.JSONFail(w, status, err.Error())
		return
	}
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": id})
}
