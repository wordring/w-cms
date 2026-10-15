package toho

// ─────────────────────────────────────────────────────────────────────────
// 部材発注書の PDF——送付前は上書き・送付後は新しい版・送った PDF の記録（2026-10-14）
//
// 【要求】発注フォルダ:
//
//	「PDF作成」ボタン … 送付前は、このボタンを押すたびに、古いPDFを上書きして、表を基に新しいPDFが作成されます。
//	再送（差し替え）  … 古いPDFは消しません。送った紙は、送った事実として保存します。発注書ページに表示するのは
//	                    最新のPDFです。古いPDFと発信FAX、メール等は、現行PDFブロックの下にリンクとして表示します。
//	発注書を送信     … 送るPDFは、発注書ページに表示している最新のPDFです（送る直前に作り直しません）。
//
// それまでは「PDF作成」を押すたびに日時つきの添付が増え、メールは送る直前に作り直していました。
//
// ── 送ったかどうかは本文が覚えます ──
//
// 送ると（メール・FAX・手渡し）、見出し「送った部材発注書」の節に1行足します——送った日・送り方・送った PDF への
// リンク（メールなら控えのページへのリンクも）。節は表示中の PDF のすぐ下に置きます（無ければ部材発注明細の下）。
// 「表示中の PDF を送ったか」は、この節にその PDF のリンクがあるかで決めます。
//
//	表示中の PDF が送っていないもの → 「PDF作成」はその添付を上書きする（ID も表示も変わらない）
//	表示中の PDF が送ったもの        → 新しい添付を作って表示を差し替える（送った PDF は添付と節のリンクに残る）
//
// ⚠ **記録の節が無いページ（2026-10-14 より前に作った部材発注書）**は、行が1つでも出ていれば（発注済・納品済・
// 未納品）表示中の PDF を送ったものとみなします（上書きしない）——送った紙を黙って消さないためです。
// ─────────────────────────────────────────────────────────────────────────

import (
	stdhtml "html"
	"net/http"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

// orderSentHeading は送った部材発注書の記録の節の見出しです。
const orderSentHeading = "送った部材発注書"

// orderMailFileName は、メールで送るときの部材発注書の PDF の名前です（相手には保存名の日時は意味がないので外す）。
func orderMailFileName(pageID string) string {
	return "発注書 " + pageID + ".pdf"
}

// shownOrderPDF は、部材発注書ページに表示している PDF（最初のファイル表示）の保存名と、届いたときの名前を返します。
// そのページ自身の添付でなければ・読めなければ ok=false。
func shownOrderPDF(user *auth.User, pageID, body string) (stored, display string, ok bool) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return "", "", false
	}
	el := firstFileView(nodes)
	if el == nil {
		return "", "", false
	}
	ref, ok := cms.ParseFileRef(cms.Attr(el, cms.FileRefAttr))
	if !ok || ref.PageID != pageID {
		return "", "", false
	}
	return cms.AttachmentOfRef(user, ref)
}

// attachIDOf は保存名（`<id>.<拡張子>`）から添付の識別子を返します。
func attachIDOf(stored string) string {
	if dot := strings.LastIndexByte(stored, '.'); dot > 0 {
		return stored[:dot]
	}
	return stored
}

// orderSentRecord は記録の節（無ければ nil）を返します。nodes は本文の断片です。
func orderSentRecord(nodes []*html.Node) *html.Node {
	return findElement(nodes, func(n *html.Node) bool {
		return n.Data == "section" && sectionHeadingText(n) == orderSentHeading
	})
}

// shownPDFWasSent は、表示中の PDF（添付の識別子 attachID）をもう送ったかを返します（上の説明のとおり）。
func shownPDFWasSent(body, pageID, attachID string) bool {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return true // 読めない本文では上書きしない（送った紙を消さない側へ倒す）
	}
	if rec := orderSentRecord(nodes); rec != nil {
		for _, ref := range cms.FileRefsIn(rec) {
			if ref.PageID == pageID && ref.ID == attachID {
				return true
			}
		}
		return false
	}
	// 記録の前に作った部材発注書——行が1つでも出ていれば送ったとみなす。
	for _, t := range tablesOfType(nodes, ourOrderItemsType) {
		trs := rowsOf(t)
		if len(trs) < 2 {
			continue
		}
		si := headerIndexOf(trs[0], "状態")
		if si < 0 {
			continue
		}
		for _, tr := range trs[1:] {
			if orderLineSent(cellText(tr, si)) {
				return true
			}
		}
	}
	return false
}

// overwriteUnsentOrderPDF は、表示中の PDF がまだ送っていないものなら、その添付を作り直した中身で上書きします。
//
// done=false なら上書きしない場面です（表示中の PDF が無い・送ったもの）——呼ぶ側は新しい版を作ります。
// done=true のときは、上書きしたか（ok=true）、断る応答を書いたか（ok=false）です。
//
// ⚠ **決めるところから上書きまで、本文の錠の中で行います**——同じ時に「送った」が記録されると、送った PDF を
// 上書きしてしまうためです。錠の中で本文は書きません（同じ錠を2度取ると止まる——body_lock.go）。
func overwriteUnsentOrderPDF(w http.ResponseWriter, user *auth.User, pageID string) (made madeOrderPDF, done, ok bool) {
	defer cms.LockPageBody(pageID)()
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		return madeOrderPDF{}, false, false // 読めないことは新しい版の道（makePaper）が答える
	}
	stored, _, shown := shownOrderPDF(user, pageID, body)
	if !shown || !strings.EqualFold(stored[strings.LastIndexByte(stored, '.')+1:], "pdf") ||
		shownPDFWasSent(body, pageID, attachIDOf(stored)) {
		return madeOrderPDF{}, false, false
	}
	pdf, err := buildOrderPDF(body, user)
	if err != nil {
		failPaper(w, err)
		return madeOrderPDF{}, true, false
	}
	if err := cms.OverwriteAttachment(pageID, user.Username, stored, stampedPDFName(orderPaperTitleOf(body)), "pdf", pdf); err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "保存できません: "+err.Error())
		return madeOrderPDF{}, true, false
	}
	auth.Audit(user.Username, "order-pdf.overwrite", pageID+" "+stored)
	return madeOrderPDF{AttachID: attachIDOf(stored), File: stored, Overwrote: true}, true, true
}

// sentOrderFile は送った PDF 1つです（記録の1行に並べる）。
type sentOrderFile struct {
	Stored  string // 保存名
	Display string // 届いたときの名前（作った日時が入っている）
}

// orderSentLineHTML は記録の1行です——送った日・送り方・（メールなら控えのページ）・送った PDF のリンク。
func orderSentLineHTML(pageID, day, method, recordID string, files []sentOrderFile) string {
	var b strings.Builder
	b.WriteString(`<p>` + stdhtml.EscapeString(day) + `　` + stdhtml.EscapeString(method))
	if recordID != "" {
		b.WriteString(`（控え <a href="/` + stdhtml.EscapeString(recordID) + `">/` + stdhtml.EscapeString(recordID) + `</a>）`)
	}
	b.WriteString(`：`)
	if len(files) == 0 {
		b.WriteString(`部材発注書の PDF は添えていません`)
	}
	for i, f := range files {
		if i > 0 {
			b.WriteString(`　`)
		}
		b.WriteString(`📎 <a href="` + stdhtml.EscapeString(page.AttachmentURLFor(pageID, f.Stored)) +
			`" download="` + stdhtml.EscapeString(f.Display) + `">` + stdhtml.EscapeString(f.Display) + `</a>`)
	}
	b.WriteString(`</p>`)
	return b.String()
}

// withOrderSentLine は記録の節の末尾に lineHTML を足します。節が無ければ作ります——置き場は表示中の PDF の
// すぐ下（無ければ最初の部材発注明細の表の下、それも無ければ本文の末尾）。
func withOrderSentLine(body, lineHTML string) (string, error) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return "", err
	}
	line, err := htmldoc.ParseFragment(lineHTML)
	if err != nil {
		return "", err
	}
	root := &html.Node{Type: html.ElementNode, Data: "div"}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	all := []*html.Node{root}
	sec := orderSentRecord(all)
	if sec == nil {
		sec = &html.Node{Type: html.ElementNode, Data: "section"}
		h := &html.Node{Type: html.ElementNode, Data: "h2"}
		h.AppendChild(&html.Node{Type: html.TextNode, Data: orderSentHeading})
		sec.AppendChild(h)
		anchor := firstFileView(all)
		if anchor == nil {
			if ts := tablesOfType(all, ourOrderItemsType); len(ts) > 0 {
				anchor = ts[0]
			}
		}
		if anchor != nil && anchor.Parent != nil {
			anchor.Parent.InsertBefore(sec, anchor.NextSibling)
		} else {
			root.AppendChild(sec)
		}
	}
	for _, n := range line {
		sec.AppendChild(n)
	}
	var out []*html.Node
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		out = append(out, c)
	}
	return htmldoc.Render(out), nil
}

// recordOrderSent は「送った」ことを本文へ反映します——行を発注済みに・部材発注日を送った日に（markOrderSent）・
// 記録の節に1行。files が nil なら、表示中の PDF を送ったものとして記録します（FAX・手渡し——人が出したのは
// 画面に出ている紙）。cur は錠の中で読んだ本文です（RewriteBody の中から呼ぶ）。
func recordOrderSent(cur string, user *auth.User, pageID, day, method, recordID string, files []sentOrderFile) (string, int) {
	out, n := markOrderSent(cur, day)
	if files == nil {
		if stored, display, ok := shownOrderPDF(user, pageID, cur); ok {
			files = []sentOrderFile{{Stored: stored, Display: display}}
		}
	}
	if withLine, err := withOrderSentLine(out, orderSentLineHTML(pageID, day, method, recordID, files)); err == nil {
		out = withLine
	}
	return out, n
}

// mailedOrderFiles は、送信の控え（recordID）が添えたファイルのうち、この部材発注書ページの添付を返します
// （控えの「添付ファイル」の節のリンク——comm/mail の fillMailBody）。控えが無い・読めなければ nil。
func mailedOrderFiles(user *auth.User, pageID, recordID string) []sentOrderFile {
	if recordID == "" {
		return nil
	}
	body, err := cms.ReadPageBody(recordID)
	if err != nil {
		return nil
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return nil
	}
	files := []sentOrderFile{} // 控えが読めたら、空でも「何も添えていない」と記録する（nil と分ける）
	for _, n := range nodes {
		for _, ref := range cms.FileRefsIn(n) {
			if ref.PageID != pageID {
				continue
			}
			if stored, display, ok := cms.AttachmentOfRef(user, ref); ok {
				files = append(files, sentOrderFile{Stored: stored, Display: display})
			}
		}
	}
	return files
}

// orderNoteOf は部材発注書ページのいまの備考（相手に伝える欄）を、行を改行でつないで返します。
// 節が無ければ、2026-09-23 までの紙のタグ `備考` を読みます（readOrderDoc と同じ順）。
func orderNoteOf(body string) string {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return ""
	}
	if sec := findElement(nodes, func(n *html.Node) bool {
		return n.Data == "section" && sectionHeadingText(n) == orderNoteHeading
	}); sec != nil {
		return strings.Join(noteLinesOf(sec), "\n")
	}
	for _, n := range nodes {
		if v := strings.TrimSpace(cms.TagValue(n, orderNoteHeading)); v != "" {
			return v
		}
	}
	return ""
}

// OrderNoteAPIHandler は POST /api/our-order/note です。入力: {page_id, note}——部材発注書ページの「備考」の節を
// 書き換えます（見積書・部材見積依頼書の備考の欄と同じ口の形・estimate_note.go）。
func OrderNoteAPIHandler(w http.ResponseWriter, r *http.Request) {
	serveDocNote(w, r, isOurOrderPage, "部材発注書ページではありません", "our-order.note")
}

// isOurOrderPage は部材発注書ページ（テンプレートの外で、部材発注明細の表を持つ）かです。
func isOurOrderPage(pageID string) bool {
	if cms.IsTemplateArea(pageID) {
		return false
	}
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		return false
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return false
	}
	return len(tablesOfType(nodes, ourOrderItemsType)) > 0
}
