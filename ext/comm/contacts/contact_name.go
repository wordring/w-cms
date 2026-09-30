package contacts

// アドレス→連絡帳の名前を、通信の拡張の口へ預けます（2026-09-30・通信箱の未処理の一覧の相手の欄）。
//
// ⚠ **読めるページの名前だけ**を答えます（`ContactPageForAddress` に利用者を渡す）——一覧に出すのは名前
// そのものなので、本文のリンク（`cms.RegisterContactResolver`・利用者を見ない）とは規律が違います。

import (
	"w-cms/ext/comm"
	"w-cms/internal/auth"
)

func init() {
	comm.RegisterContactNamer(func(user *auth.User, addr string) (string, bool) {
		if user == nil {
			return "", false // 誰が見ているか分からないなら名前は出さない
		}
		_, title, ok := ContactPageForAddress(user, addr)
		return title, ok
	})
}
