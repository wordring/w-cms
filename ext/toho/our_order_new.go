package toho

// ─────────────────────────────────────────────────────────────────────────
// 部材発注書ページを1枚作る（2026-09-21）
//
// §7b の「足りない1つ」です:
//
//	加工製品ページ  材料／外注加工の**定義**
//	      ↓
//	未手配の一覧    **残要手配数**（受注横断・[unordered.go]）
//	      ↓
//	  ⚠ 人が仕入先を決める（相見積もりを見て）        ← 機械にはできない
//	      ↓
//	自社の部材発注書ページ `仕入先` タグ＋明細            ← **ここ**
//
// ⚠ **置き場は `記録／年／月／製造記録`**（2026-10-16 から。それまでは `部材発注／年／月`——2026-09-21 ユーザー決定）。受注ページの下には
// 置けません——発注は**納期のグループなどから**発行され、**1枚が複数の受注に
// またがります**。
//
// ⚠ **`弊社品番` を必ず書きます。** これが無いと、**どの加工製品のぶんか**を
// 誰も辿れません——手配状況の鏡はこの列で束ねています。
//
// ⚠ **部材発注書番号はページ番号そのもの**です（同日決定）。別に採番すると、
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
)

// ourOrderLine は部材発注書へ入れる1行です（画面から送られてくる形）。
type ourOrderLine struct {
	ProductID string `json:"product_id"` // 弊社品番（加工製品ページ）
	// ForOrder は「どの受注ページのための部材か」（2026-10-01・procure_ledger.go——受注ごとに数える）。
	ForOrder string `json:"for_order,omitempty"`
	// Mfg は「どの製造ページの分か」（2026-10-21・明細の列「製造」）。製造ページの発注部材表から作ると、作る口が埋めます。
	Mfg string `json:"mfg,omitempty"`
	// Breakdown は1行にまとめた行の内訳（`案件/弊社品番:数` を「、」で・2026-10-21・order_merge.go）。
	Breakdown string `json:"breakdown,omitempty"`
	// Vendor は推奨業者（発注部材表だけの列・2026-10-21）。
	Vendor string `json:"vendor,omitempty"`
	// ItemID・Color は `品番`・`表面`（2026-09-25 に足した）。⚠ それまで運んでいなかった
	// ので、**発注部材表から部材発注書を作ると品番と表面が落ちていました**（塗装・鍍金の
	// 部材発注書で「緑」が消える）。
	ItemID   string `json:"item_id"`
	Material string `json:"material"`
	Shape    string `json:"shape"`
	Size     string `json:"size"`
	Color    string `json:"color"`
	ItemName string `json:"item_name"`
	// Kind・No・Work・Spec・Supplied は `種類`・`番号`・`加工内容`・`仕様`・`支給`（2026-09-28・
	// 部材の種類ごとの運び方——order_kinds.go）。`種類` は手配済みを数える鍵の種類。
	Kind string `json:"kind,omitempty"`
	No   string `json:"no,omitempty"`
	// WorkPartNo は `加工部品番号`（外注加工で外注先へ出す部品・2026-10-22）。品番・品名（加工製品）とは別。
	WorkPartNo string `json:"work_part_no,omitempty"`
	// WorkPartName は `加工部品名`（外注先へ出す部品の名前・2026-10-22）。
	WorkPartName string `json:"work_part_name,omitempty"`
	Work         string `json:"work,omitempty"`
	Spec         string `json:"spec,omitempty"`
	Supplied     string `json:"supplied,omitempty"`
	Quantity     string `json:"quantity"`
	// Lot は「どのロットのための数か」（2026-10-14・見積依頼必要部材表と見積依頼部材表だけの列 `ロット`——rfq.go）。
	// 部材見積依頼の重複を「同じもの・同じロット」で見るために運びます。発注の表には列が無いので書かれません。
	Lot  string `json:"lot,omitempty"`
	Unit string `json:"unit"`
	Cost string `json:"cost"`
	Note string `json:"note"`
	// TempPage・TempRow は「**臨時部材表の何行目から来たか**」（2026-09-25）。
	// 発注部材表へ入れたら、その行を臨時部材表から消します（移す・引き算しない）。
	TempPage string `json:"temp_page,omitempty"`
	TempRow  int    `json:"temp_row,omitempty"`
}

// NewOurOrderAPIHandler は POST /api/our-order/new です。
//
// ⚠ **仕入先が無ければ作りません。** 1枚＝1社が部材発注書の単位で、あとから足せる
// ものではありません（相手が決まっていないなら、それは部材発注書ではなく手配メモです）。
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
		// ⚠ **作り終えたら、その表は消します**——中身は部材発注書ページへ移ったので、
		// **残すと古い写しになり、次の発注のときに混ざります**。⚠ 09-22 は部材発注書への
		// リンクに化けさせていましたが、2026-09-24 に「未発注の部材発注書」の一覧（DBから
		// 数える）ができたので、本文にリンクを残すのをやめました（ユーザー:「発注フォルダ
		// ページに『⚠ まだ発注していません（4行）』と言ったゴミが残っています」）。
		DraftPage  string `json:"draft_page"`
		DraftIndex string `json:"draft_index"`
		// Join は相乗りする部材発注書のページ番号です（2026-10-21・order_join.go）。あれば新しく作らず、その部材発注書に足す。
		Join string `json:"join"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	supplier := strings.TrimSpace(req.Supplier)
	if supplier == "" {
		cms.JSONFail(w, http.StatusBadRequest, "仕入先を入れてください（部材発注書は1枚に1社です）")
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
	// 発注部材表が製造ページにあれば、明細の「製造」の列にその製造ページを書く（2026-10-21）。
	req.Lines = withMfg(req.Lines, mfgParentOr(user, req.DraftPage))
	if strings.TrimSpace(req.Join) != "" {
		joinOurOrder(w, user, req.Join, supplier, req.Lines, req.DraftPage, req.DraftIndex)
		return
	}

	// ⚠ **送られてきた差出人をそのまま信じません**——署名を持つ人の中に居るかを確かめます（trustedSigner）。
	signerID := trustedSigner(user, req.Signer)
	// ⚠ **本文はテンプレート「部材発注書」を写して組みます**（2026-09-27）。**ページを作る前に**
	//    テンプレートと器（部材発注明細表・備考の節）を確かめます——作ってから断ると、
	//    題が「作成中」のページが置き場に残ります。
	tmpl, err := cms.PageTemplateBody(PurchaseOrderTemplate)
	if err != nil {
		cms.JSONFail(w, http.StatusConflict, "部材発注書ページを作れません: "+err.Error())
		return
	}
	build := func(pageID string) (string, error) {
		return buildOurOrderHTML(tmpl, pageID, supplier, when.Format("2006-01-02"),
			strings.TrimSpace(req.Due), strings.TrimSpace(req.Note), signerID, req.Lines)
	}
	if _, err := build(""); err != nil {
		cms.JSONFail(w, http.StatusConflict, "部材発注書ページを作れません: "+err.Error())
		return
	}

	// ⚠ **置き場は `記録／年／月／製造記録`**（2026-10-16・record_box.go）。**月は作った日**です（部材発注日ではない——
	// 利用者:「その文書を作成した日のフォルダで良いと思います」）。無ければ作ります。
	// 発注部材表が製造ページ（`ページ種類：製造`）にあっても、置き場は記録です（2026-10-21。2026-10-19〜20 は製造ページの子に作っていた）。
	// 製造ページとは、明細の「製造」の列でつながる（下の withMfg で、作る前に埋める）。
	parentID, err := recordFolderOr("", user, RecordWorkFolder, time.Now())
	if err != nil {
		cms.JSONFail(w, createFailStatus(err, http.StatusInternalServerError), err.Error())
		return
	}
	newID, err := newBuiltPage(parentID, user, "部材発注書", build)
	if err != nil {
		cms.JSONFail(w, createFailStatus(err, http.StatusInternalServerError), err.Error())
		return
	}
	auth.Audit(user.Username, "our-order-new", newID+" "+supplier+" "+
		strconv.Itoa(len(req.Lines))+"行")

	// ⚠ **元の発注部材表は消します**（2026-09-24。09-22 はリンクに化けさせていました）。
	//    部材発注書は部材発注フォルダ（2026-10-19 からはトップ直下の「製造」）の「未発注の部材発注書」がDBから並べるので、本文にリンクは要りません。
	//    ⚠ **失敗しても部材発注書は取り消しません**——**紙のほうが重い**ので、
	//    「表が残ってしまった」は人が消せば済みます。理由を添えるだけにします。
	out := map[string]any{"success": true, "page_id": newID, "url": "/" + newID}
	if note := removeDraftAfterOrder(user, req.DraftPage, req.DraftIndex); note != "" {
		out["draft_note"] = note
	}
	cms.WriteJSON(w, out)
}

// PurchaseOrderTemplate は部材発注書ページを作るテンプレートの題です（2026-09-27）。
//
// ⚠ **部材発注書ページの形はこのテンプレートが決めます**——タグの並び・部材発注明細表の列の並び・
// 備考欄。機械が書くのは題と値と行の数だけで、テンプレートが無ければ作りません。
// キャプション「部材発注明細表」の表が要ります。備考を書くときは見出し「備考」の節も要ります。
const PurchaseOrderTemplate = "部材発注書"

// 部材発注書ページ（弊社から業者への注文）の可変タグの名前です（2026-10-15 に受注ページのタグと分けた）。
//
// ⚠ **受注ページの `発注書番号`・`発注日`（`OrderNoTag`・`OrderedAtTag`）とは別のタグ**です——あちらは
// 客先の注文の番号と日付。2026-10-15 の利用者の決定（「発注」を「部材発注」に）で、業者の側だけ名前を改めた。
// ⚠ **紙には `部材` を外した名前で刷ります**（`paperHeadLabel`——業者に見えるものは変えない）。
const (
	PurchaseNoTag  = "部材発注書番号"
	PurchasedAtTag = "部材発注日"
)

// buildOurOrderHTML は部材発注書ページの本文を、テンプレート tmpl を埋めて組みます。
//
// ⚠ **列は見出しの言葉で合わせます**（`fillVocabTable`）——テンプレートで列を並べ替えても
// 崩れず、テンプレートに無い列は値があれば右端へ足します。
func buildOurOrderHTML(tmpl, pageID, supplier, orderAt, due, note, signerID string, lines []ourOrderLine) (string, error) {
	d := cms.NewPageDraft(PurchaseOrderTemplate, tmpl)
	d.SetTitle(issuerTitle(PageKindOurPurchase, "部材発注　"+supplier)) // 題の頭に【弊社】（2026-10-16・page_kind.go）
	// ⚠ **部材発注書番号はページ番号そのもの**（別に採番しない）。
	// ⚠ **タグは受注ページと同じ口で書きます**（`setHeaderTag`——日付は正規形へ）。
	setHeaderTag(d.DraftBlock, PurchaseNoTag, pageID)
	setHeaderTag(d.DraftBlock, SupplierTag, supplier)
	setHeaderTag(d.DraftBlock, PurchasedAtTag, orderAt)
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
	// ⚠ **`納品日` は空の欄で置きます**（2026-10-14・【要求】製造 §8の並びの 2「『発注書番号』『仕入先』
	//    『発注日』『発注担当』『納品日』といったタグが並びます」）——届いた日は機械には分からないので、人が書く場所だけ
	//    作ります。テンプレートに欄があればそのまま。紙には刷りません（設定 `order_print_heads` に無い）。
	d.DraftBlock.EnsureTag(DeliveredAtTag)

	rows := make([]map[string]string, 0, len(lines))
	for _, ln := range lines {
		rows = append(rows, ourOrderRowValues(ln))
	}
	if _, err := fillVocabTable(d.DraftBlock, ourOrderItemsType, rows); err != nil {
		return "", err
	}
	if err := fillOrderNote(d, note); err != nil {
		return "", err
	}
	return d.HTML(), nil
}

// DeliveredAtTag は部材発注書ページの、仕入先から品物が届いた日のタグです（2026-10-14・型は設定の語彙で date）。
// 「納期」は予定や希望、「納品日」は確定した期日です（【要求】製造 §8）。
const DeliveredAtTag = "納品日"

// orderNoteHeading は部材発注書ページの備考欄の見出しです（本文とPDFで共有）。
const orderNoteHeading = "備考"

// fillOrderNote は部材発注明細表の下の**備考欄**（見出し「備考」の節）へ note を入れます（2026-09-24）。
//
// 要求（【要求】製造 §8）:「その下のブロックに『備考』入力欄があります。
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

// costCell は単価を書き出します（人が表に書いたとおり）。
//
// ⚠ **`0` も書きます**（2026-10-21 から。それまでは `0` を空に落としていた）。紙の上の `0` は**「0円で発注した」**、
// 空欄は**「まだ分からない」**で、別のことです——人が `0` と書いたなら、それは「0円」です。
//
// > 2026-09-22 利用者:「価格を入れないという選択肢もあると便利です。（0円とは別がありがたい）」
//
// 「参考単価が無い」を `0` にしないのは、**送り元**の仕事です（機械が集めるとき——mfg_view.go の `unorderedCostValue`）。
// ここで落とすと、人が発注部材表に書いた `0` まで空になります（【要求】製造 §4 の「⚠ いま」に書いていた食い違い）。
func costCell(s string) string {
	return strings.TrimSpace(s)
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

// ourOrderRowValues は部材発注明細表の1行の値です（列の機械キー → 値）。部材発注書を作るときと、相乗りで行を足すとき（order_join.go）が使う。
func ourOrderRowValues(ln ourOrderLine) map[string]string {
	// ⚠ **材料の行に `品名` は書きません。** 材料に単独の名前は無く、
	//    **材質・形状・寸法の3つで決まります**——`鉄 FB t4.5*75*1090` と書くと、
	//    同じことが紙の上で2回言われ、**直すときに食い違います**。
	//    購入部品は逆で、**品名が同一性そのもの**なので残します。
	return map[string]string{
		// for-order は案件（どの案件のための部材か）——2026-10-19 から写す。それまでは発注部材表から部材発注書へ移すときに落ち、
		// 手配済みの数え方（procure_ledger.go）は案件の無い発注を「在庫」として納期の早い案件から当てていた。紙には刷らない。
		"our-item-id":    ln.ProductID,
		"for-order":      ln.ForOrder,
		"mfg":            ln.Mfg,
		"breakdown":      ln.Breakdown,
		"kind":           ln.Kind,
		"no":             ln.No,
		"work-part-no":   ln.WorkPartNo,
		"work-part-name": ln.WorkPartName,
		"work":           ln.Work,
		"spec":           ln.Spec,
		"supplied":       ln.Supplied,
		"item-id":        ln.ItemID,
		"item-name":      itemNameOf(ln),
		"material":       ln.Material,
		"shape":          ln.Shape,
		"size":           ln.Size,
		"color":          ln.Color,
		"quantity":       ln.Quantity,
		"unit":           ln.Unit,
		"cost":           costCell(ln.Cost),
		"note":           ln.Note,
		// ⚠ **状態は「未発注」から始めます**（2026-09-22）。紙はできましたが、
		//    **まだ外へ出ていません**——メール・FAX・手渡しのどれかで出したときに
		//    `発注済` へ進みます（[order_status.go](order_status.go)）。
		//    ⚠ **それまでの既定は `未納品` でした**——「紙を作る＝発注した」と
		//    読む形で、**出す前の紙と出した紙が見分けられません**でした。
		"status": OrderLineUnsent,
	}
}
