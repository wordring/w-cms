package main

import (
	"strings"
	"testing"
)

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

// TestSkipPageFor は「移さないページ」をセクションと題で引くことを固定します（2026-10-22）——別のセクションの同じ題は外さない。
func TestSkipPageFor(t *testing.T) {
	s := settings{SkipPages: []skipPageRule{{Section: "02 記録 / 2024 / 10月 / 試作B", Title: "製造リスト", Why: "部品ではない"}}}
	if skip, why := s.skipPageFor("02 記録 / 2024 / 10月 / 試作B", " 製造リスト"); !skip || why != "部品ではない" {
		t.Errorf("外していません: %v %q", skip, why)
	}
	if skip, _ := s.skipPageFor("02 記録 / 2024 / 06月 / 試作A", "製造リスト"); skip {
		t.Error("書いていないセクションのページを外しています")
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

// TestFoldRunRevisionsDry は、この回に作ったページの中の改訂違いを見つけて、古い版だけを「旧版にする」と報告すること
// （いちばん新しい版は残す・改訂記号の前が違うページは触らない）を、下見の形で固定します（2026-10-22・foldRunRevisions）。
func TestFoldRunRevisionsDry(t *testing.T) {
	mk := func(key, no string) runPage {
		return runPage{key: key, title: key, pr: &pageRecord{DrawingNo: no}, note: &pageNote{title: key}}
	}
	a, b, c, d := mk("a", "A100-H01-12-00A"), mk("b", "A100-H01-12-00D"), mk("c", "A100-H01-12-00"), mk("d", "K120-77")
	if foldRunRevisions(nil, []runPage{a, b, c, d}, true) {
		t.Error("下見で製造の記録を書き換えたことになっています")
	}
	for _, old := range []runPage{a, c} {
		if len(old.note.infos) != 1 || !strings.Contains(old.note.infos[0], "旧版") || !strings.Contains(old.note.infos[0], "A100-H01-12-00D") {
			t.Errorf("%s を 00D の旧版にすると言っていません: %v", old.key, old.note.infos)
		}
	}
	if len(b.note.infos) != 0 || len(d.note.infos) != 0 {
		t.Errorf("いちばん新しい版か、別の図面に触っています: %v %v", b.note.infos, d.note.infos)
	}
}

// TestMachinePageWritable は、装置のページに「まとめ」を書いてよいのは、題だけか「ページ種類：装置」だけのページ（作ったばかり）か、
// 「移行中」のタグが残っているページだけであることを固定します（2026-10-22）。
func TestMachinePageWritable(t *testing.T) {
	for body, want := range map[string]bool{
		`<h1>装置A</h1>`: true,
		`<h1>装置A</h1><dl data-type="tags"><dt>ページ種類</dt><dd>装置</dd></dl>`:                       true,
		`<h1>装置A</h1><dl data-type="tags"><dt>移行中</dt><dd>確認待ち</dd></dl><h2>データ</h2>`:           true,
		`<h1>装置A</h1><dl data-type="tags"><dt>ページ種類</dt><dd>装置</dd></dl><p>人が書いた</p>`:           false,
		`<h1>装置A</h1><dl data-type="tags"><dt>ページ種類</dt><dd>装置</dd><dt>区分</dt><dd>試作</dd></dl>`: false,
	} {
		if got := machinePageWritable(body); got != want {
			t.Errorf("machinePageWritable(%q) = %v", body, got)
		}
	}
}
