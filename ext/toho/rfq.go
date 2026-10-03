package toho

// ─────────────────────────────────────────────────────────────────────────
// 見積依頼——業者へ値段を聞く（2026-10-03・【要求】見積依頼・【考察】見積の依頼と見積書 §2.0c・§2.0d）
//
// 利用者:「まず、見積もりを依頼する部材は見積依頼必要部材表に入ります。ここに入れるには、１.臨時部材表に書き込む。
// 2.再見積依頼フォームの弊社品番テキストボックスに書き込みボタンを押す。…3．受注フォルダの受注残表を読み込み収集する。
// 見積依頼が発注と異なるのは、ロットが同じ品物を重複して依頼する必要が無いことです。…重複するものは見積依頼必要部材表に
// 入れるが重複とわかるように背景を赤くします。そして消すことが出来るようにするのです」。
//
//	見積依頼（トップ直下の置き場）
//	  ├ 再見積依頼（フォーム）      … 弊社品番＋ロット →「集める」              ← この段（1）
//	  ├ 見積依頼の臨時部材表         … 人が書く（行は見積依頼必要部材表にも並ぶ）
//	  ├ 見積依頼必要部材表           … **本文の表**（集めた行が入る・人が数を直せる）——重複は赤＋⚠・「不要」で消す
//	  │    ↓ 選んで・入れる先を選んで →「見積依頼部材表へ入れる」
//	  ├ 見積依頼部材表               … 業者ごとに足し引き（↩ 戻す）
//	  │    ↓ 仕入先・差出人を入れて →「見積依頼書ページを作る」               ← 段 2
//	  └ 年／月／見積依頼 ○○商店     … 見積依頼書ページ（見積依頼明細・紙・送る）
//
// ⚠ **発注の道具を共有します**（table_lines.go——表の種類を引数に）。列は発注明細と同じ（`orderItemColumns`）。
// ⚠ **機械は依頼済みを数えません**——重複は赤くするだけ（利用者の決定・同じ日の朝の「行ごとに数える」は採らなかった）。
// ⚠ **重複を見るのは見積依頼必要部材表の中だけ**（前に出した見積依頼書とは比べない・利用者:「見積依頼必要部材表に同時に
//    同じもので同じロットが出ていたらに赤くしておけば十分」）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

const (
	// RFQBoxTitle は見積依頼の置き場（トップ直下）の題です。
	RFQBoxTitle = "見積依頼"
	// RFQNeedsType は見積依頼必要部材表です（本文の表——集めた行が入る）。
	RFQNeedsType = "rfq-needs"
	// RFQTempPartsType は見積依頼の臨時部材表です（人が書く——行は見積依頼必要部材表にも並ぶ）。
	RFQTempPartsType = "rfq-temp-parts"
	// RFQDraftType は見積依頼部材表です（業者ごとに足し引きする——機械だけが作る）。
	RFQDraftType = "rfq-draft"
	// RFQItemsType は見積依頼明細です（見積依頼書ページの明細——返事の単価もここ）。
	RFQItemsType = "rfq-items"
	// RFQRequoteViewType は再見積依頼のフォームです（弊社品番＋ロット →「集める」）。
	RFQRequoteViewType = "rfq-requote"
)

// rfqLineStatuses は見積依頼明細の状態です（2026-10-02 利用者の選択「発注明細と同じ＋状態」）。
// ⚠ **設定の語彙（table_vocabulary の見積依頼部材表・見積依頼明細）と同じ並び**——変えるときは両方。
func rfqLineStatuses() []string { return []string{"未回答", "回答あり", "辞退"} }

// rfqColumns は見積依頼部材表・見積依頼明細の列です——発注明細と同じ列で、状態の選択肢だけ違う。
// ⚠ **同じ宣言から作ります**（写して書くと、列を足した日に移すときに落ちる）。
func rfqColumns() []cms.VocabColumn {
	cols := orderItemColumns()
	for i := range cols {
		if cols[i].Field == "status" {
			cols[i].Enum = rfqLineStatuses()
		}
	}
	return cols
}

// rfqNeedsColumns は見積依頼必要部材表の列です——発注明細の列から単価と状態を除いたもの（まだ聞いていない）。
func rfqNeedsColumns() []cms.VocabColumn {
	var out []cms.VocabColumn
	for _, c := range orderItemColumns() {
		if c.Field == "cost" || c.Field == "status" {
			continue
		}
		out = append(out, c)
	}
	return out
}

// rfqTempColumns は見積依頼の臨時部材表の列です——発注の臨時部材表の列から単価を除いたもの。
func rfqTempColumns() []cms.VocabColumn {
	var out []cms.VocabColumn
	for _, c := range tempPartsColumns() {
		if c.Field == "cost" {
			continue
		}
		out = append(out, c)
	}
	return out
}

func init() {
	cms.RegisterVocab(
		cms.VocabDef{
			// テンプレート「見積依頼」に人が置く表（スラッシュメニューに出す——テンプレート駆動）。
			Type: RFQNeedsType, DisplayName: "見積依頼必要部材表", Category: "業務", Icon: "📋", Element: "table",
			Columns: rfqNeedsColumns(),
		},
		cms.VocabDef{
			Type: RFQTempPartsType, DisplayName: "見積依頼の臨時部材表", Category: "業務", Icon: "🧺", Element: "table",
			Columns: rfqTempColumns(),
		},
		cms.VocabDef{
			// ⚠ 機械だけが節で包んで作る（発注部材表と同じ）——スラッシュメニューに出さない。
			Type: RFQDraftType, DisplayName: "見積依頼部材表", Category: "業務", Icon: "🛒", Element: "table",
			Hidden: true, Columns: rfqColumns(),
		},
		cms.VocabDef{
			// テンプレート「見積依頼書」に人が置く表。
			Type: RFQItemsType, DisplayName: "見積依頼明細", Category: "業務", Icon: "📨", Element: "table",
			Columns: rfqColumns(),
		},
		cms.VocabDef{
			Type: RFQRequoteViewType, DisplayName: "再見積依頼", Category: "ビュー", Icon: "🔁", Element: "section",
			View: true,
		},
	)
	cms.RegisterView(RFQRequoteViewType, rfqRequoteViewHTML)
	cms.RegisterMirror(RFQNeedsType, cms.MirrorHandlerFunc(renderRFQNeeds))
	cms.RegisterMirror(RFQDraftType, cms.MirrorHandlerFunc(renderRFQDraft))
	cms.RegisterRequiredPage(cms.RequiredPage{
		Title:     RFQBoxTitle,
		Extension: "toho",
		Why: "業者へ出す見積依頼の置き場です（見積依頼必要部材表 → 見積依頼部材表 → 業者ごとの見積依頼書ページ・見積依頼／年／月）。" +
			"弊社が顧客へ出す見積書は「見積」のほうです。",
	})
}

// rfqLineFromValues は、加工製品の構成部品の1行の値（列の名前ごと——`productNeeds` の Values）を1行にします。
func rfqLineFromValues(v map[string]string) ourOrderLine {
	return ourOrderLine{
		Kind: v["種類"], No: v["番号"], ItemID: v["品番"], ItemName: v["品名"], Work: v["加工内容"],
		Material: v["材質"], Shape: v["形状"], Size: v["寸法"], Color: v["表面"], Spec: v["仕様"], Supplied: v["支給"],
		Unit: v["単位"],
	}
}

// rfqDupKey は「同じもの・同じロット」を見分ける鍵です（重複の赤）。
//
// 種類は見ません——臨時部材表には種類の列が無く、同じ材料を書いても集めた行（種類「材料」）と重ならなくなる。
// 加工製品（弊社品番）と受注も見ません——別の品物が同じ材料を同じ数だけ使うなら、同じ業者へ二度聞く必要は無いので重複です。
// 文字は全角半角・前後の空白・大小を畳んで比べます。
func rfqDupKey(ln ourOrderLine) string {
	parts := []string{ln.No, ln.ItemID, ln.ItemName, ln.Work, ln.Material, ln.Shape, ln.Size, ln.Color,
		ln.Spec, ln.Supplied, ln.Quantity, ln.Unit}
	for i, p := range parts {
		parts[i] = strings.ToLower(cms.NormalizeText(p))
	}
	return strings.Join(parts, "\x1f")
}

// rfqTempPartsOf は本文の見積依頼の臨時部材表から、空でない行を読みます。
func rfqTempPartsOf(body string) []tempPart {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return nil
	}
	tables := tablesOfType(nodes, RFQTempPartsType)
	if len(tables) == 0 {
		return nil
	}
	rows := rowsOf(tables[0])
	var out []tempPart
	for i := 1; i < len(rows); i++ {
		ln := lineOfRow(rows[0], rows[i])
		if !lineIsEmpty(ln) {
			out = append(out, tempPart{Row: i, Line: ln})
		}
	}
	return out
}

// countTablesOn はページの本文にある vocabType の表の数です（画面の「何枚目へ足す」の選択肢）。
func countTablesOn(pageID int, vocabType string) int {
	body, err := cms.ReadPageBody(page.FormatID(pageID))
	if err != nil {
		return 0
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return 0
	}
	return len(tablesOfType(nodes, vocabType))
}

// renderRFQNeeds は見積依頼必要部材表に、選ぶ列・重複の印・臨時部材表の行・操作の欄を足します（鏡——本文には残らない）。
func renderRFQNeeds(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
	cms.DropChrome(el)
	box := draftBoxOf(el)
	if box != el {
		cms.DropChrome(box)
	}
	rows := rowsOf(el)
	if len(rows) == 0 {
		return false, nil
	}
	pageID := page.FormatID(ctx.PageID)
	lines := make([]ourOrderLine, len(rows))
	count := map[string]int{}
	for i := 1; i < len(rows); i++ {
		lines[i] = lineOfRow(rows[0], rows[i])
		if !lineIsEmpty(lines[i]) {
			count[rfqDupKey(lines[i])]++
		}
	}
	// 臨時部材表の行も並べる（発注と同じ——書いた行が必要部材表に並び、入れたら臨時部材表から消える）。
	var temps []tempPart
	if body, err := cms.ReadPageBody(pageID); err == nil {
		temps = rfqTempPartsOf(body)
	}
	for _, tp := range temps {
		count[rfqDupKey(tp.Line)]++
	}
	dupMark := `<span class="rfq-dup-mark" title="同じもの・同じ数が、この表の中に2つ以上あります">⚠ 重複</span>`
	addRowChromeCells(el, "rfq-pick", func(row int, tr *html.Node) string {
		ln := lines[row]
		if lineIsEmpty(ln) {
			return ""
		}
		s := `<input type="checkbox" class="rfq-check" data-rfq-row="` + strconv.Itoa(row) + `"/>`
		if count[rfqDupKey(ln)] > 1 {
			s += dupMark
			addClass(tr, "rfq-dup")
		}
		return s
	})
	if len(temps) > 0 {
		// ⚠ **行は節（ノード）で組みます**——`appendHTML` は本文の文脈で解析するので、`<tbody><tr><td>` の札が落ちて、
		//    チェックの欄と文字だけが表の中に残る（ブラウザが表の外へ追い出す・2026-10-03 に E2E で踏んだ）。
		fields := fieldsOfHeader(el, RFQNeedsType)
		tbody := &html.Node{Type: html.ElementNode, Data: "tbody",
			Attr: []html.Attribute{{Key: "class", Val: "vocab-chrome rfq-temp-rows"}}}
		for _, tp := range temps {
			cls := "rfq-temp-row"
			mark := ""
			if count[rfqDupKey(tp.Line)] > 1 {
				cls += " rfq-dup"
				mark = dupMark
			}
			tr := &html.Node{Type: html.ElementNode, Data: "tr", Attr: []html.Attribute{{Key: "class", Val: cls}}}
			for _, f := range fields {
				td := &html.Node{Type: html.ElementNode, Data: "td"}
				if v := orderLineValue(tp.Line, f); v != "" {
					td.AppendChild(&html.Node{Type: html.TextNode, Data: v})
				}
				tr.AppendChild(td)
			}
			pick := &html.Node{Type: html.ElementNode, Data: "td", Attr: []html.Attribute{{Key: "class", Val: "rfq-pick"}}}
			appendHTML(pick, `<input type="checkbox" class="rfq-check" data-rfq-temp-row="`+strconv.Itoa(tp.Row)+
				`"/><span class="rfq-temp-mark">臨時</span>`+mark)
			tr.AppendChild(pick)
			tbody.AppendChild(tr)
		}
		el.AppendChild(tbody)
	}
	form := rfqNeedsFormHTML(pageID, ctx.PageID)
	if box != el {
		appendHTML(box, `<div class="vocab-chrome rfq-form-box" contenteditable="false">`+form+`</div>`)
	} else {
		// 節で包まれていない表（手で置いた表）——表の足元（tfoot）に置く（表の中へ div を直に足さない）。
		appendFootHTML(el, len(fieldsOfHeader(el, RFQNeedsType))+1, "rfq-form-row", form)
	}
	return false, nil
}

// rfqNeedsFormHTML は見積依頼必要部材表の操作の欄です（入れる先・見積依頼部材表へ入れる・不要）。
func rfqNeedsFormHTML(pageID string, pageIDInt int) string {
	var b strings.Builder
	b.WriteString(`<p class="unorder-help">行を選んで「見積依頼部材表へ入れる」を押すと、下に<strong>見積依頼部材表</strong>ができます` +
		`（見積依頼書は<strong>1枚に1社</strong>——同じ業者へ聞くものだけを選ぶ）。` +
		`<strong>⚠ 重複</strong>は同じもの・同じ数が表の中に2つ以上あるもの——要らなければ選んで「不要」で消します。` +
		`数は表を直接直せます（編集モード）。</p>`)
	b.WriteString(`<div class="matsearch-form rfq-needs-form" data-rfq-page="` + pageID + `">`)
	b.WriteString(`<label class="matsearch-field"><span>入れる先</span><select class="matsearch-input" data-rfq="into">` +
		`<option value="">新しく作る</option>`)
	for i := 1; i <= countTablesOn(pageIDInt, RFQDraftType); i++ {
		b.WriteString(`<option value="` + strconv.Itoa(i) + `">` + strconv.Itoa(i) + `枚目の見積依頼部材表へ足す</option>`)
	}
	b.WriteString(`</select></label>`)
	b.WriteString(`<button type="button" class="matsearch-go" data-rfq-move="1">見積依頼部材表へ入れる</button>`)
	b.WriteString(`<button type="button" class="matsearch-go rfq-remove-go" data-rfq-remove="1">🗑 不要（消す）</button>`)
	b.WriteString(`</div><div class="unorder-result" data-rfq-result="1"></div>`)
	return b.String()
}

// renderRFQDraft は見積依頼部材表の各行に「↩ 戻す」を足します（鏡）。見積依頼書ページを作る欄は段2。
func renderRFQDraft(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
	cms.DropChrome(el)
	idx := ctx.Counter(RFQDraftType) + 1
	pageID := page.FormatID(ctx.PageID)
	addRowChromeCells(el, "draft-row-act", func(row int, _ *html.Node) string {
		return `<button type="button" class="chip-btn rfq-draft-back"` +
			` data-rfq-page="` + pageID + `" data-rfq-table="` + strconv.Itoa(idx) + `" data-rfq-row="` + strconv.Itoa(row) + `"` +
			` title="見積依頼必要部材表へ戻します（この行を外します）">↩ 戻す</button>`
	})
	return false, nil
}

// rfqRequoteViewHTML は再見積依頼のフォームです（弊社品番＋ロット →「集める」）。
func rfqRequoteViewHTML(_ *auth.User, pageIDInt int) string {
	return `<h3 class="materials-title">🔁 再見積依頼</h3>` +
		`<p class="unorder-help">弊社品番（加工製品ページの番号）を書いて「集める」を押すと、その加工製品の材料・購入部品・外注加工が` +
		`下の<strong>見積依頼必要部材表</strong>に入ります。数は<strong>ロット × 部材の数量</strong>——ロットが空なら見積計算表の` +
		`ロット（見積計算表がロットごとに何枚もあれば、その数だけ行になる・無ければ1個分）。</p>` +
		`<div class="matsearch-form rfq-collect-form" data-rfq-page="` + page.FormatID(pageIDInt) + `">` +
		`<label class="matsearch-field"><span>弊社品番</span><input type="text" class="matsearch-input" data-rfq="product" placeholder="001234"/></label>` +
		`<label class="matsearch-field"><span>ロット</span><input type="number" min="1" class="matsearch-input" data-rfq="lot" placeholder="空なら見積計算表"/></label>` +
		`<button type="button" class="matsearch-go" data-rfq-collect="1">集める</button>` +
		`</div><div class="unorder-result" data-rfq-collect-result="1"></div>`
}
