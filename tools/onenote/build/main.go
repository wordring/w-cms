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
//   - 置き場は本物の木（取引先／社名／加工製品／装置名称）——セクションごとに「製造の設定.json」で決める。
//     試作・見積もり・旧型は段のフォルダではなく加工製品ページのタグ「区分」（2026-09-29）。木の形はサーバーが
//     知っている（/api/product-folder・ext/toho/product_tree.go）ので、道具は社名と装置名称を渡すだけ。
//     装置名称が設定にも表題欄にも無い品目は「不明」の置き場へ（図面の装置名称のタグは空のまま・2026-09-29）。
//   - 作ったページには「移行中：確認待ち」。**やり直しは、そのタグが残っているページだけを上書きする**
//     （利用者:「『移行中』のタグを外したページは触らない」）。上げたファイルは「製造の記録.json」に
//     覚えて、やり直しで二度上げない。
//   - 何を落とし・何を決めかねたかは「製造の報告.md」に書く（黙って捨てない）。
//   - 図面は Gemini で表題欄を読み、図面番号・図面名称・装置名称を入れる。印刷イメージ（PNG）は XPS の束から
//     ベクターの PDF にし、添付の PDF と同じ図面なら移さない（drawing.go）。同じ図面番号のページがあれば作らない。
//   - 取引先ごとの決まり（題を図面名称に・品番＝図面番号・品名＝図面名称）は設定のファイルの「取引先の決まり」。
//
// ⚠ 実データの名前をこのファイルに書かないこと（公開リポジトリ）——社名などは設定のファイルに書く。
//
// 使い方（リポジトリの根で・サーバーを動かしたまま）:
//
//	go run ./tools/onenote/build            # 製造する
//	go run ./tools/onenote/build -dry       # 下見だけ（サーバーに書かない・製造の下見\ にHTML）
//	go run ./tools/onenote/build -fresh     # 前に作った「移行中」のページをごみ箱へ移して作り直す
//
// Gemini を使うときは秘密の設定（GEMINI_API_KEY）を読み込んでから動かす。
package main

import (
	"crypto/sha256"
	"encoding/hex"
	"bytes"
	"crypto/tls"
	"database/sql"
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
	"strconv"
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
	// Kinds は加工製品ページに付ける区分（試作・見積もり・旧型…・2026-09-29 に段のフォルダから替えた）。
	// 空なら通常の製品。選択肢は w-cms の設定の語彙「区分」。
	Kinds []string `json:"区分,omitempty"`
	Machine string `json:"装置名称"`
	// Skip は**移さない理由**です（空でなければ、そのセクションのページは作らない・2026-09-29）。
	// 例: 利用者（2026-09-29）「旧製品／【旧】○○は移植する必要はありません。○○に移植した元データだからです」。
	// 置き場が無いときの「決まっていません」と分けるため（決めた結果として移さないのか、まだ決めていないのか）。
	Skip string `json:"移さない,omitempty"`
}

type settings struct {
	Template string               `json:"テンプレート"`
	Sections map[string]placement `json:"セクション"`
	// TableDest は**人が決めた表の行き先**です（2026-09-29・tables.go）——w-cms のページ番号 → ■節 → その節の表ごとの
	// 行き先（キャプション・「表のまま」・空は機械の決まりのまま）。例: {"001392": {"材料": ["材料", "外注加工", "外注加工"]}}。
	TableDest map[string]map[string][]string `json:"表の行き先,omitempty"`
	// PartNoIsDrawingNo は**品番に図面番号を入れる取引先**です（2026-09-28 利用者:「〈ある取引先〉に限っては、
	// 加工製品の品番に図面番号を入れてください」）。品番は取引先ごとの取り決めなので、取引先の名前で決める
	// （名前は実データなので、このファイルではなく設定のファイルに書く）。
	PartNoIsDrawingNo []string `json:"品番を図面番号にする取引先"`
	// Rules は取引先ごとの決まり（取引先の名前 → 決まり）です。上の「品番を図面番号にする取引先」もここへ畳む。
	Rules map[string]partnerRule `json:"取引先の決まり"`
	// MachineNotes は装置名称のページに書くワンノートのページの題です（既定は「まとめ」——ワンノートはセクションの
	// ページに書けなかったので、装置の話を「まとめ」というページに書いていた）。
	MachineNotes []string `json:"装置のページに書く題"`
}

// isMachineNote はワンノートのページを装置名称のページに書くかです（題の頭の●などは除いて比べる）。
func (s settings) isMachineNote(title string) bool {
	names := s.MachineNotes
	if len(names) == 0 {
		names = []string{"まとめ"}
	}
	t := normName(titleMarkRe.ReplaceAllString(title, ""))
	for _, n := range names {
		if normName(n) == t {
			return true
		}
	}
	return false
}

// partnerRule は取引先ごとの加工製品ページの決まりです（2026-09-28 利用者:「〈ある取引先〉に関しては、
// 加工製品のページ名は図面名称を入れてください。品名タグを作って値として図面名称を入れてください」）。
// 書ける値は1つずつ——知らない値は止める（黙って既定に戻さない）。
type partnerRule struct {
	PartNo   string `json:"品番"` // "図面番号": 題の下の品番タグに図面番号
	ItemName string `json:"品名"` // "図面名称": 題の下の品名タグに図面名称
	Title    string `json:"題"`  // "図面名称": 題は図面名称だけ（既定は「図面番号 図面名称」）
}

func (r partnerRule) check(partner string) error {
	for _, f := range []struct{ key, got, want string }{
		{"品番", r.PartNo, "図面番号"}, {"品名", r.ItemName, "図面名称"}, {"題", r.Title, "図面名称"},
	} {
		if f.got != "" && f.got != f.want {
			return fmt.Errorf("%s の「取引先の決まり」の「%s」の「%s」は「%s」だけ書けます（「%s」は分かりません）",
				settingsName, partner, f.key, f.want, f.got)
		}
	}
	return nil
}

// ruleFor は取引先の決まりです（名前は正規化して比べる）。
func (s settings) ruleFor(partner string) (partnerRule, error) {
	key := cms.NormalizeNameForIngest(partner)
	var r partnerRule
	for n, v := range s.Rules {
		if cms.NormalizeNameForIngest(n) == key {
			if err := v.check(n); err != nil {
				return r, err
			}
			r = v
		}
	}
	for _, n := range s.PartNoIsDrawingNo {
		if cms.NormalizeNameForIngest(n) == key {
			r.PartNo = "図面番号"
		}
	}
	return r, nil
}

type pageRecord struct {
	WCMS      string            `json:"w-cms"`
	Files     map[string]upload `json:"添付"`
	Built     string            `json:"製造"`
	DrawingNo string            `json:"図面番号,omitempty"`
	Kind      string            `json:"種類,omitempty"` // 空＝加工製品ページ・kindMachine＝装置のページに書いた
}

type upload struct {
	ID  string `json:"id"`
	URL string `json:"url"`
	// SHA は上げたときの中身の指紋です（2026-09-29）。中身が変わっていたら上げ直す——家の吸い出しと
	// 職場の製造を同時に動かすと、届きかけのファイルを上げてしまうことがあり、ワンノートで直した画像が
	// 同じ名前で届くこともある。名前だけで覚えていると、どちらも古いまま残る。空は指紋を取る前の記録。
	SHA string `json:"sha256,omitempty"`
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
		Skipped    string   `json:"skipped"` // 別のノートブックに同じものがあるので吸い出さなかった（その理由）
		LocalIDs   strings1 `json:"localIds"` // 機械ごとのワンノートのページID（目録の鍵は機械に依らない page-id・2026-09-29）

		// ワンノートでも失われたもの（2026-09-30・吸い出しの「ワンノートでも失われたもの.txt」に人が書いたもの）——取り直さない。
		LostInOneNote strings1 `json:"lostInOneNote"`
	} `json:"pages"`
}

// strings1 は文字列の並びです——⚠ PowerShell の ConvertTo-Json は要素1つの配列を**ただの文字列**で書くことがある
// （`$( … )` が配列をほどく・2026-09-29 に踏んだ）ので、どちらの形も読む。
type strings1 []string

func (s *strings1) UnmarshalJSON(b []byte) error {
	var one string
	if json.Unmarshal(b, &one) == nil {
		*s = strings1{one}
		return nil
	}
	var many []string
	if err := json.Unmarshal(b, &many); err != nil {
		return err
	}
	*s = many
	return nil
}

func main() {
	dry := flag.Bool("dry", false, "下見だけ（サーバーに書かない）")
	notebook := flag.String("notebook", "板金部", "ノートブック")
	dirFlag := flag.String("dir", "", "吸い出したデータの置き場（既定はデスクトップの w-cms\\ワンノート\\<ノートブック>）")
	mutool := flag.String("mutool", filepath.Join(desktop(), "w-cms", "道具", "mupdf", "mutool.exe"),
		"印刷イメージの XPS を PDF にする mutool（無ければ PNG のまま）")
	gemini := flag.Bool("gemini", true, "Gemini で図面の表題欄を読む（控えのあるファイルは呼ばない・GEMINI_API_KEY が要る）")
	fresh := flag.Bool("fresh", false, "前に作った「移行中」のページをごみ箱へ移して作り直す（前に上げたファイルを残さない・ページ番号は変わる）")
	onlyFlag := flag.String("only", "", "作り直すページを w-cms のページIDで絞る（カンマ区切り・報告は「製造の報告（一部）.md」）")
	flag.Parse()
	root := *dirFlag
	if root == "" {
		root = filepath.Join(desktop(), "w-cms", "ワンノート", *notebook)
	}
	e := &env{root: root, mutool: *mutool, gemini: *gemini, fresh: *fresh, seen: map[string]string{}, machineNotes: map[string]string{}}
	for _, id := range strings.Split(*onlyFlag, ",") {
		if id = strings.TrimSpace(id); id != "" {
			if e.only == nil {
				e.only = map[string]bool{}
			}
			e.only[fmt.Sprintf("%06s", strings.TrimLeft(id, "/"))] = true
		}
	}
	// 設定（語の型——図面番号は code）と索引（同じ図面番号のページを探す・読むだけ）。リポジトリの根で動かす。
	if err := cms.LoadSettings(); err != nil {
		fmt.Fprintln(os.Stderr, "失敗: 設定を読めません（リポジトリの根で動かしてください）:", err)
		os.Exit(1)
	}
	if db, err := sql.Open("sqlite", filepath.ToSlash(filepath.Join("data", "cms.db"))+
		"?_pragma=busy_timeout(5000)&_pragma=query_only(1)"); err == nil {
		e.db = db
		defer db.Close()
	}
	if err := run(e, *dry); err != nil {
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

func run(e *env, dry bool) error {
	root := e.root
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
			set.Sections[p.Section] = placement{}
		}
		if werr := writeJSON(setPath, set); werr != nil {
			return werr
		}
		return fmt.Errorf("%s を作りました。セクションごとに取引先・装置名称（と、あれば区分）を書いてから、もう一度動かしてください", setPath)
	}
	e.tableDest = set.TableDest
	var rec record
	recPath := filepath.Join(root, recordName)
	if err := readJSON(recPath, &rec); err != nil || rec.Pages == nil {
		rec = record{Pages: map[string]*pageRecord{}}
	}
	// 製造の記録の鍵を目録の鍵へ移し替える（2026-09-29）——目録の鍵が機械ごとのページIDから機械に依らない page-id に
	// 変わった。前の鍵（会社の機械のID）で覚えているページは、目録の localIds で当てて移す。⚠ 移さないと、同じ
	// ワンノートのページを「初めて」と読んで w-cms に2枚目を作る。
	moved := 0
	for key, p := range cat.Pages {
		if rec.Pages[key] != nil {
			continue
		}
		for _, lid := range p.LocalIDs {
			if r := rec.Pages[lid]; r != nil && lid != key {
				rec.Pages[key] = r
				delete(rec.Pages, lid)
				moved++
				break
			}
		}
	}
	if moved > 0 && !dry {
		if err := writeJSON(recPath, rec); err != nil {
			return err
		}
		fmt.Printf("製造の記録の鍵を %d 件移し替えました\n", moved)
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
	if dry {
		// 下見は毎回作り直す（前の回の題のままの HTML が残ると、どれが今の結果か分からない）。
		os.RemoveAll(filepath.Join(root, previewDir))
	}
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
		if p.Skipped != "" {
			rep.skip(p.Title, "吸い出していません（"+p.Skipped+"）")
			continue
		}
		// 装置名称は空でもよい（図面の表題欄から読む）——取引先は設定で決める（区分は無くてよい）。
		pl, ok := set.Sections[p.Section]
		if ok && strings.TrimSpace(pl.Skip) != "" {
			rep.skip(p.Title, "移さないと決めたセクションです（"+strings.TrimSpace(pl.Skip)+"）")
			continue
		}
		if !ok || strings.TrimSpace(pl.Partner) == "" {
			rep.skip(p.Title, "置き場が決まっていません（"+settingsName+" の「"+p.Section+"」）")
			continue
		}
		pg, err := parsePage(filepath.Join(root, p.Dir, "page.xml"))
		if err != nil {
			rep.skip(p.Title, "page.xml を読めません: "+err.Error())
			continue
		}
		pr := rec.Pages[id]
		// `-only` なら、そのページIDで作ったページだけ（ほかは黙って飛ばす——報告も別のファイル）。
		if len(e.only) > 0 && (pr == nil || !e.only[pr.WCMS]) {
			continue
		}
		if pr == nil {
			pr = &pageRecord{Files: map[string]upload{}}
			rec.Pages[id] = pr
		}
		note := rep.page(p.Title)
		if p.Incomplete {
			note.warn("⚠ 吸い出しで取れなかったファイルがあります（次の吸い出しで取れれば、製造し直すと入ります）")
		}
		if len(p.LostInOneNote) > 0 {
			note.warn("⚠ ワンノートでも失われているファイルがあります——入りません（" + strings.Join(p.LostInOneNote, "・") + "）")
		}
		rule, err := set.ruleFor(pl.Partner)
		if err != nil {
			return err
		}
		build := func() error {
			if set.isMachineNote(p.Title) {
				return buildMachineNote(e, c, p.Dir, pl, pg, pr, note, dry)
			}
			return buildOne(e, c, p.Dir, tmpl, set.Template, pl, rule, pg, pr, note, dry)
		}
		if err := build(); err != nil {
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
	// 受注の行を、いま在る加工製品ページへ結び直す（2026-09-30 夜）——⚠ 取り込みは整理を通らずにページを作るので、
	// ここで結ばないと、先に整理した受注の `弊社品番` が空のまま残る（利用者:「品番〈図番〉の加工製品ページはあるのに、
	// 受注残の表に弊社品番なしになるのは何故でしょう？」）。下見では呼ばない（サーバーに書く口なので）。
	if !dry {
		if res, err := c.relinkOrders(); err != nil {
			rep.notes = append(rep.notes, "⚠ 受注の行を結び直せませんでした: "+err.Error())
		} else {
			rep.relink = &res
		}
	}
	if e.mutoolMissing {
		rep.notes = append(rep.notes, "⚠ mutool がありません（"+e.mutool+"）——印刷イメージは PNG のまま入れました")
	}
	if e.geminiOff {
		rep.notes = append(rep.notes, "⚠ GEMINI_API_KEY が無いので図面の表題欄を読んでいません（控えのあるファイルだけ使いました）")
	}
	name := reportName
	if len(e.only) > 0 {
		name = "製造の報告（一部）.md" // いつもの報告を一部の回で上書きしない
	}
	if err := os.WriteFile(filepath.Join(root, name), []byte(rep.markdown(dry)), 0o644); err != nil {
		return err
	}
	fmt.Println(rep.summary())
	fmt.Println("報告:", filepath.Join(root, name))
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
	// Struck は表のセルが取り消し線で打ち消されているか（Rows と同じ形・2026-09-29——tables.go）。
	Struck [][]bool
	File  string     // 画像・添付の吸い出したファイル名（files\ の中）
	Name  string     // 添付の元の名前
	Print bool       // 印刷イメージ

	XPS      string // 印刷イメージの元の XPS の束（XPS\ の中の名前）
	XPSPage  int    // 束の中のページ（1から）
	xpsIndex string // page.xml の xpsFileIndex（束を引くため）
	// 図面の下ごしらえ（drawing.go の prepare）。
	Vector string        // 束から切り出したベクターの PDF（PDF化\ の中・無ければ PNG のまま入れる）
	Judged bool          // 表題欄を読んだ（Gemini か控え）
	Reads  []drawingRead // 読んだ図面（図面でなければ空）
	Skip   string        // 移さない理由（添付の PDF と同じ図面の印刷イメージ）
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
	// 印刷イメージの束（XPSFile の xpsFileIndex → 吸い出しが付けた wcmsFile）。
	bundles := map[string]string{}
	var findXPS func(n *xnode)
	findXPS = func(n *xnode) {
		for i := range n.Nodes {
			c := &n.Nodes[i]
			if c.XMLName.Local == "XPSFile" {
				if f := c.attr("wcmsFile"); f != "" {
					bundles[c.attr("xpsFileIndex")] = f
				}
				continue
			}
			findXPS(c)
		}
	}
	findXPS(&root)
	for i := range pg.Items {
		if it := &pg.Items[i]; it.Print && it.XPSPage > 0 {
			it.XPS = bundles[it.xpsIndex]
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
				rows, struck := tableRows(c)
				*out = append(*out, item{Kind: "table", Rows: rows, Struck: struck})
			case "Image":
				// File が空なら吸い出せていない画像（報告に出す）——印刷イメージなら XPS から取れることがある。
				it := item{Kind: "image", File: c.attr("wcmsFile"), Print: c.attr("isPrintOut") == "true",
					xpsIndex: c.attr("xpsFileIndex")}
				if n, err := strconv.Atoi(c.attr("originalPageNumber")); err == nil && it.Print {
					it.XPSPage = n + 1 // originalPageNumber は0から・束のページは1から
				}
				*out = append(*out, it)
			case "InsertedFile":
				*out = append(*out, item{Kind: "file", Name: c.attr("preferredName"), File: insertedFileName(c)})
			case "OEChildren":
				walkOE(c, out)
			}
		}
	}
}

func tableRows(t *xnode) ([][]string, [][]bool) {
	var rows [][]string
	var struck [][]bool
	for i := range t.Nodes {
		r := &t.Nodes[i]
		if r.XMLName.Local != "Row" {
			continue
		}
		var cells []string
		var ss []bool
		for j := range r.Nodes {
			c := &r.Nodes[j]
			if c.XMLName.Local != "Cell" {
				continue
			}
			var parts []string
			collectText(c, &parts)
			cells = append(cells, strings.Join(parts, "\n"))
			ss = append(ss, cellStruck(c))
		}
		rows = append(rows, cells)
		struck = append(struck, ss)
	}
	return rows, struck
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
var titleMarkRe = regexp.MustCompile(`^[●○〇◎★☆◆◇■□▲△]+\s*`)

// bracketMarkRe は題の頭の【…】（【旧】【追加工】など）です——題に残す印。
var bracketMarkRe = regexp.MustCompile(`^(?:【[^】]*】\s*)+`)

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

func buildOne(e *env, c *client, dir, tmpl, tmplTitle string, pl placement, rule partnerRule, pg *onePage, pr *pageRecord,
	note *pageNote, dry bool) error {
	root := e.root
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
	// 題の頭の印（●など）は外す（2026-09-28 利用者:「●の意味は、変更の有り無しなどをページ名からわかるように
	// してたものです。消してください」）。
	name = titleMarkRe.ReplaceAllString(name, "")
	// 題の頭の【旧】【追加工】…は人が付けた印——題に残す（2026-09-28 利用者:「旧図面という情報を残す必要はあります」）。
	// 図面名称・品名（表題欄の名前）には入れない。
	var mark string
	if m := bracketMarkRe.FindString(name); m != "" {
		mark = strings.Join(strings.Fields(cms.NormalizeNameForIngest(m)), "")
		name = strings.TrimSpace(name[len(m):])
	}
	drawingNo = cms.NormalizeNameForIngest(drawingNo)
	name = cms.NormalizeNameForIngest(name)

	// 図面の下ごしらえ——印刷イメージは XPS からベクターの PDF に、表題欄を読み、添付の PDF と同じ図面の
	// 印刷イメージは移さない（drawing.go）。図面番号・図面名称・装置名称は表題欄から入れる。
	e.prepare(dir, secs, note)
	machine := cms.NormalizeNameForIngest(pl.Machine)
	m, hasMain := mainDrawing(secs, drawingNo, name)
	if hasMain {
		if no := cms.NormalizeNameForIngest(m.No); no != "" {
			if drawingNo != "" && normNo(no) != normNo(drawingNo) {
				note.ask("図面番号がワンノート（■図面番号）と図面の表題欄で違います——表題欄の方を入れました")
				note.info("図面番号: ワンノート「" + drawingNo + "」→ 表題欄「" + no + "」")
			}
			drawingNo = no
		}
		if nm := cms.NormalizeNameForIngest(m.Name); nm != "" {
			if normName(nm) != normName(name) {
				note.info("図面名称は表題欄から「" + nm + "」（ワンノートの題「" + name + "」）")
			}
			name = nm
		}
		if mm := cms.NormalizeNameForIngest(m.Machine); mm != "" {
			if machine == "" {
				machine = mm
			} else if normName(mm) != normName(machine) {
				note.ask("装置名称が図面（" + mm + "）と置き場（" + machine + "）で違います——置き場の方を入れました")
			}
		}
	} else if e.gemini {
		note.warn("⚠ 図面の表題欄を読めませんでした——ワンノートの題と ■図面番号 のままです")
	}
	// 移さないと決めた印刷イメージ（添付の PDF と同じ図面）は、ここで節から外す。
	for si := range secs {
		kept := secs[si].Items[:0]
		for _, it := range secs[si].Items {
			if it.Skip == "" {
				kept = append(kept, it)
			}
		}
		secs[si].Items = kept
	}
	if drawingNo == "" {
		note.ask("図面番号がありません（■図面番号 も表題欄も）——題は図面名称だけにしました（人が書く）")
	}
	// 装置の区別が無い品目は「不明」の置き場へ（2026-09-29 利用者:「装置の区別が無い取引先の品目は、「不明」の下に
	// 入れてください」）。⚠ 図面の `装置名称` タグは空のまま——分からないことを「不明」という装置名にしない。
	folder := machine
	if folder == "" {
		folder = unknownMachine
		note.info("装置名称が分からないので「" + unknownMachine + "」の下に入れました（置き場の設定にも図面の表題欄にも無い）")
	}
	title := strings.TrimSpace(drawingNo + " " + name)
	if rule.Title == "図面名称" && name != "" {
		title = name // この取引先の題は図面名称だけ（図面番号は図面ブロックのタグと品番で引く）
	}
	title = mark + title

	// 前に作った「移行中」のページを作り直す（-fresh）——ごみ箱へ移して、新しく作る。
	pageID := pr.WCMS
	if !dry && e.fresh && pageID != "" {
		if body, ok := c.readBody(pageID); ok && strings.Contains(body, "<dt>"+migrateTag+"</dt>") {
			if err := c.deletePage(pageID); err != nil {
				return err
			}
			note.info("前に作ったページ " + pageID + " をごみ箱へ移して作り直しました")
			pageID, pr.WCMS, pr.Files = "", "", map[string]upload{}
		}
	}

	// 同じ図面番号のページがあれば作らない（2026-09-28 利用者:「2枚目は作らず、報告に出す」）——図面ごとに
	// 加工製品ページは1枚。ワンノートのほうの中身（写真など）は人が移す。
	if dup := e.duplicate(drawingNo, pageID); dup != "" {
		var rest []string
		for _, s := range secs {
			if s.Name != "図面番号" && s.Name != "図面" && s.Name != "前書き" && !s.empty() {
				rest = append(rest, s.Name)
			}
		}
		msg := "図面番号 " + drawingNo + " のページが既にあります（" + dup + "）——このページは作っていません"
		if len(rest) > 0 {
			msg += "。移すなら人の手で: " + strings.Join(rest, "・")
		}
		note.warn("⚠ " + msg)
		note.ask("同じ図面番号のページが既にあるので作らなかったワンノートのページがあります（ページごとの節を見てください）")
		note.duplicate = true
		return nil
	}
	if drawingNo != "" {
		e.seen[normNo(drawingNo)] = pg.Title
	}

	// w-cms のページを決める（作る・やり直す・確定済みなら触らない）。
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
			parent, err := c.productFolder(pl.Partner, folder)
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
	pr.DrawingNo = drawingNo

	var token string
	if !dry {
		t, err := c.lock(pageID)
		if err != nil {
			return err
		}
		token = t
		defer c.unlock(pageID, token)
	}
	up, reportLeft := uploaderFor(c, root, dir, pageID, token, pr, note, dry)
	defer reportLeft()

	d := cms.NewPageDraft(tmplTitle, tmpl)
	d.SetTitle(title)
	if rule.PartNo == "図面番号" {
		d.SetTag("品番", drawingNo) // この取引先の品番は図面番号（題の下のタグ——受注明細の品番と結ぶ）
	}
	if rule.ItemName == "図面名称" {
		d.SetTag("品名", name) // この取引先の品名は図面名称（題の下のタグ）
	}
	// 弊社品番＝このページの番号（2026-09-28 利用者:「弊社品番としてタグにページ番号を入れてください。
	// 検索できるようにです」）。
	d.SetTag("弊社品番", pageID)
	// 区分（試作・見積もり・旧型）はセクションの置き場から（2026-09-29・段のフォルダの代わり）。
	d.SetTag("区分", pl.Kinds...)
	d.SetTag(migrateTag, migrateValue)
	blk, err := d.RequireContainer("図面")
	if err != nil {
		return err
	}
	d.AssignBlockID(blk.Node())
	blk.SetTag("図面番号", drawingNo)
	blk.SetTag("図面名称", name)
	blk.SetTag("装置名称", machine)
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
	// 図面の後ろの目印は改訂明細（図面ブロックのすぐ後ろの表）——図面と改訂明細の間に節を割り込ませない。
	afterDrawing := blk.Node()
	if t, ok := d.Table("改訂明細"); ok && t.Parent == blk.Node().Parent {
		afterDrawing = t
	}
	// 表の節（材料・外注加工…）が1つでも出たら、それより後ろのテンプレートに無い節はページの末尾へ——
	// ワンノートは ■材料 ■見積もり ■工程 ■完成品 ■外注加工 … の順に書くことがあり、並びのまま置くと
	// テンプレートの表（材料 → 外注加工 → 購入部品 → 支給部品）の間に割り込む。
	pastTables := false
	var fills tableFills // テンプレートの表へ入れる行（行き先の表ごと・最後に1回で埋める——tables.go）
	for _, s := range secs {
		if s.Name == "図面番号" {
			continue
		}
		if tableMap[s.Name].Caption != "" {
			pastTables = true
		}
		if s.empty() {
			if s.Name != "前書き" {
				note.dropped(s.Name)
			}
			if s.Name == "図面" {
				anchor, before = afterDrawing, false
			}
			// 空でもテンプレートに入れ物のある節なら、並びの目印はそこへ進める——進めないと、あとの節が
			// テンプレートの表（外注加工・購入部品…）より前に入る。
			if m := tableMap[s.Name]; m.Caption != "" {
				if box, ok := d.Container(m.Caption); ok {
					anchor, before = box.Node(), false
				}
			}
			continue
		}
		switch {
		case s.Name == "図面":
			before = false
			// **図面1枚ごとに図面ブロックを1つ**（2026-09-29 利用者:「図面一枚一枚がブロック（Section）という
			// 構造は難しいですか？人間にはそのほうが分かりやすいですが。ブロックを消したり移動したり汎用的な操作
			// なので」「図面が複数あれば、図面用のタグも複数あるのかもしれません」）。それまでは ■図面 の画像も
			// PDF も1つのブロックに詰めていたので、1枚だけ消す・動かすができず、タグ（図面番号・図面名称）も
			// 1組しか持てなかった。解析の「二つ目の図面として追加」と同じ形——主な図面はテンプレートの図面
			// ブロックへ、ほかの図面はテンプレートの図面ブロックを写して1枚ずつ、主な図面の直後（改訂明細より前）へ。
			mainAt := -1
			for i, it := range s.Items {
				if hasMain && (it.Kind == "image" || it.Kind == "file") && readsInclude(it.Reads, m) {
					mainAt = i
					break
				}
			}
			placedMain := false
			last := blk.Node()
			for i, it := range s.Items {
				switch it.Kind {
				case "image", "file":
					u, ok := up("図面", it)
					if !ok {
						continue
					}
					ref := pageID + "-" + u.ID
					if !placedMain && (i == mainAt || mainAt < 0) {
						if !blk.SetFileView(ref) {
							appendHTML(blk.Node(), fileViewHTML(ref))
						}
						placedMain = true
						continue
					}
					// 1つのファイルに別の加工製品の図面も入っていたら、このページのものだけを名乗る（2026-09-30・ownReads）。
					reads, others := ownReads(it.Reads, m, hasMain)
					if len(others) > 0 {
						var where []string
						for _, o := range others {
							at := e.duplicate(o.No, pageID)
							if at == "" {
								at = "どのページにも無い——要るなら人が作る"
							}
							where = append(where, strings.TrimSpace(o.No+" "+o.Name)+"（"+at+"）")
						}
						note.ask("1つの PDF に、このページのものでない図面も入っていました——このページには名乗らせていません: " +
							strings.Join(where, "・"))
					}
					n, err := extraDrawingBlock(tmplTitle, tmpl, reads, machine, pl.Partner, ref)
					if err != nil {
						note.warn("⚠ 2枚目からの図面のブロックを作れません（主な図面のブロックへ入れました）: " + err.Error())
						appendHTML(blk.Node(), fileViewHTML(ref))
						continue
					}
					insertAfter(last, n)
					d.AssignBlockID(n)
					last = n
				case "text":
					appendHTML(blk.Node(), paragraphs(it.Text))
				case "table":
					appendHTML(blk.Node(), plainTableS(it.Rows, it.Struck))
				}
			}
			anchor = afterDrawing
		case tableMap[s.Name].Caption != "":
			m := tableMap[s.Name]
			box, ok := d.Container(m.Caption)
			if !ok {
				note.warn("⚠ テンプレートに「" + m.Caption + "」の節がありません——表は落としました")
				continue
			}
			// 表は**行き先を決めて集め**、ページの最後に1回で埋める（tables.go）。
			dests := e.tableDest[pageID][s.Name] // 人が決めた行き先（製造の設定.json の「表の行き先」）
			ti := 0
			var primary []string // この節でテンプレートの表へ入れた最初の表の見出し
			for _, it := range s.Items {
				switch it.Kind {
				case "table":
					if !tableHasData(it.Rows) {
						continue
					}
					rows, nObs := markObsolete(it.Rows, it.Struck)
					if nObs > 0 {
						note.info(fmt.Sprintf("「%s」の取り消し線の行 %d 行は、区分を「%s」にしました", s.Name, nObs, obsoleteValue))
					}
					dest := ""
					if ti < len(dests) {
						dest = strings.TrimSpace(dests[ti])
					}
					ti++
					if dest == keepPlain {
						appendHTML(box.Node(), plainTableS(it.Rows, it.Struck))
						continue
					}
					if dest != "" {
						dm, ok := tableByCaption(dest)
						if !ok {
							note.warn("⚠ 表の行き先「" + dest + "」はテンプレートの表ではありません——キャプションの無い表で残しました")
							appendHTML(box.Node(), plainTableS(it.Rows, it.Struck))
							continue
						}
						if pc, pr, ok := partsList(rows); ok && dest == "購入部品" {
							fills.add(dest, pc, pr, false)
							continue
						}
						if dest == "外注加工" {
							rows = forOutsourcing(rows)
						}
						cols, vals := mapTable(rows, dm.Rename, dm.Number)
						fills.add(dest, cols, vals, dm.Number)
						note.info("「" + s.Name + "」の " + fmt.Sprint(ti) + " 番目の表は、設定のとおり「" + dest + "」へ入れました")
						continue
					}
					// 見出しの無い2列の表（品名｜〇個）は購入部品（利用者:「○○は、下の表が購入部品のようです」）。
					if pc, pr, ok := partsList(rows); ok {
						fills.add("購入部品", pc, pr, false)
						note.ask("「" + s.Name + "」の見出しの無い2列の表（品名｜個数）を購入部品へ入れました——違っていれば" +
							settingsName + " の「表の行き先」で直せます")
						continue
					}
					cols, vals := mapTable(rows, m.Rename, m.Number)
					// 最初の表と、同じ見出しの表（取り消した表の代わりの新しい表など）はテンプレートの表へ。
					if primary == nil || sharesColumns(primary, cols, 2) {
						if primary != nil {
							note.info("「" + s.Name + "」の " + fmt.Sprint(ti) + " 番目の表は見出しが同じなので、「" + m.Caption + "」の表へまとめました")
						} else {
							primary = cols
						}
						fills.add(m.Caption, cols, vals, m.Number)
						continue
					}
					appendHTML(box.Node(), plainTableS(it.Rows, it.Struck))
					note.ask("「" + s.Name + "」に見出しの違う表があります——キャプションの無い表（DBに入らない）で残しました（行き先は " +
						settingsName + " の「表の行き先」で決められます・ページ番号 " + pageID + "）")
				case "text":
					appendHTML(box.Node(), paragraphs(it.Text))
					// 表の外の文——説明（※…）でなければ、表に入れるべき中身かもしれない（例: 購入品の
					// 「カラー かなめ商会 530円」が ■材料 の下に文で書かれている）。機械は文から行を作らない。
					// 利用者（2026-09-28）:「文で書かれた部品は人が表に入れます」——移すページの一覧として報告に出す。
					if !strings.HasPrefix(it.Text, "※") {
						note.todo("■" + s.Name + " の表の外に文があります（部品なら人が表へ移す）: " + firstLine(it.Text))
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
			nodes, err := htmldoc.ParseFragment(sectionHTML(pageID, s, up))
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
			if pastTables {
				for _, n := range nodes {
					d.Node().AppendChild(n)
				}
				continue
			}
			for _, n := range nodes {
				insertAfter(anchor, n)
				anchor = n
			}
		}
	}
	fills.fill(d, note)
	body := d.HTML()
	if dry {
		out := filepath.Join(root, previewDir)
		os.MkdirAll(out, 0o755)
		return os.WriteFile(filepath.Join(out, safeName(title, 80)+".html"), []byte(body), 0o644)
	}
	return c.save(pageID, token, body)
}

// sectionHTML はテンプレートに入れ物の無い節を、見出しの節の HTML にします（前書きは見出し無し）。
func sectionHTML(pageID string, s section, up func(string, item) (upload, bool)) string {
	var b strings.Builder
	// ■見積もり は原価の内訳——表の名前は「見積計算表」（2026-09-28 利用者:「見積計算表はどうでしょう？」）。
	// ⚠ 節の見出しも「見積計算表」に——「見積もり」のままだと w-cms の「見積もり」（売値）の形式として読まれる。
	heading := s.Name
	if s.Name == "見積もり" {
		heading = estimateTable
	}
	// 前書き（最初の■より前）は見出しを付けず、題のすぐ下に置く。
	if s.Name != "前書き" {
		b.WriteString("<section><h2>" + stdhtml.EscapeString(heading) + "</h2>")
	}
	for _, it := range s.Items {
		switch it.Kind {
		case "text":
			b.WriteString(paragraphs(it.Text))
		case "table":
			if s.Name == "見積もり" && tableHasData(it.Rows) {
				b.WriteString(captionTable(estimateTable, it.Rows, it.Struck))
			} else {
				b.WriteString(plainTableS(it.Rows, it.Struck))
			}
		case "image", "file":
			if u, ok := up(s.Name, it); ok {
				b.WriteString(mediaHTML(pageID, u, it))
			}
		}
	}
	if s.Name != "前書き" {
		b.WriteString("</section>")
	}
	return b.String()
}

// kindMachine は製造の記録の「種類」——ワンノートのページを装置名称のページに書いた印。
const kindMachine = "装置のページ"

// unknownMachine は装置の区別が無い品目の置き場の題です（取引先／社名／加工製品／不明・2026-09-29 利用者）。
const unknownMachine = "不明"

// buildMachineNote は「まとめ」のようなページを、装置名称のページ（取引先／社名／加工製品／装置名称）に書きます
// （2026-09-28 利用者:「装置名称のページに移植したら良いと思います。ワンノートはフォルダページに書くことが出来なかった
// ので、まとめページにしています」）。加工製品ではないので、テンプレートも Gemini も使わず、節を並びのまま置く。
//
//   - 書けるのは、装置のページが題だけ（作ったばかり）か「移行中」のタグが残っているときだけ——人が書いた装置のページは
//     触らない（報告に出す）。
//   - 同じ装置のページに、この回に別のページを書いたら書かない（上書きし合わない・報告に出す）。
//   - 前の製造で加工製品ページとして作っていたら（まだ「移行中」なら）ごみ箱へ移す。
func buildMachineNote(e *env, c *client, dir string, pl placement, pg *onePage, pr *pageRecord, note *pageNote, dry bool) error {
	machine := cms.NormalizeNameForIngest(pl.Machine)
	if machine == "" {
		// 装置の区別が無い取引先の「まとめ」は、品目と同じ「不明」の置き場のページに書く。
		machine = unknownMachine
	}
	if !dry && pr.WCMS != "" && pr.Kind != kindMachine {
		if body, ok := c.readBody(pr.WCMS); ok && strings.Contains(body, "<dt>"+migrateTag+"</dt>") {
			if err := c.deletePage(pr.WCMS); err != nil {
				return err
			}
			note.info("前に加工製品ページとして作った " + pr.WCMS + " をごみ箱へ移しました")
		}
		pr.WCMS, pr.Files = "", map[string]upload{}
	}
	pageID := "000000" // 下見の仮
	if !dry {
		id, err := c.productFolder(pl.Partner, machine)
		if err != nil {
			return err
		}
		pageID = id
		note.wcms = pageID
		if body, _ := c.readBody(pageID); !machinePageWritable(body) {
			note.warn("⚠ 装置のページ（/" + pageID + "）に人が書いた中身があるので触りません——このページの中身は人が移す")
			return nil
		}
		if t, ok := e.machineNotes[pageID]; ok {
			note.warn("⚠ 装置のページ（/" + pageID + "）には、この回にワンノートの「" + t + "」を書きました——こちらは書いていません（人が移す）")
			return nil
		}
		e.machineNotes[pageID] = pg.Title
		if pr.WCMS != pageID {
			pr.Files = map[string]upload{}
		}
	}
	pr.WCMS, pr.Kind = pageID, kindMachine
	note.info("装置のページ「" + machine + "」に書きました（加工製品ではない）")

	var token string
	if !dry {
		t, err := c.lock(pageID)
		if err != nil {
			return err
		}
		token = t
		defer c.unlock(pageID, token)
	}
	up, reportLeft := uploaderFor(c, e.root, dir, pageID, token, pr, note, dry)
	defer reportLeft()
	var b strings.Builder
	b.WriteString("<h1>" + stdhtml.EscapeString(machine) + "</h1>")
	b.WriteString(`<dl data-type="tags"><dt>` + migrateTag + `</dt><dd>` + migrateValue + `</dd></dl>`)
	for _, s := range sectionsOf(pg) {
		// **材料の空の表は落とす**（2026-09-29 利用者:「装置のページの材料の項目は表が空なら見出し表共に消して
		// よいです。表の中身があれば消さずに、表にキャプションを付けずDBに入らないようにしてください」）——
		// 空の表だけの節は下の s.empty() で見出しごと落ちる。文の残る節は見出しを残す（消すと文だけが前の節に
		// くっついて、何の話か分からなくなる）。中身のある表はキャプションを付けない（sectionHTML のまま）。
		if s.Name == "材料" {
			kept := s.Items[:0:0]
			for _, it := range s.Items {
				if it.Kind == "table" && !tableHasData(it.Rows) {
					continue
				}
				kept = append(kept, it)
			}
			s.Items = kept
		}
		if s.empty() {
			if s.Name != "前書き" {
				note.dropped(s.Name)
			}
			continue
		}
		b.WriteString(sectionHTML(pageID, s, up))
	}
	if dry {
		out := filepath.Join(e.root, previewDir)
		os.MkdirAll(out, 0o755)
		return os.WriteFile(filepath.Join(out, "装置 "+safeName(machine, 80)+".html"), []byte(b.String()), 0o644)
	}
	return c.save(pageID, token, b.String())
}

// machinePageWritable は装置のページに書いてよいかです——題だけ（作ったばかり）か「移行中」のタグが残っている。
func machinePageWritable(body string) bool {
	if strings.Contains(body, "<dt>"+migrateTag+"</dt>") {
		return true
	}
	rest := strings.TrimSpace(body)
	if i := strings.Index(rest, "</h1>"); strings.HasPrefix(rest, "<h1>") && i >= 0 {
		rest = strings.TrimSpace(rest[i+len("</h1>"):])
	}
	return rest == ""
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

// estimateTable は ■見積もり（原価の内訳）の表の名前です（利用者が決めた）。
const estimateTable = "見積計算表"

// firstLine は文の1行目です（報告に出す長さに）。
func firstLine(s string) string {
	l := strings.SplitN(strings.TrimSpace(s), "\n", 2)[0]
	if r := []rune(l); len(r) > 40 {
		l = string(r[:40]) + "…"
	}
	return l
}

// captionTable はキャプションで名乗る表です（表の写し＝DBに、この名前の表として入る）。
func captionTable(caption string, rows [][]string, struck [][]bool) string {
	return strings.Replace(plainTableS(rows, struck), "<table>", "<table><caption>"+stdhtml.EscapeString(caption)+"</caption>", 1)
}

// mediaHTML は節の中の画像（img）・添付（ファイル表示）です。
func mediaHTML(pageID string, u upload, it item) string {
	if it.Kind == "image" && it.Vector == "" {
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

// productFolder は `取引先／社名／加工製品／装置名称` をサーバーに用意させ、装置名称のページを返します
// （2026-09-29）。それまでは道具が木の道を自分で辿って作っていました——**段をやめた日に道具だけが段を
// 作り続ける**形なので、木の形はサーバー（整理と同じ ext/toho/product_tree.go）に任せます。
// 「加工製品」の箱はテンプレート「取引先の加工製品」から作られます（無ければ 409 で止まる）。
func (c *client) productFolder(partner, machine string) (string, error) {
	b, _ := json.Marshal(map[string]string{"customer": partner, "machine": machine})
	res, err := c.do("POST", "/api/product-folder", bytes.NewReader(b), "application/json", "")
	if err != nil {
		return "", err
	}
	defer res.Body.Close()
	var out struct {
		PageID  string `json:"page_id"`
		Message string `json:"message"`
	}
	json.NewDecoder(res.Body).Decode(&out)
	if res.StatusCode != 200 || out.PageID == "" {
		return "", fmt.Errorf("加工製品の置き場を用意できません（%d）: %s", res.StatusCode, out.Message)
	}
	return out.PageID, nil
}

// relinkResult は /api/order-items/relink の答えです（ext/toho/order_relink.go）。
type relinkResult struct {
	Pages   int      `json:"pages"`
	Rows    int      `json:"rows"`
	Editing []string `json:"editing"`
	Links   []struct {
		Order   string `json:"order"`
		Code    string `json:"code"`
		Product string `json:"product"`
		Title   string `json:"title"`
		Rows    int    `json:"rows"`
	} `json:"links"`
}

// relinkOrders は受注の行を、いま在る加工製品ページへ結び直します（2026-09-30 夜）。結ぶのはサーバー——歯止め
// （候補がちょうど1件のときだけ・人の入れた値は触らない・編集中の受注ページは飛ばす）も同じ口の中。
func (c *client) relinkOrders() (relinkResult, error) {
	var out relinkResult
	res, err := c.do("POST", "/api/order-items/relink", nil, "application/json", "")
	if err != nil {
		return out, err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 {
		b, _ := io.ReadAll(res.Body)
		return out, fmt.Errorf("%d: %s", res.StatusCode, strings.TrimSpace(string(b)))
	}
	if err := json.NewDecoder(res.Body).Decode(&out); err != nil {
		return out, err
	}
	return out, nil
}

// markdown は報告の節です。⚠ 結び先の題も書く——【旧】の付いたページに結んでよいかは人が見る。
func (r relinkResult) markdown() string {
	var b strings.Builder
	b.WriteString(fmt.Sprintf("## 受注の行を結んだ（%d行・受注 %d枚を見た）\n\n", r.Rows, r.Pages))
	if len(r.Links) == 0 {
		b.WriteString("新しく結んだ行はありません。\n")
	}
	for _, l := range r.Links {
		b.WriteString(fmt.Sprintf("- /%s の品番 %s → /%s %s（%d行）\n", l.Order, l.Code, l.Product, l.Title, l.Rows))
	}
	if len(r.Editing) > 0 {
		b.WriteString("- ⚠ 編集中で飛ばした受注ページ: /" + strings.Join(r.Editing, "・/") +
			"（閉じてから製造をもう一度流すと結ばれます）\n")
	}
	return b.String() + "\n"
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

// deletePage はページをごみ箱へ移します（編集ロックを取ってから・誰かが編集中なら断られる）。
func (c *client) deletePage(id string) error {
	token, err := c.lock(id)
	if err != nil {
		return err
	}
	res, err := c.do("POST", "/api/delete-page?id="+id, nil, "", token)
	if err != nil {
		c.unlock(id, token)
		return err
	}
	defer res.Body.Close()
	if res.StatusCode != 200 && res.StatusCode != 204 {
		c.unlock(id, token)
		msg, _ := io.ReadAll(res.Body)
		return fmt.Errorf("ページを消せません（%s: %d %s）", id, res.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
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
	title     string
	wcms      string
	duplicate bool // 同じ図面番号のページがあるので作らなかった
	infos []string
	warns []string
	asks  []string
	todos []string
	drops []string
}

func (n *pageNote) info(s string) { n.infos = append(n.infos, s) }
func (n *pageNote) warn(s string) { n.warns = append(n.warns, s) }
func (n *pageNote) ask(s string)  { n.asks = append(n.asks, s) }
func (n *pageNote) todo(s string) { n.todos = append(n.todos, s) }
func (n *pageNote) dropped(s string) {
	n.drops = append(n.drops, s)
}

type report struct {
	pages  []*pageNote
	skips  []string
	notes  []string      // 回全体の注意（道具が無い・鍵が無い）
	relink *relinkResult // 受注の行を結び直した結果（本番の回だけ）
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
	if r.relink != nil {
		b.WriteString(r.relink.markdown())
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
		for _, s := range p.asks {
			b.WriteString("- 【聞きたい】" + s + "\n")
		}
		for _, s := range p.todos {
			b.WriteString("- 【人の手】" + s + "\n")
		}
		if len(p.drops) > 0 {
			b.WriteString("- 中身の無い見出しを落としました: " + strings.Join(p.drops, "・") + "\n")
		}
		b.WriteString("\n")
	}
	return b.String()
}


// uploaderFor は1つのページへファイルを上げる口です（まだ上げていないものだけ・製造の記録に覚える）。
// 2つ目の関数は、前の製造で上げて今は使っていないファイルを報告に出す（終わりに呼ぶ）。
func uploaderFor(c *client, root, dir, pageID, token string, pr *pageRecord, note *pageNote, dry bool) (
	func(secName string, it item) (upload, bool), func()) {
	// 上げる（まだ上げていないものだけ）。名前は見える名前（節＋番号・添付は元の名前）。
	// 印刷イメージは XPS から切り出した PDF があればそちら（名前は「図面1.pdf」のように）。
	counter := map[string]int{}
	used := map[string]bool{}
	up := func(secName string, it item) (upload, bool) {
		key, path := it.File, ""
		if it.Vector != "" {
			key, path = "pdf:"+filepath.Base(it.Vector), it.Vector
		} else if it.File != "" {
			path = filepath.Join(root, dir, "files", it.File)
		} else {
			note.warn("⚠ " + secName + " の画像が吸い出せていません")
			return upload{}, false
		}
		used[key] = true
		st, err := os.Stat(path)
		if err != nil || st.Size() == 0 {
			if u, ok := pr.Files[key]; ok {
				return u, true // 前に上げたもの（いまは読めなくても、上げた分はある）
			}
			note.warn("⚠ " + secName + " のファイルが無いか空です（吸い出し直しで取れれば入ります）: " + filepath.Base(path))
			return upload{}, false
		}
		sum := fileSHA(path)
		if u, ok := pr.Files[key]; ok {
			if u.SHA == "" || sum == "" || u.SHA == sum {
				if u.SHA == "" && sum != "" {
					u.SHA = sum // 指紋を取る前の記録——いまの中身を覚える（上げ直しはしない）
					pr.Files[key] = u
				}
				return u, true
			}
			note.info("「" + filepath.Base(path) + "」は中身が変わったので上げ直しました（前のファイルは添付に残ります）")
		}
		shown := it.Name
		switch {
		case it.Vector != "":
			counter[secName]++
			shown = fmt.Sprintf("%s%d.pdf", secName, counter[secName])
		case it.Kind == "image":
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
			return upload{ID: "xxxx", URL: "/" + pageID + "/" + filepath.Base(path)}, true
		}
		u, err := c.upload(pageID, token, path, shown)
		if err != nil {
			note.warn("⚠ " + shown + " を上げられません: " + err.Error())
			return upload{}, false
		}
		u.SHA = sum
		pr.Files[key] = u
		return u, true
	}
	reportLeft := func() {
		// 前の製造で上げて、いまは使っていないファイル（添付には版が無く消せない——-fresh で作り直すと残らない）。
		left := 0
		for k := range pr.Files {
			if !used[k] {
				left++
			}
		}
		if left > 0 {
			note.info(fmt.Sprintf("前の製造で上げたファイルが %d 個、使われずにページに残っています（-fresh で作り直すと残りません）", left))
		}
	}
	return up, reportLeft
}
// fileSHA はファイルの中身の指紋（sha256 の16進）です。読めなければ空。
func fileSHA(path string) string {
	f, err := os.Open(path)
	if err != nil {
		return ""
	}
	defer f.Close()
	h := sha256.New()
	if _, err := io.Copy(h, f); err != nil {
		return ""
	}
	return hex.EncodeToString(h.Sum(nil))
}
