package subcon

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"

	"w-cms/ext/comm"
	"w-cms/ext/comm/contacts"
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
	makeDrawingPage(t, inbox, "K120-W01-0040-03", "Φ32パイプ取付ベース", "標準2輪", "南北スポーツ機械")

	before := countPages(t)
	rows := getProposal(t, &auth.User{Username: "alice"}, inbox)
	if countPages(t) != before {
		t.Errorf("提案だけでページが増えました: %d -> %d", before, countPages(t))
	}
	if len(rows) != 1 {
		t.Fatalf("提案の行数が違います: %+v", rows)
	}
	// 推奨値が索引から入っていること（人はこれを直す）。
	//
	// **`Φ` は `φ` に畳まれます**（2026-09-06 ユーザー:「材料や図面にΦという記号が
	// 多く出てきます。大小の揺れを正規化しましょう」）。NFKC は大小を変換しないので、
	// 設定の置き換え表（`char_folding`）が効いた形です。
	got := rows[0]
	if got.Customer != "南北スポーツ機械" || got.MachineName != "標準2輪" ||
		got.DrawingName != "φ32パイプ取付ベース" || got.DrawingNo != "K120-W01-0040-03" {
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
// 固定します。顧客名の打ち替えと、**段の選択**（試作か現行か）は機械には決められない
// ——メールを読むしかない——ので、ここが効かないと機能そのものが無意味になります。
func TestFileDrawingsUsesEditedValues(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	partID := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")

	// 人が顧客名を打ち替え、段に「試作」を選んだ（メールを読んで試作と分かった）。
	results := postFiling(t, &auth.User{Username: "alice"}, []filingRequest{{
		PageID: partID, Customer: "南北スポーツ機械", Stage: "試作",
		MachineName: "標準2輪", DrawingName: "取付ベース",
	}})
	if len(results) != 1 || results[0].Outcome != "moved" {
		t.Fatalf("移動になっていません: %+v", results)
	}

	// 「取引先」の下に顧客名、その下に装置名称、その下に部品ページ
	// （2026-09-05 ユーザー決定。トップ直下が名簿になるのを避けた）。
	boxID, ok := findChildByTitle(cms.TopPageID, CustomerBoxTitle)
	if !ok {
		t.Fatalf("「%s」ページが作られていません", CustomerBoxTitle)
	}
	custID, ok := findChildByTitle(boxID, "南北スポーツ機械")
	if !ok {
		t.Fatalf("顧客名ページが「%s」の下に作られていません", CustomerBoxTitle)
	}
	// 装置の上に段が1枚入る（2026-09-05 ユーザー決定）。
	stageID, ok := findChildByTitle(custID, "試作")
	if !ok {
		t.Fatalf("段のページ（試作）が作られていません")
	}
	machID, ok := findChildByTitle(stageID, "標準2輪")
	if !ok {
		t.Fatalf("人が打ち替えた装置名称が使われていません（推奨値のままになっている疑い）")
	}
	if _, ok := findChildByTitle(machID, "取付ベース"); !ok {
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
	partID := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "", "")

	before := countPages(t)
	results := postFiling(t, &auth.User{Username: "alice"}, []filingRequest{{
		PageID: partID, Customer: "", MachineName: "", DrawingName: "取付ベース",
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
// ユーザー:「その部品のページが既に存在するとしたら、その図面は改定図面です」。
// **旧版は最新版の子ページ**になります（2026-09-06 ユーザー:「旧版を最も新しい版の
// 子にしてはどうでしょう」）——もとは同じページに積み上げる形でした。
func TestFileDrawingsSecondBecomesRevision(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	u := &auth.User{Username: "alice"}

	first := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	postFiling(t, u, []filingRequest{{PageID: first, Customer: "南北スポーツ", Stage: "現行",
		MachineName: "標準2輪", DrawingName: "取付ベース"}})

	// 改定図面が届いた（図面番号に改訂記号が付く形）。
	second := makeDrawingPageFrom(t, inbox, "pdf002", "K120-1A", "取付ベース", "標準2輪", "南北スポーツ")
	results := postFiling(t, u, []filingRequest{{PageID: second, Customer: "南北スポーツ", Stage: "現行",
		MachineName: "標準2輪", DrawingName: "取付ベース"}})
	if len(results) != 1 || results[0].Outcome != "revision" {
		t.Fatalf("改定として扱われていません: %+v", results)
	}
	if results[0].TargetID != first {
		t.Errorf("合流先が違います: %+v", results[0])
	}

	// 合流先に載るのは**最新の図面だけ**——旧版は子ページへ出ている。
	body, err := os.ReadFile(filepath.Join(page.GetPageDir(first), first+".html"))
	if err != nil {
		t.Fatalf("合流先を読めません: %v", err)
	}
	html := string(body)
	if !strings.Contains(html, "<dd>K120-1A</dd>") {
		t.Fatalf("最新の図面が載っていません:\n%s", html)
	}
	if strings.Contains(html, "<dd>K120-1</dd>") {
		t.Errorf("旧版が最新版のページに残っています（子へ移っていない）:\n%s", html)
	}
	if n := strings.Count(html, "<h2>図面</h2>"); n != 1 {
		t.Errorf("図面ブロックが%d個あります（最新の1つだけのはず）:\n%s", n, html)
	}

	// 旧版が**子ページ**になっていて、そこに旧版の図面ブロックがあること。
	oldID, ok := findChildByTitle(first, "旧版 K120-1 取付ベース")
	if !ok {
		t.Fatalf("旧版の子ページがありません")
	}
	oldBody, err := os.ReadFile(filepath.Join(page.GetPageDir(oldID), oldID+".html"))
	if err != nil {
		t.Fatalf("旧版ページを読めません: %v", err)
	}
	// 由来（受信元）もブロックごと付いて行く——出所を失わない。
	if !strings.Contains(string(oldBody), "<dd>K120-1</dd>") ||
		!strings.Contains(string(oldBody), "000012-pdf001") {
		t.Errorf("旧版ページに図面と由来が移っていません:\n%s", oldBody)
	}

	// 改訂履歴の旧版の行が、その子ページへのリンクになっていること。
	if !strings.Contains(html, `<a href="/`+oldID+`">K120-1</a>`) {
		t.Errorf("改訂履歴から旧版ページへ飛べません:\n%s", html)
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
		return filingRequest{PageID: id, Customer: "南北スポーツ",
			MachineName: "標準2輪", DrawingName: "取付ベース"}
	}

	first := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	postFiling(t, u, []filingRequest{row(first)})

	// 同じPDFをもう一度解析してしまった（由来が同じ）。
	again := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
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

	first := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	postFiling(t, u, []filingRequest{{PageID: first, Customer: "南北スポーツ", Stage: "現行",
		MachineName: "標準2輪", DrawingName: "取付ベース"}})

	// 別のメールで届いたが、図面番号は同じ。
	second := makeDrawingPageFrom(t, inbox, "pdf002", "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	req := filingRequest{PageID: second, Customer: "南北スポーツ", Stage: "現行",
		MachineName: "標準2輪", DrawingName: "取付ベース"}

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

// TestSenderAddressOfFollowsSourceRef は、**下請けが通信記録から差出人を引く鎖**を
// 固定します（2026-09-16）。
//
// この鎖は顧客名の推奨の土台です:
//
//	部品ページ → `受信元` タグ → 通信記録 → `差出人` → 取引先ページ → その題
//
// 2026-09-16 まで**試験がありませんでした**。タグ名は生の文字列で書かれていて
// （`name = '差出人'`）、通信側が欄の名前を変えても**エラーにならず、顧客名の
// 推奨がただ空になる**——空欄は「まだ読めていない」に見えるので、誰も気づきません
// （実際 2026-09-13 に `差出人アドレス` を廃止したとき、送信の控えが同じ壊れ方を
// しました）。名前は定数（`comm.FromTag`）を通しましたが、**畳んだ値がアドレスで
// あること**は定数では表せないので、ここで確かめます。
func TestSenderAddressOfFollowsSourceRef(t *testing.T) {
	setupFilingTest(t, "000100")

	// ① 通信記録——`差出人` は `名前 <アドレス>`。畳んだ値がアドレスだけになる
	//    （列型 email。`config/settings.json` の `vocabulary` が決めます）。
	if err := page.WriteSidecar("000110", page.PageMeta{
		Owner: "alice", Mode: "330", ParentID: "000100",
	}); err != nil {
		t.Fatalf("通信記録のサイドカー: %v", err)
	}
	record := "<h1>見積もりのお願い</h1>" +
		`<dl data-type="tags">` +
		"<dt>" + comm.FromTag + "</dt><dd>山田 太郎 &lt;yamada@example.co.jp&gt;</dd>" +
		"</dl>"
	if err := cms.SyncIndex("000110", record); err != nil {
		t.Fatalf("通信記録の索引: %v", err)
	}

	// ② 部品ページ——由来は「ページID-添付ID」。ハイフンの前だけが元ページ。
	if err := page.WriteSidecar("000111", page.PageMeta{
		Owner: "alice", Mode: "330", ParentID: "000110",
	}); err != nil {
		t.Fatalf("部品ページのサイドカー: %v", err)
	}
	part := "<h1>ブラケット</h1>" +
		`<dl data-type="tags">` +
		"<dt>" + SourceRefTag + "</dt><dd>000110-c3p7</dd>" +
		"</dl>"
	if err := cms.SyncIndex("000111", part); err != nil {
		t.Fatalf("部品ページの索引: %v", err)
	}

	if got := senderAddressOf(111); got != "yamada@example.co.jp" {
		t.Errorf("由来をたどって差出人アドレスを引けません: %q", got)
	}
	// 由来が無ければ空（新しい顧客の1通目や、手で作った部品ページ）。
	// **空を返すのは正常**です——呼ぶ側は読めた名前へ戻ります。
	if got := senderAddressOf(110); got != "" {
		t.Errorf("由来の無いページで空を返していません: %q", got)
	}
}

// TestFilingLinksToContactsBook は、整理が**2つの木を参照タグで結ぶ**ことを
// 固定します（2026-09-16）。
//
// 2026-09-16 にアドレス帳（`連絡帳`）と部品階層（`取引先`）を別の木に分けました。
// **題だけで結んでいると、どちらかを改名した日に切れます**——部品階層のフォルダ名も
// 連絡帳の社名も、人が直すものです。参照はページIDなので切れません。
//
// **人が選んだ社名で引きます**（機械が推した組織ではなく）——整理の画面は
// 「機械が出して人が直す」場所なので、打ち替えた結果が正です。
func TestFilingLinksToContactsBook(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)

	// 連絡帳に組織を1枚（アドレス帳で登録した状態）。
	user := &auth.User{Username: "alice"}
	bookID, err := contacts.EnsureContactsBox(user)
	if err != nil {
		t.Fatalf("連絡帳を作れません: %v", err)
	}
	orgID, err := cms.CreateChildPage(bookID, "alice",
		"<h1>南北スポーツ機械</h1><dl data-type=\"tags\"><dt>取引</dt><dd>顧客</dd></dl>")
	if err != nil {
		t.Fatalf("組織ページを作れません: %v", err)
	}

	partID := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")
	results := postFiling(t, user, []filingRequest{{
		PageID: partID, Customer: "南北スポーツ機械", Stage: "現行",
		MachineName: "標準2輪", DrawingName: "取付ベース",
	}})
	if len(results) != 1 || results[0].Outcome != "moved" {
		t.Fatalf("移動になっていません: %+v", results)
	}

	boxID, ok := findChildByTitle(cms.TopPageID, CustomerBoxTitle)
	if !ok {
		t.Fatalf("「%s」が作られていません", CustomerBoxTitle)
	}
	custID, ok := findChildByTitle(boxID, "南北スポーツ機械")
	if !ok {
		t.Fatal("顧客名ページがありません")
	}
	body, err := cms.ReadPageBody(custID)
	if err != nil {
		t.Fatal(err)
	}
	want := "<dt>" + comm.CounterpartTag + "</dt><dd>" + orgID + "</dd>"
	if !strings.Contains(body, want) {
		t.Errorf("連絡帳への参照がありません: %s（%s を期待）", body, want)
	}

	// **2度目で増えません**（同じタグが2つ並ばない）。
	part2 := makeDrawingPage(t, inbox, "K120-2", "脚受け", "標準2輪", "南北スポーツ")
	postFiling(t, user, []filingRequest{{
		PageID: part2, Customer: "南北スポーツ機械", Stage: "現行",
		MachineName: "標準2輪", DrawingName: "脚受け",
	}})
	body, _ = cms.ReadPageBody(custID)
	if n := strings.Count(body, want); n != 1 {
		t.Errorf("参照タグが%d個あります（1個を期待）: %s", n, body)
	}
}

// TestFilingWithoutContactsBookEntry は、**連絡帳に居ない相手では結ばない**ことを
// 固定します。新しい顧客の1通目がこの形で、**結べないのは異常ではありません**。
func TestFilingWithoutContactsBookEntry(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	user := &auth.User{Username: "alice"}

	partID := makeDrawingPage(t, inbox, "K130-1", "台座", "新型機", "はじめての客先")
	results := postFiling(t, user, []filingRequest{{
		PageID: partID, Customer: "はじめての客先", Stage: "現行",
		MachineName: "新型機", DrawingName: "台座",
	}})
	// **整理そのものは通ります**——結べないからといって止めません。
	if len(results) != 1 || results[0].Outcome != "moved" {
		t.Fatalf("結べないだけで整理が止まりました: %+v", results)
	}
	boxID, _ := findChildByTitle(cms.TopPageID, CustomerBoxTitle)
	custID, ok := findChildByTitle(boxID, "はじめての客先")
	if !ok {
		t.Fatal("顧客名ページがありません")
	}
	body, _ := cms.ReadPageBody(custID)
	if strings.Contains(body, "<dt>"+comm.CounterpartTag+"</dt>") {
		t.Errorf("居ない相手へ参照を書きました: %s", body)
	}
}

// TestUnlinkedCustomersOnlyShowsFixable は、警告が**直せるものだけ**を出すことを
// 固定します（2026-09-16・§5.3）。
//
// **全部を警告にすると狼少年になります**——FAXだけ・電話だけの客先は正当に
// 連絡先ゼロで、それは異常ではありません。直しようのないものを並べると、
// 人は一覧ごと信用しなくなります（未処理一覧で一度学んだこと）。
//
// ⚠ 設計文書（§5.3）の条件は、**そのままでは一度も発火しませんでした**——
// 「アドレスから題に解決できるのに連絡先が無い」は、解決に連絡先のタグが要るので
// 自分を打ち消します。ここで使うのは整理が辿る鎖（`受信元` → 通信記録 → `差出人`）です。
func TestUnlinkedCustomersOnlyShowsFixable(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	user := &auth.User{Username: "alice"}

	// ⚠ **ページIDは機械が採ります**（明示しないこと）。最初はここで `009201` の
	// ように書いていて、**自動採番の部品ページを上書き**していました——`板` が
	// 9202、`軸` が 9203 になり、`受信元` の鎖が切れて**番人が一度も効きません**
	// でした（2026-09-16 に変異試験で発覚）。
	recBody := "<h1>受信</h1><dl data-type=\"tags\">" +
		"<dt>チャネル</dt><dd>メール</dd>" +
		"<dt>差出人</dt><dd>山田 太郎 &lt;yamada@example.co.jp&gt;</dd></dl>"
	faxBody := "<h1>FAX</h1><dl data-type=\"tags\">" +
		"<dt>チャネル</dt><dd>FAX</dd></dl>"

	// ① メール由来の部品（`受信元` → 通信記録に `差出人` がある）。
	mailPart := makeDrawingPage(t, inbox, "K140-1", "腕", "メール機", "メールの客先")
	mailRec := newRecordForTest(t, recBody, inbox)
	// ② FAX由来の部品（通信記録に `差出人` が無い）。
	faxPart := makeDrawingPage(t, inbox, "K150-1", "板", "FAX機", "FAXの客先")
	faxRec := newRecordForTest(t, faxBody, inbox)
	// ③ **メールも来ていて、連絡帳にも居る相手**——警告してはいけない側の本命。
	linkedPart := makeDrawingPage(t, inbox, "K160-1", "軸", "済機", "結びつく客先")
	linkedRec := newRecordForTest(t, recBody, inbox)
	bookID, err := contacts.EnsureContactsBox(user)
	if err != nil {
		t.Fatalf("連絡帳: %v", err)
	}
	if _, err := cms.CreateChildPage(bookID, "alice",
		"<h1>結びつく客先</h1><dl data-type=\"tags\"><dt>取引</dt><dd>顧客</dd></dl>"); err != nil {
		t.Fatalf("組織ページ: %v", err)
	}

	setSourceRef(t, mailPart, mailRec)
	setSourceRef(t, faxPart, faxRec)
	setSourceRef(t, linkedPart, linkedRec)

	for _, f := range []struct{ part, customer, machine, drawing string }{
		{mailPart, "メールの客先", "メール機", "腕"},
		{faxPart, "FAXの客先", "FAX機", "板"},
		{linkedPart, "結びつく客先", "済機", "軸"},
	} {
		if r := postFiling(t, user, []filingRequest{{
			PageID: f.part, Customer: f.customer, Stage: "現行",
			MachineName: f.machine, DrawingName: f.drawing,
		}}); len(r) != 1 || r[0].Outcome != "moved" {
			t.Fatalf("整理できません（%s）: %+v", f.customer, r)
		}
	}

	list := UnlinkedCustomers(user)
	titles := map[string]string{}
	for _, c := range list {
		titles[c.Title] = c.Address
	}
	if addr, ok := titles["メールの客先"]; !ok {
		t.Errorf("メールが来ている相手が出ていません: %+v", list)
	} else if addr != "yamada@example.co.jp" {
		t.Errorf("証拠のアドレスが違います: %q", addr)
	}
	// **ここが本丸**——FAXだけの客先は静かなまま。
	if _, ok := titles["FAXの客先"]; ok {
		t.Errorf("FAXだけの客先を警告しました（狼少年になります）: %+v", list)
	}
	// **もう1つの本丸**——結びついている相手も静かなまま。片付けたものが翌日も
	// 並んでいると、一覧そのものが信用されなくなります。
	if _, ok := titles["結びつく客先"]; ok {
		t.Errorf("連絡帳と結びついている相手を警告しました: %+v", list)
	}
}

// newRecordForTest は通信記録らしいページを1枚作り、そのIDを返します。
//
// ⚠ **IDは機械に採らせます**——明示すると、自動採番のページと衝突して
// **静かに上書き**します（この試験で実際に起きました）。
func newRecordForTest(t *testing.T, body, parentID string) string {
	t.Helper()
	id, err := cms.CreateChildPage(parentID, "alice", body)
	if err != nil {
		t.Fatalf("記録ページを作れません: %v", err)
	}
	return id
}

// setSourceRef は部品ページの `受信元` を書き替えます（どの記録から来たかを差し替える）。
func setSourceRef(t *testing.T, partID, recID string) {
	t.Helper()
	body, err := cms.ReadPageBody(partID)
	if err != nil {
		t.Fatal(err)
	}
	re := regexp.MustCompile(`<dt>` + SourceRefTag + `</dt><dd>[^<]*</dd>`)
	body = re.ReplaceAllString(body, "<dt>"+SourceRefTag+"</dt><dd>"+recID+"-a1b2</dd>")
	if err := os.WriteFile(
		filepath.Join(page.GetPageDir(partID), partID+".html"), []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
	if err := cms.SyncIndex(partID, body); err != nil {
		t.Fatal(err)
	}
}
