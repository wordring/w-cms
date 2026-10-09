package toho

// ─────────────────────────────────────────────────────────────────────────
// 発注書をメールで送る——送る欄の用件「発注書」（2026-09-30）
//
// 利用者:「メール表示、メール送信、メール編集などを部品化したら良いと思います」。
//
// それまで発注書の送信の欄は、宛先・件名・本文の欄をこの拡張が自分で描き、送るときは画面が
// 「PDFを作る → 送る → 発注済みにする」の3段を順に呼んでいました（order_send.go の旧 orderSendFormHTML・
// app.js の旧 sendMail）。いまは**メールを書く欄は1つの部品**（`assets/mail-compose.js`）で、ここは
// その用件の中身だけを渡します（開発方針 §0「口はこちら、言葉は拡張から」・`comm.RegisterSendPurpose`）:
//
//	初期値   … 宛先の候補（連絡帳の仕入先）・件名（発注書番号入り）・本文（メールの署名入り）・
//	           外注加工の資料（印を付けて並べる）・連絡帳に宛先が無い／資料が無いの一言
//	送る直前 … 発注書のPDFを作って添える（ページにも表示する・`makeOrderPDF`）
//	送れた後 … 取消でない行を発注済みに・発注日を送った日に（`markOrderSent`）
//
// ⚠ **3段の順と「送れたのに印が付かなかった」の扱いは変えていません**——印を付けられなかったときは
// 送れたことと一緒に理由を返し（`after_error`）、人が行ごとの「✓ 発注済」を押します。
// ⚠ 送る直前に PDF を作り直すのは 09-22 からのまま（「表示中の最新のPDFを送る」〔C4・09-24 決定〕は
// 再送とキャンセルの仕事で扱う——【要求】発注フォルダ「発注書の再送とキャンセル」）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"w-cms/ext/comm"
	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
)

// OrderMailPurpose は送る欄の用件の名前です（下書きの `下書き` タグの値にもなる）。
const OrderMailPurpose = "発注書"

func init() {
	comm.RegisterSendPurpose(OrderMailPurpose, comm.SendPurpose{
		Defaults:  orderMailDefaults,
		Prepare:   prepareOrderMail,
		AfterSent: afterOrderMail,
		NeedsPage: true,
	})
}

// orderMailDefaults は発注書のメールの初期値です。
func orderMailDefaults(user *auth.User, pageID string) (comm.ComposeDraft, error) {
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		return comm.ComposeDraft{}, errors.New("発注書のページを読めません: " + err.Error())
	}
	head, rows, _, err := readOrderDoc(body)
	if err != nil {
		return comm.ComposeDraft{}, err
	}
	supplier := strings.TrimSpace(head[SupplierTag])
	d := comm.ComposeDraft{
		Purpose: OrderMailPurpose, PageID: pageID,
		To:      supplierAddresses(user, supplier),
		Subject: orderMailSubject(pageID, supplier),
		Body:    orderMailBody(head, supplier, pageID),
		// ⚠ **PDFは押したときに作ります**（下書きを開いただけで添付を増やさない）。
		SendNote: "印の付いた発注書のPDFは送るときに作って添付し（このページの添付にも残ります）、" +
			"送れたら取消でない行を発注済みにします。",
		// 送るときに作る PDF を添付の欄に印つきで並べる（2026-10-03・見積書と同じ）。
		Generated: "発注書 " + pageID + ".pdf",
		Reload: true,
	}
	noteNoSupplierAddress(&d, supplier)
	// 外注加工の資料——取消していない行の「資料 <番号>」から。
	attachLineDocs(&d, user, rows)
	return d, nil
}

// noteNoSupplierAddress は、仕入先の連絡先が連絡帳から引けなかったら送る欄に注意を添えます（発注書・見積依頼書）。
//
// ⚠ **引けなかったことを黙りません**——空欄だと「連絡帳に居ないから出せない」のか「引く仕掛けが壊れている」のかが
// 分かりません。
func noteNoSupplierAddress(d *comm.ComposeDraft, supplier string) {
	if len(d.To) == 0 {
		d.Notes = append(d.Notes, "⚠ 「"+supplier+"」の連絡先が連絡帳にありません（題が一致する組織ページに「"+
			contacts.EmailTag+"」のタグを付けると、ここに出ます）。")
	}
}

// attachLineDocs は、明細の行（弊社品番＋番号）が指す外注加工の資料を、全部に印を付けて送る欄の添付に並べます
// （人が外せる）。資料が無い・読めない行は一言を添えます（発注書・見積依頼書——2026-10-09 に写しを寄せた）。
func attachLineDocs(d *comm.ComposeDraft, user *auth.User, rows []map[string]string) {
	docs, notes := orderDocs(user, rows)
	for _, doc := range docs {
		d.Attachments = append(d.Attachments, comm.ComposeAttachment{
			PageID: doc.PageID, File: doc.File, Name: doc.Name, Checked: true})
	}
	d.Notes = append(d.Notes, notes...)
}

// prepareOrderMail は送る直前に発注書のPDFを作り、添えるファイルとして返します。
//
// ⚠ **関門は `/api/order-pdf` と同じ**です（write 権限・テンプレートの外・誰も編集していない——本文に
// PDFを開く印を置くため）。断るときは応答を書いて false（メールは1通も出ない）。
func prepareOrderMail(w http.ResponseWriter, r *http.Request, pageID string) ([]comm.ComposeAttachment, bool) {
	user := auth.CurrentUser(r)
	if !requireWritableIdle(w, r, pageID) {
		return nil, false
	}
	made, ok := makeOrderPDF(w, user, pageID)
	if !ok {
		return nil, false
	}
	// ⚠ **送るときの名前は日時を外します**——相手には保存名の日時は意味がなく、件名と揃っていたほうが
	// 探しやすい（09-22 の画面の送り方と同じ名前）。
	return []comm.ComposeAttachment{{PageID: pageID, File: made.File, Name: "発注書 " + pageID + ".pdf", Checked: true}}, true
}

// afterOrderMail は送れたあとに、取消でない行を発注済みにして発注日を送った日にします。
//
// ⚠ **ここはもう応答を書けません**（メールは出たあと）——書けない・編集中なら理由を返し、送る欄が
// 「送りましたが、発注済みの印を付けられませんでした」と人に伝えます。
func afterOrderMail(user *auth.User, pageID, _ string) error {
	n, err := strconv.Atoi(pageID)
	if err != nil {
		return errors.New("発注書のページIDが不正です")
	}
	if !page.GetPerms(n).CanWrite(user) || cms.IsTemplateArea(pageID) {
		return errors.New("発注済みの印を付けられませんでした（このページを書き換える権限がありません）。行ごとの「✓ 発注済」を押してください。")
	}
	if holder, open := editlock.Locks.EditorOpen(n); open {
		return errors.New("発注済みの印を付けられませんでした（このページは編集中です（" + holder + "））。編集が終わったら行ごとの「✓ 発注済」を押してください。")
	}
	changed := 0
	sentOn := time.Now().Format("2006-01-02")
	if err := cms.RewriteBody(pageID, user.Username, func(cur string) string {
		out, c := markOrderSent(cur, sentOn)
		changed = c
		return out
	}); err != nil {
		return errors.New("発注済みの印を付けられませんでした: " + err.Error() + "　行ごとの「✓ 発注済」を押してください。")
	}
	auth.Audit(user.Username, "our-order.sent", pageID+" "+sendByMail+" "+strconv.Itoa(changed)+"行")
	return nil
}
