package toho

// ─────────────────────────────────────────────────────────────────────────
// 見積書の紙（PDF）と、見積明細の足元（2026-10-01）
//
// 紙の欄は利用者の見本（御見積書）の並び——題・日付・№・宛名（会社 担当者 様／御中）・「下記のとおり御見積申し上げます」・
// 差出人（見積担当の「発注書の署名」——会社の住所・電話は発注書と同じ）・受渡期日・受渡場所・取引方法・有効期限・
// 「下記価格には消費税は含んでおりません。税率 ○％」・明細（摘要・数量・単価・金額〈税抜〉）・合計（税抜）・備考。
// 刷る道具（字の大きさ・列幅・改ページ）は発注書の紙（order_pdf.go）と共有します。
// ─────────────────────────────────────────────────────────────────────────

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	stdhtml "html"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/signintech/gopdf"
	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

func init() {
	cms.RegisterMirror(EstimateItemsType, cms.MirrorHandlerFunc(renderEstimateItems))
}

// readEstimateDoc は見積書ページの本文から、タグ・明細（見出しの名前で）・備考を読みます。
func readEstimateDoc(body string) (head map[string]string, rows []map[string]string, note []string, err error) {
	nodes, perr := htmldoc.ParseFragment(body)
	if perr != nil {
		return nil, nil, nil, perr
	}
	head = map[string]string{}
	root := &html.Node{Type: html.ElementNode, Data: "div"}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	for _, k := range []string{EstimateNoTag, EstimateClientTag, EstimatePersonTag, EstimateDateTag, EstimateDueTag,
		EstimatePlaceTag, EstimateTradeTag, EstimateValidTag, EstimateSignerTag} {
		head[k] = strings.TrimSpace(cms.TagValue(root, k))
	}
	tables := tablesOfType([]*html.Node{root}, EstimateItemsType)
	if len(tables) == 0 {
		return nil, nil, nil, errors.New("見積明細の表がありません")
	}
	trs := rowsOf(tables[0])
	if len(trs) < 2 {
		return head, nil, nil, nil
	}
	labels := cellTexts(trs[0])
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
	if sec := findElement([]*html.Node{root}, func(n *html.Node) bool {
		return n.Data == "section" && sectionHeadingText(n) == estimateNoteHeading
	}); sec != nil {
		note = noteLinesOf(sec)
	}
	return head, rows, note, nil
}

// estimateAmount は1行の金額（数量×単価・税抜）です。どちらかが数でなければ ok=false。
func estimateAmount(r map[string]string) (int, bool) {
	q, okQ := estimateNumber(r["数量"])
	p, okP := estimateNumber(r["単価"])
	if !okQ || !okP {
		return 0, false
	}
	return int(q*p + 0.5), true
}

// estimateTotal は明細の合計（税抜）です。金額の出ない行があれば ok=false（合計を刷らない——発注書と同じ）。
func estimateTotal(rows []map[string]string) (int, bool) {
	total := 0
	for _, r := range rows {
		a, ok := estimateAmount(r)
		if !ok {
			return 0, false
		}
		total += a
	}
	return total, len(rows) > 0
}

// jpDate は YYYY-MM-DD を「YYYY年M月D日」にします（読めなければそのまま）。
func jpDate(s string) string {
	if t, err := time.Parse("2006-01-02", strings.TrimSpace(s)); err == nil {
		return fmt.Sprintf("%d年%d月%d日", t.Year(), int(t.Month()), t.Day())
	}
	return s
}

// buildEstimatePDF は見積書ページの本文から PDF を組みます。
func buildEstimatePDF(body string, viewer *auth.User) ([]byte, error) {
	font := PDFFont()
	if font == "" {
		return nil, ErrNoPDFFont
	}
	if _, err := os.Stat(font); err != nil {
		return nil, fmt.Errorf("%w（いまの設定: %s）", ErrNoPDFFont, font)
	}
	head, rows, note, err := readEstimateDoc(body)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errors.New("見積明細に行がありません")
	}
	p := &gopdf.GoPdf{}
	p.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	p.AddPage()
	if err := addPDFFont(p, font); err != nil {
		return nil, err
	}
	if err := p.SetFont("jp", "", pdfFontSz); err != nil {
		return nil, err
	}

	// ── 題・日付・№ ──
	pdfText(p, pdfLeft+190, pdfTop, 18, "御　見　積　書")
	pdfTextRight(p, pdfRight, pdfTop, pdfFontSz, jpDate(head[EstimateDateTag]))
	pdfTextRight(p, pdfRight, pdfTop+pdfLine, pdfFontSz, "№ "+head[EstimateNoTag])
	y := pdfTop + 34

	// ── 宛名 ──
	to := strings.TrimSpace(head[EstimateClientTag])
	if person := strings.TrimSpace(head[EstimatePersonTag]); person != "" {
		to += "　" + person + "　様"
	} else {
		to += "　御中"
	}
	y = pdfText(p, pdfLeft, y, 13, to)
	p.SetLineWidth(0.6)
	p.Line(pdfLeft, y-2, pdfLeft+260, y-2)
	y += 6
	y = pdfText(p, pdfLeft, y, pdfFontSz, "下記のとおり御見積申し上げます")

	// ── 差出人（右）——見積担当の署名（発注書の署名と同じ節）──
	ry := pdfTop + 34
	for _, ln := range senderLines(map[string]string{OrderSignerTag: head[EstimateSignerTag]}, viewer) {
		ry = pdfText(p, 330, ry, pdfFontSz, ln)
	}
	if ry > y {
		y = ry
	}
	y += 8

	// ── 受渡期日・受渡場所・取引方法・有効期限 ──
	for _, k := range estimateHeads {
		y = pdfText(p, pdfLeft, y, pdfFontSz, k+"　："+head[k])
	}
	y += 4
	if rate, ok := TaxRate(); ok {
		y = pdfText(p, pdfLeft, y, pdfFontSz, "下記価格には消費税は含んでおりません。　税率 "+rateText(rate)+"％")
	}
	y += 4

	// ── 明細 ──
	cols := []orderPDFColumn{
		{Label: "摘要", Width: 240},
		{Label: "数量", Width: 60},
		{Label: "単価", Width: 80, Right: true},
		{Label: "金額(税抜)", Width: 90, Right: true},
	}
	var cells []map[string]string
	for _, r := range rows {
		desc := r["品名"]
		if no := r["品番"]; no != "" {
			desc = strings.TrimSpace(desc + "　" + no)
		}
		if n := r["備考"]; n != "" {
			desc += "（" + n + "）"
		}
		c := map[string]string{"摘要": desc, "数量": strings.TrimSpace(r["数量"] + " " + r["単位"]), "単価": r["単価"]}
		if a, ok := estimateAmount(r); ok {
			c["金額(税抜)"] = strconv.Itoa(a)
		}
		cells = append(cells, c)
	}
	y = pdfTable(p, y, cols, cells)
	y += 4
	if total, ok := estimateTotal(rows); ok {
		pdfTextRight(p, pdfRight, y, 11, "合計（税抜）　"+comma(total)+" 円")
		y += pdfLine
	}
	y += 6

	// ── 備考 ──
	if len(note) > 0 {
		y = pdfText(p, pdfLeft, y, pdfFontSz, estimateNoteHeading+"：")
		for _, ln := range note {
			parts, err := p.SplitText(ln, pdfRight-pdfLeft-12)
			if err != nil || len(parts) == 0 {
				parts = []string{ln}
			}
			for _, s := range parts {
				if y+pdfFontSz+4 > pdfBottom {
					p.AddPage()
					y = pdfTop
				}
				y = pdfText(p, pdfLeft+12, y, pdfFontSz, s)
			}
		}
	}
	var buf bytes.Buffer
	if _, err := p.WriteTo(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// makeEstimatePDF は見積書の PDF を作って添付に残し、ページに表示します（関門は呼ぶ側が通す）。断るときは応答を書いて false。
func makeEstimatePDF(w http.ResponseWriter, user *auth.User, pageID string) (madeOrderPDF, bool) {
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		cms.JSONFail(w, http.StatusNotFound, "ページを読めません: "+err.Error())
		return madeOrderPDF{}, false
	}
	pdf, err := buildEstimatePDF(body, user)
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, ErrNoPDFFont) {
			code = http.StatusServiceUnavailable
		}
		cms.JSONFail(w, code, err.Error())
		return madeOrderPDF{}, false
	}
	name := "御見積書 " + pageID + " " + time.Now().Format("20060102-150405") + ".pdf"
	attachID, fileName, err := cms.SaveAttachmentFrom(pageID, user.Username, name, "pdf", pdf)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "保存できません: "+err.Error())
		return madeOrderPDF{}, false
	}
	auth.Audit(user.Username, "estimate-pdf", pageID+" "+fileName)
	ref := pageID + "-" + attachID
	note := ""
	changed := false
	if err := cms.RewriteBody(pageID, user.Username, func(cur string) string {
		out, ok := placePDFView(cur, ref, EstimateItemsType)
		changed = ok
		return out
	}); err != nil {
		note = "⚠ PDFは作りましたが、ページに表示できません: " + err.Error()
	} else if !changed {
		note = "⚠ PDFは作りましたが、置き場所が分かりません（見積明細の表がありません）。添付からは開けます。"
	}
	return madeOrderPDF{AttachID: attachID, File: fileName, ViewNote: note}, true
}

// EstimatePDFAPIHandler は POST /api/estimate-pdf です（入力: {page_id}）。
func EstimatePDFAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := gateWritablePage(w, r, req.PageID)
	if !okID {
		return
	}
	made, ok := makeEstimatePDF(w, user, pageID)
	if !ok {
		return
	}
	out := map[string]any{"success": true, "attach_id": made.AttachID, "file": made.File, "url": "/" + pageID + "/" + made.File}
	if made.ViewNote != "" {
		out["view_note"] = made.ViewNote
	}
	json.NewEncoder(w).Encode(out)
}

// renderEstimateItems は見積明細の足元に、合計（税抜）・PDF を作る・メールで送る を出します（鏡）。
func renderEstimateItems(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
	if el.Data != "table" {
		return true, nil
	}
	cms.DropChrome(el)
	span := headerCellCount(el)
	var rows []map[string]string
	trs := rowsOf(el)
	if len(trs) > 1 {
		labels := cellTexts(trs[0])
		for _, tr := range trs[1:] {
			cells := cellTexts(tr)
			r := map[string]string{}
			for i, l := range labels {
				if i < len(cells) {
					r[strings.TrimSpace(l)] = cells[i]
				}
			}
			if strings.TrimSpace(r["品名"]+r["単価"]+r["数量"]) != "" {
				rows = append(rows, r)
			}
		}
	}
	if total, ok := estimateTotal(rows); ok {
		appendFootHTML(el, span, "estimate-total", `<strong>合計（税抜）: `+comma(total)+`円</strong>`)
	} else if len(rows) > 0 {
		appendFootRow(el, span, "estimate-note", "合計を出せません（数量か単価の無い行があります）")
	}
	if ctx.Viewer == nil || !canWritePage(ctx.Viewer, ctx.PageID) || cms.IsTemplateArea(page.FormatID(ctx.PageID)) {
		return false, nil
	}
	pid := stdhtml.EscapeString(page.FormatID(ctx.PageID))
	appendFootHTML(el, span, "estimate-actions",
		`<button type="button" class="chip-btn estimate-pdf-go" data-estimate-page="`+pid+`">📄 PDFを作る</button> `+
			`<span class="estimate-pdf-say"></span>`+
			`<details class="estimate-mail"><summary>✉️ メールで送る</summary>`+
			`<div data-mail-compose="`+EstimateMailPurpose+`" data-mail-page="`+pid+`"></div></details>`)
	return false, nil
}
