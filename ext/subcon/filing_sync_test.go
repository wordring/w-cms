package subcon

// 整理が図面ブロックの値を索引へ揃える、その振る舞いの試験（2026-09-11）。
//
// ユーザー:「整理で治した客先名は図面ブロックの索引にも反映させます。検索するときに
// 困るからです」——実データで、階層は正しいのに索引が誤読を持ったままの加工製品ページが
// 生まれていました。`客先` で探しても出てこないので、索引の意味がありません。

import (
	"regexp"
	"strconv"
	"strings"
	"testing"

	"w-cms/internal/auth"
	"w-cms/internal/cms"
	"w-cms/internal/database"
)

// TestFileDrawingsSyncsIndexedFields は、整理で人が直した値が**図面ブロックと索引にも
// 反映される**ことを固定します。
func TestFileDrawingsSyncsIndexedFields(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	partID := makeDrawingPage(t, inbox, "K120-1", "取付ベース",
		"標準２輪", "株式会社南北北スポーツ機械") // ← 解析の読み違い

	results := postFiling(t, &auth.User{Username: "alice"}, []filingRequest{{
		PageID: partID, Customer: "南北スポーツ機械", Stage: "現行",
		MachineName: "標準2輪", DrawingName: "取付ベース",
	}})
	if len(results) != 1 || results[0].Outcome != "moved" {
		t.Fatalf("移動になっていません: %+v", results)
	}

	body, err := cms.ReadPageBody(partID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(body, "南北北スポーツ機械") {
		t.Errorf("本文に誤読の客先が残っています: %s", body)
	}
	if !strings.Contains(body, "<dt>客先</dt><dd>南北スポーツ機械</dd>") {
		t.Errorf("本文の客先が直っていません: %s", body)
	}
	if !strings.Contains(body, "<dt>装置名称</dt><dd>標準2輪</dd>") {
		t.Errorf("本文の装置名称が直っていません: %s", body)
	}

	// **索引が肝心**——③計算も将来の検索もここを引きます（D-1）。
	assertIndexed(t, partID, "客先", "南北スポーツ機械")
	assertIndexed(t, partID, "装置名称", "標準2輪")
}

// TestFileDrawingsFillsEmptyClientName は、**解析が読めなかった項目を整理が埋める**
// ことを固定します。
//
// 実データの7枚のうち3枚で `客先` が空でした。**空欄は行が無いのではなく
// `<dd><br/></dd>`**（サニタイズが空の `dd` に入れる）で、ここを見落とすと素通りします
// ——行は在るのに値が無く、索引にも入らないので永久に引けません。
func TestFileDrawingsFillsEmptyClientName(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	partID := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "")

	// 前提の確認——空欄が実データと同じ形（`<br/>`）になっていること。
	body, err := cms.ReadPageBody(partID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "<dt>客先</dt><dd><br/></dd>") {
		t.Fatalf("空欄の形が想定と違います（この試験の前提が崩れています）: %s", body)
	}

	results := postFiling(t, &auth.User{Username: "alice"}, []filingRequest{{
		PageID: partID, Customer: "南北スポーツ機械", Stage: "現行",
		MachineName: "標準2輪", DrawingName: "取付ベース",
	}})
	if len(results) != 1 || results[0].Outcome != "moved" {
		t.Fatalf("移動になっていません: %+v", results)
	}
	body, err = cms.ReadPageBody(partID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "<dt>客先</dt><dd>南北スポーツ機械</dd>") {
		t.Errorf("空欄だった客先が埋まっていません: %s", body)
	}
	assertIndexed(t, partID, "客先", "南北スポーツ機械")
}

// TestFileDrawingsAddsMissingClientName は、**行そのものが無いとき足す**ことを固定します。
//
// 実データでは見ていませんが（空欄は上の `<br/>` の形で現れる）、人が行ごと消した
// ページが来ても `客先` で引けるようにしておきます。
func TestFileDrawingsAddsMissingClientName(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	partID := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "")

	if err := cms.RewriteBody(partID, "alice", func(cur string) string {
		return regexp.MustCompile(`<dt>客先</dt><dd>.*?</dd>`).ReplaceAllString(cur, "")
	}); err != nil {
		t.Fatal(err)
	}
	if body, _ := cms.ReadPageBody(partID); strings.Contains(body, "客先") {
		t.Fatalf("前提が作れていません（客先の行が残っています）: %s", body)
	}

	results := postFiling(t, &auth.User{Username: "alice"}, []filingRequest{{
		PageID: partID, Customer: "南北スポーツ機械", Stage: "現行",
		MachineName: "標準2輪", DrawingName: "取付ベース",
	}})
	if len(results) != 1 || results[0].Outcome != "moved" {
		t.Fatalf("移動になっていません: %+v", results)
	}
	body, err := cms.ReadPageBody(partID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "<dt>客先</dt><dd>南北スポーツ機械</dd>") {
		t.Errorf("客先の行が足されていません: %s", body)
	}
	assertIndexed(t, partID, "客先", "南北スポーツ機械")
}

// TestFileDrawingsKeepsHandEditedValues は、**人が手を入れた値を機械が踏み潰さない**
// ことを固定します。
//
// 値にリンクや強調が書かれていると単純な差し替えができません。そのときは黙って
// そのままにします——階層は直るので、困るのはその1件を `客先` で探すときだけです。
func TestFileDrawingsKeepsHandEditedValues(t *testing.T) {
	const inbox = "000012"
	setupFilingTest(t, inbox)
	partID := makeDrawingPage(t, inbox, "K120-1", "取付ベース", "標準2輪", "仮")

	if err := cms.RewriteBody(partID, "alice", func(cur string) string {
		return strings.Replace(cur, "<dt>客先</dt><dd>仮</dd>",
			"<dt>客先</dt><dd><strong>要確認</strong></dd>", 1)
	}); err != nil {
		t.Fatal(err)
	}

	results := postFiling(t, &auth.User{Username: "alice"}, []filingRequest{{
		PageID: partID, Customer: "南北スポーツ機械", Stage: "現行",
		MachineName: "標準2輪", DrawingName: "取付ベース",
	}})
	if len(results) != 1 || results[0].Outcome != "moved" {
		t.Fatalf("移動になっていません: %+v", results)
	}
	body, err := cms.ReadPageBody(partID)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(body, "<strong>要確認</strong>") {
		t.Errorf("人が書いた印が消えています: %s", body)
	}
}

func assertIndexed(t *testing.T, pageID, field, value string) {
	t.Helper()
	idInt, err := strconv.Atoi(pageID)
	if err != nil {
		t.Fatal(err)
	}
	// ⚠ **見るのは `page_tags`** です（2026-09-18 に図面ブロックのヘッダをタグへ移した）。
	// 横断検索の口（`PagesByTag`）が読む表はこちらです。
	var n int
	if err := database.DB.QueryRow(
		`SELECT COUNT(*) FROM page_tags WHERE page_id = ? AND name = ? AND value = ?`,
		idInt, field, value).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Errorf("タグの索引に %s=%s が入っていません", field, value)
	}
}
