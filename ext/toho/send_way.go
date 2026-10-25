package toho

import (
	stdhtml "html"
	"strings"

	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/database"
)

// 送る欄の「送り方」の選択と「▶ 送る」、相手ごとの送り方の勧め（2026-10-26・ext/comm/contacts/send_way.go）。
//
// > 2026-10-26 利用者:「fax、メールなどのメソッドをコンボボックスにして送るボタンにしてはどうでしょう」
//
// 部材発注書・部材見積依頼書・見積書の送る欄は、送り方を選ぶ欄（select）と「▶ 送る」の1組にします。選んだ送り方の
// いままでのボタン（📠 FAX で送る・🤝 手渡した など）は、本文に残したまま隠し、「▶ 送る」がそれを押します（メールなら
// 「メールで送る」の欄を開く——宛先や本文を確かめてから、その欄の送信で送る）。配線は ext/toho/assets/toho.js。
//
// 連絡帳の送り方の使い道は、送り方の欄で最初に選ばれているものだけです（2026-10-26 利用者:「メソッドのコンボボックスが
// デフォルトで選択されているのが設定の使い方です」——メールの欄を開く・畳むなどは変えない）。
// 最初に選ばれているのは、相手の連絡帳の送り方です。部材発注書・部材見積依頼書は仕入先の組織、見積書は見積先担当の
// 人（書いていなければ見積先の組織）。書類ごとの「<書類>の送り方」が先、無ければ総称の「送り方」（2026-10-26 利用者:「書類
// ごとの設定があればそれを使い、総称的な設定しがなければ、それを用いれば良いと思います」）。勧めの無い相手は、1つ目（メール）。

// 書類の名前です（連絡帳の「<書類>の送り方」のタグの頭）。
const (
	orderDocName    = "部材発注書"
	rfqDocName      = "部材見積依頼書"
	estimateDocName = "見積書"
)

// sendWayFor は、書類 doc のページ pageID の相手（partyTag の組織・personTag の人。人の無い書類は空）に勧める送り方です。
func sendWayFor(user *auth.User, pageID int, doc, partyTag, personTag string) contacts.SendWay {
	if user == nil {
		return contacts.SendWay{}
	}
	org := cms.PageTagValue(database.DB, pageID, partyTag)
	person := ""
	if personTag != "" {
		person = cms.PageTagValue(database.DB, pageID, personTag)
	}
	return contacts.PreferredSendWay(user, org, person, doc)
}

// sendWayNoteHTML は、送る欄の頭の一言です（勧めが無ければ空）。
func sendWayNoteHTML(w contacts.SendWay) string {
	if w.Way == "" {
		return ""
	}
	return `<p class="send-way-note">📌 連絡帳の勧め: <strong>` + stdhtml.EscapeString(w.Way) + `</strong>（` +
		stdhtml.EscapeString(strings.TrimSpace(w.From)) + `の「` + stdhtml.EscapeString(w.Tag) + `」）</p>`
}

// sendWayOpt は送り方の選択肢1つです。Way は連絡帳の送り方の値（勧めで選ぶ。記録だけの選択肢は空）、Target は「▶ 送る」が
// 押す（details なら開く）要素の CSS セレクタ（送る欄の中で探す）、Go はそれを選んだときのボタンの文字。
type sendWayOpt struct {
	Way, Label, Target, Go string
}

// 選択肢の部品です（書類ごとに、押す先のセレクタだけが違う）。
func mailWayOpt(target string) sendWayOpt {
	return sendWayOpt{Way: contacts.SendWayMail, Label: "📧 メール", Target: target, Go: "▶ メールを書く"}
}

var faxWayOpt = sendWayOpt{Way: contacts.SendWayFax, Label: "📠 FAX（w-cms から送る）", Target: "[data-fax-purpose]", Go: "▶ 送る"}

// sendWayPickHTML は、送り方を選ぶ欄と「▶ 送る」です。最初に選ばれるのは勧め（way）の送り方、無ければ1つ目。
func sendWayPickHTML(way contacts.SendWay, opts []sendWayOpt) string {
	sel := 0
	for i, o := range opts {
		if way.Way != "" && o.Way == way.Way {
			sel = i
			break
		}
	}
	var b strings.Builder
	b.WriteString(`<select class="send-way-pick" aria-label="送り方">`)
	for i, o := range opts {
		b.WriteString(`<option data-target="` + stdhtml.EscapeString(o.Target) + `" data-go="` + stdhtml.EscapeString(o.Go) + `"`)
		if i == sel {
			b.WriteString(` selected`)
		}
		b.WriteString(`>` + stdhtml.EscapeString(o.Label) + `</option>`)
	}
	b.WriteString(`</select> <button type="button" class="chip-btn send-way-go">` + stdhtml.EscapeString(opts[sel].Go) + `</button> `)
	return b.String()
}
