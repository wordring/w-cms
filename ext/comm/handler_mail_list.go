package comm

// ─────────────────────────────────────────────────────────────────────────
// メールの一覧（2026-09-29）
//
// 利用者:「過去のメールを一覧で見る方法を作れませんか？受信送信両方、そしてIn reply toによる返信の連鎖も
// 一覧性があるように」。
//
// **新しい記録は要りません。** 通信記録（チャネル＝メール）のタグ——向き・受信日時／送信日時・差出人・宛先・
// Message-ID・In-Reply-To・親ページID・添付・対応・下書き——を索引から**まとめて**読み、1件ずつ返します
// （2026-10-09 までのタグの名前は「メッセージID」「返信元メッセージID」）。
// 返信の鎖（スレッド）は画面が組みます（`親ページID` で親をたどるだけ——ThreadOf が
// 1件ずつ辿るのと同じ鎖を、全部まとめて。ext/comm/assets/pages/mails.js）。2026-10-09 までは Message-ID と In-Reply-To を
// 突き合わせていました。絞り込みも画面でします（千通ほどなら十分速い）。
//
//   - 2026-10-29 から、**メールだけでなく通信記録の全部**（FAX・電話・メモも——チャネルのタグを持つページ）を返します。
//     画面は通信箱の見え方「スレッド」（ext/comm/assets/comm-threads.js）で、メール一覧のページ（mails.html）はなくした
//     （利用者:「メール一覧は通信箱に統合して、見え方を選択できるようにしたいです」・【考察】通信箱とメール一覧の統合）。
//     テンプレートの中の記録は出しません。日時の無い記録（メモ）は発信日時、それも無ければページを作った日時。
//   - **読めるものだけ**（`page.CanView`）。読めない記録は黙って落ちる（見せ分けC案）。
//   - 索引は**先に読み切ってから絞る**（`cms.TagRowsNamed`——行を読みながら別のクエリを投げない）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"sort"
	"strings"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// MailListItem はメールの一覧の1件です。
type MailListItem struct {
	PageID      string   `json:"page_id"`
	Title       string   `json:"title"`
	Channel     string   `json:"channel"`   // チャネル（メール・FAX・電話・メモ・2026-10-29）
	Direction   string   `json:"direction"` // 受信／送信
	When        string   `json:"when"`      // 受信日時か送信日時（向きに応じて片方）
	From        []string `json:"from"`
	To          []string `json:"to"`
	Cc          []string `json:"cc"`
	MessageID   string   `json:"message_id"`
	InReplyTo   string   `json:"in_reply_to"` // In-Reply-To ヘッダの写し
	Parent      string   `json:"parent"`      // 親ページID（スレッドの親——画面はこれで木を組む・2026-10-09）
	Attachments string   `json:"attachments"` // 添付の数（無ければ空）
	Handled     string   `json:"handled"`     // 対応（済／不要）。空は未処理
	Draft       string   `json:"draft"`       // 下書きなら用件（返信・新規・部材発注書）。送った記録は空（2026-09-30）
}

// MailList は user が読める通信記録（2026-10-29 からはメールだけでなく全部のチャネル）を、新しい順に返します。
func MailList(user *auth.User) ([]MailListItem, error) {
	rows, err := cms.TagRowsNamed(database.DB, ChannelTag, DirectionTag, ReceivedAtTag, SentAtTag, SentOutAtTag,
		FromTag, ToTag, CcTag, MessageIDTag, InReplyToTag, ParentPageTag, AttachmentCountTag, HandledTag, DraftTag)
	if err != nil {
		return nil, err
	}
	byPage := map[int]*MailListItem{}
	isRecord := map[int]bool{}
	outAt := map[int]string{} // 発信日時（手で書いた記録——受信日時・送信日時が無いとき）
	for _, r := range rows {
		it := byPage[r.PageID]
		if it == nil {
			it = &MailListItem{From: []string{}, To: []string{}, Cc: []string{}}
			byPage[r.PageID] = it
		}
		switch r.Name {
		case ChannelTag:
			if v := strings.TrimSpace(r.Value); v != "" && it.Channel == "" {
				it.Channel = v
				isRecord[r.PageID] = true
			}
		case DirectionTag:
			it.Direction = r.Value
		case ReceivedAtTag, SentAtTag:
			if it.When == "" {
				it.When = r.Value
			}
		case SentOutAtTag:
			if outAt[r.PageID] == "" {
				outAt[r.PageID] = r.Value
			}
		case FromTag:
			it.From = append(it.From, r.Value)
		case ToTag:
			it.To = append(it.To, r.Value)
		case CcTag:
			it.Cc = append(it.Cc, r.Value)
		case MessageIDTag:
			it.MessageID = r.Value
		case InReplyToTag:
			it.InReplyTo = r.Value
		case ParentPageTag:
			it.Parent = r.Value
		case AttachmentCountTag:
			it.Attachments = r.Value
		case HandledTag:
			if r.Value != HandledPending { // 空は未処理（2026-10-19 から未処理もタグの値で持つが、答えの形は変えない）
				it.Handled = r.Value
			}
		case DraftTag:
			it.Draft = r.Value
		}
	}
	// 読み切ったあとで絞る（読めるか・題）。
	out := make([]MailListItem, 0, len(isRecord))
	for id := range isRecord {
		if !page.CanView(user, id) || cms.IsTemplateArea(page.FormatID(id)) {
			continue
		}
		it := byPage[id]
		it.PageID = page.FormatID(id)
		it.Title = cms.PageTitleByID(id)
		if it.When == "" {
			it.When = outAt[id]
		}
		if it.When == "" && it.Draft == "" {
			if meta, ok := page.ReadSidecar(it.PageID); ok {
				it.When = meta.CreatedAt
			}
		}
		out = append(out, *it)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].When != out[j].When {
			return out[i].When > out[j].When // 新しい順（日時は ISO 8601 なので文字の順で並ぶ）
		}
		return out[i].PageID > out[j].PageID
	})
	return out, nil
}

// MailListAPIHandler は GET /api/mails です（読める通信記録を全部・新しい順。2026-10-29 からはメールだけでなく全部のチャネル）。
func MailListAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONGet(w, r)
	if !ok {
		return
	}
	list, err := MailList(user)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "通信の一覧を読めません: "+err.Error())
		return
	}
	cms.WriteJSON(w, map[string]any{"success": true, "mails": list})
}

// ThreadsRedirectAPIHandler は GET /api/comm/threads です——通信箱の見え方「スレッド」へ送ります（2026-10-29）。
// 上のメニューの「✉️ 通信の一覧」と、なくしたメール一覧の古い住所（mails.html が送ってくる）の行き先。通信箱が無ければ 404。
func ThreadsRedirectAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		http.Error(w, "Method not allowed", http.StatusMethodNotAllowed)
		return
	}
	id, ok := MailBoxPageID()
	if !ok {
		http.Error(w, ErrNoMailBox.Error(), http.StatusNotFound)
		return
	}
	http.Redirect(w, r, "/"+id+"?view=threads", http.StatusFound)
}
