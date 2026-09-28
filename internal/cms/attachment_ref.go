package cms

// ─────────────────────────────────────────────────────────────────────────
// 本文のある部分が指している添付を集める（2026-09-28）
//
// 「このブロックの中のファイルを、メールに付ける・紙に綴じる」ための共通の道具です。
// 最初の使い手は東邦の拡張（外注加工ごとの資料のブロック——利用者:「加工製品のページに、
// 外注加工ごとに資料のブロック（開いたり閉じたりできる）を用意して、そこに保存したファイルを
// メールやFAX、印刷等に追加できるようにしてはどうでしょう？」）ですが、**どの業務でも
// 「ここにあるファイルを渡す」は起きる**のでコアに置きます（開発方針 §0）。
//
// 本文がファイルを指す形は3つあります——どれも**ページIDと添付IDの組**に畳めます:
//
//	<section data-type="file-view" data-ref="000223-fhea"></section>   ファイル表示の印
//	<a href="/000235/abcd.pdf" download="図面.pdf">図面.pdf</a>          本文へ落とした 📎
//	<img src="/000235/abcd.jpg">                                        画像
//
// ⚠ **指す先は別のページでもよい**（顧客の図面は通信記録のページに届いている）。だから
// 読めるかどうかは**指されたページ**で確かめます（`AttachmentOfRef`）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"os"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// FileRef は本文が指している添付1つです。
type FileRef struct {
	PageID string // 6桁のページID
	ID     string // 添付ID（保存名から拡張子を除いたもの）
}

// attachmentPathRe は添付の配信URL（`page.AttachmentURLFor` が組む形）です。
var attachmentPathRe = regexp.MustCompile(`^/([0-9]{6})/([0-9A-Za-z]+)\.[0-9A-Za-z]+$`)

// FileRefsIn は n の中（n を含む）で添付を指しているものを、出てきた順に返します（重複は1つ）。
func FileRefsIn(n *html.Node) []FileRef {
	var out []FileRef
	seen := map[FileRef]bool{}
	add := func(r FileRef, ok bool) {
		if ok && !seen[r] {
			seen[r] = true
			out = append(out, r)
		}
	}
	WalkElements(n, func(el *html.Node) {
		switch el.Data {
		case "section":
			if VocabTypeOf(el) == FileViewType {
				p, id, ok := parseRefValue(Attr(el, FileRefAttr))
				add(FileRef{PageID: p, ID: id}, ok && id != "")
			}
		case "a":
			add(fileRefOfURL(Attr(el, "href")))
		case "img":
			add(fileRefOfURL(Attr(el, "src")))
		}
	})
	return out
}

// fileRefOfURL は添付の配信URLを参照へ畳みます（添付でなければ ok=false）。
func fileRefOfURL(u string) (FileRef, bool) {
	m := attachmentPathRe.FindStringSubmatch(strings.TrimSpace(u))
	if m == nil {
		return FileRef{}, false
	}
	return FileRef{PageID: m[1], ID: m[2]}, true
}

// AttachmentOfRef は参照の指す添付の保存名（`<id>.<拡張子>`）と、届いたときの名前を返します。
//
// **読めない相手には ok=false**——「読めない」と「無い」は区別しません（匿名の404統一と
// 同じ規律）。読めるかは**指されたページ**で見ます。
func AttachmentOfRef(user *auth.User, ref FileRef) (stored, display string, ok bool) {
	idInt, err := strconv.Atoi(ref.PageID)
	if err != nil || !page.CanView(user, idInt) {
		return "", "", false
	}
	stored, ok = attachmentFileOf(ref.PageID, ref.ID)
	if !ok {
		return "", "", false
	}
	return stored, AttachmentDisplayName(ref.PageID, stored), true
}

// attachmentFileOf は添付IDの保存名を探します（`files/` の中を名前だけ見る）。
func attachmentFileOf(pageID, attachID string) (string, bool) {
	entries, err := os.ReadDir(page.AttachmentDir(pageID))
	if err != nil {
		return "", false
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		n := e.Name()
		if IsAttachmentMetaFile(n) {
			continue // 目録（meta.json）は添付ではない
		}
		if dot := strings.LastIndexByte(n, '.'); dot > 0 && n[:dot] == attachID {
			return n, true
		}
	}
	return "", false
}
