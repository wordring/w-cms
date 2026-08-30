package cms

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// ─────────────────────────────────────────────────────────────────────────
// プラグイン例（特殊な値の注入 ＋ 集計API付き）: 部品の構成部材（BOM）
//
//   <table data-type="part-materials"> の行（③計算形式。見出しから機械キー
//   item-name / cost / supplier-name / quantity へ解決。語彙モデル §8.1）
//   部品番号はページ横断メタ（可変タグ）から TagValue で取得
//
//   → part_materials（part_id はページの「部品番号」タグから全行に注入）
//
// さらに RouteProvider を実装し、GET /api/required-materials（部材手配計算API）を
// 提供します（Tier 2: 集計ロジックはコードプラグインとして持つ）。
// ─────────────────────────────────────────────────────────────────────────

func init() {
	Register(materialsPlugin{})
}

type materialsPlugin struct{}

func (materialsPlugin) Name() string { return "materials" }

func (materialsPlugin) Schema() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS part_materials (
			id INTEGER PRIMARY KEY AUTOINCREMENT,
			part_id TEXT,
			material_name TEXT,
			cost INTEGER,
			supplier_name TEXT,
			quantity INTEGER,
			page_id INTEGER,
			FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
		);`,
	}
}

func (materialsPlugin) Tables() []string {
	return []string{"part_materials"}
}

// Triggers は部材表だけを担当することを宣言します。
func (materialsPlugin) Triggers() []string { return []string{"part-materials"} }

// OnPageStart は当該ページ分を洗い流します。
func (materialsPlugin) OnPageStart(ctx *ObserveContext) error {
	_, err := ctx.Tx.Exec(`DELETE FROM part_materials WHERE page_id = ?`, ctx.PageID)
	return err
}

// OnElement は1つの部材表を読み、行を書き込みます。
func (materialsPlugin) OnElement(ctx *ObserveContext, el *html.Node) (bool, error) {
	if el.Data != "table" {
		return true, nil
	}
	// part_id は部材行自身の値ではなく、ページ全体の「部品番号」タグ（可変タグ）から
	// 取得し、ページ内のすべての部材行に一括で付与する。**文書順では拾えない**
	// （タグが表より後ろにあってもよい）ので、コアが用意する ctx.Tag から引く。
	// 鍵の名前はレジストリ宣言（part-materials の RequiresTag）が持つ——ここへ直書き
	// すると、見出しを改名したときに告知する側と読む側がずれる（設計総点検⑤）。
	materialsDef, _ := VocabDefByType("part-materials")
	partID := ""
	if ctx.Tag != nil {
		partID = ctx.Tag(materialsDef.RequiresTag)
	}

	insert := func(itemName string, cost int, supplierName string, quantity int) error {
		_, err := ctx.Tx.Exec(`
			INSERT INTO part_materials (part_id, material_name, cost, supplier_name, quantity, page_id)
			VALUES (?, ?, ?, ?, ?, ?)
		`, partID, itemName, cost, supplierName, quantity, ctx.PageID)
		return err
	}
	return false, syncMaterialsTable(el, insert)
}

// syncMaterialsTable は <table data-type="part-materials"> のデータ行を insert へ流し込みます。
//
// 列の対応は文書自身の見出し行から解決する（語彙モデル §5.1）: 鍵は見出しの表示文字で、
// レジストリ宣言（part-materials の Label）を通じて機械キーへ正規化される。
// 見出しを「単価（税抜）」等へ改名すると解決できなくなり、その列は読まれない
// （保存時に UnresolvedVocabFields が告知する）。
// 数値（cost / quantity）は語彙の正規化（¥・桁区切り・全角の吸収）を通して読む。
// quantity の空セルは 1 として扱う（旧 <m-material> の既定を引き継いだ値）。
func syncMaterialsTable(table *html.Node, insert func(string, int, string, int) error) error {
	def, _ := VocabDefByType("part-materials")
	// 見出し行から機械キーへの解決は共有部品（VocabTableRows）に任せる。
	// ここには同じ処理の逐語コピーがあったが、鍵の決め方が変わったときに
	// **片方だけ直る**危険があるので寄せた（受発注の明細も同じ関数を通る）。
	for _, values := range VocabTableRows(table, def) {
		quantity := 1 // 空セルの既定（旧 Quantity() と同じ）
		if v := values["quantity"]; v != "" {
			quantity = vocabNumber(v)
		}
		if err := insert(values["item-name"], vocabNumber(values["cost"]), values["supplier-name"], quantity); err != nil {
			return err
		}
	}
	return nil
}

// vocabNumber はセルの数値を語彙の正規化（¥8,000 → 8000 等）を通して整数で返します。
// 解釈できなければ 0（旧 AtoiSafe と同じ安全側）。
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
