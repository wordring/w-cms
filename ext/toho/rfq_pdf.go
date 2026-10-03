package toho

// ─────────────────────────────────────────────────────────────────────────
// 見積依頼書の紙（PDF）・備考の欄・送る（2026-10-03・見積依頼の段2の後半）
//
// 利用者:「何時ものように見積依頼明細からPDFを作り送付です。見積依頼明細の下に備考を書き込む欄が必要です。
// PDFの下に発送項目（メールを書くボックス）」。見本（見積依頼の Word）は 日付・題・宛名・差出人・材質 形状 寸法 個数 の表・
// 「よろしくお願いします。」・「○○用」。
//
//	見積依頼書ページ
//	  見積依頼明細 ── 足元（鏡）: 備考の欄・「📄 PDFを作る」
//	  （PDF のファイル表示——表の直後に機械が置く）
//	  備考の節
//	  「見積依頼を送る」（ビュー・テンプレートの末尾）: ✉️ メールで送る（用件「見積依頼」）・「送った（FAX・手渡し）」
//
//   - 紙: 題「見積依頼書」・日付（見積依頼日）・№（見積依頼番号）・仕入先 御中・差出人（見積依頼担当の署名——発注書と同じ節）・
//     明細（列は設定 `rfq_print_columns`——どの行にも値の無い列は刷らない、⚠ **単価はいつも空欄で刷る**〔業者が書き込む〕・
//     金額は刷らない）・「よろしくお願いします。」・備考。**辞退の行は刷らない**。
//   - メールの添付は発注書と同じ——送るときに作る PDF（印つき・外せば作らない）と、外注加工の資料（行の 弊社品番＋番号）。
//   - 送れたら（メール）、または「送った（FAX・手渡し）」で、`送付日` に今日。
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

	"w-cms/ext/comm"
	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// RFQMailPurpose は送る欄の用件「見積依頼」です。
const RFQMailPurpose = "見積依頼"

// RFQSendViewType は見積依頼書ページの末尾に置く「見積依頼を送る」の形式名です。
const RFQSendViewType = "rfq-send"

// rfqLineDeclined は見積依頼明細の「辞退」——紙に刷らない。
const rfqLineDeclined = "辞退"

func init() {
	cms.RegisterMirror(RFQItemsType, cms.MirrorHandlerFunc(renderRFQItems))
	cms.RegisterVocab(cms.VocabDef{
		Type: RFQSendViewType, DisplayName: "見積依頼を送る", Category: "ビュー", Icon: "✉️", Element: "section",
		View: true,
	})
	cms.RegisterView(RFQSendViewType, rfqSendViewHTML)
	comm.RegisterSendPurpose(RFQMailPurpose, comm.SendPurpose{
		Defaults:  rfqMailDefaults,
		Prepare:   prepareRFQMail,
		AfterSent: afterRFQMail,
		NeedsPage: true,
	})
}

// readRFQDoc は見積依頼書ページの本文から、タグ・明細（見出しの名前で・辞退は除く）・備考を読みます。
func readRFQDoc(body string) (head map[string]string, rows []map[string]string, note []string, err error) {
	nodes, perr := htmldoc.ParseFragment(body)
	if perr != nil {
		return nil, nil, nil, errors.New("本文を読めません")
	}
	root := &html.Node{Type: html.ElementNode, Data: "div"}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	head = map[string]string{}
	for _, k := range []string{RFQNoTag, SupplierTag, RFQDateTag, RFQSignerTag, EstimateSentTag, RFQAnsweredTag} {
		head[k] = strings.TrimSpace(cms.TagValue(root, k))
	}
	tables := tablesOfType([]*html.Node{root}, RFQItemsType)
	if len(tables) == 0 {
		return nil, nil, nil, errors.New("見積依頼明細の表がありません（このページは見積依頼書ではないようです）")
	}
	trs := rowsOf(tables[0])
	if len(trs) >= 2 {
		labels := cellTexts(trs[0])
		for _, tr := range trs[1:] {
			if tr.Parent != nil && tr.Parent.Data == "tfoot" {
				continue
			}
			r := map[string]string{}
			any := false
			for i, v := range cellTexts(tr) {
				if i >= len(labels) {
					break
				}
				r[strings.TrimSpace(labels[i])] = strings.TrimSpace(v)
				if strings.TrimSpace(v) != "" {
					any = true
				}
			}
			if any && r["状態"] != rfqLineDeclined {
				rows = append(rows, r)
			}
		}
	}
	if sec := findElement([]*html.Node{root}, func(n *html.Node) bool {
		return n.Data == "section" && sectionHeadingText(n) == rfqNoteHeading
	}); sec != nil {
		note = noteLinesOf(sec)
	}
	return head, rows, note, nil
}

// rfqPaperColumns は紙に刷る列です——設定の並びで、どの行にも値の無い列は落とし、単価はいつも残す（空欄で刷る）。
func rfqPaperColumns(rows []map[string]string) []orderPDFColumn {
	var cols []orderPDFColumn
	for _, label := range RFQPrintColumns() {
		c := orderPDFColumn{Label: label, Width: 70}
		if w, ok := pdfColumnWidths[label]; ok {
			c.Width = w
		}
		c.Right = pdfRightColumns[label]
		used := label == "単価"
		for _, r := range rows {
			if strings.TrimSpace(r[label]) != "" {
				used = true
				break
			}
		}
		if used {
			cols = append(cols, c)
		}
	}
	return cols
}

// buildRFQPDF は見積依頼書ページの本文から PDF を組みます。
func buildRFQPDF(body string, viewer *auth.User) ([]byte, error) {
	font := PDFFont()
	if font == "" {
		return nil, ErrNoPDFFont
	}
	if _, err := os.Stat(font); err != nil {
		return nil, fmt.Errorf("%w（いまの設定: %s）", ErrNoPDFFont, font)
	}
	head, rows, note, err := readRFQDoc(body)
	if err != nil {
		return nil, err
	}
	if len(rows) == 0 {
		return nil, errors.New("見積依頼明細に刷る行がありません（辞退の行は刷りません）")
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
	pdfText(p, pdfLeft+180, pdfTop, 18, "見　積　依　頼　書")
	pdfTextRight(p, pdfRight, pdfTop, pdfFontSz, jpDate(head[RFQDateTag]))
	pdfTextRight(p, pdfRight, pdfTop+pdfLine, pdfFontSz, "№ "+head[RFQNoTag])
	y := pdfTop + 34

	// ── 宛名 ──
	y = pdfText(p, pdfLeft, y, 13, strings.TrimSpace(head[SupplierTag])+"　御中")
	p.SetLineWidth(0.6)
	p.Line(pdfLeft, y-2, pdfLeft+260, y-2)
	y += 6
	y = pdfText(p, pdfLeft, y, pdfFontSz, "下記につきまして、御見積をお願いいたします。")
	y = pdfText(p, pdfLeft, y, pdfFontSz, "単価をご記入のうえ、ご返送くださいますようお願いいたします。")

	// ── 差出人（右）——見積依頼担当の署名（発注書の署名と同じ節）──
	ry := pdfTop + 34
	for _, ln := range senderLines(map[string]string{OrderSignerTag: head[RFQSignerTag]}, viewer) {
		ry = pdfText(p, 330, ry, pdfFontSz, ln)
	}
	if ry > y {
		y = ry
	}
	y += 10

	// ── 明細（単価は空欄で刷る）──
	cols := rfqPaperColumns(rows)
	cells := make([]map[string]string, 0, len(rows))
	for _, r := range rows {
		c := map[string]string{}
		for k, v := range r {
			c[k] = v
		}
		c["単価"] = "" // 業者が書き込む欄
		cells = append(cells, c)
	}
	y = pdfTable(p, y, cols, cells)
	y += 8
	y = pdfText(p, pdfLeft, y, pdfFontSz, "よろしくお願いします。")
	y += 6

	// ── 備考（「○○用」など）──
	if len(note) > 0 {
		y = pdfText(p, pdfLeft, y, pdfFontSz, rfqNoteHeading+"：")
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

// makeRFQPDF は見積依頼書の PDF を作って添付に残し、ページに表示します（関門は呼ぶ側が通す）。断るときは応答を書いて false。
func makeRFQPDF(w http.ResponseWriter, user *auth.User, pageID string) (madeOrderPDF, bool) {
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		cms.JSONFail(w, http.StatusNotFound, "ページを読めません: "+err.Error())
		return madeOrderPDF{}, false
	}
	pdf, err := buildRFQPDF(body, user)
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, ErrNoPDFFont) {
			code = http.StatusServiceUnavailable
		}
		cms.JSONFail(w, code, err.Error())
		return madeOrderPDF{}, false
	}
	name := "見積依頼書 " + pageID + " " + time.Now().Format("20060102-150405") + ".pdf"
	attachID, fileName, err := cms.SaveAttachmentFrom(pageID, user.Username, name, "pdf", pdf)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "保存できません: "+err.Error())
		return madeOrderPDF{}, false
	}
	auth.Audit(user.Username, "rfq-pdf", pageID+" "+fileName)
	ref := pageID + "-" + attachID
	note := ""
	changed := false
	if err := cms.RewriteBody(pageID, user.Username, func(cur string) string {
		out, ok := placePDFView(cur, ref, RFQItemsType)
		changed = ok
		return out
	}); err != nil {
		note = "⚠ PDFは作りましたが、ページに表示できません: " + err.Error()
	} else if !changed {
		note = "⚠ PDFは作りましたが、置き場所が分かりません（見積依頼明細の表がありません）。添付からは開けます。"
	}
	return madeOrderPDF{AttachID: attachID, File: fileName, ViewNote: note}, true
}

// isRFQPage は見積依頼書ページか（テンプレートの外で、見積依頼番号のタグを持つ）です。
func isRFQPage(pageID string) bool {
	return !cms.IsTemplateArea(pageID) && cms.PageTagValue(database.DB, pageNum(pageID), RFQNoTag) != ""
}

// RFQPDFAPIHandler は POST /api/rfq-pdf です（入力: {page_id}）。
func RFQPDFAPIHandler(w http.ResponseWriter, r *http.Request) {
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
	made, ok := makeRFQPDF(w, user, pageID)
	if !ok {
		return
	}
	out := map[string]any{"success": true, "attach_id": made.AttachID, "file": made.File, "url": "/" + pageID + "/" + made.File}
	if made.ViewNote != "" {
		out["view_note"] = made.ViewNote
	}
	json.NewEncoder(w).Encode(out)
}

// RFQNoteAPIHandler は POST /api/rfq/note です（入力: {page_id, note}）——見積依頼書ページの「備考」の節を書き換えます。
func RFQNoteAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		Note   string `json:"note"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := gateWritablePage(w, r, req.PageID)
	if !okID {
		return
	}
	if !isRFQPage(pageID) {
		cms.JSONFail(w, http.StatusBadRequest, "見積依頼書ページではありません")
		return
	}
	var failed error
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		out, err := withEstimateNote(cur, req.Note) // 見出し「備考」の節——見積書と同じ形
		if err != nil {
			failed = err
			return cur
		}
		return out
	}) {
		return
	}
	if failed != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "備考を書けません: "+failed.Error())
		return
	}
	auth.Audit(user.Username, "rfq.note", pageID)
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": pageID})
}

// RFQSentAPIHandler は POST /api/rfq/sent です（入力: {page_id}）——FAX・手渡しで送ったとき、`送付日` に今日を書きます。
func RFQSentAPIHandler(w http.ResponseWriter, r *http.Request) {
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
	if !isRFQPage(pageID) {
		cms.JSONFail(w, http.StatusBadRequest, "見積依頼書ページではありません")
		return
	}
	today := time.Now().Format("2006-01-02")
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		return withTagValues(cur, EstimateSentTag, []string{today}, true)
	}) {
		return
	}
	auth.Audit(user.Username, "rfq.sent", pageID+" FAX・手渡し")
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": pageID, "date": today})
}

// renderRFQItems は見積依頼明細の足元に、備考の欄と「📄 PDFを作る」を出します（鏡・書ける人にだけ）。
func renderRFQItems(ctx *cms.MirrorContext, el *html.Node) (bool, error) {
	if el.Data != "table" {
		return true, nil
	}
	cms.DropChrome(el)
	if ctx.Viewer == nil || !canWritePage(ctx.Viewer, ctx.PageID) || cms.IsTemplateArea(page.FormatID(ctx.PageID)) {
		return false, nil
	}
	span := headerCellCount(el)
	pid := page.FormatID(ctx.PageID)
	note := ""
	if body, err := cms.ReadPageBody(pid); err == nil {
		if _, _, lines, err := readRFQDoc(body); err == nil {
			note = strings.Join(lines, "\n")
		}
	}
	appendFootHTML(el, span, "estimate-note-row", noteBoxHTML(pid, note, "/api/rfq/note", "備考（「○○用」など——紙では明細の下に刷ります）"))
	appendFootHTML(el, span, "estimate-actions",
		`<button type="button" class="chip-btn rfq-pdf-go" data-rfq-page="`+pid+`">📄 PDFを作る</button> `+
			`<span class="rfq-pdf-say"></span>`)
	return false, nil
}

// rfqSendViewHTML は見積依頼書ページの末尾の「見積依頼を送る」です（PDF の下——利用者:「PDFの下に発送項目」）。
func rfqSendViewHTML(user *auth.User, pageIDInt int) string {
	pid := page.FormatID(pageIDInt)
	if user == nil || !canWritePage(user, pageIDInt) || cms.IsTemplateArea(pid) {
		return ""
	}
	sent := cms.PageTagValue(database.DB, pageIDInt, EstimateSentTag)
	var b strings.Builder
	b.WriteString(`<h3 class="materials-title">✉️ 見積依頼を送る</h3>`)
	open := ` open`
	if strings.TrimSpace(sent) != "" {
		b.WriteString(`<p class="unorder-help">送付日: ` + stdhtml.EscapeString(sent) + `（もう一度送ることもできます）</p>`)
		open = ""
	}
	b.WriteString(`<details class="estimate-mail rfq-mail"` + open + `><summary>✉️ メールで送る</summary>` +
		`<div data-mail-compose="` + RFQMailPurpose + `" data-mail-page="` + pid + `"></div></details>`)
	b.WriteString(`<p class="unorder-help">FAX・手渡しで送ったら: ` +
		`<button type="button" class="chip-btn rfq-sent-go" data-rfq-page="` + pid + `">送った（FAX・手渡し）</button> ` +
		`<span class="rfq-sent-say"></span>（送付日に今日の日付を書きます）</p>`)
	return b.String()
}

func rfqMailDefaults(user *auth.User, pageID string) (comm.ComposeDraft, error) {
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		return comm.ComposeDraft{}, errors.New("見積依頼書のページを読めません: " + err.Error())
	}
	head, rows, _, err := readRFQDoc(body)
	if err != nil {
		return comm.ComposeDraft{}, err
	}
	supplier := strings.TrimSpace(head[SupplierTag])
	var b strings.Builder
	b.WriteString(supplier + "\nご担当者様\n\nいつもお世話になっております。\n見積依頼書をお送りいたします。お手数ですが、単価をご記入のうえ、" +
		"ご返送くださいますようお願いいたします。\n\n")
	if sig := contacts.MySignature(user, "メールの署名"); len(sig) > 0 {
		b.WriteString(strings.Join(sig, "\n") + "\n")
	}
	d := comm.ComposeDraft{
		Purpose: RFQMailPurpose, PageID: pageID,
		To:        supplierAddresses(user, supplier),
		Subject:   "見積依頼（№ " + head[RFQNoTag] + "）",
		Body:      b.String(),
		SendNote:  "印の付いた見積依頼書のPDFは送るときに作って添付し（このページの添付にも残ります）、送れたら「" + EstimateSentTag + "」のタグに今日の日付を書きます。",
		Generated: "見積依頼書 " + pageID + ".pdf",
		Reload:    true,
	}
	if len(d.To) == 0 {
		d.Notes = append(d.Notes, "⚠ 「"+supplier+"」の連絡先が連絡帳にありません（題が一致する組織ページに「"+
			contacts.EmailTag+"」のタグを付けると、ここに出ます）。")
	}
	// 外注加工の資料（行の 弊社品番＋番号 の「資料 <番号>」）——発注書と同じ。全部に印を付けて並べる（人が外せる）。
	docs, notes := orderDocs(user, rows)
	for _, doc := range docs {
		d.Attachments = append(d.Attachments, comm.ComposeAttachment{PageID: doc.PageID, File: doc.File, Name: doc.Name, Checked: true})
	}
	d.Notes = append(d.Notes, notes...)
	return d, nil
}

func prepareRFQMail(w http.ResponseWriter, r *http.Request, pageID string) ([]comm.ComposeAttachment, bool) {
	user := auth.CurrentUser(r)
	if !requireWritableIdle(w, r, pageID) {
		return nil, false
	}
	made, ok := makeRFQPDF(w, user, pageID)
	if !ok {
		return nil, false
	}
	return []comm.ComposeAttachment{{PageID: pageID, File: made.File, Name: "見積依頼書 " + pageID + ".pdf", Checked: true}}, true
}

func afterRFQMail(user *auth.User, pageID, _ string) error {
	n, err := strconv.Atoi(pageID)
	if err != nil {
		return errors.New("見積依頼書のページIDが不正です")
	}
	if !page.GetPerms(n).CanWrite(user) || cms.IsTemplateArea(pageID) {
		return errors.New("送付日を書けませんでした（このページを書き換える権限がありません）")
	}
	if holder, open := editlock.Locks.EditorOpen(n); open {
		return errors.New("送付日を書けませんでした（このページは編集中です（" + holder + "））")
	}
	today := time.Now().Format("2006-01-02")
	if err := cms.RewriteBody(pageID, user.Username, func(cur string) string {
		return withTagValues(cur, EstimateSentTag, []string{today}, true)
	}); err != nil {
		return errors.New("送付日を書けませんでした: " + err.Error())
	}
	auth.Audit(user.Username, "rfq.sent", pageID+" mail")
	return nil
}
