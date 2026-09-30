package toho

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/cms/editlock"
	"w-cms/internal/cms/page"
)

// 受注の行を、あとからできた加工製品ページへ結び直す（2026-09-30 夜・order_relink.go）。
//
// 利用者:「品番〈図番〉の加工製品ページはあるのに、受注残の表に弊社品番なしになるのは何故でしょう？」——ワンノートの取り込みは
// 整理を通らずに加工製品ページを作るので、先に整理した受注の行が空のまま残っていた。

// TestRelinkAllOrdersFillsLaterProduct は、**受注が先・加工製品ページが後**でも結び直せることを固定します。
//
//   - 空いている行だけ埋める（人が入れた値は触らない）・候補の無い品番は空のまま
//   - 何をどこへ結んだかを返す（結び先の題も——【旧】の付いたページかを人が見るため）
//   - 2回目は何も書かない
func TestRelinkAllOrdersFillsLaterProduct(t *testing.T) {
	setupExtTest(t, "000500", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	withProductCodeTags(t, "図面番号", "品番")
	// 受注が先——このときはまだ加工製品ページが無い。
	seedBody(t, "000500", orderBody([2]string{"", "K120-01-211"}, [2]string{"", "K120-99-999"}))
	addPage(t, 501, -1, "受注", "alice", "330", true)
	seedBody(t, "000501", orderBody([2]string{"000777", "K120-01-211"})) // 人が入れた値
	// あとから取り込みが加工製品ページを作る（整理を通らない）。
	seedProductPage(t, 502, "【旧】K120-01-211 留めブラケット", "K120-01-211")

	res := relinkAllOrders(adminUser())
	if res.Pages != 2 {
		t.Errorf("見た受注ページが %d 枚です（2を期待）", res.Pages)
	}
	if res.Rows != 1 || len(res.Links) != 1 {
		t.Fatalf("埋めた行 %d・結び %d です（1・1を期待）: %+v", res.Rows, len(res.Links), res.Links)
	}
	want := orderLink{Order: "000500", Code: "K120-01-211", Product: "000502", Title: "【旧】K120-01-211 留めブラケット", Rows: 1}
	if res.Links[0] != want {
		t.Errorf("結びが %+v です（%+v を期待）", res.Links[0], want)
	}
	body, err := cms.ReadPageBody("000500")
	if err != nil || !strings.Contains(body, "000502") {
		t.Errorf("受注ページに書き戻されていません（%v）:\n%s", err, body)
	}
	other, _ := cms.ReadPageBody("000501")
	if !strings.Contains(other, "000777") || strings.Contains(other, "000502") {
		t.Errorf("⚠ 人が入れた弊社品番を書き換えています:\n%s", other)
	}
	if again := relinkAllOrders(adminUser()); again.Rows != 0 || len(again.Links) != 0 {
		t.Errorf("2回目で %d 行書き換えています: %+v", again.Rows, again.Links)
	}
}

// TestRelinkAllOrdersSkipsOpenEditor は、⚠ **編集中の受注ページは飛ばし、飛ばしたと言う**ことを固定します
// （オートセーブと黙って上書きし合うため）。
func TestRelinkAllOrdersSkipsOpenEditor(t *testing.T) {
	setupExtTest(t, "000510", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	withProductCodeTags(t, "図面番号", "品番")
	seedBody(t, "000510", orderBody([2]string{"", "K120-01-211"}))
	seedProductPage(t, 511, "K120-01-211 留めブラケット", "K120-01-211")
	if r := editlock.Locks.TryAcquire(510, "alice", ""); !r.Acquired {
		t.Fatal("ロックを取れません")
	}
	t.Cleanup(func() { editlock.Locks.ForceRelease(510) })

	res := relinkAllOrders(adminUser())
	if res.Rows != 0 || len(res.Editing) != 1 || res.Editing[0] != "000510" {
		t.Errorf("編集中のページを飛ばしていません（行 %d・飛ばした %v）", res.Rows, res.Editing)
	}
	if body, _ := cms.ReadPageBody("000510"); strings.Contains(body, "000511") {
		t.Errorf("⚠ 編集中のページを書き換えています:\n%s", body)
	}
}

// TestOrderItemsRelinkAPIIsAdminPost は、口が **POST だけ・管理者だけ**であることを固定します
// （全部の受注ページの本文を書き換える口なので）。
func TestOrderItemsRelinkAPIIsAdminPost(t *testing.T) {
	setupExtTest(t, "000520", page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	call := func(method string, u *auth.User) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "/api/order-items/relink", nil)
		req = auth.WithUser(req, u)
		rec := httptest.NewRecorder()
		OrderItemsRelinkAPIHandler(rec, req)
		return rec
	}
	if rec := call(http.MethodGet, adminUser()); rec.Code != http.StatusMethodNotAllowed {
		t.Errorf("GET が %d です（405を期待）", rec.Code)
	}
	if rec := call(http.MethodPost, &auth.User{Username: "alice"}); rec.Code != http.StatusForbidden {
		t.Errorf("管理者でない利用者が %d です（403を期待）", rec.Code)
	}
	rec := call(http.MethodPost, adminUser())
	if rec.Code != http.StatusOK {
		t.Fatalf("管理者が %d です: %s", rec.Code, rec.Body.String())
	}
	var res relinkResult
	if err := json.Unmarshal(rec.Body.Bytes(), &res); err != nil {
		t.Fatalf("応答が読めません: %v: %s", err, rec.Body.String())
	}
	// 空でも [] で返す（null だと道具の側が「知らされていない」と取り違える）。
	if !strings.Contains(rec.Body.String(), `"editing":[]`) || !strings.Contains(rec.Body.String(), `"links":[]`) {
		t.Errorf("空の一覧が [] で返っていません: %s", rec.Body.String())
	}
}
