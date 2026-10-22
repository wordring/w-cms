package main

import "testing"

// TestOwnReads は、1つの PDF に一式の図面が入っていたとき、**このページのものだけ**を名乗ることを固定します
// （2026-09-30 利用者:「一つのPDFに複数の図面が入っている場合もあり得ます」）。
func TestOwnReads(t *testing.T) {
	main := drawingRead{No: "K120-01-00", Name: "本体製缶"}
	set := []drawingRead{
		{No: "K120-01-00", Name: "標準2輪 本体製缶"},
		{No: "K120-02-00", Name: "標準2輪 本体製缶(溶接指示図)"},
		{No: "K120-05-03", Name: "側板(SW側)"},
		{No: "K120-701-01", Name: "カバー(右)"},
	}
	own, others := ownReads(set, main, true)
	if len(own) != 2 || own[0].No != "K120-01-00" || own[1].No != "K120-02-00" {
		t.Errorf("このページの図面（同じ番号・名前が主な図面を含む）を選べていません: %+v", own)
	}
	if len(others) != 2 || others[0].No != "K120-05-03" || others[1].No != "K120-701-01" {
		t.Errorf("別の加工製品の図面を外せていません: %+v", others)
	}
	// 1枚だけのファイルは、このページに添えられた図面（別の名前でも減らさない）。
	if own, others := ownReads([]drawingRead{{No: "K120-09", Name: "溶接図"}}, main, true); len(own) != 1 || len(others) != 0 {
		t.Errorf("1枚だけのファイルを外しています: %+v %+v", own, others)
	}
	// 主な図面が分からない・どれも当てはまらないときは全部返す（決められないので減らさない）。
	if own, _ := ownReads(set[2:], main, true); len(own) != 2 {
		t.Errorf("当てはまらないときに減らしています: %+v", own)
	}
	if own, _ := ownReads(set, drawingRead{}, false); len(own) != 4 {
		t.Errorf("主な図面が無いときに減らしています: %+v", own)
	}
}

// TestRevisionSplit は図面番号を改訂記号の前（基）と改訂記号に分けることと、改訂の新旧の比べ方を固定します（2026-10-22・olderRevisionOf）。
// 利用者:「旧版として子ページに入れる（道具を直します）出お願いします」——02 記録の古い版の図面を、w-cms の今の版の子にするため。
func TestRevisionSplit(t *testing.T) {
	for _, c := range []struct{ no, base, rev string }{
		{"A100-B01-02-02A", normNo("A100-B01-02-02"), "A"},
		{"A100-B01-02-02B", normNo("A100-B01-02-02"), "B"},
		{"A100-K10-02-08-1-C", normNo("A100-K10-02-08-1"), "C"},
		{"A100-001-00 B", normNo("A100-001-00"), "B"},
		{"K120-P16-0003_rev0", normNo("K120-P16-0003"), "0"},
		{"K120-131-3", normNo("K120-131-3"), ""},
		{"Ｋ１２０－１３１－３Ａ", normNo("K120-131-3"), "A"}, // 全角でも同じ基
		{"PK050_114", normNo("PK050_114"), ""},
	} {
		if base, rev := revisionSplit(c.no); base != c.base || rev != c.rev {
			t.Errorf("revisionSplit(%q) = %q, %q（want %q, %q）", c.no, base, rev, c.base, c.rev)
		}
	}
	for _, c := range []struct {
		a, b string
		want bool
	}{
		{"B", "A", true}, {"A", "B", false}, {"A", "", true}, {"", "A", false}, {"E", "C", true}, {"10", "9", true}, {"A", "A", false},
	} {
		if got := revNewer(c.a, c.b); got != c.want {
			t.Errorf("revNewer(%q, %q) = %v", c.a, c.b, got)
		}
	}
	// 改訂記号の前が違えば別の図面（A100 と A200 は別の品物）。
	if a, _ := revisionSplit("A100-L09-01-09A"); a == func() string { b, _ := revisionSplit("A200-L09-01-09C"); return b }() {
		t.Error("A100 と A200 の基が同じになっています")
	}
}

// TestDrawingNoHintFor は「図面番号の当て」をセクションと題で引くこと（前後の空白は見ない・別のセクションの同じ題には当てない）を
// 固定します（2026-10-22）。
func TestDrawingNoHintFor(t *testing.T) {
	s := settings{DrawingNoHints: []drawingNoHint{
		{Section: "02 記録 / 2024 / 06月 / 試作A", Title: "本体補強", No: "A100-002", From: "案件ページの品番"},
		{Section: "02 記録 / 2024 / 10月 / 試作B", Title: "本体補強", No: "A200-002"},
	}}
	if h := s.drawingNoHintFor("02 記録 / 2024 / 06月 / 試作A", " 本体補強 "); h.No != "A100-002" || h.From != "案件ページの品番" {
		t.Errorf("当たりません: %+v", h)
	}
	if h := s.drawingNoHintFor("02 記録 / 2024 / 10月 / 試作B", "本体補強"); h.No != "A200-002" {
		t.Errorf("セクションで分けていません: %+v", h)
	}
	if h := s.drawingNoHintFor("02 記録 / 2024 / 07月 / 試作C", "本体補強"); h.No != "" {
		t.Errorf("書いていないセクションに当てています: %+v", h)
	}
}
