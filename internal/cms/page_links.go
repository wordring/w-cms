package cms

import (
	"database/sql"
	"regexp"
	"strconv"
	"strings"

	"golang.org/x/net/html"
)

// ─────────────────────────────────────────────────────────────────────────
// 本文のリンクとファイル表示の索引（2026-10-23・【考察】消すと壊れるページと表 §5 の案A）
//
//	2026-10-23 利用者:「すべてのページに効く仕組みとして、消す前に①参照のタグと表のセル②本文のリンクとファイル表示（保存のときに索引へ入れる）
//	③そのページの表が集計に使われているか、を調べて警告する形をコアに作ってください。」
//
// 本文の中で、ほかのページ（やその添付）を指している所を、保存のたびに page_links へ洗い替えます。
// 消す前の確かめ（delete_impact.go）が「このページを指しているページ」を引くための索引です。
//
//   - 拾うのは3つです: リンク（<a href="/NNNNNN…">）・画像など（src="/NNNNNN/…"）・ファイル表示などの印（data-ref="NNNNNN-xxxx"）。
//   - 自分自身を指すもの（自分の添付の画像など）は入れません。
//   - テンプレートの下のページは入れません（②索引・③計算と同じ線）。
//
// ⚠ 配送係（walk.go）は印（data-type）のある要素しか届けないので、ここだけは木を自分で1回歩きます。
// 書くのは SyncIndex の中の1か所（同じトランザクション）で、表の持ち主はこのプラグイン（スキーマの登録と、表が増えたときの作り直しのため）。
// ─────────────────────────────────────────────────────────────────────────

func init() {
	Register(pageLinksPlugin{})
}

type pageLinksPlugin struct{}

func (pageLinksPlugin) Name() string { return "page_links" }

func (pageLinksPlugin) Schema() []string {
	return []string{
		`CREATE TABLE IF NOT EXISTS page_links (
			page_id INTEGER NOT NULL,
			target INTEGER NOT NULL,
			sub TEXT NOT NULL,
			kind TEXT NOT NULL,
			FOREIGN KEY (page_id) REFERENCES pages(id) ON DELETE CASCADE
		);`,
		`CREATE INDEX IF NOT EXISTS idx_page_links_target ON page_links(target);`,
		`CREATE INDEX IF NOT EXISTS idx_page_links_page ON page_links(page_id);`,
	}
}

func (pageLinksPlugin) Tables() []string { return []string{"page_links"} }

// リンクの種類（page_links.kind・消す前の確かめに出る言葉）。
const (
	LinkKindAnchor   = "リンク"
	LinkKindEmbed    = "画像など"
	LinkKindFileView = "ファイル表示"
)

// linkPathRe はページを指す道（/NNNNNN・/NNNNNN/添付・/NNNNNN#ブロック）です。
// 住所つき（https://…/NNNNNN）・古い添付の住所（/data/master/NN/NNNNNN/files/添付）・末尾の / も数えます（2026-10-30。数えないと、消す前の
// 確かめが「指されていない」と答えた）。住所の中の名前は見ません——ほかのサイトの同じ形の住所も数えますが、消す前に多めに知らせるだけです。
var linkPathRe = regexp.MustCompile(`^(?:https?://[^/]+)?(?:/data/master/[0-9]{2})?/([0-9]{6})(?:/(?:files/)?([^/?#]+))?/?(?:[?#](.*))?$`)

// dataRefRe は印の参照（NNNNNN-xxxx）です。
var dataRefRe = regexp.MustCompile(`^([0-9]{6})(?:-([A-Za-z0-9]+))?$`)

type pageLink struct {
	target int
	sub    string
	kind   string
}

// pageLinksOf は本文の木から、ほかのページを指す所を集めます（self は除く）。
func pageLinksOf(root *html.Node, self int) []pageLink {
	var out []pageLink
	seen := map[pageLink]bool{}
	add := func(id, sub, kind string) {
		n, err := strconv.Atoi(id)
		if err != nil || n == self {
			return
		}
		l := pageLink{target: n, sub: sub, kind: kind}
		if !seen[l] {
			seen[l] = true
			out = append(out, l)
		}
	}
	WalkElements(root, func(n *html.Node) {
		for _, a := range n.Attr {
			switch {
			case a.Key == "href" && n.Data == "a", a.Key == "src":
				m := linkPathRe.FindStringSubmatch(strings.TrimSpace(a.Val))
				if m == nil {
					continue
				}
				kind := LinkKindEmbed
				if a.Key == "href" {
					kind = LinkKindAnchor
				}
				sub := m[2]
				if sub == "" {
					sub = m[3]
				}
				add(m[1], sub, kind)
			case a.Key == "data-ref":
				if m := dataRefRe.FindStringSubmatch(strings.TrimSpace(a.Val)); m != nil {
					add(m[1], m[2], LinkKindFileView)
				}
			}
		}
	})
	return out
}

// syncPageLinks はページ pageID の page_links を洗い替えます（SyncIndex のトランザクションの中で）。
// root が nil（ページを消すとき）やテンプレートの下なら、消すだけです。
func syncPageLinks(tx *sql.Tx, pageID int, root *html.Node, template bool) error {
	if _, err := tx.Exec(`DELETE FROM page_links WHERE page_id = ?`, pageID); err != nil {
		return err
	}
	if root == nil || template {
		return nil
	}
	for _, l := range pageLinksOf(root, pageID) {
		if _, err := tx.Exec(`INSERT INTO page_links (page_id, target, sub, kind) VALUES (?, ?, ?, ?)`,
			pageID, l.target, l.sub, l.kind); err != nil {
			return err
		}
	}
	return nil
}
