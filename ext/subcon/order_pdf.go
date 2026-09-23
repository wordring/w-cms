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
	// ⚠ **余白を詰めました**（2026-09-22 → 09-23）。A4 は幅 595pt なので、
	//    32pt は約 11mm——**家庭用・業務用どちらのプリンタでも入る**範囲です。
	//    広げたぶんは**字を大きくするために使います**。
	pdfLeft  = 32.0
	pdfRight = 563.0
	pdfTop   = 50.0
	// pdfBottom は紙の下の限界です（A4 は高さ 842pt）。
	//
	// ⚠ **これを越えたら改ページします**（2026-09-23）。それまでは**描き続けるだけ**で、
	// ⚠ **溢れた行は黙って消えていました**——字を大きくして行が高くなったぶん、
	// 起きやすくなります。**行の落丁は、紙を見ても気づけません。**
	pdfBottom = 790.0
	// pdfFontSz は**表以外**（ヘッダの項目・差出人・備考）の字です。
	//
	// ⚠ **表の字は固定していません**——中身を測って**入る中でいちばん大きい**
	// ものを選びます（`pdfFontSteps`・`fitTableColumns`）。
	pdfFontSz = 10.5
	// pdfLine は表以外の行送りです（表の行送りは字の大きさから出します）。
	pdfLine = 16.0
)

// pdfFontSteps は明細表の字の候補です（**大きい順に試します**）。
//
// ⚠ **FAXで潰れるのが出発点です**（2026-09-22 ユーザー:「**発注書のPDFが文字が
// 小さすぎてFAXで送ると潰れるような気がします**」）。G3 FAX の解像度は
// **203×98dpi（標準）**で、⚠ **縦が 98dpi しかありません**——9pt の和文は
// 縦12走査線ほどしかなく、画数の多い漢字（`鋼`・`鍍`・`厚`）は塗り潰れます。
//
// ⚠ **だから「入るなら大きく」です。** 12pt から順に試し、**入らなくなったところで
// 1段下げます**。⚠ **下限は 9pt**——それまでの大きさなので、**どんな中身でも
// 今より小さくはなりません**。
var pdfFontSteps = []float64{12, 11.5, 11, 10.5, 10, 9.5, 9}

// pdfCellPad はセルの左右の余白です（線と字がくっつかないように）。
const pdfCellPad = 4.0

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
	// 添付を足す操作なので write 権限。
	// ⚠ **2026-09-22 から本文も触ります**（PDFを開くマーカーを置く）。それまでは
	//    「本文は変えないので編集ロックは要りません」でしたが、**変えるようになった
	//    ので関門が要ります**（`handler_gate.go`）。
	pageID, okID := gateWritablePage(w, r, req.PageID)
	if !okID {
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

	// ⚠ **作っただけでは、ページを開いても出てきません**（2026-09-22 ユーザー報告:
	//    「発注書のページにPDFが表示されていません」）。添付として保存されるだけ
	//    だったので、**本文に「ここで開く」マーカーを置きます**。
	//    ⚠ **失敗してもPDFは取り消しません**——**紙のほうが重い**ので、
	//    「画面に出ない」は人が貼り直せば済みます。理由を添えるだけにします。
	out := map[string]any{
		"success": true, "attach_id": attachID, "file": fileName,
		"url": "/" + pageID + "/" + fileName,
	}
	if note := showOrderPDFOnPage(user, pageID, attachID); note != "" {
		out["view_note"] = note
	}
	json.NewEncoder(w).Encode(out)
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
	if err := addPDFFont(p, font); err != nil {
		return nil, err
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

// pdfInk は1つの文字列を書きます。
//
// ユーザー（2026-09-23）:「**PDFの文字が細くてかすれてるような気がします**」。
//
// ⚠ **原因はフォントの既定の太さでした。** それまでの設定は Windows の
// `NotoSansJP-VF.ttf`——**可変フォント**で、`fvar` を読むと **`wght` の既定値が
// 100（Thin）**です。⚠ **gopdf は可変軸を扱わないので、いちばん細い形がそのまま
// 刷られていました**（実測で確認）。**太さはフォントそのもので取ります**
// （[ttc.go](ttc.go) が `.ttc` から太い書体を取り出せるようにしました）。
//
// ⚠ **重ね刷り（疑似ボールド）は採りませんでした。** 同じ文字を少しずらして4回書けば
// 太く見えますが、⚠ **PDFから取り出す文字が4重になります**——`発注書発注書発注書発注書`。
// 相手がコピーしたときも、**こちらが後でその紙を機械に読ませるとき**も壊れます。
// **見た目のために、中身を壊さない。**
//
// ⚠ **この関数は残します**——**紙へ字を置く口を1つに保つ**ためです。次に太さや
// 位置を触るとき、**散らばっていると1か所だけ直すことになります**。
func pdfInk(p *gopdf.GoPdf, x, y, size float64, s string) {
	p.SetXY(x, y)
	p.Cell(nil, s)
}

// pdfText は1行書いて、次の行のyを返します。
func pdfText(p *gopdf.GoPdf, x, y, size float64, s string) float64 {
	if strings.TrimSpace(s) == "" {
		return y
	}
	p.SetFont("jp", "", size)
	pdfInk(p, x, y, size, s)
	return y + size + 4
}

// pdfTextRight は右端を揃えて書きます（⚠ 金額は揃っていないと読めません）。
func pdfTextRight(p *gopdf.GoPdf, right, y, size float64, s string) {
	p.SetFont("jp", "", size)
	w, _ := p.MeasureTextWidth(s)
	pdfInk(p, right-w, y, size, s)
}

// pdfTableHead は見出し行を1つ刷り、次のyを返します（改ページのたびに呼びます）。
func pdfTableHead(p *gopdf.GoPdf, y float64, cols []orderPDFColumn, line float64) float64 {
	x := pdfLeft
	for _, c := range cols {
		pdfInk(p, x+pdfCellPad, y+3, line-7, c.Label)
		p.Line(x, y, x, y+line)
		x += c.Width
	}
	p.Line(pdfLeft, y, x, y)
	p.Line(x, y, x, y+line)
	p.Line(pdfLeft, y+line, x, y+line)
	return y + line
}

// pdfTable は明細表を組みます（線と文字を自分で置きます）。
func pdfTable(p *gopdf.GoPdf, y float64, cols []orderPDFColumn, rows []map[string]string) float64 {
	// ⚠ **罫線も太くします**——FAX では細い線も飛びます（字だけ太らせても、
	//    表の枠が消えると**どの値がどの列か分からなくなります**）。
	p.SetLineWidth(0.8)
	// ⚠ **列幅は中身を測って決めます**（2026-09-23）。それまでは**固定のポイント値**で、
	//    **字の大きさに追随しませんでした**——字を大きくすると隣の列へはみ出し、
	//    ⚠ **`p.Cell(nil, …)` は幅を持たないので切られもせず、重なって印刷されます**。
	//    測れば、①字を大きくできる ②はみ出しが構造的に起きない、の両方が片付きます。
	cols, size := fitTableColumns(p, cols, rows, pdfRight-pdfLeft)
	line := size + 7 // 行の高さ（字の上下に余白）
	p.SetFont("jp", "", size)

	y = pdfTableHead(p, y, cols, line)

	for _, r := range rows {
		// ⚠ **折り返しは最後の手段ですが、無いと困ります**——長い `品名` が1つ
		//    入っただけで、**字を下限まで下げても入らない**ことがあります。
		//    そのときは**切らずに折ります**（紙から値が消えるほうが危ない）。
		lines := make([][]string, len(cols))
		high := 1
		for i, c := range cols {
			v := pdfCellValue(r, c)
			if v == "" {
				continue
			}
			ls, err := p.SplitText(v, c.Width-2*pdfCellPad)
			if err != nil || len(ls) == 0 {
				ls = []string{v}
			}
			lines[i] = ls
			if len(ls) > high {
				high = len(ls)
			}
		}
		h := float64(high) * line
		// ⚠ **紙の下からはみ出したら改ページします**（2026-09-23）。それまでは
		//    **描き続けるだけ**で、⚠ **溢れた行は黙って消えていました**
		//    ——字を大きくして行が高くなったぶん、起きやすくなります。
		//    ⚠ **見出しも刷り直します**（2枚目に列の名前が無いと読めません）。
		if y+h > pdfBottom {
			p.AddPage()
			p.SetFont("jp", "", size)
			y = pdfTableHead(p, pdfTop, cols, line)
		}
		x := pdfLeft
		for i, c := range cols {
			for n, s := range lines[i] {
				ly := y + float64(n)*line + 3
				cx := x + pdfCellPad
				if c.Right {
					w, _ := p.MeasureTextWidth(s)
					cx = x + c.Width - w - pdfCellPad
				}
				pdfInk(p, cx, ly, size, s)
			}
			p.Line(x, y, x, y+h)
			x += c.Width
		}
		p.Line(x, y, x, y+h)
		p.Line(pdfLeft, y+h, x, y+h)
		y += h
	}
	return y
}

// pdfCellValue はそのセルに刷る文字です。
//
// ⚠ **`金額` は本文の列ではなく計算です**（数量 × 単価）。測る側と書く側で違う値を
// 使うと、**測ったより長い文字が入ってはみ出します**——だから1つの口にします。
func pdfCellValue(r map[string]string, c orderPDFColumn) string {
	if c.Label == "金額" {
		return comma(cms.VocabNumber(r["数量"]) * cms.VocabNumber(r["単価"]))
	}
	v := r[c.Label]
	if c.Right && v != "" {
		return comma(cms.VocabNumber(v))
	}
	return v
}

// fitTableColumns は中身を測って、**列幅**と**字の大きさ**を決めます。
//
// ⚠ **「入るなら大きく」です**（2026-09-23・FAX対策）。`pdfFontSteps` を大きい順に
// 試し、**全部の列が入る最初の大きさ**を採ります。⚠ **下限は 9pt**——それまでの
// 大きさなので、**どんな中身でも今より小さくはなりません**。
//
// ⚠ **入らないときは、字を下げる前に折り返します。** 長い品名が1つ入っただけで
// 全体が 9.5pt に落ちるのは**割に合いません**——**折り返した 12pt のほうが、
// 1行に収めた 9.5pt より読めます**（FAX で効くのは1文字の大きさで、行数ではない）。
// だから**文字の列を細らせて折り返させ**、それでも無理なときだけ字を下げます。
//
// ⚠ **細らせるのは文字の列だけ**です。数の列（数量・単価・金額）は折り返しても
// 読みやすくならず、⚠ **金額が2行に割れると読み違えます**。
//
// ⚠ **見出しより細くはしません**——値が入っても**何の列か分からなければ読めません**。
func fitTableColumns(p *gopdf.GoPdf, cols []orderPDFColumn, rows []map[string]string,
	avail float64) ([]orderPDFColumn, float64) {
	var last []orderPDFColumn
	lastSize := pdfFontSteps[len(pdfFontSteps)-1]
	for _, s := range pdfFontSteps {
		w, total := measureColumns(p, cols, rows, s)
		last, lastSize = w, s
		if total <= avail {
			return spreadSpare(w, avail-total), s
		}
		if squeezed, ok := squeezeText(p, w, avail, s); ok {
			return squeezed, s
		}
	}
	// ⚠ **いちばん小さい字でも入らないときは、比で縮めます**（折り返しが受け止める）。
	//    ここで諦めると**紙からはみ出して印刷されます**——見えない誤りのほうが困ります。
	total := 0.0
	for _, c := range last {
		total += c.Width
	}
	if total > avail && total > 0 {
		k := avail / total
		for i := range last {
			last[i].Width *= k
		}
	}
	return last, lastSize
}

// squeezeText は文字の列を細らせて、折り返しで収める試みです。
//
// 細らせる量は**自然な幅に比例**させます——いちばん長い列（たいてい `品名`）が
// いちばん譲る形で、**短い列は元の幅のまま**です。
func squeezeText(p *gopdf.GoPdf, cols []orderPDFColumn, avail, size float64) ([]orderPDFColumn, bool) {
	p.SetFont("jp", "", size)
	fixed, flex, floor := 0.0, 0.0, 0.0
	mins := make([]float64, len(cols))
	for i, c := range cols {
		if c.Right {
			fixed += c.Width
			continue
		}
		w, _ := p.MeasureTextWidth(c.Label)
		mins[i] = w + 2*pdfCellPad
		flex += c.Width
		floor += mins[i]
	}
	room := avail - fixed
	if flex == 0 || room < floor {
		return nil, false // 見出しすら入らない——字を下げるしかない
	}
	// ⚠ **下限に当たった列は固定して、残りで配り直します**（繰り返し）。
	//    1回で割り当てると、**短い列が下限に押し戻されたぶんだけ合計が超え**、
	//    「入らない」と誤って判定します——**実際に踏みました**（2026-09-23）:
	//    `単位` が下限に戻るだけで、長い品名の紙が 12pt → 9.5pt に落ちていました。
	out := make([]orderPDFColumn, len(cols))
	copy(out, cols)
	pinned := make([]bool, len(cols))
	for again := true; again; {
		again = false
		freeRoom, freeNat := room, 0.0
		for i, c := range out {
			switch {
			case c.Right:
			case pinned[i]:
				freeRoom -= mins[i]
			default:
				freeNat += cols[i].Width
			}
		}
		if freeNat == 0 {
			break
		}
		k := freeRoom / freeNat
		for i := range out {
			if out[i].Right || pinned[i] {
				continue
			}
			if w := cols[i].Width * k; w >= mins[i] {
				out[i].Width = w
			} else {
				out[i].Width, pinned[i], again = mins[i], true, true
			}
		}
	}
	total := 0.0
	for _, c := range out {
		total += c.Width
	}
	if total > avail+0.5 {
		return nil, false
	}
	return spreadSpare(out, avail-total), true
}

// spreadSpare は余りを文字の列へ均等に配ります（数の列は広げても読みやすくならない）。
func spreadSpare(cols []orderPDFColumn, spare float64) []orderPDFColumn {
	if spare <= 0 {
		return cols
	}
	text := 0
	for _, c := range cols {
		if !c.Right {
			text++
		}
	}
	if text == 0 {
		return cols
	}
	add := spare / float64(text)
	for i := range cols {
		if !cols[i].Right {
			cols[i].Width += add
		}
	}
	return cols
}

// measureColumns はその字の大きさでの列幅と合計を返します。
//
// ⚠ **見出しも測ります**——値が短くても、**見出しが入らなければ読めません**。
func measureColumns(p *gopdf.GoPdf, cols []orderPDFColumn, rows []map[string]string,
	size float64) ([]orderPDFColumn, float64) {
	p.SetFont("jp", "", size)
	out := make([]orderPDFColumn, len(cols))
	total := 0.0
	for i, c := range cols {
		out[i] = c
		w, _ := p.MeasureTextWidth(c.Label)
		for _, r := range rows {
			if vw, _ := p.MeasureTextWidth(pdfCellValue(r, c)); vw > w {
				w = vw
			}
		}
		out[i].Width = w + 2*pdfCellPad
		total += out[i].Width
	}
	return out, total
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
// ⚠ **全行が空の列は落とします**（ユーザーの実物に合わせて）。材料の発注書に `表面` は
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
	cancelled := 0
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
		// ⚠ **取り消した行は紙に刷りません**（2026-09-22）。出す紙は**いま注文する
		//    もの**で、取り消したものは注文ではありません——刷ると**合計金額にも
		//    入ります**（`buildOrderPDF` が数量×単価を足すので）。
		//
		// ⚠ **行そのものは本文に残します。** 一度は注文しようとした事実で、
		//    **発注済みから取り消した行は相手も知っています**——消すと、
		//    「そんな注文は無かった」という紙になります。
		if orderLineCancelled(r["状態"]) {
			cancelled++
			continue
		}
		rows = append(rows, r)
	}
	if len(rows) == 0 {
		if cancelled > 0 {
			return nil, nil, nil, errors.New("発注明細は全部取り消されています（" +
				strconv.Itoa(cancelled) + "行）")
		}
		return nil, nil, nil, errors.New("発注明細に中身のある行がありません")
	}

	// 紙に出す列を決める。⚠ **並びと幅は実物に寄せます**。
	want := []orderPDFColumn{
		{Label: "品番", Width: 95},
		{Label: "品名", Width: 150},
		{Label: "材質", Width: 70},
		{Label: "形状", Width: 60},
		{Label: "寸法", Width: 110},
		{Label: "表面", Width: 70},
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
