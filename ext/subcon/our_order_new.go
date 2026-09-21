package subcon

// ─────────────────────────────────────────────────────────────────────────
// 発注書ページを1枚作る（2026-09-21）
//
// §7b の「足りない1つ」です:
//
//	加工製品ページ  材料／外注加工の**定義**
//	      ↓
//	未手配の一覧    **残要手配数**（受注横断・[unordered.go]）
//	      ↓
//	  ⚠ 人が仕入先を決める（相見積もりを見て）        ← 機械にはできない
//	      ↓
//	自社の発注書ページ `仕入先` タグ＋明細            ← **ここ**
//
// ⚠ **置き場は `発注／年／月`**（2026-09-21 ユーザー決定）。受注ページの下には
// 置けません——発注は**納期のグループなどから**発行され、**1枚が複数の受注に
// またがります**。
//
// ⚠ **`弊社品番` を必ず書きます。** これが無いと、**どの加工製品のぶんか**を
// 誰も辿れません——手配状況の鏡はこの列で束ねています。
//
// ⚠ **発注書番号はページ番号そのもの**です（同日決定）。別に採番すると、
// 同じものに2つの名前ができます。
// ─────────────────────────────────────────────────────────────────────────

import (
	"encoding/json"
	stdhtml "html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// ourOrderLine は発注書へ入れる1行です（画面から送られてくる形）。
type ourOrderLine struct {
	ProductID string `json:"product_id"` // 弊社品番（加工製品ページ）
	Material  string `json:"material"`
	Shape     string `json:"shape"`
	Size      string `json:"size"`
	ItemName  string `json:"item_name"`
	Quantity  string `json:"quantity"`
	Unit      string `json:"unit"`
	Cost      string `json:"cost"`
	Note      string `json:"note"`
}

// NewOurOrderAPIHandler は POST /api/our-order/new です。
//
// ⚠ **仕入先が無ければ作りません。** 1枚＝1社が発注書の単位で、あとから足せる
// ものではありません（相手が決まっていないなら、それは発注書ではなく手配メモです）。
func NewOurOrderAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		Supplier string         `json:"supplier"`
		OrderAt  string         `json:"order_at"`
		Due      string         `json:"due"`
		Note     string         `json:"note"`
		Lines    []ourOrderLine `json:"lines"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	supplier := strings.TrimSpace(req.Supplier)
	if supplier == "" {
		cms.JSONFail(w, http.StatusBadRequest, "仕入先を入れてください（発注書は1枚に1社です）")
		return
	}
	if len(req.Lines) == 0 {
		cms.JSONFail(w, http.StatusBadRequest, "発注する行を1つ以上選んでください")
		return
	}
	when := time.Now()
	if t, err := time.Parse("2006-01-02", strings.TrimSpace(req.OrderAt)); err == nil {
		when = t
	}

	// ⚠ **置き場は `発注／年／月`**（年月は発注日）。無ければ作ります。
	boxID, err := cms.EnsureTopLevelBox(PurchaseOrderBoxTitle, user.Username)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError,
			"「"+PurchaseOrderBoxTitle+"」ページを用意できません: "+err.Error())
		return
	}
	monthID, err := cms.EnsureDateFolders(boxID, user.Username, when)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "年月のフォルダを作れません: "+err.Error())
		return
	}
	newID, err := cms.CreateChildPage(monthID, user.Username, "<h1>作成中</h1>")
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "発注書ページを作れません: "+err.Error())
		return
	}
	body := buildOurOrderHTML(newID, supplier, when.Format("2006-01-02"),
		strings.TrimSpace(req.Due), strings.TrimSpace(req.Note), req.Lines)
	if err := cms.RewriteBody(newID, user.Username, func(string) string { return body }); err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "本文を書けません: "+err.Error())
		return
	}
	auth.Audit(user.Username, "our-order-new", newID+" "+supplier+" "+
		strconv.Itoa(len(req.Lines))+"行")
	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(map[string]any{
		"success": true, "page_id": newID, "url": "/" + newID})
}

// buildOurOrderHTML は発注書ページの本文を組みます。
//
// ⚠ **見出し行は宣言から組みます**（`headerRowHTML`）——手書きに戻すと、
// 列を足した日に**足した列がどこからも読めません**。エラーは出ません。
func buildOurOrderHTML(pageID, supplier, orderAt, due, note string, lines []ourOrderLine) string {
	var b strings.Builder
	b.WriteString(`<h1>発注　` + stdhtml.EscapeString(supplier) + `</h1>`)
	b.WriteString(`<dl data-type="tags">`)
	// ⚠ **発注書番号はページ番号そのもの**（別に採番しない）。
	b.WriteString(`<dt>` + OrderNoTag + `</dt><dd>` + stdhtml.EscapeString(pageID) + `</dd>`)
	b.WriteString(`<dt>` + SupplierTag + `</dt><dd>` + stdhtml.EscapeString(supplier) + `</dd>`)
	b.WriteString(`<dt>` + OrderedAtTag + `</dt><dd>` + stdhtml.EscapeString(orderAt) + `</dd>`)
	if due != "" {
		b.WriteString(`<dt>` + DueDateTag + `</dt><dd>` + stdhtml.EscapeString(due) + `</dd>`)
	}
	if note != "" {
		b.WriteString(`<dt>備考</dt><dd>` + stdhtml.EscapeString(note) + `</dd>`)
	}
	b.WriteString(`</dl>`)

	b.WriteString(`<table data-type="` + ourOrderItemsType + `"><caption>` +
		stdhtml.EscapeString(displayNameOf(ourOrderItemsType)) + `</caption><tbody>`)
	b.WriteString(headerRowHTML(ourOrderItemsType))
	for _, ln := range lines {
		// ⚠ **材料の行に `品名` は書きません。** 材料に単独の名前は無く、
		//    **材質・形状・寸法の3つで決まります**——`鉄 FB t4.5*75*1090` と書くと、
		//    同じことが紙の上で2回言われ、**直すときに食い違います**。
		//    購入部品は逆で、**品名が同一性そのもの**なので残します。
		itemName := strings.TrimSpace(ln.ItemName)
		if strings.TrimSpace(ln.Material) != "" || strings.TrimSpace(ln.Shape) != "" ||
			strings.TrimSpace(ln.Size) != "" {
			itemName = ""
		}
		vals := map[string]string{
			"our-item-id": strings.TrimSpace(ln.ProductID),
			"item-name":   itemName,
			"material":    strings.TrimSpace(ln.Material),
			"shape":       strings.TrimSpace(ln.Shape),
			"size":        strings.TrimSpace(ln.Size),
			"quantity":    strings.TrimSpace(ln.Quantity),
			"unit":        strings.TrimSpace(ln.Unit),
			"cost":        moneyOrEmpty(ln.Cost),
			"note":        strings.TrimSpace(ln.Note),
			// ⚠ **状態は「未納品」から始めます**（空だと「納品済かどうか不明」に見える）。
			"status": "未納品",
		}
		b.WriteString(`<tr>`)
		for _, c := range columnsOf(ourOrderItemsType) {
			b.WriteString(`<td>` + stdhtml.EscapeString(vals[c.Field]) + `</td>`)
		}
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table>`)
	return b.String()
}

// moneyOrEmpty は単価を書き出します。
//
// ⚠ **`0` は書きません。** 紙の上の `0` は**「0円で発注した」**という意味で、
// **「まだ分からない」とは別のこと**です——⚠ **仕入先は紙に書いてあるとおりに
// 読みます**。空欄なら「あとで決める」と伝わります。
//
// ⚠ **送り元でも落としていますが、ここでも落とします。** この口は画面以外からも
// 叩けるので、**紙に出る手前が最後の砦**です。
func moneyOrEmpty(s string) string {
	v := strings.TrimSpace(s)
	if n, err := strconv.Atoi(v); err == nil && n == 0 {
		return ""
	}
	return v
}
