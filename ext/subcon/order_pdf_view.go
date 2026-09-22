package subcon

// ─────────────────────────────────────────────────────────────────────────
// 作った発注書PDFを、そのページの上で開く（2026-09-22）
//
// ユーザー:「**発注書のページにPDFが表示されていません**」。
//
// ⚠ **原因は「作る道が画面に無かった」ことでした。** `/api/order-pdf` は 09-21 から
// 在りましたが、**呼ぶボタンがどこにもありません**でした（当時は API を直に叩いて
// 確かめただけ）。しかも作ったPDFは**添付として保存されるだけ**で、本文には何も
// 置かれないので、**ページを開いても出てきません**。
//
// ユーザーの描いた流れ（§7d）にも、はっきりそう書かれていました——
// 「新たな表の下に**発注書を埋め込み表示**し、その下に発注フォームがあります」。
//
// ⚠ **置くのはマーカー1つだけ**です（`<section data-type="file-view" data-ref="…">`）。
// 中身はページを返すたびにコアの鏡が描き、`.vocab-chrome` に包むので本文には
// 焼き付きません——**`<embed>` を本文に書かせない**のがこの仕組みの要です
// （書く人が決められるのはページIDと添付IDだけで、URLは機械が組みます）。
//
// ⚠ **作り直したら差し替えます**（増やしません）。発注書は何度でも作り直せますが、
// **ページの上に3枚並んでいても、どれが最新か分かりません**。古いPDFは添付として
// 残り、**実際に送ったものは通信箱の控え**が持っています。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
)

// showOrderPDFOnPage は、作ったPDFをそのページで開けるようにします。
//
// 返すのは**添える一文**だけです（空なら何も言うことがない）。
// ⚠ **うまくいかなくてもPDFは取り消しません**——**紙のほうが重い**ので、
// 「画面に出ない」は人が貼り直せば済みます。
func showOrderPDFOnPage(user *auth.User, pageID, attachID string) string {
	ref := pageID + "-" + attachID
	changed := false
	if err := cms.RewriteBody(pageID, user.Username, func(cur string) string {
		out, ok := placeOrderPDFView(cur, ref)
		changed = ok
		return out
	}); err != nil {
		return "⚠ PDFは作りましたが、ページに表示できません: " + err.Error()
	}
	if !changed {
		// ⚠ **表が無ければ置き場所が決まりません。** 黙ると「作ったのに出ない」に
		//    なるので、そう言います。
		return "⚠ PDFは作りましたが、置き場所が分かりません（発注明細の表がありません）。" +
			"添付からは開けます。"
	}
	return ""
}

// placeOrderPDFView は本文に「このPDFを開く」マーカーを置きます。
//
// 既にマーカーがあれば**参照を差し替え**、無ければ**発注明細の表の直後**に足します。
// 戻り値の ok は、置けたか（本文が変わったか）です。
func placeOrderPDFView(body, ref string) (string, bool) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body, false
	}
	// ① 既にあるマーカーを探して差し替える。
	if el := firstFileView(nodes); el != nil {
		if cms.Attr(el, cms.FileRefAttr) == ref {
			return body, false // 同じものを指している（版を無駄に進めない）
		}
		setAttr(el, cms.FileRefAttr, ref)
		return htmldoc.Render(nodes), true
	}
	// ② 無ければ発注明細の表の直後へ。
	tables := tablesOfType(nodes, ourOrderItemsType)
	if len(tables) == 0 {
		return body, false
	}
	marker, merr := htmldoc.ParseFragment(
		`<section data-type="` + cms.FileViewType + `" ` + cms.FileRefAttr + `="` +
			stdhtml.EscapeString(ref) + `"></section>`)
	if merr != nil || len(marker) == 0 {
		return body, false
	}
	// ⚠ トップレベルの表には `Parent` が無い（`spliceNodes` がその罠を引き受ける）。
	return spliceNodes(nodes, tables[0], marker, true)
}

// firstFileView は本文の最初のファイル表示のマーカーを返します（無ければ nil）。
//
// ⚠ **発注書ページの持ち物は機械が組んだものだけ**なので、「最初の1つ」で足ります。
// 人が別のファイルを貼っていたら、そちらが差し替わる可能性はありますが、
// **発注書ページに手でファイルを貼る用事はありません**（添付は落とせます）。
func firstFileView(nodes []*html.Node) *html.Node {
	return findElement(nodes, func(n *html.Node) bool {
		return n.Data == "section" && cms.Attr(n, "data-type") == cms.FileViewType
	})
}

// setAttr は属性を書き換えます（無ければ足します）。
func setAttr(el *html.Node, key, val string) {
	for i := range el.Attr {
		if el.Attr[i].Key == key {
			el.Attr[i].Val = val
			return
		}
	}
	el.Attr = append(el.Attr, html.Attribute{Key: key, Val: val})
}
