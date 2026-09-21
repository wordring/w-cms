package subcon

import (
	"bytes"
	"compress/zlib"
	"errors"
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
		`<th>寸法</th><th>色</th><th>数量</th><th>単位</th><th>単価</th>` +
		`<th>備考</th><th>状態</th></tr>` + rows + `</tbody></table>`
}

// TestOrderPDFRoundTrips は、⚠ **書いた文字がPDFから読み返せる**ことを固定します。
func TestOrderPDFRoundTrips(t *testing.T) {
	withPDFFont(t, systemJPFont(t))

	body := pdfOrderBody(`<tr><td></td><td></td><td></td><td>鉄STPG370EG</td>` +
		`<td>φ27.2</td><td>t3.4*定尺</td><td></td><td>10</td><td>個</td><td>3200</td>` +
		`<td>ビードカット品</td><td>未納品</td></tr>`)
	pdf, err := buildOrderPDF(body)
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
// ⚠ **形式は1つのまま、紙だけ種類ごとに違う**——材料の発注書に `色` は出ず、
// 塗装の発注書に `材質` は出ません。これが「まとめる方法」の実体です。
func TestOrderPDFDropsEmptyColumns(t *testing.T) {
	withPDFFont(t, systemJPFont(t))

	// 材料の発注書（`品番`・`品名`・`色` は空）。
	mat, err := buildOrderPDF(pdfOrderBody(
		`<tr><td></td><td></td><td></td><td>鉄</td><td>板</td><td>t3.2</td>` +
			`<td></td><td>5</td><td>枚</td><td>800</td><td></td><td>未納品</td></tr>`))
	if err != nil {
		t.Fatalf("材料: %v", err)
	}
	if s := pdfTextOf(t, mat); strings.Contains(s, "色") {
		t.Errorf("⚠ 材料の発注書に `色` の列が出ています:\n%s", s)
	}

	// 塗装の発注書（`材質`・`形状`・`寸法` は空）。
	paint, err := buildOrderPDF(pdfOrderBody(
		`<tr><td>000036</td><td>K120-01-242</td><td>押さえプレート</td>` +
			`<td></td><td></td><td></td><td>緑</td><td>20</td><td>個</td><td>160</td>` +
			`<td></td><td>未納品</td></tr>`))
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
	_, err := buildOrderPDF(pdfOrderBody(`<tr><td></td><td></td><td>x</td><td></td>` +
		`<td></td><td></td><td></td><td>1</td><td>個</td><td>1</td><td></td><td>未納品</td></tr>`))
	if !errors.Is(err, ErrNoPDFFont) {
		t.Fatalf("フォント未設定を知らせていません: %v", err)
	}
	if !strings.Contains(err.Error(), "pdf_font") || !strings.Contains(err.Error(), ".ttc") {
		t.Errorf("⚠ 直し方（設定の名前と `.ttc` が使えないこと）が書かれていません: %v", err)
	}
}

// TestOrderPDFRefusesNonOrderPage は、**発注書でないページを断る**ことを固定します。
func TestOrderPDFRefusesNonOrderPage(t *testing.T) {
	withPDFFont(t, systemJPFont(t))
	if _, err := buildOrderPDF(`<h1>ただのページ</h1><p>本文</p>`); err == nil {
		t.Fatal("発注明細が無いのにPDFを作っています")
	}
}
