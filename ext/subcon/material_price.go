package subcon

// ─────────────────────────────────────────────────────────────────────────
// 材料の参考単価を、材料表の右に出す——表示のときだけ（2026-09-21）
//
// ユーザー:「材料の単価は**検索して最新情報をひくべきもの**で、本来はここに
// あるべきものではありません。しかしながら、ワンノートには検索して表示する機能が
// 無いので参考価格を書いていました」。
//
// ⚠ **本文には書きません（鏡型）。** 単価は時間とともに変わり、仕様（材質・形状・
// 寸法）は変わりません。**変わるものと変わらないものを同じ行に書くと、行ごと
// 古くなります**——そして直す人は現れない、というのがワンノートの実データが
// 示していることです。鏡なら開くたびに引き直します。
//
// ⚠ **古い参考価格は消しません**（ユーザー決定）。購入記録がまだ無いので、
// 移植した `単価（ロット1）みなと` などが**唯一の価格記録**です。可変列は索引に
// 入るので、**残したまま引ける形になっています**——消す必要がそもそもありません。
// ただし**「最新単価」の欄には出しません**。日付の無い数字は、古いほど危ない
// ——「あるのに古い」は「無い」より悪い結果を生みます（その値で見積もって受注
// すると、損が出るまで誰も気づかない）。
//
// 正本は [docs/【考察】材料の単価と価格の履歴.md]。
// ─────────────────────────────────────────────────────────────────────────

import (
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// 参考単価の列です。⚠ **見出しの表示文字が鍵**なので、直すと画面の言葉も変わります。
const (
	priceColLabel    = "最新単価"
	priceDateLabel   = "時点"
	priceSupplierCol = "仕入先"
)

// materialPrice は「その材料を最後にいくらで買ったか」です。
type materialPrice struct {
	Cost     int    // 単価（発注明細の `単価`）
	Date     string // 発注日（読めなければ空）
	Supplier string // 仕入先
	PageID   int    // 出所の発注書ページ
}

// renderMaterialPrices は材料表の右へ「最新単価・時点・仕入先」を足します。
//
// ⚠ **引けなかったことを黙りません。** 空欄にすると「単価が出ない」と
// 「そんな材料は買ったことがない」が見分けられません。
func renderMaterialPrices(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
	rows := rowsOf(el)
	if len(rows) < 2 {
		return true, nil // 見出しだけ・空の表には足さない（雑音になる）
	}
	head := rows[0]
	mi := headerIndexOf(head, "材質")
	si := headerIndexOf(head, "形状")
	zi := headerIndexOf(head, "寸法")
	if mi < 0 && si < 0 && zi < 0 {
		return true, nil // 材料の表に見えない（人が列を入れ替えた等）
	}

	prices, err := latestMaterialPrices(ctx.DB, ctx.Viewer)
	if err != nil {
		// ⚠ **引けないことは、表を見せない理由になりません。** 本文は正本で、
		//    参考単価はおまけです（応答の一部が読めないだけで全体を捨てない、と
		//    同じ判断）。
		return true, nil
	}

	appendPriceCell(head, "th", priceColLabel)
	appendPriceCell(head, "th", priceDateLabel)
	appendPriceCell(head, "th", priceSupplierCol)

	for _, tr := range rows[1:] {
		key := materialKeyOf(cellText(tr, mi), cellText(tr, si), cellText(tr, zi))
		switch p, ok := prices[key]; {
		case key == "":
			// ⚠ **空の鍵で引き当てない。** 材質・形状・寸法がすべて空の行は
			//    実データに在ります（ワンノートの材料表は空のことがある）。
			//    引き当てにいくと **`"" == ""` で無関係な発注に当たります**。
			appendPriceNote(tr, "⚠ 材質・形状・寸法が空です")
		case !ok:
			appendPriceNote(tr, "⚠ 買った記録がありません")
		default:
			appendPriceCell(tr, "td", comma(p.Cost)+"円")
			appendPriceCell(tr, "td", orDash(p.Date))
			appendPriceCell(tr, "td", orDash(p.Supplier))
		}
	}
	return true, nil
}

// latestMaterialPrices は**全社の発注明細**から、材料ごとの最新単価を集めます。
//
// ⚠ **スコープは `RelatedPages` ではありません。** 前に同じ材料を買ったのは
// まったく別の受注かもしれないので、**関係の有無にかかわらず記録すべて**を当たります。
//
// ⚠ **読めないページは混ぜません。** 材料表には誰でも行を書けるので、絞らないと
// **読めない発注書の単価と仕入先が引けてしまいます**（設計総点検で踏んだ穴と同じ形）。
func latestMaterialPrices(db cms.ReadOnlyDB, viewer *auth.User) (map[string]materialPrice, error) {
	rows, err := cms.VocabRowsOfType(db, ourOrderItemsType)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return map[string]materialPrice{}, nil
	}

	// 発注日と仕入先は**発注書ページのタグ**です（明細の列ではありません）。
	tags, err := cms.TagRowsNamed(db, OrderedAtTag, SupplierTag)
	if err != nil {
		return nil, err
	}
	dateOf, supplierOf := map[int]string{}, map[int]string{}
	for _, t := range tags {
		switch t.Name {
		case OrderedAtTag:
			if _, dup := dateOf[t.PageID]; !dup {
				dateOf[t.PageID] = t.Value
			}
		case SupplierTag:
			if _, dup := supplierOf[t.PageID]; !dup {
				supplierOf[t.PageID] = t.Value
			}
		}
	}

	visible := map[int]bool{}
	canView := func(id int) bool {
		if v, ok := visible[id]; ok {
			return v
		}
		v := page.CanView(viewer, id)
		visible[id] = v
		return v
	}

	out := map[string]materialPrice{}
	for _, r := range rows {
		if !canView(r.PageID) {
			continue
		}
		key := materialKeyOf(r.Values["material"], r.Values["shape"], r.Values["size"])
		if key == "" {
			continue // 鍵にならない行（空の鍵で引き当てない）
		}
		cost := r.Num("cost")
		if cost <= 0 {
			continue // 単価の無い行は「いくらで買ったか」を語れない
		}
		p := materialPrice{Cost: cost, Date: dateOf[r.PageID],
			Supplier: supplierOf[r.PageID], PageID: r.PageID}
		if cur, ok := out[key]; ok && !newerPrice(p, cur) {
			continue
		}
		out[key] = p
	}
	return out, nil
}

// newerPrice は a のほうが新しいかを返します。
//
// ⚠ **日付が無いものは、いちばん古いものとして扱います**——「日付が読めなかった」
// 行が最新を名乗ると、いつの値か分からない数が「最新単価」の欄に出ます。
// 日付が同じ（または両方とも無い）ときは**あとから作られたページ**を採ります。
func newerPrice(a, b materialPrice) bool {
	if a.Date != b.Date {
		return a.Date > b.Date // ISO なので辞書順＝時系列。空は最小
	}
	return a.PageID > b.PageID
}

// materialKeyOf は材料の鍵（材質＋形状＋寸法）を畳んで作ります。
//
// **材料に単独の「名前」はありません**——`materialNameOf` と同じ3つ組です。
// 畳むのは `NormalizeText`（全角英数・半角カナ・直径記号の揺れ）まで。
// ⚠ **`NormalizeCode` は使いません**——寸法の `t3.4*定尺` からハイフンや長音を
// 落とすと、別の寸法と当たりえます。
//
// ⚠ **3つとも空なら空を返します。** 呼ぶ側は空の鍵で引き当ててはいけません
// （`"" == ""` で無関係な行に当たる）。
func materialKeyOf(material, shape, size string) string {
	parts := make([]string, 0, 3)
	any := false
	for _, v := range []string{material, shape, size} {
		v = cms.NormalizeText(strings.TrimSpace(v))
		if v != "" {
			any = true
		}
		parts = append(parts, v)
	}
	if !any {
		return ""
	}
	return strings.Join(parts, "\x00")
}

// headerIndexOf は見出し行からその列の位置を返します（無ければ -1）。
func headerIndexOf(head *html.Node, label string) int {
	for i, c := range cellsOf(head) {
		if strings.TrimSpace(textOf(c)) == label {
			return i
		}
	}
	return -1
}

// appendPriceCell は行の末尾へ、クロームのセルを1つ足します。
//
// ⚠ **`vocab-chrome` を付けるのは必須です**——付けないと、人が画面の表をコピーして
// 貼ったときに**本当の列として保存されます**（`class` はサニタイズで落ちるので、
// 貼られた時点では見分けが付かなくなる）。
func appendPriceCell(tr *html.Node, tag, text string) {
	td := &html.Node{Type: html.ElementNode, Data: tag,
		Attr: []html.Attribute{{Key: "class", Val: "vocab-chrome mat-price"}}}
	td.AppendChild(&html.Node{Type: html.TextNode, Data: text})
	tr.AppendChild(td)
}

// appendPriceNote は3列ぶんをまとめて1つの断り文にします。
func appendPriceNote(tr *html.Node, text string) {
	td := &html.Node{Type: html.ElementNode, Data: "td",
		Attr: []html.Attribute{
			{Key: "class", Val: "vocab-chrome mat-price mat-price-none"},
			{Key: "colspan", Val: "3"},
		}}
	td.AppendChild(&html.Node{Type: html.TextNode, Data: text})
	tr.AppendChild(td)
}

func orDash(s string) string {
	if strings.TrimSpace(s) == "" {
		return "—"
	}
	return s
}
