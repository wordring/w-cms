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
