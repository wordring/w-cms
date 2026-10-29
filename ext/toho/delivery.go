package toho

// ─────────────────────────────────────────────────────────────────────────
// 納品書——客先へ出す納品書を、受注明細の行から作る（2026-10-17）
//
// 利用者（2026-10-16）:「分納はよくあります。」「納品書をまとめることはあります。まとめない場合もあります。」
// 「A. 納品書を正本にします。」「納品書を作る前に、品物だけ先に出ることはありました。本来は良くない事でしたが、今後もあり得ます。」
// 「「受注が中心になっている」と気付いたのは、昨日納品書を書いてもらった時です。受注から品番をコピペしてて気づきました。」
// 筆者の案（2026-10-17・利用者は「一点だけ確認です」と仮納品書を足して了承——【考察】全体の設計 §3.6・直す順の8番）:
//
//   - 置き場は `記録／年／月／製造記録`（月は作った日）。題は「【弊社】納品書　<客先>」。納品書番号はページ番号そのもの。
//     （2026-10-19 から、製造ページから作れる——DeliveryNewAPIHandler の `mfg`。2026-10-21 から置き場はいつも記録で、明細の「製造」の列にその製造ページを書く。）
//   - **受注残表で行を選んで作る**。1枚に1社（客先の違う行は混ぜない）、別々の受注の行は混ぜてよい。今回納める数は残りが初め
//     から入っていて、直せる。品番・品名・単位・単価・発注書番号は**受注明細から写す**（人が品番を写さない）。
//     （2026-10-19 から、受注残表は案件残表・受注明細は案件明細。製造ページの製造明細表の「納品」の欄からも作れる——mfg_view.go。）
//   - 単価は載せる。決まっていない行は空欄（0 にしない——空欄は「まだ決まっていない」）。
//   - 作ったときの状態は「未発行」（`発行日` が空）。品物が先に出たときも、紙を作らずにページだけ作れる。紙（PDF）はあとから。
//   - 受注明細の行とは、`受注`（受注ページへの参照）と `品番`・`弊社品番` で結ぶ（出荷済数量の鏡が数える）。
//     （いまの列の名前は `案件`。出荷済数量の鏡が結ぶのは `案件` と `品番`（品番が空なら `品名`）だけで、`弊社品番` は見ない——
//     delivery_shipped.go の shippedKey。`弊社品番` は写すだけ。）
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

const (
	// DeliveryTemplate は納品書ページのテンプレートの題です。
	DeliveryTemplate = "納品書"
	// DeliveryItemsType は納品明細表の形式です。
	DeliveryItemsType = "delivery-items"
	// 納品書のタグ。
	DeliveryNoTag       = "納品書番号"
	DeliveryClientTag   = "客先"
	DeliveryIssuedTag   = "発行日" // 紙（PDF）を作った日。空なら「未発行」
	DeliverySignerTag   = "納品担当"
	DeliveryPlaceTag    = "納品場所"
	PageKindOurDelivery = "弊社納品書"
)

// deliveryItemColumns は納品明細表の列です（受注明細表と同じ名前・同じ型——横断検索が片方を取りこぼさないように）。
func deliveryItemColumns() []cms.VocabColumn {
	return []cms.VocabColumn{
		{Field: "our-item-id", Label: OurItemNoTag, Type: cms.ColRef},
		{Field: "order", Label: "案件", Type: cms.ColRef},
		// mfg はどの製造ページの分か（2026-10-21・【考察】製造単位の部材発注 §6.1）——書類は記録の年月に置くので、製造ページとは、この列でつながる。紙には刷らない。
		{Field: "mfg", Label: "製造", Type: cms.ColRef},
		{Field: "order-no", Label: OrderNoTag, Type: cms.ColCode},
		{Field: "item-id", Label: ItemNoTag, Type: cms.ColCode},
		{Field: "item-name", Label: "品名", Type: cms.ColText},
		{Field: "quantity", Label: "数量", Type: cms.ColNumber},
		{Field: "unit", Label: "単位", Type: cms.ColEnum, Enum: unitChoices()},
		{Field: "price", Label: "単価", Type: cms.ColNumber},
		{Field: "note", Label: "備考", Type: cms.ColText},
	}
}

func init() {
	cms.RegisterVocab(cms.VocabDef{
		// **納品明細表**——納品書ページの明細。機械が受注明細の行から作るので、スラッシュメニューには出さない。
		Type:        DeliveryItemsType,
		DisplayName: "納品明細表",
		Category:    "業務",
		Icon:        "🚚",
		Element:     "table",
		Hidden:      true,
		Columns:     deliveryItemColumns(),
	})
	cms.RegisterPageTemplate(cms.PageTemplate{
		Title:     DeliveryTemplate,
		Extension: "toho",
		Why: "製造ページの納品作業の「納品書を作る」が、納品書ページ（記録／年／月／製造記録）を作るときに写します。" +
			"キャプション「納品明細表」の表が要ります（タグ 納品書番号・客先・納品日・発行日・納品担当・納品場所・ページ種類〔弊社納品書〕は、あれば埋めます）。",
	})
}

// deliveryPick は選んだ案件明細の1行です（どの案件のどの行か・今回納める数）。製造待ち・製造ページを作る口と、納品書を作る口が使う。
type deliveryPick struct {
	Order string `json:"order"` // 案件ページ
	Row   int    `json:"row"`   // 案件明細表のデータ行の番号（索引の RowNo）
	Item  string `json:"item"`  // 品番（行の取り違えを防ぐ照合）
	Qty   string `json:"qty"`   // 今回納める数
	// Name は品名です。Row が負のとき（製造ページから——案件明細の行番号を持たない）、品番か、品番が空なら品名で行を探す（2026-10-19）。
	Name string `json:"name"`
	// Price・Note は納品表の「単価」「備考」です（2026-10-28・無ければ nil——案件の単価のまま）。タップ無し・塗装無しで先に納めるときなど、
	// 今回の分だけ単価を変える（利用者:「一部を納品して価格が変わる（タップ無しや塗装無しで早く出す）ことがあるので、定型作業として可能にしたいのです。」）。空の単価は「まだ決まっていない」。
	Price *string `json:"price,omitempty"`
	Note  *string `json:"note,omitempty"`
}

// deliveryLine は納品明細表の1行です。
type deliveryLine struct {
	OurItemNo, Order, OrderNo, ItemNo, ItemName, Qty, Unit, Price, Note string
	Client                                                              string
	Mfg                                                                 string // 製造ページ（2026-10-21）
}

// pickedOrderRow は、案件残表（か製造ページ）で選んだ1行 p を、案件ページの案件明細表の索引から探します（読めない・合わないなら理由）。
// ok は、その行を持ってよい案件ページの種類か。byName なら、p.Row が負のとき品番（空なら品名）で行を探します（製造ページから作る納品書）。
// ⚠ 見つけた行の品番が p.Item と違えば断ります（行の取り違えを防ぐ照合）。
// 2026-10-19 に寄せた（納品書と製造ページを作る口が、同じ探し方と同じ断りの文を写していた）。
func pickedOrderRow(user *auth.User, p deliveryPick, kindOK func(string) bool, byName bool) (string, cms.VocabRow, string) {
	id, ok := refPageID(p.Order)
	if !ok {
		return "", cms.VocabRow{}, "案件ページの番号が読めません: " + p.Order
	}
	pid := pageNum(id)
	if !page.CanView(user, pid) || !kindOK(pageKindOf(pid)) {
		return "", cms.VocabRow{}, "案件ページが見つかりません: /" + id
	}
	rows, err := cms.VocabTableRowsOf(database.DB, pid, clientOrderItemsType)
	if err != nil {
		return "", cms.VocabRow{}, "案件明細表を読めません: /" + id
	}
	if byName && p.Row < 0 {
		for _, r := range rows {
			if shippedKey(id, r.Values["item-id"], r.Values["item-name"]) == shippedKey(id, p.Item, p.Name) {
				p.Row = r.RowNo
				break
			}
		}
	}
	for _, r := range rows {
		if r.RowNo != p.Row {
			continue
		}
		if strings.TrimSpace(r.Values["item-id"]) != strings.TrimSpace(p.Item) {
			return "", cms.VocabRow{}, "/" + id + " の " + strconv.Itoa(p.Row+1) + " 行目の品番が変わっています（案件フォルダを開き直してください）"
		}
		return id, r, ""
	}
	return "", cms.VocabRow{}, "/" + id + " の " + strconv.Itoa(p.Row+1) + " 行目が見つかりません（案件フォルダを開き直してください）"
}

// orderedOfItem は案件ページ pid の案件明細表で、案件と品物の鍵が key の行の数量の合計です（読めない行が1つでもあれば false）。
func orderedOfItem(pid int, key string) (float64, bool) {
	rows, err := cms.VocabTableRowsOf(database.DB, pid, clientOrderItemsType)
	if err != nil {
		return 0, false
	}
	id := page.FormatID(pid)
	sum, found := 0.0, false
	for _, r := range rows {
		if shippedKey(id, r.Values["item-id"], r.Values["item-name"]) != key {
			continue
		}
		q, ok := estimateNumber(r.Values["quantity"])
		if !ok {
			return 0, false
		}
		sum, found = sum+q, true
	}
	return sum, found
}

// oneClient は、行の客先が1つにそろっているかを見ます（client は今までの客先・空なら初めの行）。
// そろっていなければ、混ざった2つの名前を返します（断りの文は呼び手が組む）。
func oneClient(client, next string) (string, bool) {
	if client == "" {
		return next, true
	}
	return client, cms.NormalizeText(next) == cms.NormalizeText(client)
}

// deliveryLineOf は選んだ1行を、受注明細表の索引から納品明細の行にします（読めない・合わないなら理由）。
//
// ⚠ 今回納める数は、案件明細の数量から、ほかの納品書で納めた数（shipped）と、この納品書のほかの行で納める数（pending）を
// 引いた残りまで（2026-10-26）。それまでは見ておらず、押し直しや数え違いで、注文より多い数の納品書ができ、出荷済数量と
// 請求が多すぎた。案件明細の数量が読めない行は見ない。pending には、この行の数を足して返す。
// 納めた数は案件と品物で数える（出荷済数量の鏡と同じ）ので、同じ案件に同じ品物の行が2つ以上あれば、注文の数もその行の合計で比べる。
func deliveryLineOf(user *auth.User, p deliveryPick, shipped shippedLookup, pending map[string]float64) (deliveryLine, string) {
	id, r, why := pickedOrderRow(user, p, func(k string) bool { return k == PageKindCustomerOrder }, true)
	if why != "" {
		return deliveryLine{}, why
	}
	v := r.Values
	tags, _ := cms.TagsOfPage(database.DB, pageNum(id))
	qty := strings.TrimSpace(p.Qty)
	n, ok := estimateNumber(qty)
	if !ok || n <= 0 {
		return deliveryLine{}, "/" + id + " の「" + strings.TrimSpace(v["item-name"]) + "」の今回納める数が読めません: " + qty
	}
	key := shippedKey(id, v["item-id"], v["item-name"])
	if ordered, okO := orderedOfItem(pageNum(id), key); okO && ordered > 0 {
		done := shipped[key] + pending[key]
		if n > ordered-done+1e-9 {
			return deliveryLine{}, "/" + id + " の「" + strings.TrimSpace(v["item-name"]) + "」は、注文 " + fmtQty(ordered) + " のうち " +
				fmtQty(done) + " を納めた（ほかの納品書・この納品書のほかの行）ので、今回は " + fmtQty(ordered-done) + " までです（今回 " + qty + "）"
		}
	}
	price := strings.TrimSpace(v["price"])
	if p.Price != nil { // 納品表で今回の単価を決めた（2026-10-28）
		price = strings.TrimSpace(*p.Price)
		if pv, okP := estimateNumber(price); price != "" && (!okP || pv < 0) {
			return deliveryLine{}, "/" + id + " の「" + strings.TrimSpace(v["item-name"]) + "」の単価が読めません: " + price
		}
	}
	note := ""
	if p.Note != nil {
		note = strings.TrimSpace(*p.Note)
	}
	pending[key] += n
	return deliveryLine{
		OurItemNo: strings.TrimSpace(v["our-item-id"]), Order: id, OrderNo: strings.TrimSpace(cms.FirstTag(tags, OrderNoTag)),
		ItemNo: strings.TrimSpace(v["item-id"]), ItemName: strings.TrimSpace(v["item-name"]), Qty: qty,
		Unit: strings.TrimSpace(v["unit"]), Price: price, Note: note,
		Client: strings.TrimSpace(cms.FirstTag(tags, OrderClientTag)),
	}, ""
}

// buildDeliveryHTML は納品書ページの本文を、テンプレート tmpl を埋めて組みます。
func buildDeliveryHTML(tmpl, pageID, client, signer string, lines []deliveryLine) (string, error) {
	d := cms.NewPageDraft(DeliveryTemplate, tmpl)
	d.SetTitle(issuerTitle(PageKindOurDelivery, "納品書　"+client))
	setHeaderTag(d.DraftBlock, DeliveryNoTag, pageID)
	setHeaderTag(d.DraftBlock, DeliveryClientTag, client)
	d.SetTag(PageKindTag, PageKindOurDelivery)
	// 納品日・発行日・納品場所は人が書く欄（空で置く）。発行日が空＝未発行。
	for _, t := range []string{DeliveredAtTag, DeliveryIssuedTag, DeliveryPlaceTag} {
		d.DraftBlock.EnsureTag(t)
	}
	// 納品担当は、作った人の連絡帳のページ（署名のある人・ログイン名と同じ題）を初めに入れる（2026-10-17——空だと紙に差出人が出ない）。
	if signer != "" {
		setHeaderTag(d.DraftBlock, DeliverySignerTag, signer)
	} else {
		d.DraftBlock.EnsureTag(DeliverySignerTag)
	}
	rows := make([]map[string]string, 0, len(lines))
	for _, ln := range lines {
		rows = append(rows, map[string]string{
			"our-item-id": ln.OurItemNo, "order": ln.Order, "mfg": ln.Mfg, "order-no": ln.OrderNo, "item-id": ln.ItemNo,
			"item-name": ln.ItemName, "quantity": ln.Qty, "unit": ln.Unit, "price": ln.Price, "note": ln.Note,
		})
	}
	if _, err := fillVocabTable(d.DraftBlock, DeliveryItemsType, rows); err != nil {
		return "", err
	}
	return d.HTML(), nil
}

// DeliveryNewAPIHandler は POST /api/delivery/new です。入力: {items:[{order, row, item, name, qty}], mfg}。納品書ページを作り、そのIDを返します。
// 製造ページからは row:-1——行番号の代わりに品番か品名で行を探し、明細の「製造」の列にその製造ページを書く（2026-10-19。2026-10-21 から置き場は記録・明細の「製造」の列でつながる）。
// ⚠ **2026-10-22 から mfg は必須**です。それまでは案件残表の「🚚 選んだ行で納品書を作る」が mfg 無しで呼んでいたが、そのボタンを外した
// （利用者:「案件フォルダに「選んだ行で納品書を作る」というボタンがありますが無用です。」「まつわる余分なコードも削除しましょう」）。
// 納品書はいつも製造ページから作る——明細の「製造」の列で製造とつながり、全部納めた製造を閉じる（mfg_close.go）のも、それで数える。
func DeliveryNewAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		Items []deliveryPick `json:"items"`
		// Mfg は製造ページ（2026-10-19・mfg_view.go）。あれば、明細の「製造」の列に書く（2026-10-21 から、納品書はいつも 記録／年／月／製造記録 に作る）。
		Mfg string `json:"mfg"`
		// FromDraft は、製造ページの納品表から作ったか（2026-10-21 夕・mfg_work.go）。作れたら、その納品表を消す。
		FromDraft bool `json:"from_draft"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	if strings.TrimSpace(req.Mfg) == "" {
		cms.JSONFail(w, http.StatusBadRequest, "納品書は製造ページの納品作業から作ります（製造ページがありません）")
		return
	}
	mfgID, ok := refPageID(req.Mfg)
	if !ok || pageKindOf(pageNum(mfgID)) != PageKindMfg || !canWritePage(user, pageNum(mfgID)) || cms.IsTemplateArea(mfgID) {
		cms.JSONFail(w, http.StatusBadRequest, "製造ページが見つからないか、書けません: "+req.Mfg)
		return
	}
	if len(req.Items) == 0 {
		cms.JSONFail(w, http.StatusBadRequest, "納める行を1つ以上選んでください")
		return
	}
	// 1つずつ通し、納品表から作るときは、その納品表がまだ在るかを確かめる（2026-10-26）——作ったあと納品表は消すので、
	// 応答が届かずに押し直すと、2枚目の納品書ができて出荷済数量と請求が二重になっていた。
	deliveryNewMu.Lock()
	defer deliveryNewMu.Unlock()
	// 2026-10-26 夕から、在るだけでなく、画面が送った行（案件・品番・品名・数量）と同じ中身の納品表かも確かめる——送った行が納品表の
	// 指紋。ほかの人が作り直した納品表から、描き直す前の古い行で納品書を作らない（画面は送る前に書きかけを保存するので、直した数量は
	// 本文に入っている）。
	if req.FromDraft {
		body, err := cms.ReadPageBody(mfgID)
		nodes, perr := htmldoc.ParseFragment(body)
		if err != nil || perr != nil || len(tablesOfType(nodes, MfgDeliveryDraftType)) == 0 {
			cms.JSONFail(w, http.StatusConflict, "この製造ページの納品表は、もう納品書にしました（または消されています）。ページを読み直してください。")
			return
		}
		if !anyDraftMatches(nodes, req.Items) {
			cms.JSONFail(w, http.StatusConflict, "この製造ページの納品表が、画面の表と違います（ほかの人が作り直したか、直しました）。ページを読み直してください。")
			return
		}
	}
	var lines []deliveryLine
	client := ""
	// 納めた数は、1つずつ通している中で数える（deliveryNewMu——直前に作った納品書も数に入る）。
	shipped, pending := newShippedLookup(database.DB), map[string]float64{}
	for _, p := range req.Items {
		ln, why := deliveryLineOf(user, p, shipped, pending)
		if why != "" {
			cms.JSONFail(w, http.StatusConflict, why)
			return
		}
		var same bool
		if client, same = oneClient(client, ln.Client); !same {
			cms.JSONFail(w, http.StatusBadRequest, "納品書は1枚に1社です（「"+client+"」と「"+ln.Client+"」が混ざっています）")
			return
		}
		ln.Mfg = mfgID
		lines = append(lines, ln)
	}
	tmpl, err := cms.PageTemplateBody(DeliveryTemplate)
	if err != nil {
		cms.JSONFail(w, http.StatusConflict, "納品書ページを作れません: "+err.Error())
		return
	}
	signer := ""
	if s, ok := DefaultSigner(user, Signers(user)); ok {
		signer = page.FormatID(s.PageID)
	}
	if _, err := buildDeliveryHTML(tmpl, "", client, signer, lines); err != nil {
		cms.JSONFail(w, http.StatusConflict, "納品書ページを作れません: "+err.Error())
		return
	}
	// 置き場は 記録／年／月／製造記録（2026-10-21 から、製造ページから作っても——製造ページとは明細の「製造」の列でつながる）。
	parentID, err := recordFolderOr("", user, RecordWorkFolder, time.Now())
	if err != nil {
		cms.JSONFail(w, createFailStatus(err, http.StatusInternalServerError), err.Error())
		return
	}
	newID, err := newBuiltPage(parentID, user, "納品書", func(newID string) (string, error) {
		return buildDeliveryHTML(tmpl, newID, client, signer, lines)
	})
	if err != nil {
		cms.JSONFail(w, createFailStatus(err, http.StatusInternalServerError), err.Error())
		return
	}
	auth.Audit(user.Username, "delivery.new", newID+" "+client+" "+strconv.Itoa(len(lines))+"行")
	out := map[string]any{"success": true, "page_id": newID, "url": "/" + newID}
	// 製造ページの納品表から作ったなら、その納品表を消す（2026-10-21 夕）。⚠ 消せなくても納品書は取り消さない——理由を添えるだけ。
	// ⚠ 消すのは、送った行と同じ中身の納品表だけ（2026-10-26 夕）。それまでは納品表を全部消していたので、作っているあいだにほかの人が
	// 「納品」で作り直した納品表（まだ納めていない別の行）まで消えた。
	if req.FromDraft {
		dropped := 0
		if err := cms.RewriteBody(mfgID, user.Username, func(cur string) string {
			out, n := dropTablesWhere(cur, MfgDeliveryDraftType, func(t *html.Node) bool { return draftMatchesPicks(t, req.Items) })
			dropped = n
			return out
		}); err != nil {
			out["draft_note"] = "⚠ 製造ページの納品表を消せませんでした: " + err.Error()
		} else if dropped == 0 {
			out["draft_note"] = "⚠ 製造ページの納品表は、作っているあいだにほかの人が作り直したので、消さずに残しました（この納品書で納めた分を確かめてください）"
		}
	}
	cms.WriteJSON(w, out)
}

// anyDraftMatches は本文の納品表のどれかが、送った行 items と同じ中身かです。
func anyDraftMatches(nodes []*html.Node, items []deliveryPick) bool {
	for _, t := range tablesOfType(nodes, MfgDeliveryDraftType) {
		if draftMatchesPicks(t, items) {
			return true
		}
	}
	return false
}

// draftMatchesPicks は、納品表 t の数量のある行（表の順）が、画面が送った行 items と同じか（案件・品番・品名・数量）です。
// 画面（toho.js の mfg-dlvdraft-make）も、数量のある行だけを表の順に送る。
func draftMatchesPicks(t *html.Node, items []deliveryPick) bool {
	rows := rowsOf(t)
	if len(rows) == 0 {
		return false
	}
	head := headerIndex(rows[0])
	n := 0
	for _, tr := range rows[1:] {
		if tr.Parent != nil && tr.Parent.Data == "tfoot" {
			continue
		}
		cells := cellsOf(tr)
		at := func(label string) string {
			if i, ok := head[label]; ok && i < len(cells) {
				return strings.TrimSpace(textOf(cells[i]))
			}
			return ""
		}
		if at("数量") == "" {
			continue
		}
		if n >= len(items) {
			return false
		}
		p := items[n]
		if trimSlash(at("案件")) != trimSlash(p.Order) || at(ItemNoTag) != strings.TrimSpace(p.Item) ||
			at("品名") != strings.TrimSpace(p.Name) || at("数量") != strings.TrimSpace(p.Qty) {
			return false
		}
		// 単価・備考も、送ってきたなら同じであること（2026-10-28——画面の古い単価で納品書を作らない）。
		if p.Price != nil && at("単価") != strings.TrimSpace(*p.Price) {
			return false
		}
		if p.Note != nil && at("備考") != strings.TrimSpace(*p.Note) {
			return false
		}
		n++
	}
	return n == len(items)
}

// deliveryNewMu は、納品書を作る仕事を1つずつ通します（納品表を確かめてから消すまでに、同じ納品表から2枚目を作らせない）。
var deliveryNewMu sync.Mutex
