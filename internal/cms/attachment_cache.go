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
// 2026-10-27 からは、**書くための本文の口（素の GET /api/load・/api/sync の書き出し）にも添えます**。添えないと、パソコンの既定の
// 書ける形では写真が印の無い住所で描き直され、初めて開くと同じ写真を2回受け取り、開くたびに1枚ずつ問い合わせていました
// （【考察】キャッシュ制御 §3.1・案A）。添えた印は、保存の手前のサニタイズ（Sanitize）と画面の書き出し（app.js の serializeAttrs）
// の両方で外すので、本文には書き込まれません（StripAttachmentVersions）。
//
// 利用者:「【考察】キャッシュ制御の勧める順で作ってください。公開の添付は10分にとどめる（勧め）でお願いします。」
// ─────────────────────────────────────────────────────────────────────────

import (
	"os"
	"regexp"
	"strings"

	"w-cms/internal/cms/page"
)

// bodyImgAttachmentRe は本文の写真のうち、添付のきれいな住所（`?`・`#` の無いもの）を指すものです。
// 本文はサニタイズで書き直したあとなので、属性は二重引用符で囲まれています。
var bodyImgAttachmentRe = regexp.MustCompile(`(<img\b[^>]*?\ssrc=")/([0-9]{6})/([^"/?#&]+)"`)

// bodyImgVersionRe は、本文の写真のうち、添付のきれいな住所に版の印（と表示用の印 `view=1`——photo_view.go）だけを
// 添えたものです（StripAttachmentVersions が外す）。印の形は page.AttachmentVersion（36進の数字と小文字）。
// ほかの問い合わせが付いた住所には触りません。書き出しでは `&` が `&amp;` になるので、どちらも受けます。
var bodyImgVersionRe = regexp.MustCompile(`(<img\b[^>]*?\ssrc="/[0-9]{6}/[^"/?#&]+)\?(?:view=1&(?:amp;)?)?v=[0-9a-z]+"`)

// StripAttachmentVersions は、本文の写真の住所から版の印（`?v=`・`?view=1&v=`）を外します。印の無い本文は1バイトも変えません。
func StripAttachmentVersions(body string) string {
	if !strings.Contains(body, "v=") {
		return body
	}
	return bodyImgVersionRe.ReplaceAllString(body, `$1"`)
}

// RenderAttachmentVersions は本文の写真の住所に `?v=<版の印>` を添えます。JPEG の写真は、表示用の写し（photo_view.go）を
// 指す `?view=1&v=<版の印>` にし、設定 `photo_view` の `lazy` なら `loading="lazy"`（画面に近づくまで読まない）も添えます
// （どちらも設定 photo_view があるときだけ。loading はサニタイズが許さない属性なので、本文には残らない）。
// 読めない添付（無い・消えた）はそのまま。
func RenderAttachmentVersions(body string) string {
	conf := PhotoViewConfig()
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
		q := "?v="
		if conf != nil && isViewablePhoto(sub[3]) {
			q = "?view=1&amp;v="
		}
		head := sub[1]
		if conf != nil && conf.Lazy {
			head = strings.Replace(head, "<img", `<img loading="lazy"`, 1)
		}
		return head + "/" + sub[2] + "/" + sub[3] + q + page.AttachmentVersion(fi) + `"`
	})
}
