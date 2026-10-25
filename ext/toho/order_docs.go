package toho

// ─────────────────────────────────────────────────────────────────────────
// 外注加工の資料を部材発注書に添える（2026-09-28）
//
// 利用者:「加工製品のページに、外注加工ごとに資料のブロック（開いたり閉じたりできる）を用意して、
// そこに保存したファイルをメールやFAX、印刷等に追加できるようにしてはどうでしょう？顧客の図面も
// そこに入っているならそのまま渡します」。決まった形（【考察】部材の種類ごとの発注項目 §12）:
//
//	加工製品ページ                                   部材発注書ページ（部材発注明細表）
//	  外注加工表  番号 1 | レーザー切断 …           弊社品番 000235 | 番号 1 | …
//	  ▼ 資料 1   ← 題の「1」が番号                          │
//	     📄 図面.pdf（通信記録の添付を指す）   ←─────────────┘ 行の 弊社品番＋番号 で辿る
//	     📄 展開.dxf
//
//   - **ブロックは本文の折りたたみ**（`<details><summary>資料 1</summary>`）。名乗りは題の言葉
//     （表が caption で名乗るのと同じ）。中のファイルはコアの `cms.FileRefsIn` が拾う
//     （ファイル表示の印・📎 リンク・画像）。**指す先は別のページでもよい**。
//   - **メール**: 送信欄に候補を並べ、全部チェックした状態で出す（人が外せる）。
//   - **FAX・印刷**: 部材発注書のうしろに PDF のページと画像を綴じた**別の1本**を作る
//     （`/api/order-pdf-docs`）。⚠ **部材発注書のPDF（7年保存の正本）には綴じない**。
//     ⚠ 紙にできない形式（DXF・CAD・Excel・ZIP）は綴じられないので、そう言う。
//   - 新しい列は要らない——部材発注明細表の行は `弊社品番` と `番号` を持つ（§11）。
//
// ⚠ **2026-10-19 から、資料は外注加工表の「資料」の列で指します**（【考察】部材の種類ごとの発注項目 §12a）。
// 利用者:「資料の置き場をページ内に一つにして、資料一つ一つにいつものIDを付けましょう。ページID-いつものIDで資料を特定し、その連結IDを空白文字や「、」で区切って表の該当セルに羅列しておけば良いのでは？」
//
//	加工製品ページ
//	  外注加工表  番号 1 | レーザー切断 | … | 資料 000040-dr01、000235-k2pq
//	  ▼ 資料     ← 置き場はページ内に1つ（中のファイルの ID を「🔗 ID」で写してセルに並べる）
//
//   - 部材発注明細表の行（`弊社品番`＋`種類`）から、その種類の元の表（設定 `order_kinds` の from）の行を、
//     **手配済みを数える鍵と同じ鍵**で当てます（外注加工なら 番号＋加工内容）。番号の無い行も当たります。
//   - そのセルが空なら、それまでの折りたたみ「資料 <番号>」を読みます（移行のあいだ）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"bytes"
	"fmt"
	"image"
	_ "image/jpeg" // 綴じる画像の大きさを読む
	_ "image/png"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/signintech/gopdf"
	"golang.org/x/net/html"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/htmldoc"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// docsFoldWord は資料の折りたたみの題の言葉です（「資料 1」の「資料」）。
const docsFoldWord = "資料"

// docsColumnLabel は、元の表（外注加工表など）で資料のファイルの ID を並べる列の名前です（2026-10-19）。
const docsColumnLabel = "資料"

// orderDoc は部材発注書に添える資料のファイル1つです。
type orderDoc struct {
	PageID string // 添付のあるページ（6桁）
	File   string // 保存名
	Name   string // 届いたときの名前
	Line   string // どの行の資料か（弊社品番-番号）
}

// printable は紙に綴じられる形式か（PDF と、gopdf が置ける画像）です。
func (d orderDoc) printable() bool {
	switch strings.ToLower(filepath.Ext(d.File)) {
	case ".pdf", ".png", ".jpg", ".jpeg":
		return true
	}
	return false
}

// orderTableRows は部材発注明細表から、中身のある行を見出し→値で返します。
// **取り消した行は数えるだけで返しません**（紙に刷らない・資料も添えない——order_status.go）。
func orderTableRows(table *html.Node) (rows []map[string]string, cancelled int) {
	trs := rowsOf(table)
	if len(trs) < 2 {
		return nil, 0
	}
	labels := cellTexts(trs[0])
	for _, tr := range trs[1:] {
		r := map[string]string{}
		any := false
		for i, v := range cellTexts(tr) {
			if i >= len(labels) {
				break
			}
			r[labels[i]] = v
			if v != "" {
				any = true
			}
		}
		if !any {
			continue
		}
		if orderLineCancelled(r["状態"]) {
			cancelled++
			continue
		}
		rows = append(rows, r)
	}
	return rows, cancelled
}

// orderDocs は部材発注明細表の行から資料を集めます。notes は人に見せる一言（資料の無い行・読めないファイル）。
//
// 行が指すのは、**`弊社品番` のページの元の表の行の「資料」の列**（並べたファイルの ID・2026-10-19 から）。
// そのセルが空なら、**「資料 <番号>」の折りたたみ**です（それまでの形）。
// 資料の列を持たない種類（材料など）で番号の無い行は見ません。
// 同じ行が2度出ても、同じファイルが2つの行から指されても、1つにします。
func orderDocs(user *auth.User, rows []map[string]string) (docs []orderDoc, notes []string) {
	seenLine := map[string]bool{}
	seenFile := map[string]bool{}
	for _, r := range rows {
		no := strings.TrimSpace(r["番号"])
		pid, ok := page.NormalizeID(strings.TrimSpace(r[OurItemNoTag]))
		if !ok {
			continue
		}
		cell, hasColumn := docsCellOfLine(user, pid, r)
		if no == "" && !hasColumn {
			continue
		}
		line := orderLineNo(pid, no)
		if no == "" {
			line = pid + "（" + strings.TrimSpace(r["加工内容"]+" "+r["品名"]) + "）"
		}
		if seenLine[line] {
			continue
		}
		seenLine[line] = true
		var refs []cms.FileRef
		if ids := cms.SplitRefList(cell); len(ids) > 0 {
			for _, s := range ids {
				ref, ok := cms.ParseFileRef(s)
				if !ok {
					notes = append(notes, line+" の資料 "+s+" はファイルの ID（ページ番号-添付ID）ではありません")
					continue
				}
				refs = append(refs, ref)
			}
		} else {
			var fold *html.Node
			if no != "" {
				fold = docsFoldIn(user, pid, no)
			}
			if fold == nil {
				if no != "" {
					notes = append(notes, line+" は資料がありません（外注加工表の「"+docsColumnLabel+"」が空で、加工製品ページに「"+docsFoldWord+" "+no+"」の折りたたみも無い）")
				} else {
					notes = append(notes, line+" は資料がありません（外注加工表の「"+docsColumnLabel+"」が空）")
				}
				continue
			}
			refs = cms.FileRefsIn(fold)
		}
		for _, ref := range refs {
			stored, name, ok := cms.AttachmentOfRef(user, ref)
			if !ok {
				notes = append(notes, line+" の資料 "+ref.PageID+"-"+ref.ID+" を読めません")
				continue
			}
			key := ref.PageID + "/" + stored
			if seenFile[key] {
				continue
			}
			seenFile[key] = true
			docs = append(docs, orderDoc{PageID: ref.PageID, File: stored, Name: name, Line: line})
		}
	}
	return docs, notes
}

// orderLineNo は外注加工の行の呼び名（紙に刷る番号）です——`弊社品番-番号`（2026-09-28 決定）。
// 既に弊社品番で始まる番号（人が手で書いた）はそのまま。
func orderLineNo(productID, no string) string {
	if productID == "" || strings.HasPrefix(no, productID) {
		return no
	}
	return productID + "-" + no
}

// docsCellOfLine は、部材発注明細表の行 r に当たる、加工製品ページ pid の元の表の行の「資料」のセルを返します。
// hasColumn は、その種類の元の表が「資料」の列を持つか（持たない種類は資料を添えない）。
//
// 当て方は、**手配済みを数える鍵と同じ**です（設定 `order_kinds` の key・`orderKind.keyOf`）——
// 必要部材表から発注書へ運んだ行は、その鍵で元の行と結ばれているからです。読めないページは見ません。
func docsCellOfLine(user *auth.User, pid string, r map[string]string) (cell string, hasColumn bool) {
	k, ok := orderKindByName(r["種類"])
	if !ok {
		return "", false
	}
	def, ok := cms.VocabDefByHeading(k.From)
	if !ok {
		return "", false
	}
	for _, c := range def.Columns {
		if c.Label == docsColumnLabel {
			hasColumn = true
		}
	}
	if !hasColumn {
		return "", false
	}
	idInt, err := strconv.Atoi(pid)
	if err != nil || !page.CanView(user, idInt) {
		return "", true
	}
	want := k.keyOf(r)
	if want == "" {
		return "", true
	}
	src, err := cms.VocabTableRowsOf(database.DB, idInt, def.Type)
	if err != nil {
		return "", true
	}
	var tags map[string][]string
	for _, m := range src {
		if tags == nil {
			tags, _ = cms.TagsOfPage(database.DB, idInt)
		}
		if k.keyOf(k.valuesOf(def, m, tags, idInt)) == want {
			return labelValue(def, m, docsColumnLabel), true
		}
	}
	return "", true
}

// docsFoldIn は加工製品ページの「資料 <番号>」の折りたたみを返します（読めない・無いなら nil）。
//
// 題の比べ方は**空白と全角半角を畳んで**——「資料 1」「資料1」「資料　１」は同じ。
func docsFoldIn(user *auth.User, productID, no string) *html.Node {
	idInt, err := strconv.Atoi(productID)
	if err != nil || !page.CanView(user, idInt) {
		return nil
	}
	body, err := cms.ReadPageBody(productID)
	if err != nil {
		return nil
	}
	nodes, err := htmldoc.ParseFragment(body)
	if err != nil {
		return nil
	}
	want := foldKey(docsFoldWord + no)
	var found *html.Node
	for _, n := range nodes {
		cms.WalkElements(n, func(el *html.Node) {
			if found == nil && el.Data == "details" && foldKey(foldTitle(el)) == want {
				found = el
			}
		})
	}
	return found
}

// foldTitle は折りたたみの題（直接の子の summary）の文字です。
func foldTitle(details *html.Node) string {
	for c := details.FirstChild; c != nil; c = c.NextSibling {
		if c.Type == html.ElementNode && c.Data == "summary" {
			return textOf(c)
		}
	}
	return ""
}

// foldKey は題を比べる形へ畳みます（NFKC・空白を除く）。
func foldKey(s string) string {
	return strings.Join(strings.Fields(cms.NormalizeText(s)), "")
}

// hasPrintableDoc は紙に綴じられる資料が1つでもあるかです（FAX・印刷用のボタンを出すか）。
func hasPrintableDoc(docs []orderDoc) bool {
	for _, d := range docs {
		if d.printable() {
			return true
		}
	}
	return false
}

// OrderPDFDocsAPIHandler は POST /api/order-pdf-docs です（入力: {page_id}）。
//
// 部材発注書のうしろに資料（PDF のページ・画像）を綴じた **FAX・印刷用の1本**を作り、そのページの添付として
// 保存して開くURLを返します。⚠ **本文は触りません**（表示中の部材発注書のPDFは差し替えない）——
// だから編集中でも断りません（write 権限とテンプレートの外、だけを見る）。
func OrderPDFDocsAPIHandler(w http.ResponseWriter, r *http.Request) {
	serveBoundPaper(w, r, orderBoundKind)
}

// orderBoundKind は部材発注書の、資料を綴じた1本の作り方です（この口と、FAX で送る紙——fax_paper.go）。
var orderBoundKind = boundKind{
	rows: func(body string) ([]map[string]string, error) {
		_, rows, _, err := readOrderDoc(body)
		return rows, err
	},
	build: buildOrderPDF,
	name:  func(rows []map[string]string, _ string) string { return orderPaperTitle(rows) },
	audit: "order-pdf-docs",
}

// boundKind は資料を綴じた1本（FAX・印刷用）の、紙の種類ごとに違うところです（部材発注書・部材見積依頼書）。
type boundKind struct {
	check func(pageID string) string                          // 作れないページなら理由（nil か空なら作る）
	rows  func(body string) ([]map[string]string, error)       // 明細の行（資料はその行が指す加工製品ページのもの）
	build func(body string, viewer *auth.User) ([]byte, error) // 綴じる前の紙
	name  func(rows []map[string]string, pageID string) string // 添付の名前の頭（うしろに「＋資料 日時.pdf」）
	audit string
}

// serveBoundPaper は資料を綴じた1本を作る口（POST・入力 {page_id}）の共通の形です。
//
// ⚠ **本文は触りません**（表示中の紙の PDF は差し替えない）——だから編集中でも断らず、write 権限とテンプレートの
// 外だけを見ます。
func serveBoundPaper(w http.ResponseWriter, r *http.Request, k boundKind) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req struct {
		PageID string `json:"page_id"`
	}
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	// 書ける・テンプレートの外・道具が握っていない（2026-10-20 まで、道具が握っているかを見ていなかった——ほかの口と同じ gateWritablePage に）。
	pageID, ok := gateWritablePage(w, r, req.PageID)
	if !ok {
		return
	}
	if k.check != nil {
		if why := k.check(pageID); why != "" {
			cms.JSONFail(w, http.StatusBadRequest, why)
			return
		}
	}
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		cms.JSONFail(w, http.StatusNotFound, "ページを読めません: "+err.Error())
		return
	}
	rows, err := k.rows(body)
	if err != nil {
		cms.JSONFail(w, http.StatusBadRequest, err.Error())
		return
	}
	if docs, _ := orderDocs(user, rows); !hasPrintableDoc(docs) {
		cms.JSONFail(w, http.StatusBadRequest, "紙に綴じられる資料（PDF・画像）がありません")
		return
	}
	paper, err := k.build(body, user)
	if err != nil {
		failPaper(w, err)
		return
	}
	b, err := bindAndSave(user, pageID, rows, paper, k.name(rows, pageID), k.audit)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	cms.WriteJSON(w, map[string]any{
		"success": true, "attach_id": b.AttachID, "file": b.File,
		"url": "/" + pageID + "/" + b.File, "skipped": b.Skipped,
	})
}

// boundPaper は、資料を綴じてページの添付に残した1本です（Skipped は綴じなかったものの一言）。
type boundPaper struct {
	AttachID, File, Name string
	Skipped              []string
}

// bindAndSave は、紙 paper のうしろに明細の行 rows が指す資料を綴じ、ページ pageID の添付に残します
// （名前は「<namePrefix>＋資料 日時.pdf」）。綴じられる資料があるかは呼ぶ側が見る（「資料を綴じる」の口と、FAX で送る紙——
// fax_paper.go——で共通・2026-10-25 夜に寄せた）。
func bindAndSave(user *auth.User, pageID string, rows []map[string]string, paper []byte, namePrefix, audit string) (boundPaper, error) {
	docs, notes := orderDocs(user, rows)
	bound, skipped, err := bindOrderDocs(paper, docs)
	if err != nil {
		return boundPaper{}, fmt.Errorf("綴じられません: %w", err)
	}
	name := namePrefix + "＋資料 " + time.Now().Format("20060102-150405") + ".pdf"
	attachID, fileName, err := cms.SaveAttachmentFrom(pageID, user.Username, name, "pdf", bound)
	if err != nil {
		return boundPaper{}, fmt.Errorf("保存できません: %w", err)
	}
	auth.Audit(user.Username, audit, pageID+" "+fileName)
	return boundPaper{AttachID: attachID, File: fileName, Name: name, Skipped: append(skipped, notes...)}, nil
}

// bindOrderDocs は部材発注書のPDFのうしろに資料を綴じます。skipped は綴じなかったものの一言。
//
// ⚠ **資料のページは元の大きさのまま**綴じます（A3 の図面を A4 に縮めない——縮めるかは
// 印刷・FAX の側が決める）。⚠ **読めないPDFは飛ばします**——gofpdi は壊れた・暗号化された
// PDF で落ちることがあるので、**先に捨てる器で試し**、通ったものだけを本番の器へ入れます
// （本番の器の途中で落ちると、それまで綴じた分まで壊れるため）。
func bindOrderDocs(orderPDF []byte, docs []orderDoc) ([]byte, []string, error) {
	p := &gopdf.GoPdf{}
	p.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	if err := importPDFPages(p, orderPDF); err != nil {
		return nil, nil, fmt.Errorf("部材発注書のPDFを読み直せません: %w", err)
	}
	var skipped []string
	for _, d := range docs {
		if !d.printable() {
			skipped = append(skipped, d.Name+" は紙にできません（メールでだけ渡せます）")
			continue
		}
		path, ok := page.AttachmentPath(d.PageID, d.File)
		if !ok {
			skipped = append(skipped, d.Name+" が見つかりません")
			continue
		}
		data, err := os.ReadFile(path)
		if err != nil {
			skipped = append(skipped, d.Name+" を読めません")
			continue
		}
		if strings.EqualFold(filepath.Ext(d.File), ".pdf") {
			trial := &gopdf.GoPdf{}
			trial.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
			if err := importPDFPages(trial, data); err != nil {
				skipped = append(skipped, d.Name+" は綴じられません（PDFを読めません: "+err.Error()+"）")
				continue
			}
			if err := importPDFPages(p, data); err != nil {
				return nil, nil, fmt.Errorf("%s を綴じる途中で失敗しました: %w", d.Name, err)
			}
			continue
		}
		if err := placeImagePage(p, data); err != nil {
			skipped = append(skipped, d.Name+" は綴じられません（画像を読めません）")
		}
	}
	var buf bytes.Buffer
	if _, err := p.WriteTo(&buf); err != nil {
		return nil, nil, err
	}
	return buf.Bytes(), skipped, nil
}

// importPDFPages は PDF の全ページを、それぞれの大きさのページとして足します（落ちたら err）。
func importPDFPages(p *gopdf.GoPdf, data []byte) (err error) {
	defer func() {
		if rec := recover(); rec != nil {
			err = fmt.Errorf("%v", rec)
		}
	}()
	// 展開すると大きすぎる PDF（展開爆弾）は gofpdi に渡さない（pdf_inflate_guard.go・2026-10-26）。
	if !pdfInflateWithinBudget(data) {
		return fmt.Errorf("中身を広げると大きすぎます（%d MB を超える）", pdfInflateBudget>>20)
	}
	rs := io.ReadSeeker(&guardedReader{Reader: bytes.NewReader(data),
		deadline: time.Now().Add(pdfImportTimeLimit)})
	sizes := p.GetStreamPageSizes(&rs)
	if len(sizes) == 0 {
		return fmt.Errorf("ページがありません")
	}
	for no := 1; no <= len(sizes); no++ {
		box, ok := sizes[no]["/MediaBox"]
		if !ok {
			return fmt.Errorf("%d ページ目の大きさが分かりません", no)
		}
		w, h := box["w"], box["h"]
		p.AddPageWithOption(gopdf.PageOption{PageSize: &gopdf.Rect{W: w, H: h}})
		tpl := p.ImportPageStream(&rs, no, "/MediaBox")
		p.UseImportedTemplate(tpl, 0, 0, w, h)
	}
	return nil
}

// placeImagePage は画像を A4 の1ページに、余白の内側へ縦横比を保って置きます（横長なら横向きの紙）。
func placeImagePage(p *gopdf.GoPdf, data []byte) error {
	cfg, _, err := image.DecodeConfig(bytes.NewReader(data))
	if err != nil || cfg.Width == 0 || cfg.Height == 0 {
		return fmt.Errorf("画像を読めません")
	}
	// 大きさを名乗るだけの小さな画像（復号すると何 GB にもなる）は綴じない（2026-10-26・取り込みの JPEG と同じ上限）。
	if err := cms.CheckImageSize(cfg.Width, cfg.Height); err != nil {
		return err
	}
	holder, err := gopdf.ImageHolderByBytes(data)
	if err != nil {
		return err
	}
	pw, ph := gopdf.PageSizeA4.W, gopdf.PageSizeA4.H
	if cfg.Width > cfg.Height {
		pw, ph = ph, pw
	}
	const margin = 28.0
	scale := (pw - 2*margin) / float64(cfg.Width)
	if s := (ph - 2*margin) / float64(cfg.Height); s < scale {
		scale = s
	}
	w, h := float64(cfg.Width)*scale, float64(cfg.Height)*scale
	p.AddPageWithOption(gopdf.PageOption{PageSize: &gopdf.Rect{W: pw, H: ph}})
	return p.ImageByHolder(holder, (pw-w)/2, (ph-h)/2, &gopdf.Rect{W: w, H: h})
}

// pdfImportTimeLimit は1つのPDFを読むのに許す時間です（図面の束でも数秒で済む）。
const pdfImportTimeLimit = 20 * time.Second

// pdfImportMaxEOF は、読み終わりを越えて読みに来てよい回数です（まともなPDFでは数回）。
const pdfImportMaxEOF = 10000

// guardedReader は gofpdi に渡す読み口です——⚠ **gofpdi は壊れたPDFで終わらない繰り返しに入ります**
// （2026-09-28 に試験で踏んだ）。字句を読む関数が**読み終わりで空の字句を返す**ので、探す字句
// （`startxref` など）が無いと同じ所を回り続けます——サーバーではCPUを1つ食い潰したまま戻りません。
// 繰り返しは9か所あり、手前で形を検査しても塞ぎ切れないので、**読み口で止めます**: 読み終わりを
// 何度も越えて読みに来たら・時間を過ぎたら panic し、`importPDFPages` の recover が理由に変えます。
// ⚠ 止めた gopdf の器は捨てます（だから本番の器の前に、捨てる器で試す——`bindOrderDocs`）。
type guardedReader struct {
	*bytes.Reader
	eofs     int
	deadline time.Time
}

func (g *guardedReader) Read(b []byte) (int, error) {
	if time.Now().After(g.deadline) {
		panic("読み終わりません（時間切れ・壊れたPDFの疑い）")
	}
	n, err := g.Reader.Read(b)
	if err == io.EOF {
		if g.eofs++; g.eofs > pdfImportMaxEOF {
			panic("ファイルの終わりを越えて読み続けています（壊れたPDF）")
		}
	}
	return n, err
}
