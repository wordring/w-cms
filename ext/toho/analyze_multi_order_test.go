package toho

import (
	"encoding/json"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms/page"
)

// TestParseJudgmentWithSeveralOrders は、応答の `orders`（発注書ごとの配列）を読み、1枚ずつの判定に並べ直すことを
// 固定します（2026-09-30 利用者:「一枚のPDFに複数の発注書が入っています…返答も発注書ごとに配列のように」）。
// 原本の写しも紙ごとに解き、客先が空の紙は上の客先で埋める。
func TestParseJudgmentWithSeveralOrders(t *testing.T) {
	j, err := parseOrderJudgment(`{"doc_type":"order","is_client_order":true,"customer":"南北スポーツ","orders":[
	  {"order_no":"PO-1","order_date":"2026-08-01","subtotal":"3000","items":[{"item_no":"A-1","item_name":"ブラケット","price":"1500","quantity":"2"}],
	   "source_table":{"headers":["図番","品名"],"rows":[["A-1","ブラケット"]]}},
	  {"order_no":"PO-2","customer":"南北スポーツ機械","order_date":"2026-08-02","items":[{"item_no":"B-1","item_name":"カバー","price":"500","quantity":"4"}]}
	]}`)
	if err != nil {
		t.Fatal(err)
	}
	got := j.orderList()
	if len(got) != 2 || got[0].OrderNo != "PO-1" || got[1].OrderNo != "PO-2" {
		t.Fatalf("発注書ごとに並んでいません: %+v", got)
	}
	if !got[0].IsClientOrder || got[0].Customer != "南北スポーツ" || got[1].Customer != "南北スポーツ機械" {
		t.Errorf("紙ごとの客先・印が違います: %+v %+v", got[0], got[1])
	}
	if len(got[0].SourceTable.Rows) != 1 || len(got[0].Items) != 1 || got[1].Items[0].ItemNo != "B-1" {
		t.Errorf("紙ごとの明細・原本の写しが混ざっています: %+v %+v", got[0], got[1])
	}
	// 古い形（単数の項目）は1枚として読む。
	one, _ := parseOrderJudgment(`{"doc_type":"order","is_client_order":true,"order_no":"PO-9","items":[]}`)
	if l := one.orderList(); len(l) != 1 || l[0].OrderNo != "PO-9" {
		t.Errorf("古い形を1枚として読んでいません: %+v", l)
	}
}

// TestAnalyzePDFWithSeveralOrdersMakesPagePerSheet は、2枚の発注書が入ったPDFから受注ページが2枚でき、
// 明細が混ざらないことを固定します。
func TestAnalyzePDFWithSeveralOrdersMakesPagePerSheet(t *testing.T) {
	const id = "000013"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})
	putAttachment(t, id, "two.pdf", []byte("%PDF-1.4 two orders"))
	stubJudge(t, func(pdf []byte) (*orderJudgment, error) {
		return &orderJudgment{IsClientOrder: true, DocType: "order", Customer: "南北スポーツ", Orders: []orderJudgment{
			{OrderNo: "PO-1", OrderDate: "2026-08-01", Items: []orderPDFItem{{ItemNo: "A-1", ItemName: "ブラケット", Price: "1500", Quantity: "2"}}},
			{OrderNo: "PO-2", OrderDate: "2026-08-02", Items: []orderPDFItem{{ItemNo: "B-1", ItemName: "カバー", Price: "500", Quantity: "4"}}},
		}}, nil
	})
	rr := postAnalyze(t, &auth.User{Username: "alice"}, map[string]string{"page_id": id, "file": "two.pdf"})
	if rr.Code != 200 {
		t.Fatalf("解析が失敗しました: %d %s", rr.Code, rr.Body.String())
	}
	var res struct {
		PageID string `json:"page_id"`
		Pages  []struct {
			PageID string `json:"page_id"`
			Title  string `json:"title"`
		} `json:"pages"`
	}
	json.Unmarshal(rr.Body.Bytes(), &res)
	if len(res.Pages) != 2 || res.PageID != res.Pages[0].PageID {
		t.Fatalf("2枚できていません: %s", rr.Body.String())
	}
	for i, want := range []struct{ no, item, other string }{{"PO-1", "ブラケット", "カバー"}, {"PO-2", "カバー", "ブラケット"}} {
		body := readPageBody(t, res.Pages[i].PageID)
		if !strings.Contains(body, "<h1>受注 "+want.no+"</h1>") || !strings.Contains(body, "<td>"+want.item+"</td>") ||
			strings.Contains(body, "<td>"+want.other+"</td>") {
			t.Errorf("%s のページの明細が違う（混ざっている）:\n%s", want.no, body)
		}
		if meta, ok := page.ReadSidecar(res.Pages[i].PageID); !ok || meta.ParentID != id {
			t.Errorf("%s の親が解析元ではありません: %+v", want.no, meta)
		}
	}
}
