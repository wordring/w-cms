package subcon

import (
	"os"
	"strings"
	"testing"
)

// `.ttc` から1書体を取り出す（2026-09-23）。
//
// ユーザー:「**PDFの文字が細くてかすれてるような気がします**」——原因は
// **可変フォントの既定の太さが Thin だった**ことで、⚠ **太い和文フォントは
// Windows では全部 `.ttc`**、そして **gopdf は `.ttc` を読めません**でした。

// systemTTC は試験に使えるフォントコレクションを探します（無ければ飛ばす）。
//
// ⚠ **無いことを失敗にしません**——フォントは環境の持ち物で、**壊れたのか、
// この機械に無いだけなのか**が読む人に分かる必要があります。
func systemTTC(t *testing.T) string {
	t.Helper()
	for _, p := range []string{
		`C:\Windows\Fonts\BIZ-UDGothicB.ttc`,
		`C:\Windows\Fonts\meiryob.ttc`,
		`C:\Windows\Fonts\YuGothB.ttc`,
		`C:\Windows\Fonts\msgothic.ttc`,
	} {
		if _, err := os.Stat(p); err == nil {
			return p
		}
	}
	t.Skip("フォントコレクション（.ttc）がこの機械にありません")
	return ""
}

// TestTTCFaceMakesAUsableFont は、⚠ **取り出した書体でPDFが刷れて、文字が
// 読み返せる**ことを固定します。
//
// ⚠ **「落ちなかった」では足りません。** 表の切り出しを取り違えると
// （`tag / checkSum / offset / length` の並び）、**長さを位置として読んで**
// 黙って別の場所を写します——フォントは壊れますが、**エラーは出ません**
// （豆腐が並ぶだけ）。だから**書いた文字が戻ること**まで見ます。
func TestTTCFaceMakesAUsableFont(t *testing.T) {
	path := systemTTC(t)
	withPDFFont(t, path)

	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !IsTTC(data) {
		t.Fatalf("%s がコレクションとして見分けられません", path)
	}
	n, err := TTCFaceCount(data)
	if err != nil || n < 1 {
		t.Fatalf("書体の数を読めません: %d %v", n, err)
	}
	face, err := TTCFace(data, 0)
	if err != nil {
		t.Fatalf("書体を取り出せません: %v", err)
	}
	// ⚠ **取り出したものは素のTTFであること**（もう `ttcf` ではない）。
	if IsTTC(face) {
		t.Error("⚠ 取り出した結果がまだコレクションのままです")
	}
	if len(face) < 1000 {
		t.Errorf("⚠ 取り出した書体が小さすぎます（%d バイト）", len(face))
	}

	// 実際に刷って、和文が読み返せること。
	pdf, err := buildOrderPDF(pdfOrderBody(
		`<tr><td></td><td></td><td></td><td>鉄STPG370EG</td><td>角パイプ</td>`+
			`<td>□75*75*t3.2*1090</td><td></td><td>10</td><td>本</td><td>3200</td>`+
			`<td></td><td>発注済</td></tr>`), nil)
	if err != nil {
		t.Fatalf("取り出した書体でPDFを作れません: %v", err)
	}
	got := pdfTextOf(t, pdf)
	for _, want := range []string{"発注書", "鉄STPG370EG", "角パイプ", "株式会社みなと商店"} {
		if !strings.Contains(got, want) {
			t.Errorf("⚠ 取り出した書体で %q が読み返せません。読み返した中身:\n%s", want, got)
		}
	}
}

// TestTTCFaceRefusesNonsense は、⚠ **壊れた入力で黙らない**ことを固定します。
func TestTTCFaceRefusesNonsense(t *testing.T) {
	if _, err := TTCFaceCount([]byte("OTTO____")); err == nil {
		t.Error("⚠ 素のフォントをコレクションとして受けています")
	}
	data, err := os.ReadFile(systemTTC(t))
	if err != nil {
		t.Fatal(err)
	}
	n, _ := TTCFaceCount(data)
	if _, err := TTCFace(data, n); err == nil {
		t.Errorf("⚠ 無い書体（%d 番目）を取り出せてしまいます", n)
	}
	if _, err := TTCFace(data, -1); err == nil {
		t.Error("⚠ 負の番号を受けています")
	}
}

// TestPDFTextIsNotOverprinted は、⚠ **同じ文字を重ね刷りしていない**ことを
// 固定します。
//
// ⚠ **疑似ボールドは中身を壊します。** 少しずらして4回書けば太く見えますが、
// **PDFから取り出す文字が4重になります**——`発注書発注書発注書発注書`。
// 相手がコピーしたときも、**こちらが後でその紙を機械に読ませるとき**も壊れます。
func TestPDFTextIsNotOverprinted(t *testing.T) {
	withPDFFont(t, systemJPFont(t))
	pdf, err := buildOrderPDF(pdfOrderBody(
		`<tr><td></td><td></td><td></td><td>鉄</td><td>板</td><td>t3.2</td>`+
			`<td></td><td>5</td><td>枚</td><td>800</td><td></td><td>発注済</td></tr>`), nil)
	if err != nil {
		t.Fatalf("PDFを作れません: %v", err)
	}
	got := pdfTextOf(t, pdf)
	if n := strings.Count(got, "発注書番号"); n != 1 {
		t.Errorf("⚠ 「発注書番号」が %d 回あります（1回を期待——重ね刷りしています）:\n%s", n, got)
	}
}
