package sheetmetal

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 部品ページの整理（提案→人が直す→実行）のテスト。
//
// 固定するのは、この機能の**約束**そのものです:
//
//   - **提案は何も作らない**（顧客名・装置名称のページが生まれるのは実行のときだけ）
//     ——ユーザー:「見積もりや試作の場合があるので、フォルダ名はユーザーが確認した
//     ほうが良いでしょう。実行ボタンを押してからページ作成です」
//   - **人が直した値がそのまま使われる**（`【試作】…` は機械には決められない）
//   - **既にあれば改定図面**として合流し、仮のページは片付く
//   - **空欄はそのまま**（「まだ決められない」の意思表示。通信箱が保留の置き場）

// setupFilingTest は受信箱役のページとトップページを用意します。
func setupFilingTest(t *testing.T, inboxID string) {
	t.Helper()
	setupExtTest(t, inboxID, page.PageMeta{Owner: "alice", Mode: "330"})
	// トップページ——顧客名ページの置き場（トップ直下）。
	if err := page.WriteSidecar(cms.TopPageID, page.PageMeta{Owner: "alice", Mode: "330"}); err != nil {
		t.Fatalf("トップページの用意に失敗: %v", err)
	}
	if err := cms.SyncIndex(cms.TopPageID, "<h1>トップ</h1>"); err != nil {
		t.Fatalf("トップページの索引に失敗: %v", err)
	}
}

// makeDrawingPage は解析が作るのと同じ形の部品ページを inbox の子として作ります。
func makeDrawingPage(t *testing.T, inboxID, no, name, machine, customer string) string {
	return makeDrawingPageFrom(t, inboxID, "pdf001", no, name, machine, customer)
}

// makeDrawingPageFrom は由来の添付を指定して作ります。**由来が同じなら重複**と
// みなされるので、改定を作るテストでは別の添付にすること（実際の改定は別のメールで届く）。
func makeDrawingPageFrom(t *testing.T, inboxID, attachID, no, name, machine, customer string) string {
	t.Helper()
	j := &orderJudgment{
		DocType: "drawing", DrawingNo: no, DrawingName: name,
		MachineName: machine, Customer: customer,
	}
	id, err := cms.CreateChildPage(inboxID, "alice", buildPartPageHTML(inboxID, attachID, "", j, nil))
	if err != nil {
		t.Fatalf("部品ページを作れません: %v", err)
	}
	return id
}

func getProposal(t *testing.T, u *auth.User, pageID string) []filingRow {
	t.Helper()
	req := httptest.NewRequest("GET", "/api/filing-proposal?page_id="+pageID, nil)
	if u != nil {
		req = auth.WithUser(req, u)
	}
	rr := httptest.NewRecorder()
	FilingProposalAPIHandler(rr, req)
	if rr.Code != 200 {
		t.Fatalf("提案を取れません: %d %s", rr.Code, rr.Body.String())
	}
	var res struct {
		Rows []filingRow `json:"rows"`
	}
	json.Unmarshal(rr.Body.Bytes(), &res)
	return res.Rows
}

func postFiling(t *testing.T, u *auth.User, rows []filingRequest) []filingResult {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"rows": rows})
	req := httptest.NewRequest("POST", "/api/file-drawings", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	if u != nil {
		req = auth.WithUser(req, u)
	}
	rr := httptest.NewRecorder()
	FileDrawingsAPIHandler(rr, req)
	if rr.Code != 200 {
		t.Fatalf("実行できません: %d %s", rr.Code, rr.Body.String())
	}
	var res struct {
		Results []filingResult `json:"results"`
	}
	json.Unmarshal(rr.Body.Bytes(), &res)
	return res.Results
}

func countPages(t *testing.T) int {
	t.Helper()
	var n int
	database.DB.QueryRow(`SELECT COUNT(*) FROM pages`).Scan(&n)
	return n
}

// TestFilingProposalCreatesNothing は、**提案が何も作らない**ことを固定します。
// ユーザー:「実行ボタンを押してからページ作成です」——名前が全部読めていても、
// 見積もり・試作の可能性があるので確認を経ないとフォルダは生まれません。
func TestFilingProposalCreatesNothing(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	makeDrawingPage(t, inbox, "Y050-W01-0040-03", "Φ32パイプ脚取付台", "オールラウンド2輪", "トーアスポーツマシーン")

	before := countPages(t)
	rows := getProposal(t, &auth.User{Username: "alice"}, inbox)
	if countPages(t) != before {
		t.Errorf("提案だけでページが増えました: %d -> %d", before, countPages(t))
	}
	if len(rows) != 1 {
		t.Fatalf("提案の行数が違います: %+v", rows)
	}
	// 推奨値が索引から入っていること（人はこれを直す）。
	got := rows[0]
	if got.Customer != "トーアスポーツマシーン" || got.MachineName != "オールラウンド2輪" ||
		got.DrawingName != "Φ32パイプ脚取付台" || got.DrawingNo != "Y050-W01-0040-03" {
		t.Errorf("推奨値が違います: %+v", got)
	}
}

// TestFilingProposalSkipsNonDrawings は、図面ではない子（受注ページなど）が
// 一覧に出ないことを固定します。
func TestFilingProposalSkipsNonDrawings(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	makeDrawingPage(t, inbox, "A-1", "部品A", "装置X", "客先Y")
	if _, err := cms.CreateChildPage(inbox, "alice",
		buildOrderPageHTML(inbox, "pdf002", "", sampleJudgment)); err != nil {
		t.Fatalf("受注ページを作れません: %v", err)
	}

	rows := getProposal(t, &auth.User{Username: "alice"}, inbox)
	if len(rows) != 1 || rows[0].DrawingName != "部品A" {
		t.Errorf("図面ページだけが並んでいません: %+v", rows)
	}
}

// TestFileDrawingsUsesEditedValues は、**人が直した値がそのまま使われる**ことを
// 固定します。試作の `【試作】…` は機械には決められないので、ここが効かないと
// 機能そのものが無意味になります。
func TestFileDrawingsUsesEditedValues(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	partID := makeDrawingPage(t, inbox, "Y050-1", "脚取付台", "オールラウンド2輪", "トーアスポーツ")

	// 人が装置名称を打ち替えた（メールを読んで試作と分かった）。
	results := postFiling(t, &auth.User{Username: "alice"}, []filingRequest{{
		PageID: partID, Customer: "トーアスポーツマシーン",
		MachineName: "【試作】オールラウンド2輪", DrawingName: "脚取付台",
	}})
	if len(results) != 1 || results[0].Outcome != "moved" {
		t.Fatalf("移動になっていません: %+v", results)
	}

	// トップ直下に顧客名、その下に装置名称、その下に部品ページ。
	custID, ok := findChildByTitle(cms.TopPageID, "トーアスポーツマシーン")
	if !ok {
		t.Fatalf("顧客名ページがトップ直下に作られていません")
	}
	machID, ok := findChildByTitle(custID, "【試作】オールラウンド2輪")
	if !ok {
		t.Fatalf("人が打ち替えた装置名称が使われていません（推奨値のままになっている疑い）")
	}
	if _, ok := findChildByTitle(machID, "脚取付台"); !ok {
		t.Errorf("部品ページが装置名称の下へ移っていません")
	}
	meta, _ := page.ReadSidecar(partID)
	if meta.ParentID != machID {
		t.Errorf("親が付け替わっていません: %+v", meta)
	}
}

// TestFileDrawingsSkipsEmptyFields は、空欄の行を**動かさない**ことを固定します。
// 空欄は「まだ決められない」の意思表示で、通信箱が保留の置き場です
// ——空の顧客ページを増やさないためでもあります。
func TestFileDrawingsSkipsEmptyFields(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	partID := makeDrawingPage(t, inbox, "Y050-1", "脚取付台", "", "")

	before := countPages(t)
	results := postFiling(t, &auth.User{Username: "alice"}, []filingRequest{{
		PageID: partID, Customer: "", MachineName: "", DrawingName: "脚取付台",
	}})
	if len(results) != 1 || results[0].Outcome != "skipped" {
		t.Fatalf("空欄なのに動かしています: %+v", results)
	}
	if countPages(t) != before {
		t.Errorf("空欄の行でページが作られました: %d -> %d", before, countPages(t))
	}
	meta, _ := page.ReadSidecar(partID)
	if meta.ParentID != inbox {
		t.Errorf("空欄なのに親が変わりました: %+v", meta)
	}
}

// TestFileDrawingsSecondBecomesRevision は、同じ行き先に同名のページが既にあるとき
// **改定図面として合流**し、仮のページが片付くことを固定します。
//
// ユーザー:「その部品のページが既に存在するとしたら、その図面は改定図面です」
// 「既存ページの図面の項目の先頭に配置してはどうでしょう？」
func TestFileDrawingsSecondBecomesRevision(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}

	first := makeDrawingPage(t, inbox, "Y050-1", "脚取付台", "オールラウンド2輪", "トーアスポーツ")
	postFiling(t, u, []filingRequest{{PageID: first, Customer: "トーアスポーツ",
		MachineName: "オールラウンド2輪", DrawingName: "脚取付台"}})

	// 改定図面が届いた（図面番号に改訂記号が付く形）。
	second := makeDrawingPageFrom(t, inbox, "pdf002", "Y050-1A", "脚取付台", "オールラウンド2輪", "トーアスポーツ")
	results := postFiling(t, u, []filingRequest{{PageID: second, Customer: "トーアスポーツ",
		MachineName: "オールラウンド2輪", DrawingName: "脚取付台"}})
	if len(results) != 1 || results[0].Outcome != "revision" {
		t.Fatalf("改定として扱われていません: %+v", results)
	}
	if results[0].TargetID != first {
		t.Errorf("合流先が違います: %+v", results[0])
	}

	// 合流先に図面が2つ並び、**新しいほうが先頭**であること。
	body, err := os.ReadFile(filepath.Join(page.GetPageDir(first), first+".html"))
	if err != nil {
		t.Fatalf("合流先を読めません: %v", err)
	}
	html := string(body)
	newAt, oldAt := strings.Index(html, "Y050-1A"), strings.Index(html, "<dd>Y050-1</dd>")
	if newAt < 0 || oldAt < 0 {
		t.Fatalf("図面が2つ揃っていません:\n%s", html)
	}
	if newAt > oldAt {
		t.Errorf("新しい図面が先頭にありません（改定の並びが逆）:\n%s", html)
	}

	// **社内コードが成立していること**——ユーザー:「部品の社内コードは部品ページの
	// ページ番号と改定番号を足したものになるのでは？改定番号等は、改定を記す項目の
	// data-idとなるのではないでしょうか？…すると、社内コードでその項目へ飛べる」。
	// 図面ブロックに data-id が付いていて、**改定ごとに別のID**であること。
	// 同じIDが2つあると `ページID-ブロックID` の指し先が定まらない。
	found := map[string]bool{}
	for _, m := range anyIDRe.FindAllStringSubmatch(html, -1) {
		if found[m[1]] {
			t.Errorf("ブロックIDが重複しています（社内コードが一意になりません）: %s", m[1])
		}
		found[m[1]] = true
	}

	// **改訂履歴の行が指し先**——図面ブロックは人が消せる決まりなので、消す理由の
	// 無い小さな行を社内コードの指し先にする（2026-09-03 ユーザー:「改訂履歴の項目を
	// 作り版にdata-idを割り当てれば良いのでは？」）。版ごとに別のIDであること。
	rows := revRowRe.FindAllStringSubmatch(html, -1)
	if len(rows) != 2 {
		t.Fatalf("改訂履歴が2版になっていません: %+v\n%s", rows, html)
	}
	if rows[0][1] == rows[1][1] {
		t.Errorf("版のIDが同じです（社内コードで版を区別できません）")
	}
	if rows[0][2] != "2" || rows[1][2] != "1" {
		t.Errorf("版番号の並びが違います（新しい版が上のはず）: %q %q", rows[0][2], rows[1][2])
	}

	// 仮のページは片付いている（ゴミ箱へ——物理削除ではない）。
	if _, ok := page.ReadSidecar(second); ok {
		t.Errorf("合流後も仮のページが残っています: %s", second)
	}
	if _, err := os.Stat(page.GetTrashDir(second)); err != nil {
		t.Errorf("仮のページがゴミ箱にありません: %v", err)
	}
}

// anyIDRe は本文の中のブロックIDをすべて拾います（社内コードの後半）。
var anyIDRe = regexp.MustCompile(`data-id="([0-9a-z]+)"`)

// revRowRe は改訂履歴の行（ID と 版番号）を拾います。
var revRowRe = regexp.MustCompile(`<tr data-id="([0-9a-z]+)"><td>([0-9]+)</td>`)

// TestFileDrawingsRejectsSameAttachment は、**同じ添付から作られた図面**を
// 改定にしないことを固定します。
//
// ユーザー:「同じ図面名称を2回整理すると改定になるのはちょっとマズいと思います」。
// 同じPDFを解析し直して整理に流すと、中身は同じなのに版が増えてしまいます
// ——履歴が嘘になり、社内コードも赤枠の古い図面も意味なく積み上がります。
// 由来が同じなら改定ではありえないので、確認を挟まず止めます。
func TestFileDrawingsRejectsSameAttachment(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}
	row := func(id string) filingRequest {
		return filingRequest{PageID: id, Customer: "トーアスポーツ",
			MachineName: "オールラウンド2輪", DrawingName: "脚取付台"}
	}

	first := makeDrawingPage(t, inbox, "Y050-1", "脚取付台", "オールラウンド2輪", "トーアスポーツ")
	postFiling(t, u, []filingRequest{row(first)})

	// 同じPDFをもう一度解析してしまった（由来が同じ）。
	again := makeDrawingPage(t, inbox, "Y050-1", "脚取付台", "オールラウンド2輪", "トーアスポーツ")
	results := postFiling(t, u, []filingRequest{row(again)})
	if len(results) != 1 || results[0].Outcome != "skipped" {
		t.Fatalf("同じ添付なのに合流しています: %+v", results)
	}

	// 合流先の履歴は1版のまま。
	body := readPageBody(t, first)
	if n := len(revRowRe.FindAllString(body, -1)); n != 1 {
		t.Errorf("版が増えています: %d版 %s", n, body)
	}
}

// TestFileDrawingsAsksWhenSameDrawingNo は、**図面番号が同じ**ときに人へ尋ねる
// ことを固定します。改定なら普通は番号か改訂記号が変わるので疑わしいものの、
// 番号を変えない客先もありうる——**機械には決められない**ので確認を求めます。
func TestFileDrawingsAsksWhenSameDrawingNo(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}

	first := makeDrawingPage(t, inbox, "Y050-1", "脚取付台", "オールラウンド2輪", "トーアスポーツ")
	postFiling(t, u, []filingRequest{{PageID: first, Customer: "トーアスポーツ",
		MachineName: "オールラウンド2輪", DrawingName: "脚取付台"}})

	// 別のメールで届いたが、図面番号は同じ。
	second := makeDrawingPageFrom(t, inbox, "pdf002", "Y050-1", "脚取付台", "オールラウンド2輪", "トーアスポーツ")
	req := filingRequest{PageID: second, Customer: "トーアスポーツ",
		MachineName: "オールラウンド2輪", DrawingName: "脚取付台"}

	results := postFiling(t, u, []filingRequest{req})
	if len(results) != 1 || results[0].Outcome != "needs_confirm" {
		t.Fatalf("確認を求めていません: %+v", results)
	}
	if n := len(revRowRe.FindAllString(readPageBody(t, first), -1)); n != 1 {
		t.Errorf("確認前なのに版が増えています: %d版", n)
	}

	// 人が確認した（同じ番号のまま改定する客先だった）。
	req.ConfirmRevision = true
	results = postFiling(t, u, []filingRequest{req})
	if len(results) != 1 || results[0].Outcome != "revision" {
		t.Fatalf("確認しても合流しません: %+v", results)
	}
	if n := len(revRowRe.FindAllString(readPageBody(t, first), -1)); n != 2 {
		t.Errorf("確認後に版が増えていません: %d版", n)
	}
}
