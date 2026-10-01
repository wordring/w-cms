package toho

// ─────────────────────────────────────────────────────────────────────────
// PDFの判定→受注ページ生成——ボタン起動（東邦の業務・2026-09-01）
//
// 添付PDFを Gemini で判定し、**顧客が発行した発注書**なら受注ページを
// そのページの子として生成します。
//
// ⚠ **ZIP の中の PDF は、ここでは扱いません**（2026-09-17）。メールの取り込みが
// ZIP を展開して中身を1つずつ添付にするので（`ext/comm/intake_eml.go`）、解析が見る
// のは常に「自分のブロックIDを持つ PDF」です。それまでは `entry` 引数で ZIP の中の
// 1件を取り出し、`元ファイル`・`対応DXFファイル` のタグで中のパスを添えていましたが、
// 参照が ZIP を指すのでファイル表示が PDF を開けませんでした。
//
//	通信記録ページ（親）── 📎 発注書.pdf
//	└─ 受注ページ（本APIが生成）
//	     <h1>受注 PO-xxx</h1>
//	     <dl data-type="tags"> 発注書番号・発注元・発注日・納期・小計・税・合計・
//	                          受信元: <ページID>-<添付ID>（押すと該当ブロックへ飛ぶ）
//	     <details> 読んだままの表 ＋ <table><caption>受注明細</caption> 弊社の明細
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
	"html"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/google/generative-ai-go/genai"

	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
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
		j, err = judgeOrderSheet(text)
	} else {
		j, err = judgeOrderPDF(pdf)
	}
	if err != nil {
		if errors.Is(err, cms.ErrNoGeminiKey) {
			cms.JSONFail(w, http.StatusServiceUnavailable, "サーバーに GEMINI_API_KEY 環境変数が設定されていません。設定してから起動し直してください。")
			return
		}
		cms.JSONFail(w, http.StatusBadGateway, "解析に失敗しました: "+err.Error())
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
	// ⚠ **図面の枝も同じ値を使います**——`客先` は置き場所（社名／段／装置名称）に
	// 効くので、ここで揃えないと**受注ページと加工製品ページで社名が食い違います**。
	j.Customer = contacts.OrgNameForPage(user, j.Customer)
	for i := range j.Drawings {
		j.Drawings[i].Customer = contacts.OrgNameForPage(user, j.Drawings[i].Customer)
	}
	for i := range j.Orders {
		j.Orders[i].Customer = contacts.OrgNameForPage(user, j.Orders[i].Customer)
	}

	// **図面PDFの枝**——同じページに付いているDXFと図面番号で突き合わせ、
	// 同じ部品の図面として1枚の加工製品ページにまとめる（drawing_match.go）。
	if !j.IsClientOrder && j.DocType == "drawing" {
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
		// ⚠ **本文を全部組んでから作り始めます**——テンプレートに図面ブロック・改訂明細が
		// 無ければ、1枚も作らずに断ります（途中まで作って止まると、人が片付けることになる）。
		made := []map[string]any{}
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
		for _, body := range bodies {
			newID, err := cms.CreateChildPage(pageID, user.Username, body)
			if err != nil {
				// ⚠ **途中で失敗しても、できたぶんは残します**——作れた図面まで
				// 捨てると、人はもう一度解析するしかなくなり、**通ったぶんが二重に
				// できます**。何枚できたかを返して、人に見てもらいます。
				cms.JSONFail(w, http.StatusInternalServerError,
					"加工製品ページを作れません（"+strconv.Itoa(len(made))+"枚目まで作成済み）: "+err.Error())
				return
			}
			auth.Audit(user.Username, "analyze-drawing", newID+" from "+pageID+"/"+fileName)
			made = append(made, map[string]any{"page_id": newID, "title": pageTitleOf(newID)})
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
		return
	}
	if !j.IsClientOrder {
		// 生成しないのも正常な結果——人が押した問いに「発注書ではない」と答える。
		json.NewEncoder(w).Encode(map[string]any{"success": true, "is_client_order": false})
		return
	}

	// ⚠ **発注書1枚につき1ページ**（2026-09-30・`orderList`）。図面の枝と同じく、**本文を全部組んでから作り始めます**
	// ——テンプレートに足りない印があれば1枚も作らずに断る（途中まで作って止まると、人が片付けることになる）。
	sheets := j.orderList()
	bodies := make([]string, 0, len(sheets))
	for _, o := range sheets {
		body, err := buildOrderPageHTML(orderTmpl, pageID, attachID, o)
		if err != nil {
			cms.JSONFail(w, http.StatusConflict, "受注ページを作れません: "+err.Error())
			return
		}
		bodies = append(bodies, body)
	}
	made := []map[string]any{}
	linked := 0
	for _, body := range bodies {
		newID, err := cms.CreateChildPage(pageID, user.Username, body)
		if err != nil {
			// ⚠ できたぶんは残す（図面の枝と同じ——捨てると押し直しで二重にできる）。
			cms.JSONFail(w, http.StatusInternalServerError,
				"受注ページを作れません（"+strconv.Itoa(len(made))+"枚目まで作成済み）: "+err.Error())
			return
		}
		auth.Audit(user.Username, "analyze-pdf", newID+" from "+pageID+"/"+fileName)
		// ⚠ **作った直後に、いま在る加工製品ページと結びます**（2026-09-21・[link_item.go]）。
		// **図面が先に届いていた場合、ここで結ばないと一度も埋まりません**——図面の整理は
		// もう終わっているからです。そして**返り注文は必ずこの順**です。
		//
		// ⚠ **解析の失敗にはしません**——結べなくても受注ページは正しく作れています。
		// 埋まった行数は画面へ返し、人が「何が起きたか」を見られるようにします。
		linked += LinkProductsToOrder(user, newID)
		made = append(made, map[string]any{"page_id": newID, "title": pageTitleOf(newID)})
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
判定する種類は次の3つです:
  - "order"   : 顧客（取引先）が当社宛てに発行した発注書（注文書）
  - "drawing" : 部品や製品の図面（表題欄に図面番号・図面名称があるもの）
  - "other"   : 上記以外（見積書・請求書・納品書・カタログ・案内など）
次の形式のJSONオブジェクトのみを出力してください（マークダウンのコードブロック修飾は付けない）:
{
  "doc_type": "order" または "drawing" または "other",
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
  "drawings": [{"drawing_no": "図面番号", "drawing_name": "図面名称", "machine_name": "装置名称", "customer": "客先"}]
}
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
	return &j, nil
}

// OrderPageTemplate は受注ページを作るテンプレートの題です（2026-09-27）。
//
// ⚠ **受注ページの形はこのテンプレートが決めます**——タグの並び・原本PDFと写しの枠・
// 受注明細の列の並び。機械が書くのは**題と値と行の数**だけです（利用者:「テンプレートには
// スラッシュメニューから表などの印を置き、コードはそれを埋めてはどうでしょう？」）。
// テンプレートが無ければ受注ページを作りません（`cms.ErrNoPageTemplate`）。
const OrderPageTemplate = "受注ページ"

// buildOrderPageHTML は受注ページの本文を、テンプレート tmpl を埋めて組みます。
//
// 埋めるもの（2026-09-27 からテンプレート駆動・形はテンプレートが持つ）:
//
//   - **タグ**（発注書番号・発注元・発注日・納期・小計・税・合計・由来）——`<dt>` の名前で
//     探して値を入れる。テンプレートに無いタグは、値があれば足す。
//   - **原本のPDF**——まだ配線されていないファイル表示の印へ（無ければ出さない）。
//   - **顧客の発注書（読んだまま）**——その名前の畳める枠（または見出しの節）の中へ表を
//     入れる。読めなければ枠ごと消す（無ければ出さない）。
//   - **受注明細**——キャプション `受注明細` の表へ、見出しの言葉で列を合わせて明細の数だけ
//     行を作る。⚠ **表が無ければ作らない**（明細の行き場が無い）。
//
// 行の `状態` は「未着手」で始まります（進捗の起点）。
func buildOrderPageHTML(tmpl, hostPageID, attachID string, j *orderJudgment) (string, error) {
	title := "受注 " + cms.NormalizeNameForIngest(j.OrderNo)
	if cms.NormalizeNameForIngest(j.OrderNo) == "" {
		if cms.NormalizeNameForIngest(j.Customer) != "" {
			title = "受注（" + cms.NormalizeNameForIngest(j.Customer) + "）"
		} else {
			title = "受注（番号不明）"
		}
	}

	// ── ヘッダは**可変タグ**、明細は**表**（2026-09-18 ユーザー決定）──
	//
	// ユーザー:「受注ページを作成するときにも、出来る限りタグを使いたいです。そのほうが
	// 編集者もDBに入っていると視覚的にわかるからです」「専用コードの全廃を前向きに
	// 検討してください」。
	//
	// **表とタグだけがDBに入る**——それが説明の全部になりました。機能見出しの節に
	// 素の定義リストを置く形（業務ブロックのヘッダ）はやめました。⚠ **1文書＝1ページ**が
	// 規則です（ユーザー:「発注書は一ページ一発注書で問題ない」）——ヘッダがページの
	// タグになるので、1ページに2つの発注書は置けません。
	d := cms.NewPageDraft(OrderPageTemplate, tmpl)
	d.SetTitle(title)
	setHeaderTag(d.DraftBlock, OrderNoTag, cms.NormalizeNameForIngest(j.OrderNo))
	setHeaderTag(d.DraftBlock, OrderClientTag, cms.NormalizeNameForIngest(j.Customer))
	setHeaderTag(d.DraftBlock, OrderedAtTag, j.OrderDate)
	// ⚠ **納期は行ではなくここ**（2026-09-20・実データで確認）。1文書1ページなので、
	// 書面のヘッダにあるものはページのタグになります。
	//
	// ⚠ **日付とは限りません。** 実データの1通目が「**最短納期**」でした（ユーザー）。
	// 値はそのまま書きます——**取り込みは情報を捨てない**（D-3）。`納期` は辞書で
	// `date` なので、日付として読めない値は**畳んだ値が付かないだけ**で、生の値は
	// 正本として残ります（語彙モデル §5.1「解釈できない値は併記しない。拒否もしない」）。
	//
	// ⚠ **`YYYY-MM-DD 形式で`とだけ頼んでいたころ、Gemini は空で返していました**
	// ——日付にできないので。プロンプトで「日付でない書き方は書かれているまま」と
	// 頼むまで、**書いてあるのに何も残らない**状態でした。
	setHeaderTag(d.DraftBlock, DueDateTag, j.DueDate)
	// ⚠ **検算の材料**（[checksum.go]）。空ならテンプレートの空欄のまま出ます——**書く場所が
	// 見えていれば人が埋められます**（発注書に書いてあるのに読めなかった場合、人が打てば
	// その場で検算が効きます。鏡型なので保存し直せば ⚠ が消えます）。
	setHeaderTag(d.DraftBlock, SubtotalTag, j.Subtotal)
	setHeaderTag(d.DraftBlock, TaxTag, j.Tax)
	setHeaderTag(d.DraftBlock, TotalTag, j.Total)
	// 由来参照（§9.1）——値は「元ページID-添付ID」。参照タグの文法（ref_render.go）に
	// 一致するのでリンクとして描画され、押すと元ページの該当ブロックへ飛ぶ。
	ref := hostPageID + "-" + attachID
	if attachID == "" {
		ref = hostPageID // メールの本文から作るとき（2026-10-01・analyze_mail.go）——由来はメールのページ全体
	}
	setHeaderTag(d.DraftBlock, SourceRefTag, ref)

	// ── 原本のPDF（2026-09-21 ユーザー決定）──
	//
	// ユーザー:「顧客の発注書（読んだまま）の上にPDFを表示できるようにします
	// （通常は折りたたむ）」。**写しは読み取りの結果で、PDFが原本そのもの**なので、
	// 並びは「原本 → 読んだまま → 弊社の明細」になります（並びはテンプレートが持つ）。
	//
	// 中身はコアが描きます（`internal/cms/file_view.go`）——**この拡張はPDFの
	// 出し方を知りません**。人が消せば出なくなり、`受信元` のタグ（出所の記録）は残ります。
	// ⚠ テンプレートに印が無ければ出しません（表示は飾りで、原本は元のページに在る）。
	if attachID != "" {
		d.SetFileView(ref)
	} else {
		d.DropFileView() // 原本のファイルが無い（メールの本文）——空の PDF の枠を残さない
	}

	// ── 顧客の発注書（読んだまま）──
	//
	// ⚠ **畳んで出します**（`<details>`・テンプレートの枠）。ユーザー:「顧客の表は、
	// 整理したあとも残します。**ボタンで畳めれば良い**と思います」。素のHTMLだけで畳める
	// ので、JS も `on*=` も要りません（CSP strict の下で動き、公開ページのゼロJSでも畳めます）。
	//
	// ⚠ **形式を登録していないので索引に載りません**（2026-09-20 の線引き）。
	// 原本は**証拠**であって、検索したいのは弊社の表のほうです——**二重計上も
	// 最初から起きません**。
	if box, ok := d.Container(sourceTableCaption); ok {
		if tbl := sourceTableHTML(j.SourceTable); tbl != "" {
			box.SetContent(tbl)
		} else {
			d.Remove(box.Node()) // ⚠ **読めなければ枠ごと出しません**（空の枠は誤解を生む）
		}
	}

	var rows []map[string]string
	for _, it := range j.Items {
		// ── 弊社の表は**正規形で書き起こします**（2026-09-21 ユーザー決定）──
		//
		// ユーザー:「品名が半角カナになっているので、正規化します。半角カナ以外も
		// 正規化します」。実データの品名が `ﾌﾞﾗｹｯﾄ` の形で届きました。
		//
		// ⚠ **半角カナのままだと、同じ品物が2つの綴りで並びます**——`ブラケット` と
		// `ﾌﾞﾗｹｯﾄ` は**画面では似て見えるのに、検索では別物**です。索引の畳んだ値
		// （`norm_value`）は NFKC を通るので引くほうは当たりますが、**人が読む値**
		// （`value`・表の見た目）は半角のまま残り、目で見比べる人が揺れます。
		//
		// 通すのは `NormalizeNameForIngest`（NFKC＋空白の詰め）で、`NormalizeCode`
		// ではありません——長音を潰すと `レーザー` が `レ-ザ-` になります。
		//
		// ⚠ **「機械は text を書き換えない」の例外です。** 理由は
		// [normalize_ingest.go] が図面名称で述べているのと同じで、**原本がページに
		// 出ているから**です——食い違えば、すぐ上の「読んだまま」の表と、その上の
		// PDF で見比べられます。原本の写しは**畳みません**（読んだままが正本）。
		//
		// 日付と数値は従来どおり正規形で書き起こします（D-3「正規化は取り込み時に
		// 行う」）。読めなければ生のまま入ります——取り込みは情報を捨てない。
		//
		// ⚠ **弊社品番・備考は空で出します。** 解析には決められません——
		// 弊社品番は人が文脈から結び、備考は発注書の様式しだいです（様式ページの
		// 対応表が入ったら、そこから埋まります）。**書く場所が見えていれば人が埋めます**
		// （図面ブロックで空欄の `客先` を出しているのと同じ理由）。
		rows = append(rows, map[string]string{
			"item-id":   cms.NormalizeNameForIngest(it.ItemNo),
			"item-name": cms.NormalizeNameForIngest(it.ItemName),
			"quantity":  cms.CanonicalForIngest("数量", it.Quantity),
			// ⚠ **単位も畳みます**——`ｾｯﾄ` のまま入ると選択肢（`個`／`セット`）の
			// どちらにも当たらず、**画面が「見慣れない単位」として色を付けます**。
			// まれにしか出ない単位ほど、揺れたまま気づかれません。
			"unit":  cms.NormalizeNameForIngest(it.Unit),
			"price": cms.CanonicalForIngest("単価", it.Price),
			// ⚠ **行の納期は、受注時はページの納期と同じ**（2026-09-20 ユーザー:
			// 「行の納期は、受注時にはタグの納期と同じです。**その後顧客の依頼や
			// 弊社の事情で個別に納期が変わることがあります**。すると行の納期を
			// 書き換えます」）。**配っておきます**——同じ日付をN回打たせる理由がなく、
			// 変わった行だけ直せば済みます。
			//
			// ページのタグは**先方が書いたこと**のまま残り、行は**弊社の予定**として
			// 動きます。⚠ 日付でない値（「最短納期」）もそのまま配ります——
			// 書いてあることを捨てないのが決まりです。
			// ⚠ **行の納期もページのタグと同じ作法を通します**（2026-09-21）。
			// それまでここだけ生のまま書いていたので、先方が `2026/10/15` と
			// 書いていると**ページのタグは `2026-10-15`、行は `2026/10/15`** と
			// 食い違いました。`date` として読めない値（「最短納期」）はそのまま残ります。
			"due": cms.CanonicalForIngest(DueDateTag, j.DueDate),
			// 出荷済みは空が「まだ出していない」。⚠ **分納があるので要ります**
			// ——「100個のうち40個だけ出した」は `状態` だけでは表せません。
			// ⚠ **手続きの印は空で始めます**（材料発注・納品書発行・請求書発行）。
			// 解析には分かりません——どれも**これから人がやること**です。
			"status": "未着手",
		})
	}
	// ⚠ **キャプションで名乗る表です**（2026-09-20 ユーザー:「弊社の受注表にも
	// キャプションが欲しいところです」）。原本の表と並ぶので、**どちらが何なのか
	// 見て分かる**必要があります。**列は見出しの言葉で合わせます**——テンプレートで列を
	// 並べ替えても・足しても崩れず、テンプレートに無い列は値があれば右端へ足します。
	if _, err := fillVocabTable(d.DraftBlock, clientOrderItemsType, rows); err != nil {
		return "", err
	}
	// ⚠ **言い換えたときは、そう書きます**（2026-09-21）。先方の「図面番号」を
	// 弊社の「品番」に入れたなら、その1行を表の手前に残します——**原本と見比べ
	// なくても気づける**のがここの目的です（0c の積み残し）。
	if note := itemNoSourceNote(j.Items); note != "" {
		if t, ok := d.Table(displayNameOf(clientOrderItemsType)); ok {
			d.InsertBefore(t, note)
		}
	}
	return d.HTML(), nil
}

// itemNoSourceNote は「弊社の `品番` を、先方のどの列から採ったか」の1行です。
//
// ⚠ **言い換えたときだけ書きます。** 先方にも「品番」の列があったなら、言い換えて
// いないので知らせることがありません——**毎回出る注意書きは読まれなくなります**
// （未処理一覧で一度学んだことです）。
//
// ⚠ **行ごとに違う列から採っていたら黙ります。** それは様式を読めていない印で、
// 「どの列から採ったか」を1行で言えません。**嘘を書くより黙るほうが正直**です
// ——そのときは原本の写しが手掛かりになります。
//
// ⚠ **本文に書きます（鏡型ではありません）。** 検算の ⚠ とは性格が違うためです:
// あちらは**いまの数字**についての判断なので、人が数字を直したら消えなければ
// なりません。こちらは**解析がそのとき何をしたか**という出来事で、あとから人が
// 品番を打ち直しても「解析はそこから採った」は真のままです。消したい人は消せます。
func itemNoSourceNote(items []orderPDFItem) string {
	src := ""
	for _, it := range items {
		s := cms.NormalizeNameForIngest(it.ItemNoSource)
		if s == "" {
			continue
		}
		if src == "" {
			src = s
			continue
		}
		if src != s {
			return "" // 行ごとに違う＝様式を読めていない
		}
	}
	if src == "" || src == itemNoLabel() {
		return "" // 知らせることが無い（言い換えていない・分からない）
	}
	return `<p>解析は、先方の「` + html.EscapeString(src) +
		`」を弊社の「` + html.EscapeString(itemNoLabel()) + `」に入れました。</p>`
}

// itemNoLabel は受注明細の `品番` 列の見出しを返します（宣言と同じ綴りを使うため）。
//
// ⚠ **生の文字列で持つと、列を改名した日にこの注意書きが常に出ます**——
// 「先方の『品番』を弊社の『品番』に入れました」という、意味の無い1行になります。
//
// ⚠ **変数ではなく関数です。** パッケージ変数の初期化は `init()` より**先**に
// 走るので、`var` で持つと語彙が登録される前に引くことになり、**常に空**に
// なります——空だと上の比較が素通りして、言い換えていないときも1行出ます。
func itemNoLabel() string { return labelOf(clientOrderItemsType, "item-id") }

// labelOf は形式の列の見出しを宣言から返します（未登録なら空）。
func labelOf(vocabType, field string) string {
	for _, c := range columnsOf(vocabType) {
		if c.Field == field {
			return c.Label
		}
	}
	return ""
}

// setHeaderTag はタグ name に値を入れます（空値なら何もしない＝テンプレートの空欄のまま、
// あとから人が埋める）。
//
// 日付・数値の見出し語（発注日 等）は**正規形へ揃えてから**書きます
// （D-3・`cms.CanonicalForIngest`）。図面番号や客先名は揃えません——
// 機械が畳んで書き換えると、原本と見比べたときに食い違うためです。
// 参照（`受信元`・`対応DXF`）もここを通ります——`ref` 型は正規化の対象外なので、
// 値はそのまま入ります。
func setHeaderTag(b cms.DraftBlock, name string, values ...string) {
	var vals []string
	for _, v := range values {
		if strings.TrimSpace(v) != "" {
			vals = append(vals, cms.CanonicalForIngest(name, v))
		}
	}
	b.SetTag(name, vals...)
}

// ProductTemplate は加工製品ページを作るテンプレートの題です（2026-09-27）。
//
// ⚠ **図面の解析はこのテンプレートを写して加工製品ページを作ります**——人が手で作るときに
// 選ぶ雛形と同じもの。見出し「図面」の節（図面ブロック）と、キャプション「改訂明細」の表が
// 要ります（無ければ作りません）。材料・外注加工などの表は写すだけです。
const ProductTemplate = "加工製品"

// drawingHeading は図面ブロック（見出しの節）の見出しです。
const drawingHeading = "図面"

// buildProductPageHTML は加工製品ページの本文を、テンプレート tmpl を埋めて組みます。
//
//	<h1>P103-227-6 台座Assy</h1>
//	<section><h2>図面</h2><dl data-type="tags"> 図面番号・図面名称・…・受信元・対応DXF </dl>
//	  <section data-type="file-view" data-ref="…"></section></section>
//	<table><caption>改訂明細</caption> … 1版目の行 </table>
//
// **1通のメールの中でPDFとDXFを対応づけた結果**がこのページです。過去のページを
// 図面番号で探して束ねることはしません——番号は別製品で衝突しうるので、
// 同一性を担うのは常にページID（drawing_match.go 冒頭）。
func buildProductPageHTML(tmpl, hostPageID, attachID string, j *orderJudgment, matches []matchedDXF) (string, error) {
	// 題は「図面番号 図面名称」——**図面名称は重複しうる**ので番号を先に置く。
	//
	// **ブロックと同じ正規化を通します。** 通さないと、題が `シュート先Ｔ金具` で
	// ブロックが `シュート先T金具` という食い違いが出ます（実データで出しました）。
	title := strings.TrimSpace(cms.NormalizeNameForIngest(j.DrawingNo) + " " +
		cms.NormalizeNameForIngest(j.DrawingName))
	if title == "" {
		title = "図面（番号不明）"
	}

	d := cms.NewPageDraft(ProductTemplate, tmpl)
	d.SetTitle(title)
	if err := fillDrawingBlock(d, j, hostPageID, attachID, matches); err != nil {
		return "", err
	}
	if err := fillFirstRevision(d, j.DrawingNo); err != nil {
		return "", err
	}
	return d.HTML(), nil
}

// fillFirstRevision は改訂明細の1版目を書きます（解析が作る加工製品ページと、メールから作る空の加工製品ページで同じ）。
func fillFirstRevision(d *cms.PageDraft, drawingNo string) error {
	// 改訂明細（1版目）。
	//
	// **行が社内コードの指し先です**——`ページID-行ID` で押せばその版へ飛びます
	// （2026-09-03 ユーザー:「改訂履歴の項目を作り版にdata-idを割り当てれば良いのでは？」）。
	// 図面ブロックは消せる決まりなので指し先にせず、消す理由の無い小さな行を指します。
	//
	// ⚠ **キャプションだけで名乗ります**（2026-09-27 利用者:「改訂履歴はキャプションだけで良い
	// です」「メニューの方も用語を統一したほうが良いのでは？」）。
	// ⚠ **表と行にブロックIDを振ります**——テンプレートを写すとブロックIDは外れるので、
	// 振らないと改定の行を数える正規表現（`revisionRowRe`）に1版目が数えられず、版がずれます。
	rows, err := fillVocabTable(d.DraftBlock, revisionItemsType, []map[string]string{{
		"revision":    "1",
		"drawing-no":  strings.TrimSpace(drawingNo),
		"received-at": time.Now().In(time.Local).Format("2006-01-02"),
	}})
	if err != nil {
		return err
	}
	if t, ok := d.Table(displayNameOf(revisionItemsType)); ok {
		d.AssignBlockID(t)
	}
	for _, tr := range rows {
		d.AssignBlockID(tr)
	}
	return nil
}

// fillDrawingBlock は図面ブロック（見出し「図面」の節）を埋めます。
//
// **ブロックIDを付けるのが肝**——参照値 `ページID-ブロックID` は押せばこの
// ブロックへ飛ぶので、これが**その改定の社内コード**になります（2026-09-03 ユーザー:
// 「部品の社内コードは加工製品ページのページ番号と改定番号を足したものになるのでは？
// …すると、社内コードでその項目へ飛べることになります」）。
// 図面番号が別製品と衝突しても、この番号は構造上一意です。
//
// 由来（受信元・対応DXF）を**ブロックの中**に置くのは、改定で合流させるときに
// ブロックごと運べば出所も一緒に付いて行くようにするためです。
func fillDrawingBlock(d *cms.PageDraft, j *orderJudgment, hostPageID, attachID string, matches []matchedDXF) error {
	blk, err := d.RequireContainer(drawingHeading)
	if err != nil {
		return err
	}
	d.AssignBlockID(blk.Node())
	// 図面番号も同じ扱いです（2026-09-06）——**人がいちばんコピペする値**なので、
	// 揃わないまま置くと揺れがそこから増えます。畳むのは NFKC までで、
	// `NormalizeCode` は使いません（ハイフンと長音を潰すと読めなくなる）。
	// **顧客名・装置名称・図面名称は早期に正規化します**（2026-09-06 ユーザー）。
	// この3つはそのままページの題になり、題の完全一致が階層の同一性を決めるので、
	// 畳まずに入れると `φ３２０　共通台座` が別の装置ページになります。
	//
	// ── ここは**可変タグ**です（2026-09-18 ユーザー決定）──
	//
	// ユーザー:「この定義リストを『名前：値のタグ』に変更します。なぜなら、この情報こそ
	// 検索したいものだからです。おそらくもっとも頻繁に検索し、ワンノートでは取りこぼしが
	// 多いので、w-cms を作り始めました」。タグにすると `図面番号` は設定で `code` 型なので、
	// 空白・ハイフン・長音・大小を畳んで引けます（`PagesByTagLoose`）。
	//
	// ⚠ **空欄でも欄を出します**——テンプレートの空欄（`<dd><br/></dd>`）がそのまま残ります。
	// 装置名称と客先は解析が読めないことがあり、**書く場所が見えていれば人が埋めます**
	// （実データで7枚のうち3枚に `客先` が無かった）。
	// ⚠ **`品番` は書きません**（2026-09-21 ユーザー:「加工製品ページに品番タグを付けようと
	// 思います」）。**図面からは読みません**——「何が品番か」は取引先ごとの取り決めで、
	// 図面のどこにも書かれていないからです。置き場は H1 の下（テンプレートが持つ）。
	setHeaderTag(blk, DrawingNoTag, cms.NormalizeNameForIngest(j.DrawingNo))
	setHeaderTag(blk, DrawingNameTag, cms.NormalizeNameForIngest(j.DrawingName))
	// 装置名称・客先は置き場所（社名／段／装置名称／図面名称）に効く項目。
	setHeaderTag(blk, MachineNameTag, cms.NormalizeNameForIngest(j.MachineName))
	setHeaderTag(blk, ClientNameTag, cms.NormalizeNameForIngest(j.Customer))
	ref := hostPageID + "-" + attachID
	setHeaderTag(blk, SourceRefTag, ref)
	// 一致したDXFを参照タグで指す（押すと元の通信記録ページの該当添付へ飛ぶ）。
	// 一致が無ければ何も書かない——**DXFが無いのも普通**（PDFだけの図面）。
	var dxf []string
	for _, m := range matches {
		dxf = append(dxf, hostPageID+"-"+m.AttachID)
	}
	setHeaderTag(blk, "対応DXF", dxf...)
	// **図面をここに開く、と本文に書きます**（2026-09-14）。ユーザー:「HTMLに無いものが
	// 表示されるのは極力避けたい」「表示するという意図を伝える名前が良いと思います」。
	// 中身はコアが描きます（`internal/cms/file_view.go`）——**この拡張はPDFの出し方を
	// 知りません**。テンプレートの図面ブロックに置いたファイル表示の印へ配線します
	// （**人が消せます**——消せば図面は出なくなり、参照タグ〔出所の記録〕は残ります）。
	blk.SetFileView(ref)
	return nil
}

// revisionRowHTML は改訂履歴の1行です。行の data-id が改定番号になります。
func revisionRowHTML(rev int, drawingNo, existingBody string) string {
	return `<tr data-id="` + cms.NewBlockID(existingBody) + `"><td>` +
		strconv.Itoa(rev) + "</td><td>" + html.EscapeString(strings.TrimSpace(drawingNo)) +
		"</td><td>" + time.Now().In(time.Local).Format("2006-01-02") + "</td></tr>"
}

// revisionRowRe は改訂履歴の行（見出し行を除く）を数えるための正規表現です。
var revisionRowRe = regexp.MustCompile(`<tr data-id="[0-9a-z]+"><td>`)

// InsertRevisionRow は改訂履歴の**先頭**（見出し行の直後）へ1行足し、
// 版番号を1つ進めた本文を返します。履歴が無い本文はそのまま返します
// （手で消した・古い形のページ——黙って作り直すと版番号が狂うため）。
func InsertRevisionRow(bodyHTML, drawingNo string) string {
	at := revisionTableAt(bodyHTML)
	if at < 0 {
		return bodyHTML
	}
	// 見出し行の終わりを探し、その直後へ差し込む（新しい版が上）。
	head := strings.Index(bodyHTML[at:], "</tr>")
	if head < 0 {
		return bodyHTML
	}
	insertAt := at + head + len("</tr>")
	rev := len(revisionRowRe.FindAllString(bodyHTML, -1)) + 1
	return bodyHTML[:insertAt] + revisionRowHTML(rev, drawingNo, bodyHTML) + bodyHTML[insertAt:]
}

// revisionItemsType は改訂履歴の明細表の形式名です。
//
// ⚠ **2箇所が同じ表を文字列で探します**（`InsertRevisionRow` と `linkRevisionRow`）。
// 生の文字列で持つと、**片方だけ直したときに黙って効かなくなります**——表が
// 見つからなければどちらも**本文をそのまま返す**ので、エラーが出ません。
const revisionItemsType = "drawing-revision-items"

// revisionTableAt は改訂履歴の表の開始位置を返します（無ければ -1）。
//
// ⚠ **2つの書き方を両方見ます**（2026-09-20）。形式の宣言は `data-type` 属性から
// **見える文字（`<caption>`）へ移る途中**で、しばらく併存します
// （[docs/考察/【考察】発注書から受注明細へ.md] §2.4）。
//
// ⚠ **片方しか見ていないと、caption へ移した日に改定が黙って積まれなくなります**
// ——`InsertRevisionRow` は表が見つからなければ**本文をそのまま返す**ので、
// エラーも出ません。気づくのは「版が増えていない」と誰かが思ったときです。
//
// **HTMLをパースせず文字列で探すのは、このファイルの作法です**——本文は正本なので、
// 触っていない所を1バイトも変えないため（`replaceFirstFieldValue` と同じ理由）。
func revisionTableAt(bodyHTML string) int {
	// ① 属性で名乗っている（既存の本文）。
	if at := strings.Index(bodyHTML, `<table data-type="`+revisionItemsType+`">`); at >= 0 {
		return at
	}
	// ② caption で名乗っている（新しい書き方）。表の開始位置を返すので、
	//    caption を見つけたら**その表の `<table` まで戻ります**。
	def, ok := cms.VocabDefByType(revisionItemsType)
	if !ok {
		return -1
	}
	cap := "<caption>" + def.DisplayName + "</caption>"
	capAt := strings.Index(bodyHTML, cap)
	if capAt < 0 {
		return -1
	}
	return strings.LastIndex(bodyHTML[:capAt], "<table")
}

// sourceTableHTML は顧客の発注書の写しの表を組みます（空なら何も出しません）。
// 畳める枠（`<details>`）はテンプレートが持ちます（2026-09-27）——ここは枠の中身だけ。
//
// ⚠ **`data-type` を付けません。** 形式を登録していない表は索引に載らない決まりなので
// （2026-09-20）、原本はそのまま「見せるだけ」になります。
//
// ⚠ **caption も付けません**（2026-09-28・利用者の選択）。それまでは「畳んだときに何の表か分かる
// ように」付けていましたが、**表の写し（`data/tables.db`・2026-09-25）はキャプションのある表を全部
// DB に入れる**ので、証拠の写しまで DB に入り、顧客の見出し（`No.` など）が保存のたびに「SQL で引くとき
// 引用符が要る」と告げられていました。何の表かは**外側の折りたたみの題**（テンプレートの
// 「顧客の発注書（読んだまま）」）が言います。検算はその枠の中の表を読みます（`sourceTableIn`）。
func sourceTableHTML(t orderSourceTable) string {
	if len(t.Headers) == 0 || len(t.Rows) == 0 {
		return "" // ⚠ **読めなければ出しません**（空の枠だけ出しても誤解を生む）
	}
	var b strings.Builder
	b.WriteString(`<table><tbody><tr>`)
	for _, h := range t.Headers {
		b.WriteString("<th>" + html.EscapeString(h) + "</th>")
	}
	b.WriteString("</tr>")
	for _, row := range t.Rows {
		b.WriteString("<tr>")
		for i := range t.Headers {
			v := ""
			if i < len(row) {
				v = row[i]
			}
			// ⚠ **足りない列は空で埋めます**——列数が揃っていないと、見出しと値の
			// 対応が1つずつずれて**別の列の値に見えます**。
			b.WriteString("<td>" + html.EscapeString(v) + "</td>")
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table>")
	return b.String()
}

// sourceTableCaption は原本の写しを入れる枠（テンプレートの折りたたみ・見出しの節）の題です。
//
// ⚠ **語彙に登録しません**（登録すると索引に載り、弊社の明細と二重になります）。
// 人が読むための名前です。⚠ 2026-09-28 から表の caption には書きません（`sourceTableHTML`）——
// 名前の定数に "Caption" が残っているのは、それより前の本文の caption も読むためです（`sourceTableIn`）。
const sourceTableCaption = "顧客の発注書（読んだまま）"

// parseSourceTable は原本の写しを**寛容に**解きます（解けなければ空）。
//
// ⚠ **行の形が2通りあります。** 頼んでいるのは `[["1","品名",…]]`（配列の配列）ですが、
// **Gemini は見出しを鍵にした object で返すことがよくあります**
// （`[{"No.":"1","品名":"…"}]`）。どちらでも受けます。
//
// ⚠ **ここで失敗しても解析は止めません。** 構造に直接当てていたころは、**形が合わない
// だけで発注書番号も明細も丸ごと失われて**いました（実データの2通目で「解析に失敗
// しました」が出た原因）。**おまけのために本題を落とさない。**
func parseSourceTable(raw json.RawMessage) orderSourceTable {
	if len(raw) == 0 {
		return orderSourceTable{}
	}
	// ① 頼んだとおりの形（見出しの配列＋行の配列）。
	var direct struct {
		Headers []string          `json:"headers"`
		Rows    []json.RawMessage `json:"rows"`
	}
	if err := json.Unmarshal(raw, &direct); err != nil {
		return orderSourceTable{}
	}
	out := orderSourceTable{Headers: direct.Headers}
	for _, r := range direct.Rows {
		// ②-a 行が配列（頼んだ形）。
		var cells []string
		if err := json.Unmarshal(r, &cells); err == nil {
			out.Rows = append(out.Rows, cells)
			continue
		}
		// ②-b 行が object（見出しを鍵にしている）。**見出しの順に並べ直します**。
		var byKey map[string]any
		if err := json.Unmarshal(r, &byKey); err != nil {
			continue // この行だけ飛ばす（⚠ 1行のために表ごと捨てない）
		}
		row := make([]string, len(out.Headers))
		for i, h := range out.Headers {
			row[i] = jsonCellText(byKey[h])
		}
		out.Rows = append(out.Rows, row)
	}
	if len(out.Headers) == 0 || len(out.Rows) == 0 {
		return orderSourceTable{}
	}
	return out
}

// jsonCellText は JSON の値をセルの文字へ直します（数も文字として扱う——原本の写しなので、
// 書かれていたとおりの見た目を残すのが目的）。
func jsonCellText(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return t
	case float64:
		// ⚠ **整数は小数点を付けません**（`100` が `100.000000` になると原本と違う）。
		if t == float64(int64(t)) {
			return strconv.FormatInt(int64(t), 10)
		}
		return strconv.FormatFloat(t, 'f', -1, 64)
	case bool:
		if t {
			return "true"
		}
		return "false"
	default:
		b, err := json.Marshal(t)
		if err != nil {
			return ""
		}
		return string(b)
	}
}
