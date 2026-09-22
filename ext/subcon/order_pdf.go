package subcon

// ─────────────────────────────────────────────────────────────────────────
// 発注書のPDF発行（2026-09-21）
//
// ユーザー:「**PDFを発行出来ることはMUSTです。** 発注書はPDFで送付し**7年保存**する
// 必要があるそうです」「ボタンを押したら印刷出来たいです。**メールで発注を増やしたい**
// ですが、**FAXで発注も残っています**」。
//
// ⚠ **どの経路も「PDFという1つのファイル」を作れることが前提**です（印刷・メール添付・
// FAX送信）。ブラウザの「PDFに保存」では人の手が毎回挟まり、自動送信に繋がりません。
// **だから外部依存を1つ増やしました**（`signintech/gopdf`・MIT・ユーザー承認済み。
// 経緯は [docs/【考察】発注書のPDF発行.md]）。
//
// ⚠ **できたPDFは、そのページの添付として保存します。** 7年保存はこれで済みます
// ——ファイルがページと一緒に残り、版も監査も付きます。
//
// ⚠ **「エラーが出なかった＝正しく出た」ではありません。** 作ったPDFは `ToUnicode` を
// 読み返して中身を確かめること（2026-09-21 朝に印刷で同じ失敗をしました）。
// ─────────────────────────────────────────────────────────────────────────

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"os"
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

// ErrNoPDFFont は「PDF用の日本語フォントが設定されていない」です。
//
// ⚠ **起動は止めません**（Gemini キーと同じ扱い）。PDFを押したときにだけ、
// 何をすればよいかを画面に出します。
var ErrNoPDFFont = errors.New(
	"PDF用の日本語フォントが設定されていません" +
		"（config/settings.json の extensions.subcon.pdf_font に .ttf のパスを書いてください。" +
		"⚠ .ttc は読めません）")

// 紙の寸法（A4・単位はポイント）。実物の発注書に寄せています。
const (
	pdfLeft   = 40.0
	pdfRight  = 555.0
	pdfTop    = 50.0
	pdfLine   = 16.0
	pdfFontSz = 9.0
)

// orderPDFColumn は明細表の1列です。
type orderPDFColumn struct {
	Label string
	Width float64
	Right bool // 数は右寄せ（金額が揃わないと読めません）
}

// OrderPDFAPIHandler は POST /api/order-pdf です（入力: {page_id}）。
//
// できたPDFを**そのページの添付として保存**し、開くためのURLを返します。
func OrderPDFAPIHandler(w http.ResponseWriter, r *http.Request) {
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
	pageID, okID := page.NormalizeID(req.PageID)
	if !okID {
		cms.JSONFail(w, http.StatusBadRequest, "ページIDが不正です")
		return
	}
	// 添付を足す操作なので write 権限（本文は変えないので編集ロックは要りません
	// ——解析・取り込みと同じ理屈）。
	if !page.RequirePageWrite(w, r, pageID) {
		return
	}
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		cms.JSONFail(w, http.StatusNotFound, "ページを読めません: "+err.Error())
		return
	}
	pdf, err := buildOrderPDF(body, user)
	if err != nil {
		code := http.StatusBadRequest
		if errors.Is(err, ErrNoPDFFont) {
			code = http.StatusServiceUnavailable
		}
		cms.JSONFail(w, code, err.Error())
		return
	}
	// ⚠ **名前に日付を入れます**——同じページで作り直すたびに増えるので、
	// どれがいつのものか分からないと困ります（添付は上書きされません）。
	name := "発注書 " + time.Now().Format("20060102-150405") + ".pdf"
	attachID, fileName, err := cms.SaveAttachmentFrom(pageID, user.Username, name, "pdf", pdf)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "保存できません: "+err.Error())
		return
	}
	auth.Audit(user.Username, "order-pdf", pageID+" "+fileName)
	json.NewEncoder(w).Encode(map[string]any{
		"success": true, "attach_id": attachID, "file": fileName,
		"url": "/" + pageID + "/" + fileName,
	})
}

// buildOrderPDF は発注書ページの本文からPDFを組みます。
func buildOrderPDF(body string, viewer *auth.User) ([]byte, error) {
	font := PDFFont()
	if font == "" {
		return nil, ErrNoPDFFont
	}
	if _, err := os.Stat(font); err != nil {
		return nil, fmt.Errorf("%w（いまの設定: %s）", ErrNoPDFFont, font)
	}
	head, rows, cols, err := readOrderDoc(body)
	if err != nil {
		return nil, err
	}

	p := &gopdf.GoPdf{}
	p.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	p.AddPage()
	if err := p.AddTTFFont("jp", font); err != nil {
		return nil, fmt.Errorf("フォントを読めません（%s）: %w", font, err)
	}
	if err := p.SetFont("jp", "", pdfFontSz); err != nil {
		return nil, err
	}

	y := pdfTop
	// ── 題と宛名 ──
	y = pdfText(p, pdfLeft+180, y, 16, "発注書")
	y += 6
	// ⚠ **`SupplierTag` を通すこと**（2026-09-21 に直した）。ここは生の文字列で
	// `発注先` と書いてあり、**コードの他のどこにも無い言葉**でした——ページが持つのは
	// `仕入先`（`SupplierTag`）なので、**「御中」の前がずっと空**だったはずです。
	// ⚠ **試験が不具合を固定していました**——`pdfOrderBody` も `発注先` と書いていたので、
	// 読み返しの番人は緑のまま。**生の文字列は、取り違えてもコンパイルが通ります。**
	y = pdfText(p, pdfLeft, y, 12, head[SupplierTag]+"　御中")
	y += 4

	// ── 差出人（右側）──
	ry := pdfTop + 10
	for _, ln := range senderLines(head, viewer) {
		ry = pdfText(p, 330, ry, pdfFontSz, ln)
	}
	if ry > y {
		y = ry
	}
	y += 8

	// ── ヘッダの4項目 ──
	for _, k := range []string{"発注書番号", "発注日", "納期", "納品場所"} {
		if v := head[k]; v != "" {
			y = pdfText(p, pdfLeft, y, pdfFontSz, k+"： "+v)
		}
	}
	y += 6

	// ── 明細表 ──
	y = pdfTable(p, y, cols, rows)
	y += 4

	// ── 合計 ──
	total := 0
	for _, r := range rows {
		total += cms.VocabNumber(r["数量"]) * cms.VocabNumber(r["単価"])
	}
	pdfTextRight(p, pdfRight, y, 11, "合計金額（税抜）　"+comma(total)+" 円")
	y += pdfLine + 6

	// ── 備考 ──
	if n := head["備考"]; n != "" {
		pdfText(p, pdfLeft, y, pdfFontSz, "備考： "+n)
	}

	var buf bytes.Buffer
	if _, err := p.WriteTo(&buf); err != nil {
		return nil, err
	}
	return buf.Bytes(), nil
}

// pdfText は1行書いて、次の行のyを返します。
func pdfText(p *gopdf.GoPdf, x, y, size float64, s string) float64 {
	if strings.TrimSpace(s) == "" {
		return y
	}
	p.SetFont("jp", "", size)
	p.SetXY(x, y)
	p.Cell(nil, s)
	return y + size + 4
}

// pdfTextRight は右端を揃えて書きます（⚠ 金額は揃っていないと読めません）。
func pdfTextRight(p *gopdf.GoPdf, right, y, size float64, s string) {
	p.SetFont("jp", "", size)
	w, _ := p.MeasureTextWidth(s)
	p.SetXY(right-w, y)
	p.Cell(nil, s)
}

// pdfTable は明細表を組みます（線と文字を自分で置きます）。
func pdfTable(p *gopdf.GoPdf, y float64, cols []orderPDFColumn, rows []map[string]string) float64 {
	p.SetLineWidth(0.5)
	// 見出し
	x := pdfLeft
	p.SetFont("jp", "", pdfFontSz)
	for _, c := range cols {
		p.SetXY(x+2, y+3)
		p.Cell(nil, c.Label)
		p.Line(x, y, x, y+pdfLine)
		x += c.Width
	}
	p.Line(pdfLeft, y, x, y)
	p.Line(x, y, x, y+pdfLine)
	p.Line(pdfLeft, y+pdfLine, x, y+pdfLine)
	y += pdfLine

	for _, r := range rows {
		x = pdfLeft
		for _, c := range cols {
			v := r[c.Label]
			if c.Label == "金額" {
				v = comma(cms.VocabNumber(r["数量"]) * cms.VocabNumber(r["単価"]))
			} else if c.Right && v != "" {
				v = comma(cms.VocabNumber(v))
			}
			if v != "" {
				if c.Right {
					p.SetFont("jp", "", pdfFontSz)
					w, _ := p.MeasureTextWidth(v)
					p.SetXY(x+c.Width-w-2, y+3)
				} else {
					p.SetXY(x+2, y+3)
				}
				p.Cell(nil, v)
			}
			p.Line(x, y, x, y+pdfLine)
			x += c.Width
		}
		p.Line(x, y, x, y+pdfLine)
		p.Line(pdfLeft, y+pdfLine, x, y+pdfLine)
		y += pdfLine
	}
	return y
}

// comma は桁区切りを入れます（⚠ 実物が `122,580` と書いているので揃えます）。
func comma(n int) string {
	s := strconv.Itoa(n)
	neg := strings.HasPrefix(s, "-")
	s = strings.TrimPrefix(s, "-")
	var out []string
	for len(s) > 3 {
		out = append([]string{s[len(s)-3:]}, out...)
		s = s[:len(s)-3]
	}
	out = append([]string{s}, out...)
	r := strings.Join(out, ",")
	if neg {
		r = "-" + r
	}
	return r
}

// readOrderDoc は発注書ページの本文から、ヘッダ・明細・**紙に出す列**を読み出します。
//
// ⚠ **全行が空の列は落とします**（ユーザーの実物に合わせて）。材料の発注書に `色` は
// 出ず、塗装の発注書に `材質` は出ません——**形式は1つのまま、紙だけ種類ごとに違う**
// 形にするための仕掛けです。受注残表の印刷で「画面と紙で別のHTMLを組まない」と
// 決めたのと同じ考えで、**組むのは1つ、落とすのは出すとき**。
//
// ⚠ **`弊社品番` は紙に出しません**——相手には意味が無く、**こちらが問い合わせを
// 受けたときに引くための番号**です（ユーザー:「入れる場所があるなら弊社品番も
// 入れたい」）。
func readOrderDoc(body string) (head map[string]string, rows []map[string]string,
	cols []orderPDFColumn, err error) {
	nodes, perr := htmldoc.ParseFragment(body)
	if perr != nil {
		return nil, nil, nil, errors.New("本文を読めません")
	}
	head = map[string]string{}
	var table *html.Node
	var walk func(*html.Node)
	walk = func(n *html.Node) {
		if n.Type == html.ElementNode {
			if n.Data == "dl" && cms.Attr(n, "data-type") == "tags" {
				var key string
				for c := n.FirstChild; c != nil; c = c.NextSibling {
					if c.Type != html.ElementNode {
						continue
					}
					if c.Data == "dt" {
						key = strings.TrimSpace(textOf(c))
					} else if c.Data == "dd" && key != "" {
						head[key] = strings.TrimSpace(textOf(c))
					}
				}
			}
			if n.Data == "table" && table == nil && isTableOfType(n, ourOrderItemsType) {
				table = n
			}
		}
		for c := n.FirstChild; c != nil; c = c.NextSibling {
			walk(c)
		}
	}
	for _, n := range nodes {
		walk(n)
	}
	if table == nil {
		return nil, nil, nil, errors.New("発注明細の表がありません（このページは発注書ではないようです）")
	}

	trs := rowsOf(table)
	if len(trs) < 2 {
		return nil, nil, nil, errors.New("発注明細に行がありません")
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
		if any {
			rows = append(rows, r)
		}
	}
	if len(rows) == 0 {
		return nil, nil, nil, errors.New("発注明細に中身のある行がありません")
	}

	// 紙に出す列を決める。⚠ **並びと幅は実物に寄せます**。
	want := []orderPDFColumn{
		{Label: "品番", Width: 95},
		{Label: "品名", Width: 150},
		{Label: "材質", Width: 70},
		{Label: "形状", Width: 60},
		{Label: "寸法", Width: 110},
		{Label: "色", Width: 70},
		{Label: "単位", Width: 32},
		{Label: "数量", Width: 40, Right: true},
		{Label: "単価", Width: 55, Right: true},
		{Label: "金額", Width: 65, Right: true},
	}
	for _, c := range want {
		if c.Label == "金額" {
			cols = append(cols, c) // ⚠ 金額は計算なので本文に無くても出します
			continue
		}
		used := false
		for _, r := range rows {
			if strings.TrimSpace(r[c.Label]) != "" {
				used = true
				break
			}
		}
		if used {
			cols = append(cols, c)
		}
	}
	return head, rows, cols, nil
}
