package toho

// 業者の納品書から、納品済の部材発注書を作る（2026-10-23）。
//
// 🤖 解析が、業者（仕入先）から当社宛ての納品書と判定したとき（doc_type が "supplier_delivery"）、読んだ明細で
// 部材発注書ページ（記録／年／月／製造記録）を作ります。行の状態は最初から「納品済」、単価は納品書のとおりです。
// メールなど w-cms の外で頼んだ発注の、あとからの記録になります（出した紙の単価が、最新単価の出所になる）。
//
//   - 納品書の品名・寸法は、そのまま行の「品名」「寸法」に入れます。どの加工製品のどの部材かは、機械には決められないので、
//     人が部材発注書の「🔗 加工製品の行に結ぶ」で選びます（order_bind.go）。結ぶと、行は加工製品の部材の行と同じ値になり、
//     材料表・外注加工表の「最新単価」に出ます。
//   - 納品書のスキャンと読んだままの表は、見出し「業者からの納品」の下に置きます。
//   - 同じ納品書（受信元）から2枚目は作りません（同じ値段を二重に数えないため）。
//   - 在庫表へは入れません（行は最初から納品済なので、📦 納品済を押したときの入庫は通らない）。
//
// > 2026-10-23 利用者:「ここから部材の価格データとして記録できないでしょうか？」
// > 2026-10-23 利用者（案を聞いて）:「案1で: 🤖 解析に「業者の納品書」を足し、読んだ明細で納品済の部材発注書を作ってください。
// > 加工製品の行への結びは人が選び、外注加工表にも最新単価を出してください。」

import (
	"encoding/json"
	"errors"
	stdhtml "html"
	"net/http"
	"strings"
	"time"

	"w-cms/ext/comm"
	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// SupplierDeliveryHeading は部材発注書ページの、業者の納品書（紙の写し・PDF）を置く見出しです（テンプレート「部材発注書」が持つ）。
const SupplierDeliveryHeading = "業者からの納品"

// supplierDeliveryJudgment は 🤖 解析が業者の納品書から読んだものです。
type supplierDeliveryJudgment struct {
	Supplier   string `json:"supplier"`
	DeliveryNo string `json:"delivery_no"`
	Date       string `json:"date"`
	// Note は摘要（受注日・注文番号など、納品書の下や上に書かれた添え書き）です。
	Note  string                 `json:"note"`
	Total string                 `json:"total"`
	Items []supplierDeliveryItem `json:"items"`
	// SourceTableRaw は納品書の明細表を、見出しのまま写したものです（読み違いを人が見比べるため。読めなくても先へ進む）。
	SourceTableRaw json.RawMessage  `json:"source_table"`
	SourceTable    orderSourceTable `json:"-"`
}

type supplierDeliveryItem struct {
	ItemName string `json:"item_name"`
	Size     string `json:"size"`
	Quantity string `json:"quantity"`
	Unit     string `json:"unit"`
	Price    string `json:"price"`
	Note     string `json:"note"`
}

// supplierDeliveryRows は部材発注明細表の行の値です（列の機械キー → 値）。種類・弊社品番は空のまま（人が結ぶ）。
func supplierDeliveryRows(items []supplierDeliveryItem) []map[string]string {
	var out []map[string]string
	for _, it := range items {
		name, size := cms.NormalizeNameForIngest(it.ItemName), cms.NormalizeNameForIngest(it.Size)
		if strings.TrimSpace(name+size) == "" {
			continue // 空の行（納品書の罫線だけの行）
		}
		out = append(out, map[string]string{
			"item-name": name,
			"size":      size,
			"quantity":  strings.ReplaceAll(strings.TrimSpace(it.Quantity), ",", ""),
			"unit":      strings.TrimSpace(it.Unit),
			"cost":      strings.ReplaceAll(strings.TrimSpace(it.Price), ",", ""),
			"note":      strings.TrimSpace(it.Note),
			"status":    OrderLineDelivered,
		})
	}
	return out
}

// buildSupplierDeliveryHTML は、業者の納品書から作る部材発注書ページの本文を、テンプレート tmpl を埋めて組みます。
func buildSupplierDeliveryHTML(tmpl, pageID, supplier, ref string, j *supplierDeliveryJudgment) (string, error) {
	rows := supplierDeliveryRows(j.Items)
	if len(rows) == 0 {
		return "", errors.New("納品書の明細が読めませんでした")
	}
	d := cms.NewPageDraft(PurchaseOrderTemplate, tmpl)
	d.SetTitle(issuerTitle(PageKindOurPurchase, "部材発注　"+supplier+"（業者の納品書から）"))
	setHeaderTag(d.DraftBlock, PurchaseNoTag, pageID)
	setHeaderTag(d.DraftBlock, SupplierTag, supplier)
	setHeaderTag(d.DraftBlock, DeliveredAtTag, j.Date)
	setHeaderTag(d.DraftBlock, SourceRefTag, ref)
	if _, err := fillVocabTable(d.DraftBlock, ourOrderItemsType, rows); err != nil {
		return "", err
	}
	note := "業者の納品書から作りました（w-cms の外で頼んだ発注の記録）。"
	if no := strings.TrimSpace(j.DeliveryNo); no != "" {
		note += "\n納品書番号: " + no
	}
	if s := strings.TrimSpace(j.Note); s != "" {
		note += "\n摘要: " + s
	}
	if err := fillOrderNote(d, note); err != nil {
		return "", err
	}
	// 納品書のスキャンと読んだままの表は、見出し「業者からの納品」の下（無ければ末尾に見出しを足す）。
	frag := `<section data-type="file-view" data-ref="` + stdhtml.EscapeString(ref) + `"></section>`
	if tbl := sourceTableHTML(j.SourceTable); tbl != "" {
		frag += `<p>業者の納品書（読んだまま）:</p>` + tbl
	}
	return placeUnderHeading(d.HTML(), SupplierDeliveryHeading, frag), nil
}

// supplierDeliveryMadeFrom は、受信元 ref から作った部材発注書ページを返します（無ければ空）。
func supplierDeliveryMadeFrom(ref string) string {
	ids, _ := cms.PagesByTag(database.DB, SourceRefTag, ref)
	for _, id := range ids {
		if pageKindOf(id) == PageKindOurPurchase {
			return page.FormatID(id)
		}
	}
	return ""
}

// makeSupplierDelivery は、記録 hostPageID の添付 attachID（業者の納品書）から、納品済の部材発注書ページを作ります。
func makeSupplierDelivery(user *auth.User, hostPageID, attachID string, j *supplierDeliveryJudgment) (string, error) {
	ref := hostPageID
	if attachID != "" {
		ref = hostPageID + "-" + attachID
	}
	if id := supplierDeliveryMadeFrom(ref); id != "" {
		return "", &comm.MakeError{Status: http.StatusConflict, Message: "この納品書からは、もう部材発注書 /" + id + " を作ってあります（同じ値段を二重に数えないため、2枚目は作りません）"}
	}
	supplier := contacts.OrgNameForPage(user, j.Supplier)
	if strings.TrimSpace(supplier) == "" {
		return "", &comm.MakeError{Status: http.StatusUnprocessableEntity, Message: "納品書から業者の名前が読めませんでした（部材発注書は1枚に1社です）"}
	}
	tmpl, err := cms.PageTemplateBody(PurchaseOrderTemplate)
	if err != nil {
		return "", &comm.MakeError{Status: http.StatusConflict, Message: "部材発注書ページを作れません: " + err.Error()}
	}
	build := func(pageID string) (string, error) {
		return buildSupplierDeliveryHTML(tmpl, pageID, supplier, ref, j)
	}
	if _, err := build(""); err != nil {
		return "", &comm.MakeError{Status: http.StatusUnprocessableEntity, Message: "部材発注書ページを作れません: " + err.Error()}
	}
	parentID, err := recordFolderOr("", user, RecordWorkFolder, time.Now())
	if err != nil {
		return "", err
	}
	newID, err := newBuiltPage(parentID, user, "部材発注書", build)
	if err != nil {
		return "", err
	}
	auth.Audit(user.Username, "our-order.from-delivery", newID+" "+supplier+" "+ref)
	return newID, nil
}
