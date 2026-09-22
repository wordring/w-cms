package subcon

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
	// **名簿に載る**（起動ログと画面の出し分け。internal/cms/extensions.go）。
	cms.RegisterExtension("subcon", "下請け業務")
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
	// 未手配の一覧（2026-09-21・view_unordered.go）。ここから発注書を1枚作ります。
	cms.RegisterView(UnorderedViewType, unorderedViewHTML)

	// **受注の置き場は管理画面のボタンで作れます**（2026-09-16）。
	// 整理のときにも自動で作られます（`cms.EnsureTopLevelBox`）——先に作れるように
	// したのは、**整理する前に「どこへ行くのか」を人が見られるようにする**ためです。
	// ⚠ **作業面はまだありません**（納期・受注残を見るビューが未実装）。見出しだけの
	// 箱なので、ビューができたらここの本文に足すこと。
	// **加工製品の階層の根**（2026-09-16 にアドレス帳の木と分けた・customer_box.go）。
	cms.RegisterRequiredPage(cms.RequiredPage{
		Title:     CustomerBoxTitle,
		Extension: "subcon",
		Why:       "加工製品の階層の根です（社名／段／装置名称／図面名称）。整理を実行すると、通信記録の下にできた加工製品ページがここへ移ります。相手の連絡先は「連絡帳」のほうです。",
		Body:      customerBoxBody,
	})
	cms.RegisterRequiredPage(cms.RequiredPage{
		Title:     OrderBoxTitle,
		Extension: "subcon",
		Why:       "受注ページの置き場です（受注／年／月。年月は発注日）。整理を実行すると通信箱からここへ移ります。",
	})
	// ⚠ **弊社の発注書は受注ページの下に置けません**（2026-09-21 ユーザー訂正）
	// ——発注は**納期のグループなどから**発行され、**受注明細の単位とは無関係**なので、
	// 1枚が複数の受注にまたがります。結びは参照で作ります（filing_order.go の定数）。
	cms.RegisterRequiredPage(cms.RequiredPage{
		Title:     PurchaseOrderBoxTitle,
		Extension: "subcon",
		Why:       "弊社が出す発注書の置き場です（発注／年／月。年月は発注日）。⚠ 受注ページの下には置けません——発注は納期のグループなどから発行され、受注明細の単位とは無関係だからです。受注との結びは参照で作ります。未手配の一覧（発注書を作る画面）もここに出ます。",
		Body:      purchaseOrderBoxBody,
	})
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
		// PDF解析（下請け業務）。main.go への直書きをやめてここへ寄せた
		// ——ルートも拡張と一緒に外れる（`-tags minimal` で消える）。
		{Pattern: "/api/analyze-attachment", Handler: AnalyzeAttachmentAPIHandler},
		// PDFから明細を読み、開いているブロックへ差し込む（2026-09-16 にコアから移設）。
		// ⚠ 上の `/api/analyze-attachment` とは**別の仕事**です——あちらは受注ページを
		// 1枚作り、こちらは人が開いているブロックの中へ明細を入れるだけ（parse_pdf.go）。
		{Pattern: "/api/parse-pdf", Handler: ParsePDFHandler},
		// 加工製品ページの整理（提案を出す口と、実行する口）。**提案は何も作りません**
		// ——顧客名・装置名称のページが生まれるのは実行のときだけ（filing.go）。
		{Pattern: "/api/filing-proposal", Handler: FilingProposalAPIHandler},
		// 整理の欄を打ち替えるたびに「その行き先にページがあるか」を聞く口（2026-09-20）。
		{Pattern: "/api/filing-target", Handler: FilingTargetAPIHandler},
		{Pattern: "/api/file-drawings", Handler: FileDrawingsAPIHandler},
		// 解析済みの印（添付ID → 生まれたページ）。読むだけで何も作りません。
		{Pattern: "/api/analyzed", Handler: AnalyzedAPIHandler},
		// 受注残表から受注明細の1セルを書き換える口（2026-09-21・order_edit.go）。
		// ⚠ **計算ビューで初めての「書ける鏡」**です——押した先が**別のページの
		// 本文**になるので、行番号と `品番` の2つで照合し、いまの値とも突き合わせ、
		// 編集ロックの関門を通します。
		{Pattern: "/api/order-item", Handler: OrderItemEditAPIHandler},
		// 発注書のPDFを作って、そのページの添付として残す口（2026-09-21・order_pdf.go）。
		// ⚠ **7年保存がこれで済みます**——ファイルがページと一緒に残り、版も監査も付きます。
		{Pattern: "/api/order-pdf", Handler: OrderPDFAPIHandler},
		// 材質・形状・寸法で材料を探す口（2026-09-21・dimension_search.go）。
		// ⚠ **読めないページは混ぜません**——材料の表には誰でも行を書けるので、
		// 絞らないと読めない発注書の単価と仕入先が引けてしまいます。
		{Pattern: "/api/material-search", Handler: MaterialSearchAPIHandler},
		// 未手配の一覧から発注書を1枚作る口（2026-09-21・our_order_new.go）。
		// ⚠ **仕入先が無ければ作りません**——1枚＝1社が発注書の単位です。
		{Pattern: "/api/our-order/new", Handler: NewOurOrderAPIHandler},
		// 未発注の表から**発注部材表**を1つ作る口（2026-09-22・order_draft.go）。
		// ⚠ **行が0でも作ります**——空の表から始められることが、加工製品ページに
		// 無い部材（消耗品・治具）だけを買うときの道です。
		{Pattern: "/api/our-order/draft", Handler: NewOrderDraftAPIHandler},
		// 発注部材表から1行を外す口（2026-09-22・order_draft.go）。
		// ⚠ **「戻す」は「外す」です**——一覧は毎回計算される鏡なので、
		// ここから消せば**自動的に一覧へ戻ってきます**。
		{Pattern: "/api/our-order/draft/remove", Handler: RemoveOrderDraftRowAPIHandler},
		// 発注明細の1行の `状態` を書き換える口（2026-09-22・order_status.go）。
		// ⚠ **発注済みの印は行に付きます**（ユーザー:「発注書の表の**一品ずつに
		// 発注済みの印**をつけます」）——1枚の紙の中でも、**1品だけ取り消す**・
		// **1品だけ先に納まる**が起こるためです。
		{Pattern: "/api/our-order/line-status", Handler: OrderLineStatusAPIHandler},
		// 発注書を出したことを反映する口（2026-09-22・order_send.go）。
		// ⚠ **取消でない行すべて**に `発注済` を付けます。メールは送信の成功が
		// その事実、FAX・手渡しは**人が押したこと**がその事実です。
		{Pattern: "/api/our-order/sent", Handler: OrderSentAPIHandler},
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

	// 1. スコープの受注明細（品番・数量）を索引から読む。
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
	//    部品番号は部材表の中ではなく**ページ全体のタグ**にあるので、
	//    「そのタグを持つページ」を逆引きしてから、そのページの部材表を読みます。
	//    鍵の名前はレジストリ宣言（part-materials の RequiresTag）が持つ——ここへ
	//    直書きすると、見出しを改名したときに告知する側と読む側がずれる（設計総点検⑤）。
	materialsDef, _ := cms.VocabDefByType(partMaterialsType)
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
	//    ⚠ **仕入先はページの可変タグです**（2026-09-18 にヘッダから移した）。
	//    **1文書＝1ページ**が規則なので（ユーザー:「発注書は一ページ一発注書で問題ない」）、
	//    対応づけは**ページ1つ**で閉じます——ヘッダと明細を `block_no` で突き合わせていた
	//    仕掛けは要らなくなりました（文書順に頼る対応づけが1つ消えた）。
	//    スコープは受注明細と同じ（発注書が別ページに在っても拾う）。
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
		// ⚠ **取消の行は手配済みに数えません**（2026-09-22 の決定・判定は order_status.go）。
		//    この古い集計だけが素通ししていた（2026-09-23 に発見）——数えると、取り消した
		//    材料が「手配済み」に見えたまま納期が来ます。
		if orderLineCancelled(oi.Values["status"]) {
			continue
		}
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

// purchaseOrderBoxBody は置き場「発注」の初期本文です。
//
// ⚠ **未手配の一覧をここに置きます**（2026-09-22 ユーザー決定）——**発注の作業をする
// 場所と、発注書が溜まる場所を同じ**にします（受注ページに受注残表を置くのと同じ流儀）。
// ⚠ **一覧は鏡です**（受注明細と発注明細から毎回計算する）ので、**どこに置いても中身は
// 同じ**です。変わるのは「どこから始めるか」だけ——だから置き場所は**作業の動線**で決めます。
//
// ⚠ **既にある「発注」ページには、これは入りません**——箱の本文は作られた時点のもので、
// あとからビューを足しても現れません（2026-09-16 に実データで踏んだ）。**手で貼ること。**
func purchaseOrderBoxBody() string {
	return "<h1>" + PurchaseOrderBoxTitle + "</h1>" +
		"<p>弊社が出す発注書の置き場です（発注／年／月。年月は発注日）。</p>" +
		"<p>下の一覧から行を選び、仕入先と差出人を決めると、発注書が1枚できます。" +
		"⚠ 発注書は<strong>1枚に1社</strong>です。</p>" +
		`<section data-type="` + UnorderedViewType + `"></section>`
}
