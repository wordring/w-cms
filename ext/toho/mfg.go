package toho

// ─────────────────────────────────────────────────────────────────────────
// 製造ページ——1つの案件を1枚にまとめる（2026-10-19・【考察】製造ページ・作る順の1番）
//
//	2026-10-18 利用者:「受注残表でチェックして製造開始ボタンを押すと、製造フォルダに入るのはどうですか？」
//	2026-10-19 利用者:「製造フォルダはトップ直下に一つ、その下に受注ページでチェックを入れて作った製造ページがいくつかあり、
//	請求書発行時点で製造ページは記録フォルダ以下の年月以下に移動するというのはどうでしょうか？」
//	2026-10-19 利用者:「一つの案件は一社で良いです 残りの数の一部だけで案件を始めまることがあります」
//	2026-10-19 利用者:「3．製造ページの題名は「装置名、装置名・・・」（長くなったら短縮）を基本に、装置名が無い場合、品名で置き換えてはどうでしょう」
//
//   - 置き場はトップ直下の「製造」（テンプレートから作る）。その下に製造ページ（`ページ種類：製造`）。年月では分けない。
//   - 受注残表で行を選んで「🏭 製造開始」——今回作る数を直せる（一部だけで始めてよい）。1枚に1社。
//   - 製造ページの「製造明細表」に、受注明細の行を写す（受注・品番・品名・装置名・数量・単位・納期）。
//   - 受注残表の行には「製造中」と、その製造ページへのリンク。まだどの製造にも入っていない数だけ、次の製造に入れられる。
//     （「製造に入っている数」は、閉じて記録へ移した製造ページの分も数える——newMfgLookup は置き場を見ない。）
//
// （同じ 2026-10-19 に、受注は「案件」・受注残表は「案件残表」・受注明細は「案件明細」と改名した。列の名前は `案件`。）
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"strconv"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

const (
	// MfgBoxTitle は製造ページの置き場の題です（トップ直下）。
	MfgBoxTitle = "製造"
	// MfgTemplate は製造ページのテンプレートの題です。
	MfgTemplate = "製造ページ"
	// MfgItemsType は製造明細表の形式です。
	MfgItemsType = "mfg-items"
	// 製造ページのタグ。
	MfgNoTag    = "製造番号"
	MfgStartTag = "始めた日"
	PageKindMfg = "製造"
	// MfgRefTag は、書類がどの製造ページの分かを書く列とタグの名前です（2026-10-21・【考察】製造単位の部材発注 §6.1）。
	// 書類は記録の年月に置くので、製造ページとは、明細の列（見積計算書はタグ）でつながる。列の宣言の Label も同じ文字。
	MfgRefTag = "製造"
	// mfgTitleMax は題の長さの目安（字数）です。超えたら「…ほか N」に縮める。
	mfgTitleMax = 30
)

func mfgItemColumns() []cms.VocabColumn {
	return []cms.VocabColumn{
		{Field: "our-item-id", Label: OurItemNoTag, Type: cms.ColRef},
		{Field: "order", Label: "案件", Type: cms.ColRef},
		{Field: "item-id", Label: ItemNoTag, Type: cms.ColCode},
		{Field: "item-name", Label: "品名", Type: cms.ColText},
		{Field: "machine", Label: "装置名", Type: cms.ColText},
		{Field: "quantity", Label: "数量", Type: cms.ColNumber},
		{Field: "unit", Label: "単位", Type: cms.ColEnum, Enum: unitChoices()},
		{Field: "due", Label: "納期", Type: cms.ColText},
		{Field: "made", Label: "製造完了", Type: cms.ColEnum, Enum: []string{MarkDone}},
		{Field: "note", Label: "備考", Type: cms.ColText},
	}
}

func init() {
	cms.RegisterVocab(cms.VocabDef{
		// **製造明細表**——製造ページの、この案件で作る品物。機械が受注明細の行から作るので、スラッシュメニューには出さない。
		Type: MfgItemsType, DisplayName: "製造明細表", Category: "業務", Icon: "🏭", Element: "table", Hidden: true,
		Columns: mfgItemColumns(),
	})
	cms.RegisterRequiredPage(cms.RequiredPage{
		Title:     MfgBoxTitle,
		Extension: "toho",
		Why: "進んでいる案件の置き場です（製造／製造ページ）。案件残表の「🏭 製造」で製造待ちへ入れ、製造待ちの「▶ 開始」で、この下に製造ページを作ります（2026-10-21）。" +
			"案件が終わると、製造ページは記録の年月へ移ります。" +
			"案件をまたぐ作業面もここに置きます（製造待ち・未発注の部材発注書・未送付の見積書・見積書を作る。臨時部材表・必要部材表・手配不要表は 2026-10-21 に外した）。",
	})
	cms.RegisterPageTemplate(cms.PageTemplate{
		Title:     MfgTemplate,
		Extension: "toho",
		Why: "案件残表の「🏭 製造開始」が、製造ページ（製造／…）を作るときに写します。" +
			"キャプション「製造明細表」の表が要ります（タグ 製造番号・客先・始めた日・ページ種類〔製造〕は、あれば埋めます）。",
	})
}

// ── どの製造に、どれだけ入っているか ───────────────────────────────────────

// mfgLookup は描画1回ぶんの「受注ページ×品番（品番が空なら品名） → 製造に入れた数・製造ページ」です。
type mfgLookup struct {
	qty   map[string]float64
	done  map[string]float64 // 製造完了（済）の行の数（2026-10-19・案件明細の製造完了の鏡）
	pages map[string][]string
}

// newMfgLookup は、製造ページ（`ページ種類：製造`・テンプレートの外）の製造明細表の行を全部数えます。
func newMfgLookup() *mfgLookup {
	m := &mfgLookup{qty: map[string]float64{}, done: map[string]float64{}, pages: map[string][]string{}}
	rows, err := cms.VocabRowsOfType(database.DB, MfgItemsType)
	if err != nil {
		return m
	}
	kinds := map[int]bool{}
	for _, r := range rows {
		ok, seen := kinds[r.PageID]
		if !seen {
			ok = !cms.IsTemplateArea(page.FormatID(r.PageID)) && pageKindOf(r.PageID) == PageKindMfg
			kinds[r.PageID] = ok
		}
		if !ok {
			continue
		}
		q, okQ := estimateNumber(r.Values["quantity"])
		if !okQ || q <= 0 {
			continue
		}
		k := shippedKey(r.Values["order"], r.Values["item-id"], r.Values["item-name"])
		m.qty[k] += q
		if strings.TrimSpace(r.Values["made"]) == MarkDone {
			m.done[k] += q
		}
		id := page.FormatID(r.PageID)
		if ps := m.pages[k]; len(ps) == 0 || ps[len(ps)-1] != id {
			m.pages[k] = append(ps, id)
		}
	}
	return m
}

// doneOf は、その案件明細の行のうち、製造完了（済）の製造に入った数です。
func (m *mfgLookup) doneOf(order, itemNo, itemName string) float64 {
	return m.done[shippedKey(order, itemNo, itemName)]
}

func (m *mfgLookup) of(order, itemNo, itemName string) (float64, []string) {
	k := shippedKey(order, itemNo, itemName)
	return m.qty[k], m.pages[k]
}

// （受注残表の「製造」の欄〔mfgCellHTML〕は 2026-10-21 に外した——先頭のチェックと「進み」の列へ・mfg_wait.go・backlog.go。）

// ── 製造ページを作る ────────────────────────────────────────────────────

// mfgLine は製造明細表の1行です。
type mfgLine struct {
	OurItemNo, Order, ItemNo, ItemName, Machine, Qty, Unit, Due string
	Client                                                      string
	Free                                                        int // まだどの製造にも入っていない数
}

// mfgLineOf は受注残表で選んだ1行を、受注明細表の索引から製造明細の行にします（読めない・合わないなら理由）。
func mfgLineOf(user *auth.User, p deliveryPick, mfg *mfgLookup, machines *machineLookup) (mfgLine, string) {
	// 見積の依頼からも始められる（2026-10-19）。
	id, r, why := pickedOrderRow(user, p, func(k string) bool { return k == PageKindCustomerOrder || k == PageKindCustomerQuote }, false)
	if why != "" {
		return mfgLine{}, why
	}
	pid := pageNum(id)
	tags, err := cms.TagsOfPage(database.DB, pid)
	if err != nil {
		return mfgLine{}, "案件ページを読めません: /" + id
	}
	v := r.Values
	q, okQ := estimateNumber(p.Qty)
	if !okQ || q <= 0 {
		return mfgLine{}, "/" + id + " の「" + strings.TrimSpace(v["item-name"]) + "」の今回作る数が読めません: " + p.Qty
	}
	inMfg, _ := mfg.of(id, v["item-id"], v["item-name"])
	free := r.Num("quantity") - int(inMfg)
	if int(q) > free {
		return mfgLine{}, "/" + id + " の「" + strings.TrimSpace(v["item-name"]) + "」は、まだ製造に入っていない数が " + strconv.Itoa(free) + " です（" + p.Qty + " は多すぎます）"
	}
	due := strings.TrimSpace(v["due"])
	if due == "" {
		due = cms.FirstTag(tags, DueDateTag)
	}
	machine, _ := machines.machineOf(pid, v["our-item-id"], v["item-id"], v["item-name"])
	return mfgLine{
		OurItemNo: strings.TrimSpace(v["our-item-id"]), Order: id, ItemNo: strings.TrimSpace(v["item-id"]),
		ItemName: strings.TrimSpace(v["item-name"]), Machine: strings.TrimSpace(machine), Qty: strings.TrimSpace(p.Qty),
		Unit: strings.TrimSpace(v["unit"]), Due: due, Client: strings.TrimSpace(cms.FirstTag(tags, OrderClientTag)), Free: free,
	}, ""
}

// mfgTitle は製造ページの題です——「装置名、装置名…」（同じ名前は1つ）。装置名の無い行は品名。長くなったら「…ほか N」。
func mfgTitle(lines []mfgLine) string {
	var names []string
	seen := map[string]bool{}
	for _, l := range lines {
		n := l.Machine
		if n == "" {
			n = l.ItemName
		}
		if n == "" {
			n = l.ItemNo
		}
		if n == "" || seen[n] {
			continue
		}
		seen[n] = true
		names = append(names, n)
	}
	if len(names) == 0 {
		return "製造"
	}
	title := names[0]
	for i, n := range names[1:] {
		next := title + "、" + n
		if len([]rune(next)) > mfgTitleMax {
			return title + "、ほか " + strconv.Itoa(len(names)-1-i)
		}
		title = next
	}
	return title
}

// buildMfgHTML は製造ページの本文を、テンプレート tmpl を埋めて組みます。
func buildMfgHTML(tmpl, pageID, client, start string, lines []mfgLine) (string, error) {
	d := cms.NewPageDraft(MfgTemplate, tmpl)
	d.SetTitle(mfgTitle(lines))
	setHeaderTag(d.DraftBlock, MfgNoTag, pageID)
	setHeaderTag(d.DraftBlock, DeliveryClientTag, client)
	setHeaderTag(d.DraftBlock, MfgStartTag, start)
	d.SetTag(PageKindTag, PageKindMfg)
	rows := make([]map[string]string, 0, len(lines))
	for _, l := range lines {
		rows = append(rows, map[string]string{
			"our-item-id": l.OurItemNo, "order": l.Order, "item-id": l.ItemNo, "item-name": l.ItemName, "machine": l.Machine,
			"quantity": l.Qty, "unit": l.Unit, "due": l.Due,
		})
	}
	if _, err := fillVocabTable(d.DraftBlock, MfgItemsType, rows); err != nil {
		return "", err
	}
	return d.HTML(), nil
}

// MfgNewAPIHandler は POST /api/mfg/new です。入力: {items:[{order, row, item, qty}]}。製造ページを作り、そのIDを返します。
func MfgNewAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		Items []deliveryPick `json:"items"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	if len(req.Items) == 0 {
		cms.JSONFail(w, http.StatusBadRequest, "製造を始める行を1つ以上選んでください")
		return
	}
	mfg, machines := newMfgLookup(), newMachineLookup()
	var lines []mfgLine
	client := ""
	for _, p := range req.Items {
		ln, why := mfgLineOf(user, p, mfg, machines)
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
	}
	newID, code, err := createMfgPage(user, client, lines)
	if err != nil {
		cms.JSONFail(w, code, err.Error())
		return
	}
	auth.Audit(user.Username, "mfg.new", newID+" "+client+" "+strconv.Itoa(len(lines))+"行")
	cms.WriteJSON(w, map[string]any{"success": true, "page_id": newID, "url": "/" + newID})
}

// refersTo は、参照の値 v（`002050`・`/002050` など）がページ id を指すかです。
func refersTo(v string, id int) bool {
	pid, ok := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(v), "/"))
	return ok && pageNum(pid) == id
}

// childOf は、ページ docID の親がページ parent かです（2026-10-20 までの、製造ページの子に作った書類を見分ける）。
func childOf(docID, parent int) bool {
	meta, ok := page.ReadSidecar(page.FormatID(docID))
	return ok && meta.ParentID == page.FormatID(parent)
}

// mfgDeliveryRows は、製造ページ mfg の分の納品明細の行（`ページ種類：弊社納品書` のページだけ）を返します。
//
// 「この製造の分」は、行の「製造」の列がこの製造ページを指す行です（2026-10-21 から。納品書は記録の年月に置く）。
// 列が空の行は、納品書が製造ページの子なら、この製造の分と読みます（2026-10-20 までに作った納品書）。
func mfgDeliveryRows(mfg int) []cms.VocabRow {
	rows, err := cms.VocabRowsOfType(database.DB, DeliveryItemsType)
	if err != nil {
		return nil
	}
	kind := map[int]bool{}
	child := map[int]bool{}
	var out []cms.VocabRow
	for _, r := range rows {
		ok, seen := kind[r.PageID]
		if !seen {
			ok = pageKindOf(r.PageID) == PageKindOurDelivery
			kind[r.PageID] = ok
		}
		if !ok {
			continue
		}
		if strings.TrimSpace(r.Values["mfg"]) != "" {
			if refersTo(r.Values["mfg"], mfg) {
				out = append(out, r)
			}
			continue
		}
		c, seenC := child[r.PageID]
		if !seenC {
			c = childOf(r.PageID, mfg)
			child[r.PageID] = c
		}
		if c {
			out = append(out, r)
		}
	}
	return out
}

// mfgsOfDoc は、書類 docID（明細の形式 dataType）が指す製造ページの番号を返します（明細の「製造」の列・重複なし）。
// 列が1つも書かれていなければ、親が製造ページならその番号（2026-10-20 までの形）。
func mfgsOfDoc(docID int, dataType string) []int {
	rows, _ := cms.VocabTableRowsOf(database.DB, docID, dataType)
	seen := map[int]bool{}
	var out []int
	for _, r := range rows {
		pid, ok := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(r.Values["mfg"]), "/"))
		if !ok || seen[pageNum(pid)] {
			continue
		}
		seen[pageNum(pid)] = true
		out = append(out, pageNum(pid))
	}
	if len(out) > 0 {
		return out
	}
	if meta, ok := page.ReadSidecar(page.FormatID(docID)); ok && meta.ParentID != "" {
		if id := pageNum(meta.ParentID); pageKindOf(id) == PageKindMfg {
			return []int{id}
		}
	}
	return nil
}
