package comm

// 届いた記録・送った記録を消させない（2026-10-28・【考察】コードの矛盾 §7.2）。
//
// 通信箱の下の、向きが「受信」「送信」の記録（メール・FAX・電話）は、届いた事実・送った事実の写しです。
// 管理者でない人は、ごみ箱へ移すことも、通信箱の外へ動かすこともできません（外へ動かしてから消せば、消す口の確かめを
// すり抜けるため）。要らない記録は、いままでどおり `対応：不要` で片付けます。
// 下書き・向きの無い手書きのメモ・年月の置き場は、書ける人ならいままでどおり消せます。
//
// > 2026-10-28 利用者（届いた事実の範囲の記録も、書ける人は消せる件に）:「管理者だけ（勧め）」

import (
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/database"
)

func init() {
	cms.RegisterRemovalGuard(guardRecordRemoval)
}

// guardRecordRemoval は、管理者でない人が届いた記録・送った記録を消す・通信箱の外へ動かすのを断ります。
func guardRecordRemoval(user *auth.User, pageID, newParent int) string {
	if user != nil && user.IsAdmin {
		return ""
	}
	if !InMailBox(pageID) {
		return ""
	}
	dir := cms.PageTagValue(database.DB, pageID, DirectionTag)
	if dir != DirectionIn && dir != DirectionOut {
		return ""
	}
	if newParent < 0 {
		return "届いた記録・送った記録（通信箱の「" + dir + "」）は、管理者だけが消せます。要らない記録は「" + HandledTag + "：" + HandledNotNeeded + "」にしてください。"
	}
	if root, ok := MailBoxPageID(); ok && (atoiPage(root) == newParent || InMailBox(newParent)) {
		return "" // 通信箱の中で動かすのはよい
	}
	return "届いた記録・送った記録（通信箱の「" + dir + "」）を通信箱の外へ動かせるのは、管理者だけです。"
}
