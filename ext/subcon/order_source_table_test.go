package subcon

import (
	"encoding/json"
	"strings"
	"testing"
	"unicode/utf8"

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
	if !strings.Contains(body, "<caption>受注明細</caption>") {
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
	// ⚠ **原本のPDFの枠とは別物です**——PDFの枠は常に出ます（ファイルは在るので）。
	// ここで見るのは「読んだまま」の表だけです。
	if strings.Contains(body, sourceTableCaption) {
		t.Errorf("原本が読めないのに枠を出しています:\n%s", body)
	}
}

// TestParseSourceTableAcceptsBothRowForms は、**行が配列でも object でも受ける**ことを
// 固定します。
//
// ⚠ 頼んでいるのは `[["1","品名",…]]` ですが、**Gemini は見出しを鍵にした object で
// 返すことがよくあります**（`[{"No.":"1","品名":"…"}]`）。実データの2通目で
// 「解析に失敗しました」が出た原因でした。
func TestParseSourceTableAcceptsBothRowForms(t *testing.T) {
	for _, c := range []struct{ name, raw string }{
		{"行が配列（頼んだ形）",
			`{"headers":["No.","品名","数量"],"rows":[["1","ブラケット","100"]]}`},
		{"行が object（見出しが鍵）",
			`{"headers":["No.","品名","数量"],"rows":[{"No.":"1","品名":"ブラケット","数量":"100"}]}`},
		{"数が数のまま返る",
			`{"headers":["No.","品名","数量"],"rows":[{"No.":1,"品名":"ブラケット","数量":100}]}`},
	} {
		t.Run(c.name, func(t *testing.T) {
			got := parseSourceTable([]byte(c.raw))
			if len(got.Rows) != 1 {
				t.Fatalf("行が取れていません: %+v", got)
			}
			want := []string{"1", "ブラケット", "100"}
			for i, w := range want {
				if got.Rows[0][i] != w {
					// ⚠ 数が `100.000000` になっていないか（原本の見た目を保つ）。
					t.Errorf("%d列目が %q です（%q を期待）", i, got.Rows[0][i], w)
				}
			}
		})
	}
}

// TestParseSourceTableSurvivesGarbage は、**読めない原本で空を返す（落ちない）**ことを
// 固定します。
func TestParseSourceTableSurvivesGarbage(t *testing.T) {
	for _, raw := range []string{
		``, `null`, `"ただの文字列"`, `[]`, `{"headers":[],"rows":[]}`,
		`{"headers":["A"],"rows":[42]}`,
	} {
		if got := parseSourceTable([]byte(raw)); len(got.Rows) != 0 {
			t.Errorf("%q から行が取れています: %+v", raw, got)
		}
	}
}

// TestJudgmentSurvivesBadSourceTable は、⚠ **原本が読めなくても本題が残る**ことを
// 固定します。
//
// **これがこの直しの本体です。** 構造に直接当てていたころは、**原本の形が合わない
// だけで発注書番号も明細も丸ごと失われ**、画面には「解析に失敗しました」としか
// 出ませんでした。**おまけのために本題を落とさない。**
func TestJudgmentSurvivesBadSourceTable(t *testing.T) {
	resp := `{"doc_type":"order","is_client_order":true,` +
		`"order_no":"250715-304","customer":"南北スポーツ機械","order_date":"2026-09-16",` +
		`"due_date":"最短納期",` +
		`"source_table":"表は読めませんでした",` + // ⚠ 構造に合わない形
		`"items":[{"item_no":"K120-1","item_name":"ブラケット","quantity":"100","unit":"個","price":"390"}]}`

	var j orderJudgment
	if err := json.Unmarshal([]byte(resp), &j); err != nil {
		t.Fatalf("⚠ 原本の形が合わないだけで応答全体が読めなくなっています: %v", err)
	}
	j.SourceTable = parseSourceTable(j.SourceTableRaw)

	if j.OrderNo != "250715-304" || j.DueDate != "最短納期" || len(j.Items) != 1 {
		t.Errorf("本題が失われています: %+v", j)
	}
	if len(j.SourceTable.Rows) != 0 {
		t.Errorf("読めない原本から行が取れています: %+v", j.SourceTable)
	}
	// 本文も組める（原本の枠は出ない）。
	body := buildOrderPageHTML("000001", "pdf001", &j)
	if !strings.Contains(body, "<dd>250715-304</dd>") {
		t.Errorf("発注書番号が本文に出ていません:\n%s", body)
	}
	if strings.Contains(body, sourceTableCaption) {
		t.Errorf("読めない原本の枠を出しています:\n%s", body)
	}
}

// TestAnalyzeErrorShowsResponseHead は、**返ってきたものの頭がエラー文に出る**ことを
// 固定します。
//
// ⚠ これが無いと「解析に失敗しました」としか出ず、**原因を当てられません**
// （実データで実際に当てられませんでした・2026-09-20）。直すのはたいてい
// プロンプトなので、**何が返ったかが唯一の手掛かり**です。
//
// ⚠ **文字の途中で切らないこと**もここで見ます——和文は1文字3バイトなので、
// バイト数で切ると画面に化けた文字が出ます。
func TestAnalyzeErrorShowsResponseHead(t *testing.T) {
	long := "これはJSONではありません。" + strings.Repeat("あ", 500)
	_, err := parseOrderJudgment(long)
	if err == nil {
		t.Fatal("JSONでない応答を通しています")
	}
	if !strings.Contains(err.Error(), "これはJSONではありません") {
		t.Errorf("返ってきたものが出ていません: %v", err)
	}
	if !utf8.ValidString(err.Error()) {
		t.Errorf("⚠ 文字の途中で切れています（画面に化けた文字が出ます）: %q", err.Error())
	}
}

// TestOrderPagePutsPDFAboveSourceTable は、**原本のPDFが写しの上に、畳んで**
// 置かれることを固定します（2026-09-21 ユーザー:「顧客の発注書（読んだまま）の上に
// PDFを表示できるようにします（通常は折りたたむ）」）。
//
// ⚠ **順番が仕様です**——原本（PDF）→ 読んだまま（機械の読み取り）→ 弊社の明細、と
// **確かさの順**に並びます。入れ替わると、人は読み取りの結果を原本だと思って見ます。
func TestOrderPagePutsPDFAboveSourceTable(t *testing.T) {
	body := buildOrderPageHTML("000001", "pdf001", realOrderJudgment())

	// **ファイル表示のマーカーが、その添付を指している**こと。
	marker := `<section data-type="` + cms.FileViewType + `" ` +
		cms.FileRefAttr + `="000001-pdf001"></section>`
	if !strings.Contains(body, marker) {
		t.Errorf("PDFを開くマーカーがありません:\n%s", body)
	}
	pdfAt := strings.Index(body, sourcePDFCaption)
	srcAt := strings.Index(body, sourceTableCaption)
	if pdfAt < 0 {
		t.Fatalf("原本のPDFの枠がありません:\n%s", body)
	}
	if srcAt < 0 {
		t.Fatalf("原本の写しがありません:\n%s", body)
	}
	if pdfAt > srcAt {
		t.Errorf("PDFが写しより下にあります（PDF %d / 写し %d）", pdfAt, srcAt)
	}
	// ⚠ **畳んで始まる**こと（`open` を付けない）。毎日見るのは弊社の明細で、
	// PDFは食い違いを疑ったときに開くものです。
	if strings.Contains(body, "<details open") {
		t.Errorf("PDFが開いたまま始まっています:\n%s", body)
	}
}

// TestOrderPageShowsPDFEvenWithoutSourceTable は、⚠ **写しが読めなくてもPDFは出る**
// ことを固定します。
//
// 写しは機械の読み取りなので失敗しえますが、**PDFは在ります**。読めなかったときこそ
// 人は原本を開きたいので、ここで一緒に消えると**いちばん要るときに無い**ことになります。
func TestOrderPageShowsPDFEvenWithoutSourceTable(t *testing.T) {
	j := realOrderJudgment()
	j.SourceTable = orderSourceTable{} // 読めなかった

	body := buildOrderPageHTML("000001", "pdf001", j)
	if !strings.Contains(body, sourcePDFCaption) {
		t.Errorf("写しが読めないとPDFまで消えています:\n%s", body)
	}
}
