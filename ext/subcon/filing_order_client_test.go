package subcon

import (
	"bytes"
	"encoding/json"
	"net/http/httptest"
	"strconv"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/database"
)

// 受注の整理で人が確認した発注元が、**本文と索引に届く**ことを固定します（2026-09-20）。
//
// ⚠ **画面で直しただけでは意味がありません。** `page_tags` は本文から作られるので、
// 本文が元のままなら索引も元のまま——**直した顔をして引けない**、いちばん気づきにくい
// 壊れ方です（`syncDrawingFields` が 2026-09-11 に入ったのと同じ理由）。

// postOrdersWithClient は発注元つきで整理の実行へ送ります。
func postOrdersWithClient(t *testing.T, u *auth.User, pageID, client string) []filingResult {
	t.Helper()
	b, _ := json.Marshal(map[string]any{
		"orders": []map[string]string{{"page_id": pageID, "client": client}},
	})
	req := httptest.NewRequest("POST", "/api/file-drawings", bytes.NewReader(b))
	req.Header.Set("Content-Type", "application/json")
	req = auth.WithUser(req, u)
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

// orderClientTagOf はそのページの `発注元` タグを索引から読みます（無ければ空）。
func orderClientTagOf(t *testing.T, pageID string) string {
	t.Helper()
	idInt, err := strconv.Atoi(pageID)
	if err != nil {
		t.Fatalf("ページIDが読めません: %q", pageID)
	}
	tags, err := cms.TagsOfPage(database.DB, idInt)
	if err != nil {
		t.Fatalf("タグを読めません: %v", err)
	}
	return cms.FirstTag(tags, OrderClientTag)
}

// TestFileOrderWritesBackClient は、人が直した発注元が**本文と索引の両方**へ
// 届くことを固定します。
func TestFileOrderWritesBackClient(t *testing.T) {
	const inbox = "000031"
	setupFilingTest(t, inbox)
	user := &auth.User{Username: "alice"}

	pageID := makeOrderPage(t, inbox, "PO-C1", "株式会社南北スポーツ機械", "2024-09-13")

	res := postOrdersWithClient(t, user, pageID, "南北スポーツ機械")
	if len(res) != 1 || res[0].Outcome != "moved" {
		t.Fatalf("収まっていません: %+v", res)
	}

	// **本文が直っている**（正本）。
	body, err := cms.ReadPageBody(pageID)
	if err != nil {
		t.Fatalf("本文を読めません: %v", err)
	}
	if !strings.Contains(body, "<dt>"+OrderClientTag+"</dt><dd>南北スポーツ機械</dd>") {
		t.Errorf("本文の発注元が直っていません:\n%s", body)
	}
	if strings.Contains(body, "株式会社南北スポーツ機械") {
		t.Errorf("古い社名が本文に残っています:\n%s", body)
	}
	// **索引にも届いている**——ここが本題。横断検索はこちらを引きます。
	if got := orderClientTagOf(t, pageID); got != "南北スポーツ機械" {
		t.Errorf("索引の発注元が %q です（本文と食い違っています）", got)
	}
}

// TestFileOrderKeepsClientWhenBlank は、**欄が空なら本文に触らない**ことを固定します。
//
// ⚠ 空欄を「消したい」と読むと、読めなかっただけの行で**元の値を消します**。
// 空は「まだ分からない・触っていない」であって、消す意思ではありません。
func TestFileOrderKeepsClientWhenBlank(t *testing.T) {
	const inbox = "000032"
	setupFilingTest(t, inbox)
	user := &auth.User{Username: "alice"}

	pageID := makeOrderPage(t, inbox, "PO-C2", "そのままの会社", "2024-09-13")

	if res := postOrdersWithClient(t, user, pageID, ""); len(res) != 1 || res[0].Outcome != "moved" {
		t.Fatalf("収まっていません: %+v", res)
	}
	if got := orderClientTagOf(t, pageID); got != "そのままの会社" {
		t.Errorf("空欄で発注元が消えました: %q", got)
	}
}
