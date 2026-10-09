package cms

// ─────────────────────────────────────────────────────────────────────────
// 機械が作るページもテンプレートから作る（2026-09-27）
//
// 利用者:「Dの分についても、テンプレート駆動にしたいです」「テンプレートにはスラッシュ
// メニューから表などの印を置き、コードはそれを埋めてはどうでしょう？問題になりそうなのは、
// 繰り返しの場合だと思います」「繰り返す数は、その場にならないとわからないからです」。
//
// 受注ページ・加工製品・発注書・通信記録・連絡帳の組織と人は、拡張のコードが本文を
// 丸ごと組んでいました（タグの並び・表の列・節の並びがコードに焼き込まれていた）。
// いまは**テンプレートが形を、機械が値と数を**持ちます:
//
//   - **題（h1）**は機械が書きます（題の完全一致が同一性の鍵になっているため）。
//   - **タグ**は `<dt>` の名前で探して値を入れます。**同じ名前を要るだけ繰り返します**
//     （宛先が3人なら3つ）。値が空ならテンプレートの欄をそのまま残します。
//   - **表**はキャプションで探し、**見出しの行を「1行分の形」**として、見出しの言葉で列を
//     合わせた行を要るだけ作ります。見本の行は消します（0件なら残す——人が書けるように）。
//   - **ファイル表示**は、まだ配線されていない（`data-ref` が空の）最初の印へ配線します。
//   - **機械が中身を組むもの**（メールの本文・添付の一覧・発注書の備考）は、名前の見える
//     入れ物——**見出しの節**（`<section><h2>本文</h2>`）か**畳める枠**（`<details>` の
//     `<summary>`）——を名前で探し、見出しの後ろを入れ替えます（利用者の選択:「見出しの節」）。
//
// **テンプレートに無いもの**:
//
//   - 機械が値を持つ**タグ・列**がテンプレートに無ければ**足します**（利用者の選択:「足す」）
//     ——読み手（必要部材表・状態のボタン・重複検知）は名前で探すので、捨てると機能が黙って
//     壊れます。値が空なら足しません。
//   - 値を入れる**器**（表・節）が無ければ、ページを作らずに断ります（`TemplateSlotError`）
//     ——位置を決める規則をコードに戻さないため。器が要らない飾り（原本PDFの表示など）は、
//     呼び手が「無ければ何もしない」を選べます。
//
// テンプレートの引き方は置き場と同じ（テンプレート置き場の下の葉・題が同じもの・
// `templateLeafID`）。**無ければ作りません**（`ErrNoPageTemplate`）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"errors"
	"sort"
	"strings"

	"golang.org/x/net/html"
	"golang.org/x/net/html/atom"

	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
)

// ErrNoPageTemplate は、機械がページを作るためのテンプレートが無いことを表します。
var ErrNoPageTemplate = errors.New("ページを作るテンプレートがありません" +
	"（テンプレート置き場の下に、その題のテンプレートを作ってください）")

// TemplateSlotError は、テンプレートに値を入れる器（表・見出しの節）が無いことを表します。
type TemplateSlotError struct {
	Template string // テンプレートの題
	Slot     string // 無かったもの（「表『受注明細』」など）
}

func (e *TemplateSlotError) Error() string {
	return "テンプレート「" + e.Template + "」に" + e.Slot + "がありません（テンプレートに置いてください）"
}

// ── 機械が使うテンプレートの名簿（管理画面に出すため） ─────────────────────

// PageTemplate は「この拡張が、機械でページを作るときに使うテンプレート」1つです。
type PageTemplate struct {
	Title     string // テンプレートの題（この文字で引く）
	Extension string // 持ち主の拡張ID（コアなら空）
	Why       string // 何を作るときに使うか（画面にそのまま出す）
}

// pageTemplateRegistry は登録されたテンプレートです（`init()` の中からだけ登録する）。
var pageTemplateRegistry = map[string]PageTemplate{}

// RegisterPageTemplate は機械が使うテンプレートを1つ登録します（拡張の `init` から呼ぶ）。
// 登録は**管理画面に「要る・在る」を出すため**だけで、引く口（`DraftFromTemplate`）は
// 登録に関わらず題で引きます。
func RegisterPageTemplate(t PageTemplate) {
	title := strings.TrimSpace(t.Title)
	if title == "" {
		panic("テンプレートの題が空です")
	}
	if prev, dup := pageTemplateRegistry[title]; dup {
		panic("テンプレートの題が重複しています: " + title +
			"（" + prev.Extension + " と " + t.Extension + "）")
	}
	t.Title = title
	pageTemplateRegistry[title] = t
}

// PageTemplateStatus は1つぶんの現状です（画面へ返す形）。
type PageTemplateStatus struct {
	Title     string `json:"title"`
	Extension string `json:"extension"`
	Why       string `json:"why"`
	PageID    string `json:"page_id,omitempty"` // テンプレートのページ（無ければ空）
	Problem   string `json:"problem,omitempty"` // 引けない理由（無い・2枚ある）
}

// PageTemplateStatuses は登録されたテンプレートが在るかを、拡張・題の順で返します。
func PageTemplateStatuses() []PageTemplateStatus {
	out := make([]PageTemplateStatus, 0, len(pageTemplateRegistry))
	for _, t := range pageTemplateRegistry {
		st := PageTemplateStatus{Title: t.Title, Extension: t.Extension, Why: t.Why}
		if id, err := templateLeafID(t.Title, ErrNoPageTemplate); err == nil {
			st.PageID = id
		} else {
			st.Problem = err.Error()
		}
		out = append(out, st)
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Extension != out[j].Extension {
			return out[i].Extension < out[j].Extension
		}
		return out[i].Title < out[j].Title
	})
	return out
}

// ── テンプレートを引く ───────────────────────────────────────────────

// PageTemplateBody は題 title のテンプレートの本文（正本のファイル）を返します。
// 無ければ `ErrNoPageTemplate` を題つきで包んで返します。
func PageTemplateBody(title string) (string, error) {
	return templateBody(strings.TrimSpace(title), ErrNoPageTemplate)
}

// DraftFromTemplate は題 title のテンプレートを写した下書きを返します。
func DraftFromTemplate(title string) (*PageDraft, error) {
	body, err := PageTemplateBody(title)
	if err != nil {
		return nil, err
	}
	return NewPageDraft(title, body), nil
}

// ── 下書き ─────────────────────────────────────────────────────────

// PageDraft はテンプレートを写した、これから作るページの本文です。
//
// 写すときは `CopyTemplateBody` と同じくブロックID（`data-id`）を外します。加えて、
// **定義リストと表の中の空白だけの文字**を落とします——エディタが字下げして保存した
// テンプレートでも、`<dt>客先</dt><dd>` のように詰まった形になります（本文を文字列で
// 探す読み手〔`replaceFirstFieldValue`・改訂明細の行を数える正規表現〕が、機械の
// 作ったページで空振りしないため）。
type PageDraft struct {
	DraftBlock
	template string
	root     *html.Node
}

// DraftBlock は下書きの一部（ページ全体・見出しの節・畳める枠）です。値を入れる口は
// この範囲の中だけを探します。
type DraftBlock struct {
	d *PageDraft
	n *html.Node
}

// NewPageDraft はテンプレートの本文から下書きを作ります（templateTitle はエラー文のため）。
func NewPageDraft(templateTitle, body string) *PageDraft {
	root := &html.Node{Type: html.ElementNode, Data: "body", DataAtom: atom.Body}
	if nodes, err := htmldoc.ParseFragment(body); err == nil {
		for _, n := range nodes {
			dropBlockIDs(n)
			root.AppendChild(n)
		}
	}
	dropLayoutWhitespace(root)
	d := &PageDraft{template: strings.TrimSpace(templateTitle), root: root}
	d.DraftBlock = DraftBlock{d: d, n: root}
	return d
}

// layoutOnly は、中の空白だけの文字に意味が無い要素です。
var layoutOnly = map[string]bool{
	"dl": true, "table": true, "thead": true, "tbody": true, "tfoot": true, "tr": true,
}

// dropLayoutWhitespace は layoutOnly の要素の直下にある空白だけの文字を落とします。
func dropLayoutWhitespace(n *html.Node) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		if c.Type == html.TextNode && layoutOnly[n.Data] && strings.TrimSpace(c.Data) == "" {
			n.RemoveChild(c)
		} else if c.Type == html.ElementNode {
			dropLayoutWhitespace(c)
		}
		c = next
	}
}

// HTML は下書きの本文を返します。
func (d *PageDraft) HTML() string {
	var nodes []*html.Node
	for c := d.root.FirstChild; c != nil; c = c.NextSibling {
		nodes = append(nodes, c)
	}
	return htmldoc.Render(nodes)
}

// SetTitle はページの題（最初の h1）を書きます。h1 が無ければ先頭に置きます。
func (d *PageDraft) SetTitle(title string) {
	h1 := findFirst(d.root, func(n *html.Node) bool { return n.Data == "h1" })
	if h1 == nil {
		h1 = newElement("h1")
		d.root.InsertBefore(h1, d.root.FirstChild)
	}
	setNodeText(h1, title)
}

// AssignBlockID は n にページの中で重ならないブロックIDを振り、そのIDを返します
// （社内コードの指し先になる図面ブロック・改訂明細の行など）。
func (d *PageDraft) AssignBlockID(n *html.Node) string {
	id := page.RandomShortID(page.BlockIDsIn([]byte(d.HTML())))
	setAttr(n, "data-id", id)
	return id
}

// InsertBefore は target の直前に innerHTML を差し込みます。
func (d *PageDraft) InsertBefore(target *html.Node, innerHTML string) {
	if target == nil || target.Parent == nil {
		return
	}
	nodes, err := htmldoc.ParseFragment(innerHTML)
	if err != nil {
		return
	}
	for _, n := range nodes {
		target.Parent.InsertBefore(n, target)
	}
}

// Remove は n を下書きから取り除きます。
func (d *PageDraft) Remove(n *html.Node) {
	if n != nil && n.Parent != nil {
		n.Parent.RemoveChild(n)
	}
}

// Node はこの範囲の要素を返します（ブロックIDを振るときなどに使う）。
func (b DraftBlock) Node() *html.Node { return b.n }

// Container は範囲の中から、名前が name の入れ物——**見出しの節**（直接の子の最初の
// h1〜h6 の文字）か**畳める枠**（`details` の直接の子の `summary` の文字）——を
// 文書順で探します。
func (b DraftBlock) Container(name string) (DraftBlock, bool) {
	name = strings.TrimSpace(name)
	n := findFirst(b.n, func(n *html.Node) bool {
		switch n.Data {
		case "section":
			return functionHeading(n) == name
		case "details":
			if s := directChild(n, "summary"); s != nil {
				return strings.TrimSpace(nodeText(s)) == name
			}
		}
		return false
	})
	if n == nil {
		return DraftBlock{}, false
	}
	return DraftBlock{d: b.d, n: n}, true
}

// RequireContainer は Container と同じですが、無ければ `TemplateSlotError` を返します。
func (b DraftBlock) RequireContainer(name string) (DraftBlock, error) {
	if c, ok := b.Container(name); ok {
		return c, nil
	}
	return DraftBlock{}, &TemplateSlotError{Template: b.d.template, Slot: "見出し「" + name + "」の節"}
}

// SetContent は入れ物の中身を、見出し（節の h1〜h6・枠の summary）を残して innerHTML に
// 入れ替えます。見出しより前にあるものも残します。
func (b DraftBlock) SetContent(innerHTML string) {
	head := containerHead(b.n)
	var c *html.Node
	if head != nil {
		c = head.NextSibling
	} else {
		c = b.n.FirstChild
	}
	for c != nil {
		next := c.NextSibling
		b.n.RemoveChild(c)
		c = next
	}
	nodes, err := htmldoc.ParseFragment(innerHTML)
	if err != nil {
		return
	}
	for _, n := range nodes {
		b.n.AppendChild(n)
	}
}

// containerHead は入れ物の見出しの要素（節の最初の h1〜h6・枠の summary）を返します。
func containerHead(n *html.Node) *html.Node {
	if n.Data == "details" {
		return directChild(n, "summary")
	}
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		switch c.Data {
		case "h1", "h2", "h3", "h4", "h5", "h6":
			return c
		}
	}
	return nil
}

// SetTag は範囲の中のタグ `name` に値を入れます。値が2つ以上なら**同じ名前の対を
// 繰り返します**。空の値は捨て、残りが無ければ何もしません（テンプレートの欄のまま）。
//
// テンプレートに `name` の欄が無ければ、範囲の最初の可変タグの末尾へ足します
// （可変タグが無ければ作ります——ページなら h1 の直後、入れ物なら見出しの直後）。
func (b DraftBlock) SetTag(name string, values ...string) {
	var vals []string
	for _, v := range values {
		if v = strings.TrimSpace(v); v != "" {
			vals = append(vals, v)
		}
	}
	if len(vals) == 0 {
		return
	}
	name = strings.TrimSpace(name)
	if dd := b.tagDD(name); dd != nil {
		setNodeText(dd, vals[0])
		after := dd
		for _, v := range vals[1:] {
			dt, dd2 := tagPair(name, v)
			insertAfter(after, dt)
			insertAfter(dt, dd2)
			after = dd2
		}
		return
	}
	dl := b.tagList()
	for _, v := range vals {
		dt, dd := tagPair(name, v)
		dl.AppendChild(dt)
		dl.AppendChild(dd)
	}
}

// tagDD は範囲の中の可変タグから、名前が name の最初の dd を返します（無ければ nil）。
func (b DraftBlock) tagDD(name string) *html.Node {
	var hit *html.Node
	walkDrafts(b.n, func(n *html.Node) bool {
		if hit != nil {
			return false
		}
		if n.Data != "dl" || Attr(n, "data-type") != TagsDataType {
			return true
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && c.Data == "dt" && strings.TrimSpace(nodeText(c)) == name {
				for dd := c.NextSibling; dd != nil; dd = dd.NextSibling {
					if dd.Type != html.ElementNode {
						continue
					}
					if dd.Data == "dd" {
						hit = dd
					}
					break
				}
				if hit != nil {
					return false
				}
			}
		}
		return false // dl の中へは降りない
	})
	return hit
}

// tagList は範囲の最初の可変タグを返します。無ければ作って置きます。
func (b DraftBlock) tagList() *html.Node {
	if dl := findFirst(b.n, func(n *html.Node) bool {
		return n.Data == "dl" && Attr(n, "data-type") == TagsDataType
	}); dl != nil {
		return dl
	}
	dl := newElement("dl")
	setAttr(dl, "data-type", TagsDataType)
	var head *html.Node
	if b.n == b.d.root {
		head = directChild(b.n, "h1")
	} else {
		head = containerHead(b.n)
	}
	if head != nil {
		insertAfter(head, dl)
	} else {
		b.n.InsertBefore(dl, b.n.FirstChild)
	}
	return dl
}

// Table は範囲の中から、キャプションが caption の表を文書順で探します。
func (b DraftBlock) Table(caption string) (*html.Node, bool) {
	caption = strings.TrimSpace(caption)
	t := findFirst(b.n, func(n *html.Node) bool { return n.Data == "table" && tableCaption(n) == caption })
	return t, t != nil
}

// FillTable はキャプションが caption の表に行を入れ、作った行（tr）を返します。
//
// columns は機械の列の名前、rows はその順の値です。**列は見出しの言葉で合わせます**
// ——テンプレートで列を並べ替えても・足しても崩れません。テンプレートの見出しに無い
// 列は、どこかの行に値があれば右端へ足します（値が無ければ足しません）。
// テンプレートの見本の行は消します。**rows が空なら何も変えません**（見本の行が残り、
// 人が書けます）。表が無い・見出しの行が無いときは `TemplateSlotError`。
func (b DraftBlock) FillTable(caption string, columns []string, rows [][]string) ([]*html.Node, error) {
	t, ok := b.Table(caption)
	if !ok {
		return nil, &TemplateSlotError{Template: b.d.template, Slot: "表「" + caption + "」"}
	}
	var head *html.Node
	for _, tr := range tableRows(t) {
		if directChild(tr, "th") != nil {
			head = tr
			break
		}
	}
	if head == nil {
		return nil, &TemplateSlotError{Template: b.d.template, Slot: "表「" + caption + "」の見出しの行"}
	}
	var labels []string
	for _, c := range rowCells(head) {
		labels = append(labels, strings.TrimSpace(nodeText(c)))
	}
	// 機械の列 → テンプレートの列の位置（同じ見出しが重なれば先勝ち）。
	at := map[string]int{}
	for i, l := range labels {
		if _, dup := at[l]; !dup {
			at[l] = i
		}
	}
	for ci, col := range columns {
		col = strings.TrimSpace(col)
		if _, ok := at[col]; ok || !columnHasValue(rows, ci) {
			continue
		}
		th := newElement("th")
		setNodeText(th, col)
		head.AppendChild(th)
		at[col] = len(labels)
		labels = append(labels, col)
	}
	if len(rows) == 0 {
		return nil, nil
	}
	for _, tr := range tableRows(t) {
		if tr != head {
			tr.Parent.RemoveChild(tr)
		}
	}
	made := make([]*html.Node, 0, len(rows))
	after := head
	for _, row := range rows {
		cells := make([]string, len(labels))
		for ci, col := range columns {
			if i, ok := at[strings.TrimSpace(col)]; ok && ci < len(row) && cells[i] == "" {
				cells[i] = strings.TrimSpace(row[ci])
			}
		}
		tr := newElement("tr")
		for _, v := range cells {
			td := newElement("td")
			if v != "" {
				setNodeText(td, v)
			}
			tr.AppendChild(td)
		}
		insertAfter(after, tr)
		after = tr
		made = append(made, tr)
	}
	return made, nil
}

// columnHasValue は rows のどこかの行で ci 列目に値があるかを返します。
func columnHasValue(rows [][]string, ci int) bool {
	for _, r := range rows {
		if ci < len(r) && strings.TrimSpace(r[ci]) != "" {
			return true
		}
	}
	return false
}

// SetFileView は範囲の中で**まだ配線されていない**（`data-ref` が空の）最初のファイル表示に
// ref を配線します。無ければ何もせず false（表示は飾りなので、器が無いことで断りません）。
func (b DraftBlock) SetFileView(ref string) bool {
	fv := findFirst(b.n, func(n *html.Node) bool {
		return n.Data == "section" && Attr(n, "data-type") == FileViewType &&
			strings.TrimSpace(Attr(n, FileRefAttr)) == ""
	})
	if fv == nil {
		return false
	}
	setAttr(fv, FileRefAttr, ref)
	for c := fv.FirstChild; c != nil; {
		next := c.NextSibling
		fv.RemoveChild(c) // 名札は作るときに付け直す（FillFileViewNames）
		c = next
	}
	return true
}

// DropFileView は範囲の中の**まだ配線されていない**最初のファイル表示を消します（2026-10-01）。包んでいる
// 折りたたみ・節にほかの中身が無ければ（題・見出しだけなら）、それごと消します。無ければ何もせず false。
//
// 原本のファイルが無い文書を同じテンプレートから作るとき（メールの本文から作る受注ページ——PDF の枠が要らない）に、
// 空の枠（「参照がありません」）を残さないため。
func (b DraftBlock) DropFileView() bool {
	fv := findFirst(b.n, func(n *html.Node) bool {
		return n.Data == "section" && Attr(n, "data-type") == FileViewType &&
			strings.TrimSpace(Attr(n, FileRefAttr)) == ""
	})
	if fv == nil {
		return false
	}
	box := fv.Parent
	box.RemoveChild(fv)
	if box != b.n && (box.Data == "details" || box.Data == "section") && onlyNamesLeft(box) {
		box.Parent.RemoveChild(box)
	}
	return true
}

// onlyNamesLeft は入れ物に名前（summary・見出し）と空白しか残っていないかを返します。
func onlyNamesLeft(box *html.Node) bool {
	for c := box.FirstChild; c != nil; c = c.NextSibling {
		switch c.Type {
		case html.TextNode:
			if strings.TrimSpace(c.Data) != "" {
				return false
			}
		case html.ElementNode:
			switch c.Data {
			case "summary", "h2", "h3", "h4", "h5", "h6":
			default:
				return false
			}
		}
	}
	return true
}

// ── 小さな道具 ─────────────────────────────────────────────────────

// walkDrafts は root の子孫要素を文書順に fn へ渡します。fn が false を返した要素の
// 中へは降りません。
func walkDrafts(root *html.Node, fn func(*html.Node) bool) {
	for c := root.FirstChild; c != nil; c = c.NextSibling {
		if c.Type != html.ElementNode {
			continue
		}
		if fn(c) {
			walkDrafts(c, fn)
		}
	}
}

// findFirst は root の子孫要素から pred に当たる最初のものを文書順で返します。
func findFirst(root *html.Node, pred func(*html.Node) bool) *html.Node {
	var hit *html.Node
	walkDrafts(root, func(n *html.Node) bool {
		if hit != nil {
			return false
		}
		if pred(n) {
			hit = n
			return false
		}
		return true
	})
	return hit
}

// directChild は n の直接の子で、要素名が tag の最初のものを返します。
func directChild(n *html.Node, tag string) *html.Node {
	for c := n.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == tag {
			return c
		}
	}
	return nil
}

func newElement(tag string) *html.Node {
	return &html.Node{Type: html.ElementNode, Data: tag, DataAtom: atom.Lookup([]byte(tag))}
}

// setNodeText は n の中身を文字 s だけにします。
func setNodeText(n *html.Node, s string) {
	for c := n.FirstChild; c != nil; {
		next := c.NextSibling
		n.RemoveChild(c)
		c = next
	}
	n.AppendChild(&html.Node{Type: html.TextNode, Data: s})
}

func insertAfter(ref, n *html.Node) {
	ref.Parent.InsertBefore(n, ref.NextSibling)
}

func tagPair(name, value string) (*html.Node, *html.Node) {
	dt := newElement("dt")
	setNodeText(dt, name)
	dd := newElement("dd")
	setNodeText(dd, value)
	return dt, dd
}
