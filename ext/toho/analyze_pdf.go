package toho

// ─────────────────────────────────────────────────────────────────────────
// PDFの判定→受注ページ生成——ボタン起動（東邦の業務・2026-09-01）
//
// 添付PDFを Gemini で判定し、**顧客が発行した発注書**なら受注ページ（2026-10-19 からの名前は案件ページ）を
// そのページの子として生成します。図面なら加工製品ページ、業者の見積の返事なら返事ページです。
//
// ⚠ **ZIP の中の PDF は、ここでは扱いません**（2026-09-17）。メールの取り込みが
// ZIP を展開して中身を1つずつ添付にするので（`ext/comm/intake_eml.go`）、解析が見る
// のは常に「自分のブロックIDを持つ PDF」です。それまでは `entry` 引数で ZIP の中の
// 1件を取り出し、`元ファイル`・`対応DXFファイル` のタグで中のパスを添えていましたが、
// 参照が ZIP を指すのでファイル表示が PDF を開けませんでした。
//
//	通信記録ページ（親）── 📎 発注書.pdf
//	└─ 案件ページ（本APIが生成・2026-10-19 までの名前は受注ページ）
//	     <h1>【顧客】発注書 PO-xxx</h1>
//	     <dl data-type="tags"> 発注書番号・発注元・発注日・納期・小計・税・合計・
//	                          受信元: <ページID>-<添付ID>（押すと該当ブロックへ飛ぶ）
//	     <details> 読んだままの表 ＋ <table><caption>案件明細表</caption> 弊社の明細
//	     （09-18 まではヘッダを機能見出しの節の素の dl に書いていた——`buildOrderPageHTML`）
//
// **起動は人の指先だけ**——「自動ではなくボタンのclickなどで解析が始まると良い」
// （2026-09-01 ユーザー決定）。当初は .eml 到着時の自動判定（取り込み観察係）として
// 作ったが同日ボタン起動へ一本化した。§3 の全体方針「人間ゲート型の取り込み」への
// 回帰であり、Gemini の呼び出し（コスト・誤判定）は常に人の操作の直後にだけ起きる。
// 誤生成の取り消しはページ削除（§2.7④ 可逆性——通信記録は不変で残っている）。
//
// このファイルは他社デプロイでは外す・差し替える前提の既定セットです
// （docs/考察/【考察】通信記録処理.md §3.2。Gemini はコア側インフラ gemini.go、
// プロンプト＝解釈はこのセットの持ち物）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/google/generative-ai-go/genai"

	"w-cms/ext/comm"
	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// orderJudgment は Gemini の判定＋抽出結果です（プロンプトと同じ形）。
type orderJudgment struct {
	IsClientOrder bool `json:"is_client_order"`
	// **図面PDFの枝**（2026-09-03）——添付DXFとの突き合わせに使う。
	// PDFが図面なら DocType が "drawing" になり、図面番号・図面名称が入る。
	DocType     string `json:"doc_type"`
	DrawingNo   string `json:"drawing_no"`
	DrawingName string `json:"drawing_name"`
	// 装置名称は**ページの置き場所**に効く——ワンノートの加工製品ページは
	// 「顧客名／装置名称／図面名称」の階層で作られている（2026-09-03 ユーザー）。
	// 顧客名は Customer を共用する（発注書の発行元と同じ「相手の会社名」）。
	MachineName string `json:"machine_name"`
	OrderNo     string `json:"order_no"`
	Customer    string `json:"customer"`
	OrderDate   string `json:"order_date"`
	// DueDate は納期です。⚠ **明細ではなくヘッダにあります**（実データで確認）。
	DueDate string `json:"due_date"`
	// 小計・消費税・合計金額です。⚠ **検算に使います**（[checksum.go]）。
	//
	// ⚠ **書かれているまま受け取ります——Gemini に計算させません。** 自分の読みを
	// 自分で検算させると、**数字を合うように書き直します**。誤りが消えるのではなく、
	// 見えなくなる——検算の意味がそこで失われます。
	Subtotal string `json:"subtotal"`
	Tax      string `json:"tax"`
	Total    string `json:"total"`
	// SourceTable は**発注書の明細表を、先方の見出しのまま**写したものです
	// （2026-09-20 ユーザー:「発注書の見出しを**あるがままに表にしたものを原本**として、
	// 弊社仕様に修正した表を作ると良いと思います」）。
	//
	// ⚠ **これが無いと、言い換えたことが見えません。** 実データでは先方の `図面番号` を
	// 弊社の `品番` に入れてきました——結果としては正しいのですが、**黙って言い換えて
	// いた**ので、原本と見比べない限り気づけませんでした。
	//
	// ⚠ **OCRの正しさもここでしか確かめられません**——行を1つ落としていないか、
	// 数字を取り違えていないか。対応だけ見せる形では分かりません。
	//
	// ⚠ **生で受けて、別に解きます。** 構造に直接当てると、**形が合わないだけで
	// 解析まるごとが失敗します**——実データの2通目で起きました（2026-09-20）。
	// 原本の写しは**おまけ**で、それが読めなくても発注書番号や明細は取れているべきです。
	SourceTableRaw json.RawMessage `json:"source_table"`

	// SourceTable は上を解いた結果です（解けなければ空のまま）。
	SourceTable orderSourceTable `json:"-"`
	Items       []orderPDFItem   `json:"items"`
	// Drawings は**1つのPDFに複数の図面が入っていたとき**の2枚目以降を含む一覧です
	// （2026-09-20 ユーザー:「一つのPDFに複数の図面が入っている場合もあるようです」）。
	//
	// ⚠ **それまでは1枚ぶんしか返せませんでした**（`drawing_no` が単数）。3枚入った
	// PDFを出しても Gemini は1枚ぶんだけ返し、**残りはどこにも記録されません**——
	// エラーも出ないので、気づくのは後から「あの図面どこ？」となったときです。
	//
	// **1枚につき1ページ作ります**（ユーザー決定:「3ページ作って人が整理で1ページに
	// まとめるほうが良いと思います。**一つのPDFに複数の製造製品が入っている場合が
	// あるからです**」）。最初から1ページにまとめると、別々の品物だったときに
	// **人が切り離せません**——いまは分ける操作がないので、まとめるのは人の判断で。
	Drawings []drawingJudgment `json:"drawings"`
	// Orders は**1つのPDFに発注書が何枚も入っていたとき**の発注書ごとの一覧です（2026-09-30 利用者:「一枚のPDFに複数の
	// 発注書が入っています。Geminiは複数のPDFを一枚のPDFのように返答したようです。プロンプトで複数枚の発注書が入っている
	// ことを考慮して、返答も発注書ごとに配列のように渡していただいてはどうでしょう」）。
	//
	// ⚠ **それまでは1枚ぶんの項目しか無く**、2枚の発注書の明細が1つの受注ページに混ざり、検算も合わなかった（実データの
	// 2024-11 の注文書）。**1枚につき1ページ**作ります——発注書番号も発注日も小計も、紙ごとに違うからです。
	// 各要素は上の発注書の項目（order_no・customer・…・items）を持ちます。
	Orders []orderJudgment `json:"orders"`
	// RFQReply は**業者の部材見積依頼の返事**と判定したときの中身です（2026-10-05・rfq_reply_page.go——doc_type が "rfq_reply"）。
	// 利用者:「見積依頼の返事は通信記録に入るので、解析ボタンを押して解析し、見積依頼の返事とわかれば、見積依頼の返事ページを作り」。
	RFQReply *rfqReplyJudgment `json:"rfq_reply"`
}

// orderList は判定結果を**発注書1枚ずつ**に並べ直します（`drawingList` と同じ形）。
//
// ⚠ **古い形（上の単数の項目）も読めます**——`orders` が無い応答や、試験が単数で組んだ判定は1枚として扱います。
// 客先が紙ごとに空なら、上の客先で埋めます（1通の発行元は同じ）。
func (j *orderJudgment) orderList() []*orderJudgment {
	if len(j.Orders) == 0 {
		return []*orderJudgment{j}
	}
	out := make([]*orderJudgment, 0, len(j.Orders))
	for i := range j.Orders {
		o := &j.Orders[i]
		o.IsClientOrder, o.DocType = true, "order"
		if strings.TrimSpace(o.Customer) == "" {
			o.Customer = j.Customer
		}
		out = append(out, o)
	}
	return out
}

// drawingJudgment は図面1枚ぶんです（`orderJudgment` の図面の枝と同じ項目）。
type drawingJudgment struct {
	DrawingNo   string `json:"drawing_no"`
	DrawingName string `json:"drawing_name"`
	MachineName string `json:"machine_name"`
	Customer    string `json:"customer"`
}

// drawingList は判定結果を**図面1枚ずつ**に並べ直します。
//
// ⚠ **古い形（単数の `drawing_no`）も読めます。** Gemini が `drawings` を返さなかった
// ときや、試験が単数で組み立てた判定でも、これまでどおり1枚として扱います——
// **応答の形が変わっただけで解析が止まる**のは避けます。
func (j *orderJudgment) drawingList() []drawingJudgment {
	if len(j.Drawings) > 0 {
		return j.Drawings
	}
	if strings.TrimSpace(j.DrawingNo) == "" && strings.TrimSpace(j.DrawingName) == "" {
		return nil
	}
	return []drawingJudgment{{
		DrawingNo: j.DrawingNo, DrawingName: j.DrawingName,
		MachineName: j.MachineName, Customer: j.Customer,
	}}
}

// asJudgment は図面1枚を、ページを組む関数が受け取る形へ戻します。
func (d drawingJudgment) asJudgment() *orderJudgment {
	return &orderJudgment{
		DocType: "drawing", DrawingNo: d.DrawingNo, DrawingName: d.DrawingName,
		MachineName: d.MachineName, Customer: d.Customer,
	}
}

// orderSourceTable は発注書の明細表の写しです（見出しと行を、読めたまま）。
type orderSourceTable struct {
	Headers []string   `json:"headers"`
	Rows    [][]string `json:"rows"`
}

type orderPDFItem struct {
	ItemNo string `json:"item_no"`
	// ItemNoSource は `品番` を**どの列から採ったか**です（先方の見出しの文字のまま）。
	//
	// ⚠ **「言い換えたこと」を見せるための欄**です（2026-09-21 ユーザー:「品番には
	// 入りそうなものを入れます。…そうとも限りません。これはGeminiに尋ねては
	// どうでしょう？」）。品番を持たず**図番を品番として送ってくる客先**があり、
	// 一方で図番とは別に品番を持つ客先もあります——**どの列が品番なのかは、
	// 書面を読まないと決まりません**。だから機械に選ばせ、選んだ根拠を返させます。
	//
	// ⚠ **これが無いと、言い換えたことが値からは分かりません**（0c の積み残し）。
	// 原本の写しと突き合わせれば人には分かりますが、**毎回見比べる人はいません**。
	ItemNoSource string `json:"item_no_source"`
	ItemName     string `json:"item_name"`
	Price        string `json:"price"`
	Quantity     string `json:"quantity"`
	// Unit は数量の単位です（`個`・`セット` など）。⚠ **落とすと数量の意味が
	// 変わります**——`100` が100個なのか100セットなのか分からなくなり、
	// **まれにしか出ないセットの行だけが黙って間違います**（2026-09-20）。
	Unit string `json:"unit"`
}

// judgeOrderPDF は判定の入口です。テストが偽物へ差し替えられるよう変数にしてあります
// （Gemini はネットワークと課金を伴うため、自動テストでは呼ばない）。
var judgeOrderPDF = judgeOrderPDFWithGemini

// AnalyzeAttachmentAPIHandler は POST /api/analyze-attachment です。
// 入力: {page_id, file}——file は添付の保存名（.pdf）。
func AnalyzeAttachmentAPIHandler(w http.ResponseWriter, r *http.Request) {
	if _, ok := cms.GateJSONPost(w, r); !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		File   string `json:"file"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, ok := cms.PageIDOrFail(w, req.PageID)
	if !ok {
		return
	}
	// 子ページを作る操作なので write 権限を要求する（本文は変えないので編集ロックは不要
	// ——取り込みと同じ理屈）。
	if !page.RequirePageWrite(w, r, pageID) {
		return
	}
	// 表計算（.xlsx）も受けます（2026-09-30・Excel の注文リスト——文字にして判定する・judgeOrderSheet）。
	fileName, err := cms.SafeAttachmentName(pageID, req.File,
		map[string]bool{".pdf": true, ".xlsx": true}, "解析できるのは .pdf と .xlsx だけです")
	if err != nil {
		cms.JSONFail(w, http.StatusBadRequest, err.Error())
		return
	}

	pdf, err := loadPDFForAnalysis(pageID, fileName)
	if err != nil {
		cms.JSONFail(w, http.StatusBadRequest, err.Error())
		return
	}

	// ── 作るページのテンプレートを**先に**引きます（2026-09-27）──
	//
	// 受注ページも加工製品ページも**テンプレートを写して**作ります（テンプレートが無ければ
	// 作らない）。どちらになるかは Gemini の答えで決まりますが、**聞く前に両方を確かめます**
	// ——聞いたあとで「テンプレートが無い」と分かると、有料の問い合わせが無駄になります。
	orderTmpl, err := cms.PageTemplateBody(OrderPageTemplate)
	if err == nil {
		var productTmpl string
		if productTmpl, err = cms.PageTemplateBody(ProductTemplate); err == nil {
			analyzeWithTemplates(w, r, pageID, fileName, pdf, orderTmpl, productTmpl)
			return
		}
	}
	cms.JSONFail(w, http.StatusConflict, "解析の結果を書くテンプレートがありません: "+err.Error())
}

// analyzeWithTemplates は、テンプレートを確かめたあとの解析の本体です。
func analyzeWithTemplates(w http.ResponseWriter, r *http.Request, pageID, fileName string,
	pdf []byte, orderTmpl, productTmpl string) {
	var j *orderJudgment
	var err error
	if strings.EqualFold(filepath.Ext(fileName), ".xlsx") {
		text, terr := cms.XLSXText(pdf)
		if terr != nil {
			cms.JSONFail(w, http.StatusBadRequest, terr.Error())
			return
		}
		j, err = judgeOrderSheet(sheetMailContext(pageID) + text)
	} else {
		j, err = judgeOrderPDF(pdf)
	}
	if err != nil {
		code, msg := aiFailure(err, "解析に失敗しました")
		cms.JSONFail(w, code, msg)
		return
	}
	attachID := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	user := auth.CurrentUser(r)

	// ── 社名は**書く前に**揃えます（2026-09-21 ユーザー決定）──
	//
	// ユーザー:「発注元タグに株式会社が入るのが気になります」。それまでは連絡帳に
	// 候補が見つからないと**読んだ名前をそのまま**書いていたので、⚠ **新しい客先の
	// 1通目では一度も揃いませんでした**——`株式会社○○` がそのままタグになります。
	//
	// 揃えるのは `contacts.OrgNameForPage` の1つの口です（連絡帳の登録・受注の整理・
	// 加工製品の整理と同じ）。候補が居ればその実物の題、2つ以上に割れていれば読んだ
	// まま、居なければ法人格を落とした形。⚠ **生の社名は原本の写しに残ります**
	// （「顧客の発注書（読んだまま）」）ので、食い違えば人が見比べられます。
	//
	// ⚠ **図面の枝も同じ値を使います**——`客先` は置き場所（製造台帳／社名／装置名称）に
	// 効くので、ここで揃えないと**受注ページと加工製品ページで社名が食い違います**。
	j.Customer = contacts.OrgNameForPage(user, j.Customer)
	for i := range j.Drawings {
		j.Drawings[i].Customer = contacts.OrgNameForPage(user, j.Drawings[i].Customer)
	}
	for i := range j.Orders {
		j.Orders[i].Customer = contacts.OrgNameForPage(user, j.Orders[i].Customer)
	}

	// **部材見積依頼の返事の枝**（2026-10-05・rfq_reply_page.go）——返事ページを記録の子に作り、どの部材見積依頼書への返事かを
	// 突き合わせる（候補が1つなら結んで部材見積依頼書の子へ移す）。テンプレートは判定のあとで引く——受注・図面の解析を、返事の
	// テンプレートが無い環境でも止めないため（無ければ返事と判定した時点で断る）。
	if j.DocType == "rfq_reply" && j.RFQReply != nil {
		m, err := makeRFQReply(user, pageID, attachID, j.RFQReply)
		if err != nil {
			var me *comm.MakeError
			if errors.As(err, &me) {
				cms.JSONFail(w, me.Status, me.Message)
			} else {
				cms.JSONFail(w, http.StatusInternalServerError, "部材見積依頼の返事ページを作れません: "+err.Error())
			}
			return
		}
		json.NewEncoder(w).Encode(map[string]any{
			"success": true, "is_client_order": false, "doc_type": "rfq_reply",
			"page_id": m.PageID, "title": pageTitleOf(m.PageID), "linked_rfq": m.Linked, "candidates": m.Candidates, "say": rfqReplySay(m),
		})
		return
	}

	// **図面PDFの枝**——同じページに付いているDXFと図面番号で突き合わせ、
	// 同じ部品の図面として1枚の加工製品ページにまとめる（drawing_match.go）。
	if !j.IsClientOrder && j.DocType == "drawing" {
		answerDrawings(w, user, pageID, fileName, attachID, productTmpl, j)
		return
	}
	if !j.IsClientOrder {
		// 生成しないのも正常な結果——人が押した問いに「発注書ではない」と答える。
		json.NewEncoder(w).Encode(map[string]any{"success": true, "is_client_order": false})
		return
	}

	answerOrders(w, user, pageID, fileName, attachID, orderTmpl, j)
}

// answerDrawings は図面と判定した PDF から、図面1枚につき1枚の加工製品ページを記録の子に作って答えます
// （2026-10-20 に analyzeWithTemplates から分けた）。
func answerDrawings(w http.ResponseWriter, user *auth.User, pageID, fileName, attachID, productTmpl string, j *orderJudgment) {
	// ⚠ **1枚につき1ページ作ります**（2026-09-20 ユーザー決定）。1つのPDFに
	// 複数の図面が入っていることがあり、しかも**別々の製造製品**かもしれません
	// ——最初から1ページにまとめると、別物だったときに人が切り離せません
	// （分ける操作がない）。まとめるのは整理で人が決めます
	// （「二つ目の図面として追加」）。
	drawings := j.drawingList()
	if len(drawings) == 0 {
		// 図面と判定したのに1枚も取れなかった——**黙って0ページで終わらない**。
		cms.JSONFail(w, http.StatusBadGateway,
			"図面と判定しましたが、図面番号も図面名称も読み取れませんでした")
		return
	}
	// ⚠ **本文を全部組んでから作り始めます**——テンプレートに図面ブロック・改訂明細表が
	// 無ければ、1枚も作らずに断ります（途中まで作って止まると、人が片付けることになる）。
	totalMatched := 0
	bodies := make([]string, 0, len(drawings))
	for _, d := range drawings {
		dj := d.asJudgment()
		matches := MatchDXFAttachments(pageID, dj.DrawingNo)
		totalMatched += len(matches)
		body, err := buildProductPageHTML(productTmpl, pageID, attachID, dj, matches)
		if err != nil {
			cms.JSONFail(w, http.StatusConflict, "加工製品ページを作れません: "+err.Error())
			return
		}
		bodies = append(bodies, body)
	}
	made, ok := createAnalyzedPages(w, user, pageID, bodies, "加工製品ページ", func(newID string) {
		auth.Audit(user.Username, "analyze-drawing", newID+" from "+pageID+"/"+fileName)
	})
	if !ok {
		return
	}
	out := map[string]any{
		"success": true, "is_client_order": false, "doc_type": "drawing",
		"pages": made, "matched_dxf": totalMatched,
	}
	// **1枚のときは今までどおりの形も返します**——画面が `page_id` を読んで
	// いるので、応答の形を変えただけで表示が壊れないように。
	if len(made) == 1 {
		out["page_id"] = made[0]["page_id"]
		out["title"] = made[0]["title"]
	}
	json.NewEncoder(w).Encode(out)
}

// answerOrders は顧客の発注書と判定したものから、発注書1枚につき1枚の案件ページを記録の子に作って答えます
// （2026-10-20 に analyzeWithTemplates から分けた）。
func answerOrders(w http.ResponseWriter, user *auth.User, pageID, fileName, attachID, orderTmpl string, j *orderJudgment) {
	// ⚠ **発注書1枚につき1ページ**（2026-09-30・`orderList`）。図面の枝と同じく、**本文を全部組んでから作り始めます**
	// ——テンプレートに足りない印があれば1枚も作らずに断る（途中まで作って止まると、人が片付けることになる）。
	sheets := j.orderList()
	bodies := make([]string, 0, len(sheets))
	for _, o := range sheets {
		body, err := buildOrderPageHTML(orderTmpl, pageID, attachID, o)
		if err != nil {
			cms.JSONFail(w, http.StatusConflict, "案件ページを作れません: "+err.Error())
			return
		}
		bodies = append(bodies, body)
	}
	linked := 0
	made, ok := createAnalyzedPages(w, user, pageID, bodies, "案件ページ", func(newID string) {
		auth.Audit(user.Username, "analyze-pdf", newID+" from "+pageID+"/"+fileName)
		// ⚠ **作った直後に、いま在る加工製品ページと結びます**（2026-09-21・[link_item.go]）。
		// **図面が先に届いていた場合、ここで結ばないと一度も埋まりません**——図面の整理は
		// もう終わっているからです。そして**返り注文は必ずこの順**です。
		//
		// ⚠ **解析の失敗にはしません**——結べなくても受注ページは正しく作れています。
		// 埋まった行数は画面へ返し、人が「何が起きたか」を見られるようにします。
		linked += LinkProductsToOrder(user, newID)
	})
	if !ok {
		return
	}
	// **1枚のときは今までどおりの形**（`page_id`・`title`）——画面がそれを読んでいる。2枚以上は `pages` も。
	out := map[string]any{
		"success": true, "is_client_order": true,
		"page_id": made[0]["page_id"], "title": made[0]["title"], "linked_items": linked,
	}
	if len(made) > 1 {
		out["pages"] = made
	}
	json.NewEncoder(w).Encode(out)
}

// createAnalyzedPages は、組み上がった本文 bodies を記録 pageID の子に順に作り、作ったページ（page_id・title）を返します
// （図面の枝と案件の枝が写していた——2026-10-20 に寄せた）。after は1枚作るたびに呼ぶ（監査・結び）。
// ⚠ **途中で失敗しても、できたぶんは残します**——作れたページまで捨てると、人はもう一度解析するしかなくなり、
// **通ったぶんが二重にできます**。何枚目まで作れたかを断りに入れて、人に見てもらいます（断りを書いて false）。
func createAnalyzedPages(w http.ResponseWriter, user *auth.User, pageID string, bodies []string, what string, after func(newID string)) ([]map[string]any, bool) {
	made := []map[string]any{}
	for _, body := range bodies {
		newID, err := cms.CreateChildPage(pageID, user.Username, body)
		if err != nil {
			cms.JSONFail(w, http.StatusInternalServerError,
				what+"を作れません（"+strconv.Itoa(len(made))+"枚目まで作成済み）: "+err.Error())
			return nil, false
		}
		after(newID)
		made = append(made, map[string]any{"page_id": newID, "title": pageTitleOf(newID)})
	}
	return made, true
}

// loadPDFForAnalysis は解析対象のPDFの中身を読みます。
func loadPDFForAnalysis(pageID, fileName string) ([]byte, error) {
	path, found := page.AttachmentPath(pageID, fileName)
	if !found {
		return nil, errors.New("添付が見つかりません")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("添付を読めません")
	}
	return b, nil
}

// orderJudgePrompt は判定＋抽出の頼み文です（＝解釈。このセットの持ち物で、
// 呼び出しの型——キー・クライアント・フェンス剥がし——はコアの gemini.go）。
//
// ⚠ **ここのJSONの鍵と、Go の構造体のタグは一対一です。** 片方だけ直すと、
// その項目は**エラーにならず、常に空**になります——応答に入っていないだけなので
// `json.Unmarshal` は何も言いません。番人は `TestPromptMentionsEveryJSONKey`。
//
// **関数の外に出してあるのは、その番人が読めるようにするため**です（2026-09-21）。
const orderJudgePrompt = `このPDFが何の文書かを判定し、種類に応じた項目を抽出してください。
判定する種類は次の4つです:
  - "order"     : 顧客（取引先）が当社宛てに発行した発注書（注文書）——当社が売る側
  - "drawing"   : 部品や製品の図面（表題欄に図面番号・図面名称があるもの）
  - "rfq_reply" : 業者（仕入先）から当社への見積・見積依頼への返事——当社が買う側（当社が送った見積依頼書に単価を書き込んで
                  返してきたもの、業者が当社宛てに出した見積書、**図面に数量・単価を書き込んで返してきたもの**）。当社の見積依頼書
                  には「№」で見積依頼番号が刷ってあります
                  ⚠ 図面に「50ヶ @ 2,900」のような**数量と単価の書き込み**（手書きを含む）があれば、表題欄があっても "drawing" では
                  なく "rfq_reply" です（業者が図面を見て値段を答えてきたもの）
                  ⚠ 当社が送った品物の一覧（図面番号・品名・数量など）に、**業者が単価や希望価格の列を書き足して返してきた表**も
                  "rfq_reply" です（№ も宛名も無い、値段の入った表だけのことがあります）
  - "other"     : 上記以外（当社が発行した見積書・請求書・納品書・カタログ・案内など）
次の形式のJSONオブジェクトのみを出力してください（マークダウンのコードブロック修飾は付けない）:
{
  "doc_type": "order" または "drawing" または "rfq_reply" または "other",
  "is_client_order": doc_type が "order" のとき true、それ以外は false,
  "orders": [{
    "order_no": "発注書番号（記載が無ければ空文字）",
    "customer": "発行元（顧客）の会社名（記載が無ければ空文字）",
    "order_date": "発注日を YYYY-MM-DD 形式で（記載が無ければ空文字）",
    "due_date": "納期（明細の行ではなく、書面の上のほうにある納期）。日付なら YYYY-MM-DD 形式、日付でない書き方（「最短」「最短納期」「至急」「都度」など）は**書かれているまま**返してください。記載が無ければ空文字",
    "source_table": {"headers": ["表の見出しを書かれているまま"], "rows": [["1行ぶんの値を書かれているまま"]]},
    "subtotal": "小計（書かれているまま。記載が無ければ空文字）",
    "tax": "消費税額（書かれているまま。記載が無ければ空文字）",
    "total": "合計金額（書かれているまま。記載が無ければ空文字）",
    "items": [{"item_no": "品番（下の規則で選ぶ）", "item_no_source": "item_no を採った列の見出し（先方に書かれている文字のまま。見出しが無い列から採ったときは空文字）", "item_name": "品名", "price": "単価（カンマを除いた数値文字列）", "quantity": "数量（数値文字列）", "unit": "数量の単位（個・セットなど。記載が無ければ空文字）"}]
  }],
  "drawings": [{"drawing_no": "図面番号", "drawing_name": "図面名称", "machine_name": "装置名称", "customer": "客先"}],
  "rfq_reply": doc_type が "rfq_reply" のときだけ {"rfq_no": "当社の見積依頼書の番号（「№」の後ろの数字。読めなければ空文字）", "supplier": "返事をくれた業者の会社名", "date": "返事の日付を YYYY-MM-DD で（無ければ空文字）", "source_table": {"headers": ["見出しを書かれているまま"], "rows": [["1行ぶんの値を書かれているまま"]]}}、それ以外は null
}
⚠ rfq_reply の source_table には、品物の欄（品名・材質・寸法・数量など）と、業者が書き込んだ単価・金額・備考を**書かれているまま**
入れてください。手書きの数字も読んで入れてください。**計算はしないでください**（書かれていない欄は空文字）。
⚠ rfq_reply の表に値段の列が2つ以上あるときは、**業者が答えた値段**の列の見出しの頭に「単価：」を、**当社が示した参考の値段**
（参考価格・ほかの業者の価格・昨年の価格など）の列の見出しの頭に「参考：」を付けてください（例: 「単価：希望価格」「参考：昨年価格」）。
見出しの無い列には、中身から短い見出しを付けてください（例: 色）。表の途中に見出しの行が繰り返されていたら、その行は入れないでください。
⚠ rfq_reply の rfq_no は**当社の見積依頼書の「№」**だけです。業者が自分の見積書に付けた番号（「見積No.」など）は入れないで
ください（空文字にします）。
⚠ **図面への書き込みで答えてきたとき**は、図面1枚を1行にしてください——headers は ["図面番号", "図面名称", "数量", "単価", "備考"]、
各行は [表題欄の図面番号, 表題欄の図面名称, 書き込みの数量（「50ヶ」なら "50"）, 書き込みの単価（「@ 2,900」なら "2,900"）,
そのほかの書き込み（材質の読み替え・加工の範囲・公差の希望など）をまとめて]。supplier は、書き込みに業者名が無ければ空文字に
してください（**表題欄の会社名は図面を描いた客先**なので入れない）。date も、書き込みに日付が無ければ空文字（図面の端の
印刷日時は返事の日付ではありません）。
⚠ 1つのPDFに**複数の発注書**が入っていることがあります（ページごとに別の発注書——発注書番号・発注日・小計が
紙ごとに違う）。その場合は orders に**発注書の枚数だけ**要素を入れ、明細・小計・合計は**その発注書に書かれている
ものだけ**を入れてください（別の発注書の行を混ぜない）。1枚の発注書が複数ページに続いているときは1つの要素です
（発注書番号が同じ・小計が最後のページにある）。発注書が1枚なら要素は1つ、発注書でなければ空配列にします。
⚠ subtotal・tax・total は、**書面に書かれている数字をそのまま**返してください。
**足し算・掛け算をして求めないでください**——書かれていなければ空文字にします。
（こちらで検算に使うので、計算して埋められると食い違いが見えなくなります。）
表の右下などに飛び出して書かれていることがあります。

⚠ item_no（品番）には、**その品物を特定している番号**を入れてください。
見出しが「品番」の列があればそれを使います。無ければ「図番」「図面番号」「部品番号」
「型式」「品目コード」など、**その品物を一意に指している列**から選んでください
——品番を持たず、図番を品番として送ってくる客先が実際にあります。
番号にあたる列がまったく無ければ空文字にします。**品名を入れないでください**
（品名は item_name の持ち物で、同じ名前の別の品物がありえます）。
⚠ 品番は**図面番号のような記号の列**です（英字・数字・ハイフンなどの並び。例: A100-B01-02）。
**見出しが「部品番号」「品番」でも、中身が日本語の名前（装置名・部品の呼び名）なら品番ではありません**
——客先がその欄に装置の名前を書いてくることがあります。そのときは、記号が並んでいる
「図番」「図面番号」などの列を選んでください。
どの列から採ったかを item_no_source に、**先方の見出しに書かれている文字のまま**
返してください——こちらが「言い換えたこと」を人に見せるために使います。

⚠ 1つのPDFに**複数の図面**が入っていることがあります（ページごとに別の図面、
あるいは1ページに部品図と溶接図）。その場合は drawings に**figureの数だけ**要素を入れてください。
図面が1枚だけなら要素は1つ、図面でなければ空配列にします。
drawings の各項目は次のとおりです:
  - drawing_no   : 表題欄の図面番号（記載が無ければ空文字）
  - drawing_name : 図面名称（記載が無ければ空文字）
  - machine_name : その部品が使われる装置・機械の名前（記載が無ければ空文字）
  - customer     : 客先（この図面の発注元の会社名。記載が無ければ空文字）
図面番号は突き合わせに使うので、**表題欄に書かれている文字列をそのまま**返してください
（ハイフンや記号を補ったり省いたりしない）。該当しない項目は空でかまいません。`

// judgeOrderPDFWithGemini は判定＋抽出を1コールで行います。
// プロンプト（＝解釈）は本セットの持ち物、呼び出しの型（キー・クライアント・
// フェンス剥がし）はコア（gemini.go）。
func judgeOrderPDFWithGemini(pdf []byte) (*orderJudgment, error) {
	respText, err := cms.GeminiGenerate(orderJudgePrompt, genai.Blob{MIMEType: "application/pdf", Data: pdf})
	if err != nil {
		return nil, err
	}
	return parseOrderJudgment(respText)
}

// judgeOrderSheet は表計算（.xlsx）を文字にしたものの判定の入口です（2026-09-30・試験が差し替えられるよう変数）。
//
// 利用者:「Excelの注文リストは法的に無効ですが、顧客との信頼関係で取り扱う場合があります。ハッキリと受注したと
// わかる場合は、受注ページにして良いと思います」。Gemini は .xlsx を受けないので、コアの `cms.XLSXText` で文字にして渡す。
// ⚠ **受注かどうかを決めるのは人**です——押すのは「ハッキリと受注した」と分かった表計算だけ（解析は人が押したときだけ）。
var judgeOrderSheet = judgeOrderSheetWithGemini

// sheetPreamble は表計算を渡すときに頼み文の頭へ足す断りです。
const sheetPreamble = `（ここで渡すのはPDFではなく、表計算（Excel）のファイルを文字にしたものです。「=== シート 名前」がシートの区切りで、
各行は「列=値」を | で区切っています。下の「このPDF」はこの表計算のことと読んでください。
1台あたりの個数と、今回作る数（「今回製作個数」「注文数」など）の両方があるときは、**今回作る数**を数量にしてください。
表が見出しで区切られていくつもあるときも、1つの注文なら orders の要素は1つにして、明細を全部入れてください。）

`

// sheetMailContext は、表計算が**届いたメールの記録の添付**なら、そのメールの件名・差出人・日付・本文を、判定に添える前書きに
// します（2026-10-19）。メールの記録でなければ空です。
//
// 利用者（2026-10-19）:「…は見積回答ですが、xlsxの解析で発注書・図面・部材見積依頼の回答のいずれでもないと出ます。単なる単価の
// 入った表に過ぎないからかもしれませんが、ここから見積もりの返答を作りたいです」——表計算は表しか持たないので、何の表か（客先の
// 注文か・業者の見積の返事か）を、メールの文脈で判断させる。返事のメール（`親ページID` の先がこちらの送ったメール）なら、
// 送ったメールも添える（こちらが図面と品物の一覧を送って値段を頼んだ、と読めるように）。
func sheetMailContext(pageID string) string {
	id, err := strconv.Atoi(pageID)
	if err != nil {
		return ""
	}
	tags, err := cms.TagsOfPage(database.DB, id)
	if err != nil || cms.FirstTag(tags, comm.DirectionTag) != comm.DirectionIn {
		return ""
	}
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		return ""
	}
	out := "（この表計算は、当社に届いた次のメールの添付です。何の表かは、メールの文脈も見て判断してください。）\n" +
		mailForJudgment(id, cms.PageTitleByID(id), body) + "\n"
	if sent, ok := sentMailOf(id); ok {
		if sbody, err := cms.ReadPageBody(page.FormatID(sent)); err == nil {
			out += "\n（このメールは、当社が送った次のメールへの返信です。）\n" + mailForJudgment(sent, cms.PageTitleByID(sent), sbody) + "\n"
		}
	}
	return out + "\n（ここから表計算です。）\n"
}

// sentMailOf は、記録 recordID が返信なら、その親（`親ページID`）の**こちらが送ったメールの控え**を返します（親が受信なら false）。
func sentMailOf(recordID int) (int, bool) {
	parent, err := strconv.Atoi(strings.TrimSpace(cms.PageTagValue(database.DB, recordID, comm.ParentPageTag)))
	if err != nil || parent <= 0 {
		return 0, false
	}
	if cms.PageTagValue(database.DB, parent, comm.DirectionTag) != comm.DirectionOut {
		return 0, false
	}
	return parent, true
}

func judgeOrderSheetWithGemini(text string) (*orderJudgment, error) {
	respText, err := cms.GeminiGenerateBlobs(sheetPreamble+orderJudgePrompt,
		genai.Blob{MIMEType: "text/plain", Data: []byte(text)})
	if err != nil {
		return nil, err
	}
	return parseOrderJudgment(respText)
}

// parseOrderJudgment は Gemini の返答を判定へ読み直します。
//
// ⚠ **Gemini を呼ばずに試せるように切り出してあります**（2026-09-20）。実データで
// 「解析に失敗しました」が出たとき、**読み取りだけを手元で再現できませんでした**
// ——返答を貼れば同じ道を通るようにしておきます。
func parseOrderJudgment(respText string) (*orderJudgment, error) {
	var j orderJudgment
	if err := json.Unmarshal([]byte(cms.StripJSONFence(respText)), &j); err != nil {
		// ⚠ **返ってきたものの頭を添えます**（2026-09-20）。それまでは Go の
		// 型エラーだけを返していて、**何が返ったのか分からないまま**でした
		// ——実データで「解析に失敗しました」と出たとき、原因を当てられませんでした。
		// 直すのはたいていプロンプトなので、**何が返ったかが唯一の手掛かり**です。
		// ⚠ **文字の途中で切らないこと**——和文は1文字が3バイトなので、
		// バイト数で切ると画面に化けた文字が出ます（`head[:300]` でやりかけました）。
		head := strings.TrimSpace(cms.StripJSONFence(respText))
		if r := []rune(head); len(r) > 300 {
			head = string(r[:300]) + "…"
		}
		return nil, errors.New("応答をJSONとして読めません: " + err.Error() +
			"（返ってきたもの: " + head + "）")
	}
	// ⚠ **原本の写しは、読めなくても先へ進みます**（おまけだから）。
	j.SourceTable = parseSourceTable(j.SourceTableRaw)
	// ⚠ 品番に名前の列を選んでいたら、記号の列へ替えます（item_no_column.go・2026-09-29）。
	fixItemNoColumn(&j)
	// 発注書ごとの要素も同じように（2026-09-30・1つのPDFに発注書が何枚も入っているとき）。
	for i := range j.Orders {
		j.Orders[i].SourceTable = parseSourceTable(j.Orders[i].SourceTableRaw)
		fixItemNoColumn(&j.Orders[i])
	}
	if j.RFQReply != nil {
		j.RFQReply.SourceTable = parseSourceTable(j.RFQReply.SourceTableRaw)
	}
	return &j, nil
}
