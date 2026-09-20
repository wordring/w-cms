package subcon

import (
	"strings"
	"testing"

	"w-cms/internal/cms"
	"w-cms/internal/cms/page"
	"w-cms/internal/database"
)

// **顧客の発注書の写しを、読んだまま載せます**（2026-09-20 ユーザー:「発注書の見出しを
// **あるがままに表にしたものを原本**として、弊社仕様に修正した表を作ると良いと思います」
// 「顧客の表は、整理したあとも残します。**ボタンで畳めれば良い**と思います」）。
//
// ⚠ **これが無いと、言い換えたことが見えません。** 実データでは先方の `図面番号` を
// 弊社の `品番` に入れてきました——結果としては正しいのですが、**黙って言い換えて**
// いたので、原本と見比べない限り気づけませんでした。
//
// ⚠ **OCRの正しさもここでしか確かめられません**——行を1つ落としていないか、
// 数字を取り違えていないか。対応表だけ見せる形では分かりません。

// realOrderJudgment は実物の発注書と同じ形の判定を返します
// （見出しは `No. / 品名 / サイズ / 図面番号 / 数量 / 単位 / 単価 / 金額`）。
func realOrderJudgment() *orderJudgment {
	return &orderJudgment{
		IsClientOrder: true, DocType: "order",
		OrderNo: "250715-304", Customer: "南北スポーツ機械",
		OrderDate: "2026-09-16", DueDate: "2026-10-15",
		SourceTable: orderSourceTable{
			Headers: []string{"No.", "品名", "サイズ", "図面番号", "数量", "単位", "単価", "金額"},
			Rows: [][]string{
				{"1", "ブラケット", "t3.2×100", "K120-1", "100", "個", "390", "39000"},
				{"2", "カバー", "t1.6×200", "K120-2", "2", "セット", "1500", "3000"},
			},
		},
		Items: []orderPDFItem{
			{ItemNo: "K120-1", ItemName: "ブラケット", Quantity: "100", Unit: "個", Price: "390"},
			{ItemNo: "K120-2", ItemName: "カバー", Quantity: "2", Unit: "セット", Price: "1500"},
		},
	}
}

// TestOrderPageKeepsSourceTable は、**先方の見出しがそのまま残る**ことを固定します。
func TestOrderPageKeepsSourceTable(t *testing.T) {
	body := buildOrderPageHTML("000001", "pdf001", realOrderJudgment())

	// **先方の言葉**——言い換えていない。
	for _, h := range []string{"No.", "サイズ", "図面番号", "金額"} {
		if !strings.Contains(body, "<th>"+h+"</th>") {
			t.Errorf("先方の見出し %q が残っていません:\n%s", h, body)
		}
	}
	// **畳めること**（素のHTMLだけ——CSP strict でも公開ページでも動く）。
	if !strings.Contains(body, "<details>") || !strings.Contains(body, "<summary>") {
		t.Errorf("畳める形になっていません:\n%s", body)
	}
	// ⚠ **弊社の表も並んでいる**（原本だけになっていない）。
	if !strings.Contains(body, `data-type="client-order-items"`) {
		t.Errorf("弊社の明細が消えています:\n%s", body)
	}
}

// TestOrderSourceTableIsNotIndexed は、⚠ **原本が索引に載らない**ことを固定します。
//
// **これが今回いちばん大事な性質**です。載ると弊社の明細と**二重計上**になり、
// 集計が静かに倍になります。形式を登録していないので載らない——2026-09-20 に入れた
// 「登録された語彙だけDBに入る」の線引きが、そのままここで効きます。
func TestOrderSourceTableIsNotIndexed(t *testing.T) {
	const id = "000085"
	setupExtTest(t, id, page.PageMeta{Owner: "alice", Group: "sales", Mode: "330"})

	body := buildOrderPageHTML(id, "pdf001", realOrderJudgment())
	if err := cms.SyncIndex(id, body); err != nil {
		t.Fatalf("SyncIndex: %v", err)
	}

	rows, err := database.DB.Query(
		`SELECT DISTINCT data_type FROM vocab_index WHERE page_id = ?`, 85)
	if err != nil {
		t.Fatalf("索引のクエリ: %v", err)
	}
	defer rows.Close()
	for rows.Next() {
		var dt string
		rows.Scan(&dt)
		if dt != "client-order-items" {
			t.Errorf("⚠ 原本の表まで索引に載っています（弊社の明細と二重計上になります）: %q", dt)
		}
	}

	// **数量が二重になっていないこと**——弊社の明細ぶんだけ。
	var n int
	database.DB.QueryRow(
		`SELECT COUNT(*) FROM vocab_index WHERE page_id = ? AND field = ?`, 85, "数量").Scan(&n)
	if n != 2 {
		t.Errorf("数量の行が %d 件です（明細2行ぶんの2を期待——原本が混ざっていませんか）", n)
	}
}

// TestOrderSourceTablePadsShortRows は、**列数の足りない行を空で埋める**ことを
// 固定します。
//
// ⚠ 揃っていないと、見出しと値の対応が1つずつずれて**別の列の値に見えます**。
// OCR は列を取りこぼすことがあるので、ここは必ず起きます。
func TestOrderSourceTablePadsShortRows(t *testing.T) {
	j := realOrderJudgment()
	j.SourceTable.Rows = [][]string{{"1", "ブラケット"}} // 8列のうち2つしか読めなかった

	body := buildOrderPageHTML("000001", "pdf001", j)
	// 原本の行は8セルになる（見出しと同じ数）。
	at := strings.Index(body, "<caption>"+sourceTableCaption+"</caption>")
	if at < 0 {
		t.Fatalf("原本の表がありません:\n%s", body)
	}
	seg := body[at:]
	end := strings.Index(seg, "</details>")
	seg = seg[:end]
	rows := strings.Split(seg, "<tr>")
	if len(rows) < 3 {
		t.Fatalf("行がありません:\n%s", seg)
	}
	if n := strings.Count(rows[2], "<td>"); n != 8 {
		t.Errorf("行の列数が見出しと揃っていません: %d（8を期待）\n%s", n, rows[2])
	}
}

// TestOrderSourceTableOmittedWhenEmpty は、**読めなければ出さない**ことを固定します。
//
// ⚠ 空の枠だけ出すと「原本はこうでした」と誤解させます。出さないほうが正直です。
func TestOrderSourceTableOmittedWhenEmpty(t *testing.T) {
	j := realOrderJudgment()
	j.SourceTable = orderSourceTable{}

	body := buildOrderPageHTML("000001", "pdf001", j)
	if strings.Contains(body, "<details>") {
		t.Errorf("原本が読めないのに枠を出しています:\n%s", body)
	}
}
