package toho

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/ext/comm"
	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// メールの記録から作るページ（2026-10-01・ext/comm/record_make.go・mail_product.go）。利用者:「「受注ページ」「加工製品ページ」を
// コンボボックスで選択して「作成」ボタンを押せばいいかも」。

// getRecordMakers は GET /api/record-makers を呼びます。
func getRecordMakers(t *testing.T, u *auth.User, pageID string) (kinds []string, made []comm.MadePage) {
	t.Helper()
	req := auth.WithUser(httptest.NewRequest("GET", "/api/record-makers?page_id="+pageID, nil), u)
	rr := httptest.NewRecorder()
	comm.RecordMakersAPIHandler(rr, req)
	var got struct {
		Kinds []struct{ Name string } `json:"kinds"`
		Made  []comm.MadePage         `json:"made"`
	}
	if err := json.Unmarshal(rr.Body.Bytes(), &got); err != nil || rr.Code != 200 {
		t.Fatalf("種類を引けません: %d %s", rr.Code, rr.Body.String())
	}
	for _, k := range got.Kinds {
		kinds = append(kinds, k.Name)
	}
	return kinds, got.Made
}

// TestRecordMakersListKindsByDirection は、選ぶ欄の種類が受信のメールにだけ・並びの順に出て、送信の控え・下書きには
// 出ないことを固定します。
func TestRecordMakersListKindsByDirection(t *testing.T) {
	const id = "000017"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	u := &auth.User{Username: "alice"}
	seedBody(t, id, mailRecordBody("見積のお願い", "図面を送ります。"))
	if kinds, _ := getRecordMakers(t, u, id); strings.Join(kinds, "・") != MailOrderKind+"・"+MailProductKind {
		t.Errorf("受信のメールの種類が違います: %v", kinds)
	}
	seedBody(t, id, strings.Replace(mailRecordBody("送った控え", "本文"), "<dd>受信</dd>", "<dd>送信</dd>", 1))
	if kinds, _ := getRecordMakers(t, u, id); len(kinds) != 0 {
		t.Errorf("送信の控えに種類が出ています: %v", kinds)
	}
	seedBody(t, id, strings.Replace(mailRecordBody("下書き", "本文"), "</dl>", "<dt>"+comm.DraftTag+"</dt><dd>はい</dd></dl>", 1))
	if kinds, _ := getRecordMakers(t, u, id); len(kinds) != 0 {
		t.Errorf("下書きに種類が出ています: %v", kinds)
	}
}

// TestMakeProductFromMail は「加工製品ページ」で、テンプレートから空の加工製品ページがメールの記録の子にでき（受信元は
// メールのページ全体・改訂明細の1版目・図面の枠は空）、開くページとして返り、印に出ることを固定します。
func TestMakeProductFromMail(t *testing.T) {
	const id = "000018"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	u := &auth.User{Username: "alice"}
	seedBody(t, id, mailRecordBody("見積のお願い", "図面を送ります。"))

	b, _ := json.Marshal(map[string]string{"page_id": id, "kind": MailProductKind})
	req := auth.WithUser(httptest.NewRequest("POST", "/api/record-make", bytes.NewReader(b)), u)
	req.Header.Set("Content-Type", "application/json")
	rr := httptest.NewRecorder()
	comm.RecordMakeAPIHandler(rr, req)
	var res struct {
		Pages []comm.MadePage `json:"pages"`
		Open  string          `json:"open"`
		Say   string          `json:"say"`
	}
	json.Unmarshal(rr.Body.Bytes(), &res)
	if rr.Code != 200 || len(res.Pages) != 1 || res.Open != res.Pages[0].PageID {
		t.Fatalf("加工製品ページができていません: %d %s", rr.Code, rr.Body.String())
	}
	newID := res.Open
	body := readPageBody(t, newID)
	for _, want := range []string{
		"<h1>" + blankProductTitle + "</h1>",
		"<dt>" + SourceRefTag + "</dt><dd>" + id + "</dd>",
		`<section data-ref="" data-type="file-view"></section>`,
		"<caption>改訂明細</caption>",
	} {
		if !strings.Contains(body, want) {
			t.Errorf("加工製品ページに %q がありません:\n%s", want, body)
		}
	}
	if meta, _ := page.ReadSidecar(newID); meta.ParentID != id {
		t.Errorf("メールの記録の子になっていません: 親=%s", meta.ParentID)
	}
	if !strings.Contains(res.Say, "客先は空") {
		t.Errorf("差出人が連絡帳に無いことを言っていません: %q", res.Say)
	}
	// 印——加工製品として出て、受注には数えない。
	if _, made := getRecordMakers(t, u, id); len(made) != 1 || made[0].PageID != newID || made[0].Kind != "加工製品" {
		t.Errorf("作った加工製品ページの印が違います: %+v", made)
	}
	if marks := mailOrderPages(u, id); len(marks) != 0 {
		t.Errorf("加工製品ページを受注として数えています: %+v", marks)
	}
}
