package subcon

// ─────────────────────────────────────────────────────────────────────────
// 検算の結果を受注ページに出す——表示のときだけ（2026-09-20）
//
// ユーザー:「顧客の発注書に数量と単価があって、金額や総額もあるなら、検算が
// 出来ると思います」。計算そのものは [checksum.go]、ここは**見せ方**です。
//
// ⚠ **引き金は弊社の受注明細で、原本ではありません。** 原本は `<details>` で
// 畳まれているので、そちらに出すと**開くまで読まれない警告**になります。
// 弊社の明細は常に見えていますし、**検算が守っているのはこの明細の数字**なので
// 筋も通ります。
//
// ⚠ **本文には書きません（鏡型）。** 書き込むと、人が数字を直しても ⚠ が残り、
// 逆に直した結果が間違っていても ⚠ が出ません。**毎回数え直すので、直せば消えます。**
// `class` はサニタイズで落ちるので、そもそも印は鏡でしか付けられません
// （`ref-missing` の薄赤・`row-obsolete` と同じ作り）。
//
// ⚠ **合っているときも黙りません。** 黙ると「検算して合った」と「そもそも検算して
// いない」が見分けられません——E2E が前提の無い項目を「飛ばす」と言うのと同じ理由です。
// ただし文面は控えめにします。**辻褄が合うことは、正しいことを意味しません**
// （揃えて読み違えることはありえます）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/cms"
)

func init() {
	cms.RegisterMirror(clientOrderItemsType, cms.MirrorHandlerFunc(renderOrderChecksum))
}

// renderOrderChecksum は明細の足元に、**検算の結果と結びの気づき**を出します。
func renderOrderChecksum(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
	cms.DropChrome(el)
	span := headerCellCount(el)
	// ⚠ **加工製品ページに結べない行**（2026-09-25・unlinked_orders.go）——
	//    足元の行を足す**前に**数えます（足元の行は空の行に見えるため）。
	unlinked := markUnlinkedRows(ctx.DB, el)
	renderChecksumRows(ctx, el, span)

	// **結びについての気づきも同じ足元に出します**（2026-09-21・[link_item.go]）。
	//
	// ⚠ **検算の早期戻りに巻き込まないこと。** 手で作った受注ページには原本も小計も
	// ありませんが、**弊社品番は結べます**——中に入れると、そういうページで黙ります。
	// ⚠ 場所を分けないのは、**注意書きが画面に散るとどれも読まれなくなる**ためです。
	for _, n := range orderLinkNotes(ctx.DB, ctx.Viewer, el) {
		class := "checksum-note"
		if strings.HasPrefix(n, "⚠") {
			class = "checksum-ng"
		}
		appendChecksumRow(el, span, class, n)
	}
	for _, n := range unlinked {
		appendChecksumRow(el, span, "checksum-ng", n)
	}
	return true, nil
}

// renderChecksumRows は検算の結果だけを足元へ書きます。
func renderChecksumRows(ctx *cms.MirrorContext, el *html.Node, span int) {
	// ⚠ タグは**可変タグ（`dl[data-type="tags"]`）だけ**から読みます（`cms.TagValue`）。
	// 素の `dl`（業務ブロックのヘッダ）に同じ名前があっても別物です
	// （「タグと表だけがDBに入る」の線引きと同じ）。
	root := rootOf(el)
	src, hasSource := sourceTableIn(root)
	sub := cms.TagValue(root, SubtotalTag)
	tax := cms.TagValue(root, TaxTag)
	total := cms.TagValue(root, TotalTag)

	// ⚠ **材料が1つも無ければ黙ります。** 手で作った受注ページに毎回
	// 「検算できません」と出ると、ただの雑音です。
	if !hasSource && sub == "" && tax == "" && total == "" {
		return
	}

	c := checkOrderArithmetic(src, sub, tax, total)

	switch warn := c.Warnings(); {
	case len(warn) > 0:
		for _, w := range warn {
			appendChecksumRow(el, span, "checksum-ng", "⚠ 検算: "+w)
		}
	case c.Checked == 0 && !c.HasSubtotal && !c.HasTotal:
		// 原本はあるのに何も検算できなかった——**黙らずに理由を言います**。
		// たいていは見出しが弊社の知らない言葉で、**様式ページ**が入れば解けます。
		appendChecksumRow(el, span, "checksum-note",
			"検算できません（数量・単価・金額の列と、小計・合計のタグが見つかりません）")
		return // 検算していないので、飛ばした行数も出さない
	default:
		appendChecksumRow(el, span, "checksum-ok", agreedMessage(c))
	}
	// 検算したなら、見なかった行があることも言います（合っていても・合っていなくても）。
	if s := c.Skipped(); s != "" {
		appendChecksumRow(el, span, "checksum-note", s)
	}
}

// agreedMessage は「合っています」の文に、何を検算したかを添えます。
//
// ⚠ **`✓` を付けるのは、色だけに頼らないためです**（2026-09-21）。合格の行は
// 薄い緑、疑わしい行は薄い赤になりましたが、**赤と緑は赤緑色覚でいちばん近づく
// 組み合わせ**です——`e2e/probe-phone.js` と同じ計算で測ると ΔE は
// **正常視 24.4／2型（緑）11.1／1型（赤）9.5**。同スクリプトの物差し
// （10以上で「別の色」と分かる・2以下でほぼ同じ）の、ちょうど境目です。
//
// ⚠ とはいえ**後退ではありません**——それまでの合格は白地＋灰文字で、赤との差は
// **8.5／9.5** でした（白と薄赤）。緑にして落ちてはいません。
// **印の形**は色覚にも白黒印刷にも影響されないので、`⚠` と `✓` の対を
// 最後の見分けとして置いておきます。
func agreedMessage(c orderChecksum) string {
	msg := "✓ 検算: 合っています"
	if c.Checked == 0 {
		return msg
	}
	msg += "（明細" + strconv.Itoa(c.Checked) + "行"
	if c.HasSubtotal {
		msg += "・小計"
	}
	if c.HasSubtotal && c.HasTax && c.HasTotal {
		msg += "・合計"
	}
	return msg + "）"
}

// appendChecksumRow は表の足元に検算の1行を足します（`appendFootRow`）。
func appendChecksumRow(table *html.Node, span int, class, text string) {
	appendFootRow(table, span, "order-checksum "+class, text)
}

// lastChild は直下の同名要素を返します（無ければ nil）。
func lastChild(el *html.Node, name string) *html.Node {
	var got *html.Node
	for c := el.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == name {
			got = c
		}
	}
	return got
}

// headerCellCount は見出し行のセル数を返します（足元の行を横いっぱいに伸ばすため）。
//
// ⚠ **宣言の列数ではなく、本文の見出し行を数えます**——人が列を足すことがあり、
// ずれると足元の行だけ幅が合いません。
func headerCellCount(table *html.Node) int {
	for _, tr := range rowsOf(table) {
		if n := len(cellsOf(tr)); n > 0 {
			return n
		}
	}
	return 1
}

// rootOf は文書（断片）の最上位まで上がります。
func rootOf(el *html.Node) *html.Node {
	n := el
	for n.Parent != nil {
		n = n.Parent
	}
	return n
}

// sourceTableIn は原本の写しの表を探し、見出しと行に開きます。
//
// 見分けるのは **`<caption>` の文字**です（§2.4「見える文字が形式を宣言する」）。
// ⚠ 原本は**語彙に登録していません**（登録すると索引に載り、弊社の明細と
// 二重計上になります）ので、属性では見つけられません。
func sourceTableIn(root *html.Node) (orderSourceTable, bool) {
	table := findElement([]*html.Node{root}, func(n *html.Node) bool {
		if n.Data != "table" {
			return false
		}
		cap := lastChild(n, "caption")
		return cap != nil && strings.TrimSpace(textOf(cap)) == sourceTableCaption
	})
	if table == nil {
		return orderSourceTable{}, false
	}
	var out orderSourceTable
	for i, tr := range rowsOf(table) {
		var cells []string
		for _, c := range cellsOf(tr) {
			cells = append(cells, strings.TrimSpace(textOf(c)))
		}
		if i == 0 {
			out.Headers = cells
			continue
		}
		out.Rows = append(out.Rows, cells)
	}
	return out, len(out.Headers) > 0
}
