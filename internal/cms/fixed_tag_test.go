package cms

import (
	"encoding/json"
	"net/http/httptest"
	neturl "net/url"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// 作るときに決めるタグ（fixed_tag.go・2026-10-27）の試験。語の名前と値は、コアの試験の中だけの架空のもの（「書類種別」）。

const fixedTestWord = "書類種別"

// withFixedWord は、設定の語彙に「作るときに決める語」を1つ足します（人が選べるのは「メモ」「台帳」）。
func withFixedWord(t *testing.T) {
	t.Helper()
	withSettings(t, func(s *Settings) {
		v := map[string]VocabWord{}
		for k, w := range s.Vocabulary {
			v[k] = w
		}
		v[fixedTestWord] = VocabWord{Type: ColEnum, Values: []string{"メモ", "台帳", "請求"}, Fixed: true, Creatable: []string{"メモ", "台帳"}}
		s.Vocabulary = v
	})
}

func kindTags(v string) string {
	return `<dl data-type="tags" data-id="tg01"><dt>` + fixedTestWord + `</dt><dd>` + v + `</dd><dt>メモ欄</dt><dd>x</dd></dl>`
}

func TestKeepFixedTagsRestoresForNonAdmin(t *testing.T) {
	setupSaveTest(t)
	withFixedWord(t)
	before := `<h1 data-id="h001">a</h1>` + kindTags("台帳")
	carol := &auth.User{Username: "carol"}

	// 替えた → 戻す
	after := `<h1 data-id="h001">a</h1>` + kindTags("請求")
	got, kept := KeepFixedTags(carol, before, after)
	if len(kept) != 1 || kept[0] != fixedTestWord || !sameTagValues(tagValuesInBody(got, fixedTestWord), []string{"台帳"}) {
		t.Fatalf("替えた値が戻っていない: kept=%v\n%s", kept, got)
	}
	if !strings.Contains(got, "<dd>x</dd>") {
		t.Errorf("ほかのタグまで消えた:\n%s", got)
	}
	// 消した → 戻す（タグの並びごと消しても）
	got, kept = KeepFixedTags(carol, before, `<h1 data-id="h001">a</h1><p>本文</p>`)
	if len(kept) != 1 || !sameTagValues(tagValuesInBody(got, fixedTestWord), []string{"台帳"}) {
		t.Fatalf("消した値が戻っていない: kept=%v\n%s", kept, got)
	}
	// 無かったのに足した → 消す
	got, kept = KeepFixedTags(carol, `<h1>a</h1><dl data-type="tags"><dt>メモ欄</dt><dd>x</dd></dl>`,
		`<h1>a</h1><dl data-type="tags"><dt>メモ欄</dt><dd>x</dd><dt>`+fixedTestWord+`</dt><dd>請求</dd></dl>`)
	if len(kept) != 1 || len(nonEmpty(tagValuesInBody(got, fixedTestWord))) != 0 {
		t.Fatalf("足した値が消えていない: kept=%v\n%s", kept, got)
	}
	// 2つめの組を足した → 消す
	got, _ = KeepFixedTags(carol, before, `<h1 data-id="h001">a</h1>`+kindTags("台帳")+
		`<section><dl data-type="tags"><dt>`+fixedTestWord+`</dt><dd>請求</dd></dl></section>`)
	if !sameTagValues(tagValuesInBody(got, fixedTestWord), []string{"台帳"}) {
		t.Fatalf("足した2つめの組が残った:\n%s", got)
	}
	// 変えていない → そのまま（書き直さない）
	if got, kept := KeepFixedTags(carol, before, before+"<p>足した</p>"); len(kept) != 0 || got != before+"<p>足した</p>" {
		t.Errorf("変えていないのに書き直した: kept=%v", kept)
	}
	// 管理者は替えられる
	if got, kept := KeepFixedTags(&auth.User{Username: "root", IsAdmin: true}, before, after); len(kept) != 0 || got != after {
		t.Errorf("管理者の値まで戻した: kept=%v", kept)
	}
}

// TestSyncKeepsFixedTag は、/api/sync で管理者でない人が書類の種類を替えても、元の値のまま保存され、画面へ描き直させることを確かめます。
func TestSyncKeepsFixedTag(t *testing.T) {
	setupSaveTest(t)
	withFixedWord(t)
	const id = "000951"
	newPage(t, id, `<h1 data-id="h001">記録</h1>`+kindTags("台帳")+`<p data-id="p001">本文</p>`,
		page.PageMeta{Owner: "carol", Mode: page.DefaultMode, ParentID: TopPageID})

	post := func(req SyncRequest) (int, map[string]any) {
		payload, _ := json.Marshal(req)
		r := httptest.NewRequest("POST", "/api/sync", strings.NewReader(string(payload)))
		r = auth.WithUser(r, &auth.User{Username: "carol"})
		rr := httptest.NewRecorder()
		SyncAPIHandler(rr, r)
		var out map[string]any
		json.Unmarshal(rr.Body.Bytes(), &out)
		return rr.Code, out
	}
	code, open := post(SyncRequest{PageID: id, Full: true})
	if code != 200 {
		t.Fatalf("開けない: %d", code)
	}
	var base []SyncBase
	var order []string
	for _, b := range open["blocks"].([]any) {
		m := b.(map[string]any)
		base = append(base, SyncBase{ID: m["id"].(string), H: m["h"].(string)})
		order = append(order, m["id"].(string))
	}
	h := ""
	for _, b := range base {
		if b.ID == "tg01" {
			h = b.H
		}
	}
	// 種類を「請求」に替え、同じタグの並びの別の値も直す。
	code, resp := post(SyncRequest{PageID: id, Base: base, Order: order, Changes: []SyncChange{{ID: "tg01", BaseH: h,
		HTML: `<dl data-type="tags" data-id="tg01"><dt>` + fixedTestWord + `</dt><dd>請求</dd><dt>メモ欄</dt><dd>y</dd></dl>`}}})
	if code != 200 {
		t.Fatalf("保存できない: %d %v", code, resp)
	}
	body := bodyOfFile(t, id)
	if !sameTagValues(tagValuesInBody(body, fixedTestWord), []string{"台帳"}) {
		t.Fatalf("⚠ 管理者でない人が種類を替えられた:\n%s", body)
	}
	if !strings.Contains(body, "<dd>y</dd>") {
		t.Errorf("同じ並びのほかのタグの直しまで消えた:\n%s", body)
	}
	if ft, _ := resp["fixed_tags"].([]any); len(ft) != 1 || ft[0] != fixedTestWord {
		t.Errorf("戻したことを返していない: %v", resp["fixed_tags"])
	}
	redraw := false
	for _, b := range resp["blocks"].([]any) {
		m := b.(map[string]any)
		if m["id"] == "tg01" && m["why"] == "fixed" && strings.Contains(m["html"].(string), "台帳") {
			redraw = true
		}
	}
	if !redraw {
		t.Errorf("戻したブロックを画面へ描き直させていない: %v", resp["blocks"])
	}
}

func newPageForm(t *testing.T, u *auth.User, vals neturl.Values) *httptest.ResponseRecorder {
	t.Helper()
	req := httptest.NewRequest("POST", "/api/new-page", strings.NewReader(vals.Encode()))
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	req = auth.WithUser(req, u)
	rr := httptest.NewRecorder()
	NewPageAPIHandler(rr, req)
	return rr
}

// TestNewPageFixedTag は、新規作成で作るときに決める語の値を選べること・人の選べない値では作れないことを確かめます。
func TestNewPageFixedTag(t *testing.T) {
	setupSaveTest(t)
	withFixedWord(t)
	classify := newTemplateTree(t)
	newPage(t, "000012", `<h1>請求の紙</h1><dl data-type="tags"><dt>`+fixedTestWord+`</dt><dd>請求</dd></dl>`, page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: classify})
	newPage(t, "000013", `<h1>台帳の紙</h1><dl data-type="tags"><dt>`+fixedTestWord+`</dt><dd>台帳</dd></dl>`, page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: classify})
	newPage(t, "000020", "<h1>置き場</h1>", page.PageMeta{Owner: "alice", Mode: page.DefaultMode, ParentID: TopPageID})
	// テンプレートの持ち主（読める人）で、管理者でない人として作る。
	carol := &auth.User{Username: "alice"}
	idOf := func(rr *httptest.ResponseRecorder) string {
		return strings.TrimSuffix(strings.TrimPrefix(rr.Header().Get("Location"), "/"), "?edit=true")
	}

	// 空のページで選べる値を選ぶ
	rr := newPageForm(t, carol, neturl.Values{"parent": {"000020"}, "fixed." + fixedTestWord: {"メモ"}})
	if rr.Code != 302 {
		t.Fatalf("選べる値で作れない: %d %s", rr.Code, rr.Body.String())
	}
	if got := tagValuesInBody(bodyOf(t, idOf(rr)), fixedTestWord); !sameTagValues(got, []string{"メモ"}) {
		t.Errorf("選んだ値が入っていない: %v", got)
	}
	// 何も選ばない → 種類なし
	rr = newPageForm(t, carol, neturl.Values{"parent": {"000020"}})
	if rr.Code != 302 || len(nonEmpty(tagValuesInBody(bodyOf(t, idOf(rr)), fixedTestWord))) != 0 {
		t.Errorf("選ばなかったのに値が入った: %d", rr.Code)
	}
	// 人の選べない値・テンプレートでは作れない（ページも残さない）
	var before int
	countPages := func() int { n := 0; database.DB.QueryRow("SELECT COUNT(*) FROM pages").Scan(&n); return n }
	before = countPages()
	if rr := newPageForm(t, carol, neturl.Values{"parent": {"000020"}, "fixed." + fixedTestWord: {"請求"}}); rr.Code != 403 {
		t.Errorf("⚠ 選べない値で作れた: %d", rr.Code)
	}
	if rr := newPageForm(t, carol, neturl.Values{"parent": {"000020"}, "template": {"000012"}}); rr.Code != 403 {
		t.Errorf("⚠ 選べない値のテンプレートから作れた: %d", rr.Code)
	}
	if n := countPages(); n != before {
		t.Errorf("断ったのにページの行が残った: %d → %d", before, n)
	}
	// 選べる値のテンプレートは、テンプレートの値のまま（欄は見ない）
	rr = newPageForm(t, carol, neturl.Values{"parent": {"000020"}, "template": {"000013"}, "fixed." + fixedTestWord: {"メモ"}})
	if rr.Code != 302 || !sameTagValues(tagValuesInBody(bodyOf(t, idOf(rr)), fixedTestWord), []string{"台帳"}) {
		t.Errorf("テンプレートの値になっていない: %d %s", rr.Code, rr.Body.String())
	}
	// 管理者は作れる
	if rr := newPageForm(t, &auth.User{Username: "root", IsAdmin: true}, neturl.Values{"parent": {"000020"}, "template": {"000012"}}); rr.Code != 302 {
		t.Errorf("管理者が作れない: %d %s", rr.Code, rr.Body.String())
	}
}

// TestTemplatesMenuHidesMachineKinds は、人の選べない値のテンプレートと、それしか無い分類をメニューに出さないことを確かめます。
// ⚠ ファイルの DB で（読めるかの確かめが :memory: では黙って全部落ちる）。
func TestTemplatesMenuHidesMachineKinds(t *testing.T) {
	setupTemplateAPITest(t)
	withFixedWord(t)
	classify := newTemplateTree(t)
	newPage(t, "000012", `<h1>請求の紙</h1><dl data-type="tags"><dt>`+fixedTestWord+`</dt><dd>請求</dd></dl>`, page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: classify})
	newPage(t, "000013", `<h1>台帳の紙</h1><dl data-type="tags"><dt>`+fixedTestWord+`</dt><dd>台帳</dd></dl>`, page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: classify})
	newPage(t, "000014", "<h1>機械だけ</h1>", page.PageMeta{Owner: "alice", Mode: page.DefaultMode, ParentID: "000010"})
	newPage(t, "000015", `<h1>請求の紙2</h1><dl data-type="tags"><dt>`+fixedTestWord+`</dt><dd>請求</dd></dl>`, page.PageMeta{
		Owner: "alice", Mode: page.DefaultMode, ParentID: "000014"})
	tree := getTemplates(t, &auth.User{Username: "alice"})
	var titles []string
	for _, g := range tree {
		for _, c := range g.Children {
			titles = append(titles, c.Title)
		}
	}
	if len(tree) != 1 || strings.Join(titles, ",") != "台帳の紙" {
		t.Errorf("メニュー: %+v（分類「業務」の下に台帳の紙だけのはず・中身の全部出せない分類「機械だけ」は出さない）", tree)
	}
}
