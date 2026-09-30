package toho

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// writeTestAttachment は inbox の添付を1つ置きます（中身を比べる試験のため）。
func writeTestAttachment(t *testing.T, inbox, attachID, content string) {
	t.Helper()
	dir := page.AttachmentDir(inbox)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, attachID+".pdf"), []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

// TestFilingMarksSameDrawing は、既にある加工製品の候補に「同じ図面番号」「同じファイル」の印が付くことを固定します
// （2026-09-30 利用者:「同じものがあるということを提示して欲しい」「PDFの中身も比較してくれるなら」）。
//
//   - 同じ番号・同じ中身のPDF（別のメールの別の添付）→ 両方の印
//   - 同じ番号・違う中身 → 番号だけ（図番を変えない改定かもしれない）
//   - 版の印が違う番号（rev1）→ 候補には出るが「同じ番号」ではない
func TestFilingMarksSameDrawing(t *testing.T) {
	const inbox = "000055"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}
	writeTestAttachment(t, inbox, "pdf001", "%PDF-1.4 取付ベース")
	writeTestAttachment(t, inbox, "pdf002", "%PDF-1.4 取付ベース")
	writeTestAttachment(t, inbox, "pdf003", "%PDF-1.4 取付ベース（作り直し）")
	writeTestAttachment(t, inbox, "pdf004", "%PDF-1.4 取付ベース rev1")

	first := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	postFiling(t, u, []filingRequest{secondRow(first, "取付ベース", "")})

	marks := func(draft, no string) productCandidate {
		t.Helper()
		cands := productCandidates(u, "南北スポーツ", no, "取付ベース")
		markDuplicateCandidates(u, draft, cands)
		if len(cands) != 1 || cands[0].PageID != first {
			t.Fatalf("候補が既にある加工製品を指していません: %+v", cands)
		}
		return cands[0]
	}
	sameFile := makeDrawingPageFrom(t, inbox, "pdf002", "K120-1", "取付ベース", "", "南北スポーツ")
	if c := marks(sameFile, "K120-1"); !c.SameNo || !c.SameFile {
		t.Errorf("同じ番号・同じ中身なのに印が足りません: %+v", c)
	}
	remade := makeDrawingPageFrom(t, inbox, "pdf003", "k120-1", "取付ベース", "", "南北スポーツ")
	if c := marks(remade, "k120-1"); !c.SameNo || c.SameFile {
		t.Errorf("同じ番号・違う中身の印が違います: %+v", c)
	}
	rev := makeDrawingPageFrom(t, inbox, "pdf004", "K120-1 rev1", "取付ベース", "", "南北スポーツ")
	if c := marks(rev, "K120-1 rev1"); c.SameNo || c.SameFile {
		t.Errorf("版の印が違うのに同じ図面と言っています: %+v", c)
	}
}

// TestFilingDiscardsDuplicate は「重複（取り込まない）」を固定します。
//
//   - 仮のページはごみ箱へ・既にある加工製品に**このメールの受信元**が1つ足される（解析済みの印が既にある加工製品を指す）
//   - 装置名称が空でも片付けられる（行き先の欄を見ない）
//   - 同じ添付の2度目の解析なら受信元は増やさない
//   - 同じ番号でも同じファイルでもない相手・相手なしは断る（届いた図面を黙って捨てない）
func TestFilingDiscardsDuplicate(t *testing.T) {
	const inbox = "000056"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}
	writeTestAttachment(t, inbox, "pdf001", "%PDF-1.4 取付ベース")
	writeTestAttachment(t, inbox, "pdf002", "%PDF-1.4 取付ベース")
	writeTestAttachment(t, inbox, "pdf009", "%PDF-1.4 別の品目")

	first := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	postFiling(t, u, []filingRequest{secondRow(first, "取付ベース", "")})

	// 断る: 相手なし・何も共有しない相手。
	unrelated := makeDrawingPageFrom(t, inbox, "pdf009", "Z999-1", "別の品目", "", "南北スポーツ")
	if r := postFiling(t, u, []filingRequest{{PageID: unrelated, Merge: "duplicate"}}); len(r) != 1 || r[0].Outcome != "needs_choice" {
		t.Errorf("相手なしを断っていません: %+v", r)
	}
	if r := postFiling(t, u, []filingRequest{{PageID: unrelated, Merge: "duplicate", DuplicateOf: first}}); len(r) != 1 || r[0].Outcome != "skipped" {
		t.Errorf("同じ図面でない相手を断っていません: %+v", r)
	}
	if meta, ok := page.ReadSidecar(unrelated); !ok || meta.ParentID != inbox {
		t.Errorf("断ったのにページが動いた・消えた: %v %+v", ok, meta)
	}

	// 別のメールで同じPDFが届いた——装置名称は空のまま。
	again := makeDrawingPageFrom(t, inbox, "pdf002", "K120-1", "取付ベース", "", "南北スポーツ")
	r := postFiling(t, u, []filingRequest{{PageID: again, Merge: "duplicate", DuplicateOf: first}})
	if len(r) != 1 || r[0].Outcome != "discarded" || r[0].TargetID != first {
		t.Fatalf("重複として片付けていません: %+v", r)
	}
	if !strings.Contains(r[0].Message, "同じ図面番号・同じファイル") || !strings.Contains(r[0].Message, "受信元を書き足しました") {
		t.Errorf("何をしたかを言っていません: %s", r[0].Message)
	}
	if _, err := os.Stat(page.BodyPath(again)); err == nil {
		t.Errorf("仮のページが残っています")
	}
	body := readPageBody(t, first)
	if !strings.Contains(body, "<dd>"+inbox+"-pdf002</dd>") || strings.Count(body, "<dt>"+SourceRefTag+"</dt>") != 2 {
		t.Errorf("既にある加工製品に受信元が足されていません:\n%s", body)
	}
	if got, err := analyzedAttachments(u, inbox); err != nil || got["pdf002"].PageID != first {
		t.Errorf("解析済みの印が既にある加工製品を指していません: %+v %v", got, err)
	}

	// 同じ添付をもう一度解析した——受信元は増やさない。
	twice := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "", "南北スポーツ")
	r = postFiling(t, u, []filingRequest{{PageID: twice, Merge: "duplicate", DuplicateOf: first}})
	if len(r) != 1 || r[0].Outcome != "discarded" || strings.Contains(r[0].Message, "書き足しました") {
		t.Errorf("同じ添付の2度目: %+v", r)
	}
	if n := strings.Count(readPageBody(t, first), "<dt>"+SourceRefTag+"</dt>"); n != 2 {
		t.Errorf("同じ受信元を2つ書いています: %d", n)
	}
}

// TestAddTagToDrawingPicksTheBlock は、図面が2枚あるページで**同じと見えた図面**のタグの並びへ足すことを固定します。
func TestAddTagToDrawingPicksTheBlock(t *testing.T) {
	body := `<h1>取付ベース</h1><section><h2>図面</h2><dl data-type="tags"><dt>図面番号</dt><dd>A</dd></dl></section>` +
		`<section><h2>材料</h2></section>` +
		`<section><h2>図面</h2><dl data-type="tags"><dt>図面番号</dt><dd>B</dd></dl></section>`
	got, ok := addTagToDrawing(body, 1, `<dt>受信元</dt><dd>X</dd>`)
	if !ok || !strings.Contains(got, `<dd>B</dd><dt>受信元</dt><dd>X</dd></dl>`) || strings.Contains(got, `<dd>A</dd><dt>受信元`) {
		t.Errorf("2枚目の図面へ足していません:\n%s", got)
	}
	if _, ok := addTagToDrawing(body, 2, "x"); ok {
		t.Errorf("無い図面へ足したと言っています")
	}
}

// TestCompareDrawings は「🤖 中身を比べる」の口を固定します——同じファイルなら Gemini を呼ばずに答え、
// 違えば Gemini へ（キーが無い試験では 503）。返答の読み方も。
func TestCompareDrawings(t *testing.T) {
	const inbox = "000057"
	setupFilingTest(t, inbox)
	t.Setenv("GEMINI_API_KEY", "")
	u := &auth.User{Username: "alice"}
	writeTestAttachment(t, inbox, "pdf001", "%PDF-1.4 取付ベース")
	writeTestAttachment(t, inbox, "pdf002", "%PDF-1.4 取付ベース")
	writeTestAttachment(t, inbox, "pdf003", "%PDF-1.4 取付ベース（作り直し）")
	first := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	postFiling(t, u, []filingRequest{secondRow(first, "取付ベース", "")})

	call := func(draft string) (int, map[string]any) {
		b, _ := json.Marshal(map[string]string{"page_id": draft, "with": first})
		req := auth.WithUser(httptest.NewRequest("POST", "/api/compare-drawings", bytes.NewReader(b)), u)
		req.Header.Set("Content-Type", "application/json")
		rr := httptest.NewRecorder()
		CompareDrawingsAPIHandler(rr, req)
		var out map[string]any
		json.Unmarshal(rr.Body.Bytes(), &out)
		return rr.Code, out
	}
	same := makeDrawingPageFrom(t, inbox, "pdf002", "K120-1", "取付ベース", "", "南北スポーツ")
	if code, out := call(same); code != 200 || out["same"] != true || out["same_file"] != true {
		t.Errorf("同じファイルを同じと言っていません: %d %+v", code, out)
	}
	remade := makeDrawingPageFrom(t, inbox, "pdf003", "K120-1", "取付ベース", "", "南北スポーツ")
	if code, _ := call(remade); code != 503 {
		t.Errorf("違うファイルは Gemini へ行くはず（キーが無いので 503）: %d", code)
	}

	v, err := parseCompareVerdict("```json\n{\"same\": false, \"differences\": [\"寸法 120 → 125\"], \"summary\": \"寸法が違う\"}\n```")
	if err != nil || v.Same || len(v.Differences) != 1 || v.Summary != "寸法が違う" {
		t.Errorf("返答を読めていません: %+v %v", v, err)
	}
	if v, err := parseCompareVerdict(`{"same": true}`); err != nil || v.Differences == nil {
		t.Errorf("違いが無いときは空の並び: %+v %v", v, err)
	}
	if _, err := parseCompareVerdict("見比べました"); err == nil || !strings.Contains(err.Error(), "見比べました") {
		t.Errorf("読めない返答に何が返ったかを添えていません: %v", err)
	}
}
