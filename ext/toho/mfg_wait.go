package toho

// ─────────────────────────────────────────────────────────────────────────
// 製造待ち——案件残表から「製造」へ入れて、そこで開始する（2026-10-21・【考察】案件残から製造へ）
//
//	2026-10-21 利用者:「表の先頭にチェックボックスを付け、表の下に「製造」ボタンを付けます。チェックしてボタンを押すと「製造」フォルダへ入ります」
//	2026-10-21 利用者:「製造フォルダに入った案件は表で分かるように表示します。この表の先頭にはチェックボックスがあり、表の下には「開始」ボタンがあります。
//	ここで開始した案件は製造のひとまとまりとなります。ただし、開始した案件はいつでも単体で取り消せるようにします。」
//	2026-10-21 利用者:「製造フォルダの表に入った案件もキャンセルして案件フォルダに戻すことが出来ます。」
//
//   - 案件残表の先頭のチェックと「🏭 製造開始」（/api/mfg/wait・2026-10-22 までの名前は「🏭 製造」）: 選んだ案件明細の行の、まだ製造に入っていない数を、その行の「製造待ち」の欄へ書く。
//   - 「製造」の置き場の「製造待ち」のビュー: 製造待ちの行を客先ごとの表に並べる。先頭のチェックと「▶ 開始」（/api/mfg/start）で、
//     製造ページ（製造のひとまとまり・1社）を作り、製造待ちの欄を空に戻す。「↩ 案件へ戻す」（/api/mfg/unwait）は欄を空にするだけ。
//   - 製造ページの製造明細表の「↩ 製造待ちへ」（/api/mfg/back）: その行を製造明細から外し、数を案件明細の「製造待ち」へ戻す。
//
// ⚠ **状態は案件明細の行（製造待ちの数）と、製造ページの製造明細表の行が持ちます**——ほかに表は持ちません。
// 戻しても、その行のために作った書類（部材発注書・納品書など）は残ります。部材は案件ごとに引き算するので、
// 別の製造で始め直しても二重に発注しません（mfgPartLines が UnorderedItems の「案件×加工製品」の残りを使う）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"
	stdhtml "html"
	"net/http"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

const (
	// MfgWaitLabel は案件明細表の「製造待ち」の列の見出しです。
	MfgWaitLabel = "製造待ち"
	// MfgWaitViewType は「製造」の置き場に置く、製造待ちのビューの形式です。
	MfgWaitViewType = "mfg-wait"
)

func init() {
	cms.RegisterVocab(cms.VocabDef{
		Type:        MfgWaitViewType,
		DisplayName: "製造待ち",
		Category:    "ビュー",
		Icon:        "⏳",
		Element:     "section",
		View:        true,
	})
	cms.RegisterView(MfgWaitViewType, mfgWaitViewHTML)
}

// orderOrQuote は、製造に入れてよい案件ページの種類か（注文と見積の依頼・2026-10-19 から。自社案件も・2026-10-21）。
func orderOrQuote(k string) bool {
	return k == PageKindCustomerOrder || k == PageKindCustomerQuote || k == PageKindOwnCase
}

// waitOf は案件明細の行の「製造待ち」の数です（空・読めなければ 0）。
func waitOf(v map[string]string) float64 {
	n, ok := estimateNumber(v["mfg-wait"])
	if !ok || n < 0 {
		return 0
	}
	return n
}

// waitPick は画面で選んだ案件明細の1行です（案件ページ・行の番号・品番——行の取り違えを防ぐ照合）。
type waitPick struct {
	Order string `json:"order"`
	Row   int    `json:"row"`
	Item  string `json:"item"`
}

// writeWait は案件ページ orderID の案件明細表の row 行目の「製造待ち」を oldVal から newVal へ書きます
// （照合は行番号・品番・いまの値の3つ）。書けなければ、状態の番号と理由を返します。
func writeWait(user *auth.User, orderID string, row int, itemNo, oldVal, newVal string) (int, error) {
	if err := requireWriteTo(user, orderID); err != nil {
		return http.StatusForbidden, err
	}
	body, err := cms.ReadPageBody(orderID)
	if err != nil {
		return http.StatusNotFound, errors.New("案件ページを読めません: /" + orderID)
	}
	_, cellErr, err := rebuildBody(orderID, user.Username, body, func(cur string) (string, error) {
		return setItemTableCell(cur, clientOrderItemsType, row, itemNo, MfgWaitLabel, oldVal, newVal)
	})
	if err != nil {
		return http.StatusInternalServerError, errors.New("保存できません: " + err.Error())
	}
	if cellErr != nil {
		if errors.Is(cellErr, errCellConflict) {
			return http.StatusConflict, errors.New("/" + orderID + " の " + strconv.Itoa(row+1) + " 行目が別の場所で変わっています（開き直してください）")
		}
		return http.StatusConflict, errors.New("/" + orderID + ": " + cellErr.Error())
	}
	return http.StatusOK, nil
}

// decodeWaitPicks は {items:[{order,row,item}]} を読みます（1つも無ければ断る）。
func decodeWaitPicks(w http.ResponseWriter, r *http.Request, none string) ([]waitPick, bool) {
	var req struct {
		Items []waitPick `json:"items"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return nil, false
	}
	if len(req.Items) == 0 {
		cms.JSONFail(w, http.StatusBadRequest, none)
		return nil, false
	}
	return req.Items, true
}

// MfgWaitAPIHandler は POST /api/mfg/wait です（案件残表の「🏭 製造開始」）。入力: {items:[{order, row, item}]}。
// 選んだ行の、まだ製造に入っていない数（数量 − 手の付いた数〔製造ページに入れた数か納めた数の大きいほう〕 − いまの製造待ち）を、
// その行の「製造待ち」へ足します。
func MfgWaitAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	items, ok := decodeWaitPicks(w, r, "案件残表の先頭のチェックで、製造へ入れる行を選んでください")
	if !ok {
		return
	}
	mfg := newMfgLookup()
	n := 0
	for _, p := range items {
		id, row, why := pickedOrderRow(user, deliveryPick{Order: p.Order, Row: p.Row, Item: p.Item}, orderOrQuote, false)
		if why != "" {
			cms.JSONFail(w, http.StatusConflict, why)
			return
		}
		cur := waitOf(row.Values)
		free := float64(row.Num("quantity")) - mfg.handled(id, row.Values["item-id"], row.Values["item-name"]) - cur
		if free <= 0 {
			continue // もう全部、製造に入っている
		}
		if code, err := writeWait(user, id, row.RowNo, row.Values["item-id"], row.Values["mfg-wait"], fmtQty(cur+free)); err != nil {
			cms.JSONFail(w, code, err.Error())
			return
		}
		n++
	}
	if n == 0 {
		cms.JSONFail(w, http.StatusConflict, "選んだ行は、もう全部が製造に入っています")
		return
	}
	auth.Audit(user.Username, "mfg.wait", strconv.Itoa(n)+"行")
	cms.WriteJSON(w, map[string]any{"success": true, "rows": n})
}

// MfgUnwaitAPIHandler は POST /api/mfg/unwait です（製造待ちの「↩ 案件へ戻す」）。入力: {items:[{order, row, item}]}。
// 選んだ行の「製造待ち」を空に戻します（案件残表の、まだ製造に入っていない数に戻る）。
func MfgUnwaitAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	items, ok := decodeWaitPicks(w, r, "製造待ちの表の先頭のチェックで、案件へ戻す行を選んでください")
	if !ok {
		return
	}
	n := 0
	for _, p := range items {
		id, row, why := pickedOrderRow(user, deliveryPick{Order: p.Order, Row: p.Row, Item: p.Item}, orderOrQuote, false)
		if why != "" {
			cms.JSONFail(w, http.StatusConflict, why)
			return
		}
		if waitOf(row.Values) <= 0 {
			continue
		}
		if code, err := writeWait(user, id, row.RowNo, row.Values["item-id"], row.Values["mfg-wait"], ""); err != nil {
			cms.JSONFail(w, code, err.Error())
			return
		}
		n++
	}
	auth.Audit(user.Username, "mfg.unwait", strconv.Itoa(n)+"行")
	cms.WriteJSON(w, map[string]any{"success": true, "rows": n})
}

// MfgStartAPIHandler は POST /api/mfg/start です（製造待ちの「▶ 開始」）。入力: {items:[{order, row, item}]}。
// 選んだ行の製造待ちの数で製造ページ（1社）を作り、その行の「製造待ち」を空に戻します。
func MfgStartAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	items, ok := decodeWaitPicks(w, r, "製造待ちの表の先頭のチェックで、開始する行を選んでください")
	if !ok {
		return
	}
	// 開始は1つずつ通す。製造待ちを先に外してから製造ページを作る（作れなければ戻す）——2つのタブで同時に押すと、
	// どちらも製造待ちを見て、同じ数の製造ページが2枚できていた（2026-10-22 の見直し）。
	mfgStartMu.Lock()
	defer mfgStartMu.Unlock()
	mfg, machines := newMfgLookup(), newMachineLookup(viewCheck(user))
	picked := map[string]bool{}
	type started struct {
		order, item, old string
		row              int
	}
	var lines []mfgLine
	var done []started
	client := ""
	for _, p := range items {
		id, row, why := pickedOrderRow(user, deliveryPick{Order: p.Order, Row: p.Row, Item: p.Item}, orderOrQuote, false)
		if why != "" {
			cms.JSONFail(w, http.StatusConflict, why)
			return
		}
		if k := id + "#" + strconv.Itoa(row.RowNo); picked[k] {
			continue // 同じ行が2度来ても1度だけ
		} else {
			picked[k] = true
		}
		wait := waitOf(row.Values)
		if wait <= 0 {
			cms.JSONFail(w, http.StatusConflict, "/"+id+" の「"+strings.TrimSpace(row.Values["item-name"])+"」は製造待ちではありません（開き直してください）")
			return
		}
		ln, why := mfgLineOf(user, deliveryPick{Order: id, Row: row.RowNo, Item: p.Item, Qty: fmtQty(wait)}, mfg, machines)
		if why != "" {
			cms.JSONFail(w, http.StatusConflict, why)
			return
		}
		var same bool
		if client, same = oneClient(client, ln.Client); !same {
			cms.JSONFail(w, http.StatusBadRequest, "1つの製造は1社です（「"+client+"」と「"+ln.Client+"」が混ざっています）")
			return
		}
		lines = append(lines, ln)
		done = append(done, started{order: id, item: row.Values["item-id"], old: row.Values["mfg-wait"], row: row.RowNo})
	}
	// 先に製造待ちから外す（いまの値のときだけ外す——ほかの人が替えていたら止める）。
	var cleared []started
	restore := func() []string {
		var notes []string
		for _, s := range cleared {
			if _, err := writeWait(user, s.order, s.row, s.item, "", s.old); err != nil {
				notes = append(notes, err.Error())
			}
		}
		return notes
	}
	for _, s := range done {
		if code, err := writeWait(user, s.order, s.row, s.item, s.old, ""); err != nil {
			notes := restore()
			msg := "製造待ちを外せません（開き直してください）: " + err.Error()
			if len(notes) > 0 {
				msg += "。⚠ 戻せなかった製造待ちがあります: " + strings.Join(notes, "／")
			}
			cms.JSONFail(w, code, msg)
			return
		}
		cleared = append(cleared, s)
	}
	newID, code, err := createMfgPage(user, client, lines)
	if err != nil {
		msg := err.Error()
		if notes := restore(); len(notes) > 0 {
			msg += "。⚠ 製造待ちを戻せなかった行があります（案件ページの「" + MfgWaitLabel + "」を書き戻してください）: " + strings.Join(notes, "／")
		}
		cms.JSONFail(w, code, msg)
		return
	}
	auth.Audit(user.Username, "mfg.start", newID+" "+client+" "+strconv.Itoa(len(lines))+"行")
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": newID, "url": "/" + newID})
}

// mfgStartMu は製造の開始（/api/mfg/start）を1つずつ通すロックです。
var mfgStartMu sync.Mutex

// createMfgPage は、客先 client の製造ページを「製造」の置き場の下に作り、そのページ番号を返します
// （作れなければ、状態の番号と理由）。製造待ちの「▶ 開始」と /api/mfg/new が使います（2026-10-21 から、案件残表の「🏭 製造開始」は製造待ちへ入れるだけ——/api/mfg/wait。
// /api/mfg/new を呼ぶのは、いまは E2E だけ）。
func createMfgPage(user *auth.User, client string, lines []mfgLine) (string, int, error) {
	sort.SliceStable(lines, func(i, j int) bool { return lines[i].Due < lines[j].Due })
	tmpl, err := cms.PageTemplateBody(MfgTemplate)
	if err != nil {
		return "", http.StatusConflict, errors.New("製造ページを作れません: " + err.Error())
	}
	start := time.Now().Format("2006-01-02")
	if _, err := buildMfgHTML(tmpl, "", client, start, lines); err != nil {
		return "", http.StatusConflict, errors.New("製造ページを作れません: " + err.Error())
	}
	boxID, err := cms.EnsureTopLevelBox(MfgBoxTitle, user.Username)
	if err != nil {
		return "", http.StatusConflict, errors.New("「" + MfgBoxTitle + "」を用意できません: " + err.Error())
	}
	newID, err := newBuiltPage(boxID, user, "製造", func(newID string) (string, error) {
		return buildMfgHTML(tmpl, newID, client, start, lines)
	})
	if err != nil {
		return "", createFailStatus(err, http.StatusInternalServerError), err
	}
	return newID, http.StatusOK, nil
}

// MfgBackAPIHandler は POST /api/mfg/back です（製造明細表の「↩ 製造待ちへ」）。入力: {page_id, row, item}（row は製造明細表のデータ行の番号）。
// その行を製造明細表から外し、数を案件明細の「製造待ち」へ戻します。
// ⚠ この製造で納めた分がある行は戻しません（戻すと、納めた分まで作り直すことになる）。
func MfgBackAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
		Row    int    `json:"row"`
		Item   string `json:"item"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	pageID, okID := gateWritablePage(w, r, req.PageID)
	if !okID || !requireMfgPage(w, pageID) {
		return
	}
	mfgID := pageNum(pageID)
	if !mfgIsOpen(mfgID) {
		cms.JSONFail(w, http.StatusConflict, "この製造はもう閉じています（記録へ移したものは戻せません）")
		return
	}
	rows, err := cms.VocabTableRowsOf(database.DB, mfgID, MfgItemsType)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "製造明細表を読めません: "+err.Error())
		return
	}
	var line *cms.VocabRow
	for i := range rows {
		if rows[i].RowNo == req.Row {
			line = &rows[i]
			break
		}
	}
	if line == nil || strings.TrimSpace(line.Values["item-id"]) != strings.TrimSpace(req.Item) {
		cms.JSONFail(w, http.StatusConflict, "製造明細表の "+strconv.Itoa(req.Row+1)+" 行目が変わっています（開き直してください）")
		return
	}
	v := line.Values
	qty, okQ := estimateNumber(v["quantity"])
	if !okQ || qty <= 0 {
		cms.JSONFail(w, http.StatusBadRequest, "この行の数量が読めません")
		return
	}
	order := strings.TrimPrefix(strings.TrimSpace(v["order"]), "/")
	if deliveredUnder(mfgID)[shippedKey(order, v["item-id"], v["item-name"])] > 0 {
		cms.JSONFail(w, http.StatusConflict, "この行は、この製造で納めた分があるので戻せません")
		return
	}
	// 戻す先の案件明細の行（品番、品番が空なら品名で探し、同じ品物の行が2つ以上なら納期の合う行）。
	id, orow, why := pickedOrderRow(user, deliveryPick{Order: order, Row: backOrderRow(order, v), Item: v["item-id"], Name: v["item-name"]}, orderOrQuote, true)
	if why != "" {
		cms.JSONFail(w, http.StatusConflict, why)
		return
	}
	if err := requireWriteTo(user, id); err != nil {
		cms.JSONFail(w, http.StatusForbidden, err.Error())
		return
	}
	// 先に製造明細から外す——外したあとで戻せなくても、数は「まだ製造に入っていない」に戻るだけで、なくならない。
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		cms.JSONFail(w, http.StatusNotFound, "製造ページを読めません: "+err.Error())
		return
	}
	_, cellErr, err := rebuildBody(pageID, user.Username, body, func(cur string) (string, error) {
		return removeItemTableRow(cur, MfgItemsType, req.Row, v["item-id"], v["quantity"])
	})
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "保存できません: "+err.Error())
		return
	}
	if cellErr != nil {
		failCellEdit(w, cellErr)
		return
	}
	cur := waitOf(orow.Values)
	if code, err := writeWait(user, id, orow.RowNo, orow.Values["item-id"], orow.Values["mfg-wait"], fmtQty(cur+qty)); err != nil {
		cms.JSONFail(w, code, "製造明細からは外しましたが、製造待ちへ戻せませんでした（案件残表から入れ直せます）: "+err.Error())
		return
	}
	auth.Audit(user.Username, "mfg.back", pageID+" → /"+id+" "+strings.TrimSpace(v["item-name"])+" "+fmtQty(qty))
	// 最後の行を外したら、消しても何も失わない製造ページはごみ箱へ（2026-10-23・mfg_drop.go）。
	res := map[string]any{"success": true}
	if d := dropEmptyMfgPage(user, pageID); d.Dropped {
		res["dropped"], res["box"] = true, d.Box
	} else if d.Kept != "" {
		res["kept"] = d.Kept
	}
	cms.WriteJSON(w, res)
}

// backOrderRow は、製造明細の行 v を戻す先の、案件 order の案件明細の行の番号です（見つからなければ -1）。
//
// ⚠ 同じ案件に同じ品物の行が2つ以上ある（納期を分けた）とき、製造明細の行と納期の合う行へ戻す（2026-10-26。それまでは
// 最初の行へ戻し、納期の違う行の製造待ちが増え、元の行は製造待ちに戻らなかった）。納期の合う行が無ければ最初の行。
func backOrderRow(order string, v map[string]string) int {
	id, ok := page.NormalizeID(order)
	if !ok {
		return -1
	}
	rows, err := cms.VocabTableRowsOf(database.DB, pageNum(id), clientOrderItemsType)
	if err != nil {
		return -1
	}
	want := shippedKey(id, v["item-id"], v["item-name"])
	due := normDue(v["due"])
	first := -1
	for _, r := range rows {
		if shippedKey(id, r.Values["item-id"], r.Values["item-name"]) != want {
			continue
		}
		if first < 0 {
			first = r.RowNo
		}
		if normDue(r.Values["due"]) == due {
			return r.RowNo
		}
	}
	return first
}

// normDue は納期を比べる形にします（日付として読めれば日付、読めなければ空白を詰めた文字）。
func normDue(s string) string {
	if d, ok := cms.NormalizeValue(cms.ColDate, s); ok {
		return d
	}
	return strings.Join(strings.Fields(s), "")
}

// removeItemTableRow は、形式 vocabType の明細表（ページに1つ）の row 行目（見出しを除いて0から）を消した本文を返します。
// ⚠ 照合は行番号・品番・数量の3つ——どれかが違えば errCellConflict（行が動いた・直された）。
func removeItemTableRow(body, vocabType string, row int, itemNo, qty string) (string, error) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return "", errors.New("本文を読めません")
	}
	tables := tablesOfType(nodes, vocabType)
	if len(tables) == 0 {
		return "", errors.New(displayNameOf(vocabType) + "が見つかりません")
	}
	rows := rowsOf(tables[0])
	if row < 0 || row+1 >= len(rows) {
		return "", errCellConflict
	}
	head := headerIndex(rows[0])
	tr := rows[row+1]
	cells := cellsOf(tr)
	at := func(label string) string {
		if i, ok := head[label]; ok && i < len(cells) {
			return strings.TrimSpace(textOf(cells[i]))
		}
		return ""
	}
	if at(ItemNoTag) != strings.TrimSpace(itemNo) || at("数量") != strings.TrimSpace(qty) {
		return "", errCellConflict
	}
	tr.Parent.RemoveChild(tr)
	return htmldoc.Render(nodes), nil
}

// ── 「製造」の置き場の製造待ちのビュー ─────────────────────────────────────

// mfgWaitViewHTML は製造待ちの行を、客先ごとの表に並べます（先頭のチェック・足元の「▶ 開始」「↩ 案件へ戻す」）。
// 1つの表が1社なので、表ごとに開始すれば1社の製造になります（2026-10-21 利用者の答え「1社だけ（いまと同じ）」）。
func mfgWaitViewHTML(user *auth.User, _ int) string {
	type waitRow struct {
		backlogRow
		Due string
	}
	byClient := map[string][]waitRow{}
	var clients []string
	for _, g := range backlogGroups(user) {
		for _, r := range g.Rows {
			if r.Waiting <= 0 {
				continue
			}
			if _, seen := byClient[g.Client]; !seen {
				clients = append(clients, g.Client)
			}
			byClient[g.Client] = append(byClient[g.Client], waitRow{r, g.Due})
		}
	}
	if len(clients) == 0 {
		return `<p class="child-list-empty">製造待ちはありません（案件残表で行の先頭にチェックを付けて「🏭 製造開始」を押すと、ここに並びます）。</p>`
	}
	var b strings.Builder
	for _, c := range clients {
		title := c
		if title == "" {
			title = "（発注元なし）"
		}
		b.WriteString(`<section class="backlog-sheet mfgwait-sheet w-no-table-menu"><h3 class="backlog-title">` + stdhtml.EscapeString(title) + `</h3>`)
		b.WriteString(`<table class="backlog-table mfgwait-table"><tbody><tr><th class="no-print">選ぶ</th><th>装置名</th><th>弊社品番</th><th>品番</th><th>品名</th>` +
			`<th>` + MfgWaitLabel + `</th><th>納期</th><th class="no-print">案件</th></tr>`)
		for _, r := range byClient[c] {
			b.WriteString(`<tr` + rowAttrs(r.backlogRow) + `>` +
				`<td class="cell-atomic no-print"><input type="checkbox" class="mfgwait-pick" aria-label="この行を選ぶ"` +
				` data-order="` + stdhtml.EscapeString(r.OrderPageID) + `" data-row="` + strconv.Itoa(r.RowNo) +
				`" data-item="` + stdhtml.EscapeString(r.ItemNo) + `"></td>` +
				machineCellHTML(r.backlogRow) + ourItemCellHTML(r.OurItemNo) +
				atomicCell(stdhtml.EscapeString(r.ItemNo)) + atomicCell(stdhtml.EscapeString(r.ItemName)) +
				atomicCell(strconv.Itoa(r.Waiting)) + atomicCell(stdhtml.EscapeString(r.Due)) +
				noPrintCell(`<a href="/`+stdhtml.EscapeString(r.OrderPageID)+`">`+stdhtml.EscapeString(orderLabel(r.backlogRow))+`</a>`) +
				`</tr>`)
		}
		b.WriteString(`</tbody></table>`)
		b.WriteString(`<div class="mfgwait-bar no-print"><button type="button" class="chip-btn mfgwait-start-go">▶ 選んだ行で開始</button> ` +
			`<button type="button" class="chip-btn mfgwait-back-go">↩ 選んだ行を案件へ戻す</button> <span class="mfgwait-say"></span></div></section>`)
	}
	return b.String()
}

// ── 製造明細表の「↩ 製造待ちへ」 ─────────────────────────────────────────
