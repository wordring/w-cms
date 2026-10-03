package toho

// ─────────────────────────────────────────────────────────────────────────
// 東邦の業務の設定の節（`config/settings.json` の `extensions.toho`・2026-09-15）
//
// ユーザー決定（拡張の組み替え §4.2）:「拡張が自分の節を登録」。それまで段
// （`machine_stages`）は**使うのが下請けだけなのに、型も検査もコアの settings.go** に
// ありました。設定ファイルは1本のまま、読み方と検査をここへ移しています。
//
//	"extensions": {
//	  "toho": { "product_code_tags": ["図面番号", "品番"], … }
//	}
//
// ⚠ **段（`machine_stages`）は 2026-09-29 に無くなりました**——加工製品の木から段のフォルダを
// やめ、試作・見積・旧型はタグ `区分`（選択肢は語彙）にした（product_tree.go）。
// 書いたままの設定は、打ち間違いと同じく起動を止めます（黙って無視しない）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"w-cms/internal/cms"
)

// settingsSection は `extensions.toho` の中身です。
type settingsSection struct {
	// ProductCodeTags は「**加工製品ページを言い当てる番号**」のタグ名です
	// （2026-09-21）。受注明細の `品番` からページを特定するときに、この名前の
	// タグを**順に**引きます。
	//
	// ⚠ **客先によって、番号がどのタグに入るかが違います**——みなと商店は図番で
	// 発注してくるのでページの `図面番号` に当たり、品番で発注してくる客先は
	// `品番` に当たります。図面の無い製品は `品番` しか持ちません。
	// **タグ名を問わず値で当てる**のはそのためです。
	//
	// ⚠ **`code` 型の語にすること**（設定の `vocabulary`）。`text` だと長音を
	// 畳まないので、`レーザー` と `レ-ザ-` のような揺れを越えられません。
	// **未指定なら結びません**（既定の一覧はありません）。
	ProductCodeTags []string `json:"product_code_tags,omitempty"`

	// PDFFont は**発注書のPDFに埋め込む日本語フォント**（`.ttf` のパス）です
	// （2026-09-21）。
	//
	// ⚠ **日本語のPDFはフォントを埋め込まないと読めません。** そして**同梱できるかは
	// ライセンス次第**なので、**パスを設定で指す形**にしました——w-cms は MIT で公開
	// されており、フォントを無条件に同梱はできません。
	//
	// ⚠ **`.ttc`（コレクション）も読めます**（2026-09-23）。gopdf は読めませんが、
	// こちらで**1書体を取り出して**渡します（[ttc.go](ttc.go)）——**Windows の太い
	// 和文フォントは全部 `.ttc`** なので、それまでは**いちばん読みやすいフォントを
	// 締め出して**いました。どの書体かは `pdf_font_face`。
	//
	// ⚠ **細いフォントを指さないこと。** `NotoSansJP-VF.ttf` は**可変フォント**で、
	// `wght` の既定値が **100（Thin）**です——gopdf は可変軸を扱わないので
	// **いちばん細い形が刷られ**、FAXでかすれます（2026-09-23 ユーザー報告）。
	//
	// **未指定ならPDFを作りません**（起動は止めません——Gemini キーと同じ扱いで、
	// 画面に「設定されていません」と出します）。
	PDFFont string `json:"pdf_font,omitempty"`

	// PDFFontFace は `.ttc` の中の何番目の書体かです（既定は0）。
	//
	// ⚠ **コレクションには似た書体が何本も入っています**——`BIZ-UDGothicB.ttc` なら
	// 0 が BIZ UDゴシック B、1 が BIZ UDPゴシック B（プロポーショナル）。
	// **どちらが好みかは人が決めます。**
	PDFFontFace int `json:"pdf_font_face,omitempty"`

	// Company は**発注書の差出人**です（2026-09-21）。実物の発注書に入っていた項目。
	//
	// ⚠ **設定に置きます。** 自社を表すページを作る案もありますが、`取引：自社` の
	// タグは 2026-09-18 に全廃しており（誰も読んでいなかった）、**同じものを2度作る**
	// ことになります。設定なら `git pull` で全環境へ届きます。
	// ⚠ **秘密ではありません**（相手に渡す紙に印刷する情報です）。
	Company companyInfo `json:"company,omitempty"`

	// OrderKinds は**部材の種類ごとの運び方**です（2026-09-28・order_kinds.go の冒頭）——
	// 加工製品ページのどの表から、発注の表のどの列へ運び、何を鍵に手配済みを数えるか。
	// 利用者:「どの列が必要か設定ファイルに書きましょうか？」「一度ご提案通りにやってみましょう」。
	// **未指定なら必要部材表に何も出しません**（既定の種類はありません）。
	OrderKinds []orderKind `json:"order_kinds,omitempty"`

	// OrderPrintColumns は**発注書の紙に刷る列**の候補と並び順です（2026-09-28）。どの行にも値の
	// 無い列は刷りません（【要求】発注フォルダ「PDFを作成するときに空の項目を消す」）。`金額` は
	// 計算の列。利用者:「実際に運用して見ないと私にはわかりません」——使ってみて直す1行。
	// **未指定なら、それまでの並び**（品番・品名・材質・形状・寸法・表面・単位・数量・単価・金額）。
	OrderPrintColumns []string `json:"order_print_columns,omitempty"`

	// RFQPrintColumns は**見積依頼書の紙に刷る列**の候補と並び順です（2026-10-03・rfq_pdf.go）。発注書と同じく、どの行にも
	// 値の無い列は刷らない——ただし `単価` は**いつも空欄で刷る**（業者が書き込む欄）。`金額` は刷らない。
	// **未指定なら発注書の紙の列（order_print_columns）から `金額` を除いた並び**。
	RFQPrintColumns []string `json:"rfq_print_columns,omitempty"`

	// OrderPrintHeads は**発注書の紙の上に刷るタグ**と並び順です（2026-09-28）。発注書ページが
	// そのタグを持っていれば、**値が空でも見出しだけ刷ります**——法で決まった記載事項（検査完了期日・
	// 支払期日・支払方法）を「念のため記載して空欄運用」するため（利用者）。持っていないタグは刷らない。
	// **未指定なら、それまでの4つ**（発注書番号・発注日・納期・納品場所）。
	OrderPrintHeads []string `json:"order_print_heads,omitempty"`

	// EstimateProfitRate は見積計算表の**弊社利益の既定の率**（%）です（2026-10-01・estimate.go）。利用者:「弊社利益は
	// デフォルトで10％なので、単価に1.1をかければ良いのですが、利益率を変更できるようにしたいです」——表ごとの率は表の
	// 「弊社利益」の行が持ち、無い表にこれを使う。**未指定なら確定単価を出しません**（そう書く）。
	EstimateProfitRate *float64 `json:"estimate_profit_rate,omitempty"`

	// EstimateTags は新しい見積書に入れる**タグの既定値**です（2026-10-01・estimate_doc.go）——見本の「取引方法: 従来通り」
	// 「有効期限: 1カ月」。作ったあとは見積書ページで直せる。
	EstimateTags map[string]string `json:"estimate_tags,omitempty"`

	// TaxRate は見積書の紙に刷る**消費税率**（%）です（2026-10-01・見本「下記価格には消費税は含んでおりません。税率 10％」）。
	// 未指定なら税率の行を刷りません。
	TaxRate *float64 `json:"tax_rate,omitempty"`
}

// companyInfo は発注書に刷る差出人です（実物の見出しに合わせた項目）。
type companyInfo struct {
	Name    string `json:"name"`
	Zip     string `json:"zip"`
	Address string `json:"address"`
	Person  string `json:"person"`
	Tel     string `json:"tel"`
	Fax     string `json:"fax"`
}

var (
	// stagesMu は節の全部の値を守ります（名前は段を持っていたころの名残）。
	// ⚠ **1つの錠を共有します。** 設定の反映は1回で全部を差し替えるので、別の錠にすると
	// 「番号のタグは新しいがフォントは古い」という中途半端な瞬間ができます。
	stagesMu          sync.RWMutex
	productCodeTags   []string
	pdfFont           string
	pdfFontFace       int
	companyInf        companyInfo
	orderKinds        []orderKind
	orderPrintColumns []string
	rfqPrintColumns   []string
	orderPrintHeads   []string
	estimateRate      *float64
	estimateTags      map[string]string
	taxRate           *float64
)

func init() {
	cms.RegisterSettingsSection("toho", parseSettings)
}

// parseSettings は節を読んで検査し、効かせる関数を返します（コアが全体の検査のあとに呼ぶ）。
func parseSettings(raw json.RawMessage) (func(), error) {
	var s settingsSection
	if len(raw) > 0 {
		dec := json.NewDecoder(bytes.NewReader(raw))
		// 打ち間違えたキー（machine_stage など）を黙って無視しない——コアと同じ流儀。
		dec.DisallowUnknownFields()
		if err := dec.Decode(&s); err != nil {
			return nil, fmt.Errorf("書式が不正です（手で直してください）: %w", err)
		}
	}
	// ⚠ **重複と空だけ断ります。** 型（`code` であること）はここでは見ません——
	// この検査はコアが語彙を効かせる**前**に走ることがあり、見ると「まだ読み込まれて
	// いない語」を誤って断ります（2026-09-20 に `vocab_formats` で同じ形を踏みました）。
	seenTag := map[string]bool{}
	for _, t := range s.ProductCodeTags {
		v := strings.TrimSpace(t)
		if v == "" {
			return nil, fmt.Errorf("product_code_tags に空のタグ名があります")
		}
		if seenTag[v] {
			return nil, fmt.Errorf("product_code_tags に %q が2回あります", t)
		}
		seenTag[v] = true
	}
	// ⚠ **拡張子だけ見ます**（在るかどうかは見ません——設定を読むのはDB再構築でも
	// 走るので、そのときファイルが一時的に見えない環境で起動を止めたくありません）。
	// ⚠ **`.ttc` も通します**（2026-09-23）——1書体を取り出して渡すので読めます。
	if f := strings.TrimSpace(s.PDFFont); f != "" {
		low := strings.ToLower(f)
		if !strings.HasSuffix(low, ".ttf") && !strings.HasSuffix(low, ".ttc") {
			return nil, fmt.Errorf("pdf_font は .ttf か .ttc を指してください（%q）", s.PDFFont)
		}
	}
	if r := s.EstimateProfitRate; r != nil && (*r < 0 || *r >= 1000) {
		return nil, fmt.Errorf("estimate_profit_rate は 0 以上 1000 未満の %%（%v）", *r)
	}
	if s.PDFFontFace < 0 {
		return nil, fmt.Errorf("pdf_font_face は0以上です（%d）", s.PDFFontFace)
	}
	if err := validateOrderKinds(s.OrderKinds); err != nil {
		return nil, err
	}
	if err := validateOrderPrintColumns(s.OrderPrintColumns); err != nil {
		return nil, err
	}
	if err := validateOrderPrintColumns(s.RFQPrintColumns); err != nil {
		return nil, fmt.Errorf("rfq_print_columns: %w", err)
	}
	kinds := s.OrderKinds
	printCols := s.OrderPrintColumns
	rfqCols := s.RFQPrintColumns
	var heads []string
	for _, h := range s.OrderPrintHeads {
		if h = strings.TrimSpace(h); h != "" {
			heads = append(heads, h)
		}
	}
	codeTags := s.ProductCodeTags
	font := strings.TrimSpace(s.PDFFont)
	face := s.PDFFontFace
	company := s.Company
	rate := s.EstimateProfitRate
	etags := map[string]string{}
	for k, v := range s.EstimateTags {
		if k, v = strings.TrimSpace(k), strings.TrimSpace(v); k != "" && v != "" {
			etags[k] = v
		}
	}
	tax := s.TaxRate
	return func() {
		stagesMu.Lock()
		productCodeTags = codeTags
		pdfFont = font
		pdfFontFace = face
		companyInf = company
		orderKinds = kinds
		orderPrintColumns = printCols
		rfqPrintColumns = rfqCols
		orderPrintHeads = heads
		estimateRate = rate
		estimateTags = etags
		taxRate = tax
		stagesMu.Unlock()
	}, nil
}

// EstimateProfitRate は見積計算表の弊社利益の既定の率（%）を返します（未設定なら ok=false）。
func EstimateProfitRate() (float64, bool) {
	stagesMu.RLock()
	defer stagesMu.RUnlock()
	if estimateRate == nil {
		return 0, false
	}
	return *estimateRate, true
}

// EstimateTagDefaults は新しい見積書に入れるタグの既定値です（写しを返す）。
func EstimateTagDefaults() map[string]string {
	stagesMu.RLock()
	defer stagesMu.RUnlock()
	out := map[string]string{}
	for k, v := range estimateTags {
		out[k] = v
	}
	return out
}

// TaxRate は見積書に刷る消費税率（%）です（未設定なら ok=false）。
func TaxRate() (float64, bool) {
	stagesMu.RLock()
	defer stagesMu.RUnlock()
	if taxRate == nil {
		return 0, false
	}
	return *taxRate, true
}

// PDFFont は発注書のPDFへ埋め込むフォントのパスを返します（未設定なら空）。
func PDFFont() string {
	stagesMu.RLock()
	defer stagesMu.RUnlock()
	return pdfFont
}

// Company は発注書の差出人を返します。
func Company() companyInfo {
	stagesMu.RLock()
	defer stagesMu.RUnlock()
	return companyInf
}

// ProductCodeTags は「加工製品ページを言い当てる番号」のタグ名を返します（並び順つき）。
// 返した配列は書き換えないこと（参照側が共有しています）。
func ProductCodeTags() []string {
	stagesMu.RLock()
	defer stagesMu.RUnlock()
	return productCodeTags
}

// PDFFontFace は `.ttc` の中の何番目の書体を使うかを返します（既定は0）。
func PDFFontFace() int {
	stagesMu.RLock()
	defer stagesMu.RUnlock()
	return pdfFontFace
}
