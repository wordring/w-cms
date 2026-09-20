package subcon

// ─────────────────────────────────────────────────────────────────────────
// 受注ページの整理——通信箱から「受注」の年月へ移す（2026-09-06）
//
// ユーザー:「整理の前に、部品ページや受注ページの置き場所を決めましょう」。
// 決まったのは **トップ直下の「受注」／年／月**（加工製品ページのように顧客の下へは
// 置かない）。受注は複数の装置にまたがるので、装置の木には収まりません。
//
// **置き場所を決める理由は、探しやすさだけではありません。** 未処理の一覧は
// 通信箱の**子孫を全部**拾うので（`isDescendantOf` は深さ無制限）、解析で生まれた
// 受注ページはそのまま作業待ちに並びます——メール1通が2行にも3行にもなる。
// 家を与えると、移した時点で一覧から自然に外れます。
//
// **移すのは人の操作**（同日ユーザー決定）。行き先は発注日だけで決まるので直す欄は
// ありませんが、**いつ動かすかは人が決めます**——解析の直後に機械が動かすと、
// 判定を間違えた受注ページが黙って通信箱の外へ出てしまい、気づく機会が無くなります。
//
// 年月は**発注日**で決めます（受け取った日ではなく）。読めなければページの更新時刻。
// 発注書に日付が無いことは実際にあり、そのとき取り込みを止める理由はありません。
// ─────────────────────────────────────────────────────────────────────────

import (
	"strconv"
	"strings"
	"time"

	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// OrderBoxTitle は受注ページの置き場（トップ直下）の名前です。
//
// **業務の言葉なので拡張が持ちます**——コアが持つのは「トップ直下に題で箱を
// 用意する」仕組みだけ（`cms.EnsureTopLevelBox`）。通信箱・テンプレート置き場と
// 同じく **h1（ページ名）が正**です。
const OrderBoxTitle = "受注"

// orderRow は整理の画面に出す受注ページ1枚ぶんです。
//
// 行き先（`Destination`）は**発注日だけで決まる**ので直せません。⚠ **`ClientName`
// だけは直せます**（2026-09-20・3箇所を同じマナーに揃えた最後の1つ）。
type orderRow struct {
	PageID      string `json:"page_id"`
	Title       string `json:"title"`
	OrderNo     string `json:"order_no"`
	ClientName  string `json:"client_name"`
	OrderedAt   string `json:"ordered_at"`  // 発注日（読めたときだけ）
	Destination string `json:"destination"` // 「受注／2024年／09月」
}

// orderChildrenOf は、そのページの子のうち**発注書ブロックを持つもの**を集めます。
func orderChildrenOf(user *auth.User, parentIDInt int) ([]orderRow, error) {
	dbRows, err := database.DB.Query(
		`SELECT id, title FROM pages WHERE parent_id = ? ORDER BY id ASC`, parentIDInt)
	if err != nil {
		return nil, err
	}
	type child struct {
		id    int
		title string
	}
	// **先に読み切ってから解釈します**——行を読みながら別のクエリを投げると
	// カーソルが接続を握ったままになる（view_unhandled.go で踏んだ罠）。
	var children []child
	for dbRows.Next() {
		var c child
		if err := dbRows.Scan(&c.id, &c.title); err != nil {
			dbRows.Close()
			return nil, err
		}
		children = append(children, c)
	}
	err = dbRows.Err()
	dbRows.Close()
	if err != nil {
		return nil, err
	}

	out := []orderRow{}
	for _, c := range children {
		if !page.CanView(user, c.id) {
			continue // 見せ分け（C案）——読めないものは黙って落ちる
		}
		// ⚠ **可変タグから読みます**（2026-09-18 にヘッダから移した）。
		tags, err := cms.TagsOfPage(database.DB, c.id)
		if err != nil || cms.FirstTag(tags, OrderNoTag) == "" {
			continue // 受注ページではない（加工製品ページなど）
		}
		orderedAt := cms.FirstTag(tags, OrderedAtTag)
		when := orderDateOf(c.id, orderedAt)
		out = append(out, orderRow{
			PageID:      formatID(c.id),
			Title:       c.title,
			OrderNo:     cms.FirstTag(tags, OrderNoTag),
			ClientName:  suggestOrderClient(user, cms.FirstTag(tags, OrderClientTag)),
			OrderedAt:   strings.TrimSpace(orderedAt),
			Destination: OrderBoxTitle + "／" + when.Format("2006年") + "／" + when.Format("01月"),
		})
	}
	return out, nil
}

// suggestOrderClient は発注元の欄の初期値を決めます——**連絡帳に法人格違いの同じ
// 組織が居れば、その実物の題**（2026-09-20 ユーザー:「受注、加工製品、連絡帳を同じ
// マナーで提案の欄を作ってはどうでしょう？」）。
//
// ⚠ **加工製品の `suggestCustomer` とは効き目が違います。** あちらは `linkPartner` が
// **完全一致**で2つの木を結ぶので、揃えないと**黙って結ばれません**。こちらは
// `発注元` を引く読み手がまだ1人も居ないので（`fileOneOrder` が見るのは
// `発注書番号` と `発注日` だけ）、**いま直しても何も起きません**。
//
// それでも入口で揃えるのは、**効くのが横断検索が入ったとき**だからです
// （w-cms を作り始めた動機の1つ・[要件定義書.md] §4.4）。そのとき揃っていない
// データは、遡って直せません。
//
// ⚠ **アドレスの鎖は使いません。** 加工製品は `受信元` から通信記録の差出人まで
// たどれますが（推測ゼロの完全一致）、受注ページにその鎖があるかは未確認です。
// **確かめずに鎖を足すと、間違った相手を推して人がそのまま押します**——名前だけで
// 当たる今の形なら、外れても読んだ名前のまま残ります。
func suggestOrderClient(user *auth.User, read string) string {
	if strings.TrimSpace(read) == "" {
		return "" // **空は埋めません**——読めなかったことを人に見せる
	}
	return contacts.SuggestOrgTitle(user, read)
}

// orderDateOf は受注ページの年月を決める日付を返します。
//
// **発注日が第一**——受け取った日で並べると、月末に届いた前月ぶんが翌月に混ざります。
// 読めなければページの更新時刻、それも読めなければ現在時刻（止めない）。
func orderDateOf(pageIDInt int, ordered string) time.Time {
	if t, ok := parseOrderDate(ordered); ok {
		return t
	}
	var updated string
	database.DB.QueryRow(`SELECT COALESCE(updated_at, '') FROM pages WHERE id = ?`,
		pageIDInt).Scan(&updated)
	if t, err := time.Parse(time.RFC3339, strings.TrimSpace(updated)); err == nil {
		return t.In(time.Local)
	}
	return time.Now()
}

// parseOrderDate は発注日の文字列を読みます。
//
// 索引の値は取り込みのときに正規化されている（D-3）ので `2024-09-13` の形が本命ですが、
// **読めなかった値は生のまま入る**（取り込みは情報を捨てない）ので、素直な形も見ます。
func parseOrderDate(v string) (time.Time, bool) {
	s := strings.TrimSpace(v)
	if s == "" {
		return time.Time{}, false
	}
	for _, layout := range []string{"2006-01-02", "2006/01/02", "2006年01月02日"} {
		if t, err := time.Parse(layout, s); err == nil {
			return t, true
		}
	}
	return time.Time{}, false
}

// orderRequest は「実行」で送られてくる受注ページ1行です。
//
// ⚠ **もとはIDの文字列だけでした**（2026-09-20 に変えた）。直す欄が無かったので
// 「押した」という事実だけを送っていましたが、`発注元` が直せるようになったので
// 値を運びます。画面（`assets/app.js` の `runFiling`）も同じ形で送ります。
type orderRequest struct {
	PageID string `json:"page_id"`
	// Client は人が確認した発注元です。**空なら本文に触りません**——欄を空にした
	// ことを「消したい」とは読みません（`replaceFirstFieldValue` が値を消してしまう）。
	Client string `json:"client"`
}

// fileOneOrder は受注ページ1枚を「受注／年／月」へ収めます。
//
// **題は変えません**——加工製品ページは図面名称に揃えますが（人が打ち替えた値が正）、
// 受注の題は解析が発注書番号から作ったものです。⚠ **`発注元` だけは書き戻します**
// （2026-09-20）。
func fileOneOrder(user *auth.User, row orderRequest) filingResult {
	pageID, ok := page.NormalizeID(row.PageID)
	if !ok {
		return filingResult{PageID: row.PageID, Outcome: "skipped", Message: "ページIDが不正です"}
	}
	idInt, err := strconv.Atoi(pageID)
	if err != nil || !canWritePage(user, idInt) {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "このページを動かす権限がありません"}
	}
	// **受注ページ以外を動かさない。** 画面から送られた ID をそのまま信じると、
	// 加工製品ページや通信記録まで受注の箱へ入れられます。
	// ⚠ 見るのは `発注書番号` のタグです（2026-09-18 にヘッダから移した）。
	tags, err := cms.TagsOfPage(database.DB, idInt)
	if err != nil || cms.FirstTag(tags, OrderNoTag) == "" {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "受注ページではありません"}
	}
	// **開いている人が居たら、その行は飛ばします**——下で本文を読んで・変えて・書くので、
	// エディタが開いているとオートセーブと上書きし合います（`fileOneDrawing` と同じ作法）。
	if holder, open := editlock.Locks.EditorOpen(idInt); open {
		return filingResult{PageID: pageID, Outcome: "skipped",
			Message: "このページは編集中です（" + holder + "）。閉じてからもう一度お試しください"}
	}

	boxID, err := cms.EnsureTopLevelBox(OrderBoxTitle, user.Username)
	if err != nil {
		return filingResult{PageID: pageID, Outcome: "skipped",
			Message: "「" + OrderBoxTitle + "」ページを用意できません: " + err.Error()}
	}
	when := orderDateOf(idInt, cms.FirstTag(tags, OrderedAtTag))
	monthID, err := cms.EnsureDateFolders(boxID, user.Username, when)
	if err != nil {
		return filingResult{PageID: pageID, Outcome: "skipped",
			Message: "年月のフォルダを用意できません: " + err.Error()}
	}
	if _, _, err := cms.SetPageParent(user, pageID, monthID); err != nil {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "移動できません: " + err.Error()}
	}
	// **人が確認した発注元を本文へ書き戻します**（2026-09-20）。⚠ **移してから**書きます
	// ——移動は行き先だけで決まるので、書き戻しに失敗しても収まったことは変わりません。
	// 逆にすると、書けなかったときに移すかどうかで迷います。
	if err := syncOrderClient(user, pageID, row.Client); err != nil {
		// **収まったことは事実なので、失敗にしません**（`BumpUpdatedAt` と同じ判断）。
		auth.Audit(user.Username, "file-order.client-failed", pageID+": "+err.Error())
	}
	auth.Audit(user.Username, "file-order.move", pageID+" -> "+monthID)
	return filingResult{PageID: pageID, Outcome: "moved",
		Message: OrderBoxTitle + "／" + when.Format("2006年") + "／" + when.Format("01月") + " へ収めました"}
}

// syncOrderClient は人が確認した発注元を、受注ページの可変タグへ書き戻します。
//
// **索引のためです**——`page_tags` は本文から作られるので、画面で直しても本文が
// 元のままなら、揃えた意味がありません（`syncDrawingFields` と同じ理由）。
//
// ⚠ **足しません。差し替えるだけです。** `発注元` の行は解析が必ず書くので
// （`buildOrderPageHTML`）、無いときは**この形のページではない**——そこへ勝手に
// 行を挿すと、人が消した行を機械が戻すことになります。図面ブロックが足すのは、
// 解析が読めなかった項目を書かない作りだからで、事情が違います。
//
// **値に印が付いていたら触りません**（`replaceFirstFieldValue` が `[^<]*` で見る
// ——人がリンクや強調を書いたら、機械は踏み潰さない）。
func syncOrderClient(user *auth.User, pageID, client string) error {
	client = cms.NormalizeNameForIngest(client)
	if client == "" {
		return nil // **空欄は「消したい」ではありません**（読めなかった・触らなかった）
	}
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		return err
	}
	fixed := replaceFirstFieldValue(body, OrderClientTag, client)
	if fixed == body {
		return nil // **変わらないなら書きません**——版と更新日時を無駄に進めない
	}
	return cms.RewriteBody(pageID, user.Username, func(string) string { return fixed })
}
