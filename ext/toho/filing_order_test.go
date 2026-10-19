package toho

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
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
	id, err := cms.CreateChildPage(inboxID, "alice", testOrderPage(inboxID, "pdf001", j))
	if err != nil {
		t.Fatalf("案件ページを作れません: %v", err)
	}
	return id
}

// postOrders は受注ページだけを整理の実行へ送ります（発注元は触らない形）。
//
// ⚠ **送る形は 2026-09-20 に変わりました**——IDの配列から `{page_id, client}` の
// 配列へ（発注元が直せるようになったため）。`client` を空にすると本文には触りません。
func postOrders(t *testing.T, u *auth.User, ids []string) []filingResult {
	t.Helper()
	rows := make([]map[string]string, 0, len(ids))
	for _, id := range ids {
		rows = append(rows, map[string]string{"page_id": id})
	}
	b, _ := json.Marshal(map[string]any{"orders": rows})
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

// TestFileOrderGoesToRecordByCreatedDate は、受注ページが **`記録／年／月／依頼`（受注ページを作った日の年月）**へ
// 収まることを固定します（2026-10-16 利用者:「その文書を作成した日のフォルダで良いと思います」）。
//
// それまでは `受注／年／月`（発注日の年月・2026-09-06 ユーザー決定）でした。発注日が昔でも、作った日の月に入ります。
func TestFileOrderGoesToRecordByCreatedDate(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	orderID := makeOrderPage(t, inbox, "250401-203", "南北スポーツ機械", "2024-09-13")

	results := postOrders(t, &auth.User{Username: "alice"}, []string{orderID})
	if len(results) != 1 || results[0].Outcome != "moved" {
		t.Fatalf("移動になっていません: %+v", results)
	}

	if got, want := placeOf(orderID), recordPath(RecordRequestFolder, orderFolderDateOf(pageNum(orderID))); got != want {
		t.Errorf("置き場が %s です（%s のはず）", got, want)
	}
	if strings.Contains(placeOf(orderID), "2024年") {
		t.Errorf("発注日（2024-09-13）の年月に入っています: %s", placeOf(orderID))
	}
	if _, ok := findChildByTitle(cms.TopPageID, OrderBoxTitle); ok {
		t.Errorf("「%s」の箱に年月を作っています（書類は記録へ）", OrderBoxTitle)
	}
}

// TestFileOrderRefusesNonOrderPage は、**受注ページ以外を動かさない**ことを固定します。
//
// 画面から送られたIDをそのまま信じると、図面ページや通信記録まで受注の箱へ入ります
// ——一覧から消えるので、間違いに気づくのは探しに行ったときです。
func TestFileOrderRefusesNonOrderPage(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	partID := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "南北スポーツ")

	results := postOrders(t, &auth.User{Username: "alice"}, []string{partID})
	if len(results) != 1 || results[0].Outcome != "skipped" {
		t.Fatalf("加工製品ページを受注として動かしています: %+v", results)
	}
	if _, ok := findChildByTitle(cms.TopPageID, OrderBoxTitle); ok {
		t.Errorf("動かさないのに「%s」ページを作っています", OrderBoxTitle)
	}
	meta, _ := page.ReadSidecar(partID)
	if meta.ParentID != inbox {
		t.Errorf("親が動いています: %+v", meta)
	}
}

// TestFileOrderWithoutNumber は、**発注書番号の無い注文**も受注ページとして整理に出て収まることを固定します
// （2026-09-30——月ごとの加工品の表のように番号を持たない客先がある・`isOrderPageTags`）。
// 加工製品ページ（`客先` だけ）は相変わらず受注ではない（上の TestFileOrderRefusesNonOrderPage）。
func TestFileOrderWithoutNumber(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	orderID := makeOrderPage(t, inbox, "", "やまと工作所", "2025-07-02")
	u := &auth.User{Username: "alice"}

	idInt, _ := strconv.Atoi(inbox)
	rows, err := orderChildrenOf(u, idInt)
	if err != nil || len(rows) != 1 || rows[0].PageID != orderID {
		t.Fatalf("番号の無い案件ページが整理に出ていません: %+v %v", rows, err)
	}
	if res := postOrders(t, u, []string{orderID}); len(res) != 1 || res[0].Outcome != "moved" {
		t.Fatalf("番号の無い案件ページを収めていません: %+v", res)
	}
	if k := kindOfPage(mustAtoi(t, orderID)); k != "案件" {
		t.Errorf("添付の印が「案件」になりません: %q", k)
	}
}
