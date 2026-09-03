package sheetmetal

import (
	"strconv"
	"strings"
	"testing"

	"golang.org/x/text/encoding/japanese"
	"golang.org/x/text/transform"
)

// DXF の表題欄読み取りのテスト。
//
// 実物2枚（`X008-135-4 架台Assy`・構想図）で確かめた振る舞いを、合成した最小の
// DXF で固定します。とくに**Shift_JIS の罠**——`図` は `0x90 0x7D` で2バイト目が
// `}`——は、実装順を1つ入れ替えるだけで壊れるので必ず見ます。

// sjisBytes は文字列を Shift_JIS へ符号化します（和文CADの実物形式）。
func sjisBytes(t *testing.T, s string) string {
	t.Helper()
	out, _, err := transform.String(japanese.ShiftJIS.NewEncoder(), s)
	if err != nil {
		t.Fatalf("Shift_JIS符号化エラー: %v", err)
	}
	return out
}

// dxfEntity は1つの TEXT 要素を DXF の行へ組み立てます。
func dxfEntity(kind, text string, x, y, h float64) string {
	return strings.Join([]string{
		"  0", kind,
		" 10", ftoa(x), " 20", ftoa(y), " 40", ftoa(h),
		"  1", text, "",
	}, "\r\n")
}

func ftoa(f float64) string { return strconv.FormatFloat(f, 'f', 2, 64) }

// TestParseDXFTextsDecodesShiftJIS は、和文の復号と MTEXT の書式コード剥がしが
// **この順序で**行われることを固定します。
//
// 逆順にすると `図` の2バイト目 `}` を書式コードとして消してしまい、
// `図面番号` が `趨ﾊ番号` に化けます（実装中に実際に踏んだ）。
func TestParseDXFTextsDecodesShiftJIS(t *testing.T) {
	body := dxfEntity("TEXT", sjisBytes(t, "図面番号"), 100, 50, 2.5) +
		// MTEXT の書式コードつき（実物の形）。中身は和文。
		dxfEntity("MTEXT", "{\\fMS ゴシック|b0|i0;"+sjisBytes(t, "架台Assy(溶接図）")+"}", 100, 40, 2.5)

	texts := ParseDXFTexts([]byte(body))
	if len(texts) != 2 {
		t.Fatalf("要素数が違います: %d (%+v)", len(texts), texts)
	}
	if texts[0].Text != "図面番号" {
		t.Errorf("Shift_JIS が復号されていません: %q", texts[0].Text)
	}
	if texts[1].Text != "架台Assy(溶接図）" {
		t.Errorf("MTEXT の書式コードが剥がれていません（または復号順が逆）: %q", texts[1].Text)
	}
	if texts[0].H != 2.5 {
		t.Errorf("文字の高さが取れていません: %v", texts[0].H)
	}
}

// TestDXFTitleBlockPairsLabelAndValue は、ラベルとその**真下の値**が対になること、
// 和英併記の英語ラベルを値と取り違えないことを固定します。
func TestDXFTitleBlockPairsLabelAndValue(t *testing.T) {
	// 実物の配置を写した最小の表題欄（高さ2.06・行間およそ2〜3行ぶん）。
	body := dxfEntity("TEXT", sjisBytes(t, "図面番号"), 309.3, 37.3, 2.06) +
		dxfEntity("TEXT", "X008-135-4", 311.1, 32.7, 2.06) +
		dxfEntity("TEXT", "DRAWING NUMBER", 323.8, 36.2, 2.06) + // 英語併記（値ではない）
		dxfEntity("TEXT", sjisBytes(t, "個数"), 267.4, 51.4, 2.06) +
		dxfEntity("TEXT", "Quantity", 276.1, 50.4, 2.06) + // ほぼ同じ高さの英語（値ではない）
		dxfEntity("TEXT", "1", 274.6, 45.8, 2.06)

	got := DXFTitleBlock(ParseDXFTexts([]byte(body)))
	if got["図面番号"] != "X008-135-4" {
		t.Errorf("図面番号が取れていません: %q（全体 %+v）", got["図面番号"], got)
	}
	if got["個数"] != "1" {
		t.Errorf("個数が英語ラベルに引っ張られています: %q", got["個数"])
	}
	// 英語ラベルはラベルとして扱わない（タグが二重にならない）。
	if _, dup := got["DRAWINGNUMBER"]; dup {
		t.Errorf("英語ラベルまで拾っています: %+v", got)
	}
}

// TestDXFTitleBlockEmptyIsEmpty は、表題欄が空欄の図面（構想図など）で
// **何も捏造しない**ことを固定します。実物の構想図は図面番号が未記入でした。
func TestDXFTitleBlockEmptyIsEmpty(t *testing.T) {
	// ラベルだけが並び、値が入っていない表題欄。
	body := dxfEntity("TEXT", sjisBytes(t, "図面番号"), 2556.8, 53.5, 2.06) +
		dxfEntity("TEXT", sjisBytes(t, "日付"), 2556.8, 43.6, 2.06) +
		dxfEntity("TEXT", sjisBytes(t, "作成"), 2496.8, 43.5, 2.06)

	got := DXFTitleBlock(ParseDXFTexts([]byte(body)))
	if v, ok := got["図面番号"]; ok {
		t.Errorf("空欄なのに値を作っています: %q", v)
	}
	if len(got) != 0 {
		t.Errorf("空欄の表題欄から項目が出ています: %+v", got)
	}
}

// TestParseDXFTextsSkipsBinary は、バイナリDXF を読まない（変換器が要る）ことを
// 固定します。中途半端に読むより、読めないと分かるほうがよい。
func TestParseDXFTextsSkipsBinary(t *testing.T) {
	if got := ParseDXFTexts([]byte("AutoCAD Binary DXF\r\n\x00\x01")); got != nil {
		t.Errorf("バイナリDXFを読もうとしています: %+v", got)
	}
}

// TestDXFTitleBlockWideRowPitch は、行の高さが**文字高の3.5倍**ある様式でも
// 値が取れることを固定します。
//
// 実データ調査（2026-09-03・911件）で、行の高さは様式ごとに文字高の2.2倍〜3.5倍まで
// ばらつくと分かりました。当初の「文字高の3倍まで」という窓では、この様式の表題欄が
// **丸ごと空振り**していました（PFC2・P200 の2様式で10項目とも0件）。
func TestDXFTitleBlockWideRowPitch(t *testing.T) {
	body := dxfEntity("TEXT", sjisBytes(t, "装置名称"), 1546.5, 305.6, 7.94) +
		dxfEntity("TEXT", sjisBytes(t, "パーフェクトコーチⅡ"), 1546.5, 275.9, 12.94) // 3.7文字ぶん下

	got := DXFTitleBlock(ParseDXFTexts([]byte(body)))
	if got["装置名称"] != "パーフェクトコーチⅡ" {
		t.Errorf("行の広い様式で値が取れていません: %q（全体 %+v）", got["装置名称"], got)
	}
}

// TestDXFTitleBlockLabelsAreNotValues は、**辞書に載っているラベルは値にならない**
// ことを固定します。
//
// 窓を広げた代償で、隣の欄のラベルが値の候補に入ります。実データの構想図（表題欄が
// 未記入）で `作成: 確認` という**捏造**が出ました——「確認」は隣の欄のラベルなのに、
// 辞書に無いので値と見なされたためです。辞書は「拾う項目の一覧」であると同時に
// 「値ではないと知っている語の一覧」でもあります（2026-09-03 ユーザー提案）。
func TestDXFTitleBlockLabelsAreNotValues(t *testing.T) {
	body := dxfEntity("TEXT", sjisBytes(t, "作成"), 100.0, 50.0, 2.5) +
		dxfEntity("TEXT", sjisBytes(t, "確認"), 106.0, 50.0, 2.5) // 隣の欄のラベル

	got := DXFTitleBlock(ParseDXFTexts([]byte(body)))
	if v, ok := got["作成"]; ok {
		t.Errorf("隣のラベルを値として拾っています: 作成=%q", v)
	}
}

// TestDXFTitleBlockNearestLabelWins は、値が**一番近いラベル**のものになることを
// 固定します——枠線を読まずに「同じ枠内」を再現する仕掛けの芯です
// （2026-09-03 ユーザー提案「図面の枠線を認識して同じ枠内のラベルと値を対応付ける」の、
// 線を読まない版）。窓を広く取れるのは、この絞りがあるからです。
func TestDXFTitleBlockNearestLabelWins(t *testing.T) {
	// 「材質」と「質量」が縦に並び、値は「質量」のすぐ下にある。
	body := dxfEntity("TEXT", sjisBytes(t, "材質"), 100.0, 60.0, 2.5) +
		dxfEntity("TEXT", sjisBytes(t, "質量"), 100.0, 50.0, 2.5) +
		dxfEntity("TEXT", "4.95kg", 100.0, 45.0, 2.5)

	got := DXFTitleBlock(ParseDXFTexts([]byte(body)))
	if got["質量"] != "4.95kg" {
		t.Errorf("近いほうのラベルに割り当たっていません: %+v", got)
	}
	if v, ok := got["材質"]; ok {
		t.Errorf("遠いラベルが値を盗んでいます: 材質=%q", v)
	}
}
