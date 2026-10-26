package cms

import (
	"regexp"
	"crypto/rand"
	"fmt"
	"hash/fnv"
	"sort"
	"strings"
	"sync"

	"golang.org/x/net/html"

	"w-cms/internal/cms/htmldoc"
)

// ─────────────────────────────────────────────────────────────────────────
// ブロック単位の保護の部品（2026-10-12）
//
// 利用者:「ブロック単位の保護に変更し、保護の内容は編集中の同ブロックを同時に編集したものが居たら、
// 警告してマージできるように両方表示します」（【考察】編集モードの再考 §6）。
//
//   - ブロックは本文の**いちばん上の段の要素**（data-id で識別）。表は1つの表がまるごと、節は節ごと1ブロック。
//   - 指紋（blockHash）は**サーバーが正本のファイルから計算したもの**だけを使う——画面の書き出しとサーバーの
//     書き出しは字面が違いうる（属性の順・空白）ので、画面が自分で計算した指紋とは比べない。
//   - ぶつかったかどうかは「書き始めたときの指紋」と「いまの指紋」で見る。違えば中身を正規化して比べ直し
//     （canonBlock）、それでも両方が変えていれば、表のセルごとに3つを比べて合わせる（mergeBlock3）。
// ─────────────────────────────────────────────────────────────────────────

// storedBlock は正本の本文の上の段の1ブロックです。
type storedBlock struct {
	ID   string
	HTML string // 正本の書き出し（html.Render）
	Hash string
}

// splitStoredBlocks は本文を上の段の要素に分けます（空白だけの文字・コメントは捨てる——画面の書き出しも
// 上の段の要素だけを保存するので、同じ扱い）。
func splitStoredBlocks(body string) ([]storedBlock, error) {
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return nil, err
	}
	var out []storedBlock
	for _, n := range nodes {
		if n.Type != html.ElementNode {
			continue
		}
		h := renderNode(n)
		out = append(out, storedBlock{ID: Attr(n, htmldoc.BlockIDAttr), HTML: h, Hash: blockHash(h)})
	}
	return out, nil
}

// ChromeOfAttr は、鏡がブロックの直後（上の段）に置いた飾り付け（.vocab-chrome）が、どのブロックに付いているかの印です
// （画面へ返すときだけ付ける——本文には書かない）。
const ChromeOfAttr = "data-chrome-of"

// renderedBlocksWithChrome は、鏡を埋めた本文 body を、ブロック ID → 画面へ返す書き出しにします。
//
// ⚠ 鏡がブロックの直後（上の段）に置いた ID の無い飾り付け（.vocab-chrome——表の下の操作の帯など）は、直前のブロックの
// 書き出しの後ろへ足し、ChromeOfAttr でそのブロックの ID を付ける（2026-10-26）。それまでは ID が無いのでどのブロックにも
// 入らず捨てていて、書ける形に入ると業務の帯が消え、閲覧へ戻しても読み直すまで出なかった。最初のブロックより前の飾り付けは
// 付ける先が無いので、いままでどおり返さない。
func renderedBlocksWithChrome(body string) map[string]string {
	out := map[string]string{}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return out
	}
	last := ""
	for _, n := range nodes {
		if n.Type != html.ElementNode {
			continue
		}
		id := Attr(n, htmldoc.BlockIDAttr)
		if id == "" && last != "" && hasClassName(n, "vocab-chrome") {
			setAttr(n, ChromeOfAttr, last)
			out[last] += renderNode(n)
			continue
		}
		if id == "" {
			continue
		}
		out[id] = renderNode(n)
		last = id
	}
	return out
}

// hasClassName は要素 n の class に name があるかです。
func hasClassName(n *html.Node, name string) bool {
	for _, c := range strings.Fields(Attr(n, "class")) {
		if c == name {
			return true
		}
	}
	return false
}

// renderNode は1つのノードを書き出します。
func renderNode(n *html.Node) string {
	var sb strings.Builder
	html.Render(&sb, n)
	return sb.String()
}

// joinStoredBlocks はブロックを本文へ戻します（1ブロック1行）。
func joinStoredBlocks(blocks []storedBlock) string {
	var sb strings.Builder
	for _, b := range blocks {
		sb.WriteString(b.HTML)
		sb.WriteString("\n")
	}
	return sb.String()
}

// blockHash はブロックの指紋です（書き出しの文字列の FNV-1a 64）。
func blockHash(s string) string {
	h := fnv.New64a()
	h.Write([]byte(s))
	return fmt.Sprintf("%016x", h.Sum64())
}

// stableBlockID は中身から決まるブロック ID を返します（同じ中身の k 番目——同じ中身のブロックが並んでも別の ID）。
// 形は画面の newBlockId と同じ4桁の base36。used と重なれば塩を足して計算し直す。
func stableBlockID(html string, k int, used map[string]bool) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	for salt := 0; salt < 100; salt++ {
		h := fnv.New64a()
		fmt.Fprintf(h, "%s\x00%d\x00%d", html, k, salt)
		v := h.Sum64()
		b := make([]byte, 4)
		for i := range b {
			b[i] = digits[v%36]
			v /= 36
		}
		if id := string(b); !used[id] {
			return id
		}
	}
	return newStoredBlockID(used)
}

// newStoredBlockID は used に無いブロック ID を返します（画面の newBlockId と同じ形——4桁の base36）。
func newStoredBlockID(used map[string]bool) string {
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	for attempt := 0; attempt < 100; attempt++ {
		b := make([]byte, 4)
		rand.Read(b)
		for i := range b {
			b[i] = digits[int(b[i])%36]
		}
		if id := string(b); !used[id] {
			return id
		}
	}
	// 万一ここまで衝突が続いたら桁を増やして確実に抜ける。
	b := make([]byte, 8)
	rand.Read(b)
	for i := range b {
		b[i] = digits[int(b[i])%36]
	}
	return string(b)
}

// ensureStoredBlockIDs は ID の無いブロックと、重複した ID の2つ目以降へ ID を振ります（振った数を返す）。
// 機械が書いた本文は上の段の要素に ID を持たないことがある——同時に書く形では、どのブロックかを ID で
// 見分けるので、ここで揃える。⚠ **振ったブロックは書き出しが変わる**ので HTML と指紋も直す。
//
// ⚠ **振る ID は中身から決まる**（stableBlockID——同じ中身なら毎回同じ ID）。振っただけでは書かない（/api/sync は本当に
// 変えたときだけ書く）——パソコンの既定が書ける形になったので、開いただけで ID を書くと、見ただけのページが書き換わる
// （版が増え・更新日時が進み・ほかの人へ知らせが飛ぶ——2026-10-12 に E2E verify-page-updates.js が見つけた）。書かなくても、
// 次の要求でサーバーが同じ ID を計算し直すので、画面とサーバーで ID が食い違わない。
func ensureStoredBlockIDs(blocks []storedBlock) int {
	used := map[string]bool{}
	for _, b := range blocks {
		if b.ID != "" {
			used[b.ID] = true
		}
	}
	seen := map[string]bool{}
	sameHTML := map[string]int{}
	changed := 0
	for i := range blocks {
		id := blocks[i].ID
		if id != "" && !seen[id] {
			seen[id] = true
			continue
		}
		k := sameHTML[blocks[i].HTML]
		sameHTML[blocks[i].HTML]++
		fresh := stableBlockID(blocks[i].HTML, k, used)
		used[fresh] = true
		seen[fresh] = true
		nodes, err := htmldoc.ParseFragment(blocks[i].HTML)
		if err != nil {
			continue
		}
		for _, n := range nodes {
			if n.Type != html.ElementNode {
				continue
			}
			setAttr(n, htmldoc.BlockIDAttr, fresh)
			blocks[i].HTML = renderNode(n)
			blocks[i].Hash = blockHash(blocks[i].HTML)
			blocks[i].ID = fresh
			changed++
			break
		}
	}
	return changed
}

// whitespaceInsensitive は、子を1行ずつ字下げして書いても表示が変わらない要素（画面の WHITESPACE_INSENSITIVE と同じ）。
var whitespaceInsensitive = map[string]bool{
	"ul": true, "ol": true, "dl": true, "table": true, "thead": true, "tbody": true, "tfoot": true, "tr": true,
	"colgroup": true, "figure": true, "details": true, "section": true, "article": true, "header": true,
	"footer": true, "aside": true, "nav": true,
}

// canonBlock はブロックの書き出しを比べられる形に揃えます——サニタイズを通し、属性を名前の順に並べ、
// 字下げだけの空白（上の要素の中）を捨てる。画面とサーバーで字面が違っても、同じ中身なら同じになる。
func canonBlock(s string) string {
	nodes, err := htmldoc.ParseFragment(Sanitize(s))
	if err != nil {
		return s
	}
	var sb strings.Builder
	for _, n := range nodes {
		if n.Type == html.TextNode && strings.TrimSpace(n.Data) == "" {
			continue
		}
		canonNode(n)
		html.Render(&sb, n)
	}
	return sb.String()
}

func canonNode(n *html.Node) {
	if n.Type == html.ElementNode {
		sort.SliceStable(n.Attr, func(i, j int) bool { return n.Attr[i].Key < n.Attr[j].Key })
		// ファイル表示の印の中身（ファイルの名前）はサーバーが埋めるもので、描いた形では空になる——比べるときは中身を見ない
		// （見ていたので、図面の節を2人で書くと、合わせられるのに衝突と出ていた・2026-10-22 の見直し）。
		if n.Data == "section" && Attr(n, "data-type") == "file-view" {
			for n.FirstChild != nil {
				n.RemoveChild(n.FirstChild)
			}
			return
		}
		if whitespaceInsensitive[n.Data] {
			for c := n.FirstChild; c != nil; {
				next := c.NextSibling
				if c.Type == html.TextNode && strings.TrimSpace(c.Data) == "" {
					n.RemoveChild(c)
				}
				c = next
			}
		}
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		canonNode(c)
	}
}

// cellMark はセルの中身の代わりに骨組みへ置く印です（私用領域の文字——本文には現れない）。
const cellMark = ""

// cellParts はブロックを「骨組み」（セルの中身を印にしたもの）と「セルの中身」の列に分けます。
// セルは td・th のいちばん外側（入れ子の表のセルは外側のセルの中身に含める）。
func cellParts(canon string) (skeleton string, cells []string, ok bool) {
	nodes, err := htmldoc.ParseFragment(canon)
	if err != nil {
		return "", nil, false
	}
	var walk func(n *html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && (c.Data == "td" || c.Data == "th") {
				var sb strings.Builder
				for k := c.FirstChild; k != nil; k = k.NextSibling {
					html.Render(&sb, k)
				}
				cells = append(cells, sb.String())
				for k := c.FirstChild; k != nil; {
					next := k.NextSibling
					c.RemoveChild(k)
					k = next
				}
				c.AppendChild(&html.Node{Type: html.TextNode, Data: cellMark})
				continue
			}
			walk(c)
		}
	}
	var sb strings.Builder
	for _, n := range nodes {
		root := &html.Node{Type: html.DocumentNode}
		// 上の段の要素そのものがセルになることは無い（td が上の段に来るとパーサが捨てる）。
		root.AppendChild(n)
		walk(root)
		html.Render(&sb, n)
		root.RemoveChild(n)
	}
	return sb.String(), cells, true
}

// pick3 は「元・自分・相手」の3つから1つを選びます（片方だけが変えたならその側・同じに変えたならそれ・
// 両方が違うように変えたら ok=false）。
func pick3(base, mine, theirs string) (string, bool) {
	switch {
	case mine == theirs:
		return mine, true
	case mine == base:
		return theirs, true
	case theirs == base:
		return mine, true
	}
	return "", false
}

// mergeBlock3 は同じブロックを自分と相手が両方変えたとき、黙って合わせられれば合わせた書き出しを返します。
//
// 利用者が選んだ形（【考察】編集モードの再考 §6 問い4「エ」）:「表は、違うセルの変更なら自動で合わせる——
// 書き始めたときの表・自分の表・相手の表の3つを比べ、違うセルを直しただけなら黙って両方を入れる。同じセルを
// 違う値にしたとき、または行・列を足した・消したときだけ並べる」。
//
//   - セルの数が3つで違う（行・列を足した・消した）→ 合わせない。
//   - 骨組み（セルの外——見出し・キャプション・属性）も3つを比べ、両方が違うように変えていたら合わせない。
//   - セルの無いブロック（段落など）は骨組みだけの比べになる——両方が変えていれば合わせない。
func mergeBlock3(base, mine, theirs string) (string, bool) {
	cb, cm, ct := canonBlock(base), canonBlock(mine), canonBlock(theirs)
	// ブロック全体で片方だけが変えた（または同じに変えた）なら、セルを数えるまでもない——行を足した側をそのまま採る。
	if whole, ok := pick3(cb, cm, ct); ok {
		return whole, true
	}
	skB, cellsB, okB := cellParts(cb)
	skM, cellsM, okM := cellParts(cm)
	skT, cellsT, okT := cellParts(ct)
	if !okB || !okM || !okT {
		return "", false
	}
	if len(cellsB) != len(cellsM) || len(cellsB) != len(cellsT) {
		return "", false
	}
	// ⚠ 行（<tr> とその属性——行の ID）の並びを変えた側があれば、セルを位置で合わせない（2026-10-26）——行を並べ替えると、
	// 相手のセルが別の行に入る。キャプションや見出しの属性だけの変化は、行を動かさないので合わせてよい。
	if rowTags(skM) != rowTags(skB) || rowTags(skT) != rowTags(skB) {
		return "", false
	}
	sk, ok := pick3(skB, skM, skT)
	if !ok {
		return "", false
	}
	// ⚠ 両方が同じ行のセルを変えていたら合わせない（2026-10-26）。セルは位置で合わせるので、片方が行を消して足すと
	// （セルの数は同じ）、あとの行が全部ずれ、相手が直したセルの値が別の行に入った——「B の数量 5」が C の行に入った。
	// 行のずれは、ずれた行が全部「変わった」に見えるので、同じ行を両方が変えたら並べることで止まる。
	rowOf := cellRows(skB, len(cellsB))
	if rowOf == nil {
		return "", false
	}
	changedM := map[int]bool{}
	for i := range cellsB {
		if cellsM[i] != cellsB[i] {
			changedM[rowOf[i]] = true
		}
	}
	for i := range cellsB {
		if cellsT[i] != cellsB[i] && changedM[rowOf[i]] {
			return "", false
		}
	}
	merged := make([]string, len(cellsB))
	for i := range cellsB {
		c, ok := pick3(cellsB[i], cellsM[i], cellsT[i])
		if !ok {
			return "", false
		}
		merged[i] = c
	}
	parts := strings.Split(sk, cellMark)
	if len(parts) != len(merged)+1 {
		return "", false
	}
	var sb strings.Builder
	for i, p := range parts {
		sb.WriteString(p)
		if i < len(merged) {
			sb.WriteString(merged[i])
		}
	}
	return sb.String(), true
}

// trOpenRe は骨組みの中の行の始まり（<tr> か <tr …>）です。
var trOpenRe = regexp.MustCompile(`<tr[\s>]`)

// trTagRe は行の開きタグ全体（属性つき）です。
var trTagRe = regexp.MustCompile(`<tr(?:\s[^>]*)?>`)

// rowTags は骨組みの行の開きタグを順に並べたものです（行の数・ID・並びを比べるため）。
func rowTags(sk string) string {
	return strings.Join(trTagRe.FindAllString(sk, -1), "\x00")
}

// cellRows は、骨組み sk の n 個のセルそれぞれが何行目にあるかを返します（骨組みとセルの数が合わなければ nil）。
func cellRows(sk string, n int) []int {
	parts := strings.Split(sk, cellMark)
	if len(parts) != n+1 {
		return nil
	}
	rowOf := make([]int, n)
	r := 0
	for i := 0; i < n; i++ {
		r += len(trOpenRe.FindAllStringIndex(parts[i], -1))
		rowOf[i] = r
	}
	return rowOf
}

// ── 誰がそのブロックを書いたか（ぶつかったときの「○○さん」）──────────────────

var blockWriters = struct {
	sync.Mutex
	m map[string]map[string]string // ページ → ブロック ID → 最後に書いた人
}{m: map[string]map[string]string{}}

// noteBlockWriters は、書き換える前と後の本文を比べ、変わった（足した）ブロックを author が書いたと覚えます。
// 再起動で消えてよい（知らせの名前に使うだけ）。
func noteBlockWriters(pageID, author, before, after string) {
	if author == "" {
		return
	}
	old, _ := splitStoredBlocks(before)
	nw, err := splitStoredBlocks(after)
	if err != nil {
		return
	}
	prev := map[string]string{}
	for _, b := range old {
		if b.ID != "" {
			prev[b.ID] = b.Hash
		}
	}
	blockWriters.Lock()
	defer blockWriters.Unlock()
	m := blockWriters.m[pageID]
	if m == nil {
		m = map[string]string{}
		blockWriters.m[pageID] = m
	}
	for _, b := range nw {
		if b.ID != "" && prev[b.ID] != b.Hash {
			m[b.ID] = author
		}
	}
}

// blockWriterOf はそのブロックを最後に書いた人を返します（分からなければ空）。
func blockWriterOf(pageID, blockID string) string {
	blockWriters.Lock()
	defer blockWriters.Unlock()
	return blockWriters.m[pageID][blockID]
}
