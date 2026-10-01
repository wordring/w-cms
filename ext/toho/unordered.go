package toho

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
	Machine       string `json:"machine"`         // 装置名称（同じ納期の中の並び順）
	Material      string `json:"material"`        // 材質
	Shape         string `json:"shape"`           // 形状
	Size          string `json:"size"`            // 寸法
	Name          string `json:"name"`            // 表示用の名前
	Kind          string `json:"kind"`            // 種類（設定 order_kinds の kind）
	Remaining     int    `json:"remaining"`       // 残要手配数
	Cost          int    `json:"cost"`            // 参考単価（引けなければ 0）
	Supplier      string `json:"supplier"`        // その単価の仕入先
	Migrating     bool   `json:"migrating"`       // ⚠ 移行の確認前
	// Values は発注の表の列ごとの値（2026-09-28・種類の columns から）——画面が発注部材表へ運ぶ。
	Values map[string]string `json:"values,omitempty"`

	// 以下は**臨時部材表から来た行**だけが持ちます（2026-09-25・temp_parts.go）。
	// TempRow > 0 なら臨時部材の行で、発注部材表へ入れると臨時部材表から消えます。
	TempPage string `json:"temp_page,omitempty"`
	TempRow  int    `json:"temp_row,omitempty"`
	ItemID   string `json:"item_id,omitempty"`
	ItemName string `json:"item_name,omitempty"`
	Color    string `json:"color,omitempty"`
	Unit     string `json:"unit,omitempty"`
	Note     string `json:"note,omitempty"`
	CostRaw  string `json:"cost_raw,omitempty"` // 人が書いた単価（書いてあればそれを運ぶ）
}

// UnorderedItems は、まだ手配していない購入品を受注横断で集めます（納期順）。
func UnorderedItems(user *auth.User) ([]UnorderedItem, error) {
	db := database.DB
	prices, err := latestMaterialPrices(db, user)
	if err != nil {
		prices = map[string]materialPrice{}
	}
	// ⚠ **臨時部材表の行も並べます**（2026-09-25・temp_parts.go）——計算の鎖に乗らない
	//    材料は、人が書いたこの表が「必要」の記録です。⚠ **受注明細が0件でも出します**。
	temp := TempPartItems(user, prices)
	canView := viewCheck(user)
	// 手配の対象になる受注の行（受注残表と同じ線引き——移行中・完了・出し終えた行は入れない・2026-10-01 に出し終えた行も）。
	open, err := openOrderRows(db, canView)
	if err != nil {
		return nil, err
	}
	if len(open) == 0 {
		return temp, nil
	}
	// ⚠ **手当て（発注明細・発注部材表・手配不要）は受注ごとに当てます**（2026-10-01・procure_ledger.go）。それまでは
	//    加工製品ごとの通算を、どの受注の行からも引いていました（2つの受注が同時に残ると両方0に見える・納め終えた
	//    注文のために買った分が次の注文を埋める）。⚠ **発注部材表に入れた分も引きます**（2026-09-22——引かないと
	//    二重に出て、同じものを2回発注しかねません）。
	ledger := buildLedger(db, canView, open)

	var out []UnorderedItem
	for _, o := range open {
		mig := isMigrating(db, o.pid)
		machine := machineOf(db, o.pid)
		for _, it := range ledger.needs[refOf(o.row)] {
			rem := it.Required - ledger.coverOf(o.row, it.Key).total()
			if rem <= 0 {
				continue // 手配済み（発注した・発注部材表に入れた・不要にした・在庫を回した）
			}
			u := UnorderedItem{
				OrderPageID: o.row.PageID, OrderTitle: cms.PageTitleByID(o.row.PageID),
				Client: o.client, Due: o.due,
				ProductPageID: o.pid, ProductTitle: cms.PageTitleByID(o.pid), Machine: machine,
				Name: it.Name, Kind: it.Kind, Remaining: rem, Migrating: mig, Values: it.Values,
			}
			fillUnorderedMaterial(db, o.pid, &u, prices)
			out = append(out, u)
		}
	}
	sortUnordered(out)
	// ⚠ **臨時部材は先頭にまとめます**——納期も装置も無いので、並べ替えに混ぜると
	//    計算の行の間に散ります。書いた順のまま出します。
	return append(temp, out...), nil
}

// fillUnorderedMaterial は材料の3つ組と参考単価を埋めます。
//
// ⚠ **名前から材質・形状・寸法を切り出しません**（`materialNameOf` が空白で繋いだ
// ものなので、`鉄 角パイプ □75*75` を割り戻すと**空白を含む値で壊れます**）。
// 材料表をもう一度読んで、**名前が一致する行**から採ります。
func fillUnorderedMaterial(db cms.ReadOnlyDB, productID int, u *UnorderedItem,
	prices map[string]materialPrice) {
	// ⚠ 2026-09-28 から、3つ組は**集計が種類の columns から組んだ値**（`Values`）から採ります
	//    ——材料表を読み直して名前で当てる必要が無くなった。3つ組のある行だけ参考単価を引く。
	u.Material = strings.TrimSpace(u.Values["材質"])
	u.Shape = strings.TrimSpace(u.Values["形状"])
	u.Size = strings.TrimSpace(u.Values["寸法"])
	if u.Material == "" && u.Shape == "" && u.Size == "" {
		return
	}
	if p, ok := prices[materialKeyOf(u.Material, u.Shape, u.Size)]; ok {
		u.Cost, u.Supplier = p.Cost, p.Supplier
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
		// ⚠ **同じ納期の中は装置順**（2026-09-22 ユーザー:「納期順で並べ替えをし、
		//    さらに**同じ納期の中で装置順**に並べ替えをします」）。
		//    装置ごとにまとまっていないと、**1台ぶんの部材が表のあちこちに散り**、
		//    まとめて発注しにくくなります。
		//    ⚠ **装置が空のものは後ろ**——読めなかったものを先頭に置きません
		//    （納期が読めないものを先頭に置くのとは**逆の判断**です。あちらは
		//    「急ぎの合図」でしたが、装置が空なのは**ただ分からない**だけです）。
		if a.Machine != b.Machine {
			if a.Machine == "" || b.Machine == "" {
				return b.Machine == ""
			}
			return a.Machine < b.Machine
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

// machineOf は加工製品ページの装置名称を返します。
//
// ⚠ **階層（`取引先／社名／段／装置名称／図面名称`）からは辿りません**——ワンノートから
// 移したページはまだ階層に入っていないことがあり、**タグのほうが確かです**。
func machineOf(db cms.ReadOnlyDB, productID int) string {
	tags, err := cms.TagsOfPage(db, productID)
	if err != nil {
		return ""
	}
	return strings.TrimSpace(cms.FirstTag(tags, MachineNameTag))
}
