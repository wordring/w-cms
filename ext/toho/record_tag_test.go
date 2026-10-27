package toho

import (
	"regexp"
	"strings"
	"testing"

	"w-cms/ext/comm"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
)

// 「通信記録」のタグ（2026-10-27・record_tag.go）。
// 利用者:「これは、メールから図面を追加や更新したなら図面につけるべきですが、加工製品ページを作ったなら加工製品ページ冒頭のタグに
// 付くことになると思います」

// recordTagsIn は本文の「通信記録」の値を、ページの頭（最初の節より前）と節の中に分けて返します。
func recordTagsIn(body string) (top, inSections []string) {
	re := regexp.MustCompile(`<dt>` + comm.RecordRefTag + `</dt>\s*<dd>\s*([^<]*?)\s*</dd>`)
	cut := strings.Index(body, "<section")
	if cut < 0 {
		cut = len(body)
	}
	for _, m := range re.FindAllStringSubmatchIndex(body, -1) {
		v := body[m[2]:m[3]]
		if m[0] < cut {
			top = append(top, v)
		} else {
			inSections = append(inSections, v)
		}
	}
	return
}

// TestRecordTagOnNewProductPages は、メールから加工製品ページを作ると（解析・空のページ）、ページの頭のタグに通信記録が
// 付き、図面のブロックには付かないことを固定します。
func TestRecordTagOnNewProductPages(t *testing.T) {
	j := &orderJudgment{DocType: "drawing", DrawingNo: "X1", DrawingName: "ブラケット"}
	analyzed := testProductPage("000042", "pdf-a", j, nil)
	blank, err := buildBlankProductHTML(testProductTemplate(), "000042", "")
	if err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"解析": analyzed, "空のページ": blank} {
		top, in := recordTagsIn(body)
		if strings.Join(top, ",") != "000042" || len(in) != 0 {
			t.Errorf("⚠ %s: 通信記録がページの頭に1つ（000042）のはず: 頭 %v・節の中 %v\n%s", name, top, in, body)
		}
	}
}

// TestRecordTagMovesWithDrawing は、整理の図面追加で既にあるページへ図面を運ぶと、運んだ図面のブロックに、その図面が届いた
// 通信記録が付くことと、同じ値を二重に書かないことを固定します。
func TestRecordTagMovesWithDrawing(t *testing.T) {
	const inbox = "000042"
	setupFilingTest(t, inbox)
	user := &auth.User{Username: "alice"}
	mail2, err := cms.CreateChildPage(inbox, "alice", "<h1>2通目のメール</h1>")
	if err != nil {
		t.Fatal(err)
	}
	dst := makeDrawingPageFrom(t, inbox, "pdf-a", "X1", "ブラケット", "装置A", "客A")
	src := makeDrawingPageFrom(t, mail2, "pdf-b", "X2", "ブラケット", "装置A", "客A")
	if err := mergeAsDrawing(user, src, dst); err != nil {
		t.Fatalf("mergeAsDrawing: %v", err)
	}
	body, _ := cms.ReadPageBody(dst)
	top, in := recordTagsIn(body)
	if strings.Join(top, ",") != inbox || strings.Join(in, ",") != mail2 {
		t.Errorf("⚠ 頭は %s・運んだ図面のブロックは %s のはず: 頭 %v・節の中 %v\n%s", inbox, mail2, top, in, body)
	}

	// 二重に書かない。
	block := `<section><h2>図面</h2><dl data-type="tags"><dt>受信元</dt><dd>` + mail2 + `-pdfb</dd>` + recordTagPair(mail2) + `</dl></section>`
	if got := withRecordTag(block, recordOfMovingBlock(block, "")); strings.Count(got, comm.RecordRefTag) != 1 {
		t.Errorf("⚠ 同じ通信記録を二重に書きました: %s", got)
	}
	if got := recordPageOfSource("001160-xyub"); got != "001160" {
		t.Errorf("受信元から通信記録のページを読めません: %q", got)
	}
	if got := recordPageOfSource("A100-B01"); got != "" {
		t.Errorf("ページ番号でない値を通信記録にしました: %q", got)
	}
}
