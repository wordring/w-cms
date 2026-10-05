package toho

// ─────────────────────────────────────────────────────────────────────────
// 見積依頼の記録が無い返事から、見積依頼書ページを作る（2026-10-05・移行期）
//
// 利用者:「移行期なので見積依頼の記録が無い。見積回答があります。これはどうしたら良いですか？」→（筆者の勧め「返事から見積依頼書
// ページを作る」に）「/001863 は弊社品番に結び付けられますか？」→（明細の品名欄の図面番号で3行とも加工製品ページに当たると答えて）
// 「作る」・品名の無い最後の行は「（上の行の品物）の20個の単価」（上の行と同じ品物の数量違い）。
//
//	GET  /api/rfq-reply/draft?page_id=   … 返事ページの「業者の返事（読んだまま）」の表を、見積依頼明細の列に読み替えた**案**
//	                                       （弊社品番は品名・品番の欄の図面番号・品番から、加工製品ページが1つに決まれば当てる）
//	POST /api/rfq-reply/make-rfq         … 人が直した案で見積依頼書ページを作り（回答あり・単価入り・回答日と見積依頼日は返事の
//	                                       回答日——過去の見積もりの移しと同じ createAnsweredRFQPage）、返事ページを結んでその子へ移す
//
// ⚠ **Gemini は呼びません**——読んだままの表は解析のときに読んであるので、列の見出しで当社の列へ並べ替えるだけ（見出しは業者ごとに違う
// ——「詳 細」「数 量」「単 価」のように空白が入る）。読み替えは案で、人が直してから作る。
// ⚠ 品名の欄が空で数量・単価だけの行は、**上の行と同じ品物の数量違い**とみなして品物を写す（利用者の答え）——備考にそう書く。
// ⚠ 合計・小計・消費税の行は入れない（品物ではない）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// replyColumnOf は業者の表の見出しを、見積依頼明細の列（ourOrderLine の鍵）へ読み替えます（読み替えない列は空）。
func replyColumnOf(header string) string {
	h := strings.Map(func(r rune) rune {
		if r == ' ' || r == '　' || r == '\t' {
			return -1
		}
		return r
	}, cms.NormalizeText(header))
	has := func(words ...string) bool {
		for _, w := range words {
			if strings.Contains(h, w) {
				return true
			}
		}
		return false
	}
	switch {
	case has("金額", "合計", "小計"):
		return ""
	case has("単価"):
		return "cost"
	case has("単位"):
		return "unit"
	case has("数量", "個数", "ロット") || h == "数":
		return "quantity"
	case has("備考", "摘要"):
		return "note"
	case has("品番", "図番", "図面番号", "型式", "型番"):
		return "item_id"
	case has("材質"):
		return "material"
	case has("形状"):
		return "shape"
	case has("寸法", "サイズ"):
		return "size"
	case has("表面", "色"):
		return "color"
	case has("加工内容", "工程"):
		return "work"
	case has("品名", "品目", "詳細", "名称", "内容", "仕様", "明細"):
		return "item_name"
	}
	return ""
}

// replyTotalRe は品物ではない行（合計・小計・消費税・値引）の品名です。
var replyTotalRe = regexp.MustCompile(`^(合計|小計|総計|消費税|税|値引|出精値引|送料)`)

// codeTokenRe は品名の中の図面番号・品番らしい並びです（英字か数字で始まり、数字を含む4字以上）。
var codeTokenRe = regexp.MustCompile(`[A-Za-z0-9][A-Za-z0-9_\-]{3,}`)

// productByCodeInText は文の中の図面番号・品番から、加工製品ページがちょうど1つに決まればそのページ番号と当てた番号を返します。
func productByCodeInText(user *auth.User, texts ...string) (string, string) {
	for _, t := range texts {
		for _, tok := range codeTokenRe.FindAllString(cms.NormalizeText(t), -1) {
			if !strings.ContainsAny(tok, "0123456789") {
				continue
			}
			seen := map[int]bool{}
			for _, tag := range []string{"品番", DrawingNoTag} {
				ids, _ := cms.PagesByTagLoose(database.DB, tag, tok)
				for _, id := range ids {
					if !cms.IsTemplateArea(page.FormatID(id)) && page.CanView(user, id) {
						seen[id] = true
					}
				}
			}
			if len(seen) == 1 {
				for id := range seen {
					return page.FormatID(id), tok
				}
			}
		}
	}
	return "", ""
}

// rfqReplyDraftRow は案の1行です（画面に出し、人が直して作る口へ戻す）。
type rfqReplyDraftRow struct {
	ourOrderLine
	ProductTitle string `json:"product_title"` // 当てた加工製品ページの題（画面の手掛かり）
}

// readReplyTable は返事ページの本文から「業者の返事（読んだまま）」の表を見出しと行で読みます。
func readReplyTable(body string) (headers []string, rows [][]string) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return nil, nil
	}
	box := findElement(nodes, func(n *html.Node) bool {
		if n.Data != "details" {
			return false
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && c.Data == "summary" && strings.TrimSpace(textOf(c)) == rfqReplySourceCaption {
				return true
			}
		}
		return false
	})
	if box == nil {
		return nil, nil
	}
	tbl := findElement([]*html.Node{box}, func(n *html.Node) bool { return n.Data == "table" })
	if tbl == nil {
		return nil, nil
	}
	trs := rowsOf(tbl)
	if len(trs) == 0 {
		return nil, nil
	}
	headers = replyCellTexts(trs[0])
	for _, tr := range trs[1:] {
		rows = append(rows, replyCellTexts(tr))
	}
	return headers, rows
}

// rfqReplyDraft は返事ページの読んだままの表を、見積依頼明細の行の案にします。
func rfqReplyDraft(user *auth.User, body string) []rfqReplyDraftRow {
	headers, rows := readReplyTable(body)
	cols := make([]string, len(headers))
	for i, h := range headers {
		cols[i] = replyColumnOf(h)
	}
	var out []rfqReplyDraftRow
	for _, cells := range rows {
		v := map[string]string{}
		for i, c := range cells {
			if i < len(cols) && cols[i] != "" && strings.TrimSpace(c) != "" {
				if v[cols[i]] != "" {
					v[cols[i]] += " " // 同じ列へ2つの見出しが当たったら（品名と仕様など）つなぐ
				}
				v[cols[i]] += strings.Join(strings.Fields(c), " ") // セルの中の改行（<br>）・続く空白は1つの空白に
			}
		}
		if len(v) == 0 || replyTotalRe.MatchString(v["item_name"]) {
			continue
		}
		r := rfqReplyDraftRow{ourOrderLine: ourOrderLine{
			ItemID: v["item_id"], ItemName: v["item_name"], Work: v["work"], Material: v["material"], Shape: v["shape"], Size: v["size"],
			Color: v["color"], Quantity: v["quantity"], Unit: v["unit"], Cost: cleanPrice(v["cost"]), Note: v["note"],
		}}
		empty := r.ItemID == "" && r.ItemName == "" && r.Work == "" && r.Material == "" && r.Size == ""
		if empty && len(out) > 0 && (r.Quantity != "" || r.Cost != "") {
			// 品名の無い行は、上の行と同じ品物の数量違い（利用者の答え・2026-10-05）。
			prev := out[len(out)-1]
			r.ProductID, r.ProductTitle, r.ItemID, r.ItemName, r.Work = prev.ProductID, prev.ProductTitle, prev.ItemID, prev.ItemName, prev.Work
			r.Material, r.Shape, r.Size, r.Color, r.Kind = prev.Material, prev.Shape, prev.Size, prev.Color, prev.Kind
			r.Note = strings.TrimSpace(strings.Join([]string{r.Note, "上の行と同じ品物（数量違い）"}, " "))
			out = append(out, r)
			continue
		}
		if pid, code := productByCodeInText(user, r.ItemID, r.ItemName); pid != "" {
			r.ProductID, r.ProductTitle = pid, pageTitleOf(pid)
			if r.ItemID == "" {
				r.ItemID = code
			}
		}
		out = append(out, r)
	}
	return out
}

// replyPageOrFail は書ける見積依頼の返事ページか、まだ見積依頼書に結ばれていないかを確かめます（断るときは応答を書く）。
func replyPageOrFail(w http.ResponseWriter, r *http.Request, raw string, mustWrite bool) (string, map[string][]string, bool) {
	var id string
	var ok bool
	if mustWrite {
		id, ok = gateWritablePage(w, r, raw)
	} else {
		var n int
		id, n, _, ok = cms.GateJSONPageRead(w, r, raw)
		_ = n
	}
	if !ok {
		return "", nil, false
	}
	tags, err := cms.TagsOfPage(database.DB, pageNum(id))
	if err != nil || !isRFQReplyTags(tags) {
		cms.JSONFail(w, http.StatusBadRequest, "見積依頼の返事ページではありません")
		return "", nil, false
	}
	if strings.TrimSpace(cms.FirstTag(tags, RFQReplyLinkTag)) != "" {
		cms.JSONFail(w, http.StatusConflict, "この返事はもう見積依頼書に結ばれています")
		return "", nil, false
	}
	return id, tags, true
}

// RFQReplyDraftAPIHandler は GET /api/rfq-reply/draft?page_id= です。
func RFQReplyDraftAPIHandler(w http.ResponseWriter, r *http.Request) {
	id, tags, ok := replyPageOrFail(w, r, r.URL.Query().Get("page_id"), false)
	if !ok {
		return
	}
	body, err := cms.ReadPageBody(id)
	if err != nil {
		cms.JSONFail(w, http.StatusNotFound, "ページを読めません")
		return
	}
	rows := rfqReplyDraft(auth.CurrentUser(r), body)
	if len(rows) == 0 {
		cms.JSONFail(w, http.StatusBadRequest, "業者の返事（読んだまま）の表がありません（見積依頼書の明細は手で書いてください）")
		return
	}
	if rows == nil {
		rows = []rfqReplyDraftRow{}
	}
	cms.WriteJSON(w, map[string]any{"success": true, "supplier": cms.FirstTag(tags, SupplierTag),
		"date": cms.FirstTag(tags, RFQAnsweredTag), "rows": rows})
}

// RFQReplyMakeRFQAPIHandler は POST /api/rfq-reply/make-rfq です（入力: {page_id（返事ページ）, rows, note}）。
func RFQReplyMakeRFQAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string         `json:"page_id"`
		Rows   []ourOrderLine `json:"rows"`
		Note   string         `json:"note"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	replyID, tags, ok := replyPageOrFail(w, r, req.PageID, true)
	if !ok {
		return
	}
	var lines []ourOrderLine
	priced := 0
	for _, ln := range req.Rows {
		ln.Cost = cleanPrice(ln.Cost)
		if pid := strings.TrimSpace(ln.ProductID); pid != "" {
			norm, okID := page.NormalizeID(pid)
			if !okID {
				cms.JSONFail(w, http.StatusBadRequest, "弊社品番「"+pid+"」がページ番号ではありません")
				return
			}
			ln.ProductID = norm
		}
		if strings.TrimSpace(ln.ItemName+ln.ItemID+ln.Material+ln.Work+ln.Cost) == "" {
			continue
		}
		if ln.Cost != "" {
			priced++
		}
		lines = append(lines, ln)
	}
	if priced == 0 {
		cms.JSONFail(w, http.StatusBadRequest, "単価の入った行がありません")
		return
	}
	supplier := cms.FirstTag(tags, SupplierTag)
	if strings.TrimSpace(supplier) == "" {
		cms.JSONFail(w, http.StatusBadRequest, "返事ページに仕入先がありません（仕入先のタグを書いてください）")
		return
	}
	note := strings.TrimSpace(req.Note)
	if note == "" {
		note = "見積依頼の記録の無い返事（/" + replyID + "）から作った見積依頼書——見積依頼日は返事の回答日"
	}
	newID, code, err := createAnsweredRFQPage(user, supplier, cms.FirstTag(tags, RFQAnsweredTag), note, lines)
	if err != nil {
		cms.JSONFail(w, code, err.Error())
		return
	}
	auth.Audit(user.Username, "rfq-reply.make-rfq", newID+" ← "+replyID+" "+strconv.Itoa(len(lines))+"行")
	if err := linkRFQReply(user, replyID, newID); err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "見積依頼書ページ /"+newID+" は作りましたが、返事を結べません: "+err.Error())
		return
	}
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": newID, "rows": len(lines)})
}

// replyCellTexts は行のセルの文字です——`<br>` は空白として読む（品名と図面番号が改行で分かれて書かれていても、くっつけない）。
func replyCellTexts(tr *html.Node) []string {
	var out []string
	for _, c := range cellsOf(tr) {
		var b strings.Builder
		var walk func(n *html.Node)
		walk = func(n *html.Node) {
			switch {
			case n.Type == html.TextNode:
				b.WriteString(n.Data)
			case n.Type == html.ElementNode && n.Data == "br":
				b.WriteString(" ")
			}
			for k := n.FirstChild; k != nil; k = k.NextSibling {
				walk(k)
			}
		}
		walk(c)
		out = append(out, b.String())
	}
	return out
}
