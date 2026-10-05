package toho

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"github.com/google/generative-ai-go/genai"

	"w-cms/ext/comm"
	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// 見積依頼の返事（2026-10-05・rfq_reply.go）——回答を記録・🤖 返事を読む（候補・読む・書く）。
// ⚠ **Gemini は呼びません**——読む口（rfqReadReplyAI）を偽物に差し替えて、何を渡したかと、返したものの扱いを見ます。

// rfqReplyRowHTML は見積依頼明細の1行です。
func rfqReplyRowHTML(mat, size, qty, price, note, state string) string {
	return `<tr><td>000031</td><td>材料</td><td>` + mat + `</td><td>板</td><td>` + size + `</td><td>` + qty + `</td><td>枚</td><td>` +
		price + `</td><td>` + note + `</td><td>` + state + `</td></tr>`
}

// rfqReplyBody は見積依頼書ページ（000050）の本文です。
func rfqReplyBody(rows ...string) string {
	return `<h1>見積依頼　わかば鋼業</h1><dl data-type="tags"><dt>` + RFQNoTag + `</dt><dd>000050</dd><dt>` + SupplierTag +
		`</dt><dd>わかば鋼業</dd><dt>` + RFQAnsweredTag + `</dt><dd><br/></dd></dl>` +
		`<table><caption>見積依頼明細</caption><tbody><tr><th>弊社品番</th><th>種類</th><th>材質</th><th>形状</th><th>寸法</th><th>数量</th>` +
		`<th>単位</th><th>単価</th><th>備考</th><th>状態</th></tr>` + strings.Join(rows, "") + `</tbody></table>`
}

// seedRFQReply は見積依頼書ページ 50（root）と返事のメールを置きます。
//
//	60 … 「RE: 見積依頼（№ 000050）」受信（root）——本文と添付の見積書
//	61 … 「見積依頼（№ 000050）」送信（root）——候補に出ない
//	62 … 「RE: 見積依頼（№ 000050）」受信（alice だけが読める）——候補に出ない・読めない
func seedRFQReply(t *testing.T, body string) {
	t.Helper()
	t.Setenv("GEMINI_API_KEY", "")
	setupMaterialsPermsTest(t)
	addPage(t, 0, -1, "トップ", "admin", "302", true)
	addPage(t, 50, 0, "見積依頼　わかば鋼業", "root", "302", true)
	writeBodyFile(t, 50, body)
	mail := func(id int, owner, mode, title, dir, text string) {
		addPage(t, id, 0, title, owner, mode, mode != "300")
		writeBodyFile(t, id, `<h1>`+title+`</h1><dl data-type="tags"><dt>`+comm.DirectionTag+`</dt><dd>`+dir+`</dd><dt>`+
			comm.ChannelTag+`</dt><dd>メール</dd></dl><section><h2>`+comm.MailBodyHeading+`</h2><pre>`+text+`</pre></section>`)
	}
	mail(60, "root", "302", "RE: 見積依頼（№ 000050）", comm.DirectionIn, "SS400 t6 は 1枚 1,200円 です。")
	mail(61, "root", "302", "見積依頼（№ 000050）", comm.DirectionOut, "見積依頼書をお送りします。")
	mail(62, "alice", "300", "RE: 見積依頼（№ 000050）", comm.DirectionIn, "内緒の返事")
	for _, f := range []struct{ page, name string }{
		{"000050", "FAX返事.pdf"}, {"000050", "見積依頼書 000050 20261005-090000.pdf"}, {"000060", "見積書.pdf"},
	} {
		if _, _, err := cms.SaveAttachmentFrom(f.page, "root", f.name, "upload", []byte("%PDF-1.4 "+f.name)); err != nil {
			t.Fatal(err)
		}
	}
}

var rfqRoot = &auth.User{Username: "root"}

func storedNamed(t *testing.T, pageID, name string) string {
	t.Helper()
	for stored, m := range cms.ReadAttachmentMetas(pageID) {
		if m.Name == name {
			return stored
		}
	}
	t.Fatalf("%s に %s がありません", pageID, name)
	return ""
}

// TestRFQAnsweredMarksPricedRows は「✓ 回答を記録」——単価の入った未回答の行だけを「回答あり」にし、回答日に今日を書く・
// 記録する行が無ければ断って版を作らない——を固定します。
func TestRFQAnsweredMarksPricedRows(t *testing.T) {
	seedRFQReply(t, rfqReplyBody(
		rfqReplyRowHTML("SS400", "t6*80*120", "6", "1200", "", rfqLineUnanswered),
		rfqReplyRowHTML("A5052", "t3*50*50", "2", "", "", rfqLineUnanswered),
		rfqReplyRowHTML("SUS304", "t2*40*40", "4", "500", "", rfqLineDeclined),
	))
	code, out := postRFQ(t, rfqRoot, RFQAnsweredAPIHandler, map[string]any{"page_id": "000050"})
	if code != 200 || out["changed"] != float64(1) {
		t.Fatalf("回答を記録できません: %d %v", code, out)
	}
	body := readPageBody(t, "000050")
	v, _ := readRFQTable(body)
	var states []string
	for _, r := range v.replyRows() {
		states = append(states, r.Status)
	}
	if strings.Join(states, ",") != rfqLineAnswered+","+rfqLineUnanswered+","+rfqLineDeclined {
		t.Errorf("状態が %v です（単価の入った未回答の行だけ回答あり）", states)
	}
	if !strings.Contains(body, "<dt>"+RFQAnsweredTag+"</dt><dd>"+rfqToday()+"</dd>") {
		t.Errorf("回答日に今日が入っていません:\n%s", body)
	}
	// 断るときは本文のファイルに触らない（書き直すと更新日時が進み、ページが「更新された」ことになる）。
	st, _ := os.Stat(page.BodyPath("000050"))
	if code, _ := postRFQ(t, rfqRoot, RFQAnsweredAPIHandler, map[string]any{"page_id": "000050"}); code != 409 {
		t.Errorf("記録する行が無いのに %d（409 のはず）", code)
	}
	if after, _ := os.Stat(page.BodyPath("000050")); !after.ModTime().Equal(st.ModTime()) {
		t.Errorf("⚠ 断ったのに本文のファイルを書き直しました")
	}
}

// TestRFQApplyReplyWritesCheckedRows は、確かめた返事を書く口——単価（全角・カンマ・円を畳む）と回答あり・辞退・備考
// （既にあれば「／」で足す）・回答日——と、読んだあとに行がずれていたら何も書かないことを固定します。
func TestRFQApplyReplyWritesCheckedRows(t *testing.T) {
	seedRFQReply(t, rfqReplyBody(
		rfqReplyRowHTML("SS400", "t6*80*120", "6", "", "", rfqLineUnanswered),
		rfqReplyRowHTML("A5052", "t3*50*50", "2", "", "", rfqLineUnanswered),
		rfqReplyRowHTML("SUS304", "t2*40*40", "4", "", "既存", rfqLineUnanswered),
	))
	before := readPageBody(t, "000050")
	v, _ := readRFQTable(before)
	rows := v.replyRows()
	if rows[0].What != "材料 SS400 板 t6*80*120 ×6枚" {
		t.Fatalf("何の値段かが %q です", rows[0].What)
	}
	// ずれていたら書かない（1行でも）。
	for _, bad := range []rfqReplyLine{{Row: 1, What: "違う", Price: "5"}, {Row: 9, What: rows[0].What, Price: "5"}} {
		lines := []rfqReplyLine{{Row: 2, What: rows[1].What, Price: "7"}, bad}
		if code, _ := postRFQ(t, rfqRoot, RFQApplyReplyAPIHandler, map[string]any{"page_id": "000050", "lines": lines}); code != 409 {
			t.Errorf("ずれた行 %+v で %d（409 のはず）", bad, code)
		}
	}
	if readPageBody(t, "000050") != before {
		t.Fatalf("⚠ 断ったのに本文が変わりました")
	}
	code, out := postRFQ(t, rfqRoot, RFQApplyReplyAPIHandler, map[string]any{"page_id": "000050", "lines": []rfqReplyLine{
		{Row: 1, What: rows[0].What, Price: "１，２００円"},
		{Row: 2, What: rows[1].What, Declined: true, Note: "材料が無い"},
		{Row: 3, What: rows[2].What, Note: "納期2週"},
	}})
	if code != 200 || out["changed"] != float64(3) {
		t.Fatalf("書けません: %d %v", code, out)
	}
	after := readPageBody(t, "000050")
	v, _ = readRFQTable(after)
	got := v.replyRows()
	if got[0].Price != "1200" || got[0].Status != rfqLineAnswered {
		t.Errorf("1行目が %+v です（単価 1200・回答あり）", got[0])
	}
	if got[1].Status != rfqLineDeclined || v.text(v.rows[1], "備考") != "材料が無い" {
		t.Errorf("2行目が %+v・備考 %q です（辞退・材料が無い）", got[1], v.text(v.rows[1], "備考"))
	}
	if got[2].Status != rfqLineUnanswered || v.text(v.rows[2], "備考") != "既存／納期2週" {
		t.Errorf("3行目が %+v・備考 %q です（未回答のまま・備考に足す）", got[2], v.text(v.rows[2], "備考"))
	}
	if !strings.Contains(after, "<dt>"+RFQAnsweredTag+"</dt><dd>"+rfqToday()+"</dd>") {
		t.Errorf("回答日に今日が入っていません")
	}
}

// TestRFQReadReplyGuessesWithoutWriting は 🤖 返事を読む——ファイルなら Blob（MIME）・メールなら本文を指示に入れて渡し、
// 行の一覧（何の値段か）を指示に入れ、返ってきた案の単価を畳み・範囲の外と重なりを捨て、**本文は書かない**——を固定します。
func TestRFQReadReplyGuessesWithoutWriting(t *testing.T) {
	seedRFQReply(t, rfqReplyBody(
		rfqReplyRowHTML("SS400", "t6*80*120", "6", "", "", rfqLineUnanswered),
		rfqReplyRowHTML("A5052", "t3*50*50", "2", "", "", rfqLineUnanswered),
	))
	var gotPrompt string
	var gotBlobs []genai.Blob
	orig := rfqReadReplyAI
	rfqReadReplyAI = func(prompt string, blobs ...genai.Blob) (string, error) {
		gotPrompt, gotBlobs = prompt, blobs
		return "```json\n" + `{"rows":[{"row":1,"unit_price":"1,200円"},{"row":9,"unit_price":"1"},{"row":2,"declined":true,"note":"材料が無い"},{"row":1,"unit_price":"9"}],"summary":"SS400 は 1200円"}` + "\n```", nil
	}
	t.Cleanup(func() { rfqReadReplyAI = orig })
	before := readPageBody(t, "000050")

	code, out := postRFQ(t, rfqRoot, RFQReadReplyAPIHandler, map[string]any{
		"page_id": "000050", "source_page": "000050", "source_file": storedNamed(t, "000050", "FAX返事.pdf")})
	if code != 200 {
		t.Fatalf("読めません: %d %v", code, out)
	}
	if len(gotBlobs) != 1 || gotBlobs[0].MIMEType != "application/pdf" || string(gotBlobs[0].Data) != "%PDF-1.4 FAX返事.pdf" {
		t.Errorf("返事のファイルを渡していません: %+v", gotBlobs)
	}
	if !strings.Contains(gotPrompt, "1: 材料 SS400 板 t6*80*120 ×6枚\n2: 材料 A5052 板 t3*50*50 ×2枚") || !strings.Contains(gotPrompt, "計算はしないでください") {
		t.Errorf("指示に行の一覧か「計算しない」がありません:\n%s", gotPrompt)
	}
	b, _ := json.Marshal(out["guesses"])
	var guesses []rfqReplyGuess
	json.Unmarshal(b, &guesses)
	if len(guesses) != 2 || guesses[0] != (rfqReplyGuess{Row: 1, UnitPrice: "1200"}) ||
		guesses[1] != (rfqReplyGuess{Row: 2, Declined: true, Note: "材料が無い"}) {
		t.Errorf("案が %s です（単価を畳む・範囲の外と重なりは捨てる）", b)
	}
	if out["summary"] != "SS400 は 1200円" {
		t.Errorf("要点が %v です", out["summary"])
	}
	if readPageBody(t, "000050") != before {
		t.Errorf("⚠ 読むだけなのに本文が変わりました")
	}

	// メールの本文——Blob は渡さず、本文を指示に入れる。
	gotBlobs = nil
	if code, out := postRFQ(t, rfqRoot, RFQReadReplyAPIHandler, map[string]any{"page_id": "000050", "source_page": "000060"}); code != 200 {
		t.Fatalf("メールの本文を読めません: %d %v", code, out)
	}
	if len(gotBlobs) != 0 || !strings.Contains(gotPrompt, "返事のメールの本文:\nSS400 t6 は 1枚 1,200円 です。") {
		t.Errorf("メールの本文を渡していません: blobs=%d\n%s", len(gotBlobs), gotPrompt)
	}
	// 読めない返事は「無い」と同じ顔。
	if code, _ := postRFQ(t, rfqRoot, RFQReadReplyAPIHandler, map[string]any{"page_id": "000050", "source_page": "000062"}); code != 404 {
		t.Errorf("読めないメールを読もうとして %d（404 のはず）", code)
	}
}

// TestRFQReplySources は 🤖 で読む返事の候補——このページのファイル（作った見積依頼書の PDF は除く）と、題に
// 「№ <このページ>」を含む**受信**メールの本文と添付（読めるものだけ）——を固定します。
func TestRFQReplySources(t *testing.T) {
	seedRFQReply(t, rfqReplyBody(rfqReplyRowHTML("SS400", "t6*80*120", "6", "", "", rfqLineUnanswered)))
	req := auth.WithUser(httptest.NewRequest("GET", "/api/rfq/reply-sources?page_id=000050", nil), rfqRoot)
	rr := httptest.NewRecorder()
	RFQReplySourcesAPIHandler(rr, req)
	var out struct {
		Sources []rfqReplySource `json:"sources"`
	}
	json.Unmarshal(rr.Body.Bytes(), &out)
	var labels []string
	for _, s := range out.Sources {
		labels = append(labels, s.Label+"@"+s.PageID)
	}
	want := "このページ: FAX返事.pdf@000050|メール: RE: 見積依頼（№ 000050）（本文）@000060|メール: RE: 見積依頼（№ 000050） の 見積書.pdf@000060"
	if strings.Join(labels, "|") != want {
		t.Errorf("候補が違います:\n got  %v\n want %v", strings.Join(labels, "|"), want)
	}
}
