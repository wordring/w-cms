package comm

// ─────────────────────────────────────────────────────────────────────────
// アドレス→連絡帳の名前（2026-09-30）
//
// 利用者:「通信箱のトップページの未処理表ですが、アドレスから連絡帳の名前が引ける場合、名前だけの表示にして、
// 引けないアドレスも短縮表示にして、表の幅を節約してください。タイトルもあまりにも長いものは短縮表示してください」。
//
// ⚠ **連絡帳は通信の拡張を読み込む側**なので（`ext/comm/contacts` → `ext/comm`）、通信から連絡帳を直には
// 呼べません。口をここに置き、中身は連絡帳の拡張が登録します（`RegisterMailer` と同じ形）。
// ⚠ コアの `cms.RegisterContactResolver` は**利用者を見ません**（本文のリンクを描く口・踏んだ先で関門が
// 判定する）。一覧に**名前そのもの**を出すこちらは、読める連絡先だけを答えます。
// ─────────────────────────────────────────────────────────────────────────

import (
	"net/mail"
	"strings"

	"w-cms/internal/auth"
)

// ContactNamer はアドレスから連絡帳の名前（そのアドレスを持つページの題）を答えます。
type ContactNamer func(user *auth.User, addr string) (name string, ok bool)

var contactNamer ContactNamer

// RegisterContactNamer は答える係を登録します（連絡帳の拡張の init から・二重登録は落とす）。
func RegisterContactNamer(f ContactNamer) {
	if contactNamer != nil {
		panic("comm.RegisterContactNamer: 二重に登録しています")
	}
	contactNamer = f
}

// splitMailbox は `名前 <アドレス>` を名前とアドレスに分けます（読めなければ全体をアドレスとみなす）。
func splitMailbox(v string) (name, addr string) {
	v = strings.TrimSpace(v)
	if a, err := mail.ParseAddress(v); err == nil {
		return strings.TrimSpace(a.Name), strings.TrimSpace(a.Address)
	}
	if i := strings.LastIndex(v, "<"); i >= 0 {
		if j := strings.Index(v[i:], ">"); j > 0 {
			return strings.Trim(strings.TrimSpace(v[:i]), `"`), strings.TrimSpace(v[i+1 : i+j])
		}
	}
	return "", v
}

// shortPartner は一覧に出す相手の短い名前です——連絡帳の名前 → メールに書かれた名前 → アドレスの順で1つ選び、
// max 文字を超えたら「…」で切ります（元の値は呼ぶ側が title に置く）。names は同じ描画の中での控え。
func shortPartner(user *auth.User, from string, max int, names map[string]string) string {
	name, addr := splitMailbox(from)
	label := ""
	if addr != "" && contactNamer != nil {
		key := strings.ToLower(addr)
		if n, seen := names[key]; seen {
			label = n
		} else {
			if n, ok := contactNamer(user, addr); ok {
				label = strings.TrimSpace(n)
			}
			names[key] = label
		}
	}
	if label == "" {
		label = name
	}
	if label == "" {
		label = addr
	}
	return ellipsize(label, max)
}

// ellipsize は s を max 文字（ルーン）までにし、切ったら末尾を「…」にします。
func ellipsize(s string, max int) string {
	r := []rune(s)
	if max <= 1 || len(r) <= max {
		return s
	}
	return string(r[:max-1]) + "…"
}
