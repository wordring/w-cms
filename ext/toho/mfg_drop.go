package toho

// ─────────────────────────────────────────────────────────────────────────
// 取り消しで空になった製造ページは、ごみ箱へ移す（2026-10-23・【考察】消すと壊れるページと表）
//
//	2026-10-23 利用者:「製造明細表でチェックボックスをチェックし取り消しボタンを押すとその案件は製造フォルダへ戻ります。その時に、
//	明細表が空になり、リンクが無いなど可能な状態であれば、ページを消すことは出来ますか？」
//	→「その条件で、取り消しで製造明細表が空になった製造ページを自動でごみ箱へ移す形を作ってください。」
//
// 製造明細表の「↩ 取り消し」（/api/mfg/back）で最後の行を外したとき、次の全部が揃えば、製造ページをごみ箱へ移します。
// 1つでも欠ければ残し、その理由を返します（画面が伝える）。消すと何かを失う・何かが壊れるときは残す、が線です。
//
//  1. まだ「製造」の下にある（記録へ移した製造は、もともと取り消せない）。
//  2. 製造明細表にデータの行が1つも無い。
//  3. 子ページが無い。添付ファイルが無い。
//  4. ほかのページから指されていない（書類の「製造」の列・タグなど。cms.PagesReferringTo）。
//  5. 人が書いたものが無い——本文が、いまのテンプレートから同じ客先・始めた日・製造番号で組んだ空の製造ページと同じ形
//     （見出し・文・表とその行の数・ファイル表示などの印。字下げや <br> は見ない）。
//     テンプレートを作ったあとで直した場合も「違う」となり、残す（安全な側）。
//
// 消すのは cms.DeletePageToTrash（ごみ箱へ移すだけ・戻せる）。監査には mfg.drop を残す。
// ⚠ 確かめてから移すまでのあいだに人が書いたら、その書き込みはページと一緒にごみ箱へ行く（窓は数ミリ秒）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"strconv"
	"strings"

	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// mfgDropResult は、取り消しのあとに製造ページをどうしたかです。
type mfgDropResult struct {
	Dropped bool   // ごみ箱へ移した
	Kept    string // 製造明細表は空になったが残した理由（空でなければ ""）
	Box     string // 移したときの戻り先（「製造」の置き場のページ番号）
}

// dropEmptyMfgPage は、製造明細表が空になった製造ページ pageID を、消しても何も失わないときだけごみ箱へ移します。
// 製造明細表にまだ行があれば、何もせずに空の結果を返します。
func dropEmptyMfgPage(user *auth.User, pageID string) mfgDropResult {
	mfgID := pageNum(pageID)
	if !mfgIsOpen(mfgID) {
		return mfgDropResult{}
	}
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		return mfgDropResult{}
	}
	if n, ok := mfgItemRowCount(body); !ok || n > 0 {
		return mfgDropResult{}
	}
	keep := func(why string) mfgDropResult {
		return mfgDropResult{Kept: "製造明細表は空になりましたが、" + why + "ので、この製造ページは残しました（要らなければ手で消せます）。"}
	}
	if kids, err := cms.ChildPages(database.DB, mfgID); err != nil || len(kids) > 0 {
		return keep("子ページがある")
	}
	if len(cms.ReadAttachmentMetas(pageID)) > 0 {
		return keep("添付ファイルがある")
	}
	refs, err := cms.PagesReferringTo(database.DB, pageID)
	if err != nil {
		return keep("ほかのページから指されているかを確かめられなかった")
	}
	if len(refs) > 0 {
		return keep(strings.Join(pageRefList(refs), "・") + " がこの製造ページを指している")
	}
	if !mfgBodyIsBlank(pageID, body) {
		return keep("テンプレートに無い書き込みがある")
	}
	box, _ := cms.TopLevelPageByTitle(MfgBoxTitle)
	if _, err := cms.DeletePageToTrash(pageID); err != nil {
		return keep("ごみ箱へ移せなかった（" + err.Error() + "）")
	}
	auth.Audit(user.Username, "mfg.drop", pageID+" 取り消しで製造明細表が空になったのでごみ箱へ")
	return mfgDropResult{Dropped: true, Box: box}
}

// pageRefList はページ番号を「/001234」の形で並べます（多ければ先頭の3つと件数）。
func pageRefList(ids []int) []string {
	var out []string
	for i, id := range ids {
		if i == 3 {
			out = append(out, "ほか "+strconv.Itoa(len(ids)-3)+" 枚")
			break
		}
		out = append(out, "/"+page.FormatID(id))
	}
	return out
}

// mfgItemRowCount は本文の製造明細表の、データの行（どれかのセルに字がある行）の数です。表が無ければ ok=false。
func mfgItemRowCount(body string) (int, bool) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return 0, false
	}
	tables := tablesOfType(nodes, MfgItemsType)
	if len(tables) == 0 {
		return 0, false
	}
	n := 0
	for _, tr := range rowsOf(tables[0])[1:] {
		for _, c := range cellsOf(tr) {
			if strings.TrimSpace(textOf(c)) != "" {
				n++
				break
			}
		}
	}
	return n, true
}

// mfgBodyIsBlank は、製造ページの本文 body が、いまのテンプレートから同じ製造番号・客先・始めた日で組んだ空の製造ページと同じ形かです。
func mfgBodyIsBlank(pageID, body string) bool {
	tmpl, err := cms.PageTemplateBody(MfgTemplate)
	if err != nil {
		return false
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return false
	}
	tag := func(name string) string {
		for _, n := range nodes {
			if v := cms.TagValue(n, name); v != "" {
				return v
			}
		}
		return ""
	}
	blank, err := buildMfgHTML(tmpl, tag(MfgNoTag), tag(DeliveryClientTag), tag(MfgStartTag), nil)
	if err != nil {
		return false
	}
	have, ok1 := bodyShape(body)
	want, ok2 := bodyShape(blank)
	return ok1 && ok2 && have == want
}

// bodyShape は本文の「形」を1本の文字列にします——見出し（h1 を除く）と文、表と行の数、リストの項目、
// 画像・リンク・ファイル表示や鏡の印。字下げ・<br>・空の段落・空の表の行・data-id は見ません（エディタで保存しても変わらないもの）。
func bodyShape(body string) (string, bool) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return "", false
	}
	var b strings.Builder
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		switch n.Type {
		case html.TextNode:
			if t := strings.Join(strings.Fields(n.Data), " "); t != "" {
				b.WriteString(t)
				b.WriteByte('|')
			}
			return
		case html.ElementNode:
			switch n.Data {
			case "h1":
				return // 題は製造明細表の品名から作るので、行が無くなると変わる
			case "tr":
				if strings.TrimSpace(textOf(n)) == "" {
					return // 空の行は失うものが無い（テンプレートから組むと空の行が1つ残る）
				}
				b.WriteString("<tr>")
			case "table", "li", "img", "a", "video", "audio", "iframe", "dl", "dt":
				b.WriteString("<" + n.Data + ">")
			case "section":
				for _, a := range n.Attr {
					if a.Key == "data-type" || a.Key == "data-mirror" || a.Key == "data-view" || a.Key == "data-ref" {
						b.WriteString("<section " + a.Key + "=" + a.Val + ">")
					}
				}
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	return b.String(), true
}
