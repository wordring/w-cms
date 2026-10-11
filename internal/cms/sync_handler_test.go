package cms

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// /api/sync を通した試験（2026-10-12・【考察】編集モードの再考 §6「試験の約束」）。
// 2人（alice・bob）が同じページを、それぞれ自分の画面の元の並びから書く。

type syncResp struct {
	Success   bool           `json:"success"`
	Written   bool           `json:"written"`
	Blocks    []SyncBlockOut `json:"blocks"`
	Conflicts []SyncConflict `json:"conflicts"`
	Kept      []string       `json:"kept"`
	KeptBy    map[string]string
}

func postSync(t *testing.T, user string, req SyncRequest) (int, syncResp) {
	t.Helper()
	payload, _ := json.Marshal(req)
	r := httptest.NewRequest("POST", "/api/sync", strings.NewReader(string(payload)))
	r = auth.WithUser(r, &auth.User{Username: user, IsAdmin: true})
	rr := httptest.NewRecorder()
	SyncAPIHandler(rr, r)
	var got syncResp
	if rr.Code == 200 {
		if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil {
			t.Fatalf("応答が読めない: %v %s", err, rr.Body.String())
		}
	}
	return rr.Code, got
}

// screen は試験の「画面」——開いたときの並びと指紋を持ち、変えたいブロックを送る。
type screen struct {
	user  string
	page  string
	base  []SyncBase
	html  map[string]string // 画面が持っている書き出し（元の書き出し）
	order []string
}

func openScreen(t *testing.T, user, id string) *screen {
	t.Helper()
	code, resp := postSync(t, user, SyncRequest{PageID: id, Full: true})
	if code != 200 {
		t.Fatalf("開けない: %d", code)
	}
	s := &screen{user: user, page: id, html: map[string]string{}}
	s.adopt(resp)
	return s
}

// adopt は応答の並び・指紋・書き出しを画面の元として覚える（画面が描き直したことにする）。
func (s *screen) adopt(resp syncResp) {
	s.base = nil
	s.order = nil
	for _, b := range resp.Blocks {
		s.base = append(s.base, SyncBase{ID: b.ID, H: b.H})
		s.order = append(s.order, b.ID)
		if b.HTML != "" {
			s.html[b.ID] = b.HTML
		}
	}
}

// edit は1ブロックを書き換えて送る。
func (s *screen) edit(t *testing.T, id, newHTML string) (int, syncResp) {
	t.Helper()
	h := ""
	for _, b := range s.base {
		if b.ID == id {
			h = b.H
		}
	}
	return postSync(t, s.user, SyncRequest{
		PageID: s.page, Base: s.base, Order: s.order,
		Changes: []SyncChange{{ID: id, BaseH: h, BaseHTML: s.html[id], HTML: newHTML}},
	})
}

func setupSyncPage(t *testing.T, id, body string) {
	t.Helper()
	postSaveAs(t, id, body, "alice")
}

func bodyOfFile(t *testing.T, id string) string {
	t.Helper()
	b, err := os.ReadFile(page.BodyPath(id))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

const syncTestBody = `<h1 data-id="h1h1">題</h1><p data-id="aaaa">A</p><p data-id="bbbb">B</p>`

func TestSyncTwoUsersDifferentBlocks(t *testing.T) {
	setupSaveTest(t)
	const id = "000901"
	setupSyncPage(t, id, syncTestBody)
	alice := openScreen(t, "alice", id)
	bob := openScreen(t, "bob", id)

	if code, r := alice.edit(t, "aaaa", `<p data-id="aaaa">A-alice</p>`); code != 200 || !r.Written {
		t.Fatalf("alice が書けない: %d %+v", code, r)
	}
	code, r := bob.edit(t, "bbbb", `<p data-id="bbbb">B-bob</p>`)
	if code != 200 || len(r.Conflicts) != 0 {
		t.Fatalf("違うブロックなのにぶつかった: %d %+v", code, r.Conflicts)
	}
	body := bodyOfFile(t, id)
	if !strings.Contains(body, "A-alice") || !strings.Contains(body, "B-bob") {
		t.Fatalf("どちらかが消えた: %s", body)
	}
	// bob の応答には alice が変えたブロックの書き出しが入る（画面が描き直す）。
	var got *SyncBlockOut
	for i := range r.Blocks {
		if r.Blocks[i].ID == "aaaa" {
			got = &r.Blocks[i]
		}
	}
	if got == nil || got.Why != "other" || !strings.Contains(got.HTML, "A-alice") {
		t.Fatalf("相手の変更が応答に無い: %+v", got)
	}
	// bob が自分で書いたブロックは描き直さない（書き出しを返さない）。
	for _, b := range r.Blocks {
		if b.ID == "bbbb" && b.HTML != "" {
			t.Fatalf("自分のブロックまで描き直させた: %+v", b)
		}
	}
}

func TestSyncConflictSameBlock(t *testing.T) {
	setupSaveTest(t)
	const id = "000902"
	setupSyncPage(t, id, syncTestBody)
	alice := openScreen(t, "alice", id)
	bob := openScreen(t, "bob", id)

	alice.edit(t, "aaaa", `<p data-id="aaaa">alice の版</p>`)
	code, r := bob.edit(t, "aaaa", `<p data-id="aaaa">bob の版</p>`)
	if code != 200 || len(r.Conflicts) != 1 {
		t.Fatalf("同じブロックなのにぶつからない: %d %+v", code, r)
	}
	c := r.Conflicts[0]
	if c.ID != "aaaa" || c.Kind != "changed" || !strings.Contains(c.HTML, "alice の版") || c.By != "alice" || c.H == "" {
		t.Fatalf("相手の版・名前が返らない: %+v", c)
	}
	body := bodyOfFile(t, id)
	if !strings.Contains(body, "alice の版") || strings.Contains(body, "bob の版") {
		t.Fatalf("ぶつかったのに相手の版が上書きされた: %s", body)
	}
	// bob が「これで決める」——相手の版を元にして送り直すと当たる。
	bob.base = nil
	for _, b := range r.Blocks {
		bob.base = append(bob.base, SyncBase{ID: b.ID, H: b.H})
	}
	bob.html["aaaa"] = c.HTML
	if code, r2 := bob.edit(t, "aaaa", `<p data-id="aaaa">二人の版</p>`); code != 200 || len(r2.Conflicts) != 0 {
		t.Fatalf("決めたのに当たらない: %d %+v", code, r2)
	}
	if !strings.Contains(bodyOfFile(t, id), "二人の版") {
		t.Fatalf("決めた版が入らない")
	}
}

func TestSyncTableCellsMergeThroughHandler(t *testing.T) {
	setupSaveTest(t)
	const id = "000903"
	setupSyncPage(t, id, `<h1 data-id="h1h1">題</h1>`+tbl)
	alice := openScreen(t, "alice", id)
	bob := openScreen(t, "bob", id)
	alice.edit(t, "t1", strings.Replace(tbl, "<td>1</td>", "<td>10</td>", 1))
	code, r := bob.edit(t, "t1", strings.Replace(tbl, "<td>B</td>", "<td>B2</td>", 1))
	if code != 200 || len(r.Conflicts) != 0 {
		t.Fatalf("違うセルなのにぶつかった: %d %+v", code, r.Conflicts)
	}
	body := bodyOfFile(t, id)
	if !strings.Contains(body, "<td>10</td>") || !strings.Contains(body, "<td>B2</td>") {
		t.Fatalf("合わさっていない: %s", body)
	}
	for _, b := range r.Blocks {
		if b.ID == "t1" && (b.Why != "merged" || !strings.Contains(b.HTML, "<td>10</td>")) {
			t.Fatalf("合わせた表を画面へ返していない: %+v", b)
		}
	}
}

func TestSyncDeletedByOtherAndKept(t *testing.T) {
	setupSaveTest(t)
	const id = "000904"
	setupSyncPage(t, id, syncTestBody)
	alice := openScreen(t, "alice", id)
	bob := openScreen(t, "bob", id)

	// alice が bbbb を消す（bob はまだ知らない）→ bob が bbbb を書いていた → deleted でぶつかる。
	if code, _ := postSync(t, "alice", SyncRequest{PageID: id, Base: alice.base, Order: []string{"h1h1", "aaaa"}}); code != 200 {
		t.Fatalf("消せない")
	}
	if strings.Contains(bodyOfFile(t, id), `data-id="bbbb"`) {
		t.Fatalf("消えていない")
	}
	_, r := bob.edit(t, "bbbb", `<p data-id="bbbb">bob が書いていた</p>`)
	if len(r.Conflicts) != 1 || r.Conflicts[0].Kind != "deleted" || r.Conflicts[0].By != "" && r.Conflicts[0].By != "alice" {
		t.Fatalf("消されたブロックを書いていたのに知らせない: %+v", r.Conflicts)
	}
	if strings.Contains(bodyOfFile(t, id), "bob が書いていた") {
		t.Fatalf("聞く前に残してしまった")
	}
	// bob が「残す」——足したブロックとして送ると入る。
	bobOrder := []string{}
	var bobBase []SyncBase
	for _, b := range r.Blocks {
		bobOrder = append(bobOrder, b.ID)
		bobBase = append(bobBase, SyncBase{ID: b.ID, H: b.H})
	}
	bobOrder = append(bobOrder, "bbbb")
	postSync(t, "bob", SyncRequest{PageID: id, Base: bobBase, Order: bobOrder,
		Changes: []SyncChange{{ID: "bbbb", HTML: `<p data-id="bbbb">bob が書いていた</p>`}}})
	if !strings.Contains(bodyOfFile(t, id), "bob が書いていた") {
		t.Fatalf("残すを選んだのに入らない")
	}

	// 逆: alice が古い元で aaaa を消そうとする——そのあいだに bob が aaaa を変えていたら残す（kept）。
	alice = openScreen(t, "alice", id)
	bob = openScreen(t, "bob", id)
	bob.edit(t, "aaaa", `<p data-id="aaaa">bob が直した</p>`)
	var order []string
	for _, o := range alice.order {
		if o != "aaaa" {
			order = append(order, o)
		}
	}
	code, r2 := postSync(t, "alice", SyncRequest{PageID: id, Base: alice.base, Order: order})
	if code != 200 || len(r2.Kept) != 1 || r2.Kept[0] != "aaaa" {
		t.Fatalf("相手が変えたブロックを黙って消した: %d %+v", code, r2)
	}
	if !strings.Contains(bodyOfFile(t, id), "bob が直した") {
		t.Fatalf("残していない")
	}
	for _, b := range r2.Blocks {
		if b.ID == "aaaa" && (b.Why != "kept" || !strings.Contains(b.HTML, "bob が直した")) {
			t.Fatalf("残したブロックを画面へ返していない: %+v", b)
		}
	}
}

func TestSyncFullAssignsMissingIDs(t *testing.T) {
	setupSaveTest(t)
	const id = "000905"
	setupSyncPage(t, id, `<h1>題</h1><p>ID の無い段落</p>`)
	code, r := postSync(t, "alice", SyncRequest{PageID: id, Full: true})
	if code != 200 || len(r.Blocks) != 2 {
		t.Fatalf("開けない: %d %+v", code, r)
	}
	// 振った ID は書き出しに入って画面へ渡る——が、**開いただけでは書かない**（見ただけのページを書き換えない）。
	if r.Written {
		t.Fatalf("開いただけで書いた")
	}
	if strings.Contains(bodyOfFile(t, id), "data-id") {
		t.Fatalf("開いただけで正本に ID を書いた: %s", bodyOfFile(t, id))
	}
	for _, b := range r.Blocks {
		if b.ID == "" || !strings.Contains(b.HTML, `data-id="`+b.ID+`"`) || b.Why != "full" {
			t.Fatalf("ID を振って画面へ渡していない: %+v", b)
		}
	}
	// もう一度開いても同じ ID（中身から決まる）——画面とサーバーで食い違わない。
	_, r2 := postSync(t, "alice", SyncRequest{PageID: id, Full: true})
	if r2.Written || r2.Blocks[1].ID != r.Blocks[1].ID || r2.Blocks[1].H != r.Blocks[1].H {
		t.Fatalf("開くたびに振り直した: %+v / %+v", r2.Blocks, r.Blocks)
	}
	// その ID のブロックを書き換えると当たり、そのとき ID ごと正本へ書く。
	s := &screen{user: "alice", page: id, html: map[string]string{}}
	s.adopt(r)
	pid := r.Blocks[1].ID
	code, r3 := s.edit(t, pid, `<p data-id="`+pid+`">書き換えた</p>`)
	if code != 200 || !r3.Written || len(r3.Conflicts) != 0 {
		t.Fatalf("振った ID のブロックを書けない: %d %+v", code, r3)
	}
	body := bodyOfFile(t, id)
	if !strings.Contains(body, `<p data-id="`+pid+`">書き換えた</p>`) || !strings.Contains(body, `data-id="`+r.Blocks[0].ID+`"`) {
		t.Fatalf("書いたときに ID が正本へ入らない: %s", body)
	}
	if strings.Count(body, "<p") != 1 {
		t.Fatalf("同じブロックが2つになった: %s", body)
	}
}

// 同じ中身のブロックが並んでも別の ID（k 番目）——ID の無い本文で、同じ段落が2つあっても取り違えない。
func TestStableBlockIDsForSameContent(t *testing.T) {
	blocks, _ := splitStoredBlocks(`<p>同じ</p><p>同じ</p>`)
	ensureStoredBlockIDs(blocks)
	again, _ := splitStoredBlocks(`<p>同じ</p><p>同じ</p>`)
	ensureStoredBlockIDs(again)
	if blocks[0].ID == blocks[1].ID || blocks[0].ID != again[0].ID || blocks[1].ID != again[1].ID {
		t.Fatalf("同じ中身の ID が重なる・計算し直すと変わる: %s %s / %s %s", blocks[0].ID, blocks[1].ID, again[0].ID, again[1].ID)
	}
}

func TestSyncNoChangeDoesNotWrite(t *testing.T) {
	setupSaveTest(t)
	const id = "000906"
	setupSyncPage(t, id, syncTestBody)
	alice := openScreen(t, "alice", id)
	before, _ := page.ReadSidecar(id)
	code, r := postSync(t, "alice", SyncRequest{PageID: id, Base: alice.base, Order: alice.order})
	if code != 200 || r.Written {
		t.Fatalf("変えていないのに書いた: %d %+v", code, r)
	}
	after, _ := page.ReadSidecar(id)
	if before.UpdatedAt != after.UpdatedAt {
		t.Fatalf("更新日時が進んだ")
	}
	for _, b := range r.Blocks {
		if b.HTML != "" {
			t.Fatalf("変わっていないのに描き直させた: %+v", b)
		}
	}
}

// 書いたタブが知らせに乗る（書ける形のタブは自分の保存では読み直さない）。
func TestSyncNotifiesWithTab(t *testing.T) {
	setupSaveTest(t)
	const id = "000907"
	setupSyncPage(t, id, syncTestBody)
	alice := openScreen(t, "alice", id)
	ch := watchers.subscribe(id, pageWatcher{user: "bob", tab: "tab-bob"})
	defer watchers.unsubscribe(id, ch)
	postSync(t, "alice", SyncRequest{PageID: id, Base: alice.base, Order: alice.order, Tab: "tab-alice",
		Changes: []SyncChange{{ID: "aaaa", BaseH: alice.base[1].H, BaseHTML: alice.html["aaaa"], HTML: `<p data-id="aaaa">x</p>`}}})
	select {
	case ev := <-ch:
		if ev.Type != "updated" || ev.Tab != "tab-alice" || ev.By != "alice" {
			t.Fatalf("知らせが違う: %+v", ev)
		}
	default:
		t.Fatalf("知らせが来ない")
	}
}

func TestPageWatchersNames(t *testing.T) {
	setupSaveTest(t)
	const id = "000908"
	setupSyncPage(t, id, syncTestBody)
	a := watchers.subscribe(id, pageWatcher{user: "alice", tab: "t-a"})
	defer watchers.unsubscribe(id, a)
	b1 := watchers.subscribe(id, pageWatcher{user: "bob", tab: "t-b1"})
	defer watchers.unsubscribe(id, b1)
	b2 := watchers.subscribe(id, pageWatcher{user: "bob", tab: "t-b2"})
	defer watchers.unsubscribe(id, b2)

	r := httptest.NewRequest("GET", "/api/page-watchers?id="+id+"&tab=t-a", nil)
	r = auth.WithUser(r, &auth.User{Username: "alice", IsAdmin: true})
	rr := httptest.NewRecorder()
	PageWatchersAPIHandler(rr, r)
	var got struct{ Users []string }
	json.Unmarshal(rr.Body.Bytes(), &got)
	if strings.Join(got.Users, ",") != "bob" {
		t.Fatalf("開いている人 got=%v（自分のタブを除き bob を1回）", got.Users)
	}
}

// ── 同時に書いても、どの書き込みも消えない（ページごとの書き込みの錠）──────────────
//
// 人の保存（/api/sync で別々のブロックを足す）と業務のボタン（RewriteBody で末尾へ足す）を同じページへ
// 同時に流す。錠が無いと、読んでから書くまでのあいだに入った書き込みが消える。
// ⚠ ファイルDB（:memory: は接続ごとに別の DB——並列に索引を書くと別の DB へ行く）。
func TestConcurrentWritesLoseNothing(t *testing.T) {
	newTestFileDB(t)
	const id = "000909"
	setupSyncPage(t, id, `<h1 data-id="h1h1">題</h1>`)
	base := openScreen(t, "alice", id)

	const n = 15
	var wg sync.WaitGroup
	errs := make(chan string, 4*n)
	for i := 0; i < n; i++ {
		wg.Add(2)
		go func(i int) {
			defer wg.Done()
			bid := fmt.Sprintf("s%03d", i)
			payload, _ := json.Marshal(SyncRequest{PageID: id, Base: base.base, Order: append(append([]string{}, base.order...), bid),
				Changes: []SyncChange{{ID: bid, HTML: fmt.Sprintf(`<p data-id="%s">人-%d</p>`, bid, i)}}})
			r := httptest.NewRequest("POST", "/api/sync", strings.NewReader(string(payload)))
			r = auth.WithUser(r, &auth.User{Username: "alice", IsAdmin: true})
			rr := httptest.NewRecorder()
			SyncAPIHandler(rr, r)
			if rr.Code != 200 {
				errs <- fmt.Sprintf("sync %d: %d %s", i, rr.Code, rr.Body.String())
			}
		}(i)
		go func(i int) {
			defer wg.Done()
			err := RewriteBody(id, "machine", func(cur string) string {
				return cur + fmt.Sprintf(`<p>機械-%d</p>`, i)
			})
			if err != nil {
				errs <- fmt.Sprintf("rewrite %d: %v", i, err)
			}
		}(i)
	}
	wg.Wait()
	close(errs)
	for e := range errs {
		t.Error(e)
	}
	body := bodyOfFile(t, id)
	for i := 0; i < n; i++ {
		if !strings.Contains(body, fmt.Sprintf("人-%d<", i)) {
			t.Errorf("人の書き込み %d が消えた", i)
		}
		if !strings.Contains(body, fmt.Sprintf("機械-%d<", i)) {
			t.Errorf("機械の書き込み %d が消えた", i)
		}
	}
}
