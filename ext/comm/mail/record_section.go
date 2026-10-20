package mail

// 記録の箱の「✉️ 返信」（2026-10-20・ext/comm/record_box.go の節）——メールの拡張の持ち物なので、メールを外した組では出ません。
// 下書きのページ（`下書き` のタグ）では、返信の代わりに、下書きを開いた送る欄の置き場を出します（開いたらすぐ直して送れる）。
// 送る欄そのものは部品（ext/comm/mail/assets/mail-compose.js）が描き、開く配線は ext/comm/mail/assets/mail.js。

import (
	"w-cms/ext/comm"
	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

func init() {
	comm.RegisterRecordSection(comm.RecordSection{Order: 20, Render: replySectionHTML})
}

func replySectionHTML(user *auth.User, pageIDInt int, draft bool) string {
	id := page.FormatID(pageIDInt)
	if draft {
		return `<div class="mail-compose-host" data-compose-draft="` + id + `"></div>`
	}
	return `<button type="button" class="mail-reply-open" data-page="` + id + `">✉️ 返信</button><div class="mail-compose-host"></div>`
}
