package subcon

import (
	"bytes"
	"compress/zlib"
	"errors"
	"github.com/signintech/gopdf"
	"io"
	"os"
	"regexp"
	"strconv"
	"strings"
	"testing"
)

// **発注書のPDF発行**（2026-09-21）。ユーザー:「**PDFを発行出来ることはMUSTです。**
// 発注書はPDFで送付し**7年保存**する必要があるそうです」。
//
// ⚠ **「落ちなかった」では足りません。** 作ったPDFの `ToUnicode` を読み返して、
// **書いたはずの文字が戻ること**を確かめます——2026-09-21 の朝に、印刷で
// 「エラーが出ない＝正しい」と思い込んで失敗したばかりです。

// withPDFFont は試験のあいだフォントを差し替えます。
func withPDFFont(t *testing.T, path string) {
	t.Helper()
	stagesMu.Lock()
	old := pdfFont
	pdfFont = path
	stagesMu.Unlock()
	t.Cleanup(func() {
		stagesMu.Lock()
		pdfFont = old
		stagesMu.Unlock()
	})
}

// systemJPFont は試験に使える日本語フォントを探します（無ければ試験を飛ばす）。
//
// ⚠ **無いことを失敗にしません。** フォントは環境の持ち物で、**壊れたのか、
// この機械に無いだけなのか**が読む人に分かる必要があります（E2E の流儀と同じ）。
func systemJPFont(t *testing.T) string {
	t.Helper()
	for _, p := range []string{
		`C:\Windows\Fonts\NotoSansJP-VF.ttf`,
		"/usr/share/fonts/opentype/noto/NotoSansCJK-Regular.ttc",
	} {
		if st, err := os.Stat(p); err == nil && !st.IsDir() && strings.HasSuffix(p, ".ttf") {
			return p
		}
	}
	t.Skip("飛ばします: 日本語の .ttf が見つかりません（⚠ .ttc は読めません）")
	return ""
}

// pdfTextOf は作ったPDFから文字を読み返します（`ToUnicode` 経由）。
//
// ⚠ **これが番人の心臓です。** バイト数や「エラーが出ないこと」を見るだけの試験は、
// **文字化けしたPDFでも通ります**。
func pdfTextOf(t *testing.T, d []byte) string {
	t.Helper()
	g2u := map[int]string{}
	var content []byte
	for _, m := range regexp.MustCompile(`stream\r?\n`).FindAllIndex(d, -1) {
		e := bytes.Index(d[m[1]:], []byte("endstream"))
		if e < 0 {
			continue
		}
		raw := d[m[1] : m[1]+e]
		t2 := raw
		if zr, err := zlib.NewReader(bytes.NewReader(raw)); err == nil {
			if out, err := io.ReadAll(zr); err == nil {
				t2 = out
			}
		}
		if bytes.Contains(t2, []byte("beginbfchar")) || bytes.Contains(t2, []byte("beginbfrange")) {
			for _, blk := range regexp.MustCompile(`(?s)beginbfchar(.*?)endbfchar`).FindAllSubmatch(t2, -1) {
				for _, pr := range regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>`).FindAllSubmatch(blk[1], -1) {
					gid, _ := strconv.ParseInt(string(pr[1]), 16, 32)
					g2u[int(gid)] = utf16beOf(string(pr[2]))
				}
			}
			for _, blk := range regexp.MustCompile(`(?s)beginbfrange(.*?)endbfrange`).FindAllSubmatch(t2, -1) {
				for _, pr := range regexp.MustCompile(`<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>\s*<([0-9A-Fa-f]+)>`).FindAllSubmatch(blk[1], -1) {
					s0, _ := strconv.ParseInt(string(pr[1]), 16, 32)
					e0, _ := strconv.ParseInt(string(pr[2]), 16, 32)
					u0, _ := strconv.ParseInt(string(pr[3]), 16, 32)
					for i := int64(0); i <= e0-s0; i++ {
						g2u[int(s0+i)] = string(rune(u0 + i))
					}
				}
			}
		}
		if bytes.Contains(t2, []byte("BT")) {
			content = append(content, t2...)
		}
	}
	var b strings.Builder
	for _, h := range regexp.MustCompile(`<([0-9A-Fa-f]+)>`).FindAllSubmatch(content, -1) {
		s := string(h[1])
		for i := 0; i+4 <= len(s); i += 4 {
			gid, _ := strconv.ParseInt(s[i:i+4], 16, 32)
			b.WriteString(g2u[int(gid)])
		}
	}
	return b.String()
}

func utf16beOf(hex string) string {
	var out []rune
	for i := 0; i+4 <= len(hex); i += 4 {
		v, _ := strconv.ParseInt(hex[i:i+4], 16, 32)
		out = append(out, rune(v))
	}
	return string(out)
}

// pdfOrderBody は発注書ページの本文を組みます。
func pdfOrderBody(rows string) string {
	return `<h1>発注</h1><dl data-type="tags">` +
		`<dt>発注書番号</dt><dd>44</dd>` +
		`<dt>` + SupplierTag + `</dt><dd>株式会社みなと商店</dd>` +
		`<dt>発注日</dt><dd>2026-08-19</dd>` +
		`<dt>備考</dt><dd>急ぎでお願いします</dd></dl>` +
		`<table data-type="our-order-items"><caption>発注明細</caption><tbody>` +
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>材質</th><th>形状</th>` +
		`<th>寸法</th><th>表面</th><th>数量</th><th>単位</th><th>単価</th>` +
		`<th>備考</th><th>状態</th></tr>` + rows + `</tbody></table>`
}

// TestOrderPDFRoundTrips は、⚠ **書いた文字がPDFから読み返せる**ことを固定します。
func TestOrderPDFRoundTrips(t *testing.T) {
	withPDFFont(t, systemJPFont(t))

	body := pdfOrderBody(`<tr><td></td><td></td><td></td><td>鉄STPG370EG</td>` +
		`<td>φ27.2</td><td>t3.4*定尺</td><td></td><td>10</td><td>個</td><td>3200</td>` +
		`<td>ビードカット品</td><td>未納品</td></tr>`)
	pdf, err := buildOrderPDF(body, nil)
	if err != nil {
		t.Fatalf("PDFを作れません: %v", err)
	}
	if len(pdf) < 1000 {
		t.Fatalf("PDFが小さすぎます（%dバイト）", len(pdf))
	}
	got := pdfTextOf(t, pdf)
	for _, want := range []string{
		"発注書", "株式会社みなと商店", "鉄STPG370EG", "φ27.2", "急ぎでお願いします",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("⚠ PDFに %q が入っていません。読み返した中身:\n%s", want, got)
		}
	}
	// ⚠ **金額は2回出ます**（明細の行と、合計）。**数えて確かめること**——
	// 1回でも出ていれば通る書き方だと、**明細の金額を消しても合計のほうで通って
	// しまいます**（変異試験で実際に空振りしました・2026-09-21）。
	if n := strings.Count(got, "32,000"); n != 2 {
		t.Errorf("⚠ 金額 32,000 が %d 回です（明細と合計で2回を期待）。読み返した中身:\n%s",
			n, got)
	}
}

// TestOrderPDFDropsEmptyColumns は、⚠ **使わない列を紙に出さない**ことを固定します。
//
// ⚠ **形式は1つのまま、紙だけ種類ごとに違う**——材料の発注書に `表面` は出ず、
// 塗装の発注書に `材質` は出ません。これが「まとめる方法」の実体です。
func TestOrderPDFDropsEmptyColumns(t *testing.T) {
	withPDFFont(t, systemJPFont(t))

	// 材料の発注書（`品番`・`品名`・`表面` は空）。
	mat, err := buildOrderPDF(pdfOrderBody(
		`<tr><td></td><td></td><td></td><td>鉄</td><td>板</td><td>t3.2</td>`+
			`<td></td><td>5</td><td>枚</td><td>800</td><td></td><td>未納品</td></tr>`), nil)
	if err != nil {
		t.Fatalf("材料: %v", err)
	}
	if s := pdfTextOf(t, mat); strings.Contains(s, "表面") {
		t.Errorf("⚠ 材料の発注書に `色` の列が出ています:\n%s", s)
	}

	// 塗装の発注書（`材質`・`形状`・`寸法` は空）。
	paint, err := buildOrderPDF(pdfOrderBody(
		`<tr><td>000036</td><td>K120-01-242</td><td>押さえプレート</td>`+
			`<td></td><td></td><td></td><td>緑</td><td>20</td><td>個</td><td>160</td>`+
			`<td></td><td>未納品</td></tr>`), nil)
	if err != nil {
		t.Fatalf("塗装: %v", err)
	}
	s := pdfTextOf(t, paint)
	if strings.Contains(s, "材質") {
		t.Errorf("⚠ 塗装の発注書に `材質` の列が出ています:\n%s", s)
	}
	if !strings.Contains(s, "緑") || !strings.Contains(s, "K120-01-242") {
		t.Errorf("塗装に要る列が出ていません:\n%s", s)
	}
	// ⚠ **弊社品番は紙に出しません**（相手には意味が無い番号）。
	if strings.Contains(s, "000036") {
		t.Errorf("⚠ 弊社品番が紙に出ています（相手には意味がありません）:\n%s", s)
	}
}

// TestOrderPDFNeedsFont は、**フォントが無ければ理由を言う**ことを固定します。
//
// ⚠ 起動は止めません（Gemini キーと同じ扱い）。⚠ そして**何をすればよいか**を
// 書きます——「読めません」だけでは直せません。
func TestOrderPDFNeedsFont(t *testing.T) {
	withPDFFont(t, "")
	_, err := buildOrderPDF(pdfOrderBody(`<tr><td></td><td></td><td>x</td><td></td>`+
		`<td></td><td></td><td></td><td>1</td><td>個</td><td>1</td><td></td><td>未納品</td></tr>`), nil)
	if !errors.Is(err, ErrNoPDFFont) {
		t.Fatalf("フォント未設定を知らせていません: %v", err)
	}
	if !strings.Contains(err.Error(), "pdf_font") || !strings.Contains(err.Error(), ".ttc") {
		t.Errorf("⚠ 直し方（設定の名前と、使える形式）が書かれていません: %v", err)
	}
}

// TestOrderPDFRefusesNonOrderPage は、**発注書でないページを断る**ことを固定します。
func TestOrderPDFRefusesNonOrderPage(t *testing.T) {
	withPDFFont(t, systemJPFont(t))
	if _, err := buildOrderPDF(`<h1>ただのページ</h1><p>本文</p>`, nil); err == nil {
		t.Fatal("発注明細が無いのにPDFを作っています")
	}
}

// ── FAXで読める大きさにする（2026-09-23）────────────────────────────────
//
// ユーザー:「**発注書のPDFが文字が小さすぎてFAXで送ると潰れるような気がします**」。
//
// ⚠ **G3 FAX の標準モードは縦 98dpi しかありません。** 9pt の和文は縦12走査線ほどで、
// 画数の多い漢字（`鋼`・`鍍`・`厚`）は塗り潰れます。

// newTestPDF は測るためだけの GoPdf を1つ作ります。
func newTestPDF(t *testing.T) *gopdf.GoPdf {
	t.Helper()
	withPDFFont(t, systemJPFont(t))
	p := &gopdf.GoPdf{}
	p.Start(gopdf.Config{PageSize: *gopdf.PageSizeA4})
	p.AddPage()
	if err := p.AddTTFFont("jp", PDFFont()); err != nil {
		t.Fatalf("フォントを読めません: %v", err)
	}
	return p
}

// pdfTestCols は紙に出る列の並びです（`readOrderDoc` の `want` と同じ順）。
func pdfTestCols() []orderPDFColumn {
	return []orderPDFColumn{
		{Label: "品番"}, {Label: "品名"}, {Label: "材質"}, {Label: "形状"}, {Label: "寸法"},
		{Label: "表面"}, {Label: "単位"},
		{Label: "数量", Right: true}, {Label: "単価", Right: true}, {Label: "金額", Right: true},
	}
}

// usedCols は値の入っている列だけに絞ります（紙と同じ落とし方）。
func usedCols(rows []map[string]string) []orderPDFColumn {
	var out []orderPDFColumn
	for _, c := range pdfTestCols() {
		for _, r := range rows {
			if pdfCellValue(r, c) != "" {
				out = append(out, c)
				break
			}
		}
	}
	return out
}

// TestOrderPDFTypeIsBigEnoughForFax は、⚠ **ふつうの発注書が 9pt より大きく組まれる**
// ことを固定します。
//
// ⚠ **これがこの変更の目的そのものです。** 「入るなら大きく」なので、
// **下がったら番人が落ちる**形にしておかないと、列を1つ足した日に黙って 9pt へ
// 戻ります——そして**戻ったことは紙を見るまで分かりません**。
func TestOrderPDFTypeIsBigEnoughForFax(t *testing.T) {
	p := newTestPDF(t)
	rows := []map[string]string{
		{"材質": "鉄STPG370EG", "形状": "角パイプ", "寸法": "□75*75*t3.2*1090",
			"単位": "本", "数量": "10", "単価": "3200"},
		{"材質": "SS400", "形状": "FB", "寸法": "t4.5*75*1090",
			"単位": "本", "数量": "2", "単価": "860"},
	}
	cols, size := fitTableColumns(p, usedCols(rows), rows, pdfRight-pdfLeft)
	if size <= 9 {
		t.Errorf("⚠ 材料の発注書が %.1fpt です（9pt より大きいことを期待——FAXで潰れます）", size)
	}
	total := 0.0
	for _, c := range cols {
		total += c.Width
	}
	// ⚠ **紙から出ていないこと**。字を大きくして列が溢れたら、
	//    **隣の列へ重なって印刷されます**（`p.Cell` は切りません）。
	if total > pdfRight-pdfLeft+0.5 {
		t.Errorf("⚠ 表が紙からはみ出しています（%.1f / %.1f）", total, pdfRight-pdfLeft)
	}
}

// TestOrderPDFWrapsBeforeShrinking は、⚠ **長い品名で字を下げない**ことを固定します。
//
// ⚠ **折り返した 12pt のほうが、1行に収めた 9.5pt より読めます**——FAX で効くのは
// **1文字の大きさ**で、行数ではありません。⚠ **実際に踏みました**（2026-09-23）:
// `単位` が下限に戻るだけで合計が超え、長い品名の紙が 12pt → 9.5pt に落ちていました。
func TestOrderPDFWrapsBeforeShrinking(t *testing.T) {
	p := newTestPDF(t)
	rows := []map[string]string{
		{"品番": "A100-B01-0051",
			"品名": "みらい産業向けスライドブラケット組立（左右セット・塗装あり）",
			"単位": "個", "数量": "20", "単価": "1500"},
	}
	cols, size := fitTableColumns(p, usedCols(rows), rows, pdfRight-pdfLeft)
	// ⚠ **「9pt より大きい」では足りません**（2026-09-23・変異試験で空振り）。
	//    折り返しをやめても **9.5pt には収まる**ので、その条件では
	//    **縮めたのか折ったのかを見分けられません**。**いちばん大きい候補**を要求します。
	if want := pdfFontSteps[0]; size < want {
		t.Fatalf("⚠ 長い品名で字が %.1fpt に下がりました（%.1fpt のまま折り返すはず）", size, want)
	}
	// ⚠ **そして本当に折り返すこと**——字が大きいだけで1行に収まっているなら、
	//    それは「長い品名」になっていません（番人の下ごしらえが効いていない）。
	p.SetFont("jp", "", size)
	var name orderPDFColumn
	for _, c := range cols {
		if c.Label == "品名" {
			name = c
		}
	}
	if name.Width == 0 {
		t.Fatal("品名の列がありません")
	}
	if ls, err := p.SplitText(rows[0]["品名"], name.Width-2*pdfCellPad); err != nil || len(ls) < 2 {
		t.Errorf("⚠ 品名が折り返されていません（%d 行・幅 %.1f）", len(ls), name.Width)
	}
	// ⚠ **見出しより細い列を作らないこと**——値が入っても**何の列か分かりません**。
	p.SetFont("jp", "", size)
	for _, c := range cols {
		w, _ := p.MeasureTextWidth(c.Label)
		if c.Width < w+2*pdfCellPad-0.5 {
			t.Errorf("⚠ 列 %q が見出しより細いです（%.1f < %.1f）", c.Label, c.Width, w+2*pdfCellPad)
		}
	}
}

// TestOrderPDFBreaksPages は、⚠ **紙の下からはみ出したら改ページする**ことを
// 固定します。
//
// ⚠ **それまでは描き続けるだけで、溢れた行は黙って消えていました。** 字を大きくして
// 行が高くなったぶん起きやすくなります——⚠ **行の落丁は、紙を見ても気づけません**
// （残った行は互いに辻褄が合ったままなので）。
//
// ⚠ **文字を読み返す検査では捕まりません**——紙から出ても**中身は残る**ためです。
// だから**返ってきた y（いまどこまで書いたか）**を見ます。
func TestOrderPDFBreaksPages(t *testing.T) {
	p := newTestPDF(t)
	var rows []map[string]string
	for i := 0; i < 60; i++ {
		rows = append(rows, map[string]string{
			"材質": "SS400", "形状": "FB", "寸法": "t4.5*75*1090",
			"単位": "本", "数量": "2", "単価": "860"})
	}
	y := pdfTable(p, pdfTop, usedCols(rows), rows)
	if y > pdfBottom {
		t.Errorf("⚠ 表が紙の下（%.0f）を越えました: y=%.0f——溢れた行は黙って消えます",
			pdfBottom, y)
	}
}

// TestOrderSurfaceColumnCarriesPlatingAndBare は、⚠ **`表面` が色・鍍金・生地を
// まとめて運ぶ**ことを固定します（2026-09-23 ユーザー:「**発注における色は表面に
// 変えて、メッキや生地などもここに入れては**どうでしょう」）。
//
// ⚠ **`色` では鍍金と生地が入りません**——`三価ユニクロ` は色ではなく、
// `生地` は「何もしない」という指示です。
func TestOrderSurfaceColumnCarriesPlatingAndBare(t *testing.T) {
	withPDFFont(t, systemJPFont(t))
	body := pdfOrderBody(
		`<tr><td></td><td>K120-01-242</td><td>押さえプレート</td><td></td><td></td><td></td>` +
			`<td>緑</td><td>個</td><td>20</td><td>160</td><td></td><td>発注済</td></tr>` +
			`<tr><td></td><td>K120-01-243</td><td>座金</td><td></td><td></td><td></td>` +
			`<td>三価ユニクロ</td><td>個</td><td>50</td><td>30</td><td></td><td>発注済</td></tr>` +
			`<tr><td></td><td>K120-01-244</td><td>カラー</td><td></td><td></td><td></td>` +
			`<td>生地</td><td>個</td><td>10</td><td>90</td><td></td><td>発注済</td></tr>`)
	pdf, err := buildOrderPDF(body, nil)
	if err != nil {
		t.Fatalf("PDFを作れません: %v", err)
	}
	got := pdfTextOf(t, pdf)
	for _, want := range []string{"表面", "緑", "三価ユニクロ", "生地"} {
		if !strings.Contains(got, want) {
			t.Errorf("⚠ 紙に %q がありません。読み返した中身:\n%s", want, got)
		}
	}
}

// TestOrderNoteIsASectionNotATag は、⚠ **備考がタグではなく表の下の節**であることを
// 固定します（2026-09-24 ユーザー:「発注書ページに『備考』タグがありますが、要求に
// 『備考』タグはありません」）。要求は「その下のブロックに『備考』入力欄があります。
// 備考欄は複数行書けます」。
func TestOrderNoteIsASectionNotATag(t *testing.T) {
	body := buildOurOrderHTML("000138", "みなと商店", "2026-09-24", "", "定尺で可\n急ぎでお願いします", "", nil)
	if strings.Contains(body, "<dt>備考</dt>") {
		t.Errorf("⚠ 備考がタグに書かれています:\n%s", body)
	}
	for _, want := range []string{"<section><h2>備考</h2>", "<p>定尺で可</p>", "<p>急ぎでお願いします</p>"} {
		if !strings.Contains(body, want) {
			t.Errorf("本文に %q がありません:\n%s", want, body)
		}
	}
	// ⚠ **表の下**にあること（要求の並び: 発注明細 → 備考欄）。
	if strings.Index(body, "<h2>備考</h2>") < strings.Index(body, "</table>") {
		t.Errorf("⚠ 備考欄が表より上にあります:\n%s", body)
	}
	// ⚠ **空でも欄は置く**（後から書き足す場所が要る）。
	if empty := buildOurOrderHTML("000139", "みなと商店", "2026-09-24", "", "", "", nil); !strings.Contains(empty, "<h2>備考</h2>") {
		t.Errorf("⚠ 備考が空のとき欄がありません:\n%s", empty)
	}
}

// TestOrderPDFPrintsNoteLines は、⚠ **備考欄の複数行が紙に刷られる**ことを、PDFを
// 読み返して固定します。`<br>` も行の区切り、**古い紙のタグ `備考` も読む**。
func TestOrderPDFPrintsNoteLines(t *testing.T) {
	withPDFFont(t, systemJPFont(t))
	rows := `<tr><td></td><td></td><td></td><td>SS400</td><td>板</td><td>t3.2</td>` +
		`<td></td><td>2</td><td>枚</td><td>800</td><td>社内メモ</td><td>未発注</td></tr>`
	table := `<table><caption>発注明細</caption><tbody>` +
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>材質</th><th>形状</th>` +
		`<th>寸法</th><th>表面</th><th>数量</th><th>単位</th><th>単価</th>` +
		`<th>備考</th><th>状態</th></tr>` + rows + `</tbody></table>`
	head := `<h1>発注</h1><dl data-type="tags"><dt>` + SupplierTag + `</dt><dd>みなと商店</dd></dl>`

	// 新しい形: 節（段落と <br> の両方で区切る）。
	body := head + table + `<section><h2>備考</h2><p>訂正版（2026-09-24 送付分を破棄し、本書に差し替えてください）</p>` +
		`<p>定尺で可<br/>急ぎでお願いします</p></section>`
	pdf, err := buildOrderPDF(body, nil)
	if err != nil {
		t.Fatalf("PDFを作れません: %v", err)
	}
	text := pdfTextOf(t, pdf)
	for _, want := range []string{"訂正版", "定尺で可", "急ぎでお願いします"} {
		if !strings.Contains(text, want) {
			t.Errorf("⚠ 備考の %q が紙にありません:\n%s", want, text)
		}
	}
	// ⚠ **行の備考（社内メモ）は刷らない**——他社が知る必要のないもの。
	if strings.Contains(text, "社内メモ") {
		t.Errorf("⚠ 行の備考が紙に出ています:\n%s", text)
	}
	// ⚠ <br> で区切った2行が1行に繋がっていないこと——**読み手を直接見ます**
	//    （`pdfTextOf` は紙の上の文字を区切りなしで返すので、別の行でも隣り合って見える）。
	head2, _, _, err := readOrderDoc(body)
	if err != nil {
		t.Fatalf("読めません: %v", err)
	}
	if got := strings.Split(head2[orderNoteHeading], "\n"); len(got) != 3 || got[1] != "定尺で可" || got[2] != "急ぎでお願いします" {
		t.Errorf("⚠ 備考の行の分け方が違います（段落と <br> の両方で3行のはず）: %q", got)
	}

	// 古い形: タグの 備考（09-23 までの紙）も読む。
	old := `<h1>発注</h1><dl data-type="tags"><dt>` + SupplierTag + `</dt><dd>みなと商店</dd>` +
		`<dt>備考</dt><dd>昔のタグ</dd></dl>` + table
	pdf2, err := buildOrderPDF(old, nil)
	if err != nil {
		t.Fatalf("PDFを作れません: %v", err)
	}
	if !strings.Contains(pdfTextOf(t, pdf2), "昔のタグ") {
		t.Errorf("⚠ 古い紙のタグの備考が刷られていません")
	}
}
