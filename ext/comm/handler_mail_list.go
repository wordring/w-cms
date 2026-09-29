package comm

// ─────────────────────────────────────────────────────────────────────────
// メールの一覧（2026-09-29）
//
// 利用者:「過去のメールを一覧で見る方法を作れませんか？受信送信両方、そしてIn reply toによる返信の連鎖も
// 一覧性があるように」。
//
// **新しい記録は要りません。** 通信記録（チャネル＝メール）のタグ——向き・受信日時／送信日時・差出人・宛先・
// メッセージID・返信元メッセージID（`In-Reply-To`）・添付・対応——を索引から**まとめて**読み、1件ずつ返します。
// 返信の鎖（スレッド）は画面が組みます（メッセージID と 返信元メッセージID を突き合わせるだけ——`/api/thread` が
// 1件ずつ辿るのと同じ鎖を、全部まとめて）。絞り込みも画面でします（千通ほどなら十分速い）。
//
//   - **読めるものだけ**（`page.CanView`）。読めない記録は黙って落ちる（見せ分けC案）。
//   - 索引は**先に読み切ってから絞る**（`cms.TagRowsNamed`——行を読みながら別のクエリを投げない）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/http"
	"sort"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// MailListItem はメールの一覧の1件です。
type MailListItem struct {
	PageID      string   `json:"page_id"`
	Title       string   `json:"title"`
	Direction   string   `json:"direction"` // 受信／送信
	When        string   `json:"when"`      // 受信日時か送信日時（向きに応じて片方）
	From        []string `json:"from"`
	To          []string `json:"to"`
	Cc          []string `json:"cc"`
	MessageID   string   `json:"message_id"`
	InReplyTo   string   `json:"in_reply_to"` // 返信元メッセージID（スレッドの親）
	Attachments string   `json:"attachments"` // 添付の数（無ければ空）
	Handled     string   `json:"handled"`     // 対応（済／不要）。空は未処理
}

// MailList は user が読めるメールの記録を、新しい順に返します。
func MailList(user *auth.User) ([]MailListItem, error) {
	rows, err := cms.TagRowsNamed(database.DB, ChannelTag, DirectionTag, ReceivedAtTag, SentAtTag,
		FromTag, ToTag, CcTag, MessageIDTag, InReplyToTag, AttachmentCountTag, HandledTag)
	if err != nil {
		return nil, err
	}
	byPage := map[int]*MailListItem{}
	isMail := map[int]bool{}
	for _, r := range rows {
		it := byPage[r.PageID]
		if it == nil {
			it = &MailListItem{From: []string{}, To: []string{}, Cc: []string{}}
			byPage[r.PageID] = it
		}
		switch r.Name {
		case ChannelTag:
			if r.Value == ChannelMail {
				isMail[r.PageID] = true
			}
		case DirectionTag:
			it.Direction = r.Value
		case ReceivedAtTag, SentAtTag:
			if it.When == "" {
				it.When = r.Value
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
		case AttachmentCountTag:
			it.Attachments = r.Value
		case HandledTag:
			it.Handled = r.Value
		}
	}
	// 読み切ったあとで絞る（読めるか・題）。
	out := make([]MailListItem, 0, len(isMail))
	for id := range isMail {
		if !page.CanView(user, id) {
			continue
		}
		it := byPage[id]
		it.PageID = page.FormatID(id)
		it.Title = cms.PageTitleByID(id)
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

// MailListAPIHandler は GET /api/mails です（読めるメールの記録を全部・新しい順）。
func MailListAPIHandler(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "application/json")
	if r.Method != http.MethodGet {
		cms.JSONFail(w, http.StatusMethodNotAllowed, "Method not allowed")
		return
	}
	user := auth.CurrentUser(r)
	if user == nil {
		cms.JSONFail(w, http.StatusForbidden, "ログインが必要です")
		return
	}
	list, err := MailList(user)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "メールの一覧を読めません: "+err.Error())
		return
	}
	cms.WriteJSON(w, map[string]any{"success": true, "mails": list})
}
