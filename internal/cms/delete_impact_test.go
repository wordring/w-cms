package cms

import (
	"encoding/json"
	"net/http/httptest"
	"os"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// TestDeleteImpactFindsReferrersAndTables は、消す前の確かめ（delete_impact.go）が、どのページにも同じに、
// ①参照のタグ ②本文のリンク・画像など・ファイル表示 ③自分の表 を見つけることを固定します（2026-10-23）。
// 利用者:「すべてのページに効く仕組みとして、消す前に①参照のタグと表のセル②本文のリンクとファイル表示（保存のときに索引へ入れる）
// ③そのページの表が集計に使われているか、を調べて警告する形をコアに作ってください。」
func TestDeleteImpactFindsReferrersAndTables(t *testing.T) {
	newTestFileDB(t)
	open := page.PageMeta{Owner: "alice", Mode: page.DefaultMode}
	newPage(t, "000030", `<h1>指される</h1><p><img src="/000030/abcd.png"></p>`, open) // 自分の画像は数えない
	newPage(t, "000031", `<h1>タグで指す</h1><dl data-type="tags"><dt>受信元</dt><dd>000030</dd></dl>`, open)
	newPage(t, "000032", `<h1>リンクで指す</h1><p><a href="/000030#ab12">前の版</a></p>`, open)
	newPage(t, "000033", `<h1>画像で指す</h1><p><img src="/000030/abcd.png"></p>`, open)
	newPage(t, "000034", `<h1>ファイル表示で指す</h1><section data-type="file-view" data-ref="000030-abcd"></section>`, open)
	newPage(t, "000035", `<h1>見られない</h1><dl data-type="tags"><dt>受信元</dt><dd>000030</dd></dl>`,
		page.PageMeta{Owner: "bob", Mode: "300"})
	newPage(t, "000036", `<h1>関係ない</h1><p>000030 と本文に書いただけ（参照ではない）</p>`, open)

	alice := &auth.User{Username: "alice"}
	d, err := DeleteImpactOf(database.DB, alice, "000030")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, r := range d.Refs {
		got[r.PageID] = strings.Join(r.How, "・")
	}
	want := map[string]string{"000031": "タグ「受信元」", "000032": LinkKindAnchor, "000033": LinkKindEmbed, "000034": LinkKindFileView}
	for id, how := range want {
		if got[id] != how {
			t.Errorf("⚠ /%s の指し方が %q です（%q のはず）・全部: %v", id, got[id], how, got)
		}
	}
	if len(d.Refs) != len(want) {
		t.Errorf("⚠ 指しているページの数が違います（自分・本文に番号を書いただけのページを数えていないか）: %v", got)
	}
	if d.Hidden != 1 {
		t.Errorf("⚠ 見られないページを数えていません（中身は出さずに数だけ）: %d", d.Hidden)
	}
	if len(d.Tables) != 0 {
		t.Errorf("⚠ 表の無いページに表があると言っています: %v", d.Tables)
	}

	// ③ 自分の表（索引に入っている表）。
	newPage(t, "000040", `<h1>表がある</h1><table data-type="part-materials"><tr><th>部材名</th><th>単価</th></tr>`+
		`<tr><td>ねじ</td><td>10</td></tr><tr><td>座金</td><td>5</td></tr><tr><td></td><td></td></tr></table>`, open)
	d, _ = DeleteImpactOf(database.DB, alice, "000040")
	if len(d.Tables) != 1 || d.Tables[0].Rows != 2 || d.Tables[0].Name != "部材定義" {
		t.Errorf("⚠ 自分の表（字のある行 2）を見つけていません: %+v", d.Tables)
	}

	// 本文から指す所を消すと、索引からも消える（保存のたびの洗い替え）。
	newPage(t, "000032", `<h1>リンクを外した</h1>`, open)
	if d, _ = DeleteImpactOf(database.DB, alice, "000030"); len(d.Refs) != 3 {
		t.Errorf("⚠ 外したリンクが索引に残っています: %+v", d.Refs)
	}
}

// TestDeletePageRefusesWhenItBreaksSomething は、消すと何かが起きるページは、ふつうの削除では断り（409）、
// それでも消す（force=1）のは管理者だけ（ほかは 403）であることを固定します。何も起きないページは今までどおり消せます。
func TestDeletePageRefusesWhenItBreaksSomething(t *testing.T) {
	newTestFileDB(t)
	open := page.PageMeta{Owner: "alice", Mode: page.DefaultMode}
	newPage(t, "000050", `<h1>指される</h1>`, open)
	newPage(t, "000051", `<h1>指す</h1><p><a href="/000050">リンク</a></p>`, open)
	newPage(t, "000052", `<h1>何も無い</h1>`, open)

	del := func(id, query string, u *auth.User) *httptest.ResponseRecorder {
		req := httptest.NewRequest("POST", "/api/delete-page?id="+id+query, nil)
		req = auth.WithUser(req, u)
		req.Header.Set("X-Lock-Token", lockAs(t, mustAtoi(id), u.Username))
		rr := httptest.NewRecorder()
		DeletePageAPIHandler(rr, req)
		return rr
	}
	alice := &auth.User{Username: "alice"}
	if rr := del("000050", "", alice); rr.Code != 409 || !strings.Contains(rr.Body.String(), "/000051") {
		t.Fatalf("⚠ 指されているページを、確かめずに消せます（または指している所を言いません）: %d %s", rr.Code, rr.Body.String())
	}
	if rr := del("000050", "&force=1", alice); rr.Code != 403 {
		t.Fatalf("⚠ 管理者でない人が、それでも消せます: %d %s", rr.Code, rr.Body.String())
	}
	if _, err := os.Stat(page.GetPageDir("000050")); err != nil {
		t.Fatalf("⚠ 断ったのにページが消えています: %v", err)
	}
	if rr := del("000050", "&force=1", &auth.User{Username: "root", IsAdmin: true}); rr.Code != 200 {
		t.Fatalf("⚠ 管理者がそれでも消せません: %d %s", rr.Code, rr.Body.String())
	}
	if rr := del("000052", "", alice); rr.Code != 200 {
		t.Fatalf("⚠ 何も起きないページを消せません: %d %s", rr.Code, rr.Body.String())
	}

	// 確かめの API。
	newPage(t, "000053", `<h1>指される2</h1>`, open)
	newPage(t, "000054", `<h1>指す2</h1><dl data-type="tags"><dt>受信元</dt><dd>000053</dd></dl>`, open)
	req := auth.WithUser(httptest.NewRequest("GET", "/api/delete-check?id=000053", nil), alice)
	rr := httptest.NewRecorder()
	DeleteCheckAPIHandler(rr, req)
	var out struct {
		Empty    bool     `json:"empty"`
		Lines    []string `json:"lines"`
		CanForce bool     `json:"can_force"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &out); err != nil || rr.Code != 200 {
		t.Fatalf("確かめの API が読めません: %d %s", rr.Code, rr.Body.String())
	}
	if out.Empty || out.CanForce || !strings.Contains(strings.Join(out.Lines, "\n"), "/000054") {
		t.Errorf("⚠ 確かめの答えが違います: %+v", out)
	}
	// 読めるだけの人には見せない（見えないページから指されている数・子の数が分かるので・2026-10-26）。
	newPage(t, "000055", `<h1>読むだけ</h1>`, page.PageMeta{Owner: "bob", Mode: "302", ParentID: TopPageID})
	req = auth.WithUser(httptest.NewRequest("GET", "/api/delete-check?id=000055", nil), alice)
	rr = httptest.NewRecorder()
	DeleteCheckAPIHandler(rr, req)
	if rr.Code != 403 {
		t.Errorf("⚠ 消せない人に消す前の確かめを返しています: %d %s", rr.Code, rr.Body.String())
	}
}
