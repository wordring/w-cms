package sheetmetal

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
)

// makeOrderPage は解析が作るのと同じ形の受注ページを inbox の子として作ります。
func makeOrderPage(t *testing.T, inboxID, no, client, orderedAt string) string {
	t.Helper()
	j := &orderJudgment{
		IsClientOrder: true, DocType: "order",
		OrderNo: no, Customer: client, OrderDate: orderedAt,
	}
	id, err := cms.CreateChildPage(inboxID, "alice", buildOrderPageHTML(inboxID, "pdf001", "", j))
	if err != nil {
		t.Fatalf("受注ページを作れません: %v", err)
	}
	return id
}

// postOrders は受注ページだけを整理の実行へ送ります。
func postOrders(t *testing.T, u *auth.User, ids []string) []filingResult {
	t.Helper()
	b, _ := json.Marshal(map[string]any{"orders": ids})
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

// TestFileOrderGoesToOrderBoxByOrderDate は、受注ページが **発注日の年月**へ
// 収まることを固定します（2026-09-06 ユーザー決定）。
//
// 受け取った日で並べると、月末に届いた前月ぶんが翌月に混ざります——ここが効かないと
// 「先月の受注」が月をまたいで散ります。
func TestFileOrderGoesToOrderBoxByOrderDate(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	orderID := makeOrderPage(t, inbox, "260602-102", "トーアスポーツマシーン", "2024-09-13")

	results := postOrders(t, &auth.User{Username: "alice"}, []string{orderID})
	if len(results) != 1 || results[0].Outcome != "moved" {
		t.Fatalf("移動になっていません: %+v", results)
	}

	boxID, ok := findChildByTitle(cms.TopPageID, OrderBoxTitle)
	if !ok {
		t.Fatalf("「%s」ページが作られていません", OrderBoxTitle)
	}
	yearID, ok := findChildByTitle(boxID, "2024年")
	if !ok {
		t.Fatalf("年フォルダが発注日から作られていません")
	}
	monthID, ok := findChildByTitle(yearID, "09月")
	if !ok {
		t.Fatalf("月フォルダが発注日から作られていません")
	}
	meta, _ := page.ReadSidecar(orderID)
	if meta.ParentID != monthID {
		t.Errorf("親が付け替わっていません: %+v", meta)
	}
}

// TestFileOrderRefusesNonOrderPage は、**受注ページ以外を動かさない**ことを固定します。
//
// 画面から送られたIDをそのまま信じると、図面ページや通信記録まで受注の箱へ入ります
// ——一覧から消えるので、間違いに気づくのは探しに行ったときです。
func TestFileOrderRefusesNonOrderPage(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	partID := makeDrawingPage(t, inbox, "Y050-1", "脚取付台", "オールラウンド2輪", "トーアスポーツ")

	results := postOrders(t, &auth.User{Username: "alice"}, []string{partID})
	if len(results) != 1 || results[0].Outcome != "skipped" {
		t.Fatalf("部品ページを受注として動かしています: %+v", results)
	}
	if _, ok := findChildByTitle(cms.TopPageID, OrderBoxTitle); ok {
		t.Errorf("動かさないのに「%s」ページを作っています", OrderBoxTitle)
	}
	meta, _ := page.ReadSidecar(partID)
	if meta.ParentID != inbox {
		t.Errorf("親が動いています: %+v", meta)
	}
}
