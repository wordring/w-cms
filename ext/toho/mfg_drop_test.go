package toho

import (
	"os"
	"strings"
	"testing"

	"w-cms/internal/cms/page"
)

// TestMfgDropEmptyPage は、取り消しで製造明細表が空になった製造ページを、消しても何も失わないときだけごみ箱へ移すことを固定します
// （2026-10-23・mfg_drop.go）。利用者:「その条件で、取り消しで製造明細表が空になった製造ページを自動でごみ箱へ移す形を作ってください。」
//   - 行が残っているあいだは消さない。最後の行を外すと消し、「製造」の置き場を返す。外した数は製造待ちへ戻る。
//   - 人が書き足した製造ページ・ほかのページから指されている製造ページは、空になっても残し、理由を返す。
func TestMfgDropEmptyPage(t *testing.T) {
	setupExtTest(t, "000980", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	u := adminUser()
	addPage(t, 981, -1, "案件", "alice", "302", true)
	seedWaitOrder(t, 982, 981, "あけぼの精工", "2026-11-01", waitItem("A-1", "ブラケット", "100", "2026-11-01")+waitItem("A-2", "カバー", "5", "2026-11-01"))
	pick := func(row int, item string) map[string]any { return map[string]any{"order": "000982", "row": row, "item": item} }
	start := func(items ...map[string]any) string {
		t.Helper()
		var in []any
		for _, it := range items {
			in = append(in, it)
		}
		postRFQ(t, u, MfgWaitAPIHandler, map[string]any{"items": in}) // 取り消しで製造待ちに戻った行は、もう入っている（409 でよい）
		code, out := postRFQ(t, u, MfgStartAPIHandler, map[string]any{"items": in})
		if code != 200 {
			t.Fatalf("開始できません: %d %v", code, out)
		}
		return out["page_id"].(string)
	}
	exists := func(id string) bool {
		_, err := os.Stat(page.GetPageDir(id))
		return err == nil
	}

	// 1. 何も書き足していない製造ページ: 1行目では消さず、最後の行で消す。
	mfg := start(pick(0, "A-1"), pick(1, "A-2"))
	code, out := postRFQ(t, u, MfgBackAPIHandler, map[string]any{"page_id": mfg, "row": 1, "item": "A-2"})
	if code != 200 || out["dropped"] != nil || out["kept"] != nil {
		t.Fatalf("⚠ まだ行がある製造ページで消す・残すを答えています: %d %v", code, out)
	}
	code, out = postRFQ(t, u, MfgBackAPIHandler, map[string]any{"page_id": mfg, "row": 0, "item": "A-1"})
	if code != 200 || out["dropped"] != true || out["box"] == "" {
		t.Fatalf("⚠ 空になった製造ページをごみ箱へ移していません: %d %v", code, out)
	}
	if exists(mfg) {
		t.Errorf("⚠ ごみ箱へ移したはずの製造ページ /%s が残っています", mfg)
	}
	if _, err := os.Stat(page.GetTrashDir(mfg)); err != nil {
		t.Errorf("⚠ /%s がごみ箱にありません: %v", mfg, err)
	}
	if w := waitsOf(t, 982); w["A-1"] != 100 || w["A-2"] != 5 {
		t.Errorf("⚠ 外した数が製造待ちに戻っていません: %v", w)
	}

	// 2. 人が書き足した製造ページは、空になっても残す。
	mfg = start(pick(0, "A-1"))
	body := readPageBody(t, mfg)
	writeBodyFile(t, pageNum(mfg), body+`<p>材料は来週入る</p>`)
	code, out = postRFQ(t, u, MfgBackAPIHandler, map[string]any{"page_id": mfg, "row": 0, "item": "A-1"})
	if kept, _ := out["kept"].(string); code != 200 || out["dropped"] != nil || !strings.Contains(kept, "テンプレートに無い書き込み") {
		t.Fatalf("⚠ 人が書き足した製造ページを消したか、理由を言っていません: %d %v", code, out)
	}
	if !exists(mfg) {
		t.Fatalf("⚠ 書き足した製造ページ /%s が消えています", mfg)
	}

	// 3. ほかのページから指されている製造ページは、空になっても残す（指している所を言う）。
	mfg = start(pick(0, "A-1"))
	addPage(t, 989, 981, "書類", "alice", "302", true)
	syncBody(t, 989, `<h1>書類</h1><dl data-type="tags"><dt>`+MfgRefTag+`</dt><dd>`+mfg+`</dd></dl>`)
	code, out = postRFQ(t, u, MfgBackAPIHandler, map[string]any{"page_id": mfg, "row": 0, "item": "A-1"})
	if kept, _ := out["kept"].(string); code != 200 || out["dropped"] != nil || !strings.Contains(kept, "/000989") {
		t.Fatalf("⚠ 指されている製造ページを消したか、指している所を言っていません: %d %v", code, out)
	}
	if !exists(mfg) {
		t.Fatalf("⚠ 指されている製造ページ /%s が消えています", mfg)
	}
}

// TestBodyShapeIgnoresEditorFormatting は、エディタで保存し直しただけの本文（字下げ・data-id・空欄の <br>）を「同じ形」と見ることを固定します。
// 違いを見落とすと人の書き込みを消し、余計に見分けると空の製造ページが残り続けます。
func TestBodyShapeIgnoresEditorFormatting(t *testing.T) {
	machine := `<h1>カバー</h1><dl data-type="tags"><dt>客先</dt><dd>みなと商店</dd><dt>備考</dt><dd></dd></dl>` +
		`<h2>作業開始</h2><table><caption>製造明細表</caption><tbody><tr><th>品番</th><th>数量</th></tr></tbody></table>`
	edited := "<h1 data-id=\"ab12\">カバー（題は変わってよい）</h1>\n<dl data-id=\"cd34\" data-type=\"tags\">\n    <dt>客先</dt>\n    <dd>みなと商店</dd>\n" +
		"    <dt>備考</dt>\n    <dd><br/></dd>\n</dl>\n<h2 data-id=\"ef56\">作業開始</h2>\n<table data-id=\"gh78\">\n    <caption>製造明細表</caption>\n" +
		"    <tbody>\n        <tr>\n            <th>品番</th>\n            <th>数量</th>\n        </tr>\n    </tbody>\n</table>"
	a, _ := bodyShape(machine)
	b, _ := bodyShape(edited)
	if a != b {
		t.Errorf("⚠ 保存し直しただけの本文を違う形と見ています:\n%s\n%s", a, b)
	}
	for _, more := range []string{`<p>メモ</p>`, `<section data-type="file-view" data-ref="000001-abcd"></section>`,
		`<table><caption>手配不要表</caption><tbody><tr><th>品名</th></tr><tr><td></td></tr></tbody></table>`} {
		if c, _ := bodyShape(machine + more); c == a {
			t.Errorf("⚠ 書き足したもの %q を見落としています", more)
		}
	}
}
