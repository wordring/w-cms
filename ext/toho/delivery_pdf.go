package toho

// ─────────────────────────────────────────────────────────────────────────
// 納品書の紙（PDF）と、納品明細表の足元（2026-10-17）
//
// 紙の形は、2026-10-15・16 に w-cms の外で作った納品書（客先へ渡した実物）のまま——題「納　品　書」・日付（納品日、空なら今日）・№・
// 宛名「客先　御中」・差出人（納品担当の「部材発注書の署名」——会社の住所・電話は部材発注書と同じ）・「下記のとおり納品いたします。」・
// 貴社注文番号（明細の発注書番号が1つなら上に、いくつもあるなら列に）・明細（No.・品番・品名・数量・単位・単価・金額）・
// 小計・消費税・合計。刷る道具（字の大きさ・列幅・改ページ）は部材発注書の紙（order_pdf.go）と共有します。
//
//   - **単価の決まっていない行**は、単価・金額を空欄で刷ります（0 にしない）。そういう行があると小計・消費税・合計は刷りません
//     （利用者:「価格はデータがそろった時に私が決めます」——空の値を足した合計は正しくない）。単価が1つも無ければ単価と金額の列ごと刷らない。
//   - **PDF を作ると、`発行日` に今日を書きます**（空のときだけ）——空なら「未発行」、入っていれば「発行済」（delivery.go）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"math"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

func init() {
	cms.RegisterMirror(DeliveryItemsType, cms.MirrorHandlerFunc(renderDeliveryItems))
}

// readDeliveryDoc は納品書ページの本文から、タグと明細（見出しの名前で）を読みます。
func readDeliveryDoc(body string) (head map[string]string, rows []map[string]string, err error) {
	nodes, perr := htmldoc.ParseFragment(body)
	if perr != nil {
		return nil, nil, perr
	}
	root := wrapInDiv(nodes)
	head = map[string]string{}
	for _, k := range []string{DeliveryNoTag, DeliveryClientTag, DeliveredAtTag, DeliveryIssuedTag, DeliverySignerTag, DeliveryPlaceTag} {
		head[k] = strings.TrimSpace(cms.TagValue(root, k))
	}
	tables := tablesOfType([]*html.Node{root}, DeliveryItemsType)
	if len(tables) == 0 {
		return nil, nil, errors.New("納品明細表がありません")
	}
	return head, itemRowsOf(tables[0]), nil
}

// itemRowsOf は表の行を、見出しの名前の地図にして返します（空の行・足元の行は落とす）。
func itemRowsOf(t *html.Node) []map[string]string {
	trs := rowsOf(t)
	if len(trs) < 2 {
		return nil
	}
	labels := cellTexts(trs[0])
	var rows []map[string]string
	for _, tr := range trs[1:] {
		if tr.Parent != nil && tr.Parent.Data == "tfoot" {
			continue
		}
		cells := cellTexts(tr)
		r := map[string]string{}
		empty := true
		for i, l := range labels {
			if i < len(cells) {
				r[strings.TrimSpace(l)] = strings.TrimSpace(cells[i])
				if strings.TrimSpace(cells[i]) != "" {
					empty = false
				}
			}
		}
		if !empty {
			rows = append(rows, r)
		}
	}
	return rows
}

// deliveryTotals は小計（金額の和）を返します。単価の無い行があれば ok=false（合計を刷らない）。anyPrice は単価が1つでもあるか。
func deliveryTotals(rows []map[string]string) (sub int, ok, anyPrice bool) {
	ok = len(rows) > 0
	for _, r := range rows {
		if _, has := estimateNumber(r["単価"]); has {
			anyPrice = true
		}
		a, has := estimateAmount(r)
		if !has {
			ok = false
			continue
		}
		sub += a
	}
	return sub, ok, anyPrice
}

// buildDeliveryPDF は納品書ページの本文から PDF を組みます。
func buildDeliveryPDF(body string, viewer *auth.User) ([]byte, error) {
	font, err := paperFont()
	if err != nil {
		return nil, err
	}
	head, rows, err := readDeliveryDoc(body)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errors.New("納品明細表に行がありません")
	}
	p, err := startPaper(font)
	if err != nil {
		return nil, err
	}
	date := head[DeliveredAtTag]
	if date == "" {
		date = time.Now().Format("2006-01-02")
	}
	pdfText(p, pdfLeft+190, pdfTop, 18, "納　品　書")
	pdfTextRight(p, pdfRight, pdfTop, pdfFontSz, jpDate(date))
	pdfTextRight(p, pdfRight, pdfTop+pdfLine, pdfFontSz, "№ "+head[DeliveryNoTag])
	y := pdfTop + 34
	y = pdfText(p, pdfLeft, y, 13, strings.TrimSpace(head[DeliveryClientTag])+"　御中")
	p.SetLineWidth(0.6)
	p.Line(pdfLeft, y-2, pdfLeft+260, y-2)
	y = pdfSender(p, pdfTop+34, y, senderLines(map[string]string{OrderSignerTag: head[DeliverySignerTag]}, viewer))
	y += 6
	y = pdfText(p, pdfLeft, y, pdfFontSz, "下記のとおり納品いたします。")
	// 貴社注文番号——明細の発注書番号が1つなら上に、いくつもあるなら列に（2026-10-16 付の実物から）。
	single, asColumn := deliveryOrderNos(rows)
	if single != "" {
		y = pdfText(p, pdfLeft, y, pdfFontSz, "貴社注文番号： "+single)
	}
	if pl := head[DeliveryPlaceTag]; pl != "" {
		y = pdfText(p, pdfLeft, y, pdfFontSz, "納品場所： "+pl)
	}
	y += 10
	sub, okTotal, anyPrice := deliveryTotals(rows)
	cols := []orderPDFColumn{{Label: "No."}}
	if asColumn {
		cols = append(cols, orderPDFColumn{Label: "注文番号"})
	}
	cols = append(cols, orderPDFColumn{Label: "品番"}, orderPDFColumn{Label: "品名"},
		orderPDFColumn{Label: "数量", Right: true}, orderPDFColumn{Label: "単位"})
	if anyPrice {
		cols = append(cols, orderPDFColumn{Label: "単価", Right: true}, orderPDFColumn{Label: "金額", Right: true})
	}
	cells := make([]map[string]string, 0, len(rows))
	for i, r := range rows {
		c := map[string]string{"No.": strconv.Itoa(i + 1), "注文番号": r[OrderNoTag], "品番": r[ItemNoTag], "品名": r["品名"],
			"数量": r["数量"], "単位": r["単位"], "単価": r["単価"]}
		if a, ok := estimateAmount(r); ok {
			c["金額"] = comma(a)
		}
		cells = append(cells, c)
	}
	y = pdfTable(p, y, cols, cells)
	y += 6
	y = pdfRoom(p, y, 3*pdfLine) // 合計の3行が入らなければ次の紙へ
	if okTotal {
		if rate, ok := TaxRate(); ok {
			tax := int(math.Round(float64(sub) * rate / 100))
			label := "消費税（" + rateText(rate) + "%）"
			for _, ln := range [][2]string{{"小計", comma(sub)}, {label, comma(tax)}, {"合計", comma(sub + tax)}} {
				pdfTextRight(p, pdfRight, y, 11, ln[0]+"　"+ln[1]+" 円")
				y += pdfLine
			}
		} else {
			// 税率が決まっていなければ、消費税と合計を書かない（「消費税 0 円」と書くと、事実でないことが紙に載る）。
			pdfTextRight(p, pdfRight, y, 11, "小計　"+comma(sub)+" 円")
			y += pdfLine
			pdfText(p, pdfLeft, y, pdfFontSz, "※ 消費税率が設定されていないため、消費税と合計は記載していません。")
		}
	} else if anyPrice {
		pdfText(p, pdfLeft, y, pdfFontSz, "※ 単価の決まっていない品があるため、合計は記載していません。")
	}
	return finishPaper(p)
}

// makeDeliveryPDF は納品書の PDF を作って添付に残し、ページに表示し、`発行日` に今日を書きます（空のときだけ）。
func makeDeliveryPDF(w http.ResponseWriter, user *auth.User, pageID string) (madeOrderPDF, bool) {
	made, ok := makePaper(w, user, pageID, paperKind{
		build: buildDeliveryPDF,
		name:  func(_, pageID string) string { return "納品書 " + pageID },
		audit: "delivery-pdf",
		show:  showBelowItems(DeliveryItemsType, "納品明細表"),
	})
	if !ok {
		return made, false
	}
	stampIssuedToday(&made, user, pageID, func(cur string) string {
		head, _, err := readDeliveryDoc(cur)
		if err != nil {
			return ""
		}
		return head[DeliveryIssuedTag]
	})
	// 全部納めた製造ページを記録へ（2026-10-21・mfg_close.go——納品までは製造の管理下）。
	closeFinishedMfgPages(user, pageNum(pageID), time.Now())
	return made, true
}

// stampIssuedToday は、紙を作ったページの `発行日` に今日を書きます（空のときだけ——issued が今の発行日を読む）。
// 書けなければ made の表示の断りに添えます。納品書と請求書が同じ8行を写していたので寄せた（2026-10-19）。
func stampIssuedToday(made *madeOrderPDF, user *auth.User, pageID string, issued func(body string) string) {
	today := time.Now().Format("2006-01-02")
	if err := cms.RewriteBody(pageID, user.Username, func(cur string) string {
		if issued(cur) != "" {
			return cur
		}
		return withTagValues(cur, DeliveryIssuedTag, []string{today}, false)
	}); err != nil {
		made.ViewNote = strings.TrimSpace(made.ViewNote + "（発行日を書けませんでした: " + err.Error() + "）")
	}
}

// appendIssuedStateRow は、明細の表の足元に「状態: 未発行／発行済（日付）」の行を足します（本文の `発行日` を読む）。
func appendIssuedStateRow(el *html.Node, span int) {
	issued := strings.TrimSpace(cms.TagValue(rootOf(el), DeliveryIssuedTag))
	state := "未発行（紙をまだ作っていません）"
	if issued != "" {
		state = "発行済（" + issued + "）"
	}
	appendFootRow(el, span, "delivery-state", "状態: "+state)
}

// DeliveryPDFAPIHandler は POST /api/delivery-pdf です（入力: {page_id}）。
func DeliveryPDFAPIHandler(w http.ResponseWriter, r *http.Request) {
	servePaper(w, r, makeDeliveryPDF)
}

// renderDeliveryItems は納品明細表の足元に、状態（未発行・発行済）・合計・PDF を作る を出します（鏡）。
func renderDeliveryItems(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
	if el.Data != "table" {
		return true, nil
	}
	cms.DropChrome(el)
	span := headerCellCount(el)
	rows := itemRowsOf(el)
	appendIssuedStateRow(el, span)
	if sub, ok, _ := deliveryTotals(rows); ok {
		appendFootHTML(el, span, "estimate-total", `<strong>小計（税抜）: `+comma(sub)+`円</strong>`)
	} else if len(rows) > 0 {
		appendFootRow(el, span, "estimate-note", "小計を出せません（単価の決まっていない行があります）")
	}
	if !writableMirror(ctx) {
		return false, nil
	}
	appendFootHTML(el, span, "delivery-actions", pdfButtonHTML("delivery", page.FormatID(ctx.PageID)))
	return false, nil
}

// deliveryOrderNos は、貴社注文番号を紙の上に1つ刷るか（single）、明細の列にするか（asColumn）を決めます。
//
// ⚠ 上に1つ刷るのは、全部の行が同じ番号のときだけ——1行だけに番号があると、番号の無い行もその注文のように刷っていた
// （2026-10-22 の見直し）。番号の無い行が混ざるときは列にする（その行は空欄）。どの行にも無ければ、どちらにも出さない。
func deliveryOrderNos(rows []map[string]string) (single string, asColumn bool) {
	nos := map[string]bool{}
	blank := false
	for _, r := range rows {
		if n := r[OrderNoTag]; n != "" {
			nos[n] = true
		} else {
			blank = true
		}
	}
	if len(nos) == 1 && !blank {
		for n := range nos {
			single = n
		}
		return single, false
	}
	return "", len(nos) > 0
}
