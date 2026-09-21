package subcon

// ─────────────────────────────────────────────────────────────────────────
// まだ手配していない購入品を、**受注を横断して**集める（2026-09-21）
//
// ユーザー:「弊社の発注書は、**受注明細の単位とは無関係に、納期のグループなどから
// 発行**されます」。
//
// ⚠ **だから起点は1つの受注ページではありません。** 受注ページの手配状況
// （[procurement.go]）は「この受注はどうなっているか」を見るもので、
// **発注書を作るとき**に見たいのは「**いま買わなければならないもの全部**」です。
//
// ⚠ **スコープ（`RelatedPages`）に頼りません**——`cms.VocabRowsOfType` で
// **全社の受注明細**を読みます。受注ページがどこに置かれても効きます。
//
// ⚠ **並びは納期順**（発注をまとめる単位がそれなので）。日付として読めない納期
// （`最短納期`・`都度`）は**落とさず先頭へ**——落とすと「書いてあるのに出てこない」
// ことになります（受注残表と同じ扱い）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"sort"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/database"
)

// UnorderedItem は「まだ手配していない購入品」1行です。
type UnorderedItem struct {
	OrderPageID   int    `json:"order_page_id"`   // 受注ページ
	OrderTitle    string `json:"order_title"`     //
	Client        string `json:"client"`          // 発注元（お客様）
	Due           string `json:"due"`             // 納期（生の値）
	ProductPageID int    `json:"product_page_id"` // 加工製品ページ＝弊社品番
	ProductTitle  string `json:"product_title"`   //
	Material      string `json:"material"`        // 材質
	Shape         string `json:"shape"`           // 形状
	Size          string `json:"size"`            // 寸法
	Name          string `json:"name"`            // 表示用の名前
	Kind          string `json:"kind"`            // 材料 / 購入部品
	Remaining     int    `json:"remaining"`       // 残要手配数
	Cost          int    `json:"cost"`            // 参考単価（引けなければ 0）
	Supplier      string `json:"supplier"`        // その単価の仕入先
	Migrating     bool   `json:"migrating"`       // ⚠ 移行の確認前
}

// UnorderedItems は、まだ手配していない購入品を受注横断で集めます（納期順）。
func UnorderedItems(user *auth.User) ([]UnorderedItem, error) {
	db := database.DB
	orders, err := cms.VocabRowsOfType(db, clientOrderItemsType)
	if err != nil {
		return nil, err
	}
	if len(orders) == 0 {
		return nil, nil
	}
	canView := viewCheck(user)
	ordered, err := orderedByProduct(db, canView)
	if err != nil {
		return nil, err
	}
	prices, err := latestMaterialPrices(db, user)
	if err != nil {
		prices = map[string]materialPrice{}
	}

	// ページのタグ（発注元・納期）は1ページにつき1度だけ読む。
	type head struct{ client, due string }
	heads := map[int]head{}
	headOf := func(id int) head {
		if h, ok := heads[id]; ok {
			return h
		}
		tags, _ := cms.TagsOfPage(db, id)
		h := head{client: cms.FirstTag(tags, OrderClientTag), due: cms.FirstTag(tags, DueDateTag)}
		heads[id] = h
		return h
	}

	var out []UnorderedItem
	for _, o := range orders {
		if !canView(o.PageID) {
			continue
		}
		// ⚠ **完了した行は手配の対象ではありません**（受注残表と同じ線引き）。
		if strings.TrimSpace(o.Values["status"]) == StatusDone {
			continue
		}
		pid, ok := productOfOrderRow(db, o)
		if !ok {
			continue // ⚠ どの加工製品か分からない行は、買うものも分かりません
		}
		if !canView(pid) {
			continue
		}
		h := headOf(o.PageID)
		due := strings.TrimSpace(o.Values["due"])
		if due == "" {
			due = h.due // 行に無ければページの納期（受注残表と同じ順）
		}
		mig := isMigrating(db, pid)
		for _, it := range procurementItemsOf(db, pid, cms.VocabQuantity(o), ordered) {
			if it.Remaining <= 0 {
				continue // 手配済み
			}
			u := UnorderedItem{
				OrderPageID: o.PageID, OrderTitle: cms.PageTitleByID(o.PageID),
				Client: h.client, Due: due,
				ProductPageID: pid, ProductTitle: cms.PageTitleByID(pid),
				Name: it.Name, Kind: it.Kind, Remaining: it.Remaining, Migrating: mig,
			}
			fillUnorderedMaterial(db, pid, &u, prices)
			out = append(out, u)
		}
	}
	sortUnordered(out)
	return out, nil
}

// fillUnorderedMaterial は材料の3つ組と参考単価を埋めます。
//
// ⚠ **名前から材質・形状・寸法を切り出しません**（`materialNameOf` が空白で繋いだ
// ものなので、`鉄 角パイプ □75*75` を割り戻すと**空白を含む値で壊れます**）。
// 材料表をもう一度読んで、**名前が一致する行**から採ります。
func fillUnorderedMaterial(db cms.ReadOnlyDB, productID int, u *UnorderedItem,
	prices map[string]materialPrice) {
	if u.Kind != displayNameOf(partMaterialsType) {
		return
	}
	rows, err := cms.VocabTableRowsOf(db, productID, partMaterialsType)
	if err != nil {
		return
	}
	for _, m := range rows {
		if materialNameOf(m) != u.Name {
			continue
		}
		u.Material = strings.TrimSpace(m.Values["material"])
		u.Shape = strings.TrimSpace(m.Values["shape"])
		u.Size = strings.TrimSpace(m.Values["size"])
		if p, ok := prices[materialKeyOf(u.Material, u.Shape, u.Size)]; ok {
			u.Cost, u.Supplier = p.Cost, p.Supplier
		}
		return
	}
}

// sortUnordered は納期順に並べます。
//
// ⚠ **日付として読めない納期は先頭**です（`最短納期`・`都度`）——**急ぎの合図**として
// 書かれていることが多く、後ろへ回すと見落とします（受注残表と同じ判断）。
func sortUnordered(list []UnorderedItem) {
	sort.SliceStable(list, func(i, j int) bool {
		a, b := list[i], list[j]
		ad, bd := isDateLike(a.Due), isDateLike(b.Due)
		if ad != bd {
			return !ad // 日付でないほうが先
		}
		if a.Due != b.Due {
			return a.Due < b.Due
		}
		if a.ProductPageID != b.ProductPageID {
			return a.ProductPageID < b.ProductPageID
		}
		return a.Name < b.Name
	})
}

// isDateLike は納期が日付として読めるかを返します。
func isDateLike(s string) bool {
	_, ok := cms.NormalizeValue(cms.ColDate, strings.TrimSpace(s))
	return ok && strings.TrimSpace(s) != ""
}
