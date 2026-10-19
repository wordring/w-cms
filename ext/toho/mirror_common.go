package toho

import (
	stdhtml "html"

	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// 鏡（表示のときにサーバーが足す、保存されない部分）が共に使う小さな道具です（2026-10-20 にまとめた）。

// writableMirror は、鏡を描いているページを、見ている人が書けるかです。テンプレートの中は書けないと扱います
// （テンプレートから紙を作ったり、部材発注書を作ったりさせない）。
func writableMirror(ctx *cms.MirrorContext) bool {
	return ctx.Viewer != nil && canWritePage(ctx.Viewer, ctx.PageID) && !cms.IsTemplateArea(page.FormatID(ctx.PageID))
}

// pdfButtonHTML は、書類の紙を作る「📄 PDFを作る」のボタンと、知らせの欄です。kind は class と data 属性の頭
// （delivery・invoice・estimate・rfq）で、画面（assets/app.js）は `.<kind>-pdf-go` の `data-<kind>-page` を読みます。
func pdfButtonHTML(kind, pageID string) string {
	pid := stdhtml.EscapeString(pageID)
	return `<button type="button" class="chip-btn ` + kind + `-pdf-go" data-` + kind + `-page="` + pid + `">📄 PDFを作る</button> ` +
		`<span class="` + kind + `-pdf-say"></span>`
}
