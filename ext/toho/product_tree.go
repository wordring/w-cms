package toho

// ─────────────────────────────────────────────────────────────────────────
// 加工製品の木——`取引先／社名／加工製品／装置名称／品目`（2026-09-29）
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
//     「取引先の加工製品」から作ります**（テンプレート駆動・「加工製品の一覧」の鏡を
//     テンプレートが持つ）。
//   - 試作・見積もり・旧型は**加工製品ページのタグ `区分`** です。⚠ **1枚に2つ付くことが
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
	"w-cms/internal/cms/editlock"
)

const (
	// ProductsBoxTitle は社名ページの下で加工製品をまとめる箱の題です。
	ProductsBoxTitle = "加工製品"
	// ProductsBoxTemplate はその箱を作るときに写すテンプレートの題です（「加工製品」は
	// 加工製品ページのテンプレート〔ProductTemplate〕の題なので、別の名前にしています）。
	ProductsBoxTemplate = "取引先の加工製品"
	// ProductKindTag は加工製品の区分（試作・見積もり・旧型…）のタグ名です。
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

// ensureProductsBox は社名ページの下の「加工製品」を返し、無ければテンプレートから作ります。
//
// ⚠ **テンプレートが無ければ作りません**（テンプレート駆動・2026-09-27 利用者:「テンプレートが
// 無ければ作れないまで行きます」）——箱の作業面（加工製品の一覧）をコードに焼かないためです。
func ensureProductsBox(user *auth.User, customerID string) (string, error) {
	if id, ok := findChildByTitle(customerID, ProductsBoxTitle); ok {
		return id, nil
	}
	parentInt, err := strconv.Atoi(customerID)
	if err != nil {
		return "", err
	}
	if !canWritePage(user, parentInt) {
		return "", errors.New("社名のページへ書き込む権限がありません")
	}
	d, err := cms.DraftFromTemplate(ProductsBoxTemplate)
	if err != nil {
		return "", err
	}
	d.SetTitle(ProductsBoxTitle)
	return cms.CreateChildPage(customerID, user.Username, d.HTML())
}

// ensureProductPath は `取引先／社名／加工製品／装置名称` を辿り、無ければ作って、
// 装置名称のページを返します（machine が空なら「加工製品」の箱）。
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
	customerID, err := ensureChildPage(user, boxID, customer)
	if err != nil {
		return "", fmt.Errorf("顧客名ページを用意できません: %w", err)
	}
	linkPartner(user, customerID, customer)
	productsID, err := ensureProductsBox(user, customerID)
	if err != nil {
		return "", fmt.Errorf("「%s」ページを用意できません: %w", ProductsBoxTitle, err)
	}
	if machine == "" {
		return productsID, nil
	}
	machineID, err := ensureChildPage(user, productsID, machine)
	if err != nil {
		return "", fmt.Errorf("装置名称ページを用意できません: %w", err)
	}
	return machineID, nil
}

// setProductKinds は加工製品ページの `区分` のタグを kinds に揃えます。
//
// replace が偽なら**足すだけ**です（既にある区分は消さない）。整理で既にあるページへ
// 合流するときがこれで、画面の欄の初期値は**運んでくる側**のページの区分なので、
// 合流先の区分を消してよい根拠がありません。
//
// ⚠ **編集中のページには書きません**（オートセーブと上書きし合う・競合対策 §9）。
func setProductKinds(user *auth.User, pageID string, kinds []string, replace bool) error {
	idInt, err := strconv.Atoi(pageID)
	if err != nil {
		return err
	}
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		return err
	}
	if next := withTagValues(body, ProductKindTag, kinds, replace); next == body {
		return nil // 変わらない——版を増やさない
	}
	if holder, open := editlock.Locks.EditorOpen(idInt); open {
		return errors.New("このページは編集中です（" + holder + "）")
	}
	return cms.RewriteBody(pageID, user.Username, func(current string) string {
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
		if at := pageTagListEnd(body); at >= 0 {
			body = body[:at] + pair + body[at:]
			continue
		}
		body = cms.InsertAfterH1(body, `<dl data-type="tags">`+pair+`</dl>`)
	}
	return body
}

// pageTagListEnd は**ページの**可変タグの並び（最初の節より前のもの）の `</dl>` の位置を返します。
// 無ければ -1——図面ブロックの中の並びへ、ページの区分を混ぜないためです。
func pageTagListEnd(body string) int {
	at := cms.EndOfFirstTagList(body)
	if at < 0 {
		return -1
	}
	if sec := strings.Index(body, "<section"); sec >= 0 && sec < at {
		return -1
	}
	return at
}

// ProductFolderAPIHandler は POST /api/product-folder です（2026-09-29）。
// 入力: {customer, machine}。`取引先／社名／加工製品／装置名称` を用意して、そのページIDを返します
// （machine が空なら「加工製品」の箱）。
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
		if errors.Is(err, cms.ErrNoPageTemplate) {
			status = http.StatusConflict // テンプレートを置けば通る（ほかの「テンプレートが無い」と同じ 409）
		}
		cms.JSONFail(w, status, err.Error())
		return
	}
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": id})
}
