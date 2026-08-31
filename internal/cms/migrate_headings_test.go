package cms

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"w-cms/internal/cms/page"

	"w-cms/internal/database"
)

// 見出し形への一括変換のテスト。
//
// 肝は**変換前後で索引の中身が一致する**こと——形式名（data_type）・鍵（field）・
// 値（value）・正規化値が1行も変わらなければ、③計算（部材手配・進捗）は変換に
// 気づかない。正本がファイルであることの利点で、この不変条件さえ立てば
// 「クリーンアップで集計が壊れた」は起きない。

// indexRows はページの索引を「形式|鍵|値」の列で返します（文書順）。
// block_id は比較に含めない——容器の溶解や wrap で**良くなる方向**に変わるため
// （容器の data-id を受発注が引き継ぎ、素の dl/table は包んだ section が引き継ぐ）。
func migIndexRows(t *testing.T, pageID int) []string {
	t.Helper()
	rows, err := database.DB.Query(
		`SELECT data_type, block_no, row_no, field, value, COALESCE(norm_value,'')
		 FROM vocab_index WHERE page_id = ? ORDER BY data_type, block_no, row_no, field`, pageID)
	if err != nil {
		t.Fatalf("クエリエラー: %v", err)
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var dt, field, value, norm string
		var blockNo, rowNo int
		rows.Scan(&dt, &blockNo, &rowNo, &field, &value, &norm)
		out = append(out, fmt.Sprintf("%s|%d|%d|%s|%s|%s", dt, blockNo, rowNo, field, value, norm))
	}
	return out
}

// oldFormBody は移行前の代表的なページ（属性マーカーの受注＋file容器＋見積＋検査記録＋タグ）。
const oldFormBody = `<h1>受注ページ</h1>` +
	`<dl data-type="tags" data-id="t1"><dt>部品番号</dt><dd>X1</dd></dl>` +
	`<section data-type="file" data-src="po.pdf" data-id="f1">` +
	`<p>📎 <a href="/data/master/00/000070/po.pdf">po.pdf</a></p>` +
	`<section data-type="client-order">` +
	`<dl><dt>発注書番号</dt><dd>PO-MIG</dd><dt>発注元</dt><dd>トーア</dd></dl>` +
	`<table data-type="client-order-items"><tbody>` +
	`<tr><th>品番</th><th>品名</th><th>単価</th><th>数量</th><th>状態</th></tr>` +
	`<tr><td>X1</td><td>部品X1</td><td>¥8,000</td><td>10</td><td>加工中</td></tr>` +
	`</tbody></table>` +
	`</section>` +
	`</section>` +
	`<dl data-type="our-estimate" data-id="e1"><dt>品番</dt><dd>X1</dd><dt>見積金額</dt><dd>12000</dd></dl>` +
	`<table data-type="inspection-record" data-id="k1"><tbody>` +
	`<tr><th>品番</th><th>判定</th></tr><tr><td>X1</td><td>合格</td></tr>` +
	`</tbody></table>`

// TestConvertHeadingKeepsIndex は、変換の前後で索引の中身が変わらないことを検証します。
func TestConvertHeadingKeepsIndex(t *testing.T) {
	setupSaveTest(t)

	if err := SyncIndex("000070", oldFormBody); err != nil {
		t.Fatalf("SyncIndex（旧形式）エラー: %v", err)
	}
	before := migIndexRows(t, 70)
	if len(before) == 0 {
		t.Fatal("前提が崩れています: 旧形式が索引に載っていません")
	}

	out, changed, skipped := ConvertHeadingHTML(oldFormBody)
	if !changed {
		t.Fatal("変換が起きていません")
	}
	if skipped != 0 {
		t.Errorf("見送りが出ました: %d", skipped)
	}
	out = Sanitize(out)
	if err := SyncIndex("000070", out); err != nil {
		t.Fatalf("SyncIndex（変換後）エラー: %v", err)
	}
	after := migIndexRows(t, 70)

	if strings.Join(before, "\n") != strings.Join(after, "\n") {
		t.Errorf("変換で索引が変わりました:\n--- 変換前\n%s\n--- 変換後\n%s",
			strings.Join(before, "\n"), strings.Join(after, "\n"))
	}

	// 変換後の本文に業務の機械語が残っていないこと（tags と、素通しの file は別）。
	for _, bad := range []string{`data-type="client-order`, `data-type="our-estimate"`,
		`data-type="inspection-record"`, `data-type="file"`} {
		if strings.Contains(out, bad) {
			t.Errorf("変換後も %s が残っています:\n%s", bad, out)
		}
	}
	// 見出しの宣言が入っていること。
	for _, want := range []string{"<h2>顧客の発注書</h2>", "<h2>弊社の見積もり</h2>", "<h2>検査記録</h2>"} {
		if !strings.Contains(out, want) {
			t.Errorf("変換後に %s がありません:\n%s", want, out)
		}
	}
	// ブロック識別子の引き継ぎ: 容器の f1 → 受注セクション、e1/k1 → 包んだ section。
	for _, want := range []string{`data-id="f1"`, `data-id="e1"`, `data-id="k1"`} {
		if !strings.Contains(out, want) {
			t.Errorf("ブロック識別子 %s が失われています:\n%s", want, out)
		}
	}

	// 冪等性: もう一度変換しても変化しない。
	if _, changed2, _ := ConvertHeadingHTML(out); changed2 {
		t.Error("再実行で変化しました（冪等ではない）")
	}
}

// TestConvertHeadingLeavesSafeCases は、触ってはいけないものを触らないことを検証します。
func TestConvertHeadingLeavesSafeCases(t *testing.T) {
	cases := []struct {
		name string
		body string
	}{
		{"可変タグ", `<dl data-type="tags"><dt>担当</dt><dd>山田</dd></dl>`},
		{"単独のPDF添付（受発注なし）", `<section data-type="file" data-src="a.pdf"><p><a href="/data/master/00/000001/a.pdf">a.pdf</a></p></section>`},
		{"未知の形式", `<table data-type="mystery-form"><tbody><tr><th>x</th></tr><tr><td>1</td></tr></tbody></table>`},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, changed, _ := ConvertHeadingHTML(tc.body); changed {
				t.Errorf("触ってはいけないものが変換されました: %s", tc.body)
			}
		})
	}
}

// TestConvertHeadingSkipsConflictingHeading は、既にある見出しが表示名と違うセクションを
// 安全側に倒して見送る（data-type を残す）ことを検証します。
func TestConvertHeadingSkipsConflictingHeading(t *testing.T) {
	body := `<section data-type="client-order"><h2>作業メモ</h2>` +
		`<dl><dt>発注書番号</dt><dd>PO-1</dd></dl></section>`
	out, changed, skipped := ConvertHeadingHTML(body)
	if skipped != 1 {
		t.Errorf("見送りが数えられていません: %d", skipped)
	}
	if changed && !strings.Contains(out, `data-type="client-order"`) {
		t.Errorf("見出しが食い違うのに data-type が外されました:\n%s", out)
	}
}

// TestMigrateAttachmentsInDir は、添付の移動と本文の書き換えを検証します。
// 正本2ファイルと versions/ files/ は触らず、添付だけが files/ へ移り、
// 本文の配信アドレス（生のUTF-8・パーセント符号化の両方）が追随する。
func TestMigrateAttachmentsInDir(t *testing.T) {
	setupSaveTest(t)
	const id = "000071"
	dir := page.GetPageDir(id)
	os.MkdirAll(filepath.Join(dir, "versions"), 0755)
	os.WriteFile(filepath.Join(dir, id+".html"), []byte("x"), 0644)
	os.WriteFile(filepath.Join(dir, id+".meta.json"), []byte("{}"), 0644)
	os.WriteFile(filepath.Join(dir, "見積書.pdf"), []byte("%PDF-"), 0644)
	os.WriteFile(filepath.Join(dir, "photo.png"), []byte("png"), 0644)
	os.WriteFile(filepath.Join(dir, "versions", "20260101T000000Z.json"), []byte("{}"), 0644)

	moved, err := migrateAttachmentsInDir(dir, id)
	if err != nil {
		t.Fatalf("移行エラー: %v", err)
	}
	if len(moved) != 2 {
		t.Fatalf("移動した数が違います: %v", moved)
	}
	for _, name := range []string{"見積書.pdf", "photo.png"} {
		if _, err := os.Stat(filepath.Join(dir, "files", name)); err != nil {
			t.Errorf("%s が files/ に居ません: %v", name, err)
		}
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("%s が直下に残っています", name)
		}
	}
	// 正本と versions/ は無傷。
	for _, keep := range []string{id + ".html", id + ".meta.json"} {
		if _, err := os.Stat(filepath.Join(dir, keep)); err != nil {
			t.Errorf("正本 %s が動かされました: %v", keep, err)
		}
	}
	if _, err := os.Stat(filepath.Join(dir, "versions", "20260101T000000Z.json")); err != nil {
		t.Errorf("versions/ が動かされました: %v", err)
	}

	// 本文の書き換え（生UTF-8とパーセント符号化の両方）。
	body := `<p><a href="/data/master/00/000071/見積書.pdf">見積書</a></p>` +
		`<p><img src="/data/master/00/000071/photo.png"></p>` +
		`<p><a href="/data/master/00/000071/%E8%A6%8B%E7%A9%8D%E6%9B%B8.pdf">符号化リンク</a></p>`
	out := rewriteAttachmentURLs(body, id, moved)
	for _, want := range []string{
		`/data/master/00/000071/files/見積書.pdf`,
		`/data/master/00/000071/files/photo.png`,
		`/data/master/00/000071/files/%E8%A6%8B%E7%A9%8D%E6%9B%B8.pdf`,
	} {
		if !strings.Contains(out, want) {
			t.Errorf("書き換え後に %s がありません:\n%s", want, out)
		}
	}

	// 冪等: もう一度動かしても何も起きない。
	again, err := migrateAttachmentsInDir(dir, id)
	if err != nil || len(again) != 0 {
		t.Errorf("再実行で変化しました: moved=%v err=%v", again, err)
	}
}
