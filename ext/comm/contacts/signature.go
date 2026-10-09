package contacts

// ─────────────────────────────────────────────────────────────────────────
// 署名——連絡帳の人のページに書いた「○○の署名」の見出しの中身（2026-09-22・2026-09-30 に東邦の拡張から移した・
// 2026-10-02 からふつうの見出しの下も読む）
//
// 利用者（2026-09-22）:「連絡帳には自社の担当者のページがありますから、そこに**発注書用の署名や
// メール用の署名**を書いておけばよいのでは？」。
//
// ⚠ **署名は「項目」ではなく「文面」です**（利用者決定）——節に自由文で書き、行の並びとして読みます。
// だから索引には入らず（「タグと表だけがDBに入る」）、読むときは本文を直に切り出します。
//
// ⚠ **2026-09-30 にここへ移しました**——メールの署名は返信にも新しいメールにも要る**共通の道具**で、
// 発注書（東邦の業務）だけの持ち物ではないためです（開発方針 §0「共通に使えるものを拡張に閉じ込めない」）。
// 発注書の署名の見出し（`発注書の署名`）と、紙に刷る差出人の決め方は東邦の拡張に残ります。
// ─────────────────────────────────────────────────────────────────────────

import (
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// MailSignatureHeading はメールの署名を置く節の見出しです（⚠ 見出しの表示文字が鍵）。
const MailSignatureHeading = "メールの署名"

// SignatureOf はページ本文から、その見出しの節の中身を行として返します。
//
// ⚠ **読めるかは呼ぶ側が見ます**（この関数は本文を読むだけ）。
func SignatureOf(pageID int, heading string) []string {
	body, err := cms.ReadPageBody(page.FormatID(pageID))
	if err != nil {
		return nil
	}
	return SignatureLines(body, heading)
}

// SignatureLines は本文から、その見出しの中身を行にして返します（無ければ nil）。
//
// 読む形は2つ——**見出しの節**（`<section><h2>発注書の署名</h2><p>…</p></section>`）と、
// **ふつうの見出し**（`<h2>発注書の署名</h2><p>…</p>`——次の同じか上の段の見出しまで）。
// ⚠ **ふつうの見出しも読みます**（2026-10-02）——利用者がエディタの「見出し2」で署名を書いたら効かなかった。
// 節の形しか読んでいませんでしたが、エディタで見出しを打てば素の `<h2>` になるので、普通に書くとこちらです。
//
// ⚠ **`<br>` も行の区切りです**——署名を1つの段落に改行で書く人が居ます。
// ⚠ **見出しそのものは返しません**（紙やメールに「メールの署名」とは書かない）。
func SignatureLines(bodyHTML, heading string) []string {
	nodes, err := htmldoc.ParseFragment(bodyHTML)
	if err != nil {
		return nil
	}
	// ⚠ 断片の頭のノードは兄弟を持ちません（ParseFragment が切り離す）——見出しから次の段落へ進めるよう、
	// 仮の親にまとめます。
	root := &html.Node{Type: html.ElementNode, Data: "body"}
	for _, n := range nodes {
		root.AppendChild(n)
	}
	var found []string
	cms.WalkElements(root, func(n *html.Node) {
		if found != nil {
			return
		}
		if n.Data == "section" {
			if sectionHeading(n) == heading {
				found = sectionTextLines(n)
			}
			return
		}
		if headingLevel(n) < 2 || isSectionHead(n) {
			return // 節の見出しは上の枝が読む
		}
		if strings.TrimSpace(cms.TextWithBreaks(n)) == heading {
			found = linesAfterHeading(n)
		}
	})
	return found
}

// headingLevel は h1〜h6 の段（1〜6）を返します（見出しでなければ 0）。
func headingLevel(n *html.Node) int {
	if n.Type != html.ElementNode || len(n.Data) != 2 || n.Data[0] != 'h' || n.Data[1] < '1' || n.Data[1] > '6' {
		return 0
	}
	return int(n.Data[1] - '0')
}

// isSectionHead は、その見出しが節の直下の（節の名前になる）見出しかを返します。
func isSectionHead(h *html.Node) bool {
	return h.Parent != nil && h.Parent.Data == "section" && firstHeading(h.Parent) == h
}

// linesAfterHeading は、ふつうの見出しの後ろの兄弟を、次の同じか上の段の見出しまで行にします。
//
// ⚠ **節（`<section>`）の手前でも止めます**——ファイル表示や見出しの節は別のもので、署名の続きではありません。
// 下の段の見出しは書きません（節の読み方 `sectionTextLines` と同じ）。
func linesAfterHeading(h *html.Node) []string {
	level := headingLevel(h)
	var out []string
	add := func(s string) {
		for _, ln := range strings.Split(s, "\n") {
			if ln = strings.TrimSpace(ln); ln != "" {
				out = append(out, ln)
			}
		}
	}
	for c := h.NextSibling; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			add(c.Data)
			continue
		}
		if c.Type != html.ElementNode {
			continue
		}
		if lv := headingLevel(c); lv > 0 {
			if lv <= level {
				break
			}
			continue
		}
		if c.Data == "section" {
			break
		}
		add(cms.TextWithBreaks(c))
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// MySignature は「いま操作している人」の署名を返します（無ければ nil）。
//
// ⚠ **ログイン名と同じ題の人のページ**です（2026-09-22 利用者:「ログイン名と同じ連絡帳ページがあれば、
// その人が差出人の可能性が高い」）。当たらないのは異常ではありません（ログイン名が `a` なら当たらない）
// ——そのときは署名なしで出し、人が書き足します。⚠ **読める人のページだけ**。
// ⚠ **畳んで比べます**（`NormalizeText`・全角半角と前後の空白の揺れ）。`NormalizeCode` は使いません
// ——人の名前から長音を落とすと別人になります。
func MySignature(user *auth.User, heading string) []string {
	if user == nil {
		return nil
	}
	want := cms.NormalizeText(user.Username)
	if want == "" {
		return nil
	}
	boxID, ok := contactsBoxInt()
	if !ok {
		return nil
	}
	// 連絡帳 ／ 組織 ／ 人 の2段だけ歩きます（⚠ 先に読み切ってから絞る——`cms.ChildPages`）。
	orgs, err := cms.ChildPages(database.DB, boxID)
	if err != nil {
		return nil
	}
	for _, org := range orgs {
		if !page.CanView(user, org.ID) {
			continue
		}
		people, err := cms.ChildPages(database.DB, org.ID)
		if err != nil {
			continue
		}
		for _, p := range people {
			if cms.NormalizeText(p.Title) != want || !page.CanView(user, p.ID) {
				continue
			}
			if lines := SignatureOf(p.ID, heading); len(lines) > 0 {
				return lines
			}
		}
	}
	return nil
}

// sectionHeading は節の直下の見出し（h2〜h6）の文字を返します。
//
// ⚠ **直下だけ**を見ます——入れ子の節の見出しを拾うと、別の節の中身を署名として書くことになります。
func sectionHeading(section *html.Node) string {
	if h := firstHeading(section); h != nil {
		return strings.TrimSpace(cms.TextWithBreaks(h))
	}
	return ""
}

// firstHeading は節の直下の最初の見出し（h2〜h6）を返します（無ければ nil）。
func firstHeading(section *html.Node) *html.Node {
	for c := section.FirstChild; c != nil; c = c.NextSibling {
		if headingLevel(c) >= 2 {
			return c
		}
	}
	return nil
}

// sectionTextLines は節の中身を行の並びにします（見出しは外す）。
func sectionTextLines(section *html.Node) []string {
	var out []string
	add := func(s string) {
		for _, ln := range strings.Split(s, "\n") {
			if ln = strings.TrimSpace(ln); ln != "" {
				out = append(out, ln)
			}
		}
	}
	for c := section.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.TextNode {
			add(c.Data)
			continue
		}
		if c.Type != html.ElementNode {
			continue
		}
		switch c.Data {
		case "h2", "h3", "h4", "h5", "h6":
			continue // 見出しは書かない
		default:
			add(cms.TextWithBreaks(c))
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}
