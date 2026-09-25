package subcon

// ─────────────────────────────────────────────────────────────────────────
// 受注ページに「加工製品ごとの購入品と、どの発注書で手配したか」を集める（2026-09-21）
//
// ユーザー:「**受注明細の一行は加工製品一種類**で、加工製品一種類に必要な材料など
// 購入品は複数あります。ですから、各受注ページに**各加工製品ごとの項目と購入品の表を
// 集める**必要があり、その表の列の一つとして、**発注書番号と発注書ページへのリンク**が
// 必要になると思います」。
//
// ⚠ **導出（鏡）です。人が直すのは発注書のほう**（ユーザー:「導出ですが、人間が
// 修正する場合もあります」→「保存先は、先ほど決めた**発注ページ**で良いのでは？」）。
//
// ⚠ **この判断で「上書きの保存先」問題が消えました。** 筆者は受注ページ側に書ける表を
// 置こうとしていましたが、**発注の事実の正本は発注書**です——「この行は実はあの加工製品
// のぶんだった」を直すのは、**発注書の `弊社品番` を書き直すこと**。受注ページ側は
// **純粋な鏡**のままでよく、書き込む先が要りません。
//
// ⚠ **スコープ（`RelatedPages`）に頼りません。** 発注書は `発注／年／月` に置かれ、
// 受注ページからは `受注 →（弊社品番）→ 加工製品 →（弊社品番）→ 発注書` の**2段**です
// ——深さ1の集計範囲では**黙って0件**になります。だから
// `cms.VocabRowsOfType` で**全社の発注明細**を読み、`弊社品番` で束ねます
// （材料の参考単価と同じやり方）。**置き場が変わっても壊れません。**
// ─────────────────────────────────────────────────────────────────────────

import (
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// ProcurementOrder は「その購入品を手配した発注書」1枚です。
type ProcurementOrder struct {
	PageID int    `json:"page_id"`
	Title  string `json:"title"`
	Qty    int    `json:"qty"`
}

// ProcurementItem は加工製品1種類に要る購入品1行です。
type ProcurementItem struct {
	Name      string             `json:"name"` // 材質 形状 寸法 ／ 品名
	Kind      string             `json:"kind"` // 材料 / 購入部品
	Per       int                `json:"per"`  // 一台あたり
	Required  int                `json:"required"`
	Ordered   int                `json:"ordered"`
	Remaining int                `json:"remaining"`
	Orders    []ProcurementOrder `json:"orders"`
	// Key は束ねる鍵（材料なら3つ組、購入部品なら畳んだ品名）です。
	//
	// ⚠ **未手配の一覧が「発注部材表に入っている分」を引くのに使います**
	// （2026-09-22）——**同じ鍵**（`procKey`）で束ねないと、**引き算が合いません**。
	Key string `json:"-"`
}

// ProcurementProduct は受注明細の1行（＝加工製品1種類）です。
type ProcurementProduct struct {
	PageID    int               `json:"page_id"`
	Title     string            `json:"title"`
	ItemName  string            `json:"item_name"` // 受注明細の品名
	Qty       int               `json:"qty"`       // 受注数量
	Migrating bool              `json:"migrating"`
	Items     []ProcurementItem `json:"items"`
	Why       string            `json:"why"` // 引けなかった理由（空なら引けた）
}

// ProcurementByProduct は受注ページの手配状況を、加工製品ごとに集めます。
func ProcurementByProduct(user *auth.User, orderPageID int) ([]ProcurementProduct, error) {
	db := database.DB
	canView := viewCheck(user)

	items, err := cms.VocabTableRowsOf(db, orderPageID, clientOrderItemsType)
	if err != nil {
		return nil, err
	}
	if len(items) == 0 {
		return nil, nil
	}
	ordered, err := orderedByProduct(db, canView)
	if err != nil {
		return nil, err
	}

	var out []ProcurementProduct
	for _, it := range items {
		// ⚠ **弊社品番（加工製品ページ）が無ければ、何にも結べません。** 黙らずに
		//    理由を出します——空欄だと「要る物が無い」ように見えます。
		p := ProcurementProduct{
			ItemName: strings.TrimSpace(it.Values["item-name"]),
			Qty:      cms.VocabQuantity(it),
		}
		// 結び方は2通り——`弊社品番` と、`品番` の逆引き（`productOfOrderRow` に経緯）。
		idInt, ok := productOfOrderRow(db, it)
		if !ok {
			p.Why = "⚠ どの加工製品か分かりません（弊社品番を入れてください）"
			out = append(out, p)
			continue
		}
		p.PageID, p.Title = idInt, cms.PageTitleByID(idInt)
		if !canView(idInt) {
			// ⚠ **読めないことは知らせません**（C案）——行ごと落とします。
			continue
		}
		p.Migrating = isMigrating(db, idInt)
		p.Items = procurementItemsOf(db, idInt, p.Qty, ordered)
		if len(p.Items) == 0 && p.Why == "" {
			p.Why = "（この加工製品に購入品の登録がありません）"
		}
		out = append(out, p)
	}
	return out, nil
}

// productByCode は `品番` から加工製品ページを1枚だけ引きます（2枚以上なら引きません）。
//
// ⚠ **2つの経路を見ます。** 設定の `product_code_tags`（運用者が足せる・図面番号／
// 品番／部品番号）と、**材料表の宣言が要求するタグ**（`part-materials` の
// `RequiresTag`）。⚠ **設定だけに頼ると、運用者がその語を外した日に、移行前のページが
// 丸ごと出なくなります**——宣言側の鍵は**コードが持っている**ので外れません。
//
// ⚠ **2枚以上に当たったら引きません**——同じ番号で別の加工製品がありえます
// （「別の製品の図面番号が一致してしまう場合もあり…」）。**決めるのは人**。
func productByCode(db cms.ReadOnlyDB, code string) (int, bool) {
	hits := productCandidatesByCode(db, code)
	if len(hits) != 1 {
		return 0, false
	}
	return hits[0], true
}

// productCandidatesByCode は `品番` に当たる加工製品ページを全部返します（認可はしない）。
//
// `productByCode` の中身で、**当たった枚数**が要る口（結べない行の警告・
// unlinked_orders.go）のために分けました。⚠ **引く名前を別に組まないこと**——
// 警告と必要部材表が別の規則で引くと、「赤くないのに出ない」行が生まれます。
func productCandidatesByCode(db cms.ReadOnlyDB, code string) []int {
	names := append([]string{}, ProductCodeTags()...)
	if def, ok := cms.VocabDefByType(partMaterialsType); ok && def.RequiresTag != "" {
		names = append(names, def.RequiresTag)
	}
	return pagesByAnyTag(db, names, code)
}

// procurementItemsOf は加工製品1ページぶんの購入品を、必要数・発注済数つきで返します。
func procurementItemsOf(db cms.ReadOnlyDB, productID, orderQty int,
	ordered map[string][]ProcurementOrder) []ProcurementItem {
	var out []ProcurementItem
	add := func(kind, name, key string, per int) {
		if name == "" {
			return
		}
		item := ProcurementItem{Name: name, Kind: kind, Per: per, Key: key,
			Required: per * orderQty, Orders: ordered[procKey(productID, key)]}
		for _, o := range item.Orders {
			item.Ordered += o.Qty
		}
		item.Remaining = item.Required - item.Ordered
		if item.Remaining < 0 {
			item.Remaining = 0
		}
		out = append(out, item)
	}

	if rows, err := cms.VocabTableRowsOf(db, productID, partMaterialsType); err == nil {
		for _, m := range rows {
			key := materialKeyOf(m.Values["material"], m.Values["shape"], m.Values["size"])
			add(displayNameOf(partMaterialsType), materialNameOf(m), key, cms.VocabQuantity(m))
		}
	}
	if rows, err := cms.VocabTableRowsOf(db, productID, partPurchasedType); err == nil {
		for _, m := range rows {
			name := strings.TrimSpace(m.Values["item-name"])
			add(displayNameOf(partPurchasedType), name,
				cms.NormalizeText(name), cms.VocabQuantity(m))
		}
	}
	return out
}

// orderedByProduct は**全社の発注明細**を「加工製品＋購入品」の鍵で束ねます。
//
// ⚠ **`弊社品番` が書かれている行だけ**が結ばれます。書かれていない発注は
// 「どの加工製品のぶんか」を語らないので、**混ぜません**——混ぜると、同じ材料を使う
// 別の製品の発注が、この製品の手配済みに化けます。
func orderedByProduct(db cms.ReadOnlyDB, canView func(int) bool) (map[string][]ProcurementOrder, error) {
	rows, err := cms.VocabRowsOfType(db, ourOrderItemsType)
	if err != nil {
		return nil, err
	}
	out := map[string][]ProcurementOrder{}
	for _, r := range rows {
		if !canView(r.PageID) {
			continue
		}
		// ⚠ **取消の行も「手配した」に数えます**（2026-09-23 ユーザー:「状態を取り消しに
		//    することの意味が、**その部材はもう発注しない**ということになりました。
		//    **発注書ページで消費して発注しなくなります**」）。09-22 は逆（数えない＝
		//    必要部材表へ自動で戻る）でしたが、覆りました。⚠ **戻したいときは行末の
		//    「必要部材表へ戻す」**——行が発注書から消えるので、ここで数えなくなります。
		//
		// ⚠ **`未発注` も数えます。** 紙はできているので、ここで引かないと
		//    **同じものをもう一度発注書に入れてしまいます**。
		productID, ok := page.NormalizeID(strings.TrimSpace(r.Values["our-item-id"]))
		if !ok || productID == "" {
			continue // 弊社品番の無い行は結べない
		}
		// 鍵は材料なら3つ組、無ければ品名。⚠ **発注書の書き方に合わせます**。
		key := materialKeyOf(r.Values["material"], r.Values["shape"], r.Values["size"])
		if key == "" {
			key = cms.NormalizeText(strings.TrimSpace(r.Values["item-name"]))
		}
		if key == "" {
			continue
		}
		k := procKey(pageNum(productID), key)
		out[k] = append(out[k], ProcurementOrder{
			PageID: r.PageID, Title: cms.PageTitleByID(r.PageID), Qty: cms.VocabQuantity(r)})
	}
	return out, nil
}

// pageNum はゼロ詰め6桁のページIDを数にします（`page.NormalizeID` を通ったあとに使う）。
func pageNum(id string) int {
	n, _ := strconv.Atoi(id)
	return n
}

// procKey は「加工製品ページ＋購入品」の鍵です。
func procKey(productID int, itemKey string) string {
	return page.FormatID(productID) + "\x00" + itemKey
}
