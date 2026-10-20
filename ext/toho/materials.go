package toho

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
// （2026-09-21 から、計算ビューの描画は加工製品ごとの集計 `procurementViewHTML`〔view_procurement.go〕へ
// 替わりました。いま `RequiredMaterials` を呼ぶのは `/api/required-materials` と試験だけです——下の init()。）
//
// 「語彙とプラグインは運用者のもの」（要件定義書 §1.1・§4.5）の形に近づいた、
// というのがこの引き算の意味です。コアのスキーマから板金部の語彙が消えました。
// ─────────────────────────────────────────────────────────────────────────

func init() {
	// **名簿に載る**（起動ログと画面の出し分け。internal/cms/extensions.go）。
	cms.RegisterExtension("toho", "東邦の業務")
	// 画面の JS・CSS は、この拡張のフォルダの assets/ に置けば拾われます（2026-10-20・internal/cms/ext_scripts.go の置き場の決まり）。
	cms.Register(materialsPlugin{})
	// 計算ビューの描画も自分で登録する（形式の宣言と対）。
	// ⚠ **描き方を 2026-09-21 に作り直しました**（view_procurement.go）。
	// それまでは**材料名で受注ページ全体を合算**していたので、**どの加工製品のぶんか**が
	// 消えていました。ユーザー:「各受注ページに**各加工製品ごとの項目と購入品の表を
	// 集める**必要があり、その表の列の一つとして、**発注書番号と発注書ページへのリンク**が
	// 必要」。⚠ 古い集計（`RequiredMaterials`）は `/api/required-materials` を叩く
	// 相手が居るので残してあります——**表示だけ**を差し替えました。
	cms.RegisterView("required-materials", procurementViewHTML)
	// 材料を探す欄（2026-09-21・view_material_search.go）。
	cms.RegisterView(MaterialSearchViewType, materialSearchViewHTML)
	// 未手配の一覧（2026-09-21・view_unordered.go）。ここから部材発注書を1枚作ります。
	cms.RegisterView(UnorderedViewType, unorderedViewHTML)
	// 未発注の部材発注書（2026-09-24・unsent_orders.go）。DBから毎回数える鏡。
	cms.RegisterView(UnsentOrdersViewType, unsentOrdersViewHTML)
	// 加工製品の一覧（2026-09-29・product_list.go）。区分と装置名称で絞って見る鏡。
	cms.RegisterView(ProductListViewType, productListViewHTML)

	// **受注の置き場は管理画面のボタンで作れます**（2026-09-16）。
	// 整理のときにも自動で作られます（`cms.EnsureTopLevelBox`）——先に作れるように
	// したのは、**整理する前に「どこへ行くのか」を人が見られるようにする**ためです。
	// **作業面は受注残**（2026-09-27 に本文を足した——それまでは見出しだけの箱で、職場の受注
	// フォルダの受注残は人が後から貼っていた）。整理の途中で作るときも同じ本文になります
	// （`cms.EnsureTopLevelBox` も同じ題のテンプレートを写す・テンプレート駆動の A）。
	// **加工製品の階層の根**（2026-09-16 にアドレス帳の木と分けた・customer_box.go）。
	cms.RegisterRequiredPage(cms.RequiredPage{
		Title:     CustomerBoxTitle,
		Extension: "toho",
		Why:       "加工製品の台帳の根です（社名／装置名称／加工製品。装置名称は無くてもよい）。整理を実行すると、通信記録の下にできた加工製品ページがここへ移ります。相手の連絡先は「連絡帳」のほうです。",
	})
	cms.RegisterRequiredPage(cms.RequiredPage{
		Title:     OrderBoxTitle,
		Extension: "toho",
		Why:       "案件の作業面です（案件残表）。案件ページは、整理を実行すると記録／年／月／依頼へ移ります（2026-10-16 から。それより前に収めた案件ページはこの下にあります）。",
	})
	// ⚠ 置き場「部材発注」と「見積」は 2026-10-19 になくした（登録も外した）。作業面（臨時部材表・必要部材表・手配不要表・
	// 未発注の部材発注書・未送付の見積書・見積書を作る）は、トップ直下の「製造」へ移した（mfg.go の登録の Why）。
	// 2026-10-19 利用者:「部材発注、見積もり、部材見積依頼には子ページが無いのでフォルダを削除できるのでは？」

	// 見積書ページ（2026-10-01・estimate_doc.go）——見積計算表の「見積書に入れる」が写す。
	cms.RegisterPageTemplate(cms.PageTemplate{
		Title:     EstimateTemplate,
		Extension: "toho",
		Why:       "見積表の「見積書に入れる」が見積書ページを作るときに写します。キャプション「見積明細表」の表が要ります（タグ 見積番号・見積先・見積先担当・見積日・受渡期日・受渡場所・取引方法・有効期限・見積担当と、見出し「備考」の節は、あれば埋めます）。",
	})

	// **機械が作るページもテンプレートから作ります**（2026-09-27・テンプレート駆動の D）。
	// 形（タグの並び・表の列・節）はテンプレート、値と行の数は機械——管理画面に「在る・無い」を出す。
	cms.RegisterPageTemplate(cms.PageTemplate{
		Title:     OrderPageTemplate,
		Extension: "toho",
		Why:       "発注書PDFの解析が案件ページを作るときに写します。キャプション「案件明細表」の表が要ります（原本PDFのファイル表示・「顧客の発注書（読んだまま）」の枠は、あれば埋めます）。",
	})
	cms.RegisterPageTemplate(cms.PageTemplate{
		Title:     ProductTemplate,
		Extension: "toho",
		Why:       "図面PDFの解析が加工製品ページを作るときに写します。見出し「図面」の節（図面ブロック）と、キャプション「改訂明細表」の表が要ります。",
	})
	// テンプレート「製造の加工製品」（社名の下の「加工製品」の箱）は 2026-10-16 にやめました（箱をなくした・product_tree.go）。
	cms.RegisterPageTemplate(cms.PageTemplate{
		Title:     PurchaseOrderTemplate,
		Extension: "toho",
		Why:       "発注部材表の「部材発注書ページを作る」が写します。キャプション「部材発注明細表」の表と、見出し「備考」の節が要ります。",
	})
}

type materialsPlugin struct{}

func (materialsPlugin) Name() string { return "materials" }

// Schema / Tables は空です。専用テーブルを持たない③計算だけのプラグインで、
// 読む先は汎用索引（vocab_index プラグインが所有）です。
func (materialsPlugin) Schema() []string { return nil }

func (materialsPlugin) Tables() []string { return nil }

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

// filterVisible は読めないページをスコープから落とします。
//
// **欠けたことは知らせません**（C案の決定）——読めないページ由来の行は黙って
// 落ちます。全員 admin か同一グループの運用では発動しません。
func filterVisible(ids []int, canView func(int) bool) []int {
	out := ids[:0:0]
	for _, id := range ids {
		if canView(id) {
			out = append(out, id)
		}
	}
	return out
}

// RequiredMaterials は指定ページ（受注ページ）に紐づく部材の要手配数・発注済数を
// 集計します。呼ぶのは /api/required-materials（と試験）だけです。
// 2026-09-21 までは計算ビューのサーバー事前描画も共用していましたが、画面の表示は
// 加工製品ごとの集計 `procurementViewHTML`（view_procurement.go）へ替わりました。
// この口を叩くのは `e2e/verify-template.js` だけなので、消すなら当て先を先に替えること。
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

	materialsMap := make(map[string]*RequiredMaterialResponse)

	// 定義元ページの可視判定は1ページにつき1度だけ引く（品番ごとに何度も辿らない）。
	canView := viewCheck(user)

	// 0. 集計のスコープ＝**このページと、参照で直接つながっているページ**
	//    （`cms.RelatedPages`・2026-09-04 の参照追従化）。取り込みで生まれた
	//    受注ページは通信記録ページを `受信元` で指しているので、通信記録の上で
	//    集計しても明細が見えるようになります。読めないページは混ぜません。
	scope, err := cms.RelatedPages(db, pageIDInt)
	if err != nil {
		return nil, err
	}
	scope = filterVisible(scope, canView)
	// ⚠ **「移行中」の受注ページは数えません**（2026-09-29・受注残・必要部材表と同じ線引き）。
	kept := scope[:0]
	for _, id := range scope {
		if !isMigrating(db, id) {
			kept = append(kept, id)
		}
	}
	scope = kept

	// 1. スコープの受注明細表（品番・数量）を索引から読む。
	//    **ページで絞る**のは変わりません——同じ発注書番号を別ページで使っても
	//    混ざらない（硬い表のころ order_no のサブクエリで他ページの明細まで
	//    拾った・設計総点検③）。広がったのは「どのページを見るか」だけです。
	var orderItems []cms.VocabRow
	for _, id := range scope {
		rows, err := cms.VocabTableRowsOf(db, id, clientOrderItemsType)
		if err != nil {
			return nil, err
		}
		orderItems = append(orderItems, rows...)
	}

	// 2. 各受注部品に対し、必要な部材の定義を集めて総必要数を積む。
	//
	//    品番は部材表の中ではなく**ページ全体のタグ**にあるので、
	//    「そのタグを持つページ」を逆引きしてから、そのページの部材表を読みます。
	//    ⚠ 見るタグは設定の照合タグ（`product_code_tags`・図面番号／品番／部品番号）——
	//    2026-09-27 まではレジストリ宣言（part-materials の RequiresTag＝部品番号）1つでしたが、
	//    宣言を外しました（vocab.go）。受注明細表の照合（`productByCode`）と同じ名前を見ます。

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
			for _, defPageID := range pagesByAnyTag(db, ProductCodeTags(), partID) {
				rows, err := cms.VocabTableRowsOf(db, defPageID, partMaterialsType)
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
				// 埋まるのは発注実績が付いたとき——部材発注書のヘッダから仕入先が来ます。
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
	//    ブロック番号は形式ごとの文書順連番なので、1つの部材発注書セクションが
	//    （↑ここまでは 2026-09-18 より前の説明で、文が途中で切れています。いまは下のとおり。）
	//    ⚠ **仕入先はページの可変タグです**（2026-09-18 にヘッダから移した）。
	//    **1文書＝1ページ**が規則なので（ユーザー:「発注書は一ページ一発注書で問題ない」）、
	//    対応づけは**ページ1つ**で閉じます——ヘッダと明細を `block_no` で突き合わせていた
	//    仕掛けは要らなくなりました（文書順に頼る対応づけが1つ消えた）。
	//    スコープは受注明細表と同じ（部材発注書が別ページに在っても拾う）。
	var ourItems []cms.VocabRow
	supplierOf := map[int]string{} // ページ → 仕入先
	for _, id := range scope {
		tags, err := cms.TagsOfPage(db, id)
		if err != nil {
			return nil, err
		}
		if s := cms.FirstTag(tags, SupplierTag); s != "" {
			supplierOf[id] = s
		}
		rows, err := cms.VocabTableRowsOf(db, id, ourOrderItemsType)
		if err != nil {
			return nil, err
		}
		ourItems = append(ourItems, rows...)
	}
	for _, oi := range ourItems {
		// ⚠ **取消の行も手配済みに数えます**（2026-09-23 に覆った・判定と経緯は
		//    order_status.go）。取消は「その部材はもう発注しない」の意味です。
		name := oi.Values["item-name"]
		quantity := cms.VocabQuantity(oi)
		if existing, ok := materialsMap[name]; ok {
			existing.Ordered += quantity
		} else {
			materialsMap[name] = &RequiredMaterialResponse{
				MaterialName:  name,
				SupplierName:  supplierOf[oi.PageID],
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
// ③計算は部材発注明細表の品名と突き合わせるので、ここで1つの文字列に繋ぎます。
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
