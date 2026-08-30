package cms

import (
	"testing"

	"w-cms/internal/database"
)

// TestSectionHeaderIsIndexed は、業務文書ブロックのヘッダが汎用索引に載ることを
// 固定します。
//
// **ヘッダの <dl> は data-type を持ちません**（鍵は dt の表示文字・語彙モデル §8.2）。
// 配送係は引き金（data-type）のある要素しか届けないので、素の dl は誰の手にも
// 渡りません。硬いドメイン表があったころは各プラグインが section を受け取って
// 自分で子の dl を読んでいたため気づきませんでしたが、テーブルを廃して索引へ
// 一本化する（D-1）と、**発注元・発注日・発注先がどこにも残らなくなります**。
//
// 明細表だけが索引に載って、ヘッダが黙って消える——という壊れ方をするので、
// 実HTMLを SyncIndex に通して両方が載ることを見ます。
func TestSectionHeaderIsIndexed(t *testing.T) {
	seedOrderPages(t, "000071")
	if err := SyncIndex("000071", clientOrderHTML("PO-7", "得意先X", "PART-X")); err != nil {
		t.Fatalf("SyncIndexエラー: %v", err)
	}

	get := func(dataType, field string) string {
		t.Helper()
		var v string
		err := database.DB.QueryRow(
			`SELECT value FROM vocab_index WHERE page_id = 71 AND data_type = ? AND field = ?`,
			dataType, field).Scan(&v)
		if err != nil {
			t.Errorf("索引に data_type=%q field=%q がありません: %v", dataType, field, err)
			return ""
		}
		return v
	}

	// ヘッダ（section の data-type の下に載る）
	if v := get("client-order", "発注書番号"); v != "PO-7" {
		t.Errorf("発注書番号が索引と違います: %q (期待 PO-7)", v)
	}
	if v := get("client-order", "発注元"); v != "得意先X" {
		t.Errorf("発注元が索引と違います: %q (期待 得意先X)", v)
	}
	if v := get("client-order", "発注日"); v != "2026-08-20" {
		t.Errorf("発注日が索引と違います: %q", v)
	}
	// 明細（従来どおり）
	if v := get("client-order-items", "品番"); v != "PART-X" {
		t.Errorf("品番が索引と違います: %q (期待 PART-X)", v)
	}

	// 発注日は date 型と解決され、正規化値が入る（ヘッダにも型知識が効くこと）。
	var norm string
	if err := database.DB.QueryRow(
		`SELECT COALESCE(norm_value,'') FROM vocab_index
		 WHERE page_id = 71 AND data_type = 'client-order' AND field = '発注日'`).Scan(&norm); err != nil {
		t.Fatalf("正規化値のクエリエラー: %v", err)
	}
	if norm != "2026-08-20" {
		t.Errorf("発注日が日付として正規化されていません: %q", norm)
	}
}
