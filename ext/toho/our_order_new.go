package toho

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
	// ForOrder は「どの受注ページのための部材か」（2026-10-01・procure_ledger.go——受注ごとに数える）。
	ForOrder string `json:"for_order,omitempty"`
	// ItemID・Color は `品番`・`表面`（2026-09-25 に足した）。⚠ それまで運んでいなかった
	// ので、**発注部材表から発注書を作ると品番と表面が落ちていました**（塗装・鍍金の
	// 発注書で「緑」が消える）。
	ItemID   string `json:"item_id"`
	Material string `json:"material"`
	Shape    string `json:"shape"`
	Size     string `json:"size"`
	Color    string `json:"color"`
	ItemName string `json:"item_name"`
	// Kind・No・Work・Spec・Supplied は `種類`・`番号`・`加工内容`・`仕様`・`支給`（2026-09-28・
	// 部材の種類ごとの運び方——order_kinds.go）。`種類` は手配済みを数える鍵の種類。
	Kind     string `json:"kind,omitempty"`
	No       string `json:"no,omitempty"`
	Work     string `json:"work,omitempty"`
	Spec     string `json:"spec,omitempty"`
	Supplied string `json:"supplied,omitempty"`
	Quantity string `json:"quantity"`
	Unit     string `json:"unit"`
	Cost     string `json:"cost"`
	Note     string `json:"note"`
	// TempPage・TempRow は「**臨時部材表の何行目から来たか**」（2026-09-25）。
	// 発注部材表へ入れたら、その行を臨時部材表から消します（移す・引き算しない）。
	TempPage string `json:"temp_page,omitempty"`
	TempRow  int    `json:"temp_row,omitempty"`
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
		// ⚠ **作り終えたら、その表は消します**——中身は発注書ページへ移ったので、
		// **残すと古い写しになり、次の発注のときに混ざります**。⚠ 09-22 は発注書への
		// リンクに化けさせていましたが、2026-09-24 に「未発注の発注書」の一覧（DBから
		// 数える）ができたので、本文にリンクを残すのをやめました（ユーザー:「発注フォルダ
		// ページに『⚠ まだ発注していません（4行）』と言ったゴミが残っています」）。
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
	// ⚠ **本文はテンプレート「発注書」を写して組みます**（2026-09-27）。**ページを作る前に**
	//    テンプレートと器（発注明細の表・備考の節）を確かめます——作ってから断ると、
	//    題が「作成中」のページが置き場に残ります。
	tmpl, err := cms.PageTemplateBody(PurchaseOrderTemplate)
	if err != nil {
		cms.JSONFail(w, http.StatusConflict, "発注書ページを作れません: "+err.Error())
		return
	}
	build := func(pageID string) (string, error) {
		return buildOurOrderHTML(tmpl, pageID, supplier, when.Format("2006-01-02"),
			strings.TrimSpace(req.Due), strings.TrimSpace(req.Note), signerID, req.Lines)
	}
	if _, err := build(""); err != nil {
		cms.JSONFail(w, http.StatusConflict, "発注書ページを作れません: "+err.Error())
		return
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
	body, err := build(newID)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "発注書ページを組めません: "+err.Error())
		return
	}
	if !rewriteBodyOrFail(w, newID, user.Username, func(string) string { return body }) {
		return
	}
	auth.Audit(user.Username, "our-order-new", newID+" "+supplier+" "+
		strconv.Itoa(len(req.Lines))+"行")

	// ⚠ **元の発注部材表は消します**（2026-09-24。09-22 はリンクに化けさせていました）。
	//    発注書は発注フォルダの「未発注の発注書」がDBから並べるので、本文にリンクは要りません。
	//    ⚠ **失敗しても発注書は取り消しません**——**紙のほうが重い**ので、
	//    「表が残ってしまった」は人が消せば済みます。理由を添えるだけにします。
	out := map[string]any{"success": true, "page_id": newID, "url": "/" + newID}
	if note := removeDraftAfterOrder(user, req.DraftPage, req.DraftIndex); note != "" {
		out["draft_note"] = note
	}
	cms.WriteJSON(w, out)
}

// PurchaseOrderTemplate は発注書ページを作るテンプレートの題です（2026-09-27）。
//
// ⚠ **発注書ページの形はこのテンプレートが決めます**——タグの並び・発注明細の列の並び・
// 備考欄。機械が書くのは題と値と行の数だけで、テンプレートが無ければ作りません。
// キャプション「発注明細」の表が要ります。備考を書くときは見出し「備考」の節も要ります。
const PurchaseOrderTemplate = "発注書"

// buildOurOrderHTML は発注書ページの本文を、テンプレート tmpl を埋めて組みます。
//
// ⚠ **列は見出しの言葉で合わせます**（`fillVocabTable`）——テンプレートで列を並べ替えても
// 崩れず、テンプレートに無い列は値があれば右端へ足します。
func buildOurOrderHTML(tmpl, pageID, supplier, orderAt, due, note, signerID string, lines []ourOrderLine) (string, error) {
	d := cms.NewPageDraft(PurchaseOrderTemplate, tmpl)
	d.SetTitle("発注　" + supplier)
	// ⚠ **発注書番号はページ番号そのもの**（別に採番しない）。
	// ⚠ **タグは受注ページと同じ口で書きます**（`setHeaderTag`——日付は正規形へ）。
	setHeaderTag(d.DraftBlock, OrderNoTag, pageID)
	setHeaderTag(d.DraftBlock, SupplierTag, supplier)
	setHeaderTag(d.DraftBlock, OrderedAtTag, orderAt)
	setHeaderTag(d.DraftBlock, DueDateTag, due)
	// ⚠ **備考はタグにしません**（2026-09-24 ユーザー:「発注書ページに『備考』タグが
	//    ありますが、要求に『備考』タグはありません」）——要求は**表の下の備考欄**で、
	//    **複数行書けます**。下の `fillOrderNote` が書きます。
	// ⚠ **誰が出したかを残します**（2026-09-22）。値は連絡帳の担当者ページのID
	//    （`ref` 型なので押せば飛べる）。
	//    ⚠ **署名の文面は焼き込みません**——**出した紙の正本はPDF**で、それはこの
	//    ページの添付として残ります（`SaveAttachmentFrom`）。本文へ写すと二重になり、
	//    あとから署名が変わったときに**紙と本文が食い違います**。
	setHeaderTag(d.DraftBlock, OrderSignerTag, signerID)

	rows := make([]map[string]string, 0, len(lines))
	for _, ln := range lines {
		// ⚠ **材料の行に `品名` は書きません。** 材料に単独の名前は無く、
		//    **材質・形状・寸法の3つで決まります**——`鉄 FB t4.5*75*1090` と書くと、
		//    同じことが紙の上で2回言われ、**直すときに食い違います**。
		//    購入部品は逆で、**品名が同一性そのもの**なので残します。
		rows = append(rows, map[string]string{
			"our-item-id": ln.ProductID,
			"kind":        ln.Kind,
			"no":          ln.No,
			"work":        ln.Work,
			"spec":        ln.Spec,
			"supplied":    ln.Supplied,
			"item-id":     ln.ItemID,
			"item-name":   itemNameOf(ln),
			"material":    ln.Material,
			"shape":       ln.Shape,
			"size":        ln.Size,
			"color":       ln.Color,
			"quantity":    ln.Quantity,
			"unit":        ln.Unit,
			"cost":        moneyOrEmpty(ln.Cost),
			"note":        ln.Note,
			// ⚠ **状態は「未発注」から始めます**（2026-09-22）。紙はできましたが、
			//    **まだ外へ出ていません**——メール・FAX・手渡しのどれかで出したときに
			//    `発注済` へ進みます（[order_status.go](order_status.go)）。
			//    ⚠ **それまでの既定は `未納品` でした**——「紙を作る＝発注した」と
			//    読む形で、**出す前の紙と出した紙が見分けられません**でした。
			"status": OrderLineUnsent,
		})
	}
	if _, err := fillVocabTable(d.DraftBlock, ourOrderItemsType, rows); err != nil {
		return "", err
	}
	if err := fillOrderNote(d, note); err != nil {
		return "", err
	}
	return d.HTML(), nil
}

// orderNoteHeading は発注書ページの備考欄の見出しです（本文とPDFで共有）。
const orderNoteHeading = "備考"

// fillOrderNote は発注明細の下の**備考欄**（見出し「備考」の節）へ note を入れます（2026-09-24）。
//
// 要求（【要求】発注フォルダ）:「その下のブロックに『備考』入力欄があります。
// **備考欄は複数行書けます**」。⚠ **相手に伝えることを書く欄**です——行の `備考` 列
// （他社が知る必要のないメモ）とは別で、こちらは**紙に刷ります**。
//
// ⚠ **空でも欄は置きます**——後から書き足す場所が画面に無いと、人はタグや
// 行の備考に書いてしまいます（欄はテンプレートが持つ。note が空なら欄はそのまま）。
// ⚠ note があるのに欄が無ければ作りません（`cms.TemplateSlotError`）——紙に刷る言葉を
// 黙って捨てないため。
func fillOrderNote(d *cms.PageDraft, note string) error {
	var b strings.Builder
	for _, ln := range strings.Split(strings.ReplaceAll(note, "\r\n", "\n"), "\n") {
		if ln = strings.TrimSpace(ln); ln != "" {
			b.WriteString(`<p>` + stdhtml.EscapeString(ln) + `</p>`)
		}
	}
	if b.Len() == 0 {
		return nil
	}
	sec, err := d.RequireContainer(orderNoteHeading)
	if err != nil {
		return err
	}
	sec.SetContent(b.String())
	return nil
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
