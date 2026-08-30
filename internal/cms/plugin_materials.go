package cms

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// ─────────────────────────────────────────────────────────────────────────
// プラグイン例（集計のみ）: 部品の構成部材（BOM）の手配計算
//
// **このプラグインはもうテーブルを持ちません**（D-1・2026-08-31）。部材表
// <table data-type="part-materials"> の中身は汎用索引 vocab_index が受け持ち、
// ここに残るのは③計算——GET /api/required-materials（部材手配計算API）と、
// 計算ビューのサーバー事前描画が共用する RequiredMaterials だけです。
//
// 「語彙とプラグインは運用者のもの」（要件定義書 §1.1・§4.5）の形に近づいた、
// というのがこの引き算の意味です。コアのスキーマから板金部の語彙が消えました。
// ─────────────────────────────────────────────────────────────────────────

func init() {
	Register(materialsPlugin{})
}

type materialsPlugin struct{}

func (materialsPlugin) Name() string { return "materials" }

// Schema / Tables は空です。専用テーブルを持たない③計算だけのプラグインで、
// 読む先は汎用索引（vocab_index プラグインが所有）です。
func (materialsPlugin) Schema() []string { return nil }

func (materialsPlugin) Tables() []string { return nil }

// vocabNumber は表の値を数として読みます（¥・桁区切り・全角を吸収）。
func vocabNumber(raw string) int {
	if norm, ok := NormalizeValue(ColNumber, raw); ok {
		return AtoiSafe(norm)
	}
	return AtoiSafe(raw)
}

// Routes は部材手配計算APIのエンドポイントを提供します（RouteProvider実装）。
func (materialsPlugin) Routes() []Route {
	return []Route{
		{Pattern: "/api/required-materials", Handler: RequiredMaterialsAPIHandler},
	}
}

// RequiredMaterialResponse は部材手配の進捗状況を返却するためのJSON構造体です。
type RequiredMaterialResponse struct {
	MaterialName  string `json:"material_name"`
	SupplierName  string `json:"supplier_name"`
	Cost          int    `json:"cost"`
	TotalRequired int    `json:"total_required"`
	Ordered       int    `json:"ordered"`
	Remaining     int    `json:"remaining"`
}

// RequiredMaterialsAPIHandler は指定されたpage_id(受注ページ)に紐づく部材の
// 要手配数・発注済数を集計して返却します。
func RequiredMaterialsAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}

	pageID := r.URL.Query().Get("page_id")
	if pageID == "" {
		http.Error(w, "Missing page_id parameter", http.StatusBadRequest)
		return
	}
	// 集計対象ページの read 権限を要求する
	if !page.RequirePageRead(w, r, pageID) {
		return
	}

	pageIDInt, err := strconv.Atoi(pageID)
	if err != nil {
		http.Error(w, "Invalid page_id format", http.StatusBadRequest)
		return
	}

	list, err := RequiredMaterials(auth.CurrentUser(r), pageIDInt)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(list)
}

// RequiredMaterials は指定ページ（受注ページ）に紐づく部材の要手配数・発注済数を
// 集計します。/api/required-materials と計算ビューのサーバー事前描画
// （view_render.go）が共用する。
//
// **読む先は汎用索引 vocab_index だけです**（D-1・2026-08-31）。かつては
// client_order_items / part_materials / our_order_items+our_orders という
// 硬いドメイン表4つを引いていましたが、テーブルごと廃しました。鍵の変換
// （見出しの表示文字 → 機械キー）は vocab_query.go が引き受けます。
//
// user は閲覧者（匿名は nil）。**部材の定義元ページを読めない相手には、その定義を
// 集計へ混ぜない**。集計対象ページ（受注ページ）の read だけでは足りないため:
// 品番は本文へ自由に書けるので、自分のページに任意の品番を1行置けば、読めない
// 部品定義ページの部材名・仕入先・原価を引けてしまっていた（設計総点検）。
// 判定は page.CanView に集約し、一覧の絞り込みと同じ規則を使う。
func RequiredMaterials(user *auth.User, pageIDInt int) ([]RequiredMaterialResponse, error) {
	db := database.DB

	// 1. そのページの受注明細（品番・数量）を索引から読む。
	//    ページで絞るので、同じ発注書番号を別ページで使っても混ざらない
	//    （硬い表のころ order_no のサブクエリで他ページの明細まで拾った・設計総点検③）。
	orderItems, err := vocabTableRowsOf(db, pageIDInt, "client-order-items")
	if err != nil {
		return nil, err
	}

	materialsMap := make(map[string]*RequiredMaterialResponse)

	// 定義元ページの可視判定は1ページにつき1度だけ引く（品番ごとに何度も辿らない）。
	visible := map[int]bool{}
	canView := func(defPageID int) bool {
		if v, ok := visible[defPageID]; ok {
			return v
		}
		v := page.CanView(user, defPageID)
		visible[defPageID] = v
		return v
	}

	// 2. 各受注部品に対し、必要な部材の定義を集めて総必要数を積む。
	//
	//    部品番号は部材表の中ではなく**ページ全体のタグ**にあるので、
	//    「そのタグを持つページ」を逆引きしてから、そのページの部材表を読みます。
	//    鍵の名前はレジストリ宣言（part-materials の RequiresTag）が持つ——ここへ
	//    直書きすると、見出しを改名したときに告知する側と読む側がずれる（設計総点検⑤）。
	materialsDef, _ := VocabDefByType("part-materials")
	tagName := materialsDef.RequiresTag

	// 同じ品番が明細に何度出ても、定義の引き直しは1度だけ。
	defsFor := map[string][]VocabRow{}

	for _, item := range orderItems {
		partID := item.Values["item-id"]
		if partID == "" {
			continue // 品番の無い行は突き合わせようがない
		}
		orderQty := vocabQuantity(item)

		mats, ok := defsFor[partID]
		if !ok {
			pageIDs, err := pagesByTag(db, tagName, partID)
			if err != nil {
				return nil, err
			}
			for _, defPageID := range pageIDs {
				rows, err := vocabTableRowsOf(db, defPageID, "part-materials")
				if err != nil {
					return nil, err
				}
				mats = append(mats, rows...)
			}
			defsFor[partID] = mats
		}

		for _, m := range mats {
			if !canView(m.PageID) {
				continue // 定義元ページを読めない相手には見せない
			}
			name := m.Values["item-name"]
			totalReq := vocabQuantity(m) * orderQty
			if existing, ok := materialsMap[name]; ok {
				existing.TotalRequired += totalReq
			} else {
				materialsMap[name] = &RequiredMaterialResponse{
					MaterialName:  name,
					SupplierName:  m.Values["supplier-name"],
					Cost:          m.Num("cost"),
					TotalRequired: totalReq,
					Ordered:       0,
				}
			}
		}
	}

	// 3. 同じページの発注実績から発注済数を積む。
	//    仕入先は明細ではなくヘッダ（名前：値）にあるので、同じページの
	//    our-order ブロックから引きます。**対応づけは block_no**——索引の
	//    ブロック番号は形式ごとの文書順連番なので、1つの発注書セクションが
	//    ヘッダ1つと明細表1つを持つ限り、同じ番号どうしが対になります。
	//    （硬い表のころは発注書番号で結んでいたが、番号が重複すると仕入先が
	//    入れ替わりえた・設計総点検③。文書順なら重複しても取り違えない）
	headers, err := vocabBlocksOf(db, pageIDInt, "our-order")
	if err != nil {
		return nil, err
	}
	supplierOf := map[int]string{}
	for _, h := range headers {
		supplierOf[h.BlockNo] = h.Values["supplier-name"]
	}

	ourItems, err := vocabTableRowsOf(db, pageIDInt, "our-order-items")
	if err != nil {
		return nil, err
	}
	for _, oi := range ourItems {
		name := oi.Values["item-name"]
		quantity := vocabQuantity(oi)
		if existing, ok := materialsMap[name]; ok {
			existing.Ordered += quantity
		} else {
			materialsMap[name] = &RequiredMaterialResponse{
				MaterialName:  name,
				SupplierName:  supplierOf[oi.BlockNo],
				Cost:          0,
				TotalRequired: 0,
				Ordered:       quantity,
			}
		}
	}

	// 4. 残要注文数を算出し、スライスに変換
	list := make([]RequiredMaterialResponse, 0)
	for _, m := range materialsMap {
		m.Remaining = m.TotalRequired - m.Ordered
		if m.Remaining < 0 {
			m.Remaining = 0
		}
		list = append(list, *m)
	}

	// 表示・応答が呼び出しごとに変わらないよう部材名順に揃える（map の走査順は不定）。
	sort.Slice(list, func(i, j int) bool { return list[i].MaterialName < list[j].MaterialName })
	return list, nil
}

// vocabQuantity は数量列を読みます。**空セルは 1**（旧 <m-material> の既定を
// 引き継いだ値で、硬い表のころは索引を書く側が同じ既定を当てていた）。
func vocabQuantity(row VocabRow) int {
	if row.Values["quantity"] == "" {
		return 1
	}
	return row.Num("quantity")
}
