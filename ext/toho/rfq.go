package toho

// ─────────────────────────────────────────────────────────────────────────
// 部材見積依頼——業者へ値段を聞く（2026-10-03・【要求】部材見積依頼・【考察】見積の依頼と見積書 §2.0c・§2.0d）
//
// 利用者:「まず、見積もりを依頼する部材は見積依頼必要部材表に入ります。ここに入れるには、１.臨時部材表に書き込む。
// 2.再見積依頼フォームの弊社品番テキストボックスに書き込みボタンを押す。…3．受注フォルダの受注残表を読み込み収集する。
// 見積依頼が発注と異なるのは、ロットが同じ品物を重複して依頼する必要が無いことです。…重複するものは見積依頼必要部材表に
// 入れるが重複とわかるように背景を赤くします。そして消すことが出来るようにするのです」。
//
//	部材見積依頼（トップ直下の置き場）
//	  ├ 再部材見積依頼（フォーム）      … 弊社品番＋ロット →「集める」              ← この段（1）
//	  ├ 部材見積依頼の臨時部材表         … 人が書く（行は見積依頼必要部材表にも並ぶ）
//	  ├ 見積依頼必要部材表           … **本文の表**（集めた行が入る・人が数を直せる）——重複は赤＋⚠・「不要」で消す
//	  │    ↓ 選んで・入れる先を選んで →「見積依頼部材表へ入れる」
//	  ├ 見積依頼部材表               … 業者ごとに足し引き（↩ 戻す）
//	  │    ↓ 仕入先・差出人を入れて →「部材見積依頼書ページを作る」               ← 段 2
//	  └ 年／月／部材見積依頼 ○○商店     … 部材見積依頼書ページ（部材見積依頼明細表・紙・送る）
//
// ⚠ **発注の道具を共有します**（table_lines.go——表の種類を引数に）。列は部材発注明細表と同じ（`orderItemColumns`）。
// ⚠ **機械は依頼済みを数えません**——重複は赤くするだけ（利用者の決定・同じ日の朝の「行ごとに数える」は採らなかった）。
// ⚠ **重複を見るのは見積依頼必要部材表の中だけ**（前に出した部材見積依頼書とは比べない・利用者:「見積依頼必要部材表に同時に
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
	// RFQBoxTitle は部材見積依頼の置き場（トップ直下）の題です。
	RFQBoxTitle = "部材見積依頼"
	// RFQNeedsType は見積依頼必要部材表です（本文の表——集めた行が入る）。
	RFQNeedsType = "rfq-needs"
	// RFQTempPartsType は部材見積依頼の臨時部材表です（人が書く——行は見積依頼必要部材表にも並ぶ）。
	RFQTempPartsType = "rfq-temp-parts"
	// RFQDraftType は見積依頼部材表です（業者ごとに足し引きする——機械だけが作る）。
	RFQDraftType = "rfq-draft"
	// RFQItemsType は部材見積依頼明細表です（部材見積依頼書ページの明細——返事の単価もここ）。
	RFQItemsType = "rfq-items"
	// RFQRequoteViewType は再部材見積依頼のフォームです（弊社品番＋ロット →「集める」）。
	RFQRequoteViewType = "rfq-requote"
)

// rfqLineStatuses は部材見積依頼明細表の状態です（2026-10-02 利用者の選択「発注明細と同じ＋状態」）。
// ⚠ **設定の語彙（table_vocabulary の見積依頼部材表・部材見積依頼明細表）と同じ並び**——変えるときは両方。
func rfqLineStatuses() []string { return []string{"未回答", "回答あり", "辞退"} }

// rfqColumns は見積依頼部材表・部材見積依頼明細表の列です——部材発注明細表と同じ列で、状態の選択肢だけ違う。
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

// rfqLotColumn は見積依頼必要部材表・見積依頼部材表の `ロット` の列です（2026-10-14）。
//
// 【要求】部材見積依頼 §2:「重複とみなすのは、見積依頼必要部材表の中に、同じものが同じロットで同時に出ているときだけ」。
// 重複をロットで見るには、行がロットを持っていなければなりません——再部材見積依頼フォーム（弊社品番＋ロット）と受注残表
// （受注残の数）から集めた行に、集めたときのロットを残します。見積依頼部材表にも置くのは、「↩ 戻す」で戻した行が
// ロットを失わないため。部材見積依頼明細表（紙）には置きません（紙の列は部材発注書と同じ・数量で足りる）。
// ⚠ 型は設定（`config/settings.json` の table_vocabulary）と同じ number——変えるときは両方。
func rfqLotColumn() cms.VocabColumn {
	return cms.VocabColumn{Field: "lot", Label: "ロット", Type: cms.ColNumber}
}

// withLotBeforeQuantity は列の並びの `数量` の前に `ロット` を挟みます（`数量` が無ければ末尾）。
func withLotBeforeQuantity(cols []cms.VocabColumn) []cms.VocabColumn {
	out := make([]cms.VocabColumn, 0, len(cols)+1)
	put := false
	for _, c := range cols {
		if c.Field == "quantity" && !put {
			out = append(out, rfqLotColumn())
			put = true
		}
		out = append(out, c)
	}
	if !put {
		out = append(out, rfqLotColumn())
	}
	return out
}

// rfqDraftColumns は見積依頼部材表の列です——部材見積依頼明細表の列（部材発注明細表と同じ）に `ロット` を足したもの。
func rfqDraftColumns() []cms.VocabColumn {
	return withLotBeforeQuantity(rfqColumns())
}

// rfqNeedsColumns は見積依頼必要部材表の列です——部材発注明細表の列から単価と状態を除き（まだ聞いていない）、`ロット` を足したもの。
func rfqNeedsColumns() []cms.VocabColumn {
	var out []cms.VocabColumn
	for _, c := range orderItemColumns() {
		if c.Field == "cost" || c.Field == "status" {
			continue
		}
		out = append(out, c)
	}
	return withLotBeforeQuantity(out)
}

// rfqTempColumns は部材見積依頼の臨時部材表の列です——発注の臨時部材表の列から単価を除いたもの。
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
			// テンプレート「部材見積依頼」に人が置く表（スラッシュメニューに出す——テンプレート駆動）。
			Type: RFQNeedsType, DisplayName: "見積依頼必要部材表", Category: "業務", Icon: "📋", Element: "table",
			Columns: rfqNeedsColumns(),
		},
		cms.VocabDef{
			Type: RFQTempPartsType, DisplayName: "部材見積依頼の臨時部材表", Category: "業務", Icon: "🧺", Element: "table",
			Columns: rfqTempColumns(),
		},
		cms.VocabDef{
			// ⚠ 機械だけが節で包んで作る（発注部材表と同じ）——スラッシュメニューに出さない。
			Type: RFQDraftType, DisplayName: "見積依頼部材表", Category: "業務", Icon: "🛒", Element: "table",
			Hidden: true, Columns: rfqDraftColumns(),
		},
		cms.VocabDef{
			// テンプレート「部材見積依頼書」に人が置く表。
			Type: RFQItemsType, DisplayName: "部材見積依頼明細表", Category: "業務", Icon: "📨", Element: "table",
			Columns: rfqColumns(),
		},
		cms.VocabDef{
			Type: RFQRequoteViewType, DisplayName: "再部材見積依頼", Category: "ビュー", Icon: "🔁", Element: "section",
			View: true,
		},
	)
	cms.RegisterView(RFQRequoteViewType, rfqRequoteViewHTML)
	cms.RegisterMirror(RFQNeedsType, cms.MirrorHandlerFunc(renderRFQNeeds))
	cms.RegisterMirror(RFQDraftType, cms.MirrorHandlerFunc(renderRFQDraft))
	cms.RegisterRequiredPage(cms.RequiredPage{
		Title:     RFQBoxTitle,
		Extension: "toho",
		Why: "業者へ出す部材見積依頼の置き場です（見積依頼必要部材表 → 見積依頼部材表 → 業者ごとの部材見積依頼書ページ・部材見積依頼／年／月）。" +
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
// 【要求】部材見積依頼 §2:「重複とみなすのは、見積依頼必要部材表の中に、同じものが同じロットで同時に出ているときだけ」
// （2026-10-14 に、それまでの「同じもの・同じ数」から改めた）。
//
// 「同じもの」は部材を表す列（番号・品番・品名・加工内容・材質・形状・寸法・表面・仕様・支給）です。
// 種類は見ません——臨時部材表には種類の列が無く、同じ材料を書いても集めた行（種類「材料」）と重ならなくなる。
// 加工製品（弊社品番）と受注も見ません——別の品物が同じ材料を同じロットで使うなら、同じ業者へ二度聞く必要は無いので重複です。
// 数量も見ません——同じロットでも、加工製品ごとに1個あたりの数が違えば数量は違います。
//
// ⚠ **ロットの分からない行**（臨時部材表に手で書いた行・ロットの列が無かった頃の行）は、ロットの代わりに**数量と単位**で
// 比べます（それまでの見方）。ロットの分かる行とは重ねません（ロットと数は比べられない）。
// 文字は全角半角・前後の空白・大小を畳んで比べます。
func rfqDupKey(ln ourOrderLine) string {
	fold := func(s string) string { return strings.ToLower(cms.NormalizeText(s)) }
	parts := []string{ln.No, ln.ItemID, ln.ItemName, ln.Work, ln.Material, ln.Shape, ln.Size, ln.Color,
		ln.Spec, ln.Supplied}
	for i, p := range parts {
		parts[i] = fold(p)
	}
	if lot := fold(ln.Lot); lot != "" {
		parts = append(parts, "ロット", lot)
	} else {
		parts = append(parts, "数", fold(ln.Quantity), fold(ln.Unit))
	}
	return strings.Join(parts, "\x1f")
}

// ensureLotColumn は本文の n 枚目（1始まり）の vocabType の表に `ロット` の見出しが無ければ、`数量` の前（無ければ末尾）に
// 列を足し、ほかの行にも空のセルを挟みます（2026-10-14）。
//
// ⚠ **ロットの列が無かった頃の表**（見積依頼必要部材表・見積依頼部材表）へ、ロットの分かる行を足すときに使います——
// 列が無いまま足すと、見出しに合わせて並べる（`fieldsOfHeader`）ので、ロットが黙って落ちます。
// 表が見つからない・既に列がある・壊れて読めないときは本文をそのまま返します。
func ensureLotColumn(body, vocabType string, n int) string {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body
	}
	tables := tablesOfType(nodes, vocabType)
	if n < 1 || n > len(tables) {
		return body
	}
	rows := rowsOf(tables[n-1])
	if len(rows) == 0 || len(cellsOf(rows[0])) == 0 {
		return body
	}
	idx := headerIndex(rows[0])
	label := rfqLotColumn().Label
	if _, ok := idx[label]; ok {
		return body
	}
	at := -1
	if i, ok := idx["数量"]; ok {
		at = i
	}
	for r, tr := range rows {
		tag := "td"
		if r == 0 {
			tag = cellsOf(tr)[0].Data // 見出しの行は th（手で直した表が td で書いていても揃える）
		}
		cell := &html.Node{Type: html.ElementNode, Data: tag}
		if r == 0 {
			cell.AppendChild(&html.Node{Type: html.TextNode, Data: label})
		}
		cells := cellsOf(tr)
		if at >= 0 && at < len(cells) {
			tr.InsertBefore(cell, cells[at])
		} else {
			tr.AppendChild(cell)
		}
	}
	return htmldoc.Render(nodes)
}

// appendRFQLines は本文の n 枚目の vocabType の表（見積依頼必要部材表・見積依頼部材表）へ行を足します。
// ロットの分かる行があり、表にロットの列が無ければ、先に列を足します（`ensureLotColumn`）。
func appendRFQLines(body, vocabType string, n int, lines []ourOrderLine) (string, int, bool) {
	for _, ln := range lines {
		if strings.TrimSpace(ln.Lot) != "" {
			body = ensureLotColumn(body, vocabType, n)
			break
		}
	}
	return appendLinesToTable(body, vocabType, n, lines)
}

// rfqTempPartsOf は本文の部材見積依頼の臨時部材表から、空でない行を読みます。
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
	dupMark := `<span class="rfq-dup-mark" title="同じもの・同じロットが、この表の中に2つ以上あります（ロットの無い行は同じ数）">⚠ 重複</span>`
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
		`（部材見積依頼書は<strong>1枚に1社</strong>——同じ業者へ聞くものだけを選ぶ）。` +
		`<strong>⚠ 重複</strong>は同じもの・同じロットが表の中に2つ以上あるもの（ロットの無い行は同じ数で見ます）——要らなければ選んで「不要」で消します。` +
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

// renderRFQDraft は見積依頼部材表の各行に「↩ 戻す」を足します（鏡）。部材見積依頼書ページを作る欄は段2。
func renderRFQDraft(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
	cms.DropChrome(el)
	idx := ctx.Counter(RFQDraftType) + 1
	pageID := page.FormatID(ctx.PageID)
	addRowChromeCells(el, "draft-row-act", func(row int, _ *html.Node) string {
		return `<button type="button" class="chip-btn rfq-draft-back"` +
			` data-rfq-page="` + pageID + `" data-rfq-table="` + strconv.Itoa(idx) + `" data-rfq-row="` + strconv.Itoa(row) + `"` +
			` title="見積依頼必要部材表へ戻します（この行を外します）">↩ 戻す</button>`
	})
	// 「部材見積依頼書ページを作る」の欄は表の外（包む節の中・表の直後）へ——発注部材表の「部材発注書を作る」と同じ（rfq_doc.go）。
	box := draftBoxOf(el)
	cms.DropChrome(box)
	appendHTML(box, `<div class="vocab-chrome draft-form-box" contenteditable="false">`+
		rfqDocFormHTML(ctx.Viewer, pageID, idx)+`</div>`)
	return false, nil
}

// rfqRequoteViewHTML は再部材見積依頼のフォームです（弊社品番＋ロット →「集める」）。
func rfqRequoteViewHTML(_ *auth.User, pageIDInt int) string {
	return `<h3 class="materials-title">🔁 再部材見積依頼</h3>` +
		`<p class="unorder-help">弊社品番（加工製品ページの番号）を書いて「集める」を押すと、その加工製品の材料・購入部品・外注加工が` +
		`下の<strong>見積依頼必要部材表</strong>に入ります。数は<strong>ロット × 部材の数量</strong>——ロットが空なら見積表の` +
		`ロット（見積表がロットごとに何枚もあれば、その数だけ行になる・無ければ1個分）。</p>` +
		`<div class="matsearch-form rfq-collect-form" data-rfq-page="` + page.FormatID(pageIDInt) + `">` +
		`<label class="matsearch-field"><span>弊社品番</span><input type="text" class="matsearch-input" data-rfq="product" placeholder="001234"/></label>` +
		`<label class="matsearch-field"><span>ロット</span><input type="number" min="1" class="matsearch-input" data-rfq="lot" placeholder="空なら見積表"/></label>` +
		`<button type="button" class="matsearch-go" data-rfq-collect="1">集める</button>` +
		`</div><div class="unorder-result" data-rfq-collect-result="1"></div>` +
		// 装置フォルダから選ぶ（2026-10-03）——利用者:「装置フォルダのページ番号を入力する欄を作り、その下にある加工製品を
		// 列挙する一時的な表を作ります。その表でチェックした加工製品から見積依頼必要部材表に追加する」。表は画面が組む
		// （本文に残さない・app.js の wireRFQ）。番号はブラウザに憶える（data-w-remember）——入れたあとに読み直しても、
		// 同じ装置の残りを続けて選べる。
		`<p class="unorder-help">装置フォルダのページ番号を書いて「一覧を出す」を押すと、その下の加工製品が並びます。` +
		`チェックしたものを、それぞれのロット（空なら見積表のロット）で見積依頼必要部材表へ入れます。</p>` +
		`<div class="matsearch-form rfq-folder-form" data-rfq-page="` + page.FormatID(pageIDInt) + `" data-w-remember="1">` +
		`<label class="matsearch-field"><span>装置フォルダ</span><input type="text" class="matsearch-input" data-rfq="folder" placeholder="001234"/></label>` +
		`<button type="button" class="matsearch-go" data-rfq-folder="1">一覧を出す</button>` +
		`</div><div class="rfq-folder-list" data-rfq-folder-list="1"></div>`
}
