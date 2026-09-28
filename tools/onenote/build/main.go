// Command build はワンノートから吸い出したデータ（tools/onenote/extract.ps1 の出力）から、
// w-cms の加工製品ページを製造します（2026-09-28）。
//
// 利用者:「移行データの製造を吸い出したデータから行うようにして、通信料を節約したいです。
// 移行データの製造は気が済むまで何度もやり直すので」「ワンノートのページを新しい方法論に移行する
// ことを意識してw-cmsに入れてください」。
//
//   - 読むのはデスクトップの w-cms\ワンノート\<ノートブック>\ だけ（ワンノートには触らない）。
//   - ページは**加工製品のテンプレートを埋めて**作る（解析が作るページと同じ形——題は「図面番号 図面名称」、
//     図面ブロックに図面番号・図面名称・装置名称・客先のタグ、改訂明細の1版目）。材料・外注加工は
//     キャプションの表へ。それ以外の ■見出し は中身があれば見出しの節で、並びを保って残す。
//   - 置き場は本物の木（取引先／社名／段／装置名称）——セクションごとに「製造の設定.json」で決める。
//   - 作ったページには「移行中：確認待ち」。**やり直しは、そのタグが残っているページだけを上書きする**
//     （利用者:「『移行中』のタグを外したページは触らない」）。上げたファイルは「製造の記録.json」に
//     覚えて、やり直しで二度上げない。
//   - 何を落とし・何を決めかねたかは「製造の報告.md」に書く（黙って捨てない）。
//
// ⚠ 実データの名前をこのファイルに書かないこと（公開リポジトリ）——社名などは設定のファイルに書く。
//
// 使い方（リポジトリの根で・サーバーを動かしたまま）:
//
//	go run ./tools/onenote/build            # 製造する
//	go run ./tools/onenote/build -dry       # 下見だけ（サーバーに書かない・製造の下見\ にHTML）
package main

import (
	"bytes"
	"crypto/tls"
	"encoding/json"
	"encoding/xml"
	"errors"
	"flag"
	"fmt"
	stdhtml "html"
	"io"
	"mime/multipart"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"golang.org/x/net/html"

	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
)

const (
	settingsName = "製造の設定.json"
	recordName   = "製造の記録.json"
	reportName   = "製造の報告.md"
	previewDir   = "製造の下見"
	migrateTag   = "移行中"
	migrateValue = "確認待ち"
)

// placement は1つのセクションの置き場です（設定のファイル・人が書く）。
type placement struct {
	Partner string `json:"取引先"`
	Stage   string `json:"段"`
	Machine string `json:"装置名称"`
}

type settings struct {
	Template string               `json:"テンプレート"`
	Sections map[string]placement `json:"セクション"`
}

type pageRecord struct {
	WCMS  string            `json:"w-cms"`
	Files map[string]upload `json:"添付"`
	Built string            `json:"製造"`
}

type upload struct {
	ID  string `json:"id"`
	URL string `json:"url"`
}

type record struct {
	Pages map[string]*pageRecord `json:"ページ"`
}

type catalog struct {
	Pages map[string]struct {
		Title      string   `json:"title"`
		Section    string   `json:"section"`
		Dir        string   `json:"dir"`
		Files      []string `json:"files"`
		Gone       bool     `json:"gone"`
		Incomplete bool     `json:"incomplete"`
	} `json:"pages"`
}

func main() {
	dry := flag.Bool("dry", false, "下見だけ（サーバーに書かない）")
	notebook := flag.String("notebook", "板金部", "ノートブック")
	dirFlag := flag.String("dir", "", "吸い出したデータの置き場（既定はデスクトップの w-cms\\ワンノート\\<ノートブック>）")
	flag.Parse()
	root := *dirFlag
	if root == "" {
		root = filepath.Join(desktop(), "w-cms", "ワンノート", *notebook)
	}
	if err := run(root, *dry); err != nil {
		fmt.Fprintln(os.Stderr, "失敗:", err)
		os.Exit(1)
	}
}

// desktop はデスクトップの場所です（OneDrive へ移されていればそちら）。
func desktop() string {
	home, _ := os.UserHomeDir()
	for _, p := range []string{filepath.Join(home, "OneDrive", "デスクトップ"), filepath.Join(home, "OneDrive", "Desktop"),
		filepath.Join(home, "Desktop")} {
		if st, err := os.Stat(p); err == nil && st.IsDir() {
			return p
		}
	}
	return home
}

func readJSON(path string, v any) error {
	b, err := os.ReadFile(path)
	if err != nil {
		return err
	}
	return json.Unmarshal(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), v)
}

func writeJSON(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	return os.WriteFile(path, b, 0o644)
}

func run(root string, dry bool) error {
	var cat catalog
	if err := readJSON(filepath.Join(root, "目録.json"), &cat); err != nil {
		return fmt.Errorf("目録.json を読めません（先に吸い出してください）: %w", err)
	}
	// 設定——無ければ、目録のセクションを並べた雛形を作って止まる（置き場は人が書く）。
	var set settings
	setPath := filepath.Join(root, settingsName)
	if err := readJSON(setPath, &set); err != nil {
		set = settings{Template: "加工製品", Sections: map[string]placement{}}
		for _, p := range cat.Pages {
			set.Sections[p.Section] = placement{Stage: "現行"}
		}
		if werr := writeJSON(setPath, set); werr != nil {
			return werr
		}
		return fmt.Errorf("%s を作りました。セクションごとに取引先・段・装置名称を書いてから、もう一度動かしてください", setPath)
	}
	var rec record
	recPath := filepath.Join(root, recordName)
	if err := readJSON(recPath, &rec); err != nil || rec.Pages == nil {
		rec = record{Pages: map[string]*pageRecord{}}
	}

	c, err := newClient()
	if err != nil {
		return err
	}
	tmpl, err := c.templateBody(set.Template)
	if err != nil {
		return err
	}

	var rep report
	ids := make([]string, 0, len(cat.Pages))
	for id := range cat.Pages {
		ids = append(ids, id)
	}
	sort.Slice(ids, func(i, j int) bool {
		a, b := cat.Pages[ids[i]], cat.Pages[ids[j]]
		if a.Section != b.Section {
			return a.Section < b.Section
		}
		return a.Title < b.Title
	})
	for _, id := range ids {
		p := cat.Pages[id]
		if p.Gone {
			continue
		}
		pl, ok := set.Sections[p.Section]
		if !ok || strings.TrimSpace(pl.Partner) == "" || strings.TrimSpace(pl.Machine) == "" {
			rep.skip(p.Title, "置き場が決まっていません（"+settingsName+" の「"+p.Section+"」）")
			continue
		}
		pg, err := parsePage(filepath.Join(root, p.Dir, "page.xml"))
		if err != nil {
			rep.skip(p.Title, "page.xml を読めません: "+err.Error())
			continue
		}
		pr := rec.Pages[id]
		if pr == nil {
			pr = &pageRecord{Files: map[string]upload{}}
			rec.Pages[id] = pr
		}
		note := rep.page(p.Title)
		if p.Incomplete {
			note.warn("⚠ 吸い出しで取れなかったファイルがあります（次の吸い出しで取れれば、製造し直すと入ります）")
		}
		if err := buildOne(c, root, p.Dir, tmpl, set.Template, pl, pg, pr, note, dry); err != nil {
			note.warn("⚠ 製造できませんでした: " + err.Error())
			continue
		}
		if !dry {
			pr.Built = time.Now().Format(time.RFC3339)
			if err := writeJSON(recPath, rec); err != nil { // 1ページごとに残す（途中で止まっても二度上げない）
				return err
			}
		}
	}
	if err := os.WriteFile(filepath.Join(root, reportName), []byte(rep.markdown(dry)), 0o644); err != nil {
		return err
	}
	fmt.Println(rep.summary())
	fmt.Println("報告:", filepath.Join(root, reportName))
	return nil
}

// ── ワンノートのページ（page.xml）を読む ───────────────────────────────────

type xnode struct {
	XMLName xml.Name
	Attrs   []xml.Attr `xml:",any,attr"`
	Nodes   []xnode    `xml:",any"`
	Text    string     `xml:",chardata"`
}

func (n *xnode) attr(name string) string {
	for _, a := range n.Attrs {
		if a.Name.Local == name {
			return a.Value
		}
	}
	return ""
}

func (n *xnode) child(name string) *xnode {
	for i := range n.Nodes {
		if n.Nodes[i].XMLName.Local == name {
			return &n.Nodes[i]
		}
	}
	return nil
}

// item はページの中身の1つです（文・表・画像・添付）——並びのまま。
type item struct {
	Kind  string     // "text" | "table" | "image" | "file"
	Text  string     // 文
	Rows  [][]string // 表
	File  string     // 画像・添付の吸い出したファイル名（files\ の中）
	Name  string     // 添付の元の名前
	Print bool       // 印刷イメージ
}

type onePage struct {
	Title   string
	Created string // YYYY-MM-DD
	Items   []item
}

var tagRe = regexp.MustCompile(`(?s)<[^>]*>`)

// plain は T の中身（HTML の断片）を素の文字にします。
func plain(s string) string {
	s = strings.ReplaceAll(s, "<br>", "\n")
	s = tagRe.ReplaceAllString(s, "")
	return strings.TrimSpace(stdhtml.UnescapeString(strings.ReplaceAll(s, "&nbsp;", " ")))
}

func parsePage(path string) (*onePage, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	var root xnode
	if err := xml.Unmarshal(bytes.TrimPrefix(b, []byte("\xef\xbb\xbf")), &root); err != nil {
		return nil, err
	}
	pg := &onePage{}
	if t, err := time.Parse(time.RFC3339, root.attr("dateTime")); err == nil {
		pg.Created = t.In(time.Local).Format("2006-01-02")
	}
	if tt := root.child("Title"); tt != nil {
		var parts []string
		collectText(tt, &parts)
		pg.Title = strings.TrimSpace(strings.Join(parts, " "))
	}
	// アウトラインは紙の上の位置（上から・左から）で読む。
	var outlines []*xnode
	for i := range root.Nodes {
		if root.Nodes[i].XMLName.Local == "Outline" {
			outlines = append(outlines, &root.Nodes[i])
		}
	}
	pos := func(n *xnode) (float64, float64) {
		var x, y float64
		if p := n.child("Position"); p != nil {
			fmt.Sscan(p.attr("x"), &x)
			fmt.Sscan(p.attr("y"), &y)
		}
		return y, x
	}
	sort.SliceStable(outlines, func(i, j int) bool {
		yi, xi := pos(outlines[i])
		yj, xj := pos(outlines[j])
		if yi != yj {
			return yi < yj
		}
		return xi < xj
	})
	for _, o := range outlines {
		if ch := o.child("OEChildren"); ch != nil {
			walkOE(ch, &pg.Items)
		}
	}
	return pg, nil
}

// collectText は n の下の T を全部集めます。
func collectText(n *xnode, out *[]string) {
	for i := range n.Nodes {
		c := &n.Nodes[i]
		if c.XMLName.Local == "T" {
			if t := plain(c.Text); t != "" {
				*out = append(*out, t)
			}
			continue
		}
		collectText(c, out)
	}
}

// walkOE は OEChildren の OE を並びのまま歩きます。⚠ **表のセルの中へは降りません**（セルを二度拾わない
// ——2026-09-21 の移植で踏んだ）。字下げ（OE の中の OEChildren）は続けて歩きます。
func walkOE(children *xnode, out *[]item) {
	for i := range children.Nodes {
		oe := &children.Nodes[i]
		if oe.XMLName.Local != "OE" {
			continue
		}
		for j := range oe.Nodes {
			c := &oe.Nodes[j]
			switch c.XMLName.Local {
			case "T":
				if t := plain(c.Text); t != "" {
					*out = append(*out, item{Kind: "text", Text: t})
				}
			case "Table":
				*out = append(*out, item{Kind: "table", Rows: tableRows(c)})
			case "Image":
				if f := c.attr("wcmsFile"); f != "" {
					*out = append(*out, item{Kind: "image", File: f, Print: c.attr("isPrintOut") == "true"})
				} else {
					*out = append(*out, item{Kind: "image"}) // 吸い出せていない画像（報告に出す）
				}
			case "InsertedFile":
				*out = append(*out, item{Kind: "file", Name: c.attr("preferredName"), File: insertedFileName(c)})
			case "OEChildren":
				walkOE(c, out)
			}
		}
	}
}

func tableRows(t *xnode) [][]string {
	var rows [][]string
	for i := range t.Nodes {
		r := &t.Nodes[i]
		if r.XMLName.Local != "Row" {
			continue
		}
		var cells []string
		for j := range r.Nodes {
			c := &r.Nodes[j]
			if c.XMLName.Local != "Cell" {
				continue
			}
			var parts []string
			collectText(c, &parts)
			cells = append(cells, strings.Join(parts, "\n"))
		}
		rows = append(rows, cells)
	}
	return rows
}

// insertedFileName は吸い出しが付けた添付のファイル名です（extract.ps1 の名付けと対）:
// att_<objectID（波括弧を除いて先頭40字）>__<元の名前（安全化・80字）>。
func insertedFileName(f *xnode) string {
	oid := strings.NewReplacer("{", "", "}", "").Replace(f.attr("objectID"))
	return "att_" + safeName(oid, 40) + "__" + safeName(f.attr("preferredName"), 80)
}

var unsafeRe = regexp.MustCompile(`[\\/:*?"<>|\x00-\x1f]`)

func safeName(s string, max int) string {
	t := strings.TrimRight(strings.TrimSpace(unsafeRe.ReplaceAllString(s, "_")), ".")
	if r := []rune(t); len(r) > max {
		t = string(r[:max])
	}
	if t == "" {
		t = "_"
	}
	return t
}

// ── 節に分ける ─────────────────────────────────────────────────────────

type section struct {
	Name  string
	Items []item
}

// sectionsOf は「■見出し」で区切ります。最初の■より前は「前書き」。
func sectionsOf(pg *onePage) []section {
	out := []section{{Name: "前書き"}}
	for _, it := range pg.Items {
		if it.Kind == "text" && strings.HasPrefix(it.Text, "■") {
			out = append(out, section{Name: strings.TrimSpace(strings.TrimPrefix(it.Text, "■"))})
			continue
		}
		cur := &out[len(out)-1]
		// 前書きの1行目がページの題と同じなら落とす（ワンノートは題を本文にも書くことが多い）。
		if cur.Name == "前書き" && len(cur.Items) == 0 && it.Kind == "text" && it.Text == pg.Title {
			continue
		}
		cur.Items = append(cur.Items, it)
	}
	return out
}

func (s section) empty() bool {
	for _, it := range s.Items {
		switch it.Kind {
		case "text":
			if strings.TrimSpace(it.Text) != "" {
				return false
			}
		case "table":
			if tableHasData(it.Rows) {
				return false
			}
		default:
			return false
		}
	}
	return true
}

// tableHasData は見出しの行を除いて値のある行があるかです。
func tableHasData(rows [][]string) bool {
	for _, r := range rows[min(1, len(rows)):] {
		for _, c := range r {
			if strings.TrimSpace(c) != "" {
				return true
			}
		}
	}
	return false
}

// ── 1ページを製造する ──────────────────────────────────────────────────

// titleMarkRe は題の頭の印（●など）です——図面名称には入れない（意味は報告で聞く）。
var titleMarkRe = regexp.MustCompile(`^[●○◎★☆◆◇■□▲△]+\s*`)

// tableMap は ■見出し → 加工製品の表（キャプション）と、列の言い換え（ワンノートの見出し → w-cms の見出し）。
var tableMap = map[string]struct {
	Caption string
	Rename  map[string]string
	Number  bool // 番号（1から）を振る
}{
	"材料":   {Caption: "材料"},
	"外注加工": {Caption: "外注加工", Rename: map[string]string{"加工": "加工内容"}, Number: true},
	"購入品":  {Caption: "購入部品"},
	"支給品":  {Caption: "支給部品"},
}

func buildOne(c *client, root, dir, tmpl, tmplTitle string, pl placement, pg *onePage, pr *pageRecord,
	note *pageNote, dry bool) error {
	secs := sectionsOf(pg)
	drawingNo := ""
	for _, s := range secs {
		if s.Name == "図面番号" {
			for _, it := range s.Items {
				if it.Kind == "text" && it.Text != "" {
					drawingNo = strings.SplitN(it.Text, "\n", 2)[0]
					break
				}
			}
		}
	}
	name := pg.Title
	if m := titleMarkRe.FindString(name); m != "" {
		note.ask("題の頭に「" + strings.TrimSpace(m) + "」があります——図面名称からは外しました。この印の意味は？（残すべきなら言ってください）")
		name = titleMarkRe.ReplaceAllString(name, "")
	}
	drawingNo = cms.NormalizeNameForIngest(drawingNo)
	name = cms.NormalizeNameForIngest(name)
	if drawingNo == "" {
		note.ask("■図面番号 がありません——題は図面名称だけにしました（Gemini で図面から読むか、人が書く）")
	}
	title := strings.TrimSpace(drawingNo + " " + name)

	// w-cms のページを決める（作る・やり直す・確定済みなら触らない）。
	pageID := pr.WCMS
	if !dry {
		if pageID != "" {
			body, ok := c.readBody(pageID)
			switch {
			case !ok:
				note.info("前に作ったページ " + pageID + " がありません——作り直します")
				pageID = ""
			case !strings.Contains(body, "<dt>"+migrateTag+"</dt>"):
				note.info("「" + migrateTag + "」のタグが外れているので触りません（" + pageID + "）")
				note.wcms = pageID
				return nil
			}
		}
		if pageID == "" {
			parent, err := c.ensurePath([]string{"取引先", pl.Partner, pl.Stage, pl.Machine})
			if err != nil {
				return err
			}
			id, err := c.newPage(parent, "<h1>"+stdhtml.EscapeString(title)+"</h1>")
			if err != nil {
				return err
			}
			pageID = id
			pr.WCMS = id
			pr.Files = map[string]upload{}
		}
	} else if pageID == "" {
		pageID = "000000" // 下見の仮
	}
	note.wcms = pageID

	var token string
	if !dry {
		t, err := c.lock(pageID)
		if err != nil {
			return err
		}
		token = t
		defer c.unlock(pageID, token)
	}
	// 上げる（まだ上げていないものだけ）。名前は見える名前（節＋番号・添付は元の名前）。
	counter := map[string]int{}
	up := func(secName string, it item) (upload, bool) {
		if it.File == "" {
			note.warn("⚠ " + secName + " の画像が吸い出せていません")
			return upload{}, false
		}
		if u, ok := pr.Files[it.File]; ok {
			return u, true
		}
		path := filepath.Join(root, dir, "files", it.File)
		st, err := os.Stat(path)
		if err != nil || st.Size() == 0 {
			note.warn("⚠ " + secName + " のファイルが無いか空です（吸い出し直しで取れれば入ります）: " + it.File)
			return upload{}, false
		}
		shown := it.Name
		if it.Kind == "image" {
			// ⚠ **拡張子は中身から**——ワンノートが形式を言わない画像は吸い出しが .png で保存するが、
			//    中身が JPEG のことがある（2026-09-28・13枚）。画像の口は拡張子と中身が違うと断る。
			ext, ok := imageExt(path)
			if !ok {
				note.warn("⚠ " + secName + " の画像はブラウザで表示できない形式（" + ext + "）なので入れていません")
				return upload{}, false
			}
			counter[secName]++
			shown = fmt.Sprintf("%s%d%s", secName, counter[secName], ext)
		}
		if dry {
			return upload{ID: "xxxx", URL: "/" + pageID + "/" + it.File}, true
		}
		u, err := c.upload(pageID, token, path, shown)
		if err != nil {
			note.warn("⚠ " + shown + " を上げられません: " + err.Error())
			return upload{}, false
		}
		pr.Files[it.File] = u
		return u, true
	}

	d := cms.NewPageDraft(tmplTitle, tmpl)
	d.SetTitle(title)
	d.SetTag(migrateTag, migrateValue)
	blk, err := d.RequireContainer("図面")
	if err != nil {
		return err
	}
	d.AssignBlockID(blk.Node())
	blk.SetTag("図面番号", drawingNo)
	blk.SetTag("図面名称", name)
	blk.SetTag("装置名称", cms.NormalizeNameForIngest(pl.Machine))
	blk.SetTag("客先", cms.NormalizeNameForIngest(pl.Partner))
	if _, err := d.FillTable("改訂明細", []string{"版", "図面番号", "受領日"}, [][]string{{"1", drawingNo, pg.Created}}); err == nil {
		if t, ok := d.Table("改訂明細"); ok {
			d.AssignBlockID(t)
			for _, tr := range childRows(t) {
				d.AssignBlockID(tr)
			}
		}
	}

	// 節を並びのまま置く。テンプレートに入れ物のある節はそこへ、無い節は直前に置いたものの後ろへ。
	anchor := blk.Node()
	before := true // 図面より前の節（不具合・参考図）は図面の前へ
	for _, s := range secs {
		if s.Name == "図面番号" {
			continue
		}
		if s.empty() {
			if s.Name != "前書き" {
				note.dropped(s.Name)
			}
			continue
		}
		switch {
		case s.Name == "図面":
			before = false
			fileViews := 0
			for _, it := range s.Items {
				switch it.Kind {
				case "image", "file":
					u, ok := up("図面", it)
					if !ok {
						continue
					}
					ref := pageID + "-" + u.ID
					if fileViews == 0 && blk.SetFileView(ref) {
						fileViews++
						continue
					}
					appendHTML(blk.Node(), `<section data-type="file-view" data-ref="`+stdhtml.EscapeString(ref)+`"></section>`)
					fileViews++
				case "text":
					appendHTML(blk.Node(), paragraphs(it.Text))
				case "table":
					appendHTML(blk.Node(), plainTable(it.Rows))
				}
			}
			anchor = blk.Node()
		case tableMap[s.Name].Caption != "":
			m := tableMap[s.Name]
			box, ok := d.Container(m.Caption)
			if !ok {
				note.warn("⚠ テンプレートに「" + m.Caption + "」の節がありません——表は落としました")
				continue
			}
			filled := false
			for _, it := range s.Items {
				switch it.Kind {
				case "table":
					if !filled && tableHasData(it.Rows) {
						cols, rows := mapTable(it.Rows, m.Rename, m.Number)
						if _, err := box.FillTable(m.Caption, cols, rows); err != nil {
							note.warn("⚠ 「" + m.Caption + "」を埋められません: " + err.Error())
						}
						filled = true
						continue
					}
					if tableHasData(it.Rows) {
						appendHTML(box.Node(), plainTable(it.Rows))
						note.ask("「" + s.Name + "」に表が2つ以上あります——2つ目からはキャプションの無い表（DBに入らない）で残しました")
					}
				case "text":
					appendHTML(box.Node(), paragraphs(it.Text))
					// 表の外の文——説明（※…）でなければ、表に入れるべき中身かもしれない（例: 購入品の
					// 「カラー かなめ商会 530円」が ■材料 の下に文で書かれている）。機械は文から行を作らない。
					if !strings.HasPrefix(it.Text, "※") {
						note.ask("■" + s.Name + " の表の外に文があります——表の行（購入部品など）にするものか確かめてください")
					}
				case "image", "file":
					if u, ok := up(s.Name, it); ok {
						appendHTML(box.Node(), mediaHTML(pageID, u, it))
					}
				}
			}
			anchor = box.Node()
			before = false
		default:
			var b strings.Builder
			// 前書き（最初の■より前）は見出しを付けず、題のすぐ下に置く。
			if s.Name != "前書き" {
				b.WriteString("<section><h2>" + stdhtml.EscapeString(s.Name) + "</h2>")
			}
			for _, it := range s.Items {
				switch it.Kind {
				case "text":
					b.WriteString(paragraphs(it.Text))
				case "table":
					b.WriteString(plainTable(it.Rows))
				case "image", "file":
					if u, ok := up(s.Name, it); ok {
						b.WriteString(mediaHTML(pageID, u, it))
					}
				}
			}
			if s.Name != "前書き" {
				b.WriteString("</section>")
			}
			if s.Name == "見積もり" {
				note.ask("■見積もり の表は**キャプションを付けていません**（DBに入らない）——中身は原価の内訳（工程・数・単位）で、" +
					"w-cms の「見積もり」（売値）とは別物のため。どの名前の表にするか決めてください")
			}
			nodes, err := htmldoc.ParseFragment(b.String())
			if err != nil || len(nodes) == 0 {
				continue
			}
			if before && s.Name != "前書き" {
				for _, n := range nodes {
					blk.Node().Parent.InsertBefore(n, blk.Node())
				}
				continue
			}
			if s.Name == "前書き" {
				// 前書きは題のすぐ下（タグの後ろ）——図面ブロックの前へ。
				for _, n := range nodes {
					blk.Node().Parent.InsertBefore(n, blk.Node())
				}
				continue
			}
			for _, n := range nodes {
				insertAfter(anchor, n)
				anchor = n
			}
		}
	}
	body := d.HTML()
	if dry {
		out := filepath.Join(root, previewDir)
		os.MkdirAll(out, 0o755)
		return os.WriteFile(filepath.Join(out, safeName(title, 80)+".html"), []byte(body), 0o644)
	}
	return c.save(pageID, token, body)
}

// imageExt は画像の中身から拡張子を決めます（ブラウザで表示できる形式なら ok）。
func imageExt(path string) (string, bool) {
	f, err := os.Open(path)
	if err != nil {
		return "", false
	}
	defer f.Close()
	head := make([]byte, 16)
	n, _ := io.ReadFull(f, head)
	head = head[:n]
	switch {
	case bytes.HasPrefix(head, []byte{0x89, 'P', 'N', 'G'}):
		return ".png", true
	case bytes.HasPrefix(head, []byte{0xff, 0xd8}):
		return ".jpg", true
	case bytes.HasPrefix(head, []byte("GIF8")):
		return ".gif", true
	case len(head) >= 12 && string(head[:4]) == "RIFF" && string(head[8:12]) == "WEBP":
		return ".webp", true
	case bytes.HasPrefix(head, []byte{0x01, 0x00, 0x00, 0x00}):
		return "EMF", false
	case bytes.HasPrefix(head, []byte{0xd7, 0xcd, 0xc6, 0x9a}):
		return "WMF", false
	case bytes.HasPrefix(head, []byte("BM")):
		return "BMP", false
	}
	return "不明", false
}

// mapTable はワンノートの表を（列の名前, 行）へ。見出しは1行目・空の行は落とす。
func mapTable(rows [][]string, rename map[string]string, number bool) ([]string, [][]string) {
	var cols []string
	for _, h := range rows[0] {
		h = strings.TrimSpace(h)
		if r, ok := rename[h]; ok {
			h = r
		}
		cols = append(cols, h)
	}
	if number {
		cols = append([]string{"番号"}, cols...)
	}
	var out [][]string
	n := 0
	for _, r := range rows[1:] {
		empty := true
		for _, v := range r {
			if strings.TrimSpace(v) != "" {
				empty = false
			}
		}
		if empty {
			continue
		}
		if number {
			n++
			r = append([]string{fmt.Sprint(n)}, r...)
		}
		out = append(out, r)
	}
	return cols, out
}

func paragraphs(t string) string {
	var b strings.Builder
	for _, line := range strings.Split(t, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			b.WriteString("<p>" + stdhtml.EscapeString(line) + "</p>")
		}
	}
	return b.String()
}

// plainTable はキャプションの無い表です（DBに入らない——名前は人が決める）。
func plainTable(rows [][]string) string {
	var b strings.Builder
	b.WriteString("<table><tbody>")
	for i, r := range rows {
		b.WriteString("<tr>")
		cell := "td"
		if i == 0 {
			cell = "th"
		}
		for _, v := range r {
			b.WriteString("<" + cell + ">" + stdhtml.EscapeString(v) + "</" + cell + ">")
		}
		b.WriteString("</tr>")
	}
	b.WriteString("</tbody></table>")
	return b.String()
}

// mediaHTML は節の中の画像（img）・添付（ファイル表示）です。
func mediaHTML(pageID string, u upload, it item) string {
	if it.Kind == "image" {
		return `<p><img src="` + stdhtml.EscapeString(u.URL) + `"></p>`
	}
	return `<section data-type="file-view" data-ref="` + stdhtml.EscapeString(pageID+"-"+u.ID) + `"></section>`
}

func appendHTML(parent *html.Node, frag string) {
	nodes, err := htmldoc.ParseFragment(frag)
	if err != nil {
		return
	}
	for _, n := range nodes {
		parent.AppendChild(n)
	}
}

func insertAfter(ref, n *html.Node) {
	ref.Parent.InsertBefore(n, ref.NextSibling)
}

func childRows(t *html.Node) []*html.Node {
	var out []*html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			if c.Type == html.ElementNode && c.Data == "tr" {
				out = append(out, c)
			} else if c.Type == html.ElementNode {
				walk(c)
			}
		}
	}
	walk(t)
	return out
}

// ── w-cms への口 ───────────────────────────────────────────────────────

type client struct {
	base string
	hc   *http.Client
}

func newClient() (*client, error) {
	base := strings.TrimRight(os.Getenv("WCMS_BASE"), "/")
	if base == "" {
		base = "https://localhost:8443"
	}
	jar, _ := cookiejar.New(nil)
	c := &client{base: base, hc: &http.Client{
		Jar: jar,
		// 手元のサーバーは自己署名の証明書（ローカル検証のため確かめない）。
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
		Timeout:       2 * time.Minute,
	}}
	user, pass := os.Getenv("WCMS_USER"), os.Getenv("WCMS_PASS")
	if user == "" {
		user, pass = "a", "a" // ローカル開発の管理者（引き継ぎ・環境の節）
	}
	res, err := c.do("POST", "/api/login", strings.NewReader(url.Values{"username": {user}, "password": {pass}}.Encode()),
		"application/x-www-form-urlencoded", "")
	if err != nil {
		return nil, err
	}
	res.Body.Close()
	if loc := res.Header.Get("Location"); strings.Contains(loc, "error") {
		return nil, errors.New("ログインできません")
	}
	return c, nil
}

func (c *client) do(method, path string, body io.Reader, ctype, token string) (*http.Response, error) {
	req, err := http.NewRequest(method, c.base+path, body)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Origin", c.base)
	if ctype != "" {
		req.Header.Set("Content-Type", ctype)
	}
	if token != "" {
		req.Header.Set("X-Lock-Token", token)
	}
	return c.hc.Do(req)
}

func (c *client) getJSON(path string, v any) error {
	res, err := c.do("GET", path, nil, "", "")
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		return fmt.Errorf("%s: %d", path, res.StatusCode)
	}
	return json.NewDecoder(res.Body).Decode(v)
}

// bodyPath は正本の本文ファイルです（リポジトリの根で動かす——サーバーと同じ data\）。
func bodyPath(id string) string {
	return filepath.Join("data", "master", id[:2], id, id+".html")
}

func (c *client) readBody(id string) (string, bool) {
	b, err := os.ReadFile(bodyPath(id))
	return string(b), err == nil
}

type treeNode struct {
	ID       string     `json:"id"`
	Title    string     `json:"title"`
	Children []treeNode `json:"children"`
}

// templateBody はテンプレート置き場の下から、題が title のテンプレートの本文を読みます。
func (c *client) templateBody(title string) (string, error) {
	var tree []treeNode
	if err := c.getJSON("/api/templates", &tree); err != nil {
		return "", err
	}
	var found string
	var walk func([]treeNode)
	walk = func(ns []treeNode) {
		for _, n := range ns {
			if found == "" && n.Title == title {
				found = n.ID
			}
			walk(n.Children)
		}
	}
	walk(tree)
	if found == "" {
		return "", fmt.Errorf("テンプレート「%s」がありません", title)
	}
	body, ok := c.readBody(found)
	if !ok {
		return "", fmt.Errorf("テンプレート「%s」（%s）の本文を読めません——リポジトリの根で動かしてください", title, found)
	}
	return body, nil
}

func (c *client) children(id string) ([]treeNode, error) {
	var raw json.RawMessage
	if err := c.getJSON("/api/children?parent_id="+id, &raw); err != nil {
		return nil, err
	}
	var list []map[string]any
	if err := json.Unmarshal(raw, &list); err != nil {
		var wrap struct {
			Children []map[string]any `json:"children"`
		}
		if err := json.Unmarshal(raw, &wrap); err != nil {
			return nil, err
		}
		list = wrap.Children
	}
	var out []treeNode
	for _, m := range list {
		id, _ := firstOf(m, "id", "ID").(string)
		t, _ := firstOf(m, "title", "Title").(string)
		out = append(out, treeNode{ID: id, Title: t})
	}
	return out, nil
}

func firstOf(m map[string]any, keys ...string) any {
	for _, k := range keys {
		if v, ok := m[k]; ok {
			return v
		}
	}
	return nil
}

// ensurePath はトップから題の並びを辿り、無ければ作ります（途中のページは題だけ——整理と同じ形）。
func (c *client) ensurePath(titles []string) (string, error) {
	parent := "000000"
	for _, t := range titles {
		t = cms.NormalizeNameForIngest(t)
		kids, err := c.children(parent)
		if err != nil {
			return "", err
		}
		next := ""
		for _, k := range kids {
			if strings.TrimSpace(k.Title) == t {
				next = k.ID
				break
			}
		}
		if next == "" {
			id, err := c.newPage(parent, "<h1>"+stdhtml.EscapeString(t)+"</h1>")
			if err != nil {
				return "", err
			}
			next = id
		}
		parent = next
	}
	return parent, nil
}

var pageIDRe = regexp.MustCompile(`(\d{6})`)

func (c *client) newPage(parent, body string) (string, error) {
	res, err := c.do("POST", "/api/new-page", strings.NewReader(url.Values{"parent": {parent}}.Encode()),
		"application/x-www-form-urlencoded", "")
	if err != nil {
		return "", err
	}
	res.Body.Close()
	m := pageIDRe.FindStringSubmatch(res.Header.Get("Location"))
	if m == nil {
		return "", fmt.Errorf("ページを作れません（%d）", res.StatusCode)
	}
	t, err := c.lock(m[1])
	if err != nil {
		return "", err
	}
	defer c.unlock(m[1], t)
	return m[1], c.save(m[1], t, body)
}

func (c *client) lock(id string) (string, error) {
	res, err := c.do("POST", "/api/lock?id="+id, nil, "", "")
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		// ⚠ 誰かが編集中なら奪わない（本物の木のページ）。
		return "", fmt.Errorf("編集中のため触りません（%s: %d %s）", id, res.StatusCode, strings.TrimSpace(string(b)))
	}
	var v struct {
		Token string `json:"token"`
	}
	if err := json.NewDecoder(res.Body).Decode(&v); err != nil {
		return "", err
	}
	return v.Token, nil
}

func (c *client) unlock(id, token string) {
	if res, err := c.do("POST", "/api/unlock?id="+id+"&token="+url.QueryEscape(token), nil, "", ""); err == nil {
		res.Body.Close()
	}
}

func (c *client) save(id, token, body string) error {
	b, _ := json.Marshal(map[string]string{"page_id": id, "html": body, "token": token})
	res, err := c.do("POST", "/api/save", bytes.NewReader(b), "application/json", token)
	if err != nil {
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		msg, _ := io.ReadAll(res.Body)
		return fmt.Errorf("保存できません（%s: %d %s）", id, res.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}

// upload は1つのファイルを上げます（種類で口を振り分ける——画像・PDF は中身検査つきの専用口）。
func (c *client) upload(pageID, token, path, shown string) (upload, error) {
	ext := strings.ToLower(filepath.Ext(path))
	endpoint, field := "/api/upload-file", "file"
	switch ext {
	case ".png", ".jpg", ".jpeg", ".gif", ".bmp", ".webp":
		endpoint, field = "/api/upload-image", "image_file"
	case ".pdf":
		endpoint, field = "/api/upload-pdf", "pdf_file"
	}
	content, err := os.ReadFile(path)
	if err != nil {
		return upload{}, err
	}
	var buf bytes.Buffer
	w := multipart.NewWriter(&buf)
	w.WriteField("page_id", pageID)
	fw, err := w.CreateFormFile(field, shown)
	if err != nil {
		return upload{}, err
	}
	fw.Write(content)
	w.Close()
	res, err := c.do("POST", endpoint, &buf, w.FormDataContentType(), token)
	if err != nil {
		return upload{}, err
	}
	defer res.Body.Close()
	var v struct {
		ID   string `json:"id"`
		Src  string `json:"src"`
		Href string `json:"href"`
	}
	body, _ := io.ReadAll(res.Body)
	if res.StatusCode != 200 || json.Unmarshal(body, &v) != nil || v.ID == "" {
		return upload{}, fmt.Errorf("%d %s", res.StatusCode, strings.TrimSpace(string(body)))
	}
	u := v.Href
	if u == "" || !strings.HasPrefix(u, "/") {
		u = v.Src
	}
	if !strings.HasPrefix(u, "/") {
		u = "/" + pageID + "/" + u
	}
	return upload{ID: v.ID, URL: u}, nil
}

// ── 報告 ─────────────────────────────────────────────────────────────

type pageNote struct {
	title string
	wcms  string
	infos []string
	warns []string
	asks  []string
	drops []string
}

func (n *pageNote) info(s string) { n.infos = append(n.infos, s) }
func (n *pageNote) warn(s string) { n.warns = append(n.warns, s) }
func (n *pageNote) ask(s string)  { n.asks = append(n.asks, s) }
func (n *pageNote) dropped(s string) {
	n.drops = append(n.drops, s)
}

type report struct {
	pages []*pageNote
	skips []string
}

func (r *report) page(title string) *pageNote {
	n := &pageNote{title: title}
	r.pages = append(r.pages, n)
	return n
}

func (r *report) skip(title, why string) { r.skips = append(r.skips, title+" — "+why) }

func (r *report) summary() string {
	warns, asks := 0, 0
	for _, p := range r.pages {
		warns += len(p.warns)
		asks += len(p.asks)
	}
	return fmt.Sprintf("製造 %d ページ・飛ばした %d・気をつけること %d・聞きたいこと %d", len(r.pages), len(r.skips), warns, asks)
}

func (r *report) markdown(dry bool) string {
	var b strings.Builder
	b.WriteString("# 製造の報告\n\n")
	b.WriteString("製造した日時: " + time.Now().Format("2006-01-02 15:04") + "\n\n")
	if dry {
		b.WriteString("⚠ **下見**です（サーバーには書いていません・" + previewDir + "\\ にHTML）。\n\n")
	}
	b.WriteString(r.summary() + "\n\n")
	// 聞きたいことは同じ文をまとめる（ページごとに同じ問いが並ぶと読めない）。
	asked := map[string][]string{}
	var order []string
	for _, p := range r.pages {
		for _, a := range p.asks {
			if _, ok := asked[a]; !ok {
				order = append(order, a)
			}
			asked[a] = append(asked[a], p.title)
		}
	}
	if len(order) > 0 {
		b.WriteString("## 聞きたいこと\n\n")
		for _, a := range order {
			b.WriteString("- " + a + "（" + fmt.Sprint(len(asked[a])) + "ページ）\n")
		}
		b.WriteString("\n")
	}
	if len(r.skips) > 0 {
		b.WriteString("## 飛ばしたページ\n\n")
		for _, s := range r.skips {
			b.WriteString("- " + s + "\n")
		}
		b.WriteString("\n")
	}
	b.WriteString("## ページごと\n\n")
	for _, p := range r.pages {
		b.WriteString("### " + p.title)
		if p.wcms != "" {
			b.WriteString("（/" + p.wcms + "）")
		}
		b.WriteString("\n\n")
		for _, s := range p.infos {
			b.WriteString("- " + s + "\n")
		}
		for _, s := range p.warns {
			b.WriteString("- " + s + "\n")
		}
		if len(p.drops) > 0 {
			b.WriteString("- 中身の無い見出しを落としました: " + strings.Join(p.drops, "・") + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}
