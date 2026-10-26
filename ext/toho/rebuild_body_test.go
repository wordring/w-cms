package toho

import (
	"regexp"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// 既にあるページの本文を書く4か所（整理の図面の項目・整理の発注元・製造完了のセル・案件明細のセル）が、
// 錠の中で読み直した本文から組み直すことを固定します（2026-10-19 夜・【考察】文書の食い違い §3.3 の形・rebuildBody）。
// それまでは錠の外で読んだ本文から組んだものを書いていたので、読んでから書くまでのあいだにほかの人が保存すると、その保存が消えた。

// betweenReadAndWrite は、rebuildBody が錠の外で組んでから錠の中で書くまでのあいだに、bob の保存を挟みます。
// change は bob が本文に加える変更です。挟んだ回数を返す関数も返します（挟まっていなければ試験の前提が崩れている）。
func betweenReadAndWrite(t *testing.T, change func(cur string) string) func() int {
	t.Helper()
	n := 0
	rebuildBeforeWrite = func(id string) {
		n++
		if err := cms.RewriteBody(id, "bob", change); err != nil {
			t.Fatalf("ほかの人の保存: %v", err)
		}
	}
	t.Cleanup(func() { rebuildBeforeWrite = nil })
	return func() int { return n }
}

func appendBob(cur string) string { return cur + "<p>bob の書き込み</p>" }

// shapeOf は、本文から上の段のブロック ID とタグのあいだの空白を外した形です（機械の書き替えは、ID の無いブロックに
// 画面と同じ ID を入れる——2026-10-26・cms.RewriteBody。試験が本文の形を文字で比べるとき用）。
func shapeOf(body string) string {
	body = regexp.MustCompile(` data-id="[0-9a-z]+"`).ReplaceAllString(body, "")
	return regexp.MustCompile(`>[ \t\r\n]+<`).ReplaceAllString(body, "><")
}

// TestSyncDrawingFieldsKeepsConcurrentSave は、整理が図面の項目を揃えるあいだのほかの人の保存が残ることを固定します。
func TestSyncDrawingFieldsKeepsConcurrentSave(t *testing.T) {
	setupExtTest(t, "000120", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	seedBody(t, "000120", `<h1>留めブラケット</h1><section><h2>図面</h2><dl data-type="tags">`+
		`<dt>`+ClientNameTag+`</dt><dd>株式会社みなと商店</dd><dt>`+MachineNameTag+`</dt><dd>ボール回転機</dd></dl></section>`)
	hit := betweenReadAndWrite(t, appendBob)
	if err := syncDrawingFields(&auth.User{Username: "alice"}, "000120", "みなと商店", "ボール回転機", ""); err != nil {
		t.Fatalf("揃えられません: %v", err)
	}
	if hit() != 1 {
		t.Fatalf("ほかの人の保存を挟めていません（%d 回）", hit())
	}
	body := readPageBody(t, "000120")
	if !strings.Contains(body, "<dt>"+ClientNameTag+"</dt><dd>みなと商店</dd>") || !strings.Contains(body, "bob の書き込み") {
		t.Errorf("⚠ 揃えた客先とほかの人の保存の両方が残っていません:\n%s", body)
	}
}

// TestSyncOrderClientKeepsConcurrentSave は、整理が発注元を書き戻すあいだのほかの人の保存が残ることを固定します。
func TestSyncOrderClientKeepsConcurrentSave(t *testing.T) {
	setupExtTest(t, "000130", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	seedBody(t, "000130", `<h1>受注</h1><dl data-type="tags"><dt>発注書番号</dt><dd>PO-1</dd>`+
		`<dt>`+OrderClientTag+`</dt><dd>株式会社あけぼの精工</dd></dl>`)
	hit := betweenReadAndWrite(t, appendBob)
	if err := syncOrderClient(&auth.User{Username: "alice"}, "000130", "あけぼの精工"); err != nil {
		t.Fatalf("書き戻せません: %v", err)
	}
	if hit() != 1 {
		t.Fatalf("ほかの人の保存を挟めていません（%d 回）", hit())
	}
	body := readPageBody(t, "000130")
	if !strings.Contains(body, "<dt>"+OrderClientTag+"</dt><dd>あけぼの精工</dd>") || !strings.Contains(body, "bob の書き込み") {
		t.Errorf("⚠ 書き戻した発注元とほかの人の保存の両方が残っていません:\n%s", body)
	}
}

// TestMfgMadeKeepsConcurrentSave は、製造完了のセルを書くあいだのほかの人の保存が残ること、
// そのあいだに同じセルが変わったら（錠の中の照合で）409 で断って、ほかの人の値を残すことを固定します。
func TestMfgMadeKeepsConcurrentSave(t *testing.T) {
	setupExtTest(t, "000140", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	u := adminUser()
	addPage(t, 141, -1, "案件", "alice", "302", true)
	seedOrder(t, 142, 141, "あけぼの精工", "2026-11-01", item("A-1", "ブラケット", "100", "2026-11-01", ""))
	code, out := postRFQ(t, u, MfgNewAPIHandler, map[string]any{"items": []map[string]any{{"order": "000142", "row": 0, "item": "A-1", "qty": "40"}}})
	if code != 200 {
		t.Fatalf("製造ページを作れません: %d %v", code, out)
	}
	mfg := out["page_id"].(string)

	hit := betweenReadAndWrite(t, appendBob)
	if code, out := postRFQ(t, u, MfgMadeAPIHandler, map[string]any{"page_id": mfg, "row": 0, "item_no": "A-1", "old": "", "value": "済"}); code != 200 {
		t.Fatalf("製造完了を書けません: %d %v", code, out)
	}
	if hit() != 1 {
		t.Fatalf("ほかの人の保存を挟めていません（%d 回）", hit())
	}
	if body := readPageBody(t, mfg); !strings.Contains(body, "<td>済</td>") || !strings.Contains(body, "bob の書き込み") {
		t.Errorf("⚠ 製造完了とほかの人の保存の両方が残っていません:\n%s", body)
	}

	// そのあいだに bob が同じセルを空に戻したら、「済 → 空」のつもりの書き込みは 409（bob の値が残る）。
	betweenReadAndWrite(t, func(cur string) string { return strings.Replace(cur, "<td>済</td>", "<td>bob</td>", 1) })
	if code, out := postRFQ(t, u, MfgMadeAPIHandler, map[string]any{"page_id": mfg, "row": 0, "item_no": "A-1", "old": "済", "value": ""}); code != 409 {
		t.Errorf("⚠ 錠の中で照合していません（409 のはず）: %d %v", code, out)
	}
	if body := readPageBody(t, mfg); !strings.Contains(body, "<td>bob</td>") {
		t.Errorf("⚠ ほかの人の値が消えています:\n%s", body)
	}
}

// TestOrderItemEditKeepsConcurrentSave は、案件明細のセルを書くあいだのほかの人の保存が残ること、
// そのあいだに同じセルが変わったら（錠の中の照合で）409 で断って、ほかの人の値を残すことを固定します。
func TestOrderItemEditKeepsConcurrentSave(t *testing.T) {
	setupExtTest(t, "000150", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	u := adminUser()
	addPage(t, 151, -1, "受注", "alice", "302", true)
	seedBody(t, "000151", `<h1>受注</h1><dl data-type="tags"><dt>発注書番号</dt><dd>PO-1</dd></dl>`+
		`<table data-type="`+clientOrderItemsType+`"><caption>案件明細表</caption><tbody>`+
		`<tr><th>弊社品番</th><th>品番</th><th>品名</th><th>数量</th><th>備考</th></tr>`+
		item2("A-1", "ブラケット", "100")+item2("A-2", "カバー", "5")+`</tbody></table>`)

	hit := betweenReadAndWrite(t, appendBob)
	if code, out := postRFQ(t, u, OrderItemEditAPIHandler, map[string]any{"page_id": "000151", "row": 1, "item_no": "A-2", "field": "備考", "old": "", "value": "急ぎ"}); code != 200 {
		t.Fatalf("書けません: %d %v", code, out)
	}
	if hit() != 1 {
		t.Fatalf("ほかの人の保存を挟めていません（%d 回）", hit())
	}
	if body := readPageBody(t, "000151"); !strings.Contains(body, "<td>急ぎ</td>") || !strings.Contains(body, "bob の書き込み") {
		t.Errorf("⚠ 書いたセルとほかの人の保存の両方が残っていません:\n%s", body)
	}

	// そのあいだに bob が同じセルを直したら、409（bob の値が残る）。
	betweenReadAndWrite(t, func(cur string) string { return strings.Replace(cur, "<td>急ぎ</td>", "<td>bob</td>", 1) })
	if code, out := postRFQ(t, u, OrderItemEditAPIHandler, map[string]any{"page_id": "000151", "row": 1, "item_no": "A-2", "field": "備考", "old": "急ぎ", "value": "至急"}); code != 409 {
		t.Errorf("⚠ 錠の中で照合していません（409 のはず）: %d %v", code, out)
	}
	if body := readPageBody(t, "000151"); !strings.Contains(body, "<td>bob</td>") || strings.Contains(body, "至急") {
		t.Errorf("⚠ ほかの人の値が消えています:\n%s", body)
	}
}

// item2 は案件明細表の1行（弊社品番・品番・品名・数量・備考）を組みます。
func item2(itemNo, name, qty string) string {
	return `<tr><td></td><td>` + itemNo + `</td><td>` + name + `</td><td>` + qty + `</td><td></td></tr>`
}

// TestMergeRevisionKeepsConcurrentSave は、整理の図面改定（mergeAsRevision）が合流先を書き換えるあいだのほかの人の保存が残ることを固定します（2026-10-20）。
// それまでは錠の外で読んだ合流先の本文から組んで書いていたので、そのあいだの保存が消えた。
func TestMergeRevisionKeepsConcurrentSave(t *testing.T) {
	const inbox = "000042"
	setupFilingTest(t, inbox)
	user := &auth.User{Username: "alice"}
	dst := makeDrawingPageFrom(t, inbox, "pdf-a", "Q1", "ブラケット", "装置A", "客A")
	src := makeDrawingPageFrom(t, inbox, "pdf-b", "Q2", "ブラケット", "装置A", "客A")
	hit := betweenReadAndWrite(t, appendBob)
	if err := mergeAsRevision(user, src, dst); err != nil {
		t.Fatalf("mergeAsRevision: %v", err)
	}
	if hit() != 1 {
		t.Fatalf("ほかの人の保存を挟めていません（%d 回）", hit())
	}
	body := readPageBody(t, dst)
	if !strings.Contains(body, "bob の書き込み") || !strings.Contains(body, "Q2") {
		t.Errorf("⚠ 新しい図面とほかの人の保存の両方が残っていません:\n%s", body)
	}
}

// TestMergeRevisionRefusesWhenDrawingMoved は、そのあいだに合流先の図面そのものが変わったら、書かずに断り、
// 作りかけた旧版ページを片付けることを固定します（旧版へ運んだ図面と、合流先に残る図面が食い違わないように）。
func TestMergeRevisionRefusesWhenDrawingMoved(t *testing.T) {
	const inbox = "000042"
	setupFilingTest(t, inbox)
	user := &auth.User{Username: "alice"}
	dst := makeDrawingPageFrom(t, inbox, "pdf-a", "Q1", "ブラケット", "装置A", "客A")
	src := makeDrawingPageFrom(t, inbox, "pdf-b", "Q2", "ブラケット", "装置A", "客A")

	betweenReadAndWrite(t, func(cur string) string { return strings.Replace(cur, "<dd>Q1</dd>", "<dd>Q1-bob</dd>", 1) })
	if err := mergeAsRevision(user, src, dst); err == nil {
		t.Fatalf("図面が変わったのに断りませんでした")
	}
	body := readPageBody(t, dst)
	if !strings.Contains(body, "Q1-bob") || strings.Contains(body, "Q2") {
		t.Errorf("⚠ 断ったのに合流先が変わっています:\n%s", body)
	}
	if _, ok := findChildByTitle(dst, "旧版 Q1 ブラケット"); ok {
		t.Errorf("⚠ 作りかけた旧版ページが残っています")
	}
	if _, err := cms.ReadPageBody(src); err != nil {
		t.Errorf("⚠ 断ったのに仮のページが片付けられました")
	}

}

// TestMergeDrawingKeepsConcurrentSave は、図面追加（mergeAsDrawing）でも同じことを固定します。
func TestMergeDrawingKeepsConcurrentSave(t *testing.T) {
	const inbox = "000042"
	setupFilingTest(t, inbox)
	user := &auth.User{Username: "alice"}
	dst := makeDrawingPageFrom(t, inbox, "pdf-a", "Q1", "ブラケット", "装置A", "客A")
	src := makeDrawingPageFrom(t, inbox, "pdf-b", "Q3", "溶接図", "装置A", "客A")
	hit := betweenReadAndWrite(t, appendBob)
	if err := mergeAsDrawing(user, src, dst); err != nil {
		t.Fatalf("mergeAsDrawing: %v", err)
	}
	if hit() != 1 {
		t.Fatalf("ほかの人の保存を挟めていません（%d 回）", hit())
	}
	body := readPageBody(t, dst)
	if !strings.Contains(body, "bob の書き込み") || !strings.Contains(body, "Q3") || !strings.Contains(body, "Q1") {
		t.Errorf("⚠ 足した図面とほかの人の保存の両方が残っていません:\n%s", body)
	}
}

// TestRFQNewDocKeepsOtherDraftTable は、部材見積依頼書を作ったあとの片付けが、読んでから片付けるまでのあいだに表の番号がずれても、
// 別の業者の見積依頼部材表を消さないことを固定します（2026-10-20）。それまでは錠の中で「その番号の表」をそのまま消していた。
func TestRFQNewDocKeepsOtherDraftTable(t *testing.T) {
	const box = "000040"
	setupExtTest(t, box, page.PageMeta{Owner: "root", Mode: "330"})
	first := tableOfLinesHTML(RFQDraftType, []ourOrderLine{{Kind: "購入部品", ItemID: "M8-20", ItemName: "六角ボルト", Quantity: "10", Unit: "本"}}, true)
	second := tableOfLinesHTML(RFQDraftType, []ourOrderLine{{Kind: "購入部品", ItemID: "M6-10", ItemName: "なべ小ねじ", Quantity: "40", Unit: "本"}}, true)
	seedBody(t, box, `<h1>`+RFQBoxTitle+`</h1>`+first+second)
	// ほかの人が先に1枚目から作って片付けた（2枚目が1枚目になる）。
	betweenReadAndWrite(t, func(cur string) string {
		return regexp.MustCompile(`(?s)<table[^>]*>(?:(?:[^<]|<[^/]|</[^t]|</t[^a])*?)六角ボルト.*?</table>`).ReplaceAllString(cur, "")
	})
	code, out := postRFQ(t, &auth.User{Username: "root", IsAdmin: true}, RFQNewDocAPIHandler,
		map[string]any{"page_id": box, "table": 1, "supplier": "わかば鋼業"})
	if code != 200 {
		t.Fatalf("部材見積依頼書を作れません: %d %v", code, out)
	}
	after := readPageBody(t, box)
	if !strings.Contains(after, "なべ小ねじ") {
		t.Errorf("⚠ 別の見積依頼部材表が消えました:\n%s", after)
	}
	if out["draft_note"] == nil {
		t.Errorf("片付けられなかったことを知らせていません: %v", out)
	}
}

// TestOrderReturnRowRefusesShiftedRow は、「必要部材表へ戻す」で、読んでから外すまでのあいだにほかの人の保存で行がずれたら、
// 番号だけで別の行を外さずに 409 で断ることを固定します（order_return.go・2026-10-20）。
func TestOrderReturnRowRefusesShiftedRow(t *testing.T) {
	setupExtTest(t, "000152", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	u := adminUser()
	seedBody(t, "000152", returnTestBody())

	// bob が、表の先頭に行を1つ足す（2行目だった FB がずれて3行目に）。
	hit := betweenReadAndWrite(t, func(cur string) string {
		return strings.Replace(cur, "<tr><td>000031</td><td>鉄</td><td>板</td>",
			"<tr><td>000031</td><td>鉄</td><td>丸棒</td><td>φ10</td><td>1</td><td>未発注</td></tr><tr><td>000031</td><td>鉄</td><td>板</td>", 1)
	})
	if code, out := postRFQ(t, u, OrderReturnRowAPIHandler, map[string]any{"page_id": "000152", "row": 2}); code != 409 {
		t.Errorf("⚠ ずれた行を外しています（409 のはず）: %d %v", code, out)
	}
	if hit() != 1 {
		t.Fatalf("ほかの人の保存を挟めていません（%d 回）", hit())
	}
	body := readPageBody(t, "000152")
	for _, keep := range []string{"t3.2*100*200", "t4.5*75*1090", "t9*100*150", "φ10"} {
		if !strings.Contains(body, keep) {
			t.Errorf("⚠ %s の行が消えています:\n%s", keep, body)
		}
	}

	// ずれていなければ、指した行を外す。
	rebuildBeforeWrite = nil
	if code, out := postRFQ(t, u, OrderReturnRowAPIHandler, map[string]any{"page_id": "000152", "row": 3}); code != 200 {
		t.Fatalf("外せません: %d %v", code, out)
	}
	if body := readPageBody(t, "000152"); strings.Contains(body, "t4.5*75*1090") || !strings.Contains(body, "φ10") {
		t.Errorf("指した行（FB）だけを外していません:\n%s", body)
	}
}
