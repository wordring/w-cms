package cms

// ─────────────────────────────────────────────────────────────────────────
// 本文の写真の住所に、版の印を添える（2026-10-27）
//
// 利用者:「単なるトンネルではなく、画像のキャッシュなどはできますか」→「作ってください」。
//
// 添付の配信（page.setAttachmentCache）は、住所の `?v=` が今の版の印と合えば、その人のブラウザに確かめずに使わせます。
// 本文の写真（`<img src="/<ページ番号>/<保存名>">`）にも、**閲覧の描き方のときだけ**印を添えます。一度見た写真は、
// 次に開いたときほとんど通信なしで出ます（社外から VPS を通して開くときに効く）。上書きすると印が変わるので、
// 古い写真を見せ続けることもありません。
//
// ⚠ **書くための本文の口（素の GET /api/load・/api/sync の書き出し）には添えません**——アンカー（anchor.go）と同じで、
// 添えた住所が本文として保存されてしまうため。もし保存されても害はありません（古い印の住所は「毎回確かめる」で配る）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"os"
	"regexp"

	"w-cms/internal/cms/page"
)

// bodyImgAttachmentRe は本文の写真のうち、添付のきれいな住所（`?`・`#` の無いもの）を指すものです。
// 本文はサニタイズで書き直したあとなので、属性は二重引用符で囲まれています。
var bodyImgAttachmentRe = regexp.MustCompile(`(<img\b[^>]*?\ssrc=")/([0-9]{6})/([^"/?#&]+)"`)

// RenderAttachmentVersions は本文の写真の住所に `?v=<版の印>` を添えます（閲覧の描き方だけ）。
// 読めない添付（無い・消えた）はそのまま。
func RenderAttachmentVersions(body string) string {
	return bodyImgAttachmentRe.ReplaceAllStringFunc(body, func(m string) string {
		sub := bodyImgAttachmentRe.FindStringSubmatch(m)
		p, ok := page.AttachmentPath(sub[2], sub[3])
		if !ok {
			return m
		}
		fi, err := os.Stat(p)
		if err != nil || !fi.Mode().IsRegular() {
			return m
		}
		return sub[1] + "/" + sub[2] + "/" + sub[3] + "?v=" + page.AttachmentVersion(fi) + `"`
	})
}
