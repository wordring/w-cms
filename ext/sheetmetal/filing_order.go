package sheetmetal

// ─────────────────────────────────────────────────────────────────────────
// 受注ページの整理——通信箱から「受注」の年月へ移す（2026-09-06）
//
// ユーザー:「整理の前に、部品ページや受注ページの置き場所を決めましょう」。
// 決まったのは **トップ直下の「受注」／年／月**（部品ページのように顧客の下へは
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

	"w-cms/internal/auth"
	"w-cms/internal/cms"
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
// 部品ページと違って**直す欄がありません**——行き先が発注日だけで決まるためで、
// 画面には「どこへ入るか」を見せて、押すかどうかだけを人に委ねます。
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
		blocks, err := cms.VocabBlocksOf(database.DB, c.id, "client-order")
		if err != nil || len(blocks) == 0 {
			continue // 受注ページではない（部品ページなど）
		}
		v := blocks[0].Values
		when := orderDateOf(c.id, v["ordered-at"])
		out = append(out, orderRow{
			PageID:      formatID(c.id),
			Title:       c.title,
			OrderNo:     v["order-no"],
			ClientName:  v["client-name"],
			OrderedAt:   strings.TrimSpace(v["ordered-at"]),
			Destination: OrderBoxTitle + "／" + when.Format("2006年") + "／" + when.Format("01月"),
		})
	}
	return out, nil
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

// fileOneOrder は受注ページ1枚を「受注／年／月」へ収めます。
//
// **題は変えません**——部品ページは図面名称に揃えますが（人が打ち替えた値が正）、
// 受注の題は解析が発注書番号から作ったもので、直す欄がありません。
func fileOneOrder(user *auth.User, rawID string) filingResult {
	pageID, ok := page.NormalizeID(rawID)
	if !ok {
		return filingResult{PageID: rawID, Outcome: "skipped", Message: "ページIDが不正です"}
	}
	idInt, err := strconv.Atoi(pageID)
	if err != nil || !canWritePage(user, idInt) {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "このページを動かす権限がありません"}
	}
	blocks, err := cms.VocabBlocksOf(database.DB, idInt, "client-order")
	if err != nil || len(blocks) == 0 {
		// **受注ページ以外を動かさない。** 画面から送られた ID をそのまま信じると、
		// 図面ページや通信記録まで受注の箱へ入れられます。
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "受注ページではありません"}
	}

	boxID, err := cms.EnsureTopLevelBox(OrderBoxTitle, user.Username)
	if err != nil {
		return filingResult{PageID: pageID, Outcome: "skipped",
			Message: "「" + OrderBoxTitle + "」ページを用意できません: " + err.Error()}
	}
	when := orderDateOf(idInt, blocks[0].Values["ordered-at"])
	monthID, err := cms.EnsureDateFolders(boxID, user.Username, when)
	if err != nil {
		return filingResult{PageID: pageID, Outcome: "skipped",
			Message: "年月のフォルダを用意できません: " + err.Error()}
	}
	if _, _, err := cms.SetPageParent(user, pageID, monthID); err != nil {
		return filingResult{PageID: pageID, Outcome: "skipped", Message: "移動できません: " + err.Error()}
	}
	auth.Audit(user.Username, "file-order.move", pageID+" -> "+monthID)
	return filingResult{PageID: pageID, Outcome: "moved",
		Message: OrderBoxTitle + "／" + when.Format("2006年") + "／" + when.Format("01月") + " へ収めました"}
}
