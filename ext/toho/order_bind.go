package toho

// 部材発注書の行を、加工製品の部材の行に結ぶ（2026-10-23・supplier_delivery.go のあと）。
//
// 業者の納品書から作った部材発注書の行は、納品書の品名・寸法のままで、どの加工製品のどの部材かが分かりません。
// 機械には決められないので、人が「🔗 加工製品の行に結ぶ」で選びます。
//
//   - 候補は、納品書の行の数字（寸法など）と、加工製品の部材の行の数字の重なりで並べます。仕入先が部材の行の推奨業者と
//     同じなら上へ。探す言葉（品番・題・部材の名前）でも絞れます。
//   - 結ぶと、行は部材の行と同じ値になります（弊社品番・種類・材質・形状・寸法・番号・加工内容 など——製造ページから
//     発注するときと同じ運び方・productNeeds）。数量・単価・状態・単位はそのまま。もとの品名・寸法は備考へ残します。
//   - 行は番号と中身の指紋（rowSig）で指します（ほかの人が行を足した・消したあとに、別の行へ効かないように）。
//
// > 2026-10-23 利用者:「加工製品の行への結びは人が選び、外注加工表にも最新単価を出してください。」

import (
	"net/http"
	"regexp"
	"sort"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// bindDescLabels は、結ぶときに部材の行の値で置き換える列です（部材が何かを語る列。数量・単価・状態などは触らない）。
var bindDescLabels = []string{"種類", "番号", "品番", "品名", "加工部品番号", "加工部品名", "加工内容", "材質", "形状", "寸法", "表面", "仕様", "支給"}

// bindCandidate は結ぶ相手の候補（加工製品の部材の行1つ）です。
type bindCandidate struct {
	ProductID    string `json:"product_id"`
	ProductTitle string `json:"product_title"`
	Kind         string `json:"kind"`
	Key          string `json:"key"`
	Label        string `json:"label"`
	Vendor       string `json:"vendor,omitempty"`
	Score        int    `json:"score"`
}

var bindNumRe = regexp.MustCompile(`[0-9]+(?:\.[0-9]+)?`)

// bindNumbers は文字の中の数（寸法・番号）を集めます（全角も畳む）。
func bindNumbers(s string) map[string]bool {
	out := map[string]bool{}
	for _, n := range bindNumRe.FindAllString(cms.NormalizeText(s), -1) {
		out[strings.TrimLeft(n, "0")] = true
	}
	delete(out, "")
	return out
}

// bindScore は数の重なりの点です（2桁以上の数は2点、1桁は1点）。
func bindScore(line, cand map[string]bool) int {
	score := 0
	for n := range line {
		if cand[n] {
			if len(n) >= 2 {
				score += 2
			} else {
				score++
			}
		}
	}
	return score
}

// resolveOrderRow は、部材発注書の本文 body の1つ目の部材発注明細表で、画面が送った行の番号 row（1から）と指紋 sig から、
// いまのその行の番号を引き直します（sig が空なら番号のまま・合う行がちょうど1つでなければ ok=false）。
// 行の状態を変える・行を外す口が、ロックの中で読んだ本文で使う（2026-10-26。それまでは番号だけで、ほかの人が行を足したり
// 外したりしたあとに押すと、別の行が「納品済」になって在庫表へ入る・別の行が外れることがあった）。
func resolveOrderRow(body string, row int, sig string) (int, bool) {
	if row < 1 {
		return row, row == 0 && sig == "" // 0（全部の行）は指紋を持たない
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return 0, false
	}
	tables := tablesOfType(nodes, ourOrderItemsType)
	if len(tables) == 0 {
		return 0, false
	}
	rows, ok := resolveRowsBySig(tables[0], []int{row}, []string{sig})
	if !ok || len(rows) != 1 {
		return 0, false
	}
	return rows[0], true
}

// orderLineAt は部材発注書 pageID の1つ目の部材発注明細表の、データ行 row（1から）を指紋 sig で引き直して返します。
func orderLineAt(nodes []*html.Node, row int, sig string) (table, tr *html.Node, ok bool) {
	tables := tablesOfType(nodes, ourOrderItemsType)
	if len(tables) == 0 {
		return nil, nil, false
	}
	rows, ok := resolveRowsBySig(tables[0], []int{row}, []string{sig})
	if !ok {
		return nil, nil, false
	}
	trs := rowsOf(tables[0])
	if rows[0] < 1 || rows[0] >= len(trs) {
		return nil, nil, false
	}
	return tables[0], trs[rows[0]], true
}

// rowLabelValues は表の行を、見出し → 値で返します（見出しが重なれば先勝ち）。
func rowLabelValues(table, tr *html.Node) map[string]string {
	out := map[string]string{}
	labels, texts := cellTexts(rowsOf(table)[0]), cellTexts(tr)
	for i, l := range labels {
		if _, dup := out[l]; dup || l == "" {
			continue
		}
		if i < len(texts) {
			out[l] = texts[i]
		}
	}
	return out
}

// bindCandidates は、行 vals に結ぶ相手の候補を、点の高い順に返します（q があれば、題・品番・部材の名前に q を含むものだけ）。
func bindCandidates(user *auth.User, vals map[string]string, supplier, q string, limit int) []bindCandidate {
	var text strings.Builder
	for _, l := range bindDescLabels {
		text.WriteString(vals[l] + " ")
	}
	lineNums := bindNumbers(text.String())
	q = cms.NormalizeText(strings.TrimSpace(q))
	ids, _ := cms.PagesByTag(database.DB, PageKindTag, PageKindProduct)
	canView := viewCheck(user)
	var out []bindCandidate
	for _, id := range ids {
		if !canView(id) || cms.IsTemplateArea(page.FormatID(id)) {
			continue
		}
		title := cms.PageTitleByID(id)
		tags, _ := cms.TagsOfPage(database.DB, id)
		head := title + " " + cms.FirstTag(tags, ItemNoTag) + " " + cms.FirstTag(tags, DrawingNoTag)
		for _, it := range productNeeds(database.DB, id, 1) {
			var desc []string
			for _, l := range bindDescLabels {
				if v := strings.TrimSpace(it.Values[l]); v != "" && l != "種類" {
					desc = append(desc, v)
				}
			}
			label := strings.Join(desc, " ")
			score := bindScore(lineNums, bindNumbers(label))
			if supplier != "" && it.Vendor != "" && sameCompany(supplier, it.Vendor) {
				score += 3
			}
			if q != "" {
				if !strings.Contains(cms.NormalizeText(head+" "+label), q) {
					continue
				}
				score += 100 // 探す言葉に当たったものを先に
			} else if score == 0 {
				continue
			}
			out = append(out, bindCandidate{ProductID: page.FormatID(id), ProductTitle: title, Kind: it.Kind, Key: it.Key,
				Label: label, Vendor: it.Vendor, Score: score})
		}
	}
	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Score != out[j].Score {
			return out[i].Score > out[j].Score
		}
		return out[i].ProductID < out[j].ProductID
	})
	if len(out) > limit {
		out = out[:limit]
	}
	return out
}

// OrderBindCandidatesAPIHandler は GET /api/our-order/bind-candidates?page_id=&row=&sig=&q= です（結ぶ相手の候補）。
func OrderBindCandidatesAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		cms.JSONFail(w, http.StatusMethodNotAllowed, "GET だけです")
		return
	}
	user := auth.CurrentUser(r)
	pageID, ok := cms.PageIDOrFail(w, r.URL.Query().Get("page_id"))
	if !ok {
		return
	}
	if user == nil || !page.CanView(user, pageNum(pageID)) {
		cms.JSONFail(w, http.StatusNotFound, "ページがありません")
		return
	}
	row, _ := strconv.Atoi(r.URL.Query().Get("row"))
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		cms.JSONFail(w, http.StatusNotFound, "部材発注書を読めません")
		return
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "本文を読めません")
		return
	}
	table, tr, ok := orderLineAt(nodes, row, r.URL.Query().Get("sig"))
	if !ok {
		cms.JSONFail(w, http.StatusConflict, "行が見つかりません（画面を読み直してください）")
		return
	}
	supplier := bodyTagValue(body, SupplierTag)
	cms.WriteJSON(w, map[string]any{"success": true,
		"candidates": bindCandidates(user, rowLabelValues(table, tr), supplier, r.URL.Query().Get("q"), 30)})
}

// OrderBindAPIHandler は POST /api/our-order/bind です。入力: {page_id, row, sig, product_id, key}。
// 部材発注明細表の行を、加工製品 product_id の部材の行（鍵 key）と同じ値にします。
func OrderBindAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID    string `json:"page_id"`
		Row       int    `json:"row"`
		Sig       string `json:"sig"`
		ProductID string `json:"product_id"`
		Key       string `json:"key"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := cms.PageIDOrFail(w, req.PageID)
	if !okID {
		return
	}
	pid, okP := page.NormalizeID(trimSlash(req.ProductID))
	if !okP || pid == "" || !page.CanView(user, pageNum(pid)) {
		cms.JSONFail(w, http.StatusBadRequest, "加工製品ページがありません")
		return
	}
	var item *ProcurementItem
	for _, it := range productNeeds(database.DB, pageNum(pid), 1) {
		if it.Key == req.Key {
			it := it
			item = &it
			break
		}
	}
	if item == nil {
		cms.JSONFail(w, http.StatusConflict, "加工製品ページの部材の行が見つかりません（直されたかもしれません。選び直してください）")
		return
	}
	if !requireWritableIdle(w, r, pageID) {
		return
	}
	failed := ""
	if !rewriteBodyOrFail(w, pageID, user.Username, func(cur string) string {
		out, why := bindOrderLine(cur, req.Row, req.Sig, pid, item)
		failed = why
		return out
	}) {
		return
	}
	if failed != "" {
		cms.JSONFail(w, http.StatusConflict, failed)
		return
	}
	auth.Audit(user.Username, "our-order.bind", pageID+" 行"+strconv.Itoa(req.Row)+" → "+pid+" "+item.Name)
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": pageID})
}

// bindOrderLine は本文 body の部材発注明細表の行を、加工製品 pid の部材 item の値にします（できなければ理由）。
func bindOrderLine(body string, row int, sig, pid string, item *ProcurementItem) (string, string) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return body, "本文を読めません"
	}
	table, tr, ok := orderLineAt(nodes, row, sig)
	if !ok {
		return body, "行が見つかりません（画面を読み直してください）"
	}
	head := headerIndex(rowsOf(table)[0])
	cells := cellsOf(tr)
	before := rowLabelValues(table, tr)
	set := func(label, v string) {
		if i, ok := head[label]; ok && i < len(cells) {
			setCellText(cells[i], v)
		}
	}
	// もとの品名・寸法などは備考へ残す（納品書に書かれていた言葉）。
	var was []string
	for _, l := range bindDescLabels {
		if v := strings.TrimSpace(before[l]); v != "" && l != "種類" && v != strings.TrimSpace(item.Values[l]) {
			was = append(was, v)
		}
	}
	set(OurItemNoTag, pid)
	set("種類", item.Kind)
	for _, l := range bindDescLabels[1:] {
		set(l, strings.TrimSpace(item.Values[l]))
	}
	if len(was) > 0 {
		note := strings.TrimSpace(before["備考"])
		add := "納品書の記載: " + strings.Join(was, " ")
		if !strings.Contains(note, add) {
			if note != "" {
				note += "／"
			}
			set("備考", note+add)
		}
	}
	return htmldoc.Render(nodes), ""
}
