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
	stdhtml "html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
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
		Why: "案件残表の「🚚 選んだ行で納品書を作る」が、納品書ページ（記録／年／月／製造記録）を作るときに写します。" +
			"キャプション「納品明細表」の表が要ります（タグ 納品書番号・客先・納品日・発行日・納品担当・納品場所・ページ種類〔弊社納品書〕は、あれば埋めます）。",
	})
}

// deliveryPick は案件残表で選んだ1行です（どの案件のどの行か・今回納める数）。
type deliveryPick struct {
	Order string `json:"order"` // 案件ページ
	Row   int    `json:"row"`   // 案件明細表のデータ行の番号（索引の RowNo）
	Item  string `json:"item"`  // 品番（行の取り違えを防ぐ照合）
	Qty   string `json:"qty"`   // 今回納める数
	// Name は品名です。Row が負のとき（製造ページから——案件明細の行番号を持たない）、品番か、品番が空なら品名で行を探す（2026-10-19）。
	Name string `json:"name"`
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
	id, ok := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(p.Order), "/"))
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

// oneClient は、行の客先が1つにそろっているかを見ます（client は今までの客先・空なら初めの行）。
// そろっていなければ、混ざった2つの名前を返します（断りの文は呼び手が組む）。
func oneClient(client, next string) (string, bool) {
	if client == "" {
		return next, true
	}
	return client, cms.NormalizeText(next) == cms.NormalizeText(client)
}

// deliveryLineOf は選んだ1行を、受注明細表の索引から納品明細の行にします（読めない・合わないなら理由）。
func deliveryLineOf(user *auth.User, p deliveryPick) (deliveryLine, string) {
	id, r, why := pickedOrderRow(user, p, func(k string) bool { return k == PageKindCustomerOrder }, true)
	if why != "" {
		return deliveryLine{}, why
	}
	v := r.Values
	tags, _ := cms.TagsOfPage(database.DB, pageNum(id))
	qty := strings.TrimSpace(p.Qty)
	if n, ok := estimateNumber(qty); !ok || n <= 0 {
		return deliveryLine{}, "/" + id + " の「" + strings.TrimSpace(v["item-name"]) + "」の今回納める数が読めません: " + qty
	}
	return deliveryLine{
		OurItemNo: strings.TrimSpace(v["our-item-id"]), Order: id, OrderNo: strings.TrimSpace(cms.FirstTag(tags, OrderNoTag)),
		ItemNo: strings.TrimSpace(v["item-id"]), ItemName: strings.TrimSpace(v["item-name"]), Qty: qty,
		Unit: strings.TrimSpace(v["unit"]), Price: strings.TrimSpace(v["price"]),
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

// DeliveryNewAPIHandler は POST /api/delivery/new です。入力: {items:[{order, row, item, qty}]}。納品書ページを作り、そのIDを返します。
// （2026-10-19 から、製造ページからは {items:[{order, row:-1, item, name, qty}], mfg}——行番号の代わりに品番か品名で行を探し、明細の「製造」の列にその製造ページを書く（2026-10-21 から置き場は記録・明細の「製造」の列でつながる）。）
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
	mfgID := ""
	if strings.TrimSpace(req.Mfg) != "" {
		id, ok := page.NormalizeID(strings.TrimPrefix(strings.TrimSpace(req.Mfg), "/"))
		if !ok || pageKindOf(pageNum(id)) != PageKindMfg || !canWritePage(user, pageNum(id)) {
			cms.JSONFail(w, http.StatusBadRequest, "製造ページが見つからないか、書けません: "+req.Mfg)
			return
		}
		mfgID = id
	}
	if len(req.Items) == 0 {
		cms.JSONFail(w, http.StatusBadRequest, "納める行を1つ以上選んでください")
		return
	}
	var lines []deliveryLine
	client := ""
	for _, p := range req.Items {
		ln, why := deliveryLineOf(user, p)
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
	if req.FromDraft && mfgID != "" {
		if err := cms.RewriteBody(mfgID, user.Username, func(cur string) string { return dropTablesOfType(cur, MfgDeliveryDraftType) }); err != nil {
			out["draft_note"] = "⚠ 製造ページの納品表を消せませんでした: " + err.Error()
		}
	}
	cms.WriteJSON(w, out)
}

// deliveryPickHTML は受注残表の1行の「納品」の選ぶ欄です（残りが無い行は選べない）。
func deliveryPickHTML(r backlogRow, client string) string {
	if r.Quote {
		return `<input type="checkbox" disabled title="見積の依頼です（注文が来たら納品できます）">`
	}
	if r.Own {
		return `<input type="checkbox" disabled title="自社案件です（納品書は作りません）">`
	}
	if r.Remaining <= 0 {
		return `<input type="checkbox" disabled title="案件残がありません">`
	}
	return `<input type="checkbox" class="backlog-dlv-pick" title="この行を納品書に載せる" data-order="` + stdhtml.EscapeString(r.OrderPageID) +
		`" data-row="` + strconv.Itoa(r.RowNo) + `" data-item="` + stdhtml.EscapeString(r.ItemNo) + `" data-name="` +
		stdhtml.EscapeString(r.ItemName) + `" data-remaining="` + strconv.Itoa(r.Remaining) + `" data-client="` + stdhtml.EscapeString(client) + `">`
}
