package toho

// ─────────────────────────────────────────────────────────────────────────
// 現品票（2026-10-30・docs/考察/【考察】現品票の印刷.md）
//
// > 2026-10-23 利用者:「ubuntuにブラザーql800を繋げて現品票を印刷しようと思います。納品用と塗装、メッキ等の表面処理用途なります。」
// > 2026-10-30 利用者:「現品票は上下62ミリで横に長く使っています。大きな文字で印刷しています」
//
// 品物や箱に貼る札を、業務サーバーの QL-800 で刷ります。札の中身は w-cms にもう書いてある行から作ります（人が書き写さない）。
//
//   - **納品用**: 納品書ページの納品明細表の行から（客先の正式名称・品番・品名・数量・注文番号・納品日・弊社名・QR）。
//   - **表面処理用**: 部材発注書ページの部材発注明細表の、外注加工の行から（業者・品番・加工部品番号・表面と仕様・数量・納期・
//     発注書番号・戻り先・QR）。取り消した行は出さない。
//   - 札は横長（長さ genpinLengthMM × ロールの幅 62mm）。PDF で組み、ext/label が白黒の絵にしてプリンターの道具へ渡す。
//   - 行ごとに枚数・箱の分け方（「20,20」→「1/2 箱 20」「2/2 箱 20」）・一言を決められる。
//   - 刷ったことは監査の記録だけに残す（問い5の答え・本文は変えない）。QR はそのページを開く住所（画面の QR と同じ表記）。
//
//	GET  /api/genpin?page_id=X                                 … {kind, rows:[{row, item, name, qty, unit, sub}], printer}
//	GET  /api/genpin/preview?page_id=X&row=N&item=…&boxes=…&note=… … 1枚目の見本（PNG・刷るのと同じ白黒）
//	POST /api/genpin/print   {page_id, items:[{row, item, copies, boxes, note}]} … 刷る
// ─────────────────────────────────────────────────────────────────────────

import (
	"context"
	"errors"
	stdhtml "html"
	"math"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/signintech/gopdf"

	"w-cms/ext/comm/contacts"
	"w-cms/ext/label"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// genpinLengthMM は札の長さ（mm）です。62mm のロールを横に長く使う（2026-10-30 利用者）。
const genpinLengthMM = 100

// genpinMaxLabels は1度に刷る札の上限です（道具と同じ）。
const genpinMaxLabels = 50

const (
	genpinKindDelivery = "delivery" // 納品用
	genpinKindSurface  = "surface"  // 表面処理用
)

// genpinRow は札を作れる1行です。
type genpinRow struct {
	Row  int    `json:"row"`  // 表の中身の行の番号（0 から・取り消した行を除いた並び）
	Item string `json:"item"` // 品番（行の取り違えを確かめる）
	Name string `json:"name"`
	Qty  string `json:"qty"`
	Unit string `json:"unit"`
	Sub  string `json:"sub"` // 一覧に添える言葉（加工部品番号・表面 など）

	cols map[string]string
}

// genpinDoc は、札を作るページの中身です。
type genpinDoc struct {
	Kind   string
	Head   map[string]string
	Rows   []genpinRow
	Signer string // 署名を引く担当のタグの値
}

// readGenpinDoc は、ページの本文から札を作れる行を読みます（納品書か部材発注書）。
func readGenpinDoc(body string) (genpinDoc, error) {
	if head, rows, err := readDeliveryDoc(body); err == nil {
		d := genpinDoc{Kind: genpinKindDelivery, Head: head, Signer: head[DeliverySignerTag]}
		for i, r := range rows {
			d.Rows = append(d.Rows, genpinRow{Row: i, Item: r[ItemNoTag], Name: r["品名"], Qty: r["数量"], Unit: r["単位"],
				Sub: r[OrderNoTag], cols: r})
		}
		if len(d.Rows) == 0 {
			return d, errors.New("納品明細表に行がありません")
		}
		return d, nil
	}
	head, rows, _, err := readOrderDoc(body)
	if err != nil {
		return genpinDoc{}, errors.New("現品票は、納品書と部材発注書のページから刷れます")
	}
	d := genpinDoc{Kind: genpinKindSurface, Head: head, Signer: head[OrderSignerTag]}
	n := 0
	for _, r := range rows {
		if !isSurfaceRow(r) {
			continue
		}
		sub := strings.TrimSpace(r["加工部品番号"] + " " + r["表面"])
		d.Rows = append(d.Rows, genpinRow{Row: n, Item: r[ItemNoTag], Name: r["品名"], Qty: r["数量"], Unit: r["単位"], Sub: sub, cols: r})
		n++
	}
	if len(d.Rows) == 0 {
		return d, errors.New("部材発注明細表に外注加工の行がありません（現品票は外注加工の行から刷れます）")
	}
	return d, nil
}

// isSurfaceRow は、部材発注明細表の行が表面処理の札にできる外注加工の行かを返します
// （種類が空の行は、表面か仕様が書いてあれば外注加工とみる——手で書いた行）。
func isSurfaceRow(r map[string]string) bool {
	kind := strings.TrimSpace(r["種類"])
	if kind == "外注加工" {
		return true
	}
	return kind == "" && (strings.TrimSpace(r["表面"]) != "" || strings.TrimSpace(r["仕様"]) != "")
}

// genpinButtonHTML は表の下の「🏷 現品票」です（押すと画面が欄を開く——toho.js）。読める人なら押せる（刷っても本文は変わらない）。
func genpinButtonHTML(pageID string) string {
	return `<button type="button" class="chip-btn genpin-go" data-genpin-page="` + stdhtml.EscapeString(pageID) + `">🏷 現品票</button>`
}

// genpinPageOf は、user が読める札のページを読みます。
func genpinPageOf(w http.ResponseWriter, user *auth.User, raw string) (string, genpinDoc, bool) {
	pageID, ok := page.NormalizeID(raw)
	if !ok || !page.CanView(user, pageNum(pageID)) {
		cms.JSONFail(w, http.StatusNotFound, "ページが見つかりません")
		return "", genpinDoc{}, false
	}
	if cms.IsTemplateArea(pageID) {
		cms.JSONFail(w, http.StatusBadRequest, "テンプレートからは刷れません")
		return "", genpinDoc{}, false
	}
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, "ページを読めません")
		return "", genpinDoc{}, false
	}
	d, err := readGenpinDoc(body)
	if err != nil {
		cms.JSONFail(w, http.StatusBadRequest, err.Error())
		return "", genpinDoc{}, false
	}
	return pageID, d, true
}

// GenpinAPIHandler は GET /api/genpin?page_id=X です。
func GenpinAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		cms.JSONFail(w, http.StatusMethodNotAllowed, "GET だけです")
		return
	}
	user := auth.CurrentUser(r)
	if user == nil {
		cms.JSONFail(w, http.StatusUnauthorized, "ログインしてください")
		return
	}
	_, d, ok := genpinPageOf(w, user, r.URL.Query().Get("page_id"))
	if !ok {
		return
	}
	cms.WriteJSON(w, map[string]any{"success": true, "kind": d.Kind, "rows": d.Rows, "length_mm": genpinLengthMM})
}

// genpinPick は、刷る1行の頼みです。
type genpinPick struct {
	Row    int    `json:"row"`
	Item   string `json:"item"`
	Copies int    `json:"copies"`
	Boxes  string `json:"boxes"`
	Note   string `json:"note"`
}

// genpinLabel は札1枚の中身です。
type genpinLabel struct {
	Kind  string
	Top   string // いちばん上（「現品票」と相手）
	Big   string // 大きく（品番）
	Sub   string // その下（品名・加工部品番号）
	Work  string // 表面処理の中身
	Qty   string // 数量（箱に分けたときはその箱の数）
	Box   string // 「1/2 箱」
	Note  string // 一言
	Foot  []string
	QRURL string
}

// splitBoxes は「20,20」「20、20」「20 20」を数の並びにします（空なら nil）。
func splitBoxes(s string) ([]string, error) {
	s = strings.NewReplacer("、", ",", "，", ",", " ", ",", "　", ",").Replace(strings.TrimSpace(s))
	var out []string
	for _, f := range strings.Split(s, ",") {
		if f = strings.TrimSpace(f); f == "" {
			continue
		}
		if v, err := strconv.ParseFloat(normalizeDigits(f), 64); err != nil || v <= 0 {
			return nil, errors.New("箱に分ける数が読めません: " + f)
		}
		out = append(out, normalizeDigits(f))
	}
	if len(out) > genpinMaxLabels {
		return nil, errors.New("箱が多すぎます")
	}
	return out, nil
}

// normalizeDigits は全角の数字を半角にします。
func normalizeDigits(s string) string {
	return strings.Map(func(r rune) rune {
		if r >= '０' && r <= '９' {
			return r - '０' + '0'
		}
		if r == '．' {
			return '.'
		}
		return r
	}, s)
}

// genpinLabelsOf は1行の札を、箱と枚数の分だけ作ります。
func genpinLabelsOf(d genpinDoc, row genpinRow, pick genpinPick, top, company, qrURL string) ([]genpinLabel, error) {
	boxes, err := splitBoxes(pick.Boxes)
	if err != nil {
		return nil, err
	}
	if len(boxes) > 0 {
		if q, err := strconv.ParseFloat(normalizeDigits(row.Qty), 64); err == nil {
			sum := 0.0
			for _, b := range boxes {
				v, _ := strconv.ParseFloat(b, 64)
				sum += v
			}
			if math.Abs(sum-q) > 1e-9 {
				return nil, errors.New(row.Item + ": 箱の数の合計（" + trimFloat(sum) + "）が数量（" + row.Qty + "）と合いません")
			}
		}
	}
	copies := pick.Copies
	if copies < 1 {
		copies = 1
	}
	if copies > genpinMaxLabels {
		return nil, errors.New("枚数が多すぎます")
	}
	base := genpinLabel{Kind: d.Kind, Top: top, Big: row.Item, Note: strings.TrimSpace(pick.Note), QRURL: qrURL}
	c := row.cols
	switch d.Kind {
	case genpinKindDelivery:
		base.Sub = row.Name
		var f []string
		if no := c[OrderNoTag]; no != "" {
			f = append(f, "注文番号 "+no)
		}
		date := d.Head[DeliveredAtTag]
		if date == "" {
			date = time.Now().Format("2006-01-02")
		}
		f = append(f, "納品日 "+date)
		base.Foot = []string{strings.Join(f, "　"), company}
	default:
		base.Sub = strings.TrimSpace(strings.Join(nonEmpty(c["加工部品番号"], c["加工部品名"]), "　"))
		if base.Sub == "" {
			base.Sub = row.Name
		}
		base.Work = strings.Join(nonEmpty(c["表面"], c["仕様"], c["加工内容"]), "　")
		var f []string
		if due := d.Head[DueDateTag]; due != "" {
			f = append(f, "納期 "+due)
		}
		if no := d.Head[PurchaseNoTag]; no != "" {
			f = append(f, "発注書 "+no)
		}
		base.Foot = []string{strings.Join(f, "　"), "戻り先 " + company}
	}
	unit := row.Unit
	qtys := boxes
	if len(qtys) == 0 {
		qtys = []string{normalizeDigits(row.Qty)}
	}
	var out []genpinLabel
	for i, q := range qtys {
		l := base
		l.Qty = strings.TrimSpace(q + " " + unit)
		if len(boxes) > 1 {
			l.Box = strconv.Itoa(i+1) + "/" + strconv.Itoa(len(boxes)) + " 箱"
		}
		for k := 0; k < copies; k++ {
			out = append(out, l)
		}
	}
	return out, nil
}

func nonEmpty(ss ...string) []string {
	var out []string
	for _, s := range ss {
		if s = strings.TrimSpace(s); s != "" {
			out = append(out, s)
		}
	}
	return out
}

func trimFloat(v float64) string { return strconv.FormatFloat(v, 'f', -1, 64) }

// genpinTop は札のいちばん上の言葉です（客先は連絡帳の正式名称）。
func genpinTop(d genpinDoc, viewer *auth.User) string {
	if d.Kind == genpinKindDelivery {
		if c := d.Head[DeliveryClientTag]; c != "" {
			return "現品票　" + contacts.OfficialName(viewer, c) + "　御中"
		}
		return "現品票"
	}
	if s := d.Head[SupplierTag]; s != "" {
		return "表面処理　現品票　" + contacts.OfficialName(viewer, s) + "　様"
	}
	return "表面処理　現品票"
}

// genpinCompany は弊社の名前です（担当の署名の1行目——紙の差出人と同じ）。
func genpinCompany(d genpinDoc, viewer *auth.User) string {
	if lines := senderLines(map[string]string{OrderSignerTag: d.Signer}, viewer); len(lines) > 0 {
		return lines[0]
	}
	return ""
}

// genpinLabelsFor は頼みの全部の札を作ります。
func genpinLabelsFor(d genpinDoc, picks []genpinPick, viewer *auth.User, qrURL string) ([]genpinLabel, error) {
	top, company := genpinTop(d, viewer), genpinCompany(d, viewer)
	var out []genpinLabel
	for _, pk := range picks {
		if pk.Row < 0 || pk.Row >= len(d.Rows) || strings.TrimSpace(d.Rows[pk.Row].Item) != strings.TrimSpace(pk.Item) {
			return nil, errors.New("表が変わりました。開き直してから刷ってください")
		}
		ls, err := genpinLabelsOf(d, d.Rows[pk.Row], pk, top, company, qrURL)
		if err != nil {
			return nil, err
		}
		out = append(out, ls...)
		if len(out) > genpinMaxLabels {
			return nil, errors.New("1度に刷れるのは " + strconv.Itoa(genpinMaxLabels) + " 枚までです")
		}
	}
	if len(out) == 0 {
		return nil, errors.New("刷る行を選んでください")
	}
	return out, nil
}

// ── 札の紙（PDF） ────────────────────────────────────────────────

const genpinMargin = 6.0

// buildGenpinPDF は札を1枚1ページの PDF にします（横長・高さはロールの印字の幅）。
func buildGenpinPDF(labels []genpinLabel) ([]byte, error) {
	font, err := paperFont()
	if err != nil {
		return nil, err
	}
	w := float64(genpinLengthMM) / 25.4 * 72
	h := label.HeightPt(label.Dots62)
	p := &gopdf.GoPdf{}
	p.Start(gopdf.Config{PageSize: gopdf.Rect{W: w, H: h}})
	if err := addPDFFont(p, font); err != nil {
		return nil, err
	}
	for _, l := range labels {
		p.AddPage()
		drawGenpin(p, l, w, h)
	}
	return finishPaper(p)
}

// fitText は s を maxW に収まる大きさ（max から min まで）で x, y に書き、次の y を返します。min でも入らなければ末尾を落とす。
func fitText(p *gopdf.GoPdf, x, y, maxW, max, min float64, s string) float64 {
	s = strings.TrimSpace(s)
	if s == "" {
		return y
	}
	size := max
	for ; size > min; size -= 1 {
		p.SetFont("jp", "", size)
		if tw, _ := p.MeasureTextWidth(s); tw <= maxW {
			break
		}
	}
	p.SetFont("jp", "", size)
	rs := []rune(s)
	for len(rs) > 1 {
		if tw, _ := p.MeasureTextWidth(string(rs)); tw <= maxW {
			break
		}
		rs = append(rs[:len(rs)-2], '…')
	}
	pdfInk(p, x, y, size, string(rs))
	return y + size + 2
}

// drawGenpin は札1枚を描きます。右下に QR、左に字を上から積みます。
func drawGenpin(p *gopdf.GoPdf, l genpinLabel, w, h float64) {
	const qrSize = 58.0
	x := genpinMargin
	textW := w - 2*genpinMargin - qrSize - 6
	full := w - 2*genpinMargin
	y := genpinMargin
	y = fitText(p, x, y, full, 10, 7, l.Top)
	p.SetLineWidth(0.8)
	p.Line(x, y, w-genpinMargin, y)
	y += 3
	y = fitText(p, x, y, full, 30, 14, l.Big)
	y = fitText(p, x, y, full, 14, 8, l.Sub)
	if l.Work != "" {
		y = fitText(p, x, y, textW, 16, 9, l.Work)
	}
	qty := "数量　" + l.Qty
	if l.Box != "" {
		qty += "　（" + l.Box + "）"
	}
	y = fitText(p, x, y, textW, 22, 10, qty)
	if l.Note != "" {
		y = fitText(p, x, y, textW, 12, 8, l.Note)
	}
	footY := h - genpinMargin - 2*10.0
	if y > footY {
		footY = y
	}
	for _, f := range l.Foot {
		footY = fitText(p, x, footY, textW, 8, 6, f)
	}
	if l.QRURL != "" {
		if m, err := cms.NewQR(l.QRURL); err == nil {
			drawQR(p, m, w-genpinMargin-qrSize, h-genpinMargin-qrSize, qrSize)
		}
	}
}

// drawQR は QR を size の正方形に描きます（静かな余白は札の余白で足りる）。
func drawQR(p *gopdf.GoPdf, m *cms.QRMatrix, x, y, size float64) {
	cell := size / float64(m.Size)
	p.SetFillColor(0, 0, 0)
	for j := 0; j < m.Size; j++ {
		for i := 0; i < m.Size; i++ {
			if m.At(i, j) {
				p.RectFromUpperLeftWithStyle(x+float64(i)*cell, y+float64(j)*cell, cell+0.05, cell+0.05, "F")
			}
		}
	}
}

// ── 口 ─────────────────────────────────────────────────────────

// genpinRequest は見本と刷るの頼みです。
type genpinRequest struct {
	PageID string       `json:"page_id"`
	Items  []genpinPick `json:"items"`
	genpinPick
}

// genpinRender は頼みの札を、白黒の絵にします。
func genpinRender(w http.ResponseWriter, r *http.Request, user *auth.User, req genpinRequest, previewOnly bool) (string, []label.Bitmap, bool) {
	pageID, d, ok := genpinPageOf(w, user, req.PageID)
	if !ok {
		return "", nil, false
	}
	picks := req.Items
	if previewOnly {
		picks = []genpinPick{req.genpinPick}
	}
	labels, err := genpinLabelsFor(d, picks, user, cms.RequestBaseURL(r)+"/"+pageID)
	if err != nil {
		cms.JSONFail(w, http.StatusBadRequest, err.Error())
		return "", nil, false
	}
	if previewOnly {
		labels = labels[:1]
	}
	pdf, err := buildGenpinPDF(labels)
	if err != nil {
		failPaper(w, err)
		return "", nil, false
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	bms, err := label.Render(ctx, pdf, label.Dots62)
	if err != nil {
		cms.JSONFail(w, http.StatusServiceUnavailable, err.Error())
		return "", nil, false
	}
	return pageID, bms, true
}

// GenpinPreviewAPIHandler は GET /api/genpin/preview?page_id=X&row=N&item=…&boxes=…&note=… です（1枚目の見本の PNG）。
// 画面は <img> で出す（CSP が blob: の絵を許さないので、同じ住所の GET にした）。本文も何も変えない。
func GenpinPreviewAPIHandler(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodGet {
		cms.JSONFail(w, http.StatusMethodNotAllowed, "GET だけです")
		return
	}
	user := auth.CurrentUser(r)
	if user == nil {
		cms.JSONFail(w, http.StatusUnauthorized, "ログインしてください")
		return
	}
	q := r.URL.Query()
	row, _ := strconv.Atoi(q.Get("row"))
	req := genpinRequest{PageID: q.Get("page_id"), genpinPick: genpinPick{Row: row, Item: q.Get("item"), Copies: 1, Boxes: q.Get("boxes"), Note: q.Get("note")}}
	_, bms, ok := genpinRender(w, r, user, req, true)
	if !ok {
		return
	}
	png, err := bms[0].PNG()
	if err != nil {
		cms.JSONFail(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "image/png")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(png)
}

// GenpinPrintAPIHandler は POST /api/genpin/print です。
func GenpinPrintAPIHandler(w http.ResponseWriter, r *http.Request) {
	user, ok := cms.GateJSONPost(w, r)
	if !ok {
		return
	}
	var req genpinRequest
	if !cms.DecodeJSONBody(w, r, &req) {
		return
	}
	if !label.Available() { // 絵にする前に断る（道具が無いのに mutool を回さない）
		cms.JSONFail(w, http.StatusServiceUnavailable, label.ErrUnavailable.Error())
		return
	}
	pageID, bms, ok := genpinRender(w, r, user, req, false)
	if !ok {
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	n, err := label.Print(ctx, bms)
	if n > 0 {
		auth.Audit(user.Username, "genpin.print", pageID+" "+strconv.Itoa(n)+"枚")
	}
	if err != nil {
		msg := err.Error()
		if n > 0 {
			msg = strconv.Itoa(n) + " 枚刷ったところで止まりました: " + msg
		}
		cms.JSONFail(w, http.StatusServiceUnavailable, msg)
		return
	}
	cms.WriteJSON(w, map[string]any{"success": true, "printed": n})
}
