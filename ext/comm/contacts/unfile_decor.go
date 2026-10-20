package contacts

// 「未分類へ戻す」の飾り（2026-10-20・docs/考察/【考察】画面の JS のコアと拡張.md §4.5）——`メールアドレス` のタグの値の欄に
// 「未分類へ戻す」を足します（コアのタグの飾りの口・internal/cms/tag_decor.go）。それまでは連絡帳の JS がタグを探して足していました。
// 押すとそのタグだけ外れ、未登録の一覧へ戻ります（POST /api/contacts/unfile・ページは消さない）。配線は ext/comm/contacts/assets/contacts.js。

import (
	stdhtml "html"

	"w-cms/internal/cms"
)

func init() {
	cms.RegisterTagDecorator(EmailTag, unfileDecoration)
}

func unfileDecoration(ctx *cms.MirrorContext, value string) string {
	return `<button type="button" class="vocab-chrome chip-btn contact-unfile" contenteditable="false"` +
		` data-addr="` + stdhtml.EscapeString(value) + `"` +
		` title="` + stdhtml.EscapeString(value+" をこのページから外し、「未登録の連絡先」へ戻します（ページは消しません）") + `">未分類へ戻す</button>`
}
