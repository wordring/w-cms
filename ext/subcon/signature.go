package subcon

// ─────────────────────────────────────────────────────────────────────────
// 発注書の差出人＝連絡帳の担当者ページに書いた署名（2026-09-22）
//
// ユーザー:「発注書の差出人なんですが、**担当者によって変わるので都度選ぶしかない
// のでは？** …連絡帳には自社の担当者のページがありますから、そこに**発注書用の
// 署名やメール用の署名**を書いておけばよいのでは？」
//
// ⚠ **これは `config/settings.json` の `company` を置き換えます。** 設定は Git 管理で
// 公開リポジトリに入るので、住所も氏名も書けませんでした（`company` はいまも空）。
// `data/` なら外へ出ません。
//
// ── どれが自社かを、機械は見分けません ──────────────────────────────────
//
// ⚠ **`取引：自社` は 2026-09-18 に全廃**したので、手掛かりはありません。そこで
// ユーザーの案（2026-09-22）:「**ログイン名と同じ連絡帳ページがあれば、その人が
// 差出人の可能性が高い**です」。
//
// つまり**機械は初期値を賢くするだけ**で、決めるのは人です——社名の推奨値
// （`contacts.OrgNameForPage`）と同じ流儀。⚠ **候補は署名を持つ人に絞ります**
// ——署名の在ることが、その人が差出人になれる印です（印を別に持たない）。
//
// ── 署名は節に自由文 ────────────────────────────────────────────────────
//
// ⚠ **項目に割りません**（ユーザー決定）。実物の発注書の右上は社名・住所・担当・
// TEL・FAX が並びますが、**人によって行を足したり減らしたり**します。署名は
// 「項目」ではなく「文面」です。
//
// ⚠ **だから索引には入りません**（「タグと表だけがDBに入る」）。読むときは本文を
// 直に切り出します——連絡帳の人は数人なので、これで足ります。
// ─────────────────────────────────────────────────────────────────────────

import (
	"sort"
	"strings"

	"golang.org/x/net/html"

	"w-cms/ext/comm/contacts"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

const (
	// OrderSignatureHeading は発注書の署名を置く節の見出しです。
	//
	// ⚠ **見出しの表示文字が鍵**です（この仕組みの他のすべてと同じ）。
	OrderSignatureHeading = "発注書の署名"

	// MailSignatureHeading はメールの署名を置く節の見出しです。
	//
	// ⚠ **まだ誰も読んでいません**（2026-09-22）。ユーザーが「発注書用の署名や
	// メール用の署名」と並べたので、**置き場だけ先に決めてあります**——返信を作る口
	// （`ext/comm/mail/reply.go`）が使うときに、同じ `signatureLines` で読めます。
	MailSignatureHeading = "メールの署名"

	// OrderSignerTag は発注書ページが持つ「誰が出したか」の参照タグです。
	//
	// ⚠ **`差出人` とは名付けません。** それは通信の持ち物（`comm.FromTag`＝メールの
	// 差出人）で、**索引は名前で引く**ので、同じ語に2つの意味を持たせると混ざります。
	OrderSignerTag = "発注担当"
)

// Signer は発注書の差出人になれる人です。
type Signer struct {
	PageID int      // 連絡帳の人のページ
	Title  string   // その題（＝人の名前）
	Org    string   // 属する組織の題（同姓の人を見分けるため）
	Lines  []string // 署名の行（本文に書いたまま）
}

// Signers は署名を持つ連絡帳のページを集めます（題の順）。
//
// ⚠ **読めるページだけ**です。⚠ **先に読み切ってから絞ります**——行を読みながら
// `page.CanView` を呼ぶと `:memory:` の試験で静かに全部落ちます。
func Signers(user *auth.User) []Signer {
	boxID, ok := contacts.ContactsBoxPageID()
	if !ok {
		return nil // 連絡帳が無ければ候補も無い（異常ではない）
	}
	db := database.DB
	canView := viewCheck(user)

	// 連絡帳 ／ 組織 ／ 人 の2段だけ歩きます。
	orgs, err := cms.ChildPages(db, pageNum(boxID))
	if err != nil {
		return nil
	}
	var out []Signer
	for _, org := range orgs {
		if !canView(org.ID) {
			continue
		}
		people, err := cms.ChildPages(db, org.ID)
		if err != nil {
			continue
		}
		for _, p := range people {
			if !canView(p.ID) {
				continue
			}
			lines := SignatureOf(p.ID, OrderSignatureHeading)
			if len(lines) == 0 {
				continue // ⚠ 署名の無い人は候補にしません（印を別に持たない）
			}
			out = append(out, Signer{PageID: p.ID, Title: p.Title, Org: org.Title, Lines: lines})
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Title != out[j].Title {
			return out[i].Title < out[j].Title
		}
		return out[i].PageID < out[j].PageID
	})
	return out
}

// SignatureOf はページ本文から、その見出しの節の中身を行として返します。
//
// ⚠ **読めるかは呼ぶ側が見ます**（この関数は本文を読むだけ）。
func SignatureOf(pageID int, heading string) []string {
	body, err := cms.ReadPageBody(page.FormatID(pageID))
	if err != nil {
		return nil
	}
	return signatureLines(body, heading)
}

// signatureLines は本文から、その見出しを持つ節の中身を行にして返します。
//
// ⚠ **`<br>` も行の区切りです**——署名を1つの段落に改行で書く人が居ます。
// ⚠ **見出しそのものは返しません**（紙に「発注書の署名」とは刷らない）。
func signatureLines(bodyHTML, heading string) []string {
	nodes, err := htmldoc.ParseFragment(bodyHTML)
	if err != nil {
		return nil
	}
	var found []string
	for _, root := range nodes {
		if found != nil {
			break
		}
		cms.WalkElements(root, func(n *html.Node) {
			if found != nil || n.Data != "section" {
				return
			}
			if sectionHeading(n) != heading {
				return
			}
			found = sectionTextLines(n)
		})
	}
	return found
}

// sectionHeading は節の直下の見出し（h2〜h6）の文字を返します。
//
// ⚠ **直下だけ**を見ます——入れ子の節の見出しを拾うと、別の節の中身を署名として
// 刷ることになります。
func sectionHeading(section *html.Node) string {
	for c := section.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		switch c.Data {
		case "h2", "h3", "h4", "h5", "h6":
			return strings.TrimSpace(textOf(c))
		}
	}
	return ""
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
			continue // 見出しは紙に刷らない
		default:
			add(brToNewline(c))
		}
	}
	if len(out) == 0 {
		return nil
	}
	return out
}

// brToNewline は要素の文字を、`<br>` を改行として取り出します。
func brToNewline(n *html.Node) string {
	var sb strings.Builder
	var walk func(*html.Node)
	walk = func(x *html.Node) {
		switch {
		case x.Type == html.TextNode:
			sb.WriteString(x.Data)
		case x.Type == html.ElementNode && x.Data == "br":
			sb.WriteString("\n")
		}
		for c := x.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	walk(n)
	return sb.String()
}

// DefaultSigner は「いま操作している人」に当たる署名を返します。
//
// ユーザー（2026-09-22）:「**ログイン名と同じ連絡帳ページがあれば、その人が差出人の
// 可能性が高い**です」。
//
// ⚠ **当たらなくても異常ではありません**——ログイン名が `a` で題が `南 康一` なら
// 当たりません。そのときは**人が選びます**（空で返す）。
// ⚠ **畳んで比べます**（`NormalizeText`）——全角・半角・前後の空白の揺れを越えるため。
// ⚠ **`NormalizeCode` は使いません**——人の名前から長音を落とすと別人になります。
func DefaultSigner(user *auth.User, list []Signer) (Signer, bool) {
	if user == nil {
		return Signer{}, false
	}
	want := cms.NormalizeText(user.Username)
	if want == "" {
		return Signer{}, false
	}
	for _, s := range list {
		if cms.NormalizeText(s.Title) == want {
			return s, true
		}
	}
	return Signer{}, false
}

// signerPage は発注書ページのタグ（`発注担当`）が名指しする人のページ番号を返します。
//
// ⚠ **`発注担当` のタグは人が本文で書き換えられます**——ここは「誰を指しているか」を
// 読むだけで、読めるか・署名があるかは呼ぶ側が確かめます。
func signerPage(head map[string]string) (int, bool) {
	id, ok := page.NormalizeID(strings.TrimSpace(head[OrderSignerTag]))
	if !ok {
		return 0, false
	}
	return pageNum(id), true
}

// senderLines は発注書の紙に刷る差出人の行を返します。
//
// ⚠ **担当者ページの署名が先、設定の `company` は後ろ盾**です（2026-09-22）。
// 設定は Git 管理で公開リポジトリに入るため住所も氏名も書けず、実データでは
// **空のまま**でした——だから普通はこちらに落ちません。⚠ **それでも残すのは、
// 連絡帳をまだ作っていない環境で「差出人が丸ごと空の発注書」が出るのを避けるため**です。
//
// ⚠ **空の行は刷りません**——`〒` や `TEL：` だけが並んだ紙が出ます。
//
// ⚠ **読める人の署名しか刷りません**（`page.CanView`）。口（`NewOurOrderAPIHandler`）も
// 差出人を検めますが、**`発注担当` のタグは人が本文で書き換えられます**——だから
// **紙に出る手前のここが最後の砦**です。
func senderLines(head map[string]string, viewer *auth.User) []string {
	// ① このページが名指しした担当者の署名。
	if n, ok := signerPage(head); ok && page.CanView(viewer, n) {
		if lines := SignatureOf(n, OrderSignatureHeading); len(lines) > 0 {
			return lines
		}
	}
	// ② 後ろ盾（設定）。
	c := Company()
	var out []string
	for _, ln := range []string{
		c.Name, strings.TrimSpace("〒" + c.Zip + " " + c.Address),
		"担当： " + c.Person, "TEL： " + c.Tel, "FAX： " + c.Fax,
	} {
		if strings.TrimSpace(strings.Trim(ln, "〒 担当：TELFAX")) == "" {
			continue
		}
		out = append(out, ln)
	}
	return out
}
