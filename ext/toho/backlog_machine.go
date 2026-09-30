package toho

// ─────────────────────────────────────────────────────────────────────────
// 受注残表の装置名（2026-09-30）
//
// 利用者:「受注残の表に装置名も入れたい」。
//
// 引く順:
//
//  1. 行の弊社品番が指す加工製品ページの**置き場の装置**（`取引先／社名／加工製品／装置名称／品目` の装置名称・
//     product_tree.go の木の形）——弊社が決めた名前なので第一。
//  2. 結んでいない行は、受注ページの**顧客の発注書（読んだまま）**から——同じ行（品番の記号か品名で当てる）の
//     「機種」「装置」などの列、または**名前が書かれた「部品番号」の列**（客先がそこに装置名を書いてくる・
//     item_no_column.go と同じ見分け方）。
//
// どちらも無ければ空。⚠ **本文には書きません**（受注残表は鏡）——加工製品ページを作って結べば、次に開いたとき 1. になる。
// ─────────────────────────────────────────────────────────────────────────

import (
	"strings"

	stdhtml "html"

	"golang.org/x/net/html"

	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

// machineHeaders は読んだままの表で装置名を持つ列の見出しです（空白・全角半角は畳んで比べる）。
var machineHeaders = map[string]bool{"機種": true, "装置": true, "装置名": true, "装置名称": true, "機械": true, "機械名": true}

// machineLookup は受注残の行の装置名を引く控えです（1回の集計のあいだだけ持つ）。
type machineLookup struct {
	product map[string]string            // 弊社品番 → 置き場の装置名
	source  map[int]*sourceMachineIndex // 受注ページ → 読んだままの表から引いた装置名
}

// sourceMachineIndex は1枚の受注ページの読んだままの表から作った「品番／品名 → 装置名」です。
type sourceMachineIndex struct {
	byCode map[string]string
	byName map[string]string
}

func newMachineLookup() *machineLookup {
	return &machineLookup{product: map[string]string{}, source: map[int]*sourceMachineIndex{}}
}

// machineOf は受注ページ orderID の1行の装置名を返します（無ければ空）。fromProduct は加工製品ページから引いたか。
func (m *machineLookup) machineOf(orderID int, ourItemNo, itemNo, itemName string) (name string, fromProduct bool) {
	if v := m.ofProduct(ourItemNo); v != "" {
		return v, true
	}
	idx := m.ofSource(orderID)
	if itemNo != "" {
		if v := idx.byCode[cms.NormalizeCode(cms.NormalizeNameForIngest(itemNo))]; v != "" {
			return v, false
		}
	}
	if itemName != "" {
		return idx.byName[cms.NormalizeText(cms.NormalizeNameForIngest(itemName))], false
	}
	return "", false
}

// ofProduct は加工製品ページの置き場の装置名です——親の題（親の親が「加工製品」の箱のとき）。
func (m *machineLookup) ofProduct(ourItemNo string) string {
	id, ok := page.NormalizeID(strings.TrimSpace(ourItemNo))
	if !ok {
		return ""
	}
	if v, seen := m.product[id]; seen {
		return v
	}
	v := ""
	if meta, ok := page.ReadSidecar(id); ok && meta.ParentID != "" {
		if pm, ok := page.ReadSidecar(meta.ParentID); ok && pm.ParentID != "" &&
			strings.TrimSpace(pageTitleOf(pm.ParentID)) == ProductsBoxTitle {
			v = strings.TrimSpace(pageTitleOf(meta.ParentID))
		}
	}
	m.product[id] = v
	return v
}

// ofSource は受注ページの読んだままの表から「品番／品名 → 装置名」を作ります（読めなければ空の控え）。
func (m *machineLookup) ofSource(orderID int) *sourceMachineIndex {
	if idx, ok := m.source[orderID]; ok {
		return idx
	}
	idx := &sourceMachineIndex{byCode: map[string]string{}, byName: map[string]string{}}
	m.source[orderID] = idx
	body, err := cms.ReadPageBody(page.FormatID(orderID))
	if err != nil {
		return idx
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return idx
	}
	root := &html.Node{Type: html.ElementNode, Data: "div"}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	src, ok := sourceTableIn(root)
	if !ok {
		return idx
	}
	indexSourceMachines(src, idx)
	return idx
}

// indexSourceMachines は読んだままの表から装置名の列を見つけ、行ごとに「品番の記号・品名 → 装置名」を控えます。
//
// 装置名の列: 見出しが「機種」「装置」など／または「部品番号」の列で、値が名前（記号でない）のもの。
// 品名の列: 見出しに「品名」を含む列。
func indexSourceMachines(src orderSourceTable, idx *sourceMachineIndex) {
	machineCol, nameCol := -1, -1
	for i, h := range src.Headers {
		k := strings.ReplaceAll(cms.NormalizeText(h), " ", "")
		switch {
		case machineHeaders[k]:
			machineCol = i
		case k == "部品番号" && machineCol < 0 && columnHoldsNames(src, i):
			machineCol = i
		case strings.Contains(k, "品名") && nameCol < 0:
			nameCol = i
		}
	}
	if machineCol < 0 {
		return
	}
	for _, row := range src.Rows {
		if machineCol >= len(row) {
			continue
		}
		machine := strings.TrimSpace(row[machineCol])
		if machine == "" {
			continue
		}
		for i, c := range row {
			if i != machineCol && looksLikeCode(c) {
				idx.byCode[cms.NormalizeCode(cms.NormalizeNameForIngest(c))] = machine
			}
		}
		if nameCol >= 0 && nameCol < len(row) {
			if n := strings.TrimSpace(row[nameCol]); n != "" {
				idx.byName[cms.NormalizeText(cms.NormalizeNameForIngest(n))] = machine
			}
		}
	}
}

// columnHoldsNames は列の値が（空を除いて）どれも名前で、記号が1つも無いかです。
func columnHoldsNames(src orderSourceTable, col int) bool {
	seen := 0
	for _, row := range src.Rows {
		if col >= len(row) || strings.TrimSpace(row[col]) == "" {
			continue
		}
		if looksLikeCode(row[col]) || !looksLikeName(row[col]) {
			return false
		}
		seen++
	}
	return seen > 0
}

// machineCellHTML は受注残表の装置名のセルです（どこから引いたかをマウスを載せると言う）。
func machineCellHTML(r backlogRow) string {
	// 紙にも出す（2026-09-30 夜 利用者:「装置名は紙にも出して欲しいです」）。⚠ 見出しと同じ印に揃えること——片方だけ
	// `no-print` だと紙で見出しと値が1つずれる。
	if r.Machine == "" {
		return `<td class="cell-atomic"></td>`
	}
	from := "顧客の発注書（読んだまま）から"
	if r.MachineFromProduct {
		from = "加工製品ページの置き場の装置"
	}
	return `<td class="cell-atomic" title="` + stdhtml.EscapeString(from) + `">` + stdhtml.EscapeString(r.Machine) + `</td>`
}

