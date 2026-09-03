package sheetmetal

import (
	"encoding/json"
	"net/http"
	"sort"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
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
	cms.Register(materialsPlugin{})
	// 計算ビューの描画も自分で登録する（形式の宣言と対）。
	cms.RegisterView("required-materials", requiredMaterialsViewHTML)
}

type materialsPlugin struct{}

func (materialsPlugin) Name() string { return "materials" }

// Schema / Tables は空です。専用テーブルを持たない③計算だけのプラグインで、
// 読む先は汎用索引（vocab_index プラグインが所有）です。
func (materialsPlugin) Schema() []string { return nil }

func (materialsPlugin) Tables() []string { return nil }

// Routes は部材手配計算APIのエンドポイントを提供します（RouteProvider実装）。
func (materialsPlugin) Routes() []cms.Route {
	return []cms.Route{
		{Pattern: "/api/required-materials", Handler: RequiredMaterialsAPIHandler},
		// PDF解析（板金部の既定セット）。main.go への直書きをやめてここへ寄せた
		// ——ルートも拡張と一緒に外れる（`-tags minimal` で消える）。
		{Pattern: "/api/analyze-attachment", Handler: AnalyzeAttachmentAPIHandler},
		// 部品ページの整理（提案を出す口と、実行する口）。**提案は何も作りません**
		// ——顧客名・装置名称のページが生まれるのは実行のときだけ（filing.go）。
		{Pattern: "/api/filing-proposal", Handler: FilingProposalAPIHandler},
		{Pattern: "/api/file-drawings", Handler: FileDrawingsAPIHandler},
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
	orderItems, err := cms.VocabTableRowsOf(db, pageIDInt, "client-order-items")
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
	materialsDef, _ := cms.VocabDefByType("part-materials")
	tagName := materialsDef.RequiresTag

	// 同じ品番が明細に何度出ても、定義の引き直しは1度だけ。
	defsFor := map[string][]cms.VocabRow{}

	for _, item := range orderItems {
		partID := item.Values["item-id"]
		if partID == "" {
			continue // 品番の無い行は突き合わせようがない
		}
		orderQty := cms.VocabQuantity(item)

		mats, ok := defsFor[partID]
		if !ok {
			pageIDs, err := cms.PagesByTag(db, tagName, partID)
			if err != nil {
				return nil, err
			}
			for _, defPageID := range pageIDs {
				rows, err := cms.VocabTableRowsOf(db, defPageID, "part-materials")
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
			name := materialNameOf(m)
			totalReq := cms.VocabQuantity(m) * orderQty
			if existing, ok := materialsMap[name]; ok {
				existing.TotalRequired += totalReq
			} else {
				// **仕入先と単価は定義側から採りません**（2026-09-03 ユーザー:
				// 「仕入れ先は複数あります」「単価は外してよいと思います」）。
				// 埋まるのは発注実績が付いたとき——発注書のヘッダから仕入先が来ます。
				materialsMap[name] = &RequiredMaterialResponse{
					MaterialName:  name,
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
	headers, err := cms.VocabBlocksOf(db, pageIDInt, "our-order")
	if err != nil {
		return nil, err
	}
	supplierOf := map[int]string{}
	for _, h := range headers {
		supplierOf[h.BlockNo] = h.Values["supplier-name"]
	}

	ourItems, err := cms.VocabTableRowsOf(db, pageIDInt, "our-order-items")
	if err != nil {
		return nil, err
	}
	for _, oi := range ourItems {
		name := oi.Values["item-name"]
		quantity := cms.VocabQuantity(oi)
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

// materialNameOf は材料の行から**名前にあたるもの**を作ります。
//
// 材料に単独の「名前」の列はありません——ユーザーの実務では
// **材質・形状・寸法の3つで決まります**（`SS400 板 t3.2 1000×500`）。
// ③計算は発注明細の品名と突き合わせるので、ここで1つの文字列に繋ぎます。
//
// 古い形（部材名の1列だけ）で書かれた行も読めるようにしてあります
// ——列を変える前に作られたページを黙って落とさないため。
func materialNameOf(row cms.VocabRow) string {
	if n := strings.TrimSpace(row.Values["item-name"]); n != "" {
		return n
	}
	parts := make([]string, 0, 3)
	for _, f := range []string{"material", "shape", "size"} {
		if v := strings.TrimSpace(row.Values[f]); v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, " ")
}
