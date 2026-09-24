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
	stdhtml "html"
	"net/http"
	"strconv"
	"strings"
	"time"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
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
		Signer   string         `json:"signer"`
		Lines    []ourOrderLine `json:"lines"`
		// DraftPage / DraftIndex は「**どの発注部材表から作ったか**」です（2026-09-22）。
		//
		// ⚠ **作り終えたら、その表はこの発注書ページへのリンクに化けます**
		// （ユーザー決定:「**発注書ページが出来て、実際に発注するまで発注ページに
		// 発注書ページへのリンクが残れば良いのでは？**」）——中身は発注書ページへ
		// 移ったので、**残すと古い写しになり、次の発注のときに混ざります**。
		DraftPage  string `json:"draft_page"`
		DraftIndex string `json:"draft_index"`
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
	// ⚠ **送られてきた差出人をそのまま信じません**——**署名を持つ人の中に居るか**を
	//    確かめます。読めない人・署名の無い人を紙に刷らないため（画面は選ばせるだけで、
	//    口は誰でも叩けます）。
	signerID := ""
	if want := strings.TrimSpace(req.Signer); want != "" {
		for _, sg := range Signers(user) {
			if page.FormatID(sg.PageID) == want {
				signerID = want
				break
			}
		}
	}
	body := buildOurOrderHTML(newID, supplier, when.Format("2006-01-02"),
		strings.TrimSpace(req.Due), strings.TrimSpace(req.Note), signerID, req.Lines)
	if !rewriteBodyOrFail(w, newID, user.Username, func(string) string { return body }) {
		return
	}
	auth.Audit(user.Username, "our-order-new", newID+" "+supplier+" "+
		strconv.Itoa(len(req.Lines))+"行")

	// ⚠ **元の発注部材表を、この発注書ページへのリンクに化けさせます**（2026-09-22）。
	//    ⚠ **失敗しても発注書は取り消しません**——**紙のほうが重い**ので、
	//    「表が残ってしまった」は人が消せば済みます。理由を添えるだけにします。
	out := map[string]any{"success": true, "page_id": newID, "url": "/" + newID}
	if note := replaceDraftWithLink(user, req.DraftPage, req.DraftIndex,
		newID, supplier, len(req.Lines)); note != "" {
		out["draft_note"] = note
	}
	cms.WriteJSON(w, out)
}

// buildOurOrderHTML は発注書ページの本文を組みます。
//
// ⚠ **見出し行は宣言から組みます**（`headerRowHTML`）——手書きに戻すと、
// 列を足した日に**足した列がどこからも読めません**。エラーは出ません。
func buildOurOrderHTML(pageID, supplier, orderAt, due, note, signerID string, lines []ourOrderLine) string {
	var b strings.Builder
	b.WriteString(`<h1>発注　` + stdhtml.EscapeString(supplier) + `</h1>`)
	b.WriteString(`<dl data-type="tags">`)
	// ⚠ **発注書番号はページ番号そのもの**（別に採番しない）。
	// ⚠ **タグは受注ページと同じ口で書きます**（`writeHeaderPair`——日付は正規形へ）。
	writeHeaderPair(&b, OrderNoTag, pageID)
	writeHeaderPair(&b, SupplierTag, supplier)
	writeHeaderPair(&b, OrderedAtTag, orderAt)
	if due != "" {
		writeHeaderPair(&b, DueDateTag, due)
	}
	// ⚠ **備考はタグにしません**（2026-09-24 ユーザー:「発注書ページに『備考』タグが
	//    ありますが、要求に『備考』タグはありません」）——要求は**表の下の備考欄**で、
	//    **複数行書けます**。下の `orderNoteSectionHTML` が書きます。
	// ⚠ **誰が出したかを残します**（2026-09-22）。値は連絡帳の担当者ページのID
	//    （`ref` 型なので押せば飛べる）。
	//    ⚠ **署名の文面は焼き込みません**——**出した紙の正本はPDF**で、それはこの
	//    ページの添付として残ります（`SaveAttachmentFrom`）。本文へ写すと二重になり、
	//    あとから署名が変わったときに**紙と本文が食い違います**。
	if signerID != "" {
		writeHeaderPair(&b, OrderSignerTag, signerID)
	}
	b.WriteString(`</dl>`)

	b.WriteString(`<table><caption>` +
		stdhtml.EscapeString(displayNameOf(ourOrderItemsType)) + `</caption><tbody>`)
	b.WriteString(headerRowHTML(ourOrderItemsType))
	for _, ln := range lines {
		// ⚠ **材料の行に `品名` は書きません。** 材料に単独の名前は無く、
		//    **材質・形状・寸法の3つで決まります**——`鉄 FB t4.5*75*1090` と書くと、
		//    同じことが紙の上で2回言われ、**直すときに食い違います**。
		//    購入部品は逆で、**品名が同一性そのもの**なので残します。
		itemName := itemNameOf(ln)
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
			// ⚠ **状態は「未発注」から始めます**（2026-09-22）。紙はできましたが、
			//    **まだ外へ出ていません**——メール・FAX・手渡しのどれかで出したときに
			//    `発注済` へ進みます（[order_status.go](order_status.go)）。
			//    ⚠ **それまでの既定は `未納品` でした**——「紙を作る＝発注した」と
			//    読む形で、**出す前の紙と出した紙が見分けられません**でした。
			"status": OrderLineUnsent,
		}
		b.WriteString(`<tr>`)
		for _, c := range columnsOf(ourOrderItemsType) {
			b.WriteString(`<td>` + stdhtml.EscapeString(vals[c.Field]) + `</td>`)
		}
		b.WriteString(`</tr>`)
	}
	b.WriteString(`</tbody></table>`)
	b.WriteString(orderNoteSectionHTML(note))
	return b.String()
}

// orderNoteHeading は発注書ページの備考欄の見出しです（本文とPDFで共有）。
const orderNoteHeading = "備考"

// orderNoteSectionHTML は発注明細の下の**備考欄**を組みます（2026-09-24）。
//
// 要求（【要求】発注フォルダ）:「その下のブロックに『備考』入力欄があります。
// **備考欄は複数行書けます**」。⚠ **相手に伝えることを書く欄**です——行の `備考` 列
// （他社が知る必要のないメモ）とは別で、こちらは**紙に刷ります**。
//
// ⚠ **空でも欄は置きます**——後から書き足す場所が画面に無いと、人はタグや
// 行の備考に書いてしまいます。
func orderNoteSectionHTML(note string) string {
	var b strings.Builder
	b.WriteString(`<section><h2>` + orderNoteHeading + `</h2>`)
	wrote := false
	for _, ln := range strings.Split(strings.ReplaceAll(note, "\r\n", "\n"), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			b.WriteString(`<p>` + stdhtml.EscapeString(ln) + `</p>`)
			wrote = true
		}
	}
	if !wrote {
		b.WriteString(`<p><br/></p>`)
	}
	b.WriteString(`</section>`)
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

// itemNameOf は、その行に書く `品名` を返します。
//
// ⚠ **材料の行には書きません。** 材料に単独の名前は無く、**材質・形状・寸法の3つで
// 決まります**——`鉄 FB t4.5*75*1090` と書くと、同じことが紙の上で2回言われ、
// **直すときに食い違います**。⚠ **購入部品は逆**で、**品名が同一性そのもの**なので残します。
//
// ⚠ **発注部材表（`order_draft.go`）と共有します**——同じ規則を2か所に書くと、
// **片方だけ直した日に、表と紙で品名が違います**。
func itemNameOf(ln ourOrderLine) string {
	if strings.TrimSpace(ln.Material) != "" || strings.TrimSpace(ln.Shape) != "" ||
		strings.TrimSpace(ln.Size) != "" {
		return ""
	}
	return strings.TrimSpace(ln.ItemName)
}
