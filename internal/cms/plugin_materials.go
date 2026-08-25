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
// user は閲覧者（匿名は nil）。**部材の定義元ページを読めない相手には、その定義を
// 集計へ混ぜない**。集計対象ページ（受注ページ）の read だけでは足りないため:
// 品番は本文へ自由に書けるので、自分のページに任意の品番を1行置けば、読めない
// 部品定義ページの部材名・仕入先・原価を引けてしまっていた（設計総点検）。
// 判定は page.CanView に集約し、一覧の絞り込みと同じ規則を使う。
func RequiredMaterials(user *auth.User, pageIDInt int) ([]RequiredMaterialResponse, error) {
	// 1. そのページ内の受注 client_orders の明細を取得する
	//    明細が page_id を持つので直接引く。order_no のサブクエリだったころは、
	//    同じ番号を別ページで使うと他ページの明細まで拾っていた（設計総点検③）。
	rows, err := database.DB.Query(`
		SELECT item_id, quantity
		FROM client_order_items
		WHERE page_id = ?
	`, pageIDInt)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	type orderItem struct {
		ItemID   string
		Quantity int
	}
	var clientItems []orderItem
	for rows.Next() {
		var item orderItem
		if err := rows.Scan(&item.ItemID, &item.Quantity); err == nil {
			clientItems = append(clientItems, item)
		}
	}

	// 2. 各受注部品に対し、必要な部材の定義 part_materials を取得し、総必要数を集計する
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

	for _, item := range clientItems {
		// 行を回しながら権限を引くと、入れ子のクエリが別接続を掴んで
		// フェイルクローズしうる。先に読み切ってから絞る。
		type materialRow struct {
			Name      string
			Supplier  string
			Cost      int
			UnitQty   int
			DefPageID int
		}
		var mats []materialRow
		matRows, err := database.DB.Query(`
			SELECT material_name, cost, supplier_name, quantity, COALESCE(page_id, 0)
			FROM part_materials
			WHERE part_id = ?
		`, item.ItemID)
		if err != nil {
			return nil, err
		}
		for matRows.Next() {
			var m materialRow
			if err := matRows.Scan(&m.Name, &m.Cost, &m.Supplier, &m.UnitQty, &m.DefPageID); err == nil {
				mats = append(mats, m)
			}
		}
		matRows.Close()

		for _, m := range mats {
			if !canView(m.DefPageID) {
				continue // 定義元ページを読めない相手には見せない
			}
			totalReq := m.UnitQty * item.Quantity
			if existing, ok := materialsMap[m.Name]; ok {
				existing.TotalRequired += totalReq
			} else {
				materialsMap[m.Name] = &RequiredMaterialResponse{
					MaterialName:  m.Name,
					SupplierName:  m.Supplier,
					Cost:          m.Cost,
					TotalRequired: totalReq,
					Ordered:       0,
				}
			}
		}
	}

	// 3. 同じ page_id に紐づく弊社の発注実績 our_orders の明細を取得し、発注済数を集計する
	//    JOIN の条件に page_id も入れる。番号だけで結ぶと、同じ番号を使う別ページの
	//    ヘッダと結合して仕入先が入れ替わりうる（設計総点検③）。
	ourRows, err := database.DB.Query(`
		SELECT ooi.item_name, ooi.quantity, oo.supplier_name
		FROM our_order_items ooi
		JOIN our_orders oo ON ooi.order_no = oo.order_no AND ooi.page_id = oo.page_id
		WHERE ooi.page_id = ?
	`, pageIDInt)
	if err != nil {
		return nil, err
	}
	defer ourRows.Close()

	for ourRows.Next() {
		var itemName, supplierName string
		var quantity int
		if err := ourRows.Scan(&itemName, &quantity, &supplierName); err == nil {
			if existing, ok := materialsMap[itemName]; ok {
				existing.Ordered += quantity
			} else {
				materialsMap[itemName] = &RequiredMaterialResponse{
					MaterialName:  itemName,
					SupplierName:  supplierName,
					Cost:          0,
					TotalRequired: 0,
					Ordered:       quantity,
				}
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
