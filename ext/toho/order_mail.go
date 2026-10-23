package toho

// ─────────────────────────────────────────────────────────────────────────
// 部材発注書をメールで送る——送る欄の用件「部材発注書」（2026-09-30）
//
// 利用者:「メール表示、メール送信、メール編集などを部品化したら良いと思います」。
//
// それまで部材発注書の送信の欄は、宛先・件名・本文の欄をこの拡張が自分で描き、送るときは画面が
// 「PDFを作る → 送る → 発注済みにする」の3段を順に呼んでいました（order_send.go の旧 orderSendFormHTML・
// app.js の旧 sendMail）。いまは**メールを書く欄は1つの部品**（`ext/comm/mail/assets/mail-compose.js`）で、ここは
// その用件の中身だけを渡します（開発方針 §0「口はこちら、言葉は拡張から」・`comm.RegisterSendPurpose`）:
//
//	初期値   … 宛先の候補（連絡帳の仕入先）・件名（部材発注書番号入り）・本文（メールの署名入り）・
//	           外注加工の資料（印を付けて並べる）・連絡帳に宛先が無い／資料が無いの一言
//	送る直前 … ページに表示している部材発注書のPDFを添える（まだ無いときだけ作って表示する・`makeOrderPDF`）
//	送れた後 … 取消でない行を発注済みに・部材発注日を送った日に・送ったPDFを記録の節へ（`recordOrderSent`）
//
// ⚠ **3段の順と「送れたのに印が付かなかった」の扱いは変えていません**——印を付けられなかったときは
// 送れたことと一緒に理由を返し（`after_error`）、人が行ごとの「✓ 発注済」を押します。
// ⚠ 2026-10-14 まで、送る直前に PDF を毎回作り直していました。いまは表示中の最新のPDFを送ります
// （【要求】製造 §8「部材発注書を送信」・C4〔09-24 決定〕——order_pdf_sent.go）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"w-cms/ext/comm"
	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// OrderMailPurpose は送る欄の用件の名前です（下書きの `下書き` タグの値にもなる）。
const OrderMailPurpose = "部材発注書"

func init() {
	comm.RegisterSendPurpose(OrderMailPurpose, comm.SendPurpose{
		Defaults:  orderMailDefaults,
		Prepare:   prepareOrderMail,
		AfterSent: afterOrderMail,
		NeedsPage: true,
	})
}

// orderMailDefaults は部材発注書のメールの初期値です。
func orderMailDefaults(user *auth.User, pageID string) (comm.ComposeDraft, error) {
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		return comm.ComposeDraft{}, errors.New("部材発注書のページを読めません: " + err.Error())
	}
	head, rows, _, err := readOrderDoc(body)
	if err != nil {
		return comm.ComposeDraft{}, err
	}
	supplier := strings.TrimSpace(head[SupplierTag])
	d := comm.ComposeDraft{
		Purpose: OrderMailPurpose, PageID: pageID,
		To:      supplierAddresses(user, supplier),
		// 件名と本文の宛名は正式名称（2026-10-23・宛先のアドレスを引くのは短い名前のまま）。
		Subject: orderMailSubject(pageID, contacts.OfficialName(user, supplier)),
		Body:    orderMailBody(head, contacts.OfficialName(user, supplier), pageID),
		// ⚠ **送るのはページに表示している PDF**（2026-10-14・【要求】製造 §8「部材発注書を送信」——送る直前に
		//    作り直さない）。まだ無いときだけ送るときに作る（下書きを開いただけで添付を増やさない）。
		SendNote: "印の付いた部材発注書のPDFは、このページに表示しているものを添えます（作り直しません——明細や備考を直したら、" +
			"先に「PDF作成」を押してください。まだ無ければ送るときに作ります）。" +
			"送れたら取消でない行を発注済みにし、送ったPDFをこのページの「" + orderSentHeading + "」に記録します。",
		// 添えるPDFを添付の欄に印つきで並べる（2026-10-03・見積書と同じ）。
		Generated: orderMailFileName(pageID),
		Reload:    true,
	}
	if body, err := cms.ReadPageBody(pageID); err == nil {
		if _, _, shown := shownOrderPDF(user, pageID, body); shown {
			d.GeneratedNote = "このページに表示しているPDF"
		}
	}
	noteNoSupplierAddress(&d, supplier)
	// 外注加工の資料——取消していない行の「資料 <番号>」から。
	attachLineDocs(&d, user, rows)
	return d, nil
}

// noteNoSupplierAddress は、仕入先の連絡先が連絡帳から引けなかったら送る欄に注意を添えます（部材発注書・部材見積依頼書）。
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
// （人が外せる）。資料が無い・読めない行は一言を添えます（部材発注書・部材見積依頼書——2026-10-09 に写しを寄せた）。
func attachLineDocs(d *comm.ComposeDraft, user *auth.User, rows []map[string]string) {
	docs, notes := orderDocs(user, rows)
	for _, doc := range docs {
		d.Attachments = append(d.Attachments, comm.ComposeAttachment{
			PageID: doc.PageID, File: doc.File, Name: doc.Name, Checked: true})
	}
	d.Notes = append(d.Notes, notes...)
}

// prepareOrderMail は、送るときに添える部材発注書のPDFを返します——**ページに表示している PDF**です
// （2026-10-14・【要求】製造 §8「部材発注書を送信」:「送るPDFは、発注書ページに表示している最新のPDFです
// （送る直前に作り直しません）」）。まだ表示している PDF が無いときだけ、ここで作って表示します。
//
// ⚠ **関門は `/api/order-pdf` と同じ**です（write 権限・テンプレートの外・誰も編集していない——送れたあとに
// 発注済みの印と記録を本文へ書くため）。断るときは応答を書いて false（メールは1通も出ない）。
// 「誰も編集していない」は、2026-10-12 からは道具（`/api/lock`）が握っていないことだけを見ます。
func prepareOrderMail(w http.ResponseWriter, r *http.Request, pageID string) ([]comm.ComposeAttachment, bool) {
	user := auth.CurrentUser(r)
	if !requireWritableIdle(w, r, pageID) {
		return nil, false
	}
	// ⚠ **送るときの名前は日時を外します**——相手には保存名の日時は意味がなく、件名と揃っていたほうが
	// 探しやすい（09-22 の画面の送り方と同じ名前）。
	if body, err := cms.ReadPageBody(pageID); err == nil {
		if stored, _, shown := shownOrderPDF(user, pageID, body); shown {
			markOrderSending(pageID)
			return []comm.ComposeAttachment{{PageID: pageID, File: stored, Name: orderMailFileName(pageID), Checked: true}}, true
		}
	}
	made, ok := makeOrderPDF(w, user, pageID)
	if !ok {
		return nil, false
	}
	markOrderSending(pageID)
	return []comm.ComposeAttachment{{PageID: pageID, File: made.File, Name: orderMailFileName(pageID), Checked: true}}, true
}

// orderSending は、送っている最中の部材発注書です（ページ → 送り始めた時刻）。そのあいだ「PDF作成」は上書きせずに
// 新しい版を作る——送っている PDF を上書きすると、残した PDF と相手に届いた PDF が違った（2026-10-22 の見直し）。
// 送れたら外す（afterOrderMail）。送れなかったときは、orderSendingFor で消える。
var orderSending sync.Map

// orderSendingFor は、送り始めてから送れたか分かるまでの、長くて十分な時間です。
const orderSendingFor = 10 * time.Minute

func markOrderSending(pageID string) { orderSending.Store(pageID, time.Now()) }

// isOrderSending は、いま送っている最中かです。
func isOrderSending(pageID string) bool {
	v, ok := orderSending.Load(pageID)
	return ok && time.Since(v.(time.Time)) < orderSendingFor
}

// afterOrderMail は送れたあとに、取消でない行を発注済みにして部材発注日を送った日にし、送った PDF を記録の節
// （「送った部材発注書」・order_pdf_sent.go）へ1行足します。送った PDF は、送信の控え（recordID）が添えたファイルの
// うち、このページの添付です（控えが無ければ表示中の PDF とみなします）。
//
// ⚠ **ここはもう応答を書けません**（メールは出たあと）——書けない・編集中なら理由を返し、送る欄が
// 「送りましたが、発注済みの印を付けられませんでした」と人に伝えます。
func afterOrderMail(user *auth.User, pageID, recordID string) error {
	defer orderSending.Delete(pageID)
	n, err := strconv.Atoi(pageID)
	if err != nil {
		return errors.New("部材発注書のページIDが不正です")
	}
	if !page.GetPerms(n).CanWrite(user) || cms.IsTemplateArea(pageID) {
		return errors.New("発注済みの印を付けられませんでした（このページを書き換える権限がありません）。行ごとの「✓ 発注済」を押してください。")
	}
	changed := 0
	sentOn := time.Now().Format("2006-01-02")
	files := mailedOrderFiles(user, pageID, recordID)
	if err := cms.RewriteBodyUnlessHeld(pageID, user.Username, func(cur string) string {
		out, c := recordOrderSent(cur, user, pageID, sentOn, sendByMail, recordID, files)
		changed = c
		return out
	}); err != nil {
		if held := (*cms.HeldError)(nil); errors.As(err, &held) {
			return errors.New("発注済みの印を付けられませんでした（" + held.Error() + "）。編集が終わったら行ごとの「✓ 発注済」を押してください。")
		}
		return errors.New("発注済みの印を付けられませんでした: " + err.Error() + "　行ごとの「✓ 発注済」を押してください。")
	}
	auth.Audit(user.Username, "our-order.sent", pageID+" "+sendByMail+" "+strconv.Itoa(changed)+"行")
	return nil
}
