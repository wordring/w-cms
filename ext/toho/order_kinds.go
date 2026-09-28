package toho

// ─────────────────────────────────────────────────────────────────────────
// 部材の種類ごとの運び方——設定 `extensions.toho.order_kinds`（2026-09-28）
//
// 利用者:「材料と外注加工、その他では、必要な項目が違うため、発注部材表にうまく入りません」
// 「どの列が必要か設定ファイルに書きましょうか？」「設定の形は、一度ご提案通りにやってみましょう。
// それを見ながら考えましょう」。正本は [docs/考察/【考察】部材の種類ごとの発注項目.md] §3・§9。
//
// 種類（材料・外注加工・購入部品・支給部品）ごとに、
//
//   - **from**    … 加工製品ページのどの表から（キャプション）
//   - **columns** … 発注の表（発注部材表・発注明細）のどの列へ、何を運ぶか
//                  （右側は元の表の列の名前・`@タグ`〔加工製品ページのタグ〕・`@弊社品番`・`|` は「無ければ次」）
//   - **key**     … 手配済みを数える鍵（発注の表の列の名前）
//   - **display** … 必要部材表に出す名前（発注の表の列の名前・無ければ key）
//   - **title**   … 紙の題（支給部品は「支給願い」・無ければ「発注書」）
//
// を書きます。**業務の言葉は設定にだけ**あり、コードは読み方を知っているだけです（開発方針 §0）。
// ⚠ 数量は元の表の個数（1台あたり）× 受注数——`columns` の `数量` の右側が個数の列の名前です。
// ⚠ 発注の表の列（`orderItemColumns`）に無い名前へは運べません——検査で断ります。
// ─────────────────────────────────────────────────────────────────────────

import (
	"fmt"
	"strings"

	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// orderKind は種類1つぶんの運び方です（設定 `order_kinds` の1件）。
type orderKind struct {
	Kind    string            `json:"kind"`
	From    string            `json:"from"`
	Key     []string          `json:"key"`
	Display []string          `json:"display,omitempty"`
	Columns map[string]string `json:"columns"`
	Title   string            `json:"title,omitempty"`
}

// orderQtyLabel は発注の表の数量の列の名前です（元の個数 × 受注数を入れる）。
const orderQtyLabel = "数量"

// validateOrderKinds は設定の種類を検査します（書いた時点で気づけるように）。
func validateOrderKinds(kinds []orderKind) error {
	orderLabels := map[string]bool{}
	for _, c := range orderItemColumns() {
		orderLabels[c.Label] = true
	}
	seen := map[string]bool{}
	for i, k := range kinds {
		name := strings.TrimSpace(k.Kind)
		if name == "" {
			return fmt.Errorf("order_kinds の %d 番目に kind がありません", i+1)
		}
		if seen[name] {
			return fmt.Errorf("order_kinds に %q が2回あります", name)
		}
		seen[name] = true
		if strings.TrimSpace(k.From) == "" {
			return fmt.Errorf("order_kinds の %q に from（加工製品ページの表のキャプション）がありません", name)
		}
		if len(k.Columns) == 0 {
			return fmt.Errorf("order_kinds の %q に columns がありません", name)
		}
		for col := range k.Columns {
			if !orderLabels[col] {
				return fmt.Errorf("order_kinds の %q の columns にある %q は、発注の表の列にありません", name, col)
			}
		}
		if len(k.Key) == 0 {
			return fmt.Errorf("order_kinds の %q に key（手配済みを数える鍵）がありません", name)
		}
		for _, l := range append(append([]string{}, k.Key...), k.Display...) {
			if _, ok := k.Columns[l]; !ok {
				return fmt.Errorf("order_kinds の %q の key・display にある %q が columns にありません", name, l)
			}
		}
	}
	return nil
}

// validateOrderPrintColumns は紙に刷る列を検査します（発注の表の列か「金額」）。
func validateOrderPrintColumns(cols []string) error {
	ok := map[string]bool{"金額": true}
	for _, c := range orderItemColumns() {
		ok[c.Label] = true
	}
	for _, c := range cols {
		if !ok[c] {
			return fmt.Errorf("order_print_columns の %q は、発注の表の列にありません", c)
		}
	}
	return nil
}

// OrderKinds は設定の種類を並び順どおりに返します（書き換えないこと）。
func OrderKinds() []orderKind {
	stagesMu.RLock()
	defer stagesMu.RUnlock()
	return orderKinds
}

// orderKindByName は種類を名前で引きます。
func orderKindByName(name string) (orderKind, bool) {
	name = strings.TrimSpace(name)
	for _, k := range OrderKinds() {
		if k.Kind == name {
			return k, true
		}
	}
	return orderKind{}, false
}

// OrderPrintColumns は紙に刷る列の候補を並び順どおりに返します（空なら既定の並び）。
func OrderPrintColumns() []string {
	stagesMu.RLock()
	defer stagesMu.RUnlock()
	return orderPrintColumns
}

// labelValue は表の行から、列の**見出しの名前**で値を読みます（登録された列は機械キーで入っている）。
func labelValue(def cms.VocabDef, row cms.VocabRow, label string) string {
	for _, c := range def.Columns {
		if c.Label == label && c.Field != "" {
			return strings.TrimSpace(row.Values[c.Field])
		}
	}
	return strings.TrimSpace(row.Values[label])
}

// resolveSource は columns の右側（`名前`・`@タグ`・`@弊社品番`・`|` は「無ければ次」）を解きます。
func resolveSource(src string, def cms.VocabDef, row cms.VocabRow, tags map[string][]string, productID int) string {
	for _, alt := range strings.Split(src, "|") {
		alt = strings.TrimSpace(alt)
		v := ""
		switch {
		case alt == "@弊社品番":
			v = page.FormatID(productID)
		case strings.HasPrefix(alt, "@"):
			v = cms.FirstTag(tags, strings.TrimPrefix(alt, "@"))
		default:
			v = labelValue(def, row, alt)
		}
		if v = strings.TrimSpace(v); v != "" {
			return v
		}
	}
	return ""
}

// valuesOf は元の表の1行から、発注の表の列ごとの値を組みます（数量は除く——呼び手が掛ける）。
func (k orderKind) valuesOf(def cms.VocabDef, row cms.VocabRow, tags map[string][]string, productID int) map[string]string {
	out := map[string]string{"種類": k.Kind}
	for col, src := range k.Columns {
		if col == orderQtyLabel {
			continue
		}
		out[col] = resolveSource(src, def, row, tags, productID)
	}
	return out
}

// perUnit は1台あたりの個数です（元の表の列が空なら1——`cms.VocabQuantity` と同じ）。
func (k orderKind) perUnit(def cms.VocabDef, row cms.VocabRow) int {
	src, ok := k.Columns[orderQtyLabel]
	if !ok {
		return 1
	}
	v := labelValue(def, row, strings.TrimSpace(src))
	if v == "" {
		return 1
	}
	return cms.VocabNumber(v)
}

// keyOf は手配済みを数える鍵です——種類の名前＋鍵の列の値（畳んだ文字）。鍵の列が全部空なら空。
//
// ⚠ **発注部材表・発注書の行も同じ関数で鍵を作ります**（`orderRowKey`）——別の作り方をすると
// 引き算が合わず、手配した部材が必要部材表に残ります（二重発注）。
func (k orderKind) keyOf(vals map[string]string) string {
	parts := make([]string, len(k.Key))
	any := false
	for i, l := range k.Key {
		parts[i] = cms.NormalizeText(strings.TrimSpace(vals[l]))
		if parts[i] != "" {
			any = true
		}
	}
	if !any {
		return ""
	}
	return k.Kind + "\x1f" + strings.Join(parts, "\x1f")
}

// displayOf は必要部材表に出す名前です（display の列、無ければ key の列の値を空白で繋ぐ）。
func (k orderKind) displayOf(vals map[string]string) string {
	labels := k.Display
	if len(labels) == 0 {
		labels = k.Key
	}
	var parts []string
	for _, l := range labels {
		if v := strings.TrimSpace(vals[l]); v != "" {
			parts = append(parts, v)
		}
	}
	return strings.Join(parts, " ")
}

// orderRowKey は発注部材表・発注明細の1行の、手配済みを数える鍵です（`orderKind.keyOf` と同じ）。
//
// ⚠ **種類の無い古い行**（2026-09-28 より前）は、**鍵の列のどれかに値がある最初の種類**として読みます
// ——材料の行（材質・形状・寸法）は材料、品名だけの行は購入部品になります（それまでの読み方と同じ）。
func orderRowKey(def cms.VocabDef, row cms.VocabRow) string {
	vals := map[string]string{}
	for _, c := range def.Columns {
		vals[c.Label] = labelValue(def, row, c.Label)
	}
	if k, ok := orderKindByName(vals["種類"]); ok {
		return k.keyOf(vals)
	}
	for _, k := range OrderKinds() {
		if key := k.keyOf(vals); key != "" {
			return key
		}
	}
	return ""
}

// orderLineAttrs は、発注の表の列の名前と、画面の行が運ぶ属性（`data-*`）・`ourOrderLine` の
// JSON の名前の対応です（必要部材表の行 → 画面 → 発注部材表）。
var orderLineAttrs = []struct{ Label, Attr string }{
	{"種類", "kind"}, {"番号", "no"}, {"品番", "itemid"}, {"品名", "itemname"}, {"加工内容", "work"},
	{"材質", "material"}, {"形状", "shape"}, {"寸法", "size"}, {"表面", "color"},
	{"仕様", "spec"}, {"支給", "supplied"},
}
