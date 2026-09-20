package subcon

// ─────────────────────────────────────────────────────────────────────────
// PDFの判定→受注ページ生成——ボタン起動（下請け業務・2026-09-01）
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
//	     <section><h2>顧客の発注書</h2> ヘッダ dl ＋ 明細 table（機能見出し形・D-2）
//	     <dl data-type="tags"> 受信元: <ページID>-<添付ID>（押すと該当ブロックへ飛ぶ）
//
// **起動は人の指先だけ**——「自動ではなくボタンのclickなどで解析が始まると良い」
// （2026-09-01 ユーザー決定）。当初は .eml 到着時の自動判定（取り込み観察係）として
// 作ったが同日ボタン起動へ一本化した。§3 の全体方針「人間ゲート型の取り込み」への
// 回帰であり、Gemini の呼び出し（コスト・誤判定）は常に人の操作の直後にだけ起きる。
// 誤生成の取り消しはページ削除（§2.7④ 可逆性——通信記録は不変で残っている）。
//
// このファイルは他社デプロイでは外す・差し替える前提の既定セットです
// （docs/【考察】通信記録処理.md §3.2。Gemini はコア側インフラ gemini.go、
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
	MachineName string         `json:"machine_name"`
	OrderNo     string         `json:"order_no"`
	Customer    string         `json:"customer"`
	OrderDate   string         `json:"order_date"`
	// DueDate は納期です。⚠ **明細ではなくヘッダにあります**（実データで確認）。
	DueDate string `json:"due_date"`
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
	SourceTable orderSourceTable `json:"source_table"`
	Items       []orderPDFItem `json:"items"`
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
	ItemNo   string `json:"item_no"`
	ItemName string `json:"item_name"`
	Price    string `json:"price"`
	Quantity string `json:"quantity"`
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
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodPost {
		cms.JSONFail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		File   string `json:"file"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, ok := page.NormalizeID(req.PageID)
	if !ok {
		cms.JSONFail(w, http.StatusBadRequest, "ページIDが不正です")
		return
	}
	// 子ページを作る操作なので write 権限を要求する（本文は変えないので編集ロックは不要
	// ——取り込みと同じ理屈）。
	if !page.RequirePageWrite(w, r, pageID) {
		return
	}
	fileName, err := cms.SafeAttachmentName(pageID, req.File,
		map[string]bool{".pdf": true}, "解析できるのは .pdf だけです")
	if err != nil {
		cms.JSONFail(w, http.StatusBadRequest, err.Error())
		return
	}

	pdf, err := loadPDFForAnalysis(pageID, fileName)
	if err != nil {
		cms.JSONFail(w, http.StatusBadRequest, err.Error())
		return
	}

	j, err := judgeOrderPDF(pdf)
	if err != nil {
		if errors.Is(err, cms.ErrNoGeminiKey) {
			cms.JSONFail(w, http.StatusServiceUnavailable, "サーバーに GEMINI_API_KEY 環境変数が設定されていません。設定してから起動し直してください。")
			return
		}
		cms.JSONFail(w, http.StatusBadGateway, "解析に失敗しました: "+err.Error())
		return
	}
	attachIDOf := func() string { return strings.TrimSuffix(fileName, filepath.Ext(fileName)) }

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
		user := auth.CurrentUser(r)
		made := []map[string]any{}
		totalMatched := 0
		for _, d := range drawings {
			dj := d.asJudgment()
			matches := MatchDXFAttachments(pageID, dj.DrawingNo)
			totalMatched += len(matches)
			newID, err := cms.CreateChildPage(pageID, user.Username,
				buildProductPageHTML(pageID, attachIDOf(), dj, matches))
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

	attachID := strings.TrimSuffix(fileName, filepath.Ext(fileName))
	newID, err := cms.CreateChildPage(pageID, auth.CurrentUser(r).Username,
		buildOrderPageHTML(pageID, attachID, j))
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "受注ページを作れません: "+err.Error())
		return
	}
	auth.Audit(auth.CurrentUser(r).Username, "analyze-pdf", newID+" from "+pageID+"/"+fileName)

	json.NewEncoder(w).Encode(map[string]any{
		"success": true, "is_client_order": true,
		"page_id": newID, "title": pageTitleOf(newID),
	})
}

// pageTitleOf は索引から題を引きます（引けなければIDをそのまま返す）。
func pageTitleOf(pageID string) string {
	idInt, err := strconv.Atoi(pageID)
	if err != nil {
		return pageID
	}
	if t := cms.PageTitleByID(idInt); t != "" {
		return t
	}
	return pageID
}

// loadPDFForAnalysis は解析対象のPDFの中身を読みます。
func loadPDFForAnalysis(pageID, fileName string) ([]byte, error) {
	path, found := page.AttachmentPath(pageID, fileName)
	if !found {
		return nil, errors.New("添付が見つかりません")
	}
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, errors.New("PDFを読めません")
	}
	return b, nil
}

// judgeOrderPDFWithGemini は判定＋抽出を1コールで行います。
// プロンプト（＝解釈）は本セットの持ち物、呼び出しの型（キー・クライアント・
// フェンス剥がし）はコア（gemini.go）。
func judgeOrderPDFWithGemini(pdf []byte) (*orderJudgment, error) {
	prompt := `このPDFが何の文書かを判定し、種類に応じた項目を抽出してください。
判定する種類は次の3つです:
  - "order"   : 顧客（取引先）が当社宛てに発行した発注書（注文書）
  - "drawing" : 部品や製品の図面（表題欄に図面番号・図面名称があるもの）
  - "other"   : 上記以外（見積書・請求書・納品書・カタログ・案内など）
次の形式のJSONオブジェクトのみを出力してください（マークダウンのコードブロック修飾は付けない）:
{
  "doc_type": "order" または "drawing" または "other",
  "is_client_order": doc_type が "order" のとき true、それ以外は false,
  "order_no": "発注書番号（発注書のとき。記載が無ければ空文字）",
  "customer": "発行元（顧客）の会社名（記載が無ければ空文字）",
  "order_date": "発注日を YYYY-MM-DD 形式で（記載が無ければ空文字）",
  "due_date": "納期（明細の行ではなく、書面の上のほうにある納期）。日付なら YYYY-MM-DD 形式、日付でない書き方（「最短」「最短納期」「至急」「都度」など）は**書かれているまま**返してください。記載が無ければ空文字",
  "source_table": {"headers": ["表の見出しを書かれているまま"], "rows": [["1行ぶんの値を書かれているまま"]]},
  "items": [{"item_no": "品番", "item_name": "品名", "price": "単価（カンマを除いた数値文字列）", "quantity": "数量（数値文字列）", "unit": "数量の単位（個・セットなど。記載が無ければ空文字）"}],
  "drawings": [{"drawing_no": "図面番号", "drawing_name": "図面名称", "machine_name": "装置名称", "customer": "客先"}]
}
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

	respText, err := cms.GeminiGenerate(prompt, genai.Blob{MIMEType: "application/pdf", Data: pdf})
	if err != nil {
		return nil, err
	}
	var j orderJudgment
	if err := json.Unmarshal([]byte(cms.StripJSONFence(respText)), &j); err != nil {
		return nil, errors.New("応答をJSONとして読めません: " + err.Error())
	}
	return &j, nil
}

// buildOrderPageHTML は受注ページの本文を組みます（機能見出し形・D-2）。
// 形はページテンプレートの受注ページと同じ: ヘッダ dl（発注書番号・発注元・発注日）＋
// 明細 table（品番・品名・単価・数量・状態）。状態は「未着手」で始まる（進捗の起点）。
func buildOrderPageHTML(hostPageID, attachID string, j *orderJudgment) string {
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
	//
	// ⚠ **明細の表は `data-type` を自分で持ちます。** それまで節の `Items` 宣言を頼りに
	// 「節の中の素の表」として見つけていましたが、節をやめたので表が自分で名乗ります。
	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	b.WriteString(`<dl data-type="tags">`)
	writeHeaderPair(&b, OrderNoTag, cms.NormalizeNameForIngest(j.OrderNo))
	writeHeaderPair(&b, OrderClientTag, cms.NormalizeNameForIngest(j.Customer))
	writeHeaderPair(&b, OrderedAtTag, j.OrderDate)
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
	writeHeaderPair(&b, DueDateTag, j.DueDate)
	// 由来参照（§9.1）——値は「元ページID-添付ID」。参照タグの文法（ref_render.go）に
	// 一致するのでリンクとして描画され、押すと元ページの該当ブロックへ飛ぶ。
	b.WriteString("<dt>" + SourceRefTag + "</dt><dd>" +
		html.EscapeString(hostPageID+"-"+attachID) + "</dd>")
	b.WriteString("</dl>")
	// ── 顧客の発注書（読んだまま）──
	//
	// ⚠ **畳んで出します**（`<details>`）。ユーザー:「顧客の表は、整理したあとも
	// 残します。**ボタンで畳めれば良い**と思います」。素のHTMLだけで畳めるので、
	// JS も `on*=` も要りません（CSP strict の下で動き、公開ページのゼロJSでも畳めます）。
	//
	// ⚠ **形式を登録していないので索引に載りません**（2026-09-20 の線引き）。
	// 原本は**証拠**であって、検索したいのは弊社の表のほうです——**二重計上も
	// 最初から起きません**。
	b.WriteString(sourceTableHTML(j.SourceTable))
	b.WriteString(`<table data-type="client-order-items"><tbody>`)
	// ⚠ **見出しは宣言から組みます**（2026-09-20）。列を足したのに見出しを手で書いた
	// ままだと、**宣言と本文が黙ってずれます**——索引は見出しの表示文字で引くので、
	// ずれた列はどこからも読めません。`vocab.go` が正本です。
	b.WriteString("<tr>")
	for _, c := range clientOrderItemColumns() {
		b.WriteString("<th>" + html.EscapeString(c.Label) + "</th>")
	}
	b.WriteString("</tr>")
	for _, it := range j.Items {
		// 日付と数値は**正規形で書き起こす**（D-3「正規化は取り込み時に行う」）。
		// 読めなければ生のまま入ります——取り込みは情報を捨てない。
		//
		// ⚠ **弊社品番・納期・備考は空で出します。** 解析には決められません——
		// 弊社品番は人が文脈から結び、納期と備考は発注書の様式しだいです（様式ページの
		// 対応表が入ったら、そこから埋まります）。**書く場所が見えていれば人が埋めます**
		// （図面ブロックで空欄の `客先` を出しているのと同じ理由）。
		b.WriteString("<tr><td></td>" + // 弊社品番（人が結ぶ）
			"<td>" + html.EscapeString(it.ItemNo) + "</td>" +
			"<td>" + html.EscapeString(it.ItemName) + "</td>" +
			"<td>" + html.EscapeString(cms.CanonicalForIngest("数量", it.Quantity)) + "</td>" +
			"<td>" + html.EscapeString(it.Unit) + "</td>" +
			"<td>" + html.EscapeString(cms.CanonicalForIngest("単価", it.Price)) + "</td>" +
			"<td></td>" + // 備考（⚠ 先方の `サイズ` はここへ入ります・様式の対応表が入ったら）
			"<td>未着手</td></tr>")
	}
	b.WriteString("</tbody></table>")
	return b.String()
}

// writeHeaderPair はヘッダ dl の1対を書きます（空値は空欄＝あとから人が埋める）。
//
// 日付・数値の見出し語（発注日 等）は**正規形へ揃えてから**書きます
// （D-3・`cms.CanonicalForIngest`）。図面番号や客先名は揃えません——
// 機械が畳んで書き換えると、原本と見比べたときに食い違うためです。
func writeHeaderPair(b *strings.Builder, name, value string) {
	b.WriteString("<dt>" + html.EscapeString(name) + "</dt>")
	if strings.TrimSpace(value) == "" {
		b.WriteString("<dd><br/></dd>")
	} else {
		b.WriteString("<dd>" + html.EscapeString(cms.CanonicalForIngest(name, value)) + "</dd>")
	}
}

// buildProductPageHTML は加工製品ページの本文を組みます（機能見出し形・D-2）。
//
//	<h1>P103-227-6 台座Assy</h1>
//	<section><h2>図面</h2><dl> 図面番号・図面名称 </dl></section>
//	<dl data-type="tags"> 受信元: <ページID>-<PDFの添付ID>
//	                      対応DXF: <ページID>-<DXFの添付ID>（一致した数だけ繰り返す）
//
// **1通のメールの中でPDFとDXFを対応づけた結果**がこのページです。過去のページを
// 図面番号で探して束ねることはしません——番号は別製品で衝突しうるので、
// 同一性を担うのは常にページID（drawing_match.go 冒頭）。
func buildProductPageHTML(hostPageID, attachID string, j *orderJudgment, matches []matchedDXF) string {
	// 題は「図面番号 図面名称」——**図面名称は重複しうる**ので番号を先に置く。
	//
	// **ブロックと同じ正規化を通します。** 通さないと、題が `シュート先Ｔ金具` で
	// ブロックが `シュート先T金具` という食い違いが出ます（実データで出しました）。
	title := strings.TrimSpace(cms.NormalizeNameForIngest(j.DrawingNo) + " " +
		cms.NormalizeNameForIngest(j.DrawingName))
	if title == "" {
		title = "図面（番号不明）"
	}

	var b strings.Builder
	b.WriteString("<h1>" + html.EscapeString(title) + "</h1>")
	sec := drawingSectionHTML(j, hostPageID, attachID, matches, "")
	b.WriteString(sec)
	// 改訂履歴は**下に**置く（図面は新しいものが上に積まれるので、位置が競合しない）。
	b.WriteString(revisionsSectionHTML(j, sec))
	return b.String()
}

// drawingSectionHTML は図面ブロック1つ分を組みます。
//
// **ブロックIDを付けるのが肝**——参照値 `ページID-ブロックID` は押せばこの
// ブロックへ飛ぶので、これが**その改定の社内コード**になります（2026-09-03 ユーザー:
// 「部品の社内コードは加工製品ページのページ番号と改定番号を足したものになるのでは？
// …すると、社内コードでその項目へ飛べることになります」）。
// 図面番号が別製品と衝突しても、この番号は構造上一意です。
//
// existingBody は採番の重複を避けるための既存本文です（改定で差し込むとき）。
// 由来（受信元・対応DXF）を**ブロックの中**に置くのは、改定で合流させるときに
// ブロックごと運べば出所も一緒に付いて行くようにするためです。
func drawingSectionHTML(j *orderJudgment, hostPageID, attachID string,
	matches []matchedDXF, existingBody string) string {
	// 図面番号も同じ扱いです（2026-09-06）——**人がいちばんコピペする値**なので、
	// 揃わないまま置くと揺れがそこから増えます。畳むのは NFKC までで、
	// `NormalizeCode` は使いません（ハイフンと長音を潰すと読めなくなる）。
	no := cms.NormalizeNameForIngest(j.DrawingNo)
	// **顧客名・装置名称・図面名称は早期に正規化します**（2026-09-06 ユーザー）。
	// この3つはそのままページの題になり、題の完全一致が階層の同一性を決めるので、
	// 畳まずに入れると `φ３２０　共通台座` が別の装置ページになります。
	name := cms.NormalizeNameForIngest(j.DrawingName)

	// ── ここは**可変タグ1つ**です（2026-09-18 ユーザー決定）──
	//
	// ユーザー:「この定義リストを『名前：値のタグ』に変更します。なぜなら、この情報こそ
	// 検索したいものだからです。おそらくもっとも頻繁に検索し、ワンノートでは取りこぼしが
	// 多いので、w-cms を作り始めました」。
	//
	// **それまで素の `<dl>`（業務ブロックのヘッダ）に置いていました。** 索引には入って
	// いましたが `vocab_index` のほうで、**横断検索の口（`PagesByTag`）が読む表ではありません**
	// ——つまり「いちばん検索したい値が、検索の口を持たない表に入っていた」わけです。
	// タグにすると `図面番号` は設定で `code` 型なので、空白・ハイフン・長音・大小を畳んで
	// 引けます（`PagesByTagLoose`）——`P103-227-6` を `P103 227 6` と打っても当たります。
	//
	// ⚠ **見た目がタグと同じで振る舞いが違う、という混乱も消えます。** 素の `<dl>` と
	// `<dl data-type="tags">` は画面では区別が付きませんでした。いまは
	// **「タグと表だけがDBに入る」**の1文で説明できます。
	//
	// ⚠ **空欄でも欄を出します**（`<dd><br/></dd>`）——装置名称と客先は解析が読めない
	// ことがあり、**書く場所が見えていれば人が埋めます**（実データで7枚のうち3枚に
	// `客先` が無かった）。
	var b strings.Builder
	b.WriteString(`<section data-id="` + cms.NewBlockID(existingBody) + `"><h2>図面</h2>`)
	b.WriteString(`<dl data-type="tags">`)
	writeHeaderPair(&b, DrawingNoTag, no)
	writeHeaderPair(&b, DrawingNameTag, name)
	// 装置名称・客先は置き場所（社名／段／装置名称／図面名称）に効く項目。
	writeHeaderPair(&b, MachineNameTag, cms.NormalizeNameForIngest(j.MachineName))
	writeHeaderPair(&b, ClientNameTag, cms.NormalizeNameForIngest(j.Customer))
	b.WriteString("<dt>" + SourceRefTag + "</dt><dd>" +
		html.EscapeString(hostPageID+"-"+attachID) + "</dd>")
	// 一致したDXFを参照タグで指す（押すと元の通信記録ページの該当添付へ飛ぶ）。
	// 一致が無ければ何も書かない——**DXFが無いのも普通**（PDFだけの図面）。
	for _, m := range matches {
		b.WriteString("<dt>対応DXF</dt><dd>" +
			html.EscapeString(hostPageID+"-"+m.AttachID) + "</dd>")
	}
	b.WriteString("</dl>")
	// **図面をここに開く、と本文に書きます**（2026-09-14）。ユーザー:「HTMLに無いものが
	// 表示されるのは極力避けたい」「表示するという意図を伝える名前が良いと思います」。
	//
	// 中身はコアが描きます（`internal/cms/file_view.go`）——**この拡張はPDFの出し方を
	// 知りません**。開くのはコアの機能で、拡張は「ここに開く」と書くだけです。
	// **人が消せます**——消せば図面は出なくなり、参照タグ（出所の記録）は残ります。
	// **配線は属性1つです**（2026-09-15 に中の参照タグから移した）。もとは中へ
	// `受信元` のタグを書いていましたが、それは**すぐ上の図面ブロックにも在る**ので、
	// 同じ値が2つの意味（届いた記録／表示先の指定）で並んでいました。
	b.WriteString(`<section data-type="` + cms.FileViewType + `" ` +
		cms.FileRefAttr + `="` + html.EscapeString(hostPageID+"-"+attachID) +
		`"></section>`)
	b.WriteString("</section>")
	return b.String()
}

// revisionsSectionHTML は改訂履歴のブロックを組みます（1版目）。
//
// **行が社内コードの指し先です**——`ページID-行ID` で押せばその版へ飛びます
// （2026-09-03 ユーザー:「改訂履歴の項目を作り版にdata-idを割り当てれば良いのでは？」）。
// 図面ブロックは消せる決まりなので指し先にせず、消す理由の無い小さな行を指します。
func revisionsSectionHTML(j *orderJudgment, existingBody string) string {
	var b strings.Builder
	b.WriteString(`<section data-id="` + cms.NewBlockID(existingBody) + `"><h2>改訂履歴</h2>`)
	b.WriteString(`<table data-type="` + revisionItemsType + `"><tbody>`)
	b.WriteString("<tr><th>版</th><th>図面番号</th><th>受領日</th></tr>")
	b.WriteString(revisionRowHTML(1, j.DrawingNo, existingBody))
	b.WriteString("</tbody></table></section>")
	return b.String()
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
// （[docs/【考察】発注書から受注明細へ.md] §2.4）。
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

// sourceTableHTML は顧客の発注書の写しを、畳める形で組みます（空なら何も出しません）。
//
// ⚠ **`data-type` を付けません。** 形式を登録していない表は索引に載らない決まりなので
// （2026-09-20）、原本はそのまま「見せるだけ」になります。**caption は人のため**に
// 付けます——畳んだときに何の表か分かるように。
func sourceTableHTML(t orderSourceTable) string {
	if len(t.Headers) == 0 || len(t.Rows) == 0 {
		return "" // ⚠ **読めなければ出しません**（空の枠だけ出しても誤解を生む）
	}
	var b strings.Builder
	b.WriteString(`<details><summary>` + html.EscapeString(sourceTableCaption) + `</summary>`)
	b.WriteString(`<table><caption>` + html.EscapeString(sourceTableCaption) + `</caption><tbody><tr>`)
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
	b.WriteString("</tbody></table></details>")
	return b.String()
}

// sourceTableCaption は原本の写しの見出しです。
//
// ⚠ **語彙に登録しません**（登録すると索引に載り、弊社の明細と二重になります）。
// 人が読むための名前です。
const sourceTableCaption = "顧客の発注書（読んだまま）"
