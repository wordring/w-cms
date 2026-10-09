package toho

// ─────────────────────────────────────────────────────────────────────────
// 外注加工の資料を発注書に添える（2026-09-28）
//
// 利用者:「加工製品のページに、外注加工ごとに資料のブロック（開いたり閉じたりできる）を用意して、
// そこに保存したファイルをメールやFAX、印刷等に追加できるようにしてはどうでしょう？顧客の図面も
// そこに入っているならそのまま渡します」。決まった形（【考察】部材の種類ごとの発注項目 §12）:
//
//	加工製品ページ                                   発注書ページ（発注明細）
//	  外注加工の表  番号 1 | レーザー切断 …           弊社品番 000235 | 番号 1 | …
//	  ▼ 資料 1   ← 題の「1」が番号                          │
//	     📄 図面.pdf（通信記録の添付を指す）   ←─────────────┘ 行の 弊社品番＋番号 で辿る
//	     📄 展開.dxf
//
//   - **ブロックは本文の折りたたみ**（`<details><summary>資料 1</summary>`）。名乗りは題の言葉
//     （表が caption で名乗るのと同じ）。中のファイルはコアの `cms.FileRefsIn` が拾う
//     （ファイル表示の印・📎 リンク・画像）。**指す先は別のページでもよい**。
//   - **メール**: 送信欄に候補を並べ、全部チェックした状態で出す（人が外せる）。
//   - **FAX・印刷**: 発注書のうしろに PDF のページと画像を綴じた**別の1本**を作る
//     （`/api/order-pdf-docs`）。⚠ **発注書のPDF（7年保存の正本）には綴じない**。
//     ⚠ 紙にできない形式（DXF・CAD・Excel・ZIP）は綴じられないので、そう言う。
//   - 新しい列は要らない——発注明細の行は `弊社品番` と `番号` を持つ（§11）。
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
)

// docsFoldWord は資料の折りたたみの題の言葉です（「資料 1」の「資料」）。
const docsFoldWord = "資料"

// orderDoc は発注書に添える資料のファイル1つです。
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

// orderTableRows は発注明細の表から、中身のある行を見出し→値で返します。
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

// orderDocs は発注明細の行から資料を集めます。notes は人に見せる一言（資料の無い行・読めないファイル）。
//
// 行が指すのは **`弊社品番` のページの「資料 <番号>」の折りたたみ**。番号の無い行（材料・購入部品）は
// 見ません。同じ行が2度出ても、同じファイルが2つの行から指されても、1つにします。
func orderDocs(user *auth.User, rows []map[string]string) (docs []orderDoc, notes []string) {
	seenLine := map[string]bool{}
	seenFile := map[string]bool{}
	for _, r := range rows {
		no := strings.TrimSpace(r["番号"])
		pid, ok := page.NormalizeID(strings.TrimSpace(r[OurItemNoTag]))
		if no == "" || !ok {
			continue
		}
		line := orderLineNo(pid, no)
		if seenLine[line] {
			continue
		}
		seenLine[line] = true
		fold := docsFoldIn(user, pid, no)
		if fold == nil {
			notes = append(notes, line+" は資料がありません（加工製品ページに「"+docsFoldWord+" "+no+"」の折りたたみが無い）")
			continue
		}
		for _, ref := range cms.FileRefsIn(fold) {
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
// 発注書のうしろに資料（PDF のページ・画像）を綴じた **FAX・印刷用の1本**を作り、そのページの添付として
// 保存して開くURLを返します。⚠ **本文は触りません**（表示中の発注書のPDFは差し替えない）——
// だから編集中でも断りません（write 権限とテンプレートの外、だけを見る）。
func OrderPDFDocsAPIHandler(w http.ResponseWriter, r *http.Request) {
	serveBoundPaper(w, r, boundKind{
		rows: func(body string) ([]map[string]string, error) {
			_, rows, _, err := readOrderDoc(body)
			return rows, err
		},
		build: buildOrderPDF,
		name:  func(rows []map[string]string, _ string) string { return orderPaperTitle(rows) },
		audit: "order-pdf-docs",
	})
}

// boundKind は資料を綴じた1本（FAX・印刷用）の、紙の種類ごとに違うところです（発注書・見積依頼書）。
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
	pageID, ok := cms.PageIDOrFail(w, req.PageID)
	if !ok || !page.RequirePageWrite(w, r, pageID) || !cms.RefuseTemplateArea(w, pageID) {
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
	docs, notes := orderDocs(user, rows)
	if !hasPrintableDoc(docs) {
		cms.JSONFail(w, http.StatusBadRequest, "紙に綴じられる資料（PDF・画像）がありません")
		return
	}
	paper, err := k.build(body, user)
	if err != nil {
		failPaper(w, err)
		return
	}
	bound, skipped, err := bindOrderDocs(paper, docs)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "綴じられません: "+err.Error())
		return
	}
	name := k.name(rows, pageID) + "＋資料 " + time.Now().Format("20060102-150405") + ".pdf"
	attachID, fileName, err := cms.SaveAttachmentFrom(pageID, user.Username, name, "pdf", bound)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "保存できません: "+err.Error())
		return
	}
	auth.Audit(user.Username, k.audit, pageID+" "+fileName)
	cms.WriteJSON(w, map[string]any{
		"success": true, "attach_id": attachID, "file": fileName,
		"url": "/" + pageID + "/" + fileName, "skipped": append(skipped, notes...),
	})
}

// bindOrderDocs は発注書のPDFのうしろに資料を綴じます。skipped は綴じなかったものの一言。
//
// ⚠ **資料のページは元の大きさのまま**綴じます（A3 の図面を A4 に縮めない——縮めるかは
// 印刷・FAX の側が決める）。⚠ **読めないPDFは飛ばします**——gofpdi は壊れた・暗号化された
// PDF で落ちることがあるので、**先に捨てる器で試し**、通ったものだけを本番の器へ入れます
// （本番の器の途中で落ちると、それまで綴じた分まで壊れるため）。
func bindOrderDocs(orderPDF []byte, docs []orderDoc) ([]byte, []string, error) {
	p := &gopdf.GoPdf{}
	p.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	if err := importPDFPages(p, orderPDF); err != nil {
		return nil, nil, fmt.Errorf("発注書のPDFを読み直せません: %w", err)
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
